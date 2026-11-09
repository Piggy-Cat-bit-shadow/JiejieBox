package daemon

// PP02 — the gRPC surface of the manual refresh: the protocol's job is to carry the kernel's
// report faithfully.

import (
	"context"
	"errors"
	"testing"

	"github.com/sagernet/sing-box/provider/remote"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRefreshRemoteProvidersRPCReportsPerProvider(t *testing.T) {
	published := &fakeProvider{tag: "a", report: reportOf(remote.ApplyStatusCommitted)}
	failed := &fakeProvider{tag: "b", err: errors.New("download failed")}
	service := refreshService(published, failed)

	result, err := service.RefreshRemoteProviders(context.Background(), &RefreshRemoteProvidersRequest{TimeoutMs: 1000})
	require.NoError(t, err)
	require.Len(t, result.Results, 2)
	require.Equal(t, "a", result.Results[0].Tag)
	require.Equal(t, string(ProviderRefreshPublished), result.Results[0].Status)
	require.EqualValues(t, 2, result.Results[0].AppliedRevision, "the revision comes from the kernel report")
	require.Equal(t, string(ProviderRefreshFailed), result.Results[1].Status)
	require.Contains(t, result.Results[1].Reason, "download failed")
	require.False(t, result.BoxChanged)
	require.False(t, result.Cancelled)
}

func TestRefreshRemoteProvidersRPCSelectionIsCarriedThrough(t *testing.T) {
	first := &fakeProvider{tag: "first", report: reportOf(remote.ApplyStatusCommitted)}
	second := &fakeProvider{tag: "second", report: reportOf(remote.ApplyStatusCommitted)}
	service := refreshService(first, second)

	result, err := service.RefreshRemoteProviders(context.Background(), &RefreshRemoteProvidersRequest{
		Tags: []string{"missing", "second"},
	})
	require.NoError(t, err)
	require.Len(t, result.Results, 2)
	require.Equal(t, "missing", result.Results[0].Tag)
	require.Equal(t, string(ProviderRefreshSkipped), result.Results[0].Status, "a tag the box does not serve is reported, not invented")
	require.Equal(t, "second", result.Results[1].Tag)
	require.Zero(t, first.calls, "an unselected provider must not be refreshed by the RPC")
}

func TestRefreshRemoteProvidersRPCRequiresARunningService(t *testing.T) {
	service := &StartedService{serviceStatus: &ServiceStatus{Status: ServiceStatus_IDLE}}
	_, err := service.RefreshRemoteProviders(context.Background(), &RefreshRemoteProvidersRequest{})
	require.Error(t, err)
	require.Equal(t, codes.FailedPrecondition, status.Code(err),
		"a stopped service is a request-level failure the client can distinguish from a provider failure")
}
