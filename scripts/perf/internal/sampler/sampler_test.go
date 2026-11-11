package sampler

// Stage-3 infrastructure audit: the sampler is the instrument every number in
// this programme came from, so it gets its own invariants instead of being
// trusted. These tests read the test process itself; nothing is mocked.

import (
	"os"
	"runtime"
	"testing"
	"time"
)

func TestReadOwnProcessIsPlausible(t *testing.T) {
	self := os.Getpid()
	stat, err := Read(self)
	if err != nil {
		t.Fatalf("Read(%d): %v", self, err)
	}
	if stat.StartTick == 0 {
		t.Fatal("StartTick is zero, so PID reuse cannot be detected")
	}
	if stat.RSSBytes <= 0 {
		t.Fatalf("RSSBytes = %d, want > 0", stat.RSSBytes)
	}
	if stat.Threads < 1 {
		t.Fatalf("Threads = %d, want at least one", stat.Threads)
	}
	if stat.FDs <= 0 {
		t.Fatalf("FDs = %d, want > 0 (stdin/stdout/stderr at minimum)", stat.FDs)
	}
	if stat.CPUTicks == 0 && stat.CPUNanos == 0 {
		t.Fatal("no CPU source at all: both /proc ticks and schedstat are zero")
	}
}

func TestReadOfADeadProcessFails(t *testing.T) {
	// PID 0 is never a user process; the read must report an error rather than a
	// zero-valued Stat that would be averaged as "the process used nothing".
	if _, err := Read(0); err == nil {
		t.Fatal("Read(0) returned no error")
	}
}

func TestCollectorSamplesAWindowAndStops(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the Linux sampler is the only implementation exercised here")
	}
	collector := Start(os.Getpid(), 50*time.Millisecond)
	// Spend the window doing a little work so the CPU counter has something to say.
	deadline := time.Now().Add(400 * time.Millisecond)
	var spin uint64
	for time.Now().Before(deadline) {
		spin += 1
	}
	_ = spin
	summary := collector.Stop()
	if summary.Samples < 3 {
		t.Fatalf("Samples = %d over ~400 ms at 50 ms; the ticker is missing windows", summary.Samples)
	}
	if summary.ReadFailures != 0 {
		t.Fatalf("ReadFailures = %d while reading our own process", summary.ReadFailures)
	}
	if summary.RSSStart == nil || summary.RSSEnd == nil || summary.RSSPeak == nil {
		t.Fatal("RSS fields are nil; the first synchronous reading did not land")
	}
	if *summary.RSSPeak < *summary.RSSEnd {
		t.Fatalf("RSSPeak %d < RSSEnd %d", *summary.RSSPeak, *summary.RSSEnd)
	}
	if summary.ThreadsPeak == nil || *summary.ThreadsPeak < 1 {
		t.Fatal("ThreadsPeak missing or below one")
	}
	if summary.FDsPeak == nil || *summary.FDsPeak <= 0 {
		t.Fatal("FDsPeak missing or non-positive")
	}
	if summary.FirstAt.IsZero() || summary.LastAt.Before(summary.FirstAt) {
		t.Fatalf("window times are inconsistent: %v .. %v", summary.FirstAt, summary.LastAt)
	}
	if summary.PIDReused {
		t.Fatal("PID reuse reported for our own live process")
	}
	if summary.CPUSeconds == nil || *summary.CPUSeconds < 0 {
		t.Fatal("CPU seconds missing or negative")
	}
}

func TestCollectorStopIsSafeToRepeat(t *testing.T) {
	collector := Start(os.Getpid(), 20*time.Millisecond)
	_ = collector.Stop()
	// A second Stop must not panic on a closed channel; the runner calls it from
	// deferred cleanup and a failure path can reach it twice.
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("second Stop panicked: %v", recovered)
		}
	}()
	_ = collector.Stop()
}
