package urltest

// Q08: the ROUND queue must be bounded independently of how many rounds arrive.
//
// The round budget (maxConcurrentRounds) bounds the rounds that are PRODUCING. The audit showed that
// it did not bound the rounds that are WAITING: every waiter was appended to the pool's queue, so 4
// rounds produced 2 waiting entries and 32 rounds produced 30. A bound on the workers does not bound
// the waiting descriptions.
//
// These tests pin the two properties the fix has to keep at the same time:
//
//   - the number of queue ENTRIES is a constant (roundWaiterLimit) however many rounds arrive, while
//     every round still gets its slot and still measures all of its members - no round is refused and
//     none is dropped;
//   - an interactive round still goes ahead of queued background rounds, now by taking the oldest
//     background entry's place: the background round loses its queue POSITION, not its turn.

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestTheRoundQueueIsBoundedHoweverManyRoundsArrive is the audit's measurement, turned into an
// assertion: the queue stays at its bound and the excess waits without an entry.
func TestTheRoundQueueIsBoundedHoweverManyRoundsArrive(t *testing.T) {
	t.Parallel()
	coordinator := NewCoordinator(10)
	limit := coordinator.RoundWaiterLimit()
	require.Equal(t, roundWaiterLimit, limit)

	// Two rounds hold the round budget, so the rest must wait.
	releaseFirst, err := coordinator.AcquireRound(context.Background())
	require.NoError(t, err)
	releaseSecond, err := coordinator.AcquireRound(context.Background())
	require.NoError(t, err)
	require.Equal(t, maxConcurrentRounds, coordinator.RoundsInFlight())

	const arrivals = 32
	const waiting = arrivals - maxConcurrentRounds
	var acquired atomic.Int64
	var group sync.WaitGroup
	for index := 0; index < waiting; index++ {
		group.Go(func() {
			release, acquireErr := coordinator.AcquireRound(context.Background())
			if acquireErr != nil {
				return
			}
			acquired.Add(1)
			release()
		})
	}
	require.Eventually(t, func() bool {
		return coordinator.RoundsQueued()+coordinator.RoundsOverflow() == waiting
	}, 5*time.Second, time.Millisecond, "every round must be waiting")
	require.Equal(t, limit, coordinator.RoundsQueued(),
		"the round queue must stop at its bound instead of growing with the number of rounds")
	require.Equal(t, waiting-limit, coordinator.RoundsOverflow(),
		"the rounds past the bound must wait without a queue entry")
	require.Greater(t, coordinator.RoundsOverflowTotal(), uint64(0))

	// Releasing one slot serves exactly one waiter and the rest stay bounded.
	releaseFirst()
	require.Eventually(t, func() bool {
		return coordinator.RoundsQueued()+coordinator.RoundsOverflow() <= waiting-1
	}, 5*time.Second, time.Millisecond)

	releaseSecond()
	// Every round still runs: the bound is on waiting DESCRIPTIONS, not on the work.
	require.Eventually(t, func() bool {
		return acquired.Load() == int64(waiting)
	}, 10*time.Second, time.Millisecond, "a bounded queue must not drop rounds")
	group.Wait()
	require.Zero(t, coordinator.RoundsInFlight())
	require.Zero(t, coordinator.RoundsQueued())
	require.Zero(t, coordinator.RoundsOverflow())
}

// TestAnInteractiveRoundTakesAQueuedBackgroundRoundsPlace keeps interaction fairness true at the
// bound: the interactive round is served first, and the background round it displaces still runs.
func TestAnInteractiveRoundTakesAQueuedBackgroundRoundsPlace(t *testing.T) {
	t.Parallel()
	coordinator := NewCoordinator(10)
	limit := coordinator.RoundWaiterLimit()

	releaseFirst, err := coordinator.AcquireRound(context.Background())
	require.NoError(t, err)
	releaseSecond, err := coordinator.AcquireRound(context.Background())
	require.NoError(t, err)

	// Fill the queue with background rounds.
	var backgroundAcquired atomic.Int64
	backgroundDone := make(chan struct{})
	var background sync.WaitGroup
	for index := 0; index < limit; index++ {
		background.Go(func() {
			release, acquireErr := coordinator.AcquireRound(context.Background())
			if acquireErr != nil {
				return
			}
			backgroundAcquired.Add(1)
			release()
		})
	}
	require.Eventually(t, func() bool {
		return coordinator.RoundsQueued() == limit
	}, 5*time.Second, time.Millisecond, "the background rounds must fill the queue")
	require.Zero(t, coordinator.RoundsOverflow())

	// The interactive round cannot get an entry - the queue is full - so it takes the oldest
	// background entry's place. That background round is demoted, not refused.
	interactiveDone := make(chan struct{})
	go func() {
		defer close(interactiveDone)
		release, acquireErr := coordinator.AcquireRoundPriority(context.Background())
		if acquireErr != nil {
			return
		}
		release()
	}()
	require.Eventually(t, func() bool {
		return coordinator.RoundsOverflow() == 1
	}, 5*time.Second, time.Millisecond, "the displaced background round must keep waiting")
	require.Equal(t, limit, coordinator.RoundsQueued())

	// The first released slot goes to the interactive round, not to a queued background one.
	releaseFirst()
	select {
	case <-interactiveDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the interactive round was not served ahead of the queued background rounds")
	}
	releaseSecond()
	background.Wait()
	close(backgroundDone)
	<-backgroundDone
	require.EqualValues(t, limit, backgroundAcquired.Load(), "the displaced round still runs")
	require.Zero(t, coordinator.RoundsQueued())
	require.Zero(t, coordinator.RoundsInFlight())
}
