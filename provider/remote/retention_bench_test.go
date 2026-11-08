package remote

// M01 — the maintenance scan a refresh leaves behind, measured rather than assumed.
//
// A provider document is parsed into outbound options and the decoded bytes are dropped when
// the apply returns. The refresh loop therefore calls runtime.GC() before parking, which is a
// FULL collection: the most expensive maintenance scan in the process. Before removing it (or
// gating it on the power governor's deep-idle state) the question is how much it actually
// reclaims, because "it looks redundant" is exactly the kind of change that turns into a
// residency regression.
//
// This benchmark answers that for the real apply path: it applies a document, then reports the
// heap before and after one forced collection. Run it explicitly:
//
//	go test ./provider/remote -run '^$' -bench BenchmarkRefreshRetention -benchtime 1x -v
//
// It is a benchmark, not a test: a threshold in CI would be a flaky assertion about the Go
// runtime rather than about this code.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
)

// buildRetentionDocument produces a syntactically valid node list of approximately size bytes.
//
// The bulk is carried in the tag: the test node type has no other field, and an unknown field
// would be rejected by the decoder (DisallowUnknownFields), which is the correct behaviour and
// must not be worked around in a measurement.
func buildRetentionDocument(size int) string {
	var builder strings.Builder
	builder.WriteString(`{"outbounds": [`)
	node := `{"type": "test", "tag": "node-%05d-%s"}`
	padding := strings.Repeat("x", 256)
	for index := 0; builder.Len() < size; index++ {
		if index > 0 {
			builder.WriteString(",")
		}
		fmt.Fprintf(&builder, node, index, padding)
	}
	builder.WriteString(`]}`)
	return builder.String()
}

func BenchmarkRefreshRetention(b *testing.B) {
	document := buildRetentionDocument(1 << 20)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(document))
	}))
	defer server.Close()

	harness := &testing.T{}
	provider, _ := newRemoteHarness(harness, server.URL)
	provider.httpClient = server.Client()

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		provider.lastOutOpts = nil
		before := readHeap()
		if err := provider.fetch(provider.ctx, false); err != nil {
			b.Fatal(err)
		}
		afterApply := readHeap()
		runtime.GC()
		afterGC := readHeap()
		b.ReportMetric(float64(int64(afterApply)-int64(before))/1024/1024, "MiB-applied")
		b.ReportMetric(float64(int64(afterGC)-int64(afterApply))/1024/1024, "MiB-reclaimed")
	}
}

// readHeap returns the live heap size in bytes.
func readHeap() uint64 {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}
