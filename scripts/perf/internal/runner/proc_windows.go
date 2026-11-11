//go:build windows

package runner

import (
	"os"
	"syscall"
)

func sysProcAttr() *syscall.SysProcAttr {
	return nil
}

func signalGroup(pid int, force bool) {
	if pid <= 0 {
		return
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	_ = process.Kill()
}

func rusageOf(state *os.ProcessState) (userSeconds, sysSeconds float64, maxRSSBytes int64) {
	// The Windows sampler records CPU per repetition; there is no wait4 rusage
	// equivalent in the standard library.
	return 0, 0, 0
}
