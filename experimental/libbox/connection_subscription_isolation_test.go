package libbox

// P1.4 — a reconnect must not let the OLD subscription's in-flight batch roll the table back.
//
// The reviewed failure: the old stream's batch reached the handler and was paused there; a new
// subscription applied its snapshot ("new"); the old callback then resumed and applied its own
// state ("old"). A single `Recv`-time context check cannot cover that window - the callback is
// already inside the consumer. The fix therefore carries the subscription's identity to the APPLY
// point, and the consumer ignores a batch from a superseded subscription.

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/daemon"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// snapshotDaemon sends one Reset snapshot naming its id, then ends the stream.
type snapshotDaemon struct {
	daemon.UnimplementedStartedServiceServer
	id string
	// hold keeps the stream open after the snapshot, so only the OTHER stream can terminate.
	hold bool
}

func (s *snapshotDaemon) SubscribeConnections(_ *daemon.SubscribeConnectionsRequest, stream daemon.StartedService_SubscribeConnectionsServer) error {
	err := stream.Send(&daemon.ConnectionEvents{
		Reset_: true,
		Events: []*daemon.ConnectionEvent{{
			Type:       daemon.ConnectionEventType_CONNECTION_EVENT_NEW,
			Id:         s.id,
			Connection: &daemon.Connection{Id: s.id},
		}},
	})
	if err != nil {
		return err
	}
	if s.hold {
		<-stream.Context().Done()
	}
	return nil
}

// gatedHandler pauses the FIRST batch it receives (holding it inside the consumer's caller), so the
// test can install a newer subscription before that batch is applied.
type gatedHandler struct {
	connections *Connections

	calls        atomic.Int32
	disconnected atomic.Int32

	firstBatch chan struct{}
	release    sync.Once
	releaseCh  chan struct{}
}

func newGatedHandler(connections *Connections) *gatedHandler {
	return &gatedHandler{
		connections: connections,
		firstBatch:  make(chan struct{}),
		releaseCh:   make(chan struct{}),
	}
}

func (h *gatedHandler) WriteConnectionEvents(events *ConnectionEvents) {
	if h.calls.Add(1) == 1 {
		close(h.firstBatch)
		<-h.releaseCh
	}
	h.connections.ApplyEvents(events)
}

func (h *gatedHandler) releaseFirst() { h.release.Do(func() { close(h.releaseCh) }) }

func (h *gatedHandler) Connected()                        {}
func (h *gatedHandler) Disconnected(string)               { h.disconnected.Add(1) }
func (h *gatedHandler) SetDefaultLogLevel(int32)          {}
func (h *gatedHandler) ClearLogs()                        {}
func (h *gatedHandler) WriteLogs(LogIterator)             {}
func (h *gatedHandler) WriteStatus(*StatusMessage)        {}
func (h *gatedHandler) WriteGroups(OutboundGroupIterator) {}
func (h *gatedHandler) WriteOutbounds(OutboundGroupItemIterator) {
}
func (h *gatedHandler) InitializeClashMode(StringIterator, string) {}
func (h *gatedHandler) UpdateClashMode(string)                     {}

func connectionSnapshotIDs(t *testing.T, connections *Connections) []string {
	t.Helper()
	iterator := connections.Iterator()
	ids := make([]string, 0, 2)
	for iterator.HasNext() {
		ids = append(ids, iterator.Next().ID)
	}
	return ids
}

// TestAReconnectIgnoresTheOldSubscriptionsPausedBatch is the reviewed failure, end to end over gRPC.
func TestAReconnectIgnoresTheOldSubscriptionsPausedBatch(t *testing.T) {
	oldAddress := bridgeTestServer(t, func(server *grpc.Server) {
		daemon.RegisterStartedServiceServer(server, &snapshotDaemon{id: "old"})
	})
	newAddress := bridgeTestServer(t, func(server *grpc.Server) {
		daemon.RegisterStartedServiceServer(server, &snapshotDaemon{id: "new"})
	})

	connections := NewConnections()
	handler := newGatedHandler(connections)
	client := NewCommandClient(handler, &CommandClientOptions{commands: []int32{CommandConnections}})
	t.Cleanup(func() { _ = client.Disconnect() })

	dialTo := func(address string) func(context.Context) (*grpc.ClientConn, daemon.StartedServiceClient, error) {
		return func(ctx context.Context) (*grpc.ClientConn, daemon.StartedServiceClient, error) {
			connection, err := grpc.NewClient(address, localDialOptions(nil)...)
			if err != nil {
				return nil, nil, err
			}
			return connection, daemon.NewStartedServiceClient(connection), nil
		}
	}

	require.NoError(t, client.connect(dialTo(oldAddress)))
	select {
	case <-handler.firstBatch:
	case <-time.After(10 * time.Second):
		t.Fatal("the first subscription's batch never reached the handler")
	}
	// The old batch is now parked inside the consumer's caller.

	require.NoError(t, client.connect(dialTo(newAddress)))
	require.Eventually(t, func() bool {
		ids := connectionSnapshotIDs(t, connections)
		return len(ids) == 1 && ids[0] == "new"
	}, 10*time.Second, time.Millisecond, "the new subscription's snapshot must be applied")

	// The old callback resumes AFTER the new state exists.
	handler.releaseFirst()
	time.Sleep(100 * time.Millisecond)
	require.Equal(t, []string{"new"}, connectionSnapshotIDs(t, connections),
		"a batch from the superseded subscription must not roll the table back")
}

// TestAStaleStreamsTerminationDoesNotReportDisconnected keeps the other half of the window: the old
// stream ending after a reconnect is not a failure of the current connection.
func TestAStaleStreamsTerminationDoesNotReportDisconnected(t *testing.T) {
	// The old server ENDS its stream after the snapshot; the new one holds it open, so only the old
	// stream can produce a termination inside the test window.
	oldAddress := bridgeTestServer(t, func(server *grpc.Server) {
		daemon.RegisterStartedServiceServer(server, &snapshotDaemon{id: "old"})
	})
	heldAddress := bridgeTestServer(t, func(server *grpc.Server) {
		daemon.RegisterStartedServiceServer(server, &snapshotDaemon{id: "held", hold: true})
	})
	connections := NewConnections()
	handler := newGatedHandler(connections)
	client := NewCommandClient(handler, &CommandClientOptions{commands: []int32{CommandConnections}})
	t.Cleanup(func() { _ = client.Disconnect() })
	dialTo := func(address string) func(context.Context) (*grpc.ClientConn, daemon.StartedServiceClient, error) {
		return func(ctx context.Context) (*grpc.ClientConn, daemon.StartedServiceClient, error) {
			connection, err := grpc.NewClient(address, localDialOptions(nil)...)
			if err != nil {
				return nil, nil, err
			}
			return connection, daemon.NewStartedServiceClient(connection), nil
		}
	}
	require.NoError(t, client.connect(dialTo(oldAddress)))
	select {
	case <-handler.firstBatch:
	case <-time.After(10 * time.Second):
		t.Fatal("the first subscription's batch never reached the handler")
	}
	// The first server has already ended its stream, and the handler is paused inside the delivery,
	// so the terminal Recv has not happened yet. Reconnecting first makes it stale.
	require.NoError(t, client.connect(dialTo(heldAddress)))
	handler.releaseFirst()
	time.Sleep(200 * time.Millisecond)
	require.Zero(t, handler.disconnected.Load(),
		"the superseded stream's termination must not be reported as the current connection failing")
}
