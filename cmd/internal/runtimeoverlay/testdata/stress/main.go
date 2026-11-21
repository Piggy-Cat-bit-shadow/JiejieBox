// Command stress attacks the two runtime structures the iOS runtime overlay changes, in the shapes
// that would expose a broken invariant rather than a missing feature.
//
// It is not a benchmark. It is the input an attack needs: many size classes, many spans per set,
// several Ps pushing and popping at once, goroutine stacks of different sizes exiting while a
// traceback and a GC walk them, and enough rounds for a leak to be visible as a trend.
//
// STRESS_MODE=span   span-set push/pop/reset across block and spine boundaries
// STRESS_MODE=stack  exited-goroutine stacks freed and reused while traced and collected
//
// It lives under testdata/ and is compiled as its own module by the tests.
package main

import (
	"fmt"
	"os"
	"runtime"
	"runtime/metrics"
	"strconv"
	"sync"
	"sync/atomic"
)

func main() {
	mode := os.Getenv("STRESS_MODE")
	procs := setting("STRESS_GOMAXPROCS", 4)
	runtime.GOMAXPROCS(procs)
	fmt.Printf("mode=%s\n", mode)
	fmt.Printf("gomaxprocs=%d\n", procs)
	switch mode {
	case "span":
		spanAttack(setting("STRESS_ROUNDS", 6))
	case "stack":
		stackAttack(setting("STRESS_ROUNDS", 30))
	default:
		fmt.Printf("unknownMode=1\n")
		os.Exit(2)
	}
	fmt.Printf("done=1\n")
}

// ---------------------------------------------------------------------------------------------
// span attack: A1
// ---------------------------------------------------------------------------------------------

// sizeClasses spans the allocator's ladder from tiny to large, deliberately including the classes a
// network program actually uses (64..2048) repeatedly so one size class grows many spans.
var sizeClasses = []int{
	8, 12, 16, 24, 32, 48, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 384, 448, 512,
	640, 768, 896, 1024, 1280, 1536, 1792, 2048, 3072, 4096, 6144, 8192, 12288, 16384, 24576,
}

type chunk struct {
	sizes []int
	bytes [][]byte
}

// spanAttack drives many size classes far past the point where one spanSet block holds their spans,
// then frees them, collects, and does it again - which is what makes the block-free and spine-growth
// paths run rather than sit idle.
func spanAttack(rounds int) {
	var sink atomic.Int64
	for round := 0; round < rounds; round++ {
		workers := runtime.GOMAXPROCS(0) * 2
		chunks := make([]chunk, workers)
		var group sync.WaitGroup
		for worker := 0; worker < workers; worker++ {
			group.Add(1)
			go func(worker int) {
				defer group.Done()
				local := chunk{sizes: make([]int, 0, 4096), bytes: make([][]byte, 0, 4096)}
				// Each worker touches every size class, many times, so one class accumulates
				// thousands of spans across the whole pool: at 32 entries per block that is
				// dozens of blocks and several spine doublings, at 512 it is a handful.
				for repeat := 0; repeat < 24; repeat++ {
					for _, size := range sizeClasses {
						buffer := make([]byte, size)
						buffer[0] = byte(size)
						buffer[len(buffer)-1] = byte(worker)
						local.sizes = append(local.sizes, size)
						local.bytes = append(local.bytes, buffer)
					}
				}
				// Verify what is still live before handing it back: a corrupted heap shows up here
				// rather than only as a crash later.
				for index, buffer := range local.bytes {
					size := local.sizes[index]
					if int(buffer[0]) != size&0xff || int(buffer[len(buffer)-1]) != worker&0xff {
						fmt.Printf("intact=0\n")
						os.Exit(3)
					}
					sink.Add(int64(buffer[len(buffer)/2]))
				}
				// Free in a different order than allocated, so pushes and pops interleave.
				for index := len(local.bytes) - 1; index >= 0; index -= 2 {
					local.bytes[index] = nil
				}
				chunks[worker] = local
			}(worker)
		}
		group.Wait()
		// Free the rest from this goroutine while the pool is warm, then collect so the spans go
		// back through the spanSet and the block-free path.
		for worker := range chunks {
			chunks[worker].bytes = nil
		}
		runtime.GC()
		runtime.GC()
		fmt.Printf("round.%d.metadata.other=%d\n", round, metric("/memory/classes/metadata/other:bytes"))
		fmt.Printf("round.%d.mspan.inuse=%d\n", round, metric("/memory/classes/metadata/mspan/inuse:bytes"))
		fmt.Printf("round.%d.heap.objects=%d\n", round, metric("/memory/classes/heap/objects:bytes"))
	}
	fmt.Printf("sink=%d\n", sink.Load())
}

// ---------------------------------------------------------------------------------------------
// stack attack: A3
// ---------------------------------------------------------------------------------------------

// depths makes goroutines exit with stacks of different histories: shallow ones that never grew
// past the starting size (the ones A3 newly frees), deep ones that grew (upstream already freed
// those), and a mix so a reused g alternates between them.
var depths = []int{0, 2, 8, 32, 96, 240, 12, 64, 0, 160}

var stackSink atomic.Int64

// stackAttack creates and retires goroutines of several stack sizes while a traceback and a GC walk
// them. If a freed stack were still reachable from a live g, a traceback over all goroutines is the
// cheapest way to make that visible.
func stackAttack(rounds int) {
	first := int64(0)
	for round := 0; round < rounds; round++ {
		if round%4 == 0 {
			// Traceback every live goroutine, including the ones being torn down in this round.
			traceAll()
			runtime.GC()
		}
		var group sync.WaitGroup
		for index := 0; index < 64; index++ {
			group.Add(1)
			go func(index int) {
				defer group.Done()
				recurse(depths[index%len(depths)], index)
			}(index)
		}
		group.Wait()
		runtime.GC()
		resident := int64(metric("/memory/classes/heap/stacks:bytes"))
		fmt.Printf("round.%d.stacks=%d\n", round, resident)
		if round == 4 {
			first = resident
		}
		// The trend is the leak check: a stack that is freed but never reused, or a g that keeps a
		// stale stack, shows up as growth that does not come back down.
		if round > 4 && first > 0 && resident > first*3 {
			fmt.Printf("leaked=1 first=%d now=%d\n", first, resident)
			os.Exit(4)
		}
	}
}

//go:noinline
func recurse(depth, tag int) {
	var frame [256]byte
	frame[0] = byte(depth)
	frame[len(frame)-1] = byte(tag)
	stackSink.Add(int64(frame[depth&0xff]))
	if depth == 0 {
		return
	}
	recurse(depth-1, tag+1)
	stackSink.Add(int64(frame[depth&0x3]))
}

func traceAll() {
	buffer := make([]byte, 1<<20)
	for {
		written := runtime.Stack(buffer, true)
		if written < len(buffer) {
			break
		}
		buffer = make([]byte, len(buffer)*2)
	}
}

// ---------------------------------------------------------------------------------------------

func metric(name string) uint64 {
	sample := []metrics.Sample{{Name: name}}
	metrics.Read(sample)
	if sample[0].Value.Kind() == metrics.KindBad {
		// A name this toolchain does not publish used to read back as 0, which looked like a
		// measurement of zero bytes. It is a bug in the probe.
		fmt.Printf("invalidMetric=%s\n", name)
		os.Exit(2)
	}
	if sample[0].Value.Kind() != metrics.KindUint64 {
		return 0
	}
	return sample[0].Value.Uint64()
}

func setting(name string, fallback int) int {
	if text := os.Getenv(name); text != "" {
		if parsed, err := strconv.Atoi(text); err == nil && parsed > 0 {
			return parsed
		}
	}
	return fallback
}
