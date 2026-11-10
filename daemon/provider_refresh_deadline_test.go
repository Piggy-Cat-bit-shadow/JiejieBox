package daemon

// E — the refresh's bounds and its cancellation, with fake providers standing in for the box.
//
// Three things the entry has to get right, and none of them is visible from a successful refresh:
//
//	the caller's context    reaches the provider's fetch, not just the loop around it
//	the batch ceiling       bounds a selection even when the caller set no deadline
//	a completed publish     is reported as published even when the wait ended afterwards
//
// The last one is the expensive mistake: a node that WAS published and is then reported as never
// published makes the next plan compare against the wrong revision, and the connection the user
// already has is described as if it did not exist.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/provider/remote"

	"github.com/stretchr/testify/require"
)

// contextProvider is a provider that honours the caller's context, and records what it saw.
type contextProvider struct {
	adapter.Provider
	tag string
	// behaviour is what the refresh does when it runs. It receives the context the entry passed
	// down, which is the whole point: the test can wait on it and prove it is the caller's.
	behaviour func(ctx context.Context) (*remote.ApplyReport, error)
	sawCancel bool
	sawError  error
}

func (p *contextProvider) Tag() string { return p.tag }

func (p *contextProvider) UpdateWithReportContext(ctx context.Context) (*remote.ApplyReport, error) {
	report, err := p.behaviour(ctx)
	if ctxErr := ctx.Err(); ctxErr != nil {
		p.sawCancel = true
		p.sawError = ctxErr
	}
	return report, err
}

// TestTheProvidersFetchReceivesTheCallersContext proves the cancellation reaches the fetch rather
// than stopping at the loop. Before this, the entry created a bounded context for each provider and
// then handed the provider nothing, so an in-flight HTTP request ran to the provider's own
// 30-second default however short the caller's bound was.
func TestTheProvidersFetchReceivesTheCallersContext(t *testing.T) {
	provider := &contextProvider{tag: "slow"}
	provider.behaviour = func(ctx context.Context) (*remote.ApplyReport, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	service := refreshService(provider)

	started := time.Now()
	summary, err := service.RefreshProviders(context.Background(), nil, 20*time.Millisecond)
	elapsed := time.Since(started)

	require.NoError(t, err)
	require.Less(t, elapsed, 5*time.Second, "the per-provider bound must end the attempt")
	require.Len(t, summary.Results, 1)
	require.Equal(t, ProviderRefreshCancelled, summary.Results[0].Status,
		"a bound that expired is a cancellation, not a verdict about the provider's document")
	require.True(t, provider.sawCancel, "the provider's fetch must be the thing that was cancelled")
	require.ErrorIs(t, provider.sawError, context.DeadlineExceeded)
}

// TestTheBatchCeilingBoundsTheWholeSelectionWithoutACallerDeadline covers the live fast path, which
// called the entry with a background context: nothing above the per-provider timeout existed, so a
// selection of N providers could take N times its bound while holding the service lifecycle lock.
func TestTheBatchCeilingBoundsTheWholeSelectionWithoutACallerDeadline(t *testing.T) {
	previous := providerRefreshBatchBudget
	providerRefreshBatchBudget = 40 * time.Millisecond
	t.Cleanup(func() { providerRefreshBatchBudget = previous })

	providers := make([]*contextProvider, 0, 3)
	adapters := make([]adapter.Provider, 0, 3)
	for _, tag := range []string{"a", "b", "c"} {
		provider := &contextProvider{tag: tag}
		provider.behaviour = func(ctx context.Context) (*remote.ApplyReport, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		providers = append(providers, provider)
		adapters = append(adapters, provider)
	}
	service := refreshService(adapters...)

	started := time.Now()
	summary, err := service.RefreshProviders(context.Background(), nil, time.Hour)
	elapsed := time.Since(started)

	require.NoError(t, err)
	require.Less(t, elapsed, 5*time.Second,
		"the batch ceiling must bound the whole selection, not each provider")
	require.Len(t, summary.Results, 3)
	for _, result := range summary.Results {
		require.Equal(t, ProviderRefreshCancelled, result.Status)
	}
	require.True(t, summary.Cancelled)
}

// TestAPublishThatOutlivedTheDeadlineIsStillReportedPublished is the honest half of cancellation:
// the document was committed, the wait ended afterwards, and both facts are true.
func TestAPublishThatOutlivedTheDeadlineIsStillReportedPublished(t *testing.T) {
	provider := &contextProvider{tag: "published-then-cancelled"}
	provider.behaviour = func(ctx context.Context) (*remote.ApplyReport, error) {
		<-ctx.Done()
		// The commit happened; the cancellation arrived while the caller was waiting for the
		// answer. This is the shape a fetch produces when it publishes and is then aborted.
		return reportOf(remote.ApplyStatusCommitted), ctx.Err()
	}
	service := refreshService(provider)

	summary, err := service.RefreshProviders(context.Background(), nil, 20*time.Millisecond)
	require.NoError(t, err)
	require.Len(t, summary.Results, 1)
	result := summary.Results[0]
	require.Equal(t, ProviderRefreshPublished, result.Status,
		"a published node must never be reported as never published")
	require.Equal(t, uint64(2), result.AppliedRevision)
	require.Contains(t, result.Reason, "published before the attempt was cancelled",
		"the receipt says both: what happened, and that the wait ended")
}

// TestAPartialPublishIsReportedPublishedWithItsPendingTags keeps the existing rule: a document the
// live graph accepted for some members is a publish, and its pending members are named.
func TestAPartialPublishIsReportedPublishedWithItsPendingTags(t *testing.T) {
	provider := &contextProvider{tag: "partial"}
	provider.behaviour = func(context.Context) (*remote.ApplyReport, error) {
		return &remote.ApplyReport{
			Status:            remote.ApplyStatusPartial,
			RequestedRevision: 3,
			AppliedRevision:   2,
			PendingTags:       []string{"group"},
		}, nil
	}
	service := refreshService(provider)

	summary, err := service.RefreshProviders(context.Background(), nil, time.Second)
	require.NoError(t, err)
	require.Equal(t, ProviderRefreshPublished, summary.Results[0].Status)
	require.Equal(t, []string{"group"}, summary.Results[0].PendingTags)
}

// TestAProviderErrorIsNotTurnedIntoACancellation keeps the two apart in the other direction: an
// attempt inside its bound that failed is a failure.
func TestAProviderErrorIsNotTurnedIntoACancellation(t *testing.T) {
	provider := &contextProvider{tag: "broken"}
	provider.behaviour = func(context.Context) (*remote.ApplyReport, error) {
		return nil, errors.New("download failed")
	}
	service := refreshService(provider)

	summary, err := service.RefreshProviders(context.Background(), nil, time.Second)
	require.NoError(t, err)
	require.Equal(t, ProviderRefreshFailed, summary.Results[0].Status)
	require.Contains(t, summary.Results[0].Reason, "download failed")
}
