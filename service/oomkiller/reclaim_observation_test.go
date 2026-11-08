package oomkiller

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// newObservedTimer is a layered fixture with the reclaim observer attached and a simulated clock.
//
// The clock is installed before the timer could start anything (the fixture never starts it), which
// is the contract clockNow documents: the seam exists for policy time, not for a running executor.
func newObservedTimer(t *testing.T) (*layeredTimer, *reclaimObserver, *time.Time) {
	t.Helper()
	fixture := newLayeredTimer(t, 35)
	observer := newReclaimObserver()
	fixture.adaptiveTimer.reclaim = observer
	clock := time.Unix(1700000000, 0)
	fixture.adaptiveTimer.now = func() time.Time { return clock }
	return fixture, observer, &clock
}

// TestReclaimObservationRecordsTheCacheLayerWithItsEntryCount is the L1 contract: the action that
// ran, the footprint it was credited with, and the REAL number of entries the cache capability
// reported. The entry count is the only reclaim number that is an object count rather than a
// footprint delta, so it must survive to the report instead of being dropped at the seam.
func TestReclaimObservationRecordsTheCacheLayerWithItsEntryCount(t *testing.T) {
	fixture, observer, clock := newObservedTimer(t)
	fixture.setUsage(33)
	fixture.adaptiveTimer.trimCaches = func(context.Context) int {
		fixture.setUsage(30)
		return 12
	}

	fixture.release()

	snapshot := observer.snapshot(*clock)
	require.Equal(t, uint64(1), snapshot.Actions)
	require.Equal(t, uint64(1), snapshot.IdleActions)
	require.Len(t, snapshot.Recent, 1)
	action := snapshot.Recent[0]
	require.Equal(t, "idle", action.Kind)
	require.Equal(t, "cache", action.Stage)
	require.Equal(t, uint64(33*1024*1024), action.BeforeBytes)
	require.Equal(t, uint64(30*1024*1024), action.AfterBytes)
	require.Equal(t, uint64(3*1024*1024), action.GainBytes)
	require.True(t, action.CacheEntriesKnown)
	require.Equal(t, 12, action.CacheEntriesRemoved)
	require.False(t, action.LowYield)
	require.Equal(t, "memory_limit", action.Mode)
	require.False(t, snapshot.CoolingDown, "a pass that reached the floor must not start a cooldown")
}

// TestReclaimObservationMarksLayersWithoutACount keeps "the layer does not count entries" distinct
// from "the layer removed none". The pool layer has no count today, and a report that rendered both
// as zero would claim a fact nobody measured.
func TestReclaimObservationMarksLayersWithoutACount(t *testing.T) {
	fixture, observer, clock := newObservedTimer(t)
	fixture.setUsage(33)
	fixture.adaptiveTimer.trimCaches = func(context.Context) int { return 0 }
	fixture.adaptiveTimer.trimMemory = func(context.Context) { fixture.setUsage(30) }

	fixture.release()

	snapshot := observer.snapshot(*clock)
	require.Len(t, snapshot.Recent, 1)
	action := snapshot.Recent[0]
	require.Equal(t, "pools", action.Stage)
	require.Equal(t, 0, action.CacheEntriesRemoved, "the cache pass ran and removed nothing")
	require.True(t, action.CacheEntriesKnown)

	// The runtime stage is not the cache stage: a manager with no cache capability never ran L1 and
	// must not claim a count for it.
	fixture2, observer2, clock2 := newObservedTimer(t)
	fixture2.setUsage(33)
	fixture2.adaptiveTimer.trimCaches = nil
	fixture2.adaptiveTimer.freeOSMemory = func() {}
	fixture2.release()
	snapshot2 := observer2.snapshot(*clock2)
	action2 := snapshot2.Recent[0]
	require.Equal(t, "runtime", action2.Stage)
	require.False(t, action2.CacheEntriesKnown)
	require.Equal(t, -1, action2.CacheEntriesRemoved)
}

// TestReclaimObservationRecordsTheLowYieldCooldownAndSuppressesTheNextPass pins the two halves
// together. The record must carry the decision and the deadline, and the decision must still be what
// stops the next sample from paying for the same work again - a record that only described a
// cooldown the code no longer honoured would be a display counter, not feedback.
func TestReclaimObservationRecordsTheLowYieldCooldownAndSuppressesTheNextPass(t *testing.T) {
	fixture, observer, clock := newObservedTimer(t)
	fixture.setUsage(33)

	// A pass over all three layers that returns nothing: the footprint stays where it is.
	fixture.release()
	snapshot := observer.snapshot(*clock)
	require.Equal(t, uint64(1), snapshot.Actions)
	require.Equal(t, "runtime", snapshot.Recent[0].Stage, "the ladder must run to L3 to prove the low-yield case")
	require.True(t, snapshot.Recent[0].LowYield)
	require.Equal(t, *clock, snapshot.Recent[0].At)
	require.Equal(t, clock.Add(defaultIdleCooldown), snapshot.Recent[0].CooldownUntil)
	require.True(t, snapshot.CoolingDown)
	require.Equal(t, defaultIdleCooldown, snapshot.CooldownRemaining)

	// Inside the cooldown, a real footprint drop is still suppressed: the drop is the same floor the
	// last pass already proved it could not improve on.
	*clock = clock.Add(2 * time.Second)
	fixture.setUsage(32)
	fixture.releaseAt(32)
	require.Equal(t, uint64(1), observer.snapshot(*clock).Actions,
		"a pass inside the low-yield cooldown must not run")

	// After the cooldown the next opportunity is allowed again, and a paying pass ends the cooldown.
	*clock = clock.Add(defaultIdleCooldown)
	fixture.setUsage(31)
	fixture.adaptiveTimer.trimCaches = func(context.Context) int {
		fixture.setUsage(29)
		return 4
	}
	fixture.releaseAt(31)
	snapshot = observer.snapshot(*clock)
	require.Equal(t, uint64(2), snapshot.Actions)
	require.Equal(t, uint64(2), snapshot.IdleActions)
	require.False(t, snapshot.Recent[1].LowYield)
	require.False(t, snapshot.CoolingDown, "a pass that reached the floor must not cool down")
}

// TestReclaimObservationRingIsBoundedAndTheTotalsAreNot proves the memory bound and the accounting
// that makes it safe: removing detail must not remove the count.
func TestReclaimObservationRingIsBoundedAndTheTotalsAreNot(t *testing.T) {
	observer := newReclaimObserver()
	for index := 0; index < 20; index++ {
		observer.record(reclaimActionRecord{
			Kind:                "idle",
			Stage:               "cache",
			GainBytes:           10,
			CacheEntriesRemoved: index,
			CacheEntriesKnown:   true,
		})
	}
	snapshot := observer.snapshot(time.Now())
	require.Equal(t, uint64(20), snapshot.Actions)
	require.Len(t, snapshot.Recent, reclaimActionRingSize)
	require.Equal(t, 12, snapshot.Recent[0].CacheEntriesRemoved, "the ring must keep the NEWEST actions")
	require.Equal(t, 19, snapshot.Recent[len(snapshot.Recent)-1].CacheEntriesRemoved)
	require.Equal(t, uint64(20*10), snapshot.TotalGainBytes)
}

// TestReclaimObservationRecordsPressureAndCrisisBranches keeps the other two destructive branches in
// the same stream, so a report does not read an idle-only history on a device whose memory was
// released by a reset.
func TestReclaimObservationRecordsPressureAndCrisisBranches(t *testing.T) {
	fixture, observer, clock := newObservedTimer(t)
	fixture.setUsage(45)
	fixture.adaptiveTimer.freeOSMemory = func() { fixture.setUsage(43) }

	fixture.adaptiveTimer.runPressureReclaim()

	snapshot := observer.snapshot(*clock)
	require.Equal(t, uint64(1), snapshot.PressureActions)
	require.Equal(t, "pressure", snapshot.Recent[0].Kind)
	require.Equal(t, "cache+gc", snapshot.Recent[0].Stage)
	require.Equal(t, uint64(2*1024*1024), snapshot.Recent[0].GainBytes)

	var resets atomic.Int32
	fixture.adaptiveTimer.releaseNetwork = func(context.Context) { resets.Add(1) }
	usageMiB := uint64(46)
	fixture.adaptiveTimer.sampleFunc = func() memorySample { return idleSample(usageMiB) }
	fixture.setUsage(46)
	fixture.adaptiveTimer.freeOSMemory = func() { usageMiB = 42 }

	fixture.adaptiveTimer.runCrisisReclaim(&reclaimRequest{
		kind:   reclaimCrisis,
		reason: resetReasonThreshold,
		sample: idleSample(46),
	})

	snapshot = observer.snapshot(*clock)
	require.Equal(t, uint64(1), snapshot.CrisisActions)
	last := snapshot.Recent[len(snapshot.Recent)-1]
	require.Equal(t, "crisis", last.Kind)
	require.Equal(t, "network-reset", last.Stage)
	require.Equal(t, uint64(46*1024*1024), last.BeforeBytes)
	require.Equal(t, uint64(42*1024*1024), last.AfterBytes)
	require.Equal(t, uint64(4*1024*1024), last.GainBytes)
	require.Equal(t, int32(1), resets.Load())
}

// TestReportMetadataCarriesTheReclaimRecordNextToTheClientDocument proves the production write path,
// not only the renderer: the key must be spliced into the metadata document the report actually
// contains, and the client's own keys must survive it unchanged.
func TestReportMetadataCarriesTheReclaimRecordNextToTheClientDocument(t *testing.T) {
	basePath := t.TempDir()
	recorder := NewRecorder(RecorderOptions{
		BasePath: basePath,
		MetadataCallback: func(status ReportStatus) any {
			return map[string]any{"schema": 1, "peakMemory": status.PeakMemory}
		},
	})
	recorder.Start()
	t.Cleanup(func() { _ = recorder.Close() })

	service := &Service{reclaim: newReclaimObserver()}
	service.reclaim.record(reclaimActionRecord{
		Kind:        "idle",
		Stage:       "cache",
		At:          time.Unix(1700000000, 0),
		BeforeBytes: 100,
		AfterBytes:  60,
		GainBytes:   40,
	})
	recorder.SetExtraMetadata("memoryReclaim", service.reclaimMetadata)
	require.NoError(t, recorder.ensureDraftLocked())

	content, err := os.ReadFile(filepath.Join(basePath, DraftDirectoryName, metadataFileName))
	require.NoError(t, err)
	var document map[string]any
	require.NoError(t, json.Unmarshal(content, &document))
	require.Contains(t, document, "memoryReclaim")
	reclaim, ok := document["memoryReclaim"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, float64(1), reclaim["actions"])
	require.Contains(t, document, "schema", "the client's own keys must survive the splice")
	require.Contains(t, document, "peakMemory")
}

// TestReclaimMetadataIsAdditiveAndBounded is the report contract: a map under its own key, a fixed
// recent ring, and no cache entry count claimed for a layer that does not have one.
func TestReclaimMetadataIsAdditiveAndBounded(t *testing.T) {
	service := &Service{reclaim: newReclaimObserver()}
	require.NotNil(t, service.reclaimMetadata())
	require.Nil(t, (&Service{}).reclaimMetadata(), "no observer must mean no key")

	now := time.Unix(1700000000, 0)
	service.reclaim.record(reclaimActionRecord{
		Kind:                "idle",
		Stage:               "cache",
		At:                  now,
		BeforeBytes:         100,
		AfterBytes:          80,
		GainBytes:           20,
		CacheEntriesRemoved: 3,
		CacheEntriesKnown:   true,
		LowYield:            false,
	})
	service.reclaim.record(reclaimActionRecord{
		Kind:          "idle",
		Stage:         "runtime",
		At:            now.Add(time.Second),
		BeforeBytes:   80,
		AfterBytes:    80,
		LowYield:      true,
		CooldownUntil: now.Add(defaultIdleCooldown),
	})

	document, ok := service.reclaimMetadata().(map[string]any)
	require.True(t, ok)
	require.Equal(t, uint64(2), document["actions"])
	require.Equal(t, uint64(2), document["idleActions"])
	require.Equal(t, uint64(1), document["lowYieldActions"])
	require.Equal(t, uint64(20), document["totalGainBytes"])

	recent, ok := document["recent"].([]any)
	require.True(t, ok)
	require.Len(t, recent, 2)
	first, ok := recent[0].(map[string]any)
	require.True(t, ok)
	require.Contains(t, first, "cacheEntriesRemoved", "the carried cache count must be rendered")
	second, ok := recent[1].(map[string]any)
	require.True(t, ok)
	require.NotContains(t, second, "cacheEntriesRemoved", "a layer without a count must not render one")
	require.Contains(t, second, "cooldownUntil")
}
