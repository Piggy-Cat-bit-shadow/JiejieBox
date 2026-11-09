package dns

// O02 — the DNS half of "complete the process path on demand".
//
// A process_name / process_path / package_name DNS rule needs the /proc walk, while a domain or
// address rule does not. The walk used to happen before the rule walk started, so a configuration
// with one path rule paid for it on every query - and, because the route layer folds the DNS rule
// list into its own "paths needed" decision, on every CONNECTION too.
//
// This file is the DNS-side mechanism only: the rule walk completes the owner-only answer when it
// reaches a rule that reads a path. The completer is INJECTED - the route layer owns the lookup and
// the split cache, and dns must not depend on it. Until a completer is injected (unit tests, a
// standalone router) nothing changes: the rules are evaluated against whatever metadata the caller
// supplied, which is what the package did before.

import (
	"context"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
)

// SetProcessPathCompleter installs the function the rule walk calls before evaluating a rule that
// reads a process path.
//
// The function receives the query's own metadata (same pointer the walk matches with) and must fill
// ProcessInfo. Returning with ProcessInfo unchanged means "could not complete": the rule then
// evaluates as it would have without the fact, exactly as it did before this mechanism existed.
func (r *Router) SetProcessPathCompleter(complete func(ctx context.Context, metadata *adapter.InboundContext)) {
	if r == nil {
		return
	}
	r.rulesAccess.Lock()
	r.processPathCompleter = complete
	r.rulesAccess.Unlock()
}

// dnsRuleNeedsProcessPaths reports whether evaluating this DNS rule can read a field that only a
// /proc walk provides.
//
// A referenced rule-set is treated as needing paths: the document is remote and mutable, so it can
// carry process conditions this configuration never mentions. That is the conservative reading the
// route layer already uses, kept identical here so the two halves cannot disagree about when the
// walk is required.
func dnsRuleNeedsProcessPaths(rule option.DNSRule) bool {
	switch rule.Type {
	case C.RuleTypeLogical:
		for _, nested := range rule.LogicalOptions.Rules {
			if dnsRuleNeedsProcessPaths(nested) {
				return true
			}
		}
		return false
	default:
		options := rule.DefaultOptions
		return len(options.ProcessName) > 0 ||
			len(options.ProcessPath) > 0 ||
			len(options.ProcessPathRegex) > 0 ||
			len(options.PackageName) > 0 ||
			len(options.PackageNameRegex) > 0 ||
			len(options.RuleSet) > 0
	}
}

// processPathRuleFlags maps the raw rule list to one flag per built rule, in the same order.
func processPathRuleFlags(rawRules []option.DNSRule) []bool {
	if len(rawRules) == 0 {
		return nil
	}
	flags := make([]bool, len(rawRules))
	for index, rawRule := range rawRules {
		flags[index] = dnsRuleNeedsProcessPaths(rawRule)
	}
	return flags
}

// ruleNeedsProcessPath reports whether the rule at this index reads a process field. The flag is
// indexed with the built rule list; an index past it (a router built without flags) answers false,
// which keeps the old "evaluate against what the caller supplied" behaviour.
func (r *Router) ruleNeedsProcessPath(ruleIndex int) bool {
	if r == nil || ruleIndex < 0 {
		return false
	}
	r.rulesAccess.RLock()
	defer r.rulesAccess.RUnlock()
	return ruleIndex < len(r.ruleNeedsPaths) && r.ruleNeedsPaths[ruleIndex]
}

// dnsProcessNeed names WHICH process field evaluating a rule can read.
//
// The distinction is load-bearing: a process_name rule is answered by the paths, a package_name
// rule by the package list, and one field is NOT a substitute for the other. Treating any process
// fact as "the process is known" made a preview report a decision it could not actually make.
type dnsProcessNeed uint8

const (
	dnsNeedNone    dnsProcessNeed = 0
	dnsNeedPath    dnsProcessNeed = 1 << 0
	dnsNeedPackage dnsProcessNeed = 1 << 1
)

// dnsRuleProcessNeed reports which process fields this rule can read.
//
// A referenced rule-set is credited with the PATH need (a remote document can carry process
// conditions; that is the conservative reading the route layer uses), never with the package need:
// a rule-set naming a package on a platform without a package manager could never match, and
// demanding packages would make the preview undecidable everywhere.
func dnsRuleProcessNeed(rule option.DNSRule) dnsProcessNeed {
	switch rule.Type {
	case C.RuleTypeLogical:
		var need dnsProcessNeed
		for _, nested := range rule.LogicalOptions.Rules {
			need |= dnsRuleProcessNeed(nested)
		}
		return need
	default:
		options := rule.DefaultOptions
		var need dnsProcessNeed
		if len(options.ProcessName) > 0 || len(options.ProcessPath) > 0 || len(options.ProcessPathRegex) > 0 || len(options.RuleSet) > 0 {
			need |= dnsNeedPath
		}
		if len(options.PackageName) > 0 || len(options.PackageNameRegex) > 0 {
			need |= dnsNeedPackage
		}
		return need
	}
}

// processNeedFlags maps the raw rule list to one mask per built rule, in the same order.
func processNeedFlags(rawRules []option.DNSRule) []dnsProcessNeed {
	if len(rawRules) == 0 {
		return nil
	}
	flags := make([]dnsProcessNeed, len(rawRules))
	for index, rawRule := range rawRules {
		flags[index] = dnsRuleProcessNeed(rawRule)
	}
	return flags
}

// processNeedAt reports the fields the rule at this index may read. A router built without masks
// (a test) falls back to ruleNeedsPaths, which is read as a path need.
func (r *Router) processNeedAt(ruleIndex int) dnsProcessNeed {
	if r == nil || ruleIndex < 0 {
		return dnsNeedNone
	}
	r.rulesAccess.RLock()
	defer r.rulesAccess.RUnlock()
	if ruleIndex < len(r.ruleProcessNeeds) {
		return r.ruleProcessNeeds[ruleIndex]
	}
	if ruleIndex < len(r.ruleNeedsPaths) && r.ruleNeedsPaths[ruleIndex] {
		return dnsNeedPath
	}
	return dnsNeedNone
}

// metadataHasProcessNeed reports whether the metadata already carries every field the rule needs.
// A supplied fact is authoritative: it is what the caller stated, and no walk replaces it.
func metadataHasProcessNeed(metadata *adapter.InboundContext, need dnsProcessNeed) bool {
	if need == dnsNeedNone {
		return true
	}
	if metadata == nil || metadata.ProcessInfo == nil {
		return false
	}
	if need&dnsNeedPath != 0 && len(metadata.ProcessInfo.ProcessPaths) == 0 {
		return false
	}
	if need&dnsNeedPackage != 0 && len(metadata.ProcessInfo.PackageNames) == 0 {
		return false
	}
	return true
}

// processNeedDependency renders the missing fact for a preview.
func processNeedDependency(need dnsProcessNeed) string {
	switch need {
	case dnsNeedPath:
		return "process_path"
	case dnsNeedPackage:
		return "process_package"
	case dnsNeedPath | dnsNeedPackage:
		return "process_path, process_package"
	default:
		return ""
	}
}

// completeProcessPathsForRule runs the injected completer once, when the walk reaches a rule that
// reads a path and the metadata does not have one yet.
//
// It reports whether it ran, which the caller uses to stop trying for the rest of the walk: a
// completer that could not fill the field would otherwise be invoked again for the next path rule,
// turning one failed lookup into one per rule.
func (r *Router) completeProcessPathsForRule(ctx context.Context, metadata *adapter.InboundContext, ruleIndex int) bool {
	if r == nil || ruleIndex < 0 {
		return false
	}
	need := r.processNeedAt(ruleIndex)
	if need == dnsNeedNone {
		return false
	}
	r.rulesAccess.RLock()
	completer := r.processPathCompleter
	r.rulesAccess.RUnlock()
	if completer == nil {
		return false
	}
	if metadataHasProcessNeed(metadata, need) {
		return false
	}
	completer(ctx, metadata)
	return true
}
