//go:build linux

package sampler

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	clkTckOnce sync.Once
	clkTckVal  int
)

func linuxClockTicks() int {
	clkTckOnce.Do(func() {
		clkTckVal = 100
		if out, err := exec.Command("getconf", "CLK_TCK").Output(); err == nil {
			if v, convErr := strconv.Atoi(strings.TrimSpace(string(out))); convErr == nil && v > 0 {
				clkTckVal = v
			}
		}
	})
	return clkTckVal
}

func readStat(pid int) (Stat, error) {
	st := Stat{At: time.Now()}

	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return st, err
	}
	text := string(raw)
	// The comm field is parenthesised and may itself contain spaces and
	// parentheses, so everything up to the LAST ')' is the process name.
	closeIdx := strings.LastIndexByte(text, ')')
	if closeIdx < 0 || closeIdx+2 >= len(text) {
		return st, os.ErrInvalid
	}
	fields := strings.Fields(text[closeIdx+2:])
	// fields[0] is state (field 3). utime is field 14.
	if len(fields) < 20 {
		return st, os.ErrInvalid
	}
	utime, _ := strconv.ParseUint(fields[11], 10, 64)
	stime, _ := strconv.ParseUint(fields[12], 10, 64)
	cutime, _ := strconv.ParseUint(fields[13], 10, 64)
	cstime, _ := strconv.ParseUint(fields[14], 10, 64)
	st.CPUTicks = utime + stime + cutime + cstime
	st.Threads, _ = strconv.Atoi(fields[17])
	st.ThreadsKnown = true
	st.StartTick, _ = strconv.ParseUint(fields[19], 10, 64)

	if status, statusErr := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status"); statusErr == nil {
		for _, line := range strings.Split(string(status), "\n") {
			key, value, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			value = strings.TrimSpace(value)
			switch key {
			case "VmRSS":
				if kb, convErr := parseKB(value); convErr == nil {
					st.RSSBytes = kb
				}
			case "VmHWM":
				if kb, convErr := parseKB(value); convErr == nil {
					st.HWMBytes = kb
				}
			case "Threads":
				if n, convErr := strconv.Atoi(value); convErr == nil {
					st.Threads = n
				}
			}
		}
	}

	if entries, readErr := os.ReadDir("/proc/" + strconv.Itoa(pid) + "/fd"); readErr == nil {
		st.FDs = len(entries)
		st.FDsKnown = true
	}

	if schedstat, schedErr := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/schedstat"); schedErr == nil {
		if fields := strings.Fields(string(schedstat)); len(fields) > 0 {
			st.CPUNanos, _ = strconv.ParseUint(fields[0], 10, 64)
		}
	}

	return st, nil
}

func parseKB(value string) (int64, error) {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return 0, os.ErrInvalid
	}
	kb, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return 0, err
	}
	return kb * 1024, nil
}
