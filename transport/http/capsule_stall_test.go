//go:build with_quic

package http

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/sagernet/quic-go"
	"github.com/sagernet/quic-go/http3"
	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// The Capsule branch, measured separately from the DATAGRAM branch.
//
// # Why this cannot be inferred from the DATAGRAM result
//
// A DATAGRAM write is bounded because a peer that gets no acknowledgements cannot keep sending, so
// one side's idle timeout eventually fires. A capsule write blocks on a different thing: the request
// stream's flow-control window. The connection stays healthy there -- both sides keep acknowledging
// -- so OUR idle timer is refreshed and never rescues the writer. What ends the block is the PEER's
// timer: while the stream write is parked we send no packets at all, so the peer stops hearing from
// us and closes the connection when its own idle timeout expires.
//
// The consequence is worth stating plainly: the bound exists, but its length is chosen by the peer.
// With a peer configured for a long idle timeout, a shutdown that lands on a blocked capsule write
// waits that long.

// startStalledCapsuleServer accepts a CONNECT-UDP request and then never reads the request body.
func startStalledCapsuleServer(t *testing.T, idleTimeout time.Duration) string {
	t.Helper()
	tlsConfig := testServerTLSConfig(t)
	tlsConfig.NextProtos = []string{http3.NextProtoH3}
	packetConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	server := &http3.Server{
		Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Capsule-Protocol", "?1")
			writer.WriteHeader(http.StatusOK)
			if flusher, ok := writer.(http.Flusher); ok {
				flusher.Flush()
			}
			// Deliberately never reads the request body.
			<-request.Context().Done()
		}),
		TLSConfig: tlsConfig,
		QUICConfig: &quic.Config{
			HandshakeIdleTimeout: 5 * time.Second,
			MaxIdleTimeout:       idleTimeout,
			EnableDatagrams:      false,
		},
		EnableDatagrams: false,
	}
	go func() { _ = server.Serve(packetConn) }()
	t.Cleanup(func() { _ = server.Close() })
	return packetConn.LocalAddr().String()
}

// TestCapsuleWriteAgainstAStalledPeer fills the stream window against a peer that never reads, and
// measures what ends the block.
func TestCapsuleWriteAgainstAStalledPeer(t *testing.T) {
	const peerIdleTimeout = 10 * time.Second
	address := startStalledCapsuleServer(t, peerIdleTimeout)
	client := connectUDPTestClient(t, address)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	packetConn, err := client.listenPacket(ctx, M.ParseSocksaddr("echo.test:443"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = packetConn.Close() })

	payload := make([]byte, 1200)
	written := 0
	var blockDuration time.Duration
	var blockErr error
	for written < 200000 {
		pkt := buf.NewSize(len(payload))
		pkt.Write(payload)
		writeStart := time.Now()
		writeErr := packetConn.WritePacket(pkt, M.ParseSocksaddr("echo.test:443"))
		elapsed := time.Since(writeStart)
		if elapsed > time.Second {
			blockDuration = elapsed
			blockErr = writeErr
			break
		}
		if writeErr != nil {
			t.Fatalf("write %d failed early: %v", written, writeErr)
		}
		written++
	}

	require.NotZero(t, blockDuration, "the stream window was never filled within the test")
	t.Logf("MEASURED: write %d blocked for %v, then returned with %v", written,
		blockDuration.Round(time.Millisecond), blockErr)

	// The block is bounded by the PEER's idle timeout: while parked we send nothing, so it stops
	// hearing from us. Allow generous jitter around the configured value.
	require.Greater(t, blockDuration, peerIdleTimeout/2,
		"the write returned too early to be the peer's idle timeout")
	require.Less(t, blockDuration, peerIdleTimeout*3,
		"the write took much longer than the peer's idle timeout")

	// And the connection really is gone afterwards: the next write must fail immediately.
	pkt := buf.NewSize(len(payload))
	pkt.Write(payload)
	nextStart := time.Now()
	nextErr := packetConn.WritePacket(pkt, M.ParseSocksaddr("echo.test:443"))
	require.Error(t, nextErr, "the connection must be closed after the peer's idle timeout")
	require.Less(t, time.Since(nextStart), 2*time.Second,
		"a write on the closed connection must fail fast, not block again")
	t.Logf("MEASURED: the next write failed in %v with %v", time.Since(nextStart).Round(time.Millisecond), nextErr)
}
