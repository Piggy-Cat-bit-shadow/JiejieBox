//go:build with_quic

package httpclient

import (
	"context"
	stdTLS "crypto/tls"
	"fmt"
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

// A warm peer that serves the first request, consumes the second request's body and then rejects
// it with H3_REQUEST_REJECTED, and serves anything after that. This is the sequence the review
// reproduced: the rejection arrives AFTER the request was dispatched on a cached connection.
func startWarmRejectingH3Peer(t *testing.T) (address string, streams *atomic.Int32) {
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
					index := streams.Add(1)
					_, readErr := io.ReadAll(stream)
					if readErr != nil {
						stream.CancelWrite(quic.StreamErrorCode(0x10c))
						continue
					}
					if index == 2 {
						// The request was read, then explicitly not processed.
						stream.CancelWrite(quic.StreamErrorCode(0x10b))
						continue
					}
					// HEADERS frame carrying QPACK static-table :status = 200, then FIN.
					_, _ = stream.Write([]byte{0x01, 0x03, 0x00, 0x00, 0xd9})
					_ = stream.Close()
				}
			}()
		}
	}()
	return packetConn.LocalAddr().String(), streams
}

type warmTrackedBody struct {
	reader          io.Reader
	closes          atomic.Int32
	readsAfterClose atomic.Int32
}

func (b *warmTrackedBody) Read(p []byte) (int, error) {
	if b.closes.Load() > 0 {
		b.readsAfterClose.Add(1)
		return 0, io.ErrClosedPipe
	}
	return b.reader.Read(p)
}

func (b *warmTrackedBody) Close() error { b.closes.Add(1); return nil }

// waitForBodyClose waits for the close the caller owns to arrive. The rebuild
// path hands bodies to both the HTTP/3 attempt and the cross-version fallback,
// and the attempt that loses the race is closed by its own goroutine, so the
// close can land just after RoundTrip returns. Waiting keeps "closed exactly
// once" as the assertion instead of making it a property of the scheduler.
func waitForBodyClose(t *testing.T, body *warmTrackedBody, label string) int32 {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if closes := body.closes.Load(); closes > 0 {
			return closes
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s was never closed", label)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestWarmRejectedReplayablePostFallsBackWithAFreshBody: the warm H3 stream consumed and closed the
// original body; the rejection must reach this layer (so the fallback is allowed) and the fallback
// must use a rebuilt body, never the closed original.
func TestWarmRejectedReplayablePostFallsBackWithAFreshBody(t *testing.T) {
	address, streams := startWarmRejectingH3Peer(t)
	h2Server, h2Transport := startRetrySafetyH2Server(t, okHandler)
	transport := newRetrySafetyTransport(h2Transport, dialRetrySafetyH3(address), time.Hour)
	t.Cleanup(func() { _ = transport.h3Transport.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	warm, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+address+"/warm", nil)
	require.NoError(t, err)
	warmResponse, err := transport.RoundTrip(warm)
	require.NoError(t, err)
	require.Equal(t, "HTTP/3.0", warmResponse.Proto)
	require.NoError(t, warmResponse.Body.Close())
	require.EqualValues(t, 1, streams.Load())

	original := &warmTrackedBody{reader: strings.NewReader("payload")}
	var copies []*warmTrackedBody
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+address+"/reject", original)
	require.NoError(t, err)
	request.GetBody = func() (io.ReadCloser, error) {
		copy := &warmTrackedBody{reader: strings.NewReader("payload")}
		copies = append(copies, copy)
		return copy, nil
	}
	response, err := transport.RoundTrip(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, "HTTP/2.0", response.Proto,
		"an explicitly unprocessed request may be retried on another version")
	require.Equal(t, 1, h2Server.executed())
	require.Equal(t, []string{"payload"}, h2Server.receivedBodies())
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, "ok", string(body))

	require.EqualValues(t, 2, streams.Load(),
		"the warm GET and one rejected POST; the repository must not re-send on H3")
	require.Equal(t, int32(1), original.closes.Load(), "the original body is closed exactly once")
	require.Zero(t, original.readsAfterClose.Load(),
		"the original body must never be read after the warm attempt closed it")
	require.Len(t, copies, 1, "exactly one rebuilt body is used for the fallback")
	require.Equal(t, int32(1), waitForBodyClose(t, copies[0], "the rebuilt body"), "the rebuilt body is closed by the fallback")
}

// TestWarmRejectedOnceOnlyPostFailsWithoutReuse: the same warm rejection with a body that cannot be
// rebuilt. Nothing may be re-sent and the original must not be read after its single close.
func TestWarmRejectedOnceOnlyPostFailsWithoutReuse(t *testing.T) {
	address, streams := startWarmRejectingH3Peer(t)
	h2Server, h2Transport := startRetrySafetyH2Server(t, okHandler)
	transport := newRetrySafetyTransport(h2Transport, dialRetrySafetyH3(address), time.Hour)
	t.Cleanup(func() { _ = transport.h3Transport.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	warm, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+address+"/warm", nil)
	require.NoError(t, err)
	warmResponse, err := transport.RoundTrip(warm)
	require.NoError(t, err)
	require.NoError(t, warmResponse.Body.Close())

	original := &warmTrackedBody{reader: strings.NewReader("payload")}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+address+"/reject", original)
	require.NoError(t, err)
	response, err := transport.RoundTrip(request)
	if response != nil {
		_ = response.Body.Close()
	}
	require.Error(t, err)
	require.ErrorIs(t, err, errH3RequestRejected)
	require.Zero(t, h2Server.executed(), "a body that cannot be rebuilt must not be re-sent")
	require.EqualValues(t, 2, streams.Load())
	require.Equal(t, int32(1), original.closes.Load())
	require.Zero(t, original.readsAfterClose.Load())

	require.False(t, transport.h3Broken(requestAuthority(request)),
		"an explicit rejection must not arm the H3 verdict")
}

// TestColdRejectedReplayablePostWithoutKeyFallsBack: the same explicit rejection on a COLD pool
// with a replayable body and no Idempotency-Key. The dependency retries internally once with a
// rebuilt body and then this layer dispatches cross-version with another rebuild; the original is
// closed exactly once and never read after close.
func TestColdRejectedReplayablePostWithoutKeyFallsBack(t *testing.T) {
	address, streams := startRequestRejectingHTTP3Server(t)
	h2Server, h2Transport := startRetrySafetyH2Server(t, okHandler)
	transport := newRetrySafetyTransport(h2Transport, dialRetrySafetyH3(address), time.Hour)
	t.Cleanup(func() { _ = transport.h3Transport.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	original := &warmTrackedBody{reader: strings.NewReader("payload")}
	var copies []*warmTrackedBody
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+address+"/reject", original)
	require.NoError(t, err)
	request.GetBody = func() (io.ReadCloser, error) {
		copy := &warmTrackedBody{reader: strings.NewReader("payload")}
		copies = append(copies, copy)
		return copy, nil
	}
	response, err := transport.RoundTrip(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, "HTTP/2.0", response.Proto)
	require.Equal(t, 1, h2Server.executed())
	require.Equal(t, []string{"payload"}, h2Server.receivedBodies())
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, "ok", string(body))

	require.EqualValues(t, 2, streams.Load(),
		"the dependency's internal retry and this layer's attempt; no third H3 dispatch")
	require.Equal(t, int32(1), original.closes.Load())
	require.Zero(t, original.readsAfterClose.Load())
	require.Len(t, copies, 2)
	for index, copy := range copies {
		require.Equal(t, int32(1), waitForBodyClose(t, copy, fmt.Sprintf("rebuilt body %d", index)),
			"each rebuilt body is closed exactly once")
	}
	require.False(t, transport.h3Broken(requestAuthority(request)))
}

// TestWarmBodylessRejectIsReportedWithoutAnUnsafeRetry documents the bodyless
// compatibility limit the review found: for a nil body (or http.NoBody) quic-go's
// OnlyCached request can be reused for its internal retry regardless of GetBody,
// so the rejection is lost and the flow ends on the "cache disappeared after
// dispatch" guard. That guard is deliberate: the evidence is gone, and a cold
// retry (or an H2 fallback) would be a guess. No unsafe duplicate is sent, no
// verdict is armed, and the warm GET path is unaffected. Making this case fall
// back needs dependency-level evidence, so it is recorded as a compatibility
// todo rather than worked around here.
func TestWarmBodylessRejectIsReportedWithoutAnUnsafeRetry(t *testing.T) {
	for _, testCase := range []struct {
		name string
		body io.Reader
		nil  bool
	}{
		{name: "nil body", nil: true},
		{name: "http.NoBody", body: http.NoBody},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			address, streams := startWarmRejectingH3Peer(t)
			h2Server, h2Transport := startRetrySafetyH2Server(t, okHandler)
			transport := newRetrySafetyTransport(h2Transport, dialRetrySafetyH3(address), time.Hour)
			t.Cleanup(func() { _ = transport.h3Transport.Close() })

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			warm, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+address+"/warm", nil)
			require.NoError(t, err)
			warmResponse, err := transport.RoundTrip(warm)
			require.NoError(t, err)
			require.Equal(t, "HTTP/3.0", warmResponse.Proto)
			require.NoError(t, warmResponse.Body.Close())

			var request *http.Request
			if testCase.nil {
				request, err = http.NewRequestWithContext(ctx, http.MethodPost, "https://"+address+"/empty", nil)
			} else {
				request, err = http.NewRequestWithContext(ctx, http.MethodPost, "https://"+address+"/empty", testCase.body)
			}
			require.NoError(t, err)
			response, err := transport.RoundTrip(request)
			if response != nil {
				_ = response.Body.Close()
			}
			require.ErrorContains(t, err, "cache entry disappeared after the request was dispatched",
				"the lost evidence must stop the retry instead of guessing")
			require.Zero(t, h2Server.executed(), "no unsafe cross-version retry")
			require.EqualValues(t, 2, streams.Load(), "the warm GET and the rejected bodyless POST only")
			require.False(t, transport.h3Broken(requestAuthority(request)), "the rejection is not a verdict")
		})
	}
}
