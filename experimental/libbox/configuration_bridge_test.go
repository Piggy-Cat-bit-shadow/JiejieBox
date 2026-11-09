package libbox

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/daemon"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
)

type configurationOnlyServer struct {
	daemon.UnimplementedStartedServiceServer
}

func (s *configurationOnlyServer) PreviewServiceChange(context.Context, *daemon.PreviewServiceChangeRequest) (*daemon.JSONDocument, error) {
	return &daemon.JSONDocument{Json: `{"started":true,"targetRevision":"abc123","unchanged":false,"liveOnly":false,"restartRequired":false,"changedDocument":false}`}, nil
}

func (s *configurationOnlyServer) GetLastServiceChange(context.Context, *emptypb.Empty) (*daemon.JSONDocument, error) {
	return &daemon.JSONDocument{Json: `{"status":"degraded","persistence":"degraded","activeRevision":"abc123"}`}, nil
}

func (s *configurationOnlyServer) GetLastConfigRecovery(context.Context, *emptypb.Empty) (*daemon.JSONDocument, error) {
	return &daemon.JSONDocument{Json: "null"}, nil
}

func TestCommandClientCarriesConfigurationDocuments(t *testing.T) {
	address := bridgeTestServer(t, func(server *grpc.Server) {
		daemon.RegisterStartedServiceServer(server, &configurationOnlyServer{})
	})
	client := bridgeTestClient(t, address)

	plan, err := client.PreviewServiceChange(`{}`)
	require.NoError(t, err)
	require.JSONEq(t, `{"started":true,"targetRevision":"abc123","unchanged":false,"liveOnly":false,"restartRequired":false,"changedDocument":false}`, plan)

	report, err := client.GetLastServiceChange()
	require.NoError(t, err)
	require.JSONEq(t, `{"status":"degraded","persistence":"degraded","activeRevision":"abc123"}`, report)

	recovery, err := client.GetLastConfigRecovery()
	require.NoError(t, err)
	require.JSONEq(t, "null", recovery, "no recovery event is the JSON literal null, not an error")
}

func TestCommandClientTreatsAnOldKernelAsUnavailable(t *testing.T) {
	// No service is registered: every method answers UNIMPLEMENTED, which is exactly what an
	// older daemon does. The bridge must return an empty string, not an error and not "null".
	address := bridgeTestServer(t, func(*grpc.Server) {})
	client := bridgeTestClient(t, address)

	for name, call := range map[string]func() (string, error){
		"preview":  func() (string, error) { return client.PreviewServiceChange(`{}`) },
		"report":   client.GetLastServiceChange,
		"recovery": client.GetLastConfigRecovery,
	} {
		document, err := call()
		require.NoError(t, err, name)
		require.Empty(t, document, "%s must be empty on an old kernel", name)
	}
}
