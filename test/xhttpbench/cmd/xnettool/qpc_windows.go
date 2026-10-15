//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

// queryPerformanceCounter is the Windows high-resolution performance counter,
// called directly so the harness can tell Go's monotonic clock apart from the
// best clock the operating system offers.

var (
	kernel32Counter  = syscall.NewLazyDLL("kernel32.dll")
	performanceCount = kernel32Counter.NewProc("QueryPerformanceCounter")
	performanceRate  = kernel32Counter.NewProc("QueryPerformanceFrequency")
)

func queryPerformanceCounter() int64 {
	var counter int64
	result, _, _ := performanceCount.Call(uintptr(unsafe.Pointer(&counter)))
	if result == 0 {
		return 0
	}
	return counter
}

// queryPerformanceCounterNanoseconds converts the counter to nanoseconds.
func queryPerformanceCounterNanoseconds() int64 {
	frequency := queryPerformanceFrequency()
	if frequency == 0 {
		return 0
	}
	return queryPerformanceCounter() * 1e9 / frequency
}

func queryPerformanceFrequency() int64 {
	var frequency int64
	result, _, _ := performanceRate.Call(uintptr(unsafe.Pointer(&frequency)))
	if result == 0 {
		return 0
	}
	return frequency
}

func qpcSupported() bool {
	return queryPerformanceFrequency() != 0
}

// Cached counter state, so a per-operation latency sample does not pay a
// QueryPerformanceFrequency call, and so every sample shares one origin.
var (
	cachedFrequency = queryPerformanceFrequency()
	cachedOrigin    = queryPerformanceCounter()
)

// highResolutionMicros returns microseconds since the first QPC read of this
// process. Microseconds rather than nanoseconds because the counter delta times
// 1e9 could overflow int64 on a counter that has been running for hours, and
// because microseconds are the unit the harness reports.
func highResolutionMicros() (float64, bool) {
	if cachedFrequency == 0 {
		return 0, false
	}
	delta := queryPerformanceCounter() - cachedOrigin
	return float64(delta) * 1e6 / float64(cachedFrequency), true
}
