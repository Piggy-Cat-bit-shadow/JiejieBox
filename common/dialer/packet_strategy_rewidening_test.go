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

// A strict family strategy must survive an EMPTY candidate list on the packet path.
//
// # The hole
//
// `ListenSerialNetworkPacket` (the package helper) treats an empty address list as "the caller had no
// candidates, so use the destination":
//
//	if len(destinationAddresses) == 0 {
//	    if !destination.IsIP() {
//	        panic("invalid usage")
//	    }
//	    destinationAddresses = []netip.Addr{destination.Addr}
//	}
//
// That is a reasonable convenience for a caller that genuinely has nothing, and it is a strategy violation
// for a caller whose list is empty BECAUSE THE POLICY EMPTIED IT. The wrapper filters the caller's list and
// then hands the result down:
//
//	candidates := withoutFamilyExcluded(destinationAddresses, d.effectiveFamilyStrategy())
//	... recovery ...
//	return ListenSerialNetworkPacket(ctx, d.attemptDialer(), destination, candidates, ...)
//
// so with `ipv4_only` and a UDP flow to an IPv6 literal, the filtered list is empty, the helper puts the
// literal back, and the socket is created for the family the operator forbade. Measured before the fix: the
// IPv6 literal reached the transport under an `ipv4_only` policy.
//
// The stream path had the same shape of problem and was fixed by reporting the exclusion; the packet path
// needed BOTH halves, because the re-synthesis happens below the wrapper and cannot be seen from there.

// TestPacketPathNeverDialsAFamilyTheStrategyExcluded is the regression.
func TestPacketPathNeverDialsAFamilyTheStrategyExcluded(t *testing.T) {
	originalIPv6 := netip.MustParseAddr("2409:8c54:871:1007::2c")

	// The machine is dual-stack, so nothing about capability is in play: only the policy excludes this
	// destination, and the policy is exactly what must be honoured.
	chain := newParallelProductionChain(t, netcap.StateProvisioned, netcap.StateProvisioned, C.DomainStrategyIPv4Only)
	chain.inner.packetConn = &countingPacketConn{}

	// No addresses configured, so recovery finds nothing and the filtered list stays empty: this is the
	// pure re-synthesis hole.
	ctx, cancel := context.WithTimeout(wechatFlow(originalIPv6, 443, "szminorshort.weixin.qq.com"), 3*time.Second)
	defer cancel()

	conn, _, err := chain.listenSerialNetworkPacketAtTheOutbound(ctx, M.SocksaddrFrom(originalIPv6, 443), nil)
	if conn != nil {
		conn.Close()
	}

	require.Error(t, err,
		"an ipv4_only policy excludes this destination and nothing else is available, so the flow must fail")
	require.NotContains(t, chain.tried(), originalIPv6,
		"the excluded literal must never reach the transport: re-synthesising it from an empty list turns "+
			"`ipv4_only` into a suggestion on the packet path")
	require.Empty(t, chain.tried(),
		"no candidate was admitted, so no socket attempt may happen at all")
}

// TestPacketPathIsRescuedByRecoveryWhenTheStrategyExcludesTheLiteral is the counterpart: the policy emptied
// the list, but the sniffed name has an address the policy admits.
//
// This is the packet path's version of TestRecoveryRescuesALiteralTheStrategyExcludes. It covers the same
// admission question - which family gets to END the lookup - on the path that has no scheduler.
func TestPacketPathIsRescuedByRecoveryWhenTheStrategyExcludesTheLiteral(t *testing.T) {
	originalIPv4 := netip.MustParseAddr("112.53.55.92")
	recoveredIPv6 := netip.MustParseAddr("2409:8c54:871:1007::2c")

	chain := newParallelProductionChain(t, netcap.StateProvisioned, netcap.StateProvisioned, C.DomainStrategyIPv6Only)
	chain.inner.packetConn = &countingPacketConn{}
	chain.router.addressesAAAA = []netip.Addr{recoveredIPv6}
	chain.router.addressesA = []netip.Addr{netip.MustParseAddr("112.53.55.93")}

	ctx, cancel := context.WithTimeout(wechatFlow(originalIPv4, 443, "szminorshort.weixin.qq.com"), 5*time.Second)
	defer cancel()

	conn, _, err := chain.listenSerialNetworkPacketAtTheOutbound(ctx, M.SocksaddrFrom(originalIPv4, 443), nil)
	if conn != nil {
		conn.Close()
	}

	require.NoError(t, err,
		"the policy admits the recovered AAAA, so the flow must be carried by it rather than refused")
	require.Contains(t, chain.tried(), recoveredIPv6)
	require.NotContains(t, chain.tried(), originalIPv4,
		"the excluded literal still never becomes a socket attempt")
}

// TestPacketPathAdmitsALiteralTheStrategyAllows is the control: without it, a fix that refused every packet
// flow would pass both tests above.
func TestPacketPathAdmitsALiteralTheStrategyAllows(t *testing.T) {
	originalIPv4 := netip.MustParseAddr("112.53.55.92")

	chain := newParallelProductionChain(t, netcap.StateProvisioned, netcap.StateProvisioned, C.DomainStrategyIPv4Only)
	chain.inner.packetConn = &countingPacketConn{}

	ctx, cancel := context.WithTimeout(wechatFlow(originalIPv4, 443, "szminorshort.weixin.qq.com"), 3*time.Second)
	defer cancel()

	conn, address, err := chain.listenSerialNetworkPacketAtTheOutbound(ctx, M.SocksaddrFrom(originalIPv4, 443), nil)
	if conn != nil {
		conn.Close()
	}

	require.NoError(t, err, "the policy admits this literal, so it must be dialled")
	require.Equal(t, originalIPv4, address)
	require.Contains(t, chain.tried(), originalIPv4)
}
