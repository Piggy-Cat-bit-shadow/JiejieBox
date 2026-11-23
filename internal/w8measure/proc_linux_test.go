//go:build linux

package w8measure

// Linux process counters.
//
//	/proc/self/status VmRSS / VmHWM   resident set and the exact peak (VmHWM)
//	/proc/self/io   syscr / syscw     the number of read()/write() SYSCALLS the process issued
//	                                  (this is the strace -c equivalent without strace, which is
//	                                   not installed on this host; perf is not installed either)
//	/proc/self/fd                     open file descriptors

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

func w8ParseProcStatus(content string) map[string]uint64 {
	values := make(map[string]uint64)
	for _, line := range strings.Split(content, "\n") {
		name, rest, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		value, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		values[name] = value
	}
	return values
}

func readProcessMemory() (rss uint64, peakRSS uint64, ok bool) {
	content, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0, 0, false
	}
	values := w8ParseProcStatus(string(content))
	// Both are reported in kB.
	return values["VmRSS"] * 1024, values["VmHWM"] * 1024, true
}

func readProcessIO() (readOps uint64, writeOps uint64, readBytes uint64, writeBytes uint64, ok bool) {
	content, err := os.ReadFile("/proc/self/io")
	if err != nil {
		return 0, 0, 0, 0, false
	}
	values := make(map[string]uint64)
	for _, line := range strings.Split(string(content), "\n") {
		name, rest, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		value, parseErr := strconv.ParseUint(strings.TrimSpace(rest), 10, 64)
		if parseErr != nil {
			continue
		}
		values[name] = value
	}
	return values["syscr"], values["syscw"], values["rchar"], values["wchar"], true
}

func readHandleCount() (uint64, bool) {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return 0, false
	}
	return uint64(len(entries)), true
}

// diskFreeBytes reports the free bytes of the filesystem holding path. A tmpfs mounted
// for the ENOSPC sample reports its size here.
func diskFreeBytes(path string) int64 {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return -1
	}
	return int64(stat.Bavail) * int64(stat.Bsize)
}
