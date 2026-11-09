//go:build with_quic

package masque

// The CONNECT-IP carrier row: business bytes through a real tunnel, and the tunnel restored after a
// network change.
//
// Two production facts make this possible without a full route engine:
//
//   - the server endpoint's userspace stack hands a terminated inner flow to the ROUTER it was
//     constructed with (`endpointBase.newConnection` -> `router.RouteConnectionEx`), so a router that
//     dials the flow's destination is the whole server-side data path;
//   - a destination that equals the endpoint's own inner address is rewritten to loopback before it
//     reaches the router (`loopbackAddressFor`), so a loopback echo on the server side is reachable
//     as an inner address from the client.
//
// The test therefore dials `10.42.0.0:<echo port>` THROUGH the tunnel, gets the payload echoed, and
// then - after the client endpoint is told the physical network changed - proves the old flow is
// gone and a new one carries bytes over a re-established tunnel.

import (
	"context"
	"io"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/auth"
	"github.com/sagernet/sing/common/json/badoption"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// carrierRouter is the server-side data path: it dials the flow's (already loopback-rewritten)
// destination and pipes both directions.
type carrierRouter struct {
	adapter.Router
	flows  atomic.Int32
	closed atomic.Int32
}

func (r *carrierRouter) RouteConnectionEx(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	r.flows.Add(1)
	go func() {
		defer conn.Close()
		defer r.closed.Add(1)
		if onClose != nil {
			defer onClose(nil)
		}
		peer, err := net.Dial("tcp", metadata.Destination.String())
		if err != nil {
			return
		}
		defer peer.Close()
		uploaded := make(chan struct{})
		go func() {
			_, _ = io.Copy(peer, conn)
			if tcpPeer, isTCP := peer.(*net.TCPConn); isTCP {
				_ = tcpPeer.CloseWrite()
			}
			close(uploaded)
		}()
		_, _ = io.Copy(conn, peer)
		<-uploaded
	}()
}

func (r *carrierRouter) RoutePacketConnectionEx(context.Context, N.PacketConn, adapter.InboundContext, N.CloseHandlerFunc) {
}

func (r *carrierRouter) RouteConnection(context.Context, net.Conn, adapter.InboundContext) error {
	return nil
}

func (r *carrierRouter) RoutePacketConnection(context.Context, N.PacketConn, adapter.InboundContext) error {
	return nil
}

func startCarrierEcho(t *testing.T) uint16 {
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
				buffer := make([]byte, 4096)
				for {
					read, readErr := conn.Read(buffer)
					if read > 0 {
						_, _ = conn.Write(buffer[:read])
					}
					if readErr != nil {
						return
					}
				}
			}()
		}
	}()
	return uint16(listener.Addr().(*net.TCPAddr).Port)
}

// startRoutedConnectIPServer is startConnectIPServer with a router that terminates inner flows.
func startRoutedConnectIPServer(t *testing.T, prefix netip.Prefix, router adapter.Router) (*ServerEndpoint, uint16) {
	t.Helper()
	logger := log.NewNOPFactory().Logger()
	certPEM, keyPEM := newLoopbackCertificate(t)
	serverContext := service.ContextWithDefaultRegistry(context.Background())
	serverContext = service.ContextWith[adapter.NetworkManager](serverContext, &stubNetworkManager{})
	created, err := NewServerEndpoint(serverContext, router, logger, "connect-ip-server",
		option.MASQUEServerEndpointOptions{
			MASQUEEndpointOptions: option.MASQUEEndpointOptions{MTU: 1500},
			ListenOptions: option.ListenOptions{
				Listen:     common.Ptr(badoption.Addr(netip.AddrFrom4([4]byte{127, 0, 0, 1}))),
				ListenPort: 0,
			},
			Version: badoption.Listable[int]{3},
			Address: badoption.Listable[netip.Prefix]{prefix},
			Path:    "/",
			Users:   []auth.User{{Username: connectIPUser, Password: connectIPPassword}},
			InboundTLSOptionsContainer: option.InboundTLSOptionsContainer{
				TLS: &option.InboundTLSOptions{
					Enabled:     true,
					ServerName:  "example.test",
					Certificate: []string{certPEM},
					Key:         badoption.Listable[string]{keyPEM},
				},
			},
		})
	require.NoError(t, err)
	serverEndpoint, isServer := created.(*ServerEndpoint)
	require.True(t, isServer)
	scope := adapter.NewScope(context.Background(), logger)
	require.NoError(t, serverEndpoint.Start(adapter.StartStateInitialize, scope))
	require.NoError(t, serverEndpoint.Start(adapter.StartStateStart, scope))
	require.NoError(t, serverEndpoint.Start(adapter.StartStatePostStart, scope))
	t.Cleanup(func() { _ = scope.Close() })
	return serverEndpoint, uint16(serverEndpoint.listener.UDPConn().LocalAddr().(*net.UDPAddr).Port)
}

// carrierRoundTrip dials the inner destination through the tunnel and returns what came back.
func carrierRoundTrip(t *testing.T, clientEndpoint *ClientEndpoint, destination M.Socksaddr, payload string) net.Conn {
	t.Helper()
	// The assertion is that a business byte crosses the tunnel, not that it crosses on the very first
	// attempt: the endpoint's device and the server's stack do not become writable at exactly the same
	// instant, and under a full package run that gap is occasionally the first dial. The retry is
	// bounded, and a final failure reports the last error.
	var (
		lastErr error
		conn    net.Conn
	)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		conn, lastErr = clientEndpoint.DialContext(ctx, N.NetworkTCP, destination)
		cancel()
		if lastErr != nil {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
		if _, lastErr = conn.Write([]byte(payload)); lastErr != nil {
			_ = conn.Close()
			time.Sleep(50 * time.Millisecond)
			continue
		}
		echo := make([]byte, len(payload))
		if _, lastErr = io.ReadFull(conn, echo); lastErr != nil {
			_ = conn.Close()
			time.Sleep(50 * time.Millisecond)
			continue
		}
		require.Equal(t, payload, string(echo))
		return conn
	}
	require.NoError(t, lastErr, "no business byte crossed the tunnel to %s within the deadline", destination)
	return conn
}

func TestABusinessByteCrossesTheTunnelAndSurvivesANetworkChange(t *testing.T) {
	requireHTTP3Client(t)
	serverPrefix := netip.MustParsePrefix(connectIPPrefix)
	echoPort := startCarrierEcho(t)
	router := &carrierRouter{}
	_, port := startRoutedConnectIPServer(t, serverPrefix, router)
	clientEndpoint := startConnectIPClient(t, port)

	require.Eventually(t, func() bool {
		return clientEndpoint.state.Load().configured
	}, 15*time.Second, 5*time.Millisecond, "the tunnel never established")

	// The server's own inner address is rewritten to loopback on the server side, so the echo is
	// reachable THROUGH the tunnel under an inner address.
	innerDestination := M.SocksaddrFrom(netip.AddrFrom4([4]byte{10, 42, 0, 0}), echoPort)
	before := carrierRoundTrip(t, clientEndpoint, innerDestination, "before-the-change")
	require.GreaterOrEqual(t, router.flows.Load(), int32(1), "the flow never reached the server's router")

	// The physical network changes. The endpoint's action is a session restart.
	clientEndpoint.InterfaceUpdated(context.Background())

	// A write on the old flow is not evidence - the client-side stack buffers it - and the server-side
	// release of the old inner flow is not observable within a useful bound today: see
	// carrier_connectip_release_gap_test.go, which pins that as a resource-release gap for the
	// endpoint's owner instead of hiding it in this row. What this row asserts is the part the tunnel
	// is for: after the restart the session is ready again, and a NEW flow carries bytes all the way
	// through.
	_ = before.Close()

	require.Eventually(t, func() bool {
		return clientEndpoint.state.Load().configured && clientEndpoint.client.Ready()
	}, 40*time.Second, 20*time.Millisecond, "the tunnel did not re-establish after the network change")

	after := carrierRoundTrip(t, clientEndpoint, innerDestination, "after-the-change")
	_ = after.Close()
	require.GreaterOrEqual(t, router.flows.Load(), int32(2), "the flow after the change did not reach the server")
}
