//go:build windows

package main

import (
	"fmt"
	"syscall"
)

// Process placement: affinity and priority, so a measurement is not decided by
// which core the scheduler happened to pick.
//
// The four roles of a cell are given disjoint core sets (the load generator, the
// tunnel client, the reference server and the helper processes), and the three
// that are on the measured path are raised to high priority. Both clients get
// the same placement, so the comparison stays fair even where the placement is
// imperfect.

const (
	priorityHigh        = 0x00000080 // HIGH_PRIORITY_CLASS
	priorityAboveNormal = 0x00008000
	priorityNormal      = 0x00000020
)

const (
	processSetInformation   = 0x0200
	processQueryInformation = 0x0400

	createNoWindow = 0x08000000
)

var (
	kernel32Process        = syscall.NewLazyDLL("kernel32.dll")
	procOpenForPlacement   = kernel32Process.NewProc("OpenProcess")
	procClosePlacement     = kernel32Process.NewProc("CloseHandle")
	procSetProcessAffinity = kernel32Process.NewProc("SetProcessAffinityMask")
	procSetPriorityClass   = kernel32Process.NewProc("SetPriorityClass")
)

func configureProcess(pid int, affinity uintptr, priority uint32) error {
	if pid <= 0 {
		return fmt.Errorf("configureProcess: pid %d", pid)
	}
	handle, _, callErr := procOpenForPlacement.Call(processSetInformation|processQueryInformation, 0, uintptr(uint32(pid)))
	if handle == 0 {
		return fmt.Errorf("OpenProcess(%d): %v", pid, callErr)
	}
	defer procClosePlacement.Call(handle)
	if affinity != 0 {
		if result, _, callErr := procSetProcessAffinity.Call(handle, affinity); result == 0 {
			return fmt.Errorf("SetProcessAffinityMask(%d, %#x): %v", pid, affinity, callErr)
		}
	}
	if priority != 0 {
		if result, _, callErr := procSetPriorityClass.Call(handle, uintptr(priority)); result == 0 {
			return fmt.Errorf("SetPriorityClass(%d, %#x): %v", pid, priority, callErr)
		}
	}
	return nil
}

// backgroundProcessAttributes keeps a child process from opening a console
// window, which on Windows would steal focus and perturb the measurement.
func backgroundProcessAttributes() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
