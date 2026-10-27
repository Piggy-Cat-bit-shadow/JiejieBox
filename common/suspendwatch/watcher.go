package suspendwatch

import (
	"sync"
	"sync/atomic"
	"time"
)

// DefaultInterval is how often the watcher takes a reading. It is a watchdog, not a poll of any
// service: one comparison of two clocks every few seconds, and on macOS only. The bound it places on
// recovery is therefore "at most one interval after the host wakes", which is why it is short enough
// to matter to a person and long enough not to be noise.
const DefaultInterval = 5 * time.Second

// DefaultThreshold is the divergence that counts as a suspend. A five-second gap cannot be produced
// by ordinary scheduler latency (which advances both clocks together), and it is below the shortest
// sleep a user would notice as "the lid was closed for a while".
const DefaultThreshold = 5 * time.Second

// Watcher runs a Detector on a timer and calls a callback when the host resumes.
//
// It owns exactly one timer. Close stops it and waits for the goroutine, so a shutdown cannot leave
// a callback that races a torn-down manager.
type Watcher struct {
	interval time.Duration
	detector Detector
	now      func() (time.Time, time.Duration)

	startOnce sync.Once
	closeOnce sync.Once
	started   atomic.Bool
	stop      chan struct{}
	done      chan struct{}
}

// New returns a watcher that reads the real wall and monotonic clocks.
func New(interval, threshold time.Duration) *Watcher {
	return newWatcher(interval, threshold, realClock())
}

// newWatcher is the test seam: both readings are supplied by the caller.
func newWatcher(interval, threshold time.Duration, now func() (time.Time, time.Duration)) *Watcher {
	if interval <= 0 {
		interval = DefaultInterval
	}
	if threshold <= 0 {
		threshold = DefaultThreshold
	}
	return &Watcher{
		interval: interval,
		detector: Detector{Threshold: threshold},
		now:      now,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// realClock returns the wall clock with its monotonic reading stripped, and a monotonic reading from
// the same instant. Stripping the monotonic part from the wall value is what makes the subtraction
// in Detector use the wall clock rather than silently preferring the monotonic one.
func realClock() func() (time.Time, time.Duration) {
	start := time.Now()
	return func() (time.Time, time.Duration) {
		return time.Now().Round(0), time.Since(start)
	}
}

// Start launches the watcher. Calling it more than once is a no-op; Close must be called to stop it.
//
// The first reading is taken SYNCHRONOUSLY, before the goroutine starts. If the baseline were taken
// on the first ticker tick, a host that suspends immediately after Start would wake before the
// baseline existed, and that first suspend would be invisible. The clock read is not concurrent with
// the ticker: the ticker does not exist until after this call.
func (w *Watcher) Start(onResume func(gap time.Duration)) {
	w.startOnce.Do(func() {
		w.started.Store(true)
		wall, mono := w.now()
		w.detector.Observe(wall, mono)
		go w.run(onResume)
	})
}

// Close stops the watcher and waits for its goroutine. It is idempotent.
func (w *Watcher) Close() {
	w.closeOnce.Do(func() {
		close(w.stop)
	})
	if w.started.Load() {
		<-w.done
	}
}

func (w *Watcher) run(onResume func(gap time.Duration)) {
	defer close(w.done)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-w.stop:
			return
		case <-ticker.C:
			wall, mono := w.now()
			resumed, gap := w.detector.Observe(wall, mono)
			if resumed && onResume != nil {
				onResume(gap)
			}
		}
	}
}
