//go:build !windows

package runner

import (
	"os"
	"syscall"
)

func sysProcAttr() *syscall.SysProcAttr {
	// A process group lets stop() reach children the proxy may have spawned,
	// instead of leaving a reporter or a native engine alive after the parent.
	return &syscall.SysProcAttr{Setpgid: true}
}

func signalGroup(pid int, force bool) {
	if pid <= 0 {
		return
	}
	signal := syscall.SIGTERM
	if force {
		signal = syscall.SIGKILL
	}
	_ = syscall.Kill(-pid, signal)
}

func rusageOf(state *os.ProcessState) (userSeconds, sysSeconds float64, maxRSSBytes int64) {
	usage, ok := state.SysUsage().(*syscall.Rusage)
	if !ok {
		return 0, 0, 0
	}
	userSeconds = float64(usage.Utime.Sec) + float64(usage.Utime.Usec)/1e6
	sysSeconds = float64(usage.Stime.Sec) + float64(usage.Stime.Usec)/1e6
	maxRSSBytes = int64(usage.Maxrss) * 1024
	return userSeconds, sysSeconds, maxRSSBytes
}
