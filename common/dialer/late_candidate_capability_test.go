package dialer

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/netcap"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// Layer 7 of the debug round: the late-candidate stream across a capability change.
//
// # The case the brief names
//
// A race has begun on generation A. While it is in flight the network transitions, and the new
// generation has a different answer about IPv6. The requirement is that two generations' verdicts are
// never mixed inside one race.
//
// # Why the design satisfies it, and what this test actually has to prove
//
// The verdict is read ONCE, when the scheduler is built for this connection, and the candidates it
// admits are the candidates this race will ever consider. A later change cannot reach the launched
// plan or the launch queue, because nothing re-reads the capability while the race runs - which is
// deliberate: a race whose admissible set changed underneath it would be racing under two policies at
// once, and there is no reading of "which family is preferred" that makes that coherent.
//
// What the test therefore has to prove is the pair of consequences:
//
//	the IN-FLIGHT race is not rewritten mid-flight, and
//	the NEXT connection gets the NEW answer immediately
//
// Both are asserted below. The second is what keeps the first from being lock-in: a verdict lasts
// exactly one connection.

// TestCapabilityChangeDoesNotRewriteAnInFlightRace is the "do not mix generations" half.
//
// The race is started while IPv6 is provisioned, and the capability is changed to unavailable while
// both attempts are still outstanding. The assertion is on what the CHANGE must not do:
//
//	it must not cancel an attempt that the race admitted, and
//	it must not remove a candidate from a race already in flight
//
// Both are observable: the IPv6 attempt records an attempt (so it was launched and allowed to run),
// and the dial succeeds (so the race concluded normally rather than being aborted by the change).
//
// The connection is deliberately not asserted to be IPv6. Which family wins is the plan's business -
// this test is about the change being inert, and pinning the winner would make the assertion about
// the schedule rather than about the capability.
func TestCapabilityChangeDoesNotRewriteAnInFlightRace(t *testing.T) {
	dialer := newCapabilityDialer(t, netcap.StateProvisioned, netcap.StateProvisioned)
	inner := dialer.inner
	inner.stall = nil
	// Both attempts must still be outstanding when the flip happens, or the change would be observing
	// a decided race rather than an in-flight one.
	inner.connectDelay = 200 * time.Millisecond

	scheduler := dialer.newDualStackScheduler(5*time.Millisecond, admissionForTest(dialer))
	plan := planCandidates([]netip.Addr{address6, address4}, netip.Addr{}, 0)

	// Flip the machine's answer to "no IPv6" while both attempts are running.
	flipped := make(chan struct{})
	go func() {
		defer close(flipped)
		time.Sleep(20 * time.Millisecond)
		dialer.manager.setStates(netcap.StateProvisioned, netcap.StateUnavailable)
	}()

	conn, _, err := scheduler.dial(context.Background(), plan,
		func(ctx context.Context, address netip.Addr) (net.Conn, error) {
			return inner.DialContext(ctx, "tcp", M.SocksaddrFrom(address, 443))
		})
	require.NoError(t, err)
	require.NotNil(t, conn, "the race must conclude normally: the change is not an abort")
	<-flipped

	require.ElementsMatch(t, []netip.Addr{address6, address4}, dialer.tried(),
		"both admitted attempts must have run: a verdict that changed mid-flight must not cancel or "+
			"withdraw a candidate from a race that is already under way")
}

// TestTheNextRaceGetsTheNewVerdict is the "no lock-in" half, and the reason the previous test is not a
// bug.
//
// The verdict is re-read for every connection. A family that has become absent must stop being
// attempted at the very next dial - not when some cache expires, and not when the process restarts,
// because a capability that only ever loosens or only ever tightens is not a capability.
func TestTheNextRaceGetsTheNewVerdict(t *testing.T) {
	dialer := newCapabilityDialer(t, netcap.StateProvisioned, netcap.StateProvisioned)
	connectImmediately(dialer)

	// First connection: both families are dialable, so both are offered.
	conn, err := DialParallelNetwork(context.Background(), dialer, "tcp",
		M.SocksaddrFrom(address6, 443),
		[]netip.Addr{address6, address4},
		true, nil, nil, nil, 5*time.Millisecond, admissionForTest(dialer))
	require.NoError(t, err)
	require.NotNil(t, conn)
	require.Contains(t, dialer.tried(), address6)

	// The machine loses its IPv6 addressing. No transition is involved: this is the same network with
	// different addressing, which is exactly the case a re-read exists for.
	dialer.manager.setStates(netcap.StateProvisioned, netcap.StateUnavailable)

	before := len(dialer.tried())
	conn, err = DialParallelNetwork(context.Background(), dialer, "tcp",
		M.SocksaddrFrom(address6, 443),
		[]netip.Addr{address6, address4},
		true, nil, nil, nil, 5*time.Millisecond, admissionForTest(dialer))
	require.NoError(t, err)
	require.NotNil(t, conn)

	after := dialer.tried()[before:]
	require.Equal(t, []netip.Addr{address4}, after,
		"the very next connection must use the new verdict, so the verdict is read per connection "+
			"rather than cached for the life of the dialer")
}

// TestRemovingTheLastDialableFamilyFailsTheRaceImmediately covers the stream that carries only
// refused candidates.
//
// When every family in the stream has become undialable, the race must conclude as soon as the
// resolver finishes rather than waiting for the caller's timeout. The conclusion is driven by the
// resolver closing the stream - not by a timer - so the failure is immediate and attributable.
func TestRemovingTheLastDialableFamilyFailsTheRaceImmediately(t *testing.T) {
	dialer := newCapabilityDialer(t, netcap.StateUnavailable, netcap.StateUnavailable)
	connectImmediately(dialer)
	scheduler := dialer.newDualStackScheduler(5*time.Millisecond, admissionForTest(dialer))

	late := make(chan dualStackCandidate, 2)
	late <- dualStackCandidate{address: address4, family: familyIPv4}
	late <- dualStackCandidate{address: address6, family: familyIPv6}
	close(late)

	start := time.Now()
	_, _, err := scheduler.dialWithLateCandidates(context.Background(),
		planCandidates(nil, netip.Addr{}, 0), late,
		func(ctx context.Context, address netip.Addr) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp", M.SocksaddrFrom(address, 443))
		})
	elapsed := time.Since(start)

	require.Error(t, err, "a stream of only-refused candidates must fail rather than hang")
	require.Empty(t, dialer.tried(), "and must not spend an attempt it knows cannot succeed")
	require.Less(t, elapsed, time.Second,
		"the race concludes when the stream closes, not when the caller's timeout expires")
}

// TestAnEmptyPlanWithALateStreamStillWaitsForIt guards the other direction of the same rule.
//
// An empty plan plus an open stream means the resolver has not answered yet - the ordinary shape for a
// name whose families arrive at different times. The filter must not turn that into an immediate
// failure: only a stream that has CLOSED with nothing usable concludes the race.
func TestAnEmptyPlanWithALateStreamStillWaitsForIt(t *testing.T) {
	dialer := newCapabilityDialer(t, netcap.StateUnavailable, netcap.StateProvisioned)
	connectImmediately(dialer)
	scheduler := dialer.newDualStackScheduler(5*time.Millisecond, admissionForTest(dialer))

	late := make(chan dualStackCandidate)
	go func() {
		// The IPv6 answer arrives after the race has started, and the IPv4 half is refused when it
		// does arrive. Neither ordering may lose the one usable candidate.
		time.Sleep(20 * time.Millisecond)
		late <- dualStackCandidate{address: address6, family: familyIPv6}
		late <- dualStackCandidate{address: address4, family: familyIPv4}
		close(late)
	}()

	start := time.Now()
	conn, winner, err := scheduler.dialWithLateCandidates(context.Background(),
		planCandidates(nil, netip.Addr{}, 0), late,
		func(ctx context.Context, address netip.Addr) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp", M.SocksaddrFrom(address, 443))
		})
	require.NoError(t, err)
	require.NotNil(t, conn)
	require.Equal(t, address6, winner)
	require.GreaterOrEqual(t, time.Since(start), 20*time.Millisecond,
		"the race waited for the stream rather than concluding on an empty plan")
	require.Equal(t, []netip.Addr{address6}, dialer.tried())
}
