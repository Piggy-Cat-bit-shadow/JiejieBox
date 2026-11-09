package wireguard

// A real WireGuard pair in one process: the production endpoint on both sides, a real handshake over
// loopback UDP, and a business byte through the tunnel.
//
// The endpoint tests here pin port conflicts and MTU budgets; none of them establishes a session, so
// the data path had no live evidence. Two production endpoints are enough, because the endpoint's
// decrypted flows go to the router it was constructed with (`RouteConnectionEx`), so a router that
// dials the destination IS the server side - the same shape the MASQUE CONNECT-IP row uses.
//
// The AWG variant of the same row needs `with_awg`; without that tag the obfuscation options are an
// explicit error, which is exactly why the plain case is pinned here and the AWG case is recorded as
// needing that build tag.

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
	"github.com/sagernet/sing-box/route"
	"github.com/sagernet/sing/common/json/badoption"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/stretchr/testify/require"
)

// wgCarrierRouter is the server-side data path: it dials the fixture's echo and pipes both ways.
type wgCarrierRouter struct {
	adapter.Router
	target string
	flows  atomic.Int32
}

func (r *wgCarrierRouter) RouteConnectionEx(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	r.flows.Add(1)
	go func() {
		defer conn.Close()
		if onClose != nil {
			defer onClose(nil)
		}
		peer, err := net.Dial("tcp", r.target)
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

func (r *wgCarrierRouter) RoutePacketConnectionEx(context.Context, N.PacketConn, adapter.InboundContext, N.CloseHandlerFunc) {
}

func (r *wgCarrierRouter) RouteConnection(context.Context, net.Conn, adapter.InboundContext) error {
	return nil
}

func (r *wgCarrierRouter) RoutePacketConnection(context.Context, N.PacketConn, adapter.InboundContext) error {
	return nil
}

func startWGEcho(t *testing.T) (string, uint16) {
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
	return listener.Addr().String(), uint16(listener.Addr().(*net.TCPAddr).Port)
}

// freeUDPPort reserves a UDP port for the server endpoint, which has to be told its port up front.
func freeUDPPort(t *testing.T) uint16 {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	port := uint16(conn.LocalAddr().(*net.UDPAddr).Port)
	_ = conn.Close()
	return port
}

func startEndpoint(t *testing.T, tag string, router adapter.Router, options option.WireGuardEndpointOptions) *Endpoint {
	t.Helper()
	// A production endpoint requires the network manager in the service context (interface finder and
	// monitor); the same construction fixture the port-conflict test uses.
	networkManager, err := route.NewNetworkManager(context.Background(), log.NewNOPFactory().NewLogger("wg-nm"), option.RouteOptions{}, option.DNSOptions{})
	require.NoError(t, err)
	ctx := service.ContextWith[adapter.NetworkManager](context.Background(), networkManager)
	created, err := NewEndpoint(ctx, router, log.NewNOPFactory().NewLogger(tag), tag, options)
	require.NoError(t, err)
	endpoint, isEndpoint := created.(*Endpoint)
	require.True(t, isEndpoint)
	scope := adapter.NewScope(context.Background(), log.NewNOPFactory().NewLogger(tag+"-scope"))
	require.NoError(t, endpoint.Start(adapter.StartStateInitialize, scope))
	require.NoError(t, endpoint.Start(adapter.StartStateStart, scope))
	require.NoError(t, endpoint.Start(adapter.StartStatePostStart, scope))
	t.Cleanup(func() { _ = scope.Close() })
	return endpoint
}

func TestABusinessByteCrossesARealWireGuardPair(t *testing.T) {
	runWireGuardCarrier(t, option.AmneziaWGOptions{})
}

// runWireGuardCarrier is the shared body: a real pair, a real handshake, a business byte. The AWG
// test reuses it with obfuscation enabled on both ends.
func runWireGuardCarrier(t *testing.T, awg option.AmneziaWGOptions) {
	t.Helper()
	serverKey, err := wgtypes.GeneratePrivateKey()
	require.NoError(t, err)
	clientKey, err := wgtypes.GeneratePrivateKey()
	require.NoError(t, err)

	echoAddress, echoPort := startWGEcho(t)
	router := &wgCarrierRouter{target: echoAddress}
	serverPort := freeUDPPort(t)

	serverEndpoint := startEndpoint(t, "wg-server", router, option.WireGuardEndpointOptions{
		MTU:              1408,
		AmneziaWGOptions: awg,
		Address:          badoption.Listable[netip.Prefix]{netip.MustParsePrefix("10.66.0.1/24")},
		PrivateKey:       serverKey.String(),
		ListenPort:       serverPort,
		Peers: []option.WireGuardPeer{{
			PublicKey:  clientKey.PublicKey().String(),
			AllowedIPs: []netip.Prefix{netip.MustParsePrefix("10.66.0.2/32")},
		}},
	})
	require.NotNil(t, serverEndpoint)

	clientEndpoint := startEndpoint(t, "wg-client", nil, option.WireGuardEndpointOptions{
		MTU:              1408,
		AmneziaWGOptions: awg,
		Address:          badoption.Listable[netip.Prefix]{netip.MustParsePrefix("10.66.0.2/24")},
		PrivateKey:       clientKey.String(),
		Peers: []option.WireGuardPeer{{
			Address:    "127.0.0.1",
			Port:       serverPort,
			PublicKey:  serverKey.PublicKey().String(),
			AllowedIPs: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")},
		}},
	})
	require.NotNil(t, clientEndpoint)

	destination := M.SocksaddrFrom(netip.AddrFrom4([4]byte{10, 66, 0, 1}), echoPort)
	dialed := func() net.Conn {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		conn, dialErr := clientEndpoint.DialContext(ctx, N.NetworkTCP, destination)
		require.NoError(t, dialErr, "the WireGuard endpoint could not dial through the tunnel")
		return conn
	}
	var conn net.Conn
	require.Eventually(t, func() bool {
		candidate, dialErr := func() (net.Conn, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			return clientEndpoint.DialContext(ctx, N.NetworkTCP, destination)
		}()
		if dialErr != nil {
			return false
		}
		conn = candidate
		return true
	}, 20*time.Second, 100*time.Millisecond, "the WireGuard handshake never completed")
	require.NotNil(t, conn)
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	_, err = conn.Write([]byte("through-the-tunnel"))
	require.NoError(t, err)
	reply := make([]byte, len("through-the-tunnel"))
	_, err = io.ReadFull(conn, reply)
	require.NoError(t, err, "no reply crossed the WireGuard tunnel")
	require.Equal(t, "through-the-tunnel", string(reply))
	_ = conn.Close()
	require.GreaterOrEqual(t, router.flows.Load(), int32(1), "the flow never reached the server endpoint's router")
	_ = dialed
}
