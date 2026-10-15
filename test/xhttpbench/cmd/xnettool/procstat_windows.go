//go:build windows

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"syscall"
	"time"
	"unsafe"
)

// Per-process CPU and memory accounting, used to charge a phase's cost to the
// processes that actually paid it.
//
// The load generator samples the tunnel client and the reference server around
// its own measured phase, so a phase's CPU cost does not have to be inferred
// from process lifetime (which would include startup, handshakes and teardown)
// and does not need a second process to be scheduled on time.
//
// kernel32 is called directly rather than through golang.org/x/sys/windows so
// the harness keeps building against the module graph of the repository under
// test without adding a dependency to it.

const (
	processQueryLimitedInformation = 0x1000

	// processMemoryCounters is PROCESS_MEMORY_COUNTERS.
	processMemoryCountersSize = 10 * 8 // cb + PageFaultCount are DWORD, then 8 SIZE_T
)

var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess          = kernel32.NewProc("OpenProcess")
	procCloseHandle          = kernel32.NewProc("CloseHandle")
	procGetProcessTimes      = kernel32.NewProc("GetProcessTimes")
	procGetProcessMemoryInfo = kernel32.NewProc("K32GetProcessMemoryInfo")
)

type processSample struct {
	PID            int     `json:"pid"`
	CPUSeconds     float64 `json:"cpu_sec"`
	WorkingSetByte uint64  `json:"working_set_bytes"`
	PeakWSBytes    uint64  `json:"peak_working_set_bytes"`
}

type filetime struct {
	Low  uint32
	High uint32
}

func (f filetime) nanoseconds() uint64 {
	return uint64(f.High)<<32 | uint64(f.Low)
}

func sampleProcess(pid int) (processSample, error) {
	handle, _, callErr := procOpenProcess.Call(processQueryLimitedInformation, 0, uintptr(uint32(pid)))
	if handle == 0 {
		return processSample{}, fmt.Errorf("OpenProcess(%d): %v", pid, callErr)
	}
	defer procCloseHandle.Call(handle)

	var creation, exit, kernel, user filetime
	result, _, callErr := procGetProcessTimes.Call(handle,
		uintptr(unsafe.Pointer(&creation)),
		uintptr(unsafe.Pointer(&exit)),
		uintptr(unsafe.Pointer(&kernel)),
		uintptr(unsafe.Pointer(&user)))
	if result == 0 {
		return processSample{}, fmt.Errorf("GetProcessTimes(%d): %v", pid, callErr)
	}
	cpuSeconds := float64(kernel.nanoseconds()+user.nanoseconds()) / 1e7

	sample := processSample{PID: pid, CPUSeconds: cpuSeconds}

	var counters struct {
		CB                  uint32
		PageFaultCount      uint32
		PeakWorkingSetSize  uintptr
		WorkingSetSize      uintptr
		QuotaPeakPagedPool  uintptr
		QuotaPagedPoolUsage uintptr
		QuotaPeakNonPaged   uintptr
		QuotaNonPagedPool   uintptr
		PagefileUsage       uintptr
		PeakPagefileUsage   uintptr
	}
	counters.CB = uint32(unsafe.Sizeof(counters))
	result, _, _ = procGetProcessMemoryInfo.Call(handle,
		uintptr(unsafe.Pointer(&counters)),
		uintptr(counters.CB))
	if result != 0 {
		sample.WorkingSetByte = uint64(counters.WorkingSetSize)
		sample.PeakWSBytes = uint64(counters.PeakWorkingSetSize)
	}
	return sample, nil
}

func sampleProcesses(pids []int) map[int]processSample {
	samples := make(map[int]processSample, len(pids))
	for _, pid := range pids {
		if pid <= 0 {
			continue
		}
		sample, err := sampleProcess(pid)
		if err != nil {
			continue
		}
		samples[pid] = sample
	}
	return samples
}

// cpuTracker samples a set of processes before and after a phase and reports the
// delta. It also samples the wall clock, so a caller can report CPU per byte and
// CPU per second without trusting two clocks.
type cpuTracker struct {
	pids   []int
	before map[int]processSample
	start  time.Time
}

func newCPUTracker(pids []int) *cpuTracker {
	return &cpuTracker{pids: pids, before: sampleProcesses(pids), start: time.Now()}
}

func (tracker *cpuTracker) finish() (map[string]processSample, float64) {
	elapsed := time.Since(tracker.start).Seconds()
	after := sampleProcesses(tracker.pids)
	delta := make(map[string]processSample, len(after))
	for pid, sample := range after {
		if previous, ok := tracker.before[pid]; ok {
			sample.CPUSeconds -= previous.CPUSeconds
			if sample.CPUSeconds < 0 {
				sample.CPUSeconds = 0
			}
		}
		delta[fmt.Sprintf("%d", pid)] = sample
	}
	return delta, elapsed
}

func runProcStat(args []string) error {
	flags := flag.NewFlagSet("procstat", flag.ExitOnError)
	var pids pidList
	flags.Var(&pids, "pid", "process id to sample (repeatable)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(pids) == 0 {
		return fmt.Errorf("procstat: no -pid given")
	}
	samples := make([]processSample, 0, len(pids))
	for _, pid := range pids {
		sample, err := sampleProcess(pid)
		if err != nil {
			return err
		}
		samples = append(samples, sample)
	}
	encoded, err := json.Marshal(map[string]any{"processes": samples})
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, string(encoded))
	return nil
}
