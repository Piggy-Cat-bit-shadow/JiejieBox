package oomkiller

import (
	"context"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/log"

	"github.com/stretchr/testify/require"
)

// newIdleReleaseTimer builds the smallest timer that reaches releaseIdleMemory's decision. The
// network manager is nil; the trim and FreeOSMemory executors are injected so the test can count
// executions without a real GC or a real connection pool.
func newIdleReleaseTimer(t *testing.T) (*adaptiveTimer, *int, *int) {
	t.Helper()
	trims := 0
	frees := 0
	timer := &adaptiveTimer{
		timerConfig: timerConfig{policyMode: policyModeMemoryLimit},
		logger:      log.NewNOPFactory().Logger(),
		limitThresholds: pressureThresholds{
			resume:  30 * 1024 * 1024,
			armed:   40 * 1024 * 1024,
			trigger: 45 * 1024 * 1024,
		},
		timer:           time.NewTimer(time.Hour),
		lastGoroutines:  runtime.NumGoroutine(),
		lastConnections: 10,
		trimMemory:      func(context.Context) { trims++ },
		freeOSMemory:    func() { frees++ },
		// The runtime-evidence gate below the armed line would otherwise skip the forced GC on the
		// machine running the test, whose real free-span figure is not the 33 MiB fixture's. These
		// tests assert the release decision, so they state the evidence explicitly.
		runtimeReclaimable: func() uint64 { return defaultIdleBenefitFloor },
	}
	t.Cleanup(func() {
		if timer.timer != nil {
			timer.timer.Stop()
		}
	})
	return timer, &trims, &frees
}

func idleSample(mib uint64) memorySample {
	return memorySample{usage: mib * 1024 * 1024}
}

// The 33 MB / one-second loop: a stable footprint with no drop and no pressure must not run
// TrimMemory or FreeOSMemory at all.
func TestIdleReleaseDoesNotRepeatAtAStableFootprint(t *testing.T) {
	timer, trims, frees := newIdleReleaseTimer(t)
	for range 5 {
		timer.releaseIdleMemory(idleSample(33), 10)
	}
	require.Zero(t, *trims, "a stable 33 MB footprint has no reclaimable opportunity")
	require.Zero(t, *frees, "no forced GC without an opportunity")
}

// A real drop since the previous sample is an opportunity: exactly one release, then the
// low-yield cooldown suppresses the next identical sample.
func TestIdleReleaseRunsOnceForARealDropThenCoolsDown(t *testing.T) {
	timer, trims, frees := newIdleReleaseTimer(t)
	timer.lastUsage = 35 * 1024 * 1024

	timer.releaseIdleMemory(idleSample(33), 10)
	require.Equal(t, 1, *trims)
	require.Equal(t, 1, *frees)

	timer.releaseIdleMemory(idleSample(33), 10)
	require.Equal(t, 1, *trims, "the cooldown must suppress a repeat at the same footprint")
	require.Equal(t, 1, *frees)
}

// A stable armed footprint (41 MiB, between armed 40 and trigger 45) has no observed opportunity:
// it must not run the idle Trim/GC loop either. Crisis handling at/above trigger belongs to poll's
// state machine, not to this opportunistic path.
func TestIdleReleaseDoesNotThrashAtArmedFootprint(t *testing.T) {
	timer, trims, frees := newIdleReleaseTimer(t)
	timer.lastUsage = 35 * 1024 * 1024
	timer.releaseIdleMemory(idleSample(33), 10)
	require.Equal(t, 1, *frees)

	for range 5 {
		timer.releaseIdleMemory(idleSample(41), 10)
	}
	require.Equal(t, 1, *trims, "a stable armed footprint is not an idle-release opportunity")
	require.Equal(t, 1, *frees, "no second forced GC at a stable 41 MiB")
}

// The idle cleanup is single-flight: a second tick that wants to release must wait for the first,
// not run a second TrimMemory/FreeOSMemory in parallel.
func TestIdleReleaseIsSingleFlight(t *testing.T) {
	timer, _, _ := newIdleReleaseTimer(t)
	var trims atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	timer.trimMemory = func(context.Context) {
		trims.Add(1)
		close(entered)
		<-release
	}
	timer.lastUsage = 35 * 1024 * 1024

	firstDone := make(chan struct{})
	go func() {
		timer.releaseIdleMemory(idleSample(33), 10)
		close(firstDone)
	}()
	<-entered

	secondDone := make(chan struct{})
	go func() {
		timer.releaseIdleMemory(idleSample(33), 10)
		close(secondDone)
	}()
	select {
	case <-secondDone:
		t.Fatal("the second release entered while the first was still trimming")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	<-firstDone
	<-secondDone
	require.Equal(t, int32(1), trims.Load())
}

// stop must wait for an idle release that already passed its active check, exactly as it waits for a
// pressure callback. Otherwise Close can return while TrimMemory is still touching the network.
func TestStopWaitsForAnInFlightIdleRelease(t *testing.T) {
	timer, _, _ := newIdleReleaseTimer(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	timer.trimMemory = func(context.Context) {
		close(entered)
		<-release
	}
	timer.lastUsage = 35 * 1024 * 1024

	releaseDone := make(chan struct{})
	go func() {
		timer.releaseIdleMemory(idleSample(33), 10)
		close(releaseDone)
	}()
	<-entered

	stopDone := make(chan struct{})
	go func() {
		timer.stop()
		close(stopDone)
	}()
	select {
	case <-stopDone:
		t.Fatal("stop returned while the idle trim was still running")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	<-releaseDone
	<-stopDone
}
