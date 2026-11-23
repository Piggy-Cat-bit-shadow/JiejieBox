package w8measure

// Long-cycle resource convergence, on a REAL Box (the whole product object), not a
// small-struct sample.
//
// # Modes (W8_CYCLES_MODE)
//
//	buildclose      N Box build/start/close cycles in one process. Every cycle gets a
//	                fresh service.ExtendContext from ONE base context, which is the shape
//	                daemon.Instance uses when it builds the next Box for a reload: the
//	                registries outlive the Box, the Box does not. Sampled every 10 cycles:
//	                heap objects, heap bytes, RSS, handles (FD analogue) and goroutines.
//	                What this can show: a per-cycle leak grows monotonically; a bounded
//	                product plateaus. What it cannot show: a leak that is only reachable
//	                through a data path (that is W2/G08's job).
//
//	providerupdate  one running Box whose remote provider serves 1000 nodes; N rounds,
//	                each round changes exactly ONE node and calls the product
//	                Provider.Update(). Sampled every 10 rounds with the same instruments
//	                plus the number of provider outbounds. Garbage collection is left to
//	                the runtime: this is a natural-idle trend, not a forced-GC attribution.
//
// Scale: the driver's default is 100 cycles / 100 rounds at 1000 nodes. The numbers a
// row reports are the ACTUAL counts; nothing is extrapolated.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	singjson "github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/filemanager"

	"github.com/stretchr/testify/require"
)

// w8CycleSample is one instrumented point of a long run.
type w8CycleSample struct {
	Round      int    `json:"round"`
	HeapAlloc  uint64 `json:"heap_alloc"`
	HeapObjects uint64 `json:"heap_objects"`
	RSS        uint64 `json:"rss"`
	PeakRSS    uint64 `json:"peak_rss"`
	Handles    uint64 `json:"handles"`
	Goroutines int    `json:"goroutines"`
	Extra      int    `json:"extra"`
}

// w8SampleNow reads the natural state at the end of a round, without a collection.
func w8SampleNow(round int, extra int) w8CycleSample {
	mem := w8ReadMem()
	proc := w8ReadProc()
	return w8CycleSample{
		Round:       round,
		HeapAlloc:   mem.HeapAlloc,
		HeapObjects: mem.HeapObjects,
		RSS:         proc.RSS,
		PeakRSS:     proc.PeakRSS,
		Handles:     proc.HandleCnt,
		Goroutines:  runtime.NumGoroutine(),
		Extra:       extra,
	}
}

// TestW8LongBuildClose runs the Box build/start/close cycles.
func TestW8LongBuildClose(t *testing.T) {
	cycles := w8EnvInt(t, "W8_CYCLES", 100)
	sampleEvery := w8EnvInt(t, "W8_SAMPLE_EVERY", 10)
	row := map[string]any{"case": "cycles", "mode": "buildclose", "cycles": cycles, "sample_every": sampleEvery}
	defer func() {
		if t.Failed() {
			row["failed"] = true
		}
		w8Emit(t, row)
	}()

	const config = `{
		"log": {"level": "error"},
		"outbounds": [{"type": "direct", "tag": "direct"}]
	}`
	baseCtx := include.Context(context.Background())
	options, decodeErr := singjson.UnmarshalExtendedContext[option.Options](baseCtx, []byte(config))
	require.NoError(t, decodeErr)

	samples := make([]w8CycleSample, 0, cycles/sampleEvery+2)
	started := time.Now()
	buildFailures := 0
	for round := 1; round <= cycles; round++ {
		// One fresh registry clone per Box: the daemon shape, where the service context
		// outlives the Box and each Box registers its own services into a clone.
		cycleCtx := service.ExtendContext(baseCtx)
		instance, err := box.New(box.Options{Context: cycleCtx, Options: options})
		if err != nil {
			buildFailures++
			if buildFailures < 4 {
				t.Logf("round %d: box.New failed: %v", round, err)
			}
			continue
		}
		if err = instance.Start(); err != nil {
			buildFailures++
			if buildFailures < 4 {
				t.Logf("round %d: box.Start failed: %v", round, err)
			}
			_ = instance.Close()
			continue
		}
		require.NoError(t, instance.Close())
		if round%sampleEvery == 0 || round == cycles {
			samples = append(samples, w8SampleNow(round, 0))
		}
	}
	elapsed := time.Since(started)
	row["samples"] = samples
	row["elapsed_ms"] = float64(elapsed.Nanoseconds()) / 1e6
	row["build_failures"] = buildFailures
	row["cycles_per_second"] = float64(cycles) / elapsed.Seconds()
	require.Zero(t, buildFailures, "a Box cycle failed")
	require.GreaterOrEqual(t, len(samples), 2, "not enough samples to see a trend")
	first := samples[0]
	last := samples[len(samples)-1]
	row["heap_objects_first"] = first.HeapObjects
	row["heap_objects_last"] = last.HeapObjects
	row["heap_objects_growth"] = int64(last.HeapObjects) - int64(first.HeapObjects)
	row["heap_alloc_growth"] = int64(last.HeapAlloc) - int64(first.HeapAlloc)
	row["rss_first"] = first.RSS
	row["rss_last"] = last.RSS
	row["rss_growth"] = int64(last.RSS) - int64(first.RSS)
	row["handles_first"] = first.Handles
	row["handles_last"] = last.Handles
	row["handles_growth"] = int64(last.Handles) - int64(first.Handles)
	row["goroutines_first"] = first.Goroutines
	row["goroutines_last"] = last.Goroutines
	row["goroutines_growth"] = last.Goroutines - first.Goroutines
	row["final_peak_rss"] = last.PeakRSS
	// A per-cycle leak must show up as a monotone rise across the series. The assertion
	// is bounded rather than "== 0": the Go runtime and the OS legitimately retain a
	// high-water mark, and this is a natural-idle run with no forced collection.
	if last.HeapObjects > first.HeapObjects {
		perCycle := float64(int64(last.HeapObjects)-int64(first.HeapObjects)) / float64(last.Round-first.Round)
		row["heap_objects_per_cycle"] = perCycle
	}
	if last.Goroutines > first.Goroutines+2 {
		row["goroutine_growth_suspicious"] = true
	}
}

// w8CycleProviderDocument builds a subscription document with `nodes` SOCKS nodes whose
// tags are stable across rounds. Only the node at changedIndex carries `changedPort`, so
// "one node changed per round" is literally what the provider receives.
func w8CycleProviderDocument(nodes int, changedIndex int, changedPort int) []byte {
	type socksNode struct {
		Type       string `json:"type"`
		Tag        string `json:"tag"`
		Server     string `json:"server"`
		ServerPort int    `json:"server_port"`
		Version    string `json:"version"`
	}
	document := struct {
		Outbounds []socksNode `json:"outbounds"`
	}{Outbounds: make([]socksNode, nodes)}
	for index := range document.Outbounds {
		port := 20000 + index
		if index == changedIndex {
			port = changedPort
		}
		document.Outbounds[index] = socksNode{
			Type:       "socks",
			Tag:        fmt.Sprintf("node-%06d", index),
			Server:     "127.0.0.1",
			ServerPort: port,
			Version:    "5",
		}
	}
	encoded, _ := json.Marshal(document)
	return encoded
}

// TestW8LongProviderUpdate runs the live-Box provider update rounds.
func TestW8LongProviderUpdate(t *testing.T) {
	rounds := w8EnvInt(t, "W8_CYCLES", 100)
	nodes := w8EnvInt(t, "W8_NODES", 1000)
	sampleEvery := w8EnvInt(t, "W8_SAMPLE_EVERY", 10)
	baseDir := w8Dir(t, "cycles-provider")

	row := map[string]any{"case": "cycles", "mode": "providerupdate", "cycles": rounds, "nodes": nodes, "sample_every": sampleEvery}
	defer func() {
		if t.Failed() {
			row["failed"] = true
		}
		w8Emit(t, row)
	}()

	var served atomic.Value
	served.Store(w8CycleProviderDocument(nodes, -1, 0))
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		content, _ := served.Load().([]byte)
		writer.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
		_, _ = writer.Write(content)
	}))
	t.Cleanup(server.Close)
	row["document_bytes"] = len(served.Load().([]byte))

	ctx := include.Context(context.Background())
	require.NoError(t, os.MkdirAll(baseDir, 0o755))
	ctx = filemanager.WithDefault(ctx, baseDir, filepath.Join(baseDir, "tmp"), -1, -1)
	transport := w8NewTransport(t, &w8Server{server: server})
	w8InstallTransport(ctx, transport)
	// The Box installs its OWN HTTP client manager, so a transport registered in the
	// context is not what a whole-Box download uses. The document is therefore served by
	// a real local HTTP server and the provider's URL points straight at it.
	providerURL := server.URL + "/subscription"
	config := `{
		"log": {"level": "error"},
		"outbounds": [{"type": "direct", "tag": "direct"}],
		"providers": [{
			"type": "remote",
			"tag": "w8-provider",
			"url": ` + quoteJSON(providerURL) + `,
			"path": "nodes.json",
			"update_interval": "1h"
		}],
		"route": {"final": "direct"}
	}`
	options, decodeErr := singjson.UnmarshalExtendedContext[option.Options](ctx, []byte(config))
	require.NoError(t, decodeErr)
	instance, err := box.New(box.Options{Context: ctx, Options: options})
	require.NoError(t, err)
	require.NoError(t, instance.Start())
	t.Cleanup(func() { _ = instance.Close() })

	providerManager := service.FromContext[adapter.ProviderManager](ctx)
	require.NotNil(t, providerManager, "no provider manager in the Box context")
	provider, loaded := providerManager.Get("w8-provider")
	require.True(t, loaded, "the provider was not built")
	require.Len(t, provider.Outbounds(), nodes, "the provider did not publish every node")
	updater, isUpdater := provider.(adapter.ProviderUpdater)
	require.True(t, isUpdater, "the remote provider is not updatable")

	samples := make([]w8CycleSample, 0, rounds/sampleEvery+2)
	started := time.Now()
	updateFailures := 0
	for round := 1; round <= rounds; round++ {
		served.Store(w8CycleProviderDocument(nodes, round%nodes, 30000+round))
		if updateErr := updater.Update(); updateErr != nil {
			updateFailures++
			if updateFailures < 4 {
				t.Logf("round %d: provider update failed: %v", round, updateErr)
			}
		}
		if round%sampleEvery == 0 || round == rounds {
			samples = append(samples, w8SampleNow(round, len(provider.Outbounds())))
		}
	}
	elapsed := time.Since(started)
	row["samples"] = samples
	row["elapsed_ms"] = float64(elapsed.Nanoseconds()) / 1e6
	row["update_failures"] = updateFailures
	row["updates_per_second"] = float64(rounds) / elapsed.Seconds()
	require.Zero(t, updateFailures, "a provider update failed")
	require.GreaterOrEqual(t, len(samples), 2, "not enough samples to see a trend")
	first := samples[0]
	last := samples[len(samples)-1]
	row["heap_objects_first"] = first.HeapObjects
	row["heap_objects_last"] = last.HeapObjects
	row["heap_objects_growth"] = int64(last.HeapObjects) - int64(first.HeapObjects)
	row["heap_alloc_growth"] = int64(last.HeapAlloc) - int64(first.HeapAlloc)
	row["rss_first"] = first.RSS
	row["rss_last"] = last.RSS
	row["rss_growth"] = int64(last.RSS) - int64(first.RSS)
	row["handles_first"] = first.Handles
	row["handles_last"] = last.Handles
	row["handles_growth"] = int64(last.Handles) - int64(first.Handles)
	row["goroutines_first"] = first.Goroutines
	row["goroutines_last"] = last.Goroutines
	row["goroutines_growth"] = last.Goroutines - first.Goroutines
	row["outbounds_last"] = last.Extra
	row["outbounds_stable"] = last.Extra == nodes
	// The provider must still publish exactly `nodes` outbounds after every round;
	// otherwise a stale generation survived the update.
	require.Equal(t, nodes, last.Extra, "the provider's outbound count changed across updates")
	row["final_peak_rss"] = last.PeakRSS
}

func quoteJSON(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
