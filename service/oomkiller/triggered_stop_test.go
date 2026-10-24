package oomkiller

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/log"

	"github.com/stretchr/testify/require"
)

// A poll that has decided Triggered must finish its destructive cleanup before stop() returns.
// The hook runs at the start of releaseMemory, so the test holds the cleanup inside the barrier
// and checks that stop() is still blocked. This is the deterministic negative control for the
// "stop returns, old poll keeps resetting the network" interleaving.
func TestStopWaitsForTriggeredPollCleanup(t *testing.T) {
	timer := &adaptiveTimer{
		timerConfig: timerConfig{
			policyMode:     policyModeMemoryLimit,
			killerDisabled: true,
			minInterval:    time.Second,
			maxInterval:    10 * time.Second,
		},
		logger: log.NewNOPFactory().Logger(),
		limitThresholds: pressureThresholds{
			trigger: 45 * 1024 * 1024,
			armed:   40 * 1024 * 1024,
			resume:  30 * 1024 * 1024,
		},
		timer:    time.NewTimer(time.Hour),
		pressure: &atomic.Uint32{},
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	timer.cleanupHook = func() {
		close(entered)
		<-release
	}
	timer.sampleFunc = func() memorySample { return idleSample(46) }
	t.Cleanup(func() {
		if timer.timer != nil {
			timer.timer.Stop()
		}
	})

	pollDone := make(chan struct{})
	go func() {
		timer.poll()
		close(pollDone)
	}()
	<-entered

	stopDone := make(chan struct{})
	go func() {
		timer.stop()
		close(stopDone)
	}()
	select {
	case <-stopDone:
		t.Fatal("stop returned while the Triggered poll's cleanup was still running")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	<-pollDone
	<-stopDone
	require.Equal(t, pressureStateTriggered, timer.state)
}
