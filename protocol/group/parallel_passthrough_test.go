package group

import (
	"context"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// parallelLeaf records the complete candidate/interface request a group forwards to its selected
// member. It is the real ParallelNetworkDialer surface, not a helper the group calls directly.
type parallelLeaf struct {
	adapter.Outbound
	tag string

	calls     atomic.Int32
	addresses []netip.Addr
	strategy  *constant.NetworkStrategy
}

func (o *parallelLeaf) Type() string      { return "parallel-test" }
func (o *parallelLeaf) Tag() string       { return o.tag }
func (o *parallelLeaf) Network() []string { return []string{N.NetworkTCP, N.NetworkUDP} }

func (o *parallelLeaf) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return &trackedConn{}, nil
}

func (o *parallelLeaf) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return &stubPacketConn{}, nil
}

func (o *parallelLeaf) DialParallelNetwork(_ context.Context, _ string, _ M.Socksaddr, addresses []netip.Addr, strategy *constant.NetworkStrategy, _ []constant.InterfaceType, _ []constant.InterfaceType, _ time.Duration) (net.Conn, error) {
	o.calls.Add(1)
	o.addresses = append([]netip.Addr(nil), addresses...)
	o.strategy = strategy
	return &trackedConn{}, nil
}

func (o *parallelLeaf) ListenSerialNetworkPacket(_ context.Context, _ M.Socksaddr, addresses []netip.Addr, strategy *constant.NetworkStrategy, _ []constant.InterfaceType, _ []constant.InterfaceType, _ time.Duration) (net.PacketConn, netip.Addr, error) {
	o.calls.Add(1)
	o.addresses = append([]netip.Addr(nil), addresses...)
	o.strategy = strategy
	return &stubPacketConn{}, addresses[0], nil
}

// TestSelectorForwardsTheCandidateListThroughNestedGroups is the product-path assertion: the outer
// selector selects the inner selector once, and the inner selector forwards the complete list and
// strategy to the real parallel leaf. A group that implemented only DialContext would silently
// serialize the candidates; a group that selected per candidate would call the leaf more than once.
func TestSelectorForwardsTheCandidateListThroughNestedGroups(t *testing.T) {
	leaf := &parallelLeaf{tag: "leaf"}
	manager := &taggedOutboundManager{byTag: map[string]adapter.Outbound{"leaf": leaf}}
	ctx := service.ContextWith[adapter.OutboundManager](context.Background(), manager)

	innerCreated, err := NewSelector(ctx, nil, log.NewNOPFactory().NewLogger("inner"), "inner", option.SelectorOutboundOptions{
		Outbounds: []string{"leaf"},
		Default:   "leaf",
	})
	require.NoError(t, err)
	inner := innerCreated.(*Selector)
	require.NoError(t, inner.Start(adapter.StartStateStart, &adapter.Scope{}))

	manager.byTag["inner"] = inner
	outerCreated, err := NewSelector(ctx, nil, log.NewNOPFactory().NewLogger("outer"), "outer", option.SelectorOutboundOptions{
		Outbounds: []string{"inner"},
		Default:   "inner",
	})
	require.NoError(t, err)
	outer := outerCreated.(*Selector)
	require.NoError(t, outer.Start(adapter.StartStateStart, &adapter.Scope{}))

	addresses := []netip.Addr{netip.MustParseAddr("2001:db8::1"), netip.MustParseAddr("192.0.2.1")}
	strategy := constant.NetworkStrategyFallback
	conn, err := outer.DialParallelNetwork(context.Background(), N.NetworkTCP, M.ParseSocksaddrHostPort("example.com", 443), addresses, &strategy, nil, nil, 0)
	require.NoError(t, err)
	require.NotNil(t, conn)
	require.Equal(t, int32(1), leaf.calls.Load(), "the outer group must make one selection and one leaf call")
	require.Equal(t, addresses, leaf.addresses, "the full candidate list must survive both group boundaries")
	require.Equal(t, &strategy, leaf.strategy, "the interface strategy must survive both group boundaries")

	packetConn, resolvedAddress, err := outer.ListenSerialNetworkPacket(context.Background(), M.ParseSocksaddrHostPort("example.com", 443), addresses, &strategy, nil, nil, 0)
	require.NoError(t, err)
	require.NotNil(t, packetConn)
	require.Equal(t, addresses[0], resolvedAddress)
	require.Equal(t, int32(2), leaf.calls.Load(), "the UDP path must also reach the parallel leaf once")

	loadBalanceCreated, err := NewLoadBalance(ctx, nil, log.NewNOPFactory().NewLogger("lb"), "lb", option.LoadBalanceOutboundOptions{
		Outbounds: []string{"inner"},
	})
	require.NoError(t, err)
	loadBalance := loadBalanceCreated.(*LoadBalance)
	require.NoError(t, loadBalance.Start(adapter.StartStateStart, &adapter.Scope{}))

	conn, err = loadBalance.DialParallelNetwork(context.Background(), N.NetworkTCP, M.ParseSocksaddrHostPort("example.com", 443), addresses, &strategy, nil, nil, 0)
	require.NoError(t, err)
	require.NotNil(t, conn)
	require.Equal(t, int32(3), leaf.calls.Load(), "loadbalance must select once and forward the same candidates")
}
