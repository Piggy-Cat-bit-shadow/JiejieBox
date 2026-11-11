// Package sampler turns a running process into the resource numbers the schema
// asks for.
//
// The design has one deliberate asymmetry: periodic sampling exists to show a
// time series (peak RSS, peak threads, peak FDs) and to attribute CPU to a
// phase, while the *authoritative* CPU total comes from wait4 rusage when the
// child is reaped. A sampled total misses the last interval and a fast exit
// altogether; rusage does not. ProcSummary records which one a number came
// from.
package sampler

import (
	"runtime"
	"sync"
	"time"
)

// Stat is one point-in-time reading.
type Stat struct {
	CPUTicks uint64 // utime+stime+cutime+cstime, in clock ticks
	CPUNanos uint64 // from /proc/<pid>/schedstat, 0 when unavailable
	RSSBytes int64
	HWMBytes int64
	Threads  int
	FDs      int
	// ThreadsKnown and FDsKnown say whether the two numbers above were actually
	// measured. A platform that cannot see them must leave the flag false so the
	// summary reports null; a zero from an unsupported probe is not a measurement.
	ThreadsKnown bool
	FDsKnown     bool
	StartTick    uint64 // process creation time; detects PID reuse. /proc field 22, or the Windows creation FILETIME
	At           time.Time
}

// Summary is the reduction of a sampling window.
type Summary struct {
	Samples      int
	CPUSeconds   *float64
	RSSStart     *int64
	RSSEnd       *int64
	RSSPeak      *int64
	ThreadsPeak  *int
	FDsPeak      *int
	CPUSource    string
	ClockTicks   int
	FirstAt      time.Time
	LastAt       time.Time
	PIDReused    bool
	ReadFailures int
}

// Collector samples one process until Stop.
type Collector struct {
	pid      int
	interval time.Duration
	clkTck   int

	mu       sync.Mutex
	stats    []Stat
	first    Stat
	last     Stat
	failures int
	reused   bool
	started  bool

	stop chan struct{}
	done chan struct{}
}

// Read reads one process once. It is safe to call for a process that is gone;
// the error then says so.
func Read(pid int) (Stat, error) {
	return readStat(pid)
}

// Start begins sampling pid. interval <= 0 selects a default.
func Start(pid int, interval time.Duration) *Collector {
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	c := &Collector{pid: pid, interval: interval, clkTck: clockTicks(), stop: make(chan struct{}), done: make(chan struct{})}
	// Take the first reading synchronously so RSSStart is the state before any
	// measured traffic, not the state after the first interval.
	if st, err := readStat(pid); err == nil {
		c.absorb(st)
	} else {
		c.failures++
	}
	go c.loop()
	return c
}

func (c *Collector) loop() {
	defer close(c.done)
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-c.stop:
			return
		case <-ticker.C:
			st, err := readStat(c.pid)
			c.mu.Lock()
			if err != nil {
				c.failures++
			} else {
				c.absorbLocked(st)
			}
			c.mu.Unlock()
		}
	}
}

func (c *Collector) absorb(st Stat) {
	c.mu.Lock()
	c.absorbLocked(st)
	c.mu.Unlock()
}

func (c *Collector) absorbLocked(st Stat) {
	if c.started && st.StartTick != 0 && c.first.StartTick != 0 && st.StartTick != c.first.StartTick {
		c.reused = true
	}
	if !c.started {
		c.first = st
		c.started = true
	}
	c.last = st
	c.stats = append(c.stats, st)
}

// Stop ends sampling and returns the reduction. It is idempotent.
func (c *Collector) Stop() Summary {
	select {
	case <-c.stop:
	default:
		close(c.stop)
	}
	<-c.done

	c.mu.Lock()
	defer c.mu.Unlock()
	s := Summary{Samples: len(c.stats), ClockTicks: c.clkTck, CPUSource: "proc-sampled", PIDReused: c.reused, ReadFailures: c.failures}
	if len(c.stats) == 0 {
		return s
	}
	s.FirstAt = c.first.At
	s.LastAt = c.last.At
	start, end := c.first.RSSBytes, c.last.RSSBytes
	s.RSSStart, s.RSSEnd = &start, &end
	var peakRSS int64
	var peakThreads, peakFDs int
	threadsMeasured, fdsMeasured := false, false
	cpuTicks := c.first.CPUTicks
	if c.last.CPUTicks >= cpuTicks {
		cpuTicks = c.last.CPUTicks - cpuTicks
	} else {
		cpuTicks = 0
	}
	for _, st := range c.stats {
		if st.HWMBytes > peakRSS {
			peakRSS = st.HWMBytes
		}
		if st.RSSBytes > peakRSS {
			peakRSS = st.RSSBytes
		}
		if st.ThreadsKnown && (!threadsMeasured || st.Threads > peakThreads) {
			peakThreads = st.Threads
			threadsMeasured = true
		}
		if st.FDsKnown && (!fdsMeasured || st.FDs > peakFDs) {
			peakFDs = st.FDs
			fdsMeasured = true
		}
	}
	s.RSSPeak = &peakRSS
	// Leave a peak nil when no sample measured it: the schema and the report
	// render nil as null, which is the honest value for "not observable here".
	if threadsMeasured {
		s.ThreadsPeak = &peakThreads
	}
	if fdsMeasured {
		s.FDsPeak = &peakFDs
	}
	if tick := seconds(cpuTicks, c.clkTck); tick != nil {
		s.CPUSeconds = tick
	}
	return s
}

// ApplyRusage merges a sampled summary with the authoritative rusage figures.
// CPUSeconds from rusage wins; the sampled value is kept only when rusage is
// unavailable, and CPUSource says which happened.
func (s *Summary) ApplyRusage(userSeconds, sysSeconds float64, maxRSSBytes int64) {
	total := userSeconds + sysSeconds
	s.CPUSeconds = &total
	s.CPUSource = "wait4-rusage"
	if maxRSSBytes > 0 {
		if s.RSSPeak == nil || maxRSSBytes > *s.RSSPeak {
			s.RSSPeak = &maxRSSBytes
		}
	}
}

func seconds(ticks uint64, clkTck int) *float64 {
	if clkTck <= 0 || ticks == 0 {
		zero := 0.0
		if ticks == 0 && clkTck > 0 {
			return &zero
		}
		return nil
	}
	v := float64(ticks) / float64(clkTck)
	return &v
}

func clockTicks() int {
	switch runtime.GOOS {
	case "linux", "windows":
		return linuxClockTicks()
	default:
		return 0
	}
}

// ClockTicks reports the clock frequency /proc/<pid>/stat counts CPU in, or 0
// when the platform has no such clock.
func ClockTicks() int { return clockTicks() }

// readStat is defined per platform in stat_*.go.
// linuxClockTicks is defined in stat_linux.go.
