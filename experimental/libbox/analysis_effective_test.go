package libbox

// C02 — declared values, the defaults the core applies, and the platform facts a document needs.
//
// The analysis must not answer "unknown" for facts it has, and must not answer "honoured" for facts
// it cannot see. These cases pin the boundary:
//
//   - a value the document leaves unset is reported with the value the core will use and the source
//     "default" (an unset log.level is INFO in this core, and an unset log.output discards);
//   - a value the document sets is reported with source "declared";
//   - a condition whose fact belongs to the PLATFORM (process attribution, Wi-Fi state, network
//     classification) is reported as a requirement, with the handshake that supplies it;
//   - a rule-set reference the document never declares is reported as the start failure it is.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func defaultValue(t *testing.T, analysis ConfigAnalysis, field string) ConfigValue {
	t.Helper()
	for _, value := range analysis.Defaults {
		if value.Field == field {
			return value
		}
	}
	t.Fatalf("the analysis reported no default for %q", field)
	return ConfigValue{}
}

func requiredNeeds(analysis ConfigAnalysis) []string {
	needs := make([]string, 0, len(analysis.Requirements))
	for _, requirement := range analysis.Requirements {
		needs = append(needs, requirement.Need)
	}
	return needs
}

func TestAnalysisReportsTheLogDefaultsItWillUse(t *testing.T) {
	analysis := analyse(t, `{"log": {}}`)
	level := defaultValue(t, analysis, "log.level")
	require.Equal(t, "info", level.Effective, "this core configures info when the level is unset")
	require.Equal(t, configValueDefault, level.Source)
	require.NotEmpty(t, level.Reason)

	output := defaultValue(t, analysis, "log.output")
	require.Equal(t, "discard", output.Effective, "an unset output discards every line")
	require.Equal(t, configValueDefault, output.Source)
}

func TestAnalysisReportsDeclaredValuesAsDeclared(t *testing.T) {
	analysis := analyse(t, `{"log": {"level": "error", "output": "stdout"}}`)
	level := defaultValue(t, analysis, "log.level")
	require.Equal(t, "error", level.Declared)
	require.Equal(t, "error", level.Effective)
	require.Equal(t, configValueDeclared, level.Source)

	output := defaultValue(t, analysis, "log.output")
	require.Equal(t, "stdout", output.Effective)
	require.Equal(t, configValueDeclared, output.Source)

	file := analyse(t, `{"log": {"output": "/tmp/sing-box.log"}}`)
	require.Equal(t, "file:/tmp/sing-box.log", defaultValue(t, file, "log.output").Effective,
		"any output other than stdout is the file path the core opens")
}

func TestAnalysisReportsADisabledLog(t *testing.T) {
	analysis := analyse(t, `{"log": {"disabled": true, "level": "error"}}`)
	level := defaultValue(t, analysis, "log.level")
	require.Equal(t, "disabled", level.Effective, "a disabled log emits nothing, whatever the level says")
	require.Equal(t, configValueDeclared, level.Source)
	require.Equal(t, "discard", defaultValue(t, analysis, "log.output").Effective)
}

func TestAnalysisReportsTheClashAPIDefaults(t *testing.T) {
	analysis := analyse(t, `{"experimental": {"clash_api": {}}}`)
	mode := defaultValue(t, analysis, "clash_api.default_mode")
	require.Equal(t, "Rule", mode.Effective, "the mode manager starts in Rule")
	require.Equal(t, configValueDefault, mode.Source)
	require.Equal(t, "no listener", defaultValue(t, analysis, "clash_api.external_controller").Effective)

	declared := analyse(t, `{"experimental": {"clash_api": {"default_mode": "Global", "external_controller": "127.0.0.1:9090"}}}`)
	modeValue := defaultValue(t, declared, "clash_api.default_mode")
	require.Equal(t, "Global", modeValue.Effective)
	require.Equal(t, configValueDeclared, modeValue.Source)
	require.Equal(t, "127.0.0.1:9090", defaultValue(t, declared, "clash_api.external_controller").Effective)
}

func TestAnalysisNamesTheProcessFactARuleNeeds(t *testing.T) {
	analysis := analyse(t, `{"route": {"rules": [{"process_name": ["curl"], "action": "reject"}]}}`)
	require.Equal(t, []string{platformNeedFindProcess}, requiredNeeds(analysis))
	requirement := analysis.Requirements[0]
	require.Equal(t, "route.rules[0]", requirement.Where)
	require.Contains(t, requirement.Reason, "NeedFindProcess")
	require.False(t, analysis.Checks.RuntimeChecked, "naming the requirement is not observing it")
}

func TestAnalysisNamesTheProcessFactFindProcessNeeds(t *testing.T) {
	analysis := analyse(t, `{"route": {"find_process": true}}`)
	require.Equal(t, []string{platformNeedFindProcess}, requiredNeeds(analysis))
	require.Equal(t, "route.find_process", analysis.Requirements[0].Where)
}

func TestAnalysisNamesTheWIFIFactAWIFIRuleNeeds(t *testing.T) {
	analysis := analyse(t, `{"route": {"rules": [{"wifi_ssid": ["home"], "action": "reject"}]}}`)
	require.Equal(t, []string{platformNeedWIFIState}, requiredNeeds(analysis))
	require.Contains(t, analysis.Requirements[0].Reason, "NeedWIFIState")
}

func TestAnalysisNamesTheNetworkFactANetworkConditionNeeds(t *testing.T) {
	analysis := analyse(t, `{"dns": {"rules": [{"network_type": ["wifi"], "action": "reject"}]}}`)
	require.Equal(t, []string{platformNeedNetworkType}, requiredNeeds(analysis))
	require.Equal(t, "dns.rules[0]", analysis.Requirements[0].Where)
}

func TestAnalysisNamesTheFactANestedRuleNeeds(t *testing.T) {
	analysis := analyse(t, `{"route": {"rules": [
		{"type": "logical", "mode": "and", "rules": [
			{"process_name": ["curl"]},
			{"domain": ["example.com"]}
		], "action": "reject"}
	]}}`)
	require.Equal(t, []string{platformNeedFindProcess}, requiredNeeds(analysis),
		"a nested condition needs the same platform fact as a top-level one")
}

func TestAnalysisListsOneEntryPerPlatformFact(t *testing.T) {
	analysis := analyse(t, `{"route": {"rules": [
		{"process_name": ["curl"], "action": "reject"},
		{"package_name": ["org.example"], "action": "reject"}
	]}}`)
	require.Equal(t, []string{platformNeedFindProcess}, requiredNeeds(analysis),
		"the question is which facts the client must ask for, not how many rules use them")
}

func TestAnalysisInventsNoRequirementForAPlainDocument(t *testing.T) {
	analysis := analyse(t, `{"log": {"level": "error"}, "outbounds": [{"type": "direct", "tag": "direct"}]}`)
	require.Empty(t, analysis.Requirements)
}

func TestAnalysisPredictsARuleSetThatWasNeverDeclared(t *testing.T) {
	analysis := analyse(t, `{"route": {"rules": [{"rule_set": "missing", "action": "reject"}]}}`)
	require.Contains(t, findingCodes(analysis), findingDanglingReference)
	var finding ConfigFinding
	for _, candidate := range analysis.Findings {
		if strings.Contains(candidate.Detail, "rule_set missing") {
			finding = candidate
		}
	}
	require.Equal(t, "route.rules[0] rule_set missing", finding.Detail)
	require.Contains(t, finding.Reason, "rule-set not found: missing",
		"the finding must name the failure the box would produce")
}

func TestAnalysisAcceptsADeclaredRuleSet(t *testing.T) {
	analysis := analyse(t, `{"route": {
		"rule_set": [{"type": "inline", "tag": "declared", "rules": [{"domain": ["example.com"]}]}],
		"rules": [{"rule_set": "declared", "action": "reject"}]
	}}`)
	require.Empty(t, analysis.Findings, "a declared rule-set is a resolvable reference")
}

func TestAnalysisPredictsAMissingDNSRuleSet(t *testing.T) {
	analysis := analyse(t, `{"dns": {"rules": [{"rule_set": "missing-set", "action": "reject"}]}}`)
	require.Contains(t, findingCodes(analysis), findingDanglingReference)
	require.Equal(t, "dns.rules[0] rule_set missing-set", analysis.Findings[0].Detail)
}
