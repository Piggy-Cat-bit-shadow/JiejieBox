package libbox

// A heartbeat batch carries no events but a RAISED drop count: that is the signal the daemon sends
// when a subscription lost events and the source is silent. The client must treat it as a loss (not as
// an empty increment), resubscribe, and take the snapshot. The negative control is equally important:
// a heartbeat with no new loss must not trigger anything, or every quiet second would resubscribe.

import (
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/daemon"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// heartbeatDaemon sends a snapshot, then an empty batch whose drop count rose, then holds.
type heartbeatDaemon struct {
	daemon.UnimplementedStartedServiceServer
	// loss is the cumulative drop count announced on the heartbeat.
	loss uint64
	// withHeartbeat=false sends a NO-LOSS heartbeat and then a normal increment (negative control).
	withHeartbeat bool

	access        sync.Mutex
	subscriptions int
}

func (s *heartbeatDaemon) count() int {
	s.access.Lock()
	defer s.access.Unlock()
	s.subscriptions++
	return s.subscriptions
}

func (s *heartbeatDaemon) made() int {
	s.access.Lock()
	defer s.access.Unlock()
	return s.subscriptions
}

func (s *heartbeatDaemon) SubscribeConnections(_ *daemon.SubscribeConnectionsRequest, stream daemon.StartedService_SubscribeConnectionsServer) error {
	if s.count() != 1 {
		if err := stream.Send(&daemon.ConnectionEvents{
			InstanceId: "i1", Sequence: 1, Reset_: true,
			Events: []*daemon.ConnectionEvent{newEvent("current")},
		}); err != nil {
			return err
		}
		// After the repair, the stream is healthy: a normal increment must apply.
		if err := stream.Send(&daemon.ConnectionEvents{
			InstanceId: "i1", Sequence: 2,
			Events: []*daemon.ConnectionEvent{newEvent("after")},
		}); err != nil {
			return err
		}
		<-stream.Context().Done()
		return nil
	}
	if err := stream.Send(&daemon.ConnectionEvents{
		InstanceId: "i1", Sequence: 1, Reset_: true,
		Events: []*daemon.ConnectionEvent{newEvent("old")},
	}); err != nil {
		return err
	}
	if s.withHeartbeat {
		if err := stream.Send(&daemon.ConnectionEvents{
			InstanceId: "i1", Sequence: 2, Dropped: s.loss,
		}); err != nil {
			return err
		}
	} else {
		if err := stream.Send(&daemon.ConnectionEvents{
			InstanceId: "i1", Sequence: 2, Dropped: 0,
		}); err != nil {
			return err
		}
		if err := stream.Send(&daemon.ConnectionEvents{
			InstanceId: "i1", Sequence: 3, Dropped: 0,
			Events: []*daemon.ConnectionEvent{newEvent("next")},
		}); err != nil {
			return err
		}
	}
	<-stream.Context().Done()
	return nil
}

func TestALossHeartbeatTriggersAResync(t *testing.T) {
	daemonServer := &heartbeatDaemon{withHeartbeat: true, loss: 36}
	address := bridgeTestServer(t, func(server *grpc.Server) {
		daemon.RegisterStartedServiceServer(server, daemonServer)
	})

	connections := NewConnections()
	handler := &resyncRecordingHandler{connections: connections}
	client := NewCommandClient(handler, &CommandClientOptions{commands: []int32{CommandConnections}})
	t.Cleanup(func() { _ = client.Disconnect() })
	require.NoError(t, client.connect(dialTestServer(address)))

	require.Eventually(t, func() bool {
		activeIDs, _ := rowsByState(connections, ConnectionStateActive)
		return len(activeIDs) == 2
	}, 10*time.Second, 5*time.Millisecond, "the heartbeat must lead to the repaired snapshot and healthy increments")
	activeIDs, _ := rowsByState(connections, ConnectionStateActive)
	require.ElementsMatch(t, []string{"current", "after"}, activeIDs,
		"the repaired snapshot is the authority and the following increment applies")
	require.Equal(t, 2, daemonServer.made(), "exactly one resubscription")

	var repaired bool
	for _, batch := range handler.snapshotBatches() {
		if batch.Resynced {
			repaired = true
		}
	}
	require.True(t, repaired, "the repair snapshot must be marked")
	require.False(t, connections.NeedsResync())

	// Cancellation is final: no new subscription is started after Disconnect.
	require.NoError(t, client.Disconnect())
	time.Sleep(100 * time.Millisecond)
	require.Equal(t, 2, daemonServer.made(), "cancellation must not start another subscription")
}

func TestACleanHeartbeatDoesNotResubscribe(t *testing.T) {
	daemonServer := &heartbeatDaemon{}
	address := bridgeTestServer(t, func(server *grpc.Server) {
		daemon.RegisterStartedServiceServer(server, daemonServer)
	})

	connections := NewConnections()
	handler := &resyncRecordingHandler{connections: connections}
	client := NewCommandClient(handler, &CommandClientOptions{commands: []int32{CommandConnections}})
	t.Cleanup(func() { _ = client.Disconnect() })
	require.NoError(t, client.connect(dialTestServer(address)))

	require.Eventually(t, func() bool {
		activeIDs, _ := rowsByState(connections, ConnectionStateActive)
		return len(activeIDs) == 2
	}, 10*time.Second, 5*time.Millisecond, "the quiet heartbeat is an empty increment, then the real one applies")
	time.Sleep(100 * time.Millisecond)
	require.Equal(t, 1, daemonServer.made(), "a heartbeat without new loss must not resubscribe")
}
