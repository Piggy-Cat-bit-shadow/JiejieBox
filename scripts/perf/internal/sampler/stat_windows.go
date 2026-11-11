//go:build windows

package sampler

import (
	"errors"
	"syscall"
	"time"
	"unsafe"
)

// Windows reports process CPU in 100 ns units, so the collector's tick clock is
// set to 10,000,000 to make ticks/clockTicks() seconds.
const windowsClockTicks = 10_000_000

func linuxClockTicks() int { return windowsClockTicks }

var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess          = kernel32.NewProc("OpenProcess")
	procGetProcessTimes      = kernel32.NewProc("GetProcessTimes")
	procCloseHandle          = kernel32.NewProc("CloseHandle")
	psapi                    = syscall.NewLazyDLL("psapi.dll")
	procGetProcessMemoryInfo = psapi.NewProc("GetProcessMemoryInfo")
)

type processMemoryCounters struct {
	CB                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

func readStat(pid int) (Stat, error) {
	st := Stat{At: time.Now()}
	const processQueryLimitedInformation = 0x1000
	handle, _, err := procOpenProcess.Call(processQueryLimitedInformation, 0, uintptr(pid))
	if handle == 0 {
		return st, err
	}
	defer procCloseHandle.Call(handle)

	var creation, exit, kernelTime, userTime syscall.Filetime
	ok, _, callErr := procGetProcessTimes.Call(handle,
		uintptr(unsafe.Pointer(&creation)),
		uintptr(unsafe.Pointer(&exit)),
		uintptr(unsafe.Pointer(&kernelTime)),
		uintptr(unsafe.Pointer(&userTime)))
	if ok == 0 {
		return st, callErr
	}
	st.CPUTicks = filetimeTicks(kernelTime) + filetimeTicks(userTime)

	var counters processMemoryCounters
	counters.CB = uint32(unsafe.Sizeof(counters))
	ok, _, _ = procGetProcessMemoryInfo.Call(handle, uintptr(unsafe.Pointer(&counters)), uintptr(counters.CB))
	if ok != 0 {
		st.RSSBytes = int64(counters.WorkingSetSize)
		st.HWMBytes = int64(counters.PeakWorkingSetSize)
	} else {
		return st, errors.New("GetProcessMemoryInfo failed")
	}
	return st, nil
}

func filetimeTicks(ft syscall.Filetime) uint64 {
	return uint64(ft.HighDateTime)<<32 | uint64(ft.LowDateTime)
}
