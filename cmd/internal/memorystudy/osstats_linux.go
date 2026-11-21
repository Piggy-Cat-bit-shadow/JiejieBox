//go:build linux

package main

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// Linux gives the process its own counters in /proc/self, which is both cheaper and more accurate
// than any snapshot: threads, resident set and the open-descriptor table are all one read away.
//
// /proc/self/status is used rather than /proc/self/statm because statm's resident field is in pages
// and its meaning differs under some kernels; status names the value it is reporting.

func platformStaticNotes() map[string]string {
	return map[string]string{
		"process.file_descriptors":     "entries in /proc/self/fd; the directory read itself holds one descriptor",
		"process.threads":              "Threads: from /proc/self/status, including threads the Go runtime did not create",
		"process.working_set_bytes":    "RSS from /proc/self/status: host resident set, NOT an Apple phys_footprint",
		"process.private_commit_bytes": "VmSize from /proc/self/status: virtual size, NOT private commit and NOT a footprint",
	}
}

func readHostProcess(readings *processReadings, unsupported map[string]string) {
	readings.Supported = true

	status, err := os.Open("/proc/self/status")
	if err != nil {
		unsupported["process.threads"] = "/proc/self/status: " + err.Error()
		unsupported["process.working_set_bytes"] = "/proc/self/status: " + err.Error()
		unsupported["process.private_commit_bytes"] = "/proc/self/status: " + err.Error()
	} else {
		scanner := bufio.NewScanner(status)
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, "Threads:"):
				readings.Threads = parseStatusKilobytes(line, 1)
			case strings.HasPrefix(line, "VmRSS:"):
				readings.WorkingSetBytes = parseStatusKilobytes(line, 1024)
			case strings.HasPrefix(line, "VmSize:"):
				readings.PrivateCommitBytes = parseStatusKilobytes(line, 1024)
			}
		}
		_ = status.Close()
		if readings.Threads == nil {
			unsupported["process.threads"] = "/proc/self/status has no Threads field"
		}
		if readings.WorkingSetBytes == nil {
			unsupported["process.working_set_bytes"] = "/proc/self/status has no VmRSS field"
		}
		if readings.PrivateCommitBytes == nil {
			unsupported["process.private_commit_bytes"] = "/proc/self/status has no VmSize field"
		}
	}

	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		unsupported["process.file_descriptors"] = "/proc/self/fd: " + err.Error()
		return
	}
	count := uint64(len(entries))
	readings.FileDescriptors = &count
}

// parseStatusKilobytes reads the first number out of a "Name:\t1234 kB" status line and applies the
// unit multiplier. A line that does not parse yields nil, which becomes an explicit null rather
// than a zero.
func parseStatusKilobytes(line string, multiplier uint64) *uint64 {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return nil
	}
	value, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return nil
	}
	value *= multiplier
	return &value
}
