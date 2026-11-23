//go:build windows

package wireguard

// Process handle count for the W5/W6 resource ledgers on Windows.
//
// syscall.NewLazyDLL is the stdlib path, so this costs the module no new dependency (go.sum is a
// shared file and must not move for a test). Handles are not POSIX file descriptors; the ledger
// labels which one it measured.

import (
	"syscall"
	"unsafe"
)

var procGetProcessHandleCount = syscall.NewLazyDLL("kernel32.dll").NewProc("GetProcessHandleCount")

func processFDCount() (int, bool) {
	var count uint32
	current, err := syscall.GetCurrentProcess()
	if err != nil {
		return 0, false
	}
	result, _, _ := procGetProcessHandleCount.Call(uintptr(current), uintptr(unsafe.Pointer(&count)))
	if result == 0 {
		return 0, false
	}
	return int(count), true
}
