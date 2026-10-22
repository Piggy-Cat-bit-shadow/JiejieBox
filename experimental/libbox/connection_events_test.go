package libbox

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The event-order contract of the client's connection map.
//
// # Why this file exists
//
// The phone's Activity page renders exactly what this map holds: a row is "Active" when its
// `ClosedAt` is zero, and the state filter that decides what is visible reads the same field. So a
// map that turns a closed connection back into an open one is not a cosmetic bug - it is the
// reported symptom, produced entirely on the client side, with the core holding nothing.
//
// The daemon's stream is well behaved (NEW then CLOSED for an id, a Reset snapshot on subscribe, no
// event for an id it never announced), but the map must not depend on that to stay truthful: a
// reconnecting client, a late batch, a duplicate, or an event for an id it never saw are all inputs
// it can receive. The contract these tests pin is the defensible one:
//
//	CLOSED always WINS for an id: it sets ClosedAt and clears the rates, whatever state that id was
//	    in, including "never seen" (which must insert a CLOSED row, never an active one);
//	UPDATE never creates an entry and never clears ClosedAt;
//	NEW replaces an entry (that is what a NEW for an existing id means) and is the only thing that
//	    can make an id active again;
//	Reset clears the map before the batch is applied, so no pre-reset id survives it.
//
// # The five-minute window, measured rather than assumed
//
// `evictClosedConnections` runs inside every ApplyEvents and drops rows whose ClosedAt is more than
// five minutes old. It is a cache policy, and the test at the bottom records its actual behaviour:
// it is decided by the event's timestamp, not by arrival, so a stale CLOSED can never leave a row
// behind - and it can never produce an "active" row, which is why it is not part of the display
// contract. The consequence for a reader is that the Closed filter holds only recent closes, and the
// history is evicted lazily (on the next event) rather than on a timer of its own.

func recentClosedAt() int64 { return time.Now().Add(-time.Minute).UnixMilli() }

func connectionEventFor(id string, eventType int32, closedAt int64) *ConnectionEvent {
	return &ConnectionEvent{
		Type:     eventType,
		ID:       id,
		ClosedAt: closedAt,
		Connection: &Connection{
			ID:        id,
			CreatedAt: time.Now().Add(-time.Minute).UnixMilli(),
		},
	}
}

func newTestConnections(events ...*ConnectionEvent) *Connections {
	connections := NewConnections()
	connections.ApplyEvents(&ConnectionEvents{events: events})
	return connections
}

// rowsByState returns the ids visible under a state and, for the closed ones, their ClosedAt.
func rowsByState(connections *Connections, state int32) (ids []string, closedAt int64) {
	connections.FilterState(state)
	iterator := connections.Iterator()
	for iterator.HasNext() {
		connection := iterator.Next()
		ids = append(ids, connection.ID)
		if connection.ClosedAt != 0 {
			closedAt = connection.ClosedAt
		}
	}
	return ids, closedAt
}

func TestConnectionClosedEventNeverProducesAnActiveRow(t *testing.T) {
	// A closed event for an id the map has never seen: the daemon's reconciler can send this after a
	// reconnect, and it must insert a CLOSED row rather than an active one.
	closedAt := recentClosedAt()
	connections := newTestConnections(connectionEventFor("late-id", ConnectionEventClosed, closedAt))

	activeIDs, _ := rowsByState(connections, ConnectionStateActive)
	require.Empty(t, activeIDs, "a CLOSED event for an unknown id must never appear as active")

	closedIDs, rowClosedAt := rowsByState(connections, ConnectionStateClosed)
	require.Equal(t, []string{"late-id"}, closedIDs, "the history row must exist so the close is visible")
	require.Equal(t, closedAt, rowClosedAt)
}

func TestConnectionClosedEventBeatsEveryEarlierState(t *testing.T) {
	closedAt := recentClosedAt()
	connections := newTestConnections(
		connectionEventFor("conn-1", ConnectionEventNew, 0),
		connectionEventFor("conn-1", ConnectionEventUpdate, 0),
		connectionEventFor("conn-1", ConnectionEventClosed, closedAt),
	)
	activeIDs, _ := rowsByState(connections, ConnectionStateActive)
	require.Empty(t, activeIDs)
	closedIDs, rowClosedAt := rowsByState(connections, ConnectionStateClosed)
	require.Equal(t, []string{"conn-1"}, closedIDs)
	require.Equal(t, closedAt, rowClosedAt)
}

func TestConnectionDuplicateClosedEventIsIdempotent(t *testing.T) {
	closedAt := recentClosedAt()
	connections := newTestConnections(
		connectionEventFor("conn-1", ConnectionEventNew, 0),
		connectionEventFor("conn-1", ConnectionEventClosed, closedAt),
		connectionEventFor("conn-1", ConnectionEventClosed, closedAt),
	)
	closedIDs, _ := rowsByState(connections, ConnectionStateClosed)
	require.Equal(t, []string{"conn-1"}, closedIDs)
	activeIDs, _ := rowsByState(connections, ConnectionStateActive)
	require.Empty(t, activeIDs)
}

func TestConnectionUpdateAfterCloseCannotReactivate(t *testing.T) {
	closedAt := recentClosedAt()
	connections := newTestConnections(
		connectionEventFor("conn-1", ConnectionEventNew, 0),
		connectionEventFor("conn-1", ConnectionEventClosed, closedAt),
	)
	// A stale UPDATE (a batch that arrived out of order) must not clear ClosedAt, and an UPDATE for
	// an unknown id must not create an active row.
	connections.ApplyEvents(&ConnectionEvents{events: []*ConnectionEvent{
		{Type: ConnectionEventUpdate, ID: "conn-1", UplinkDelta: 10, DownlinkDelta: 20},
		{Type: ConnectionEventUpdate, ID: "never-seen", UplinkDelta: 1},
	}})

	activeIDs, _ := rowsByState(connections, ConnectionStateActive)
	require.Empty(t, activeIDs, "an update must never create or reopen a row")
	closedIDs, rowClosedAt := rowsByState(connections, ConnectionStateClosed)
	require.Equal(t, []string{"conn-1"}, closedIDs)
	require.Equal(t, closedAt, rowClosedAt, "an update must never clear a close")
}

func TestConnectionResetDropsPreResetIDs(t *testing.T) {
	connections := newTestConnections(
		connectionEventFor("before-reset", ConnectionEventNew, 0),
	)
	connections.ApplyEvents(&ConnectionEvents{
		Reset: true,
		events: []*ConnectionEvent{
			connectionEventFor("after-reset", ConnectionEventNew, 0),
		},
	})
	activeIDs, _ := rowsByState(connections, ConnectionStateActive)
	require.Equal(t, []string{"after-reset"}, activeIDs,
		"the snapshot that arrives with Reset is the whole truth; ids from before it must be gone")
}

func TestConnectionNewForTheSameIDReplacesIt(t *testing.T) {
	// A NEW for an id that is closed is the daemon saying the id exists again. It replaces the row;
	// this is the only transition that may make an id active, and it is listed here so the contract
	// is complete rather than implied.
	connections := newTestConnections(
		connectionEventFor("conn-1", ConnectionEventNew, 0),
		connectionEventFor("conn-1", ConnectionEventClosed, recentClosedAt()),
		connectionEventFor("conn-1", ConnectionEventNew, 0),
	)
	activeIDs, _ := rowsByState(connections, ConnectionStateActive)
	require.Equal(t, []string{"conn-1"}, activeIDs, "NEW is the one event that can reopen an id")
}

// TestConnectionClosedRowsOlderThanTheWindowNeverLinger pins the policy, not the display contract.
//
// The eviction is decided by the ClosedAt the event carries, so a closed row whose timestamp is
// already older than the window is dropped in the same ApplyEvents that inserted it. That is the
// behaviour a reader should expect from the Closed filter (recent closes only), and it is the reason
// the window cannot be the explanation for an ACTIVE row: it only ever removes closed rows.
func TestConnectionClosedRowsOlderThanTheWindowNeverLinger(t *testing.T) {
	stale := time.Now().Add(-6 * time.Minute).UnixMilli()
	connections := newTestConnections(connectionEventFor("stale", ConnectionEventClosed, stale))

	allIDs, _ := rowsByState(connections, ConnectionStateAll)
	require.Empty(t, allIDs, "a close older than the window is not kept")
	activeIDs, _ := rowsByState(connections, ConnectionStateActive)
	require.Empty(t, activeIDs, "and it is certainly not turned into an active row")
}

// TestConnectionFilterByStateNeverShowsAClosedRowAsActive is the display-side invariant the report
// depends on: the default filter is Active, and it must be exactly the rows with no ClosedAt.
func TestConnectionFilterByStateNeverShowsAClosedRowAsActive(t *testing.T) {
	connections := newTestConnections(
		connectionEventFor("active", ConnectionEventNew, 0),
		connectionEventFor("closed", ConnectionEventNew, 0),
		connectionEventFor("closed", ConnectionEventClosed, recentClosedAt()),
	)
	activeIDs, _ := rowsByState(connections, ConnectionStateActive)
	require.Equal(t, []string{"active"}, activeIDs)
	closedIDs, _ := rowsByState(connections, ConnectionStateClosed)
	require.Equal(t, []string{"closed"}, closedIDs)
}
