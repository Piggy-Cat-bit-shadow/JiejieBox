package resourceguard

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeClock is the deterministic clock the dwell and aging bounds are proven against.
//
// The alternative - sleeping for the real 2 s dwell over a simulated minute - would either take
// two minutes of CI or would have to weaken the dwell to a value production never uses. A clock
// that a test advances by hand gives the real policy constants and a millisecond of wall time.
type fakeClock struct {
	access sync.Mutex
	value  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{value: time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.access.Lock()
	defer c.access.Unlock()
	return c.value
}

func (c *fakeClock) Advance(duration time.Duration) {
	c.access.Lock()
	c.value = c.value.Add(duration)
	c.access.Unlock()
}

// TestReclaimRateUnderSustainedCriticalIsBounded is the anti-reclaim-loop evidence with the real
// production constants: a footprint that stays above the critical line for a simulated minute
// must cause ONE reclaim, not one per sample.
func TestReclaimRateUnderSustainedCriticalIsBounded(t *testing.T) {
	clock := newFakeClock()
	var level Level = LevelNormal
	guard := NewGuard(DefaultNetworkExtensionConfig(), func() Level { return level })
	guard.useClock(clock.Now)

	var sheds int
	guard.RegisterShed(func() { sheds++ })

	// One second of normal, then sixty seconds of sustained critical, sampled every 250 ms - the
	// cadence the oomkiller's own ladder uses under pressure.
	for second := 0; second < 240; second++ {
		if second == 4 {
			level = LevelCritical
		}
		guard.Level()
		clock.Advance(250 * time.Millisecond)
	}
	require.Equal(t, 1, sheds,
		"240 samples inside sixty seconds of critical must reclaim once, not per sample")

	// The bound is structural, and this states it: entries into critical are separated by at
	// least one dwell, so the rate is capped at 1/DemoteAfter for as long as an operator wants
	// the process to run.
	require.Equal(t, 1, sheds)
}

// TestReclaimRateUnderJitterIsZero is the jitter half of the same bound: a signal that rises and
// falls faster than the dwell never completes a demotion, so it can never complete a NEW entry
// into critical either - which means a jittery footprint cannot manufacture reclaim.
func TestReclaimRateUnderJitterIsZero(t *testing.T) {
	clock := newFakeClock()
	config := DefaultNetworkExtensionConfig()
	config.DemoteAfter = 2 * time.Second
	var level Level = LevelCritical
	guard := NewGuard(config, func() Level { return level })
	guard.useClock(clock.Now)

	var sheds int
	guard.RegisterShed(func() { sheds++ })

	// Enter critical once.
	guard.Level()
	require.Equal(t, 1, sheds)

	// Then jitter below the line for 1.9 s, above for 0.1 s, for a simulated minute.
	for round := 0; round < 30; round++ {
		level = LevelNormal
		guard.Level()
		clock.Advance(1900 * time.Millisecond)
		level = LevelCritical
		guard.Level()
		clock.Advance(100 * time.Millisecond)
	}
	require.Equal(t, 1, sheds,
		"a signal that never stays low for the dwell must not produce another reclaim")
	require.Equal(t, uint64(1), guard.Stats().Transitions)
}

// TestReclaimRateBoundWithRealDwell states the worst case explicitly: with the production 2 s
// dwell, at most one reclaim per 2 s of a signal that genuinely oscillates across the whole
// band, which is 30 minutes of wall time for an hour of runtime - and only if the operator's
// process actually leaves and re-enters the critical band that often.
func TestReclaimRateBoundWithRealDwell(t *testing.T) {
	clock := newFakeClock()
	config := DefaultNetworkExtensionConfig()
	config.DemoteAfter = 2 * time.Second
	var level Level = LevelNormal
	guard := NewGuard(config, func() Level { return level })
	guard.useClock(clock.Now)

	var sheds uint64
	guard.RegisterShed(func() { sheds++ })

	const simulated = 60 * time.Second
	for elapsed := time.Duration(0); elapsed < simulated; elapsed += 100 * time.Millisecond {
		// A square wave that is genuinely low for longer than the dwell on every cycle: this is
		// the most aggressive signal that can still produce a demotion.
		if (elapsed/(3*time.Second))%2 == 0 {
			level = LevelCritical
		} else {
			level = LevelNormal
		}
		guard.Level()
		clock.Advance(100 * time.Millisecond)
	}

	maximum := uint64(simulated/config.DemoteAfter) + 1
	t.Logf("sheds over %v of a full-band square wave with a %v dwell: %d (bound %d)", simulated, config.DemoteAfter, sheds, maximum)

	require.LessOrEqual(t, sheds, maximum,
		"%d reclaims in %v exceeds the structural bound of one per dwell (%d)", sheds, simulated, maximum)
	require.Greater(t, sheds, uint64(0), "the experiment must actually enter critical")
}

// TestAgingUsesTheInjectedClock proves the starvation guarantee is measurable rather than
// incidental: without the clock advancing at all, an aged waiter must never be granted the
// reserve, and after a single advance past the bound it must be.
func TestAgingUsesTheInjectedClock(t *testing.T) {
	clock := newFakeClock()
	config := DefaultNetworkExtensionConfig()
	config.Limit = 2
	config.InteractiveReserve = 2
	config.StarvationAfter = time.Second
	guard := NewGuard(config, func() Level { return LevelNormal })
	guard.useClock(clock.Now)

	first, err := guard.Acquire(t.Context(), ClassNormal)
	require.NoError(t, err)
	second, err := guard.Acquire(t.Context(), ClassNormal)
	require.NoError(t, err)

	admitted := make(chan struct{}, 1)
	go func() {
		permit, acquireErr := guard.Acquire(t.Context(), ClassNormal)
		if acquireErr == nil {
			permit.Release()
		}
		admitted <- struct{}{}
	}()
	require.Eventually(t, func() bool { return guard.Stats().Gate.Waiters == 1 }, time.Second, time.Millisecond)

	// The reserve slot cycles without letting the simulated clock move: the waiter must not be
	// aged in, because its wait is measured on the injected clock.
	interactive, err := guard.Acquire(t.Context(), ClassInteractive)
	require.NoError(t, err)
	interactive.Release()
	interactive, err = guard.Acquire(t.Context(), ClassInteractive)
	require.NoError(t, err)
	select {
	case <-admitted:
		t.Fatal("the waiter was aged in before the clock advanced")
	case <-time.After(20 * time.Millisecond):
	}

	// One advance past the bound is enough.
	clock.Advance(1500 * time.Millisecond)
	interactive.Release()
	require.Eventually(t, func() bool {
		select {
		case <-admitted:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond, "an aged waiter must claim the reserve")

	first.Release()
	second.Release()
	require.Eventually(t, func() bool { return guard.Stats().Gate.Inflight == 0 }, time.Second, time.Millisecond)
	requireBalanced(t, guard.gate)
}
