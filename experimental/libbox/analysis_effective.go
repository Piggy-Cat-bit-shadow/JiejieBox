package libbox

// C02 — the values the core will actually use, and the platform facts a document depends on.
//
// AnalyzeConfig reports what a document DECLARES. Two things are missing from that alone, and this
// file adds them without building a Box:
//
//   - Defaults: for the load-bearing fields a document can leave unset, the value the core applies
//     and where it came from (declared or default). They are not decoration. An unset log.level is
//     TRACE in this core, not info, and an unset log.output discards every line - facts a caller
//     otherwise learns from silence.
//   - Requirements: the facts the PLATFORM must supply before a declared condition can ever match.
//     Process attribution, Wi-Fi state and network classification are not core facts: the core asks
//     the platform for them (CommandServer.NeedFindProcess, CommandServer.NeedWIFIState), so a
//     document that needs them is reported as PENDING on the platform side and never as honoured.
//
// Neither list claims a runtime observation. Checks.RuntimeChecked stays false, exactly as before.

import (
	"strconv"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
)

// ConfigValue is one value the core will use, with the declaration it came from.
type ConfigValue struct {
	Field string `json:"field"`
	// Declared is what the document said, when it said anything.
	Declared string `json:"declared,omitempty"`
	// Effective is the value the core will use.
	Effective string `json:"effective"`
	// Source is "declared" when the document set it, "default" when the core did.
	Source string `json:"source"`
	Reason string `json:"reason,omitempty"`
}

// ConfigRequirement is a fact the platform must supply for a declared condition to work.
type ConfigRequirement struct {
	Need  string `json:"need"`
	Where string `json:"where"`
	// Reason says which platform handshake provides it, so a client knows what to ask for.
	Reason string `json:"reason"`
}

const (
	configValueDeclared = "declared"
	configValueDefault  = "default"

	platformNeedFindProcess = "find_process"
	platformNeedWIFIState   = "wifi_state"
	platformNeedNetworkType = "network_type"
)

const (
	findProcessReason = "the platform supplies connection ownership (CommandServer.NeedFindProcess); " +
		"without it a process condition never matches"
	wifiStateReason = "the platform supplies Wi-Fi state (CommandServer.NeedWIFIState); " +
		"without it a Wi-Fi condition never matches"
	networkTypeReason = "the platform classifies the network (type, metered, constrained); " +
		"without it a network condition never matches"
)

// analyseEffective reports the defaults the core applies and the platform facts the document needs.
func analyseEffective(options *option.Options) (defaults []ConfigValue, requirements []ConfigRequirement) {
	defaults = append(defaults, logDefaults(options)...)
	defaults = append(defaults, clashAPIDefaults(options)...)
	return defaults, ruleRequirements(options)
}

// logDefaults reports the effective log configuration.
func logDefaults(options *option.Options) []ConfigValue {
	var disabled bool
	var declaredLevel, declaredOutput string
	if options.Log != nil {
		disabled = options.Log.Disabled
		declaredLevel = options.Log.Level
		declaredOutput = options.Log.Output
	}
	if disabled {
		return []ConfigValue{
			{
				Field: "log.level", Declared: "disabled", Effective: "disabled", Source: configValueDeclared,
				Reason: "log.disabled is set: the core emits no log line at all",
			},
			{
				Field: "log.output", Declared: declaredOutput, Effective: "discard", Source: configValueDeclared,
				Reason: "log.disabled is set: nothing is written",
			},
		}
	}
	level := ConfigValue{Field: "log.level", Declared: declaredLevel, Effective: declaredLevel, Source: configValueDeclared}
	if declaredLevel == "" {
		level.Effective = "trace"
		level.Source = configValueDefault
		level.Reason = "level is unset: this core configures log/trace"
	}
	output := ConfigValue{Field: "log.output", Declared: declaredOutput, Effective: declaredOutput, Source: configValueDeclared}
	switch declaredOutput {
	case "":
		output.Effective = "discard"
		output.Source = configValueDefault
		output.Reason = "output is unset: every line is discarded"
	case "stdout":
		output.Effective = "stdout"
	default:
		// Any other value is a file path, which is what the core opens.
		output.Effective = "file:" + declaredOutput
	}
	return []ConfigValue{level, output}
}

// clashAPIDefaults reports the effective Clash API configuration, when the document configures one.
func clashAPIDefaults(options *option.Options) []ConfigValue {
	if options.Experimental == nil || options.Experimental.ClashAPI == nil {
		return nil
	}
	clashAPI := options.Experimental.ClashAPI
	mode := ConfigValue{
		Field: "clash_api.default_mode", Declared: clashAPI.DefaultMode,
		Effective: clashAPI.DefaultMode, Source: configValueDeclared,
	}
	if clashAPI.DefaultMode == "" {
		mode.Effective = "Rule"
		mode.Source = configValueDefault
		mode.Reason = "default_mode is unset: the mode manager starts in Rule"
	}
	controller := ConfigValue{
		Field: "clash_api.external_controller", Declared: clashAPI.ExternalController,
		Effective: clashAPI.ExternalController, Source: configValueDeclared,
	}
	if clashAPI.ExternalController == "" {
		controller.Effective = "no listener"
		controller.Source = configValueDefault
		controller.Reason = "external_controller is unset: the Clash API serves no HTTP endpoint"
	}
	return []ConfigValue{mode, controller}
}

// ruleNeeds are the platform-supplied facts one rule's conditions depend on.
type ruleNeeds struct {
	findProcess bool
	wifiState   bool
	networkType bool
}

// needsOfRouteRule folds one routing rule's conditions, nested rules included.
func needsOfRouteRule(rule option.Rule) ruleNeeds {
	switch rule.Type {
	case "", C.RuleTypeDefault:
		return needsOfRawRule(rule.DefaultOptions.RawDefaultRule)
	case C.RuleTypeLogical:
		var needs ruleNeeds
		for _, child := range rule.LogicalOptions.Rules {
			needs = needs.or(needsOfRouteRule(child))
		}
		return needs
	default:
		return ruleNeeds{}
	}
}

// needsOfDNSRule folds one DNS rule's conditions, nested rules included.
func needsOfDNSRule(rule option.DNSRule) ruleNeeds {
	switch rule.Type {
	case "", C.RuleTypeDefault:
		return needsOfRawDNSRule(rule.DefaultOptions.RawDefaultDNSRule)
	case C.RuleTypeLogical:
		var needs ruleNeeds
		for _, child := range rule.LogicalOptions.Rules {
			needs = needs.or(needsOfDNSRule(child))
		}
		return needs
	default:
		return ruleNeeds{}
	}
}

func needsOfRawRule(rule option.RawDefaultRule) ruleNeeds {
	return ruleNeeds{
		findProcess: len(rule.ProcessName) > 0 || len(rule.ProcessPath) > 0 || len(rule.ProcessPathRegex) > 0 ||
			len(rule.PackageName) > 0 || len(rule.PackageNameRegex) > 0 ||
			len(rule.User) > 0 || len(rule.UserID) > 0 || len(rule.PreferredBy) > 0,
		wifiState:   len(rule.WIFISSID) > 0 || len(rule.WIFIBSSID) > 0,
		networkType: len(rule.NetworkType) > 0 || rule.NetworkIsExpensive || rule.NetworkIsConstrained,
	}
}

func needsOfRawDNSRule(rule option.RawDefaultDNSRule) ruleNeeds {
	return ruleNeeds{
		findProcess: len(rule.ProcessName) > 0 || len(rule.ProcessPath) > 0 || len(rule.ProcessPathRegex) > 0 ||
			len(rule.PackageName) > 0 || len(rule.PackageNameRegex) > 0 ||
			len(rule.User) > 0 || len(rule.UserID) > 0 || len(rule.PreferredBy) > 0,
		wifiState:   len(rule.WIFISSID) > 0 || len(rule.WIFIBSSID) > 0,
		networkType: len(rule.NetworkType) > 0 || rule.NetworkIsExpensive || rule.NetworkIsConstrained,
	}
}

func (n ruleNeeds) or(other ruleNeeds) ruleNeeds {
	return ruleNeeds{
		findProcess: n.findProcess || other.findProcess,
		wifiState:   n.wifiState || other.wifiState,
		networkType: n.networkType || other.networkType,
	}
}

// requirementsOf turns one rule's needs into requirement entries.
func (n ruleNeeds) requirementsOf(where string) []ConfigRequirement {
	var requirements []ConfigRequirement
	if n.findProcess {
		requirements = append(requirements, ConfigRequirement{Need: platformNeedFindProcess, Where: where, Reason: findProcessReason})
	}
	if n.wifiState {
		requirements = append(requirements, ConfigRequirement{Need: platformNeedWIFIState, Where: where, Reason: wifiStateReason})
	}
	if n.networkType {
		requirements = append(requirements, ConfigRequirement{Need: platformNeedNetworkType, Where: where, Reason: networkTypeReason})
	}
	return requirements
}

// ruleRequirements lists the platform facts the document's own conditions need.
//
// One entry per fact, naming the first condition that needs it: the list answers "must the client
// ask the platform for this", and repeating the same answer per rule would only make it longer.
func ruleRequirements(options *option.Options) []ConfigRequirement {
	seen := make(map[string]bool)
	var requirements []ConfigRequirement
	add := func(candidate []ConfigRequirement) {
		for _, requirement := range candidate {
			if seen[requirement.Need] {
				continue
			}
			seen[requirement.Need] = true
			requirements = append(requirements, requirement)
		}
	}
	if options.Route != nil {
		if options.Route.FindProcess {
			add([]ConfigRequirement{{Need: platformNeedFindProcess, Where: "route.find_process", Reason: findProcessReason}})
		}
		for index, rule := range options.Route.Rules {
			add(needsOfRouteRule(rule).requirementsOf("route.rules[" + strconv.Itoa(index) + "]"))
		}
	}
	if options.DNS != nil {
		for index, rule := range options.DNS.Rules {
			add(needsOfDNSRule(rule).requirementsOf("dns.rules[" + strconv.Itoa(index) + "]"))
		}
	}
	return requirements
}
