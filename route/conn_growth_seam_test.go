package route

// The relay growth-permit lifecycle at the level the reviewer's G07 finding actually
// reproduces: real proxied connections through ConnectionManager.NewConnection, with the
// production copy engine and the production growth budget.
//
// The defect this file pins: route/conn.go used to call growthBudget.Admit once per copy
// DIRECTION before the copy started. Admit counts the direction as an enlarged-buffer
// holder immediately, so a direction that never copies enough bytes to grow (an idle
// keep-alive, a tunnel waiting for a request, a long poll) still occupies a permit for
// its whole life. With the default policy (256 ordinary flows) 128 quiet bidirectional
// flows were enough to consume every permit; a later bulk direction was refused growth
// and kept the 32 KiB read buffer for its whole transfer.
//
// The first test does not reference the permit seam at all: it drives real connections
// and reads the budget snapshot. That is deliberate, because it is the only level at
// which the defect is "the wiring holds a permit it should not": the budget's own unit
// tests cannot see who calls Admit and when. It therefore compiled and FAILED at the
// parent revision this work started from, and passes after the wiring change.

import (
	"bytes"
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/resourceguard"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// growthProbeConn wraps one end of a stream so a test can see exactly which read
// buffers the production copy loop asks for.
//
// It implements N.ReadWaiter, which is the seam the copy engine itself uses for a
// socket: InitializeReadWaiter is called when the loop starts and again when it
// switches to the enlarged buffer, so the options the loop is about to use are
// observable without replacing the copy engine. WaitReadBuffer allocates from those
// same options, so the bytes really are read through the buffer geometry under test.
type growthProbeConn struct {
	net.Conn

	access  sync.Mutex
	options N.ReadWaitOptions
	reads   int
	grown   bool
	early   bool
}

func (c *growthProbeConn) InitializeReadWaiter(options N.ReadWaitOptions) bool {
	c.access.Lock()
	c.options = options
	if options.IncreaseBuffer {
		c.grown = true
	}
	c.access.Unlock()
	// false: the waiter is already a real ReadWaiter; the loop must use it, not copy
	// the stream itself (copyExtended's needCopy return value).
	return false
}

func (c *growthProbeConn) WaitReadBuffer() (*buf.Buffer, error) {
	c.access.Lock()
	options := c.options
	c.access.Unlock()
	for {
		buffer := options.NewBuffer()
		readN, err := c.Conn.Read(buffer.FreeBytes())
		if readN > 0 {
			buffer.Truncate(readN)
			c.access.Lock()
			c.reads++
			c.access.Unlock()
			return buffer, nil
		}
		buffer.Release()
		if err != nil {
			return nil, err
		}
	}
}

// CloseWrite forwards the write-direction end so the production half-close path stays
// reachable through the probe wrapper. Without it the route layer would see a plain
// net.Conn and tear the whole flow down where it should half-close.
func (c *growthProbeConn) CloseWrite() error {
	if closeWriter, isCloseWriter := c.Conn.(interface{ CloseWrite() error }); isCloseWriter {
		return closeWriter.CloseWrite()
	}
	return c.Conn.Close()
}

func (c *growthProbeConn) grownReads() bool {
	c.access.Lock()
	defer c.access.Unlock()
	return c.grown
}

func (c *growthProbeConn) readCount() int {
	c.access.Lock()
	defer c.access.Unlock()
	return c.reads
}

// EarlyCopyBufferGrowth makes this connection's writer geometry-required for the copy
// that FEEDS it (the download direction), exactly as protocol/naive's padded writer does
// through adapter.CopyBufferGrowthTuner.
func (c *growthProbeConn) EarlyCopyBufferGrowth() bool { return c.early }

// growthProbeDialer hands NewConnection one prepared outbound end per dial.
type growthProbeDialer struct {
	pending chan net.Conn
}

func (d *growthProbeDialer) DialContext(_ context.Context, _ string, _ M.Socksaddr) (net.Conn, error) {
	select {
	case conn := <-d.pending:
		return conn, nil
	case <-time.After(5 * time.Second):
		return nil, io.ErrNoProgress
	}
}

func (d *growthProbeDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, net.ErrClosed
}

// relayProbePair is one proxied connection: the application side of the inbound, the
// inbound end the manager copies from/to, the outbound end the dialer returns, and the
// peer the manager writes to/reads from.
type relayProbePair struct {
	app       net.Conn
	inbound   *growthProbeConn
	outbound  *growthProbeConn
	peer      net.Conn
	closeOnce sync.Once
}

// newRelayProbePair builds the pair over real loopback TCP when onTCP is true, so
// CloseWrite and RST behave as they do in production. With onTCP false it uses
// net.Pipe, which is enough for the quiet-flow and bulk-flow shape and much cheaper to
// start 128 times.
func newRelayProbePair(t *testing.T, onTCP bool) *relayProbePair {
	t.Helper()
	var app, inbound, peer, outbound net.Conn
	if onTCP {
		app, inbound = newLoopbackTCPPair(t)
		peer, outbound = newLoopbackTCPPair(t)
	} else {
		app, inbound = net.Pipe()
		peer, outbound = net.Pipe()
	}
	pair := &relayProbePair{
		app:      app,
		inbound:  &growthProbeConn{Conn: inbound},
		outbound: &growthProbeConn{Conn: outbound},
		peer:     peer,
	}
	t.Cleanup(pair.close)
	return pair
}

func (p *relayProbePair) close() {
	p.closeOnce.Do(func() {
		_ = p.app.Close()
		_ = p.peer.Close()
		_ = p.inbound.Close()
		_ = p.outbound.Close()
	})
}

// newLoopbackTCPPair returns two connected TCP sockets. The first is the application
// side, the second is what the route layer sees; either end may CloseWrite.
func newLoopbackTCPPair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			accepted <- nil
			return
		}
		accepted <- conn
	}()
	client, err := net.Dial("tcp", listener.Addr().String())
	require.NoError(t, err)
	server := <-accepted
	require.NotNil(t, server, "the loopback listener did not accept the pair")
	return client, server
}

// quietRelayFixture starts proxied connections that exchange no bytes.
type quietRelayFixture struct {
	manager *ConnectionManager
	dialer  *growthProbeDialer
	pairs   []*relayProbePair
}

func newQuietRelayFixture(t *testing.T, budget *resourceguard.GrowthBudget, flows int, onTCP bool) *quietRelayFixture {
	t.Helper()
	fixture := &quietRelayFixture{
		manager: NewConnectionManager(log.NewNOPFactory().Logger()),
		dialer:  &growthProbeDialer{pending: make(chan net.Conn, flows+8)},
	}
	fixture.manager.growth = budget
	t.Cleanup(func() { _ = fixture.manager.Close() })
	for index := 0; index < flows; index++ {
		pair := newRelayProbePair(t, onTCP)
		fixture.pairs = append(fixture.pairs, pair)
		fixture.dialer.pending <- pair.outbound
		fixture.manager.NewConnection(context.Background(), fixture.dialer, pair.inbound,
			adapter.InboundContext{Destination: M.ParseSocksaddrHostPort("quiet.example", 443)}, nil)
	}
	return fixture
}

func (f *quietRelayFixture) start(t *testing.T, pair *relayProbePair, from string) {
	t.Helper()
	f.dialer.pending <- pair.outbound
	f.manager.NewConnection(context.Background(), f.dialer, pair.inbound,
		adapter.InboundContext{Destination: M.ParseSocksaddrHostPort(from, 443)}, nil)
}

// keepingWriter records everything written to it, so a payload can be compared after it
// crossed the relay rather than only counted.
type keepingWriter struct {
	access sync.Mutex
	data   []byte
}

func (w *keepingWriter) Write(payload []byte) (int, error) {
	w.access.Lock()
	w.data = append(w.data, payload...)
	w.access.Unlock()
	return len(payload), nil
}

func (w *keepingWriter) bytes() []byte {
	w.access.Lock()
	defer w.access.Unlock()
	return append([]byte(nil), w.data...)
}

// TestQuietRelayDirectionsDoNotHoldGrowthPermits is the G07 regression.
//
// Before the fix, each of the 128 quiet bidirectional flows held two permits (one per
// direction) from the moment NewConnection wired the copies, so InUse was 256 while not
// one byte had been copied. The budget then refused the bulk direction at its growth
// point, which is why the bulk probes' grown flags stayed false. After the fix a quiet
// direction holds nothing, so InUse is 0, both directions of the bulk flow grow when
// they cross the threshold, each holds exactly one permit while its enlarged buffer is
// live, and the pool drains to zero with no double release when the flows end.
func TestQuietRelayDirectionsDoNotHoldGrowthPermits(t *testing.T) {
	budget := resourceguard.NewGrowthBudget(resourceguard.DefaultGrowthConfig(), nil)
	fixture := newQuietRelayFixture(t, budget, 128, false)

	// NewConnection wired both copy directions for all 128 flows. No payload has been
	// written anywhere, so no read buffer can have grown. A permit held here is held
	// for nothing.
	quietStats := budget.Stats()
	t.Logf("after 128 quiet bidirectional flows (256 directions), zero bytes copied: in_use=%d granted=%d refused=%d geometry=%d",
		quietStats.InUse, quietStats.Granted, quietStats.Refused, quietStats.GeometryNeeded)
	require.Zero(t, quietStats.InUse,
		"128 quiet bidirectional relay flows copied zero bytes but occupy %d growth permits", quietStats.InUse)
	require.Zero(t, quietStats.Granted,
		"no quiet direction has copied enough bytes to grow, so none may be granted")

	// The bulk connection arrives while every quiet flow is still open. Both directions
	// copy 2 MiB, well past DefaultIncreaseBufferAfter (512000), and neither is refused.
	bulk := newRelayProbePair(t, false)
	fixture.start(t, bulk, "bulk.example")

	const payloadSize = 2 << 20
	uploadPayload := bytes.Repeat([]byte{0xA5}, payloadSize)
	downloadPayload := bytes.Repeat([]byte{0x5A}, payloadSize)
	uploadSink := &keepingWriter{}
	downloadSink := &keepingWriter{}

	uploadWritten := make(chan struct{})
	go func() {
		defer close(uploadWritten)
		_, _ = bulk.app.Write(uploadPayload)
	}()
	downloadWritten := make(chan struct{})
	go func() {
		defer close(downloadWritten)
		_, _ = bulk.peer.Write(downloadPayload)
	}()

	// The destinations of both copy directions need a reader, or the relay's writes
	// block before the copy can read the next chunk.
	uploadDrained := make(chan struct{})
	go func() {
		defer close(uploadDrained)
		_, _ = io.CopyN(uploadSink, bulk.peer, payloadSize)
	}()
	downloadDrained := make(chan struct{})
	go func() {
		defer close(downloadDrained)
		_, _ = io.CopyN(downloadSink, bulk.app, payloadSize)
	}()

	for name, done := range map[string]chan struct{}{"upload": uploadWritten, "download": downloadWritten} {
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			t.Fatalf("the %s payload was never accepted by the bulk connection", name)
		}
	}
	require.Eventually(t, func() bool {
		return bulk.inbound.grownReads() && bulk.outbound.grownReads()
	}, 30*time.Second, time.Millisecond,
		"a bulk direction that copied %d bytes was never allowed to grow", payloadSize)

	bulkStats := budget.Stats()
	t.Logf("bulk connection (2 MiB each direction) while the quiet flows are open: upload_grown=%v download_grown=%v in_use=%d granted=%d refused=%d",
		bulk.inbound.grownReads(), bulk.outbound.grownReads(), bulkStats.InUse, bulkStats.Granted, bulkStats.Refused)
	require.Equal(t, 2, bulkStats.InUse,
		"both directions copied 2 MiB, so each must hold exactly one permit while its enlarged buffer is live")
	require.Equal(t, uint64(2), bulkStats.Granted, "each direction grows once")
	require.Zero(t, bulkStats.Refused, "no direction was refused while the quiet flows held nothing")
	require.Greater(t, bulk.inbound.readCount(), 0)
	require.Greater(t, bulk.outbound.readCount(), 0)

	// Every byte arrived intact on both application sides: growth is a buffer choice,
	// never a payload one.
	for name, done := range map[string]chan struct{}{"upload": uploadDrained, "download": downloadDrained} {
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			t.Fatalf("the %s payload never reached the far application side", name)
		}
	}
	require.Equal(t, uploadPayload, uploadSink.bytes(), "the upload direction reordered or lost payload bytes")
	require.Equal(t, downloadPayload, downloadSink.bytes(), "the download direction reordered or lost payload bytes")

	// Ending the bulk flow returns both permits; ending the quiet flows must not move
	// the counter in either direction, because they never held one.
	bulk.close()
	require.Eventually(t, func() bool { return budget.Stats().InUse == 0 },
		30*time.Second, time.Millisecond, "the bulk direction's permits were not returned when its copies ended")
	for _, pair := range fixture.pairs {
		pair.close()
	}
	require.Eventually(t, func() bool { return budget.Stats().InUse == 0 },
		30*time.Second, time.Millisecond)

	final := budget.Stats()
	t.Logf("final after all flows closed: in_use=%d granted=%d released=%d refused=%d double_releases=%d",
		final.InUse, final.Granted, final.Released, final.Refused, final.DoubleReleases)
	require.Zero(t, final.DoubleReleases, "a permit was released twice")
	require.Equal(t, final.Granted-final.Released, uint64(final.InUse),
		"the growth budget's own identity must hold: Granted - Released == InUse")
}

// TestGrowthPermitIsReturnedOnHalfCloseAndReset pins the ownership half over real TCP:
// a direction that actually grows holds its permit until ITS copy returns, a half-close
// of the upload side releases only the upload permit while the download direction keeps
// copying, and a reset returns the remaining permit exactly once.
func TestGrowthPermitIsReturnedOnHalfCloseAndReset(t *testing.T) {
	t.Run("half-close", func(t *testing.T) {
		budget := resourceguard.NewGrowthBudget(resourceguard.DefaultGrowthConfig(), nil)
		fixture := newQuietRelayFixture(t, budget, 0, true)
		conn := newRelayProbePair(t, true)
		fixture.start(t, conn, "bulk.example")

		const payloadSize = 2 << 20
		payload := bytes.Repeat([]byte{0x3C}, payloadSize)
		uploadSink := &keepingWriter{}
		downloadSink := &keepingWriter{}

		uploadWritten := make(chan struct{})
		go func() {
			defer close(uploadWritten)
			_, _ = conn.app.Write(payload)
		}()
		downloadWritten := make(chan struct{})
		go func() {
			defer close(downloadWritten)
			_, _ = conn.peer.Write(payload)
		}()
		uploadDrained := make(chan struct{})
		go func() {
			defer close(uploadDrained)
			_, _ = io.CopyN(uploadSink, conn.peer, payloadSize)
		}()
		downloadDrained := make(chan struct{})
		go func() {
			defer close(downloadDrained)
			_, _ = io.CopyN(downloadSink, conn.app, payloadSize)
		}()
		for name, done := range map[string]chan struct{}{"upload": uploadWritten, "download": downloadWritten} {
			select {
			case <-done:
			case <-time.After(30 * time.Second):
				t.Fatalf("the %s payload was never accepted", name)
			}
		}
		require.Eventually(t, func() bool {
			return conn.inbound.grownReads() && conn.outbound.grownReads()
		}, 30*time.Second, time.Millisecond, "both directions should have grown past the threshold")
		require.Eventually(t, func() bool { return budget.Stats().InUse == 2 },
			30*time.Second, time.Millisecond, "each grown direction holds one permit")

		// Half-close the application's write direction: the upload copy reads EOF and
		// returns; the download copy is still blocked reading the peer and keeps its
		// permit.
		require.NoError(t, conn.app.(interface{ CloseWrite() error }).CloseWrite())
		require.Eventually(t, func() bool { return budget.Stats().InUse == 1 },
			30*time.Second, time.Millisecond,
			"the upload direction's permit did not return when its copy ended")
		require.Zero(t, budget.Stats().DoubleReleases)

		// The download direction is still alive: its permit must NOT have been returned
		// just because the other direction ended. (Its 2 MiB had already been delivered
		// before the half-close, so the permit count is the observable, not the drain.)
		require.Equal(t, 1, budget.Stats().InUse)

		// Ending the peer ends the download copy and returns the last permit.
		_ = conn.peer.Close()
		require.Eventually(t, func() bool { return budget.Stats().InUse == 0 },
			30*time.Second, time.Millisecond)
		final := budget.Stats()
		require.Zero(t, final.DoubleReleases)
		require.Equal(t, final.Granted-final.Released, uint64(final.InUse))
	})

	t.Run("reset", func(t *testing.T) {
		budget := resourceguard.NewGrowthBudget(resourceguard.DefaultGrowthConfig(), nil)
		fixture := newQuietRelayFixture(t, budget, 0, true)
		conn := newRelayProbePair(t, true)
		fixture.start(t, conn, "reset.example")

		const payloadSize = 2 << 20
		payload := bytes.Repeat([]byte{0x6D}, payloadSize)
		written := make(chan struct{})
		go func() {
			defer close(written)
			_, _ = conn.app.Write(payload)
		}()
		drained := make(chan struct{})
		go func() {
			defer close(drained)
			_, _ = io.CopyN(io.Discard, conn.peer, int64(len(payload)))
		}()
		select {
		case <-written:
		case <-time.After(30 * time.Second):
			t.Fatal("the payload was never accepted")
		}
		select {
		case <-drained:
		case <-time.After(30 * time.Second):
			t.Fatal("the payload never reached the far side")
		}
		require.Eventually(t, func() bool { return conn.inbound.grownReads() },
			30*time.Second, time.Millisecond, "the upload direction never grew")
		require.Eventually(t, func() bool { return budget.Stats().InUse == 1 },
			30*time.Second, time.Millisecond)

		// An abrupt close of the application side is a reset from the copy's point of
		// view: the read fails, the copy returns, and the permit must come back.
		tcpConn, isTCP := conn.app.(*net.TCPConn)
		require.True(t, isTCP)
		require.NoError(t, tcpConn.SetLinger(0))
		require.NoError(t, conn.app.Close())
		require.Eventually(t, func() bool { return budget.Stats().InUse == 0 },
			30*time.Second, time.Millisecond, "the reset direction did not return its permit")
		final := budget.Stats()
		require.Zero(t, final.DoubleReleases)
		require.Equal(t, final.Granted-final.Released, uint64(final.InUse))
	})
}

// TestGeometryRequiredDirectionGrowsWithoutAHeldPermit pins the exemption end to end:
// a direction whose resolved threshold is the geometry-required one grows from the first
// transfer even while the ordinary policy has no capacity at all, and it takes no
// ordinary permit.
//
// The end marked below is the one the existing route wiring consults for that direction
// (growthThreshold receives the inbound conn for the upload call and the outbound conn
// for the download call; that argument order is pre-existing and is not changed by this
// work). The assertion is the exemption itself, on both directions of a connection.
func TestGeometryRequiredDirectionGrowsWithoutAHeldPermit(t *testing.T) {
	payload := bytes.Repeat([]byte{0x77}, 256<<10)

	t.Run("upload", func(t *testing.T) {
		// Zero ordinary flows: anything charged to the ordinary cap would be refused.
		budget := resourceguard.NewGrowthBudget(resourceguard.GrowthConfig{Flows: 0}, nil)
		fixture := newQuietRelayFixture(t, budget, 0, false)
		conn := newRelayProbePair(t, false)
		conn.inbound.early = true // the end the upload call inspects
		fixture.start(t, conn, "geometry-upload.example")

		written := make(chan struct{})
		go func() {
			defer close(written)
			_, _ = conn.app.Write(payload)
		}()
		drained := make(chan struct{})
		go func() {
			defer close(drained)
			_, _ = io.CopyN(io.Discard, conn.peer, int64(len(payload)))
		}()
		select {
		case <-written:
		case <-time.After(30 * time.Second):
			t.Fatal("the geometry upload payload was never accepted")
		}
		require.Eventually(t, func() bool { return conn.inbound.grownReads() },
			30*time.Second, time.Millisecond, "a geometry-required upload direction was refused growth")
		stats := budget.Stats()
		require.Zero(t, stats.InUse, "a geometry direction must not charge the ordinary cap")
		require.GreaterOrEqual(t, stats.GeometryNeeded, uint64(1),
			"a geometry direction must still be counted for diagnostics")
		select {
		case <-drained:
		case <-time.After(30 * time.Second):
			t.Fatal("the geometry upload payload never reached the far side")
		}
	})

	t.Run("download", func(t *testing.T) {
		budget := resourceguard.NewGrowthBudget(resourceguard.GrowthConfig{Flows: 0}, nil)
		fixture := newQuietRelayFixture(t, budget, 0, false)
		conn := newRelayProbePair(t, false)
		conn.outbound.early = true // the end the download call inspects
		fixture.start(t, conn, "geometry-download.example")

		written := make(chan struct{})
		go func() {
			defer close(written)
			_, _ = conn.peer.Write(payload)
		}()
		drained := make(chan struct{})
		go func() {
			defer close(drained)
			_, _ = io.CopyN(io.Discard, conn.app, int64(len(payload)))
		}()
		select {
		case <-written:
		case <-time.After(30 * time.Second):
			t.Fatal("the geometry download payload was never accepted")
		}
		require.Eventually(t, func() bool { return conn.outbound.grownReads() },
			30*time.Second, time.Millisecond, "a geometry-required download direction was refused growth")
		stats := budget.Stats()
		require.Zero(t, stats.InUse, "a geometry direction must not charge the ordinary cap")
		require.GreaterOrEqual(t, stats.GeometryNeeded, uint64(1),
			"a geometry direction must still be counted for diagnostics")
		select {
		case <-drained:
		case <-time.After(30 * time.Second):
			t.Fatal("the geometry download payload never reached the application side")
		}
	})
}

// TestGrowthRefusalUnderPressureIsBoundedToMilestones pins that pressure still refuses
// growth at the new boundary, and that the retry schedule is bounded: a 2 MiB copy may
// re-ask at the threshold and at one 1 MiB retry milestone, not on every read.
func TestGrowthRefusalUnderPressureIsBoundedToMilestones(t *testing.T) {
	var level atomic.Uint32
	level.Store(uint32(resourceguard.LevelNormal))
	budget := resourceguard.NewGrowthBudget(resourceguard.DefaultGrowthConfig(), func() resourceguard.Level {
		return resourceguard.Level(level.Load())
	})
	fixture := newQuietRelayFixture(t, budget, 0, false)

	conn := newRelayProbePair(t, false)
	fixture.start(t, conn, "pressure.example")

	// Raise pressure before the copy reaches its threshold, then deliver 2 MiB. The
	// direction may copy everything, but it must stay on the small buffer.
	level.Store(uint32(resourceguard.LevelCritical))
	payload := bytes.Repeat([]byte{0x11}, 2<<20)
	written := make(chan struct{})
	go func() {
		defer close(written)
		_, _ = conn.app.Write(payload)
	}()
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		_, _ = io.CopyN(io.Discard, conn.peer, int64(len(payload)))
	}()
	select {
	case <-written:
	case <-time.After(30 * time.Second):
		t.Fatal("the payload was never accepted under pressure")
	}
	select {
	case <-drained:
	case <-time.After(30 * time.Second):
		t.Fatal("the refused direction did not copy every byte")
	}
	require.False(t, conn.inbound.grownReads(), "Critical must refuse an ordinary direction")
	stats := budget.Stats()
	require.Zero(t, stats.InUse, "a refused direction must not acquire a permit")
	require.GreaterOrEqual(t, stats.Refused, uint64(1))
	require.LessOrEqual(t, stats.Refused, uint64(2),
		"a 2 MiB copy must re-ask at bounded milestones (threshold and one 1 MiB retry), not per read")
}

var _ N.ReadWaiter = (*growthProbeConn)(nil)
var _ N.Dialer = (*growthProbeDialer)(nil)
var _ adapter.CopyBufferGrowthTuner = (*growthProbeConn)(nil)
