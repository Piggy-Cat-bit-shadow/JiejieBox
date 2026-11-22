package mieru

// Receive-quota ownership and cross-session progress, on real sessions.
//
// The parent-side decision (which budget reaches which client config) is covered by
// n_datapath_test.go. This file is about what the SDK does with the number once a session exists, and
// every assertion is made against a value the protocol publishes or a byte the peer received:
//
//   - `Session.UnreadPayloadBytes()` is the protocol's own gauge of received-but-unread plaintext
//     payload. The quota is admitted strictly (`cur+n > quota` is refused), so the gauge can never
//     exceed the budget; that is the invariant the sample loop below checks.
//   - after the application drains the stream, the gauge must return to exactly zero - not "small",
//     not negative - and every byte must have arrived once, in order.
//   - closing a session whose reader is parked must release the parking wait, which is observable as
//     the peer's blocked write making progress instead of sitting forever.
//
// The server is the package's real inbound on loopback; the client is a real transportRuntime built
// through newTransportSpec, i.e. the same construction path the outbound uses, so the quota under test
// is the quota production writes.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/receivebudget"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	mieruprotocol "github.com/enfein/mieru/v3/pkg/protocol"

	"github.com/stretchr/testify/require"
)

const (
	quotaPushChunk       = 16 << 10
	quotaStallWindow     = 700 * time.Millisecond
	quotaTestDeadline    = 45 * time.Second
	quotaDrainDeadline   = 45 * time.Second
	quotaStreamingLength = 16 << 20
	quotaDialTimeout     = 20 * time.Second
	pushDestinationPort  = 443
	// slowPushChunk is deliberately small: the protocol's receive window is counted in segments
	// (4096 of them), and a stream segment holds at most 32 KiB, so only a small chunk lets an
	// unread session's window close within a few megabytes of backlog.
	slowPushChunk = 1 << 10
)

// quotaPayload is the deterministic byte stream the server pushes: every byte is a function of its
// offset so the client can verify content and order.
func quotaPayload(offset int64) byte {
	return byte(offset*71 + 13)
}

// pushRoute is one routed connection whose data direction is controlled by the test.
type pushRoute struct {
	accepted atomic.Int64
	started  chan struct{}
	pushDone chan struct{}
}

// quotaRouter pushes a deterministic stream into connections whose destination port is
// pushDestinationPort, and echoes every other connection. It is the only writer towards the client,
// so "how many bytes the server could write" is directly observable.
type quotaRouter struct {
	adapter.Router
	pushLength int64
	// chunkSize is how much payload one Write carries. It matters: a stream segment holds at most
	// maxPDU (32 KiB), so a 32 KiB chunk makes one session's receive window 4096 *segments* worth of
	// large segments (128 MiB), while a 1 KiB chunk makes the same window close after 4 MiB. The
	// cross-session case uses small chunks so the peer's window can actually be exhausted in a
	// reasonable amount of memory.
	chunkSize int
	routes    chan *pushRoute
}

func newQuotaRouter(pushLength int64, chunkSize int) *quotaRouter {
	return &quotaRouter{pushLength: pushLength, chunkSize: chunkSize, routes: make(chan *pushRoute, 8)}
}

func (r *quotaRouter) RouteConnectionEx(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	route := &pushRoute{
		started:  make(chan struct{}, 1),
		pushDone: make(chan struct{}),
	}
	r.routes <- route
	go func() {
		defer conn.Close()
		if onClose != nil {
			defer onClose(nil)
		}
		route.started <- struct{}{}
		if metadata.Destination.Port == pushDestinationPort {
			r.push(conn, route)
			return
		}
		r.echo(conn)
	}()
}

func (r *quotaRouter) push(conn net.Conn, route *pushRoute) {
	chunk := make([]byte, r.chunkSize)
	var offset int64
	for offset < r.pushLength {
		size := int64(len(chunk))
		if remaining := r.pushLength - offset; remaining < size {
			size = remaining
		}
		for index := int64(0); index < size; index++ {
			chunk[index] = quotaPayload(offset + index)
		}
		written, err := conn.Write(chunk[:size])
		route.accepted.Add(int64(written))
		offset += int64(written)
		if err != nil || written == 0 {
			return
		}
	}
	close(route.pushDone)
}

func (r *quotaRouter) echo(conn net.Conn) {
	buffer := make([]byte, 4096)
	for {
		read, err := conn.Read(buffer)
		if read > 0 {
			if _, writeErr := conn.Write(buffer[:read]); writeErr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func (r *quotaRouter) RoutePacketConnectionEx(ctx context.Context, conn N.PacketConn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	conn.Close()
}

// startQuotaInbound runs the package's inbound with the controlled router.
func startQuotaInbound(t *testing.T, pushLength int64, chunkSize int) (*quotaRouter, uint16) {
	t.Helper()
	port := freePortForBothTransports(t)
	router := newQuotaRouter(pushLength, chunkSize)
	created, err := NewInbound(context.Background(), router, nopLogger(), "mieru-quota-in",
		inboundOptionsForPort(port, "tcp"))
	require.NoError(t, err)
	scope := adapter.NewScope(context.Background(), nopLogger())
	require.NoError(t, created.(*Inbound).Start(adapter.StartStateStart, scope))
	t.Cleanup(func() { _ = scope.Close() })
	return router, port
}

// newQuotaClient builds one client through the production construction path. The returned runtime owns
// the client, so several sessions can be opened through it - which is what makes cross-session
// behaviour observable instead of inferred.
func newQuotaClient(t *testing.T, port uint16, multiplexing string, policy *receivebudget.Policy) *transportRuntime {
	t.Helper()
	options := outboundOptionsForPort(port, "tcp", "alice", "alice-password")
	options.Multiplexing = multiplexing

	ctx := context.Background()
	if policy != nil {
		ctx = boxContextWithReceiveBudget(*policy)
	}
	spec, err := newTransportSpec(ctx, "quota-probe", options)
	require.NoError(t, err)
	runtime, err := newTransportRuntime(spec)
	require.NoError(t, err)
	require.NoError(t, runtime.start())
	t.Cleanup(func() { _ = runtime.stop() })
	return runtime
}

// crossSessionUnderlayBudget is the number of real underlay sockets the cross-session test admits.
// One socket is what makes its oracle deterministic: every successful second dial must have reused
// the slow session's underlay, and a rejected dial is the mux trying to leave the shared connection.
// The rejection is retried, never silently measured on another connection.
const crossSessionUnderlayBudget = 1

// crossSessionUnderlayAttempts bounds the retries; the mux reuses an existing underlay with
// probability factor/(factor+1) = 2/3 for the middle profile, so failing all 8 attempts is ~1e-4.
const crossSessionUnderlayAttempts = 8

// errUnderlayBudgetExhausted is returned by underlayBudgetDialer when the mux asks for one more real
// underlay socket than the test admits.
var errUnderlayBudgetExhausted = errors.New("mieru test: underlay socket budget exhausted")

// underlayBudgetDialer admits a fixed number of real underlay sockets and rejects the rest. The SDK
// creates a new underlay only when the mux does not reuse an existing one, so a rejection is the
// observable "the mux tried to leave the shared connection" signal. It is the same seam the
// production serverDialer exposes to the admission tests: the client is still built through
// newTransportSpec + newTransportRuntime, only the innermost socket creation is bounded.
type underlayBudgetDialer struct {
	inner N.Dialer
	limit int

	mu    sync.Mutex
	dials int
}

func (d *underlayBudgetDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	d.mu.Lock()
	if d.dials >= d.limit {
		d.mu.Unlock()
		return nil, errUnderlayBudgetExhausted
	}
	d.dials++
	d.mu.Unlock()
	return d.inner.DialContext(ctx, network, destination)
}

func (d *underlayBudgetDialer) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return d.inner.ListenPacket(ctx, destination)
}

// newQuotaClientWithUnderlayBudget builds the same production client as newQuotaClient, but the
// innermost dialer admits at most budget real underlay sockets.
func newQuotaClientWithUnderlayBudget(t *testing.T, port uint16, multiplexing string, budget int) *transportRuntime {
	t.Helper()
	options := outboundOptionsForPort(port, "tcp", "alice", "alice-password")
	options.Multiplexing = multiplexing
	spec, err := newTransportSpec(context.Background(), "quota-probe", options)
	require.NoError(t, err)
	spec.dialer = &underlayBudgetDialer{inner: spec.dialer, limit: budget}
	runtime, err := newTransportRuntime(spec)
	require.NoError(t, err)
	require.NoError(t, runtime.start())
	t.Cleanup(func() { _ = runtime.stop() })
	return runtime
}

// dialQuotaSession opens one session through a runtime and returns the protocol session object.
func dialQuotaSession(t *testing.T, runtime *transportRuntime, destination M.Socksaddr) *mieruprotocol.Session {
	t.Helper()
	netAddr, err := toNetAddrSpec(destination, N.NetworkTCP)
	require.NoError(t, err)
	dialCtx, cancel := context.WithTimeout(context.Background(), quotaDialTimeout)
	defer cancel()
	conn, err := runtime.client.DialContext(dialCtx, netAddr)
	require.NoError(t, err)
	session, isSession := conn.(*mieruprotocol.Session)
	require.True(t, isSession, "the SDK client returns its protocol session; the gauge lives there")
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// nextRoute waits for the inbound to route another session.
func nextRoute(t *testing.T, router *quotaRouter) *pushRoute {
	t.Helper()
	select {
	case route := <-router.routes:
		select {
		case <-route.started:
		case <-time.After(quotaTestDeadline):
			t.Fatal("the route never started")
		}
		return route
	case <-time.After(quotaTestDeadline):
		t.Fatal("the inbound never routed the session")
		return nil
	}
}

// awaitParked waits until the server's push has stopped making progress and returns the number of
// bytes it managed to push, together with the highest gauge value sampled meanwhile.
func awaitParked(t *testing.T, route *pushRoute, gauge func() int64) (pushed int64, maxGauge int64) {
	t.Helper()
	deadline := time.Now().Add(quotaTestDeadline)
	last := int64(-1)
	lastChange := time.Now()
	for {
		current := route.accepted.Load()
		if current != last {
			last = current
			lastChange = time.Now()
		}
		if observed := gauge(); observed > maxGauge {
			maxGauge = observed
		}
		if last > 0 && time.Since(lastChange) > quotaStallWindow {
			return last, maxGauge
		}
		if time.Now().After(deadline) {
			t.Fatalf("the server never stalled (pushed %d bytes)", last)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// drainSession reads the whole pushed stream from the session, verifies every byte, and returns the
// gauge observed after the last read.
func drainSession(t *testing.T, session *mieruprotocol.Session, length int64) int64 {
	t.Helper()
	require.NoError(t, session.SetReadDeadline(time.Now().Add(quotaDrainDeadline)))
	defer session.SetReadDeadline(time.Time{})
	buffer := make([]byte, quotaPushChunk)
	var offset int64
	for offset < length {
		size := int64(len(buffer))
		if remaining := length - offset; remaining < size {
			size = remaining
		}
		read, err := io.ReadFull(session, buffer[:size])
		require.NoError(t, err, "reading the pushed stream at offset %d", offset)
		for index := int64(0); index < int64(read); index++ {
			require.Equal(t, quotaPayload(offset+index), buffer[index], "byte %d is wrong", offset+index)
		}
		offset += int64(read)
	}
	return session.UnreadPayloadBytes()
}

// TestReceiveQuotaBoundsUnreadPayloadAndReturnsEverything is the P15 core on a live session: the
// budget is a hard bound on unread payload (strict admission), the peer is genuinely backpressured
// while the application is not reading, and the drain returns every byte and exactly zero accounting.
func TestReceiveQuotaBoundsUnreadPayloadAndReturnsEverything(t *testing.T) {
	policy := receivebudget.DefaultNetworkExtension()
	router, port := startQuotaInbound(t, quotaStreamingLength, quotaPushChunk)
	runtime := newQuotaClient(t, port, "off", &policy)
	require.Equal(t, policy.UnreadPayloadBytes, runtime.config.ReceiveByteQuota,
		"the exclusive session under test must actually carry the budget")
	session := dialQuotaSession(t, runtime, M.ParseSocksaddr("192.0.2.10:443"))

	route := nextRoute(t, router)
	pushed, maxGauge := awaitParked(t, route, session.UnreadPayloadBytes)
	t.Logf("parked after %d pushed bytes; gauge max %d, quota %d", pushed, maxGauge, policy.UnreadPayloadBytes)
	require.Less(t, pushed, int64(quotaStreamingLength),
		"the server must not be able to push the whole stream into a reader that is not reading")
	require.LessOrEqual(t, maxGauge, policy.UnreadPayloadBytes,
		"unread payload exceeded the budget: admission is not strict")
	require.Greater(t, maxGauge, int64(0), "some payload must be parked for the bound to mean anything")

	after := drainSession(t, session, quotaStreamingLength)
	require.Equal(t, int64(0), after, "every byte must be returned exactly once, leaving no residue")
	select {
	case <-route.pushDone:
	case <-time.After(quotaTestDeadline):
		t.Fatal("the server never finished pushing after the client drained the stream")
	}
	require.Equal(t, int64(quotaStreamingLength), route.accepted.Load(),
		"the server must have pushed exactly the stream, no more and no less")
	require.Equal(t, int64(0), session.UnreadPayloadBytes(),
		"the gauge must stay at zero once the stream is consumed")
}

// TestReceiveQuotaCloseReleasesAParkedReader covers the failure mode a quota introduces: a reader
// parked on a full budget must be released by Close, not held until the peer gives up. The oracle is
// the peer: its blocked write has to end (progress or error) within the deadline, and the client's
// gauge must return to zero.
func TestReceiveQuotaCloseReleasesAParkedReader(t *testing.T) {
	policy := receivebudget.Policy{UnreadPayloadBytes: int64(mieruprotocol.MinReceiveByteQuota)}
	router, port := startQuotaInbound(t, quotaStreamingLength, quotaPushChunk)
	runtime := newQuotaClient(t, port, "off", &policy)
	session := dialQuotaSession(t, runtime, M.ParseSocksaddr("192.0.2.10:443"))

	route := nextRoute(t, router)
	pushed, _ := awaitParked(t, route, session.UnreadPayloadBytes)
	require.Greater(t, pushed, int64(0))
	require.Less(t, pushed, int64(quotaStreamingLength), "the reader must be parked for this case")

	require.NoError(t, session.Close())

	// The parked admission waits on the session's closed channel; Close must release it and let the
	// underlay reader continue, which shows up as the server's write no longer being stuck.
	progressed := make(chan int64, 1)
	go func() {
		before := route.accepted.Load()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if route.accepted.Load() != before {
				progressed <- route.accepted.Load()
				return
			}
			select {
			case <-route.pushDone:
				progressed <- route.accepted.Load()
				return
			default:
			}
			time.Sleep(10 * time.Millisecond)
		}
		progressed <- -1
	}()
	select {
	case observed := <-progressed:
		require.NotEqual(t, int64(-1), observed,
			"Close did not release the parked quota admission: the peer's write stayed stuck")
		t.Logf("after Close the server progressed to %d bytes", observed)
	case <-time.After(15 * time.Second):
		t.Fatal("Close did not release the parked quota admission")
	}

	// Closing deliberately keeps the payload the application can still read (recvQueue and unreadBuf)
	// accounted; it releases only what Read can no longer reach. So the second half of the invariant
	// is that the parked bytes remain readable exactly once and the gauge reaches zero when they are
	// consumed - not that Close zeroes it, and certainly not that Close leaves it behind.
	require.NoError(t, session.SetReadDeadline(time.Now().Add(10*time.Second)))
	buffer := make([]byte, quotaPushChunk)
	var consumed int64
	for {
		read, readErr := session.Read(buffer)
		for index := int64(0); index < int64(read); index++ {
			require.Equal(t, quotaPayload(consumed+index), buffer[index],
				"byte %d read after Close is not the pushed stream", consumed+index)
		}
		consumed += int64(read)
		if readErr != nil {
			break
		}
		if read == 0 {
			break
		}
	}
	t.Logf("Close left %d readable bytes; gauge after reading them: %d", consumed, session.UnreadPayloadBytes())
	require.Greater(t, consumed, int64(0), "payload parked at Close must remain readable")
	require.LessOrEqual(t, consumed, policy.UnreadPayloadBytes,
		"Close must not let more than the budget become readable")
	require.Equal(t, int64(0), session.UnreadPayloadBytes(),
		"consuming the parked payload must return its accounting exactly once")
}

// TestASlowSessionMustNotStallAConcurrentSession is P16: with upstream multiplexing (two sessions per
// underlay) one session whose application stopped reading must not stop another session's traffic.
//
// `multiplexing: middle` is what makes sessions share an underlay in the SDK, and the budget must be
// absent from this profile by construction - a shared reader may never be parked by one session, which
// is exactly why applyReceiveBudget refuses to write a quota here. The test asserts that refusal, so
// the profile it exercises is the profile its name claims.
//
// What the protocol may do with a session that cannot keep up on a shared underlay is fail that
// session, not wait for it: its bounded receive queue fills only when the application has stopped
// reading, and waiting there would stall the single reader that carries every other session. The
// regression therefore has two parts: while one session is overloaded another session on the same
// connection must be establishable and must complete round trips, and the overloaded session must end
// (its already queued prefix stays readable, then reads return EOF) instead of parking its reader
// forever.
func TestASlowSessionMustNotStallAConcurrentSession(t *testing.T) {
	const slowPushLength = 10 << 20
	router, port := startQuotaInbound(t, slowPushLength, slowPushChunk)

	runtime := newQuotaClientWithUnderlayBudget(t, port, "middle", crossSessionUnderlayBudget)
	require.Zero(t, runtime.config.ReceiveByteQuota,
		"a shared reader must not carry a quota")

	slowSession := dialQuotaSession(t, runtime, M.ParseSocksaddr("192.0.2.10:443"))
	slowRoute := nextRoute(t, router)
	pushed, slowGauge := awaitParked(t, slowRoute, slowSession.UnreadPayloadBytes)
	t.Logf("slow session: server wrote %d of %d, gauge max %d, gauge now %d",
		pushed, int64(slowPushLength), slowGauge, slowSession.UnreadPayloadBytes())
	require.Greater(t, pushed, int64(0), "the slow session must have reached its receive window")
	require.Less(t, pushed, int64(slowPushLength), "the slow session must be parked for this case")

	// dial opens one session and consumes its routed connection, keeping the fake server's route
	// channel aligned with the dials.
	dial := func(destination M.Socksaddr, timeout time.Duration) (*mieruprotocol.Session, error) {
		netAddr, err := toNetAddrSpec(destination, N.NetworkTCP)
		if err != nil {
			return nil, err
		}
		dialCtx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		conn, err := runtime.client.DialContext(dialCtx, netAddr)
		if err != nil {
			return nil, err
		}
		session, isSession := conn.(*mieruprotocol.Session)
		if !isSession {
			_ = conn.Close()
			return nil, fmt.Errorf("unexpected connection type %T", conn)
		}
		nextRoute(t, router)
		return session, nil
	}

	// The mux picks an existing underlay only probabilistically, so a second dial may ask the dialer
	// for a fresh connection. The dialer admits one socket, so that attempt fails with
	// errUnderlayBudgetExhausted instead of quietly measuring a different connection; the test then
	// retries until the second session really shares the slow session's underlay. Every successful
	// attempt is logged, so `-count` runs can prove the shared-reader oracle was exercised every time,
	// and exhausting the attempts fails the test instead of passing without it.
	stalledAt := slowRoute.accepted.Load()
	var fastSession *mieruprotocol.Session
	freshConnectionAttempts := 0
	for attempt := 1; attempt <= crossSessionUnderlayAttempts; attempt++ {
		probe, err := dial(M.ParseSocksaddr("198.51.100.20:80"), 10*time.Second)
		if err != nil {
			if errors.Is(err, errUnderlayBudgetExhausted) || strings.Contains(err.Error(), errUnderlayBudgetExhausted.Error()) {
				freshConnectionAttempts++
				t.Logf("attempt %d/%d: the mux asked for a fresh connection (not admitted); retrying on the shared underlay",
					attempt, crossSessionUnderlayAttempts)
				continue
			}
			t.Fatalf("cross-session head-of-line blocking on a shared underlay: while one session was "+
				"parked at %d bytes (gauge %d), establishing a second session failed with %q. The shared "+
				"underlay's single reader is blocked by the one session that cannot consume its receive "+
				"queue (mieru fork: pkg/protocol/underlay_base.go deliverSegmentToSession, called from "+
				"the reader loop in pkg/protocol/underlay_stream.go)",
				stalledAt, slowGauge, err)
		}
		if probe.LocalAddr().String() != slowSession.LocalAddr().String() {
			require.NoError(t, probe.Close(), "closing a probe session that landed on another connection")
			t.Logf("attempt %d/%d: the second session landed on another connection; retrying",
				attempt, crossSessionUnderlayAttempts)
			continue
		}
		fastSession = probe
		t.Logf("shared-reader round trips exercised on attempt %d/%d (%d fresh-connection attempts skipped)",
			attempt, crossSessionUnderlayAttempts, freshConnectionAttempts)
		break
	}
	if fastSession == nil {
		t.Fatalf("the mux did not place the second session on the slow session's shared underlay within "+
			"%d attempts; the cross-session oracle was not exercised, so this run proves nothing",
			crossSessionUnderlayAttempts)
	}

	for round := 0; round < 20; round++ {
		payload := make([]byte, 1024)
		for index := range payload {
			payload[index] = byte(round*17 + index)
		}
		require.NoError(t, fastSession.SetDeadline(time.Now().Add(10*time.Second)))
		_, err := fastSession.Write(payload)
		require.NoError(t, err, "round trip %d: the second session must be writable", round)
		echoed := make([]byte, len(payload))
		_, err = io.ReadFull(fastSession, echoed)
		require.NoError(t, err, "round trip %d: the second session must be readable", round)
		require.Equal(t, payload, echoed, "round trip %d: echoed bytes must be identical", round)
		require.Equal(t, stalledAt, slowRoute.accepted.Load(),
			"round trip %d: the second session's progress must not require the parked session to advance", round)
	}
	t.Logf("20 second-session round trips completed while the slow session stayed parked at %d bytes", stalledAt)

	// The overloaded session must be failed instead of parked: everything the protocol already queued
	// stays readable, then reads end with EOF. Before the fork fix the same read consumed the whole
	// 10 MiB stream (the shared reader resumed as soon as the application read) and never reached EOF.
	require.NoError(t, slowSession.SetReadDeadline(time.Now().Add(10*time.Second)))
	buffer := make([]byte, quotaPushChunk)
	var consumed int64
	var readErr error
	for {
		var read int
		read, readErr = slowSession.Read(buffer)
		for index := 0; index < read; index++ {
			require.Equal(t, quotaPayload(consumed+int64(index)), buffer[index],
				"byte %d read from the overloaded session is not the pushed stream", consumed+int64(index))
		}
		consumed += int64(read)
		if readErr != nil {
			break
		}
	}
	require.ErrorIs(t, readErr, io.EOF,
		"the overloaded session must be closed, not left parked: a shared reader may not wait for it")
	require.Equal(t, int64(0), slowSession.UnreadPayloadBytes(),
		"the closed session's accounting must return to zero after its queued prefix is read")
	require.Greater(t, consumed, int64(0), "payload queued before the overload must remain readable")
	require.Less(t, consumed, int64(slowPushLength),
		"the overloaded session must not deliver the whole pushed stream")
	t.Logf("the overloaded session ended after %d readable bytes", consumed)

	// The shared connection and its remaining session survive the failed one.
	payload := make([]byte, 1024)
	for index := range payload {
		payload[index] = byte(index)
	}
	require.NoError(t, fastSession.SetDeadline(time.Now().Add(10*time.Second)))
	_, err := fastSession.Write(payload)
	require.NoError(t, err, "the shared connection must stay usable after the overloaded session failed")
	echoed := make([]byte, len(payload))
	_, err = io.ReadFull(fastSession, echoed)
	require.NoError(t, err, "the shared connection must stay readable after the overloaded session failed")
	require.Equal(t, payload, echoed)
}
