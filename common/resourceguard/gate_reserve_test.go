package resourceguard

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestGateReserveIsNotConsumedByNormalWaiters is the regression for a reserve that only looked
// like a reserve.
//
// A slot that frees while the gate is AT the limit must not be handed to a queued NORMAL caller:
// doing so moves the gate to limit+1 for background work, and as many such handoffs as the
// reserve is wide exhaust the interactive headroom entirely - by exactly the flood the reserve
// exists to survive.
func TestGateReserveIsNotConsumedByNormalWaiters(t *testing.T) {
	gate := NewGate(plan{limit: 2, queue: 8, wait: 500 * time.Millisecond}, 1)

	first, err := gate.Acquire(context.Background(), ClassNormal)
	require.NoError(t, err)
	second, err := gate.Acquire(context.Background(), ClassNormal)
	require.NoError(t, err)
	require.Equal(t, 2, gate.Stats().Inflight)

	// A normal caller queues behind the limit.
	normalResult := make(chan error, 1)
	go func() {
		permit, acquireErr := gate.Acquire(context.Background(), ClassNormal)
		if permit != nil {
			permit.Release()
		}
		normalResult <- acquireErr
	}()
	require.Eventually(t, func() bool { return gate.Stats().Waiters == 1 }, time.Second, time.Millisecond)

	// An interactive caller takes the reserve and is admitted immediately.
	interactive, err := gate.Acquire(context.Background(), ClassInteractive)
	require.NoError(t, err)
	require.Equal(t, 3, gate.Stats().Inflight, "the reserve is what admits the third slot")

	// Releasing a normal permit returns the gate to the limit. The queued normal waiter must
	// NOT be admitted into the reserve: the freed slot stays empty.
	first.Release()
	time.Sleep(20 * time.Millisecond)
	require.Equal(t, 2, gate.Stats().Inflight, "a freed slot at the limit must not go to normal work")
	require.Equal(t, 1, gate.Stats().Waiters, "the normal waiter must still be waiting")
	select {
	case err := <-normalResult:
		t.Fatalf("the normal waiter was admitted into the reserve: %v", err)
	default:
	}

	// The second normal permit returning leaves the gate inside its limit, and the waiter is
	// admitted there - not into the reserve.
	second.Release()
	require.NoError(t, <-normalResult)
	require.Eventually(t, func() bool { return gate.Stats().Inflight == 1 }, time.Second, time.Millisecond,
		"only the interactive slot remains in flight")

	interactive.Release()
	require.Zero(t, gate.Stats().Inflight)
	requireBalanced(t, gate)
}

// TestGateReserveFreesForInteractiveWhileQueueIsBusy proves the reserve is reachable when it is
// needed, even with background work queued ahead of it, and that background work returns to
// being admitted once the reserve drains.
func TestGateReserveFreesForInteractiveWhileQueueIsBusy(t *testing.T) {
	gate := NewGate(plan{limit: 1, queue: 8, wait: time.Second}, 2)

	held, err := gate.Acquire(context.Background(), ClassNormal)
	require.NoError(t, err)

	queued := make(chan *Permit, 1)
	go func() {
		permit, _ := gate.Acquire(context.Background(), ClassNormal)
		queued <- permit
	}()
	require.Eventually(t, func() bool { return gate.Stats().Waiters == 1 }, time.Second, time.Millisecond)

	reserved := make([]*Permit, 0, 2)
	for index := 0; index < 2; index++ {
		permit, reserveErr := gate.Acquire(context.Background(), ClassInteractive)
		require.NoError(t, reserveErr, "reserve slot %d must be admitted ahead of queued background work", index)
		reserved = append(reserved, permit)
	}
	require.Equal(t, 3, gate.Stats().Inflight)

	for _, permit := range reserved {
		permit.Release()
	}
	// With the reserve drained but the base slot still held, the queued normal waiter still does
	// not fit - the limit is 1 and one normal permit is in flight.
	time.Sleep(20 * time.Millisecond)
	require.Equal(t, 1, gate.Stats().Waiters)

	held.Release()
	permit := <-queued
	require.NotNil(t, permit, "background work must be admitted once the base slot is free")
	permit.Release()
	require.Zero(t, gate.Stats().Inflight)
	requireBalanced(t, gate)
}
