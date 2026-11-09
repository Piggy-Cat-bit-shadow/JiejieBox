package libbox

// C01C02 鈥?what AnalyzeConfig can declare without building a box.
//
// AnalyzeConfig parses the document and checks DECLARATIONS: tags that must be unique, and
// references that must resolve inside the document. It deliberately does not build a Box,
// construct a DNS client, fetch a provider or probe anything, so its answers are available
// before any of that could run - and it says so: Checks separates parsed, declarations-checked
// and runtime-checked, and RuntimeChecked is always false here.
//
// Two facts are not errors and must not be reported as such:
//
//   - a remote provider's members do not exist at parse time. A group that names such a
//     provider is resolved at start, so its provider-rendered members ("<provider>/<node>")
//     are reported as PENDING rather than as dangling references;
//   - a value can be syntactically valid and still fail construction (an invalid certificate
//     path, an unstartable service). That is the "parsable but construction fails" category,
//     and AnalyzeConfig's answer for it is Parsed=true with DeclarationsChecked still true -
//     the failure belongs to the build, not to the document's references.

import (
	"crypto/sha256"
	"encoding/hex"
	"runtime"
	"strconv"
	"strings"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
)

// ConfigChecks says how far the analysis got. runtimeChecked is never true for AnalyzeConfig.
type ConfigChecks struct {
	Parsed              bool `json:"parsed"`
	DeclarationsChecked bool `json:"declarationsChecked"`
	RuntimeChecked      bool `json:"runtimeChecked"`
}

// ConfigFinding is one declaration problem, identified by a stable code.
type ConfigFinding struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
	// Reason is the consequence the core would produce: the failure a client would otherwise only
	// discover by starting a Box.
	Reason string `json:"reason,omitempty"`
}

// ConfigPendingProvider is a provider whose members cannot exist until it is fetched.
type ConfigPendingProvider struct {
	Tag  string `json:"tag"`
	Type string `json:"type"`
}

const (
	findingDuplicateTag      = "duplicate_tag"
	findingDanglingReference = "dangling_reference"
	findingPendingReference  = "pending_reference"
)

// analysisBuildID identifies this binary's capability set, so a report can be tied to the
// exact build that produced it instead of to a version string that may be repackaged.
func analysisBuildID() string {
	hasher := sha256.New()
	_, _ = hasher.Write([]byte(runtime.GOOS + "/" + runtime.GOARCH))
	_, _ = hasher.Write([]byte("/quic=" + boolText(C.WithQUIC)))
	return hex.EncodeToString(hasher.Sum(nil))[:16]
}

func boolText(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

// analysisRevision identifies the document, not the parse: the same bytes analysed twice
// produce the same revision, and any edit produces another.
func analysisRevision(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])[:16]
}

// analyseDeclarations checks the declaration graph the document itself can answer.
func analyseDeclarations(options *option.Options) (findings []ConfigFinding, pending []ConfigPendingProvider) {
	inboundTags := make(map[string]bool, len(options.Inbounds))
	for _, inbound := range options.Inbounds {
		if inbound.Tag == "" {
			continue
		}
		if inboundTags[inbound.Tag] {
			findings = append(findings, ConfigFinding{Code: findingDuplicateTag, Detail: "inbound " + inbound.Tag})
		}
		inboundTags[inbound.Tag] = true
	}
	outboundTags := make(map[string]bool, len(options.Outbounds))
	for _, outbound := range options.Outbounds {
		if outbound.Tag == "" {
			continue
		}
		if outboundTags[outbound.Tag] {
			findings = append(findings, ConfigFinding{Code: findingDuplicateTag, Detail: "outbound " + outbound.Tag})
		}
		outboundTags[outbound.Tag] = true
	}
	endpointTags := make(map[string]bool, len(options.Endpoints))
	for _, endpoint := range options.Endpoints {
		if endpoint.Tag == "" {
			continue
		}
		if endpointTags[endpoint.Tag] {
			findings = append(findings, ConfigFinding{Code: findingDuplicateTag, Detail: "endpoint " + endpoint.Tag})
		}
		endpointTags[endpoint.Tag] = true
	}
	providerTags := make(map[string]bool, len(options.Providers))
	for _, provider := range options.Providers {
		if provider.Tag == "" {
			continue
		}
		if providerTags[provider.Tag] {
			findings = append(findings, ConfigFinding{Code: findingDuplicateTag, Detail: "provider " + provider.Tag})
		}
		providerTags[provider.Tag] = true
		if provider.Type == "remote" {
			// A remote provider is the dynamic case: its nodes are rendered at start from a
			// document that does not exist yet.
			pending = append(pending, ConfigPendingProvider{Tag: provider.Tag, Type: provider.Type})
		}
	}

	// Every outbound tag a reference may name. Provider-rendered tags ("<provider>/<node>")
	// are not in the document, so a reference containing "/" is never dangling by itself.
	known := func(tag string) bool {
		return tag == "" || outboundTags[tag] || endpointTags[tag]
	}
	providerRendered := func(tag string) bool {
		return strings.Contains(tag, "/")
	}
	for _, outbound := range options.Outbounds {
		// The registry's CreateOptions returns a POINTER to the options struct, which is what
		// the decoder fills; the value cases keep this correct if a caller builds options by
		// hand (a test, an embedder).
		switch group := outbound.Options.(type) {
		case *option.SelectorOutboundOptions:
			findings = append(findings, checkGroupMembers(group.Outbounds, group.Providers, group.UseAllProviders, providerTags, known, providerRendered)...)
		case option.SelectorOutboundOptions:
			findings = append(findings, checkGroupMembers(group.Outbounds, group.Providers, group.UseAllProviders, providerTags, known, providerRendered)...)
		case *option.URLTestOutboundOptions:
			findings = append(findings, checkGroupMembers(group.Outbounds, group.Providers, group.UseAllProviders, providerTags, known, providerRendered)...)
		case option.URLTestOutboundOptions:
			findings = append(findings, checkGroupMembers(group.Outbounds, group.Providers, group.UseAllProviders, providerTags, known, providerRendered)...)
		}
	}
	// A rule that names a rule-set the document never declares is not a reference the Box
	// tolerates: the router refuses to start. Reporting it here is the difference between "this
	// profile is fine" and a start that fails on the device. Routing and DNS rules share the same
	// declaration list.
	declaredRuleSets := make(map[string]bool)
	if options.Route != nil {
		if options.Route.Final != "" && !known(options.Route.Final) {
			findings = append(findings, ConfigFinding{Code: findingDanglingReference, Detail: "route.final " + options.Route.Final})
		}
		for _, ruleSet := range options.Route.RuleSet {
			for _, tag := range ruleSet.Tag {
				declaredRuleSets[tag] = true
			}
		}
		for index, rule := range options.Route.Rules {
			findings = append(findings, checkRuleSetReferences(routeRuleSetReferences(rule), declaredRuleSets, "route.rules["+strconv.Itoa(index)+"]")...)
		}
	}
	if options.DNS != nil {
		dnsServerTags := make(map[string]bool, len(options.DNS.Servers))
		for _, server := range options.DNS.Servers {
			dnsServerTags[server.Tag] = true
		}
		if options.DNS.Final != "" && !dnsServerTags[options.DNS.Final] {
			findings = append(findings, ConfigFinding{Code: findingDanglingReference, Detail: "dns.final " + options.DNS.Final})
		}
		for index, rule := range options.DNS.Rules {
			findings = append(findings, checkRuleSetReferences(dnsRuleSetReferences(rule), declaredRuleSets, "dns.rules["+strconv.Itoa(index)+"]")...)
		}
	}
	return findings, pending
}

// checkRuleSetReferences reports every rule-set tag a rule names that the document never declares.
func checkRuleSetReferences(tags []string, declared map[string]bool, where string) []ConfigFinding {
	var findings []ConfigFinding
	for _, tag := range tags {
		if declared[tag] {
			continue
		}
		findings = append(findings, ConfigFinding{
			Code:   findingDanglingReference,
			Detail: where + " rule_set " + tag,
			Reason: "the box fails to start with: rule-set not found: " + tag,
		})
	}
	return findings
}

// routeRuleSetReferences flattens the rule-set tags one routing rule names, nested rules included.
func routeRuleSetReferences(rule option.Rule) []string {
	switch rule.Type {
	case "", C.RuleTypeDefault:
		return rule.DefaultOptions.RuleSet
	case C.RuleTypeLogical:
		var references []string
		for _, child := range rule.LogicalOptions.Rules {
			references = append(references, routeRuleSetReferences(child)...)
		}
		return references
	default:
		return nil
	}
}

// dnsRuleSetReferences flattens the rule-set tags one DNS rule names, nested rules included.
func dnsRuleSetReferences(rule option.DNSRule) []string {
	switch rule.Type {
	case "", C.RuleTypeDefault:
		return rule.DefaultOptions.RuleSet
	case C.RuleTypeLogical:
		var references []string
		for _, child := range rule.LogicalOptions.Rules {
			references = append(references, dnsRuleSetReferences(child)...)
		}
		return references
	default:
		return nil
	}
}

// checkGroupMembers validates one group's members and provider references.
func checkGroupMembers(members []string, providers []string, useAllProviders bool, providerTags map[string]bool, known func(string) bool, providerRendered func(string) bool) []ConfigFinding {
	var findings []ConfigFinding
	for _, member := range members {
		if known(member) || providerRendered(member) {
			continue
		}
		findings = append(findings, ConfigFinding{Code: findingDanglingReference, Detail: "group member " + member})
	}
	for _, provider := range providers {
		if providerTags[provider] {
			continue
		}
		findings = append(findings, ConfigFinding{Code: findingDanglingReference, Detail: "group provider " + provider})
	}
	_ = useAllProviders
	return findings
}
