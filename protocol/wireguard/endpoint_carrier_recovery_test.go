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
// wireGuardPair is the real two-endpoint fixture both tests share: the production endpoints, a real
// handshake over loopback UDP, and the server-side router that carries the decrypted flow.
type wireGuardPair struct {
	client      *Endpoint
	server      *Endpoint
	router      *wgCarrierRouter
	destination M.Socksaddr
}

// startWireGuardPair builds the pair and waits until the tunnel can dial. It is the same shape the
// single-path acceptance used; splitting it out lets the same-peer test act on a live session.
func startWireGuardPair(t *testing.T, awg option.AmneziaWGOptions) *wireGuardPair {
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

	pair := &wireGuardPair{
		client:      clientEndpoint,
		server:      serverEndpoint,
		router:      router,
		destination: M.SocksaddrFrom(netip.AddrFrom4([4]byte{10, 66, 0, 1}), echoPort),
	}
	var probe net.Conn
	require.Eventually(t, func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		candidate, dialErr := clientEndpoint.DialContext(ctx, N.NetworkTCP, pair.destination)
		if dialErr != nil {
			return false
		}
		probe = candidate
		return true
	}, 20*time.Second, 100*time.Millisecond, "the WireGuard handshake never completed")
	if probe != nil {
		_ = probe.Close()
	}
	return pair
}

// exchangeThroughTunnel writes a payload through the pair and requires the echo.
func (p *wireGuardPair) exchangeThroughTunnel(t *testing.T, conn net.Conn, payload string) {
	t.Helper()
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	_, err := conn.Write([]byte(payload))
	require.NoError(t, err)
	reply := make([]byte, len(payload))
	_, err = io.ReadFull(conn, reply)
	require.NoError(t, err, "no reply crossed the WireGuard tunnel")
	require.Equal(t, payload, string(reply))
}

// dial opens one flow through the tunnel.
func (p *wireGuardPair) dial(t *testing.T) net.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, err := p.client.DialContext(ctx, N.NetworkTCP, p.destination)
	require.NoError(t, err, "the WireGuard endpoint could not dial through the tunnel")
	return conn
}

// runWireGuardCarrier is the shared body of the single-path acceptance: a real pair, a real
// handshake, a business byte. The AWG test reuses it with obfuscation enabled on both ends.
func runWireGuardCarrier(t *testing.T, awg option.AmneziaWGOptions) {
	t.Helper()
	pair := startWireGuardPair(t, awg)
	conn := pair.dial(t)
	pair.exchangeThroughTunnel(t, conn, "through-the-tunnel")
	_ = conn.Close()
	require.GreaterOrEqual(t, pair.router.flows.Load(), int32(1), "the flow never reached the server endpoint's router")
}

// TestASamePeerSessionSurvivesAnOuterBindRefresh is the same-peer outer-path part that can be
// measured on this host without two physical interfaces.
//
// The peer keys and the inner addresses do not change: what changes is the endpoint's outer socket.
// `InterfaceUpdated` schedules exactly the call made here synchronously (`Endpoint.BindUpdate` ->
// the device's bind update), and the transport's rebind tests already pin the lease, the late/duplicate
// rebind decisions and the old socket accounting. What they cannot show is a real session carrying
// bytes across the refresh, which is what this test adds: an established flow keeps echoing, a second
// refresh during traffic keeps it alive, and a flow opened afterwards works.
//
// The two-physical-path variant (and the same-peer roaming on a device) still needs the device: this
// host has one outer path and the fixture is loopback.
func TestASamePeerSessionSurvivesAnOuterBindRefresh(t *testing.T) {
	pair := startWireGuardPair(t, option.AmneziaWGOptions{})
	conn := pair.dial(t)
	pair.exchangeThroughTunnel(t, conn, "before-the-refresh")

	// The production network-change action on the endpoint, synchronous so the assertions below are
	// ordered after the refresh rather than racing it.
	require.NoError(t, pair.client.endpoint.BindUpdate())

	// The WG session is keyed, not socket-bound: the established flow must keep carrying bytes.
	pair.exchangeThroughTunnel(t, conn, "after-the-refresh")

	// A second refresh while the flow is live, so a duplicate/late rebind cannot strand it.
	require.NoError(t, pair.client.endpoint.BindUpdate())
	pair.exchangeThroughTunnel(t, conn, "after-the-second-refresh")

	// And a flow opened after the refresh works through the same peer and inner addresses.
	second := pair.dial(t)
	pair.exchangeThroughTunnel(t, second, "new-flow-after-the-refresh")
	_ = second.Close()
	_ = conn.Close()

	require.GreaterOrEqual(t, pair.router.flows.Load(), int32(2),
		"the flow opened after the refresh never reached the server endpoint's router")
	require.NotNil(t, pair.server)
}
