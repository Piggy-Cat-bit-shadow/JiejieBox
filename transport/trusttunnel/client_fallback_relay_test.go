//go:build with_quic

package trusttunnel

// The real black-hole acceptance: one node serves HTTP/2 over TCP and HTTP/3 over UDP on the SAME
// address and port, behind a local relay that forwards TCP and silently drops UDP. Nothing here is a
// mock: the client's HTTP/3 attempt sends real QUIC Initials into the black hole, waits out its real
// budget, then opens a real TLS/TCP HTTP/2 session and carries payload; when the relay starts
// forwarding again and the cooldown expires, a real demand recovers HTTP/3 while the stream that was
// already open on the HTTP/2 session keeps working.
//
// This is the scenario the earlier reports called BLOCKED_BY_ENVIRONMENT. It needs no firewall change
// and no administrator rights: the relay is an ordinary user-space socket pair, which is also why it
// runs unchanged on Linux CI.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing/common/auth"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

const relayBlackholeBudget = 3 * time.Second

// dualTestServer is one Service serving both transports on one port.
type dualTestServer struct {
	service *Service
	handler *testHandler
	address M.Socksaddr
	client  *testTLSConfig
}

func startDualProtocolTestServer(t *testing.T) *dualTestServer {
	t.Helper()
	handler := &testHandler{}
	service, err := NewService(ServiceOptions{
		Handler: handler,
		Users:   []auth.User{{Username: testUsername, Password: testPassword}},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = service.Close() })

	serverTLS, clientTLS := newTestTLS(t)
	var (
		tcpListener net.Listener
		udpConn     net.PacketConn
	)
	for attempt := 0; attempt < 20; attempt++ {
		tcpListener, err = net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		port := tcpListener.Addr().(*net.TCPAddr).Port
		udpConn, err = net.ListenPacket("udp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			break
		}
		_ = tcpListener.Close()
		tcpListener = nil
	}
	require.NotNil(t, tcpListener, "no port was free for both TCP and UDP")
	require.NotNil(t, udpConn)
	t.Cleanup(func() {
		_ = tcpListener.Close()
		_ = udpConn.Close()
	})

	// One clone per transport: ServeTCP prepends h2 to the ALPN list and ServeQUIC prepends h3, and
	// the two must not race on one config. Both clones carry the same certificate.
	tcpTLS := &testTLSConfig{serverTLS.config.Clone()}
	quicTLS := &testTLSConfig{serverTLS.config.Clone()}
	go func() { _ = service.ServeTCP(tcpListener, tcpTLS) }()
	go func() { _ = service.ServeQUIC(udpConn, quicTLS) }()

	return &dualTestServer{
		service: service,
		handler: handler,
		address: M.SocksaddrFromNet(tcpListener.Addr()),
		client:  clientTLS,
	}
}

// testRelay is the middlebox: a TCP proxy to the node and a UDP socket that either forwards to the
// node or swallows the datagram. The client dials the relay for both transports.
type testRelay struct {
	addr       M.Socksaddr
	serverTCP  M.Socksaddr
	serverUDP  M.Socksaddr
	tcpProxy   net.Listener
	udpSocket  net.PacketConn
	blackhole  atomic.Bool
	dropped    atomic.Int64
	forwarded  atomic.Int64
	returned   atomic.Int64
	writeErrs  atomic.Int64
	clientAddr atomic.Pointer[net.UDPAddr]
	closeOnce  sync.Once
}

func startTestRelay(t *testing.T, server M.Socksaddr) *testRelay {
	t.Helper()
	var (
		tcpProxy  net.Listener
		udpSocket net.PacketConn
		err       error
	)
	for attempt := 0; attempt < 20; attempt++ {
		tcpProxy, err = net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		port := tcpProxy.Addr().(*net.TCPAddr).Port
		udpSocket, err = net.ListenPacket("udp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			break
		}
		_ = tcpProxy.Close()
		tcpProxy = nil
	}
	require.NotNil(t, tcpProxy)
	require.NotNil(t, udpSocket)

	relay := &testRelay{
		addr:      M.SocksaddrFromNet(tcpProxy.Addr()),
		serverTCP: server,
		serverUDP: server,
		tcpProxy:  tcpProxy,
		udpSocket: udpSocket,
	}
	relay.blackhole.Store(true)
	go relay.serveTCP()
	go relay.serveUDP()
	t.Cleanup(relay.close)
	return relay
}

func (r *testRelay) close() {
	r.closeOnce.Do(func() {
		_ = r.tcpProxy.Close()
		_ = r.udpSocket.Close()
	})
}

// DialContext is the client's dialer: it always dials the relay, never the node.
func (r *testRelay) DialContext(ctx context.Context, network string, _ M.Socksaddr) (net.Conn, error) {
	return new(net.Dialer).DialContext(ctx, network, r.addr.String())
}

func (r *testRelay) ListenPacket(ctx context.Context, _ M.Socksaddr) (net.PacketConn, error) {
	var listenConfig net.ListenConfig
	return listenConfig.ListenPacket(ctx, "udp", "127.0.0.1:0")
}

func (r *testRelay) serveTCP() {
	for {
		clientConn, err := r.tcpProxy.Accept()
		if err != nil {
			return
		}
		go func() {
			defer clientConn.Close()
			serverConn, err := net.Dial("tcp", r.serverTCP.String())
			if err != nil {
				return
			}
			defer serverConn.Close()
			done := make(chan struct{}, 2)
			go func() {
				_, _ = io.Copy(serverConn, clientConn)
				done <- struct{}{}
			}()
			go func() {
				_, _ = io.Copy(clientConn, serverConn)
				done <- struct{}{}
			}()
			<-done
		}()
	}
}

func (r *testRelay) serveUDP() {
	outbound, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return
	}
	defer outbound.Close()
	// WriteToUDP needs a *net.UDPAddr: metadata.Socksaddr reports Network() == "socks", which a
	// PacketConn refuses, and an ignored error here would look exactly like a black hole.
	serverAddr := r.serverUDP.UDPAddr()
	buffer := make([]byte, 65535)
	reply := make([]byte, 65535)
	go func() {
		for {
			n, _, err := outbound.ReadFromUDP(reply)
			if err != nil {
				return
			}
			clientAddr := r.clientAddr.Load()
			if clientAddr == nil {
				continue
			}
			if _, err = r.udpSocket.WriteTo(reply[:n], clientAddr); err != nil {
				r.writeErrs.Add(1)
				continue
			}
			r.returned.Add(1)
		}
	}()
	for {
		n, from, err := r.udpSocket.ReadFrom(buffer)
		if err != nil {
			return
		}
		if udpAddr, ok := from.(*net.UDPAddr); ok {
			r.clientAddr.Store(udpAddr)
		}
		if r.blackhole.Load() {
			// The silent drop that makes the client pay a real QUIC timeout.
			r.dropped.Add(1)
			continue
		}
		if _, err = outbound.WriteToUDP(buffer[:n], serverAddr); err != nil {
			r.writeErrs.Add(1)
			continue
		}
		r.forwarded.Add(1)
	}
}

func (r *testRelay) setBlackhole(on bool) {
	r.blackhole.Store(on)
}

// fallbackSessionOf reports whether the client's current session came from the fallback.
func fallbackSessionOf(client *Client) bool {
	client.access.Lock()
	defer client.access.Unlock()
	return client.session != nil && client.session.fallbackH2
}

// http3SessionOf reports whether the client's current session is a real HTTP/3 one.
func http3SessionOf(client *Client) bool {
	client.access.Lock()
	defer client.access.Unlock()
	if client.session == nil {
		return false
	}
	_, isHTTP3 := client.session.conn.(*http3Session)
	return isHTTP3
}

var _ N.Dialer = (*testRelay)(nil)

func TestRealRelayBlackholeFallsBackToHTTP2(t *testing.T) {
	server := startDualProtocolTestServer(t)
	echo := startEchoServer(t)
	relay := startTestRelay(t, server.address)

	client, err := NewClient(ClientOptions{
		Dialer:       relay,
		Server:       relay.addr,
		TLSConfig:    server.client.Clone(),
		Protocol:     ProtocolHTTP3,
		QUICFallback: true,
		Username:     testUsername,
		Password:     testPassword,
		AppName:      "relay-blackhole",
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	// The budget stays at the shipped value: the point is that a real QUIC handshake is paid for.
	client.h3Budget = relayBlackholeBudget
	// The cooldown is shortened so the recovery half does not add half a minute to the test.
	cooldown := 400 * time.Millisecond
	client.h3Cooldown = cooldown

	ctx := testContext(t)

	// 1. Blackholed UDP: the real HTTP/3 attempt must time out, then HTTP/2 carries the payload.
	started := time.Now()
	first, err := client.DialContext(ctx, echo)
	elapsed := time.Since(started)
	require.NoError(t, err)
	require.GreaterOrEqual(t, elapsed, relayBlackholeBudget-500*time.Millisecond,
		"the relay swallowed the QUIC Initials, so the bounded HTTP/3 attempt had to wait its real budget")
	require.Less(t, elapsed, 15*time.Second)
	echoRoundTrip(t, first, 256*1024)
	require.True(t, fallbackSessionOf(client), "the session carrying the payload is the fallback's HTTP/2 one")
	require.Greater(t, relay.dropped.Load(), int64(0), "the black hole really swallowed QUIC datagrams")
	require.NoError(t, first.Close())

	// 2. Inside the cooldown a new creation must go straight to HTTP/2: no UDP datagram may leave.
	client.RetireSuspect()
	droppedBefore := relay.dropped.Load()
	started = time.Now()
	second, err := client.DialContext(ctx, echo)
	require.NoError(t, err)
	fastElapsed := time.Since(started)
	require.Less(t, fastElapsed, 1500*time.Millisecond, "inside the cooldown the session opens over HTTP/2 directly")
	require.Equal(t, droppedBefore, relay.dropped.Load(),
		"no HTTP/3 attempt may be made while the verdict is open")
	require.True(t, fallbackSessionOf(client))

	// 3. Restore UDP forwarding, let the cooldown expire, and make a new demand recover HTTP/3 while
	//    the stream opened on the HTTP/2 session keeps working.
	relay.setBlackhole(false)
	time.Sleep(cooldown + 100*time.Millisecond)
	third, err := client.DialContext(ctx, echo)
	require.NoError(t, err)
	require.True(t, http3SessionOf(client), "after the cooldown a new demand must recover the real HTTP/3 session")
	require.Greater(t, relay.forwarded.Load(), int64(0), "the restored relay forwarded the QUIC handshake")
	require.Greater(t, relay.returned.Load(), int64(0), "and the node's replies came back through it")
	require.Zero(t, relay.writeErrs.Load(), "the relay never failed a forwarding write")
	echoRoundTrip(t, third, 64*1024)
	echoRoundTrip(t, second, 64*1024) // the stream that was on the retired HTTP/2 session still works
	require.NoError(t, second.Close())
	require.NoError(t, third.Close())

	// 4. The recovered HTTP/3 session carries a real UDP association.
	packetConn, err := client.ListenPacket(ctx)
	require.NoError(t, err)
	defer packetConn.Close()
	destination := M.ParseSocksaddr("127.0.0.1:53")
	payload := bytes.Repeat([]byte{9}, 1200)
	_, err = packetConn.WriteTo(payload, destination.UDPAddr())
	require.NoError(t, err)
	received := make([]byte, 65535)
	n, source, err := packetConn.ReadFrom(received)
	require.NoError(t, err)
	require.Equal(t, destination, M.SocksaddrFromNet(source))
	require.Equal(t, payload, received[:n], "the UDP association over the recovered session carried the datagram")
}
