package libbox

// The resync budget: a stream that keeps losing events must be repaired a bounded number of times and
// then reported, not retried forever. A resubscribe that fails outright is a connection failure, not a
// loss loop.

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/daemon"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// lossLoopDaemon sends a snapshot followed by a loss heartbeat on EVERY subscription, so every repair
// is immediately followed by another loss.
type lossLoopDaemon struct {
	daemon.UnimplementedStartedServiceServer

	access        sync.Mutex
	subscriptions int
	dropped       uint64
}

func (s *lossLoopDaemon) count() int {
	s.access.Lock()
	defer s.access.Unlock()
	s.subscriptions++
	s.dropped++
	return s.subscriptions
}

func (s *lossLoopDaemon) made() int {
	s.access.Lock()
	defer s.access.Unlock()
	return s.subscriptions
}

func (s *lossLoopDaemon) SubscribeConnections(_ *daemon.SubscribeConnectionsRequest, stream daemon.StartedService_SubscribeConnectionsServer) error {
	dropped := uint64(s.count())
	if err := stream.Send(&daemon.ConnectionEvents{
		InstanceId: "i1", Sequence: 1, Dropped: dropped - 1, Reset_: true,
		Events: []*daemon.ConnectionEvent{newEvent("row")},
	}); err != nil {
		return err
	}
	if err := stream.Send(&daemon.ConnectionEvents{
		InstanceId: "i1", Sequence: 2, Dropped: dropped,
	}); err != nil {
		return err
	}
	<-stream.Context().Done()
	return nil
}

func TestTheResyncBudgetIsFinite(t *testing.T) {
	daemonServer := &lossLoopDaemon{}
	address := bridgeTestServer(t, func(server *grpc.Server) {
		daemon.RegisterStartedServiceServer(server, daemonServer)
	})

	connections := NewConnections()
	handler := &countingHandler{connections: connections}
	client := NewCommandClient(handler, &CommandClientOptions{commands: []int32{CommandConnections}})
	t.Cleanup(func() { _ = client.Disconnect() })
	require.NoError(t, client.connect(dialTestServer(address)))

	require.Eventually(t, func() bool { return handler.disconnected.Load() == 1 },
		10*time.Second, 5*time.Millisecond, "the budget must end in a reported failure, not a loop")
	require.Equal(t, 1+connectionResyncAttempts, daemonServer.made(),
		"one initial subscription plus exactly the documented repair budget")
	require.EqualValues(t, 1, handler.disconnected.Load(), "reported once")
}

// failingResubscribeDaemon loses once, then every new subscription fails.
type failingResubscribeDaemon struct {
	daemon.UnimplementedStartedServiceServer

	access        sync.Mutex
	subscriptions int
}

func (s *failingResubscribeDaemon) count() int {
	s.access.Lock()
	defer s.access.Unlock()
	s.subscriptions++
	return s.subscriptions
}

func (s *failingResubscribeDaemon) made() int {
	s.access.Lock()
	defer s.access.Unlock()
	return s.subscriptions
}

func (s *failingResubscribeDaemon) SubscribeConnections(_ *daemon.SubscribeConnectionsRequest, stream daemon.StartedService_SubscribeConnectionsServer) error {
	if s.count() > 1 {
		return status.Error(codes.Unavailable, "the daemon is restarting")
	}
	if err := stream.Send(&daemon.ConnectionEvents{
		InstanceId: "i1", Sequence: 1, Reset_: true,
		Events: []*daemon.ConnectionEvent{newEvent("row")},
	}); err != nil {
		return err
	}
	if err := stream.Send(&daemon.ConnectionEvents{
		InstanceId: "i1", Sequence: 2, Dropped: 1,
	}); err != nil {
		return err
	}
	<-stream.Context().Done()
	return nil
}

func TestAResubscribeFailureIsReportedOnce(t *testing.T) {
	daemonServer := &failingResubscribeDaemon{}
	address := bridgeTestServer(t, func(server *grpc.Server) {
		daemon.RegisterStartedServiceServer(server, daemonServer)
	})

	connections := NewConnections()
	handler := &countingHandler{connections: connections}
	client := NewCommandClient(handler, &CommandClientOptions{commands: []int32{CommandConnections}})
	t.Cleanup(func() { _ = client.Disconnect() })
	require.NoError(t, client.connect(dialTestServer(address)))

	require.Eventually(t, func() bool { return handler.disconnected.Load() == 1 },
		10*time.Second, 5*time.Millisecond, "an establishment failure is reported")
	time.Sleep(100 * time.Millisecond)
	require.Equal(t, 2, daemonServer.made(), "one initial subscription and one failed repair attempt")
	require.EqualValues(t, 1, handler.disconnected.Load(), "not retried in a loop")
}

// countingHandler applies batches, records them and counts disconnects.
type countingHandler struct {
	connections  *Connections
	disconnected atomic.Int32

	access  sync.Mutex
	batches []*ConnectionEvents
}

func (h *countingHandler) WriteConnectionEvents(events *ConnectionEvents) {
	h.access.Lock()
	h.batches = append(h.batches, events)
	h.access.Unlock()
	h.connections.ApplyEvents(events)
}

func (h *countingHandler) snapshot() []*ConnectionEvents {
	h.access.Lock()
	defer h.access.Unlock()
	return append([]*ConnectionEvents(nil), h.batches...)
}
func (h *countingHandler) Connected()                        {}
func (h *countingHandler) Disconnected(string)               { h.disconnected.Add(1) }
func (h *countingHandler) SetDefaultLogLevel(int32)          {}
func (h *countingHandler) ClearLogs()                        {}
func (h *countingHandler) WriteLogs(LogIterator)             {}
func (h *countingHandler) WriteStatus(*StatusMessage)        {}
func (h *countingHandler) WriteGroups(OutboundGroupIterator) {}
func (h *countingHandler) WriteOutbounds(OutboundGroupItemIterator) {
}
func (h *countingHandler) InitializeClashMode(StringIterator, string) {}
func (h *countingHandler) UpdateClashMode(string)                     {}
