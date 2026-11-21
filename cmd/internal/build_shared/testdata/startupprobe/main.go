// Command startupprobe measures the RUNTIME value of the startup defaults a build linked, in a
// fresh process, so "the string is in the artifact" and "the process runs with it" are two separate
// claims with two separate pieces of evidence.
//
// It is built as its own module by startup_policy_probe_test.go and is also the runner the GM-2
// evidence run uses, so the numbers in the report and the numbers in the test come from the same
// program. It lives under testdata/ so the go tool never treats it as part of any package.
//
// # What it can and cannot prove
//
//   - -mode rate reads runtime.MemProfileRate directly, which is the value the allocator uses
//     after runtime.parseRuntimeDebugVars has applied the linked default AND the environment (the
//     environment wins). It also reads /memory/classes/profiling/buckets:bytes, the 179999-entry
//     stack-trace hash table runtime.stkbucket sysAllocs on the first sampled allocation
//     (179999 * 8 = 1439992 bytes), and the heap profile's sample count, which is what a diagnostic
//     consumer would actually get.
//   - -mode stack measures ACTUAL stack bytes (/memory/classes/heap/stacks:bytes) in the cold-G and
//     reused-G paths, plus /gc/stack/starting-size:bytes, goroutine-created count, wall time and
//     CPU seconds. A cold goroutine is created with malg(stackMin) regardless of the adaptive
//     setting; only a REUSED g gets stackalloc(startingStackSize), so the reuse path is where the
//     setting can show up.
//   - It does not measure device phys_footprint, RSS, or first-packet/tail latency. Those need the
//     product path, a network, and a device; saying otherwise would turn a Go heap class into a
//     product claim.
//
// The probe disables automatic GC (-GC percent) and triggers every GC explicitly, because
// `startingStackSize` is recomputed at EVERY GC from that cycle's scanned stacks: an automatic GC
// between the training phase and the reused-G wave would reset the value under test. Memory
// numbers here are therefore NOT a product steady state; they are a controlled runtime experiment.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// deepFrameSize is roughly the bytes one deepFrame level consumes: 768 bytes of pad plus the frame
// overhead of two arguments and a saved return address. The exact number is not important; what
// matters is that the total (depth * deepFrameSize) is far from any power-of-two boundary the
// runtime rounds startingStackSize to, so the trained value and the prime goroutines' allocated
// stacks do not coincide accidentally.
const deepFrameSize = 768

var sink atomic.Uint64

// warmup is allocated during PACKAGE INITIALIZATION, before main, when the runner sets
// PROBE_WARMUP_MIB. It exists to make one question deterministic: on a process that has already
// allocated a few MiB when main starts - which is what a libbox with its pprof, crypto, net and
// JSON initialization does - is the profiler bucket table already there? Sixteen MiB against a
// 512 KiB sampling rate makes "no sample yet" a ~1e-14 event, so the reading at main is a fact
// rather than a coin flip.
var warmup = warmupAllocation()

func warmupAllocation() []byte {
	value := strings.TrimSpace(os.Getenv("PROBE_WARMUP_MIB"))
	if value == "" {
		return nil
	}
	mib, err := strconv.Atoi(value)
	if err != nil || mib <= 0 {
		fail("PROBE_WARMUP_MIB=%q is not a positive integer", value)
	}
	buffer := make([]byte, mib*1024*1024)
	for offset := 0; offset < len(buffer); offset += 4096 {
		buffer[offset] = 1
	}
	return buffer
}

//go:noinline
func deepFrame(depth int) {
	var pad [deepFrameSize]byte
	pad[depth&(deepFrameSize-1)] = byte(depth)
	sink.Add(uint64(pad[0]))
	if depth > 0 {
		deepFrame(depth - 1)
		sink.Add(uint64(pad[1]))
	}
}

// deepBlock recurses to depth and then parks there, so the deep frames are LIVE while the goroutine
// is parked. Scanned stack size is measured from the stack pointer to the stack top
// (runtime.mgcmark.go: scannedSize := gp.stack.hi - sp), so a goroutine that recursed and RETURNED
// would be scanned as a shallow stack and would not train the adaptive starting size.
//
// ready is released from the parking point and NOT at goroutine entry: a barrier that fires before
// the recursion would let the main goroutine read the wave while every other goroutine is still
// sitting on its initial stack, which is the opposite of what a deep-stack measurement means.
func deepBlock(depth int, release <-chan struct{}, ready *sync.WaitGroup) {
	var pad [deepFrameSize]byte
	pad[depth&(deepFrameSize-1)] = byte(depth)
	if depth > 0 {
		deepBlock(depth-1, release, ready)
		sink.Add(uint64(pad[0]))
		return
	}
	ready.Done()
	<-release
	sink.Add(uint64(pad[1]))
}

// shallowBlock parks without consuming stack, which is the shape a per-connection or health-check
// goroutine has.
func shallowBlock(release <-chan struct{}, ready *sync.WaitGroup) {
	var pad [128]byte
	pad[0] = 1
	ready.Done()
	<-release
	sink.Add(uint64(pad[0]))
}

func main() {
	mode := flag.String("mode", "rate", "rate|stack")
	allocMiB := flag.Int("allocmib", 64, "rate mode: MiB to allocate through four distinct call sites")
	prime := flag.Int("prime", 0, "stack mode: deep goroutines used to train the GC's scanned-stack average")
	wave := flag.Int("wave", 128, "stack mode: goroutines in the measured wave (reuse the primed g free list)")
	depth := flag.Int("depth", 20, "stack mode: recursion levels for deep goroutines")
	deepWave := flag.Bool("deep-wave", false, "stack mode: park/run the wave at the bottom of a deep recursion instead of shallow")
	churnWaves := flag.Int("churn-waves", 0, "stack mode: if > 0, run this many create/run/exit waves instead of one parked wave")
	flag.Parse()

	if value := os.Getenv("PROBE_GOMAXPROCS"); value != "" {
		var procs int
		if _, err := fmt.Sscanf(value, "%d", &procs); err != nil || procs < 1 {
			fail("PROBE_GOMAXPROCS=%q is not a positive integer", value)
		}
		runtime.GOMAXPROCS(procs)
	}

	// runtime/debug is read here for two independent reasons: SetGCPercent(-1) is what makes the
	// starting-size experiment deterministic, and SetMaxThreads reports a real value from the
	// linked runtime/debug package. It is NOT evidence for either startup setting: SetMaxThreads
	// has no getter and no relation to memprofilerate or adaptivestackstart.
	previousMaxThreads := debug.SetMaxThreads(1 << 20)
	debug.SetGCPercent(-1)

	header := reportHeader{
		Schema:            "gm2-startupprobe/1",
		Mode:              *mode,
		GOOS:              runtime.GOOS,
		GOARCH:            runtime.GOARCH,
		GoVersion:         runtime.Version(),
		PID:               os.Getpid(),
		GOMAXPROCS:        runtime.GOMAXPROCS(0),
		MaxThreadsBefore:  previousMaxThreads,
		MemProfileRate:    int64(runtime.MemProfileRate),
		HeapProfileHolder: heapProfileSampleCount(),
		WarmupMiB:         warmupMiB(),
	}

	switch *mode {
	case "rate":
		runRate(header, *allocMiB)
	case "stack":
		runStack(header, *prime, *wave, *depth, *deepWave, *churnWaves)
	default:
		fail("unknown -mode %q", *mode)
	}
}

type reportHeader struct {
	Schema            string `json:"schema"`
	Mode              string `json:"mode"`
	GOOS              string `json:"goos"`
	GOARCH            string `json:"goarch"`
	GoVersion         string `json:"go_version"`
	PID               int    `json:"pid"`
	GOMAXPROCS        int    `json:"gomaxprocs"`
	MaxThreadsBefore  int    `json:"max_threads_before"`
	MemProfileRate    int64  `json:"mem_profile_rate"`
	HeapProfileHolder int64  `json:"heap_profile_samples_at_main"`
	WarmupMiB         int    `json:"warmup_mib"`
}

func warmupMiB() int {
	value := strings.TrimSpace(os.Getenv("PROBE_WARMUP_MIB"))
	if value == "" {
		return 0
	}
	mib, err := strconv.Atoi(value)
	if err != nil || mib <= 0 {
		return 0
	}
	return mib
}

type rateReport struct {
	reportHeader
	TotalAllocAtMain        uint64 `json:"total_alloc_at_main"`
	BucketsAtMain           uint64 `json:"buckets_at_main"`
	HeapObjectsAtMain       uint64 `json:"heap_objects_at_main"`
	GoroutinesCreatedAtMain uint64 `json:"goroutines_created_at_main"`
	AllocMiB                int    `json:"alloc_mib"`
	TotalAllocAfter         uint64 `json:"total_alloc_after"`
	BucketsAfterAlloc       uint64 `json:"buckets_after_alloc"`
	HeapObjectsAfterAlloc   uint64 `json:"heap_objects_after_alloc"`
	HeapProfileSamples      int64  `json:"heap_profile_samples_after_alloc"`
	GCCount                 uint32 `json:"gc_count"`
	WallMillis              int64  `json:"wall_millis"`
}

// runRate separates the two numbers a startup policy changes: the bucket table that exists before
// this program allocates anything of its own (it may already have been allocated by runtime/package
// initialization, which is the case for a real libbox), and the table plus samples after a known
// amount of allocation across several distinct call stacks.
func runRate(header reportHeader, allocMiB int) {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	report := rateReport{
		reportHeader:            header,
		TotalAllocAtMain:        memStats.TotalAlloc,
		BucketsAtMain:           readUint64("/memory/classes/profiling/buckets:bytes"),
		HeapObjectsAtMain:       memStats.HeapObjects,
		GoroutinesCreatedAtMain: readUint64("/sched/goroutines-created:goroutines"),
		AllocMiB:                allocMiB,
	}
	started := time.Now()
	allocateAcrossSites(allocMiB)
	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&memStats)
	report.TotalAllocAfter = memStats.TotalAlloc
	report.BucketsAfterAlloc = readUint64("/memory/classes/profiling/buckets:bytes")
	report.HeapObjectsAfterAlloc = memStats.HeapObjects
	report.HeapProfileSamples = heapProfileSampleCount()
	report.GCCount = memStats.NumGC
	report.WallMillis = time.Since(started).Milliseconds()
	emit(report)
}

// retained holds the part of the allocation the probe keeps: a live heap gives the profile
// inuse/alloc records to report, not just free records.
var retained [][]byte

// allocateAcrossSites allocates from four distinct call sites with four distinct sizes and
// lifetimes. A single allocation point would put every sample in one bucket and hide whether the
// table is sized by call-stack count or by total bytes; churn exercises the free side too.
func allocateAcrossSites(mib int) {
	if mib <= 0 {
		return
	}
	churn := mib / 4
	for round := 0; round < 4; round++ {
		// Site A: 96-byte short-lived objects.
		for index := 0; index < churn*1024*1024/96/4; index++ {
			value := make([]byte, 96)
			value[0] = byte(index)
			sink.Add(uint64(value[0]))
		}
		// Site B: 1 KiB retained objects.
		for index := 0; index < churn*1024*1024/1024/4; index++ {
			value := make([]byte, 1024)
			value[0] = byte(index)
			retained = append(retained, value)
		}
		// Site C: 4 KiB short-lived objects.
		for index := 0; index < churn*1024*1024/4096/4; index++ {
			value := make([]byte, 4096)
			value[0] = byte(index)
			sink.Add(uint64(value[0]))
		}
		// Site D: 32 KiB retained objects.
		for index := 0; index < churn*1024*1024/(32*1024)/4; index++ {
			value := make([]byte, 32*1024)
			value[0] = byte(index)
			retained = append(retained, value)
		}
	}
}

type phaseReading struct {
	StackBytes   uint64 `json:"stack_bytes"`
	StartingSize uint64 `json:"starting_size_bytes"`
	Goroutines   uint64 `json:"goroutines"`
	// NumGoroutines is runtime.NumGoroutine, reported next to the metric on purpose. The metric is
	// gcount(true) (allglen minus the g free lists), which counts a goroutine whose g has been
	// returned to a free list as gone while its STACK can still be retained there; a phase that
	// reads "goroutines: 8" with 524288 bytes of stacks is a parked wave that already exited, not a
	// live one.
	NumGoroutines int     `json:"num_goroutines"`
	Created       uint64  `json:"goroutines_created"`
	CPUSeconds    float64 `json:"cpu_seconds_used"`
	WallMicros    int64   `json:"wall_micros"`
}

type stackReport struct {
	reportHeader
	Prime        int           `json:"prime"`
	Wave         int           `json:"wave"`
	Depth        int           `json:"depth"`
	DeepWave     bool          `json:"deep_wave"`
	ChurnWaves   int           `json:"churn_waves"`
	Baseline     phaseReading  `json:"baseline"`
	AfterPrimeGC *phaseReading `json:"after_prime_gc,omitempty"`
	BeforeWave   phaseReading  `json:"before_wave"`
	// WaveLive is the parked-wave reading: every wave goroutine is blocked, so this is the
	// simultaneous stack cost. It is absent in churn mode.
	WaveLive *phaseReading `json:"wave_live,omitempty"`
	// ChurnReadings are the post-wave readings in churn mode: each wave's goroutines have run and
	// exited, so this is the free-list steady state rather than a live set.
	ChurnReadings      []phaseReading `json:"churn_readings,omitempty"`
	AfterWave          *phaseReading  `json:"after_wave,omitempty"`
	AfterGC            phaseReading   `json:"after_gc"`
	HeapProfileSamples int64          `json:"heap_profile_samples_end"`
}

// runStack measures the two goroutine-stack paths that differ in which stack a goroutine gets:
//
//	cold (a new g gets malg(stackMin), identical under both policies)
//	reuse (gfget gives a stackless free g stackalloc(startingStackSize))
//
// The prime phase parks primeCount goroutines at the bottom of a deep recursion and runs one GC
// while they are live, because gcComputeStartingStackSize averages the SCANNED stack size of the
// last cycle. The wave then reuses the gs that the prime goroutines returned. Churn mode repeats
// create/run/exit waves, which is the shape of per-connection work and is where keeping a
// starting-size stack on the free list shows up as a steady state.
//
// The probe disables automatic GC (main), so the only GCs are the ones here. That is deliberate:
// any GC between the training phase and the wave would recompute startingStackSize from that
// cycle's scans and dissolve the setting being measured.
func runStack(header reportHeader, primeCount, waveCount, depth int, deepWave bool, churnWaves int) {
	report := stackReport{reportHeader: header, Prime: primeCount, Wave: waveCount, Depth: depth, DeepWave: deepWave, ChurnWaves: churnWaves}
	report.Baseline = readPhase(0)
	if primeCount > 0 {
		release := make(chan struct{})
		var parked sync.WaitGroup
		parked.Add(primeCount)
		var ready sync.WaitGroup
		ready.Add(primeCount)
		for index := 0; index < primeCount; index++ {
			go func() {
				defer parked.Done()
				deepBlock(depth, release, &ready)
			}()
		}
		// Wait until every prime goroutine is parked at the bottom of its recursion; only then does
		// the GC scan the deep stacks this phase exists to inject into the average.
		ready.Wait()
		primeStart := time.Now()
		runtime.GC()
		phase := readPhase(time.Since(primeStart).Microseconds())
		report.AfterPrimeGC = &phase
		close(release)
		parked.Wait()
	}
	report.BeforeWave = readPhase(0)

	if churnWaves > 0 {
		for wave := 0; wave < churnWaves; wave++ {
			var group sync.WaitGroup
			group.Add(waveCount)
			waveStart := time.Now()
			for index := 0; index < waveCount; index++ {
				go func() {
					defer group.Done()
					if deepWave {
						deepFrame(depth)
						return
					}
					sink.Add(1)
				}()
			}
			group.Wait()
			report.ChurnReadings = append(report.ChurnReadings, readPhase(time.Since(waveStart).Microseconds()))
		}
	} else {
		release := make(chan struct{})
		var parked sync.WaitGroup
		parked.Add(waveCount)
		var ready sync.WaitGroup
		ready.Add(waveCount)
		waveStart := time.Now()
		for index := 0; index < waveCount; index++ {
			go func() {
				defer parked.Done()
				if deepWave {
					deepBlock(depth, release, &ready)
					return
				}
				shallowBlock(release, &ready)
			}()
		}
		ready.Wait()
		// Separate variables, not one reassigned `phase`: both report fields hold pointers, and a
		// single variable would make WaveLive alias the after-release reading.
		waveLive := readPhase(time.Since(waveStart).Microseconds())
		report.WaveLive = &waveLive
		close(release)
		parked.Wait()
		afterWave := readPhase(0)
		report.AfterWave = &afterWave
	}
	gcStart := time.Now()
	runtime.GC()
	report.AfterGC = readPhase(time.Since(gcStart).Microseconds())
	report.HeapProfileSamples = heapProfileSampleCount()
	emit(report)
}

func readPhase(wallMicros int64) phaseReading {
	return phaseReading{
		StackBytes:    readUint64("/memory/classes/heap/stacks:bytes"),
		StartingSize:  readUint64("/gc/stack/starting-size:bytes"),
		Goroutines:    readUint64("/sched/goroutines:goroutines"),
		NumGoroutines: runtime.NumGoroutine(),
		Created:       readUint64("/sched/goroutines-created:goroutines"),
		CPUSeconds:    cpuSecondsUsed(),
		WallMicros:    wallMicros,
	}
}

// cpuSecondsUsed is /cpu/classes/total:cpu-seconds minus idle, i.e. an estimate of CPU actually
// spent in Go code and the Go runtime. It is only comparable with other /cpu/classes readings, not
// with an OS process CPU time.
func cpuSecondsUsed() float64 {
	total := readFloat64("/cpu/classes/total:cpu-seconds")
	idle := readFloat64("/cpu/classes/idle:cpu-seconds")
	return total - idle
}

func readUint64(name string) uint64 {
	samples := []metrics.Sample{{Name: name}}
	metrics.Read(samples)
	if samples[0].Value.Kind() != metrics.KindUint64 {
		fail("metric %s has kind %v, want uint64: a missing metric must not be read as zero", name, samples[0].Value.Kind())
	}
	return samples[0].Value.Uint64()
}

func readFloat64(name string) float64 {
	samples := []metrics.Sample{{Name: name}}
	metrics.Read(samples)
	if samples[0].Value.Kind() != metrics.KindFloat64 {
		fail("metric %s has kind %v, want float64", name, samples[0].Value.Kind())
	}
	return samples[0].Value.Float64()
}

func emit(value any) {
	encoded, err := json.Marshal(value)
	if err != nil {
		fail("encode report: %v", err)
	}
	fmt.Println(string(encoded))
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(2)
}
