package route

import (
	"context"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// Tests for the half-close liveness observer (conn_halfclose.go).
//
// # What these pin, and what they deliberately do not
//
// The observer exists because one state had no owner: a stream whose upload direction has finished
// cleanly and whose download direction is still waiting has NOBODY reading the finished side any
// more, so a peer whose socket disappears afterwards is never noticed and the flow is held until
// close-all.
//
// These tests use a scripted connection whose read side is the one thing under control: it returns
// EOF (a live, half-closed peer) or a terminal error (a peer whose socket is gone). That is exactly
// the distinction the fix depends on, and it is the distinction that must not be inferred from
// silence:
//
//	EOF forever        -> the peer is still there and simply has not answered yet: NOTHING happens
//	terminal error     -> the peer is gone: the flow is released
//
// The real userspace TUN stack's own keepalive is exercised separately, in tun_halfclose_test.go,
// because the scripted connection cannot prove that a keepalive probe turns a vanished peer into a
// terminal error.

// halfCloseTestTimings are short enough for a test and keep their relative order.
var halfCloseTestTimings = halfCloseOptions{
	keepAliveIdle:     5 * time.Millisecond,
	keepAliveInterval: 5 * time.Millisecond,
	probeInterval:     10 * time.Millisecond,
}

// scriptedConn is the finished side: the upload copy reads EOF from it once, and every later read -
// the observer's - returns whatever the test has armed.
type scriptedConn struct {
	access       sync.Mutex
	reads        int
	nextErr      error
	nextBytes    int
	configErr    error
	closed       atomic.Bool
	halfClosed   atomic.Bool
	keepAliveSet atomic.Int32
	keepAlive    net.KeepAliveConfig
}

func newScriptedConn() *scriptedConn { return &scriptedConn{} }

func (c *scriptedConn) setNextReadErr(err error) {
	c.access.Lock()
	c.nextErr = err
	c.access.Unlock()
}

func (c *scriptedConn) setNextReadBytes(count int) {
	c.access.Lock()
	c.nextBytes = count
	c.access.Unlock()
}

func (c *scriptedConn) readCount() int {
	c.access.Lock()
	defer c.access.Unlock()
	return c.reads
}

func (c *scriptedConn) Read(buffer []byte) (int, error) {
	c.access.Lock()
	defer c.access.Unlock()
	c.reads++
	if c.reads == 1 {
		// The client's FIN: the upload direction ends cleanly.
		return 0, io.EOF
	}
	if c.nextBytes > 0 {
		n := min(c.nextBytes, len(buffer))
		c.nextBytes -= n
		for index := 0; index < n; index++ {
			buffer[index] = 'x'
		}
		return n, nil
	}
	return 0, c.nextErr
}

func (c *scriptedConn) Write(buffer []byte) (int, error) {
	return len(buffer), nil
}

// CloseWrite is what a clean half-close of the destination looks like on the other side; the
// scripted conn is always the SOURCE, so it is never called. It exists so a future test can use the
// same type as a destination.
func (c *scriptedConn) CloseWrite() error {
	c.halfClosed.Store(true)
	return nil
}

func (c *scriptedConn) Close() error {
	c.closed.Store(true)
	return nil
}

// halfCloseTerminalRead is the second capability the observer needs (see conn_halfclose.go): this
// double scripts the read side, so it declares that a later read reports the terminal state it is
// told to return. Without it the observation is correctly skipped and these decision tests would not
// test anything.
func (c *scriptedConn) halfCloseTerminalRead() bool { return true }

func (c *scriptedConn) LocalAddr() net.Addr              { return M.Socksaddr{} }
func (c *scriptedConn) RemoteAddr() net.Addr             { return M.Socksaddr{} }
func (c *scriptedConn) SetDeadline(time.Time) error      { return nil }
func (c *scriptedConn) SetReadDeadline(time.Time) error  { return nil }
func (c *scriptedConn) SetWriteDeadline(time.Time) error { return nil }
func (c *scriptedConn) SetKeepAliveConfig(config net.KeepAliveConfig) error {
	c.keepAliveSet.Add(1)
	c.access.Lock()
	c.keepAlive = config
	configErr := c.configErr
	c.access.Unlock()
	return configErr
}

func (c *scriptedConn) setConfigErr(err error) {
	c.access.Lock()
	c.configErr = err
	c.access.Unlock()
}

// silentPeerListener accepts connections and never answers, never closes.
func silentPeerListener(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	held := make(chan net.Conn, 8)
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			held <- conn
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		for {
			select {
			case conn := <-held:
				conn.Close()
			default:
				return
			}
		}
	})
	return listener
}

type halfCloseDialer struct{ target M.Socksaddr }

func (d halfCloseDialer) DialContext(ctx context.Context, network string, _ M.Socksaddr) (net.Conn, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, network, d.target.String())
	if err != nil {
		return nil, err
	}
	return conn, nil
}

func (d halfCloseDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, net.ErrClosed
}

type halfCloseFixture struct {
	manager   *ConnectionManager
	client    *scriptedConn
	closed    chan struct{}
	closeOnce sync.Once
	closeCall atomic.Int32
	closeErr  atomic.Value
}

// peerSink is the far end: it accepts and records the connection so a test can close it on purpose,
// which is how "the normal teardown won the race" is produced deterministically.
type peerSink struct {
	listener net.Listener
	access   sync.Mutex
	conns    []net.Conn
}

func newPeerSink(t *testing.T) *peerSink {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	sink := &peerSink{listener: listener}
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			sink.access.Lock()
			sink.conns = append(sink.conns, conn)
			sink.access.Unlock()
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		sink.closeAll()
	})
	return sink
}

func (s *peerSink) addr() M.Socksaddr { return M.ParseSocksaddr(s.listener.Addr().String()) }

func (s *peerSink) closeAll() {
	s.access.Lock()
	defer s.access.Unlock()
	for _, conn := range s.conns {
		conn.Close()
	}
	s.conns = nil
}

// closeWriteFirst half-closes the FIRST accepted connection, which is the peer of the inbound under
// test (the dialer's own connection is accepted after it). That is how a device-side FIN looks from
// the router: the peer that stops sending is the one whose socket it is reading.
func (s *peerSink) closeWriteFirst() error {
	s.access.Lock()
	defer s.access.Unlock()
	if len(s.conns) == 0 {
		return net.ErrClosed
	}
	if closer, isCloser := s.conns[0].(interface{ CloseWrite() error }); isCloser {
		return closer.CloseWrite()
	}
	return s.conns[0].Close()
}

func (s *peerSink) connCount() int {
	s.access.Lock()
	defer s.access.Unlock()
	return len(s.conns)
}

func newHalfCloseFixture(t *testing.T) *halfCloseFixture {
	t.Helper()
	fixture, _ := newHalfCloseFixtureSink(t, halfCloseTestTimings)
	return fixture
}

// newHalfCloseFixtureSink is the fixture with its timings and its far end exposed, for the tests
// that need to control either.
func newHalfCloseFixtureSink(t *testing.T, timings halfCloseOptions) (*halfCloseFixture, *peerSink) {
	t.Helper()
	return newHalfCloseFixtureSinkLogged(t, timings, nil)
}

func newHalfCloseFixtureSinkLogged(t *testing.T, timings halfCloseOptions, captured *stallLog) (*halfCloseFixture, *peerSink) {
	t.Helper()
	managerLogger := log.NewNOPFactory().Logger()
	if captured != nil {
		managerLogger = captured.logger()
	}
	manager := NewConnectionManager(managerLogger)
	manager.halfClose = timings
	fixture := &halfCloseFixture{
		manager: manager,
		client:  newScriptedConn(),
		closed:  make(chan struct{}),
	}
	t.Cleanup(func() { _ = manager.Close() })
	sink := newPeerSink(t)
	manager.NewConnection(context.Background(),
		halfCloseDialer{target: sink.addr()},
		fixture.client,
		adapter.InboundContext{Destination: M.ParseSocksaddrHostPort("peer.example", 443)},
		func(err error) {
			fixture.closeCall.Add(1)
			if err != nil {
				fixture.closeErr.Store(err.Error())
			}
			fixture.closeOnce.Do(func() { close(fixture.closed) })
		})
	return fixture, sink
}

// TestHalfClosedPeerGoneIsReleased is the defect: the peer's socket disappears after its FIN, and
// the flow must be released without close-all.
func TestHalfClosedPeerGoneIsReleased(t *testing.T) {
	fixture := newHalfCloseFixture(t)

	require.Eventually(t, func() bool {
		return fixture.client.keepAliveSet.Load() == 1
	}, 3*time.Second, 2*time.Millisecond,
		"a cleanly finished upload direction must arm the liveness observation")

	// While the peer answers nothing but still exists, nothing may happen.
	select {
	case <-fixture.closed:
		t.Fatalf("the flow was released while the peer was still alive (%v)", fixture.closeErr.Load())
	case <-time.After(150 * time.Millisecond):
	}

	// Now the socket is gone: every later read reports a terminal error.
	fixture.client.setNextReadErr(syscall.ECONNRESET)

	select {
	case <-fixture.closed:
	case <-time.After(3 * time.Second):
		t.Fatal("the flow was not released after the peer's socket disappeared")
	}
	require.Equal(t, int32(1), fixture.closeCall.Load(), "onClose must run exactly once")
	require.Eventually(t, fixture.client.closed.Load, time.Second, 2*time.Millisecond,
		"the finished side must be closed with the flow")
}

// TestHalfClosedLivePeerIsNotReleased is the false-positive guard, and it is the reason this is not
// an idle timeout: a peer that stays connected and says nothing for far longer than the probe
// interval must keep its connection.
func TestHalfClosedLivePeerIsNotReleased(t *testing.T) {
	fixture := newHalfCloseFixture(t)

	require.Eventually(t, func() bool {
		return fixture.client.keepAliveSet.Load() == 1
	}, 3*time.Second, 2*time.Millisecond)

	// Thirty probe intervals of silence from a peer that is still there.
	select {
	case <-fixture.closed:
		t.Fatalf("a silent but live peer must not lose its connection (%v)", fixture.closeErr.Load())
	case <-time.After(300 * time.Millisecond):
	}
	require.Equal(t, int32(0), fixture.closeCall.Load())

	// And the connection really is still usable: the surviving direction ends it, not the observer.
	fixture.client.setNextReadErr(syscall.ECONNRESET)
	select {
	case <-fixture.closed:
	case <-time.After(3 * time.Second):
		t.Fatal("the flow was not released after the peer's socket disappeared")
	}
}

// TestHalfCloseKeepAliveIsConfigured pins that the observer asks the finished side for a keepalive at
// all. Without it a userspace stack never probes, so a vanished peer would stay invisible and the
// rest of the mechanism would be dead code.
func TestHalfCloseKeepAliveIsConfigured(t *testing.T) {
	fixture := newHalfCloseFixture(t)
	require.Eventually(t, func() bool {
		return fixture.client.keepAliveSet.Load() == 1
	}, 3*time.Second, 2*time.Millisecond)

	fixture.client.access.Lock()
	config := fixture.client.keepAlive
	fixture.client.access.Unlock()

	require.True(t, config.Enable, "the observation relies on the probe, so keepalive must be on")
	require.Equal(t, halfCloseTestTimings.keepAliveIdle, config.Idle)
	require.Equal(t, halfCloseTestTimings.keepAliveInterval, config.Interval)
	require.Equal(t, halfClosedKeepAliveCount, config.Count)
}

// TestHalfClosedFlowCloseIsOnceUnderRace drives the two owners - the surviving direction and the
// observer - at the same time, and requires onClose exactly once.
func TestHalfClosedFlowCloseIsOnceUnderRace(t *testing.T) {
	for attempt := 0; attempt < 20; attempt++ {
		fixture := newHalfCloseFixture(t)
		require.Eventually(t, func() bool {
			return fixture.client.keepAliveSet.Load() == 1
		}, 3*time.Second, 2*time.Millisecond)
		fixture.client.setNextReadErr(syscall.ECONNRESET)
		select {
		case <-fixture.closed:
		case <-time.After(3 * time.Second):
			t.Fatalf("attempt %d: the flow was never released", attempt)
		}
		// Give the losing owner time to reach the same teardown.
		time.Sleep(20 * time.Millisecond)
		require.Equal(t, int32(1), fixture.closeCall.Load(),
			"attempt %d: onClose ran more than once", attempt)
	}
}

// TestHalfClosedLocalDeadlineDoesNotRelease: a local read deadline is not a peer verdict.
//
// A wrapper further out in the chain may impose one (sniffing does), and it must never be read as
// "the peer is gone" - that would kill exactly the flows this observer exists to protect.
func TestHalfClosedLocalDeadlineDoesNotRelease(t *testing.T) {
	fixture := newHalfCloseFixture(t)
	require.Eventually(t, func() bool {
		return fixture.client.keepAliveSet.Load() == 1
	}, 3*time.Second, 2*time.Millisecond)

	fixture.client.setNextReadErr(os.ErrDeadlineExceeded)
	select {
	case <-fixture.closed:
		t.Fatalf("an expired LOCAL deadline released the flow (%v)", fixture.closeErr.Load())
	case <-time.After(200 * time.Millisecond):
	}
	require.Equal(t, int32(0), fixture.closeCall.Load())
}

// TestHalfClosedUnexpectedByteStopsObservation: the observer must never eat stream data.
//
// If a read after the reported EOF returns bytes - which a well-behaved TCP stream cannot do - the
// premise is wrong, so the observation stops and the flow is left to its surviving direction. The
// one byte that was read is the cost of proving the premise; reading further is not.
func TestHalfClosedUnexpectedByteStopsObservation(t *testing.T) {
	fixture := newHalfCloseFixture(t)
	require.Eventually(t, func() bool {
		return fixture.client.keepAliveSet.Load() == 1
	}, 3*time.Second, 2*time.Millisecond)

	fixture.client.setNextReadBytes(1)
	require.Eventually(t, func() bool {
		return fixture.client.readCount() >= 2
	}, 3*time.Second, 2*time.Millisecond)
	afterProbe := fixture.client.readCount()
	require.Eventually(t, func() bool {
		return fixture.client.readCount() == afterProbe
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, afterProbe, fixture.client.readCount(),
		"the observation must stop after it reads a byte it cannot explain")
	require.Equal(t, int32(0), fixture.closeCall.Load(),
		"an unexpected byte is not a verdict about the peer")
	select {
	case <-fixture.closed:
		t.Fatal("an unexpected byte must not release the flow")
	case <-time.After(100 * time.Millisecond):
	}
}

// TestHalfCloseDiagnosticsCountTheVerdict pins the numbers a real-device run is meant to report.
//
// These are the only half-close numbers an operator sees: one line at tunnel shutdown. If they can
// drift from the mechanism - counting an observation that never started, or a release that never
// happened - the line becomes a claim nobody can check.
func TestHalfCloseDiagnosticsCountTheVerdict(t *testing.T) {
	fixture := newHalfCloseFixture(t)
	require.Eventually(t, func() bool {
		return fixture.client.keepAliveSet.Load() == 1
	}, 3*time.Second, 2*time.Millisecond)

	initial := fixture.manager.HalfCloseDiagnostics()
	require.Equal(t, uint64(1), initial.Observed, "the observation must be counted when it is armed")
	require.Equal(t, uint64(0), initial.Released, "nothing was released yet")

	fixture.client.setNextReadErr(syscall.ECONNRESET)
	select {
	case <-fixture.closed:
	case <-time.After(3 * time.Second):
		t.Fatal("the flow was never released")
	}
	final := fixture.manager.HalfCloseDiagnostics()
	require.Equal(t, uint64(1), final.Observed)
	require.Equal(t, uint64(1), final.Released)
	require.Equal(t, uint64(1), final.PeerReset,
		"a reset from the peer's stack is the reset half, not the timeout half")
	require.Equal(t, uint64(0), final.KeepaliveTimeout)
	require.Equal(t, uint64(0), final.Stopped)

	summary := final.HalfCloseSummary()
	require.Contains(t, summary, "half-closed flows: 1 observed")
	require.Contains(t, summary, "released 1")
	require.Contains(t, summary, "peer reset 1")
}

// TestHalfCloseSummaryNamesATimeoutSeparately is the other half of the classification.
func TestHalfCloseSummaryNamesATimeoutSeparately(t *testing.T) {
	snapshot := HalfCloseSnapshot{Observed: 3, Released: 2, KeepaliveTimeout: 2}
	summary := snapshot.HalfCloseSummary()
	require.Contains(t, summary, "3 observed")
	require.Contains(t, summary, "released 2")
	require.Contains(t, summary, "keepalive timeout 2")
	require.Contains(t, summary, "peer reset 0")
}

// TestKeepAliveCapabilityWalk pins the fail-safe and the unwrap: a connection that offers no
// keepalive is not something the observer may judge, and a capability that sits under a transparent
// wrapper must still be found.
//
// The unwrap matters in production rather than in theory: by the time a connection reaches a copy
// loop it is under the traffic tracker's counter wrapper, so a plain type assertion on the outer
// value would find nothing on the TUN path and the whole mechanism would silently never run.
func TestKeepAliveCapabilityWalk(t *testing.T) {
	plain, plainPeer := net.Pipe()
	t.Cleanup(func() { plain.Close(); plainPeer.Close() })
	require.Nil(t, keepAliveConnOf(plain),
		"a connection with no keepalive capability must not be observed")

	scripted := newScriptedConn()
	require.Equal(t, keepAliveConnOf(scripted), keepAliveConfigurable(scripted),
		"the capability itself must be found")

	wrapped := &upstreamWrapper{upstream: scripted}
	require.Equal(t, keepAliveConnOf(wrapped), keepAliveConfigurable(scripted),
		"the capability must be found through a transparent wrapper")
}

// upstreamWrapper is the shape every wrapper in the copy chain has: one upstream link.
type upstreamWrapper struct {
	net.Conn
	upstream net.Conn
}

func (w *upstreamWrapper) Upstream() any { return w.upstream }
