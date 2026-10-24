package oomkiller

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-tun"

	"github.com/stretchr/testify/require"
)

// newPollStopRaceTimer builds the smallest timer that reaches poll's decision without a network
// stack. A 41 MiB sample sits between armed (40) and trigger (45), so poll publishes Warning and
// takes the non-Triggered path - the path that used to be outside every stop barrier.
func newPollStopRaceTimer(pressure *atomic.Uint32, usage uint64) *adaptiveTimer {
	timer := &adaptiveTimer{
		timerConfig: timerConfig{
			policyMode:      policyModeMemoryLimit,
			minInterval:     time.Second,
			maxInterval:     10 * time.Second,
			memoryLimit:     50 * 1024 * 1024,
			safetyMargin:    5 * 1024 * 1024,
			hasSafetyMargin: true,
		},
		logger: log.NewNOPFactory().Logger(),
		limitThresholds: pressureThresholds{
			trigger: 45 * 1024 * 1024,
			armed:   40 * 1024 * 1024,
			resume:  30 * 1024 * 1024,
		},
		timer:           time.NewTimer(time.Hour),
		pressure:        pressure,
		lastConnections: 10,
	}
	timer.sampleFunc = func() memorySample { return idleSample(usage) }
	return timer
}

// TestNonTriggeredPollCannotPublishPressureAfterStop is the §2A regression.
//
// # The interleaving it forces
//
//	poll:   decision taken (Armed), lock released, <-- parked by the hook
//	stop:   timer = nil, Service wants to publish None
//	poll:   telemetry still running
//
// The non-Triggered poll did not join pressureCallbacks, so nothing joined on the work after the
// lock. Close could return, publish None, and then the old poll rewrote it to Warning. The
// Triggered path was fixed for exactly this; the ordinary path was left uncovered.
//
// The fixed contract has two parts, both asserted here:
//
//   - stop() does NOT return while an accepted poll is still in its post-decision window, so
//     Service.stopTimer cannot publish None before that poll has finished;
//   - the pressure state that poll publishes is written under the lifecycle lock, before it gives
//     the window up, so it can never land after None.
func TestNonTriggeredPollCannotPublishPressureAfterStop(t *testing.T) {
	pressure := &atomic.Uint32{}
	timer := newPollStopRaceTimer(pressure, 41)
	t.Cleanup(func() {
		if timer.timer != nil {
			timer.timer.Stop()
		}
	})

	decisionTaken := make(chan struct{})
	releasePoll := make(chan struct{})
	timer.afterDecisionHook = func() {
		close(decisionTaken)
		<-releasePoll
	}

	pollDone := make(chan struct{})
	go func() {
		defer close(pollDone)
		timer.poll()
	}()

	select {
	case <-decisionTaken:
	case <-time.After(5 * time.Second):
		t.Fatal("the poll never reached its post-decision window")
	}

	stopDone := make(chan struct{})
	go func() {
		defer close(stopDone)
		timer.stop()
	}()

	select {
	case <-stopDone:
		t.Fatal("stop returned while an accepted non-Triggered poll was still running; " +
			"its telemetry and queued work are outside the lifecycle barrier")
	case <-time.After(100 * time.Millisecond):
		// Correct: stop is waiting for the poll.
	}

	close(releasePoll)
	select {
	case <-pollDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the poll did not finish after being released")
	}
	select {
	case <-stopDone:
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not return after the poll finished")
	}

	// Service.stopTimer publishes None after stop() returns. The old poll has already finished,
	// so this write is the last one.
	pressure.Store(uint32(tun.MemoryPressureNone))
	require.Equal(t, uint32(tun.MemoryPressureNone), pressure.Load(),
		"a non-Triggered poll published pressure outside the barrier")
}
