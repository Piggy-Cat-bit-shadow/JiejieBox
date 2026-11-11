package libbox

// P1.4 (round 2) — a subscription's identity is bound when it is CREATED, not when its blocking
// establishment call happens to return.
//
// The previous fix carried a subscription number to the APPLY point and checked the connection
// generation at every delivery. Both were read AFTER `SubscribeConnections` returned, which leaves
// exactly the window the review found:
//
//  1. an establishment call parked across the next connect returns an ERROR, and the old code
//     reported `Disconnected` unconditionally - a failure of a connection that no longer exists,
//     shown to the platform as the current session failing;
//  2. the same call returns SUCCESS instead: the old code then read the NEW generation (so its
//     "still current" test passed) and took a subscription number HIGHER than the new stream's, so
//     the consumer accepted its snapshot as the fresher state and rolled the table back.
//
// The identity now travels from the connect that started the handler, and the subscription number
// is taken before the call that can block. A late establishment is therefore recognisable, and it is
// abandoned instead of being claimed.

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/daemon"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// gatedSubscribeClient intercepts SubscribeConnections before it reaches the network, so a test
// decides exactly when the call returns and with what.
type gatedSubscribeClient struct {
	daemon.StartedServiceClient

	entered chan struct{}
	release chan struct{}
	once    sync.Once
	result  func() (grpc.ServerStreamingClient[daemon.ConnectionEvents], error)
}

func newGatedSubscribeClient(inner daemon.StartedServiceClient, result func() (grpc.ServerStreamingClient[daemon.ConnectionEvents], error)) *gatedSubscribeClient {
	return &gatedSubscribeClient{
		StartedServiceClient: inner,
		entered:              make(chan struct{}),
		release:              make(chan struct{}),
		result:               result,
	}
}

func (c *gatedSubscribeClient) SubscribeConnections(context.Context, *daemon.SubscribeConnectionsRequest, ...grpc.CallOption) (grpc.ServerStreamingClient[daemon.ConnectionEvents], error) {
	c.once.Do(func() { close(c.entered) })
	<-c.release
	return c.result()
}

func (c *gatedSubscribeClient) releaseWith(result func() (grpc.ServerStreamingClient[daemon.ConnectionEvents], error)) {
	c.result = result
	close(c.release)
}

// scriptedConnectionsStream replays prepared batches, then holds until its context ends.
type scriptedConnectionsStream struct {
	ctx     context.Context
	batches []*daemon.ConnectionEvents
	index   int
}

func (s *scriptedConnectionsStream) Recv() (*daemon.ConnectionEvents, error) {
	if s.index >= len(s.batches) {
		<-s.ctx.Done()
		return nil, s.ctx.Err()
	}
	batch := s.batches[s.index]
	s.index++
	return batch, nil
}

func (s *scriptedConnectionsStream) Header() (metadata.MD, error) { return nil, nil }
func (s *scriptedConnectionsStream) Trailer() metadata.MD         { return nil }
func (s *scriptedConnectionsStream) CloseSend() error             { return nil }
func (s *scriptedConnectionsStream) Context() context.Context     { return s.ctx }
func (s *scriptedConnectionsStream) SendMsg(any) error            { return nil }
func (s *scriptedConnectionsStream) RecvMsg(any) error            { return io.EOF }

// recordingHandler applies every batch and counts disconnection reports; nothing is paused here,
// because this test controls the timing through the establishment barrier instead.
type recordingHandler struct {
	connections  *Connections
	disconnected atomic.Int32
}

func (h *recordingHandler) WriteConnectionEvents(events *ConnectionEvents) {
	h.connections.ApplyEvents(events)
}

func (h *recordingHandler) Connected()                        {}
func (h *recordingHandler) Disconnected(string)               { h.disconnected.Add(1) }
func (h *recordingHandler) SetDefaultLogLevel(int32)          {}
func (h *recordingHandler) ClearLogs()                        {}
func (h *recordingHandler) WriteLogs(LogIterator)             {}
func (h *recordingHandler) WriteStatus(*StatusMessage)        {}
func (h *recordingHandler) WriteGroups(OutboundGroupIterator) {}
func (h *recordingHandler) WriteOutbounds(OutboundGroupItemIterator) {
}
func (h *recordingHandler) InitializeClashMode(StringIterator, string) {}
func (h *recordingHandler) UpdateClashMode(string)                     {}

func dialTestServer(address string) func(context.Context) (*grpc.ClientConn, daemon.StartedServiceClient, error) {
	return func(ctx context.Context) (*grpc.ClientConn, daemon.StartedServiceClient, error) {
		connection, err := grpc.NewClient(address, localDialOptions(nil)...)
		if err != nil {
			return nil, nil, err
		}
		return connection, daemon.NewStartedServiceClient(connection), nil
	}
}

// TestASupersededEstablishmentFailureIsNotReportedAsTheCurrentConnection is review failure 1.
func TestASupersededEstablishmentFailureIsNotReportedAsTheCurrentConnection(t *testing.T) {
	oldAddress := bridgeTestServer(t, func(server *grpc.Server) {
		daemon.RegisterStartedServiceServer(server, &snapshotDaemon{id: "old", hold: true})
	})
	newAddress := bridgeTestServer(t, func(server *grpc.Server) {
		daemon.RegisterStartedServiceServer(server, &snapshotDaemon{id: "new", hold: true})
	})

	connections := NewConnections()
	handler := &recordingHandler{connections: connections}
	client := NewCommandClient(handler, &CommandClientOptions{commands: []int32{CommandConnections}})
	t.Cleanup(func() { _ = client.Disconnect() })

	gated := newGatedSubscribeClient(daemon.NewStartedServiceClient(nil), nil)
	oldDial := func(context.Context) (*grpc.ClientConn, daemon.StartedServiceClient, error) {
		connection, err := grpc.NewClient(oldAddress, localDialOptions(nil)...)
		if err != nil {
			return nil, nil, err
		}
		gated.StartedServiceClient = daemon.NewStartedServiceClient(connection)
		return connection, gated, nil
	}

	require.NoError(t, client.connect(oldDial))
	select {
	case <-gated.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the old subscription never reached its establishment call")
	}

	// The new connection is established and its snapshot applied while the old call is parked.
	require.NoError(t, client.connect(dialTestServer(newAddress)))
	require.Eventually(t, func() bool {
		ids := connectionSnapshotIDs(t, connections)
		return len(ids) == 1 && ids[0] == "new"
	}, 10*time.Second, time.Millisecond, "the new subscription's snapshot must be applied")

	// The parked call now fails. It belongs to the replaced connection.
	gated.releaseWith(func() (grpc.ServerStreamingClient[daemon.ConnectionEvents], error) {
		return nil, errors.New("injected subscribe failure")
	})
	time.Sleep(150 * time.Millisecond)

	require.Zero(t, handler.disconnected.Load(),
		"an establishment failure of a superseded subscription must not be reported as the current connection failing")
	require.Equal(t, []string{"new"}, connectionSnapshotIDs(t, connections))
}

// TestASupersededEstablishmentSuccessCannotApplyItsSnapshot is review failure 2: the late call
// succeeds after a reconnect, and nothing it carries may be applied.
func TestASupersededEstablishmentSuccessCannotApplyItsSnapshot(t *testing.T) {
	oldAddress := bridgeTestServer(t, func(server *grpc.Server) {
		daemon.RegisterStartedServiceServer(server, &snapshotDaemon{id: "old", hold: true})
	})
	newAddress := bridgeTestServer(t, func(server *grpc.Server) {
		daemon.RegisterStartedServiceServer(server, &snapshotDaemon{id: "new", hold: true})
	})

	connections := NewConnections()
	handler := &recordingHandler{connections: connections}
	client := NewCommandClient(handler, &CommandClientOptions{commands: []int32{CommandConnections}})
	t.Cleanup(func() { _ = client.Disconnect() })

	gated := newGatedSubscribeClient(daemon.NewStartedServiceClient(nil), nil)
	oldDial := func(context.Context) (*grpc.ClientConn, daemon.StartedServiceClient, error) {
		connection, err := grpc.NewClient(oldAddress, localDialOptions(nil)...)
		if err != nil {
			return nil, nil, err
		}
		gated.StartedServiceClient = daemon.NewStartedServiceClient(connection)
		return connection, gated, nil
	}

	require.NoError(t, client.connect(oldDial))
	select {
	case <-gated.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the old subscription never reached its establishment call")
	}

	require.NoError(t, client.connect(dialTestServer(newAddress)))
	require.Eventually(t, func() bool {
		ids := connectionSnapshotIDs(t, connections)
		return len(ids) == 1 && ids[0] == "new"
	}, 10*time.Second, time.Millisecond, "the new subscription's snapshot must be applied")

	// The parked call now SUCCEEDS and would deliver a snapshot naming the retired state.
	streamCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	gated.releaseWith(func() (grpc.ServerStreamingClient[daemon.ConnectionEvents], error) {
		return &scriptedConnectionsStream{
			ctx: streamCtx,
			batches: []*daemon.ConnectionEvents{{
				Reset_: true,
				Events: []*daemon.ConnectionEvent{{
					Type:       daemon.ConnectionEventType_CONNECTION_EVENT_NEW,
					Id:         "old",
					Connection: &daemon.Connection{Id: "old"},
				}},
			}},
		}, nil
	})
	time.Sleep(200 * time.Millisecond)

	require.Equal(t, []string{"new"}, connectionSnapshotIDs(t, connections),
		"a subscription established after a newer connect must be abandoned, not applied as the fresher state")
	require.Zero(t, handler.disconnected.Load(),
		"and its context ending is not a failure of the current connection")
}
