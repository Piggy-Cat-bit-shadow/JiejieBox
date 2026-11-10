//go:build with_quic

package http

import (
	"context"
	"errors"
	"net"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/quic-go"
	"github.com/sagernet/quic-go/http3"
	"github.com/sagernet/sing-box/common/tls"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// An HTTP/3 server that speaks HTTP/3 but does NOT advertise SETTINGS_ENABLE_CONNECT_PROTOCOL.
//
// This is the half-supported peer the version fallback has to survive: ordinary HTTP/3 works, so
// "is HTTP/3 up?" is yes; extended CONNECT does not, so a CONNECT request is illegal on this
// connection. A real http3.Server cannot be used here because quic-go's server always advertises
// the setting, so the server below is deliberately a raw QUIC peer that writes a control stream
// with an EMPTY SETTINGS frame - and then records whether the client wrote anything on a request
// stream.
type noExtendedConnectH3Server struct {
	address      string
	connections  atomic.Int64
	requestBytes atomic.Int64
}

func (s *noExtendedConnectH3Server) serveConn(conn *quic.Conn) {
	// HTTP/3 control stream: type 0x00, then a SETTINGS frame (0x04) with an empty payload.
	// No SETTINGS_ENABLE_CONNECT_PROTOCOL, and no other settings either.
	control, err := conn.OpenUniStream()
	if err != nil {
		return
	}
	if _, err = control.Write([]byte{0x00, 0x04, 0x00}); err != nil {
		return
	}
	for {
		stream, acceptErr := conn.AcceptStream(context.Background())
		if acceptErr != nil {
			return
		}
		// The request header is exactly what must not appear before the SETTINGS check.
		_ = stream.SetReadDeadline(time.Now().Add(2 * time.Second))
		buffer := make([]byte, 4096)
		count, _ := stream.Read(buffer)
		s.requestBytes.Add(int64(count))
	}
}

func startNoExtendedConnectH3Server(t *testing.T) *noExtendedConnectH3Server {
	t.Helper()
	tlsConfig := testServerTLSConfig(t)
	tlsConfig.NextProtos = []string{http3.NextProtoH3}
	packetConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	listener, err := quic.ListenEarly(packetConn, tlsConfig, &quic.Config{
		HandshakeIdleTimeout: 5 * time.Second,
	})
	require.NoError(t, err)
	server := &noExtendedConnectH3Server{address: packetConn.LocalAddr().String()}
	go func() {
		for {
			conn, acceptErr := listener.Accept(context.Background())
			if acceptErr != nil {
				return
			}
			server.connections.Add(1)
			go server.serveConn(conn)
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		_ = packetConn.Close()
	})
	return server
}

// loopbackDialer (defined in tunnel_transport_test.go) carries QUIC datagrams to the loopback
// server; recordingDialer (defined in client_h3_fallback_window_test.go) fails loudly so a fallback
// that reached it is observable.

func newNoExtendedConnectClient(t *testing.T, address string, tcpDialer N.Dialer) *Client {
	t.Helper()
	tlsConfig, err := tls.NewSTDClient(t.Context(), logger.NOP(), "localhost", option.OutboundTLSOptions{
		Enabled:    true,
		ServerName: "localhost",
		Insecure:   true,
		ALPN:       []string{"h3"},
	})
	require.NoError(t, err)
	udpDialer := &loopbackDialer{}
	client := &Client{
		dialer:            udpDialer,
		http1Dialer:       tcpDialer,
		authorityOverride: address,
		version:           3,
		server:            M.ParseSocksaddr(address),
	}
	h3, err := newHTTP3Client(ClientOptions{
		Dialer:    udpDialer,
		TLSConfig: tlsConfig,
		Server:    M.ParseSocksaddr(address),
		Authority: address,
		Version:   3,
	}, "")
	require.NoError(t, err)
	client.http3 = h3
	client.http3Authority = address
	return client
}

// TestHTTP3CONNECTIsNotSentWithoutExtendedConnect is the finding.
//
// The client waited for SETTINGS and then sent the Extended CONNECT anyway, without checking that
// the setting which makes the request legal was enabled. Against a peer that supports ordinary
// HTTP/3 but not CONNECT, the server saw an illegal request and the caller saw a rejection that
// looked like the peer refusing the tunnel. Nothing may be written before the check.
func TestHTTP3CONNECTIsNotSentWithoutExtendedConnect(t *testing.T) {
	server := startNoExtendedConnectH3Server(t)
	client := newNoExtendedConnectClient(t, server.address, &recordingDialer{})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := client.http3.OpenTunnel(ctx, tunnelRequest{
		protocol:            "connect-ip",
		url:                 &url.URL{Path: "/.well-known/masque/ip/"},
		destination:         client.server,
		originAuthorization: true,
	})
	require.Error(t, err)

	// The stream was opened (it must be, to learn SETTINGS), but no request header may appear on
	// it. Give the server goroutine a moment to read whatever was written. This is asserted
	// BEFORE the error shape, because "the peer saw an illegal request" is the defect itself.
	time.Sleep(100 * time.Millisecond)
	require.Zero(t, server.requestBytes.Load(),
		"no CONNECT header may be written before SETTINGS_ENABLE_CONNECT_PROTOCOL is checked")
	require.ErrorIs(t, err, errHTTP3ExtendedConnectUnsupported,
		"a server that did not enable extended CONNECT is a CAPABILITY answer, not a transport failure")
}

// TestHTTP3CONNECTCapabilityIsRememberedAndFallsBack is the consequence.
//
// A capability is not a transport failure: it must not charge the backoff ladder, it must suppress
// further HTTP/3 tunnel attempts until the network changes, and the setup that discovered it must
// still fall back so this tunnel can be established.
func TestHTTP3CONNECTCapabilityIsRememberedAndFallsBack(t *testing.T) {
	server := startNoExtendedConnectH3Server(t)
	tcpDialer := &recordingDialer{err: errors.New("the TCP fallback was reached")}
	client := newNoExtendedConnectClient(t, server.address, tcpDialer)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := client.OpenTunnel(ctx, "connect-ip", "/.well-known/masque/ip/")
	require.ErrorContains(t, err, "the TCP fallback was reached",
		"the setup that learned the capability must still fall back")
	require.False(t, client.http3Available(),
		"a server without HTTP/3 extended CONNECT must not be asked again on this network")
	require.Zero(t, client.http3Broken.Load(),
		"a capability is not a transport failure: the backoff ladder must stay untouched")
	require.Zero(t, server.requestBytes.Load())

	connectionsAfterFirst := server.connections.Load()
	require.Equal(t, int64(1), connectionsAfterFirst)

	// A second setup skips HTTP/3 entirely: no new QUIC connection is opened.
	_, err = client.OpenTunnel(ctx, "connect-ip", "/.well-known/masque/ip/")
	require.ErrorContains(t, err, "the TCP fallback was reached")
	require.Equal(t, connectionsAfterFirst, server.connections.Load(),
		"a remembered capability must not cost another HTTP/3 attempt")

	// A network transition is the only thing that forgets it.
	client.ResetConnections()
	require.True(t, client.http3Available(),
		"a new network must be allowed to try HTTP/3 again")
}

// TestCapabilityObservationBelongsToItsNetwork pins the ordering rule for the capability memory.
//
// The store used to happen whenever the H2 request returned, with nothing tying it to the network
// the attempt started on. A setup that spent the network change in flight could therefore write
// "H2 cannot do extended CONNECT" AFTER ResetConnections cleared it, and every later setup skipped
// a working HTTP/2 path in favour of HTTP/1.1.
func TestCapabilityObservationBelongsToItsNetwork(t *testing.T) {
	client := &Client{http3: &http3ClientImpl{}}

	staleEpoch := client.networkEpoch.Load()
	client.ResetConnections()
	client.observeHTTP3ExtendedConnectUnsupported(staleEpoch, 1)
	require.True(t, client.http3Available(),
		"an observation from the network being left must not suppress HTTP/3 on the new one")

	currentEpoch := client.networkEpoch.Load()
	client.observeHTTP3ExtendedConnectUnsupported(currentEpoch, 2)
	require.False(t, client.http3Available(),
		"the current network's observation must be remembered")

	client.ResetConnections()
	require.True(t, client.http3Available(), "a transition forgets the capability")
}

func TestAuditOrdinaryH3ConnectDoesNotNeedExtendedSettings(t *testing.T) {
	server := startNoExtendedConnectH3Server(t)
	tcp := &recordingDialer{err: errors.New("unexpected TCP fallback")}
	client := newNoExtendedConnectClient(t, server.address, tcp)
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, err := client.DialContext(ctx, "tcp", M.ParseSocksaddr("example.org:443"))
	require.Error(t, err) // this peer captures headers but intentionally sends no response
	require.Positive(t, server.requestBytes.Load(), "ordinary CONNECT should reach the H3 peer despite missing Extended CONNECT settings")
}

func TestAuditResetDuringInFlightH3Dial(t *testing.T) {
	server := startNoExtendedConnectH3Server(t)
	client := newNoExtendedConnectClient(t, server.address, &recordingDialer{})
	entered := make(chan struct{})
	release := make(chan struct{})
	dialDone := make(chan struct{})
	impl := client.http3.(*http3ClientImpl)
	impl.connDialer = func(ctx context.Context, _ M.Socksaddr, _ HTTP3CandidateConnector) (net.Conn, *quic.Conn, error) {
		close(entered)
		<-release
		return nil, nil, errors.New("released")
	}
	go func() {
		defer close(dialDone)
		_, _ = client.DialContext(context.Background(), "tcp", M.ParseSocksaddr("example.org:443"))
	}()
	<-entered
	resetDone := make(chan struct{})
	go func() { client.ResetConnections(); close(resetDone) }()
	defer func() { close(release); <-dialDone; <-resetDone; _ = client.Close() }()
	select {
	case <-resetDone:
	case <-time.After(150 * time.Millisecond):
		t.Error("ResetConnections remains blocked behind the old H3 dial")
	}
}

// TestH3LifecycleLogDoesNotRaceWithReset pins the log/reset fix: the trace path used to read the
// cached connection FIELD outside the pool lock while ResetConnection nilled it under it. The
// detector is the assertion here, so the test runs the two paths concurrently; on Windows the
// detector is unavailable and the test still exercises the same interleavings.
func TestH3LifecycleLogDoesNotRaceWithReset(t *testing.T) {
	server := startNoExtendedConnectH3Server(t)
	client := newNoExtendedConnectClient(t, server.address, &recordingDialer{})
	impl := client.http3.(*http3ClientImpl)
	impl.SetLifecycleLogger(logger.NOP())
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := impl.acquire(ctx)
	require.NoError(t, err)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 60; i++ {
			client.ResetConnections()
			_, _ = impl.acquire(ctx)
		}
	}()
	for i := 0; i < 400; i++ {
		_, _ = impl.acquire(ctx)
	}
	<-done
}
