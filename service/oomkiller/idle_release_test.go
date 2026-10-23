package oomkiller

import (
	"context"
	"runtime"
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
	}
	t.Cleanup(func() { timer.timer.Stop() })
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

// Real pressure bypasses the ordinary low-yield cooldown: an armed footprint must still be able
// to release even immediately after a release that produced no gain.
func TestIdleReleasePressureBypassesTheCooldown(t *testing.T) {
	timer, trims, frees := newIdleReleaseTimer(t)
	timer.lastUsage = 35 * 1024 * 1024
	timer.releaseIdleMemory(idleSample(33), 10)
	require.Equal(t, 1, *frees)
	timer.lastRelease = time.Time{}

	timer.releaseIdleMemory(idleSample(41), 10)
	require.Equal(t, 2, *trims, "an armed footprint must not be blocked by the low-yield cooldown")
	require.Equal(t, 2, *frees)
}
