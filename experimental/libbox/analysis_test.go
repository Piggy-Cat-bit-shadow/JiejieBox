package libbox

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAnalyzeConfigReportsDeclaredShape(t *testing.T) {
	config := `{
  "inbounds": [{"type": "socks", "tag": "in", "listen": "127.0.0.1", "listen_port": 1080}],
  "outbounds": [{"type": "direct", "tag": "a"}, {"type": "block", "tag": "b"}],
  "route": {
    "rules": [{"action": "route", "outbound": "a"}],
    "rule_set": [{"type": "local", "tag": "rs", "path": "rules.json"}],
    "find_process": true,
    "auto_detect_interface": true
  },
  "dns": {
    "servers": [{"type": "local", "tag": "local"}],
    "rules": [{"action": "route", "server": "local"}]
  }
}`
	encoded, err := AnalyzeConfig(config)
	require.NoError(t, err)
	require.NotNil(t, encoded)

	var analysis ConfigAnalysis
	require.NoError(t, json.Unmarshal([]byte(encoded.Value), &analysis))
	require.True(t, analysis.Parsed)
	require.NotEmpty(t, analysis.Platform)
	require.Equal(t, 1, analysis.Inbounds)
	require.Equal(t, 2, analysis.Outbounds)
	require.Equal(t, 1, analysis.RouteRules)
	require.Equal(t, 1, analysis.RuleSets)
	require.Equal(t, 1, analysis.DNSRules)
	require.Equal(t, 1, analysis.DNSServers)
	require.True(t, analysis.FindProcess)
	require.True(t, analysis.AutoDetectInterface)
}

func TestAnalyzeConfigRejectsMalformedDocument(t *testing.T) {
	_, err := AnalyzeConfig(`{"outbounds": [`)
	require.Error(t, err)
}

// Analysis is parse-only: a document whose references the full constructor rejects must still be
// classifiable, because classification is exactly what a client wants before it tries to run it.
func TestAnalyzeConfigDoesNotConstructTheBox(t *testing.T) {
	config := `{"endpoints": [{"type": "wireguard", "tag": "wg", "address": ["10.0.0.1/24"]}]}`
	encoded, err := AnalyzeConfig(config)
	require.NoError(t, err, "the lightweight path must not reject a document the constructor would")
	require.Contains(t, encoded.Value, `"parsed":true`)
	require.Error(t, CheckConfig(config),
		"the full check must still reject the dangling reference; if it ever accepts it, this test no longer proves the split")
}
