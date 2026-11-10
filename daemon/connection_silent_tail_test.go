package daemon

// The reviewed silent-tail failure: a slow connection-event subscription is blocked while 100
// connections are created and closed, then nothing else happens. The old stream delivered a
// contiguous prefix and never mentioned the loss, because its `dropped` was computed only from
// sequence gaps and no later event came to reveal one. The daemon kept the 100 closed records, but
// the subscription had no way to ask for them again.
//
// This test uses the REAL StartedService.SubscribeConnections and the REAL trafficcontrol.Manager,
// with a blocking Send standing in for the transport (the same shape the review used). It checks:
//   - the witness subscription received every source event with zero loss (the loss belongs to the
//     slow subscriber alone);
//   - the loss is announced on the next delivered batch (the counter, not a later event);
//   - delivered events + reported loss equals the published count;
//   - a fresh subscription's snapshot contains every closed record, so recovery is complete.

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/trafficcontrol"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
)

// testOutbound is only used as the chain's identity; nothing dials through it.
type testOutbound struct{}

func (testOutbound) Type() string           { return "direct" }
func (testOutbound) Tag() string            { return "direct" }
func (testOutbound) Network() []string      { return []string{N.NetworkTCP, N.NetworkUDP} }
func (testOutbound) Dependencies() []string { return nil }
func (testOutbound) Close() error           { return nil }
func (testOutbound) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return nil, net.ErrClosed
}
func (testOutbound) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, net.ErrClosed
}

// barrierStream blocks the FIRST Send until released, then records every batch.
type barrierStream struct {
	ctx     context.Context
	started chan struct{}
	release chan struct{}
	once    sync.Once

	access  sync.Mutex
	batches []*ConnectionEvents
}

func newBarrierStream(ctx context.Context) *barrierStream {
	return &barrierStream{
		ctx:     ctx,
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (s *barrierStream) Send(events *ConnectionEvents) error {
	s.once.Do(func() {
		close(s.started)
		<-s.release
	})
	s.access.Lock()
	s.batches = append(s.batches, events)
	s.access.Unlock()
	return nil
}

func (s *barrierStream) snapshot() []*ConnectionEvents {
	s.access.Lock()
	defer s.access.Unlock()
	return append([]*ConnectionEvents(nil), s.batches...)
}

func (s *barrierStream) Context() context.Context     { return s.ctx }
func (s *barrierStream) SetHeader(metadata.MD) error  { return nil }
func (s *barrierStream) SendHeader(metadata.MD) error { return nil }
func (s *barrierStream) SetTrailer(metadata.MD)       {}
func (s *barrierStream) SendMsg(any) error            { return nil }
func (s *barrierStream) RecvMsg(any) error            { return nil }

// collectingStream records every batch immediately (no barrier).
type collectingStream struct {
	ctx     context.Context
	access  sync.Mutex
	batches []*ConnectionEvents
}

func newCollectingStream(ctx context.Context) *collectingStream {
	return &collectingStream{ctx: ctx}
}

func (s *collectingStream) Send(events *ConnectionEvents) error {
	s.access.Lock()
	s.batches = append(s.batches, events)
	s.access.Unlock()
	return nil
}

func (s *collectingStream) snapshot() []*ConnectionEvents {
	s.access.Lock()
	defer s.access.Unlock()
	return append([]*ConnectionEvents(nil), s.batches...)
}

func (s *collectingStream) Context() context.Context     { return s.ctx }
func (s *collectingStream) SetHeader(metadata.MD) error  { return nil }
func (s *collectingStream) SendHeader(metadata.MD) error { return nil }
func (s *collectingStream) SetTrailer(metadata.MD)       {}
func (s *collectingStream) SendMsg(any) error            { return nil }
func (s *collectingStream) RecvMsg(any) error            { return nil }

func newSilentTailService(t *testing.T, manager *trafficcontrol.Manager, ctx context.Context) *StartedService {
	t.Helper()
	service := &StartedService{ctx: ctx}
	service.serviceStatus = &ServiceStatus{Status: ServiceStatus_STARTED}
	service.serviceAccess.Lock()
	service.instance = &Instance{trafficManager: manager}
	service.instanceID = "instance-silent-tail"
	service.serviceAccess.Unlock()
	t.Cleanup(func() {
		service.serviceAccess.Lock()
		service.instance = nil
		service.instanceID = ""
		service.serviceAccess.Unlock()
	})
	return service
}

func trackAndCloseTestConnection(t *testing.T, manager *trafficcontrol.Manager, outbound adapter.Outbound, metadata adapter.InboundContext) {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	tracked := manager.RoutedConnection(context.Background(), clientConn, metadata, nil, outbound)
	require.NoError(t, tracked.Close())
	_ = serverConn.Close()
}

func TestASilentTailLossIsAnnouncedAndRepairedByTheSnapshot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	manager := trafficcontrol.NewManager()
	service := newSilentTailService(t, manager, ctx)

	slowStream := newBarrierStream(ctx)
	go func() { _ = service.SubscribeConnections(&SubscribeConnectionsRequest{Interval: 20}, slowStream) }()
	select {
	case <-slowStream.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the subscription never reached its first Send")
	}

	// The witness proves every source event entered the fanout, so any loss belongs to the slow
	// subscription alone.
	witness, _, witnessDropped, err := manager.SubscribeEventsWithLoss()
	require.NoError(t, err)
	witnessNew := 0
	witnessClosed := 0
	drainWitness := func() {
		for {
			select {
			case event := <-witness:
				if event.Type == trafficcontrol.ConnectionEventNew {
					witnessNew++
				} else {
					witnessClosed++
				}
			default:
				return
			}
		}
	}

	outbound := testOutbound{}
	connectionMetadata := adapter.InboundContext{OutboundChain: []adapter.Outbound{outbound}}
	const connections = 100
	for index := 0; index < connections; index++ {
		trackAndCloseTestConnection(t, manager, outbound, connectionMetadata)
		// Drain after each connection: the witness proves the events reached the fanout, and a
		// drained witness cannot be confused with a slow one under load.
		drainWitness()
	}
	drainWitness()
	require.Zero(t, witnessDropped(), "the witness must not drop events")
	require.Equal(t, connections, witnessNew, "every created connection is announced")
	require.Equal(t, connections, witnessClosed, "every closed connection is announced")
	require.Zero(t, witnessDropped(), "the witness subscriber lost nothing")

	close(slowStream.release)

	// The loss must be announced on a delivered batch: either the queued prefix batch carries the
	// counter, or the ticker sends a loss-only batch. Both are the same contract.
	require.Eventually(t, func() bool {
		for _, batch := range slowStream.snapshot() {
			if batch.Dropped > 0 {
				return true
			}
		}
		return false
	}, 5*time.Second, time.Millisecond, "the loss must be announced without any further source event")

	delivered := 0
	var reportedLoss uint64
	for _, batch := range slowStream.snapshot() {
		delivered += len(batch.Events)
		if batch.Dropped > reportedLoss {
			reportedLoss = batch.Dropped
		}
	}
	require.EqualValues(t, connections*2, delivered+int(reportedLoss),
		"delivered events plus the announced loss must equal what was published")

	// A fresh subscription is the recovery: its snapshot must contain every closed record.
	recoveryStream := newCollectingStream(ctx)
	go func() { _ = service.SubscribeConnections(&SubscribeConnectionsRequest{Interval: 20}, recoveryStream) }()
	require.Eventually(t, func() bool {
		batches := recoveryStream.snapshot()
		if len(batches) == 0 {
			return false
		}
		return len(batches[0].Events) == connections && batches[0].Reset_
	}, 5*time.Second, time.Millisecond, "the recovery snapshot must carry all closed records")

	closedIDs := make(map[string]struct{}, connections)
	for _, event := range recoveryStream.snapshot()[0].Events {
		require.NotNil(t, event.Connection)
		require.NotZero(t, event.Connection.ClosedAt,
			"every connection was created and closed, so the snapshot row must carry a close timestamp")
		closedIDs[event.Id] = struct{}{}
	}
	require.Len(t, closedIDs, connections, "the snapshot's ID set must be complete")
}

// TestTheLossSignalCondition pins when the daemon sends a batch during silence: only when events are
// pending or the loss grew.
func TestTheLossSignalCondition(t *testing.T) {
	require.True(t, shouldSendConnectionBatch(1, 0, 0), "pending events are always sent")
	require.True(t, shouldSendConnectionBatch(0, 1, 0), "a new loss is announced during silence")
	require.False(t, shouldSendConnectionBatch(0, 1, 1), "an already reported loss is not repeated")
	require.False(t, shouldSendConnectionBatch(0, 0, 0), "idle with no loss sends nothing")
}
