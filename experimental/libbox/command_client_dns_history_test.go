package libbox

// The DNS history surface in DAEMON mode, over a real in-process gRPC server.
//
// The platform calls the same method names it calls on the command server. These tests pin that the
// daemon-mode path carries the same JSON contract: the request's JSON is parsed into the RPC
// fields, and the response document is handed to the platform unchanged.

import (
	"context"
	"net"
	"testing"

	"github.com/sagernet/sing-box/daemon"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
)

type fakeDNSHistoryService struct {
	daemon.UnimplementedStartedServiceServer

	enableRequest *daemon.DNSHistoryEnableRequest
	queryRequest  *daemon.DNSHistoryQueryRequest
	topKRequest   *daemon.DNSHistoryTopKRequest
	disabled      bool
	cleared       int32
}

func (s *fakeDNSHistoryService) DNSHistoryEnable(_ context.Context, request *daemon.DNSHistoryEnableRequest) (*emptypb.Empty, error) {
	s.enableRequest = request
	return &emptypb.Empty{}, nil
}

func (s *fakeDNSHistoryService) DNSHistoryDisable(_ context.Context, _ *emptypb.Empty) (*emptypb.Empty, error) {
	s.disabled = true
	return &emptypb.Empty{}, nil
}

func (s *fakeDNSHistoryService) DNSHistoryQuery(_ context.Context, request *daemon.DNSHistoryQueryRequest) (*daemon.DNSHistoryQueryResponse, error) {
	s.queryRequest = request
	return &daemon.DNSHistoryQueryResponse{
		RecordsJson: `{"records":[{"sequence":9,"name":"history.test.","qType":1,"source":"blocked","rcode":0,"durationMs":1}],"stats":{"entries":1,"bytes":90,"rejected":0,"expired":0,"queries":1,"cleared":0,"dropped":0},"truncated":true,"nextSequence":9}`,
	}, nil
}

func (s *fakeDNSHistoryService) DNSHistoryStats(_ context.Context, _ *emptypb.Empty) (*daemon.DNSHistoryStatsResponse, error) {
	return &daemon.DNSHistoryStatsResponse{StatsJson: `{"stats":{"entries":1,"bytes":90,"rejected":0,"expired":0,"queries":1,"cleared":0,"dropped":2}}`}, nil
}

func (s *fakeDNSHistoryService) DNSHistoryTopK(_ context.Context, request *daemon.DNSHistoryTopKRequest) (*daemon.DNSHistoryTopKResponse, error) {
	s.topKRequest = request
	return &daemon.DNSHistoryTopKResponse{EntriesJson: `{"field":"source","entries":[{"value":"blocked","count":1}]}`}, nil
}

func (s *fakeDNSHistoryService) DNSHistoryClear(_ context.Context, _ *emptypb.Empty) (*daemon.DNSHistoryClearResponse, error) {
	return &daemon.DNSHistoryClearResponse{Cleared: s.cleared}, nil
}

func startFakeDNSHistoryClient(t *testing.T) (*CommandClient, *fakeDNSHistoryService) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	implementation := &fakeDNSHistoryService{cleared: 3}
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
	return client, implementation
}

func TestTheDaemonClientCarriesTheDNSHistoryEnableOptions(t *testing.T) {
	client, implementation := startFakeDNSHistoryClient(t)

	boxed, err := client.DNSHistoryEnable(`{"maxEntries":16,"maxBytes":4096,"ttlMs":60000}`)
	require.NoError(t, err)
	require.Contains(t, boxed.Value, `"enabled":true`)
	require.NotNil(t, implementation.enableRequest)
	require.EqualValues(t, 16, implementation.enableRequest.MaxEntries)
	require.EqualValues(t, 4096, implementation.enableRequest.MaxBytes)
	require.EqualValues(t, 60000, implementation.enableRequest.TtlMs)
}

func TestTheDaemonClientReturnsTheQueryDocumentUnchanged(t *testing.T) {
	client, implementation := startFakeDNSHistoryClient(t)

	boxed, err := client.DNSHistoryQuery(`{"since":4,"limit":8,"transport":"remote","source":"blocked","maxBytes":2048}`)
	require.NoError(t, err)
	require.Contains(t, boxed.Value, `"nextSequence":9`, "the pagination cursor reaches the platform")
	require.Contains(t, boxed.Value, `"truncated":true`)
	require.NotNil(t, implementation.queryRequest)
	require.EqualValues(t, 4, implementation.queryRequest.Since)
	require.EqualValues(t, 8, implementation.queryRequest.Limit)
	require.Equal(t, "remote", implementation.queryRequest.Transport)
	require.Equal(t, "blocked", implementation.queryRequest.Source)
	require.EqualValues(t, 2048, implementation.queryRequest.MaxBytes)

	// An empty body still queries with the zero values, which the daemon resolves to its defaults.
	boxed, err = client.DNSHistoryQuery("")
	require.NoError(t, err)
	require.Contains(t, boxed.Value, `"records"`)
}

func TestTheDaemonClientClearsStatsAndTopK(t *testing.T) {
	client, implementation := startFakeDNSHistoryClient(t)

	boxed, err := client.DNSHistoryClear()
	require.NoError(t, err)
	require.Contains(t, boxed.Value, `"cleared":3`)

	stats, err := client.DNSHistoryStats()
	require.NoError(t, err)
	require.Contains(t, stats.Value, `"dropped":2`)

	topK, err := client.DNSHistoryTopK("source", 4)
	require.NoError(t, err)
	require.Contains(t, topK.Value, `"field":"source"`)
	require.NotNil(t, implementation.topKRequest)
	require.Equal(t, "source", implementation.topKRequest.Field)
	require.EqualValues(t, 4, implementation.topKRequest.Limit)
}

func TestTheDaemonClientDisablesTheHistory(t *testing.T) {
	client, implementation := startFakeDNSHistoryClient(t)

	require.NoError(t, client.DNSHistoryDisable())
	require.True(t, implementation.disabled)
}
