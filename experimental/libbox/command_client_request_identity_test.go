package libbox

// Q24/Q25/Q44 — the client half of request identity, over a real gRPC server.
//
// The daemon can echo a request token all it likes; if CommandClient throws the read-back away,
// no UI can settle its pending state by identity. These tests drive the production client methods
// against real gRPC servers in three shapes:
//
//   - a new daemon (identity RPC served),
//   - a handler that cannot identify the request (Unimplemented, the same answer an old daemon
//     gives for an RPC it does not know),
//   - an old daemon that really does not know the method (registered through a legacy service
//     descriptor), which is the mixed-deployment case Q44 asks about.

import (
	"context"
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/daemon"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
)

type identityManagedService struct {
	daemon.UnimplementedManagedServiceServer

	access sync.Mutex
	calls  []*daemon.ReloadServiceRequest
	legacy int
	// echo is returned as the request id; empty means "echo the request".
	echo string
}

func (s *identityManagedService) ReloadService(context.Context, *emptypb.Empty) (*emptypb.Empty, error) {
	s.access.Lock()
	s.legacy++
	s.access.Unlock()
	return &emptypb.Empty{}, nil
}

func (s *identityManagedService) ReloadServiceWithRequest(_ context.Context, request *daemon.ReloadServiceRequest) (*daemon.ReloadServiceResponse, error) {
	s.access.Lock()
	s.calls = append(s.calls, request)
	echo := s.echo
	s.access.Unlock()
	if echo == "" {
		echo = request.GetRequestId()
	}
	return &daemon.ReloadServiceResponse{
		RequestId:      echo,
		InstanceId:     "instance-after-" + request.GetRequestId(),
		ActiveRevision: "revision-after-" + request.GetRequestId(),
		Stale:          request.GetExpectedInstanceId() == "instance-replaced",
		Status:         daemon.ReloadStatusRestarted,
	}, nil
}

type identityStartedService struct {
	daemon.UnimplementedStartedServiceServer

	access  sync.Mutex
	calls   []*daemon.SelectOutboundRequest
	entered chan uint64
	release map[uint64]chan struct{}
	// oldStyle answers without the identity fields, like a daemon built before them.
	oldStyle bool
}

func (s *identityStartedService) SelectOutbound(_ context.Context, request *daemon.SelectOutboundRequest) (*daemon.SelectOutboundResponse, error) {
	s.access.Lock()
	s.calls = append(s.calls, request)
	entered := s.entered
	release := s.release[request.GetRequestId()]
	oldStyle := s.oldStyle
	s.access.Unlock()
	if entered != nil {
		entered <- request.GetRequestId()
	}
	if release != nil {
		<-release
	}
	if oldStyle {
		return &daemon.SelectOutboundResponse{}, nil
	}
	return &daemon.SelectOutboundResponse{
		RequestId:          request.GetRequestId(),
		AppliedOutboundTag: request.GetOutboundTag(),
		AppliedRevision:    request.GetRequestId() + 100,
	}, nil
}

// legacyReloadServer is the wire shape of a daemon that predates ReloadServiceWithRequest: its
// ManagedService descriptor carries only the token-less method.
type legacyReloadServer interface {
	ReloadService(context.Context, *emptypb.Empty) (*emptypb.Empty, error)
}

type legacyReloadImplementation struct {
	access sync.Mutex
	calls  int
}

func (s *legacyReloadImplementation) ReloadService(context.Context, *emptypb.Empty) (*emptypb.Empty, error) {
	s.access.Lock()
	s.calls++
	s.access.Unlock()
	return &emptypb.Empty{}, nil
}

func legacyReloadHandler(server any, ctx context.Context, decode func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	request := new(emptypb.Empty)
	if err := decode(request); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return server.(legacyReloadServer).ReloadService(ctx, request)
	}
	info := &grpc.UnaryServerInfo{Server: server, FullMethod: "/daemon.ManagedService/ReloadService"}
	handler := func(ctx context.Context, request any) (any, error) {
		return server.(legacyReloadServer).ReloadService(ctx, request.(*emptypb.Empty))
	}
	return interceptor(ctx, request, info, handler)
}

var legacyManagedServiceDescription = grpc.ServiceDesc{
	ServiceName: "daemon.ManagedService",
	HandlerType: (*legacyReloadServer)(nil),
	Methods: []grpc.MethodDesc{
		{MethodName: "ReloadService", Handler: legacyReloadHandler},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "daemon/managed_service.proto",
}

// legacySelectionServer is the wire shape of a daemon whose SelectOutbound predates the request
// identity fields. The RPC exists; the identity does not.
type legacySelectionServer interface {
	SelectOutbound(context.Context, *daemon.SelectOutboundRequest) (*daemon.SelectOutboundResponse, error)
}

type legacySelectionImplementation struct{}

func (legacySelectionImplementation) SelectOutbound(context.Context, *daemon.SelectOutboundRequest) (*daemon.SelectOutboundResponse, error) {
	return &daemon.SelectOutboundResponse{}, nil
}

func legacySelectionHandler(server any, ctx context.Context, decode func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	request := new(daemon.SelectOutboundRequest)
	if err := decode(request); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return server.(legacySelectionServer).SelectOutbound(ctx, request)
	}
	info := &grpc.UnaryServerInfo{Server: server, FullMethod: "/daemon.StartedService/SelectOutbound"}
	handler := func(ctx context.Context, request any) (any, error) {
		return server.(legacySelectionServer).SelectOutbound(ctx, request.(*daemon.SelectOutboundRequest))
	}
	return interceptor(ctx, request, info, handler)
}

var legacyStartedServiceDescription = grpc.ServiceDesc{
	ServiceName: "daemon.StartedService",
	HandlerType: (*legacySelectionServer)(nil),
	Methods: []grpc.MethodDesc{
		{MethodName: "SelectOutbound", Handler: legacySelectionHandler},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "daemon/started_service.proto",
}

func startIdentityServer(t *testing.T, register func(server *grpc.Server)) *grpc.ClientConn {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	register(server)
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(server.Stop)
	connection, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = connection.Close()
	})
	return connection
}

func commandClientOn(connection *grpc.ClientConn) *CommandClient {
	client := NewCommandClient(nil, nil)
	client.grpcConn = connection
	client.grpcClient = daemon.NewStartedServiceClient(connection)
	client.grpcManagedClient = daemon.NewManagedServiceClient(connection)
	return client
}

func decodeCommandDocument(t *testing.T, boxed *StringBox) map[string]any {
	t.Helper()
	require.NotNil(t, boxed)
	document := make(map[string]any)
	require.NoError(t, json.Unmarshal([]byte(boxed.Value), &document))
	return document
}

func TestTheReloadClientReportsTheInstanceAndRevision(t *testing.T) {
	daemonImplementation := &identityManagedService{}
	connection := startIdentityServer(t, func(server *grpc.Server) {
		daemon.RegisterManagedServiceServer(server, daemonImplementation)
	})
	client := commandClientOn(connection)

	boxed, err := client.ServiceReloadWithRequest("reload-41", "instance-before")
	require.NoError(t, err)
	document := decodeCommandDocument(t, boxed)
	require.Equal(t, true, document["supported"])
	require.Equal(t, "reload-41", document["requestId"])
	require.Equal(t, "instance-after-reload-41", document["instanceId"], "the caller learns which instance its reload left running")
	require.Equal(t, "revision-after-reload-41", document["activeRevision"])
	require.Equal(t, "restarted", document["status"])

	require.Len(t, daemonImplementation.calls, 1)
	require.Equal(t, "reload-41", daemonImplementation.calls[0].GetRequestId())
	require.Equal(t, "instance-before", daemonImplementation.calls[0].GetExpectedInstanceId())
}

func TestTheReloadClientFallsBackToTheLegacyRPCOnAnOldDaemon(t *testing.T) {
	oldDaemon := &legacyReloadImplementation{}
	connection := startIdentityServer(t, func(server *grpc.Server) {
		server.RegisterService(&legacyManagedServiceDescription, oldDaemon)
	})
	client := commandClientOn(connection)

	boxed, err := client.ServiceReloadWithRequest("reload-legacy", "instance-before")
	require.NoError(t, err, "an old daemon is not a reason to refuse the reload")
	document := decodeCommandDocument(t, boxed)
	require.Equal(t, false, document["supported"])
	require.Equal(t, true, document["unconfirmed"], "the reload happened but nobody can confirm which attempt it was")
	require.Equal(t, "reload-legacy", document["requestId"])
	require.NotContains(t, document, "instanceId")
	require.Equal(t, 1, oldDaemon.calls)
}

func TestTheReloadClientRefusesAConfirmationForAnotherRequest(t *testing.T) {
	daemonImplementation := &identityManagedService{echo: "reload-someone-else"}
	connection := startIdentityServer(t, func(server *grpc.Server) {
		daemon.RegisterManagedServiceServer(server, daemonImplementation)
	})
	client := commandClientOn(connection)

	_, err := client.ServiceReloadWithRequest("reload-mine", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "reload-someone-else")
	require.Contains(t, err.Error(), "reload-mine")
}

func TestTheLegacyReloadEntryUsesTheRequestIdentifiedRPC(t *testing.T) {
	daemonImplementation := &identityManagedService{}
	connection := startIdentityServer(t, func(server *grpc.Server) {
		daemon.RegisterManagedServiceServer(server, daemonImplementation)
	})
	client := commandClientOn(connection)

	require.NoError(t, client.ServiceReload())
	require.Len(t, daemonImplementation.calls, 1, "the shipped method now carries an identity on the wire")
	require.NotEmpty(t, daemonImplementation.calls[0].GetRequestId())
	require.Zero(t, daemonImplementation.legacy)
}

func TestTheSelectionClientReadsBackTheAppliedMember(t *testing.T) {
	startedImplementation := &identityStartedService{}
	connection := startIdentityServer(t, func(server *grpc.Server) {
		daemon.RegisterStartedServiceServer(server, startedImplementation)
	})
	client := commandClientOn(connection)

	boxed, err := client.SelectOutboundWithRequest("group-a", "member-b", 41, 7)
	require.NoError(t, err)
	document := decodeCommandDocument(t, boxed)
	require.Equal(t, true, document["supported"])
	require.EqualValues(t, 41, document["requestId"])
	require.Equal(t, "member-b", document["appliedOutboundTag"], "the read-back is what the group holds, not a copy of the request")
	require.EqualValues(t, 141, document["appliedRevision"])

	require.Len(t, startedImplementation.calls, 1)
	require.EqualValues(t, 41, startedImplementation.calls[0].GetRequestId())
	require.EqualValues(t, 7, startedImplementation.calls[0].GetExpectedRevision())
}

func TestTheSelectionClientDoesNotInventAConfirmationOnAnOldDaemon(t *testing.T) {
	connection := startIdentityServer(t, func(server *grpc.Server) {
		server.RegisterService(&legacyStartedServiceDescription, legacySelectionImplementation{})
	})
	client := commandClientOn(connection)

	boxed, err := client.SelectOutboundWithRequest("group-a", "member-b", 41, 0)
	require.NoError(t, err)
	document := decodeCommandDocument(t, boxed)
	require.Equal(t, false, document["supported"])
	require.Equal(t, true, document["unconfirmed"])
	require.EqualValues(t, 41, document["requestId"])
	require.NotContains(t, document, "appliedRevision")
}

func TestTheSelectionClientKeepsItsOwnIdentityWhenAnswersArriveOutOfOrder(t *testing.T) {
	firstRelease := make(chan struct{})
	startedImplementation := &identityStartedService{
		entered: make(chan uint64, 4),
		release: map[uint64]chan struct{}{1: firstRelease},
	}
	connection := startIdentityServer(t, func(server *grpc.Server) {
		daemon.RegisterStartedServiceServer(server, startedImplementation)
	})
	client := commandClientOn(connection)

	type answer struct {
		document map[string]any
		err      error
	}
	firstAnswer := make(chan answer, 1)
	go func() {
		boxed, err := client.SelectOutboundWithRequest("group-a", "member-1", 1, 0)
		if err != nil {
			firstAnswer <- answer{err: err}
			return
		}
		document := make(map[string]any)
		if decodeErr := json.Unmarshal([]byte(boxed.Value), &document); decodeErr != nil {
			firstAnswer <- answer{err: decodeErr}
			return
		}
		firstAnswer <- answer{document: document}
	}()
	require.EqualValues(t, 1, <-startedImplementation.entered, "the first selection is in flight")

	secondBoxed, err := client.SelectOutboundWithRequest("group-a", "member-2", 2, 0)
	require.NoError(t, err)
	second := decodeCommandDocument(t, secondBoxed)
	require.EqualValues(t, 2, second["requestId"], "the answer that arrived first names its own request")
	require.Equal(t, "member-2", second["appliedOutboundTag"])

	close(firstRelease)
	first := <-firstAnswer
	require.NoError(t, first.err)
	require.EqualValues(t, 1, first.document["requestId"], "the late answer does not settle the newer selection")
	require.Equal(t, "member-1", first.document["appliedOutboundTag"])
}

func TestTheSelectionClientTimesOutRatherThanHanging(t *testing.T) {
	// The client is a transport, not a scheduler: a daemon that never answers must surface an
	// error to the UI instead of leaving the pending selection forever.
	startedImplementation := &identityStartedService{
		entered: make(chan uint64, 1),
		release: map[uint64]chan struct{}{9: make(chan struct{})},
	}
	connection := startIdentityServer(t, func(server *grpc.Server) {
		daemon.RegisterStartedServiceServer(server, startedImplementation)
	})
	client := commandClientOn(connection)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	client.ctx = ctx

	_, err := client.SelectOutboundWithRequest("group-a", "member-9", 9, 0)
	require.Error(t, err)
	require.EqualValues(t, 9, <-startedImplementation.entered)
}
