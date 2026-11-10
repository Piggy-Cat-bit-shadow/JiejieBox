package group

import (
	"context"
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	dialer "github.com/sagernet/sing-box/common/dialer"
	"github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// The route dispatch must reach the group's PARALLEL surface, not its DialContext.
//
// # Why this test exists
//
// The production call site is common/dialer.DialSerialNetwork: route hands it the outbound and the
// candidate list, and it dispatches on ParallelNetworkDialer. A group that forwarded to its own
// narrow surface - or one that simply did not implement the rich interface - would serialize the
// candidates instead of forwarding the list, and a blackholed first address would consume the shared
// deadline before the recovery path ever saw the other family.
//
// The pre-existing passthrough test called Selector.DialParallelNetwork DIRECTLY, so it passed even
// when the selector forwarded narrowly: it never exercised the dispatch that decides whether the
// rich surface is used at all. This one goes through DialSerialNetwork, which is the line
// route.ConnectionManager executes.
func TestRouteSerialDialReachesTheGroupsParallelSurface(t *testing.T) {
	leaf := &parallelLeaf{tag: "leaf"}
	manager := &taggedOutboundManager{byTag: map[string]adapter.Outbound{"leaf": leaf}}
	ctx := service.ContextWith[adapter.OutboundManager](context.Background(), manager)

	created, err := NewSelector(ctx, nil, log.NewNOPFactory().NewLogger("group"), "group", option.SelectorOutboundOptions{
		Outbounds: []string{"leaf"},
		Default:   "leaf",
	})
	require.NoError(t, err)
	selector := created.(*Selector)
	require.NoError(t, selector.Start(adapter.StartStateStart, &adapter.Scope{}))

	addresses := []netip.Addr{netip.MustParseAddr("2001:db8::1"), netip.MustParseAddr("192.0.2.1")}
	strategy := constant.NetworkStrategyFallback

	conn, err := dialer.DialSerialNetwork(context.Background(), selector, N.NetworkTCP,
		M.ParseSocksaddrHostPort("example.com", 443), addresses, &strategy, nil, nil, 0)
	require.NoError(t, err)
	require.NotNil(t, conn)

	require.Equal(t, int32(1), leaf.calls.Load(),
		"the route dispatch must reach the group's parallel surface; a group that answers on its "+
			"own DialContext serializes the candidates and never calls the leaf's parallel method")
	require.Equal(t, addresses, leaf.addresses,
		"the complete candidate list must survive the dispatch")
	require.Equal(t, &strategy, leaf.strategy, "the strategy must survive the dispatch")
}
