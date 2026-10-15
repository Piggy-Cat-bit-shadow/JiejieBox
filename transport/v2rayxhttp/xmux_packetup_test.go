package v2rayxhttp

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
)

// SPECS/TASKS/059-XHTTP_XMUX §4
//
// h_max_request_times counts HTTP REQUESTS, not streams. packet-up issues one
// upload POST per flush (lx: XHTTP_OUTPERFORM — that used to read "per Write":
// writes are queued and coalesced now, so a stream's request count is driven by
// how often its queue flushes, not by how many times the caller wrote). Counting
// streams instead would leave the connection alive far past what the server was
// told, which is exactly the compatibility gap the task exists to close.

// countingTransport answers every request with 200 and counts them.
type countingTransport struct {
	requests atomic.Int32
}

func (t *countingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	t.requests.Add(1)
	if request.Body != nil {
		io.Copy(io.Discard, request.Body)
		request.Body.Close()
	}
	// Only the download GET holds its body open, like a real server. Upload POST
	// responses must end: sendPacket drains them inline, so a blocking body there
	// would wedge every Write.
	if request.Method == http.MethodGet {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(newBlockingReader()),
		}, nil
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("")),
	}, nil
}

// blockingReader stands in for a download stream that stays open.
type blockingReader struct {
	release chan struct{}
}

func newBlockingReader() *blockingReader {
	return &blockingReader{release: make(chan struct{})}
}

func (r *blockingReader) Read(p []byte) (int, error) {
	<-r.release
	return 0, io.EOF
}

func TestPacketUpUploadsCountAgainstRequestLimit(t *testing.T) {
	meta, err := normalizeMeta(metaOptions{
		ScMinPostsIntervalMs: "0",
	}, modePacketUp)
	if err != nil {
		t.Fatalf("normalizeMeta: %v", err)
	}
	transport := &countingTransport{}
	client := &Client{
		ctx:        context.Background(),
		scheme:     "https",
		host:       "example.com",
		serverAddr: M.ParseSocksaddr("example.com:443"),
		path:       "/xhttp",
		mode:       modePacketUp,
		meta:       meta,
		xmux:       singleTransportXmux(transport),
	}
	// Four requests allowed: the download GET plus three upload POSTs.
	client.xmux.config.hMaxRequestTimes = intRange{4, 4}

	xmuxClient, _ := client.xmux.get()
	xmuxClient.addOpenUsage(1)
	conn, err := client.dialPacketUp(context.Background(), "session", xmuxClient, newXmuxRelease(xmuxClient))
	if err != nil {
		t.Fatalf("dialPacketUp: %v", err)
	}
	defer conn.Close()

	// The download GET is dispatched from its own goroutine at dial, so count it
	// before the loop: otherwise the first write's delta would be the POST plus a
	// GET that happened to land while the test was waiting.
	deadline := time.Now().Add(2 * time.Second)
	for transport.requests.Load() < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	// One write per flush: a write queues and the flusher posts it, so waiting
	// for each POST to land is what makes "three writes, three requests" a
	// statement about the request budget instead of about flush timing.
	for i := 0; i < 3; i++ {
		before := transport.requests.Load()
		if _, err := conn.Write([]byte("payload")); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		deadline = time.Now().Add(2 * time.Second)
		for transport.requests.Load() == before && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		if got := transport.requests.Load(); got != before+1 {
			t.Fatalf("write %d produced %d upload posts, want 1", i, got-before)
		}
	}

	// The download RoundTrip runs in its own goroutine; wait for it to land so the
	// count is stable.
	deadline = time.Now().Add(2 * time.Second)
	for transport.requests.Load() < 4 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	if got := transport.requests.Load(); got != 4 {
		t.Fatalf("transport saw %d requests, want 4 (1 download + 3 uploads)", got)
	}
	if left := atomic.LoadInt32(&xmuxClient.leftRequests); left != 0 {
		t.Fatalf("leftRequests = %d after 4 requests against a limit of 4, want 0", left)
	}
	if cause := xmuxClient.evictCause(); cause != "requests" {
		t.Fatalf("evictCause = %q, want %q — the connection must be retired once its request budget is spent", cause, "requests")
	}
}
