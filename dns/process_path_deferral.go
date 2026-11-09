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

// metadataHasProcessPathFact reports whether the metadata already carries a field a path-reading
// rule can use. A supplied fact is authoritative: it is what the caller stated, and no walk is
// attempted to replace it.
func metadataHasProcessPathFact(metadata *adapter.InboundContext) bool {
	if metadata == nil || metadata.ProcessInfo == nil {
		return false
	}
	return len(metadata.ProcessInfo.ProcessPaths) > 0 || len(metadata.ProcessInfo.PackageNames) > 0
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
	r.rulesAccess.RLock()
	completer := r.processPathCompleter
	needed := ruleIndex < len(r.ruleNeedsPaths) && r.ruleNeedsPaths[ruleIndex]
	r.rulesAccess.RUnlock()
	if !needed || completer == nil {
		return false
	}
	if metadataHasProcessPathFact(metadata) {
		return false
	}
	completer(ctx, metadata)
	return true
}
