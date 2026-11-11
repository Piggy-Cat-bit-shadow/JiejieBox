package runner

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/sagernet/sing-box/scripts/perf/internal/sampler"
)

// process is one proxy binary under measurement.
type process struct {
	name    string
	cmd     *exec.Cmd
	logPath string
	logFile *os.File

	waited  chan struct{}
	waitErr error
	state   *os.ProcessState
}

func startProcess(name, binary string, args []string, dir, logPath string) (*process, error) {
	return startProcessPrefix(name, nil, binary, args, dir, logPath)
}

// startProcessPrefix starts binary with an optional command prefix, which is how
// a process is moved into another network namespace without the runner needing
// privileges in that namespace.
func startProcessPrefix(name string, prefix []string, binary string, args []string, dir, logPath string) (*process, error) {
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, err
	}
	command := make([]string, 0, len(prefix)+1+len(args))
	command = append(command, prefix...)
	command = append(command, binary)
	command = append(command, args...)
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir = dir
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = sysProcAttr()
	if err = cmd.Start(); err != nil {
		logFile.Close()
		return nil, fmt.Errorf("start %s: %w", name, err)
	}
	p := &process{name: name, cmd: cmd, logPath: logPath, logFile: logFile, waited: make(chan struct{})}
	go func() {
		p.waitErr = cmd.Wait()
		p.state = cmd.ProcessState
		close(p.waited)
	}()
	return p, nil
}

func (p *process) pid() int {
	if p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

func (p *process) alive() bool {
	select {
	case <-p.waited:
		return false
	default:
		return true
	}
}

// stop terminates the process group and returns the exit code plus the
// authoritative rusage figures. Exit code 0 with zero usage means the process
// had already exited before stop was called on a platform without rusage.
func (p *process) stop(grace time.Duration) (exitCode int, userSeconds, sysSeconds float64, maxRSSBytes int64) {
	select {
	case <-p.waited:
	default:
		signalGroup(p.pid(), false)
		select {
		case <-p.waited:
		case <-time.After(grace):
			signalGroup(p.pid(), true)
			<-p.waited
		}
	}
	_ = p.logFile.Close()
	if p.state == nil {
		return 0, 0, 0, 0
	}
	exitCode = p.state.ExitCode()
	userSeconds, sysSeconds, maxRSSBytes = rusageOf(p.state)
	return exitCode, userSeconds, sysSeconds, maxRSSBytes
}

// tail returns the last lines of the process log, for a failure record.
func (p *process) tail(lines int) string {
	raw, err := os.ReadFile(p.logPath)
	if err != nil {
		return ""
	}
	split := splitLines(string(raw))
	if len(split) > lines {
		split = split[len(split)-lines:]
	}
	out := ""
	for _, line := range split {
		out += line + "\n"
	}
	return out
}

func splitLines(text string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			lines = append(lines, text[start:i])
			start = i + 1
		}
	}
	if start < len(text) {
		lines = append(lines, text[start:])
	}
	return lines
}

// cpuMeter reads exact CPU ticks at two boundaries. It exists because sampling
// costs a fixed interval of accuracy at each end, and a per-repetition CPU
// number should not inherit that.
type cpuMeter struct {
	pid   int
	first sampler.Stat
	ok    bool
}

func newCPUMeter(pid int) *cpuMeter {
	m := &cpuMeter{pid: pid}
	if pid > 0 {
		if st, err := sampler.Read(pid); err == nil {
			m.first = st
			m.ok = true
		}
	}
	return m
}

func (m *cpuMeter) finish() (cpuSeconds *float64, startRSS, endRSS *int64) {
	if !m.ok {
		return nil, nil, nil
	}
	last, err := sampler.Read(m.pid)
	if err != nil {
		return nil, nil, nil
	}
	if last.CPUTicks < m.first.CPUTicks {
		return nil, nil, nil
	}
	ticks := last.CPUTicks - m.first.CPUTicks
	seconds := float64(ticks) / float64(sampler.ClockTicks())
	start := m.first.RSSBytes
	end := last.RSSBytes
	return &seconds, &start, &end
}

// resolvedBinary returns the binary path for a side, defaulting to the lab
// binary.
func resolvedBinary(label string, binaries map[string]string) (string, error) {
	path := binaries[label]
	if path == "" {
		path = binaries["lab"]
	}
	if path == "" {
		return "", fmt.Errorf("no binary configured for %q", label)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if _, err = os.Stat(absolute); err != nil {
		return "", fmt.Errorf("binary for %q not found: %w", label, err)
	}
	return absolute, nil
}
