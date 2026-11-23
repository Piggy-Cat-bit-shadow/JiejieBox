//go:build windows

package w8measure

// Windows process counters. These are the kernel's own numbers, not Go estimates:
//
//	GetProcessMemoryInfo -> WorkingSetSize / PeakWorkingSetSize  (RSS and exact peak RSS)
//	GetProcessIoCounters -> ReadOperationCount / WriteOperationCount
//	                        (read/write operations the process issued; the Windows analogue
//	                         of Linux syscr/syscw, and the only OS-level syscall counter
//	                         available on this host - neither strace nor perf is installed
//	                         in the WSL distribution on this machine)
//	GetProcessHandleCount -> open kernel handles (the FD analogue)

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	w8PSAPI = windows.NewLazySystemDLL("psapi.dll")

	w8ProcGetProcessMemoryInfo  = w8PSAPI.NewProc("GetProcessMemoryInfo")
	w8ProcGetProcessIoCounters  = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessIoCounters")
	w8ProcGetProcessHandleCount = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessHandleCount")
)

// w8ProcessMemoryCounters mirrors PROCESS_MEMORY_COUNTERS (72 bytes on amd64).
type w8ProcessMemoryCounters struct {
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

// w8IOCounters mirrors IO_COUNTERS.
type w8IOCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

func readProcessMemory() (rss uint64, peakRSS uint64, ok bool) {
	var counters w8ProcessMemoryCounters
	counters.CB = uint32(unsafe.Sizeof(counters))
	result, _, _ := w8ProcGetProcessMemoryInfo.Call(
		uintptr(windows.CurrentProcess()),
		uintptr(unsafe.Pointer(&counters)),
		uintptr(counters.CB),
	)
	if result == 0 {
		return 0, 0, false
	}
	return uint64(counters.WorkingSetSize), uint64(counters.PeakWorkingSetSize), true
}

func readProcessIO() (readOps uint64, writeOps uint64, readBytes uint64, writeBytes uint64, ok bool) {
	var counters w8IOCounters
	result, _, _ := w8ProcGetProcessIoCounters.Call(
		uintptr(windows.CurrentProcess()),
		uintptr(unsafe.Pointer(&counters)),
	)
	if result == 0 {
		return 0, 0, 0, 0, false
	}
	return counters.ReadOperationCount, counters.WriteOperationCount, counters.ReadTransferCount, counters.WriteTransferCount, true
}

func readHandleCount() (uint64, bool) {
	var count uint32
	result, _, _ := w8ProcGetProcessHandleCount.Call(
		uintptr(windows.CurrentProcess()),
		uintptr(unsafe.Pointer(&count)),
	)
	if result == 0 {
		return 0, false
	}
	return uint64(count), true
}

var w8ProcGetDiskFreeSpaceEx = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")

// diskFreeBytes reports the free bytes of the volume holding path. It exists to PROVE a
// bounded test filesystem is small before an ENOSPC sample is accepted.
func diskFreeBytes(path string) int64 {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return -1
	}
	var freeAvailable, total, totalFree uint64
	result, _, _ := w8ProcGetDiskFreeSpaceEx.Call(
		uintptr(unsafe.Pointer(pointer)),
		uintptr(unsafe.Pointer(&freeAvailable)),
		uintptr(unsafe.Pointer(&total)),
		uintptr(unsafe.Pointer(&totalFree)),
	)
	if result == 0 {
		return -1
	}
	return int64(freeAvailable)
}
