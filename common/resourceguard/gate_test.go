package resourceguard

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// requireBalanced asserts the gate's slot accounting invariant.
//
// Every admission takes a slot and every way a slot can be given back - Release, a grant that
// lost its waiter, a grant the waiter withdrew from - goes through exactly one decrement, so
// inflight must always equal admitted minus released. A leak in the grant/withdraw race shows
// up here as drift, and drift is the failure mode that would silently shrink the gate for the
// rest of the process lifetime.
func requireBalanced(t *testing.T, gate *Gate) {
	t.Helper()
	stats := gate.Stats()
	require.Equal(t, int(stats.Admitted-stats.Released), stats.Inflight,
		"slot accounting drifted: admitted=%d released=%d inflight=%d",
		stats.Admitted, stats.Released, stats.Inflight)
}

func newTestGate(limit int, queue int, wait time.Duration) *Gate {
	return NewGate(plan{limit: limit, queue: queue, wait: wait}, 0)
}

func TestGateAdmitsUpToLimit(t *testing.T) {
	gate := newTestGate(3, 8, time.Second)
	var permits []*Permit
	for index := 0; index < 3; index++ {
		permit, err := gate.Acquire(context.Background(), ClassNormal)
		require.NoError(t, err)
		permits = append(permits, permit)
	}
	stats := gate.Stats()
	require.Equal(t, 3, stats.Inflight)
	require.Equal(t, 3, stats.MaxInflight)
	require.Zero(t, stats.Waiters)

	for _, permit := range permits {
		permit.Release()
	}
	require.Zero(t, gate.Stats().Inflight)
	requireBalanced(t, gate)
}

func TestGateRefusesWhenQueueIsFull(t *testing.T) {
	gate := newTestGate(1, 1, time.Second)
	held, err := gate.Acquire(context.Background(), ClassNormal)
	require.NoError(t, err)

	waiting := make(chan *Permit, 1)
	go func() {
		permit, _ := gate.Acquire(context.Background(), ClassNormal)
		waiting <- permit
	}()
	require.Eventually(t, func() bool { return gate.Stats().Waiters == 1 }, time.Second, time.Millisecond)

	// The queue holds one. The third caller must be refused WITHOUT waiting: measure that the
	// call returns immediately rather than after the wait budget.
	started := time.Now()
	permit, err := gate.Acquire(context.Background(), ClassNormal)
	require.ErrorIs(t, err, ErrQueueFull)
	require.Nil(t, permit)
	require.Less(t, time.Since(started), 100*time.Millisecond)

	held.Release()
	queued := <-waiting
	require.NotNil(t, queued)
	queued.Release()
	requireBalanced(t, gate)
}

func TestGateTimedOutWaiterIsNotGrantedLater(t *testing.T) {
	gate := newTestGate(1, 4, 20*time.Millisecond)
	held, err := gate.Acquire(context.Background(), ClassNormal)
	require.NoError(t, err)

	permit, err := gate.Acquire(context.Background(), ClassNormal)
	require.ErrorIs(t, err, ErrWaitTimeout)
	require.Nil(t, permit)
	require.Equal(t, uint64(1), gate.Stats().TimedOut)

	// Releasing after the timeout must not hand the slot to a waiter that already left.
	held.Release()
	stats := gate.Stats()
	require.Zero(t, stats.Inflight)
	require.Zero(t, stats.Waiters)
	requireBalanced(t, gate)
}

func TestGateCancellationWithdrawsWaiter(t *testing.T) {
	gate := newTestGate(1, 4, time.Minute)
	held, err := gate.Acquire(context.Background(), ClassNormal)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := gate.Acquire(ctx, ClassNormal)
		done <- err
	}()
	require.Eventually(t, func() bool { return gate.Stats().Waiters == 1 }, time.Second, time.Millisecond)
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	require.Zero(t, gate.Stats().Waiters)
	require.Equal(t, uint64(1), gate.Stats().Canceled)

	held.Release()
	requireBalanced(t, gate)
}

// TestGateFairnessUnderChurn is the release/timeout race at burst scale.
//
// It is the regression for the slot leak: a waiter that is granted at the same moment its wait
// expires must hand the slot on. Without that handoff the gate loses one slot per race, and at
// 200 flows per second it degrades to a closed gate while claiming to be healthy.
func TestGateFairnessUnderChurn(t *testing.T) {
	gate := newTestGate(2, 64, time.Millisecond)

	var admitted atomic.Int64
	var failed atomic.Int64
	var wg sync.WaitGroup
	for index := 0; index < 200; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			permit, err := gate.Acquire(context.Background(), ClassNormal)
			if err != nil {
				failed.Add(1)
				return
			}
			admitted.Add(1)
			permit.Release()
		}()
	}
	wg.Wait()

	require.Equal(t, int64(200), admitted.Load()+failed.Load())
	require.Zero(t, gate.Stats().Inflight)
	require.Zero(t, gate.Stats().Waiters)
	requireBalanced(t, gate)
}

func TestGateReleaseWakesOldestWaiter(t *testing.T) {
	gate := newTestGate(1, 4, time.Minute)
	held, err := gate.Acquire(context.Background(), ClassNormal)
	require.NoError(t, err)

	order := make(chan int, 2)
	for index := 0; index < 2; index++ {
		index := index
		go func() {
			permit, err := gate.Acquire(context.Background(), ClassNormal)
			if err != nil {
				order <- -1
				return
			}
			order <- index
			permit.Release()
		}()
		// Space the arrivals so FIFO is unambiguous.
		require.Eventually(t, func() bool { return gate.Stats().Waiters == index+1 }, time.Second, time.Millisecond)
	}

	held.Release()
	require.Equal(t, 0, <-order, "the oldest waiter must be admitted first")
	require.Eventually(t, func() bool { return gate.Stats().Waiters == 0 }, time.Second, time.Millisecond)
	require.Equal(t, 1, <-order)
	requireBalanced(t, gate)
}

func TestGateInteractiveReserve(t *testing.T) {
	gate := NewGate(plan{limit: 2, queue: 4, wait: time.Second}, 1)

	normalOne, err := gate.Acquire(context.Background(), ClassNormal)
	require.NoError(t, err)
	normalTwo, err := gate.Acquire(context.Background(), ClassNormal)
	require.NoError(t, err)

	// A third NORMAL slot does not exist...
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = gate.Acquire(ctx, ClassNormal)
	require.Error(t, err)

	// ...but an interactive flow may use the reserve, even while the queue is occupied.
	interactive, err := gate.Acquire(context.Background(), ClassInteractive)
	require.NoError(t, err)
	require.Equal(t, 3, gate.Stats().Inflight)

	interactive.Release()
	normalOne.Release()
	normalTwo.Release()
	requireBalanced(t, gate)
}

func TestGateShrinkDoesNotRevokeInflight(t *testing.T) {
	gate := newTestGate(4, 8, time.Second)
	var permits []*Permit
	for index := 0; index < 4; index++ {
		permit, err := gate.Acquire(context.Background(), ClassNormal)
		require.NoError(t, err)
		permits = append(permits, permit)
	}
	gate.setPlan(plan{limit: 1, queue: 8, wait: time.Second})
	require.Equal(t, 4, gate.Stats().Inflight, "in-flight work keeps its permit across a shrink")

	for _, permit := range permits {
		permit.Release()
	}
	require.Zero(t, gate.Stats().Inflight)
	requireBalanced(t, gate)
}

func TestPermitNilSafety(t *testing.T) {
	var permit *Permit
	require.NotPanics(t, permit.Release)
	require.Zero(t, permit.Wait())
}
