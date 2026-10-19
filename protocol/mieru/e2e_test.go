package mieru

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/json/badoption"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
	"github.com/stretchr/testify/require"

	"github.com/enfein/mieru/v3/pkg/cipher"
)

// echoRouter is the destination side of the loopback tests. It records the
// metadata the inbound passed to the router and echoes traffic, so the client
// sees its own bytes come back through the real Mieru data path.
//
// The embedded adapter.Router is nil on purpose: the inbound only calls the
// two Ex methods, and implementing the full Router surface would add sixty
// lines of test doubles that are never invoked.
type echoRouter struct {
	adapter.Router
	metadata chan adapter.InboundContext
}

func newEchoRouter() *echoRouter {
	return &echoRouter{metadata: make(chan adapter.InboundContext, 16)}
}

func (r *echoRouter) RouteConnectionEx(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	select {
	case r.metadata <- metadata:
	default:
	}
	defer conn.Close()
	buffer := make([]byte, 4096)
	for {
		n, err := conn.Read(buffer)
		if n > 0 {
			if _, writeErr := conn.Write(buffer[:n]); writeErr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func (r *echoRouter) RoutePacketConnectionEx(ctx context.Context, conn N.PacketConn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	select {
	case r.metadata <- metadata:
	default:
	}
	defer conn.Close()
	packet := buf.NewPacket()
	defer packet.Release()
	for {
		packet.Reset()
		destination, err := conn.ReadPacket(packet)
		if err != nil {
			return
		}
		if err = conn.WritePacket(packet, destination); err != nil {
			return
		}
	}
}

func nopLogger() log.ContextLogger {
	return log.NewNOPFactory().Logger()
}

func freePortForBothTransports(t *testing.T) uint16 {
	t.Helper()
	for attempt := 0; attempt < 32; attempt++ {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		port := uint16(listener.Addr().(*net.TCPAddr).Port)
		require.NoError(t, listener.Close())
		packetConn, err := net.ListenPacket("udp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			continue
		}
		require.NoError(t, packetConn.Close())
		return port
	}
	t.Fatal("could not find a port free for both TCP and UDP")
	return 0
}

func startTestInbound(t *testing.T, options option.MieruInboundOptions) (*Inbound, *echoRouter, *adapter.Scope) {
	t.Helper()
	router := newEchoRouter()
	created, err := NewInbound(context.Background(), router, nopLogger(), "mieru-in", options)
	require.NoError(t, err)
	inboundInstance, ok := created.(*Inbound)
	require.True(t, ok)
	scope := adapter.NewScope(context.Background(), nopLogger())
	require.NoError(t, inboundInstance.Start(adapter.StartStateStart, scope))
	t.Cleanup(func() { _ = scope.Close() })
	return inboundInstance, router, scope
}

func startTestOutbound(t *testing.T, options option.MieruOutboundOptions) (*Outbound, *adapter.Scope) {
	t.Helper()
	created, err := NewOutbound(context.Background(), nil, nopLogger(), "mieru-out", options)
	require.NoError(t, err)
	outboundInstance, ok := created.(*Outbound)
	require.True(t, ok)
	scope := adapter.NewScope(context.Background(), nopLogger())
	require.NoError(t, outboundInstance.Start(adapter.StartStateStart, scope))
	t.Cleanup(func() { _ = scope.Close() })
	return outboundInstance, scope
}

func inboundOptionsForPort(port uint16, transport string) option.MieruInboundOptions {
	options := validInboundOptions()
	options.ListenOptions.Listen = common.Ptr(badoption.Addr(netip.MustParseAddr("127.0.0.1")))
	options.ListenOptions.ListenPort = port
	options.Transport = transport
	return options
}

func outboundOptionsForPort(port uint16, transport string, user string, password string) option.MieruOutboundOptions {
	return option.MieruOutboundOptions{
		ServerOptions: option.ServerOptions{
			Server:     "127.0.0.1",
			ServerPort: port,
		},
		Transport: transport,
		UserName:  user,
		Password:  password,
	}
}

func readExactWithTimeout(t *testing.T, conn net.Conn, size int) []byte {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(10*time.Second)))
	defer conn.SetReadDeadline(time.Time{})
	received := make([]byte, size)
	_, err := io.ReadFull(conn, received)
	require.NoError(t, err)
	return received
}

// handoffRouter returns from RouteConnectionEx immediately without touching
// the connection, exactly as the real route layer does: it starts the copy
// goroutines and returns. The inbound must not close the connection when the
// route entry returns.
type handoffRouter struct {
	adapter.Router
	connection chan net.Conn
}

func (r *handoffRouter) RouteConnectionEx(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	r.connection <- conn
}

func TestInboundDoesNotCloseConnectionAfterHandoff(t *testing.T) {
	port := freePortForBothTransports(t)
	router := &handoffRouter{connection: make(chan net.Conn, 1)}
	created, err := NewInbound(context.Background(), router, nopLogger(), "mieru-in", inboundOptionsForPort(port, "tcp"))
	require.NoError(t, err)
	scope := adapter.NewScope(context.Background(), nopLogger())
	require.NoError(t, created.(*Inbound).Start(adapter.StartStateStart, scope))
	defer scope.Close()

	outboundInstance, _ := startTestOutbound(t, outboundOptionsForPort(port, "tcp", "alice", "alice-password"))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	clientConn, err := outboundInstance.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr("192.0.2.50:443"))
	require.NoError(t, err)
	defer clientConn.Close()

	var serverConn net.Conn
	select {
	case serverConn = <-router.connection:
	case <-time.After(10 * time.Second):
		t.Fatal("the connection was never routed")
	}
	defer serverConn.Close()

	// The route entry has returned by now. If the inbound closed the session
	// on the way out, this write/read pair fails.
	_, err = clientConn.Write([]byte("after handoff"))
	require.NoError(t, err)
	require.NoError(t, serverConn.SetReadDeadline(time.Now().Add(5*time.Second)))
	received := make([]byte, len("after handoff"))
	_, err = io.ReadFull(serverConn, received)
	require.NoError(t, err)
	require.Equal(t, []byte("after handoff"), received)

	_, err = serverConn.Write([]byte("reply"))
	require.NoError(t, err)
	require.Equal(t, []byte("reply"), readExactWithTimeout(t, clientConn, len("reply")))
}

func TestMieruTCPLoopback(t *testing.T) {
	port := freePortForBothTransports(t)
	_, router, _ := startTestInbound(t, inboundOptionsForPort(port, "tcp"))
	outboundInstance, _ := startTestOutbound(t, outboundOptionsForPort(port, "tcp", "alice", "alice-password"))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	destination := M.ParseSocksaddr("192.0.2.10:443")
	conn, err := outboundInstance.DialContext(ctx, N.NetworkTCP, destination)
	require.NoError(t, err)
	defer conn.Close()

	_, err = conn.Write([]byte("ping through mieru"))
	require.NoError(t, err)
	require.Equal(t, []byte("ping through mieru"), readExactWithTimeout(t, conn, len("ping through mieru")))

	metadata := <-router.metadata
	require.Equal(t, "mieru", metadata.InboundType)
	require.Equal(t, "mieru-in", metadata.Inbound)
	require.Equal(t, "alice", metadata.User)
	require.Equal(t, destination, metadata.Destination)
	require.True(t, metadata.Source.IsValid())
}

func TestMieruUDPOverStreamLoopback(t *testing.T) {
	port := freePortForBothTransports(t)
	_, router, _ := startTestInbound(t, inboundOptionsForPort(port, "tcp"))
	outboundInstance, _ := startTestOutbound(t, outboundOptionsForPort(port, "tcp", "alice", "alice-password"))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	destination := M.ParseSocksaddr("198.51.100.20:5353")
	packetConn, err := outboundInstance.ListenPacket(ctx, destination)
	require.NoError(t, err)
	defer packetConn.Close()

	payload := []byte("udp through mieru")
	_, err = packetConn.WriteTo(payload, destination)
	require.NoError(t, err)
	buffer := make([]byte, 1500)
	require.NoError(t, packetConn.SetReadDeadline(time.Now().Add(10*time.Second)))
	n, source, err := packetConn.ReadFrom(buffer)
	require.NoError(t, err)
	require.Equal(t, payload, buffer[:n])
	require.Equal(t, destination, M.SocksaddrFromNet(source))

	metadata := <-router.metadata
	require.Equal(t, "mieru", metadata.InboundType)
	require.Equal(t, "alice", metadata.User)
	require.Equal(t, destination, metadata.Destination)
}

func TestMieruUDPTransportLoopback(t *testing.T) {
	port := freePortForBothTransports(t)
	_, router, _ := startTestInbound(t, inboundOptionsForPort(port, "udp"))
	outboundInstance, _ := startTestOutbound(t, outboundOptionsForPort(port, "udp", "alice", "alice-password"))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	destination := M.ParseSocksaddr("203.0.113.30:1234")
	packetConn, err := outboundInstance.ListenPacket(ctx, destination)
	require.NoError(t, err)
	defer packetConn.Close()

	payload := []byte("udp transport through mieru")
	_, err = packetConn.WriteTo(payload, destination)
	require.NoError(t, err)
	buffer := make([]byte, 1500)
	require.NoError(t, packetConn.SetReadDeadline(time.Now().Add(10*time.Second)))
	n, _, err := packetConn.ReadFrom(buffer)
	require.NoError(t, err)
	require.Equal(t, payload, buffer[:n])

	metadata := <-router.metadata
	require.Equal(t, "mieru", metadata.InboundType)
	require.Equal(t, "alice", metadata.User)
	require.Equal(t, destination, metadata.Destination)
}

func TestMieruFQDDestinationRoundTrip(t *testing.T) {
	port := freePortForBothTransports(t)
	_, router, _ := startTestInbound(t, inboundOptionsForPort(port, "tcp"))
	outboundInstance, _ := startTestOutbound(t, outboundOptionsForPort(port, "tcp", "alice", "alice-password"))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	destination := M.Socksaddr{Fqdn: "example.com", Port: 443}
	conn, err := outboundInstance.DialContext(ctx, N.NetworkTCP, destination)
	require.NoError(t, err)
	defer conn.Close()

	_, err = conn.Write([]byte("fqdn destination"))
	require.NoError(t, err)
	require.Equal(t, []byte("fqdn destination"), readExactWithTimeout(t, conn, len("fqdn destination")))

	metadata := <-router.metadata
	require.Equal(t, destination, metadata.Destination,
		"the FQDN must survive the Mieru request and reach the router unchanged")
}

func freeIPv6PortForBothTransports(t *testing.T) uint16 {
	t.Helper()
	listener, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skip("IPv6 loopback is unavailable: ", err)
	}
	port := uint16(listener.Addr().(*net.TCPAddr).Port)
	require.NoError(t, listener.Close())
	packetConn, err := net.ListenPacket("udp6", fmt.Sprintf("[::1]:%d", port))
	if err != nil {
		t.Skip("IPv6 UDP loopback is unavailable: ", err)
	}
	require.NoError(t, packetConn.Close())
	return port
}

func TestMieruIPv6Loopback(t *testing.T) {
	port := freeIPv6PortForBothTransports(t)
	options := inboundOptionsForPort(port, "tcp")
	options.ListenOptions.Listen = common.Ptr(badoption.Addr(netip.MustParseAddr("::1")))
	_, router, _ := startTestInbound(t, options)

	outboundOptions := outboundOptionsForPort(port, "tcp", "alice", "alice-password")
	outboundOptions.Server = "::1"
	outboundInstance, _ := startTestOutbound(t, outboundOptions)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	destination := M.ParseSocksaddr("[2001:db8::10]:443")
	conn, err := outboundInstance.DialContext(ctx, N.NetworkTCP, destination)
	require.NoError(t, err)
	defer conn.Close()

	_, err = conn.Write([]byte("ipv6 destination"))
	require.NoError(t, err)
	require.Equal(t, []byte("ipv6 destination"), readExactWithTimeout(t, conn, len("ipv6 destination")))

	metadata := <-router.metadata
	require.Equal(t, destination, metadata.Destination)
	require.True(t, metadata.Source.Addr.Is6(), "the session source must be the IPv6 loopback")
}

// TestMieruOutboundDomainServerUsesForkDNS proves the server name is resolved
// by the fork's dialer rather than by the SDK: the test registers a DNS router
// that answers the name, sets the outbound server to a name, and expects the
// connection to reach the loopback listener.
func TestMieruOutboundDomainServerUsesForkDNS(t *testing.T) {
	port := freePortForBothTransports(t)
	startTestInbound(t, inboundOptionsForPort(port, "tcp"))
	ctx := service.ContextWith[adapter.DNSRouter](context.Background(), &staticDNSRouter{
		addresses: []netip.Addr{netip.MustParseAddr("127.0.0.1")},
	})

	options := outboundOptionsForPort(port, "tcp", "alice", "alice-password")
	options.Server = "mieru.test"
	created, err := NewOutbound(ctx, nil, nopLogger(), "mieru-out", options)
	require.NoError(t, err)
	outboundInstance := created.(*Outbound)
	scope := adapter.NewScope(ctx, nopLogger())
	require.NoError(t, outboundInstance.Start(adapter.StartStateStart, scope))
	defer scope.Close()

	dialCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, err := outboundInstance.DialContext(dialCtx, N.NetworkTCP, M.ParseSocksaddr("192.0.2.99:80"))
	require.NoError(t, err)
	defer conn.Close()
	_, err = conn.Write([]byte("domain server"))
	require.NoError(t, err)
	require.Equal(t, []byte("domain server"), readExactWithTimeout(t, conn, len("domain server")))
}

// TestMieruInboundPortRange proves listen_port_ranges is not just parsed: the
// additional port is really bound and a client configured for it reaches the
// same inbound. A one-port range is the minimal form the SDK's range parser
// accepts, and it keeps the test from depending on two consecutive free ports.
func TestMieruInboundPortRange(t *testing.T) {
	primaryPort := freePortForBothTransports(t)
	var rangePort uint16
	for attempt := 0; attempt < 32; attempt++ {
		candidate := freePortForBothTransports(t)
		if candidate != primaryPort {
			rangePort = candidate
			break
		}
	}
	require.NotZero(t, rangePort, "could not find a second free port")

	options := inboundOptionsForPort(primaryPort, "tcp")
	options.ListenPortRanges = []string{fmt.Sprintf("%d-%d", rangePort, rangePort)}
	_, router, _ := startTestInbound(t, options)

	outboundInstance, _ := startTestOutbound(t, outboundOptionsForPort(rangePort, "tcp", "alice", "alice-password"))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	destination := M.ParseSocksaddr("192.0.2.60:443")
	conn, err := outboundInstance.DialContext(ctx, N.NetworkTCP, destination)
	require.NoError(t, err, "the additional port from listen_port_ranges must be listening")
	defer conn.Close()
	_, err = conn.Write([]byte("range port"))
	require.NoError(t, err)
	require.Equal(t, []byte("range port"), readExactWithTimeout(t, conn, len("range port")))
	metadata := <-router.metadata
	require.Equal(t, destination, metadata.Destination)
}

func TestMieruInterfaceUpdateRebuildsAfterDial(t *testing.T) {
	port := freePortForBothTransports(t)
	startTestInbound(t, inboundOptionsForPort(port, "tcp"))
	outboundInstance, _ := startTestOutbound(t, outboundOptionsForPort(port, "tcp", "alice", "alice-password"))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	destination := M.ParseSocksaddr("192.0.2.61:443")
	first, err := outboundInstance.DialContext(ctx, N.NetworkTCP, destination)
	require.NoError(t, err)
	_, err = first.Write([]byte("before update"))
	require.NoError(t, err)
	require.Equal(t, []byte("before update"), readExactWithTimeout(t, first, len("before update")))
	require.NoError(t, first.Close())

	previous := outboundInstance.client
	outboundInstance.InterfaceUpdated(context.Background())
	require.NotSame(t, previous, outboundInstance.client, "after a dial a network change must replace the SDK client")
	require.True(t, outboundInstance.client.IsRunning())

	second, err := outboundInstance.DialContext(ctx, N.NetworkTCP, destination)
	require.NoError(t, err)
	defer second.Close()
	_, err = second.Write([]byte("after update"))
	require.NoError(t, err)
	require.Equal(t, []byte("after update"), readExactWithTimeout(t, second, len("after update")))
}

func TestMieruOutboundCloseTerminatesSessions(t *testing.T) {
	port := freePortForBothTransports(t)
	startTestInbound(t, inboundOptionsForPort(port, "tcp"))
	outboundInstance, _ := startTestOutbound(t, outboundOptionsForPort(port, "tcp", "alice", "alice-password"))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, err := outboundInstance.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr("192.0.2.70:443"))
	require.NoError(t, err)
	_, err = conn.Write([]byte("before close"))
	require.NoError(t, err)
	require.Equal(t, []byte("before close"), readExactWithTimeout(t, conn, len("before close")))

	require.NoError(t, outboundInstance.Close())
	require.False(t, outboundInstance.client.IsRunning(), "Close must actually stop the SDK client")

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(3*time.Second)))
	_, err = conn.Read(make([]byte, 1))
	require.Error(t, err, "an established session must not survive Close")
	var netErr net.Error
	require.False(t, errors.As(err, &netErr) && netErr.Timeout(),
		"session was still open after Close; read hit the deadline: %v", err)
	_, err = conn.Write([]byte("after close"))
	require.Error(t, err, "write on a closed session must fail")
}

func TestMieruNoWaitWrongPasswordDoesNotReportSuccess(t *testing.T) {
	port := freePortForBothTransports(t)
	startTestInbound(t, inboundOptionsForPort(port, "tcp"))
	options := outboundOptionsForPort(port, "tcp", "alice", "wrong-password")
	options.HandshakeMode = "no_wait"
	outboundInstance, _ := startTestOutbound(t, options)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, dialErr := outboundInstance.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr("192.0.2.71:443"))
	if dialErr != nil {
		// An early failure is also correct: the adapter must not hide it.
		return
	}
	defer conn.Close()
	_, writeErr := conn.Write([]byte("trigger the deferred handshake"))
	if writeErr != nil {
		return
	}
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(3*time.Second)))
	_, readErr := conn.Read(make([]byte, 1))
	require.Error(t, readErr, "wrong password with no_wait produced a usable-looking connection")
	var netErr net.Error
	require.False(t, errors.As(readErr, &netErr) && netErr.Timeout(),
		"the deferred handshake error never surfaced; read hit the deadline: %v", readErr)
}

func TestMieruUserHintMandatory(t *testing.T) {
	port := freePortForBothTransports(t)
	options := inboundOptionsForPort(port, "tcp")
	options.UserHintIsMandatory = true
	startTestInbound(t, options)

	outboundInstance, _ := startTestOutbound(t, outboundOptionsForPort(port, "tcp", "alice", "alice-password"))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, err := outboundInstance.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr("192.0.2.72:443"))
	require.NoError(t, err)
	defer conn.Close()
	_, err = conn.Write([]byte("user hint"))
	require.NoError(t, err)
	require.Equal(t, []byte("user hint"), readExactWithTimeout(t, conn, len("user hint")))
}

func TestMieruInboundBindFailureIsClean(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer occupied.Close()
	port := uint16(occupied.Addr().(*net.TCPAddr).Port)

	created, err := NewInbound(context.Background(), newEchoRouter(), nopLogger(), "mieru-in", inboundOptionsForPort(port, "tcp"))
	require.NoError(t, err)
	inboundInstance := created.(*Inbound)
	scope := adapter.NewScope(context.Background(), nopLogger())
	require.Error(t, inboundInstance.Start(adapter.StartStateStart, scope))
	require.NoError(t, inboundInstance.Close())
	require.True(t, inboundInstance.closed)
}

func requireTCPPortFree(t *testing.T, port uint16) {
	t.Helper()
	require.Eventually(t, func() bool {
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			return false
		}
		_ = listener.Close()
		return true
	}, 3*time.Second, 25*time.Millisecond, "TCP port %d is still bound", port)
}

func requireUDPPortFree(t *testing.T, port uint16) {
	t.Helper()
	require.Eventually(t, func() bool {
		packetConn, err := net.ListenPacket("udp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			return false
		}
		_ = packetConn.Close()
		return true
	}, 3*time.Second, 25*time.Millisecond, "UDP port %d is still bound", port)
}

func TestMieruInboundCloseReleasesListener(t *testing.T) {
	port := freePortForBothTransports(t)
	inboundInstance, _, _ := startTestInbound(t, inboundOptionsForPort(port, "both"))
	require.NoError(t, inboundInstance.Close())
	require.Zero(t, inboundInstance.listeners.count())

	requireTCPPortFree(t, port)
	requireUDPPortFree(t, port)
}

func TestMieruInboundPartialStartClosesTheFirstListener(t *testing.T) {
	port := freePortForBothTransports(t)
	occupiedUDP, err := net.ListenPacket("udp", fmt.Sprintf("127.0.0.1:%d", port))
	require.NoError(t, err)
	defer occupiedUDP.Close()

	created, err := NewInbound(context.Background(), newEchoRouter(), nopLogger(), "mieru-in", inboundOptionsForPort(port, "both"))
	require.NoError(t, err)
	inboundInstance := created.(*Inbound)
	scope := adapter.NewScope(context.Background(), nopLogger())
	require.Error(t, inboundInstance.Start(adapter.StartStateStart, scope), "the occupied UDP port must fail the start")
	require.NoError(t, inboundInstance.Close())
	require.Zero(t, inboundInstance.listeners.count())

	// The TCP listener may have been created before the UDP bind failed. It
	// must have been closed by the partial-start cleanup, otherwise the port
	// stays unavailable until the process exits.
	requireTCPPortFree(t, port)
}

func TestMieruInboundPartialStartClosesTheUDPListener(t *testing.T) {
	port := freePortForBothTransports(t)
	occupiedTCP, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	require.NoError(t, err)
	defer occupiedTCP.Close()

	created, err := NewInbound(context.Background(), newEchoRouter(), nopLogger(), "mieru-in", inboundOptionsForPort(port, "both"))
	require.NoError(t, err)
	inboundInstance := created.(*Inbound)
	scope := adapter.NewScope(context.Background(), nopLogger())
	require.Error(t, inboundInstance.Start(adapter.StartStateStart, scope), "the occupied TCP port must fail the start")
	require.NoError(t, inboundInstance.Close())
	require.Zero(t, inboundInstance.listeners.count())

	requireUDPPortFree(t, port)
}

func TestMieruInboundCanRebindAfterClose(t *testing.T) {
	port := freePortForBothTransports(t)
	first, _, _ := startTestInbound(t, inboundOptionsForPort(port, "both"))
	require.NoError(t, first.Close())
	requireTCPPortFree(t, port)
	requireUDPPortFree(t, port)

	// A reload constructs a new Inbound on the same port; the previous Close
	// must have released both sockets.
	second, _, _ := startTestInbound(t, inboundOptionsForPort(port, "both"))
	require.NoError(t, second.Close())
}

// Close must release the server's UDP underlay read.
//
// The SDK creates a PacketUnderlay for the UDP listener at Start and its event
// loop sits in a ReadFrom with a 60-120 s deadline. Waking that read once (which
// is all PacketUnderlay.Close does before closing sessions) is not enough: the
// woken loop can re-arm a fresh deadline before the underlay's done channel is
// closed, and Close then waits for it. Measured at baseline c363e04: the rebind
// test above took exactly 65 s in 2/10 runs. Closing the PacketConn we own,
// before asking the SDK to stop, makes the read return immediately and
// permanently; the cycles here turn an intermittent re-arm into a reliable
// failure of this test on the old ordering.
func TestMieruInboundCloseReleasesTheUDPUnderlayPromptly(t *testing.T) {
	for cycle := 0; cycle < 20; cycle++ {
		port := freePortForBothTransports(t)
		inboundInstance, _, _ := startTestInbound(t, inboundOptionsForPort(port, "both"))
		start := time.Now()
		require.NoError(t, inboundInstance.Close())
		elapsed := time.Since(start)
		require.Less(t, elapsed, closeWaitTimeout,
			"cycle %d: Close took %s; a blocked UDP underlay read was not released", cycle, elapsed)
		requireTCPPortFree(t, port)
		requireUDPPortFree(t, port)
	}
}

func TestMieruInboundCanRetryAfterBindFailure(t *testing.T) {
	port := freePortForBothTransports(t)
	occupied, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	require.NoError(t, err)

	created, err := NewInbound(context.Background(), newEchoRouter(), nopLogger(), "mieru-in", inboundOptionsForPort(port, "tcp"))
	require.NoError(t, err)
	inboundInstance := created.(*Inbound)
	scope := adapter.NewScope(context.Background(), nopLogger())
	require.Error(t, inboundInstance.Start(adapter.StartStateStart, scope))
	require.NoError(t, occupied.Close(), "release the port before the retry")
	require.NoError(t, inboundInstance.Start(adapter.StartStateStart, scope), "a failed Start must be retryable")
	require.True(t, inboundInstance.server.IsRunning())
	require.Equal(t, 1, inboundInstance.listeners.count(), "the retried listener must be registered")
	require.NoError(t, inboundInstance.Close())
	require.Zero(t, inboundInstance.listeners.count(), "Close must close the registered listener")
	requireTCPPortFree(t, port)
}

func TestMieruHashedPasswordInterop(t *testing.T) {
	port := freePortForBothTransports(t)
	hashed := hex.EncodeToString(cipher.HashPassword([]byte("alice-password"), []byte("alice")))

	// Server stores only the hash, client has the plaintext.
	serverOptions := inboundOptionsForPort(port, "tcp")
	serverOptions.Users = []option.MieruUser{{Name: "alice", HashedPassword: hashed}}
	startTestInbound(t, serverOptions)
	outboundInstance, _ := startTestOutbound(t, outboundOptionsForPort(port, "tcp", "alice", "alice-password"))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, err := outboundInstance.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr("192.0.2.73:443"))
	require.NoError(t, err)
	_, err = conn.Write([]byte("hashed server"))
	require.NoError(t, err)
	require.Equal(t, []byte("hashed server"), readExactWithTimeout(t, conn, len("hashed server")))
	require.NoError(t, conn.Close())

	// Client stores only the hash, server has the plaintext.
	plainPort := freePortForBothTransports(t)
	startTestInbound(t, inboundOptionsForPort(plainPort, "tcp"))
	clientOptions := outboundOptionsForPort(plainPort, "tcp", "alice", "")
	clientOptions.HashedPassword = hashed
	hashedClient, _ := startTestOutbound(t, clientOptions)
	hashCtx, hashCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer hashCancel()
	hashConn, err := hashedClient.DialContext(hashCtx, N.NetworkTCP, M.ParseSocksaddr("192.0.2.74:443"))
	require.NoError(t, err)
	defer hashConn.Close()
	_, err = hashConn.Write([]byte("hashed client"))
	require.NoError(t, err)
	require.Equal(t, []byte("hashed client"), readExactWithTimeout(t, hashConn, len("hashed client")))
}

func TestMieruOutboundCloseTerminatesPacketSession(t *testing.T) {
	port := freePortForBothTransports(t)
	startTestInbound(t, inboundOptionsForPort(port, "tcp"))
	outboundInstance, _ := startTestOutbound(t, outboundOptionsForPort(port, "tcp", "alice", "alice-password"))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	destination := M.ParseSocksaddr("192.0.2.75:53")
	packetConn, err := outboundInstance.ListenPacket(ctx, destination)
	require.NoError(t, err)
	defer packetConn.Close()
	_, err = packetConn.WriteTo([]byte("packet before close"), destination)
	require.NoError(t, err)
	require.NoError(t, packetConn.SetReadDeadline(time.Now().Add(10*time.Second)))
	buffer := make([]byte, 64)
	n, _, err := packetConn.ReadFrom(buffer)
	require.NoError(t, err)
	require.Equal(t, "packet before close", string(buffer[:n]))

	require.NoError(t, outboundInstance.Close())
	require.NoError(t, packetConn.SetReadDeadline(time.Now().Add(3*time.Second)))
	_, _, err = packetConn.ReadFrom(buffer)
	require.Error(t, err, "an established UDP association must not survive Close")
	var netErr net.Error
	require.False(t, errors.As(err, &netErr) && netErr.Timeout(),
		"packet session was still open after Close; read hit the deadline: %v", err)
}

func TestMieruOutboundDomainServerUDPUsesForkDNS(t *testing.T) {
	port := freePortForBothTransports(t)
	startTestInbound(t, inboundOptionsForPort(port, "udp"))
	ctx := service.ContextWith[adapter.DNSRouter](context.Background(), &staticDNSRouter{
		addresses: []netip.Addr{netip.MustParseAddr("127.0.0.1")},
	})

	options := outboundOptionsForPort(port, "udp", "alice", "alice-password")
	options.Server = "mieru.test"
	created, err := NewOutbound(ctx, nil, nopLogger(), "mieru-out", options)
	require.NoError(t, err)
	outboundInstance := created.(*Outbound)
	scope := adapter.NewScope(ctx, nopLogger())
	require.NoError(t, outboundInstance.Start(adapter.StartStateStart, scope))
	defer scope.Close()

	dialCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, err := outboundInstance.DialContext(dialCtx, N.NetworkTCP, M.ParseSocksaddr("192.0.2.91:80"))
	require.NoError(t, err)
	defer conn.Close()
	_, err = conn.Write([]byte("udp domain server"))
	require.NoError(t, err)
	require.Equal(t, []byte("udp domain server"), readExactWithTimeout(t, conn, len("udp domain server")))
}

func TestMieruInterfaceUpdateClosesOldSessions(t *testing.T) {
	port := freePortForBothTransports(t)
	startTestInbound(t, inboundOptionsForPort(port, "tcp"))
	outboundInstance, _ := startTestOutbound(t, outboundOptionsForPort(port, "tcp", "alice", "alice-password"))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	destination := M.ParseSocksaddr("192.0.2.92:443")
	oldConn, err := outboundInstance.DialContext(ctx, N.NetworkTCP, destination)
	require.NoError(t, err)
	_, err = oldConn.Write([]byte("old network"))
	require.NoError(t, err)
	require.Equal(t, []byte("old network"), readExactWithTimeout(t, oldConn, len("old network")))

	previous := outboundInstance.client
	outboundInstance.InterfaceUpdated(context.Background())
	require.NotSame(t, previous, outboundInstance.client)

	require.NoError(t, oldConn.SetReadDeadline(time.Now().Add(3*time.Second)))
	_, err = oldConn.Read(make([]byte, 1))
	require.Error(t, err, "the old network's session must be terminated")
	var netErr net.Error
	require.False(t, errors.As(err, &netErr) && netErr.Timeout(),
		"old session still open after InterfaceUpdated: %v", err)

	newConn, err := outboundInstance.DialContext(ctx, N.NetworkTCP, destination)
	require.NoError(t, err)
	defer newConn.Close()
	_, err = newConn.Write([]byte("new network"))
	require.NoError(t, err)
	require.Equal(t, []byte("new network"), readExactWithTimeout(t, newConn, len("new network")))
}

func TestMieruOutboundUDPCloseReleasesUnderlaySockets(t *testing.T) {
	port := freePortForBothTransports(t)
	startTestInbound(t, inboundOptionsForPort(port, "udp"))
	outboundInstance, _ := startTestOutbound(t, outboundOptionsForPort(port, "udp", "alice", "alice-password"))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, err := outboundInstance.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr("192.0.2.93:443"))
	require.NoError(t, err)
	require.Equal(t, 1, outboundInstance.packetDialer.packetConnCount())
	require.NoError(t, conn.Close())
	require.NoError(t, outboundInstance.Close())
	require.Zero(t, outboundInstance.packetDialer.packetConnCount(), "Close must release the UDP underlay socket")
}

func TestMieruInterfaceUpdateReopensUDPUnderlay(t *testing.T) {
	port := freePortForBothTransports(t)
	startTestInbound(t, inboundOptionsForPort(port, "udp"))
	outboundInstance, _ := startTestOutbound(t, outboundOptionsForPort(port, "udp", "alice", "alice-password"))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	destination := M.ParseSocksaddr("192.0.2.94:443")
	first, err := outboundInstance.DialContext(ctx, N.NetworkTCP, destination)
	require.NoError(t, err)
	require.Equal(t, 1, outboundInstance.packetDialer.packetConnCount())
	require.NoError(t, first.Close())

	outboundInstance.InterfaceUpdated(context.Background())
	require.Zero(t, outboundInstance.packetDialer.packetConnCount(), "the old generation's UDP socket must be released")

	second, err := outboundInstance.DialContext(ctx, N.NetworkTCP, destination)
	require.NoError(t, err)
	require.Equal(t, 1, outboundInstance.packetDialer.packetConnCount(), "the new generation must be able to open a UDP socket")
	require.NoError(t, second.Close())
}

func TestMieruInboundCanRetryAfterUDPBindFailure(t *testing.T) {
	port := freePortForBothTransports(t)
	occupied, err := net.ListenPacket("udp", fmt.Sprintf("127.0.0.1:%d", port))
	require.NoError(t, err)

	created, err := NewInbound(context.Background(), newEchoRouter(), nopLogger(), "mieru-in", inboundOptionsForPort(port, "udp"))
	require.NoError(t, err)
	inboundInstance := created.(*Inbound)
	scope := adapter.NewScope(context.Background(), nopLogger())
	require.Error(t, inboundInstance.Start(adapter.StartStateStart, scope))
	require.NoError(t, occupied.Close())
	require.NoError(t, inboundInstance.Start(adapter.StartStateStart, scope), "a failed UDP Start must be retryable")
	require.True(t, inboundInstance.server.IsRunning())
	require.NoError(t, inboundInstance.Close())

	packetConn, err := net.ListenPacket("udp", fmt.Sprintf("127.0.0.1:%d", port))
	require.NoError(t, err, "the retried UDP listener must be released")
	require.NoError(t, packetConn.Close())
}

func TestMieruInboundCloseTerminatesPacketSessions(t *testing.T) {
	port := freePortForBothTransports(t)
	inboundInstance, _, _ := startTestInbound(t, inboundOptionsForPort(port, "tcp"))
	outboundInstance, _ := startTestOutbound(t, outboundOptionsForPort(port, "tcp", "alice", "alice-password"))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	destination := M.ParseSocksaddr("192.0.2.95:53")
	packetConn, err := outboundInstance.ListenPacket(ctx, destination)
	require.NoError(t, err)
	defer packetConn.Close()
	_, err = packetConn.WriteTo([]byte("inbound packet"), destination)
	require.NoError(t, err)
	require.NoError(t, packetConn.SetReadDeadline(time.Now().Add(10*time.Second)))
	buffer := make([]byte, 64)
	n, _, err := packetConn.ReadFrom(buffer)
	require.NoError(t, err)
	require.Equal(t, "inbound packet", string(buffer[:n]))

	require.NoError(t, inboundInstance.Close())
	require.NoError(t, packetConn.SetReadDeadline(time.Now().Add(3*time.Second)))
	_, _, err = packetConn.ReadFrom(buffer)
	require.Error(t, err, "the association must be closed when the inbound closes")
	var netErr net.Error
	require.False(t, errors.As(err, &netErr) && netErr.Timeout(),
		"packet association was still open after inbound Close: %v", err)
}

func TestMieruMultiUser(t *testing.T) {
	port := freePortForBothTransports(t)
	_, router, _ := startTestInbound(t, inboundOptionsForPort(port, "tcp"))

	for _, user := range []struct {
		name     string
		password string
	}{
		{"alice", "alice-password"},
		{"bob", "bob-password"},
	} {
		outboundInstance, _ := startTestOutbound(t, outboundOptionsForPort(port, "tcp", user.name, user.password))
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		conn, err := outboundInstance.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr("192.0.2.11:80"))
		require.NoError(t, err)
		_, err = conn.Write([]byte("hello " + user.name))
		require.NoError(t, err)
		require.Equal(t, []byte("hello "+user.name), readExactWithTimeout(t, conn, len("hello ")+len(user.name)))
		metadata := <-router.metadata
		require.Equal(t, user.name, metadata.User)
		require.NoError(t, conn.Close())
		cancel()
	}
}

func TestMieruInboundStartOnClosingScopeDoesNotDeadlock(t *testing.T) {
	port := freePortForBothTransports(t)
	created, err := NewInbound(context.Background(), newEchoRouter(), nopLogger(), "mieru-in", inboundOptionsForPort(port, "tcp"))
	require.NoError(t, err)
	inboundInstance := created.(*Inbound)

	scope := adapter.NewScope(context.Background(), nopLogger())
	require.NoError(t, scope.Close())

	done := make(chan error, 1)
	go func() {
		done <- inboundInstance.Start(adapter.StartStateStart, scope)
	}()
	select {
	case err = <-done:
		require.Error(t, err, "a cleanup that ran synchronously during Start must be reported, not returned as success")
	case <-time.After(10 * time.Second):
		t.Fatal("Start deadlocked while registering cleanup on a closing scope")
	}
	require.True(t, inboundInstance.closed)
	require.False(t, inboundInstance.server.IsRunning())
}

// TestMieruInboundCloseTerminatesSessions pins the half of the lifecycle the
// SDK documentation does not promise: closing the inbound must also end
// sessions it already accepted, rather than leaving the destination copy
// waiting on a stream whose server has gone away.
func TestMieruInboundCloseTerminatesSessions(t *testing.T) {
	port := freePortForBothTransports(t)
	inboundInstance, _, scope := startTestInbound(t, inboundOptionsForPort(port, "tcp"))
	outboundInstance, _ := startTestOutbound(t, outboundOptionsForPort(port, "tcp", "alice", "alice-password"))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, err := outboundInstance.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr("192.0.2.12:80"))
	require.NoError(t, err)
	defer conn.Close()
	_, err = conn.Write([]byte("hold"))
	require.NoError(t, err)
	require.Equal(t, []byte("hold"), readExactWithTimeout(t, conn, 4))

	require.NoError(t, scope.Close())
	require.False(t, inboundInstance.server.IsRunning())

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	buffer := make([]byte, 1)
	_, readErr := conn.Read(buffer)
	require.Error(t, readErr, "the client connection must be closed when the inbound closes")
}
