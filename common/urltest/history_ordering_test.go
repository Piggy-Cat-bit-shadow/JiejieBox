package urltest

// Which measurement is allowed to become "the last result".
//
// A delay describes the path as it was WHILE the probe ran. Two attempts against the same
// (node, target) can finish in an order that differs from the order they started, and the slower
// one is exactly the one most likely to have been measuring a path that has since been replaced -
// a network change, a re-dial, a route that moved. So the store keeps the evidence that STARTED
// later, and a failure is only allowed to remove what its own attempt could have seen.

import (
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"

	"github.com/stretchr/testify/require"
)

// testScope is a valid scope for the storage tests; the URL itself is irrelevant to ordering.
func testScope(t *testing.T) MeasurementScope {
	t.Helper()
	scope, err := NewMeasurementScope("https://probe.example/generate_204", nil)
	require.NoError(t, err)
	return scope
}

func historyAt(at time.Time, delay uint16) *adapter.URLTestHistory {
	return &adapter.URLTestHistory{Time: at, Delay: delay}
}

func TestAnOlderMeasurementCannotReplaceNewerHealth(t *testing.T) {
	storage := NewHistoryStorage()
	scope := testScope(t)
	newer := time.Now()
	older := newer.Add(-time.Second)

	storage.StoreHealthHistory("node-a", scope, historyAt(newer, 40))
	storage.StoreHealthHistory("node-a", scope, historyAt(older, 900))

	stored := storage.LoadURLTestHistoryFor("node-a", scope)
	require.NotNil(t, stored)
	require.EqualValues(t, 40, stored.Delay, "the slower attempt must not undo the newer result")
	require.True(t, stored.Time.Equal(newer))
}

func TestAnOlderMeasurementCannotReplaceNewerDisplay(t *testing.T) {
	storage := NewHistoryStorage()
	newer := time.Now()
	older := newer.Add(-time.Second)

	storage.StoreDisplayHistory("node-a", testScope(t), historyAt(newer, 40))
	storage.StoreDisplayHistory("node-a", testScope(t), historyAt(older, 900))

	stored := storage.LoadURLTestHistory("node-a")
	require.NotNil(t, stored)
	require.EqualValues(t, 40, stored.Delay)
}

// TestEqualTimestampsAreAccepted pins the coarse-clock case.
//
// Measured on Windows, time.Now() divides by the system tick and returns the same instant for
// consecutive readings. Treating "not strictly newer" as stale would then silently drop a real
// measurement and leave the node with no result at all.
func TestEqualTimestampsAreAccepted(t *testing.T) {
	storage := NewHistoryStorage()
	scope := testScope(t)
	at := time.Now()

	storage.StoreHealthHistory("node-a", scope, historyAt(at, 40))
	storage.StoreHealthHistory("node-a", scope, historyAt(at, 55))

	stored := storage.LoadURLTestHistoryFor("node-a", scope)
	require.NotNil(t, stored)
	require.EqualValues(t, 55, stored.Delay, "an equal timestamp is a real measurement, not stale evidence")
}

func TestAFailedOlderAttemptCannotDeleteNewerEvidence(t *testing.T) {
	storage := NewHistoryStorage()
	scope := testScope(t)
	newer := time.Now()
	attemptStart := newer.Add(-2 * time.Second)

	storage.StoreHealthHistory("node-a", scope, historyAt(newer, 40))

	// The failure belongs to an attempt that started BEFORE the stored result: it says nothing
	// about the path that result measured.
	storage.DeleteHealthHistoryOlderThanOwned("node-a", scope, attemptStart, EvidenceOwnership{})
	require.NotNil(t, storage.LoadURLTestHistoryFor("node-a", scope),
		"a failed attempt must not remove evidence that started after it")

	// The same failure, from an attempt that started later than the stored result, IS the newer
	// fact and removes it.
	storage.DeleteHealthHistoryOlderThanOwned("node-a", scope, newer.Add(2*time.Second), EvidenceOwnership{})
	require.Nil(t, storage.LoadURLTestHistoryFor("node-a", scope))
}

// TestAnOldNetworkResultCannotOverwriteNewEvidence is the network-change shape of the rule.
//
// A probe that started on the previous network and finished after the switch is stamped with its
// own start, so it cannot take the place of the result the new network's probe already stored -
// and it cannot delete it either.
func TestAnOldNetworkResultCannotOverwriteNewEvidence(t *testing.T) {
	storage := NewHistoryStorage()
	scope := testScope(t)
	oldEpochStart := time.Now()
	oldEpochResult := historyAt(oldEpochStart, 800)
	newEpochStart := oldEpochStart.Add(500 * time.Millisecond)
	newEpochResult := historyAt(newEpochStart, 30)

	// The new network's probe finishes first ...
	storage.StoreHealthHistory("node-a", scope, newEpochResult)
	// ... and the pre-switch probe, completing later, must not overwrite it.
	storage.StoreHealthHistory("node-a", scope, oldEpochResult)
	stored := storage.LoadURLTestHistoryFor("node-a", scope)
	require.NotNil(t, stored)
	require.EqualValues(t, 30, stored.Delay)

	// A late FAILURE from the pre-switch probe must not delete it either.
	storage.DeleteHealthHistoryOlderThanOwned("node-a", scope, oldEpochStart, EvidenceOwnership{})
	require.NotNil(t, storage.LoadURLTestHistoryFor("node-a", scope))
}
