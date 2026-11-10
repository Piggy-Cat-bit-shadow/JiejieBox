package httpclient

import (
	"context"
	stdTLS "crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/quic-go/http3"

	"github.com/stretchr/testify/require"
)

// The lifetime of a request body once the response headers arrive.
//
// Two invariants must hold together: receiving the headers only delivers the response (the upload
// may continue), and a caller that gives up - by closing the response or by cancelling the request
// - must be able to end an upload blocked inside Read. quic-go's send goroutine closes the body
// when it reaches EOF, but closing a QUIC stream does not wake a blocked application Reader, so
// the release has to come from the body's owner.

type readSignalBody struct {
	io.ReadCloser
	entered   chan struct{}
	exited    chan struct{}
	enterOnce sync.Once
	exitOnce  sync.Once
}

func (b *readSignalBody) Read(p []byte) (int, error) {
	b.enterOnce.Do(func() { close(b.entered) })
	defer b.exitOnce.Do(func() { close(b.exited) })
	return b.ReadCloser.Read(p)
}

// countedPipeBody counts closes AND actually closes the pipe: auditTrackedBody only counts, which
// would make an "owner closed" test pass while the blocked Read stays blocked.
type countedPipeBody struct {
	reader *io.PipeReader
	closes atomic.Int32
}

func (b *countedPipeBody) Read(p []byte) (int, error) { return b.reader.Read(p) }

func (b *countedPipeBody) Close() error {
	b.closes.Add(1)
	return b.reader.Close()
}

func newBlockedUpload(t *testing.T) (*readSignalBody, *io.PipeWriter, *countedPipeBody) {
	t.Helper()
	reader, writer := io.Pipe()
	tracked := &countedPipeBody{reader: reader}
	upload := &readSignalBody{
		ReadCloser: tracked,
		entered:    make(chan struct{}),
		exited:     make(chan struct{}),
	}
	return upload, writer, tracked
}

// startHeaderFirstServer must NOT use retrySafetyServer: its handler wrapper reads the whole
// request body before the test handler runs, which would block before the 200 and defeat the
// point of these tests.
func startHeaderFirstServer(t *testing.T, upload *readSignalBody) string {
	t.Helper()
	packet, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	server := &http3.Server{
		TLSConfig: &stdTLS.Config{
			Certificates: []stdTLS.Certificate{retrySafetyCertificate(t)},
			NextProtos:   []string{http3.NextProtoH3},
		},
		Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			<-upload.entered
			writer.WriteHeader(http.StatusOK)
			writer.(http.Flusher).Flush()
			_, _ = io.Copy(io.Discard, request.Body)
		}),
	}
	go func() { _ = server.Serve(packet) }()
	t.Cleanup(func() {
		_ = server.Close()
		_ = packet.Close()
	})
	return packet.LocalAddr().String()
}

// TestAbandoningTheWinningResponseReleasesABlockedUpload is the race path: the winning upload is
// blocked inside Read, the server answers 200, and the caller closes the response and cancels.
// The send goroutine must leave Read.
func TestAbandoningTheWinningResponseReleasesABlockedUpload(t *testing.T) {
	upload, writer, tracked := newBlockedUpload(t)
	defer writer.Close()
	address := startHeaderFirstServer(t, upload)
	transport := newRetrySafetyTransport(nil, dialRetrySafetyH3(address), time.Hour)
	t.Cleanup(func() { _ = transport.h3Transport.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+address, strings.NewReader("payload"))
	require.NoError(t, err)
	request.Header.Set("Idempotency-Key", "abandoned-upload")
	clones := 0
	request.GetBody = func() (io.ReadCloser, error) {
		clones++
		if clones == 1 {
			return upload, nil
		}
		return io.NopCloser(strings.NewReader("payload")), nil
	}
	response, err := transport.RoundTrip(request)
	require.NoError(t, err)
	require.Equal(t, "HTTP/3.0", response.Proto)
	require.NoError(t, response.Body.Close())
	cancel()

	select {
	case <-upload.exited:
	case <-time.After(time.Second):
		t.Fatal("the winning send goroutine is still blocked inside Body.Read after abandoning the response")
	}
	require.Equal(t, int32(1), tracked.closes.Load(), "the body must be closed exactly once")
}

// TestCallerCancellationAloneReleasesABlockedUpload: the caller cancels the request without
// closing the response. That is still abandonment and must release the same Read.
func TestCallerCancellationAloneReleasesABlockedUpload(t *testing.T) {
	upload, writer, tracked := newBlockedUpload(t)
	defer writer.Close()
	address := startHeaderFirstServer(t, upload)
	transport := newRetrySafetyTransport(nil, dialRetrySafetyH3(address), time.Hour)
	t.Cleanup(func() { _ = transport.h3Transport.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+address, strings.NewReader("payload"))
	require.NoError(t, err)
	request.Header.Set("Idempotency-Key", "cancel-only")
	clones := 0
	request.GetBody = func() (io.ReadCloser, error) {
		clones++
		if clones == 1 {
			return upload, nil
		}
		return io.NopCloser(strings.NewReader("payload")), nil
	}
	response, err := transport.RoundTrip(request)
	require.NoError(t, err)
	defer response.Body.Close()
	cancel()

	select {
	case <-upload.exited:
	case <-time.After(time.Second):
		t.Fatal("cancelling the request did not release the blocked upload")
	}
	require.Equal(t, int32(1), tracked.closes.Load())
}

// TestColdH3AbandonedResponseReleasesABlockedUpload is the same invariant on the sequential cold
// path (no GetBody, so no race): closing the response must release the body there too.
func TestColdH3AbandonedResponseReleasesABlockedUpload(t *testing.T) {
	upload, writer, tracked := newBlockedUpload(t)
	defer writer.Close()
	address := startHeaderFirstServer(t, upload)
	transport := newRetrySafetyTransport(nil, dialRetrySafetyH3(address), time.Hour)
	t.Cleanup(func() { _ = transport.h3Transport.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+address, upload)
	require.NoError(t, err)
	response, err := transport.RoundTrip(request)
	require.NoError(t, err)
	require.Equal(t, "HTTP/3.0", response.Proto)
	require.NoError(t, response.Body.Close())

	select {
	case <-upload.exited:
	case <-time.After(time.Second):
		t.Fatal("closing the response did not release the blocked upload on the cold path")
	}
	require.Equal(t, int32(1), tracked.closes.Load())
}

type firstReadErrorBody struct{}

func (firstReadErrorBody) Read([]byte) (int, error) {
	return 0, errors.New("first upload read failed")
}

func (firstReadErrorBody) Close() error { return nil }

// TestFirstUploadReadErrorStillClosesTheBody: the very first Read returns 0, err. The transport
// then reports a request error (the peer sees an incomplete request), and the body must still be
// closed - the send goroutine's only Close must not be swallowed.
func TestFirstUploadReadErrorStillClosesTheBody(t *testing.T) {
	server := startRetrySafetyH3Server(t, okHandler)
	transport := newRetrySafetyTransport(nil, dialRetrySafetyH3(server.address), time.Hour)
	t.Cleanup(func() { _ = transport.h3Transport.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	tracked := &auditTrackedBody{Reader: firstReadErrorBody{}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+server.address, tracked)
	require.NoError(t, err)
	response, err := transport.RoundTrip(request)
	if response != nil {
		_ = response.Body.Close()
	}
	require.Error(t, err, "a body read error aborts the request")
	require.Eventually(t, func() bool { return tracked.closes.Load() == 1 }, time.Second, time.Millisecond,
		"the upload body must be closed even when the first read fails")
}
