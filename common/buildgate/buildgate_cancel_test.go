package buildgate

// D — cancellation and quota return for the shared build reservation.
//
// A build that waits and is cancelled must not keep a queue place or a slot, and the slot it never
// took must be available to the next caller. These are the accounting facts the daemon's reload
// path relies on: a cancelled rule-set refresh or a cancelled provider refresh cannot leave the
// box believing a build is still in flight.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestACancelledWaiterLeavesTheQueueAndTheQuotaIntact(t *testing.T) {
	gate := New(1, 4, time.Minute)
	release, err := gate.Acquire(context.Background())
	require.NoError(t, err)

	waitContext, cancel := context.WithCancel(context.Background())
	waiterResult := make(chan error, 1)
	go func() {
		_, waitErr := gate.Acquire(waitContext)
		waiterResult <- waitErr
	}()
	require.Eventually(t, func() bool { return gate.Stats().Waiters == 1 }, time.Second, time.Millisecond,
		"the waiter must be queued before it is cancelled")

	cancel()
	select {
	case waitErr := <-waiterResult:
		require.ErrorIs(t, waitErr, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("a cancelled waiter did not return")
	}
	require.Eventually(t, func() bool {
		stats := gate.Stats()
		return stats.Waiters == 0 && stats.Inflight == 1 && stats.Canceled == 1
	}, time.Second, time.Millisecond, "the cancelled waiter must leave no queue place and no slot")

	// The one admitted build still holds its slot; releasing it makes the quota available again.
	release()
	require.Eventually(t, func() bool { return gate.Stats().Inflight == 0 }, time.Second, time.Millisecond)

	nextRelease, err := gate.Acquire(context.Background())
	require.NoError(t, err, "the quota is available after the holder released it")
	require.Equal(t, 1, gate.Stats().Inflight)
	nextRelease()
	require.Equal(t, 0, gate.Stats().Inflight)
}

func TestAnAlreadyCancelledAcquireTakesNothing(t *testing.T) {
	gate := New(1, 1, time.Second)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := gate.Acquire(cancelled)
	require.ErrorIs(t, err, context.Canceled)
	stats := gate.Stats()
	require.Zero(t, stats.Inflight, "a cancelled acquire never took the slot")
	require.Zero(t, stats.Waiters)
	require.Equal(t, uint64(1), stats.Canceled, "the request is accounted as cancelled, not admitted")

	release, err := gate.Acquire(context.Background())
	require.NoError(t, err)
	release()
}

func TestAFullQueueIsADeferralNotACancellation(t *testing.T) {
	gate := New(1, 1, time.Minute)
	release, err := gate.Acquire(context.Background())
	require.NoError(t, err)

	// One waiter fills the queue.
	waitContext, cancelWait := context.WithCancel(context.Background())
	defer cancelWait()
	waiting := make(chan struct{})
	go func() {
		close(waiting)
		_, _ = gate.Acquire(waitContext)
	}()
	require.Eventually(t, func() bool { return gate.Stats().Waiters == 1 }, time.Second, time.Millisecond)

	// The next caller is refused with ErrDeferred: pressure, not an error of the build.
	_, err = gate.Acquire(context.Background())
	require.ErrorIs(t, err, ErrDeferred)
	require.False(t, errors.Is(err, context.Canceled))
	release()
}
