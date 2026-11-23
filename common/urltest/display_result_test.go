package urltest

// The display TERMINAL result layer: delay 0 with a completion time is a failed probe, and the pair
// is read and written together.
//
// These tests pin the properties a client depends on and the ones the evidence layers depend on:
// a result is never combined from different probes, an older probe never overwrites a newer one, a
// configured target's result cannot leak into another target, the success-only layers keep their
// exact meaning, and the records are bounded and dropped with the node.

import (
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"

	"github.com/stretchr/testify/require"
)

func displayTargetScope(t *testing.T, link string) MeasurementScope {
	t.Helper()
	scope, err := NewMeasurementScope(link, ExpectedStatus{})
	require.NoError(t, err)
	return scope
}

// TestADisplayResultKeepsItsDelayAndTimeTogether is the client contract: 0/0 means never measured,
// 0/valid means the last probe failed, positive/valid means it succeeded.
func TestADisplayResultKeepsItsDelayAndTimeTogether(t *testing.T) {
	storage := NewHistoryStorage()
	t.Cleanup(func() { _ = storage.Close() })

	if _, loaded := storage.LoadDisplayResult("node"); loaded {
		t.Fatal("a node that was never probed must have no result")
	}

	start := time.Now()
	completed := start.Add(120 * time.Millisecond)
	require.True(t, storage.StoreDisplayResult("node", MeasurementScope{}, 0, start, completed, nil))
	result, loaded := storage.LoadDisplayResult("node")
	require.True(t, loaded)
	require.True(t, result.Failed(), "delay 0 with a completion time is the failed terminal state")
	require.Equal(t, completed.Unix(), result.CompletedAt.Unix())
	require.True(t, result.HasResult())

	// A newer success replaces it, and the pair stays from that one result.
	nextStart := completed.Add(time.Millisecond)
	nextCompleted := nextStart.Add(31 * time.Millisecond)
	require.True(t, storage.StoreDisplayResult("node", MeasurementScope{}, 31, nextStart, nextCompleted, nil))
	result, loaded = storage.LoadDisplayResult("node")
	require.True(t, loaded)
	require.False(t, result.Failed())
	require.EqualValues(t, 31, result.Delay)
	require.Equal(t, nextCompleted.Unix(), result.CompletedAt.Unix())
}

// TestADisplayResultNeverLeavesTheHealthLayer is the separation the whole design rests on: selection
// evidence must not appear just because a probe failed.
func TestADisplayResultNeverLeavesTheHealthLayer(t *testing.T) {
	storage := NewHistoryStorage()
	t.Cleanup(func() { _ = storage.Close() })
	scope := displayTargetScope(t, "https://example.com/generate_204")

	storage.StoreHealthHistory("node", scope, &adapter.URLTestHistory{Time: time.Now(), Delay: 42})
	start := time.Now()
	require.True(t, storage.StoreDisplayResult("node", scope, 0, start, start.Add(time.Millisecond), nil))

	health := storage.LoadURLTestHistoryFor("node", scope)
	require.NotNil(t, health, "a failed display result must not delete or replace health evidence")
	require.EqualValues(t, 42, health.Delay)

	// And the display success layer keeps its own meaning: a failure does not remove it.
	display := storage.LoadURLTestHistory("node")
	require.NotNil(t, display, "the last success stays readable for Clash and for the node list")
	require.EqualValues(t, 42, display.Delay)
}

// TestAnOlderProbeCannotOverwriteANewerDisplayResult pins the ordering identity: a slow probe that
// STARTED before a newer one must not describe the node afterwards.
func TestAnOlderProbeCannotOverwriteANewerDisplayResult(t *testing.T) {
	storage := NewHistoryStorage()
	t.Cleanup(func() { _ = storage.Close() })

	newerStart := time.Now()
	newerCompleted := newerStart.Add(50 * time.Millisecond)
	require.True(t, storage.StoreDisplayResult("node", MeasurementScope{}, 20, newerStart, newerCompleted, nil))

	olderStart := newerStart.Add(-time.Second)
	require.False(t, storage.StoreDisplayResult("node", MeasurementScope{}, 0, olderStart, time.Now(), nil),
		"a probe that started before the stored one must not replace it")

	result, loaded := storage.LoadDisplayResult("node")
	require.True(t, loaded)
	require.EqualValues(t, 20, result.Delay)
}

// TestAConfiguredTargetsResultIsScopedAndAManualOneIsNot keeps the two read paths apart: a URLTest
// group reads its own target, and a manual probe cannot leave an entry under a target it was not
// measured against.
func TestAConfiguredTargetsResultIsScopedAndAManualOneIsNot(t *testing.T) {
	storage := NewHistoryStorage()
	t.Cleanup(func() { _ = storage.Close() })
	targetA := displayTargetScope(t, "https://a.example/generate_204")
	targetB := displayTargetScope(t, "https://b.example/generate_204")
	start := time.Now()

	require.True(t, storage.StoreDisplayResult("node", targetA, 0, start, start.Add(time.Millisecond), nil))

	scoped, loaded := storage.LoadDisplayResultFor("node", targetA)
	require.True(t, loaded)
	require.True(t, scoped.Failed())
	_, loaded = storage.LoadDisplayResultFor("node", targetB)
	require.False(t, loaded, "one target's result must not answer a question about another")
	require.Equal(t, 1, storage.ScopedDisplayResultEntryCount())
	require.Equal(t, 1, storage.DisplayResultEntryCount(), "the tag-level entry is what the node list reads")

	// A manual probe passes no scope: it must not create a per-target entry, and it must not
	// overwrite the entry a URLTest group reads - the group asked a different question.
	require.True(t, storage.StoreDisplayResult("node", MeasurementScope{}, 8, start.Add(time.Second), start.Add(time.Second+time.Millisecond), nil))
	require.Equal(t, 1, storage.ScopedDisplayResultEntryCount(),
		"an unscoped manual result must not grow the per-target map")
	scopedResult, loaded := storage.LoadDisplayResultFor("node", targetA)
	require.True(t, loaded)
	require.True(t, scopedResult.Failed(),
		"the group's own target result is not replaced by a manual probe against another URL")
	tagResult, loaded := storage.LoadDisplayResult("node")
	require.True(t, loaded)
	require.EqualValues(t, 8, tagResult.Delay, "the node list reads the newest result for the node")
}

// TestDisplayResultsAreDroppedWithTheNodeAndTheTarget keeps the records bounded: a removed node and a
// retired target leave nothing on screen.
func TestDisplayResultsAreDroppedWithTheNodeAndTheTarget(t *testing.T) {
	storage := NewHistoryStorage()
	t.Cleanup(func() { _ = storage.Close() })
	target := displayTargetScope(t, "https://a.example/generate_204")
	start := time.Now()

	storage.StoreDisplayResult("node", target, 12, start, start.Add(time.Millisecond), nil)
	storage.StoreDisplayResult("other", MeasurementScope{}, 13, start, start.Add(time.Millisecond), nil)
	require.Equal(t, 2, storage.DisplayResultEntryCount())

	storage.DeleteDisplayResultFor("node", target)
	require.Equal(t, 0, storage.ScopedDisplayResultEntryCount())
	_, loaded := storage.LoadDisplayResult("node")
	require.True(t, loaded, "retiring a target must not hide the node's own last result")

	storage.DeleteURLTestHistory("node")
	_, loaded = storage.LoadDisplayResult("node")
	require.False(t, loaded, "a removed node leaves no terminal result behind")
	_, loaded = storage.LoadDisplayResult("other")
	require.True(t, loaded, "another node is unaffected")
}

// TestOwnershipRefusesADisplayResultForADroppedNode keeps the same provenance rule the evidence layers
// use: a probe that started before the node's evidence was dropped cannot publish afterwards.
func TestOwnershipRefusesADisplayResultForADroppedNode(t *testing.T) {
	storage := NewHistoryStorage()
	t.Cleanup(func() { _ = storage.Close() })

	tagEpoch, scopeEpoch := storage.EvidenceStamp("node", MeasurementScope{})
	ownership := &EvidenceOwnership{TagEpoch: tagEpoch, ScopeEpoch: scopeEpoch}
	storage.DeleteURLTestHistory("node") // bumps the tag epoch

	start := time.Now()
	require.False(t, storage.StoreDisplayResult("node", MeasurementScope{}, 5, start, start.Add(time.Millisecond), ownership),
		"a probe measured before the drop must not publish a result afterwards")
}

// TestAClosedStoragePublishesNothing keeps the lifecycle boundary: nothing is written or notified
// after Close returned.
func TestAClosedStoragePublishesNothing(t *testing.T) {
	storage := NewHistoryStorage()
	require.NoError(t, storage.Close())
	start := time.Now()
	require.False(t, storage.StoreDisplayResult("node", MeasurementScope{}, 1, start, start.Add(time.Millisecond), nil))
	_, loaded := storage.LoadDisplayResult("node")
	require.False(t, loaded)
}
