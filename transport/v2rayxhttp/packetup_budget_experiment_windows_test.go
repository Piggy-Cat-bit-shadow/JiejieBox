//go:build windows

package v2rayxhttp

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// processMemoryCounters mirrors PROCESS_MEMORY_COUNTERS from psapi.h. Only
// WorkingSetSize is read; the rest exists to size the struct the API expects.
type processMemoryCounters struct {
	cb                         uint32
	pageFaultCount             uint32
	peakWorkingSetSize         uintptr
	workingSetSize             uintptr
	quotaPeakPagedPoolUsage    uintptr
	quotaPagedPoolUsage        uintptr
	quotaPeakNonPagedPoolUsage uintptr
	quotaNonPagedPoolUsage     uintptr
	pagefileUsage              uintptr
	peakPagefileUsage          uintptr
}

var (
	psapiDLL                 = windows.NewLazySystemDLL("psapi.dll")
	getProcessMemoryInfoProc = psapiDLL.NewProc("GetProcessMemoryInfo")
)

// experimentWorkingSetBytes reports the process working set, which is what "RSS"
// means on Windows. x/sys does not wrap GetProcessMemoryInfo, so the call is made
// through the same lazy-DLL path it uses internally.
func experimentWorkingSetBytes() (uint64, bool) {
	counters := processMemoryCounters{cb: uint32(unsafe.Sizeof(processMemoryCounters{}))}
	ok, _, _ := getProcessMemoryInfoProc.Call(
		uintptr(windows.CurrentProcess()),
		uintptr(unsafe.Pointer(&counters)),
		uintptr(counters.cb),
	)
	if ok == 0 {
		return 0, false
	}
	return uint64(counters.workingSetSize), true
}
