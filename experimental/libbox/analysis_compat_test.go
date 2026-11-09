package libbox

// C02 — the analysis report is additive, so a client built against the previous field set keeps
// working.
//
// The report crosses the bridge as JSON behind a StringBox. A client that was built before
// Defaults/Requirements/Reason existed decodes the same bytes into its own struct and reads the
// same answers it read before; the new keys are simply not asked for. That is the compatibility
// promise, and this test is what holds it: it decodes the current report with the OLD shape.

import (
	stdjson "encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// legacyConfigAnalysis is the field set the report had before Defaults, Requirements and
// ConfigFinding.Reason were added.
type legacyConfigAnalysis struct {
	Parsed         bool   `json:"parsed"`
	Platform       string `json:"platform"`
	BuildQUIC      bool   `json:"buildQuic"`
	BuildID        string `json:"buildId"`
	ConfigRevision string `json:"configRevision"`
	Schema         string `json:"schema"`
	Inbounds       int    `json:"inbounds"`
	Outbounds      int    `json:"outbounds"`
	RouteRules     int    `json:"routeRules"`
	RuleSets       int    `json:"ruleSets"`
	DNSRules       int    `json:"dnsRules"`
	DNSServers     int    `json:"dnsServers"`
	FindProcess    bool   `json:"findProcess"`
	Checks         struct {
		Parsed              bool `json:"parsed"`
		DeclarationsChecked bool `json:"declarationsChecked"`
		RuntimeChecked      bool `json:"runtimeChecked"`
	} `json:"checks"`
	Findings []struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
	} `json:"findings"`
}

func TestAnOlderClientStillDecodesTheAnalysis(t *testing.T) {
	const config = `{
	  "log": {"level": "error"},
	  "route": {"rules": [{"process_name": ["curl"], "action": "reject"}]}
	}`
	boxed, err := AnalyzeConfig(config)
	require.NoError(t, err)

	// The report carries the new keys: the additive part is real, not implied.
	require.Contains(t, boxed.Value, `"requirements"`)
	require.Contains(t, boxed.Value, `"defaults"`)

	var legacy legacyConfigAnalysis
	require.NoError(t, stdjson.Unmarshal([]byte(boxed.Value), &legacy),
		"a client that does not know the new keys must still decode the report")

	require.True(t, legacy.Parsed)
	require.NotEmpty(t, legacy.Platform)
	require.NotEmpty(t, legacy.BuildID)
	require.NotEmpty(t, legacy.ConfigRevision)
	require.True(t, legacy.Checks.Parsed)
	require.True(t, legacy.Checks.DeclarationsChecked)
	require.False(t, legacy.Checks.RuntimeChecked, "the old field still answers the same thing")
	require.Equal(t, 1, legacy.RouteRules)
	require.Empty(t, legacy.Findings, "a document with resolvable references has no finding, old shape or new")
}
