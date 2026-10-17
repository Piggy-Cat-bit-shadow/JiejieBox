package dialer

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/netcap"
	C "github.com/sagernet/sing-box/constant"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// The FOURTH arm of the packet surface, and the last one that applied no admission policy at all.
//
// # How this file came to exist
//
// Three packet arms were already pinned: the literal (listen_packet_strategy_test.go), the
// caller-supplied candidate list (TestPacketPathNeverDialsAFamilyTheStrategyExcluded), and the parallel
// interface packet path. The fourth - an UNCONNECTED packet flow to a HOSTNAME, which is the arm
// route.ConnectionManager takes when it has no destination addresses and the destination is not an IP -
// resolved the name and handed the resolver's list straight to N.ListenSerial.
//
// Both terms were missing, and the omission was a live policy violation rather than a theoretical one.
// MEASURED before the fix, with `ipv4_only` and a name whose A answer was empty while AAAA answered:
//
//	err   = <nil>
//	tried = [2409:8c54:871:1007::2c]
//
// A UDP socket, created for the family the operator had forbidden, reported as a success. Nothing in the
// suite could see it, because every strategy test was written for the stream path or for a candidate list.
//
// # Why a socket creation makes this worse than the stream equivalent
//
// A stream dial can be rescued later - the candidate list is planned, the scheduler filters, the leaf
// re-checks, and a forbidden family is refused by at least one of them. Here there is no scheduler and no
// race: N.ListenSerial walks the list it is given and the first address that opens a socket wins. The list
// handed to it IS the decision, which is why the decision has to be made before the call.

// TestPacketDomainPathAppliesTheStrategy is the regression.
func TestPacketDomainPathAppliesTheStrategy(t *testing.T) {
	originalIPv6 := netip.MustParseAddr("2409:8c54:871:1007::2c")

	// The machine is dual-stack, so nothing about capability is in play: only the operator's policy
	// excludes the IPv6 answer, and the A answer is empty so the resolver offers nothing else.
	chain := newParallelProductionChain(t, netcap.StateProvisioned, netcap.StateProvisioned, C.DomainStrategyIPv4Only)
	chain.inner.packetConn = &countingPacketConn{}
	chain.router.addressesA = nil
	chain.router.addressesAAAA = []netip.Addr{originalIPv6}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, err := chain.resolver.ListenPacket(ctx, M.Socksaddr{Fqdn: "v6only.example", Port: 443})
	if conn != nil {
		conn.Close()
	}

	require.Error(t, err, "an ipv4_only policy excludes this destination, so the flow must fail")
	require.Contains(t, err.Error(), "excluded by strategy",
		"the refusal must name the policy rather than looking like a DNS or network failure")
	require.Empty(t, chain.tried(),
		"the forbidden family must never reach the transport: a UDP socket is created here or nowhere, "+
			"because this arm has no scheduler and a packet connection cannot be swapped afterwards")
}

// TestPacketDomainPathAppliesTheMachineVerdict is the same arm's OTHER term.
//
// The operator's policy admits everything here; the machine is the one that cannot dial. The refusal must
// say which, because "excluded by strategy" would send an operator to change a configuration that is not
// the problem.
func TestPacketDomainPathAppliesTheMachineVerdict(t *testing.T) {
	originalIPv6 := netip.MustParseAddr("2409:8c54:871:1007::2c")

	chain := newParallelProductionChain(t, netcap.StateProvisioned, netcap.StateUnavailable, C.DomainStrategyAsIS)
	chain.inner.packetConn = &countingPacketConn{}
	chain.router.addressesA = nil
	chain.router.addressesAAAA = []netip.Addr{originalIPv6}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, err := chain.resolver.ListenPacket(ctx, M.Socksaddr{Fqdn: "v6only.example", Port: 443})
	if conn != nil {
		conn.Close()
	}

	require.Error(t, err, "this machine has no IPv6 addressing, so the flow must fail rather than open a socket")
	require.ErrorIs(t, err, errLocalIPv6Unavailable,
		"the refusal must be the machine's own verdict, with the message that says so")
	require.Empty(t, chain.tried())
}

// TestPacketDomainPathAdmitsAUsableFamily is the control.
//
// Without it, a fix that refused every packet flow to a name would pass both regressions above, and the
// arm would be "fixed" by being deleted.
func TestPacketDomainPathAdmitsAUsableFamily(t *testing.T) {
	originalIPv4 := netip.MustParseAddr("112.53.55.92")
	originalIPv6 := netip.MustParseAddr("2409:8c54:871:1007::2c")

	chain := newParallelProductionChain(t, netcap.StateProvisioned, netcap.StateProvisioned, C.DomainStrategyIPv4Only)
	chain.inner.packetConn = &countingPacketConn{}
	chain.router.addressesA = []netip.Addr{originalIPv4}
	chain.router.addressesAAAA = []netip.Addr{originalIPv6}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, err := chain.resolver.ListenPacket(ctx, M.Socksaddr{Fqdn: "dual.example", Port: 443})
	require.NoError(t, err, "the policy admits this family, so the flow must be dialled")
	require.NotNil(t, conn)
	conn.Close()

	require.Equal(t, []netip.Addr{originalIPv4}, chain.tried(),
		"exactly the admitted family reaches the transport, and the excluded one does not")
}

// TestPacketDomainPathKeepsTheResolverOrderWithinTheAdmittedFamily pins that the filter does not reorder.
//
// N.ListenSerial is a serial walk, not a race: the order it is given is the order tried, and the whole
// contract of the serial path is "the resolver's order, one at a time". A filter that compacted or sorted
// would silently turn the operator's resolver preference into this package's opinion.
func TestPacketDomainPathKeepsTheResolverOrderWithinTheAdmittedFamily(t *testing.T) {
	first := netip.MustParseAddr("112.53.55.92")
	second := netip.MustParseAddr("203.0.113.7")

	chain := newParallelProductionChain(t, netcap.StateProvisioned, netcap.StateProvisioned, C.DomainStrategyIPv4Only)
	chain.inner.packetConn = &countingPacketConn{}
	chain.router.addressesA = []netip.Addr{first, second}
	chain.router.addressesAAAA = []netip.Addr{netip.MustParseAddr("2409:8c54:871:1007::2c")}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, err := chain.resolver.ListenPacket(ctx, M.Socksaddr{Fqdn: "ordered.example", Port: 443})
	require.NoError(t, err)
	conn.Close()

	require.Equal(t, []netip.Addr{first}, chain.tried(),
		"the first admitted address the resolver offered is the one tried; the walk stops at the first "+
			"success, so a reordering would be invisible except here")
}
