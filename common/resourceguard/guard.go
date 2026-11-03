package resourceguard

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing/service"
)

// Level is the pressure level the guard polices against.
//
// It is a COPY of the level the process already computes, not a second measurement. The owner
// (service/oomkiller on iOS) feeds it from the one timer that already samples the process
// footprint, so the guard cannot disagree with the reclaim path about what state the process is
// in, and there is no second monitor to drift.
type Level uint8

const (
	// LevelNormal admits at the configured limit.
	LevelNormal Level = iota
	// LevelArmed is the first pressure level: the limit tightens, the wait shortens, and the
	// queue shrinks, but admission still succeeds for a burst that fits.
	LevelArmed
	// LevelCritical is the crisis level: only a handful of expensive creations may run, the
	// wait is short, and the queue is nearly closed. Shed hooks run on the transition in.
	LevelCritical
)

func (l Level) String() string {
	switch l {
	case LevelArmed:
		return "armed"
	case LevelCritical:
		return "critical"
	default:
		return "normal"
	}
}

// Config is the admission shape at LevelNormal. The other levels are derived from it by
// planFor, so a change to the base policy moves every level together or fails to compile.
type Config struct {
	// Limit is how many expensive creations may be in flight at once when the process is
	// normal. It is the ONLY number an operator would ever need to tune.
	Limit int
	// Queue is how many callers may wait for a permit instead of being refused.
	Queue int
	// Wait is how long one waiter may wait for a permit.
	Wait time.Duration
	// InteractiveReserve is the number of slots beyond Limit that ClassInteractive may use,
	// so a background burst cannot hold every slot an interactive flow needs.
	InteractiveReserve int
	// DemoteAfter is the minimum time a level is kept after pressure falls, so one quiet
	// sample cannot re-open admission into a burst that is still arriving. The pressure source
	// already has its own hysteresis; this is a second, much shorter dwell that exists purely
	// to debounce the level the ADMISSION path sees.
	DemoteAfter time.Duration
	// StarvationAfter bounds how long a queued caller may be passed over because its class does
	// not fit: after this long it may use the interactive reserve, up to the absolute cap. It is
	// the guarantee that turns the reserve into a priority rather than a permanent lockout for
	// the other class. Zero disables the guarantee, which is only correct for tests that drive
	// the gate by hand.
	StarvationAfter time.Duration
}

// DefaultNetworkExtensionConfig is the admission policy for an iOS Network Extension.
//
// # Where the numbers come from
//
// The measured failure this policy is built against: 200 UDP flows opened in about one second,
// each dialling an AnyTLS destination, produced 201 concurrent TLS handshakes and drove the
// extension from 15 MiB to 49.8 MiB against a 50 MiB budget, with 11.3 MiB of goroutine stacks
// and 15.5 MiB of heap objects.
//
// A limit of 32 concurrent expensive creations is chosen to be far above steady state (a
// healthy process establishes a handful) and far below the burst that failed (201), so the
// transient TLS state of a burst is bounded by roughly one sixth of the failing case.
//
// The queue (256) is sized so the OBSERVED burst is absorbed rather than refused: 200 flows
// behind 32 slots still finish inside the wait budget, while a caller beyond 256 is refused in
// constant time instead of parking. A queued waiter costs one small struct and its own channel;
// it does NOT cost a goroutine, because the caller already owns one. That asymmetry is why the
// queue may be generous while the limit must not be.
//
// These are ADMISSION defaults, not memory guarantees: the gate bounds how much expensive work
// may be STARTED, and the process budget remains owned by the oomkiller policy that already
// exists.
func DefaultNetworkExtensionConfig() Config {
	return Config{
		Limit:              32,
		Queue:              256,
		Wait:               5 * time.Second,
		InteractiveReserve: 4,
		DemoteAfter:        2 * time.Second,
		StarvationAfter:    time.Second,
	}
}

// planFor derives the admission shape for one level.
//
// # Why every derived value is clamped to the level above it
//
// The ratios are deliberately simple and monotonic: half the limit at Armed, a quarter at
// Critical. The FLOORS exist so a degenerate config still has an admittable shape, and they are
// what makes the derivation unsafe unless it is clamped: with Limit=1 the Armed floor of 2 is
// larger than the Normal limit, so arming the guard would admit MORE work than normal. That is
// the policy inverting exactly when it is supposed to tighten, and it is reachable by any
// operator who sets a small limit. Clamping to the previous level makes "higher pressure never
// admits more" a property of the function rather than a property of the defaults.
func (c Config) planFor(level Level) plan {
	normal := plan{limit: max(c.Limit, 1), queue: max(c.Queue, 0), wait: c.Wait}
	if normal.wait <= 0 {
		normal.wait = time.Second
	}
	switch level {
	case LevelCritical:
		armed := c.planFor(LevelArmed)
		return plan{
			limit: min(max(c.Limit/4, 1), armed.limit),
			queue: min(max(c.Queue/8, 2), armed.queue),
			// Fixed rather than derived: at critical the guard admits within one round trip or
			// not at all. Parking a caller for a fraction of the normal budget is the
			// behaviour this level exists to stop - by the time that wait expired the process
			// would have grown by whatever the caller was going to create.
			wait: min(100*time.Millisecond, armed.wait),
		}
	case LevelArmed:
		return plan{
			limit: min(max(c.Limit/2, 2), normal.limit),
			queue: min(max(c.Queue/2, 8), normal.queue),
			wait:  min(max(c.Wait/2, 100*time.Millisecond), normal.wait),
		}
	default:
		return normal
	}
}

// Guard is the process-wide admission policy for expensive, memory-amplifying creations.
//
// # Ownership
//
// The Guard owns no measurement, no timer, and no goroutine. It owns exactly two things: the
// admission gate, and the mapping from the process's pressure level to that gate's shape. The
// pressure itself is read from a function supplied at construction, which on iOS is the
// oomkiller service's own published state.
//
// # Why it is safe to call on a nil Guard
//
// Every method tolerates a nil receiver, and a nil Guard admits everything. A build with no
// guard configured (every platform except an iOS Network Extension, and every unit test that
// does not opt in) therefore keeps exactly its previous behaviour with one nil check per
// expensive dial, and no protocol code needs a platform branch.
type Guard struct {
	config   Config
	gate     *Gate
	pressure func() Level

	access      sync.Mutex
	level       Level
	levelSince  time.Time
	applied     atomic.Uint32
	transitions uint64
	// now is the clock the dwell reads. A field rather than a direct time.Now call so the
	// hysteresis bounds can be proven over simulated time.
	now func() time.Time
	// lowSince is when the observed level became CONTINUOUSLY lower than the current one.
	//
	// It exists because a dwell measured from the last transition is not a debounce: a signal
	// alternating every few milliseconds would still satisfy "50 ms since the level changed"
	// after enough toggles, so the guard would demote on a signal that never actually stayed
	// low - and every re-entry into critical runs the shed hooks again. That is the reclaim
	// loop this system must not have. Resetting the stamp on every observation at or above the
	// current level makes the dwell mean what it says: pressure must STAY lower for the full
	// period.
	lowSince time.Time

	shedAccess sync.Mutex
	shedHooks  []func()
}

// NewGuard builds a guard around a pressure source.
//
// pressure is called on every admission and must be cheap and non-blocking (an atomic load in
// production). A nil pressure source means "always normal", which is what a test that only
// exercises the gate wants.
func NewGuard(config Config, pressure func() Level) *Guard {
	if config.Limit < 1 {
		config.Limit = 1
	}
	if config.Queue < 0 {
		config.Queue = 0
	}
	if config.Wait <= 0 {
		config.Wait = time.Second
	}
	if config.InteractiveReserve < 0 {
		config.InteractiveReserve = 0
	}
	if config.DemoteAfter <= 0 {
		config.DemoteAfter = time.Second
	}
	if pressure == nil {
		pressure = func() Level { return LevelNormal }
	}
	created := &Guard{
		config:   config,
		pressure: pressure,
		level:    LevelNormal,

		levelSince: time.Now(),
		now:        time.Now,
	}
	created.gate = NewGate(config.planFor(LevelNormal), config.InteractiveReserve)
	created.gate.aging = config.StarvationAfter
	created.gate.now = time.Now
	return created
}

// useClock replaces the clock the hysteresis and the aging guarantee read.
//
// It exists so the dwell and starvation bounds can be PROVEN over a simulated minute instead of
// slept through, which is the only way a test can assert "at most one shed per dwell" for a
// realistic 2 s dwell without a two-minute CI step. Only time-of-day reads go through it; the
// per-waiter wait budget keeps a real timer, because that one bounds a caller's patience rather
// than the policy's memory.
func (g *Guard) useClock(now func() time.Time) {
	if now == nil {
		return
	}
	g.access.Lock()
	g.now = now
	g.levelSince = now()
	g.lowSince = time.Time{}
	g.access.Unlock()
	g.gate.now = now
}

// RegisterShed adds a callback that runs when the guard enters LevelCritical.
//
// Hooks are for CHEAP, non-blocking reclamation of resources that are not carrying traffic -
// closing idle sessions, releasing pooled buffers. They must not close a connection that is
// serving a flow: the guard exists so that a working connection survives pressure, and a hook
// that kills one is the failure the layering is meant to prevent.
func (g *Guard) RegisterShed(hook func()) {
	if g == nil || hook == nil {
		return
	}
	g.shedAccess.Lock()
	g.shedHooks = append(g.shedHooks, hook)
	g.shedAccess.Unlock()
}

// Level reports the current level after advancing the state machine.
//
// Reading has a side effect - it can complete a transition and run shed hooks - because the
// transition is edge-triggered from the observation itself rather than from a timer the guard
// does not have. Callers that only want to display the level should use Stats.
func (g *Guard) Level() Level {
	if g == nil {
		return LevelNormal
	}
	level, _, _ := g.observe()
	g.apply(level)
	return level
}

// Acquire asks for permission to start one expensive creation.
//
// On a nil Guard it returns (nil, nil) and the caller proceeds unguarded, which is the
// no-guard-configured behaviour. On a configured Guard it either returns a permit - which the
// caller must Release - or one of ErrQueueFull, ErrWaitTimeout, or the context error.
func (g *Guard) Acquire(ctx context.Context, class Class) (*Permit, error) {
	if g == nil {
		return nil, nil
	}
	level, _, _ := g.observe()
	g.apply(level)
	return g.gate.Acquire(ctx, class)
}

// observe advances the level state machine and reports it.
//
// The rule: a rise is immediate, a fall waits DemoteAfter. A rise is immediate because the cost
// of trusting a stale low level is an admission decision made under pressure; a fall is delayed
// because one quiet sample in the middle of a burst must not re-open the gate that the burst is
// still filling. The second return value says whether this call completed a transition INTO
// critical; the third is the hook set to run for it.
func (g *Guard) observe() (Level, bool, []func()) {
	observed := g.pressure()
	if observed > LevelCritical {
		observed = LevelCritical
	}
	now := g.clockNow()

	g.access.Lock()
	enteredCritical := false
	switch {
	case observed > g.level:
		g.level = observed
		g.levelSince = now
		g.lowSince = time.Time{}
		g.transitions++
		enteredCritical = observed == LevelCritical
	case observed < g.level:
		if g.lowSince.IsZero() {
			g.lowSince = now
		}
		if now.Sub(g.lowSince) >= g.config.DemoteAfter {
			g.level = observed
			g.levelSince = now
			g.lowSince = time.Time{}
			g.transitions++
		}
	default:
		// At the current level: the pressure has not been lower, so any accumulated
		// low-pressure time is void.
		g.lowSince = time.Time{}
	}
	current := g.level
	g.access.Unlock()

	if !enteredCritical {
		return current, false, nil
	}
	g.shedAccess.Lock()
	hooks := make([]func(), len(g.shedHooks))
	copy(hooks, g.shedHooks)
	g.shedAccess.Unlock()
	return current, true, hooks
}

// apply pushes the level's plan into the gate and runs shed hooks once per transition.
//
// The plan is only written when the level actually changed, because Acquire is on the dial path
// of every expensive connection and re-taking the gate's lock to store an unchanged plan would
// be paid for on every dial to save nothing.
func (g *Guard) apply(level Level) {
	// Fast path: the overwhelmingly common case is a dial at an unchanged level, and this check
	// must not take the policy lock. It is an atomic load instead, which is what keeps the hot
	// path to one mutex acquisition (observe) plus the gate's own.
	if Level(g.applied.Load()) == level {
		return
	}
	g.access.Lock()
	if Level(g.applied.Load()) == level {
		g.access.Unlock()
		return
	}
	g.applied.Store(uint32(level))
	g.access.Unlock()
	g.gate.setPlan(g.config.planFor(level))
	if level == LevelCritical {
		g.runShed()
	}
}

// runShed executes the hooks synchronously.
//
// Synchronous on purpose: the hooks are the same reclaim actions the oomkiller already runs
// from its own poll, which is already a sequential executor, and running them inline keeps the
// "one reclaim authority" property - a second goroutine per transition would be a second
// executor racing the first.
func (g *Guard) runShed() {
	g.shedAccess.Lock()
	hooks := make([]func(), len(g.shedHooks))
	copy(hooks, g.shedHooks)
	g.shedAccess.Unlock()
	for _, hook := range hooks {
		hook()
	}
}

// GuardStats is the observability surface: the level, how often it moved, and the gate's
// counters. It is what turns "the governor is installed" into "the governor refused 168
// creations and the peak in-flight was 32".
type GuardStats struct {
	Level       Level
	Transitions uint64
	Gate        Stats
}

// Stats reports a consistent snapshot: the level, how often it moved, and the gate's counters.
//
// # Why reading applies the level
//
// The reported limit has to describe the SAME state as the reported level. If Stats only read
// the stored level, a caller could be told "critical, limit 32" purely because no admission had
// happened since the pressure changed - a snapshot that contradicts itself and is exactly what a
// red-team report is written from. So Stats observes and applies, like Level: an observation can
// complete a transition and run the shed hooks for it. That is one behaviour with one rule, not a
// read-only path that quietly reports a stale plan.
func (g *Guard) Stats() GuardStats {
	if g == nil {
		return GuardStats{}
	}
	level, _, _ := g.observe()
	g.apply(level)
	g.access.Lock()
	transitions := g.transitions
	g.access.Unlock()
	return GuardStats{Level: level, Transitions: transitions, Gate: g.gate.Stats()}
}

// FromContext returns the guard registered for this process, or nil.
//
// The registration is done by the component that owns the pressure policy (service/oomkiller
// on an iOS Network Extension), which is also the component that can decide whether a guard
// should exist at all. Consumers therefore never decide "am I on iOS" themselves.
func FromContext(ctx context.Context) *Guard {
	return service.FromContext[*Guard](ctx)
}

// NewContext registers a guard in a context, for the owner and for tests that build a box by
// hand.
func NewContext(ctx context.Context, guard *Guard) context.Context {
	return service.ContextWith(ctx, guard)
}

// clockNow reads the guard's clock, defaulting to the wall clock when the field is unset.
//
// It exists so the dwell and the aging rule can be driven by a simulated clock in tests without
// every production read paying for an interface or a nil check that the compiler cannot fold.
func (g *Guard) clockNow() time.Time {
	if g.now == nil {
		return time.Now()
	}
	return g.now()
}
