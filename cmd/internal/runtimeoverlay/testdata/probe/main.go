// Command probe measures the runtime classes the iOS runtime overlay is supposed to move, so the
// overlay can be attributed on a host that is not an Apple device.
//
// It is built twice against the SAME overlay: once with the probe tag, which selects the file
// carrying the overlay's values, and once without it, which selects the file restating the
// toolchain's. The two binaries differ in nothing else, so the delta is the overlay's.
//
// This file lives under testdata/ so the go tool never treats it as part of any package, and it
// is compiled as its own module by probe_test.go.
package main

import (
	"fmt"
	"os"
	"runtime"
	"runtime/metrics"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// keep holds the allocations alive: the metadata cost is only paid while the spans exist.
var keep [][]byte

func main() {
	procs := 4
	if value := os.Getenv("PROBE_GOMAXPROCS"); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
			procs = parsed
		}
	}
	runtime.GOMAXPROCS(procs)

	// The churn benchmark runs FIRST, on a small clean heap: running it after the allocation
	// phases would measure GC interference from a few thousand live spans rather than the
	// goroutine path, and the cost side is the number this change could plausibly regress.
	churn := measureGoroutineChurn()

	touchSizeClasses()
	stackBurst()
	runtime.GC()
	runtime.GC()

	reportMemory()
	fmt.Printf("churnPerGoroutineNs=%d\n", churn)
}

// touchSizeClasses allocates a few objects from every step of the size-class ladder, which is
// what makes mcentrals hold spans. Span-set metadata is per mcentral and per span class, so an
// allocation pattern that touches many classes is the one that pays for the block size.
func touchSizeClasses() {
	for size := 8; size <= 32<<10; size += size / 8 {
		for index := 0; index < 64; index++ {
			keep = append(keep, make([]byte, size))
		}
	}
}

// stackBurst creates and retires short-lived goroutines with a small frame, which is the shape a
// tunnel's health checks and per-connection goroutines have.
func stackBurst() {
	var group sync.WaitGroup
	for round := 0; round < 8; round++ {
		for index := 0; index < 256; index++ {
			group.Add(1)
			go func() {
				defer group.Done()
				var pad [512]byte
				for position := range pad {
					pad[position] = byte(position)
				}
				sink.Add(uint64(pad[0]))
			}()
		}
		group.Wait()
	}
}

// sink defeats the dead-store elimination that would otherwise remove the frame the burst pads.
//
// It is atomic because the bursts write it from many goroutines at once. A plain byte was a data
// race, and `go run -race` on this probe reported it: the probe is what a performance and stability
// claim rests on, so it must itself be race-free before its numbers mean anything.
var sink atomic.Uint64

func reportMemory() {
	samples := []metrics.Sample{
		{Name: "/memory/classes/heap/stacks:bytes"},
		{Name: "/memory/classes/metadata/other:bytes"},
		{Name: "/memory/classes/metadata/mspan/inuse:bytes"},
		// `/memory/classes/heap/inuse:bytes` used to be here. This toolchain does not publish it,
		// and the loop below skipped unknown samples while every later reader of the map got zero -
		// so the census printed "heap in use: 0 bytes" as if it had measured something. The class
		// that answers the question is the live object heap.
		{Name: "/memory/classes/heap/objects:bytes"},
		{Name: "/memory/classes/heap/released:bytes"},
		{Name: "/gc/heap/goal:bytes"},
	}
	metrics.Read(samples)
	for _, sample := range samples {
		if sample.Value.Kind() == metrics.KindBad {
			// A name this toolchain does not publish is a bug in the probe, not a reading of zero.
			fmt.Printf("invalidMetric=%s\n", sample.Name)
			os.Exit(2)
		}
		if sample.Value.Kind() != metrics.KindUint64 {
			continue
		}
		fmt.Printf("%s=%d\n", sample.Name, sample.Value.Uint64())
	}
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	fmt.Printf("memstats.StackInuse=%d\n", stats.StackInuse)
	fmt.Printf("memstats.StackSys=%d\n", stats.StackSys)
	fmt.Printf("memstats.GCSys=%d\n", stats.GCSys)
	fmt.Printf("memstats.OtherSys=%d\n", stats.OtherSys)
	fmt.Printf("memstats.Sys=%d\n", stats.Sys)
	fmt.Printf("goroutines=%d\n", runtime.NumGoroutine())
}

// measureGoroutineChurn is the cost side of the overlay. Returning an exited goroutine's stack
// means the next goroutine in the same slot allocates one instead of finding it parked, so this
// rate is the metric that would expose a regression, not the residency.
//
// The answer is the MINIMUM of several rounds, because the quantity being compared is "how fast
// can this path go" and the noise (GC timing, scheduler placement, frequency scaling) only ever
// ADDS time. A run of the same binary was measured at 219 and 431 ns per goroutine on this host;
// a mean would have reported the overlay as a 2x regression for an unrelated reason.
func measureGoroutineChurn() int64 {
	const (
		created = 20000
		rounds  = 5
	)
	burst := func() int64 {
		start := time.Now()
		var group sync.WaitGroup
		var done atomic.Int64
		for index := 0; index < created; index++ {
			group.Add(1)
			go func() {
				defer group.Done()
				var pad [512]byte
				pad[0] = byte(done.Add(1))
				sink.Add(uint64(pad[0]))
			}()
		}
		group.Wait()
		return time.Since(start).Nanoseconds() / created
	}
	best := burst()
	for round := 1; round < rounds; round++ {
		if candidate := burst(); candidate < best {
			best = candidate
		}
	}
	return best
}
