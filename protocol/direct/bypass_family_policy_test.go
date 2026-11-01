package direct

import (
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/common/dialer"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// The bypass gate must use the EFFECTIVE family policy, not the outbound's own configuration.
//
// # The defect
//
// `SocketSemantics.Blockers` consulted `AddressAllowedByStrategy` only when the outbound had configured a
// resolver of its own (`resolutionOnly != 0`). The strategy that actually applies is not a property of the
// outbound's configuration: `facts.FamilyStrategy` is the EFFECTIVE policy, and a strict one can come from
// the global DNS policy (`dns.strategy` -> the router's default -> `ResolveDialer.EffectiveFamilyStrategy`)
// with the direct outbound configuring nothing at all.
//
// The two paths then disagreed on the same flow, in the dangerous direction: with a global `ipv4_only`
// and a direct outbound holding no resolver, the userspace path refused an IPv6 literal with
// `no dial candidates`, while the bypass gate answered `BlockerNone`. A bypass is a connect performed by
// the platform, which has no family judgement, so the flow reached the very family the operator had
// forbidden - the outcome `BlockerFamilyStrategy` exists to prevent.
//
// # Why this test is at the outbound, not at the profile
//
// The profile re-derivation is only half of it. `BypassBlockers` is what the router calls, and it is the
// function that decides whether `h.familyStrategy` reaches the profile at all. Asserting here means the
// wiring (discovered from the chain, per the round's other finding) is covered too.

// TestABypassUsesTheEffectiveFamilyPolicyNotTheOutboundConfig is the regression.
func TestABypassUsesTheEffectiveFamilyPolicyNotTheOutboundConfig(t *testing.T) {
	ipv6 := netip.MustParseAddr("2001:db8::2c")
	ipv4 := netip.MustParseAddr("192.0.2.92")

	// The production shape: no dial options at all, so this outbound configures no resolver and
	// `resolutionOnly` is zero.
	outbound := bypassable(option.DialerOptions{})
	// The effective policy is strict, exactly as it would be if the operator set `dns.strategy`.
	outbound.surface.Resolve = stubResolveDialer{strategy: func() C.DomainStrategy { return C.DomainStrategyIPv4Only }}

	require.False(t, outbound.CanBypass(N.NetworkTCP, ipv6),
		"an ipv4_only policy must refuse a bypass to an IPv6 literal even when the outbound itself "+
			"configured no resolver: the userspace path filters the original attempt with the SAME "+
			"effective policy, so permitting the bypass would let the platform reach the forbidden family")
	require.Contains(t, outbound.BypassBlockers(N.NetworkTCP, ipv6).String(), "family strategy",
		"and it must say which policy refused it rather than being an unexplained refusal")

	// The controls that keep this from becoming a blanket refusal.
	require.True(t, outbound.CanBypass(N.NetworkTCP, ipv4),
		"the family the policy admits is exactly what the userspace path would dial, so it may bypass")

	outbound.surface.Resolve = stubResolveDialer{strategy: func() C.DomainStrategy { return C.DomainStrategyIPv6Only }}
	require.True(t, outbound.CanBypass(N.NetworkTCP, ipv6),
		"the matching strict policy admits this literal")
	require.False(t, outbound.CanBypass(N.NetworkTCP, ipv4),
		"and refuses the other one in the other direction")

	outbound.surface.Resolve = stubResolveDialer{strategy: func() C.DomainStrategy { return C.DomainStrategyPreferIPv4 }}
	require.True(t, outbound.CanBypass(N.NetworkTCP, ipv6),
		"a preference is not a restriction: treating prefer_ipv4 as a hard policy would refuse bypasses "+
			"for no reason and cost the fast path")

	outbound.surface.Resolve = stubResolveDialer{strategy: func() C.DomainStrategy { return C.DomainStrategyAsIS }}
	require.True(t, outbound.CanBypass(N.NetworkTCP, ipv6),
		"and AsIS restricts neither family")
}

// TestTheUDPArmAppliesTheSamePolicy is the network half of the same question.
//
// `SocketSemantics.Blockers` applies the family policy to every literal regardless of network, and
// `facts.Network` only selects the tcpOnly / udpOnly option groups. The userspace side did not match for
// UDP: `resolveDialer.ListenPacket` sent a literal straight to the leaf, so one flow had two answers -
// bypass refused, userspace dialled - and the strict policy was enforced on TCP literals and not on UDP
// ones. `common/dialer/listen_packet_strategy_test.go` is the other half of this pin; it lives there
// because that is where the dial path's own entry point is reachable.
//
// Both halves are asserted separately because the packages cannot import each other's internals, and the
// property under test is that the two answers agree, not which file the assertion is in.
func TestTheUDPArmAppliesTheSamePolicy(t *testing.T) {
	ipv6 := netip.MustParseAddr("2001:db8::2c")
	ipv4 := netip.MustParseAddr("192.0.2.92")

	outbound := bypassable(option.DialerOptions{})
	outbound.surface.Resolve = stubResolveDialer{strategy: func() C.DomainStrategy { return C.DomainStrategyIPv4Only }}

	require.False(t, outbound.CanBypass(N.NetworkUDP, ipv6),
		"a UDP flow to a literal the policy excludes must be refused by the bypass for the same reason "+
			"the TCP one is: the policy is about the destination's family, not about the transport")
	require.NotZero(t, outbound.BypassBlockers(N.NetworkUDP, ipv6)&dialer.BlockerFamilyStrategy)

	require.True(t, outbound.CanBypass(N.NetworkUDP, ipv4),
		"and the admitted family stays bypassable on UDP")

	outbound.surface.Resolve = stubResolveDialer{strategy: func() C.DomainStrategy { return C.DomainStrategyIPv6Only }}
	require.True(t, outbound.CanBypass(N.NetworkUDP, ipv6))
	require.False(t, outbound.CanBypass(N.NetworkUDP, ipv4),
		"including in the other direction, which is what makes this a policy rather than a v4 rule")
}

// TestABypassWithoutAnEffectivePolicyStillWorks keeps the fail-open direction where it belongs.
//
// A chain that cannot report an effective strategy leaves `facts.FamilyStrategy` at its zero value, which
// is AsIS: nothing is excluded. The uncertainty is about the machine's configuration, not about a
// dangerous operation, so the bypass stays available - the opposite polarity from the dial gate.
func TestABypassWithoutAnEffectivePolicyStillWorks(t *testing.T) {
	ipv6 := netip.MustParseAddr("2001:db8::2c")
	outbound := bypassable(option.DialerOptions{})
	require.Nil(t, outbound.surface.Resolve, "the fixture must not report a strategy")

	require.True(t, outbound.CanBypass(N.NetworkTCP, ipv6),
		"an outbound that cannot report an effective policy must not refuse every bypass: a strategy "+
			"nobody reported excludes nothing")
}
