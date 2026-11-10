package daemon

// The retained window can exceed one gRPC message. These tests pin the wire contract: the snapshot is
// split only when it must be, every multi-event fragment stays inside the byte budget, the parts are
// contiguous with the first carrying Reset, and a single oversized record is sent alone instead of
// being dropped.

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/trafficcontrol"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

type snapshotDescriptionRule struct {
	adapter.Rule
	description string
}

func (r *snapshotDescriptionRule) String() string { return r.description }

type snapshotRecordingStream struct {
	ctx     context.Context
	access  sync.Mutex
	batches []*ConnectionEvents
}

func newSnapshotRecordingStream(ctx context.Context) *snapshotRecordingStream {
	return &snapshotRecordingStream{ctx: ctx}
}

func (s *snapshotRecordingStream) Send(events *ConnectionEvents) error {
	s.access.Lock()
	s.batches = append(s.batches, events)
	s.access.Unlock()
	return nil
}

func (s *snapshotRecordingStream) snapshot() []*ConnectionEvents {
	s.access.Lock()
	defer s.access.Unlock()
	return append([]*ConnectionEvents(nil), s.batches...)
}

func (s *snapshotRecordingStream) Context() context.Context     { return s.ctx }
func (s *snapshotRecordingStream) SetHeader(metadata.MD) error  { return nil }
func (s *snapshotRecordingStream) SendHeader(metadata.MD) error { return nil }
func (s *snapshotRecordingStream) SetTrailer(metadata.MD)       {}
func (s *snapshotRecordingStream) SendMsg(any) error            { return nil }
func (s *snapshotRecordingStream) RecvMsg(any) error            { return nil }

func runSnapshotStream(t *testing.T, service *StartedService, manager *trafficcontrol.Manager, expectedEvents int) []*ConnectionEvents {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	stream := newSnapshotRecordingStream(ctx)
	errCh := make(chan error, 1)
	go func() {
		errCh <- service.SubscribeConnections(&SubscribeConnectionsRequest{Interval: 100}, stream)
	}()
	require.Eventually(t, func() bool {
		total := 0
		for _, batch := range stream.snapshot() {
			total += len(batch.Events)
		}
		return total >= expectedEvents
	}, 10*time.Second, time.Millisecond, "the snapshot must be sent")
	cancel()
	select {
	case <-errCh:
	case <-time.After(5 * time.Second):
		t.Fatal("the subscription did not stop after cancel")
	}
	return stream.snapshot()
}

func trackAndCloseSnapshotConnection(t *testing.T, manager *trafficcontrol.Manager, outbound adapter.Outbound, metadata adapter.InboundContext, rule adapter.Rule) {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	tracked := manager.RoutedConnection(context.Background(), clientConn, metadata, rule, outbound)
	require.NoError(t, tracked.Close())
	_ = serverConn.Close()
}

func TestALargeSnapshotIsSplitIntoBoundedParts(t *testing.T) {
	manager := trafficcontrol.NewManager()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	service := newSilentTailService(t, manager, ctx)

	outbound := testOutbound{}
	rule := &snapshotDescriptionRule{description: strings.Repeat("r", 5000)}
	metadata := adapter.InboundContext{OutboundChain: []adapter.Outbound{outbound}}
	const records = 1000
	for index := 0; index < records; index++ {
		trackAndCloseSnapshotConnection(t, manager, outbound, metadata, rule)
	}
	require.Len(t, manager.ClosedConnections(), records)

	batches := runSnapshotStream(t, service, manager, records)
	require.Greater(t, len(batches), 1, "a 5 MiB snapshot cannot fit the default 4 MiB receive limit")

	var maxSize int
	total := 0
	ids := make(map[string]struct{}, records)
	for index, batch := range batches {
		size := proto.Size(batch)
		if size > maxSize {
			maxSize = size
		}
		require.LessOrEqual(t, size, snapshotFragmentBudget+4096,
			"part %d is larger than the fragment budget", index+1)
		require.EqualValues(t, index+1, batch.SnapshotPart)
		require.EqualValues(t, len(batches), batch.SnapshotParts)
		require.Equal(t, index == 0, batch.Reset_)
		require.Zero(t, batch.Dropped, "every fragment is part of one baseline")
		require.EqualValues(t, index+1, batch.Sequence)
		total += len(batch.Events)
		for _, event := range batch.Events {
			ids[event.Id] = struct{}{}
			require.Equal(t, ConnectionEventType_CONNECTION_EVENT_NEW, event.Type)
			require.NotNil(t, event.Connection)
			require.NotZero(t, event.Connection.ClosedAt)
		}
	}
	require.Equal(t, records, total)
	require.Len(t, ids, records)
	t.Logf("parts=%d max_bytes=%d total_events=%d", len(batches), maxSize, total)
}

func TestASingleRecordOverTheFragmentBudgetIsSentAlone(t *testing.T) {
	manager := trafficcontrol.NewManager()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	service := newSilentTailService(t, manager, ctx)

	outbound := testOutbound{}
	rule := &snapshotDescriptionRule{description: strings.Repeat("r", 2<<20)}
	metadata := adapter.InboundContext{OutboundChain: []adapter.Outbound{outbound}}
	trackAndCloseSnapshotConnection(t, manager, outbound, metadata, rule)

	batches := runSnapshotStream(t, service, manager, 1)
	require.Len(t, batches, 1)
	batch := batches[0]
	require.Zero(t, batch.SnapshotParts, "a single chunk keeps the historical single-message shape")
	require.True(t, batch.Reset_)
	require.Len(t, batch.Events, 1)
	require.Greater(t, proto.Size(batch), snapshotFragmentBudget, "the record itself is over the budget")
	require.Less(t, proto.Size(batch), 4<<20, "it is still under the default receive limit")
}

// blockingSecondSendStream blocks the SECOND Send. That is the ordinary-increment Send, after the
// initial Reset: a drop that lands while it blocks must not be marked as notified by a fresh counter
// read when it returns - the message that was sent did not carry it.
type blockingSecondSendStream struct {
	ctx     context.Context
	access  sync.Mutex
	batches []*ConnectionEvents
	sends   int
	blocked chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingSecondSendStream(ctx context.Context) *blockingSecondSendStream {
	return &blockingSecondSendStream{
		ctx:     ctx,
		blocked: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (s *blockingSecondSendStream) Send(batch *ConnectionEvents) error {
	s.access.Lock()
	s.batches = append(s.batches, batch)
	s.sends++
	number := s.sends
	s.access.Unlock()
	if number == 2 {
		s.once.Do(func() { close(s.blocked) })
		<-s.release
	}
	return nil
}

func (s *blockingSecondSendStream) snapshot() []*ConnectionEvents {
	s.access.Lock()
	defer s.access.Unlock()
	return append([]*ConnectionEvents(nil), s.batches...)
}

func (s *blockingSecondSendStream) Context() context.Context     { return s.ctx }
func (s *blockingSecondSendStream) SetHeader(metadata.MD) error  { return nil }
func (s *blockingSecondSendStream) SendHeader(metadata.MD) error { return nil }
func (s *blockingSecondSendStream) SetTrailer(metadata.MD)       {}
func (s *blockingSecondSendStream) SendMsg(any) error            { return nil }
func (s *blockingSecondSendStream) RecvMsg(any) error            { return nil }

func TestADropAfterSendIsAnnouncedByTheNextBatch(t *testing.T) {
	manager := trafficcontrol.NewManager()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	service := newSilentTailService(t, manager, ctx)
	outbound := testOutbound{}
	metadata := adapter.InboundContext{OutboundChain: []adapter.Outbound{outbound}}

	stream := newBlockingSecondSendStream(ctx)
	errCh := make(chan error, 1)
	go func() {
		errCh <- service.SubscribeConnections(&SubscribeConnectionsRequest{Interval: 1000}, stream)
	}()
	require.Eventually(t, func() bool { return len(stream.snapshot()) >= 1 }, 5*time.Second, time.Millisecond)

	for index := 0; index < 70; index++ {
		trackAndCloseTestConnection(t, manager, outbound, metadata)
	}
	select {
	case <-stream.blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("the ordinary increment Send was never blocked")
	}
	blocked := stream.snapshot()[1]
	carried := blocked.Dropped
	require.EqualValues(t, 2, blocked.Sequence)

	// While that Send blocks, another 100 closes (200 events) fill the queue and overflow; the
	// subscription counter grows beyond what the blocked message carries.
	for index := 0; index < 100; index++ {
		trackAndCloseTestConnection(t, manager, outbound, metadata)
	}
	close(stream.release)

	require.Eventually(t, func() bool {
		for _, batch := range stream.snapshot() {
			if batch.Sequence > blocked.Sequence && batch.Dropped > carried {
				return true
			}
		}
		return false
	}, 5*time.Second, time.Millisecond, "the drops that landed during Send need a later notification")

	cancel()
	select {
	case <-errCh:
	case <-time.After(5 * time.Second):
		t.Fatal("the subscription did not stop")
	}
}

// TestTheFragmentBudgetBoundary pins the byte contract at its edge: a batch just under the budget is
// one message; the next record pushes it over and starts a second; a single record over the budget is
// alone in its chunk (never dropped).
func TestTheFragmentBudgetBoundary(t *testing.T) {
	event := func(rule string) *ConnectionEvent {
		return &ConnectionEvent{
			Type:       ConnectionEventType_CONNECTION_EVENT_NEW,
			Id:         "id",
			Connection: &Connection{Rule: strings.Repeat(rule, 1)},
		}
	}
	payload := strings.Repeat("r", 8000)
	var events []*ConnectionEvent
	for proto.Size(&ConnectionEvents{Events: events}) <= snapshotFragmentBudget {
		events = append(events, event(payload))
	}
	over := events
	under := events[:len(events)-1]
	require.LessOrEqual(t, proto.Size(&ConnectionEvents{Events: under}), snapshotFragmentBudget)
	require.Greater(t, proto.Size(&ConnectionEvents{Events: over}), snapshotFragmentBudget)
	require.Len(t, chunkConnectionEvents(under), 1)
	require.Len(t, chunkConnectionEvents(over), 2)

	single := event(strings.Repeat("s", snapshotFragmentBudget+1024))
	chunks := chunkConnectionEvents([]*ConnectionEvent{single})
	require.Len(t, chunks, 1, "an over-budget single record is sent alone, not dropped")
	require.Greater(t, proto.Size(&ConnectionEvents{Events: chunks[0]}), snapshotFragmentBudget)
}

// TestAnActiveConnectionCrossesTheFragmentBudgetWithClosedRecords checks the mixed view: an active
// connection and enough closed records to exceed the budget. Every ID and state must arrive, and the
// active record must not be mistaken for a closed one.
func TestAnActiveConnectionCrossesTheFragmentBudgetWithClosedRecords(t *testing.T) {
	manager := trafficcontrol.NewManager()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	service := newSilentTailService(t, manager, ctx)
	outbound := testOutbound{}
	rule := &snapshotDescriptionRule{description: strings.Repeat("r", 5000)}
	metadata := adapter.InboundContext{OutboundChain: []adapter.Outbound{outbound}}

	left, right := net.Pipe()
	active := manager.RoutedConnection(context.Background(), left, metadata, rule, outbound)
	t.Cleanup(func() {
		_ = active.Close()
		_ = right.Close()
	})
	const records = 250
	for index := 0; index < records; index++ {
		trackAndCloseSnapshotConnection(t, manager, outbound, metadata, rule)
	}

	batches := runSnapshotStream(t, service, manager, records+1)
	require.Greater(t, len(batches), 1, "the mixed view is over one budget")
	total := 0
	ids := make(map[string]struct{}, records+1)
	activeSeen := 0
	closedSeen := 0
	for index, batch := range batches {
		require.LessOrEqual(t, proto.Size(batch), snapshotFragmentBudget+4096)
		require.EqualValues(t, index+1, batch.SnapshotPart)
		require.EqualValues(t, len(batches), batch.SnapshotParts)
		for _, event := range batch.Events {
			total++
			ids[event.Id] = struct{}{}
			require.NotNil(t, event.Connection)
			if event.Connection.ClosedAt == 0 {
				activeSeen++
			} else {
				closedSeen++
			}
		}
	}
	require.Equal(t, records+1, total)
	require.Len(t, ids, records+1)
	require.Equal(t, 1, activeSeen)
	require.Equal(t, records, closedSeen)
}
