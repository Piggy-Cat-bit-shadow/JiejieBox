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

// What the heap sampling rate costs in runtime-managed memory, measured from process start.
//
// # Why this is a test in this package
//
// `runtime.MemProfileRate` is the only knob behind every heap profile this package reads: the OOM
// snapshot goes through `runtime.pprof_memProfileInternal`, which reports the samples the rate
// decides to take. Nothing in this repository sets the rate, so the process runs at Go's default
// (512 KiB) and pays for its sample buckets for its whole life - a cost a Network Extension with a
// tens-of-MiB budget should know about.
//
// # Why the rate has to be set by the child's startup environment
//
// `runtime.MemProfileRate` is a process-wide decision the runtime reads ONCE, while the process
// starts: package `runtime` parses `GODEBUG=memprofilerate=<n>` (runtime/parsedebugvars ->
// `MemProfileRate = n`). Assigning the variable later cannot undo what already happened. The first
// memory sample allocates the profiler's bucket hash table - a fixed 179,999-pointer array
// (`buckhashArray`) carved with `sysAlloc` in `runtime.stkbucket`, charged to
// `memstats.buckhash_sys`, never freed - and `/memory/classes/profiling/buckets:bytes` reports
// exactly that stat.
//
// An earlier version of this file set `runtime.MemProfileRate` inside an already-running child and
// then read the class. That measured the startup residue: every child had already sampled at 512 KiB
// before the assignment, so all three rates reported the same ~1.45 MiB table and the file concluded
// the class was a "floor the rate does not touch". Starting the same children with
// `GODEBUG=memprofilerate=0` showed the opposite: with sampling off from process start the class is a
// few KiB. The child's environment is therefore the measuring instrument, and the number it reports
// is only attributable to a rate if the child observed that rate at startup.
//
// # What the numbers say, and what they do not
//
// Measured 2026-11-21, go1.27.4, windows/amd64, same workload in every run:
//
//	rate 0        : ~5 KiB, constant - no sample is ever taken, so the table is never allocated
//	rate 4 MiB    : ~1,444,000 B - table plus a few hundred bytes of bucket entries
//	rate 512 KiB  : ~1,446,000 B - table plus about a kilobyte more of bucket entries
//
// So the two claims separate:
//
//   - Turning sampling OFF at process start avoids the whole fixed table (about 1.4 MiB here).
//   - Making the rate COARSER does not: the table is allocated by the first sample whatever the
//     rate is, and entry growth is bounded by the number of distinct (call stack, size) pairs the
//     workload touches, not by how many samples were taken. A coarser rate buys blurrier profiles,
//     not bytes.
//
// The two gates below encode exactly those two facts. They deliberately do NOT gate "the three rates
// must be within a small spread" - that was the earlier file's premise, and it turned a measurement
// artefact into an argument that the rate had no benefit at all.
//
// # Scope of the number
//
// `/memory/classes/profiling/buckets:bytes` is a Go runtime memory class: it is the runtime's own
// accounting of the hash table it reserved, not RSS and not iOS `phys_footprint`. The table is
// `sysAlloc`ed and only the pages actually hashed into are touched, so the resident cost is at most
// this number and can be smaller. This file makes no device claim.
//
// The measurement tool's own cost: one `[]metrics.Sample` per read (three reads per child), about a
// hundred bytes each, allocated after the startup read has been taken. It is far below the spread
// this file resolves and it does not create sample buckets of its own - `metrics.Read` reads memstats
// under its lock and never enters the profiler.
const (
	memProfileRateChildEnv  = "JIEJIEBOX_MEMPROFILE_RATE_CHILD"
	memProfileRateChildRate = "JIEJIEBOX_MEMPROFILE_RATE"
	memProfileRateSample    = "MEMPROFILE-RATE-SAMPLE"
	memProfileRateDebugKey  = "memprofilerate"

	memProfileRateBucketMetric = "/memory/classes/profiling/buckets:bytes"
	memProfileRateHeapMetric   = "/memory/classes/heap/objects:bytes"

	// memProfileRateChildRuns is how many independent child processes each rate gets. Sampling is
	// randomized, so one run per rate would report noise as a finding.
	memProfileRateChildRuns = 3
	// memProfileRateFixedTableFloor is the smallest gap between "sampling disabled from process
	// start" and "sampling enabled at any rate" that the profiler's fixed hash table can explain.
	// Measured at about 1.4 MiB; 1 MiB leaves room for the runtime to shrink the table without
	// silently turning this gate off, while still failing if the table stops being sampling-gated.
	memProfileRateFixedTableFloor = 1 << 20
	// memProfileRateEntryBudget is how far apart two ENABLED rates may sit. Bucket entries are
	// permanent and grow with distinct stacks and sizes, so the enabled rates may differ by that
	// growth; they may not differ by the table.
	memProfileRateEntryBudget = 256 << 10
	// memProfileRateChurnTotal is the allocation volume the released group pushes through. It is
	// sized so that even the coarse 4 MiB rate is expected to sample ~16 times; at ~8 expected
	// samples one child in ten thousand would take no sample at all and report the table as absent.
	memProfileRateChurnTotal = 64 << 20
)

// memProfileRateRun is one child process's three readings of the bucket class.
type memProfileRateRun struct {
	// startup is read before this test allocates any workload: everything the process did while
	// starting (runtime init, package init, the test framework) is already in it.
	startup uint64
	// retained is read after the live group was allocated and kept reachable.
	retained uint64
	// released is read after the churn group was allocated, dropped and collected. The live group
	// is still reachable at this point; see runMemProfileRateChild.
	released uint64
	// heap is the live heap at the retained reading, reported so a run whose live group did not
	// actually stay alive is visible instead of being read as a sampling result.
	heap uint64
}

func TestHeapSamplingRateCostsWhatItCosts(t *testing.T) {
	if os.Getenv(memProfileRateChildEnv) == "1" {
		runMemProfileRateChild(t)
		return
	}
	// 0 is "no sampling"; the two non-zero values are Go's default and a deliberately coarse one.
	// All three must run the same workload from process start for the comparison to mean anything.
	rates := []int{0, 4 << 20, 512 << 10}
	measured := make(map[int][]memProfileRateRun, len(rates))
	for _, rate := range rates {
		for run := 0; run < memProfileRateChildRuns; run++ {
			measured[rate] = append(measured[rate], runMemProfileRateCase(t, rate))
		}
	}
	t.Logf("the bucket class per rate and run (bytes):\n%s", formatMemProfileRateTable(rates, measured))

	// Gate 1: enabling sampling costs a fixed hash table; disabling it avoids the table entirely.
	// This is the fact the earlier version of this file got backwards.
	disabledCeiling := maxMemProfileRateReading(measured[0], func(run memProfileRateRun) uint64 { return run.released })
	enabledFloor := ^uint64(0)
	for _, rate := range []int{4 << 20, 512 << 10} {
		enabledFloor = minUint64(enabledFloor, minMemProfileRateReading(measured[rate], func(run memProfileRateRun) uint64 { return run.retained }))
	}
	t.Logf("fixed table: sampling disabled peaks at %d B, sampling enabled floors at %d B (gap %d B)",
		disabledCeiling, enabledFloor, enabledFloor-disabledCeiling)
	if enabledFloor < disabledCeiling+memProfileRateFixedTableFloor {
		t.Fatalf("sampling disabled from process start measured %d B and sampling enabled measured as little as %d B: "+
			"a gap of only %d B, under the %d B the profiler's fixed hash table should cost. Either the runtime stopped "+
			"allocating that table at the first sample, or this class stopped reporting it (runtime/mprof.go stkbucket "+
			"charges it to memstats.buckhash_sys, which is what /memory/classes/profiling/buckets:bytes returns). "+
			"Both change the trade-off this file exists to record, so re-read it before trusting either number",
			disabledCeiling, enabledFloor, enabledFloor-disabledCeiling, memProfileRateFixedTableFloor)
	}

	// Gate 2: the rate's VALUE does not move the class once sampling is on. Entry growth is bounded
	// by distinct (stack, size) pairs, so a coarser rate cannot win back the table it already paid
	// for. This is not "no benefit from sampling at all" - gate 1 is the benefit.
	enabledLow, enabledHigh := ^uint64(0), uint64(0)
	for _, rate := range []int{4 << 20, 512 << 10} {
		enabledLow = minUint64(enabledLow, minMemProfileRateReading(measured[rate], func(run memProfileRateRun) uint64 { return run.retained }))
		enabledHigh = maxUint64(enabledHigh, maxMemProfileRateReading(measured[rate], func(run memProfileRateRun) uint64 { return run.retained }))
	}
	t.Logf("entry growth across enabled rates: %d B of %d B, i.e. the class is spent on the table the first sample "+
		"allocates, not on how densely the rate samples", enabledHigh-enabledLow, enabledHigh)
	if enabledHigh-enabledLow > memProfileRateEntryBudget {
		t.Fatalf("the two enabled rates landed %d B apart (%d B and %d B), over the %d B entry budget: the class has "+
			"started tracking how densely the sampler samples rather than which (stack, size) buckets exist. That would "+
			"make a coarser rate a memory lever after all, so the conclusion this file records has to be re-read",
			enabledHigh-enabledLow, enabledLow, enabledHigh, memProfileRateEntryBudget)
	}

	// Gate 3: buckets are permanent. A class that shrank between two readings inside one process
	// would mean the runtime frees them, and every budget built on "this only grows" would be wrong.
	for _, rate := range rates {
		for index, run := range measured[rate] {
			if !(run.startup <= run.retained && run.retained <= run.released) {
				t.Fatalf("rate %d run %d: the bucket class fell from %d B to %d B to %d B. Buckets are allocated by "+
					"persistentalloc and never released, so a decrease means this metric no longer reports them",
					rate, index, run.startup, run.retained, run.released)
			}
		}
	}
}

// formatMemProfileRateTable renders the readings so a reader can separate the startup residue from
// the workload's own entry growth without re-running anything.
func formatMemProfileRateTable(rates []int, measured map[int][]memProfileRateRun) string {
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("%10s %4s %12s %12s %12s %12s\n", "rate", "run", "startup", "retained", "released", "liveheap"))
	for _, rate := range rates {
		for index, run := range measured[rate] {
			builder.WriteString(fmt.Sprintf("%10d %4d %12d %12d %12d %12d\n",
				rate, index, run.startup, run.retained, run.released, run.heap))
		}
	}
	return builder.String()
}

func minMemProfileRateReading(runs []memProfileRateRun, value func(memProfileRateRun) uint64) uint64 {
	result := ^uint64(0)
	for _, run := range runs {
		result = minUint64(result, value(run))
	}
	return result
}

func maxMemProfileRateReading(runs []memProfileRateRun, value func(memProfileRateRun) uint64) uint64 {
	var result uint64
	for _, run := range runs {
		result = maxUint64(result, value(run))
	}
	return result
}

func minUint64(left, right uint64) uint64 {
	if left < right {
		return left
	}
	return right
}

func maxUint64(left, right uint64) uint64 {
	if left > right {
		return left
	}
	return right
}

// runMemProfileRateCase starts one child at the given rate and returns what it read.
func runMemProfileRateCase(t *testing.T, rate int) memProfileRateRun {
	t.Helper()
	child := exec.Command(os.Args[0], "-test.run=^TestHeapSamplingRateCostsWhatItCosts$", "-test.count=1", "-test.v")
	child.Env = memProfileRateChildEnvironment(rate)
	output, err := child.CombinedOutput()
	if err != nil {
		t.Fatalf("the measuring process failed for rate %d (%v):\n%s", rate, err, output)
	}
	for _, line := range strings.Split(string(output), "\n") {
		index := strings.Index(line, memProfileRateSample)
		if index < 0 {
			continue
		}
		var run memProfileRateRun
		var reportedRate int
		_, scanErr := fmt.Sscanf(line[index:], memProfileRateSample+" rate=%d startup=%d retained=%d released=%d heap=%d",
			&reportedRate, &run.startup, &run.retained, &run.released, &run.heap)
		if scanErr != nil {
			t.Fatalf("unreadable sample line %q: %v", line, scanErr)
		}
		if reportedRate != rate {
			t.Fatalf("asked for rate %d and the child reported %d", rate, reportedRate)
		}
		return run
	}
	t.Fatalf("the measuring process printed no %s line:\n%s", memProfileRateSample, output)
	return memProfileRateRun{}
}

// memProfileRateChildEnvironment is the child's whole environment: the inherited one, minus its
// GODEBUG, plus one that carries this child's target rate. Replacing rather than appending keeps the
// test reproducible when the parent itself runs with GODEBUG=memprofilerate=0 for a manual A/B.
func memProfileRateChildEnvironment(rate int) []string {
	inherited := os.Environ()
	environment := make([]string, 0, len(inherited)+3)
	for _, entry := range inherited {
		if strings.HasPrefix(entry, "GODEBUG=") {
			continue
		}
		environment = append(environment, entry)
	}
	return append(environment,
		"GODEBUG="+mergeMemProfileRateDebug(os.Getenv("GODEBUG"), rate),
		memProfileRateChildEnv+"=1",
		memProfileRateChildRate+"="+strconv.Itoa(rate),
	)
}

// mergeMemProfileRateDebug returns a startup GODEBUG with the sampling rate replaced and every other
// debug setting kept. The runtime reads this variable once, before any package initializer runs, so
// this string is the only way to make a child start at a chosen rate.
func mergeMemProfileRateDebug(inherited string, rate int) string {
	settings := make([]string, 0, 4)
	for _, setting := range strings.Split(inherited, ",") {
		if setting == "" {
			continue
		}
		name, _, _ := strings.Cut(setting, "=")
		if name == memProfileRateDebugKey {
			continue
		}
		settings = append(settings, setting)
	}
	return strings.Join(append(settings, memProfileRateDebugKey+"="+strconv.Itoa(rate)), ",")
}

// TestMergeMemProfileRateDebug pins the environment merge without starting a process. A merge that
// silently kept an inherited rate, or dropped an unrelated debug setting, would mislabel every
// number this package reports.
func TestMergeMemProfileRateDebug(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		inherited string
		rate      int
		expected  string
	}{
		{"empty", "", 0, "memprofilerate=0"},
		{"onlyWhitespace", ",,", 524288, "memprofilerate=524288"},
		{"keepsOtherSettings", "asyncpreemptoff=1,gcstoptheworld=2", 4194304, "asyncpreemptoff=1,gcstoptheworld=2,memprofilerate=4194304"},
		{"replacesInheritedRate", "memprofilerate=0,asyncpreemptoff=1", 524288, "asyncpreemptoff=1,memprofilerate=524288"},
		{"replacesEveryDuplicate", "memprofilerate=0,memprofilerate=1", 524288, "memprofilerate=524288"},
		{"keepsLongerName", "memprofileratex=1", 524288, "memprofileratex=1,memprofilerate=524288"},
		{"keepsValueWithEquals", "x=1=2", 0, "x=1=2,memprofilerate=0"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			actual := mergeMemProfileRateDebug(testCase.inherited, testCase.rate)
			if actual != testCase.expected {
				t.Fatalf("merge(%q, %d) = %q, want %q", testCase.inherited, testCase.rate, actual, testCase.expected)
			}
		})
	}
}

// runMemProfileRateChild is the measuring process. It never sets the rate: the rate arrived with its
// environment, and the first thing it does is prove that it did.
func runMemProfileRateChild(t *testing.T) {
	requested, err := strconv.Atoi(os.Getenv(memProfileRateChildRate))
	if err != nil {
		t.Fatalf("bad rate: %v", err)
	}
	if runtime.MemProfileRate != requested {
		t.Fatalf("child started with runtime.MemProfileRate=%d, want %d: the startup GODEBUG did not take effect "+
			"(a linker-disabled memory profiler also forces 0). Every reading below would be attributed to the "+
			"wrong rate, so this run is void rather than mislabelled", runtime.MemProfileRate, requested)
	}

	for index := 0; index < 2; index++ {
		runtime.GC()
	}
	startup := memProfileRateBucketBytes(t)

	// The live group: reachable until after the last reading, so the retained measurement is taken
	// with it alive rather than after the compiler or a collection took it away.
	live := memProfileRateRetainLiveSet()
	for index := 0; index < 2; index++ {
		runtime.GC()
	}
	retained := memProfileRateBucketBytes(t)
	heap := memProfileRateHeapObjects(t)

	// The released group: allocated through the same sites and dropped immediately. Its buckets stay
	// in the class after the bytes are gone, which is the fact that makes entries monotonic.
	memProfileRateChurn(memProfileRateChurnTotal)
	for index := 0; index < 3; index++ {
		runtime.GC()
	}
	released := memProfileRateBucketBytes(t)

	fmt.Printf("%s rate=%d startup=%d retained=%d released=%d heap=%d\n",
		memProfileRateSample, runtime.MemProfileRate, startup, retained, released, heap)
	runtime.KeepAlive(live)
}

// memProfileRateBucketBytes reads the class this file measures, and fails rather than returning 0
// when the toolchain does not publish it: a missing metric is not a cost of zero.
func memProfileRateBucketBytes(t *testing.T) uint64 {
	t.Helper()
	samples := []metrics.Sample{{Name: memProfileRateBucketMetric}}
	metrics.Read(samples)
	if samples[0].Value.Kind() == metrics.KindBad {
		t.Fatalf("%s is not a metric this toolchain publishes; a missing metric must never be read as a cost of zero",
			memProfileRateBucketMetric)
	}
	return samples[0].Value.Uint64()
}

// memProfileRateHeapObjects reads the live heap so a run whose live group did not stay alive is
// visible in the table instead of being mistaken for a sampling result.
func memProfileRateHeapObjects(t *testing.T) uint64 {
	t.Helper()
	samples := []metrics.Sample{{Name: memProfileRateHeapMetric}}
	metrics.Read(samples)
	if samples[0].Value.Kind() == metrics.KindBad {
		t.Fatalf("%s is not a metric this toolchain publishes; the live-heap column would silently read 0",
			memProfileRateHeapMetric)
		return 0
	}
	return samples[0].Value.Uint64()
}

// memProfileRateRetainLiveSet allocates across the size-class ladder through four distinct call
// sites, so the table ends up with several (stack, size) buckets rather than one.
func memProfileRateRetainLiveSet() [][]byte {
	live := make([][]byte, 0, len(memProfileRateRetainSites)*12)
	for size := 64; size <= 64<<10; size <<= 1 {
		for _, site := range memProfileRateRetainSites {
			buffer := site(size)
			buffer[0] = byte(size)
			live = append(live, buffer)
		}
	}
	return live
}

// memProfileRateRetainSites keeps the allocation sites distinct: one function with a switch on the
// site argument would still be one stack, so each site is its own function. //go:noinline stops the
// compiler from folding them into the caller and collapsing the four back into one frame.
var memProfileRateRetainSites = [...]func(int) []byte{
	memProfileRateRetainA,
	memProfileRateRetainB,
	memProfileRateRetainC,
	memProfileRateRetainD,
}

//go:noinline
func memProfileRateRetainA(size int) []byte { return make([]byte, size) }

//go:noinline
func memProfileRateRetainB(size int) []byte { return make([]byte, size) }

//go:noinline
func memProfileRateRetainC(size int) []byte { return make([]byte, size) }

//go:noinline
func memProfileRateRetainD(size int) []byte { return make([]byte, size) }

// memProfileRateChurn allocates total bytes through four call sites and releases each block at once,
// so the released group produces samples without the live heap growing.
func memProfileRateChurn(total int) {
	const block = 512 << 10
	for allocated := 0; allocated < total; allocated += block {
		site := memProfileRateChurnSites[(allocated/block)%len(memProfileRateChurnSites)]
		buffer := site(block)
		buffer[0] = byte(allocated)
		runtime.KeepAlive(buffer)
	}
}

var memProfileRateChurnSites = [...]func(int) []byte{
	memProfileRateChurnA,
	memProfileRateChurnB,
	memProfileRateChurnC,
	memProfileRateChurnD,
}

//go:noinline
func memProfileRateChurnA(size int) []byte { return make([]byte, size) }

//go:noinline
func memProfileRateChurnB(size int) []byte { return make([]byte, size) }

//go:noinline
func memProfileRateChurnC(size int) []byte { return make([]byte, size) }

//go:noinline
func memProfileRateChurnD(size int) []byte { return make([]byte, size) }
