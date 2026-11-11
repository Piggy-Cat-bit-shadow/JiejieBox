// Package host collects the facts that make a number reproducible: the code,
// the toolchain, the machine, and the network namespace the traffic crossed.
package host

import (
	"bufio"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/sagernet/sing-box/scripts/perf/internal/schema"
)

// Info describes the machine the lab runs on.
func Info() schema.Host {
	h := schema.Host{OS: runtime.GOOS, Arch: runtime.GOARCH, SamplingIntervalMS: 100}
	h.CPUModel, h.CPUCores = cpuInfo()
	h.Kernel = readTrimmed("/proc/sys/kernel/osrelease")
	if h.Kernel == "" {
		h.Kernel = unameRelease()
	}
	h.Distro, h.DistroVersion = distro()
	h.WSL = strings.Contains(strings.ToLower(h.Kernel), "microsoft")
	if h.WSL {
		h.WSLKernelRelease = h.Kernel
	}
	h.Glibc = glibcVersion()
	if total := memTotal(); total > 0 {
		h.MemTotalBytes = &total
	}
	if ticks := clockTicks(); ticks > 0 {
		h.ClkTck = &ticks
	}
	h.SampleLimitNote = "samples are point reads every sampling_interval_ms; a spike shorter than one interval can be missed, and the last interval before exit is covered by wait4 rusage rather than by sampling"
	return h
}

func cpuInfo() (string, int) {
	file, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return "", runtime.NumCPU()
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	model := ""
	cores := 0
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "model name") && model == "" {
			if _, value, ok := strings.Cut(line, ":"); ok {
				model = strings.TrimSpace(value)
			}
		}
		if strings.HasPrefix(line, "processor") {
			cores++
		}
	}
	if cores == 0 {
		cores = runtime.NumCPU()
	}
	return model, cores
}

func distro() (string, string) {
	file, err := os.Open("/etc/os-release")
	if err != nil {
		return "", ""
	}
	defer file.Close()
	name, version := "", ""
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if !ok {
			continue
		}
		value = strings.Trim(value, `"`)
		switch key {
		case "NAME":
			name = value
		case "VERSION_ID":
			version = value
		}
	}
	return name, version
}

func glibcVersion() string {
	out, err := exec.Command("getconf", "GNU_LIBC_VERSION").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func memTotal() uint64 {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if !strings.HasPrefix(scanner.Text(), "MemTotal:") {
			continue
		}
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			return 0
		}
		kb, convErr := strconv.ParseUint(fields[1], 10, 64)
		if convErr != nil {
			return 0
		}
		return kb * 1024
	}
	return 0
}

func clockTicks() int {
	out, err := exec.Command("getconf", "CLK_TCK").Output()
	if err != nil {
		return 0
	}
	ticks, convErr := strconv.Atoi(strings.TrimSpace(string(out)))
	if convErr != nil {
		return 0
	}
	return ticks
}

func readTrimmed(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func unameRelease() string {
	out, err := exec.Command("uname", "-r").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
