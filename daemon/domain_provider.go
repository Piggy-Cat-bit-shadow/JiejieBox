package daemon

// C04 — the provider domain: the second domain the change plan/commit shape covers.
//
// A provider refresh has no configuration diff to plan: the plan is the selection ("these
// tags", or every provider the box serves) plus the revision it was computed against. What a
// commit can claim is the PP01 per-tag report, mapped onto the domain vocabulary:
//
//   - every selected provider published or was unchanged -> committed (unchanged when nothing
//     needed publishing);
//   - some provider failed, was busy or was skipped -> partial, with the per-tag report intact;
//   - nothing was attempted (not started, stale plan, no provider) -> failed/unsupported.
//
// Restart is never required here: a refresh is applied live by construction. That is a fact
// worth stating in the report rather than leaving the caller to infer it.

import (
	"context"
	"time"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
)

// ProviderDomainPlan is what a provider-domain commit would refresh.
type ProviderDomainPlan struct {
	Domain string `json:"domain"`
	// Supported is false when the running box has no provider manager.
	Supported         bool   `json:"supported"`
	UnsupportedReason string `json:"unsupportedReason,omitempty"`
	// Tags is the selection; empty means every provider the box serves.
	Tags []string `json:"tags,omitempty"`
	// LiveRefresh says that an explicit reload of this document can be satisfied by refreshing the
	// running box's remote providers instead of rebuilding it. It is only true when nothing in the
	// document differs, every provider is remote, and the document references no other artifact
	// (rule set, certificate provider) that a reload would be expected to re-read.
	LiveRefresh bool `json:"liveRefresh,omitempty"`
	// LiveRefreshReason explains the answer in a caller's words, especially when it is no.
	LiveRefreshReason string `json:"liveRefreshReason,omitempty"`
	// CurrentRevision and TargetRevision guard the plan against being applied to another base.
	CurrentRevision string `json:"currentRevision,omitempty"`
	TargetRevision  string `json:"targetRevision,omitempty"`
}

// ProviderDomainReport is what the commit actually did, with PP01's per-tag detail kept.
type ProviderDomainReport struct {
	Domain          string                  `json:"domain"`
	Status          DomainStatus            `json:"status"`
	Results         []ProviderRefreshResult `json:"results,omitempty"`
	RestartRequired bool                    `json:"restartRequired"`
	Reason          string                  `json:"reason,omitempty"`
	Revision        string                  `json:"revision,omitempty"`
}

// PlanProviderDomain builds the provider-domain plan.
func PlanProviderDomain(tags []string, currentRevision string, targetRevision string) ProviderDomainPlan {
	return ProviderDomainPlan{
		Domain:          "provider",
		Supported:       true,
		Tags:            append([]string(nil), tags...),
		CurrentRevision: currentRevision,
		TargetRevision:  targetRevision,
	}
}

// PlanProviderRefreshDomain answers whether an explicit reload of `target` against `current` is a
// provider-content refresh rather than a rebuild.
//
// The boundary is deliberately narrow, because getting it wrong costs a caller the local files it
// wanted re-read:
//
//   - the documents must be identical (anything else is a document change, and a provider
//     DECLARATION change needs a new Box);
//   - every provider must be remote (an inline provider's nodes are in the document; a local
//     provider reads a file a reload may need to pick up);
//   - the document must declare no rule set and no certificate provider: those are separate
//     artifacts whose files or sources a caller could expect a reload to re-read, and refreshing
//     the providers would silently not do it.
//
// Under those conditions the only inputs outside the document are the remote provider documents,
// and refreshing them is exactly what the caller asked for - without rebuilding the Box and
// dropping unrelated connections.
func PlanProviderRefreshDomain(current *option.Options, target *option.Options, unchanged bool, currentRevision string, targetRevision string) ProviderDomainPlan {
	plan := ProviderDomainPlan{
		Domain:          "provider",
		Supported:       true,
		CurrentRevision: currentRevision,
		TargetRevision:  targetRevision,
	}
	if !unchanged {
		plan.LiveRefreshReason = "the document changed; provider declarations are rebuilt with it"
		return plan
	}
	if target == nil {
		plan.LiveRefreshReason = "there is no target document"
		return plan
	}
	if len(ruleSets(target)) > 0 {
		plan.LiveRefreshReason = "the document declares rule sets, which a reload is expected to re-read"
		return plan
	}
	if target.Certificate != nil || len(target.CertificateProviders) > 0 {
		plan.LiveRefreshReason = "the document declares certificate inputs, which a reload is expected to re-read"
		return plan
	}
	for _, provider := range target.Providers {
		if provider.Type != C.ProviderTypeRemote {
			plan.LiveRefreshReason = "the document declares a provider that is not remote: " + provider.Tag
			return plan
		}
		plan.Tags = append(plan.Tags, provider.Tag)
	}
	if len(plan.Tags) == 0 {
		plan.LiveRefreshReason = "the document declares no remote provider"
		return plan
	}
	plan.LiveRefresh = true
	plan.LiveRefreshReason = "the document is unchanged and every provider is remote: refreshing them needs no rebuild"
	return plan
}

// providerLiveRefreshTimeout bounds one provider during a live refresh triggered by the
// configuration entry.
const providerLiveRefreshTimeout = 30 * time.Second

// providerRefreshDelivered reports whether a live refresh left at least one provider serving a
// document (published) or confirmed the document it already served (unchanged). A refresh where
// every selected provider failed changed nothing, and the entry must not report success for it.
func providerRefreshDelivered(report *ProviderDomainReport) bool {
	if report == nil {
		return false
	}
	for _, result := range report.Results {
		switch result.Status {
		case ProviderRefreshPublished, ProviderRefreshUnchanged:
			return true
		}
	}
	return false
}

// ApplyProviderDomain commits a provider-domain plan to the live box.
//
// The running revision is the caller's statement of which configuration is in force; a plan
// computed against another revision is refused before any provider is touched.
func (s *StartedService) ApplyProviderDomain(plan ProviderDomainPlan, runningRevision string, timeout time.Duration) (*ProviderDomainReport, error) {
	if plan.CurrentRevision != "" && runningRevision != "" && plan.CurrentRevision != runningRevision {
		return &ProviderDomainReport{
			Domain:   plan.Domain,
			Status:   DomainStatusStalePlan,
			Reason:   "the plan was computed against another configuration revision",
			Revision: plan.TargetRevision,
		}, nil
	}
	if !plan.Supported {
		return &ProviderDomainReport{Domain: plan.Domain, Status: DomainStatusUnsupported, Reason: plan.UnsupportedReason}, nil
	}
	summary, err := s.RefreshProviders(context.Background(), plan.Tags, timeout)
	if err != nil {
		return &ProviderDomainReport{Domain: plan.Domain, Status: DomainStatusFailed, Reason: err.Error(), Revision: plan.TargetRevision}, nil
	}
	report := &ProviderDomainReport{
		Domain:   plan.Domain,
		Results:  summary.Results,
		Revision: plan.TargetRevision,
	}
	if summary.BoxChanged || summary.Cancelled {
		report.Status = DomainStatusPartial
		report.Reason = "the refresh did not reach every provider"
		return report, nil
	}
	if len(summary.Results) == 0 {
		report.Status = DomainStatusUnsupported
		report.Reason = "no provider was selected"
		return report, nil
	}
	published := 0
	unchanged := 0
	problematic := 0
	for _, result := range summary.Results {
		switch result.Status {
		case ProviderRefreshPublished:
			published++
		case ProviderRefreshUnchanged:
			unchanged++
		default:
			problematic++
			if report.Reason == "" {
				report.Reason = string(result.Status) + ": " + result.Tag
			}
		}
	}
	switch {
	case problematic > 0:
		report.Status = DomainStatusPartial
	case published == 0 && unchanged > 0:
		report.Status = DomainStatusUnchanged
	default:
		report.Status = DomainStatusCommitted
	}
	return report, nil
}
