package resourceguard

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestNilGuardAdmitsEverything pins the zero-cost default: every platform except an iOS Network
// Extension runs with no guard, and protocol code must not need a branch for that.
func TestNilGuardAdmitsEverything(t *testing.T) {
	var guard *Guard
	permit, err := guard.Acquire(context.Background(), ClassNormal)
	require.NoError(t, err)
	require.Nil(t, permit)
	require.NotPanics(t, permit.Release)
	require.Equal(t, LevelNormal, guard.Level())
	require.Equal(t, GuardStats{}, guard.Stats())
	require.NotPanics(t, func() { guard.RegisterShed(func() {}) })
}

func TestGuardLevelRisesImmediatelyAndFallsAfterDwell(t *testing.T) {
	var level atomic.Uint32
	config := DefaultNetworkExtensionConfig()
	config.DemoteAfter = 60 * time.Millisecond
	guard := NewGuard(config, func() Level { return Level(level.Load()) })

	require.Equal(t, LevelNormal, guard.Level())
	require.Equal(t, 32, guard.Stats().Gate.Limit)

	level.Store(uint32(LevelArmed))
	require.Equal(t, LevelArmed, guard.Level())
	require.Equal(t, 16, guard.Stats().Gate.Limit)
	require.Equal(t, 128, guard.Stats().Gate.QueueLimit)
	require.Equal(t, 2500*time.Millisecond, guard.Stats().Gate.WaitBudget)

	level.Store(uint32(LevelCritical))
	require.Equal(t, LevelCritical, guard.Level())
	require.Equal(t, 8, guard.Stats().Gate.Limit)

	// A fall is delayed: one quiet read must not re-open admission into a burst that is still
	// arriving.
	level.Store(uint32(LevelNormal))
	require.Equal(t, LevelCritical, guard.Level())
	require.Eventually(t, func() bool { return guard.Level() == LevelNormal }, time.Second, 5*time.Millisecond)
	require.Equal(t, 32, guard.Stats().Gate.Limit)
	require.Equal(t, uint64(3), guard.Stats().Transitions)
}

func TestGuardShedRunsOnCriticalEntryOnly(t *testing.T) {
	var level atomic.Uint32
	config := DefaultNetworkExtensionConfig()
	config.DemoteAfter = 10 * time.Millisecond
	guard := NewGuard(config, func() Level { return Level(level.Load()) })

	var sheds atomic.Int64
	guard.RegisterShed(func() { sheds.Add(1) })

	level.Store(uint32(LevelArmed))
	guard.Level()
	require.Zero(t, sheds.Load(), "armed is not a shed transition")

	level.Store(uint32(LevelCritical))
	guard.Level()
	guard.Level()
	guard.Level()
	require.Equal(t, int64(1), sheds.Load(), "one shed per entry, not one per observation")

	// Returning to normal and re-entering critical is a NEW entry.
	level.Store(uint32(LevelNormal))
	require.Eventually(t, func() bool { return guard.Level() == LevelNormal }, time.Second, 5*time.Millisecond)
	level.Store(uint32(LevelCritical))
	guard.Level()
	require.Equal(t, int64(2), sheds.Load())
}

func TestGuardShedFiresOnceWhenBuiltInsideCritical(t *testing.T) {
	level := LevelCritical
	guard := NewGuard(DefaultNetworkExtensionConfig(), func() Level { return level })
	var sheds atomic.Int64
	guard.RegisterShed(func() { sheds.Add(1) })
	// The first observation is a transition INTO critical (the guard starts normal), so it
	// sheds once; every later observation at the same level must not shed again.
	guard.Level()
	require.Equal(t, int64(1), sheds.Load())
	guard.Level()
	guard.Level()
	require.Equal(t, int64(1), sheds.Load())
}

// TestGuardBurstIsBoundedButAbsorbed is the behaviour the whole package exists for, at the
// scale the iOS diagnostic measured: 200 expensive creations arriving at once.
//
// It asserts three separate things, because each can fail alone: the concurrency ceiling holds,
// the queue ABSORBS the burst rather than refusing it, and the counters account for every
// caller with no slot left behind.
func TestGuardBurstIsBoundedButAbsorbed(t *testing.T) {
	config := DefaultNetworkExtensionConfig()
	config.Limit = 8
	config.Wait = 2 * time.Second
	guard := NewGuard(config, func() Level { return LevelNormal })

	const burst = 200
	var inFlight atomic.Int64
	var peak atomic.Int64
	var admitted atomic.Int64
	var refused atomic.Int64

	var wg sync.WaitGroup
	for index := 0; index < burst; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			permit, err := guard.Acquire(context.Background(), ClassNormal)
			if err != nil {
				refused.Add(1)
				return
			}
			admitted.Add(1)
			current := inFlight.Add(1)
			for {
				observed := peak.Load()
				if current <= observed || peak.CompareAndSwap(observed, current) {
					break
				}
			}
			// The expensive work the permit is held for: a session creation. 2 ms stands in
			// for it, which is short enough to keep the test fast and long enough that the
			// ceiling is actually exercised.
			time.Sleep(2 * time.Millisecond)
			inFlight.Add(-1)
			permit.Release()
		}()
	}
	wg.Wait()

	require.LessOrEqual(t, peak.Load(), int64(8), "the admission ceiling must hold under a burst")
	require.Equal(t, int64(burst), admitted.Load()+refused.Load())
	require.Zero(t, refused.Load(), "a 200-flow burst is absorbed by the queue, not refused")
	stats := guard.Stats().Gate
	require.Zero(t, stats.Inflight)
	require.Zero(t, stats.Waiters)
	requireBalanced(t, guard.gate)
}

// TestGuardCriticalRefusesTheExcess proves the level actually changes behaviour: the same burst
// that is fully absorbed at normal is largely refused at critical, where the queue is nearly
// closed and the wait is short.
func TestGuardCriticalRefusesTheExcess(t *testing.T) {
	config := DefaultNetworkExtensionConfig()
	guard := NewGuard(config, func() Level { return LevelCritical })

	const burst = 40
	var admitted atomic.Int64
	var wg sync.WaitGroup
	var permits sync.Map
	for index := 0; index < burst; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			permit, err := guard.Acquire(context.Background(), ClassNormal)
			if err != nil {
				return
			}
			admitted.Add(1)
			permits.Store(permit, struct{}{})
		}()
	}
	wg.Wait()

	require.Equal(t, int64(8), admitted.Load(), "critical admits only the critical limit")
	stats := guard.Stats().Gate
	require.Equal(t, 8, stats.Inflight)
	require.Equal(t, int64(burst-8), int64(stats.Refused+stats.TimedOut),
		"every caller above the limit is either refused or timed out, never admitted")
	require.Equal(t, 32, stats.QueueLimit)
	require.Equal(t, 100*time.Millisecond, stats.WaitBudget)

	permits.Range(func(key, _ any) bool {
		key.(*Permit).Release()
		return true
	})
	require.Zero(t, guard.Stats().Gate.Inflight)
	requireBalanced(t, guard.gate)
}

func TestGuardStatsSnapshotIsConsistent(t *testing.T) {
	guard := NewGuard(DefaultNetworkExtensionConfig(), func() Level { return LevelArmed })
	permit, err := guard.Acquire(context.Background(), ClassNormal)
	require.NoError(t, err)
	stats := guard.Stats()
	require.Equal(t, LevelArmed, stats.Level)
	require.Equal(t, 1, stats.Gate.Inflight)
	require.Equal(t, 16, stats.Gate.Limit)
	require.Equal(t, uint64(1), stats.Gate.Admitted)
	permit.Release()
	require.Zero(t, guard.Stats().Gate.Inflight)
}
