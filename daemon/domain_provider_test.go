package daemon

// C04 — the provider domain's plan and commit.

import (
	"errors"
	"testing"
	"time"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/provider/remote"

	"github.com/stretchr/testify/require"
)

// TestPlanProviderRefreshIsOnlyForAnUnchangedRemoteProviderDocument pins the boundary of the live
// provider refresh: anything a reload is expected to re-read from a file keeps the restart path.
func TestPlanProviderRefreshIsOnlyForAnUnchangedRemoteProviderDocument(t *testing.T) {
	remoteProvider := []option.Provider{{Type: C.ProviderTypeRemote, Tag: "p"}}
	document := &option.Options{Providers: remoteProvider}

	plan := PlanProviderRefreshDomain(document, document, true, "rev-a", "rev-a")
	require.True(t, plan.LiveRefresh, "an unchanged document whose only external input is a remote provider")
	require.Equal(t, []string{"p"}, plan.Tags)

	changed := PlanProviderRefreshDomain(document, document, false, "rev-a", "rev-b")
	require.False(t, changed.LiveRefresh, "a document change is a rebuild, provider declarations included")

	inline := &option.Options{Providers: []option.Provider{{Type: C.ProviderTypeInline, Tag: "i"}}}
	plan = PlanProviderRefreshDomain(inline, inline, true, "rev-a", "rev-a")
	require.False(t, plan.LiveRefresh, "an inline provider's nodes live in the document; refreshing reads nothing")
	require.Contains(t, plan.LiveRefreshReason, "not remote")

	withRuleSet := &option.Options{
		Providers: remoteProvider,
		Route: &option.RouteOptions{RuleSet: []option.RuleSet{{
			Type: C.RuleSetTypeRemote,
			Tag:  []string{"rules"},
		}}},
	}
	plan = PlanProviderRefreshDomain(withRuleSet, withRuleSet, true, "rev-a", "rev-a")
	require.False(t, plan.LiveRefresh, "a rule set is a separate artifact a reload would be expected to re-read")
	require.Contains(t, plan.LiveRefreshReason, "rule sets")

	withoutProvider := &option.Options{}
	plan = PlanProviderRefreshDomain(withoutProvider, withoutProvider, true, "rev-a", "rev-a")
	require.False(t, plan.LiveRefresh)
	require.Contains(t, plan.LiveRefreshReason, "no remote provider")
}

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
