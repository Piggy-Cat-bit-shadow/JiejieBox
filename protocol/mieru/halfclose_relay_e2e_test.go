package mieru

// This test proves the product half-close path, not just that the SDK type has
// a method: a real client Outbound connection is handed to the real
// route.ConnectionManager relay, and the destination only writes its response
// after it has read the request to EOF. The response can only arrive if the
// relay's upload direction ended the Mieru session's write side instead of
// closing the whole session.
//
// It also runs the same flow with the connection wrapped in a type that hides
// CloseWrite. That is exactly the pre-fix state of the SDK connection, and the
// response is lost - the control that proves this test can fail for the reason
// it claims to test.

import (
	"bytes"
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/route"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// recordingOutbound wraps the real Mieru outbound, so the test can inspect the
// exact connection the relay consumed. hideCloseWrite makes the returned
// connection deny the optional writer interface, reproducing the old SDK shape.
type recordingOutbound struct {
	adapter.Outbound
	hideCloseWrite bool

	access sync.Mutex
	dialed net.Conn
}

func (o *recordingOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	conn, err := o.Outbound.DialContext(ctx, network, destination)
	if err != nil {
		return nil, err
	}
	var returned net.Conn = conn
	if o.hideCloseWrite {
		returned = hideCloseWriteConn{Conn: conn}
	}
	o.access.Lock()
	o.dialed = returned
	o.access.Unlock()
	return returned, nil
}

func (o *recordingOutbound) dialedConn() net.Conn {
	o.access.Lock()
	defer o.access.Unlock()
	return o.dialed
}

// hideCloseWriteConn is a net.Conn without CloseWrite, and without the
// replaceable-upstream marker sing uses to unwrap to the real connection, so
// route/conn.go's type assertion fails exactly as it did before the fork added
// CloseWrite to *protocol.Session.
type hideCloseWriteConn struct {
	net.Conn
}

// destinationResult is what the destination observed.
type destinationResult struct {
	request  []byte
	readErr  error
	writeErr error
}

// destinationDialer dials the local destination socket from the server side of
// the relay. The Mieru destination address is not used: the inbound already
// authenticated the flow, and the test destination is a fixed local port.
type destinationDialer struct {
	address string
}

func (d destinationDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(ctx, "tcp", d.address)
}

func (d destinationDialer) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, io.ErrClosedPipe
}

// blockingDestinationRelay is the inbound router: it hands the accepted Mieru
// session to the real relay, which dials the test destination.
type blockingDestinationRelay struct {
	adapter.Router
	manager     *route.ConnectionManager
	destination string
}

func (r *blockingDestinationRelay) RouteConnectionEx(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	r.manager.NewConnection(ctx, destinationDialer{address: r.destination}, conn, metadata, onClose)
}

func (r *blockingDestinationRelay) RoutePacketConnectionEx(ctx context.Context, conn N.PacketConn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	_ = conn.Close()
}

// startHalfCloseRelayPair starts a real Mieru inbound and outbound pair.
func startHalfCloseRelayPair(t *testing.T, transport string, router *blockingDestinationRelay) *Outbound {
	t.Helper()
	port := freePortForBothTransports(t)

	createdInbound, err := NewInbound(context.Background(), router, nopLogger(), "mieru-in", inboundOptionsForPort(port, transport))
	require.NoError(t, err)
	inbound, ok := createdInbound.(*Inbound)
	require.True(t, ok)
	inScope := adapter.NewScope(context.Background(), nopLogger())
	require.NoError(t, inbound.Start(adapter.StartStateStart, inScope))
	t.Cleanup(func() { _ = inScope.Close() })

	createdOutbound, err := NewOutbound(context.Background(), nil, nopLogger(), "mieru-out", outboundOptionsForPort(port, transport, "alice", "alice-password"))
	require.NoError(t, err)
	outbound, ok := createdOutbound.(*Outbound)
	require.True(t, ok)
	outScope := adapter.NewScope(context.Background(), nopLogger())
	require.NoError(t, outbound.Start(adapter.StartStateStart, outScope))
	t.Cleanup(func() { _ = outScope.Close() })

	return outbound
}

// runRelayHalfCloseFlow drives one app-side flow through the relay and returns
// what the app socket and the destination observed.
func runRelayHalfCloseFlow(t *testing.T, transport string, hideCloseWrite bool) (appRead []byte, appErr error, destination destinationResult) {
	t.Helper()
	request := bytes.Repeat([]byte("POST /upload HTTP/1.1\r\nContent-Length: 0\r\n\r\n"), 16)
	response := bytes.Repeat([]byte("HTTP/1.1 200 OK\r\nContent-Length: 17\r\n\r\nthe-response-body"), 32)

	// The destination only writes after it read the request to EOF.
	destinationListener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = destinationListener.Close() })
	releaseDestination := make(chan struct{})
	destinationCh := make(chan destinationResult, 1)
	go func() {
		var result destinationResult
		conn, acceptErr := destinationListener.Accept()
		if acceptErr != nil {
			result.readErr = acceptErr
			destinationCh <- result
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		result.request, result.readErr = io.ReadAll(conn)
		if result.readErr == nil {
			// Only reachable after the client's write direction ended at the
			// server application.
			_, result.writeErr = conn.Write(response)
		}
		select {
		case <-releaseDestination:
		case <-time.After(15 * time.Second):
		}
		destinationCh <- result
	}()

	router := &blockingDestinationRelay{
		manager:     route.NewConnectionManager(log.NewNOPFactory().Logger()),
		destination: destinationListener.Addr().String(),
	}
	t.Cleanup(func() { _ = router.manager.Close() })
	outbound := startHalfCloseRelayPair(t, transport, router)

	// The app side of the flow: a real TCP socket that the relay reads from.
	appListener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = appListener.Close() })
	appAccepted := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := appListener.Accept()
		if acceptErr == nil {
			appAccepted <- conn
		}
	}()
	appClient, err := net.Dial("tcp", appListener.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = appClient.Close() })

	var appConn net.Conn
	select {
	case appConn = <-appAccepted:
	case <-time.After(10 * time.Second):
		t.Fatal("the relay did not accept the app connection")
	}
	t.Cleanup(func() { _ = appConn.Close() })

	recorder := &recordingOutbound{Outbound: outbound, hideCloseWrite: hideCloseWrite}
	clientManager := route.NewConnectionManager(log.NewNOPFactory().Logger())
	t.Cleanup(func() { _ = clientManager.Close() })
	clientManager.NewConnection(context.Background(), recorder, appConn,
		adapter.InboundContext{Destination: M.ParseSocksaddrHostPort("destination.example", 80)},
		func(error) {})

	// Wait until the relay has dialed through the Mieru outbound, then assert
	// the exact expression route/conn.go uses to choose CloseWrite.
	var relayConn net.Conn
	require.Eventually(t, func() bool {
		relayConn = recorder.dialedConn()
		return relayConn != nil
	}, 10*time.Second, 5*time.Millisecond, "the relay never dialed through the Mieru outbound")

	destinationWriter, _ := N.UnwrapCountWriter(relayConn, nil)
	duplexDestination, isDuplex := N.UnwrapWriter(destinationWriter).(N.WriteCloser)
	if hideCloseWrite {
		require.False(t, isDuplex,
			"the control wrapper must not satisfy N.WriteCloser, or it does not reproduce the old SDK shape")
	} else {
		require.True(t, isDuplex,
			"the relay's N.UnwrapWriter(conn).(N.WriteCloser) assertion must see the Mieru session's CloseWrite")
		t.Logf("relay destination %T satisfies N.WriteCloser", duplexDestination)
	}

	if _, err = appClient.Write(request); err != nil {
		return nil, err, destinationResult{}
	}
	tcpClient, ok := appClient.(*net.TCPConn)
	require.True(t, ok)
	require.NoError(t, tcpClient.CloseWrite())

	// Read exactly the response. The app-side EOF is not awaited here: the
	// server side of this test keeps the destination open until the response
	// has been observed, so that a lost response can never be confused with a
	// slow close.
	responseBuf := make([]byte, len(response))
	_ = appClient.SetReadDeadline(time.Now().Add(15 * time.Second))
	n, appErr := io.ReadFull(appClient, responseBuf)
	appRead = responseBuf[:n]
	_ = appClient.SetReadDeadline(time.Time{})
	close(releaseDestination)

	select {
	case destination = <-destinationCh:
	case <-time.After(15 * time.Second):
		t.Fatal("the destination never finished")
	}

	if hideCloseWrite {
		return appRead, appErr, destination
	}

	// The destination's read must have ended only because the request
	// direction ended at the server application, and its response must have
	// crossed back through the relay unchanged.
	require.NoError(t, destination.readErr,
		"the destination must read the request to EOF; this is the server-side effect of the client CloseWrite")
	require.Equal(t, request, destination.request)
	require.NoError(t, destination.writeErr, "the destination could not write its response")
	require.NoError(t, appErr, "the complete post-EOF response must reach the app through the relay")
	require.Equal(t, response, appRead)
	t.Logf("relay delivered %d post-EOF response bytes over %s", len(appRead), transport)
	return appRead, appErr, destination
}

func TestMieruHalfCloseDeliversPostEOFResponseThroughRelay(t *testing.T) {
	for _, transport := range []string{"tcp", "udp"} {
		t.Run(transport, func(t *testing.T) {
			runRelayHalfCloseFlow(t, transport, false)
		})
	}
}

// TestMieruFullCloseControlLosesPostEOFResponse is the parent-level control:
// the same relay and the same destination, but the connection returned to the
// relay hides CloseWrite. The relay falls back to a full close, exactly as it
// does with the pre-fix SDK, and the response is lost.
func TestMieruFullCloseControlLosesPostEOFResponse(t *testing.T) {
	appRead, _, destination := runRelayHalfCloseFlow(t, "tcp", true)
	require.Empty(t, appRead,
		"the full-close fallback must not deliver the post-EOF response; if it does, the success case proves nothing")
	t.Logf("control: app received %d response bytes, destination read error %v, write error %v",
		len(appRead), destination.readErr, destination.writeErr)
}
