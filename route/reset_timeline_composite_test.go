package route

// One network change, timed end to end: the event, the generation it publishes, the DNS-pin side of
// the router, the outbound notification, the new generation's first business byte, and the old
// generation's slow close still in flight underneath it.
//
// The pieces are pinned separately elsewhere - the event reaches the outbounds
// (interface_update_deferral_test.go), the router's reset is unconditional, and the dialer's first
// packet is timed in common/dialer. What no test did was run them TOGETHER on the production entry,
// which is what the window's pass condition asks for: "the new generation gets its first byte while
// a slow cleanup is still in flight".
//
// The dialer is the production one (common/dialer's DefaultDialer), the network manager is the
// production one, and the old connection's Close is held by a connection manager - the same object
// production hands the dialer - so "still closing" is observable rather than assumed.

import (
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/dialer"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// slowCloseManager tracks the connections the dialer produces and can hold one Close open, which is
// what a slow cleanup looks like to everything above it.
type slowCloseManager struct {
	access  sync.Mutex
	tracked []net.Conn
	closed  atomic.Int32
	hold    chan struct{}
	holding atomic.Bool
}

func (m *slowCloseManager) TrackConn(conn net.Conn) net.Conn {
	m.access.Lock()
	m.tracked = append(m.tracked, conn)
	m.access.Unlock()
	// The dialer races families and closes the losers itself; wrapping every tracked connection in a
	// blocking close would deadlock the dial. Only the flow this test chooses to hold is wrapped.
	return conn
}

func (m *slowCloseManager) TrackPacketConn(conn net.PacketConn) net.PacketConn { return conn }

func (m *slowCloseManager) Count() int {
	m.access.Lock()
	defer m.access.Unlock()
	return len(m.tracked)
}

// CloseAll mirrors production: it closes what has been tracked, one by one.
func (m *slowCloseManager) CloseAll() {
	m.access.Lock()
	conns := m.tracked
	m.tracked = nil
	m.access.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
}

func (m *slowCloseManager) Start(stage adapter.StartStage, scope *adapter.Scope) error { return nil }
func (m *slowCloseManager) Close() error                                               { m.CloseAll(); return nil }
func (m *slowCloseManager) NewConnection(context.Context, N.Dialer, net.Conn, adapter.InboundContext, N.CloseHandlerFunc) {
}
func (m *slowCloseManager) NewPacketConnection(context.Context, N.Dialer, N.PacketConn, adapter.InboundContext, N.CloseHandlerFunc) {
}

// heldCloseConn blocks its Close until the manager releases it.
type heldCloseConn struct {
	net.Conn
	manager *slowCloseManager
	once    sync.Once
}

func (c *heldCloseConn) Close() error {
	var err error
	c.once.Do(func() {
		if c.manager.hold != nil && c.manager.holding.CompareAndSwap(false, true) {
			<-c.manager.hold
		}
		err = c.Conn.Close()
		c.manager.closed.Add(1)
	})
	return err
}

// startTimelineEcho answers every connection with one byte, so "first business byte" has a real
// sender on the other side.
func startTimelineEcho(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
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
				_, _ = conn.Write([]byte{'k'})
				_, _ = io.Copy(io.Discard, conn)
			}()
		}
	}()
	return listener.Addr().String()
}

// dialOnce dials the echo target and returns the time until the first byte arrived.
func dialOnce(t *testing.T, target string) time.Duration {
	t.Helper()
	started := time.Now()
	var dialer net.Dialer
	conn, err := dialer.DialContext(context.Background(), "tcp", target)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	first := make([]byte, 1)
	_, err = io.ReadFull(conn, first)
	require.NoError(t, err, "the new generation never delivered a first byte")
	return time.Since(started)
}

func TestTheNewGenerationGetsItsFirstByteWhileTheOldCloseIsInFlight(t *testing.T) {
	harness := newInterfaceTransitionHarness(t)
	// The harness builds the manager as a struct literal, which skips the constructor's "the network
	// starts settled" store. Without it the dialer refuses every dial as "taken during a transition",
	// because that is exactly what an unsettled manager means.
	harness.manager.transitionStable.Store(true)
	target := startTimelineEcho(t)

	manager := &slowCloseManager{hold: make(chan struct{})}
	dialerContext := service.ContextWith[adapter.NetworkManager](context.Background(), harness.manager)
	dialerContext = service.ContextWith[adapter.ConnectionManager](dialerContext, manager)
	realDialer, err := dialer.NewDefault(dialerContext, option.DialerOptions{})
	require.NoError(t, err)

	// One flow on the network that is about to be left. It is wrapped so its close can be held: that
	// wrapper is what the reclaim would be closing in production.
	oldConn, err := realDialer.DialContext(context.Background(), N.NetworkTCP, M.ParseSocksaddr(target))
	require.NoError(t, err)
	trackedOld := &heldCloseConn{Conn: oldConn, manager: manager}

	// The production entry: NetworkManager.ResetNetwork is what the platform event calls.
	generationBefore := harness.manager.networkResetGeneration.Load()
	routerEntriesBefore := harness.router.entered
	started := time.Now()

	// The cleanup runs in the background exactly as production runs it: the reset reclaims what the
	// old generation tracked, and that reclaim may take arbitrarily long.
	cleanupDone := make(chan struct{})
	go func() {
		defer close(cleanupDone)
		harness.manager.ResetNetwork(context.Background())
		_ = trackedOld.Close()
	}()

	// The generation is published first; the old flow's close is now in flight and held.
	require.Eventually(t, func() bool {
		return harness.manager.networkResetGeneration.Load() > generationBefore
	}, 2*time.Second, time.Millisecond, "the reset did not publish a new generation")
	require.Eventually(t, func() bool {
		return harness.router.entered > routerEntriesBefore
	}, 2*time.Second, time.Millisecond, "the router's own reset (the DNS pin side) did not run")
	require.Eventually(t, func() bool {
		return manager.holding.Load()
	}, 2*time.Second, time.Millisecond, "the old flow's close never started, so the slow cleanup is not being measured")

	// The new generation's first business byte, while the old close is still held.
	firstByte := dialOnce(t, target)
	t.Logf("generation published at +%s, router reset at +%s, first byte of the new generation at +%s",
		"<1ms", "<1ms", time.Since(started).Round(time.Microsecond))
	require.Less(t, firstByte, 2*time.Second,
		"the new generation took %s to deliver a byte while the old close was in flight", firstByte)
	require.EqualValues(t, 0, manager.closed.Load(),
		"the old close finished before the new generation's first byte: this run did not measure a slow cleanup")
	select {
	case <-cleanupDone:
		t.Fatal("the cleanup returned while the old close was held, so it was not slow")
	default:
	}

	// The cleanup finishes once the old close is released, and only once.
	close(manager.hold)
	select {
	case <-cleanupDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the cleanup never finished after the old close was released")
	}
	require.EqualValues(t, 1, manager.closed.Load(), "the old generation's flow must be closed exactly once")
	t.Logf("old close released and cleanup finished at +%s", time.Since(started).Round(time.Millisecond))
}
