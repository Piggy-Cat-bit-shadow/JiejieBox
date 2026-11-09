//go:build with_naive_outbound

package naive

// One production Naive outbound across a network change, end to end.
//
// The tests in this package pin the protocol's internals on both sides; what they never did is run
// the OUTBOUND against a real server, so the directional-reconnect row for Naive was open - and
// Naive is one of the four protocols the window names for that contrast.
//
// The server here is the production INBOUND (its own TLS, its own authentication, its own HTTP/2
// server), started on loopback over a fake router whose only job is to route the tunnelled CONNECT
// to a loopback echo. The outbound is the production one, with the inbound's certificate as its
// trust anchor: the outbound refuses `insecure`, which is exactly the kind of production constraint
// this fixture has to honour instead of working around.

import (
	"context"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	mdns "github.com/miekg/dns"
	"github.com/sagernet/sing-box/adapter"
	boxTLS "github.com/sagernet/sing-box/common/tls"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/auth"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/json/badoption"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

const (
	naiveCarrierUser     = "carrier"
	naiveCarrierPassword = "carrier-password"
	naiveCarrierSettle   = 15 * time.Second
)

// naiveCarrierRouter routes every tunnelled CONNECT to the loopback echo that stands in for the
// destination.
type naiveCarrierRouter struct {
	adapter.Router
	target  string
	streams atomic.Int32
}

func (r *naiveCarrierRouter) RouteConnectionEx(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	r.streams.Add(1)
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
			_ = peer.(*net.TCPConn).CloseWrite()
			close(uploaded)
		}()
		_, _ = io.Copy(conn, peer)
		<-uploaded
	}()
}

func (r *naiveCarrierRouter) RoutePacketConnectionEx(context.Context, N.PacketConn, adapter.InboundContext, N.CloseHandlerFunc) {
}

func (r *naiveCarrierRouter) RouteConnection(context.Context, net.Conn, adapter.InboundContext) error {
	return nil
}

func (r *naiveCarrierRouter) RoutePacketConnection(context.Context, N.PacketConn, adapter.InboundContext) error {
	return nil
}

func startNaiveCarrierEcho(t *testing.T) string {
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
	return listener.Addr().String()
}

// startNaiveCarrierInbound starts the production inbound on loopback and returns its port plus the
// certificate the outbound must trust.
func startNaiveCarrierInbound(t *testing.T, router adapter.Router) (uint16, string) {
	t.Helper()
	directory := t.TempDir()
	keyPEM, certPEM, err := boxTLS.GenerateCertificate(nil, nil, time.Now, "localhost", time.Now().Add(24*time.Hour))
	require.NoError(t, err)
	certificatePath := filepath.Join(directory, "cert.pem")
	keyPath := filepath.Join(directory, "key.pem")
	require.NoError(t, os.WriteFile(certificatePath, certPEM, 0o600))
	require.NoError(t, os.WriteFile(keyPath, keyPEM, 0o600))

	logger := log.NewNOPFactory().NewLogger("carrier-naive")
	created, err := NewInbound(context.Background(), router, logger, "naive-carrier",
		option.NaiveInboundOptions{
			ListenOptions: option.ListenOptions{
				Listen:     common.Ptr(badoption.Addr(netip.AddrFrom4([4]byte{127, 0, 0, 1}))),
				ListenPort: 0,
			},
			Users: []auth.User{{Username: naiveCarrierUser, Password: naiveCarrierPassword}},
			InboundTLSOptionsContainer: option.InboundTLSOptionsContainer{
				TLS: &option.InboundTLSOptions{
					Enabled:     true,
					ServerName:  "localhost",
					Certificate: []string{string(certPEM)},
					Key:         badoption.Listable[string]{string(keyPEM)},
				},
			},
		})
	require.NoError(t, err)
	naiveInbound, isInbound := created.(*Inbound)
	require.True(t, isInbound)
	scope := adapter.NewScope(context.Background(), logger)
	require.NoError(t, naiveInbound.Start(adapter.StartStateStart, scope))
	t.Cleanup(func() { _ = scope.Close() })
	tcpListener := naiveInbound.listener.TCPListener()
	require.NotNil(t, tcpListener, "the inbound did not publish a listener")
	return uint16(tcpListener.Addr().(*net.TCPAddr).Port), string(certPEM)
}

// carrierDNSRouter is the policy bridge the naive outbound requires. The server address is numeric,
// so no exchange is expected; the stub exists to satisfy construction.
type carrierDNSRouter struct{}

func (r *carrierDNSRouter) Start(stage adapter.StartStage, scope *adapter.Scope) error { return nil }
func (r *carrierDNSRouter) Close() error                                               { return nil }
func (r *carrierDNSRouter) Exchange(ctx context.Context, message *mdns.Msg, options adapter.DNSQueryOptions) (*mdns.Msg, error) {
	return nil, E.New("no DNS in this fixture")
}
func (r *carrierDNSRouter) ExchangeAsync(ctx context.Context, message *mdns.Msg, options adapter.DNSQueryOptions, callback func(*mdns.Msg, error)) {
	callback(nil, E.New("no DNS in this fixture"))
}
func (r *carrierDNSRouter) Lookup(ctx context.Context, domain string, options adapter.DNSQueryOptions) ([]netip.Addr, error) {
	return nil, E.New("no DNS in this fixture")
}
func (r *carrierDNSRouter) ClearCache()                                       {}
func (r *carrierDNSRouter) LookupReverseMapping(ip netip.Addr) (string, bool) { return "", false }
func (r *carrierDNSRouter) ResetNetwork()                                     {}

func newNaiveCarrierOutbound(t *testing.T, port uint16, certificate string) *Outbound {
	t.Helper()
	// The outbound requires a DNS router in the service context: Chromium's DNS goes through the
	// policy bridge, and a nil router fails construction inside cronet. A numeric server address
	// never needs a real exchange, so the stub only has to exist.
	ctx := service.ContextWithDefaultRegistry(context.Background())
	ctx = service.ContextWith[adapter.DNSRouter](ctx, &carrierDNSRouter{})
	created, err := NewOutbound(ctx, nil, log.NewNOPFactory().NewLogger("carrier-naive-out"), "naive-carrier-out",
		option.NaiveOutboundOptions{
			ServerOptions: option.ServerOptions{Server: "127.0.0.1", ServerPort: port},
			Username:      naiveCarrierUser,
			Password:      naiveCarrierPassword,
			OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{
				TLS: &option.OutboundTLSOptions{
					Enabled:     true,
					ServerName:  "localhost",
					Certificate: []string{certificate},
				},
			},
		})
	require.NoError(t, err)
	outbound, isOutbound := created.(*Outbound)
	require.True(t, isOutbound)
	// The Cronet client is started in StartStateStart, not Initialize: a fixture that stops at
	// Initialize leaves the client in Created, and every dial then waits on its started channel with no
	// timeout and no log line.
	scope := adapter.NewScope(context.Background(), log.NewNOPFactory().NewLogger("carrier-naive-out-scope"))
	require.NoError(t, outbound.Start(adapter.StartStateStart, scope))
	t.Cleanup(func() { _ = scope.Close() })
	t.Cleanup(func() { _ = outbound.client.Close() })
	return outbound
}

func naiveRoundTrip(t *testing.T, outbound *Outbound, payload string) net.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), naiveCarrierSettle)
	defer cancel()
	conn, err := outbound.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr("echo.test:443"))
	require.NoError(t, err)
	require.NotNil(t, conn)
	_ = conn.SetDeadline(time.Now().Add(naiveCarrierSettle))
	_, err = conn.Write([]byte(payload))
	require.NoError(t, err)
	echo := make([]byte, len(payload))
	_, err = io.ReadFull(conn, echo)
	require.NoError(t, err)
	require.Equal(t, payload, string(echo))
	return conn
}

func TestARealNaiveFlowSurvivesANetworkChangeByReconnecting(t *testing.T) {
	echo := startNaiveCarrierEcho(t)
	router := &naiveCarrierRouter{target: echo}
	port, certificate := startNaiveCarrierInbound(t, router)
	outbound := newNaiveCarrierOutbound(t, port, certificate)

	before := naiveRoundTrip(t, outbound, "before-the-change")
	require.EqualValues(t, 1, router.streams.Load(), "the first flow reached the inbound")

	// The production entry: what NetworkManager calls on every physical network change.
	outbound.InterfaceUpdated(context.Background())

	_ = before.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := before.Write([]byte("stale")); err == nil {
		t.Fatal("the pre-change Naive flow still accepted a write: the old session was not retired")
	}

	after := naiveRoundTrip(t, outbound, "after-the-change")
	_ = after.Close()
	require.EqualValues(t, 2, router.streams.Load(), "the flow after the change reached the inbound")
}
