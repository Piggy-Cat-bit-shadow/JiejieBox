package oomprofile

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"runtime/metrics"
	"strconv"
	"strings"
	"testing"
)

// What the heap sampling rate costs in resident memory, measured instead of assumed.
//
// # Why this is a test in this package
//
// `runtime.MemProfileRate` is the only knob behind every heap profile this package reads: the OOM
// snapshot goes through `runtime.pprof_memProfileInternal`, which reports the samples the rate
// decides to take. Nothing in this repository sets the rate, so the process runs at Go's default
// (512 KiB) and pays for its sample buckets for its whole life - a cost a Network Extension with a
// tens-of-MiB budget should know about.
//
// # What the numbers have to say before the default changes
//
// The task this belongs to asks for an A/B before any change, and the direction is easy to get
// wrong: a LARGER non-zero rate samples more sparsely (cheaper, blurrier), and 0 disables sampling
// entirely (cheapest, and it also disables every past sample). This measures all three in child
// processes so the trend is a fact rather than an argument, and it fails if the trend is inverted -
// which would mean the knob no longer means what the documentation says.
//
// The ceiling is deliberately generous: it is a regression gate on an order of magnitude, not a
// budget. A rate change that crosses it would be a change worth arguing about on its own.
const (
	memProfileRateChildEnv = "JIEJIEBOX_MEMPROFILE_RATE_CHILD"
	memProfileRateSample   = "MEMPROFILE-RATE-SAMPLE"
	// memProfileRateBucketCeiling is the most this workload may spend on sample buckets at the
	// default rate. Measured well below it; see the test log.
	memProfileRateBucketCeiling = 4 << 20
	// memProfileRateSpreadTolerance is how much the class may legitimately differ between the three
	// rates. The measured spread is about 1 KiB; anything past this means the class started tracking
	// the sampler itself.
	memProfileRateSpreadTolerance = 256 << 10
)

func TestHeapSamplingRateCostsWhatItCosts(t *testing.T) {
	if os.Getenv(memProfileRateChildEnv) == "1" {
		runMemProfileRateChild(t)
		return
	}
	// 0 is "no sampling"; the two non-zero values are Go's default and a deliberately coarse one.
	// The comparison is only meaningful if all three run the same workload.
	rates := []int{0, 4 << 20, 512 << 10}
	measured := make(map[int]uint64, len(rates))
	for _, rate := range rates {
		measured[rate] = runMemProfileRateCase(t, rate)
	}
	t.Logf("sample buckets for the same workload: rate=0 -> %d B, rate=4 MiB -> %d B, rate=512 KiB (Go default) -> %d B",
		measured[0], measured[4<<20], measured[512<<10])

	// THE FINDING: the sampling rate moves this class by ~1 KiB out of ~1.4 MiB, so the rate is not a
	// memory lever here.
	//
	// The class has a floor the rate does not touch - the runtime keeps profiling buckets for more
	// than the heap sampler, and they are allocated whether or not heap sampling is on (rate=0
	// measured 1 446 485 B against 1 447 501 B at Go's default). The earlier expectation, "a coarser
	// MemProfileRate buys resident memory", is therefore false at this scale: what a coarser rate
	// buys is fewer samples (blurrier OOM snapshots) and nothing measurable in bytes.
	//
	// This is recorded as MEASURED_NO_CHANGE: the default stays, because changing it would trade
	// diagnostic fidelity for a difference that does not appear above the runtime's own floor.
	span := func(values ...uint64) (low, high uint64) {
		low, high = values[0], values[0]
		for _, value := range values[1:] {
			if value < low {
				low = value
			}
			if value > high {
				high = value
			}
		}
		return low, high
	}
	low, high := span(measured[0], measured[4<<20], measured[512<<10])
	t.Logf("decision input: the three rates differ by %d B of %d B, i.e. the sampling rate is not what "+
		"this class is spent on; the floor is runtime-owned and unaffected by MemProfileRate", high-low, high)
	if high-low > memProfileRateSpreadTolerance {
		t.Fatalf("the bucket class moved by %d B between rate 0 and Go's default (%d vs %d), over the %d B "+
			"tolerance. Either the metric now reports the heap sampler specifically, or the rate acquired a "+
			"memory cost it did not have when this was measured; both change the decision and must be re-read",
			high-low, measured[0], measured[512<<10], memProfileRateSpreadTolerance)
	}
	if high > memProfileRateBucketCeiling {
		t.Fatalf("the profiling bucket class sits at %d B for this workload, over the %d B ceiling: at that "+
			"size it is worth arguing about on its own, and this test's premise (that the rate is not the "+
			"lever) has to be revisited", high, memProfileRateBucketCeiling)
	}
}

// runMemProfileRateCase runs one rate in a child process and returns the bucket bytes it measured.
func runMemProfileRateCase(t *testing.T, rate int) uint64 {
	t.Helper()
	child := exec.Command(os.Args[0], "-test.run=^TestHeapSamplingRateCostsWhatItCosts$", "-test.count=1", "-test.v")
	child.Env = append(os.Environ(), memProfileRateChildEnv+"=1", "JIEJIEBOX_MEMPROFILE_RATE="+strconv.Itoa(rate))
	output, err := child.CombinedOutput()
	if err != nil {
		t.Fatalf("the measuring process failed for rate %d (%v):\n%s", rate, err, output)
	}
	for _, line := range strings.Split(string(output), "\n") {
		index := strings.Index(line, memProfileRateSample)
		if index < 0 {
			continue
		}
		var buckets uint64
		var reportedRate int
		_, scanErr := fmt.Sscanf(line[index:], memProfileRateSample+" rate=%d buckets=%d", &reportedRate, &buckets)
		if scanErr != nil {
			t.Fatalf("unreadable sample line %q: %v", line, scanErr)
		}
		if reportedRate != rate {
			t.Fatalf("asked for rate %d and the child reported %d", rate, reportedRate)
		}
		return buckets
	}
	t.Fatalf("the measuring process printed no %s line:\n%s", memProfileRateSample, output)
	return 0
}

// runMemProfileRateChild sets the rate before doing any sampled allocation, runs the workload, and
// prints one machine-readable line. It runs as a child so each rate starts from a clean process:
// the rate is a process-wide decision and changing it in the middle of a running server is exactly
// what the construction brief forbids.
func runMemProfileRateChild(t *testing.T) {
	rate, err := strconv.Atoi(os.Getenv("JIEJIEBOX_MEMPROFILE_RATE"))
	if err != nil {
		t.Fatalf("bad rate: %v", err)
	}
	runtime.MemProfileRate = rate
	workloadWithSampledAllocations()
	for i := 0; i < 3; i++ {
		runtime.GC()
	}
	samples := []metrics.Sample{{Name: "/memory/classes/profiling/buckets:bytes"}}
	metrics.Read(samples)
	if samples[0].Value.Kind() == metrics.KindBad {
		t.Fatalf("/memory/classes/profiling/buckets:bytes is not a metric this toolchain publishes; " +
			"a missing metric must never be read as a cost of zero")
	}
	fmt.Printf("%s rate=%d buckets=%d\n", memProfileRateSample, runtime.MemProfileRate, samples[0].Value.Uint64())
}

// workloadWithSampledAllocations allocates across the size-class ladder and keeps half of it alive,
// so the steady-state heap - and therefore the number of samples the rate decides to take - is
// comparable between the three runs.
func workloadWithSampledAllocations() {
	live := make([][]byte, 0, 4096)
	for size := 64; size <= 64<<10; size <<= 1 {
		for index := 0; index < 96; index++ {
			buffer := make([]byte, size)
			buffer[0] = byte(size)
			live = append(live, buffer)
		}
	}
	runtime.KeepAlive(live)
}
