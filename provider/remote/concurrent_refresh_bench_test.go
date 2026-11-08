package remote

// V03A — the same workload with and without the build reservation, measured on this machine.
//
// Scenario: eight remote providers refresh at the same time, each downloading a ~1 MiB
// document from one local httptest server. That is the "concurrent build" case the card allows
// when the 10,000-node production queue is not available: same machine, same documents, same
// server, same tag set - the only difference between the two sub-benchmarks is whether a
// buildgate with two slots is installed on the context.
//
// What it shows: with the reservation, concurrent builds stay inside its limit and the extra
// ones are deferred (retryable, reported); without it, all eight build at once. The gated run
// trades tail latency for a bounded peak, which is the trade F03 makes.
//
// Replay:
//
//	go test ./provider/remote -run '^$' -bench BenchmarkConcurrentRefresh -benchtime 1x -count 3 -v

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/buildgate"
)

// concurrentRefreshProviders is the fan-out: eight providers refreshing together.
const concurrentRefreshProviders = 8

// buildConcurrentDocument produces a valid node list of roughly size bytes.
func buildConcurrentDocument(size int) string {
	var builder strings.Builder
	builder.WriteString(`{"outbounds": [`)
	node := `{"type": "test", "tag": "node-%05d-%s"}`
	padding := strings.Repeat("y", 256)
	for index := 0; builder.Len() < size; index++ {
		if index > 0 {
			builder.WriteString(",")
		}
		fmt.Fprintf(&builder, node, index, padding)
	}
	builder.WriteString(`]}`)
	return builder.String()
}

// refreshFanOut runs one refresh per provider and reports wall time, deferrals and the peak
// number of concurrent downloads the server actually saw.
func refreshFanOut(t testing.TB, gate *buildgate.Gate) (elapsed time.Duration, deferred uint64, peak int64) {
	document := buildConcurrentDocument(1 << 20)
	var inflight, peakInflight atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		current := inflight.Add(1)
		defer inflight.Add(-1)
		for {
			observed := peakInflight.Load()
			if current <= observed || peakInflight.CompareAndSwap(observed, current) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		_, _ = writer.Write([]byte(document))
	}))
	defer server.Close()

	providers := make([]*ProviderRemote, concurrentRefreshProviders)
	for index := range providers {
		provider, _ := newRemoteHarness(t, server.URL)
		provider.httpClient = server.Client()
		providers[index] = provider
	}
	ctx := context.Background()
	if gate != nil {
		ctx = buildgate.ContextWithGate(ctx, gate)
	}
	start := time.Now()
	var group sync.WaitGroup
	for _, provider := range providers {
		group.Add(1)
		go func(provider *ProviderRemote) {
			defer group.Done()
			_ = provider.fetch(ctx, true)
		}(provider)
	}
	group.Wait()
	elapsed = time.Since(start)
	if gate != nil {
		deferred = gate.Stats().Deferred
	}
	return elapsed, deferred, peakInflight.Load()
}

func BenchmarkConcurrentRefresh(b *testing.B) {
	b.Run("ungated", func(b *testing.B) {
		for range b.N {
			elapsed, _, peak := refreshFanOut(b, nil)
			b.ReportMetric(float64(elapsed.Nanoseconds())/1e6, "ms-wall")
			b.ReportMetric(float64(peak), "peak-downloads")
		}
	})
	b.Run("gated-two-slots", func(b *testing.B) {
		for range b.N {
			gate := buildgate.New(2, 64, 5*time.Second)
			elapsed, deferred, peak := refreshFanOut(b, gate)
			b.ReportMetric(float64(elapsed.Nanoseconds())/1e6, "ms-wall")
			b.ReportMetric(float64(peak), "peak-downloads")
			b.ReportMetric(float64(deferred), "deferred")
			b.ReportMetric(float64(gate.Stats().MaxInflight), "peak-builds")
			b.ReportMetric(float64(gate.Stats().AdmittedBuilds), "admitted")
		}
	})
}
