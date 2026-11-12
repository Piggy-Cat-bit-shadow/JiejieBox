package daemon

// C04 - the real configuration entry consumes the change plan.
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
	"context"
	"time"

	singjson "github.com/sagernet/sing/common/json"

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
	// Stale is set on a report whose plan could not be committed because the running configuration
	// was no longer the one the plan was computed against.
	Stale bool `json:"stale,omitempty"`
	// OverridesConsidered names the run-only override inputs this plan was computed WITH.
	//
	// They are part of the effective configuration - `parseEffectiveOptions` merges them into the
	// document before anything is compared - so a plan that silently omitted one would compare two
	// documents that differ by an input it never saw and report a change the apply does not make.
	// Naming them is how a reader can tell which inputs the answer covers.
	OverridesConsidered []string `json:"overridesConsidered,omitempty"`
	// UncoveredInputs names effective-configuration inputs this plan CANNOT see, so that no preview
	// is read as "applying this document needs no restart" when the embedder is about to change
	// something of its own. The platform's network settings (installed routes, APNs exclusion, the
	// system proxy) are applied by the embedder when it opens the TUN, not by this document, and
	// they take effect only through a rebuild.
	UncoveredInputs []string `json:"uncoveredInputs,omitempty"`
	// ChangedSections names the top-level document sections that differ outside the domains with a
	// live owner. It is the reason a restart is required, in the caller's own vocabulary: "the
	// providers changed" is actionable, "the document changed" is not.
	ChangedSections []string `json:"changedSections,omitempty"`
	// Domains are the per-domain answers. A caller that needs to explain a restart reads these;
	// they are only meaningful when Started is true, since a fresh start has nothing to compare.
	Mode     DomainPlan         `json:"mode"`
	DNS      DomainPlan         `json:"dns"`
	Rule     DomainPlan         `json:"rule"`
	Provider ProviderDomainPlan `json:"provider"`
}

// ServiceChangeReport is what the last apply attempt planned and did.
type ServiceChangeReport struct {
	Plan   ServiceChangePlan   `json:"plan"`
	Status ServiceChangeStatus `json:"status"`
	// RequestID is the token the CALLER supplied for this attempt, echoed unchanged.
	//
	// # Why a revision cannot stand in for it
	//
	// `activeRevision` identifies the DOCUMENT, not the request. Two attempts with the same content
	// have the same target revision, so a revision cannot tell "my apply" from "the one before it";
	// and an attempt that fails or is interrupted can leave the SAME revision running while doing
	// nothing the caller asked for. A client that matched on the revision alone would report success
	// for an attempt that never happened.
	//
	// It is empty for callers that supply no token (every pre-existing embedder), and an empty token
	// is not a match for anything: a client must not treat it as "mine".
	RequestID string `json:"requestId,omitempty"`
	// InstanceID is the identity of the Box instance running AFTER this attempt, or empty when
	// nothing is running. It is the same identity the connection-event batches carry, so a client can
	// join "the reload I requested" to "the stream I am now receiving" without trusting a revision.
	// A restart produces a NEW identity; a live apply keeps the existing one.
	InstanceID string `json:"instanceId,omitempty"`
	// RestartRequired repeats the plan's answer, so a caller that only reads the report sees it.
	RestartRequired bool `json:"restartRequired"`
	// Retained is true when the configuration that was running before the attempt is still the one
	// running: a rejected candidate, or a recovery back to the document that was already running.
	// A recovery to a DIFFERENT saved document (the one that ran was never saved) is not retention.
	Retained bool `json:"retained"`
	// ActiveRevision is the revision that is actually running after the attempt. It is the target
	// when the target was applied or rebuilt, the stored last-good revision when a recovery ran,
	// the previous revision when the attempt left the old configuration in place, and EMPTY when
	// the attempt left nothing running (a failed build without a recovery, an interrupted start).
	ActiveRevision string `json:"activeRevision,omitempty"`
	// Persistence is what the last-good store did with the configuration that now runs: "saved",
	// "degraded" (running, but a later failed start cannot return to it) or "not_configured".
	Persistence string `json:"persistence,omitempty"`
	// PersistenceWarning is the store's own message when Persistence is not "saved" without a
	// cause, or when the document was saved but the previous one could not be kept.
	PersistenceWarning string         `json:"persistenceWarning,omitempty"`
	Reason             string         `json:"reason,omitempty"`
	Time               time.Time      `json:"time"`
	Applied            []DomainChange `json:"applied,omitempty"`
	// Provider is the provider-refresh report when an unchanged document was satisfied live by
	// refreshing the running box's remote providers.
	Provider *ProviderDomainReport `json:"provider,omitempty"`
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
//
// # Why nil means "the running instance's overrides"
//
// The overrides are not decoration: `parseEffectiveOptions` merges them into the document, so they
// are part of what is running. A caller that passes nil because it does not know them - which is
// every preview, since the platform sends its TUN patch with the apply - used to get a plan
// computed against a DIFFERENT document than the one in force, and a body that is byte-identical
// to the running one was reported as a change. Defaulting to the overrides the running document was
// built with makes the preview compare the same input the apply would. A caller that knows its
// overrides still wins, which is what an embedder previewing a settings change needs.
func (s *StartedService) PlanServiceChange(profileContent string, options *OverrideOptions) (ServiceChangePlan, error) {
	effectiveOverrides := s.planningOverrides(options)
	target, err := s.parseEffectiveOptions(s.ctx, profileContent, effectiveOverrides)
	if err != nil {
		return ServiceChangePlan{}, err
	}
	return s.planServiceChange(&target, configRevisionOf(profileContent), effectiveOverrides), nil
}

// planningOverrides resolves the override input a plan is computed with.
func (s *StartedService) planningOverrides(options *OverrideOptions) *OverrideOptions {
	if options != nil {
		return options
	}
	s.serviceAccess.RLock()
	defer s.serviceAccess.RUnlock()
	// The same predicate `runningDocumentSnapshot` uses: during STARTING/STOPPING there is an instance
	// but no running document to compare against, so inheriting its overrides would label a
	// first-start plan with inputs from a Box that is not serving anything.
	if s.instance == nil || s.serviceStatus.Status != ServiceStatus_STARTED || !s.instance.hasOverrides {
		return nil
	}
	inherited := s.instance.overrideOptions
	return &inherited
}

// overrideInputNames lists the override inputs a plan was computed with, for the plan's own report.
//
// It names WHAT was applied, never the values: an override can carry a platform address or a
// package list, and a diagnostic document is not the place for either.
func overrideInputNames(options *OverrideOptions) []string {
	if options == nil {
		return nil
	}
	var names []string
	if options.AutoRedirect {
		names = append(names, "auto_redirect")
	}
	if len(options.IncludePackage) > 0 {
		names = append(names, "include_package")
	}
	if len(options.ExcludePackage) > 0 {
		names = append(names, "exclude_package")
	}
	if len(options.LoopbackAddress) > 0 {
		names = append(names, "loopback_address")
	}
	if len(names) == 0 {
		// An override SET was supplied and every field was zero. That is not the same input as "no
		// override": an all-zero set CLEARS the document fields the overrides own (auto_redirect),
		// so a reader comparing this plan with one computed without it must be able to tell them
		// apart.
		return []string{"empty_override_set"}
	}
	return names
}

// uncoveredOverrideInputs are the effective-configuration inputs this process never sees.
//
// The platform installs its own network settings when the embedder opens the TUN: included and
// excluded routes, the APNs exclusion, the DNS match domains, the system proxy. They are not part
// of the document and no document comparison can observe them, so a plan has to SAY so - otherwise
// "already runs this document" reads as "nothing would change", which is false for an APNs switch.
func uncoveredOverrideInputs() []string {
	return []string{"platform network settings"}
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

// runningDocumentSnapshot returns the running document and its revision, copied under the lock.
//
// The copy is not defensive style: the live apply path REPLACES the instance's document and
// revision under the same lock, so a planner that held a pointer into the instance would read two
// different generations of one document - and would do it concurrently with the write, which is
// the race a review reproduction found. Copying the struct value under the lock gives the planner
// one generation: the document and the revision it came with.
func (s *StartedService) runningDocumentSnapshot() (document option.Options, revision string, available bool) {
	s.serviceAccess.RLock()
	defer s.serviceAccess.RUnlock()
	if s.instance == nil || s.serviceStatus.Status != ServiceStatus_STARTED {
		return option.Options{}, "", false
	}
	return s.instance.options, s.instance.revision, true
}

// planServiceChange compares a target document with the running one.
func (s *StartedService) planServiceChange(target *option.Options, targetRevision string, overrides *OverrideOptions) ServiceChangePlan {
	plan := ServiceChangePlan{
		TargetRevision: targetRevision,
		// Named even when there is nothing to compare, so a report says which domains were asked
		// about rather than leaving three empty structs.
		Mode: DomainPlan{Domain: "mode"},
		DNS:  DomainPlan{Domain: "dns"},
		Rule: DomainPlan{Domain: "rule"},
		// The platform's own network settings are outside every document comparison this plan
		// makes, whatever the answer below turns out to be.
		OverridesConsidered: overrideInputNames(overrides),
		UncoveredInputs:     uncoveredOverrideInputs(),
	}
	snapshot, currentRevision, available := s.runningDocumentSnapshot()
	if !available {
		plan.Provider = PlanProviderRefreshDomain(nil, target, false, "", targetRevision)
		return plan
	}
	current := &snapshot
	plan.Started = true
	plan.CurrentRevision = currentRevision
	plan.Mode = PlanModeDomain(current, plan.CurrentRevision, target, targetRevision)
	plan.DNS = PlanDNSDomain(current, plan.CurrentRevision, target, targetRevision)
	plan.Rule = PlanRuleDomain(current, plan.CurrentRevision, target, targetRevision)
	sameDocument, liveOnly := compareDocuments(current, target)
	plan.Provider = PlanProviderRefreshDomain(current, target, sameDocument, plan.CurrentRevision, targetRevision)
	plan.ChangedDocument = !sameDocument
	switch {
	case sameDocument && len(plan.Mode.Changes) == 0:
		plan.Unchanged = true
	case liveOnly && !plan.Mode.RestartRequired && len(plan.DNS.Changes) == 0 && len(plan.Rule.Changes) == 0:
		plan.LiveOnly = true
	default:
		plan.ChangedSections = changedDocumentSections(current, target)
		plan.RestartRequired = plan.ChangedDocument || plan.Mode.RestartRequired || plan.DNS.RestartRequired || plan.Rule.RestartRequired
	}
	return plan
}

// documentSection is one top-level section of the configuration document, encoded as a value.
type documentSection struct {
	name  string
	value any
}

// documentSections lists the sections in a fixed order, so a report's reason is stable.
func documentSections(options *option.Options) []documentSection {
	if options == nil {
		return nil
	}
	return []documentSection{
		{"schema", options.Schema},
		{"log", options.Log},
		{"dns", options.DNS},
		{"ntp", options.NTP},
		{"certificate", options.Certificate},
		{"certificate_providers", options.CertificateProviders},
		{"http_clients", options.HTTPClients},
		{"network_namespaces", options.NetworkNamespaces},
		{"endpoints", options.Endpoints},
		{"inbounds", options.Inbounds},
		{"outbounds", options.Outbounds},
		{"providers", options.Providers},
		{"route", options.Route},
		{"services", options.Services},
		{"experimental", options.Experimental},
	}
}

// changedDocumentSections names the sections whose content differs.
//
// Each section is encoded with the context-aware encoder, so the comparison sees the same document
// the rest of the plan sees - a provider declaration or an outbound's server is a difference here,
// not an invisible one. A section that cannot be encoded is reported as changed: the caller must
// never be told "this section is identical" on the strength of an encoder that failed.
func changedDocumentSections(current *option.Options, target *option.Options) []string {
	currentSections := documentSections(current)
	targetSections := documentSections(target)
	var changed []string
	for index := range targetSections {
		targetEncoded, targetOK := encodeValue(targetSections[index].value)
		var currentEncoded string
		currentOK := false
		if index < len(currentSections) && currentSections[index].name == targetSections[index].name {
			currentEncoded, currentOK = encodeValue(currentSections[index].value)
		}
		if !targetOK || !currentOK || currentEncoded != targetEncoded {
			changed = append(changed, targetSections[index].name)
		}
	}
	return changed
}

// encodeValue encodes one section with the context-aware encoder.
func encodeValue(value any) (string, bool) {
	encoded, err := singjson.MarshalContext(context.Background(), value)
	if err != nil {
		return "", false
	}
	return string(encoded), true
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
	instance := s.instance
	s.serviceAccess.RUnlock()
	// The plan names the generation it was computed against. A plan computed against another one
	// must not be committed to this Box - the checks below would be about a different document -
	// and the caller is told so instead of being handed a success.
	if status == ServiceStatus_STARTED && instance != nil &&
		plan.CurrentRevision != "" && instance.revision != plan.CurrentRevision {
		report.Status = ServiceChangeStatusFailed
		report.Retained = true
		report.Reason = "the plan was computed against another configuration revision"
		report.Plan.Stale = true
		return report, false
	}
	var owner modeOwner
	// The concrete pointer is checked before the assignment: a nil *clashmode.Manager stored in a
	// non-nil interface passes an `owner == nil` test and then panics on the first method call.
	if instance != nil && instance.clashMode != nil {
		owner = instance.clashMode
	}
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
	// document. Recording it as such means the next plan compares against what actually runs. The
	// write only happens when the instance is still the one the plan was computed against: a
	// replaced Box has its own document and must not receive this one.
	s.serviceAccess.Lock()
	if s.instance == instance && s.serviceStatus.Status == ServiceStatus_STARTED {
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
