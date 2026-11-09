package daemon

// C04 — the provider domain's plan and commit.

import (
	"errors"
	"testing"
	"time"

	"github.com/sagernet/sing-box/provider/remote"

	"github.com/stretchr/testify/require"
)

func TestProviderDomainCommitsPublishedRefreshes(t *testing.T) {
	published := &fakeProvider{tag: "a", report: reportOf(remote.ApplyStatusCommitted)}
	unchanged := &fakeProvider{tag: "b", report: reportOf(remote.ApplyStatusUnchanged)}
	service := refreshService(published, unchanged)

	plan := PlanProviderDomain(nil, "rev-a", "rev-b")
	report, err := service.ApplyProviderDomain(plan, "rev-a", time.Second)
	require.NoError(t, err)
	require.Equal(t, DomainStatusCommitted, report.Status)
	require.Len(t, report.Results, 2)
	require.Equal(t, "rev-b", report.Revision)
	require.False(t, report.RestartRequired, "a provider refresh is applied live")
}

func TestProviderDomainIsUnchangedWhenNothingNeededPublishing(t *testing.T) {
	unchanged := &fakeProvider{tag: "a", report: reportOf(remote.ApplyStatusUnchanged)}
	service := refreshService(unchanged)
	report, err := service.ApplyProviderDomain(PlanProviderDomain([]string{"a"}, "", ""), "", time.Second)
	require.NoError(t, err)
	require.Equal(t, DomainStatusUnchanged, report.Status)
}

func TestProviderDomainIsPartialWhenAProviderFails(t *testing.T) {
	published := &fakeProvider{tag: "a", report: reportOf(remote.ApplyStatusCommitted)}
	failed := &fakeProvider{tag: "b", err: errors.New("download failed")}
	service := refreshService(published, failed)
	report, err := service.ApplyProviderDomain(PlanProviderDomain(nil, "", ""), "", time.Second)
	require.NoError(t, err)
	require.Equal(t, DomainStatusPartial, report.Status)
	require.Contains(t, report.Reason, "b")
	require.Len(t, report.Results, 2, "the per-provider detail is kept for the caller")
}

func TestAStaleProviderPlanIsRefusedBeforeRefreshing(t *testing.T) {
	provider := &fakeProvider{tag: "a", report: reportOf(remote.ApplyStatusCommitted)}
	service := refreshService(provider)
	report, err := service.ApplyProviderDomain(PlanProviderDomain(nil, "rev-a", "rev-b"), "rev-other", time.Second)
	require.NoError(t, err)
	require.Equal(t, DomainStatusStalePlan, report.Status)
	require.Zero(t, provider.calls, "a stale plan must not refresh anything")
}

func TestProviderDomainRequiresAStartedService(t *testing.T) {
	service := &StartedService{serviceStatus: &ServiceStatus{Status: ServiceStatus_IDLE}}
	report, err := service.ApplyProviderDomain(PlanProviderDomain(nil, "", ""), "", time.Second)
	require.NoError(t, err)
	require.Equal(t, DomainStatusFailed, report.Status)
}
