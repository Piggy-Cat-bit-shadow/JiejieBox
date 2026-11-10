package httpclient

import (
	"context"
	stdTLS "crypto/tls"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/quic-go"
	"github.com/sagernet/quic-go/http3"

	"github.com/stretchr/testify/require"
)

// The ordinary HTTP path, not CONNECT-IP: a peer that explicitly rejects a request with
// H3_REQUEST_REJECTED has NOT shown that HTTP/3 is unavailable, and the request may be retried.

func startRequestRejectingHTTP3Server(t *testing.T) (address string, streams *atomic.Int32) {
	t.Helper()
	tlsConfig := &stdTLS.Config{
		Certificates: []stdTLS.Certificate{retrySafetyCertificate(t)},
		NextProtos:   []string{http3.NextProtoH3},
	}
	packetConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	listener, err := quic.ListenEarly(packetConn, tlsConfig, &quic.Config{HandshakeIdleTimeout: 5 * time.Second})
	require.NoError(t, err)
	streams = &atomic.Int32{}
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
				for {
					stream, acceptErr := conn.AcceptStream(ctx)
					if acceptErr != nil {
						return
					}
					streams.Add(1)
					stream.CancelWrite(quic.StreamErrorCode(0x10b))
				}
			}()
		}
	}()
	return packetConn.LocalAddr().String(), streams
}

// TestRejectedReplayablePostFallsBackWithoutAVerdict: the POST's body can be rebuilt, and the peer
// said it did not process the request, so the fallback may run; nothing may be charged to the H3
// verdict.
func TestRejectedReplayablePostFallsBackWithoutAVerdict(t *testing.T) {
	address, streams := startRequestRejectingHTTP3Server(t)
	h2Server, h2Transport := startRetrySafetyH2Server(t, okHandler)
	transport := newRetrySafetyTransport(h2Transport, dialRetrySafetyH3(address), time.Hour)
	t.Cleanup(func() { _ = transport.h3Transport.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+address, io.NopCloser(strings.NewReader("payload")))
	require.NoError(t, err)
	request.Header.Set("Idempotency-Key", "rejected-replayable")
	request.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader("payload")), nil
	}
	response, err := transport.RoundTrip(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, "HTTP/2.0", response.Proto,
		"an explicitly unprocessed request may be retried on another version")
	require.Equal(t, 1, h2Server.executed())
	// quic-go retries a rejected request internally when GetBody allows it (one extra stream),
	// and this layer then dispatches the same request cross-version (the H2 call above).
	require.EqualValues(t, 2, streams.Load(), "one internal attempt + one internal retry")

	transport.verdictAccess.Lock()
	broken := len(transport.broken)
	transport.verdictAccess.Unlock()
	require.Zero(t, broken, "an explicit rejection is not a transport verdict")
	require.False(t, transport.h3Broken(requestAuthority(request)),
		"later requests must still be allowed to try HTTP/3")
}

// TestRejectedOnceOnlyPostFailsWithoutAResend: the request cannot be rebuilt, so the rejection is
// reported instead of guessed at, and still without charging the verdict.
func TestRejectedOnceOnlyPostFailsWithoutAResend(t *testing.T) {
	address, streams := startRequestRejectingHTTP3Server(t)
	h2Server, h2Transport := startRetrySafetyH2Server(t, okHandler)
	transport := newRetrySafetyTransport(h2Transport, dialRetrySafetyH3(address), time.Hour)
	t.Cleanup(func() { _ = transport.h3Transport.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+address, io.NopCloser(strings.NewReader("payload")))
	require.NoError(t, err)
	response, err := transport.RoundTrip(request)
	if response != nil {
		_ = response.Body.Close()
	}
	require.Error(t, err)
	require.ErrorIs(t, err, errH3RequestRejected)
	require.Zero(t, h2Server.executed(), "a body that cannot be rebuilt must not be re-sent")
	require.EqualValues(t, 1, streams.Load())

	transport.verdictAccess.Lock()
	broken := len(transport.broken)
	transport.verdictAccess.Unlock()
	require.Zero(t, broken)
	require.False(t, transport.h3Broken(requestAuthority(request)))
}
