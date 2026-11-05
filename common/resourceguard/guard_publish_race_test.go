package resourceguard

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Publishing a level is two steps - observe the state machine, then apply the plan - and the audit
// asked whether a caller descheduled between them can overwrite a newer transition. It could: the
// plan was chosen by a bare `applied != level` comparison, so a stale observation applied after a
// rise re-opened the gate under pressure, and a stale CRITICAL applied after a demote re-throttled a
// healthy process and ran the shed hooks again.
//
// These tests drive the interleaving by hand. `observe` and `apply` are the two halves, so a test
// can hold one caller's observation, advance the machine with a second caller, then resume the
// first - the exact ordering that a scheduler produces and that a stress test would only hit
// probabilistically.

// publishInterleave is the smallest deterministic reproduction: A observes ARMED and stops; the
// pressure rises to CRITICAL and B publishes it; A resumes with its stale ARMED observation.
func TestAStaleObservationCannotReopenTheGate(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	pressure := LevelArmed
	guard := NewGuard(DefaultNetworkExtensionConfig(), func() Level { return pressure })
	guard.useClock(clock.Now)

	armedLevel, armedVersion := guard.observe()
	require.Equal(t, LevelArmed, armedLevel)

	pressure = LevelCritical
	criticalLevel, criticalVersion := guard.observe()
	require.Equal(t, LevelCritical, criticalLevel)
	require.Greater(t, criticalVersion, armedVersion, "a rise must advance the transition sequence")
	guard.apply(criticalLevel, criticalVersion)

	criticalLimit := guard.gate.Stats().Limit
	require.Equal(t, DefaultNetworkExtensionConfig().planFor(LevelCritical).limit, criticalLimit)

	// A resumes. Its observation is older than what the gate already publishes.
	guard.apply(armedLevel, armedVersion)

	require.Equal(t, LevelCritical, guard.Stats().Level)
	require.Equal(t, criticalLimit, guard.gate.Stats().Limit,
		"a stale observation re-opened the gate while the guard reported critical pressure")
}

// The mirror image: a stale CRITICAL must not throttle a process that has already demoted, and must
// not run the shed hooks a second time. Re-running them is the reclaim loop this system exists to
// avoid.
func TestAStaleCriticalObservationCannotShedAgain(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	pressure := LevelCritical
	guard := NewGuard(DefaultNetworkExtensionConfig(), func() Level { return pressure })
	guard.useClock(clock.Now)

	var sheds int
	guard.RegisterShed(func() { sheds++ })

	criticalLevel, criticalVersion := guard.observe()
	guard.apply(criticalLevel, criticalVersion)
	require.Equal(t, 1, sheds, "the first entry into critical must shed once")

	// Demote normally: the dwell is measured from the first observation BELOW the current level, so
	// the pressure has to be seen low once before the clock can be advanced past it.
	pressure = LevelNormal
	stillCritical, _ := guard.observe()
	require.Equal(t, LevelCritical, stillCritical, "a demote must not be immediate")
	clock.Advance(DefaultNetworkExtensionConfig().DemoteAfter)
	normalLevel, normalVersion := guard.observe()
	require.Equal(t, LevelNormal, normalLevel)
	guard.apply(normalLevel, normalVersion)
	require.Equal(t, 1, sheds)
	normalLimit := guard.gate.Stats().Limit

	// The stale CRITICAL observation arrives after the demote.
	guard.apply(criticalLevel, criticalVersion)

	require.Equal(t, 1, sheds, "a stale critical observation re-ran the reclaim hooks")
	require.Equal(t, normalLimit, guard.gate.Stats().Limit,
		"a stale critical observation re-throttled a demoted guard")
	require.Equal(t, LevelNormal, guard.Stats().Level)
}

// A storm of observations and publications from many goroutines: whatever order they finish in, the
// gate's plan must be the plan of the level the guard reports, and the published version must never
// go backwards.
func TestConcurrentPublicationsKeepTheGateAndTheLevelInStep(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	var pressureAccess sync.Mutex
	pressure := LevelNormal
	readPressure := func() Level {
		pressureAccess.Lock()
		defer pressureAccess.Unlock()
		return pressure
	}
	setPressure := func(level Level) {
		pressureAccess.Lock()
		pressure = level
		pressureAccess.Unlock()
	}
	guard := NewGuard(DefaultNetworkExtensionConfig(), readPressure)
	guard.useClock(clock.Now)

	const (
		workers = 16
		rounds  = 200
	)
	levels := []Level{LevelNormal, LevelArmed, LevelCritical}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(offset int) {
			defer wg.Done()
			<-start
			for round := 0; round < rounds; round++ {
				setPressure(levels[(round+offset)%len(levels)])
				level, version := guard.observe()
				guard.apply(level, version)
			}
		}(worker)
	}
	close(start)
	wg.Wait()

	// Settle: every caller is gone, so observations under a known pressure publish the final state.
	// The dwell is measured from the first observation BELOW the level, so normal pressure has to be
	// seen once, then the clock advanced, then seen again - otherwise this assertion would be
	// testing the demote schedule rather than the publication order.
	setPressure(LevelNormal)
	guard.Level()
	clock.Advance(DefaultNetworkExtensionConfig().DemoteAfter)
	guard.Level()

	final := guard.Stats()
	require.Equal(t, LevelNormal, final.Level)
	require.Equal(t, DefaultNetworkExtensionConfig().planFor(LevelNormal).limit, final.Gate.Limit,
		"the gate's plan and the guard's level disagreed after the storm")
	require.Equal(t, uint64(0), guard.appliedVersion.Load()-uint64(final.Transitions),
		"the published version ran ahead of the state machine's sequence")
}

// The version gate must be an ordering, not a counter of writes: applying the same observation twice
// is idempotent, and applying an older one changes nothing at all.
func TestPublicationIsIdempotentAndOrdered(t *testing.T) {
	t.Parallel()
	clock := newFakeClock()
	pressure := LevelArmed
	guard := NewGuard(DefaultNetworkExtensionConfig(), func() Level { return pressure })
	guard.useClock(clock.Now)

	level, version := guard.observe()
	guard.apply(level, version)
	published := guard.appliedVersion.Load()
	limit := guard.gate.Stats().Limit

	guard.apply(level, version)
	require.Equal(t, published, guard.appliedVersion.Load())
	require.Equal(t, limit, guard.gate.Stats().Limit)

	guard.apply(LevelNormal, version-1)
	require.Equal(t, published, guard.appliedVersion.Load(), "an older version was published")
	require.Equal(t, limit, guard.gate.Stats().Limit)

	require.Equal(t, LevelArmed, guard.Stats().Level)
	_ = time.Now
}
