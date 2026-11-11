package libbox

// The stale-Reset half of the classifier, over the REAL client and the REAL table.
//
// The table-level test proves an old Reset cannot clear a newer table; this one proves the CLIENT does
// not deliver it either, and that the batch after it still applies. The script is
// `1(Reset), 2, 1(Reset, stale), 3`: an old snapshot arriving late must not reach the consumer, must
// not move the watermark, and must not turn 3 into a gap.

import (
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/daemon"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type staleResetDaemon struct {
	daemon.UnimplementedStartedServiceServer

	access        sync.Mutex
	subscriptions int
}

func (s *staleResetDaemon) countSubscription() int {
	s.access.Lock()
	defer s.access.Unlock()
	s.subscriptions++
	return s.subscriptions
}

func (s *staleResetDaemon) subscriptionsMade() int {
	s.access.Lock()
	defer s.access.Unlock()
	return s.subscriptions
}

func (s *staleResetDaemon) SubscribeConnections(_ *daemon.SubscribeConnectionsRequest, stream daemon.StartedService_SubscribeConnectionsServer) error {
	if s.countSubscription() != 1 {
		<-stream.Context().Done()
		return nil
	}
	batches := []*daemon.ConnectionEvents{
		{InstanceId: "i1", Sequence: 1, Reset_: true, Events: []*daemon.ConnectionEvent{newEvent("one")}},
		{InstanceId: "i1", Sequence: 2, Events: []*daemon.ConnectionEvent{newEvent("two")}},
		// The stale snapshot: same subscription, sequence 1, arriving after 2.
		{InstanceId: "i1", Sequence: 1, Reset_: true, Events: []*daemon.ConnectionEvent{newEvent("old-only")}},
		{InstanceId: "i1", Sequence: 3, Events: []*daemon.ConnectionEvent{newEvent("three")}},
	}
	for _, batch := range batches {
		if err := stream.Send(batch); err != nil {
			return err
		}
	}
	<-stream.Context().Done()
	return nil
}

func TestAStaleResetIsNotDeliveredAndTheNextIncrementApplies(t *testing.T) {
	daemonServer := &staleResetDaemon{}
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
		return len(activeIDs) == 3
	}, 10*time.Second, 5*time.Millisecond, "the increments on both sides of the stale reset must apply")

	activeIDs, _ := rowsByState(connections, ConnectionStateActive)
	require.ElementsMatch(t, []string{"one", "two", "three"}, activeIDs)
	require.Equal(t, 1, daemonServer.subscriptionsMade(), "a stale Reset is not a loss")

	sequences := make([]uint64, 0, 3)
	for _, batch := range handler.snapshotBatches() {
		sequences = append(sequences, uint64(batch.Sequence()))
	}
	require.Equal(t, []uint64{1, 2, 3}, sequences,
		"the stale reset is not delivered and does not move the watermark")
	require.False(t, connections.NeedsResync())
}
