package libbox

// Duplicate and stale batches must not change state - and must not be mistaken for a loss.
//
// # The reviewed failures
//
//   - On a real gRPC stream, `1(Reset), 2, 1(duplicate), 3` made the client subscribe TWICE: the
//     duplicate moved its watermark back to 1, so 3 looked like a gap. The table rejected the
//     duplicate, but the client had already acted on it.
//   - Directly on the table, applying `1(Reset), 2` and then an OLD `1(Reset)` from the same
//     subscription restored the old rows: Reset was accepted unconditionally.
//
// Both come from the same place: a second, different decision about what a batch means. The fix is
// the shared classifier (`classifyConnectionBatch`), which both layers use. A stale batch is not
// delivered, does not move any baseline, and is NOT a loss; a Reset only has authority when it comes
// from a newer subscription (or is the first baseline).

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/daemon"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// duplicateScriptDaemon sends `1R, 2, 1, 3` on the first subscription and then holds the stream open.
// Any second subscription is counted, so "did the client resubscribe?" is observable.
type duplicateScriptDaemon struct {
	daemon.UnimplementedStartedServiceServer

	access        sync.Mutex
	subscriptions int
}

func (s *duplicateScriptDaemon) countSubscription() int {
	s.access.Lock()
	defer s.access.Unlock()
	s.subscriptions++
	return s.subscriptions
}

func (s *duplicateScriptDaemon) subscriptionsMade() int {
	s.access.Lock()
	defer s.access.Unlock()
	return s.subscriptions
}

func (s *duplicateScriptDaemon) SubscribeConnections(_ *daemon.SubscribeConnectionsRequest, stream daemon.StartedService_SubscribeConnectionsServer) error {
	if s.countSubscription() != 1 {
		// A second subscription is a failure, but keep the stream open so the test can observe it
		// without racing the count.
		<-stream.Context().Done()
		return nil
	}
	send := func(events *daemon.ConnectionEvents) error { return stream.Send(events) }
	batches := []*daemon.ConnectionEvents{
		{InstanceId: "i1", Sequence: 1, Dropped: 0, Reset_: true, Events: []*daemon.ConnectionEvent{newEvent("one")}},
		{InstanceId: "i1", Sequence: 2, Dropped: 0, Events: []*daemon.ConnectionEvent{newEvent("two")}},
		{InstanceId: "i1", Sequence: 1, Dropped: 0, Events: []*daemon.ConnectionEvent{newEvent("one")}},
		{InstanceId: "i1", Sequence: 3, Dropped: 0, Events: []*daemon.ConnectionEvent{newEvent("three")}},
	}
	for _, batch := range batches {
		if err := send(batch); err != nil {
			return err
		}
	}
	<-stream.Context().Done()
	return nil
}

func TestADuplicateDoesNotResubscribeOrRegressTheTable(t *testing.T) {
	daemonServer := &duplicateScriptDaemon{}
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
	}, 10*time.Second, 5*time.Millisecond, "batches 1, 2 and 3 must all reach the consumer")

	require.Eventually(t, func() bool {
		return daemonServer.subscriptionsMade() == 1
	}, time.Second, 5*time.Millisecond)
	require.Equal(t, 1, daemonServer.subscriptionsMade(),
		"a duplicate batch is not a loss and must not cause a resubscription")

	// The duplicate was not delivered: the batch sequences are 1, 2, 3 exactly once each.
	sequences := make([]uint64, 0, 3)
	for _, batch := range handler.snapshotBatches() {
		sequences = append(sequences, uint64(batch.Sequence()))
	}
	require.Equal(t, []uint64{1, 2, 3}, sequences, "the stale duplicate is never delivered")
}

// TestAnOldResetCannotClearANewerTable drives the table directly: a same-subscription Reset that is
// not newer than the applied state has no authority.
func TestAnOldResetCannotClearANewerTable(t *testing.T) {
	connections := NewConnections()
	connections.ApplyEvents(&ConnectionEvents{
		Reset:        true,
		InstanceId:   "i1",
		sequence:     1,
		subscription: 7,
		events:       []*ConnectionEvent{newRow("a")},
	})
	connections.ApplyEvents(&ConnectionEvents{
		InstanceId:   "i1",
		sequence:     2,
		subscription: 7,
		events:       []*ConnectionEvent{newRow("b")},
	})
	require.ElementsMatch(t, []string{"a", "b"}, connectionIDs(t, connections))

	// The old Reset (same subscription, sequence 1) describes a state already superseded.
	connections.ApplyEvents(&ConnectionEvents{
		Reset:        true,
		InstanceId:   "i1",
		sequence:     1,
		subscription: 7,
		events:       []*ConnectionEvent{newRow("old-only")},
	})
	activeIDs, _ := rowsByState(connections, ConnectionStateActive)
	require.ElementsMatch(t, []string{"a", "b"}, activeIDs, "an old Reset must not clear the table")
	require.False(t, connections.NeedsResync(), "a stale Reset is not a loss signal")
}

// TestALegitimateBaselineStillReplacesTheTable: the recovery path is a NEW subscription's snapshot, and
// it must be accepted (otherwise the old-Reset fix would have closed recovery too).
func TestALegitimateBaselineStillReplacesTheTable(t *testing.T) {
	connections := NewConnections()
	connections.ApplyEvents(&ConnectionEvents{
		Reset:        true,
		InstanceId:   "i1",
		sequence:     5,
		subscription: 7,
		events:       []*ConnectionEvent{newRow("stale-row")},
	})
	connections.ApplyEvents(&ConnectionEvents{
		Reset:        true,
		InstanceId:   "i1",
		sequence:     1,
		subscription: 8,
		events:       []*ConnectionEvent{newRow("current")},
	})
	connections.ApplyEvents(&ConnectionEvents{
		InstanceId:   "i1",
		sequence:     2,
		subscription: 8,
		events:       []*ConnectionEvent{newRow("next")},
	})
	require.ElementsMatch(t, []string{"current", "next"}, connectionIDs(t, connections))
}

// TestAStaleSequenceWithADifferentDropCountIsNotALoss: a low sequence carrying new drop information
// must be ignored without resync - the drop count belongs to the batch that lost the events, not to an
// out-of-order duplicate.
func TestAStaleSequenceWithADifferentDropCountIsNotALoss(t *testing.T) {
	connections := NewConnections()
	connections.ApplyEvents(&ConnectionEvents{
		Reset:        true,
		InstanceId:   "i1",
		sequence:     1,
		subscription: 7,
		events:       []*ConnectionEvent{newRow("a")},
	})
	connections.ApplyEvents(&ConnectionEvents{
		InstanceId:   "i1",
		sequence:     2,
		dropped:      0,
		subscription: 7,
		events:       []*ConnectionEvent{newRow("b")},
	})
	connections.ApplyEvents(&ConnectionEvents{
		InstanceId:   "i1",
		sequence:     1,
		dropped:      5,
		subscription: 7,
		events:       []*ConnectionEvent{newRow("stale")},
	})
	activeIDs, _ := rowsByState(connections, ConnectionStateActive)
	require.ElementsMatch(t, []string{"a", "b"}, activeIDs)
	require.False(t, connections.NeedsResync(), "a stale batch must not set the resync flag")
}

// TestTheClassifierIsTheSingleOwner pins the pure decision table directly, including the boundary
// between stale and gap.
func TestTheClassifierIsTheSingleOwner(t *testing.T) {
	testCases := []struct {
		name   string
		state  connectionBatchState
		batch  *ConnectionEvents
		expect connectionBatchVerdict
	}{
		{"first reset", connectionBatchState{}, &ConnectionEvents{Reset: true, sequence: 1, subscription: 2}, batchAccept},
		{"older subscription", connectionBatchState{hasBaseline: true, subscription: 5, sequence: 3}, &ConnectionEvents{sequence: 4, subscription: 4}, batchStale},
		{"same-subscription stale reset", connectionBatchState{hasBaseline: true, subscription: 5, sequence: 3}, &ConnectionEvents{Reset: true, sequence: 3, subscription: 5}, batchStale},
		{"new-subscription reset", connectionBatchState{hasBaseline: true, subscription: 5, sequence: 9}, &ConnectionEvents{Reset: true, sequence: 1, subscription: 6}, batchAccept},
		{"duplicate", connectionBatchState{hasBaseline: true, subscription: 5, sequence: 3}, &ConnectionEvents{sequence: 3, subscription: 5}, batchStale},
		{"gap", connectionBatchState{hasBaseline: true, subscription: 5, sequence: 3}, &ConnectionEvents{sequence: 5, subscription: 5}, batchResync},
		{"next", connectionBatchState{hasBaseline: true, subscription: 5, sequence: 3}, &ConnectionEvents{sequence: 4, subscription: 5}, batchAccept},
		{"dropped increase", connectionBatchState{hasBaseline: true, subscription: 5, sequence: 3, dropped: 0}, &ConnectionEvents{sequence: 4, dropped: 1, subscription: 5}, batchResync},
		{"old protocol", connectionBatchState{hasBaseline: true, subscription: 5}, &ConnectionEvents{events: []*ConnectionEvent{newRow("x")}}, batchAccept},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			state := testCase.state
			require.Equal(t, testCase.expect, classifyConnectionBatch(&state, testCase.batch))
		})
	}
}

var _ = strings.Contains
