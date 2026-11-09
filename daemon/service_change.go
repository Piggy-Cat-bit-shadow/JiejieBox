package daemon

// C04 鈥?the real configuration entry consumes the change plan.
//
// `StartOrReloadService` is the one place a profile becomes the running configuration in this
// process: the libbox command server, the daemon binary and the Apple/Android embedders all reach
// it. The domains in `domain_mode.go` and `domain_restart.go` answer what a document change means;
// this file is where that answer is used before the Box is touched, and where it is reported after
// the attempt.
//
// # What the entry does with the plan
//
//   - A candidate that does not parse never becomes the running configuration: it is refused
//     before the old Box is closed, and the report says the old one was retained.
//   - A change whose whole difference is owned by an object the running Box can change in place
//     (today: the Clash mode) is applied live, through the existing mode owner. The Box, its
//     connections and its published provider state are not rebuilt.
//   - Anything else - DNS policy, routing rules, rule sets, an outbound, a provider declaration -
//     is a restart: the existing reload path runs, and the report says the restart was required.
//   - An explicit reload of an IDENTICAL document still rebuilds. That is not laziness: a reload
//     is also how a caller picks up an edited local rule set, a certificate or a local provider,
//     and this entry cannot know that a referenced file did not change. The plan reports
//     `unchanged` so a caller that only wanted to know "will this cost me my connections" is told
//     "the document needs no rebuild" - and the entry still honours the request it was given.
//
// # Revisions
//
// The plan carries the revision of the document it was computed from and the revision of the
// target. The running revision is the profile-content revision stored on the instance, so a plan
// and the `runningRevision` a later commit is given are the same identifier.

import (
	"bytes"
	"time"

	"github.com/sagernet/sing-box/option"
)

// ServiceChangeStatus is the outcome of one apply attempt.
type ServiceChangeStatus string

const (
	// ServiceChangeStarted means there was no running Box and one was started.
	ServiceChangeStatusStarted ServiceChangeStatus = "started"
	// ServiceChangeUnchanged means the target document is the running one.
	ServiceChangeStatusUnchanged ServiceChangeStatus = "unchanged"
	// ServiceChangeAppliedLive means the running Box absorbed the change in place.
	ServiceChangeStatusAppliedLive ServiceChangeStatus = "applied_live"
	// ServiceChangeRestarted means a new Box was built and started for the change.
	ServiceChangeStatusRestarted ServiceChangeStatus = "restarted"
	// ServiceChangeRecovered means the requested document failed and the stored last-good one is
	// what runs now.
	ServiceChangeStatusRecovered ServiceChangeStatus = "recovered"
	// ServiceChangeInterrupted means the start was interrupted and nothing was left running.
	ServiceChangeStatusInterrupted ServiceChangeStatus = "interrupted"
	// ServiceChangeFailed means the target is not the running document.
	ServiceChangeStatusFailed ServiceChangeStatus = "failed"
)

// ServiceChangePlan is what applying a profile would mean for the running service.
//
// It is computed BEFORE anything is touched and is carried in the report, so a caller can compare
// "what I was told" with "what happened".
type ServiceChangePlan struct {
	// Started is false when there was no running Box: the target is a fresh start.
	Started         bool   `json:"started"`
	CurrentRevision string `json:"currentRevision,omitempty"`
	TargetRevision  string `json:"targetRevision,omitempty"`
	// Unchanged is true when the target document IS the running one.
	Unchanged bool `json:"unchanged"`
	// LiveOnly is true when the whole difference is owned by an object the running Box can change
	// in place. RestartRequired is false exactly in that case (and when Unchanged).
	LiveOnly        bool `json:"liveOnly"`
	RestartRequired bool `json:"restartRequired"`
	// ChangedDocument is true when the two documents differ outside every domain that has an owner
	// here. Such a change has no live apply path, whatever the domains say.
	ChangedDocument bool `json:"changedDocument"`
	// Domains are the per-domain answers. A caller that needs to explain a restart reads these;
	// they are only meaningful when Started is true, since a fresh start has nothing to compare.
	Mode DomainPlan `json:"mode"`
	DNS  DomainPlan `json:"dns"`
	Rule DomainPlan `json:"rule"`
}

// ServiceChangeReport is what the last apply attempt planned and did.
type ServiceChangeReport struct {
	Plan   ServiceChangePlan   `json:"plan"`
	Status ServiceChangeStatus `json:"status"`
	// RestartRequired repeats the plan's answer, so a caller that only reads the report sees it.
	RestartRequired bool `json:"restartRequired"`
	// Retained is true when the configuration that was running before the attempt is still the one
	// running: a rejected candidate, or a failure the last-good recovery brought back.
	Retained bool           `json:"retained"`
	Reason   string         `json:"reason,omitempty"`
	Time     time.Time      `json:"time"`
	Applied  []DomainChange `json:"applied,omitempty"`
}

// recordServiceChange stores the outcome of the last apply attempt.
func (s *StartedService) recordServiceChange(report *ServiceChangeReport) {
	if report == nil {
		return
	}
	report.Time = time.Now()
	s.serviceChangeAccess.Lock()
	s.lastServiceChange = report
	s.serviceChangeAccess.Unlock()
}

// LastServiceChange returns what the most recent apply attempt planned and did, or nil before the
// first attempt.
func (s *StartedService) LastServiceChange() *ServiceChangeReport {
	s.serviceChangeAccess.Lock()
	defer s.serviceChangeAccess.Unlock()
	return s.lastServiceChange
}

// PlanServiceChange answers what applying this profile would require, without touching the
// running Box. An unparseable candidate is an error, the same one the entry would refuse.
func (s *StartedService) PlanServiceChange(profileContent string, options *OverrideOptions) (ServiceChangePlan, error) {
	target, err := s.parseEffectiveOptions(s.ctx, profileContent, options)
	if err != nil {
		return ServiceChangePlan{}, err
	}
	return s.planServiceChange(&target, configRevisionOf(profileContent)), nil
}

// runningInstance returns the instance a plan may compare against, or nil.
func (s *StartedService) runningInstance() *Instance {
	s.serviceAccess.RLock()
	defer s.serviceAccess.RUnlock()
	if s.instance == nil || s.serviceStatus.Status != ServiceStatus_STARTED {
		return nil
	}
	return s.instance
}

// planServiceChange compares a target document with the running one.
func (s *StartedService) planServiceChange(target *option.Options, targetRevision string) ServiceChangePlan {
	plan := ServiceChangePlan{
		TargetRevision: targetRevision,
		// Named even when there is nothing to compare, so a report says which domains were asked
		// about rather than leaving three empty structs.
		Mode: DomainPlan{Domain: "mode"},
		DNS:  DomainPlan{Domain: "dns"},
		Rule: DomainPlan{Domain: "rule"},
	}
	running := s.runningInstance()
	if running == nil {
		return plan
	}
	plan.Started = true
	plan.CurrentRevision = running.revision
	current := &running.options
	plan.Mode = PlanModeDomain(current, plan.CurrentRevision, target, targetRevision)
	plan.DNS = PlanDNSDomain(current, plan.CurrentRevision, target, targetRevision)
	plan.Rule = PlanRuleDomain(current, plan.CurrentRevision, target, targetRevision)
	sameDocument, liveOnly := compareDocuments(current, target)
	plan.ChangedDocument = !sameDocument
	switch {
	case sameDocument && len(plan.Mode.Changes) == 0:
		plan.Unchanged = true
	case liveOnly && !plan.Mode.RestartRequired && len(plan.DNS.Changes) == 0 && len(plan.Rule.Changes) == 0:
		plan.LiveOnly = true
	default:
		plan.RestartRequired = plan.ChangedDocument || plan.Mode.RestartRequired || plan.DNS.RestartRequired || plan.Rule.RestartRequired
	}
	return plan
}

// tryLiveOnlyChange commits a live-only plan through the running Box's existing owners.
//
// The boolean says whether the change was actually applied. When it is false the caller must take
// the restart path: reporting success for a change the running Box refused to absorb would be the
// one outcome this entry must never produce.
func (s *StartedService) tryLiveOnlyChange(plan ServiceChangePlan, target option.Options) (ServiceChangeReport, bool) {
	report := ServiceChangeReport{Plan: plan, Status: ServiceChangeStatusAppliedLive}
	s.serviceAccess.RLock()
	status := s.serviceStatus.Status
	var owner modeOwner
	// The concrete pointer is checked before the assignment: a nil *clashmode.Manager stored in a
	// non-nil interface passes an `owner == nil` test and then panics on the first method call.
	if s.instance != nil && s.instance.clashMode != nil {
		owner = s.instance.clashMode
	}
	s.serviceAccess.RUnlock()
	if status != ServiceStatus_STARTED || owner == nil {
		report.Status = ServiceChangeStatusFailed
		report.Retained = true
		report.Reason = "the live owner of the change is not available in the running configuration"
		return report, false
	}
	modeReport := applyModeDomain(owner, plan.Mode)
	switch modeReport.Status {
	case DomainStatusCommitted:
		report.Applied = modeReport.Applied
	case DomainStatusUnchanged:
		report.Status = ServiceChangeStatusUnchanged
	default:
		report.Status = ServiceChangeStatusFailed
		report.Retained = true
		report.Reason = modeReport.Reason
		return report, false
	}
	// Everything in the target is in force now - the mode included - so the target IS the running
	// document. Recording it as such means the next plan compares against what actually runs.
	s.serviceAccess.Lock()
	if s.instance != nil && s.serviceStatus.Status == ServiceStatus_STARTED {
		s.instance.options = target
		s.instance.revision = plan.TargetRevision
	}
	s.serviceAccess.Unlock()
	return report, true
}

// compareDocuments reports whether two documents agree, and whether their only disagreement is a
// field a live owner applies.
//
// The encoding is the context-aware one (see `encodeDocument`): the variant payloads are part of
// the document, and a comparison that ignores them would call two different configurations the
// same one. An encoding failure is "they differ": equality has to be proven, not assumed.
func compareDocuments(current *option.Options, target *option.Options) (same bool, liveOnly bool) {
	currentWithoutMode, currentErr := encodeDocument(withoutDefaultMode(current))
	targetWithoutMode, targetErr := encodeDocument(withoutDefaultMode(target))
	if currentErr != nil || targetErr != nil {
		return false, false
	}
	sameWithoutMode := bytes.Equal(currentWithoutMode, targetWithoutMode)
	if !sameWithoutMode {
		return false, false
	}
	currentEncoded, currentErr := encodeDocument(current)
	targetEncoded, targetErr := encodeDocument(target)
	if currentErr != nil || targetErr != nil {
		return false, true
	}
	return bytes.Equal(currentEncoded, targetEncoded), true
}

// withoutDefaultMode returns a shallow copy of a document with the clash mode cleared. Only the
// three structs on the path are copied; everything else is shared with the original, which this
// function never writes.
func withoutDefaultMode(options *option.Options) *option.Options {
	if options == nil {
		return nil
	}
	copied := *options
	if copied.Experimental == nil {
		return &copied
	}
	experimental := *copied.Experimental
	if experimental.ClashAPI == nil {
		return &copied
	}
	clashAPI := *experimental.ClashAPI
	clashAPI.DefaultMode = ""
	experimental.ClashAPI = &clashAPI
	copied.Experimental = &experimental
	return &copied
}
