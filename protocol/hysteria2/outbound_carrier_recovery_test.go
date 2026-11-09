package hysteria2

// One production Hysteria2 outbound across a network change, end to end.
//
// The other tests in this package measure the QUIC ceiling and the datagram sizing of the pinned
// sing-quic client against a socket they control; none of them runs a session. This is the carrier
// row the window's matrix asks for: the real outbound, the pinned sing-quic client on a real
// session with the same service the inbound wraps, and the answer to "what happens to an active
// flow when the network changes": the old session is retired and the next dial carries new
// business bytes.
//
// The server side is the pinned sing-quic service (the object protocol/hysteria2's inbound wraps)
// with the TLS config the inbound builds; only the stream's routing is replaced by a loopback echo.

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	boxTLS "github.com/sagernet/sing-box/common/tls"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-quic/hysteria2"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

const (
	hysteriaCarrierPassword = "carrier-password"
	hysteriaCarrierSettle   = 15 * time.Second
)

// hysteriaCarrierHandler answers every stream the Hysteria2 service accepts with a loopback echo.
type hysteriaCarrierHandler struct {
	target     string
	streams    atomic.Int32
	streamEnds atomic.Int32
}

func (h *hysteriaCarrierHandler) NewConnectionEx(ctx context.Context, conn net.Conn, source, destination M.Socksaddr, onClose N.CloseHandlerFunc) {
	h.streams.Add(1)
	go func() {
		defer h.streamEnds.Add(1)
		defer conn.Close()
		if onClose != nil {
			defer onClose(nil)
		}
		peer, err := net.Dial("tcp", h.target)
		if err != nil {
			return
		}
		defer peer.Close()
		go func() {
			_, _ = io.Copy(peer, conn)
			_ = peer.(*net.TCPConn).CloseWrite()
		}()
		_, _ = io.Copy(conn, peer)
	}()
}

func (h *hysteriaCarrierHandler) NewPacketConnectionEx(ctx context.Context, conn N.PacketConn, source, destination M.Socksaddr, onClose N.CloseHandlerFunc) {
	conn.Close()
}

// http404 is the masquerade handler a service without a site uses.
func http404() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNotFound)
	})
}

func startHysteriaCarrierEcho(t *testing.T) string {
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

// startHysteriaCarrierServer runs the pinned sing-quic Hysteria2 service on loopback.
func startHysteriaCarrierServer(t *testing.T, handler hysteria2.ServerHandler) uint16 {
	t.Helper()
	directory := t.TempDir()
	keyPEM, certPEM, err := boxTLS.GenerateCertificate(nil, nil, time.Now, "localhost", time.Now().Add(24*time.Hour))
	require.NoError(t, err)
	certificatePath := filepath.Join(directory, "cert.pem")
	keyPath := filepath.Join(directory, "key.pem")
	require.NoError(t, os.WriteFile(certificatePath, certPEM, 0o600))
	require.NoError(t, os.WriteFile(keyPath, keyPEM, 0o600))

	logger := log.NewNOPFactory().NewLogger("carrier-hysteria2")
	serverTLS, err := boxTLS.NewServer(context.Background(), logger, option.InboundTLSOptions{
		Enabled:         true,
		ServerName:      "localhost",
		CertificatePath: certificatePath,
		KeyPath:         keyPath,
	})
	require.NoError(t, err)
	require.NoError(t, serverTLS.Start())
	t.Cleanup(func() { _ = serverTLS.Close() })

	service, err := hysteria2.NewService[int](hysteria2.ServiceOptions{
		Context:               context.Background(),
		Logger:                logger,
		TLSConfig:             serverTLS,
		Handler:               handler,
		MasqueradeHandler:     http404(),
		IgnoreClientBandwidth: true,
	})
	require.NoError(t, err)
	service.UpdateUsers([]int{0}, []string{hysteriaCarrierPassword})
	packetConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	require.NoError(t, service.Start(packetConn))
	t.Cleanup(func() { _ = service.Close() })
	return uint16(packetConn.LocalAddr().(*net.UDPAddr).Port)
}

func newHysteriaCarrierOutbound(t *testing.T, port uint16) *Outbound {
	t.Helper()
	created, err := NewOutbound(context.Background(), nil, log.NewNOPFactory().NewLogger("carrier-out"), "hysteria2-carrier",
		option.Hysteria2OutboundOptions{
			ServerOptions: option.ServerOptions{Server: "127.0.0.1", ServerPort: port},
			Password:      hysteriaCarrierPassword,
			OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{
				TLS: &option.OutboundTLSOptions{
					Enabled:    true,
					ServerName: "localhost",
					Insecure:   true,
				},
			},
		})
	require.NoError(t, err)
	outbound, isOutbound := created.(*Outbound)
	require.True(t, isOutbound)
	t.Cleanup(func() { outbound.client.CloseWithError(net.ErrClosed) })
	return outbound
}

func hysteriaRoundTrip(t *testing.T, outbound *Outbound, payload string) net.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), hysteriaCarrierSettle)
	defer cancel()
	conn, err := outbound.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr("echo.test:443"))
	require.NoError(t, err)
	require.NotNil(t, conn)
	_ = conn.SetDeadline(time.Now().Add(hysteriaCarrierSettle))
	_, err = conn.Write([]byte(payload))
	require.NoError(t, err)
	echo := make([]byte, len(payload))
	_, err = io.ReadFull(conn, echo)
	require.NoError(t, err)
	require.Equal(t, payload, string(echo))
	return conn
}

func TestARealHysteria2FlowSurvivesANetworkChangeByReconnecting(t *testing.T) {
	handler := &hysteriaCarrierHandler{target: startHysteriaCarrierEcho(t)}
	port := startHysteriaCarrierServer(t, handler)
	outbound := newHysteriaCarrierOutbound(t, port)

	before := hysteriaRoundTrip(t, outbound, "before-the-change")
	require.EqualValues(t, 1, handler.streams.Load(), "the first flow reached the server")

	// The production entry: what NetworkManager calls on every physical network change.
	outbound.InterfaceUpdated(context.Background())

	_ = before.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := before.Write([]byte("stale")); err == nil {
		t.Fatal("the pre-change Hysteria2 flow still accepted a write: the old session was not retired")
	}

	// The retirement is also visible where it happens: the server-side stream that carried the old
	// path finishes, within a bound. That is the release observation and the time bound this matrix
	// row was missing.
	retiredAt := time.Now()
	require.Eventually(t, func() bool { return handler.streamEnds.Load() >= 1 },
		5*time.Second, 10*time.Millisecond,
		"the server never saw the retired Hysteria2 stream end: the old session's release has no bound")
	t.Logf("MEASURED old Hysteria2 stream released %s after the network change", time.Since(retiredAt).Round(time.Millisecond))

	after := hysteriaRoundTrip(t, outbound, "after-the-change")
	_ = after.Close()
	require.EqualValues(t, 2, handler.streams.Load(), "the flow after the change reached the server")
}
