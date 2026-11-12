package urltest

// The Box's ROUND budget: how many measurement rounds may be PRODUCING at once.
//
// The measurement budget bounds the probes in flight but not what produces them; each round creates
// its own worker set and submitted-task queue (protocol/group/probeBatch). These tests pin the
// coordinator half: a constant number of rounds, whatever the measurement limit is, and a round that
// cannot be admitted waits instead of being refused.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestTheRoundBudgetIsAConstantWhateverTheMeasurementLimit pins the shape: the round bound must not
// scale with the number of measurements, because the thing it bounds is the number of PRODUCERS.
func TestTheRoundBudgetIsAConstantWhateverTheMeasurementLimit(t *testing.T) {
	for _, limit := range []int{1, 3, 8, 10, 64, 1000} {
		coordinator := NewCoordinator(limit)
		require.Equal(t, limit, coordinator.Limit())
		require.Equal(t, maxConcurrentRounds, coordinator.RoundLimit(),
			"limit %d: the round budget changed with the measurement limit", limit)
	}
}

// TestTheRoundBudgetDisablesWithTheMeasurementBudget pins the misconfiguration default: a
// non-positive limit means "no coordination", not "block every round".
func TestTheRoundBudgetDisablesWithTheMeasurementBudget(t *testing.T) {
	coordinator := NewCoordinator(0)
	require.Equal(t, 0, coordinator.RoundLimit())

	release, err := coordinator.AcquireRound(context.Background())
	require.NoError(t, err)
	require.NotNil(t, release)
	release()
	release() // idempotent, and must not panic on a disabled pool

	// A nil coordinator means "no Box", which is equally unbounded.
	var nilCoordinator *Coordinator
	release, err = nilCoordinator.AcquireRound(context.Background())
	require.NoError(t, err)
	release()
}

// TestTheRoundBudgetHoldsTheNextRoundBack pins the bound itself, and that a round that cannot be
// admitted WAITS rather than failing: refusing here would be a round that measured no member at all.
func TestTheRoundBudgetHoldsTheNextRoundBack(t *testing.T) {
	coordinator := NewCoordinator(10)

	releases := make([]func(), 0, maxConcurrentRounds)
	for index := 0; index < maxConcurrentRounds; index++ {
		release, err := coordinator.AcquireRound(context.Background())
		require.NoError(t, err)
		releases = append(releases, release)
	}
	require.Equal(t, maxConcurrentRounds, coordinator.RoundsInFlight())
	require.Zero(t, coordinator.RoundsQueued())

	admitted := make(chan func(), 1)
	go func() {
		release, err := coordinator.AcquireRound(context.Background())
		require.NoError(t, err)
		admitted <- release
	}()
	require.Eventually(t, func() bool { return coordinator.RoundsQueued() == 1 },
		5*time.Second, time.Millisecond, "the extra round was not queued behind the budget")
	require.Equal(t, maxConcurrentRounds, coordinator.RoundsInFlight(),
		"the extra round was admitted over the bound")

	releases[0]()
	select {
	case release := <-admitted:
		release()
	case <-time.After(5 * time.Second):
		t.Fatal("a round that was waiting was never admitted after a slot freed")
	}
	for _, release := range releases[1:] {
		release()
	}
	require.Zero(t, coordinator.RoundsInFlight())
	require.Zero(t, coordinator.RoundsQueued())
}

// TestTheRoundBudgetPrefersAnInteractiveRound pins the ordering at the coordinator level: the pool's
// rule is per pool, so an interactive round jumps queued background rounds exactly as an interactive
// probe does.
func TestTheRoundBudgetPrefersAnInteractiveRound(t *testing.T) {
	coordinator := NewCoordinator(10)

	held := make([]func(), 0, maxConcurrentRounds)
	for index := 0; index < maxConcurrentRounds; index++ {
		release, err := coordinator.AcquireRound(context.Background())
		require.NoError(t, err)
		held = append(held, release)
	}

	background := make(chan struct{}, 2)
	go func() {
		release, err := coordinator.AcquireRound(context.Background())
		require.NoError(t, err)
		release()
		background <- struct{}{}
	}()
	require.Eventually(t, func() bool { return coordinator.RoundsQueued() == 1 },
		5*time.Second, time.Millisecond)

	// The interactive round HOLDS its slot until the test says so: releasing it immediately would
	// admit the background round and make "who was first" unobservable.
	interactive := make(chan func(), 1)
	go func() {
		release, err := coordinator.AcquireRoundPriority(context.Background())
		require.NoError(t, err)
		interactive <- release
	}()
	require.Eventually(t, func() bool { return coordinator.RoundsQueued() == 2 },
		5*time.Second, time.Millisecond)

	// One slot frees: the interactive round must take it, even though the background round queued
	// first.
	held[0]()
	var interactiveRelease func()
	select {
	case interactiveRelease = <-interactive:
	case <-time.After(5 * time.Second):
		t.Fatal("the interactive round was not admitted first")
	}
	select {
	case <-background:
		t.Fatal("the background round was admitted before the interactive one")
	default:
	}
	require.Equal(t, 1, coordinator.RoundsQueued(),
		"the background round must still be waiting while the interactive round holds its slot")

	interactiveRelease()
	select {
	case <-background:
	case <-time.After(5 * time.Second):
		t.Fatal("the background round was never admitted after the interactive one finished")
	}
	held[1]()
	require.Zero(t, coordinator.RoundsInFlight())
	require.Zero(t, coordinator.RoundsQueued())
}

// TestACancelledRoundWaiterLeavesNoEntry pins the cancellation half: a round whose context ends while
// it waits must be removed from the queue, or the budget would leak a position for the Box's life.
func TestACancelledRoundWaiterLeavesNoEntry(t *testing.T) {
	coordinator := NewCoordinator(10)
	held := make([]func(), 0, maxConcurrentRounds)
	for index := 0; index < maxConcurrentRounds; index++ {
		release, err := coordinator.AcquireRound(context.Background())
		require.NoError(t, err)
		held = append(held, release)
	}

	waitingCtx, cancel := context.WithCancel(context.Background())
	failed := make(chan error, 1)
	go func() {
		_, err := coordinator.AcquireRound(waitingCtx)
		failed <- err
	}()
	require.Eventually(t, func() bool { return coordinator.RoundsQueued() == 1 },
		5*time.Second, time.Millisecond)
	cancel()
	select {
	case err := <-failed:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("a round waiting for the budget ignored its cancelled context")
	}
	require.Zero(t, coordinator.RoundsQueued())
	for _, release := range held {
		release()
	}
	require.Zero(t, coordinator.RoundsInFlight())
}
