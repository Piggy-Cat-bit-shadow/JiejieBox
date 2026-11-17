package libbox

// P3 — the classification and revision functions the import flow calls before it decides anything.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type detectReport struct {
	Kind          string `json:"kind"`
	Format        string `json:"format"`
	Revision      string `json:"revision"`
	NodeCount     int    `json:"nodeCount"`
	EndpointCount int    `json:"endpointCount"`
	ConfigShape   *struct {
		Inbounds  int  `json:"inbounds"`
		Outbounds int  `json:"outbounds"`
		Providers int  `json:"providers"`
		HasRoute  bool `json:"hasRoute"`
		HasDNS    bool `json:"hasDNS"`
		NodesOnly bool `json:"nodesOnly"`
	} `json:"configShape"`
	Message string `json:"message"`
}

func detectImport(t *testing.T, content string) detectReport {
	t.Helper()
	box, err := DetectImport(content)
	require.NoError(t, err)
	require.NotNil(t, box)
	var report detectReport
	require.NoError(t, json.Unmarshal([]byte(box.Value), &report), box.Value)
	return report
}

// TestDetectImportSeparatesAProfileFromANodeList is the decision the UI must not guess: a node list
// and a profile are both "some text", and writing one where the other belongs is a user-visible
// mistake (a subscription replaces a profile, or a profile is imported as three nodes).
func TestDetectImportSeparatesAProfileFromANodeList(t *testing.T) {
	profile := `{
      "inbounds": [{"type":"http","tag":"in","listen":"127.0.0.1","listen_port":2080}],
      "outbounds": [{"type":"direct","tag":"direct"}],
      "route": {"final": "direct"}
    }`
	profileReport := detectImport(t, profile)
	require.Equal(t, ImportKindConfig, profileReport.Kind)
	require.NotNil(t, profileReport.ConfigShape)
	require.Equal(t, 1, profileReport.ConfigShape.Inbounds)
	require.True(t, profileReport.ConfigShape.HasRoute)
	require.False(t, profileReport.ConfigShape.NodesOnly, "a document with routing is a profile, not a node list")

	nodesOnly := `{"outbounds":[{"type":"trojan","tag":"a","server":"192.0.2.10","server_port":443,"password":"p"}]}`
	nodesOnlyReport := detectImport(t, nodesOnly)
	require.Equal(t, ImportKindConfig, nodesOnlyReport.Kind, "valid sing-box JSON is a config, the stronger statement")
	require.NotNil(t, nodesOnlyReport.ConfigShape)
	require.True(t, nodesOnlyReport.ConfigShape.NodesOnly, "outbounds with no routing is a node list in config clothing")

	links := detectImport(t, "trojan://p@192.0.2.10:443#A\nvless://11111111-1111-1111-1111-111111111111@192.0.2.11:443?encryption=none#B")
	require.Equal(t, ImportKindNodes, links.Kind)
	require.Equal(t, "links", links.Format)
	require.Equal(t, 2, links.NodeCount)

	clash := detectImport(t, "proxies:\n  - name: c\n    type: ss\n    server: 192.0.2.10\n    port: 8388\n    cipher: aes-128-gcm\n    password: p\n")
	require.Equal(t, ImportKindNodes, clash.Kind)
	require.Equal(t, "clash", clash.Format)
	require.Equal(t, 1, clash.NodeCount)

	html := detectImport(t, "<html>502</html>")
	require.Equal(t, ImportKindUnknown, html.Kind)
	require.NotEmpty(t, html.Message)

	empty := detectImport(t, "  \n\t ")
	require.Equal(t, ImportKindUnknown, empty.Kind)
}

// TestDetectImportRevisionMatchesTheCore pins the one value the host keys a profile by: it must be
// the same identifier AnalyzeConfig and the apply receipt use, or "the file changed" and "the core
// runs a different document" cannot be compared.
func TestDetectImportRevisionMatchesTheCore(t *testing.T) {
	profile := `{"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct"}}`

	report := detectImport(t, profile)
	require.Len(t, report.Revision, 16)
	require.Equal(t, analysisRevision(profile), report.Revision)

	analysis, err := AnalyzeConfig(profile)
	require.NoError(t, err)
	var document struct {
		ConfigRevision string `json:"configRevision"`
	}
	require.NoError(t, json.Unmarshal([]byte(analysis.Value), &document))
	require.Equal(t, document.ConfigRevision, report.Revision, "the two entries must agree on document identity")

	// A document that does not parse still has an identity, and it changes with the bytes.
	broken := detectImport(t, `{"outbounds": [`)
	require.Equal(t, ImportKindUnknown, broken.Kind)
	require.Len(t, broken.Revision, 16)
	require.NotEqual(t, report.Revision, broken.Revision)

	otherBroken := detectImport(t, `{"outbounds": [ `)
	require.NotEqual(t, broken.Revision, otherBroken.Revision, "whitespace is content")
}

// TestConfigRevisionIsAvailableForDocumentsThatDoNotParse is the case AnalyzeConfig cannot serve: a
// profile being edited is often invalid, and the host still has to detect an external edit.
func TestConfigRevisionIsAvailableForDocumentsThatDoNotParse(t *testing.T) {
	box, err := ConfigRevision(`{"this is": not json}`)
	require.NoError(t, err)
	require.NotNil(t, box)
	var decoded struct {
		Revision string `json:"revision"`
	}
	require.NoError(t, json.Unmarshal([]byte(box.Value), &decoded))
	require.Len(t, decoded.Revision, 16)
	require.Equal(t, analysisRevision(`{"this is": not json}`), decoded.Revision)
	require.NotContains(t, strings.ToLower(decoded.Revision), "not json")
}
