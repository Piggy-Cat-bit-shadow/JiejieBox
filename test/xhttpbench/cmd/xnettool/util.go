package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"
)

// clockBase backs the monotonic fallback clock.
var clockBase = time.Now()

// nowMicros reads the finest clock the host offers.
//
// On this project's host Go's monotonic clock was measured to tick far more
// coarsely than the operations being timed (see `xnettool clockprobe`), which
// turns per-operation latency into a row of zeros. Where a high-resolution
// counter exists it is used instead, and the clockprobe output is quoted next
// to every latency number so the resolution is never implied.
func nowMicros() float64 {
	if value, ok := highResolutionMicros(); ok {
		return value
	}
	return float64(time.Since(clockBase).Nanoseconds()) / 1000
}

// pidList is a repeatable -pid flag. It lives here rather than next to the
// Windows process API because the load generator uses it on every platform: the
// CPU accounting it feeds is Windows-only, but the flag itself must parse
// everywhere, or the harness stops cross-compiling for other targets.
type pidList []int

func (list *pidList) String() string {
	return fmt.Sprint([]int(*list))
}

func (list *pidList) Set(value string) error {
	var pid int
	if _, err := fmt.Sscanf(value, "%d", &pid); err != nil {
		return fmt.Errorf("invalid pid %q", value)
	}
	*list = append(*list, pid)
	return nil
}

// jsonMarshal keeps every result object on one encoding path, so a report
// written to stdout and the same report written to a file cannot disagree.
func jsonMarshal(value any) ([]byte, error) {
	return json.Marshal(value)
}

func writeJSONFile(path string, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", path, err)
	}
	encoded = append(encoded, '\n')
	if err = os.WriteFile(path, encoded, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// latencySummary is a distribution in microseconds. Percentiles are reported
// rather than a mean because every interesting failure of a tunnel shows up in
// the tail: a mean hides the queueing that a p99 exposes.
type latencySummary struct {
	Count int     `json:"count"`
	Min   float64 `json:"min"`
	P50   float64 `json:"p50"`
	P90   float64 `json:"p90"`
	P99   float64 `json:"p99"`
	Max   float64 `json:"max"`
	Mean  float64 `json:"mean"`
}

func summarizeLatencies(samples []float64) *latencySummary {
	if len(samples) == 0 {
		return nil
	}
	sorted := make([]float64, len(samples))
	copy(sorted, samples)
	sort.Float64s(sorted)
	total := 0.0
	for _, sample := range sorted {
		total += sample
	}
	return &latencySummary{
		Count: len(sorted),
		Min:   round3(sorted[0]),
		P50:   round3(percentile(sorted, 0.50)),
		P90:   round3(percentile(sorted, 0.90)),
		P99:   round3(percentile(sorted, 0.99)),
		Max:   round3(sorted[len(sorted)-1]),
		Mean:  round3(total / float64(len(sorted))),
	}
}

// percentile takes a sorted slice and uses the nearest-rank definition, so the
// value reported is always a sample that was actually observed.
func percentile(sorted []float64, fraction float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(fraction*float64(len(sorted)) + 0.999999)
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}

func round3(value float64) float64 {
	return float64(int64(value*1000+0.5)) / 1000
}
