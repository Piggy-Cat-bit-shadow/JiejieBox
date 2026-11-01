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

// The THIRD packet entry point, and the last one that did not apply the family strategy to a literal.
//
// # Why this file exists
//
// `TestPacketPathNeverDialsAFamilyTheStrategyExcluded` pinned the candidate list: with `ipv4_only` and a
// UDP flow to an IPv6 literal, an empty candidate list must not be re-synthesised into that literal below
// the wrapper, because that turns the policy into a suggestion. `resolveParallelNetworkDialer` and
// `DefaultDialer` were both covered.
//
// `resolveDialer.ListenPacket` was not. It is the arm `route.ConnectionManager` takes for an UNCONNECTED
// packet flow with no candidate list, and for a literal destination it went straight to the leaf - so the
// exact flow shape the candidate test protects created a socket for the forbidden family through the
// sibling entry point, and nothing in the suite could see it because every strategy test was written for
// the stream path or the candidate path.
//
// It also made the two models disagree: the native-bypass profile applies this policy to every literal, so
// one flow had two answers - userspace dials it, bypass refuses - which costs the fast path under any
// strict policy and invites the wrong repair.

// TestListenPacketRefusesALiteralTheStrategyExcludes is the regression.
func TestListenPacketRefusesALiteralTheStrategyExcludes(t *testing.T) {
	originalIPv6 := netip.MustParseAddr("2001:db8::2c")

	// The machine is dual-stack, so nothing about capability is in play: only the policy excludes this
	// destination, and the policy is exactly what must be honoured.
	chain := newParallelProductionChain(t, netcap.StateProvisioned, netcap.StateProvisioned, C.DomainStrategyIPv4Only)
	chain.inner.packetConn = &countingPacketConn{}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, err := chain.resolver.ListenPacket(ctx, M.SocksaddrFrom(originalIPv6, 443))
	if conn != nil {
		conn.Close()
	}

	require.Error(t, err, "an ipv4_only policy excludes this destination, so the flow must fail")
	require.Contains(t, err.Error(), "excluded by strategy",
		"and the refusal must name the policy rather than looking like a network failure")
	require.Empty(t, chain.tried(),
		"the excluded literal must never reach the transport: a packet socket for the forbidden family is "+
			"created here or nowhere, because this path has no scheduler and no recovery")
}

// TestListenPacketAdmitsALiteralTheStrategyAllows is the control: without it, a fix that refused every
// packet flow would pass the regression above.
func TestListenPacketAdmitsALiteralTheStrategyAllows(t *testing.T) {
	originalIPv4 := netip.MustParseAddr("192.0.2.92")

	chain := newParallelProductionChain(t, netcap.StateProvisioned, netcap.StateProvisioned, C.DomainStrategyIPv4Only)
	chain.inner.packetConn = &countingPacketConn{}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, err := chain.resolver.ListenPacket(ctx, M.SocksaddrFrom(originalIPv4, 443))
	require.NoError(t, err, "the policy admits this literal, so it must be dialled")
	require.NotNil(t, conn)
	conn.Close()
	require.Equal(t, []netip.Addr{originalIPv4}, chain.tried())
}

// TestListenPacketDoesNotApplyThePolicyToAWildcard pins the exemption.
//
// An unspecified address is how a caller asks for a wildcard socket with no destination family, which is
// the shape a listener uses. `0.0.0.0` is a VALID IPv4 address, so a check without this exemption refuses
// every wildcard listener under `ipv6_only` - and the listener would fail for a reason that has nothing to
// do with what it asked for.
func TestListenPacketDoesNotApplyThePolicyToAWildcard(t *testing.T) {
	chain := newParallelProductionChain(t, netcap.StateProvisioned, netcap.StateProvisioned, C.DomainStrategyIPv6Only)
	chain.inner.packetConn = &countingPacketConn{}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	wildcard := M.SocksaddrFrom(netip.IPv4Unspecified(), 0)
	conn, err := chain.resolver.ListenPacket(ctx, wildcard)
	require.NoError(t, err, "a wildcard socket names no family, so the policy has nothing to exclude")
	require.NotNil(t, conn)
	conn.Close()
	require.Len(t, chain.tried(), 1, "and it must reach the transport, or the exemption is a silent refusal")
}
