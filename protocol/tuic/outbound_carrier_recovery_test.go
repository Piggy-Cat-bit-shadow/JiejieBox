package tuic

// One production TUIC outbound across a network change, end to end.
//
// The other tests in this package measure the QUIC ceiling and the datagram sizing of the pinned
// sing-quic client. None of them runs the outbound against a real server, so none can say what a
// network change does to an ACTIVE TUIC flow - which is the carrier row the window's matrix asks
// for: "run the real outbound, retire the old session, and get new business bytes".
//
// The server side is the same sing-quic service the inbound wraps (with the TLS config the inbound
// builds), so this is the production protocol on both ends; only the routing of the stream to a
// target is replaced by a loopback echo, which is what makes the payload assertion possible.
//
// What is pinned:
//   - a TUIC flow carries a payload through the outbound and gets it echoed back;
//   - InterfaceUpdated (what NetworkManager calls on every physical change) retires that session:
//     a write on the pre-change stream fails;
//   - the next dial establishes a new session that carries payload again, and the server sees the
//     new stream, so the carrier recovers rather than merely recording the change.

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
	"github.com/sagernet/sing-quic/tuic"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/gofrs/uuid/v5"
	"github.com/stretchr/testify/require"
)

const (
	carrierTestUUID     = "b831381d-6324-4d53-ad4f-8cda48b30811"
	carrierTestPassword = "carrier-password"
	carrierSettle       = 15 * time.Second
)

// carrierHandler answers every stream the TUIC service accepts with a loopback echo, which stands
// in for the routing the inbound would do. streamEnds is the release observation: it moves when the
// server-side stream has actually finished, which is what "the old session was retired" means from
// the peer's side.
type carrierHandler struct {
	target     string
	streams    atomic.Int32
	streamEnds atomic.Int32
}

func (h *carrierHandler) NewConnectionEx(ctx context.Context, conn net.Conn, source, destination M.Socksaddr, onClose N.CloseHandlerFunc) {
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

func (h *carrierHandler) NewPacketConnectionEx(ctx context.Context, conn N.PacketConn, source, destination M.Socksaddr, onClose N.CloseHandlerFunc) {
	conn.Close()
}

// startCarrierEcho is the loopback peer the server streams against.
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

// startCarrierServer runs the pinned sing-quic TUIC service on loopback and returns its port.
func startCarrierServer(t *testing.T, handler tuic.ServiceHandler) uint16 {
	t.Helper()
	directory := t.TempDir()
	keyPEM, certPEM, err := boxTLS.GenerateCertificate(nil, nil, time.Now, "localhost", time.Now().Add(24*time.Hour))
	require.NoError(t, err)
	certificatePath := filepath.Join(directory, "cert.pem")
	keyPath := filepath.Join(directory, "key.pem")
	require.NoError(t, os.WriteFile(certificatePath, certPEM, 0o600))
	require.NoError(t, os.WriteFile(keyPath, keyPEM, 0o600))

	logger := log.NewNOPFactory().NewLogger("carrier-tuic")
	serverTLS, err := boxTLS.NewServer(context.Background(), logger, option.InboundTLSOptions{
		Enabled:         true,
		ServerName:      "localhost",
		CertificatePath: certificatePath,
		KeyPath:         keyPath,
	})
	require.NoError(t, err)
	require.NoError(t, serverTLS.Start())
	t.Cleanup(func() { _ = serverTLS.Close() })

	service, err := tuic.NewService[int](tuic.ServiceOptions{
		Context:   context.Background(),
		Logger:    logger,
		TLSConfig: serverTLS,
		Handler:   handler,
	})
	require.NoError(t, err)
	packetConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	require.NoError(t, service.Start(packetConn))
	t.Cleanup(func() { _ = service.Close() })

	userUUID, err := uuid.FromString(carrierTestUUID)
	require.NoError(t, err)
	service.UpdateUsers([]int{0}, [][16]byte{userUUID}, []string{carrierTestPassword})
	return uint16(packetConn.LocalAddr().(*net.UDPAddr).Port)
}

// newCarrierOutbound builds the production TUIC outbound against the loopback server.
func newCarrierOutbound(t *testing.T, port uint16) *Outbound {
	t.Helper()
	created, err := NewOutbound(context.Background(), nil, log.NewNOPFactory().NewLogger("carrier-out"), "tuic-carrier",
		option.TUICOutboundOptions{
			ServerOptions: option.ServerOptions{Server: "127.0.0.1", ServerPort: port},
			UUID:          carrierTestUUID,
			Password:      carrierTestPassword,
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
	t.Cleanup(func() { _ = outbound.client.CloseWithError(net.ErrClosed) })
	return outbound
}

// roundTrip opens one TUIC flow and requires the echo.
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

func TestARealTUICFlowSurvivesANetworkChangeByReconnecting(t *testing.T) {
	handler := &carrierHandler{target: startCarrierEcho(t)}
	port := startCarrierServer(t, handler)
	outbound := newCarrierOutbound(t, port)

	before := roundTrip(t, outbound, "before-the-change")
	require.EqualValues(t, 1, handler.streams.Load(), "the first flow reached the server")

	// The production entry: what NetworkManager calls on every physical network change.
	outbound.InterfaceUpdated(context.Background())

	// The session that carried the old network is really gone: a write on the old flow fails
	// instead of silently succeeding on a connection pinned to the previous path.
	_ = before.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := before.Write([]byte("stale")); err == nil {
		t.Fatal("the pre-change TUIC flow still accepted a write: the old session was not retired")
	}

	// The retirement is also visible where it happens: the server-side stream that carried the old
	// path finishes, within a bound. That is the release observation and the time bound this matrix
	// row was missing.
	retiredAt := time.Now()
	require.Eventually(t, func() bool { return handler.streamEnds.Load() >= 1 },
		5*time.Second, 10*time.Millisecond,
		"the server never saw the retired TUIC stream end: the old session's release has no bound")
	t.Logf("MEASURED old TUIC stream released %s after the network change", time.Since(retiredAt).Round(time.Millisecond))

	// New business bytes: the next dial establishes a new session and the server sees the flow.
	after := roundTrip(t, outbound, "after-the-change")
	_ = after.Close()
	require.EqualValues(t, 2, handler.streams.Load(), "the flow after the change reached the server")
}
