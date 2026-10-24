package oomkiller

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// layeredTimer is the fixture for the L1/L2/L3 decision. It replaces the process footprint with an
// atomic so each layer's measured gain is exact, and records the order in which the layers ran.
type layeredTimer struct {
	*adaptiveTimer
	usage atomic.Uint64

	access sync.Mutex
	order  []string
	trims  int
	frees  int
}

func newLayeredTimer(t *testing.T, lastUsageMiB uint64) *layeredTimer {
	t.Helper()
	fixture := &layeredTimer{}
	fixture.adaptiveTimer = newPollStopRaceTimer(&atomic.Uint32{}, 33)
	fixture.adaptiveTimer.lastUsage = lastUsageMiB * 1024 * 1024
	fixture.usage.Store(33 * 1024 * 1024)
	fixture.adaptiveTimer.readUsage = fixture.usage.Load
	fixture.adaptiveTimer.trimCaches = func(context.Context) {
		fixture.note("cache")
	}
	fixture.adaptiveTimer.trimMemory = func(context.Context) {
		fixture.note("pools")
	}
	fixture.adaptiveTimer.freeOSMemory = func() {
		fixture.note("runtime")
	}
	t.Cleanup(func() {
		if fixture.timer != nil {
			fixture.timer.Stop()
		}
	})
	return fixture
}

func (f *layeredTimer) note(stage string) {
	f.access.Lock()
	defer f.access.Unlock()
	f.order = append(f.order, stage)
	switch stage {
	case "pools":
		f.trims++
	case "runtime":
		f.frees++
	}
}

func (f *layeredTimer) recordedOrder() []string {
	f.access.Lock()
	defer f.access.Unlock()
	return append([]string(nil), f.order...)
}

func (f *layeredTimer) setUsage(mib uint64) {
	f.usage.Store(mib * 1024 * 1024)
}

func (f *layeredTimer) release() {
	f.releaseAt(33)
}

func (f *layeredTimer) releaseAt(mib uint64) {
	f.releaseIdleMemory(idleSample(mib), 10)
}

// The cache layer is cheapest: a gain that reaches the floor must stop the pass before a single
// socket is closed and before the runtime is collected.
func TestIdleReclaimStopsAtTheCacheLayer(t *testing.T) {
	fixture := newLayeredTimer(t, 35)
	fixture.setUsage(33)
	fixture.adaptiveTimer.trimCaches = func(context.Context) {
		fixture.note("cache")
		fixture.setUsage(31)
	}

	fixture.release()

	require.Equal(t, []string{"cache"}, fixture.recordedOrder())
	require.Zero(t, fixture.trims, "a cache gain over the floor must not close idle pools")
	require.Zero(t, fixture.frees, "a cache gain over the floor must not force a GC")
}

// A cache trim that returns only a fraction of the floor must not be treated as success: the pool
// layer is what actually reaches the threshold, and the runtime is still not collected.
func TestIdleReclaimEscalatesToPoolsWhenCacheGainIsSmall(t *testing.T) {
	fixture := newLayeredTimer(t, 35)
	fixture.setUsage(33)
	fixture.adaptiveTimer.trimCaches = func(context.Context) {
		fixture.note("cache")
		fixture.setUsage(33*1024*1024 - 100*1024)
	}
	fixture.adaptiveTimer.trimMemory = func(context.Context) {
		fixture.note("pools")
		fixture.setUsage(31)
	}

	fixture.release()

	require.Equal(t, []string{"cache", "pools"}, fixture.recordedOrder())
	require.Equal(t, 1, fixture.trims)
	require.Zero(t, fixture.frees, "the pool layer reached the floor; a GC would be pure cost")
}

// L3 is the fallback, not the default: with both cheap layers below the floor and the runtime
// reporting free spans it can return, the pass forces one GC.
func TestIdleReclaimForcesGCOnlyWhenEarlierLayersAreInsufficient(t *testing.T) {
	fixture := newLayeredTimer(t, 35)
	fixture.setUsage(33)
	fixture.adaptiveTimer.trimCaches = func(context.Context) {
		fixture.note("cache")
		fixture.setUsage(33*1024*1024 - 100*1024)
	}
	fixture.adaptiveTimer.trimMemory = func(context.Context) {
		fixture.note("pools")
		fixture.setUsage(33*1024*1024 - 200*1024)
	}
	fixture.adaptiveTimer.runtimeReclaimable = func() uint64 { return defaultIdleBenefitFloor }

	fixture.release()

	require.Equal(t, []string{"cache", "pools", "runtime"}, fixture.recordedOrder())
	require.Equal(t, 1, fixture.frees)
}

// Below the armed line, with the runtime reporting nothing returnable, the pass must accept the
// footprint as the working set instead of collecting on every real drop. This is the 33 MiB rule,
// now applied to the runtime layer rather than only to the idle trigger.
func TestIdleReclaimSkipsGCWhenTheRuntimeHasNothingToReturn(t *testing.T) {
	fixture := newLayeredTimer(t, 35)
	fixture.setUsage(33)
	fixture.adaptiveTimer.runtimeReclaimable = func() uint64 { return 0 }

	fixture.release()

	require.Equal(t, []string{"cache", "pools"}, fixture.recordedOrder())
	require.Zero(t, fixture.frees)
}

// At/above armed the runtime layer is justified even when the metric is quiet: the process is in
// the band that escalates toward the trigger, and accepting a higher footprint there is not the
// same decision as accepting it at 33 MiB.
func TestIdleReclaimForcesGCAtArmedEvenWhenRuntimeIsQuiet(t *testing.T) {
	fixture := newLayeredTimer(t, 43)
	fixture.adaptiveTimer.sampleFunc = func() memorySample { return idleSample(41) }
	fixture.setUsage(41)
	fixture.adaptiveTimer.runtimeReclaimable = func() uint64 { return 0 }

	fixture.releaseAt(41)

	require.Equal(t, []string{"cache", "pools", "runtime"}, fixture.recordedOrder())
	require.Equal(t, 1, fixture.frees)
}

// A stable armed footprint is still not an idle opportunity: the armed line only decides whether
// the runtime layer is justified ONCE a drop or shrink has been observed. Without this, the 41 MiB
// thrash the previous round removed would come back through the new layer gate.
func TestIdleReclaimDoesNotStartAtAStableArmedFootprint(t *testing.T) {
	fixture := newLayeredTimer(t, 41)
	fixture.adaptiveTimer.sampleFunc = func() memorySample { return idleSample(41) }
	fixture.setUsage(41)

	fixture.releaseAt(41)
	fixture.releaseAt(41)

	require.Empty(t, fixture.recordedOrder())
}
