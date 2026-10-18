package mieru

import (
	"context"
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

	"github.com/stretchr/testify/require"
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
		require.NoError(t, err)
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
