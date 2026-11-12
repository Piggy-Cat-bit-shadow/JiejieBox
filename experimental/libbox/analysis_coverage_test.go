package libbox

// Q21 — the coverage account of AnalyzeConfig, pinned as a test instead of prose.
//
// The audit's condition is not "report more fields". It is that the report says which fields it
// projects, where each value comes from, and what stays pending or unknown - and that the answer is
// reusable by a client without building a Box. Three properties keep that honest as the document
// grows:
//
//   - the JSON key set cannot change silently (a new field is a contract change, and this test is
//     where the contract is stated);
//   - every reported value carries its SOURCE, and a default carries its reason;
//   - nothing in the report claims a runtime fact. The platform handshakes stay pending, and a
//     remote provider stays pending rather than being reported as a dangling reference.
//
// What is deliberately NOT asserted here: that the projection covers every field of the option
// schema. It does not, and the account in evidence/q21_config_analysis_coverage.txt lists what is
// missing and why. A test that pretended otherwise would be the "abstract account nobody calls"
// this round exists to avoid.

import (
	stdjson "encoding/json"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

// coverageConfig populates every key of the report, including the omitempty ones.
const coverageConfig = `{
  "$schema": "https://sing-box.sagernet.org/schema.json",
  "log": {"level": "error"},
  "inbounds": [{"type": "socks", "tag": "in", "listen": "127.0.0.1", "listen_port": 1080}],
  "outbounds": [
    {"type": "direct", "tag": "a"},
    {"type": "selector", "tag": "group", "outbounds": ["a", "ghost"]}
  ],
  "providers": [{"type": "remote", "tag": "airport", "url": "https://example.org/sub"}],
  "route": {
    "find_process": true,
    "rules": [{"process_name": ["curl"], "action": "route", "outbound": "a"}]
  }
}`

// TestConfigAnalysisDocumentsExactlyTheProjectedKeys is the field list itself.
func TestConfigAnalysisDocumentsExactlyTheProjectedKeys(t *testing.T) {
	boxed, err := AnalyzeConfig(coverageConfig)
	require.NoError(t, err)

	var document map[string]stdjson.RawMessage
	require.NoError(t, stdjson.Unmarshal([]byte(boxed.Value), &document))

	actual := make([]string, 0, len(document))
	for key := range document {
		actual = append(actual, key)
	}
	sort.Strings(actual)
	expected := []string{
		// always present
		"autoDetectInterface", "buildId", "buildQuic", "capabilities", "checks", "configRevision",
		"dnsRules", "dnsServers", "endpoints", "findProcess", "inbounds", "objects", "outbounds",
		"parsed", "platform", "providers", "routeRules", "ruleSets", "schema", "services",
		// present only when the document populates them
		"defaults", "findings", "pendingProviders", "references", "requirements",
	}
	sort.Strings(expected)
	require.Equal(t, expected, actual,
		"the report's key set is its contract; adding or removing a key must update this list and "+
			"the coverage account together")
}

// TestConfigAnalysisDefaultsCarryTheirSource: a default is only useful when it says it IS one and
// why the core applies it.
func TestConfigAnalysisDefaultsCarryTheirSource(t *testing.T) {
	boxed, err := AnalyzeConfig(coverageConfig)
	require.NoError(t, err)

	var analysis ConfigAnalysis
	require.NoError(t, stdjson.Unmarshal([]byte(boxed.Value), &analysis))
	require.NotEmpty(t, analysis.Defaults, "log defaults are reported for every document")

	for _, value := range analysis.Defaults {
		require.NotEmpty(t, value.Field)
		require.NotEmpty(t, value.Effective)
		require.Contains(t, []string{configValueDeclared, configValueDefault}, value.Source,
			"every reported value must say whether the document declared it or the core defaulted it")
		if value.Source == configValueDefault {
			require.NotEmpty(t, value.Reason,
				"a default is a claim about the core; it must say which rule produced it")
		}
	}
}

// TestConfigAnalysisKeepsRuntimeFactsPending: the report must not promote a platform handshake or a
// fetch into an observed fact.
func TestConfigAnalysisKeepsRuntimeFactsPending(t *testing.T) {
	boxed, err := AnalyzeConfig(coverageConfig)
	require.NoError(t, err)

	var analysis ConfigAnalysis
	require.NoError(t, stdjson.Unmarshal([]byte(boxed.Value), &analysis))

	require.True(t, analysis.Checks.Parsed)
	require.True(t, analysis.Checks.DeclarationsChecked)
	require.False(t, analysis.Checks.RuntimeChecked,
		"this path never builds a box, so a runtime claim here would be false by construction")

	require.Len(t, analysis.PendingProviders, 1)
	require.Equal(t, "airport", analysis.PendingProviders[0].Tag)

	var needs []string
	for _, requirement := range analysis.Requirements {
		needs = append(needs, requirement.Need)
		require.NotEmpty(t, requirement.Where, "a requirement must name what needs it")
		require.NotEmpty(t, requirement.Reason, "a requirement must say which handshake provides it")
	}
	require.Contains(t, needs, platformNeedFindProcess,
		"a process condition depends on a platform handshake, which the report keeps pending")

	// The dangling member is a document fact and is reported as one; the remote provider is NOT
	// reported as dangling even though its members do not exist in the document.
	var details []string
	for _, finding := range analysis.Findings {
		details = append(details, finding.Detail)
	}
	require.Contains(t, details, "group member ghost")
	for _, detail := range details {
		require.NotContains(t, detail, "airport",
			"a remote provider's nodes are pending, not missing")
	}
}
