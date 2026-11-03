package resourceguard

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestGuardRepeatedPressureCycles walks the same pressure cycle repeatedly.
//
// A single transition proves the policy; a repeated cycle proves it RECOVERS. The failure this
// guards against is the one the reference implementations both hit: a stay threshold that is
// never re-crossed, which turns protection into a one-shot mechanism that stops re-arming after
// the first episode.
func TestGuardRepeatedPressureCycles(t *testing.T) {
	var level atomic.Uint32
	config := DefaultNetworkExtensionConfig()
	config.DemoteAfter = 5 * time.Millisecond
	guard := NewGuard(config, func() Level { return Level(level.Load()) })

	var sheds atomic.Int64
	guard.RegisterShed(func() { sheds.Add(1) })

	const cycles = 5
	for cycle := 0; cycle < cycles; cycle++ {
		require.Equal(t, LevelNormal, guard.Level(), "cycle %d must start normal", cycle)
		require.Equal(t, 32, guard.Stats().Gate.Limit, "cycle %d must reopen the limit", cycle)

		level.Store(uint32(LevelArmed))
		require.Equal(t, LevelArmed, guard.Level(), "cycle %d must arm immediately", cycle)
		require.Equal(t, 16, guard.Stats().Gate.Limit)

		level.Store(uint32(LevelCritical))
		require.Equal(t, LevelCritical, guard.Level(), "cycle %d must trigger immediately", cycle)
		require.Equal(t, 8, guard.Stats().Gate.Limit)

		level.Store(uint32(LevelNormal))
		require.Eventually(t, func() bool { return guard.Level() == LevelNormal }, time.Second, time.Millisecond,
			"cycle %d must recover", cycle)
	}

	require.Equal(t, int64(cycles), sheds.Load(), "each entry into critical sheds exactly once")
	require.Equal(t, uint64(cycles*3), guard.Stats().Transitions)
	require.Zero(t, guard.Stats().Gate.Inflight)
}

// FuzzGateSlotAccounting drives the gate with arbitrary acquire/release/cancel sequences and
// asserts the one invariant every path shares: inflight == admitted - released.
//
// The seed corpus runs as an ordinary test, so the invariant is checked on every `go test`; the
// fuzzer explores the interleavings that a hand-written test would have to guess.
func FuzzGateSlotAccounting(f *testing.F) {
	f.Add([]byte{0, 1, 0, 0, 1, 2, 0, 1, 1, 1})
	f.Add([]byte{1, 1, 1, 0, 0, 0, 2, 2, 2})
	f.Add([]byte{0, 0, 0, 0, 0, 0, 0, 0})
	f.Fuzz(func(t *testing.T, operations []byte) {
		gate := NewGate(plan{limit: 3, queue: 4, wait: 2 * time.Millisecond}, 1)
		var held []*Permit
		for _, operation := range operations {
			switch operation % 3 {
			case 0, 1:
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Millisecond)
				permit, err := gate.Acquire(ctx, Class(operation%2))
				cancel()
				if err == nil && permit != nil {
					held = append(held, permit)
				}
			default:
				if len(held) == 0 {
					continue
				}
				index := int(operation) % len(held)
				held[index].Release()
				held = append(held[:index], held[index+1:]...)
			}
		}
		for _, permit := range held {
			permit.Release()
		}
		stats := gate.Stats()
		require.Equal(t, int(stats.Admitted-stats.Released), stats.Inflight,
			"slot accounting drifted for %v", operations)
		require.Zero(t, stats.Inflight)
	})
}
