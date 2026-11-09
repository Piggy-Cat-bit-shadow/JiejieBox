package daemon

// C04 - the domains without a live owner: DNS policy and routing rules.
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
//
// # Why the comparison is content and not shape
//
// The first version compared len(rules), a tag/type pair per DNS server and the bare tag list of
// the rule sets. That answers "did the document get longer" and not "did the document change":
// network tcp -> udp, a single domain replaced by another, one DNS rule's ip_version 4 -> 6, a
// server whose address moved under the same tag and type, and a remote rule set re-pointed at
// another artifact all kept the shape and were all reported unchanged. The caller then applied
// the edit that needed a reload as if it needed nothing. Each element is compared by its content
// instead - including the variant payloads (a DNS server's address, an outbound's server), which
// plain encoding/json silently drops because those types only marshal through the context-aware
// encoder.
//
// # Ordered and unordered facts
//
// Routing rules and DNS rules are ordered: the first match wins, so the comparison keeps the
// order. DNS servers and rule sets are named facts - the query/rule reaches them by tag and the
// array order selects nothing - so they are compared per tag. A rule set's explicit-but-default
// format is canonicalised away by its own marshaller, so writing the default out loud is not a
// change.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	stdjson "encoding/json"
	"slices"
	"sort"
	"strconv"
	"strings"

	singjson "github.com/sagernet/sing/common/json"

	"github.com/sagernet/sing-box/option"
)

// PlanDNSDomain compares the DNS policy of two documents.
func PlanDNSDomain(current *option.Options, currentRevision string, target *option.Options, targetRevision string) DomainPlan {
	plan := DomainPlan{Domain: "dns", Supported: true, CurrentRevision: currentRevision, TargetRevision: targetRevision}
	currentServers, currentServersKnown := dnsServerFingerprints(dnsServers(current))
	targetServers, targetServersKnown := dnsServerFingerprints(dnsServers(target))
	if !currentServersKnown || !targetServersKnown || !slices.Equal(currentServers, targetServers) {
		plan.Changes = append(plan.Changes, DomainChange{
			Kind:   "dns_servers",
			From:   strings.Join(dnsServerNames(current), ","),
			To:     strings.Join(dnsServerNames(target), ","),
			Detail: serverDifference(currentServers, targetServers),
		})
	}
	currentRules, currentRulesKnown := dnsRuleFingerprints(dnsRules(current))
	targetRules, targetRulesKnown := dnsRuleFingerprints(dnsRules(target))
	if !currentRulesKnown || !targetRulesKnown || !slices.Equal(currentRules, targetRules) {
		plan.Changes = append(plan.Changes, DomainChange{Kind: "dns_rules", Detail: ruleDifference(currentRules, targetRules)})
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
	currentRules, currentRulesKnown := routeRuleFingerprints(routeRules(current))
	targetRules, targetRulesKnown := routeRuleFingerprints(routeRules(target))
	if !currentRulesKnown || !targetRulesKnown || !slices.Equal(currentRules, targetRules) {
		plan.Changes = append(plan.Changes, DomainChange{Kind: "route_rules", Detail: ruleDifference(currentRules, targetRules)})
	}
	currentRuleSets, currentRuleSetsKnown := ruleSetFingerprints(ruleSets(current))
	targetRuleSets, targetRuleSetsKnown := ruleSetFingerprints(ruleSets(target))
	if !currentRuleSetsKnown || !targetRuleSetsKnown || !slices.Equal(currentRuleSets, targetRuleSets) {
		plan.Changes = append(plan.Changes, DomainChange{
			Kind:   "rule_sets",
			From:   strings.Join(ruleSetTags(current), ","),
			To:     strings.Join(ruleSetTags(target), ","),
			Detail: serverDifference(currentRuleSets, targetRuleSets),
		})
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

// fingerprint encodes one option value as a short digest of its canonical JSON. The plan does not
// need the value itself; it needs to know whether two documents agree, and the digest is what
// keeps a plan's change list from embedding two copies of every rule.
//
// The second result is false when the value could not be encoded at all. The caller then reports
// a change: a document whose elements cannot be canonicalised cannot be proven equal to another,
// and "unchanged" is the one answer that must not be guessed.
func fingerprint(value any) (string, bool) {
	encoded, err := stdjson.Marshal(value)
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])[:16], true
}

// routeRuleFingerprints returns one digest per routing rule in document order.
func routeRuleFingerprints(rules []option.Rule) ([]string, bool) {
	fingerprints := make([]string, 0, len(rules))
	for _, rule := range rules {
		digest, known := fingerprint(rule)
		if !known {
			return fingerprints, false
		}
		fingerprints = append(fingerprints, digest)
	}
	return fingerprints, true
}

// dnsRuleFingerprints returns one digest per DNS rule in document order.
func dnsRuleFingerprints(rules []option.DNSRule) ([]string, bool) {
	fingerprints := make([]string, 0, len(rules))
	for _, rule := range rules {
		digest, known := fingerprint(rule)
		if !known {
			return fingerprints, false
		}
		fingerprints = append(fingerprints, digest)
	}
	return fingerprints, true
}

// dnsServerFingerprints returns one "tag|digest" entry per DNS server, sorted by tag.
//
// A DNS server is reached by tag, so the array order selects nothing; the tag and everything the
// server carries (the variant payload included) is what a query goes to. Two servers may share a
// tag in a broken document - sorting by the whole entry keeps that case deterministic.
//
// The payload has to be encoded separately: DNSServerOptions keeps it in a `json:"-"` field and
// only the context-aware marshaller merges it in, while the payload itself (the udp/tls/https
// server options) is plain data that encoding/json encodes as written.
func dnsServerFingerprints(servers []option.DNSServerOptions) ([]string, bool) {
	fingerprints := make([]string, 0, len(servers))
	for _, server := range servers {
		digest, known := fingerprint(struct {
			Type    string `json:"type"`
			Tag     string `json:"tag"`
			Options any    `json:"options,omitempty"`
		}{Type: server.Type, Tag: server.Tag, Options: server.Options})
		if !known {
			return fingerprints, false
		}
		fingerprints = append(fingerprints, server.Tag+"|"+digest)
	}
	sort.Strings(fingerprints)
	return fingerprints, true
}

// ruleSetFingerprints returns one "tag|digest" entry per rule-set tag, sorted by tag.
//
// A rule set may carry several tags that name the same definition; each tag is compared on that
// definition, because a routing rule reaches the set by tag.
func ruleSetFingerprints(sets []option.RuleSet) ([]string, bool) {
	fingerprints := make([]string, 0, len(sets))
	for _, set := range sets {
		digest, known := fingerprint(set)
		if !known {
			return fingerprints, false
		}
		for _, tag := range set.Tag {
			fingerprints = append(fingerprints, tag+"|"+digest)
		}
	}
	sort.Strings(fingerprints)
	return fingerprints, true
}

// ruleDifference names where two ordered rule lists first disagree.
func ruleDifference(current []string, target []string) string {
	for index := range min(len(current), len(target)) {
		if current[index] != target[index] {
			return "rule " + strconv.Itoa(index) + " differs"
		}
	}
	switch {
	case len(target) > len(current):
		return "rule " + strconv.Itoa(len(current)) + " was added"
	case len(current) > len(target):
		return "rule " + strconv.Itoa(len(target)) + " was removed"
	default:
		return ""
	}
}

// serverDifference names the tags whose entry differs between two "tag|digest" lists.
func serverDifference(current []string, target []string) string {
	currentByName := entriesByName(current)
	targetByName := entriesByName(target)
	var added, removed, changed []string
	for name, digest := range targetByName {
		currentDigest, loaded := currentByName[name]
		switch {
		case !loaded:
			added = append(added, name)
		case currentDigest != digest:
			changed = append(changed, name)
		}
	}
	for name := range currentByName {
		if _, loaded := targetByName[name]; !loaded {
			removed = append(removed, name)
		}
	}
	var parts []string
	if len(changed) > 0 {
		parts = append(parts, "changed: "+strings.Join(sortedCopy(changed), ","))
	}
	if len(added) > 0 {
		parts = append(parts, "added: "+strings.Join(sortedCopy(added), ","))
	}
	if len(removed) > 0 {
		parts = append(parts, "removed: "+strings.Join(sortedCopy(removed), ","))
	}
	return strings.Join(parts, "; ")
}

// entriesByName maps the name half of a "name|digest" list. A duplicate name keeps the last
// digest, which is deterministic because the lists are sorted.
func entriesByName(entries []string) map[string]string {
	byName := make(map[string]string, len(entries))
	for _, entry := range entries {
		name, digest, loaded := strings.Cut(entry, "|")
		if !loaded {
			continue
		}
		byName[name] = digest
	}
	return byName
}

func sortedCopy(values []string) []string {
	ordered := slices.Clone(values)
	sort.Strings(ordered)
	return ordered
}

// routeRules returns a document's routing rules, or nil.
func routeRules(options *option.Options) []option.Rule {
	if options == nil || options.Route == nil {
		return nil
	}
	return options.Route.Rules
}

// dnsRules returns a document's DNS rules, or nil.
func dnsRules(options *option.Options) []option.DNSRule {
	if options == nil || options.DNS == nil {
		return nil
	}
	return options.DNS.Rules
}

// dnsServers returns a document's DNS servers, or nil.
func dnsServers(options *option.Options) []option.DNSServerOptions {
	if options == nil || options.DNS == nil {
		return nil
	}
	return options.DNS.Servers
}

// ruleSets returns a document's rule sets, or nil.
func ruleSets(options *option.Options) []option.RuleSet {
	if options == nil || options.Route == nil {
		return nil
	}
	return options.Route.RuleSet
}

// dnsServerNames names the DNS servers a document declares, for a plan's readable from/to.
func dnsServerNames(options *option.Options) []string {
	servers := dnsServers(options)
	names := make([]string, 0, len(servers))
	for _, server := range servers {
		names = append(names, server.Tag+"/"+server.Type)
	}
	sort.Strings(names)
	return names
}

// currentDNSFinal is the document's DNS final server.
func currentDNSFinal(options *option.Options) string {
	if options == nil || options.DNS == nil {
		return ""
	}
	return options.DNS.Final
}

// currentRouteFinal is the document's routing final outbound.
func currentRouteFinal(options *option.Options) string {
	if options == nil || options.Route == nil {
		return ""
	}
	return options.Route.Final
}

// ruleSetTags names the rule sets a document declares, for a plan's readable from/to.
func ruleSetTags(options *option.Options) []string {
	sets := ruleSets(options)
	tags := make([]string, 0, len(sets))
	for _, set := range sets {
		// A rule set may carry several tags; each one is a name routing rules can reference.
		tags = append(tags, set.Tag...)
	}
	sort.Strings(tags)
	return tags
}

// encodeDocument encodes a configuration document with the context-aware encoder, which is the
// only one that sees the variant payloads: a DNS server's address and an outbound's server live
// behind `json:"-"` fields and are merged in by their MarshalJSONContext methods.
func encodeDocument(options *option.Options) ([]byte, error) {
	return singjson.MarshalContext(context.Background(), options)
}
