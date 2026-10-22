package route

import (
	"context"
	"net"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/trafficcontrol"
	"github.com/sagernet/sing-tun/gtcpip/header"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/bufio"
	"github.com/sagernet/sing/common/bufio/deadline"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// Adversarial coverage for the half-close observation and the teardown it shares with both copy
// directions.
//
// The task these tests answer: attack the first round's fix instead of confirming it. Each test
// either
//
//   - proves the release reaches the TRANSPORT (route count, peer-visible EOF), not just the traffic
//     table - the difference between "the list looks clean" and "the socket is closed";
//   - proves a legitimate shape is not killed (slow response, healthy idle, reverse close order);
//   - or proves the mechanism converges over repeated cycles and leaves no copy goroutine behind.

// adversarialTargetMode is how the loopback peer behaves after the handshake.
type adversarialTargetMode int

const (
	// adversarialSilent answers nothing and never closes: the server that simply waits.
	adversarialSilent adversarialTargetMode = iota
	// adversarialCloseWriteAfterEcho sends its FIN right after echoing the first request, which is
	// the reverse close order: the server finishes first and the client is still writing.
	adversarialCloseWriteAfterEcho
	// adversarialDelayedResponse answers the request long after the client half-closed.
	adversarialDelayedResponse
)

// adversarialTarget is a loopback peer whose behaviour the test names, and which records whether it
// saw its peer's side of the connection close. The last part is what makes "the socket was really
// closed" observable: a table entry can be deleted without anything being closed, and a peer that
// never sees EOF is how that shows up.
type adversarialTarget struct {
	access   sync.Mutex
	listener net.Listener
	conns    []net.Conn
	sawEOF   atomic.Bool
	mode     adversarialTargetMode
	delay    time.Duration
}

func startAdversarialTarget(t *testing.T, mode adversarialTargetMode, delay time.Duration) *adversarialTarget {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	target := &adversarialTarget{listener: listener, mode: mode, delay: delay}
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			target.access.Lock()
			target.conns = append(target.conns, conn)
			target.access.Unlock()
			go target.serve(conn)
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		target.access.Lock()
		for _, conn := range target.conns {
			conn.Close()
		}
		target.access.Unlock()
	})
	return target
}

func (t *adversarialTarget) port() uint16 {
	return uint16(t.listener.Addr().(*net.TCPAddr).Port)
}

func (t *adversarialTarget) sawPeerClose() bool { return t.sawEOF.Load() }

func (t *adversarialTarget) serve(conn net.Conn) {
	buffer := make([]byte, 4096)
	echoed := false
	for {
		n, readErr := conn.Read(buffer)
		if n > 0 {
			_, _ = conn.Write(buffer[:n])
			if !echoed && t.mode == adversarialCloseWriteAfterEcho {
				echoed = true
				// The server finishes its write side while the client is still connected.
				_ = conn.(*net.TCPConn).CloseWrite()
			}
		}
		if readErr != nil {
			// The client finished its write side (or the socket errors). Record it, then behave the
			// way this target is configured to behave.
			t.sawEOF.Store(true)
			switch t.mode {
			case adversarialDelayedResponse:
				time.Sleep(t.delay)
				_, _ = conn.Write([]byte("late-response"))
				// A server that has nothing left to say closes; that is what ends the flow here.
				_ = conn.Close()
			case adversarialSilent:
				// Never answer, never close: only the test's cleanup does.
				return
			}
			return
		}
	}
}

// routeCount is the connection manager's own count: the transport side, as opposed to the traffic
// table.
func (f *tunHalfCloseFixture) routeCount() int { return f.manager.Count() }

func (f *tunHalfCloseFixture) waitRoute(t *testing.T, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	last := -1
	for time.Now().Before(deadline) {
		last = f.manager.Count()
		if last == want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("the connection manager holds %d connections, want %d", last, want)
}

// ackDevice keeps acknowledging everything the stack sends for the flow, which is what a device
// whose socket still exists does and what a device whose socket is gone cannot do.
func (f *tunHalfCloseFixture) ackDevice(t *testing.T, sourcePort, destinationPort uint16, deviceSeq, stackSeq uint32) (stop func()) {
	t.Helper()
	done := make(chan struct{})
	var once sync.Once
	go func() {
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
			}
			frames := f.framesSince(f.framesSeen)
			for _, frame := range frames {
				tcp, ok := tunProbeTCP(frame)
				if !ok || tcp.SourcePort() != destinationPort || tcp.DestinationPort() != sourcePort {
					continue
				}
				if _, err := f.device.WritePackets([][]byte{
					tunProbeTCPFrame(sourcePort, destinationPort, deviceSeq, stackSeq, header.TCPFlagAck, nil),
				}); err != nil {
					return
				}
			}
			f.access.Lock()
			f.framesSeen = len(f.frames)
			f.access.Unlock()
		}
	}()
	return func() { once.Do(func() { close(done) }) }
}

// TestTUNHalfClosedPeerGoneClosesTheTransport is the release test with the two assertions that
// separate a real fix from a map cleanup: the ROUTE manager must drop the connection, and the peer
// must observe the close.
func TestTUNHalfClosedPeerGoneClosesTheTransport(t *testing.T) {
	target := startAdversarialTarget(t, adversarialSilent, 0)
	fixture := newTUNHalfCloseFixture(t, tunProbeAddr(target.port()))
	deviceNext, stackNext := fixture.establish(t, 40100, target.port())
	fixture.waitTraffic(t, 1, 3*time.Second)
	fixture.waitRoute(t, 1, 3*time.Second)

	fixture.emit(t, tunProbeTCPFrame(40100, target.port(), deviceNext, stackNext, header.TCPFlagFin|header.TCPFlagAck, nil))

	// The observation releases it, and that release must close the socket, not only the entry.
	fixture.waitTraffic(t, 0, 10*time.Second)
	fixture.waitRoute(t, 0, 5*time.Second)
	require.True(t, target.sawPeerClose(),
		"the peer must observe the connection close; an entry removed without closing the socket is "+
			"the failure this assertion exists to catch")
	require.Equal(t, int64(1), fixture.handler.closed.Load(), "onClose must run exactly once")
}

// TestTUNHalfClosedSlowResponseIsNotKilled is the false-positive guard against the fix itself.
//
// The client half-closes and the server answers 2s later, far beyond the observation's keepalive
// idle: the device is alive, so it answers the probes, and the flow must stay up until the response
// is delivered and the server closes.
func TestTUNHalfClosedSlowResponseIsNotKilled(t *testing.T) {
	target := startAdversarialTarget(t, adversarialDelayedResponse, 2*time.Second)
	fixture := newTUNHalfCloseFixture(t, tunProbeAddr(target.port()))
	deviceNext, stackNext := fixture.establish(t, 40101, target.port())
	fixture.waitTraffic(t, 1, 3*time.Second)

	fixture.emit(t, tunProbeTCPFrame(40101, target.port(), deviceNext, stackNext, header.TCPFlagFin|header.TCPFlagAck, nil))
	deviceNext++
	stopAcks := fixture.ackDevice(t, 40101, target.port(), deviceNext, stackNext)
	defer stopAcks()

	// Long enough for many probe rounds at the test timings.
	time.Sleep(2 * time.Second)
	require.Equal(t, int64(0), fixture.handler.closed.Load(),
		"a slow but legitimate response must not be released as if the peer were gone")
	require.Equal(t, 1, fixture.traffic.ConnectionsLen())

	// And the late response actually arrives: the surviving direction was never harmed.
	fixture.waitForPayload(t, 40101, target.port(), 5*time.Second)

	// The server closes after answering, which is what ends the flow.
	fixture.waitTraffic(t, 0, 10*time.Second)
	require.Equal(t, int64(1), fixture.handler.closed.Load())
}

// TestTUNServerFinFirstThenDeviceFinReleases is the reverse close order: the download direction
// finishes first, so the upload direction is the one that runs the teardown.
func TestTUNServerFinFirstThenDeviceFinReleases(t *testing.T) {
	target := startAdversarialTarget(t, adversarialCloseWriteAfterEcho, 0)
	fixture := newTUNHalfCloseFixture(t, tunProbeAddr(target.port()))
	deviceNext, stackNext := fixture.establish(t, 40102, target.port())
	fixture.waitTraffic(t, 1, 3*time.Second)

	// The server's FIN reached the device as a FIN; the device keeps uploading for a moment, and
	// that data must still reach the peer (the reverse order must not be mistaken for a dead flow).
	time.Sleep(200 * time.Millisecond)
	upload := []byte("still-uploading")
	fixture.emit(t, tunProbeTCPFrame(40102, target.port(), deviceNext, stackNext, header.TCPFlagAck|header.TCPFlagPsh, upload))
	deviceNext += uint32(len(upload))
	fixture.waitForPayload(t, 40102, target.port(), 5*time.Second)

	// The device now finishes its own side.
	fixture.emit(t, tunProbeTCPFrame(40102, target.port(), deviceNext, stackNext, header.TCPFlagFin|header.TCPFlagAck, nil))
	fixture.waitTraffic(t, 0, 10*time.Second)
	fixture.waitRoute(t, 0, 5*time.Second)
	require.Equal(t, int64(1), fixture.handler.closed.Load())
}

// TestTUNServerFinFirstThenDeviceResetReleases is the same order with an abort instead of a FIN.
func TestTUNServerFinFirstThenDeviceResetReleases(t *testing.T) {
	target := startAdversarialTarget(t, adversarialCloseWriteAfterEcho, 0)
	fixture := newTUNHalfCloseFixture(t, tunProbeAddr(target.port()))
	deviceNext, stackNext := fixture.establish(t, 40103, target.port())
	fixture.waitTraffic(t, 1, 3*time.Second)

	time.Sleep(200 * time.Millisecond)
	fixture.emit(t, tunProbeTCPFrame(40103, target.port(), deviceNext, stackNext, header.TCPFlagRst|header.TCPFlagAck, nil))
	fixture.waitTraffic(t, 0, 10*time.Second)
	fixture.waitRoute(t, 0, 5*time.Second)
}

// TestTUNHealthyIdleIsNeverObserved is the long-lived shape (WebSocket, SSH, gRPC stream): no FIN,
// nothing to say for much longer than the observation's keepalive idle, and still usable afterwards.
func TestTUNHealthyIdleIsNeverObserved(t *testing.T) {
	target := startAdversarialTarget(t, adversarialSilent, 0)
	fixture := newTUNHalfCloseFixture(t, tunProbeAddr(target.port()))
	deviceNext, stackNext := fixture.establish(t, 40104, target.port())
	fixture.waitTraffic(t, 1, 3*time.Second)

	stopAcks := fixture.ackDevice(t, 40104, target.port(), deviceNext, stackNext)
	defer stopAcks()
	time.Sleep(900 * time.Millisecond)

	snapshot := fixture.manager.HalfCloseDiagnostics()
	require.Equal(t, uint64(0), snapshot.Observed,
		"a connection that has not half-closed must never be observed, whatever it is doing")
	require.Equal(t, uint64(0), snapshot.Released)
	require.Equal(t, 1, fixture.traffic.ConnectionsLen())

	// Still a working connection in both directions.
	payload := []byte("still-alive")
	fixture.emit(t, tunProbeTCPFrame(40104, target.port(), deviceNext, stackNext, header.TCPFlagAck|header.TCPFlagPsh, payload))
	deviceNext += uint32(len(payload))
	fixture.waitForPayload(t, 40104, target.port(), 5*time.Second)
	require.Equal(t, int64(0), fixture.handler.closed.Load())
}

// TestTUNTrafficCloseAllClosesTheSocket answers the standing suspicion about the user's own
// workaround: does "Close All Connections" only drop the statistics entry, or does the socket really
// close? The traffic tracker wraps the connection, so closing it closes the socket - and this test
// requires the peer to observe that.
func TestTUNTrafficCloseAllClosesTheSocket(t *testing.T) {
	for _, trackOutbound := range []bool{false, true} {
		name := "userspace-copy"
		if trackOutbound {
			name = "splice-eligible"
		}
		t.Run(name, func(t *testing.T) {
			target := startAdversarialTarget(t, adversarialSilent, 0)
			fixture := newTUNHalfCloseFixtureTracked(t, tunProbeAddr(target.port()), trackOutbound)
			_, _ = fixture.establish(t, 40105, target.port())
			fixture.waitTraffic(t, 1, 3*time.Second)
			if trackOutbound {
				// Only a registered outbound socket is one the route manager holds; the userspace
				// copy variant has nothing in that list by construction, and its evidence is the
				// peer-visible close below.
				fixture.waitRoute(t, 1, 3*time.Second)
			}

			// The traffic manager's own close, i.e. one half of what the user's button does.
			fixture.traffic.CloseAllConnections()
			fixture.waitTraffic(t, 0, 5*time.Second)
			fixture.waitRoute(t, 0, 5*time.Second)
			require.Eventually(t, target.sawPeerClose, 3*time.Second, 5*time.Millisecond,
				"closing the tracker must close the socket; a peer that never sees EOF is the "+
					"\"list looks clean, resource still open\" failure")
		})
	}
}

// TestTUNHalfCloseCyclesConverge is the resource half: repeated natural releases must leave no copy
// goroutine, no route entry and no traffic entry behind.
func TestTUNHalfCloseCyclesConverge(t *testing.T) {
	target := startAdversarialTarget(t, adversarialSilent, 0)
	fixture := newTUNHalfCloseFixture(t, tunProbeAddr(target.port()))
	// The baseline is the fixture AT REST: the stack owns engine goroutines of its own, and counting
	// them as growth would hide the number this test is about (per-cycle leakage).
	baselineGoroutines := runtime.NumGoroutine()
	const cycles = 8
	sourcePort := uint16(40200)
	for cycle := 0; cycle < cycles; cycle++ {
		deviceNext, stackNext := fixture.establish(t, sourcePort+uint16(cycle), target.port())
		fixture.waitTraffic(t, 1, 3*time.Second)
		fixture.emit(t, tunProbeTCPFrame(sourcePort+uint16(cycle), target.port(), deviceNext, stackNext, header.TCPFlagFin|header.TCPFlagAck, nil))
		fixture.waitTraffic(t, 0, 10*time.Second)
	}
	fixture.waitRoute(t, 0, 5*time.Second)

	snapshot := fixture.manager.HalfCloseDiagnostics()
	require.Equal(t, uint64(cycles), snapshot.Observed, "every flow must have been observed")
	require.Equal(t, uint64(cycles), snapshot.Released, "every flow must have been released")

	require.Eventually(t, func() bool {
		return copyGoroutines() == 0
	}, 5*time.Second, 20*time.Millisecond,
		"a released flow must not leave a copy goroutine behind")

	// Goroutine convergence as a coarse resource check. The slack absorbs the observation's own
	// transient timer callbacks; the meaningful number is that nothing per-cycle accumulates.
	require.Eventually(t, func() bool {
		return runtime.NumGoroutine() <= baselineGoroutines+8
	}, 5*time.Second, 20*time.Millisecond,
		"goroutines must return to (nearly) the baseline: %d at start, %d after %d cycles",
		baselineGoroutines, runtime.NumGoroutine(), cycles)
	t.Logf("goroutines: baseline %d, after %d cycles %d; traffic active %d, route %d",
		baselineGoroutines, cycles, runtime.NumGoroutine(), fixture.traffic.ConnectionsLen(), fixture.manager.Count())
}

// copyGoroutines counts goroutines currently inside the stream copy loop.
func copyGoroutines() int {
	buffer := make([]byte, 1<<20)
	length := runtime.Stack(buffer, true)
	return strings.Count(string(buffer[:length]), "(*ConnectionManager).connectionCopy")
}

// TestKeepAliveWalkReachesTheSocketThroughTheSniffCache pins the capability walk against the real
// wrapper chain of a SNIFFED connection, which is the shape the report is about.
//
// The chain is the production one, in the production order: route/route.go wraps a sniffed stream in
// bufio.CachedConn BEFORE the traffic tracker wraps it, and the deadline wrapper sits under both. The
// walk was suspected of stopping at the cache - bufio.CachedConn embeds net.Conn rather than
// advertising replaceability, so it looked like a wall - and this test disproved that: CachedConn
// does implement Upstream(), and the capability is reachable through the whole chain. The test stays
// because a future wrapper in that position without an upstream link would silently make the whole
// observation inert for exactly the labelled connections, and nothing else would notice.
func TestKeepAliveWalkReachesTheSocketThroughTheSniffCache(t *testing.T) {
	socket := &walkKeepAliveConn{}
	cachedBuffer := buf.NewSize(4)
	_, _ = cachedBuffer.Write([]byte("http"))
	cached := bufio.NewCachedConn(deadline.NewConn(socket), cachedBuffer)
	counted := bufio.NewInt64CounterConn(cached, []*atomic.Int64{new(atomic.Int64)}, []*atomic.Int64{new(atomic.Int64)})
	manager := trafficcontrol.NewManager()
	tracked := manager.RoutedConnection(context.Background(), counted, adapter.InboundContext{
		Destination:   M.ParseSocksaddrHostPort("peer.example", 443),
		OutboundChain: []adapter.Outbound{walkProbeOutbound{}},
	}, nil, walkProbeOutbound{})

	setter := keepAliveConnOf(tracked)
	require.NotNil(t, setter,
		"the walk must reach the socket the stack can probe, through the sniff cache and the tracker")
	require.Same(t, socket, setter)
	require.True(t, terminalReadObservable(setter),
		"and the resolved connection must be one whose read side can report the verdict")
	require.NoError(t, setter.SetKeepAliveConfig(net.KeepAliveConfig{Enable: true, Idle: time.Second}))
	require.Equal(t, 1, socket.configured)
}

// walkKeepAliveConn stands in for the userspace TUN stream: the only object in the chain that can be
// asked for a keepalive.
type walkKeepAliveConn struct {
	net.Conn
	configured int
}

func (c *walkKeepAliveConn) SetKeepAliveConfig(net.KeepAliveConfig) error {
	c.configured++
	return nil
}

// halfCloseTerminalRead stands in for the userspace stack's read semantics: a later read reports the
// terminal state. A plain kernel socket does not, and that difference is what the two capabilities
// separate.
func (c *walkKeepAliveConn) halfCloseTerminalRead() bool { return true }

type walkProbeOutbound struct{}

func (walkProbeOutbound) Type() string           { return "probe" }
func (walkProbeOutbound) Tag() string            { return "probe" }
func (walkProbeOutbound) Network() []string      { return []string{N.NetworkTCP} }
func (walkProbeOutbound) Dependencies() []string { return nil }

func (walkProbeOutbound) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return nil, net.ErrClosed
}

func (walkProbeOutbound) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, net.ErrClosed
}

// TestTUNRouteCloseAllReleasesTrafficToo is the other half of the user's composite button.
//
// Route.CloseAll only knows the sockets the route manager registered, so this runs on the fixture
// whose outbound socket is registered (the production direct path does register it). Closing them
// must end the copy loops, which must release the traffic entries - and the peer must see it.
func TestTUNRouteCloseAllReleasesTrafficToo(t *testing.T) {
	target := startAdversarialTarget(t, adversarialSilent, 0)
	fixture := newTUNHalfCloseFixtureTracked(t, tunProbeAddr(target.port()), true)
	_, _ = fixture.establish(t, 40106, target.port())
	fixture.waitTraffic(t, 1, 3*time.Second)
	fixture.waitRoute(t, 1, 3*time.Second)

	fixture.manager.CloseAll()
	fixture.waitRoute(t, 0, 5*time.Second)
	fixture.waitTraffic(t, 0, 5*time.Second)
	require.Eventually(t, target.sawPeerClose, 3*time.Second, 5*time.Millisecond,
		"closing the route side must reach the peer, not only the manager's list")
}

// TestTUNSingleConnectionCloseReleasesEverything is what the per-row close in the UI does.
func TestTUNSingleConnectionCloseReleasesEverything(t *testing.T) {
	target := startAdversarialTarget(t, adversarialSilent, 0)
	fixture := newTUNHalfCloseFixtureTracked(t, tunProbeAddr(target.port()), true)
	_, _ = fixture.establish(t, 40107, target.port())
	fixture.waitTraffic(t, 1, 3*time.Second)

	active := fixture.traffic.Connections()
	require.Len(t, active, 1)
	tracker := fixture.traffic.Connection(active[0].ID)
	require.NotNil(t, tracker)
	require.NoError(t, tracker.Close())

	fixture.waitTraffic(t, 0, 5*time.Second)
	fixture.waitRoute(t, 0, 5*time.Second)
	require.Eventually(t, target.sawPeerClose, 3*time.Second, 5*time.Millisecond,
		"closing one tracker must close that connection's socket")
	require.Eventually(t, func() bool { return fixture.handler.closed.Load() == 1 }, 3*time.Second, 5*time.Millisecond,
		"onClose must run exactly once for the closed connection")
}

// plainOutboundConn hides everything optional about a connection: no keepalive capability, no
// upstream link, no splice capability. It is what a multiplexed proxy outbound looks like from the
// route layer, where the stream is an object the transport owns.
type plainOutboundConn struct {
	net.Conn
}

// CloseWrite is kept: a multiplexed proxy stream has to propagate a half-close, so hiding it would
// make this fixture test a shape that does not exist. What is hidden is everything OPTIONAL - the
// keepalive capability, the upstream link and the splice handover.
func (c *plainOutboundConn) CloseWrite() error {
	// Find the real socket's half-close the way a proxy stream would: through its own transport.
	for conn := any(c.Conn); conn != nil; {
		if closer, isCloser := conn.(interface{ CloseWrite() error }); isCloser {
			return closer.CloseWrite()
		}
		upstream, hasUpstream := conn.(common.WithUpstream)
		if !hasUpstream {
			break
		}
		next := upstream.Upstream()
		if next == conn {
			break
		}
		conn = next
	}
	return c.Conn.Close()
}

type plainOutboundDialer struct {
	target  string
	tracker *ConnectionManager
}

func (d plainOutboundDialer) DialContext(ctx context.Context, _ string, _ M.Socksaddr) (net.Conn, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", d.target)
	if err != nil {
		return nil, err
	}
	return &plainOutboundConn{Conn: d.tracker.TrackConn(conn)}, nil
}

func (d plainOutboundDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, net.ErrClosed
}

// TestTUNHalfClosedReleaseDoesNotDependOnTheOutboundType is the proxy-outbound shape.
//
// The observation is armed on the INBOUND connection - the peer whose socket can disappear - so it
// must not care what the outbound is. This drives the same release with an outbound connection that
// hides every optional capability (as a multiplexed proxy stream does), and requires the release to
// work anyway: the tracker leaves, the route entry goes, the peer observes the close, and the
// observation was armed in the first place.
func TestTUNHalfClosedReleaseDoesNotDependOnTheOutboundType(t *testing.T) {
	target := startAdversarialTarget(t, adversarialSilent, 0)
	fixture := newTUNHalfCloseFixture(t, tunProbeAddr(target.port()))
	fixture.handler.dialer = plainOutboundDialer{target: tunProbeAddr(target.port()), tracker: fixture.manager}

	deviceNext, stackNext := fixture.establish(t, 40108, target.port())
	fixture.waitTraffic(t, 1, 3*time.Second)
	fixture.waitRoute(t, 1, 3*time.Second)

	fixture.emit(t, tunProbeTCPFrame(40108, target.port(), deviceNext, stackNext, header.TCPFlagFin|header.TCPFlagAck, nil))
	fixture.waitTraffic(t, 0, 10*time.Second)
	fixture.waitRoute(t, 0, 5*time.Second)
	require.Eventually(t, target.sawPeerClose, 3*time.Second, 5*time.Millisecond,
		"the release must close the real socket behind the wrapper")
	require.Equal(t, uint64(1), fixture.manager.HalfCloseDiagnostics().Observed,
		"the observation is armed on the inbound side, so a capability-less outbound must not disable it")
}
