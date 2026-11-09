package route

import (
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
)

func hasRule(rules []option.Rule, cond func(rule option.DefaultRule) bool) bool {
	for _, rule := range rules {
		switch rule.Type {
		case C.RuleTypeDefault:
			if cond(rule.DefaultOptions) {
				return true
			}
		case C.RuleTypeLogical:
			if hasRule(rule.LogicalOptions.Rules, cond) {
				return true
			}
		}
	}
	return false
}

func hasDNSRule(rules []option.DNSRule, cond func(rule option.DefaultDNSRule) bool) bool {
	for _, rule := range rules {
		switch rule.Type {
		case C.RuleTypeDefault:
			if cond(rule.DefaultOptions) {
				return true
			}
		case C.RuleTypeLogical:
			if hasDNSRule(rule.LogicalOptions.Rules, cond) {
				return true
			}
		}
	}
	return false
}

func isProcessRule(rule option.DefaultRule) bool {
	return len(rule.ProcessName) > 0 || len(rule.ProcessPath) > 0 || len(rule.ProcessPathRegex) > 0 || len(rule.PackageName) > 0 || len(rule.PackageNameRegex) > 0 || len(rule.User) > 0 || len(rule.UserID) > 0
}

func isProcessDNSRule(rule option.DefaultDNSRule) bool {
	return len(rule.ProcessName) > 0 || len(rule.ProcessPath) > 0 || len(rule.ProcessPathRegex) > 0 || len(rule.PackageName) > 0 || len(rule.PackageNameRegex) > 0 || len(rule.User) > 0 || len(rule.UserID) > 0
}

// isProcessPathRule reports whether a rule reads a field that only a /proc walk
// provides: a process name, a process path or a package name. A user/user_id
// rule does not, because the socket diag table already carries the uid.
func isProcessPathRule(rule option.DefaultRule) bool {
	return len(rule.ProcessName) > 0 || len(rule.ProcessPath) > 0 || len(rule.ProcessPathRegex) > 0 || len(rule.PackageName) > 0 || len(rule.PackageNameRegex) > 0
}

// usesRuleSet reports whether a rule references a rule-set document.
//
// It decides whether the fully up-front process lookup is still required: a
// rule-set document is remote and mutable, so it can carry process conditions
// this configuration never mentions, and the walk cannot be deferred past the
// point where such a document is consulted.
func usesRuleSet(rule option.DefaultRule) bool {
	return len(rule.RuleSet) > 0
}

func isProcessPathDNSRule(rule option.DefaultDNSRule) bool {
	return len(rule.ProcessName) > 0 || len(rule.ProcessPath) > 0 || len(rule.ProcessPathRegex) > 0 || len(rule.PackageName) > 0 || len(rule.PackageNameRegex) > 0
}

// processNeed names the process fields a rule can read that only the /proc walk provides.
//
// It is a BITMASK, not a bool, because the bypass decision has to know WHICH fact is missing. A
// process_name rule is answered by the paths; a package_name rule is answered by the package list,
// which on Android comes from the package manager and is absent from the path scan on every other
// platform. Treating every process rule as needing both would refuse the native fast path for every
// Linux configuration that names a process, because Linux never has a package list to show.
type processNeed uint8

const (
	processNeedNone    processNeed = 0
	processNeedPath    processNeed = 1 << 0 // process_name, process_path, process_path_regex
	processNeedPackage processNeed = 1 << 1 // package_name, package_name_regex
)

// processNeedForRule reports which process facts evaluating this rule may read.
//
// A referenced rule_set is treated as needing the PATH, for the same reason needsProcessPaths treats
// it that way: the document is remote and mutable, so it can carry process conditions this
// configuration never mentions. The package bit is deliberately not implied - a rule-set that names
// a package on a platform without a package manager could never match, and demanding a package list
// there would disable the fast path for every rule-set configuration.
func processNeedForRule(rule option.Rule) processNeed {
	switch rule.Type {
	case C.RuleTypeLogical:
		var need processNeed
		for _, nested := range rule.LogicalOptions.Rules {
			need |= processNeedForRule(nested)
		}
		return need
	case C.RuleTypeDefault:
		return processNeedForDefaultRule(rule.DefaultOptions)
	default:
		return processNeedNone
	}
}

func processNeedForDefaultRule(rule option.DefaultRule) processNeed {
	var need processNeed
	if len(rule.ProcessName) > 0 || len(rule.ProcessPath) > 0 || len(rule.ProcessPathRegex) > 0 || len(rule.RuleSet) > 0 {
		need |= processNeedPath
	}
	if len(rule.PackageName) > 0 || len(rule.PackageNameRegex) > 0 {
		need |= processNeedPackage
	}
	return need
}

// needsProcessPaths reports whether any rule can look at a field that only a
// /proc walk provides (process name/path, package name). A user/user_id rule
// does not need one: the socket diag table already carries the uid.
//
// A referenced rule_set is treated as needing paths: a rule-set document can
// carry process conditions, so the alternative would be to decide from a
// remote, mutable document that the walk is unnecessary. Being conservative
// here keeps the owner-only fast path safe.
func needsProcessPaths(rules []option.Rule) bool {
	for _, rule := range rules {
		switch rule.Type {
		case C.RuleTypeDefault:
			if isProcessPathRule(rule.DefaultOptions) || len(rule.DefaultOptions.RuleSet) > 0 {
				return true
			}
		case C.RuleTypeLogical:
			if needsProcessPaths(rule.LogicalOptions.Rules) {
				return true
			}
		}
	}
	return false
}

func needsProcessPathDNSRules(rules []option.DNSRule) bool {
	for _, rule := range rules {
		switch rule.Type {
		case C.RuleTypeDefault:
			if isProcessPathDNSRule(rule.DefaultOptions) || len(rule.DefaultOptions.RuleSet) > 0 {
				return true
			}
		case C.RuleTypeLogical:
			if needsProcessPathDNSRules(rule.LogicalOptions.Rules) {
				return true
			}
		}
	}
	return false
}

func isNeighborRule(rule option.DefaultRule) bool {
	return len(rule.SourceMACAddress) > 0 || len(rule.SourceHostname) > 0
}

func isNeighborDNSRule(rule option.DefaultDNSRule) bool {
	return len(rule.SourceMACAddress) > 0 || len(rule.SourceHostname) > 0
}

func isWIFIRule(rule option.DefaultRule) bool {
	return len(rule.WIFISSID) > 0 || len(rule.WIFIBSSID) > 0
}

func isWIFIDNSRule(rule option.DefaultDNSRule) bool {
	return len(rule.WIFISSID) > 0 || len(rule.WIFIBSSID) > 0
}

func hasLocalNeighborDNSServer(servers []option.DNSServerOptions) bool {
	for _, server := range servers {
		if server.Type != C.DNSTypeLocal {
			continue
		}
		localOptions, isLocal := server.Options.(*option.LocalDNSServerOptions)
		if !isLocal {
			continue
		}
		if len(localOptions.NeighborDomain) > 0 {
			return true
		}
	}
	return false
}
