package resourceguard

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestAttackCancelledCallerNeverTakesASlot pins the cancel-before-acquire rule: a context that is
// already dead must not spend a permit on a dial that cannot succeed, because under pressure the
// permit is the scarce resource the whole gate exists to allocate.
func TestAttackCancelledCallerNeverTakesASlot(t *testing.T) {
	gate := NewGate(plan{limit: 2, queue: 4, wait: 50 * time.Millisecond}, 0)

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	permit, err := gate.Acquire(cancelled, ClassNormal)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, permit)

	expired, expire := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer expire()
	permit, err = gate.Acquire(expired, ClassNormal)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Nil(t, permit)

	stats := gate.Stats()
	require.Zero(t, stats.Inflight, "a dead caller must not hold a slot")
	require.Zero(t, stats.Waiters)
	require.Equal(t, uint64(2), stats.Canceled)
	require.Equal(t, uint64(2), stats.Requested)
	requireBalanced(t, gate)
}

// TestAttackMixedOutcomesKeepTheAccountingExact is the admission matrix with the outcomes mixed:
// success, dial failure, cancellation mid-wait, and wait timeout in one burst. The invariant that
// must survive is that every caller is counted exactly once and the slot count returns to zero.
func TestAttackMixedOutcomesKeepTheAccountingExact(t *testing.T) {
	for _, burst := range []int{0, 1, 31, 32, 33, 200, 500, 1000} {
		burst := burst
		t.Run("burst", func(t *testing.T) {
			config := DefaultNetworkExtensionConfig()
			config.Limit = 32
			config.Queue = 256
			config.Wait = 20 * time.Millisecond
			guard := NewGuard(config, func() Level { return LevelNormal })

			var succeeded atomic.Int64
			var failed atomic.Int64
			var wg sync.WaitGroup
			for index := 0; index < burst; index++ {
				index := index
				wg.Add(1)
				go func() {
					defer wg.Done()
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					switch index % 4 {
					case 0:
						// Cancelled at a randomised point while queued.
						go func() {
							time.Sleep(time.Duration(index%5) * time.Millisecond)
							cancel()
						}()
					case 1:
						// A dial that fails immediately after admission.
						permit, err := guard.Acquire(ctx, ClassNormal)
						if err != nil {
							failed.Add(1)
							return
						}
						permit.Release()
						failed.Add(1)
						return
					}
					permit, err := guard.Acquire(ctx, ClassNormal)
					if err != nil {
						failed.Add(1)
						return
					}
					time.Sleep(time.Duration(index%3) * time.Millisecond)
					permit.Release()
					succeeded.Add(1)
				}()
			}
			wg.Wait()

			stats := guard.Stats().Gate
			require.Equal(t, int64(burst), succeeded.Load()+failed.Load(),
				"every caller must land in exactly one outcome bucket")
			require.Zero(t, stats.Inflight, "no slot may survive the burst")
			require.Zero(t, stats.Waiters, "no waiter may survive the burst")
			require.Equal(t, int(stats.Admitted-stats.Released), stats.Inflight,
				"admitted=%d released=%d at burst %d", stats.Admitted, stats.Released, burst)
			require.Equal(t, uint64(burst), stats.Requested)
			require.Equal(t, stats.Admitted+stats.Refused+stats.Canceled+stats.TimedOut, stats.Requested,
				"the outcome buckets must partition the requests")
		})
	}
}

// TestAttackShedIsEdgeTriggeredFromTheMetricsPath proves the shed hooks are not reachable through
// a polling reader either: whatever calls Stats, one entry into critical is one shed.
func TestAttackShedIsEdgeTriggeredFromTheMetricsPath(t *testing.T) {
	var level atomic.Uint32
	level.Store(uint32(LevelCritical))
	guard := NewGuard(DefaultNetworkExtensionConfig(), func() Level { return Level(uint8(level.Load())) })
	var sheds atomic.Int64
	guard.RegisterShed(func() { sheds.Add(1) })

	for index := 0; index < 1000; index++ {
		_ = guard.Stats()
	}
	require.Equal(t, int64(1), sheds.Load(), "polling the metrics must not re-run reclaim")
}

// TestAttackPinnedGateRefusesFastRatherThanQueueing is the bounded consequence of a peer that
// never completes its handshake: the permits are held by the stalled dials, and the gate must
// degrade by refusing quickly - not by growing a queue of callers waiting for a slot that no
// release is coming for.
func TestAttackPinnedGateRefusesFastRatherThanQueueing(t *testing.T) {
	config := DefaultNetworkExtensionConfig()
	guard := NewGuard(config, func() Level { return LevelCritical })

	stalled := make([]*Permit, 0, 8)
	for index := 0; index < 8; index++ {
		permit, err := guard.Acquire(context.Background(), ClassNormal)
		require.NoError(t, err)
		stalled = append(stalled, permit)
	}

	// Every subsequent caller must fail inside the critical wait budget, and the queue must stay
	// bounded instead of accumulating. At critical the queue holds 32, so most callers are
	// refused immediately and the rest time out; nobody is admitted while the gate is pinned.
	started := time.Now()
	var failed atomic.Int64
	var wg sync.WaitGroup
	for index := 0; index < 200; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			permit, err := guard.Acquire(context.Background(), ClassNormal)
			if err != nil {
				failed.Add(1)
				return
			}
			permit.Release()
		}()
	}
	wg.Wait()
	elapsed := time.Since(started)

	require.Equal(t, int64(200), failed.Load(), "a pinned gate must refuse or time out every caller")
	require.Less(t, elapsed, 3*time.Second, "refusals must be quick, not parked")
	stats := guard.Stats().Gate
	require.LessOrEqual(t, stats.MaxInflight, 8, "the cap must hold while pinned")
	require.Zero(t, stats.Waiters)
	require.Equal(t, 8, stats.Inflight)
	require.Equal(t, uint64(200), stats.Refused+stats.TimedOut,
		"the queue bound decides refusal and the wait bound decides timeout")

	for _, permit := range stalled {
		permit.Release()
	}
	require.Zero(t, guard.Stats().Gate.Inflight)
	requireBalanced(t, guard.gate)
}

// TestAttackContextErrorIsNotSwallowed keeps the cancellation contract honest: a caller whose
// context died while queued must see its own error, not a generic timeout.
func TestAttackContextErrorIsNotSwallowed(t *testing.T) {
	gate := NewGate(plan{limit: 1, queue: 4, wait: time.Second}, 0)
	held, err := gate.Acquire(context.Background(), ClassNormal)
	require.NoError(t, err)
	defer held.Release()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, acquireErr := gate.Acquire(ctx, ClassNormal)
		done <- acquireErr
	}()
	require.Eventually(t, func() bool { return gate.Stats().Waiters == 1 }, time.Second, time.Millisecond)
	cancel()

	acquireErr := <-done
	require.True(t, errors.Is(acquireErr, context.Canceled), "expected a cancellation, got %v", acquireErr)
}
