package v2rayxhttp

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	M "github.com/sagernet/sing/common/metadata"
)

// lx: XHTTP_OUTPERFORM — allocation accounting for the packet-up uplink.
//
// These benchmarks exist because the fix that made packet-up competitive
// (batching writes into posts instead of one post per write) is only half the
// story: it moves cost from per-write to per-post, and the remaining per-byte
// and per-post costs are what a future change would regress. They measure THIS
// package's work, with a stub transport standing in for the HTTP stack, so a
// number that moves is a number this package moved.
//
// They do not measure the TLS record layer, x/net/http2's framing or the
// kernel: those belong to the libraries this fork and the reference both use.

// benchTransport answers every request immediately and consumes the body, so a
// benchmark measures the transport's own work rather than a server's.
type benchTransport struct{}

func (benchTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Body != nil {
		_, _ = io.Copy(io.Discard, request.Body)
		request.Body.Close()
	}
	if request.Method == http.MethodGet {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(newBlockingReader())}, nil
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(""))}, nil
}

func benchClient(b *testing.B, options metaOptions) *Client {
	b.Helper()
	if options.ScMinPostsIntervalMs == "" {
		options.ScMinPostsIntervalMs = "0"
	}
	meta, err := normalizeMeta(options, modePacketUp)
	if err != nil {
		b.Fatalf("normalizeMeta: %v", err)
	}
	return &Client{
		ctx:          context.Background(),
		scheme:       "https",
		host:         "example.com",
		serverAddr:   M.ParseSocksaddr("example.com:443"),
		path:         "/xhttp",
		mode:         modePacketUp,
		headers:      make(http.Header),
		paddingRange: intRange{100, 1000},
		meta:         meta,
		xmux:         singleTransportXmux(benchTransport{}),
	}
}

// BenchmarkPacketUpUplinkWrite reports the cost of handing bytes to the uplink,
// which is what the write path does now: one bounded copy into the queue.
//
// The post side of the cost is attributed to this benchmark too — the flusher
// runs while the loop is running and its allocations land in the same MemStats
// window — so the number is "write plus the posts it eventually causes", not
// "write alone".
func BenchmarkPacketUpUplinkWrite(b *testing.B) {
	for _, size := range []int{8 * 1024, 64 * 1024} {
		name := "64KiB-writes"
		if size == 8*1024 {
			name = "8KiB-writes"
		}
		b.Run(name, func(b *testing.B) {
			client := benchClient(b, metaOptions{})
			conn, err := client.DialContext(context.Background())
			if err != nil {
				b.Fatalf("DialContext: %v", err)
			}
			defer conn.Close()
			payload := make([]byte, size)
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err = conn.Write(payload); err != nil {
					b.Fatalf("Write: %v", err)
				}
			}
			b.StopTimer()
			// Drain the queue so the last post is not counted after the loop.
			_ = conn.Close()
		})
	}
}

// BenchmarkPacketUpPostConstruction reports the per-post cost that batching
// amortises: building the request for one batch and attaching the payload. With
// the default 1 MB post cap and the default 30 ms interval, a stream emits on
// the order of 30 posts per second, so this cost is paid ~30 times a second
// rather than once per write.
func BenchmarkPacketUpPostConstruction(b *testing.B) {
	client := benchClient(b, metaOptions{})
	payload := make([]byte, 1<<20)
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		request, err := client.newRequest(context.Background(), http.MethodPost, "session", "7", nil)
		if err != nil {
			b.Fatalf("newRequest: %v", err)
		}
		client.applyUplinkData(request, payload)
	}
}

// BenchmarkPaddingRefererAssembly compares the concatenation the padding header
// now uses against the url.URL -> Query -> Encode -> String chain it replaced
// (kept in xpadding_referer_test.go, which also pins the two byte-identical).
// The legacy form ran on every request, so on the packet-up path this is a
// per-post saving; the benchmark is what turns "fewer allocations" into a number.
func BenchmarkPaddingRefererAssembly(b *testing.B) {
	const padLength = 500
	pad := strings.Repeat("0", padLength)
	client := benchClient(b, metaOptions{})
	client.paddingRange = intRange{padLength, padLength}
	requestURL := &url.URL{Scheme: client.scheme, Host: client.host, Path: "/xhttp/session/7"}

	b.Run("concatenation", func(b *testing.B) {
		request := &http.Request{Header: make(http.Header), URL: requestURL}
		b.ReportAllocs()
		for b.Loop() {
			client.applyXPadding(request)
		}
	})

	b.Run("legacy url chain", func(b *testing.B) {
		request := &http.Request{Header: make(http.Header), URL: requestURL}
		b.ReportAllocs()
		for b.Loop() {
			request.Header.Set("Referer", legacyRefererChain(client.scheme, client.host, request.URL, pad))
		}
	})
}
