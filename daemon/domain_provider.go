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
)

// ProviderDomainPlan is what a provider-domain commit would refresh.
type ProviderDomainPlan struct {
	Domain string `json:"domain"`
	// Supported is false when the running box has no provider manager.
	Supported         bool   `json:"supported"`
	UnsupportedReason string `json:"unsupportedReason,omitempty"`
	// Tags is the selection; empty means every provider the box serves.
	Tags []string `json:"tags,omitempty"`
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
