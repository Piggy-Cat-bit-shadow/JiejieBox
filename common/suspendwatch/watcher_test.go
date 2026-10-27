package suspendwatch

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestTheDetectorSeesOnlyTheDivergence is the whole decision, driven rather than slept through.
func TestTheDetectorSeesOnlyTheDivergence(t *testing.T) {
	base := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	detector := Detector{Threshold: 5 * time.Second}

	// The first reading only primes: there is no previous instant to compare against.
	resumed, gap := detector.Observe(base, 0)
	require.False(t, resumed)
	require.Zero(t, gap)

	// Both clocks advance together while the process runs. A delayed callback is not a suspend.
	resumed, gap = detector.Observe(base.Add(15*time.Second), 15*time.Second)
	require.False(t, resumed, "a late tick advances both clocks and must not look like a resume")
	require.Zero(t, gap)

	// The wall advanced a minute while the monotonic clock advanced five seconds: the process was
	// not running for the other fifty-five.
	resumed, gap = detector.Observe(base.Add(75*time.Second), 20*time.Second)
	require.True(t, resumed)
	require.Equal(t, 55*time.Second, gap, "the gap is the part of the wall advance the monotonic clock did not see")

	// The reading that follows a resume is a normal one again: the event is an edge, not a state.
	resumed, _ = detector.Observe(base.Add(80*time.Second), 25*time.Second)
	require.False(t, resumed, "a suspend must be reported once, not on every later tick")
}

// TestTheThresholdIsTheBoundary pins the comparison, because it is the only number the watcher has.
func TestTheThresholdIsTheBoundary(t *testing.T) {
	base := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	detector := Detector{Threshold: 5 * time.Second}
	detector.Observe(base, 0)

	resumed, gap := detector.Observe(base.Add(10*time.Second), 5*time.Second)
	require.True(t, resumed, "exactly the threshold is a resume; the comparison is inclusive")
	require.Equal(t, 5*time.Second, gap)

	detector.Reset()
	detector.Observe(base, 0)
	resumed, _ = detector.Observe(base.Add(10*time.Second), 5*time.Second+1)
	require.False(t, resumed, "one nanosecond below the threshold is not a resume")
}

// TestABackwardClockStepIsNotASuspend covers NTP and a manual clock change. A reset caused by a
// clock correction would be a false positive on a network that never moved.
func TestABackwardClockStepIsNotASuspend(t *testing.T) {
	base := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	detector := Detector{Threshold: 5 * time.Second}
	detector.Observe(base, 10*time.Second)

	resumed, _ := detector.Observe(base.Add(-time.Minute), 15*time.Second)
	require.False(t, resumed, "a wall clock that went backwards is not evidence of a suspend")

	// The base was discarded, so the reading after the step primes instead of comparing against the
	// pre-step value. Keeping it would make the correction forward look like a suspend.
	resumed, gap := detector.Observe(base, 20*time.Second)
	require.False(t, resumed, "the backwards step itself must not leave a pending verdict")
	require.Zero(t, gap)

	// And the detector is usable again from there: a steady pair of readings is a normal tick.
	resumed, gap = detector.Observe(base.Add(10*time.Second), 30*time.Second)
	require.False(t, resumed)
	require.Zero(t, gap)
}

// TestTheWatcherReportsOnceAndStops keeps the goroutine and the timer honest: one callback for one
// divergence, and Close joins the goroutine.
func TestTheWatcherReportsOnceAndStops(t *testing.T) {
	var (
		access  sync.Mutex
		wall    = time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
		mono    time.Duration
		reports int
		lastGap time.Duration
		reads   atomic.Int32
	)
	watcher := newWatcher(time.Millisecond, time.Second, func() (time.Time, time.Duration) {
		reads.Add(1)
		access.Lock()
		defer access.Unlock()
		return wall, mono
	})
	watcher.Start(func(gap time.Duration) {
		access.Lock()
		defer access.Unlock()
		reports++
		lastGap = gap
	})
	t.Cleanup(watcher.Close)

	// Wait for the first reading rather than sleeping: the detector primes on it, and a loaded
	// runner must not be able to make the priming read arrive after the wall change.
	require.Eventually(t, func() bool {
		return reads.Load() >= 1
	}, 2*time.Second, time.Millisecond, "the watcher never took its first reading")

	access.Lock()
	wall = wall.Add(time.Minute)
	mono += 10 * time.Millisecond
	access.Unlock()

	require.Eventually(t, func() bool {
		access.Lock()
		defer access.Unlock()
		return reports == 1
	}, 2*time.Second, 2*time.Millisecond, "the watcher did not report the divergence")

	access.Lock()
	require.Equal(t, time.Minute-10*time.Millisecond, lastGap)
	access.Unlock()

	// Further steady ticks must not report again.
	time.Sleep(10 * time.Millisecond)
	access.Lock()
	require.Equal(t, 1, reports)
	access.Unlock()

	watcher.Close()
	require.False(t, watcher.started.Load() && !isClosed(watcher.done), "Close must join the watcher goroutine")
}

func isClosed(channel <-chan struct{}) bool {
	select {
	case <-channel:
		return true
	default:
		return false
	}
}

// TestCloseWithoutStartDoesNotBlock covers a manager that is closed before PostStart ran the watcher.
func TestCloseWithoutStartDoesNotBlock(t *testing.T) {
	watcher := newWatcher(time.Millisecond, time.Second, realClock())
	done := make(chan struct{})
	go func() {
		watcher.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close blocked although Start was never called")
	}
}

// TestTheWatcherIsIdempotentUnderConcurrentClose drives the two-call contract the manager relies on:
// the scope cleanup and a direct stop can both arrive.
func TestTheWatcherIsIdempotentUnderConcurrentClose(t *testing.T) {
	watcher := newWatcher(time.Millisecond, time.Second, realClock())
	watcher.Start(nil)
	var closed atomic.Int32
	var waitGroup sync.WaitGroup
	for i := 0; i < 4; i++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			watcher.Close()
			closed.Add(1)
		}()
	}
	waitGroup.Wait()
	require.EqualValues(t, 4, closed.Load())
}
