package daemon

// The way a caller's cancellation could damage the running service, and the guard that stops it.
//
// `ApplyServiceChange` is what every embedder reaches. The provider fast path now passes the caller's
// context into the refresh (that is the fix for a 15-second RPC sitting behind a 30-second fetch), and
// the desktop daemon passes its gRPC handler context straight in. So a caller that disconnects
// mid-request can cancel the process that is about to decide to close its own Box.
//
// The guard is checked BEFORE anything is planned or touched: a request whose answer has nowhere to
// go decides nothing. The second half - a cancellation that arrives while the provider refresh is
// already running - needs a real Box with a real remote provider, and lives in the libbox package
// (`TestACancellationDuringAProviderRefreshKeepsTheBox`).

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestACancelledRequestLeavesTheRunningServiceAlone(t *testing.T) {
	service := NewStartedService(ServiceOptions{Context: libboxContext(t)})
	t.Cleanup(service.Close)
	_, err := service.ApplyServiceChange(context.Background(), minimalConfig, nil)
	require.NoError(t, err)
	before := service.Instance()
	require.NotNil(t, before)

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	// A DIFFERENT document: without the guard this is the request that would close the Box and then
	// fail to build its replacement.
	report, err := service.ApplyServiceChange(cancelled, ruleConfig, nil)
	require.ErrorIs(t, err, context.Canceled)
	require.NotNil(t, report)
	require.Equal(t, ServiceChangeStatusFailed, report.Status)
	require.True(t, report.Retained, "the running configuration is untouched, and the report says so")
	require.Same(t, before, service.Instance(),
		"a request whose answer has nowhere to go must not close the Box it was going to replace")
	require.Equal(t, configRevisionOf(minimalConfig), report.ActiveRevision,
		"the report names what is actually running, not what was asked for")
	require.Contains(t, report.Reason, "cancelled")
}
