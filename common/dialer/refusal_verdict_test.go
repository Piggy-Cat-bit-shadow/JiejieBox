package dialer

import (
	"context"
	"net"
	"net/netip"
	"testing"

	C "github.com/sagernet/sing-box/constant"

	"github.com/stretchr/testify/require"
)

// The refusal a reader sees must state the fact that produced it, on every path that produces one.
//
// # The two facts, and why one message cannot serve both
//
// A global IPv6 destination can be refused for two different reasons, with different remedies:
//
//	"no interface on this machine carries an ipv6 address"   the family is absent
//	"this machine has ipv6 addressing but no route to a
//	 global ipv6 destination"                                the family exists, public egress does not
//
// `localFamilyPolicy.hardUndialableError` distinguishes them, and the direct packet path prints the
// right one. The scheduler did not: it received an `addressFamily` and called
// `hardUnavailableError(family)`, which maps ipv6 to the FAMILY message unconditionally. The egress
// term could not be expressed in that signature at all.
//
// # The production evidence, which is why this is a bug and not a wording preference
//
// In one log window the same machine produced both messages for the same family:
//
//	stream:  open connection to [2001:db8::2c]:80 ... no interface on this machine carries
//	         an ipv6 address
//	packet:  listen packet connection ... no route to a global ipv6 destination (link-local and
//	         unique-local destinations are unaffected)
//
// One of those is false about the machine. The reported case is the EGRESS one - IPv6 addressing
// present, no global route - so the stream line sent the operator looking for an address that is right
// there. That is the failure `family_capability.go` says the two messages exist to prevent.

// refusingAttempt is the attempt function for a destination that must never be attempted.
//
// A socket attempt here would prove the gate did not fire, so it fails the test rather than returning
// an error the scheduler could interpret.
func refusingAttempt(t *testing.T) dialAttemptFunc {
	t.Helper()
	return func(ctx context.Context, address netip.Addr) (net.Conn, error) {
		t.Fatalf("no attempt may be made for a destination the machine cannot reach: %s", address)
		return nil, nil
	}
}

// TestSchedulerReportsTheMachinesOwnVerdict is the scheduler half, at the gate that actually runs for a
// TUN flow whose destination is already an address.
//
// `route.ConnectionManager.NewConnection` reaches this through `DialSerialNetwork` → the outbound →
// `DialParallelNetwork` → the scheduler. The direct `DialContext` gate was already correct, which is
// exactly why this went unnoticed: no test drove the scheduler with an egress-refusing policy and
// asserted the error it produced.
func TestSchedulerReportsTheMachinesOwnVerdict(t *testing.T) {
	// The reported machine: IPv6 present on the link, provisioned, with no global route.
	policy := outageDialer(windowsLinkLocalOnly()).captureLocalFamilyPolicy()
	original := netip.MustParseAddr("2001:db8::2c")
	require.True(t, policy.hardUndialable(original),
		"the fixture must actually refuse this destination, or the test proves nothing")

	scheduler := &candidateScheduler{localFamily: policy}

	conn, _, err := scheduler.dial(context.Background(),
		planCandidates([]netip.Addr{original}, netip.Addr{}, C.DomainStrategyPreferIPv6),
		refusingAttempt(t))
	require.Error(t, err)
	require.Nil(t, conn)

	require.ErrorIs(t, err, errLocalIPv6EgressUnavailable,
		"the scheduler must report the EGRESS fact: this machine HAS ipv6 addressing")
	require.NotErrorIs(t, err, errLocalIPv6Unavailable,
		"reporting an absent family on a machine that has one sends the operator to the wrong problem")
}

// TestEmptyRecoveryReportsTheMachinesOwnVerdict is the second half, and it is a hole the recovery work
// opened rather than one it inherited.
//
// With a sniffed domain present the stream path hands the scheduler a LATE channel, so an empty plan no
// longer takes the static refusal arm - it waits for candidates that may never come. When the stream then
// closes without producing one, the scheduler concluded `no dial candidates`, naming neither the machine
// nor the destination. A recovered-literal flow therefore degraded from a wrong message to no message.
//
// The refusal has to survive that wait: the plan was emptied because the destination was refused, and
// recovery producing nothing does not change why.
func TestEmptyRecoveryReportsTheMachinesOwnVerdict(t *testing.T) {
	policy := outageDialer(windowsLinkLocalOnly()).captureLocalFamilyPolicy()
	original := netip.MustParseAddr("2001:db8::2c")
	require.True(t, policy.hardUndialable(original))

	scheduler := &candidateScheduler{localFamily: policy}

	// A recovery stream that closes immediately: the resolver had nothing, which is the NXDOMAIN,
	// AAAA-only, or everything-refused case.
	lateCandidates := make(chan dualStackCandidate)
	close(lateCandidates)

	conn, _, err := scheduler.dialWithLateCandidates(context.Background(),
		planCandidates([]netip.Addr{original}, netip.Addr{}, C.DomainStrategyPreferIPv6),
		lateCandidates,
		refusingAttempt(t))
	require.Error(t, err)
	require.Nil(t, conn)

	require.NotContains(t, err.Error(), "no dial candidates",
		"a refusal must not decay into a phrase that names neither the machine nor the destination")
	require.ErrorIs(t, err, errLocalIPv6EgressUnavailable,
		"the reason the plan was empty must survive the wait for candidates that never arrived")
	require.NotErrorIs(t, err, errLocalIPv6Unavailable)
}

// TestARefusalThatDroppedNothingKeepsItsOwnMessage guards the opposite mistake.
//
// The scheduler reports a local-family refusal only when something was actually refused. A plan that was
// empty to begin with - a resolver that answered with nothing - must keep `no dial candidates`, because
// blaming the machine for a name that has no address would be a fabricated verdict.
func TestARefusalThatDroppedNothingKeepsItsOwnMessage(t *testing.T) {
	policy := outageDialer(windowsLinkLocalOnly()).captureLocalFamilyPolicy()
	scheduler := &candidateScheduler{localFamily: policy}

	conn, _, err := scheduler.dial(context.Background(),
		planCandidates(nil, netip.Addr{}, C.DomainStrategyAsIS),
		refusingAttempt(t))
	require.Error(t, err)
	require.Nil(t, conn)
	require.Contains(t, err.Error(), "no dial candidates")
	require.NotErrorIs(t, err, errLocalIPv6EgressUnavailable)
	require.NotErrorIs(t, err, errLocalIPv6Unavailable)
}

// TestStreamAndPacketReportTheSameVerdict is the symmetry claim as one assertion.
//
// The two paths reach the gate through completely different code - the stream one through the candidate
// scheduler, the packet one through the leaf's ListenPacket - and that is why they drifted. Both are
// asked the same question here and must answer with the same fact, character for character, because the
// message is the only thing an operator has to tell the two states apart.
func TestStreamAndPacketReportTheSameVerdict(t *testing.T) {
	policy := outageDialer(windowsLinkLocalOnly()).captureLocalFamilyPolicy()
	original := netip.MustParseAddr("2001:db8::2c")

	packetErr := policy.hardUndialableError(original)
	require.ErrorIs(t, packetErr, errLocalIPv6EgressUnavailable,
		"control: the packet-side gate reports the egress fact")

	_, _, err := (&candidateScheduler{localFamily: policy}).dial(context.Background(),
		planCandidates([]netip.Addr{original}, netip.Addr{}, C.DomainStrategyAsIS),
		refusingAttempt(t))
	require.Error(t, err)

	require.ErrorIs(t, err, errLocalIPv6EgressUnavailable,
		"the stream-side scheduler must report the SAME fact, not the family one")
	require.Equal(t, packetErr.Error(), err.Error(),
		"the two paths must not disagree about the machine they are both running on")
}
