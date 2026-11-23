package device

// The measured cost of the only thing the release does: it closes a built userspace stack, and the
// next demand builds one again.
//
// This file exists because "release after retirement" is only worth wiring if it FREES something. The
// numbers below are the ones the W4 report cites:
//
//	built      the stack exists, its sockets are closed, nothing is flowing
//	released   Deferred.Release returned nil
//	rebuilt    the next demand (a UDP socket) built the next generation
//
// Heap, HeapObjects, goroutines and open file descriptors are sampled per phase after two forced
// collections (attribution only - nothing here is on a product path). The test is gated by
// JJB_W4_COST=1 because it forces two full GCs per phase and runs many samples; it is not a
// regression gate.

import (
	"encoding/json"
	"net"
	"net/netip"
	"os"
	"runtime"
	"sort"
	"strconv"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

type releaseCostSample struct {
	Sample       int     `json:"sample"`
	BuildMs      float64 `json:"build_ms"`
	ReleaseMs    float64 `json:"release_ms"`
	RebuildMs    float64 `json:"rebuild_ms"`
	BuiltHeap    uint64  `json:"built_heap"`
	BuiltObjects uint64  `json:"built_objects"`
	BuiltGorout  int     `json:"built_goroutines"`
	BuiltFDs     int     `json:"built_fds"`
	RelHeap      uint64  `json:"released_heap"`
	RelObjects   uint64  `json:"released_objects"`
	RelGorout    int     `json:"released_goroutines"`
	RelFDs       int     `json:"released_fds"`
	ReHeap       uint64  `json:"rebuilt_heap"`
	ReObjects    uint64  `json:"rebuilt_objects"`
	ReGorout     int     `json:"rebuilt_goroutines"`
	ReFDs        int     `json:"rebuilt_fds"`
}

type liveCounters struct {
	Heap    uint64
	Objects uint64
	Gorout  int
	FDs     int
}

// sampleLiveCounters forces two collections so the value does not include the previous phase's
// floating garbage, then reports what is actually retained.
func sampleLiveCounters() liveCounters {
	runtime.GC()
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return liveCounters{
		Heap:    stats.HeapAlloc,
		Objects: stats.HeapObjects,
		Gorout:  runtime.NumGoroutine(),
		FDs:     openFileDescriptors(),
	}
}

// openFileDescriptors reports the process's open descriptors on a /proc host, and -1 where the
// platform does not expose them cheaply. A -1 is reported as "not measured" rather than as zero.
func openFileDescriptors() int {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return -1
	}
	return len(entries)
}

func medianDuration(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	return sorted[len(sorted)/2]
}

// TestDeferredReleaseAndRebuildCost is the resource and next-use measurement.
func TestDeferredReleaseAndRebuildCost(t *testing.T) {
	if os.Getenv("JJB_W4_COST") != "1" {
		t.Skip("set JJB_W4_COST=1 to run the W4 release/rebuild measurement")
	}
	sampleCount := 20
	if raw := os.Getenv("JJB_W4_SAMPLES"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		require.NoError(t, err)
		sampleCount = parsed
	}
	address := netip.MustParsePrefix("10.42.0.2/32")
	destination := M.SocksaddrFrom(netip.MustParseAddr("10.42.0.1"), 53)
	udpDestination := net.UDPAddrFromAddrPort(netip.AddrPortFrom(netip.MustParseAddr("10.42.0.1"), 53))

	samples := make([]releaseCostSample, 0, sampleCount)
	for index := 0; index < sampleCount; index++ {
		instance := newRealStackDeferred(t, 1280, address)

		started := time.Now()
		packetConn, err := instance.ListenPacket(t.Context(), destination)
		require.NoError(t, err)
		buildDuration := time.Since(started)
		// One datagram makes the stack's own goroutines run once, so "built" is a warm stack rather
		// than one whose first work is still pending.
		_, err = packetConn.WriteTo([]byte{0x00, 0x01}, udpDestination)
		require.NoError(t, err)
		require.NoError(t, packetConn.Close())
		built := sampleLiveCounters()

		started = time.Now()
		require.NoError(t, instance.Release())
		releaseDuration := time.Since(started)
		released := sampleLiveCounters()

		started = time.Now()
		packetConn, err = instance.ListenPacket(t.Context(), destination)
		require.NoError(t, err)
		rebuildDuration := time.Since(started)
		require.NoError(t, packetConn.Close())
		rebuilt := sampleLiveCounters()

		require.NoError(t, instance.Close())
		sample := releaseCostSample{
			Sample:       index,
			BuildMs:      float64(buildDuration.Microseconds()) / 1000,
			ReleaseMs:    float64(releaseDuration.Microseconds()) / 1000,
			RebuildMs:    float64(rebuildDuration.Microseconds()) / 1000,
			BuiltHeap:    built.Heap,
			BuiltObjects: built.Objects,
			BuiltGorout:  built.Gorout,
			BuiltFDs:     built.FDs,
			RelHeap:      released.Heap,
			RelObjects:   released.Objects,
			RelGorout:    released.Gorout,
			RelFDs:       released.FDs,
			ReHeap:       rebuilt.Heap,
			ReObjects:    rebuilt.Objects,
			ReGorout:     rebuilt.Gorout,
			ReFDs:        rebuilt.FDs,
		}
		samples = append(samples, sample)
		t.Logf("sample %2d build=%6.2fms release=%6.2fms rebuild=%6.2fms | built heap=%9d objs=%6d gor=%3d fd=%3d | released heap=%9d objs=%6d gor=%3d fd=%3d | rebuilt heap=%9d objs=%6d gor=%3d fd=%3d",
			sample.Sample, sample.BuildMs, sample.ReleaseMs, sample.RebuildMs,
			sample.BuiltHeap, sample.BuiltObjects, sample.BuiltGorout, sample.BuiltFDs,
			sample.RelHeap, sample.RelObjects, sample.RelGorout, sample.RelFDs,
			sample.ReHeap, sample.ReObjects, sample.ReGorout, sample.ReFDs)
	}

	// Medians over the samples, and the per-sample delta distribution, which is what the claim needs:
	// the difference between "a stack exists" and "it does not" is a paired measurement, so its median
	// is reported per sample rather than as a difference of medians.
	var (
		builds, releases, rebuilds                              []float64
		heapDrops, objectDrops, goroutineDrops, fdDrops         []float64
		heapRegrows, objectRegrows, goroutineRegrows, fdRegrows []float64
	)
	for _, sample := range samples {
		builds = append(builds, sample.BuildMs)
		releases = append(releases, sample.ReleaseMs)
		rebuilds = append(rebuilds, sample.RebuildMs)
		heapDrops = append(heapDrops, float64(sample.BuiltHeap)-float64(sample.RelHeap))
		objectDrops = append(objectDrops, float64(sample.BuiltObjects)-float64(sample.RelObjects))
		goroutineDrops = append(goroutineDrops, float64(sample.BuiltGorout-sample.RelGorout))
		fdDrops = append(fdDrops, float64(sample.BuiltFDs-sample.RelFDs))
		heapRegrows = append(heapRegrows, float64(sample.ReHeap)-float64(sample.RelHeap))
		objectRegrows = append(objectRegrows, float64(sample.ReObjects)-float64(sample.RelObjects))
		goroutineRegrows = append(goroutineRegrows, float64(sample.ReGorout-sample.RelGorout))
		fdRegrows = append(fdRegrows, float64(sample.ReFDs-sample.RelFDs))
	}
	summary := map[string]any{
		"samples":                 sampleCount,
		"build_ms_median":         medianDuration(builds),
		"release_ms_median":       medianDuration(releases),
		"rebuild_ms_median":       medianDuration(rebuilds),
		"heap_drop_median":        medianDuration(heapDrops),
		"object_drop_median":      medianDuration(objectDrops),
		"goroutine_drop_median":   medianDuration(goroutineDrops),
		"fd_drop_median":          medianDuration(fdDrops),
		"heap_regrow_median":      medianDuration(heapRegrows),
		"object_regrow_median":    medianDuration(objectRegrows),
		"goroutine_regrow_median": medianDuration(goroutineRegrows),
		"fd_regrow_median":        medianDuration(fdRegrows),
	}
	// Amortized next-use cost.
	//
	// A single release/build on this host is often BELOW the platform clock's resolution (the
	// per-sample columns above land on 0.00 or ~0.5 ms), so the single event time is not a number this
	// machine can state. Averaging a long loop removes that: each iteration pays exactly one release
	// and one build, so the mean is the per-cycle cost the product pays after every retirement.
	cycles := 200
	if raw := os.Getenv("JJB_W4_CYCLES"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		require.NoError(t, err)
		cycles = parsed
	}
	amortized := newRealStackDeferred(t, 1280, address)
	cycleStart := time.Now()
	for index := 0; index < cycles; index++ {
		packetConn, err := amortized.ListenPacket(t.Context(), destination)
		require.NoError(t, err)
		_, err = packetConn.WriteTo([]byte{0x00, 0x01}, udpDestination)
		require.NoError(t, err)
		require.NoError(t, packetConn.Close())
		require.NoError(t, amortized.Release())
	}
	cycleDuration := time.Since(cycleStart)
	require.NoError(t, amortized.Close())
	perCycle := float64(cycleDuration.Nanoseconds()) / float64(cycles)
	summary["cycles"] = cycles
	summary["per_cycle_us_mean"] = perCycle / 1000
	t.Logf("amortized over %d cycles: %.1f us per (build + first datagram + release)", cycles, perCycle/1000)
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
