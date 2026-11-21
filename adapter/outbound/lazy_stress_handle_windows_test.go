//go:build windows

package outbound

// Process handle count for the long-loop leak check on Windows.
//
// A leaked socket or file is the resource a lifecycle bug produces that a heap count cannot see, and
// the LN long loop must record it per batch. syscall.NewLazyDLL is the stdlib path, so this costs the
// module no new dependency (go.sum is a shared file and must not move for a test).

import (
	"syscall"
	"unsafe"
)

var procGetProcessHandleCount = syscall.NewLazyDLL("kernel32.dll").NewProc("GetProcessHandleCount")

func processHandleCount() (uint32, bool) {
	var count uint32
	current, err := syscall.GetCurrentProcess()
	if err != nil {
		return 0, false
	}
	result, _, _ := procGetProcessHandleCount.Call(uintptr(current), uintptr(unsafe.Pointer(&count)))
	if result == 0 {
		return 0, false
	}
	return count, true
}
