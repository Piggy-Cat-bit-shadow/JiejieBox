//go:build windows

package main

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows exposes nothing about its own thread and handle counts through Go's runtime metrics, and
// both are exactly the resources a per-node or per-connection leak hides in. The runtime metric
// /sched/threads/total:threads counts only the threads the GO RUNTIME created, so a cgo or Win32
// pool growing underneath it is invisible there. This file counts what the process actually owns.
//
// No cgo is involved: these are lazy DLL lookups, so the measurement binary stays CGO_ENABLED=0 and
// is built by the same toolchain profile the product uses.

var (
	modKernel32               = windows.NewLazySystemDLL("kernel32.dll")
	procGetProcessHandleCount = modKernel32.NewProc("GetProcessHandleCount")
	// K32GetProcessMemoryInfo lives in kernel32 on Windows 7 and later; GetProcessMemoryInfo is the
	// psapi.dll export it replaced. Both are tried, because which one exists depends on the Windows
	// version and neither is guaranteed by the Go runtime.
	procK32GetProcessMemoryInfo = modKernel32.NewProc("K32GetProcessMemoryInfo")
	modPSAPI                    = windows.NewLazySystemDLL("psapi.dll")
	procGetProcessMemoryInfo    = modPSAPI.NewProc("GetProcessMemoryInfo")
)

// processMemoryCounters mirrors PROCESS_MEMORY_COUNTERS. uintptr is SIZE_T, so the layout is correct
// on both 32- and 64-bit Windows without build tags.
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
	PrivateUsage               uintptr
}

// platformStaticNotes are the run-level statements this platform needs a reader to know: which
// values are host analogues and which resource a count really names.
func platformStaticNotes() map[string]string {
	return map[string]string{
		"process.file_descriptors":     "Windows handle count (K32 GetProcessHandleCount), not POSIX file descriptors",
		"process.threads":              "owner-process threads from a toolhelp snapshot, including threads the Go runtime did not create",
		"process.working_set_bytes":    "host working set: NOT an Apple phys_footprint and never to be quoted as one",
		"process.private_commit_bytes": "host private commit: NOT an Apple phys_footprint and never to be quoted as one",
	}
}

// windowsThreadCount walks the system thread snapshot and counts the ones owned by this process.
//
// A toolhelp snapshot can fail transiently (a process is created or destroyed while the snapshot is
// taken), so a failure is reported as "null with a reason" rather than retried into a
// plausible-looking number.
func windowsThreadCount() (*uint64, string) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return nil, "CreateToolhelp32Snapshot: " + err.Error()
	}
	defer func() { _ = windows.CloseHandle(snapshot) }()

	own := windows.GetCurrentProcessId()
	var entry windows.ThreadEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	count := uint64(0)
	for err = windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID == own {
			count++
		}
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return nil, "Thread32Next: " + err.Error()
	}
	return &count, ""
}

func windowsHandleCount() (*uint64, string) {
	// Find first: a LazyProc that is missing panics inside Call, and an instrument must not take the
	// measured process down because one counter is unavailable.
	if err := procGetProcessHandleCount.Find(); err != nil {
		return nil, "GetProcessHandleCount is not available: " + err.Error()
	}
	handle, err := windows.GetCurrentProcess()
	if err != nil {
		return nil, "GetCurrentProcess: " + err.Error()
	}
	var count uint32
	ok, _, callErr := procGetProcessHandleCount.Call(uintptr(handle), uintptr(unsafe.Pointer(&count)))
	if ok == 0 {
		return nil, "GetProcessHandleCount: " + callErr.Error()
	}
	value := uint64(count)
	return &value, ""
}

func windowsMemoryCounters() (workingSet *uint64, privateCommit *uint64, reason string) {
	handle, err := windows.GetCurrentProcess()
	if err != nil {
		return nil, nil, "GetCurrentProcess: " + err.Error()
	}
	var counters processMemoryCounters
	counters.CB = uint32(unsafe.Sizeof(counters))
	for _, procedure := range []*windows.LazyProc{procK32GetProcessMemoryInfo, procGetProcessMemoryInfo} {
		if err = procedure.Find(); err != nil {
			continue
		}
		ok, _, callErr := procedure.Call(
			uintptr(handle),
			uintptr(unsafe.Pointer(&counters)),
			uintptr(counters.CB),
		)
		if ok == 0 {
			return nil, nil, procedure.Name + ": " + callErr.Error()
		}
		working := uint64(counters.WorkingSetSize)
		private := uint64(counters.PrivateUsage)
		return &working, &private, ""
	}
	return nil, nil, "no GetProcessMemoryInfo entry point is available on this Windows version"
}

func readHostProcess(readings *processReadings, unsupported map[string]string) {
	readings.Supported = true
	threads, reason := windowsThreadCount()
	if threads != nil {
		readings.Threads = threads
	} else {
		unsupported["process.threads"] = reason
	}
	handles, reason := windowsHandleCount()
	if handles != nil {
		readings.FileDescriptors = handles
	} else {
		unsupported["process.file_descriptors"] = reason
	}
	workingSet, privateCommit, reason := windowsMemoryCounters()
	if workingSet != nil {
		readings.WorkingSetBytes = workingSet
		readings.PrivateCommitBytes = privateCommit
	} else {
		unsupported["process.working_set_bytes"] = reason
		unsupported["process.private_commit_bytes"] = reason
	}
}
