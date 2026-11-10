package daemon

// PP01 — the manual refresh entry on the live box.
//
// A manual refresh is a request against the RUNNING box, not against a provider object a
// caller happens to hold: it must reach the providers the box is actually serving, report what
// each one really did (a published 200, a 304, a failure, a skip), be bounded in time, and be
// cancellable when the box stops.
//
// The three rules this file encodes:
//
//   - ONLY remote providers make network requests. Anything that is not a remote provider is
//     reported as skipped, never silently refreshed and never counted as a failure.
//   - A refresh racing the provider's own timer is not an error. The entry waits briefly and
//     retries a bounded number of times, then reports busy with the reason instead of hanging.
//   - The box identity is captured once. If the box is replaced or stopped mid-refresh, the
//     remaining providers are reported cancelled rather than refreshed against a box that no
//     longer serves them.

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/provider/remote"
)

// ProviderRefreshStatus is one provider's outcome in a manual refresh.
type ProviderRefreshStatus string

const (
	// ProviderRefreshPublished means a new document was applied.
	ProviderRefreshPublished ProviderRefreshStatus = "published"
	// ProviderRefreshUnchanged means the server reported 304 for the document in force.
	ProviderRefreshUnchanged ProviderRefreshStatus = "unchanged"
	// ProviderRefreshFailed means the refresh was attempted and failed; the previous document
	// is still serving.
	ProviderRefreshFailed ProviderRefreshStatus = "failed"
	// ProviderRefreshSkipped means this provider was not attempted (not a remote provider, or
	// not found in the running box).
	ProviderRefreshSkipped ProviderRefreshStatus = "skipped"
	// ProviderRefreshBusy means the provider's own refresh held it for the whole bounded wait.
	ProviderRefreshBusy ProviderRefreshStatus = "busy"
	// ProviderRefreshCancelled means the caller or the box stopped the refresh first.
	ProviderRefreshCancelled ProviderRefreshStatus = "cancelled"
)

// ProviderRefreshResult is what the entry can prove about one provider.
type ProviderRefreshResult struct {
	Tag               string                `json:"tag"`
	Status            ProviderRefreshStatus `json:"status"`
	RequestedRevision uint64                `json:"requestedRevision,omitempty"`
	AppliedRevision   uint64                `json:"appliedRevision,omitempty"`
	PendingTags       []string              `json:"pendingTags,omitempty"`
	RestartRequired   bool                  `json:"restartRequired,omitempty"`
	Reason            string                `json:"reason,omitempty"`
}

// ProviderRefreshSummary is one manual refresh request.
type ProviderRefreshSummary struct {
	// Results are in the order the providers were visited (tag order for "all").
	Results []ProviderRefreshResult `json:"results"`
	// Cancelled reports that the caller or the box stopped the refresh early.
	Cancelled bool `json:"cancelled"`
	// BoxChanged reports that the running box was replaced or stopped mid-refresh; the
	// remaining providers were not refreshed.
	BoxChanged bool `json:"boxChanged"`
}

// providerSource is the part of the box's provider set a manual refresh needs.
type providerSource interface {
	Providers() []adapter.Provider
	Get(tag string) (adapter.Provider, bool)
}

// providerReporter is the remote provider capability the entry needs. A provider that does not
// implement it is not a remote provider and is skipped.
type providerReporter interface {
	UpdateWithReport() (*remote.ApplyReport, error)
}

// providerContextReporter is the same capability for a provider whose fetch honours a caller's
// context. A provider that implements only `providerReporter` keeps working - its refresh is then
// bounded by the entry's own bookkeeping, not by the transport - so this is a capability, not a
// requirement.
type providerContextReporter interface {
	UpdateWithReportContext(ctx context.Context) (*remote.ApplyReport, error)
}

// refreshBusyReason is the message a provider's own timer produces while it holds the fetch.
const refreshBusyReason = "provider is updating"

// refreshAttempts and refreshRetryDelay bound the wait when the automatic timer holds a
// provider: two short sleeps, then an honest "busy".
var (
	refreshAttempts   = 3
	refreshRetryDelay = 50 * time.Millisecond
)

// providerRefreshBatchBudget bounds a WHOLE selection, independent of the per-provider timeout.
//
// # Why a batch bound exists on top of the per-provider one
//
// The per-provider timeout says how long ONE provider may take. A selection of N providers
// therefore had a worst case of N times that, with nothing above it: the live fast path called the
// entry with a background context and no deadline at all, and a client's 15-second RPC could sit
// behind twenty 30-second fetches. The caller's own cancellation is the real upper bound and is
// honoured; this is the daemon's own ceiling, so that a caller that forgot to set one cannot turn
// one apply into an unbounded wait inside the service lifecycle lock.
//
// It is deliberately generous relative to a single provider: a batch is allowed to be slower than
// one file, it is not allowed to be unbounded.
//
// A variable rather than a constant because the bound is a policy, and a policy that can only be
// exercised with a ninety-second test is a policy that is not exercised.
var providerRefreshBatchBudget = 90 * time.Second

// ProvidersForRefresh resolves the tags a manual refresh should visit.
//
// An empty selection means "every provider the box serves", in tag order. A named tag the box
// does not serve is reported skipped with the reason, not silently dropped: a caller that asked
// for a tag needs to know it was not refreshed.
func (s *StartedService) ProvidersForRefresh(tags []string) ([]adapter.Provider, []ProviderRefreshResult) {
	s.serviceAccess.RLock()
	var manager providerSource
	if s.instance != nil {
		manager = s.instance.providerManager
	}
	s.serviceAccess.RUnlock()
	if manager == nil {
		return nil, []ProviderRefreshResult{{Status: ProviderRefreshSkipped, Reason: "the running box has no provider manager"}}
	}
	if len(tags) == 0 {
		providers := manager.Providers()
		sorted := make([]adapter.Provider, 0, len(providers))
		sorted = append(sorted, providers...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].Tag() < sorted[j].Tag() })
		return sorted, nil
	}
	providers := make([]adapter.Provider, 0, len(tags))
	skipped := make([]ProviderRefreshResult, 0)
	for _, tag := range tags {
		provider, loaded := manager.Get(tag)
		if !loaded {
			skipped = append(skipped, ProviderRefreshResult{Tag: tag, Status: ProviderRefreshSkipped, Reason: "provider is not served by the running box"})
			continue
		}
		providers = append(providers, provider)
	}
	return providers, skipped
}

// RefreshProviders runs a bounded manual refresh of the providers the live box serves.
//
// timeout bounds EACH provider; ctx cancels the whole selection, and `providerRefreshBatchBudget`
// is the ceiling under which the selection runs when the caller set no deadline of its own. Both
// are reported, never folded into a failure of the provider itself.
func (s *StartedService) RefreshProviders(ctx context.Context, tags []string, timeout time.Duration) (*ProviderRefreshSummary, error) {
	s.serviceAccess.RLock()
	status := s.serviceStatus.Status
	box := s.instance
	s.serviceAccess.RUnlock()
	if status != ServiceStatus_STARTED || box == nil {
		return nil, errors.New("the service is not started")
	}
	// The batch ceiling is a child of the caller's context, so a caller's cancellation still wins
	// and an earlier deadline is not extended.
	batchContext, cancelBatch := context.WithTimeout(ctx, providerRefreshBatchBudget)
	defer cancelBatch()
	summary := &ProviderRefreshSummary{}
	providers, skipped := s.ProvidersForRefresh(tags)
	summary.Results = append(summary.Results, skipped...)
	for _, provider := range providers {
		if batchContext.Err() != nil {
			summary.Cancelled = true
			summary.Results = append(summary.Results, ProviderRefreshResult{Tag: provider.Tag(), Status: ProviderRefreshCancelled, Reason: batchContext.Err().Error()})
			continue
		}
		// The box identity is checked before EACH provider: a refresh must not continue
		// against a box that was stopped or replaced while the previous one ran.
		s.serviceAccess.RLock()
		current := s.instance
		currentStatus := s.serviceStatus.Status
		s.serviceAccess.RUnlock()
		if current != box || currentStatus != ServiceStatus_STARTED {
			summary.BoxChanged = true
			summary.Results = append(summary.Results, ProviderRefreshResult{Tag: provider.Tag(), Status: ProviderRefreshCancelled, Reason: "the running box changed"})
			continue
		}
		summary.Results = append(summary.Results, refreshOneProvider(batchContext, provider, timeout))
	}
	return summary, nil
}

// refreshOneProvider refreshes one provider, retrying briefly when its own timer holds it.
func refreshOneProvider(ctx context.Context, provider adapter.Provider, timeout time.Duration) ProviderRefreshResult {
	result := ProviderRefreshResult{Tag: provider.Tag()}
	// Both capabilities are checked: a provider that only honours a caller's context is a remote
	// provider too, and detecting it through the older interface would silently skip it.
	contextReporter, isContextAware := provider.(providerContextReporter)
	reporter, isRemote := provider.(providerReporter)
	if !isContextAware && !isRemote {
		result.Status = ProviderRefreshSkipped
		result.Reason = "not a remote provider"
		return result
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	providerContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var lastErr error
	for attempt := 0; attempt < refreshAttempts; attempt++ {
		if providerContext.Err() != nil {
			result.Status = ProviderRefreshCancelled
			result.Reason = providerContext.Err().Error()
			return result
		}
		report, err := updateProvider(providerContext, reporter, contextReporter)
		if publishSurvivedTheDeadline(report, err) {
			// The document was published and the wait ended afterwards. Reporting this as a
			// failure would tell the caller a node that exists was never published, and the next
			// attempt would then be planned against the wrong revision.
			applyReportToResult(&result, report)
			result.Reason = "published before the attempt was cancelled: " + err.Error()
			return result
		}
		if err == nil && report != nil {
			applyReportToResult(&result, report)
			return result
		}
		if err == nil {
			result.Status = ProviderRefreshFailed
			result.Reason = "the provider returned no report"
			return result
		}
		lastErr = err
		if !strings.Contains(err.Error(), refreshBusyReason) {
			break
		}
		select {
		case <-providerContext.Done():
			result.Status = ProviderRefreshCancelled
			result.Reason = providerContext.Err().Error()
			return result
		case <-time.After(refreshRetryDelay):
		}
	}
	if lastErr != nil && strings.Contains(lastErr.Error(), refreshBusyReason) {
		result.Status = ProviderRefreshBusy
		result.Reason = "the provider's own refresh held it for the whole bounded wait"
		return result
	}
	// The attempt ended because its own bound expired. That is a cancellation of THIS provider, not
	// a verdict about the provider's document: the previous document is still serving, and calling
	// it a failure would send the user looking for a broken provider instead of a slow endpoint.
	if expiredErr := providerContext.Err(); expiredErr != nil {
		result.Status = ProviderRefreshCancelled
		result.Reason = expiredErr.Error()
		if lastErr != nil {
			result.Reason = expiredErr.Error() + ": " + lastErr.Error()
		}
		return result
	}
	result.Status = ProviderRefreshFailed
	if lastErr != nil {
		result.Reason = lastErr.Error()
	}
	return result
}

// updateProvider calls the context-aware refresh when the provider has one.
func updateProvider(ctx context.Context, reporter providerReporter, contextReporter providerContextReporter) (*remote.ApplyReport, error) {
	if contextReporter != nil {
		return contextReporter.UpdateWithReportContext(ctx)
	}
	return reporter.UpdateWithReport()
}

// publishSurvivedTheDeadline reports whether a completed publish was returned WITH a cancellation.
//
// A provider that published and was then cancelled returns both: the report describes the commit,
// the error describes the cancellation. The two are not contradictory, and neither may be dropped.
//
// # Why every part of the test is needed
//
//   - The error must be the WAIT ending. A document that failed to fetch and then aborted the wait
//     also returns a report with an error, and that report describes the previous document.
//   - The applied revision must be the one THIS attempt requested. `ApplyReport` counts attempts and
//     publishes separately for exactly this reason: an attempt that failed while an older document
//     is still published carries the older applied revision, and calling that a publish would say a
//     node was refreshed when nothing was.
//   - The status must be a publish. `unknown` is "nothing was applied", which includes the case
//     where the report is all the previous attempt had to say.
func publishSurvivedTheDeadline(report *remote.ApplyReport, err error) bool {
	if err == nil || report == nil {
		return false
	}
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if report.RequestedRevision == 0 || report.AppliedRevision != report.RequestedRevision {
		return false
	}
	switch report.Status {
	case remote.ApplyStatusCommitted, remote.ApplyStatusPartial:
		return true
	default:
		return false
	}
}

// applyReportToResult copies a provider's own report into this entry's vocabulary.
func applyReportToResult(result *ProviderRefreshResult, report *remote.ApplyReport) {
	result.RequestedRevision = report.RequestedRevision
	result.AppliedRevision = report.AppliedRevision
	result.PendingTags = report.PendingTags
	result.RestartRequired = report.RestartRequired
	switch report.Status {
	case remote.ApplyStatusUnchanged:
		result.Status = ProviderRefreshUnchanged
	case remote.ApplyStatusCommitted, remote.ApplyStatusPartial:
		result.Status = ProviderRefreshPublished
	default:
		result.Status = ProviderRefreshFailed
		result.Reason = strings.Join(report.Diagnostics, "; ")
	}
}
