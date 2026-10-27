package oomkiller

import (
	"context"
	"runtime"
	runtimeDebug "runtime/debug"
	"runtime/metrics"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common/byteformats"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/memory"
)

const (
	defaultMinInterval               = 100 * time.Millisecond
	defaultMaxInterval               = 10 * time.Second
	defaultReleaseInterval           = time.Second
	defaultIdleCooldown              = 30 * time.Second
	defaultIdleBenefitFloor          = 512 * 1024
	defaultReleaseShrinkFloor        = 16
	defaultReleaseShrinkDivisor      = 10
	defaultMaxRateLookahead          = time.Second
	defaultSafetyMargin              = 5 * 1024 * 1024
	defaultAvailableTriggerMarginMin = 32 * 1024 * 1024
	defaultAvailableTriggerMarginMax = 128 * 1024 * 1024
)

type pressureState uint8

const (
	pressureStateNormal pressureState = iota
	pressureStateArmed
	pressureStateTriggered
)

// reclaimKind identifies the one destructive action an adaptiveTimer may be running.
//
// # Why the actions need a single authority
//
// Three sources can ask for memory back: a timer poll that observed a real opportunity (idle), a
// timer poll that crossed the crisis threshold (crisis), and the Darwin dispatch source (pressure).
// They all reach the same pools. Before this existed, only the idle path was serialised by
// releaseAccess; a crisis poll could run ReleaseMemory while an idle TrimMemory was still walking the
// same outbound, endpoint and DNS transport lists, and the Linux/Darwin callback could start a third
// pass. The result was not a slower release, it was a release against pools being reset underneath
// it, and two forced GCs for one event.
//
// The kinds are ordered by urgency. A pending action can be replaced by a more urgent one, and the
// in-flight action is never interrupted (there is no safe way to cancel a CloseIdleConnections
// half-way through a pool), only outlasted.
type reclaimKind uint8

const (
	reclaimNone reclaimKind = iota
	// reclaimIdle is the gentle pass: expired caches, then idle pools, then - only when the previous
	// two did not reach the benefit floor - a forced GC. It must never reset the network.
	reclaimIdle
	// reclaimPressure is the Darwin critical notification: flush the cache, publish Critical and
	// force a GC. It deliberately does not reset the network; a system-wide signal is not proof that
	// this process's paths are broken.
	reclaimPressure
	// reclaimCrisis is the threshold/rate pass: ReleaseMemory (network reset) followed by the same
	// cache flush and forced GC as a pressure event.
	reclaimCrisis
)

// reclaimRequest is one queued destructive action. It carries the sample the decision was taken
// from, so the telemetry a crisis action writes describes the observation that justified it rather
// than a later, unrelated one.
type reclaimRequest struct {
	kind          reclaimKind
	sample        memorySample
	connections   int
	reason        string
	rateTriggered bool
	reportOnly    bool
}

type memorySample struct {
	usage          uint64
	available      uint64
	availableKnown bool
}

type pressureThresholds struct {
	trigger uint64
	armed   uint64
	resume  uint64
}

type timerConfig struct {
	memoryLimit     uint64
	safetyMargin    uint64
	hasSafetyMargin bool
	minInterval     time.Duration
	maxInterval     time.Duration
	policyMode      policyMode
	killerDisabled  bool
}

func buildTimerConfig(options option.OOMKillerServiceOptions, memoryLimit uint64, policyMode policyMode, killerDisabled bool) (timerConfig, error) {
	// Inside an iOS NetworkExtension the policy is a PLATFORM safety contract, not a
	// tunable. A profile may not widen the sampling gap or shrink the footprint margin,
	// because the Go runtime soft limit is derived from the same margin: letting a profile
	// set one and not the other policed the process against two different notions of
	// "safe", and the failure mode is the process being killed rather than a slow request.
	//
	// Returning the canonical config here, before any option is read, is what makes the
	// two layers consistent by construction instead of by convention.
	if policyMode == policyModeNetworkExtension {
		policy := canonicalNetworkExtensionPolicy
		return timerConfig{
			memoryLimit:     policy.memoryLimit(),
			safetyMargin:    policy.safetyMargin,
			hasSafetyMargin: true,
			minInterval:     policy.minInterval,
			maxInterval:     policy.maxInterval,
			policyMode:      policyMode,
			killerDisabled:  killerDisabled,
		}, nil
	}

	minInterval := defaultMinInterval
	if options.MinInterval != 0 {
		minInterval = time.Duration(options.MinInterval.Build())
		if minInterval <= 0 {
			return timerConfig{}, E.New("min_interval must be greater than 0")
		}
	}

	maxInterval := defaultMaxInterval
	if options.MaxInterval != 0 {
		maxInterval = time.Duration(options.MaxInterval.Build())
		if maxInterval <= 0 {
			return timerConfig{}, E.New("max_interval must be greater than 0")
		}
	}
	if maxInterval < minInterval {
		return timerConfig{}, E.New("max_interval must be greater than or equal to min_interval")
	}

	var (
		safetyMargin    uint64
		hasSafetyMargin bool
	)
	if options.SafetyMargin != nil && options.SafetyMargin.Value() > 0 {
		safetyMargin = options.SafetyMargin.Value()
		hasSafetyMargin = true
	} else if memoryLimit > 0 {
		safetyMargin = defaultSafetyMargin
		hasSafetyMargin = true
	}

	return timerConfig{
		memoryLimit:     memoryLimit,
		safetyMargin:    safetyMargin,
		hasSafetyMargin: hasSafetyMargin,
		minInterval:     minInterval,
		maxInterval:     maxInterval,
		policyMode:      policyMode,
		killerDisabled:  killerDisabled,
	}, nil
}

type timerState struct {
	state                   pressureState
	currentInterval         time.Duration
	forceMinInterval        bool
	pendingPressureBaseline bool
	pressureBaseline        memorySample
	pressureBaselineTime    time.Time
}

type adaptiveTimer struct {
	timerConfig
	logger          log.ContextLogger
	network         adapter.NetworkManager
	connections     adapter.ConnectionManager
	cacheFile       adapter.CacheFile
	recorder        *Recorder
	pressure        *atomic.Uint32
	limitThresholds pressureThresholds

	access            sync.Mutex
	timer             *time.Timer
	releaseAccess     sync.Mutex
	lastUsage         uint64
	idleCooldownUntil time.Time
	trimMemory        func(context.Context)
	// trimCaches is the L1 executor: it drops expired cache entries and nothing that carries
	// traffic. Nil means the network manager has no cache-trimming capability, in which case the
	// idle pass starts at L2. It is separate from trimMemory so the pass can measure what the
	// cheap layer actually returned before deciding to close idle pools at all.
	trimCaches   func(context.Context)
	freeOSMemory func()
	// releaseNetwork is the crisis executor (L4). Nil means t.network.ReleaseMemory. A test
	// supplies it to observe the destructive reset without building a full network manager.
	releaseNetwork func(context.Context)
	// readUsage reads the process footprint for the layer-benefit measurements. Nil means
	// memory.Total(). It is separate from sampleFunc because a test needs to drive both the
	// observation that triggers a pass and the footprint each layer reports afterwards.
	readUsage func() uint64
	// runtimeReclaimable reports bytes the Go runtime could return without another cycle. Nil
	// means the /memory/classes/heap/free metric. It gates L3 below the armed line: a forced GC
	// is not free, and it must have a reason other than "the previous layers were quiet".
	runtimeReclaimable func() uint64

	// pressureCallbacks tracks every accepted poll, pressure callback and queued/in-flight reclaim
	// action.
	//
	// # Why a WaitGroup is needed at all
	//
	// Checking `t.timer != nil` under the lock is necessary but NOT sufficient. Work that has
	// passed the check releases the lock before doing anything slow, precisely so a long
	// FreeOSMemory does not stall Close. Without something to join on, Close can return while that
	// work is still going to publish a pressure state or touch the network:
	//
	//	poll:  active check passes, decision taken, unlock
	//	Close: timer = nil, pressure = None, Close RETURNS
	//	poll:  publishes Warning / trims / resets the network
	//
	// The active check bounds WHEN the work starts; the barrier bounds when it has FINISHED.
	//
	// # Why this ordering cannot race
	//
	// Every Add happens while t.access is held and only after the active check has passed, and
	// stop() sets timer = nil and reclaimStopped while holding the same lock before it Waits. So:
	//
	//   - work that entered before stop() took the lock has already Added, and Wait observes it;
	//   - work that enters after sees timer == nil (or stopped) and never Adds, so it cannot be
	//     missed.
	//
	// There is no window in which Add can happen after Wait has decided the count is zero, which is
	// the WaitGroup contract stop() relies on.
	pressureCallbacks sync.WaitGroup

	// The reclaim executor. It exists so the three sources - idle opportunity, crisis threshold,
	// Darwin pressure - share one destructive authority instead of racing each other through
	// releaseAccess. Polls only observe and queue; the worker performs. Crisis outranks pressure,
	// pressure outranks idle, and a more urgent pending request replaces a less urgent one so a
	// burst of notifications cannot build a queue of stale resets.
	reclaimWake      chan struct{}
	reclaimStop      chan struct{}
	reclaimExited    chan struct{}
	reclaimStarted   bool
	reclaimStopped   bool
	reclaimStopOnce  sync.Once
	pendingReclaim   *reclaimRequest
	inFlightReclaim  reclaimKind
	idleInFlight     bool
	pressureInFlight bool
	crisisInFlight   bool
	// lastAppliedObservation is the newest observation sequence whose state has been applied.
	// Polls read their sample before taking the lock, so two of them can finish reading out of
	// order on a Go scheduler that delays one goroutine. Applying the older one last would move the
	// state machine backwards - and could re-arm crisis - from a sample that predates the one
	// already applied. See observationSeq.
	lastAppliedObservation uint64
	observationSeq         atomic.Uint64

	// cleanupHook, when set, runs at the start of the destructive cleanup. Tests use it to
	// hold a callback inside the barrier deterministically instead of racing goroutines and
	// hoping to hit the interleaving.
	cleanupHook func()
	// afterDecisionHook, when set, runs after poll has published its decision and released the
	// lock, before telemetry and before any queued reclaim. It exists so a test can hold a poll in
	// the post-decision window and prove that stop() now joins on it and that the pressure state
	// was published while the poll still owned the lifecycle lock. Nil in production, where it
	// costs one predictable branch per sample.
	afterDecisionHook func()
	// sampleFunc overrides the process sample in tests. Nil means readMemorySample.
	sampleFunc func() memorySample

	lastGoroutines  int
	lastConnections int
	lastGCCycles    uint64
	lastRelease     time.Time
	timerState
}

func newAdaptiveTimer(logger log.ContextLogger, network adapter.NetworkManager, connections adapter.ConnectionManager, cacheFile adapter.CacheFile, recorder *Recorder, pressure *atomic.Uint32, config timerConfig) *adaptiveTimer {
	t := &adaptiveTimer{
		timerConfig: config,
		logger:      logger,
		network:     network,
		connections: connections,
		cacheFile:   cacheFile,
		recorder:    recorder,
		pressure:    pressure,
	}
	// The cache layer is an optional capability: a manager that cannot answer it simply has no L1,
	// and the idle pass starts at the pool layer. The assertion is done once here rather than on
	// every release, which also means a fixture manager without the capability costs nothing.
	if network != nil {
		if cacheReleaser, isReleaser := any(network).(adapter.CacheMemoryReleaser); isReleaser {
			t.trimCaches = func(ctx context.Context) {
				cacheReleaser.TrimCaches(ctx)
			}
		}
	}
	if config.policyMode == policyModeMemoryLimit || config.policyMode == policyModeNetworkExtension {
		t.limitThresholds = computeLimitThresholds(config.memoryLimit, config.safetyMargin)
	}
	return t
}

func (t *adaptiveTimer) start(carriedState *timerState) {
	t.access.Lock()
	defer t.access.Unlock()
	if t.timer != nil {
		return
	}
	if carriedState != nil {
		t.timerState = *carriedState
		t.timer = time.AfterFunc(t.minInterval, t.poll)
		return
	}
	t.startLocked()
}

func (t *adaptiveTimer) startLocked() {
	if t.timer != nil {
		return
	}
	t.state = pressureStateNormal
	t.forceMinInterval = false
	t.timer = time.AfterFunc(t.minInterval, t.poll)
}

// stoppedStateLocked is the state handed to the recorder (and possibly carried into the next
// instance by a reload).
//
// # Why transient intent does not cross a reload
//
// `forceMinInterval` and `pendingPressureBaseline` are not observations: they are "the next poll
// should do X" flags set by a pressure callback. A reload replaces the timer, and the callback that
// set the flag belonged to the old dispatch delivery - the queued action was dropped by stop() for
// exactly the same reason. Carrying the flag would make the new instance's first poll measure growth
// from a sample the old instance took (or treat an already-dropped notification as pending), which is
// the cross-instance state pollution this helper removes. The observable state and the sampling
// interval are the parts a reload legitimately carries.
func (t *adaptiveTimer) stoppedStateLocked() timerState {
	state := t.timerState
	state.forceMinInterval = false
	if state.pendingPressureBaseline {
		state.pendingPressureBaseline = false
		state.pressureBaseline = memorySample{}
		state.pressureBaselineTime = time.Time{}
	}
	// Triggered means "the reset for this episode has been handled". A queued crisis is dropped at
	// stop (above), so a reload must not inherit that claim for work that never ran: the new
	// instance would stay Triggered while the footprint is still above the trigger, and no edge
	// would ever fire again. Armed forces the next observation to re-decide; if the footprint is
	// still critical it escalates in one poll, and if it is not, it leaves the crisis band normally.
	if state.state == pressureStateTriggered {
		state.state = pressureStateArmed
	}
	return state
}

// stop halts the timer and WAITS for every accepted observation and destructive action to finish.
//
// # Why the wait is the point
//
// Setting timer = nil under the lock stops new observations from starting, but an observation that
// already passed its check is still publishing pressure, writing telemetry, or waiting in the
// reclaim queue - and a reclaim that already started is touching the network. If stop returned
// immediately, Service.Close could return while a torn-down instance still writes Warning/Critical
// or resets pools the next instance is about to use. Callers treat the return of stop as "nothing
// further will touch this service", and the barrier is what makes that true rather than likely.
//
// # What is dropped rather than waited for
//
// A request still QUEUED has not touched anything yet. Closing is exactly the moment not to start a
// network reset for a service that no longer exists, so pending requests are dropped (and their
// barrier tokens released). Only work that already started is waited for. This is the difference
// between "all in-flight destructive work completes" and "everything ever planned completes", and
// only the first is required for a safe Close.
//
// # Why the lock is released before Wait
//
// The work must be able to finish, and it does not need t.access to do so. Waiting while holding
// the lock would deadlock the moment a poll or the executor tried to acquire it, and would turn a
// slow FreeOSMemory into a stall for every other reader of the timer state.
func (t *adaptiveTimer) stop() timerState {
	t.access.Lock()
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
	t.reclaimStopped = true
	// Anything still queued is discarded before the wait: the service is closing, so starting a
	// trim or a reset now would be work for a lifecycle that has ended. In-flight work is joined
	// below.
	t.dropPendingReclaimLocked()
	executorStarted := t.reclaimStarted
	stopChannel := t.reclaimStop
	exitedChannel := t.reclaimExited
	state := t.stoppedStateLocked()
	t.access.Unlock()

	if executorStarted {
		t.reclaimStopOnce.Do(func() {
			close(stopChannel)
		})
	}

	// Everything that legitimately started before the lock was released finishes here. Nothing can
	// join the group after this point: a later poll sees timer == nil, a later callback sees the
	// stopped flag, and neither Adds.
	t.pressureCallbacks.Wait()

	if executorStarted {
		// The barrier proves the work is done; this proves the worker itself has left, so a reload
		// cannot accumulate one sleeping goroutine per instance.
		<-exitedChannel
	}

	return state
}

// poll observes one sample and decides what, if anything, to do about it.
//
// # What happens under the lock and what does not
//
// The state transition, the timer re-arm, the pressure publication and the queue decision are one
// critical section, because a stop() that ran between them could tear the instance's final
// published state. Telemetry and the destructive work run outside it: a recorder write and a
// ForceGC must not make every observer wait.
//
// # The barrier covers the whole poll, not just the destructive branch
//
// The previous version only joined pressureCallbacks when the poll was Triggered. A non-Triggered
// poll published pressure and wrote telemetry after unlocking with nothing joining on it, so
// Close could return and then an old poll rewrote None to Warning. Every accepted poll now Adds,
// and Dones after its telemetry.
func (t *adaptiveTimer) poll() {
	// The sequence is taken BEFORE the sample for the same reason a generation is captured before
	// an operation: two polls can read in either order, and the state machine must never apply an
	// older observation over a newer one. If this poll is overtaken, its sample is dropped rather
	// than moving the state backwards.
	observation := t.observationSeq.Add(1)
	sample := t.readSample()
	goroutines := runtime.NumGoroutine()
	var connections int
	if t.connections != nil {
		connections = t.connections.Count()
	}

	t.access.Lock()
	if t.timer == nil || t.reclaimStopped {
		t.access.Unlock()
		return
	}
	if observation <= t.lastAppliedObservation {
		t.access.Unlock()
		return
	}
	t.lastAppliedObservation = observation
	// From here on this poll is visible to stop(), including its telemetry below.
	t.pressureCallbacks.Add(1)

	if t.pendingPressureBaseline {
		t.pressureBaseline = sample
		t.pressureBaselineTime = time.Now()
		t.pendingPressureBaseline = false
	}
	previousState := t.state
	t.state = t.nextState(sample)
	if t.state == pressureStateNormal {
		t.forceMinInterval = false
		if !t.pressureBaselineTime.IsZero() && time.Since(t.pressureBaselineTime) > t.maxInterval {
			t.pressureBaselineTime = time.Time{}
		}
	}
	interval := t.intervalForState()
	t.timer.Reset(interval)
	triggered := previousState != pressureStateTriggered && t.state == pressureStateTriggered
	rateTriggered := false
	// The rate prediction may escalate an armed (or normal) sample before the footprint itself
	// reaches the trigger. It must not fire AGAIN while the state machine is already Triggered:
	// that would turn one prediction into a reset on every poll for as long as growth continued,
	// which is a release storm no threshold ever asked for.
	if !triggered && previousState != pressureStateTriggered && !t.pressureBaselineTime.IsZero() &&
		t.memoryLimit > 0 && sample.usage > t.pressureBaseline.usage && sample.usage < t.memoryLimit {
		elapsed := time.Since(t.pressureBaselineTime)
		if elapsed >= t.minInterval/2 {
			growth := sample.usage - t.pressureBaseline.usage
			ratePerSecond := float64(growth) / elapsed.Seconds()
			headroom := t.memoryLimit - sample.usage
			secondsUntilLimit := float64(headroom) / ratePerSecond
			lookahead := min(2*interval, defaultMaxRateLookahead)
			if secondsUntilLimit < lookahead.Seconds() {
				triggered = true
				rateTriggered = true
				t.state = pressureStateTriggered
			}
		}
	}
	state := t.state
	// Publish while the lifecycle lock is still held. This is the write that used to land after
	// stop() had already published None; doing it here makes "the last published state of a stopped
	// instance" a statement the lock guarantees rather than one the scheduler happens to preserve.
	t.pressure.Store(uint32(state.memoryPressure()))
	if triggered {
		t.submitCrisisLocked(sample, connections, rateTriggered)
	} else if t.idleOpportunityLocked(sample, connections, goroutines) {
		t.submitIdleLocked(sample, connections)
	}
	t.access.Unlock()
	defer t.pressureCallbacks.Done()

	if hook := t.afterDecisionHook; hook != nil {
		hook()
	}
	if t.recorder != nil {
		t.recorder.sample(sample, state, connections)
		if state != previousState {
			t.recorder.recordStateChange(state, sample)
		}
	}
}

// releaseIdleMemory is the SYNCHRONOUS idle entry point. Production polls queue the action on the
// reclaim executor instead; this path keeps the decision directly testable and is also the shape a
// future caller with its own serialisation can use.
//
// It takes releaseAccess BEFORE deciding so a direct caller cannot decide against a footprint that
// an in-flight executor action is about to change. That is the same lock the executor holds for the
// whole destructive action; the order is releaseAccess -> access, and no path takes them in the
// other order.
func (t *adaptiveTimer) releaseIdleMemory(sample memorySample, connections int) {
	goroutines := runtime.NumGoroutine()
	t.releaseAccess.Lock()
	defer t.releaseAccess.Unlock()

	t.access.Lock()
	if t.timer == nil || t.reclaimStopped {
		t.access.Unlock()
		return
	}
	if !t.idleOpportunityLocked(sample, connections, goroutines) {
		t.access.Unlock()
		return
	}
	t.idleInFlight = true
	// Join the lifecycle barrier under the same lock as the decision, so stop() cannot start
	// waiting between "I will trim" and the count that lets it wait.
	t.pressureCallbacks.Add(1)
	t.access.Unlock()
	defer t.pressureCallbacks.Done()

	t.runIdleReclaimLocked(&reclaimRequest{kind: reclaimIdle, sample: sample, connections: connections})

	t.access.Lock()
	t.idleInFlight = false
	t.access.Unlock()
}

// idleOpportunityLocked records the baselines of this sample and answers whether an idle release is
// justified. It must be called with access held.
//
// # What is and is not an opportunity
//
// A release is justified by an observed opportunity - a meaningful drop in goroutines/connections
// since the previous sample, or a footprint drop of at least the benefit floor - or by real
// pressure. A completed GC is NOT one: FreeOSMemory itself runs a GC, so "no GC since last time"
// made the loop self-sustaining at 33 MiB. Armed is not one either: letting `usage >= armed`
// substitute for a drop is what made a stable 41 MiB footprint run Trim + GC every second.
//
// # What is explicitly excluded
//
// A pending or running reclaim of any kind suppresses a new idle request. The opportunity belongs
// to the sample that observed it, not to a second tick that arrives while the first action is still
// working; queueing those duplicates is how one drop becomes several trims.
func (t *adaptiveTimer) idleOpportunityLocked(sample memorySample, connections int, goroutines int) bool {
	shrank := hasMeaningfulDrop(t.lastGoroutines, goroutines) || hasMeaningfulDrop(t.lastConnections, connections)
	dropped := t.lastUsage > sample.usage && t.lastUsage-sample.usage >= defaultIdleBenefitFloor
	t.lastGoroutines = goroutines
	t.lastConnections = connections
	t.lastUsage = sample.usage

	now := time.Now()
	if t.belowResume(sample) ||
		now.Sub(t.lastRelease) < defaultReleaseInterval ||
		now.Before(t.idleCooldownUntil) ||
		(!shrank && !dropped) {
		return false
	}
	if t.idleInFlight || t.pressureInFlight || t.crisisInFlight ||
		t.pendingReclaim != nil || t.inFlightReclaim != reclaimNone {
		return false
	}
	t.lastRelease = now
	return true
}

// submitIdleLocked queues the gentle pass. Callers must have just been granted an opportunity.
func (t *adaptiveTimer) submitIdleLocked(sample memorySample, connections int) bool {
	if t.reclaimStopped || t.idleInFlight || t.crisisInFlight || t.pressureInFlight ||
		t.pendingReclaim != nil || t.inFlightReclaim != reclaimNone {
		return false
	}
	t.idleInFlight = true
	t.enqueueReclaimLocked(&reclaimRequest{kind: reclaimIdle, sample: sample, connections: connections})
	return true
}

// submitPressureLocked queues the Darwin critical pass. It is a no-op when something at least as
// urgent is already pending or running: a system-wide signal that repeats while this process is
// already flushing the same cache and forcing the same GC does not need a second execution.
func (t *adaptiveTimer) submitPressureLocked() bool {
	if t.reclaimStopped || t.crisisInFlight || t.pressureInFlight {
		return false
	}
	t.pressureInFlight = true
	t.enqueueReclaimLocked(&reclaimRequest{kind: reclaimPressure})
	return true
}

// submitCrisisLocked queues the destructive reset. A crisis request outranks pressure and idle: it
// replaces a pending request of either kind (reusing that request's barrier token) or waits for the
// in-flight one. It never queues a second crisis: the state machine is already Triggered and the
// reset in hand covers the state that would justify another.
func (t *adaptiveTimer) submitCrisisLocked(sample memorySample, connections int, rateTriggered bool) bool {
	if t.reclaimStopped || t.crisisInFlight {
		return false
	}
	t.crisisInFlight = true
	reason := resetReasonThreshold
	if rateTriggered {
		reason = resetReasonRate
	}
	t.enqueueReclaimLocked(&reclaimRequest{
		kind:          reclaimCrisis,
		sample:        sample,
		connections:   connections,
		reason:        reason,
		rateTriggered: rateTriggered,
		reportOnly:    t.killerDisabled,
	})
	return true
}

// enqueueReclaimLocked installs a request as the single pending action.
//
// Replacing a pending request does NOT touch the WaitGroup: the count already represents "one
// queued action", and the replacement will release it when it runs or is dropped. Adding for the
// replacement and releasing the old token separately would open a transient zero that stop()'s Wait
// must never observe between the two operations.
func (t *adaptiveTimer) enqueueReclaimLocked(request *reclaimRequest) {
	if pending := t.pendingReclaim; pending != nil {
		// The pending request is superseded. Its dedicated in-flight flag belongs to the new
		// request now, so clear the old one to keep the idle admission gate accurate.
		switch pending.kind {
		case reclaimIdle:
			t.idleInFlight = false
		case reclaimPressure:
			t.pressureInFlight = false
		}
		t.pendingReclaim = request
		t.signalReclaimLocked()
		return
	}
	t.ensureReclaimExecutorLocked()
	t.pendingReclaim = request
	t.pressureCallbacks.Add(1)
	t.signalReclaimLocked()
}

func (t *adaptiveTimer) ensureReclaimExecutorLocked() {
	if t.reclaimStarted {
		return
	}
	t.reclaimStarted = true
	t.reclaimWake = make(chan struct{}, 1)
	t.reclaimStop = make(chan struct{})
	t.reclaimExited = make(chan struct{})
	go t.reclaimLoop(t.reclaimWake, t.reclaimStop, t.reclaimExited)
}

func (t *adaptiveTimer) signalReclaimLocked() {
	select {
	case t.reclaimWake <- struct{}{}:
	default:
	}
}

// dropPendingReclaimLocked discards the queued action and releases its token. The in-flight
// request, if any, is untouched: stop() waits for that one.
func (t *adaptiveTimer) dropPendingReclaimLocked() {
	request := t.pendingReclaim
	if request == nil {
		return
	}
	t.pendingReclaim = nil
	switch request.kind {
	case reclaimIdle:
		t.idleInFlight = false
	case reclaimPressure:
		t.pressureInFlight = false
	case reclaimCrisis:
		t.crisisInFlight = false
	}
	t.pressureCallbacks.Done()
}

// reclaimLoop is the single destructive executor. It is started on first use and exits on stop.
func (t *adaptiveTimer) reclaimLoop(wake <-chan struct{}, stopChannel <-chan struct{}, exited chan<- struct{}) {
	defer close(exited)
	for {
		t.access.Lock()
		if t.reclaimStopped {
			t.dropPendingReclaimLocked()
			t.access.Unlock()
			return
		}
		request := t.pendingReclaim
		if request == nil {
			t.access.Unlock()
			select {
			case <-wake:
			case <-stopChannel:
			}
			continue
		}
		t.pendingReclaim = nil
		t.inFlightReclaim = request.kind
		t.access.Unlock()

		t.runReclaim(request)

		t.access.Lock()
		t.finishReclaimLocked(request.kind)
		t.access.Unlock()
		t.pressureCallbacks.Done()
	}
}

// finishReclaimLocked clears the flags of the action that just finished and applies the post-crisis
// cooling. The cooldown is the important half: after a reset the footprint really did drop, and
// without it the very next ordinary poll sees that drop as a fresh opportunity and runs another
// Trim + GC immediately after the crisis pass already did both.
func (t *adaptiveTimer) finishReclaimLocked(kind reclaimKind) {
	t.inFlightReclaim = reclaimNone
	switch kind {
	case reclaimIdle:
		t.idleInFlight = false
	case reclaimPressure:
		t.pressureInFlight = false
	case reclaimCrisis:
		t.crisisInFlight = false
		now := time.Now()
		t.lastRelease = now
		t.idleCooldownUntil = now.Add(defaultIdleCooldown)
	}
}

func (t *adaptiveTimer) runReclaim(request *reclaimRequest) {
	switch request.kind {
	case reclaimIdle:
		t.runIdleReclaim(request)
	case reclaimPressure:
		t.runPressureReclaim()
	case reclaimCrisis:
		t.runCrisisReclaim(request)
	}
}

// runIdleReclaim is the layered gentle pass.
//
// # The order, and why it is observable rather than cosmetic
//
//	L1  expired caches          no socket is touched; reuse of live entries is preserved
//	L2  idle pools              sockets that carry nothing; the next demand may re-handshake
//	L3  forced GC               STW and CPU; only when L1/L2 did not reach the benefit floor
//
// Each stage is measured from the same starting footprint, and a stage that reaches the floor stops
// the pass. This is what makes "trim before GC" a decision instead of a fixed sequence: a cache
// trim that returns a megabyte must not also cost a full GC and a round of handshakes. A later pass
// starts again from L1, so the cheap layer is never skipped merely because it was insufficient once.
func (t *adaptiveTimer) runIdleReclaim(request *reclaimRequest) {
	t.releaseAccess.Lock()
	defer t.releaseAccess.Unlock()
	t.runIdleReclaimLocked(request)
}

// runIdleReclaimLocked is the body, for callers that already hold releaseAccess (the synchronous
// releaseIdleMemory entry point and the executor both do).
func (t *adaptiveTimer) runIdleReclaimLocked(request *reclaimRequest) {
	before := t.currentUsage()
	ctx := context.Background()

	// L1: expired cache entries. This is deliberately first: it has no connection ownership at all.
	if t.trimCaches != nil {
		t.trimCaches(ctx)
		afterCache := t.currentUsage()
		if gain := droppedBytes(before, afterCache); gain >= defaultIdleBenefitFloor {
			t.finishIdleReclaim(before, afterCache, "cache", gain)
			return
		}
	}

	// A crisis observed while the cheap layer was running must not wait for a pool walk it does not
	// need. The layer boundary is the cancellation point: the caches are already trimmed, so ending
	// here loses nothing and lets the destructive pass take the executor immediately.
	if t.crisisQueued() {
		afterCache := t.currentUsage()
		t.finishIdleReclaim(before, afterCache, "cache (crisis pending)", droppedBytes(before, afterCache))
		return
	}

	// L2: idle pools. This is the pass that may cost a handshake, so it only runs when the cheap
	// layer did not already reach the benefit floor.
	if t.trimMemory != nil {
		t.trimMemory(ctx)
	} else if t.network != nil {
		t.network.TrimMemory(ctx)
	}
	afterPools := t.currentUsage()
	if gain := droppedBytes(before, afterPools); gain >= defaultIdleBenefitFloor {
		t.finishIdleReclaim(before, afterPools, "pools", gain)
		return
	}

	// Same cancellation point before the most expensive action. A reset already queued behind this
	// pass will do the runtime work itself.
	if t.crisisQueued() {
		afterPools = t.currentUsage()
		t.finishIdleReclaim(before, afterPools, "pools (crisis pending)", droppedBytes(before, afterPools))
		return
	}

	// L3: the runtime. FreeOSMemory is a forced GC plus scavenge, and it is the most expensive
	// action available short of a network reset. It must have a reason beyond "the first two layers
	// were quiet": real pressure at/above armed, or idle Go pages the runtime itself reports.
	if t.shouldFreeOSMemory(request.sample) {
		if t.freeOSMemory != nil {
			t.freeOSMemory()
		} else {
			runtimeDebug.FreeOSMemory()
		}
	}
	after := t.currentUsage()
	t.finishIdleReclaim(before, after, "runtime", droppedBytes(before, after))
}

func (t *adaptiveTimer) finishIdleReclaim(before uint64, after uint64, stage string, gain uint64) {
	lowYield := after+defaultIdleBenefitFloor >= before
	t.access.Lock()
	t.lastGCCycles = readGCCycles()
	if lowYield {
		// The footprint is at its floor for this layer set. Back off instead of repeating the same
		// work on the next sample; a crisis path does not consult this cooldown.
		t.idleCooldownUntil = time.Now().Add(defaultIdleCooldown)
	}
	t.access.Unlock()

	switch {
	case gain >= defaultIdleBenefitFloor:
		t.logger.Trace("released idle memory via ", stage, ", usage: ", byteformats.FormatMemoryBytes(before),
			" -> ", byteformats.FormatMemoryBytes(after), ", delta ", byteformats.FormatMemoryBytes(gain))
	case gain > 0:
		t.logger.Trace("idle release via ", stage, " gained only ", byteformats.FormatMemoryBytes(gain),
			", cooling down for ", defaultIdleCooldown, ", usage: ",
			byteformats.FormatMemoryBytes(before), " -> ", byteformats.FormatMemoryBytes(after))
	default:
		t.logger.Trace("idle release via ", stage, " made no measurable gain, cooling down for ",
			defaultIdleCooldown, ", usage: ", byteformats.FormatMemoryBytes(after))
	}
}

// runPressureReclaim is the Darwin critical pass. It is deliberately not a network reset: the
// dispatch notification is system-wide and says nothing about whether this process's paths broke.
func (t *adaptiveTimer) runPressureReclaim() {
	t.releaseAccess.Lock()
	defer t.releaseAccess.Unlock()
	t.releaseMemory()
}

// runCrisisReclaim is the destructive reset: ReleaseMemory (which retires the network) followed by
// the cache flush, Critical publication and forced GC that the report-only mode also performs.
//
// # The decision is re-checked before the destructive step
//
// A crisis request can wait behind an in-flight idle pass. That pass may have released exactly the
// memory the crisis was called for - the trigger was observed before the trim completed, not after
// it. Resetting the network then would terminate healthy paths for a footprint that is already back
// below the armed line: the most expensive possible answer to a problem that no longer exists.
//
// The re-check is deliberately against the armed line, not the trigger: the state machine keeps
// Triggered until armed, and a rate-predicted reset is allowed to act while the footprint is still
// below the trigger. Report-only mode has no network reset to skip and is left on its own path.
func (t *adaptiveTimer) runCrisisReclaim(request *reclaimRequest) {
	t.releaseAccess.Lock()
	defer t.releaseAccess.Unlock()

	current := t.readSample()
	if !request.reportOnly && !t.atOrAboveArmed(current) {
		t.logger.Trace("crisis reset skipped, footprint fell below the armed line: ",
			byteformats.FormatMemoryBytes(current.usage), t.logDetails(current))
		return
	}

	if request.rateTriggered {
		if request.reportOnly {
			t.logger.Warn("memory growth rate critical (report only), usage: ", byteformats.FormatMemoryBytes(current.usage), t.logDetails(current))
		} else {
			t.logger.Error("memory growth rate critical, usage: ", byteformats.FormatMemoryBytes(current.usage), t.logDetails(current), ", resetting network")
		}
	} else {
		if request.reportOnly {
			t.logger.Warn("memory threshold reached (report only), usage: ", byteformats.FormatMemoryBytes(current.usage), t.logDetails(current))
		} else {
			t.logger.Error("memory threshold reached, usage: ", byteformats.FormatMemoryBytes(current.usage), t.logDetails(current), ", resetting network")
		}
	}
	if !request.reportOnly {
		t.releaseNetworkMemory(context.Background())
	}
	t.releaseMemory()
	if t.recorder != nil {
		after := t.readSample()
		t.recorder.recordReset(request.reason, current, after, request.connections, request.reportOnly)
		t.recorder.snapshot(SnapshotReasonReset, current, t.belowTrigger(after), false)
	}
}

func (t *adaptiveTimer) releaseNetworkMemory(ctx context.Context) {
	if t.releaseNetwork != nil {
		t.releaseNetwork(ctx)
		return
	}
	// A timer built without a network manager is a unit fixture; the crisis pass still does the
	// cache flush and GC, which is what report-only mode does in production too.
	if t.network != nil {
		t.network.ReleaseMemory(ctx)
	}
}

func (t *adaptiveTimer) readSample() memorySample {
	if t.sampleFunc != nil {
		return t.sampleFunc()
	}
	return readMemorySample(t.policyMode)
}

func (t *adaptiveTimer) currentUsage() uint64 {
	if t.readUsage != nil {
		return t.readUsage()
	}
	return memory.Total()
}

func droppedBytes(before uint64, after uint64) uint64 {
	if after >= before {
		return 0
	}
	return before - after
}

// crisisQueued reports whether the gentle pass should stop at its next layer boundary: a destructive
// reset is waiting behind it, or the timer is being closed and the remaining layers would be work
// for a lifecycle that is ending. It is the abort signal the layer boundaries consult.
func (t *adaptiveTimer) crisisQueued() bool {
	t.access.Lock()
	defer t.access.Unlock()
	return t.crisisInFlight || t.reclaimStopped
}

// shouldFreeOSMemory decides whether the runtime layer is justified.
func (t *adaptiveTimer) shouldFreeOSMemory(sample memorySample) bool {
	if t.atOrAboveArmed(sample) {
		return true
	}
	reclaimable := readRuntimeReclaimableBytes()
	if t.runtimeReclaimable != nil {
		reclaimable = t.runtimeReclaimable()
	}
	return reclaimable >= defaultIdleBenefitFloor
}

func (t *adaptiveTimer) atOrAboveArmed(sample memorySample) bool {
	switch t.policyMode {
	case policyModeMemoryLimit, policyModeNetworkExtension:
		return sample.usage >= t.limitThresholds.armed
	case policyModeAvailable:
		return sample.availableKnown && sample.available <= t.availableThresholds(sample).armed
	default:
		return false
	}
}

// readRuntimeReclaimableBytes reports free Go heap spans that a GC/scavenge can return to the OS.
//
// It is evidence for L3 below the armed line. It is not a promise: the runtime may choose to keep
// some free spans, and the footprint measurement is authoritative. It only prevents a forced GC
// from running when the runtime has nothing to give - which is the "33 MiB is the working set" case
// the idle loop must not turn into a collector loop.
func readRuntimeReclaimableBytes() uint64 {
	samples := []metrics.Sample{{Name: "/memory/classes/heap/free:bytes"}}
	metrics.Read(samples)
	if samples[0].Value.Kind() != metrics.KindUint64 {
		return 0
	}
	return samples[0].Value.Uint64()
}

func hasMeaningfulDrop(previous int, current int) bool {
	if current >= previous {
		return false
	}
	return previous-current >= max(defaultReleaseShrinkFloor, previous/defaultReleaseShrinkDivisor)
}

func readGCCycles() uint64 {
	samples := []metrics.Sample{{Name: "/gc/cycles/total:gc-cycles"}}
	metrics.Read(samples)
	if samples[0].Value.Kind() != metrics.KindUint64 {
		return 0
	}
	return samples[0].Value.Uint64()
}

func (t *adaptiveTimer) belowResume(sample memorySample) bool {
	switch t.policyMode {
	case policyModeMemoryLimit, policyModeNetworkExtension:
		return sample.usage < t.limitThresholds.resume
	case policyModeAvailable:
		return !sample.availableKnown || sample.available > t.availableThresholds(sample).resume
	default:
		return true
	}
}

func (t *adaptiveTimer) belowTrigger(sample memorySample) bool {
	switch t.policyMode {
	case policyModeMemoryLimit, policyModeNetworkExtension:
		return sample.usage < t.limitThresholds.trigger
	case policyModeAvailable:
		return !sample.availableKnown || sample.available > t.availableThresholds(sample).trigger
	default:
		return true
	}
}

func (t *adaptiveTimer) releaseMemory() {
	// The hook runs BEFORE the destructive work, so a test can hold a callback inside the
	// in-flight barrier and observe whether stop() waits for it. Placed after the work it would
	// only prove the work had already happened.
	//
	// It is read without the lock: tests install it before starting anything, and reading it
	// under t.access would deadlock against stop(), which holds that lock while a callback is
	// meant to be finishing.
	if hook := t.cleanupHook; hook != nil {
		hook()
	}
	if t.cacheFile != nil {
		t.cacheFile.Flush()
	}
	t.pressure.Store(uint32(tun.MemoryPressureCritical))
	badCleanup()
	if t.freeOSMemory != nil {
		t.freeOSMemory()
	} else {
		runtimeDebug.FreeOSMemory()
	}
}

func (s pressureState) memoryPressure() tun.MemoryPressure {
	switch s {
	case pressureStateArmed:
		return tun.MemoryPressureWarning
	case pressureStateTriggered:
		return tun.MemoryPressureCritical
	default:
		return tun.MemoryPressureNone
	}
}

func (t *adaptiveTimer) nextState(sample memorySample) pressureState {
	switch t.policyMode {
	case policyModeMemoryLimit, policyModeNetworkExtension:
		return nextPressureState(t.state,
			sample.usage >= t.limitThresholds.trigger,
			sample.usage >= t.limitThresholds.armed,
		)
	case policyModeAvailable:
		if !sample.availableKnown {
			return pressureStateNormal
		}
		thresholds := t.availableThresholds(sample)
		return nextPressureState(t.state,
			sample.available <= thresholds.trigger,
			sample.available <= thresholds.armed,
		)
	default:
		return pressureStateNormal
	}
}

func computeLimitThresholds(memoryLimit uint64, safetyMargin uint64) pressureThresholds {
	triggerMargin := min(safetyMargin, memoryLimit)
	armedMargin := min(triggerMargin*2, memoryLimit)
	resumeMargin := min(triggerMargin*4, memoryLimit)
	return pressureThresholds{
		trigger: memoryLimit - triggerMargin,
		armed:   memoryLimit - armedMargin,
		resume:  memoryLimit - resumeMargin,
	}
}

// RuntimeMemoryLimit returns the Go runtime soft limit for a given process budget.
//
// It is the "armed" threshold - the budget less two safety margins - and it shares the
// derivation with the canonical NetworkExtension policy rather than repeating the
// arithmetic. When this and the timer thresholds were computed independently they could
// disagree about the same budget, which is exactly the fork this helper removes.
func RuntimeMemoryLimit(memoryLimit uint64) uint64 {
	return computeLimitThresholds(memoryLimit, defaultSafetyMargin).armed
}

func (t *adaptiveTimer) availableThresholds(sample memorySample) pressureThresholds {
	var triggerMargin uint64
	if t.hasSafetyMargin {
		triggerMargin = t.safetyMargin
	} else if sample.usage == 0 {
		triggerMargin = defaultAvailableTriggerMarginMin
	} else {
		triggerMargin = max(defaultAvailableTriggerMarginMin, min(sample.usage/4, defaultAvailableTriggerMarginMax))
	}
	return pressureThresholds{
		trigger: triggerMargin,
		armed:   triggerMargin * 2,
		resume:  triggerMargin * 4,
	}
}

// intervalForState returns how long to wait before the next observation.
//
// # The fresh-start ramp
//
// A fresh timer polls at minInterval, then DOUBLES on each normal sample until it reaches
// maxInterval. The previous code special-cased the first normal poll as
//
//	if t.currentInterval == 0 { t.currentInterval = t.maxInterval }
//
// which skipped the entire ladder: the only guaranteed observations were at 100ms and then
// 10s later. With the canonical policy - a 50 MiB budget, a 5 MiB margin and a 45 MiB trigger -
// a native or Swift allocation burst of more than 5 MiB during that ten-second gap would never
// be seen by the timer.
//
// # Why the gap was not covered elsewhere
//
// The dispatch memory-pressure source is NOT a substitute. DISPATCH_MEMORYPRESSURE_CRITICAL is
// a SYSTEM-WIDE signal: it reports that the kernel is under pressure generally, not that this
// process is approaching its own limit. It can fire for another app's allocations, and there is
// no guarantee it fires before a per-process jetsam high-water mark is reached. The timer is the
// only component that observes THIS process's phys_footprint on a schedule, so the schedule has
// to be dense enough to be useful.
//
// # Why a ramp rather than a permanent fast poll
//
// Polling at 100ms forever would close the gap completely and cost a wakeup every 100ms for the
// entire life of a tunnel that is usually idle - battery spent observing a process that is not
// changing. The ramp concentrates the observations where the risk is: shortly after start, and
// whenever memory is actually moving. The cost is a bounded number of extra wakeups - 100ms,
// 200ms, 400ms, ... up to 10s, so about seven extra polls - and it is measured rather than
// assumed; see the cadence benchmark.
//
// # Where the risk actually is
//
// A process that is genuinely growing rarely stays in the normal state: it crosses the armed
// threshold and returns to minInterval on its own. The ramp's job is to make sure it is SEEN
// crossing.
func (t *adaptiveTimer) intervalForState() time.Duration {
	switch {
	case t.forceMinInterval || t.state != pressureStateNormal || !t.pressureBaselineTime.IsZero():
		// Pressure, or a recent pressure event: observe closely.
		t.currentInterval = t.minInterval
	default:
		if t.currentInterval == 0 {
			// The first normal sample after start (or after a reset). Begin the ramp from the
			// bottom rather than jumping to the ceiling.
			t.currentInterval = t.minInterval
		} else {
			t.currentInterval = min(t.currentInterval*2, t.maxInterval)
		}
	}
	return t.currentInterval
}

// intervalScheduleForDiagnostics returns the observation schedule a fresh timer follows while
// everything stays normal, for reporting and for tests.
//
// It exists so the ramp's shape is a stated property rather than something a reader has to
// derive by simulating the timer.
func intervalScheduleForDiagnostics(minInterval time.Duration, maxInterval time.Duration, steps int) []time.Duration {
	if steps <= 0 {
		return nil
	}
	schedule := make([]time.Duration, 0, steps)
	current := time.Duration(0)
	for i := 0; i < steps; i++ {
		if current == 0 {
			current = minInterval
		} else {
			current = min(current*2, maxInterval)
		}
		schedule = append(schedule, current)
	}
	return schedule
}

func (t *adaptiveTimer) logDetails(sample memorySample) string {
	switch t.policyMode {
	case policyModeMemoryLimit, policyModeNetworkExtension:
		headroom := uint64(0)
		if sample.usage < t.memoryLimit {
			headroom = t.memoryLimit - sample.usage
		}
		return ", limit: " + byteformats.FormatMemoryBytes(t.memoryLimit) + ", headroom: " + byteformats.FormatMemoryBytes(headroom)
	case policyModeAvailable:
		if sample.availableKnown {
			return ", available: " + byteformats.FormatMemoryBytes(sample.available)
		}
	}
	return ""
}

func nextPressureState(current pressureState, shouldTrigger, shouldArm bool) pressureState {
	if current == pressureStateTriggered {
		// Exit the crisis state at the ARMED line, not at resume. resume (30 MiB) is the idle
		// hysteresis floor; a process can sit at 33 MiB forever, so using it here pinned the
		// killer in Triggered after one reset and kept GOMEMLIMIT/ReleaseMemory pressure on a
		// process that was no longer near the trigger. Upstream 9c2aa1bed32a.
		if shouldArm {
			return pressureStateTriggered
		}
		return pressureStateNormal
	}
	if shouldTrigger {
		return pressureStateTriggered
	}
	if shouldArm {
		return pressureStateArmed
	}
	return pressureStateNormal
}

// readMemorySample reads the process footprint, and the available-memory figure when the
// platform actually provides one.
//
// # Why availableKnown is not simply set by mode
//
// The previous version marked the sample "known" whenever the mode was one that could use
// it. That conflates two different states: "the platform says there are N bytes free" and
// "this platform has no such API". On the second, memory.Available() returns 0, and a
// zero read as a real measurement means "no memory available" - the timer would treat a
// healthy process on an unsupported platform as being in permanent critical pressure.
//
// memory.AvailableAvailable() is the platform's own answer to whether the figure is
// meaningful, so it decides. phys_footprint (memory.Total()) remains the primary signal
// and is always available.
func readMemorySample(mode policyMode) memorySample {
	sample := memorySample{
		usage: memory.Total(),
	}
	if mode == policyModeAvailable || mode == policyModeNetworkExtension {
		if memory.AvailableAvailable() {
			sample.available = memory.Available()
			sample.availableKnown = true
		}
	}
	return sample
}

func (s pressureState) String() string {
	switch s {
	case pressureStateArmed:
		return "armed"
	case pressureStateTriggered:
		return "triggered"
	default:
		return "normal"
	}
}
