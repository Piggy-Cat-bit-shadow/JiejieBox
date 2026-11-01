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

// Recovery must not be disabled by the very policy check that creates the need for it.
//
// # The attack
//
// A strict family strategy excludes the literal the application connected to. That leaves the plan empty
// - and an empty plan is exactly when recovery is the ONLY possible source of a candidate, because the
// sniffed name may well have an address in the family the policy admits.
//
// The concrete shape: `dns.strategy: ipv4_only`, an application that connected to an IPv6 literal, a
// sniffed domain whose AAAA is excluded but whose A is admitted. The userspace path should resolve the
// name and dial the A record. Refusing on the spot instead kills a flow that was perfectly reachable under
// the operator's own policy.
//
// # Why this is a regression test and not a new feature
//
// The strategy-exclusion error added earlier in this series short-circuited on an empty filtered list:
//
//	candidates := withoutFamilyExcluded(destinationAddresses, d.effectiveFamilyStrategy())
//	if len(candidates) == 0 && len(destinationAddresses) > 0 {
//	    return nil, E.New("destination ... excluded by strategy ...")
//	}
//
// Before that change the same input fell through to `schedule` with a LATE channel, so recovery could
// still supply a candidate. The error message was worth having; skipping recovery to produce it was not.
// The two properties are compatible: when there is no immediate candidate to protect, discovery is not a
// precondition, it is the only work there is to do.

// TestRecoveryRescuesALiteralTheStrategyExcludes is the regression, in the ipv6_only direction.
func TestRecoveryRescuesALiteralTheStrategyExcludes(t *testing.T) {
	// The application connected to an IPv4 literal; the policy admits only IPv6. The sniffed name has
	// an AAAA, which the policy admits.
	originalIPv4 := netip.MustParseAddr("192.0.2.92")
	recoveredIPv6 := netip.MustParseAddr("2001:db8::2c")

	// Both families are usable, so nothing about the machine is in play: the literal is excluded by
	// POLICY alone, and the recovered address is admitted by the same policy.
	chain := newParallelProductionChain(t, netcap.StateProvisioned, netcap.StateProvisioned, C.DomainStrategyIPv6Only)
	chain.router.addressesAAAA = []netip.Addr{recoveredIPv6}
	chain.router.addressesA = []netip.Addr{netip.MustParseAddr("192.0.2.93")}

	ctx, cancel := context.WithTimeout(wechatFlow(originalIPv4, 80, "szminorshort.weixin.qq.com"), 5*time.Second)
	defer cancel()

	conn, err := chain.dialSerialNetworkAtTheOutbound(ctx, "tcp", M.SocksaddrFrom(originalIPv4, 80), nil)
	if conn != nil {
		conn.Close()
	}

	require.NoError(t, err,
		"an ipv6_only policy excludes the IPv4 literal, and the sniffed name has an AAAA: the flow must "+
			"be carried by the recovered address rather than refused for being what the policy already "+
			"told us not to use")
	require.Contains(t, chain.tried(), recoveredIPv6,
		"the recovered address the policy admits is what carries the flow")
	require.NotContains(t, chain.tried(), originalIPv4,
		"and the excluded literal must still never become a socket attempt")
}

// TestRecoveryRescuesALiteralTheStrategyExcludesIPv4Only is the same attack in the other direction.
func TestRecoveryRescuesALiteralTheStrategyExcludesIPv4Only(t *testing.T) {
	originalIPv6 := netip.MustParseAddr("2001:db8::2c")
	recoveredIPv4 := netip.MustParseAddr("192.0.2.92")

	chain := newParallelProductionChain(t, netcap.StateProvisioned, netcap.StateProvisioned, C.DomainStrategyIPv4Only)
	chain.router.addressesA = []netip.Addr{recoveredIPv4}
	chain.router.addressesAAAA = []netip.Addr{netip.MustParseAddr("2409:8c54:871:2007::10d")}

	ctx, cancel := context.WithTimeout(wechatFlow(originalIPv6, 80, "szminorshort.weixin.qq.com"), 5*time.Second)
	defer cancel()

	conn, err := chain.dialSerialNetworkAtTheOutbound(ctx, "tcp", M.SocksaddrFrom(originalIPv6, 80), nil)
	if conn != nil {
		conn.Close()
	}

	require.NoError(t, err,
		"an ipv4_only policy excludes the IPv6 literal, and the sniffed name has an A: the recovered "+
			"address must carry the flow")
	require.Contains(t, chain.tried(), recoveredIPv4)
	require.NotContains(t, chain.tried(), originalIPv6,
		"the excluded literal never becomes a socket attempt")
}

// TestAStrategyExcludedLiteralStillNamesTheStrategyWhenRecoveryFindsNothing keeps the diagnostic the
// exclusion was added for.
//
// Recovery running is not recovery succeeding. When it comes up empty, the operator still has to be told
// that a strategy - not the network - is what left nothing to dial.
func TestAStrategyExcludedLiteralStillNamesTheStrategyWhenRecoveryFindsNothing(t *testing.T) {
	originalIPv4 := netip.MustParseAddr("192.0.2.92")

	chain := newParallelProductionChain(t, netcap.StateProvisioned, netcap.StateProvisioned, C.DomainStrategyIPv6Only)
	// The resolver answers only in the family the policy forbids, so recovery cannot rescue the flow.
	chain.router.addressesAAAA = nil
	chain.router.addressesA = []netip.Addr{netip.MustParseAddr("192.0.2.93")}

	ctx, cancel := context.WithTimeout(wechatFlow(originalIPv4, 80, "szminorshort.weixin.qq.com"), 5*time.Second)
	defer cancel()

	conn, err := chain.dialSerialNetworkAtTheOutbound(ctx, "tcp", M.SocksaddrFrom(originalIPv4, 80), nil)
	if conn != nil {
		conn.Close()
	}

	require.Error(t, err)
	require.Contains(t, err.Error(), "excluded by strategy",
		"the refusal must name the policy: this is a configuration decision, not a property of the machine")
	require.Contains(t, err.Error(), "ipv6_only")
	require.Empty(t, chain.tried(), "an address the policy excludes never becomes a socket attempt")
}
