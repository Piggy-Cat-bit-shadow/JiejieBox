package anytls

// One production AnyTLS outbound across a network change, end to end.
//
// The tests in this package cover the admission budget and the fallback handshake; none of them runs
// a session through a real server, so the carrier behaviour was open: what a network change does to
// an ACTIVE AnyTLS flow. This is that row - the real outbound, the pinned sing-anytls service on a
// real TLS listener, and the answer: the old session is retired and the next dial carries new bytes.
//
// The server side is the same sing-anytls service protocol/anytls's inbound wraps; only the routing
// of the stream to a target is replaced by a loopback echo.

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	singanytls "github.com/sagernet/sing-anytls"
	"github.com/sagernet/sing-box/adapter"
	boxTLS "github.com/sagernet/sing-box/common/tls"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

const (
	carrierPassword = "anytls-carrier-password"
	carrierSettle   = 15 * time.Second
)

// carrierHandler answers every stream the AnyTLS service accepts with a loopback echo.
type carrierHandler struct {
	target     string
	streams    atomic.Int32
	streamEnds atomic.Int32
}

func (h *carrierHandler) NewConnectionEx(ctx context.Context, conn net.Conn, source M.Socksaddr, destination M.Socksaddr, onClose N.CloseHandlerFunc) {
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

func startCarrierEcho(t *testing.T) string {
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

// startCarrierServer serves the pinned sing-anytls service over TLS on loopback.
func startCarrierServer(t *testing.T, handler *carrierHandler) uint16 {
	t.Helper()
	directory := t.TempDir()
	keyPEM, certPEM, err := boxTLS.GenerateCertificate(nil, nil, time.Now, "localhost", time.Now().Add(24*time.Hour))
	require.NoError(t, err)
	certificatePath := filepath.Join(directory, "cert.pem")
	keyPath := filepath.Join(directory, "key.pem")
	require.NoError(t, os.WriteFile(certificatePath, certPEM, 0o600))
	require.NoError(t, os.WriteFile(keyPath, keyPEM, 0o600))

	boxTLSConfig, err := boxTLS.NewServer(context.Background(), log.NewNOPFactory().NewLogger("carrier-anytls"), option.InboundTLSOptions{
		Enabled:         true,
		ServerName:      "localhost",
		CertificatePath: certificatePath,
		KeyPath:         keyPath,
	})
	require.NoError(t, err)
	require.NoError(t, boxTLSConfig.Start())
	t.Cleanup(func() { _ = boxTLSConfig.Close() })

	stdTLSConfig, err := boxTLSConfig.STDConfig()
	require.NoError(t, err)
	listener, err := tls.Listen("tcp", "127.0.0.1:0", stdTLSConfig)
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	service, err := singanytls.NewService(carrierPassword, singanytls.ServiceOptions{
		Handler:         handler,
		FallbackHandler: handler,
		Logger:          log.NewNOPFactory().NewLogger("carrier-anytls-service"),
	})
	require.NoError(t, err)

	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				_ = service.NewConnection(context.Background(), conn, M.SocksaddrFromNet(conn.RemoteAddr()), nil)
			}()
		}
	}()
	return uint16(listener.Addr().(*net.TCPAddr).Port)
}

func newCarrierOutbound(t *testing.T, port uint16) *Outbound {
	t.Helper()
	created, err := NewOutbound(context.Background(), nil, log.NewNOPFactory().NewLogger("carrier-anytls-out"), "anytls-carrier",
		option.AnyTLSOutboundOptions{
			ServerOptions: option.ServerOptions{Server: "127.0.0.1", ServerPort: port},
			Password:      carrierPassword,
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
	// The admission gate is wired by Start, so a dial before it panics on a nil function.
	scope := adapter.NewScope(context.Background(), log.NewNOPFactory().NewLogger("carrier-anytls-scope"))
	require.NoError(t, outbound.Start(adapter.StartStateInitialize, scope))
	t.Cleanup(func() { _ = scope.Close() })
	t.Cleanup(func() { _ = outbound.client.Close() })
	return outbound
}

func roundTrip(t *testing.T, outbound *Outbound, payload string) net.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), carrierSettle)
	defer cancel()
	conn, err := outbound.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr("echo.test:443"))
	require.NoError(t, err)
	require.NotNil(t, conn)
	_ = conn.SetDeadline(time.Now().Add(carrierSettle))
	_, err = conn.Write([]byte(payload))
	require.NoError(t, err)
	echo := make([]byte, len(payload))
	_, err = io.ReadFull(conn, echo)
	require.NoError(t, err)
	require.Equal(t, payload, string(echo))
	return conn
}

func TestARealAnyTLSFlowSurvivesANetworkChangeByReconnecting(t *testing.T) {
	handler := &carrierHandler{target: startCarrierEcho(t)}
	port := startCarrierServer(t, handler)
	outbound := newCarrierOutbound(t, port)

	before := roundTrip(t, outbound, "before-the-change")
	require.EqualValues(t, 1, handler.streams.Load(), "the first flow reached the server")

	// The production entry: what NetworkManager calls on every physical network change.
	outbound.InterfaceUpdated(context.Background())

	_ = before.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := before.Write([]byte("stale")); err == nil {
		t.Fatal("the pre-change AnyTLS flow still accepted a write: the old session was not retired")
	}

	// The retirement is also visible where it happens: the server-side stream that carried the old
	// path finishes, within a bound. That is the release observation and the time bound this matrix
	// row was missing.
	retiredAt := time.Now()
	require.Eventually(t, func() bool { return handler.streamEnds.Load() >= 1 },
		5*time.Second, 10*time.Millisecond,
		"the server never saw the retired AnyTLS stream end: the old session's release has no bound")
	t.Logf("MEASURED old AnyTLS stream released %s after the network change", time.Since(retiredAt).Round(time.Millisecond))

	after := roundTrip(t, outbound, "after-the-change")
	_ = after.Close()
	require.EqualValues(t, 2, handler.streams.Load(), "the flow after the change reached the server")
}
