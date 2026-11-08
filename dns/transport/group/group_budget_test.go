package group

// D01 — the whole parent request's budget.
//
// The group already splits the remaining budget: the target gets half, and the
// rescue fan is supposed to live inside the other half. That split only bounds
// anything when the REQUEST has a deadline, so these tests pin the two cases the
// split was calibrated for:
//
//   - a caller with no deadline: the group installs its own parent budget, so a
//     target that eats its half cannot leave the rescue fan running unbounded;
//   - a caller WITH a deadline: that deadline stays the bound, and its expiry is
//     LOCAL — no member is blamed for a client that hung up.
//
// What must NOT change is the blackhole detector: a target that eats its own
// sub-deadline is still recorded as a member failure, because that expiry is the
// group's own verdict about the member rather than a local cancellation.

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	mDNS "github.com/miekg/dns"
)

// withGroupRequestBudget shrinks the parent budget for one test.
func withGroupRequestBudget(t *testing.T, budget time.Duration) {
	t.Helper()
	previous := groupRequestBudget
	groupRequestBudget = budget
	t.Cleanup(func() { groupRequestBudget = previous })
}

// hangUntilCancelled installs a member behaviour that blocks until its context
// ends, then reports the cancellation. It is the shape of a blackholed server.
func hangUntilCancelled(member *fakeTransport) {
	member.setBehavior(func(ctx context.Context, message *mDNS.Msg) (*mDNS.Msg, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
}

// exchangeWithin runs one exchange and fails the test if it does not return
// within the given bound, so a missing budget shows up as a clean failure instead
// of a hung test binary.
func exchangeWithin(t *testing.T, harness *groupHarness, ctx context.Context, bound time.Duration) error {
	t.Helper()
	type result struct {
		response *mDNS.Msg
		err      error
	}
	done := make(chan result, 1)
	go func() {
		response, err := harness.exchange(ctx)
		done <- result{response: response, err: err}
	}()
	select {
	case got := <-done:
		return got.err
	case <-time.After(bound):
		t.Fatalf("the parent request did not return within %s: it has no budget of its own", bound)
		return nil
	}
}

// TestAParentRequestWithoutADeadlineStillEnds is the control the budget exists
// for: with no caller deadline, both members hanging must still end the request
// within the group's own budget.
func TestAParentRequestWithoutADeadlineStillEnds(t *testing.T) {
	const budget = 300 * time.Millisecond
	withGroupRequestBudget(t, budget)

	harness := newGroupHarness(t, stableOptions("a", "b"))
	harness.setCurrent("a")
	hangUntilCancelled(harness.byTag["a"])
	hangUntilCancelled(harness.byTag["b"])
	baseline := runtime.NumGoroutine()

	started := time.Now()
	err := exchangeWithin(t, harness, context.Background(), 3*time.Second)
	elapsed := time.Since(started)

	require.Error(t, err, "both members hung, so the query must fail")
	require.Less(t, elapsed, 2*time.Second,
		"the parent request must be bounded by its own budget, took %s with a %s budget", elapsed, budget)
	require.GreaterOrEqual(t, elapsed, budget/2,
		"the target must have been given its half of the budget before the request ended, took %s", elapsed)

	// The blackhole detector still fires for the TARGET, whose sub-deadline is the
	// group's own verdict; the rescuer cut by the parent budget is not blamed.
	require.Equal(t, 1, harness.liveErrors("a"),
		"the target that ate its sub-deadline must still be recorded as failing")
	require.Zero(t, harness.liveErrors("b"),
		"a rescuer cut by the group's own budget must not be recorded as a member failure")

	// The fan goroutines must be gone: the fan cancels and drains every participant.
	require.Eventually(t, func() bool {
		return runtime.NumGoroutine() <= baseline+6
	}, 5*time.Second, 10*time.Millisecond,
		"goroutines: have %d, baseline %d — the fan did not drain its participants", runtime.NumGoroutine(), baseline)
}

// TestACallerDeadlineCutsTheRescuerWithoutBlamingIt pins the local half of the
// split. A caller deadline always fires AFTER the target's half, so the target is
// legitimately recorded for eating its sub-deadline; the rescuer that is still
// running when the caller gives up must NOT be recorded, and the request must end
// at the caller's deadline rather than at the group budget.
func TestACallerDeadlineCutsTheRescuerWithoutBlamingIt(t *testing.T) {
	withGroupRequestBudget(t, 5*time.Second) // deliberately longer than the caller's

	harness := newGroupHarness(t, stableOptions("a", "b"))
	harness.setCurrent("a")
	hangUntilCancelled(harness.byTag["a"])
	hangUntilCancelled(harness.byTag["b"])

	callerCtx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := exchangeWithin(t, harness, callerCtx, 2*time.Second)
	elapsed := time.Since(started)

	require.Error(t, err)
	require.Less(t, elapsed, time.Second,
		"the caller's deadline must bound the request, took %s", elapsed)
	require.Equal(t, 1, harness.liveErrors("a"),
		"the target's own sub-deadline expired first, so the blackhole record is the group's verdict")
	require.Zero(t, harness.liveErrors("b"),
		"a rescuer still running when the caller hung up must not be recorded as a member failure")
}

// TestACallerCancellationIsNeverAMemberFailure pins the local classification
// itself: cancelling the caller while the target is in flight ends the request with
// no member record at all, because the member never got a fair chance to answer.
func TestACallerCancellationIsNeverAMemberFailure(t *testing.T) {
	withGroupRequestBudget(t, 5*time.Second)

	harness := newGroupHarness(t, stableOptions("a", "b"))
	harness.setCurrent("a")
	hangUntilCancelled(harness.byTag["a"])
	hangUntilCancelled(harness.byTag["b"])

	callerCtx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	err := exchangeWithin(t, harness, callerCtx, 2*time.Second)

	require.Error(t, err)
	require.Zero(t, harness.liveErrors("a"),
		"a caller cancellation must not mark the target dirty")
	require.Zero(t, harness.liveErrors("b"),
		"a caller cancellation must not mark a rescuer dirty")
}

// TestAFastFailureStillRescuesWithinTheBudget keeps the budget from changing the
// happy rescue path: a target that fails at once must still be rescued by the
// other member, not cut short by the budget.
func TestAFastFailureStillRescuesWithinTheBudget(t *testing.T) {
	withGroupRequestBudget(t, 300*time.Millisecond)

	harness := newGroupHarness(t, stableOptions("a", "b"))
	harness.setCurrent("a")
	harness.byTag["a"].fail()
	harness.byTag["b"].succeed()

	response, err := harness.exchange(context.Background())
	require.NoError(t, err, "the rescue fan must answer within the parent budget")
	require.Equal(t, mDNS.RcodeSuccess, response.Rcode)
	require.Equal(t, int64(1), harness.calls("b"))
	require.Equal(t, 1, harness.liveErrors("a"))
}
