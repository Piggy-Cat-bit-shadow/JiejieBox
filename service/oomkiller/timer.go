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
	freeOSMemory      func()

	// pressureCallbacks tracks memory-pressure callbacks that have passed the active check
	// and are still running their destructive cleanup.
	//
	// # Why a WaitGroup is needed at all
	//
	// Checking `t.timer != nil` under the lock is necessary but NOT sufficient. The callback
	// releases the lock before doing its work, precisely so a long FreeOSMemory does not stall
	// Close. Without something to join on, this sequence is possible:
	//
	//	notifyPressure: active check passes, unlock
	//	Close:          stop() sets timer = nil, stopTimer() stores None, Close RETURNS
	//	notifyPressure: releaseMemory() stores Critical, flushes the cache, calls FreeOSMemory
	//
	// So Close has returned and the pressure flag a torn-down service reports is Critical
	// again, with destructive cleanup still to come. The active check bounds WHEN the work
	// starts; the barrier bounds when it has FINISHED.
	//
	// # Why this ordering cannot race
	//
	// Add(1) happens while t.access is held and only after the active check has passed, and
	// stop() sets timer = nil while holding the same lock before it Waits. So:
	//
	//   - a callback that entered before stop() took the lock has already Added, and Wait
	//     observes it;
	//   - a callback that enters after stop() set timer = nil fails the active check and never
	//     Adds, so it cannot be missed.
	//
	// There is no window in which Add can happen after Wait has decided the count is zero.
	pressureCallbacks sync.WaitGroup

	// cleanupHook, when set, runs at the start of the destructive cleanup. Tests use it to
	// hold a callback inside the barrier deterministically instead of racing goroutines and
	// hoping to hit the interleaving.
	cleanupHook func()
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

// stop halts the timer and WAITS for in-flight pressure callbacks to finish.
//
// # Why the wait is the point
//
// Setting timer = nil under the lock stops new callbacks from starting, but a callback that
// already passed its active check is still running its destructive cleanup - and if stop
// returned immediately, Service.Close could return while that cleanup is still going to store
// Critical. Callers treat the return of stop as "nothing further will touch this service", and
// the barrier is what makes that true rather than merely likely.
//
// # Why the lock is released before Wait
//
// The callback must be able to finish, and it does not need t.access to do so. Waiting while
// holding the lock would deadlock the moment a callback tried to acquire it, and would also
// turn a slow FreeOSMemory into a stall for every other reader of the timer state.
func (t *adaptiveTimer) stop() timerState {
	t.access.Lock()
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
	state := t.timerState
	t.access.Unlock()

	// Everything that legitimately started before timer was set to nil finishes here. Nothing
	// can join the group after this point: a later callback sees timer == nil and returns
	// without Add.
	t.pressureCallbacks.Wait()

	return state
}

func (t *adaptiveTimer) poll() {
	var triggered bool
	var rateTriggered bool
	sample := readMemorySample(t.policyMode)
	if t.sampleFunc != nil {
		sample = t.sampleFunc()
	}

	t.access.Lock()
	if t.timer == nil {
		t.access.Unlock()
		return
	}
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
	triggered = previousState != pressureStateTriggered && t.state == pressureStateTriggered
	if !triggered && !t.pressureBaselineTime.IsZero() && t.memoryLimit > 0 &&
		sample.usage > t.pressureBaseline.usage && sample.usage < t.memoryLimit {
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
	if triggered {
		// The destructive reset below runs outside the lock, like the idle release. Join the
		// same barrier so stop()/Close cannot return while an old poll is still resetting the
		// network or publishing Critical pressure. Add happens under access, after the timer
		// check, so stop() cannot start waiting between the decision and the Add.
		t.pressureCallbacks.Add(1)
	}
	t.access.Unlock()
	if triggered {
		defer t.pressureCallbacks.Done()
	}
	t.pressure.Store(uint32(state.memoryPressure()))
	var connections int
	if t.connections != nil {
		connections = t.connections.Count()
	}
	if t.recorder != nil {
		t.recorder.sample(sample, state, connections)
		if state != previousState {
			t.recorder.recordStateChange(state, sample)
		}
	}
	if !triggered {
		t.releaseIdleMemory(sample, connections)
		return
	}
	var reason string
	if rateTriggered {
		reason = resetReasonRate
		if t.killerDisabled {
			t.logger.Warn("memory growth rate critical (report only), usage: ", byteformats.FormatMemoryBytes(sample.usage), t.logDetails(sample))
		} else {
			t.logger.Error("memory growth rate critical, usage: ", byteformats.FormatMemoryBytes(sample.usage), t.logDetails(sample), ", resetting network")
			t.network.ReleaseMemory(context.Background())
		}
	} else {
		reason = resetReasonThreshold
		if t.killerDisabled {
			t.logger.Warn("memory threshold reached (report only), usage: ", byteformats.FormatMemoryBytes(sample.usage), t.logDetails(sample))
		} else {
			t.logger.Error("memory threshold reached, usage: ", byteformats.FormatMemoryBytes(sample.usage), t.logDetails(sample), ", resetting network")
			t.network.ReleaseMemory(context.Background())
		}
	}
	t.releaseMemory()
	if t.recorder != nil {
		after := readMemorySample(t.policyMode)
		t.recorder.recordReset(reason, sample, after, connections, t.killerDisabled)
		t.recorder.snapshot(SnapshotReasonReset, sample, t.belowTrigger(after), false)
	}
}

func (t *adaptiveTimer) releaseIdleMemory(sample memorySample, connections int) {
	t.releaseAccess.Lock()
	defer t.releaseAccess.Unlock()
	goroutines := runtime.NumGoroutine()
	now := time.Now()
	t.access.Lock()
	if t.timer == nil {
		t.access.Unlock()
		return
	}
	shrank := hasMeaningfulDrop(t.lastGoroutines, goroutines) || hasMeaningfulDrop(t.lastConnections, connections)
	dropped := t.lastUsage > sample.usage && t.lastUsage-sample.usage >= defaultIdleBenefitFloor
	t.lastGoroutines = goroutines
	t.lastConnections = connections
	t.lastUsage = sample.usage
	// An idle release is justified by an observed opportunity (a meaningful drop since the last
	// sample) or by real pressure. A completed GC is NOT such an opportunity: FreeOSMemory itself
	// runs a GC, so using "no GC since last time" as the trigger made the loop self-sustaining at a
	// stable footprint (the 33 MB / 1 s case). Below-resume still means "the process is comfortably
	// inside its floor"; above-armed overrides the low-yield cooldown so a real emergency is never
	// delayed by the ordinary backoff.
	// Opportunistic idle release only. Pressure at/above armed is handled by poll's state machine
	// (Triggered/reset), not by this path: letting "armed" bypass the cooldown made a stable
	// 41 MiB footprint run TrimMemory + FreeOSMemory every second, a new version of the original
	// loop at a higher level. Growth toward the trigger is still observed and escalated by poll.
	if t.belowResume(sample) ||
		now.Sub(t.lastRelease) < defaultReleaseInterval ||
		now.Before(t.idleCooldownUntil) ||
		(!shrank && !dropped) {
		t.access.Unlock()
		return
	}
	t.lastRelease = now
	// Join the same barrier the pressure callbacks use. Add happens while access is held and
	// after the timer != nil check, so stop() cannot start waiting between the check and the
	// Add; Close therefore cannot return while an idle trim/GC is still touching the network.
	t.pressureCallbacks.Add(1)
	t.access.Unlock()
	defer t.pressureCallbacks.Done()
	// Progressive pass first, then let the runtime return freed pages. This is not ReleaseMemory:
	// it must not reset the network or force a dial.
	trim := t.trimMemory
	if trim != nil {
		trim(context.Background())
	} else if t.network != nil {
		t.network.TrimMemory(context.Background())
	}
	free := t.freeOSMemory
	if free != nil {
		free()
	} else {
		runtimeDebug.FreeOSMemory()
	}
	after := memory.Total()
	t.access.Lock()
	t.lastGCCycles = readGCCycles()
	if after+defaultIdleBenefitFloor >= sample.usage {
		// No measurable gain: the footprint is at its floor. Back off for a while instead of
		// running a full GC every second; a real pressure state bypasses this cooldown.
		t.idleCooldownUntil = time.Now().Add(defaultIdleCooldown)
	}
	t.access.Unlock()
	delta := uint64(0)
	if after < sample.usage {
		delta = sample.usage - after
	}
	switch {
	case delta >= defaultIdleBenefitFloor:
		t.logger.Trace("released idle memory, usage: ", byteformats.FormatMemoryBytes(sample.usage),
			" -> ", byteformats.FormatMemoryBytes(after), ", delta ", byteformats.FormatMemoryBytes(delta))
	case delta > 0:
		t.logger.Trace("idle release gained only ", byteformats.FormatMemoryBytes(delta),
			", cooling down for ", defaultIdleCooldown, ", usage: ",
			byteformats.FormatMemoryBytes(sample.usage), " -> ", byteformats.FormatMemoryBytes(after))
	default:
		t.logger.Trace("idle release made no measurable gain, cooling down for ", defaultIdleCooldown,
			", usage: ", byteformats.FormatMemoryBytes(after))
	}
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
	runtimeDebug.FreeOSMemory()
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

// runtimeMemoryLimitForPolicy is the runtime soft limit the canonical policy implies.
//
// Callers inside a NetworkExtension use this rather than recomputing from the budget, so
// the value cannot diverge from the thresholds the timer enforces.
func runtimeMemoryLimitForPolicy() uint64 {
	return canonicalNetworkExtensionPolicy.runtimeMemoryLimit()
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
