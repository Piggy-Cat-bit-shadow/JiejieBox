package libbox

// PP03 — the CLI/socket path of the manual refresh, over a real in-process gRPC server.

import (
	"context"
	"net"
	"testing"

	"github.com/sagernet/sing-box/daemon"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// fakeStartedService answers only the manual refresh; the embedded implementation panics on
// anything else, which is what a test wants: an unexpected call fails loudly.
type fakeStartedService struct {
	daemon.UnimplementedStartedServiceServer
	lastTags      []string
	lastTimeoutMs int64
}

func (s *fakeStartedService) RefreshRemoteProviders(_ context.Context, request *daemon.RefreshRemoteProvidersRequest) (*daemon.RemoteProviderRefreshResponse, error) {
	s.lastTags = request.Tags
	s.lastTimeoutMs = request.TimeoutMs
	return &daemon.RemoteProviderRefreshResponse{
		Results: []*daemon.RemoteProviderRefreshResult{
			{Tag: "p1", Status: "published", AppliedRevision: 3},
			{Tag: "p2", Status: "unchanged"},
		},
		BoxChanged: false,
	}, nil
}

func TestCommandClientCallsTheRefreshRPC(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	implementation := &fakeStartedService{}
	daemon.RegisterStartedServiceServer(server, implementation)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	connection, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = connection.Close() })

	client := NewCommandClient(nil, nil)
	t.Cleanup(client.closeConnection)
	client.grpcClient = daemon.NewStartedServiceClient(connection)
	client.grpcConn = connection

	boxed, err := client.RefreshRemoteProviders(" p1 , p2 ", 2500)
	require.NoError(t, err)
	require.NotNil(t, boxed)
	require.Equal(t, []string{"p1", "p2"}, implementation.lastTags, "the selection is parsed and carried to the daemon")
	require.EqualValues(t, 2500, implementation.lastTimeoutMs)
	require.Contains(t, boxed.Value, `"p1"`)
	require.Contains(t, boxed.Value, `"published"`)
	require.Contains(t, boxed.Value, `"p2"`)
}
