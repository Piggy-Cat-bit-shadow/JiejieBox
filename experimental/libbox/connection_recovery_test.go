package libbox

// The wire fields are what let the Go consumer place a batch in the stream's order.
//
// # The reviewed failure
//
// Batches were converted and applied with nothing but the process-local subscription identity. A
// consumer that received snapshot(sequence=1) and then increment(sequence=3, dropped=1) applied the
// increment even though batch 2 - possibly the CLOSED event for a row - never arrived; the table then
// held a connection that had already ended, forever.
//
// The consumer now refuses an increment that cannot follow the last applied one (sequence gap, rising
// drop count, different instance without a Reset) and waits for the snapshot. Stale and duplicate
// batches are ignored without asking for a snapshot: they describe state already applied, and
// resynchronising on them would churn.

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// snapshotEvents builds a Reset batch with one NEW row.
func snapshotEvents(instanceID string, sequence uint64, dropped uint64, event *ConnectionEvent) *ConnectionEvents {
	batch := &ConnectionEvents{
		Reset:      true,
		InstanceId: instanceID,
		sequence:   sequence,
		dropped:    dropped,
	}
	if event != nil {
		batch.events = []*ConnectionEvent{event}
	}
	return batch
}

// incrementEvents builds a non-Reset batch.
func incrementEvents(instanceID string, sequence uint64, dropped uint64, event *ConnectionEvent) *ConnectionEvents {
	batch := &ConnectionEvents{
		InstanceId: instanceID,
		sequence:   sequence,
		dropped:    dropped,
	}
	if event != nil {
		batch.events = []*ConnectionEvent{event}
	}
	return batch
}

func newRow(id string) *ConnectionEvent {
	return &ConnectionEvent{Type: ConnectionEventNew, ID: id, Connection: &Connection{ID: id}}
}

func closedRow(id string) *ConnectionEvent {
	return connectionEventFor(id, ConnectionEventClosed, recentClosedAt())
}

func connectionIDs(t *testing.T, connections *Connections) []string {
	t.Helper()
	iterator := connections.Iterator()
	ids := make([]string, 0)
	for iterator.HasNext() {
		ids = append(ids, iterator.Next().ID)
	}
	return ids
}

func TestAWireGapIsRefusedUntilTheSnapshotArrives(t *testing.T) {
	connections := NewConnections()
	connections.ApplyEvents(snapshotEvents("instance-1", 1, 0, newRow("kept")))
	connections.ApplyEvents(incrementEvents("instance-1", 3, 1, newRow("incomplete")))

	require.Equal(t, []string{"kept"}, connectionIDs(t, connections),
		"an increment that follows a lost batch must not be applied")
	require.True(t, connections.NeedsResync())

	// The repairing snapshot replaces the table; the incomplete row never appears.
	connections.ApplyEvents(snapshotEvents("instance-1", 4, 1, newRow("current")))
	require.Equal(t, []string{"current"}, connectionIDs(t, connections))
	require.False(t, connections.NeedsResync())
}

// TestALostCloseIsRepairedByTheNextSnapshot is the reviewed symptom end to end: the CLOSED event is
// missing, the row survives in the table, and the snapshot is what removes it.
func TestALostCloseIsRepairedByTheNextSnapshot(t *testing.T) {
	connections := NewConnections()
	connections.ApplyEvents(snapshotEvents("instance-1", 1, 0, newRow("a")))
	// Sequence 2 (the CLOSED for "a") never arrives; sequence 3 is a NEW for "b".
	connections.ApplyEvents(incrementEvents("instance-1", 3, 1, newRow("b")))
	require.Equal(t, []string{"a"}, connectionIDs(t, connections))

	connections.ApplyEvents(snapshotEvents("instance-1", 4, 1, newRow("b")))
	require.Equal(t, []string{"b"}, connectionIDs(t, connections),
		"the snapshot is the authority; the lost close is repaired, not guessed")
}

func TestADroppedIncreaseRefusesTheIncrement(t *testing.T) {
	connections := NewConnections()
	connections.ApplyEvents(snapshotEvents("instance-1", 1, 0, newRow("kept")))
	// The sequence is contiguous, but the daemon reports that source events were lost: the batch may
	// be missing the CLOSED of "kept".
	connections.ApplyEvents(incrementEvents("instance-1", 2, 1, newRow("incomplete")))

	require.Equal(t, []string{"kept"}, connectionIDs(t, connections))
	require.True(t, connections.NeedsResync())
}

func TestAnInstanceChangeWithoutResetIsRefused(t *testing.T) {
	connections := NewConnections()
	connections.ApplyEvents(snapshotEvents("instance-1", 1, 0, newRow("old")))
	connections.ApplyEvents(incrementEvents("instance-2", 2, 0, newRow("new-instance")))

	require.Equal(t, []string{"old"}, connectionIDs(t, connections))
	require.True(t, connections.NeedsResync())

	connections.ApplyEvents(snapshotEvents("instance-2", 1, 0, newRow("new-instance")))
	require.Equal(t, []string{"new-instance"}, connectionIDs(t, connections),
		"a Reset from the new instance is the baseline that makes its increments applicable")
}

func TestDuplicateAndOutOfOrderBatchesAreIgnoredWithoutResync(t *testing.T) {
	connections := NewConnections()
	connections.ApplyEvents(snapshotEvents("instance-1", 1, 0, newRow("a")))
	connections.ApplyEvents(incrementEvents("instance-1", 2, 0, closedRow("a")))
	// A duplicate of sequence 2 and an older sequence 1 are stale, not missing.
	connections.ApplyEvents(incrementEvents("instance-1", 2, 0, newRow("dup")))
	connections.ApplyEvents(incrementEvents("instance-1", 1, 0, newRow("stale")))

	activeIDs, _ := rowsByState(connections, ConnectionStateActive)
	require.Empty(t, activeIDs, "the CLOSED row stays closed and stale batches create nothing")
	require.False(t, connections.NeedsResync(), "stale batches must not trigger a resync")
}

// TestZeroWireFieldsKeepTheOldBehaviour is the wire compatibility half: an older daemon sends no
// instance, sequence or drop count, and the consumer behaves exactly as before (Reset + subscription
// identity decide).
func TestZeroWireFieldsKeepTheOldBehaviour(t *testing.T) {
	connections := NewConnections()
	connections.ApplyEvents(&ConnectionEvents{Reset: true, events: []*ConnectionEvent{newRow("a")}})
	connections.ApplyEvents(&ConnectionEvents{events: []*ConnectionEvent{closedRow("a")}})

	activeIDs, _ := rowsByState(connections, ConnectionStateActive)
	require.Empty(t, activeIDs)
	require.False(t, connections.NeedsResync())
}

// TestAResyncedSnapshotIsAppliedAndClearsTheSignal ties the client's `Resynced` marker to the state:
// the repaired snapshot is a normal Reset for the consumer, and the marker is only a UI fact.
func TestAResyncedSnapshotIsAppliedAndClearsTheSignal(t *testing.T) {
	connections := NewConnections()
	connections.ApplyEvents(snapshotEvents("instance-1", 1, 0, newRow("a")))
	connections.ApplyEvents(incrementEvents("instance-1", 3, 1, nil))
	require.True(t, connections.NeedsResync())

	repaired := snapshotEvents("instance-1", 2, 1, newRow("b"))
	repaired.Resynced = true
	connections.ApplyEvents(repaired)
	require.Equal(t, []string{"b"}, connectionIDs(t, connections))
	require.False(t, connections.NeedsResync())
}
