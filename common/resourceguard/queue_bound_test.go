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

// The queue bound must be enforced, not merely documented.
//
// # Why this needs its own test
//
// The gate's queue is its memory bound: each parked caller is a waiter struct, a channel and a
// timer. A mutation that makes the bound ineffective (an always-false comparison) leaves every
// OTHER assertion in the package intact - the callers still time out, the counters still partition,
// the inflight count still returns to zero - because waiting an extra five seconds looks like a slow
// admission. These tests observe the only thing that distinguishes the two: how many callers are
// allowed to be parked at once, and whether a cancelled one actually leaves.

// TestQueueBoundRefusesBeyondItsLimit pins the bound from both sides: exactly queue callers wait,
// and every caller past it is refused in constant time instead of being parked.
func TestQueueBoundRefusesBeyondItsLimit(t *testing.T) {
	gate := NewGate(plan{limit: 1, queue: 2, wait: time.Second}, 0)
	held, err := gate.Acquire(context.Background(), ClassNormal)
	require.NoError(t, err)

	const callers = 10
	var refused atomic.Int64
	var admitted atomic.Int64
	start := make(chan struct{})
	var wg sync.WaitGroup
	for index := 0; index < callers; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			permit, acquireErr := gate.Acquire(context.Background(), ClassNormal)
			if acquireErr != nil {
				if errors.Is(acquireErr, ErrQueueFull) {
					refused.Add(1)
				}
				return
			}
			admitted.Add(1)
			permit.Release()
		}()
	}
	close(start)

	require.Eventually(t, func() bool {
		stats := gate.Stats()
		return stats.Waiters == 2 && stats.Refused == callers-2
	}, 2*time.Second, time.Millisecond,
		"the queue admitted more than its bound: %+v", gate.Stats())

	held.Release()
	wg.Wait()

	require.EqualValues(t, callers-2, refused.Load(), "callers beyond the bound must be refused")
	require.EqualValues(t, 2, admitted.Load(), "the queued callers must be admitted once slots free")
	require.Zero(t, gate.Stats().Inflight)
	require.Zero(t, gate.Stats().Waiters)
}

// TestQueuedCancellationLeavesNoWaiter pins the queue's other ending: a waiter that gives up must
// leave the queue and return the slot accounting, or a cancellation storm becomes a permanent leak
// of queue capacity.
func TestQueuedCancellationLeavesNoWaiter(t *testing.T) {
	gate := NewGate(plan{limit: 1, queue: 4, wait: time.Second}, 0)
	held, err := gate.Acquire(context.Background(), ClassNormal)
	require.NoError(t, err)

	waiterCtx, cancelWaiter := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, acquireErr := gate.Acquire(waiterCtx, ClassNormal)
		done <- acquireErr
	}()
	require.Eventually(t, func() bool { return gate.Stats().Waiters == 1 }, time.Second, time.Millisecond)

	cancelWaiter()
	select {
	case acquireErr := <-done:
		require.ErrorIs(t, acquireErr, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("a cancelled waiter was not removed from the queue")
	}

	stats := gate.Stats()
	require.Zero(t, stats.Waiters, "the cancelled waiter stayed in the queue")
	require.EqualValues(t, 1, stats.Canceled)
	require.EqualValues(t, 1, stats.Inflight, "only the held permit is in flight")

	// The queue capacity is intact: a new caller can still wait.
	held.Release()
	nextCtx, cancelNext := context.WithTimeout(context.Background(), time.Second)
	defer cancelNext()
	permit, err := gate.Acquire(nextCtx, ClassNormal)
	require.NoError(t, err)
	permit.Release()
}
