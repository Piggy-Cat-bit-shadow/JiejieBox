package daemon

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/trafficcontrol"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/gofrs/uuid/v5"
	"github.com/stretchr/testify/require"
)

// The daemon layer of the connection list: the initial snapshot, the periodic reconcile, and the
// per-event translation.
//
// # Why this file exists
//
// The phone's Activity page is fed by exactly these three functions, and nothing else: the initial
// message (with Reset) is built by `buildInitialConnectionState`, every live event goes through
// `applyConnectionEvent`, and once a second `buildTrafficUpdates` reconciles the client's view
// against the core's active table. The client's own contract is pinned in
// experimental/libbox/connection_events_test.go; this is the other half - the server's.
//
// The failure mode being excluded is the report's: if any of these three turns a closed connection
// into an active one, or fails to announce a close, the row stays Active on the phone no matter what
// the core did. They are pure functions of the manager and the snapshot map, so they can be driven
// without a Box, a service or a gRPC stream - and the manager they are driven with is the real one,
// not a double.

// daemonProbeOutbound is the metadata the tracker reads: Tag and Type.
type daemonProbeOutbound struct{}

func (daemonProbeOutbound) Type() string           { return "probe" }
func (daemonProbeOutbound) Tag() string            { return "probe" }
func (daemonProbeOutbound) Network() []string      { return []string{N.NetworkTCP} }
func (daemonProbeOutbound) Dependencies() []string { return nil }

func (daemonProbeOutbound) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return nil, net.ErrClosed
}

func (daemonProbeOutbound) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, net.ErrClosed
}

var daemonProbeChain = []adapter.Outbound{daemonProbeOutbound{}}

// joinDaemonConnection adds one real tracker to a real manager and returns its id.
func joinDaemonConnection(t *testing.T, manager *trafficcontrol.Manager, destination string) uuid.UUID {
	t.Helper()
	inbound, inboundPeer := net.Pipe()
	t.Cleanup(func() { inbound.Close(); inboundPeer.Close() })
	tracked := manager.RoutedConnection(context.Background(), inbound, adapter.InboundContext{
		Inbound:       "mixed-in",
		InboundType:   "mixed",
		Network:       N.NetworkTCP,
		Destination:   M.ParseSocksaddrHostPort(destination, 443),
		OutboundChain: daemonProbeChain,
	}, nil, daemonProbeChain[0])
	t.Cleanup(func() { tracked.Close() })
	// The manager's list is a map iteration, so the new connection is found by the destination this
	// call used rather than by position. Looking at the tail of the list made two joins in one test
	// return the same id before this was fixed.
	for _, metadata := range manager.Connections() {
		if metadata.Metadata.Destination.Fqdn == destination {
			return metadata.ID
		}
	}
	t.Fatalf("the connection to %s was not registered", destination)
	return uuid.Nil
}

func closedProtoEvents(events []*ConnectionEvent) []*ConnectionEvent {
	var closed []*ConnectionEvent
	for _, event := range events {
		if event.Type == ConnectionEventType_CONNECTION_EVENT_CLOSED {
			closed = append(closed, event)
		}
	}
	return closed
}

func activeProtoEvents(events []*ConnectionEvent) []*ConnectionEvent {
	var active []*ConnectionEvent
	for _, event := range events {
		if event.Type == ConnectionEventType_CONNECTION_EVENT_NEW {
			active = append(active, event)
		}
	}
	return active
}

// TestInitialConnectionStateCarriesActiveAndClosed is the first message after a subscribe: active
// connections as NEW, the closed history also as NEW but with ClosedAt set, and only the active ones
// enter the reconciliation snapshot.
func TestInitialConnectionStateCarriesActiveAndClosed(t *testing.T) {
	manager := trafficcontrol.NewManager()
	activeID := joinDaemonConnection(t, manager, "active.example")
	closedID := joinDaemonConnection(t, manager, "closed.example")
	require.NoError(t, manager.Connection(closedID).Close())
	require.Equal(t, 1, manager.ConnectionsLen())

	snapshots := make(map[uuid.UUID]connectionSnapshot)
	service := &StartedService{}
	events := service.buildInitialConnectionState(manager, snapshots)
	require.Len(t, events, 2)

	byID := make(map[string]*ConnectionEvent, len(events))
	for _, event := range events {
		require.Equal(t, ConnectionEventType_CONNECTION_EVENT_NEW, event.Type)
		byID[event.Id] = event
	}

	require.Zero(t, byID[activeID.String()].Connection.ClosedAt, "an active connection must arrive open")
	require.NotZero(t, byID[closedID.String()].Connection.ClosedAt,
		"the closed history must arrive closed; an open history row is the reported symptom")

	_, activeSnapshotted := snapshots[activeID]
	_, closedSnapshotted := snapshots[closedID]
	require.True(t, activeSnapshotted, "the reconcile must track active ids")
	require.False(t, closedSnapshotted, "a closed id must not be tracked as active")
}

// TestReconcileEmitsClosedForAGoneConnection is the periodic repair: an id the client was told about
// and is no longer active must get a closed event, once.
func TestReconcileEmitsClosedForAGoneConnection(t *testing.T) {
	manager := trafficcontrol.NewManager()
	connectionID := joinDaemonConnection(t, manager, "reconcile.example")
	service := &StartedService{}
	snapshots := make(map[uuid.UUID]connectionSnapshot)
	service.buildInitialConnectionState(manager, snapshots)

	require.NoError(t, manager.Connection(connectionID).Close())

	events := service.buildTrafficUpdates(manager, snapshots)
	closed := closedProtoEvents(events)
	require.Len(t, closed, 1, "the gone connection must be announced as closed")
	require.Equal(t, connectionID.String(), closed[0].Id)
	require.NotZero(t, closed[0].ClosedAt)
	require.NotNil(t, closed[0].Connection, "the closed event must carry the row so the client can render it")
	require.Equal(t, closed[0].ClosedAt, closed[0].Connection.ClosedAt)
	require.Empty(t, snapshots, "the reconcile must forget it")

	// And it must not repeat: the second pass has nothing to say about that id.
	require.Empty(t, closedProtoEvents(service.buildTrafficUpdates(manager, snapshots)))
}

// TestReconcileEmitsNewForAnUnannouncedConnection is the other direction: a connection created
// between two passes must be announced rather than silently appearing in the table.
func TestReconcileEmitsNewForAnUnannouncedConnection(t *testing.T) {
	manager := trafficcontrol.NewManager()
	service := &StartedService{}
	snapshots := make(map[uuid.UUID]connectionSnapshot)
	require.Empty(t, service.buildInitialConnectionState(manager, snapshots))

	connectionID := joinDaemonConnection(t, manager, "late.example")
	events := service.buildTrafficUpdates(manager, snapshots)
	announced := activeProtoEvents(events)
	require.Len(t, announced, 1)
	require.Equal(t, connectionID.String(), announced[0].Id)
	require.Zero(t, announced[0].Connection.ClosedAt)
	require.Len(t, snapshots, 1)
}

// TestStreamingClosedAfterReconcileNeverResurrects covers the interleaving the stream can produce:
// the reconcile announced the close and forgot the id, and then the original closed event finally
// arrives on the subscription channel. It must translate to a closed event - never a new one.
func TestStreamingClosedAfterReconcileNeverResurrects(t *testing.T) {
	manager := trafficcontrol.NewManager()
	connectionID := joinDaemonConnection(t, manager, "interleaved.example")
	service := &StartedService{}
	snapshots := make(map[uuid.UUID]connectionSnapshot)
	service.buildInitialConnectionState(manager, snapshots)

	metadata := manager.Connection(connectionID).Metadata()
	require.NoError(t, manager.Connection(connectionID).Close())
	require.Len(t, closedProtoEvents(service.buildTrafficUpdates(manager, snapshots)), 1)

	// The subscription's own event, delivered late.
	late := service.applyConnectionEvent(trafficcontrol.ConnectionEvent{
		Type:     trafficcontrol.ConnectionEventClosed,
		ID:       connectionID,
		Metadata: metadata,
		ClosedAt: time.Now(),
	}, snapshots)
	require.NotNil(t, late)
	require.Equal(t, ConnectionEventType_CONNECTION_EVENT_CLOSED, late.Type,
		"a late closed event must never be translated into a new one")
	require.NotZero(t, late.ClosedAt)
	require.Empty(t, snapshots)

	// A closed event for an id it never announced is still a closed event, not a new row.
	unknownID, err := uuid.NewV4()
	require.NoError(t, err)
	unknown := service.applyConnectionEvent(trafficcontrol.ConnectionEvent{
		Type:     trafficcontrol.ConnectionEventClosed,
		ID:       unknownID,
		Metadata: metadata,
	}, snapshots)
	require.NotNil(t, unknown)
	require.Equal(t, ConnectionEventType_CONNECTION_EVENT_CLOSED, unknown.Type)
	require.NotZero(t, unknown.ClosedAt, "a closed event without a timestamp must still be closed")
}

// TestClosedEventCarriesTheFinalTotals is P4 of the acceptance list, on the server side: the row the
// client renders as closed must carry the final counters and a non-zero ClosedAt.
func TestClosedEventCarriesTheFinalTotals(t *testing.T) {
	manager := trafficcontrol.NewManager()
	connectionID := joinDaemonConnection(t, manager, "totals.example")
	metadata := manager.Connection(connectionID).Metadata()
	metadata.Upload.Add(1234)
	metadata.Download.Add(5678)

	service := &StartedService{}
	snapshots := make(map[uuid.UUID]connectionSnapshot)
	service.buildInitialConnectionState(manager, snapshots)
	require.NoError(t, manager.Connection(connectionID).Close())

	closed := closedProtoEvents(service.buildTrafficUpdates(manager, snapshots))
	require.Len(t, closed, 1)
	require.NotNil(t, closed[0].Connection)
	require.EqualValues(t, 1234, closed[0].Connection.UplinkTotal)
	require.EqualValues(t, 5678, closed[0].Connection.DownlinkTotal)
	require.NotZero(t, closed[0].Connection.CreatedAt)
	require.NotEmpty(t, closed[0].Connection.ChainList)
}

// TestReconcileKeepsTheTrafficDeltaAccounting sums the deltas the client will add up, so "the totals
// are conserved across the reconcile" is a measurement rather than a hope.
func TestReconcileKeepsTheTrafficDeltaAccounting(t *testing.T) {
	manager := trafficcontrol.NewManager()
	connectionID := joinDaemonConnection(t, manager, "delta.example")
	service := &StartedService{}
	snapshots := make(map[uuid.UUID]connectionSnapshot)
	service.buildInitialConnectionState(manager, snapshots)

	metadata := manager.Connection(connectionID).Metadata()
	var clientUpload, clientDownload int64
	apply := func(events []*ConnectionEvent) {
		for _, event := range events {
			switch event.Type {
			case ConnectionEventType_CONNECTION_EVENT_NEW:
				clientUpload += event.Connection.UplinkTotal
				clientDownload += event.Connection.DownlinkTotal
			case ConnectionEventType_CONNECTION_EVENT_UPDATE:
				clientUpload += event.UplinkDelta
				clientDownload += event.DownlinkDelta
			}
		}
	}
	metadata.Upload.Add(100)
	metadata.Download.Add(200)
	apply(service.buildTrafficUpdates(manager, snapshots))
	metadata.Upload.Add(50)
	metadata.Download.Add(25)
	apply(service.buildTrafficUpdates(manager, snapshots))

	require.NoError(t, manager.Connection(connectionID).Close())
	apply(service.buildTrafficUpdates(manager, snapshots))

	require.EqualValues(t, 150, clientUpload, "the client's upload total must equal the manager's")
	require.EqualValues(t, 225, clientDownload, "the client's download total must equal the manager's")
	uploadTotal, downloadTotal := manager.Total()
	require.Equal(t, uploadTotal, clientUpload)
	require.Equal(t, downloadTotal, clientDownload)
}
