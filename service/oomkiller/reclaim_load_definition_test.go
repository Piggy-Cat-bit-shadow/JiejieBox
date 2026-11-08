package oomkiller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The reclaim control-loop load definition.
//
// # What it replays
//
// A device whose footprint oscillates: every other sample shows a real drop, and the cheap layer
// returns about a megabyte each time it runs. The load is the SHAPE the executor must survive - a
// pass, then a steady sample that must not become a pass, repeated - not a memory number. A cache
// trim is a real object walk and the third layer forces a GC, so the point of replaying it is to
// confirm the ladder still stops at the layer that paid and that the observation adds no work.
//
// Run it explicitly and keep the logged line:
//
//	go test ./service/oomkiller -run 'TestReclaimCycleLoadDefinition' -v -count=1
//
// It is a host measurement of the executor's bookkeeping with a simulated footprint. It is not a
// device measurement and says nothing about how much a real process would reclaim.
const (
	reclaimLoadCycles = 100
	reclaimLoadMiB    = 35
)

func TestReclaimCycleLoadDefinition(t *testing.T) {
	fixture, observer, clock := newObservedTimer(t)
	fixture.adaptiveTimer.lastUsage = reclaimLoadMiB * 1024 * 1024
	fixture.adaptiveTimer.freeOSMemory = func() {}
	fixture.adaptiveTimer.trimCaches = func(context.Context) int {
		fixture.setUsage(32)
		return 2
	}

	started := time.Now()
	for cycle := 0; cycle < reclaimLoadCycles; cycle++ {
		*clock = clock.Add(2 * time.Second)
		// The growth sample: no drop since the previous observation, so the executor must do nothing.
		fixture.setUsage(35)
		fixture.releaseAt(35)
		// The drop sample: one MiB, which the cache layer returns.
		fixture.setUsage(33)
		fixture.releaseAt(33)
	}
	elapsed := time.Since(started)

	snapshot := observer.snapshot(*clock)
	require.Equal(t, uint64(reclaimLoadCycles), snapshot.Actions,
		"one pass per cycle: the growth sample must not run one")
	require.Equal(t, snapshot.Actions, snapshot.IdleActions)
	require.Equal(t, uint64(reclaimLoadCycles), snapshot.TotalGainBytes/1024/1024,
		"each pass is credited with the megabyte the cache layer returned")
	require.Len(t, snapshot.Recent, reclaimActionRingSize, "the report keeps a bounded window")
	require.False(t, snapshot.CoolingDown, "a paying pass must not start a cooldown")
	t.Logf("LOAD reclaim cycles: samples=%d actions=%d suppressed=%d totalGainMiB=%d recent=%d elapsed=%s",
		reclaimLoadCycles*2, snapshot.Actions, reclaimLoadCycles*2-int(snapshot.Actions),
		snapshot.TotalGainBytes/1024/1024, len(snapshot.Recent), elapsed)
}
