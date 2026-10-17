package dialer

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// The wireguard consumer's contract, pinned at the layer that broke it.
//
// # The regression this exists for
//
// `transport/wireguard/endpoint.go` chooses its whole packet-I/O strategy from one question:
//
//	common.Cast[dialer.UDPListener](e.options.Dialer)
//
// A `UDPListener` answer builds `StdNetBind` over the dialer's listener control; the other answer builds
// `ClientBind` over the dialer itself. One commit made `protocol/direct.Outbound` answer `Upstream()`, which
// `common.Cast` walks repo-wide - so a WireGuard endpoint whose dialer is a detour to a NON-EMPTY direct
// outbound started discovering the leaf's `UDPListener` THROUGH the hop, switched bind implementations, and
// lost the outbound's `netns` (applied in `DefaultDialer.ListenPacket`, not in the listener control).
//
// Nothing in `protocol/direct` could see it: the change was a new method on an outbound, and the behaviour
// moved three packages away. That is the shape of a repo-wide capability protocol being answered by a type
// that does not own the consumer.
//
// # The rule
//
// A hop reports its chain on `dialChainReporter` - a name known only to this package - and NOT on `Upstream()`.
// `common.Cast` therefore stops at every outbound, which is exactly the pre-refactor behaviour, while the two
// in-package lookups that need to see below a hop still can.

// chainOverDirectShapedHop builds the shape the regression needs: a detour whose target is a hop that reports
// a local chain, over a real `DefaultDialer` (which IS a `UDPListener`).
func chainOverDirectShapedHop(t *testing.T) N.Dialer {
	t.Helper()
	ctx := service.ContextWith[adapter.NetworkManager](context.Background(), newCountingNetworkManager())
	leaf, err := NewDefault(ctx, option.DialerOptions{})
	require.NoError(t, err)
	// The hop is the shape `protocol/direct.Outbound` presents: it forwards to a chain and reports it on the
	// package-scoped protocol, not on Upstream().
	hop := &peerOutbound{chain: leaf}
	return NewDetour(&countingOutboundManager{outbound: hop}, "peer", false)
}

// TestUDPListenerDiscoveryDoesNotCrossAHop is the regression.
func TestUDPListenerDiscoveryDoesNotCrossAHop(t *testing.T) {
	chain := chainOverDirectShapedHop(t)

	_, found := common.Cast[UDPListener](chain)
	require.False(t, found,
		"common.Cast must stop at an outbound hop. Walking through it makes a WireGuard endpoint switch from "+
			"ClientBind to StdNetBind, which drops the outbound's netns and moves packet egress onto the "+
			"leaf's listener control - a change in a consumer the outbound has never heard of")
}

// TestUDPListenerDiscoveryStillWorksWithoutAHop is the positive control, so the rule above cannot be satisfied
// by discovering nothing at all.
func TestUDPListenerDiscoveryStillWorksWithoutAHop(t *testing.T) {
	ctx := service.ContextWith[adapter.NetworkManager](context.Background(), newCountingNetworkManager())
	leaf, err := NewDefault(ctx, option.DialerOptions{})
	require.NoError(t, err)
	wrapped := NewResolveDialer(ctx, leaf, true, "", adapter.DNSQueryOptions{}, 0)

	listener, found := common.Cast[UDPListener](wrapped)
	require.True(t, found, "a resolve wrapper over the real leaf must still be discovered: that is the path "+
		"every WireGuard endpoint takes when it has no detour")
	require.NotNil(t, listener)
}

// TestTheHopProtocolIsStillFollowedByTheDialerWalks keeps the narrowing from losing what the hop reports.
//
// The two in-package walks must reach the chain; only `common.Cast` must not.
func TestTheHopProtocolIsStillFollowedByTheDialerWalks(t *testing.T) {
	ctx := service.ContextWith[adapter.NetworkManager](context.Background(), newCountingNetworkManager())
	leaf, err := NewDefault(ctx, option.DialerOptions{})
	require.NoError(t, err)
	hop := &peerOutbound{chain: leaf}

	owner, found := findFamilyHealthOwner(hop)
	require.True(t, found, "the health owner is reachable through the hop")
	require.NotNil(t, owner)

	require.False(t, (&resolveDialer{dialer: hop}).dialerLocalFamilyPolicy().restrictsNothing(),
		"and the family verdict is reachable through it as well (the fixture manager has no IPv6)")

	// A detour in front of the same hop stops both, which is the property the previous commit added.
	detour := NewDetour(&countingOutboundManager{outbound: hop}, "peer", false)
	_, found = findFamilyHealthOwner(detour)
	require.False(t, found, "a detour is a boundary, not a hop")
}
