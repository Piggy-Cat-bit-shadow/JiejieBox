//go:build with_quic

package http

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/quic-go"
	"github.com/sagernet/quic-go/http3"
	E "github.com/sagernet/sing/common/exceptions"

	"github.com/stretchr/testify/require"
)

// B3 line two at the tunnel boundary: the peer PUBLISHES its tunnel session (it accepts the
// CONNECT-IP request stream) and then withholds the 200. The client must refuse to open a second
// tunnel in the same call, send the caller's retry to the HTTP/2 path, and never overlap two H3
// sessions; the published session is released when the client goes away.

type withholdingTunnelServer struct {
	address   string
	published atomic.Int32
	streams   atomic.Int32
	released  atomic.Int32
	active    atomic.Int32
	peak      atomic.Int32
}

func startWithholdingTunnelServer(t *testing.T) *withholdingTunnelServer {
	t.Helper()
	tlsConfig := testServerTLSConfig(t)
	tlsConfig.NextProtos = []string{http3.NextProtoH3}
	packetConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	listener, err := quic.ListenEarly(packetConn, tlsConfig, &quic.Config{
		HandshakeIdleTimeout: 5 * time.Second,
	})
	require.NoError(t, err)
	server := &withholdingTunnelServer{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = listener.Close()
		_ = packetConn.Close()
	})
	go func() {
		for {
			conn, acceptErr := listener.Accept(ctx)
			if acceptErr != nil {
				return
			}
			go func() {
				defer conn.CloseWithError(0, "")
				control, streamErr := conn.OpenUniStream()
				if streamErr != nil {
					return
				}
				_, _ = control.Write([]byte{0x00, 0x04, 0x02, 0x08, 0x01})
				var held int32
				for {
					stream, acceptStreamErr := conn.AcceptStream(ctx)
					if acceptStreamErr != nil {
						break
					}
					// The tunnel is "published": the stream is accepted and held, the 200 is
					// never written.
					_ = stream
					held++
					server.streams.Add(1)
					server.published.Add(1)
					active := server.active.Add(1)
					for {
						peak := server.peak.Load()
						if active <= peak || server.peak.CompareAndSwap(peak, active) {
							break
						}
					}
				}
				server.released.Add(held)
				server.active.Add(-held)
			}()
		}
	}()
	server.address = packetConn.LocalAddr().String()
	return server
}

func TestPublishedSessionWithHeld200IsNotDuplicatedOnRetry(t *testing.T) {
	server := startWithholdingTunnelServer(t)
	freezeTunnelWindow(t, 200*time.Millisecond)
	tcp := &recordingDialer{err: E.New("tcp fallback reached")}
	client := newNoExtendedConnectClient(t, server.address, tcp)

	start := time.Now()
	_, _, _, err := client.openTunnel(context.Background(), tunnelRequestForTest())
	elapsed := time.Since(start)
	require.Error(t, err)
	require.ErrorIs(t, err, errHTTP3ResponsePending,
		"the peer accepted the CONNECT and withheld the 200")
	require.EqualValues(t, 1, server.published.Load(), "the peer published one tunnel session")
	require.EqualValues(t, 1, server.streams.Load())
	require.EqualValues(t, 1, server.active.Load())
	require.Less(t, elapsed, 2*time.Second)
	require.Zero(t, tcp.calls, "the same call must not open a second tunnel")
	require.NotZero(t, client.http3TunnelAmbiguousUntil.Load(),
		"the ambiguity window must route the retry to the HTTP/2 path")

	// The caller's retry: the HTTP/2 path, no second H3 CONNECT, no overlapping session.
	_, _, _, retryErr := client.openTunnel(context.Background(), tunnelRequestForTest())
	require.ErrorContains(t, retryErr, "tcp fallback reached")
	require.EqualValues(t, 1, server.published.Load(), "the retry must not open a second H3 CONNECT")
	require.EqualValues(t, 1, server.peak.Load(), "no overlapping H3 session")

	// The published session is released when the client goes away.
	require.NoError(t, client.Close())
	require.Eventually(t, func() bool { return server.released.Load() == 1 }, 2*time.Second, time.Millisecond)
	require.Zero(t, server.active.Load())
}
