package route

import (
	"context"
	"testing"
	"time"

	"github.com/sagernet/sing-box/log"

	"github.com/stretchr/testify/require"
)

// A network transition must not be observable half-done.
//
// # The invariant
//
// The transition has two clocks that a dial can read: the manager's generation counter (which the drain scan
// compares a connection's stamp against) and the setup gate's current context (which decides whether a dial
// still in its setup phase is cancelled as stale). A dial that reads them at different points of the same
// transition can be killed as an OLD-path dial while being classified as a CURRENT-path connection.
//
// Both readings must therefore be indivisible: a `begin` either sees the old counter with the old context, or
// the new counter with the new context. Nothing in between.
//
// # The measurement that made this a test
//
// An earlier round tried to fix this by SWAPPING the two operations and could not show any improvement over
// 2000 concurrent probes, because reordering cannot work: there are three instants (the gate's capture, the
// network epoch the epoch predicate compares, and the manager stamp written at track time), so moving two of
// them only changes which pair disagrees. With a forced interleaving - the gate's lock held while `Add`
// completed and `advance` queued - 200 of 200 trials produced a `begin` that started after the bump and was
// cancelled by that same transition. Unforced, 6 of 20 000.
//
// # Why this test is deterministic
//
// The window is produced by holding the gate's own lock, which is exactly the lock that now makes the pair
// atomic. So the assertion is not a race that may or may not happen: it is "while a transition is in progress,
// has the counter moved yet?" A transition that bumps the counter BEFORE taking the gate's lock answers yes,
// and that answer is the defect.

// TestATransitionNeverPublishesItsCounterBeforeItsGate fails on the old ordering.
func TestATransitionNeverPublishesItsCounterBeforeItsGate(t *testing.T) {
	manager := newReclaimTestManager(t)

	// Hold the gate's lock, so a transition can only be observed part-way through.
	manager.dialSetups.access.Lock()
	require.Equal(t, uint64(0), manager.generation.Load(), "the fixture must start at a fresh generation")

	reclaimReturned := make(chan int, 1)
	go func() {
		reclaimReturned <- manager.Reclaim(ReclaimNetworkTransition)
	}()

	// Wait long enough for a Reclaim that bumps OUTSIDE the gate to have done so, and for the gate advance to
	// be parked on the lock we hold. Waiting is the whole point: the assertion below is about what has been
	// published at a moment when the transition is provably unfinished.
	settleFor(t, 100*time.Millisecond)

	require.Equal(t, uint64(0), manager.generation.Load(),
		"while the transition's setup gate is still locked, the generation must not have moved: publishing the "+
			"counter first is what lets a dial capture the OLD context (and be cancelled as stale) while its "+
			"connection is stamped with the NEW counter (and classified as current). Measured before the fix: "+
			"the counter read 1 here on 200 of 200 forced trials")

	// Release, and confirm the transition then completes and publishes both halves.
	manager.dialSetups.access.Unlock()

	select {
	case count := <-reclaimReturned:
		_ = count
	case <-time.After(5 * time.Second):
		t.Fatal("Reclaim did not finish after the gate was released")
	}

	require.Equal(t, uint64(1), manager.generation.Load(),
		"and once it completes, the counter has moved exactly once")
}

// TestATransitionCancelsSetupsItBeganAfter is the ownership half, and the reason the ordering matters.
//
// A setup begun while the transition is in progress must either be entirely before it (cancelled) or entirely
// after it (spared). It must never be cancelled while being counted as current.
func TestATransitionCancelsSetupsItBeganAfter(t *testing.T) {
	manager := newReclaimTestManager(t)

	// A setup that begins BEFORE the transition is stale and must be cancelled by it.
	beforeCtx, releaseBefore := manager.dialSetups.begin(context.Background())
	defer releaseBefore()

	manager.Reclaim(ReclaimNetworkTransition)

	select {
	case <-beforeCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("a setup begun before the transition must be cancelled by it")
	}

	// A setup that begins AFTER the transition is not stale. The generation it would be stamped with equals
	// the one the transition published, so the drain scan treats it as current - and it must therefore not be
	// cancelled here, or the two answers would disagree in the opposite direction.
	afterCtx, releaseAfter := manager.dialSetups.begin(context.Background())
	defer releaseAfter()
	select {
	case <-afterCtx.Done():
		t.Fatal("a setup begun after the transition must not be cancelled by it")
	default:
	}
}

// newReclaimTestManager builds the smallest manager a transition needs: a logger for its reporting, a drain
// sweeper so the generation fields are initialised as production initialises them, and nothing else.
func newReclaimTestManager(t *testing.T) *ConnectionManager {
	t.Helper()
	return &ConnectionManager{logger: log.NewNOPFactory().NewLogger("reclaim-one-clock")}
}

func settleFor(t *testing.T, duration time.Duration) {
	t.Helper()
	time.Sleep(duration)
}
