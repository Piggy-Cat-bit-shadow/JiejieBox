package daemon

// C04 — the domains without a live owner: DNS policy and routing rules.
//
// Not every domain can be applied to a running Box. The Clash mode has an owner that can switch
// it; a provider has an owner that can refresh it; but DNS servers, DNS rules and routing rules
// are owned by the Box itself, and changing them means building a new one. That is not a failure
// and it is not "unsupported": it is a RESTART REQUIREMENT, and the plan says so before anything
// is touched.
//
// The plan is still useful, and that is the point of having these domains at all: it names what
// changed, so a caller can tell "this edit needs a reload" from "this edit needs nothing" without
// diffing two documents itself, and a caller that cannot restart right now knows to postpone the
// edit instead of applying half of it.

import (
	"sort"
	"strings"

	"github.com/sagernet/sing-box/option"
)

// PlanDNSDomain compares the DNS policy of two documents.
func PlanDNSDomain(current *option.Options, currentRevision string, target *option.Options, targetRevision string) DomainPlan {
	plan := DomainPlan{Domain: "dns", Supported: true, CurrentRevision: currentRevision, TargetRevision: targetRevision}
	currentServers := dnsServerFingerprint(current)
	targetServers := dnsServerFingerprint(target)
	if !sameStringSet(currentServers, targetServers) {
		plan.Changes = append(plan.Changes, DomainChange{Kind: "dns_servers", From: strings.Join(currentServers, ","), To: strings.Join(targetServers, ",")})
	}
	if currentDNSRules(current) != currentDNSRules(target) {
		plan.Changes = append(plan.Changes, DomainChange{Kind: "dns_rules"})
	}
	if currentDNSFinal(current) != currentDNSFinal(target) {
		plan.Changes = append(plan.Changes, DomainChange{Kind: "dns_final", From: currentDNSFinal(current), To: currentDNSFinal(target)})
	}
	if len(plan.Changes) > 0 {
		// DNS is owned by the Box, so a change needs a new Box: the plan states that instead of
		// pretending a live apply exists.
		plan.RestartRequired = true
	}
	return plan
}

// PlanRuleDomain compares the routing rules and rule sets of two documents.
func PlanRuleDomain(current *option.Options, currentRevision string, target *option.Options, targetRevision string) DomainPlan {
	plan := DomainPlan{Domain: "rule", Supported: true, CurrentRevision: currentRevision, TargetRevision: targetRevision}
	if currentRouteRules(current) != currentRouteRules(target) {
		plan.Changes = append(plan.Changes, DomainChange{Kind: "route_rules"})
	}
	if !sameStringSet(ruleSetTags(current), ruleSetTags(target)) {
		plan.Changes = append(plan.Changes, DomainChange{Kind: "rule_sets", From: strings.Join(ruleSetTags(current), ","), To: strings.Join(ruleSetTags(target), ",")})
	}
	if currentRouteFinal(current) != currentRouteFinal(target) {
		plan.Changes = append(plan.Changes, DomainChange{Kind: "route_final", From: currentRouteFinal(current), To: currentRouteFinal(target)})
	}
	if len(plan.Changes) > 0 {
		plan.RestartRequired = true
	}
	return plan
}

// applyRestartDomain commits a plan for a domain the Box owns: it can only report the
// requirement, never apply it.
func applyRestartDomain(plan DomainPlan) DomainReport {
	report := DomainReport{Domain: plan.Domain, Revision: plan.TargetRevision}
	switch {
	case !plan.Supported:
		report.Status = DomainStatusUnsupported
		report.Reason = plan.UnsupportedReason
	case len(plan.Changes) == 0:
		report.Status = DomainStatusUnchanged
	case plan.RestartRequired:
		report.Status = DomainStatusRestartRequired
		report.RestartRequired = true
		report.Reason = "this domain is owned by the box; the change takes effect after a reload"
		report.Applied = nil
	default:
		report.Status = DomainStatusFailed
		report.Reason = "this domain has no live owner"
	}
	return report
}

// ApplyDNSDomain reports what a DNS policy change requires on the running box.
func (s *StartedService) ApplyDNSDomain(plan DomainPlan, runningRevision string) (*DomainReport, error) {
	if stale := staleDomainReport(plan, runningRevision); stale != nil {
		return stale, nil
	}
	report := applyRestartDomain(plan)
	return &report, nil
}

// ApplyRuleDomain reports what a routing-rule change requires on the running box.
func (s *StartedService) ApplyRuleDomain(plan DomainPlan, runningRevision string) (*DomainReport, error) {
	if stale := staleDomainReport(plan, runningRevision); stale != nil {
		return stale, nil
	}
	report := applyRestartDomain(plan)
	return &report, nil
}

// staleDomainReport refuses a plan computed against another configuration.
func staleDomainReport(plan DomainPlan, runningRevision string) *DomainReport {
	if plan.CurrentRevision == "" || runningRevision == "" || plan.CurrentRevision == runningRevision {
		return nil
	}
	return &DomainReport{
		Domain:   plan.Domain,
		Status:   DomainStatusStalePlan,
		Reason:   "the plan was computed against another configuration revision",
		Revision: plan.TargetRevision,
	}
}

// dnsServerFingerprint names the DNS servers a document declares.
func dnsServerFingerprint(options *option.Options) []string {
	if options == nil || options.DNS == nil {
		return nil
	}
	tags := make([]string, 0, len(options.DNS.Servers))
	for _, server := range options.DNS.Servers {
		tags = append(tags, server.Tag+"/"+server.Type)
	}
	sort.Strings(tags)
	return tags
}

// currentDNSRules is the number of DNS rules, which is enough to detect a change of substance
// without copying the rules themselves.
func currentDNSRules(options *option.Options) int {
	if options == nil || options.DNS == nil {
		return 0
	}
	return len(options.DNS.Rules)
}

// currentDNSFinal is the document's DNS final server.
func currentDNSFinal(options *option.Options) string {
	if options == nil || options.DNS == nil {
		return ""
	}
	return options.DNS.Final
}

// currentRouteRules is the number of routing rules.
func currentRouteRules(options *option.Options) int {
	if options == nil || options.Route == nil {
		return 0
	}
	return len(options.Route.Rules)
}

// currentRouteFinal is the document's routing final outbound.
func currentRouteFinal(options *option.Options) string {
	if options == nil || options.Route == nil {
		return ""
	}
	return options.Route.Final
}

// ruleSetTags names the rule sets a document declares.
func ruleSetTags(options *option.Options) []string {
	if options == nil || options.Route == nil {
		return nil
	}
	tags := make([]string, 0, len(options.Route.RuleSet))
	for _, ruleSet := range options.Route.RuleSet {
		// A rule set may carry several tags; each one is a name routing rules can reference.
		for _, tag := range ruleSet.Tag {
			tags = append(tags, tag)
		}
	}
	sort.Strings(tags)
	return tags
}

// sameStringSet reports whether two sorted tag lists contain the same tags.
func sameStringSet(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
