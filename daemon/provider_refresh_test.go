package daemon

// PP01 — the manual refresh entry's contract, with fake providers standing in for the box.
//
// The entry's value is that it reports what each provider REALLY did, refuses to touch a box
// that changed under it, and never counts a non-remote provider as refreshed.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/provider/remote"

	"github.com/stretchr/testify/require"
)

// fakeProvider is a provider whose Tag and refresh behaviour the test controls. The embedded
// interface is nil: only the overridden methods are ever called.
type fakeProvider struct {
	adapter.Provider
	tag      string
	report   *remote.ApplyReport
	err      error
	calls    int
	onUpdate func()
}

func (p *fakeProvider) Tag() string { return p.tag }

func (p *fakeProvider) UpdateWithReport() (*remote.ApplyReport, error) {
	p.calls++
	if p.onUpdate != nil {
		p.onUpdate()
	}
	return p.report, p.err
}

// staticProvider is a provider that does NOT implement the reporter: a non-remote provider.
type staticProvider struct {
	adapter.Provider
	tag string
}

func (p *staticProvider) Tag() string { return p.tag }

// fakeProviderSet is the narrow provider set the entry resolves against.
type fakeProviderSet struct {
	providers []adapter.Provider
}

func (s *fakeProviderSet) Providers() []adapter.Provider { return s.providers }

func (s *fakeProviderSet) Get(tag string) (adapter.Provider, bool) {
	for _, provider := range s.providers {
		if provider.Tag() == tag {
			return provider, true
		}
	}
	return nil, false
}

func refreshService(providers ...adapter.Provider) *StartedService {
	return &StartedService{
		serviceStatus: &ServiceStatus{Status: ServiceStatus_STARTED},
		instance:      &Instance{providerManager: &fakeProviderSet{providers: providers}},
	}
}

func reportOf(status remote.ApplyStatus) *remote.ApplyReport {
	return &remote.ApplyReport{Status: status, RequestedRevision: 2, AppliedRevision: 2}
}

func TestRefreshReportsWhatEachProviderActuallyDid(t *testing.T) {
	published := &fakeProvider{tag: "a", report: reportOf(remote.ApplyStatusCommitted)}
	unchanged := &fakeProvider{tag: "b", report: reportOf(remote.ApplyStatusUnchanged)}
	failed := &fakeProvider{tag: "c", err: errors.New("download failed")}
	skipped := &staticProvider{tag: "d"}
	service := refreshService(published, unchanged, failed, skipped)

	summary, err := service.RefreshProviders(context.Background(), nil, 2*time.Second)
	require.NoError(t, err)
	require.Len(t, summary.Results, 4)
	require.Equal(t, ProviderRefreshPublished, summary.Results[0].Status)
	require.Equal(t, ProviderRefreshUnchanged, summary.Results[1].Status)
	require.Equal(t, ProviderRefreshFailed, summary.Results[2].Status)
	require.Equal(t, ProviderRefreshSkipped, summary.Results[3].Status, "a non-remote provider is skipped, not refreshed")
	require.Equal(t, ProviderRefreshSkipped, summary.Results[3].Status, "a non-remote provider is never counted as refreshed")
	require.Equal(t, "d", summary.Results[3].Tag)
}

func TestRefreshSelectsTheRequestedTagsAndReportsMissingOnes(t *testing.T) {
	first := &fakeProvider{tag: "first", report: reportOf(remote.ApplyStatusCommitted)}
	second := &fakeProvider{tag: "second", report: reportOf(remote.ApplyStatusCommitted)}
	service := refreshService(first, second)

	summary, err := service.RefreshProviders(context.Background(), []string{"second", "missing"}, 2*time.Second)
	require.NoError(t, err)
	require.Len(t, summary.Results, 2)
	require.Equal(t, "missing", summary.Results[0].Tag)
	require.Equal(t, ProviderRefreshSkipped, summary.Results[0].Status, "a tag the box does not serve is reported, not dropped")
	require.Equal(t, "second", summary.Results[1].Tag)
	require.Equal(t, ProviderRefreshPublished, summary.Results[1].Status)
	require.Zero(t, first.calls, "an unselected provider must not be refreshed")
}

func TestRefreshWaitsOutAProviderItsOwnTimerHolds(t *testing.T) {
	held := &fakeProvider{tag: "held"}
	held.err = errors.New("provider is updating")
	held.report = nil
	service := refreshService(held)

	summary, err := service.RefreshProviders(context.Background(), nil, 2*time.Second)
	require.NoError(t, err)
	require.Equal(t, ProviderRefreshBusy, summary.Results[0].Status, "a bounded wait that ran out is reported as busy")
	require.Equal(t, refreshAttempts, held.calls, "the wait is bounded")
}

func TestRefreshIsCancelledByTheCaller(t *testing.T) {
	provider := &fakeProvider{tag: "p", report: reportOf(remote.ApplyStatusCommitted)}
	service := refreshService(provider)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	summary, err := service.RefreshProviders(ctx, nil, time.Second)
	require.NoError(t, err)
	require.True(t, summary.Cancelled)
	require.Equal(t, ProviderRefreshCancelled, summary.Results[0].Status)
	require.Zero(t, provider.calls, "a cancelled request must not refresh anything")
}

func TestRefreshStopsWhenTheBoxChangesUnderIt(t *testing.T) {
	first := &fakeProvider{tag: "first", report: reportOf(remote.ApplyStatusCommitted)}
	second := &fakeProvider{tag: "second", report: reportOf(remote.ApplyStatusCommitted)}
	service := refreshService(first, second)
	// The first provider replaces the box: the second must not be refreshed against another box.
	first.onUpdate = func() {
		service.serviceAccess.Lock()
		service.instance = &Instance{}
		service.serviceAccess.Unlock()
	}

	summary, err := service.RefreshProviders(context.Background(), nil, time.Second)
	require.NoError(t, err)
	require.True(t, summary.BoxChanged, "a refresh must not continue against a box that changed")
	require.Len(t, summary.Results, 2)
	require.Equal(t, ProviderRefreshPublished, summary.Results[0].Status)
	require.Equal(t, ProviderRefreshCancelled, summary.Results[1].Status)
	require.Zero(t, second.calls, "a provider of another box must not be refreshed")
}

func TestRefreshRequiresAStartedService(t *testing.T) {
	service := &StartedService{serviceStatus: &ServiceStatus{Status: ServiceStatus_IDLE}}
	_, err := service.RefreshProviders(context.Background(), nil, time.Second)
	require.Error(t, err)
}
