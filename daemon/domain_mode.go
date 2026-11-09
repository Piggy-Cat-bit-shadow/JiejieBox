package daemon

// C04 — the change plan and the per-domain commit, starting with the mode domain.
//
// A configuration change is not one operation. Some changes a running daemon can apply to the
// object that owns them (the clash mode), some require the owner to be rebuilt (the Clash API
// endpoint or its secret changed), some are not supported at all (the target removes the
// owner), and some change nothing. The plan says which of those applies BEFORE anything is
// touched, and the commit reports what actually happened instead of assuming the plan.
//
// # Why only the mode domain here
//
// Provider, rule-set and DNS-policy changes each have their own owner and their own task
// cards; this card lands ONE independently acceptable domain. The shape below - a
// revision-carrying plan, an explicit restart requirement, an honest per-domain report - is
// the contract the other domains reuse.
//
// # Why the plan carries both revisions
//
// A plan is only meaningful against the configuration it was computed from. Carrying the
// current and target revisions lets the commit refuse a plan that no longer matches the
// running configuration, which is the difference between "apply this diff" and "apply a diff
// computed against something else".

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/sagernet/sing-box/option"
)

// DomainStatus is the outcome of one domain commit.
type DomainStatus string

const (
	DomainStatusCommitted DomainStatus = "committed"
	// DomainStatusPartial means part of the domain's work was applied and part was not; the
	// report carries the per-item detail so a caller can retry exactly what is left.
	DomainStatusPartial         DomainStatus = "partial"
	DomainStatusUnchanged       DomainStatus = "unchanged"
	DomainStatusUnsupported     DomainStatus = "unsupported"
	DomainStatusRestartRequired DomainStatus = "restart_required"
	DomainStatusStalePlan       DomainStatus = "stale_plan"
	DomainStatusFailed          DomainStatus = "failed"
)

// DomainChange is one value the plan would change.
type DomainChange struct {
	Kind string `json:"kind"`
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	// Detail names where inside the value the documents first disagree: the rule index, or the
	// tags that were added, removed or changed. A caller that has to explain a restart reads it;
	// a caller that only needs the boolean ignores it.
	Detail string `json:"detail,omitempty"`
}

// DomainPlan is what a domain commit would do to the running configuration.
type DomainPlan struct {
	Domain string `json:"domain"`
	// Supported is false when the running configuration has no owner for this domain (or the
	// target removes it); UnsupportedReason says why.
	Supported         bool   `json:"supported"`
	UnsupportedReason string `json:"unsupportedReason,omitempty"`
	// RestartRequired means the owner itself must be rebuilt: a live commit cannot apply the
	// change even though the target is valid.
	RestartRequired bool           `json:"restartRequired"`
	Changes         []DomainChange `json:"changes,omitempty"`
	// CurrentRevision and TargetRevision identify the documents the plan was computed from.
	CurrentRevision string `json:"currentRevision,omitempty"`
	TargetRevision  string `json:"targetRevision,omitempty"`
}

// DomainReport is what the commit actually did.
type DomainReport struct {
	Domain          string         `json:"domain"`
	Status          DomainStatus   `json:"status"`
	Applied         []DomainChange `json:"applied,omitempty"`
	RestartRequired bool           `json:"restartRequired"`
	Reason          string         `json:"reason,omitempty"`
	// Revision is the target document's revision when the commit applied it.
	Revision string `json:"revision,omitempty"`
}

// modeOwner is the one operation the mode domain needs from its owner.
type modeOwner interface {
	SetMode(mode string)
}

// ErrNoModeOwner is returned when the running configuration has no clash mode owner.
var ErrNoModeOwner = errors.New("clash mode is not available in the running configuration")

// configRevision identifies a configuration document by content.
//
// The encoder is the context-aware one, and that is deliberate: plain encoding/json drops every
// variant payload (a DNS server's address, an outbound's server) because those types expose them
// through `json:"-"` fields and merge them in from MarshalJSONContext. A revision computed that
// way calls two documents with different servers the same document, which is exactly the answer
// a stale-plan check must not be given.
func configRevision(options *option.Options) string {
	if options == nil {
		return ""
	}
	encoded, err := encodeDocument(options)
	if err != nil {
		// The document cannot be canonicalised. Falling through to a partial encoding would
		// produce a revision that two different documents can share, so the empty revision (which
		// callers treat as "no revision") is the honest answer.
		return ""
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])[:16]
}

// clashAPI returns the Clash API options of a document, or nil.
func clashAPI(options *option.Options) *option.ClashAPIOptions {
	if options == nil || options.Experimental == nil {
		return nil
	}
	return options.Experimental.ClashAPI
}

// PlanModeDomain computes what switching to target would mean for the mode domain.
func PlanModeDomain(current *option.Options, currentRevision string, target *option.Options, targetRevision string) DomainPlan {
	plan := DomainPlan{
		Domain:          "mode",
		CurrentRevision: currentRevision,
		TargetRevision:  targetRevision,
	}
	if plan.CurrentRevision == "" {
		plan.CurrentRevision = configRevision(current)
	}
	if plan.TargetRevision == "" {
		plan.TargetRevision = configRevision(target)
	}
	currentAPI := clashAPI(current)
	targetAPI := clashAPI(target)
	switch {
	case currentAPI == nil:
		// There is no owner to switch: the running daemon never started a Clash API.
		plan.UnsupportedReason = "the running configuration has no clash_api owner"
		return plan
	case targetAPI == nil:
		plan.UnsupportedReason = "the target removes the clash_api owner, which needs a restart"
		return plan
	}
	// The owner is rebuilt when anything that shapes it changes; a mode switch alone is live.
	ownerChanged := currentAPI.ExternalController != targetAPI.ExternalController ||
		currentAPI.Secret != targetAPI.Secret ||
		currentAPI.ExternalUI != targetAPI.ExternalUI
	currentMode := currentAPI.DefaultMode
	targetMode := targetAPI.DefaultMode
	if ownerChanged {
		plan.RestartRequired = true
		plan.Changes = append(plan.Changes, DomainChange{Kind: "clash_api"})
		if currentMode != targetMode {
			plan.Changes = append(plan.Changes, DomainChange{Kind: "mode", From: currentMode, To: targetMode})
		}
		plan.Supported = true
		return plan
	}
	if currentMode == targetMode {
		// No change: a plan with no changes is supported and commits as unchanged.
		plan.Supported = true
		return plan
	}
	plan.Supported = true
	plan.Changes = append(plan.Changes, DomainChange{Kind: "mode", From: currentMode, To: targetMode})
	return plan
}

// ApplyModePlan commits a mode plan against the live daemon.
//
// The running revision is the caller's statement of which configuration is in force; a plan
// computed against another revision is refused instead of applied to the wrong base.
func (s *StartedService) ApplyModePlan(plan DomainPlan, runningRevision string) (*DomainReport, error) {
	// The revision check comes FIRST: a plan for another configuration must not even read the
	// live owner, let alone switch it.
	if plan.CurrentRevision != "" && runningRevision != "" && plan.CurrentRevision != runningRevision {
		return &DomainReport{
			Domain:   plan.Domain,
			Status:   DomainStatusStalePlan,
			Reason:   "the plan was computed against another configuration revision",
			Revision: plan.TargetRevision,
		}, nil
	}
	s.serviceAccess.RLock()
	status := s.serviceStatus.Status
	var owner modeOwner
	if s.instance != nil && s.instance.clashMode != nil {
		owner = s.instance.clashMode
	}
	s.serviceAccess.RUnlock()
	if status != ServiceStatus_STARTED {
		return &DomainReport{Domain: plan.Domain, Status: DomainStatusFailed, Reason: "the service is not started", Revision: plan.TargetRevision}, nil
	}
	if owner == nil {
		return &DomainReport{Domain: plan.Domain, Status: DomainStatusUnsupported, Reason: ErrNoModeOwner.Error()}, nil
	}
	report := applyModeDomain(owner, plan)
	return &report, nil
}

// applyModeDomain commits a plan through the live owner.
func applyModeDomain(owner modeOwner, plan DomainPlan) DomainReport {
	report := DomainReport{Domain: "mode", Revision: plan.TargetRevision}
	if !plan.Supported {
		report.Status = DomainStatusUnsupported
		report.Reason = plan.UnsupportedReason
		return report
	}
	if plan.RestartRequired {
		report.Status = DomainStatusRestartRequired
		report.RestartRequired = true
		report.Reason = "the owner must be rebuilt before this change can be applied"
		return report
	}
	if owner == nil {
		report.Status = DomainStatusUnsupported
		report.Reason = ErrNoModeOwner.Error()
		return report
	}
	applied := make([]DomainChange, 0, len(plan.Changes))
	for _, change := range plan.Changes {
		if change.Kind != "mode" {
			// A non-mode change cannot be applied by this owner; refusing it is honest.
			report.Status = DomainStatusRestartRequired
			report.RestartRequired = true
			report.Reason = "a non-mode change requires a restart"
			return report
		}
		owner.SetMode(change.To)
		applied = append(applied, change)
	}
	if len(applied) == 0 {
		report.Status = DomainStatusUnchanged
		return report
	}
	report.Status = DomainStatusCommitted
	report.Applied = applied
	return report
}
