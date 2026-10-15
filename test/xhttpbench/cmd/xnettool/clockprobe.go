package main

import (
	"flag"
	"fmt"
	"sort"
	"time"
)

// clockprobe reports the resolution of the clocks the harness can use.
//
// It exists because a latency measurement is only as good as the clock behind
// it, and the resolution of a monotonic clock is not a property of the API: on
// Windows, Go's time.Now has been backed by different sources over the years,
// and a virtualized host can quantize a counter that is advertised as
// nanosecond resolution. A benchmark that reports "p50 = 0 us" for a loopback
// round trip has measured its clock, not the transport — so the resolution is
// measured first and quoted wherever a latency number is.
func runClockProbe(args []string) error {
	flags := flag.NewFlagSet("clockprobe", flag.ExitOnError)
	durationMs := flags.Int("duration-ms", 200, "how long to sample each clock")
	if err := flags.Parse(args); err != nil {
		return err
	}
	window := time.Duration(*durationMs) * time.Millisecond

	base := time.Now()
	monotonic := sampleClock(window, func() int64 {
		return int64(time.Since(base))
	})
	wall := sampleClock(window, func() int64 {
		return time.Now().UnixNano()
	})
	performance := sampleClock(window, func() int64 {
		return queryPerformanceCounterNanoseconds()
	})

	fmt.Printf("wall clock (time.Now().UnixNano)      : %s\n", wall)
	fmt.Printf("monotonic clock (time.Since)          : %s\n", monotonic)
	fmt.Printf("QueryPerformanceCounter               : %s\n", performance)
	if performance.Samples > 0 && monotonic.Samples > 0 && performance.Min > 0 {
		fmt.Printf("QPC is %.0fx finer than Go's monotonic clock\n", float64(monotonic.Min)/float64(performance.Min))
	}
	if !qpcSupported() {
		fmt.Printf("QueryPerformanceCounter is unavailable on this host\n")
	}
	return nil
}

type clockStats struct {
	Samples int
	Min     int64
	P50     int64
	P90     int64
	Max     int64
}

func (stats clockStats) String() string {
	if stats.Samples == 0 {
		return "unavailable (no tick observed)"
	}
	return fmt.Sprintf("samples=%d min=%dns p50=%dns p90=%dns max=%dns",
		stats.Samples, stats.Min, stats.P50, stats.P90, stats.Max)
}

// sampleClock records the positive deltas between consecutive reads: that delta
// is the smallest interval the clock can distinguish.
func sampleClock(window time.Duration, read func() int64) clockStats {
	deadline := time.Now().Add(window)
	var deltas []int64
	previous := read()
	for time.Now().Before(deadline) {
		current := read()
		if delta := current - previous; delta > 0 {
			deltas = append(deltas, delta)
		}
		previous = current
	}
	if len(deltas) == 0 {
		return clockStats{}
	}
	sort.Slice(deltas, func(i, j int) bool { return deltas[i] < deltas[j] })
	return clockStats{
		Samples: len(deltas),
		Min:     deltas[0],
		P50:     deltas[len(deltas)/2],
		P90:     deltas[len(deltas)*9/10],
		Max:     deltas[len(deltas)-1],
	}
}
