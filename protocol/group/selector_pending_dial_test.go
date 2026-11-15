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
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/interrupt"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
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

// echoMember is a HEALTHY member: it dials a real loopback TCP echo target, so the test can prove
// that the connection established after the switch actually carries payload, not merely that a
// dial function returned.
type echoMember struct {
	adapter.Outbound
	tag    string
	target string
}

func (o *echoMember) Type() string      { return "stub" }
func (o *echoMember) Tag() string       { return o.tag }
func (o *echoMember) Network() []string { return []string{N.NetworkTCP, N.NetworkUDP} }

func (o *echoMember) DialContext(ctx context.Context, _ string, _ M.Socksaddr) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(ctx, N.NetworkTCP, o.target)
}

func (o *echoMember) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, E.New("packet is not used by this test")
}

func startEchoTarget(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen(N.NetworkTCP, "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer conn.Close()
				buffer := make([]byte, 256)
				for {
					read, readErr := conn.Read(buffer)
					if read > 0 {
						_, _ = conn.Write(buffer[:read])
					}
					if readErr != nil {
						return
					}
				}
			}()
		}
	}()
	return listener.Addr().String()
}

func echoThrough(t *testing.T, conn net.Conn, payload string) {
	t.Helper()
	require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))
	_, err := conn.Write([]byte(payload))
	require.NoError(t, err)
	echo := make([]byte, len(payload))
	_, err = io.ReadFull(conn, echo)
	require.NoError(t, err)
	require.Equal(t, payload, string(echo), "the connection established after the switch did not carry payload")
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
	nodeB := &echoMember{tag: "node-b", target: startEchoTarget(t)}
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
	echoThrough(t, conn, "after-the-switch")
	require.NoError(t, conn.Close())
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
	nodeB := &echoMember{tag: "node-b", target: startEchoTarget(t)}
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
	echoThrough(t, conn, "after-the-url-test-switch")
	require.NoError(t, conn.Close())
}

// TestSelectorDoesNotDeliverAConnectionThatFinishedAtTheSwitch pins P1-2's exact window: the
// member's dial has returned a connection, but the selector has not handed it to the group yet.
// A switch in that window must close the connection instead of delivering it, even though the
// switch has already scanned the established list.
func TestSelectorDoesNotDeliverAConnectionThatFinishedAtTheSwitch(t *testing.T) {
	nodeA := &trackingOutbound{tag: "node-a"}
	nodeB := &trackingOutbound{tag: "node-b"}
	selector := newSelectorMembers(t, true, map[string]adapter.Outbound{
		"node-a": nodeA,
		"node-b": nodeB,
	}, "node-a", "node-b")

	entered := make(chan struct{})
	release := make(chan struct{})
	testHookAfterMemberDial = func() {
		close(entered)
		<-release
	}
	defer func() { testHookAfterMemberDial = nil }()

	result := make(chan error, 1)
	go func() {
		conn, err := selector.DialContext(context.Background(), N.NetworkTCP, M.ParseSocksaddr("93.184.216.34:443"))
		if err == nil {
			_ = conn.Close()
		}
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("the member dial never reached the hand-off window")
	}

	require.True(t, selector.SelectOutbound("node-b"))
	close(release)
	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled,
			"a connection that finished after the switch was delivered to the caller")
	case <-time.After(3 * time.Second):
		t.Fatal("the dial did not return after the hand-off window closed")
	}
	require.True(t, nodeA.lastConn().closed.Load(),
		"the connection that lost the switch race was not closed")
}

// TestURLTestRevisionAndMemberAreOneSnapshot pins P1-3: the revision must belong to the same
// selection snapshot as the member. A selection change inserted between the revision read and the
// member read must not let the old member run under the new revision.
func TestURLTestRevisionAndMemberAreOneSnapshot(t *testing.T) {
	nodeA := &trackingOutbound{tag: "node-a"}
	nodeB := &trackingOutbound{tag: "node-b"}
	group, _ := newGroupFixture(t, "https://probe.example/generate_204", nodeA, nodeB)
	group.selected.Store(&selectedState{tcp: nodeA, udp: nodeA})
	t.Cleanup(func() { _ = group.Close() })
	wrapper := newTestURLTestWrapper(group, log.NewNOPFactory().NewLogger("urltest"))

	var once sync.Once
	testHookAfterRevisionSnapshot = func() {
		once.Do(func() {
			group.selected.Store(&selectedState{tcp: nodeB, udp: nodeB})
			group.interruptGroup.InterruptSelection(false)
		})
	}
	defer func() { testHookAfterRevisionSnapshot = nil }()

	conn, err := wrapper.DialContext(context.Background(), N.NetworkTCP, M.ParseSocksaddr("93.184.216.34:443"))
	require.NoError(t, err)
	require.NoError(t, conn.Close())
	require.Zero(t, nodeA.dialCount(),
		"the old member was dialed with a revision that belonged to the new selection")
	require.Equal(t, 1, nodeB.dialCount())
}

// TestURLTestClearSelectionCancelsPendingDial covers the clear-selection path: when the selected
// member is dropped after a failure, a dial still waiting on it must stop waiting; established
// flows are not cut by that single failure.
func TestURLTestClearSelectionCancelsPendingDial(t *testing.T) {
	blocking := newBlockingMember("node-a")
	nodeB := &trackingOutbound{tag: "node-b"}
	group, _ := newGroupFixture(t, "https://probe.example/generate_204", blocking, nodeB)
	group.selected.Store(&selectedState{tcp: blocking, udp: blocking})
	t.Cleanup(func() { _ = group.Close() })
	wrapper := newTestURLTestWrapper(group, log.NewNOPFactory().NewLogger("urltest"))

	result := make(chan error, 1)
	go func() {
		_, err := wrapper.DialContext(context.Background(), N.NetworkTCP, M.ParseSocksaddr("93.184.216.34:443"))
		result <- err
	}()
	select {
	case <-blocking.started:
	case <-time.After(3 * time.Second):
		t.Fatal("the blocking member never saw the dial")
	}

	group.clearSelectionFor(N.NetworkTCP, blocking)
	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(3 * time.Second):
		t.Fatal("clearing the selection left the pending dial waiting")
	}
	state := group.selected.Load()
	require.NotNil(t, state)
	require.Nil(t, state.tcp, "the failed member stayed selected")
}

// TestURLTestGroupCloseCancelsPendingDial: a retired group must not keep a setup alive while the
// higher layer waits for the publication barrier.
func TestURLTestGroupCloseCancelsPendingDial(t *testing.T) {
	blocking := newBlockingMember("node-a")
	nodeB := &trackingOutbound{tag: "node-b"}
	group, _ := newGroupFixture(t, "https://probe.example/generate_204", blocking, nodeB)
	group.selected.Store(&selectedState{tcp: blocking, udp: blocking})
	wrapper := newTestURLTestWrapper(group, log.NewNOPFactory().NewLogger("urltest"))

	result := make(chan error, 1)
	go func() {
		_, err := wrapper.DialContext(context.Background(), N.NetworkTCP, M.ParseSocksaddr("93.184.216.34:443"))
		result <- err
	}()
	select {
	case <-blocking.started:
	case <-time.After(3 * time.Second):
		t.Fatal("the blocking member never saw the dial")
	}

	require.NoError(t, group.Close())
	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(3 * time.Second):
		t.Fatal("closing the group left the pending dial waiting")
	}
}

// TestURLTestFirstCommittedSelectionCancelsTemporaryDial covers the first-selection gap: before a
// selection is committed, traffic can be routed to a temporary member via Select(). When the first
// health round commits a different member, a dial still waiting on the temporary one must not keep
// the old path occupied.
func TestURLTestFirstCommittedSelectionCancelsTemporaryDial(t *testing.T) {
	blocking := newBlockingMember("node-a")
	nodeB := &trackingOutbound{tag: "node-b"}
	group, storage := newGroupFixture(t, "https://probe.example/generate_204", blocking, nodeB)
	t.Cleanup(func() { _ = group.Close() })
	wrapper := newTestURLTestWrapper(group, log.NewNOPFactory().NewLogger("urltest"))

	// Only node-a has evidence, so the temporary Select() picks it.
	storage.StoreURLTestHistoryFor("node-a", group.scope, &adapter.URLTestHistory{Time: time.Now(), Delay: 900})

	result := make(chan error, 1)
	go func() {
		_, err := wrapper.DialContext(context.Background(), N.NetworkTCP, M.ParseSocksaddr("93.184.216.34:443"))
		result <- err
	}()
	select {
	case <-blocking.started:
	case <-time.After(3 * time.Second):
		t.Fatal("the blocking temporary member never saw the dial")
	}

	// Now node-b has better evidence; the first commit selects it (previous selection was nil).
	storage.StoreURLTestHistoryFor("node-b", group.scope, &adapter.URLTestHistory{Time: time.Now(), Delay: 5})
	group.performUpdateCheck()
	selected, exists := group.Select(N.NetworkTCP)
	require.True(t, exists)
	require.Equal(t, "node-b", selected.Tag())

	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(3 * time.Second):
		t.Fatal("the first committed selection left the temporary dial waiting")
	}

	conn, err := wrapper.DialContext(context.Background(), N.NetworkTCP, M.ParseSocksaddr("93.184.216.34:443"))
	require.NoError(t, err)
	require.NoError(t, conn.Close())
	require.Equal(t, 1, nodeB.dialCount())
}
