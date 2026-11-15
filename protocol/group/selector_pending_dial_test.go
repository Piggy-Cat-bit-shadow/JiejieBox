package group

// Switching a node while a dial is still in flight.
//
// The interrupt group has always covered ESTABLISHED connections. A dial that is still inside
// member.DialContext is not a connection yet, so a switch left it running against the member the
// user just left. When that member's path is broken (the incident: a TrustTunnel CONNECT that never
// answers), the request keeps the old path and the switch looks like it did nothing.
//
// These tests pin the new boundary: a selection change cancels in-flight setup, while established
// connections keep obeying interrupt_exist_connections.

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/interrupt"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// blockingMember accepts a dial and holds it until its setup context is cancelled, which is the
// shape of a member whose path stopped answering.
type blockingMember struct {
	adapter.Outbound
	tag     string
	started chan struct{}
	once    sync.Once
	dials   atomic.Int32
}

func newBlockingMember(tag string) *blockingMember {
	return &blockingMember{tag: tag, started: make(chan struct{})}
}

func (o *blockingMember) Type() string      { return "stub" }
func (o *blockingMember) Tag() string       { return o.tag }
func (o *blockingMember) Network() []string { return []string{N.NetworkTCP, N.NetworkUDP} }

func (o *blockingMember) DialContext(ctx context.Context, _ string, _ M.Socksaddr) (net.Conn, error) {
	o.dials.Add(1)
	o.once.Do(func() { close(o.started) })
	<-ctx.Done()
	return nil, context.Cause(ctx)
}

func (o *blockingMember) ListenPacket(ctx context.Context, _ M.Socksaddr) (net.PacketConn, error) {
	return nil, o.dialError(ctx)
}

func (o *blockingMember) dialError(ctx context.Context) error {
	o.once.Do(func() { close(o.started) })
	<-ctx.Done()
	return context.Cause(ctx)
}

func newSelectorMembers(t *testing.T, interruptExternal bool, members map[string]adapter.Outbound, tags ...string) *Selector {
	t.Helper()
	manager := &taggedOutboundManager{byTag: members}
	ctx := service.ContextWith[adapter.OutboundManager](context.Background(), manager)
	created, err := NewSelector(ctx, nil, log.NewNOPFactory().NewLogger("selector"), "group", option.SelectorOutboundOptions{
		Outbounds:                 tags,
		Default:                   tags[0],
		InterruptExistConnections: interruptExternal,
	})
	require.NoError(t, err)
	selector, isSelector := created.(*Selector)
	require.True(t, isSelector)
	require.NoError(t, selector.Start(adapter.StartStateStart, &adapter.Scope{}))
	return selector
}

func TestSelectorSwitchCancelsThePendingDial(t *testing.T) {
	t.Parallel()
	blocking := newBlockingMember("node-a")
	nodeB := &trackingOutbound{tag: "node-b"}
	selector := newSelectorMembers(t, false, map[string]adapter.Outbound{
		"node-a": blocking,
		"node-b": nodeB,
	}, "node-a", "node-b")

	result := make(chan error, 1)
	go func() {
		conn, err := selector.DialContext(context.Background(), N.NetworkTCP, M.ParseSocksaddr("93.184.216.34:443"))
		if err == nil {
			_ = conn.Close()
		}
		result <- err
	}()
	select {
	case <-blocking.started:
	case <-time.After(3 * time.Second):
		t.Fatal("the blocking member never saw the dial")
	}

	require.True(t, selector.SelectOutbound("node-b"))
	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled,
			"a selection change must cancel the dial that was still bound to the old member")
	case <-time.After(3 * time.Second):
		t.Fatal("the pending dial was not cancelled by the switch")
	}

	// The new selection is used for the next conn, and it is not blocked by the cancelled dial.
	conn, err := selector.DialContext(context.Background(), N.NetworkTCP, M.ParseSocksaddr("93.184.216.34:443"))
	require.NoError(t, err)
	require.NoError(t, conn.Close())
	require.NotNil(t, nodeB.lastConn(), "the new dial did not go to the selected member")
	require.EqualValues(t, 1, blocking.dials.Load(), "the old member was dialed more than once")
}

func TestSelectorSwitchKeepsEstablishedConnectionsWhileCancellingSetup(t *testing.T) {
	t.Parallel()
	nodeA := &trackingOutbound{tag: "node-a"}
	nodeB := &trackingOutbound{tag: "node-b"}
	selector := newSelectorMembers(t, false, map[string]adapter.Outbound{
		"node-a": nodeA,
		"node-b": nodeB,
	}, "node-a", "node-b")

	externalCtx := interrupt.ContextWithIsExternalConnection(context.Background(), true)
	established, err := selector.DialContext(externalCtx, N.NetworkTCP, M.ParseSocksaddr("93.184.216.34:443"))
	require.NoError(t, err)
	defer established.Close()

	require.True(t, selector.SelectOutbound("node-b"))
	require.False(t, nodeA.lastConn().closed.Load(),
		"interrupt_exist_connections=false must keep the established flow")
}

// TestURLTestSwitchCancelsThePendingDial is the automatic-selection half: a latency round commits a
// new member while a dial is still waiting on the old one, and the old dial must not keep the dead
// path occupied.
func TestURLTestSwitchCancelsThePendingDial(t *testing.T) {
	blocking := newBlockingMember("node-a")
	nodeB := &stubOutbound{tag: "node-b"}
	group, storage := newGroupFixture(t, "https://probe.example/generate_204", blocking, nodeB)
	group.selected.Store(&selectedState{tcp: blocking, udp: blocking})
	wrapper := newTestURLTestWrapper(group, log.NewNOPFactory().NewLogger("urltest"))

	result := make(chan error, 1)
	go func() {
		conn, err := wrapper.DialContext(context.Background(), N.NetworkTCP, M.ParseSocksaddr("93.184.216.34:443"))
		if err == nil {
			_ = conn.Close()
		}
		result <- err
	}()
	select {
	case <-blocking.started:
	case <-time.After(3 * time.Second):
		t.Fatal("the blocking member never saw the dial")
	}

	// Publish measurements that select node-b, then run the same update path a finished round uses.
	now := time.Now()
	storage.StoreURLTestHistoryFor("node-a", group.scope, &adapter.URLTestHistory{Time: now, Delay: 900})
	storage.StoreURLTestHistoryFor("node-b", group.scope, &adapter.URLTestHistory{Time: now, Delay: 5})
	group.performUpdateCheck()
	selected, exists := group.Select(N.NetworkTCP)
	require.True(t, exists)
	require.Equal(t, "node-b", selected.Tag())

	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(3 * time.Second):
		t.Fatal("the URL test selection change did not cancel the pending dial")
	}

	conn, err := wrapper.DialContext(context.Background(), N.NetworkTCP, M.ParseSocksaddr("93.184.216.34:443"))
	require.NoError(t, err)
	require.NoError(t, conn.Close())
	require.EqualValues(t, 1, nodeB.dialCount())
}
