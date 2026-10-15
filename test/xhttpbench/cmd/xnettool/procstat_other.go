//go:build !windows

package main

import (
	"fmt"
	"time"
)

// The CPU accounting half of the harness is Windows-only because that is the
// only environment this project measured on. The load generator still runs, and
// still reports throughput and latency — it simply cannot charge CPU to a
// process, and says so instead of reporting a zero that reads as "free".

type processSample struct {
	PID            int     `json:"pid"`
	CPUSeconds     float64 `json:"cpu_sec"`
	WorkingSetByte uint64  `json:"working_set_bytes"`
	PeakWSBytes    uint64  `json:"peak_working_set_bytes"`
}

type cpuTracker struct {
	start time.Time
}

func newCPUTracker(pids []int) *cpuTracker {
	return &cpuTracker{start: time.Now()}
}

func (tracker *cpuTracker) finish() (map[string]processSample, float64) {
	return nil, time.Since(tracker.start).Seconds()
}

func runProcStat(args []string) error {
	return fmt.Errorf("procstat: process CPU accounting is implemented for Windows only")
}
