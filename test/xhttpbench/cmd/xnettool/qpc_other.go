//go:build !windows

package main

// The high-resolution counter is a Windows facility here; on other platforms
// the probe reports time.Now alone, and the latency path falls back to it.

func queryPerformanceCounter() int64 {
	return 0
}

func queryPerformanceCounterNanoseconds() int64 {
	return 0
}

func qpcSupported() bool {
	return false
}

func highResolutionMicros() (float64, bool) {
	return 0, false
}
