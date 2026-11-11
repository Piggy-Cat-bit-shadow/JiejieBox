package libbox

// Recovery over the REAL client, a REAL in-process gRPC server and the REAL consumer.
//
// # The reviewed failure
//
// The consumer applied a batch whose predecessor never arrived: a lost CLOSED kept its row forever.
// The wire's instanceId/sequence/dropped are what make "the predecessor arrived" checkable, and the
// client is the layer that can repair it - it owns the subscription, so it can cancel a broken stream
// and open a new one, whose first batch is the daemon's snapshot.
//
// These tests pin both halves of that path: the incomplete batch is NOT delivered, a fresh
// subscription is made, the repaired snapshot is delivered with Resynced set, and the final consumer
// table equals the daemon's snapshot. An older daemon (zero fields) must keep the old behaviour: one
// subscription, no resync.

import (
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/daemon"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// resyncDaemon scripts two subscriptions: a snapshot, an incomplete increment, then (after the client
// cancels) a fresh snapshot that reflects the current state.
type resyncDaemon struct {
	daemon.UnimplementedStartedServiceServer

	access        sync.Mutex
	subscriptions int
}

func (s *resyncDaemon) subscriptionNumber() int {
	s.access.Lock()
	defer s.access.Unlock()
	s.subscriptions++
	return s.subscriptions
}

func (s *resyncDaemon) subscriptionsMade() int {
	s.access.Lock()
	defer s.access.Unlock()
	return s.subscriptions
}

func (s *resyncDaemon) SubscribeConnections(_ *daemon.SubscribeConnectionsRequest, stream daemon.StartedService_SubscribeConnectionsServer) error {
	number := s.subscriptionNumber()
	switch number {
	case 1:
		if err := stream.Send(&daemon.ConnectionEvents{
			InstanceId: "instance-1",
			Sequence:   1,
			Dropped:    0,
			Reset_:     true,
			Events:     []*daemon.ConnectionEvent{newEvent("old")},
		}); err != nil {
			return err
		}
		// Sequence 2 is missing; the drop count rose to 1. The consumer must not apply this.
		if err := stream.Send(&daemon.ConnectionEvents{
			InstanceId: "instance-1",
			Sequence:   3,
			Dropped:    1,
			Events:     []*daemon.ConnectionEvent{newEvent("incomplete")},
		}); err != nil {
			return err
		}
	default:
		if err := stream.Send(&daemon.ConnectionEvents{
			InstanceId: "instance-1",
			Sequence:   2,
			Dropped:    1,
			Reset_:     true,
			Events:     []*daemon.ConnectionEvent{newEvent("current")},
		}); err != nil {
			return err
		}
	}
	<-stream.Context().Done()
	return nil
}

func newEvent(id string) *daemon.ConnectionEvent {
	return &daemon.ConnectionEvent{
		Type:       daemon.ConnectionEventType_CONNECTION_EVENT_NEW,
		Id:         id,
		Connection: &daemon.Connection{Id: id},
	}
}

// resyncRecordingHandler records the batches it is given and applies them to a real Connections.
type resyncRecordingHandler struct {
	connections *Connections

	access  sync.Mutex
	batches []*ConnectionEvents
}

func (h *resyncRecordingHandler) WriteConnectionEvents(events *ConnectionEvents) {
	h.access.Lock()
	h.batches = append(h.batches, events)
	h.access.Unlock()
	h.connections.ApplyEvents(events)
}

func (h *resyncRecordingHandler) snapshotBatches() []*ConnectionEvents {
	h.access.Lock()
	defer h.access.Unlock()
	return append([]*ConnectionEvents(nil), h.batches...)
}

func (h *resyncRecordingHandler) Connected()                        {}
func (h *resyncRecordingHandler) Disconnected(string)               {}
func (h *resyncRecordingHandler) SetDefaultLogLevel(int32)          {}
func (h *resyncRecordingHandler) ClearLogs()                        {}
func (h *resyncRecordingHandler) WriteLogs(LogIterator)             {}
func (h *resyncRecordingHandler) WriteStatus(*StatusMessage)        {}
func (h *resyncRecordingHandler) WriteGroups(OutboundGroupIterator) {}
func (h *resyncRecordingHandler) WriteOutbounds(OutboundGroupItemIterator) {
}
func (h *resyncRecordingHandler) InitializeClashMode(StringIterator, string) {}
func (h *resyncRecordingHandler) UpdateClashMode(string)                     {}

func TestTheClientResubscribesWhenAnIncrementIsLost(t *testing.T) {
	daemonServer := &resyncDaemon{}
	address := bridgeTestServer(t, func(server *grpc.Server) {
		daemon.RegisterStartedServiceServer(server, daemonServer)
	})

	connections := NewConnections()
	handler := &resyncRecordingHandler{connections: connections}
	client := NewCommandClient(handler, &CommandClientOptions{commands: []int32{CommandConnections}})
	t.Cleanup(func() { _ = client.Disconnect() })
	require.NoError(t, client.connect(dialTestServer(address)))

	require.Eventually(t, func() bool {
		return daemonServer.subscriptionsMade() >= 2
	}, 10*time.Second, 5*time.Millisecond, "the client must resubscribe after the lost increment")

	require.Eventually(t, func() bool {
		activeIDs, _ := rowsByState(connections, ConnectionStateActive)
		return len(activeIDs) == 1 && activeIDs[0] == "current"
	}, 10*time.Second, 5*time.Millisecond,
		"the repaired snapshot is the authority: the incomplete row never appears")

	batches := handler.snapshotBatches()
	require.NotEmpty(t, batches)
	var repaired *ConnectionEvents
	for _, batch := range batches {
		require.NotContains(t, batchIDs(batch), "incomplete",
			"the incomplete increment must never be delivered")
		if batch.Resynced {
			repaired = batch
		}
	}
	require.NotNil(t, repaired, "the repairing snapshot must say it is a repair")
	require.True(t, repaired.Reset, "a repair is a Reset snapshot")
	require.False(t, connections.NeedsResync())
}

func batchIDs(batch *ConnectionEvents) []string {
	ids := make([]string, 0, len(batch.events))
	for _, event := range batch.events {
		ids = append(ids, event.ID)
	}
	return ids
}

// oldStyleDaemon streams batches with zero wire fields, like a daemon from before the fields existed.
type oldStyleDaemon struct {
	daemon.UnimplementedStartedServiceServer

	access        sync.Mutex
	subscriptions int
}

func (s *oldStyleDaemon) SubscribeConnections(_ *daemon.SubscribeConnectionsRequest, stream daemon.StartedService_SubscribeConnectionsServer) error {
	s.access.Lock()
	s.subscriptions++
	s.access.Unlock()
	if err := stream.Send(&daemon.ConnectionEvents{
		Reset_: true,
		Events: []*daemon.ConnectionEvent{newEvent("a")},
	}); err != nil {
		return err
	}
	if err := stream.Send(&daemon.ConnectionEvents{
		Events: []*daemon.ConnectionEvent{{
			Type:       daemon.ConnectionEventType_CONNECTION_EVENT_CLOSED,
			Id:         "a",
			ClosedAt:   time.Now().UnixMilli(),
			Connection: &daemon.Connection{Id: "a"},
		}},
	}); err != nil {
		return err
	}
	<-stream.Context().Done()
	return nil
}

func TestAnOldDaemonKeepsTheExistingBehaviour(t *testing.T) {
	oldServer := &oldStyleDaemon{}
	address := bridgeTestServer(t, func(server *grpc.Server) {
		daemon.RegisterStartedServiceServer(server, oldServer)
	})

	connections := NewConnections()
	handler := &resyncRecordingHandler{connections: connections}
	client := NewCommandClient(handler, &CommandClientOptions{commands: []int32{CommandConnections}})
	t.Cleanup(func() { _ = client.Disconnect() })
	require.NoError(t, client.connect(dialTestServer(address)))

	require.Eventually(t, func() bool {
		activeIDs, _ := rowsByState(connections, ConnectionStateActive)
		return len(activeIDs) == 0
	}, 10*time.Second, 5*time.Millisecond, "zero wire fields keep the Reset-based behaviour")

	time.Sleep(100 * time.Millisecond)
	oldServer.access.Lock()
	subscriptions := oldServer.subscriptions
	oldServer.access.Unlock()
	require.Equal(t, 1, subscriptions, "an old daemon must not be forced into a resync loop")
	for _, batch := range handler.snapshotBatches() {
		require.False(t, batch.Resynced)
	}
}
