package runner

import (
	"sort"
	"time"

	"github.com/sagernet/sing-box/scripts/perf/internal/schema"
)

// percentile returns the nearest-rank percentile of an ascending slice.
func percentile(sorted []float64, p float64) *float64 {
	if len(sorted) == 0 {
		return nil
	}
	if p <= 0 {
		value := sorted[0]
		return &value
	}
	if p >= 100 {
		value := sorted[len(sorted)-1]
		return &value
	}
	rank := int((p/100)*float64(len(sorted))+0.999999) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	value := sorted[rank]
	return &value
}

func median(sorted []float64) *float64 {
	if len(sorted) == 0 {
		return nil
	}
	n := len(sorted)
	if n%2 == 1 {
		value := sorted[n/2]
		return &value
	}
	value := (sorted[n/2-1] + sorted[n/2]) / 2
	return &value
}

// mad is the median absolute deviation: a spread measure that one stalled
// repetition cannot inflate the way a standard deviation can.
func mad(sorted []float64, centre *float64) *float64 {
	if len(sorted) == 0 || centre == nil {
		return nil
	}
	deviations := make([]float64, len(sorted))
	for i, value := range sorted {
		if value > *centre {
			deviations[i] = value - *centre
		} else {
			deviations[i] = *centre - value
		}
	}
	sort.Float64s(deviations)
	return median(deviations)
}

func statRow(metric, unit string, streams int, pattern string, values []float64) schema.Summary {
	sorted := make([]float64, len(values))
	copy(sorted, values)
	sort.Float64s(sorted)
	row := schema.Summary{Metric: metric, Unit: unit, Streams: streams, Pattern: pattern, N: len(sorted)}
	if len(sorted) == 0 {
		return row
	}
	row.Median = median(sorted)
	row.MAD = mad(sorted, row.Median)
	low, high := sorted[0], sorted[len(sorted)-1]
	row.Min, row.Max = &low, &high
	if len(sorted) >= 20 {
		row.P95 = percentile(sorted, 95)
	}
	if len(sorted) >= 100 {
		row.P99 = percentile(sorted, 99)
	}
	return row
}

// latencySummary reduces a per-repetition latency set. Percentiles are reported
// only when the sample count makes them meaningful: a p99 of 30 operations is
// the maximum wearing a percentile label.
func latencySummary(streams int, pattern string, latencies []time.Duration) *schema.Latency {
	if len(latencies) == 0 {
		return nil
	}
	millis := make([]float64, len(latencies))
	for i, d := range latencies {
		millis[i] = float64(d) / float64(time.Millisecond)
	}
	sort.Float64s(millis)
	response := &schema.Latency{Count: len(millis)}
	low, high := millis[0], millis[len(millis)-1]
	response.MinMS, response.MaxMS = &low, &high
	response.P50MS = percentile(millis, 50)
	if len(millis) >= 20 {
		response.P95MS = percentile(millis, 95)
	}
	if len(millis) >= 100 {
		response.P99MS = percentile(millis, 99)
	}
	var sum float64
	for _, value := range millis {
		sum += value
	}
	mean := sum / float64(len(millis))
	response.MeanMS = &mean
	_ = streams
	_ = pattern
	return response
}
