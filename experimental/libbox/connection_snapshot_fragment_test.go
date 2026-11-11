package libbox

// The client side of a split snapshot: fragments are buffered and delivered as ONE logical Reset, an
// interrupted group is never a baseline, and a missing part forces a resync. The classifier only sees
// the complete group, so the stale/resync rules stay unchanged.

import (
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/daemon"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type fragmentScriptDaemon struct {
	daemon.UnimplementedStartedServiceServer

	scripts [][]*daemon.ConnectionEvents
	// closeAfterScript ends the stream after the script instead of holding it open.
	closeAfterScript bool

	access        sync.Mutex
	subscriptions int
}

func (s *fragmentScriptDaemon) count() int {
	s.access.Lock()
	defer s.access.Unlock()
	index := s.subscriptions
	s.subscriptions++
	return index
}

func (s *fragmentScriptDaemon) made() int {
	s.access.Lock()
	defer s.access.Unlock()
	return s.subscriptions
}

func (s *fragmentScriptDaemon) SubscribeConnections(_ *daemon.SubscribeConnectionsRequest, stream daemon.StartedService_SubscribeConnectionsServer) error {
	index := s.count()
	if index >= len(s.scripts) {
		<-stream.Context().Done()
		return nil
	}
	for _, batch := range s.scripts[index] {
		if err := stream.Send(batch); err != nil {
			return err
		}
	}
	if s.closeAfterScript {
		return nil
	}
	<-stream.Context().Done()
	return nil
}

func fragmentBatch(instanceID string, sequence uint64, part uint32, parts uint32, reset bool, ids ...string) *daemon.ConnectionEvents {
	batch := &daemon.ConnectionEvents{
		InstanceId:    instanceID,
		Sequence:      sequence,
		Reset_:        reset,
		SnapshotPart:  part,
		SnapshotParts: parts,
	}
	for _, id := range ids {
		batch.Events = append(batch.Events, newEvent(id))
	}
	return batch
}

func TestAMultiPartSnapshotIsAppliedOnlyWhenComplete(t *testing.T) {
	daemonServer := &fragmentScriptDaemon{scripts: [][]*daemon.ConnectionEvents{{
		fragmentBatch("i1", 1, 1, 3, true, "a", "b"),
		fragmentBatch("i1", 2, 2, 3, false, "c"),
		fragmentBatch("i1", 3, 3, 3, false, "d"),
	}}}
	address := bridgeTestServer(t, func(server *grpc.Server) {
		daemon.RegisterStartedServiceServer(server, daemonServer)
	})
	connections := NewConnections()
	handler := &countingHandler{connections: connections}
	client := NewCommandClient(handler, &CommandClientOptions{commands: []int32{CommandConnections}})
	t.Cleanup(func() { _ = client.Disconnect() })
	require.NoError(t, client.connect(dialTestServer(address)))

	require.Eventually(t, func() bool {
		activeIDs, _ := rowsByState(connections, ConnectionStateActive)
		return len(activeIDs) == 4
	}, 10*time.Second, 5*time.Millisecond, "the complete group is applied once")

	batches := handler.snapshot()
	require.Len(t, batches, 1, "fragments are not delivered one by one")
	require.True(t, batches[0].Reset)
	require.EqualValues(t, 3, batches[0].Sequence(), "the group's sequence is its LAST part")
	require.Zero(t, batches[0].Dropped())
	require.Len(t, batches[0].events, 4)
	require.EqualValues(t, 1, daemonServer.made())
}

func TestAnInterruptedSnapshotIsNotABaseline(t *testing.T) {
	daemonServer := &fragmentScriptDaemon{
		closeAfterScript: true,
		scripts: [][]*daemon.ConnectionEvents{{
			fragmentBatch("i1", 1, 1, 3, true, "a"),
			fragmentBatch("i1", 2, 2, 3, false, "b"),
		}},
	}
	// The stream ends after part 2: the group is interrupted.
	address := bridgeTestServer(t, func(server *grpc.Server) {
		daemon.RegisterStartedServiceServer(server, daemonServer)
	})
	connections := NewConnections()
	handler := &countingHandler{connections: connections}
	client := NewCommandClient(handler, &CommandClientOptions{commands: []int32{CommandConnections}})
	t.Cleanup(func() { _ = client.Disconnect() })
	require.NoError(t, client.connect(dialTestServer(address)))

	require.Eventually(t, func() bool { return handler.disconnected.Load() >= 1 },
		10*time.Second, 5*time.Millisecond, "an interrupted snapshot ends the stream")
	activeIDs, _ := rowsByState(connections, ConnectionStateActive)
	require.Empty(t, activeIDs, "a partial group must never become the table")
	require.False(t, connections.NeedsResync())
	require.Empty(t, handler.snapshot())
}

func TestAMissingSnapshotPartForcesAResync(t *testing.T) {
	complete := &daemon.ConnectionEvents{
		InstanceId: "i1", Sequence: 1, Reset_: true,
		Events: []*daemon.ConnectionEvent{newEvent("a"), newEvent("b"), newEvent("c"), newEvent("d")},
	}
	daemonServer := &fragmentScriptDaemon{scripts: [][]*daemon.ConnectionEvents{
		{
			fragmentBatch("i1", 1, 1, 3, true, "a"),
			// Part 2 is missing.
			fragmentBatch("i1", 3, 3, 3, false, "d"),
		},
		{complete},
	}}
	address := bridgeTestServer(t, func(server *grpc.Server) {
		daemon.RegisterStartedServiceServer(server, daemonServer)
	})
	connections := NewConnections()
	handler := &countingHandler{connections: connections}
	client := NewCommandClient(handler, &CommandClientOptions{commands: []int32{CommandConnections}})
	t.Cleanup(func() { _ = client.Disconnect() })
	require.NoError(t, client.connect(dialTestServer(address)))

	require.Eventually(t, func() bool {
		activeIDs, _ := rowsByState(connections, ConnectionStateActive)
		return len(activeIDs) == 4
	}, 10*time.Second, 5*time.Millisecond, "the resubscribed complete snapshot is the authority")
	require.Equal(t, 2, daemonServer.made())
	require.False(t, connections.NeedsResync())
}

func TestAnIncrementInsideASnapshotForcesAResync(t *testing.T) {
	complete := &daemon.ConnectionEvents{
		InstanceId: "i1", Sequence: 1, Reset_: true,
		Events: []*daemon.ConnectionEvent{newEvent("a"), newEvent("b"), newEvent("c")},
	}
	daemonServer := &fragmentScriptDaemon{scripts: [][]*daemon.ConnectionEvents{
		{
			fragmentBatch("i1", 1, 1, 3, true, "a"),
			// An ordinary increment interleaved into the group: neither a fragment nor orderable.
			{InstanceId: "i1", Sequence: 2, Events: []*daemon.ConnectionEvent{newEvent("x")}},
			fragmentBatch("i1", 3, 3, 3, false, "c"),
		},
		{complete},
	}}
	address := bridgeTestServer(t, func(server *grpc.Server) {
		daemon.RegisterStartedServiceServer(server, daemonServer)
	})
	connections := NewConnections()
	handler := &countingHandler{connections: connections}
	client := NewCommandClient(handler, &CommandClientOptions{commands: []int32{CommandConnections}})
	t.Cleanup(func() { _ = client.Disconnect() })
	require.NoError(t, client.connect(dialTestServer(address)))

	require.Eventually(t, func() bool {
		activeIDs, _ := rowsByState(connections, ConnectionStateActive)
		return len(activeIDs) == 3
	}, 10*time.Second, 5*time.Millisecond, "the interleaved group is discarded and repaired")
	require.Equal(t, 2, daemonServer.made())
}

// TestAnOldConsumerSeesFragmentsAsResetThenIncrements documents the mixed-version contract: a consumer
// that does not know the fragment fields sees part 1 as a Reset and the rest as sequential
// increments. It converges to the same rows, but it has no atomic completion - so an interrupted
// group leaves a partial table, which is why the new client must buffer.
func TestAnOldConsumerSeesFragmentsAsResetThenIncrements(t *testing.T) {
	fragments := []*daemon.ConnectionEvents{
		fragmentBatch("i1", 1, 1, 3, true, "a", "b"),
		fragmentBatch("i1", 2, 2, 3, false, "c"),
		fragmentBatch("i1", 3, 3, 3, false, "d"),
	}
	connections := NewConnections()
	for _, fragment := range fragments {
		connections.ApplyEvents(connectionEventsFromGRPC(fragment))
	}
	activeIDs, _ := rowsByState(connections, ConnectionStateActive)
	require.Len(t, activeIDs, 4)
}
