//go:build with_quic

package masque

// The measured next-use cost of the retirement release, through a real tunnel.
//
// The device-level measurement (transport/device/release_cost_test.go) says what one release frees and
// what one rebuild costs in isolation. This one says what the PRODUCT pays, in the shape the product
// pays it: a real CONNECT-IP session retires, the stack is released, the next session comes up and the
// first business byte has to cross.
//
// The phases are the user-visible ones:
//
//	built        a flow crossed the tunnel, so the stack exists
//	released     the session retired and Deferred.Release returned nil
//	ready        the replacement session was established and configured
//	first-packet the first business byte crossed the REBUILT stack
//	warm         an immediate second round trip on the same rebuilt stack, for contrast
//
// Gated by JJB_W4_COST=1: it forces two collections per phase and runs several full session cycles.
// The numbers are reported raw (one line per sample plus a medians line); nothing on a product path
// was changed to make them look better.

import (
	"context"
	"encoding/json"
	"io"
	"net/netip"
	"os"
	"runtime"
	"sort"
	"strconv"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

type tunnelReleaseCostSample struct {
	Sample             int     `json:"sample"`
	BuiltHeap          uint64  `json:"built_heap"`
	BuiltObjects       uint64  `json:"built_objects"`
	BuiltGoroutines    int     `json:"built_goroutines"`
	ReleasedHeap       uint64  `json:"released_heap"`
	ReleasedObjects    uint64  `json:"released_objects"`
	ReleasedGoroutines int     `json:"released_goroutines"`
	RebuiltHeap        uint64  `json:"rebuilt_heap"`
	RebuiltObjects     uint64  `json:"rebuilt_objects"`
	RebuiltGoroutines  int     `json:"rebuilt_goroutines"`
	RetireToReleaseMs  float64 `json:"retire_to_release_ms"`
	ResumeToReadyMs    float64 `json:"resume_to_ready_ms"`
	FirstPacketMs      float64 `json:"first_packet_ms"`
	WarmPacketMs       float64 `json:"warm_packet_ms"`
}

func collectLive(t *testing.T) (uint64, uint64, int) {
	t.Helper()
	runtime.GC()
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc, stats.HeapObjects, runtime.NumGoroutine()
}

func medianOf(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	return sorted[len(sorted)/2]
}

// TestRetirementReleaseNextUseCost is the endpoint-level measurement.
func TestRetirementReleaseNextUseCost(t *testing.T) {
	if os.Getenv("JJB_W4_COST") != "1" {
		t.Skip("set JJB_W4_COST=1 to run the W4 tunnel release/next-use measurement")
	}
	requireHTTP3Client(t)
	sampleCount := 5
	if raw := os.Getenv("JJB_W4_SAMPLES"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		require.NoError(t, err)
		sampleCount = parsed
	}
	serverPrefix := netip.MustParsePrefix(connectIPPrefix)
	echoPort := startCarrierEcho(t)
	router := &carrierRouter{}
	_, port := startRoutedConnectIPServer(t, serverPrefix, router)
	clientEndpoint := startConnectIPClient(t, port)
	waitForSessionReady(t, clientEndpoint, 30*time.Second)
	facade := deferredOf(t, clientEndpoint)
	innerDestination := M.SocksaddrFrom(netip.AddrFrom4([4]byte{10, 42, 0, 0}), echoPort)

	// A helper that dials, writes one payload, reads the echo and returns how long the round trip
	// took. It is the same production path carrierRoundTrip uses, but it reports the latency instead
	// of retrying, so a first-packet number is a measurement rather than a best-of-N.
	firstRoundTrip := func(payload string) (time.Duration, error) {
		started := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		conn, err := clientEndpoint.DialContext(ctx, N.NetworkTCP, innerDestination)
		if err != nil {
			return 0, err
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
		if _, err = conn.Write([]byte(payload)); err != nil {
			return 0, err
		}
		echo := make([]byte, len(payload))
		if _, err = io.ReadFull(conn, echo); err != nil {
			return 0, err
		}
		return time.Since(started), nil
	}

	samples := make([]tunnelReleaseCostSample, 0, sampleCount)
	for index := 0; index < sampleCount; index++ {
		// Establish the "built" state with a real flow.
		_, err := firstRoundTrip("warm-up")
		require.NoError(t, err, "the tunnel did not carry the warm-up flow")
		require.True(t, facade.Built())
		builtHeap, builtObjects, builtGoroutines := collectLive(t)

		// Retire. The measurement is from the product call that ends the session to the release.
		retireStarted := time.Now()
		clientEndpoint.SetKeepIdleConnections(false)
		require.Eventually(t, func() bool {
			return facade.Diagnostics().Releases == index+1
		}, 15*time.Second, time.Millisecond, "the retired session did not release the stack")
		retireToRelease := time.Since(retireStarted)
		releasedHeap, releasedObjects, releasedGoroutines := collectLive(t)

		// A new session, then the first business byte through the rebuilt stack.
		resumeStarted := time.Now()
		clientEndpoint.SetKeepIdleConnections(true)
		waitForSessionReady(t, clientEndpoint, 60*time.Second)
		resumeToReady := time.Since(resumeStarted)
		firstPacket, err := firstRoundTrip("first")
		require.NoError(t, err, "the first packet after the rebuild did not cross")
		require.True(t, facade.Built(), "the first packet after the rebuild did not build the stack")
		rebuiltHeap, rebuiltObjects, rebuiltGoroutines := collectLive(t)
		warmPacket, err := firstRoundTrip("warm")
		require.NoError(t, err, "the warm packet after the rebuild did not cross")

		sample := tunnelReleaseCostSample{
			Sample:             index,
			BuiltHeap:          builtHeap,
			BuiltObjects:       builtObjects,
			BuiltGoroutines:    builtGoroutines,
			ReleasedHeap:       releasedHeap,
			ReleasedObjects:    releasedObjects,
			ReleasedGoroutines: releasedGoroutines,
			RebuiltHeap:        rebuiltHeap,
			RebuiltObjects:     rebuiltObjects,
			RebuiltGoroutines:  rebuiltGoroutines,
			RetireToReleaseMs:  float64(retireToRelease.Microseconds()) / 1000,
			ResumeToReadyMs:    float64(resumeToReady.Microseconds()) / 1000,
			FirstPacketMs:      float64(firstPacket.Microseconds()) / 1000,
			WarmPacketMs:       float64(warmPacket.Microseconds()) / 1000,
		}
		samples = append(samples, sample)
		t.Logf("sample %d built=%d/%d/%d released=%d/%d/%d rebuilt=%d/%d/%d | retire->release=%.2fms resume->ready=%.2fms first-packet=%.2fms warm=%.2fms | facade builds=%d releases=%d",
			sample.Sample,
			sample.BuiltHeap, sample.BuiltObjects, sample.BuiltGoroutines,
			sample.ReleasedHeap, sample.ReleasedObjects, sample.ReleasedGoroutines,
			sample.RebuiltHeap, sample.RebuiltObjects, sample.RebuiltGoroutines,
			sample.RetireToReleaseMs, sample.ResumeToReadyMs, sample.FirstPacketMs, sample.WarmPacketMs,
			facade.Diagnostics().Builds, facade.Diagnostics().Releases)
	}

	var (
		releaseMs, readyMs, firstMs, warmMs          []float64
		heapFreed, objectFreed, goroutineFreed       []float64
		heapRegrown, objectRegrown, goroutineRegrown []float64
	)
	for _, sample := range samples {
		releaseMs = append(releaseMs, sample.RetireToReleaseMs)
		readyMs = append(readyMs, sample.ResumeToReadyMs)
		firstMs = append(firstMs, sample.FirstPacketMs)
		warmMs = append(warmMs, sample.WarmPacketMs)
		heapFreed = append(heapFreed, float64(sample.BuiltHeap)-float64(sample.ReleasedHeap))
		objectFreed = append(objectFreed, float64(sample.BuiltObjects)-float64(sample.ReleasedObjects))
		goroutineFreed = append(goroutineFreed, float64(sample.BuiltGoroutines-sample.ReleasedGoroutines))
		heapRegrown = append(heapRegrown, float64(sample.RebuiltHeap)-float64(sample.ReleasedHeap))
		objectRegrown = append(objectRegrown, float64(sample.RebuiltObjects)-float64(sample.ReleasedObjects))
		goroutineRegrown = append(goroutineRegrown, float64(sample.RebuiltGoroutines-sample.ReleasedGoroutines))
	}
	summary := map[string]any{
		"samples":                     sampleCount,
		"retire_to_release_ms_median": medianOf(releaseMs),
		"resume_to_ready_ms_median":   medianOf(readyMs),
		"first_packet_ms_median":      medianOf(firstMs),
		"warm_packet_ms_median":       medianOf(warmMs),
		"heap_freed_median":           medianOf(heapFreed),
		"objects_freed_median":        medianOf(objectFreed),
		"goroutines_freed_median":     medianOf(goroutineFreed),
		"heap_regrown_median":         medianOf(heapRegrown),
		"objects_regrown_median":      medianOf(objectRegrown),
		"goroutines_regrown_median":   medianOf(goroutineRegrown),
		"note": "the endpoint-level freed/regrown deltas include the tunnel session teardown that " +
			"surrounds the retirement; the stack-only footprint is measured in " +
			"transport/device/release_cost_test.go",
	}
	t.Logf("SUMMARY %v", summary)
	if outPath := os.Getenv("JJB_W4_OUT"); outPath != "" {
		file, err := os.Create(outPath)
		require.NoError(t, err)
		encoder := json.NewEncoder(file)
		encoder.SetIndent("", "  ")
		require.NoError(t, encoder.Encode(map[string]any{"summary": summary, "samples": samples}))
		require.NoError(t, file.Close())
	}
}
