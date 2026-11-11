package sampler

// Stage-3 infrastructure audit: the sampler is the instrument every number in
// this programme came from, so it gets its own invariants instead of being
// trusted. These tests read the test process itself; nothing is mocked.

import (
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func TestReadOwnProcessIsPlausible(t *testing.T) {
	// Every field asserted below comes from /proc: StartTick (the PID-reuse guard), Threads, FDs and
	// the tick/schedstat CPU sources. The Windows implementation reads GetProcessTimes and
	// GetProcessMemoryInfo, which provide none of them, so this test states its platform
	// requirement the same way TestCollectorSamplesAWindowAndStops already does instead of
	// reporting a correct port as a failure.
	if runtime.GOOS != "linux" {
		t.Skip("the /proc-backed fields this asserts are not available on " + runtime.GOOS)
	}
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

// TestPIDReuseControlUsesCreationTime is the negative control for the PID-reuse
// guard: the guard may only fire on a *known* creation time that changed. An
// unknown time (zero) is not evidence of reuse, and a stable time is not either.
func TestPIDReuseControlUsesCreationTime(t *testing.T) {
	collector := &Collector{}
	collector.absorb(Stat{StartTick: 100})
	collector.absorb(Stat{StartTick: 100})
	if collector.reused {
		t.Fatal("an unchanged creation time was reported as PID reuse")
	}
	collector.absorb(Stat{StartTick: 0})
	if collector.reused {
		t.Fatal("an unknown creation time was reported as PID reuse")
	}
	collector.absorb(Stat{StartTick: 200})
	if !collector.reused {
		t.Fatal("a changed creation time was not reported as PID reuse")
	}
}

// TestUnmeasurableFieldsAreNullNotZero pins the reporting contract: a platform
// that cannot see threads or file descriptors must leave the peaks nil so they
// serialize as null, rather than publishing an unmeasured 0 as a measurement.
func TestUnmeasurableFieldsAreNullNotZero(t *testing.T) {
	collector := Start(os.Getpid(), 20*time.Millisecond)
	time.Sleep(120 * time.Millisecond)
	summary := collector.Stop()
	if runtime.GOOS == "linux" {
		if summary.ThreadsPeak == nil || summary.FDsPeak == nil {
			t.Fatal("Linux can read threads and FDs from /proc, so both peaks must be present")
		}
		return
	}
	if summary.ThreadsPeak != nil {
		t.Fatalf("threads are not observable on %s, but ThreadsPeak = %d", runtime.GOOS, *summary.ThreadsPeak)
	}
	if summary.FDsPeak != nil {
		t.Fatalf("file descriptors are not observable on %s, but FDsPeak = %d", runtime.GOOS, *summary.FDsPeak)
	}
}

// TestWindowsStartTickIsStableAndNonZero proves the Windows PID-reuse guard has
// a real source: the creation FILETIME must be read, must not move while the
// process lives, and must differ for a different process.
func TestWindowsStartTickIsStableAndNonZero(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the creation FILETIME is a Windows-only source")
	}
	first, err := Read(os.Getpid())
	if err != nil {
		t.Fatalf("Read(self): %v", err)
	}
	if first.StartTick == 0 {
		t.Fatal("StartTick is zero: PID reuse cannot be detected on Windows")
	}
	second, err := Read(os.Getpid())
	if err != nil {
		t.Fatalf("Read(self) again: %v", err)
	}
	if second.StartTick != first.StartTick {
		t.Fatalf("StartTick moved between reads of a live process: %d -> %d", first.StartTick, second.StartTick)
	}

	child := exec.Command("cmd.exe", "/c", "ping", "-n", "3", "127.0.0.1")
	if err := child.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	defer func() {
		_ = child.Process.Kill()
		_, _ = child.Process.Wait()
	}()
	var childStat Stat
	deadline := time.Now().Add(3 * time.Second)
	for {
		childStat, err = Read(child.Process.Pid)
		if err == nil && childStat.StartTick != 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("read child %d: %v", child.Process.Pid, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if childStat.StartTick == first.StartTick {
		t.Fatal("a different process reported the same creation time as this one")
	}
}
