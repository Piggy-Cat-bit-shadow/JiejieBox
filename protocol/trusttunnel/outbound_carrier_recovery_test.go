package trusttunnel

// One production TrustTunnel outbound across a network change, end to end.
//
// The tests in this package validate options and the wire order; none of them runs a session, so
// the carrier row the window's matrix asks for was open: the real outbound against the real
// service the inbound wraps, and what a network change does to a live flow.
//
// The server half is transport/trusttunnel's Service serving CONNECT over HTTP/2 with the same TLS
// config the inbound builds; only the stream's routing is replaced by a loopback echo, which is
// what makes the payload assertion possible.
//
// Pinned here:
//   - a flow carries a payload and gets it echoed back over the tunnel;
//   - InterfaceUpdated - what NetworkManager calls on every physical change - retires the session:
//     a write on the pre-change stream fails;
//   - the next dial establishes a new session that carries payload again and the server sees it.
//
// The QUIC (HTTP/3) half of the same service is left to the window that owns the protocol internals:
// this is the TCP carrier row.

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	boxTLS "github.com/sagernet/sing-box/common/tls"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/transport/trusttunnel"
	"github.com/sagernet/sing/common/auth"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

const (
	trustTunnelCarrierUser     = "carrier-user"
	trustTunnelCarrierPassword = "carrier-password"
	trustTunnelSettle          = 15 * time.Second
)

// trustTunnelCarrierHandler answers every CONNECT with a loopback echo.
type trustTunnelCarrierHandler struct {
	target  string
	streams atomic.Int32
}

func (h *trustTunnelCarrierHandler) NewConnection(ctx context.Context, conn *trusttunnel.ServerConn, metadata trusttunnel.Metadata) {
	h.streams.Add(1)
	go func() {
		defer conn.Close()
		peer, err := net.Dial("tcp", h.target)
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

func (h *trustTunnelCarrierHandler) NewPacketConnection(ctx context.Context, conn N.PacketConn, metadata trusttunnel.Metadata) {
	conn.Close()
}

// startTrustTunnelCarrierEcho is the loopback peer the tunnelled stream is piped to.
func startTrustTunnelCarrierEcho(t *testing.T) string {
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

// startTrustTunnelCarrierServer serves CONNECT over HTTP/2 on loopback.
func startTrustTunnelCarrierServer(t *testing.T, handler trusttunnel.Handler) uint16 {
	t.Helper()
	directory := t.TempDir()
	keyPEM, certPEM, err := boxTLS.GenerateCertificate(nil, nil, time.Now, "localhost", time.Now().Add(24*time.Hour))
	require.NoError(t, err)
	certificatePath := filepath.Join(directory, "cert.pem")
	keyPath := filepath.Join(directory, "key.pem")
	require.NoError(t, os.WriteFile(certificatePath, certPEM, 0o600))
	require.NoError(t, os.WriteFile(keyPath, keyPEM, 0o600))

	logger := log.NewNOPFactory().NewLogger("carrier-trusttunnel")
	serverTLS, err := boxTLS.NewServer(context.Background(), logger, option.InboundTLSOptions{
		Enabled:         true,
		ServerName:      "localhost",
		CertificatePath: certificatePath,
		KeyPath:         keyPath,
	})
	require.NoError(t, err)
	require.NoError(t, serverTLS.Start())
	t.Cleanup(func() { _ = serverTLS.Close() })

	service, err := trusttunnel.NewService(trusttunnel.ServiceOptions{
		Context: context.Background(),
		Logger:  logger,
		Handler: handler,
		Users: []auth.User{{
			Username: trustTunnelCarrierUser,
			Password: trustTunnelCarrierPassword,
		}},
	})
	require.NoError(t, err)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = service.ServeTCP(listener, serverTLS) }()
	t.Cleanup(func() { _ = service.Close() })
	return uint16(listener.Addr().(*net.TCPAddr).Port)
}

func newTrustTunnelCarrierOutbound(t *testing.T, port uint16) *Outbound {
	t.Helper()
	created, err := NewOutbound(context.Background(), nil, log.NewNOPFactory().NewLogger("carrier-tt"), "trusttunnel-carrier",
		option.TrustTunnelOutboundOptions{
			ServerOptions: option.ServerOptions{Server: "127.0.0.1", ServerPort: port},
			Username:      trustTunnelCarrierUser,
			Password:      trustTunnelCarrierPassword,
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
	t.Cleanup(func() { _ = outbound.Close() })
	return outbound
}

func trustTunnelRoundTrip(t *testing.T, outbound *Outbound, payload string) net.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), trustTunnelSettle)
	defer cancel()
	conn, err := outbound.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr("echo.test:443"))
	require.NoError(t, err)
	require.NotNil(t, conn)
	_ = conn.SetDeadline(time.Now().Add(trustTunnelSettle))
	_, err = conn.Write([]byte(payload))
	require.NoError(t, err)
	echo := make([]byte, len(payload))
	_, err = io.ReadFull(conn, echo)
	require.NoError(t, err)
	require.Equal(t, payload, string(echo))
	return conn
}

func TestARealTrustTunnelFlowSurvivesANetworkChangeByReconnecting(t *testing.T) {
	handler := &trustTunnelCarrierHandler{target: startTrustTunnelCarrierEcho(t)}
	port := startTrustTunnelCarrierServer(t, handler)
	outbound := newTrustTunnelCarrierOutbound(t, port)

	before := trustTunnelRoundTrip(t, outbound, "before-the-change")
	require.EqualValues(t, 1, handler.streams.Load(), "the first flow reached the server")

	// The production entry: what NetworkManager calls on every physical network change.
	outbound.InterfaceUpdated(context.Background())

	_ = before.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := before.Write([]byte("stale")); err == nil {
		t.Fatal("the pre-change TrustTunnel flow still accepted a write: the old session was not retired")
	}

	after := trustTunnelRoundTrip(t, outbound, "after-the-change")
	_ = after.Close()
	require.EqualValues(t, 2, handler.streams.Load(), "the flow after the change reached the server")
}
