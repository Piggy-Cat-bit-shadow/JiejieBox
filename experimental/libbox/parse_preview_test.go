package libbox

import (
	"encoding/json"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

const previewLinks = "trojan://password@192.0.2.10:443?sni=example.com#A\nvless://11111111-1111-1111-1111-111111111111@192.0.2.11:443?encryption=none#B"

func decodeParsePreview(t *testing.T, content string) map[string]any {
	t.Helper()
	box, err := ParsePreview(content)
	require.NoError(t, err, "a recognised or unrecognised document is a result, not a Go error")
	require.NotNil(t, box)
	require.True(t, utf8.ValidString(box.Value), "the bridge must never return invalid UTF-8")
	var report map[string]any
	require.NoError(t, json.Unmarshal([]byte(box.Value), &report))
	return report
}

func TestParsePreviewReportsLinks(t *testing.T) {
	report := decodeParsePreview(t, previewLinks)
	require.Equal(t, true, report["ok"])
	require.Equal(t, "links", report["format"])
	require.EqualValues(t, 2, report["count"])

	items, isList := report["items"].([]any)
	require.True(t, isList)
	require.Len(t, items, 2)

	first, isItem := items[0].(map[string]any)
	require.True(t, isItem)
	require.Equal(t, "A", first["tag"])
	require.Equal(t, "trojan", first["type"])
	require.Equal(t, "192.0.2.10", first["server"])
	require.EqualValues(t, 443, first["serverPort"])

	// Content must be the complete node document: that is what a client writes into its profile.
	var node map[string]any
	require.NoError(t, json.Unmarshal([]byte(first["content"].(string)), &node))
	require.Equal(t, "trojan", node["type"])
	require.Equal(t, "A", node["tag"])
}

func TestParsePreviewReportsSkippedLines(t *testing.T) {
	report := decodeParsePreview(t, previewLinks+"\nssr://MTkyLjAuMi4xMDo4Mzg4")
	require.Equal(t, true, report["ok"])
	require.EqualValues(t, 2, report["count"])
	require.EqualValues(t, 3, report["declared"])

	skipped, isList := report["skipped"].([]any)
	require.True(t, isList)
	require.Len(t, skipped, 1)
	entry, isEntry := skipped[0].(map[string]any)
	require.True(t, isEntry)
	require.EqualValues(t, 3, entry["line"])
	require.Contains(t, entry["reason"], "unsupported scheme")
}

func TestParsePreviewMarksEndpoints(t *testing.T) {
	// The clash reader is the only path that yields an endpoint today; the sing-box reader
	// returns before decoding `endpoints` when the document has no `outbounds` key.
	clashWireGuard := "proxies:\n" +
		"  - name: wg-node\n" +
		"    type: wireguard\n" +
		"    server: 192.0.2.10\n" +
		"    port: 51820\n" +
		"    ip: 10.0.0.2\n" +
		"    private-key: AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n" +
		"    public-key: AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n" +
		"    udp: true\n"
	report := decodeParsePreview(t, clashWireGuard)
	require.Equal(t, true, report["ok"])
	require.EqualValues(t, 0, report["count"])
	require.EqualValues(t, 1, report["endpointCount"])

	items := report["items"].([]any)
	require.Len(t, items, 1)
	item := items[0].(map[string]any)
	require.Equal(t, true, item["endpoint"])
	require.Equal(t, "wireguard", item["type"])
}

// TestParsePreviewUnrecognisedIsAResult pins the contract this entry exists for: an import form
// asks "is this a subscription", and "no" must be a value with a stable code - not a Go error that
// forces every caller to match on reader text.
func TestParsePreviewUnrecognisedIsAResult(t *testing.T) {
	report := decodeParsePreview(t, "<!DOCTYPE html><html><body>502 Bad Gateway</body></html>")
	require.Equal(t, false, report["ok"])
	require.EqualValues(t, 0, report["count"])

	failure, isFailure := report["error"].(map[string]any)
	require.True(t, isFailure)
	require.Equal(t, ParsePreviewCodeNotRecognized, failure["code"])
	require.NotEmpty(t, failure["message"])
	require.NotEmpty(t, report["diagnostics"])
}

func TestParsePreviewEmptyInputIsItsOwnCode(t *testing.T) {
	report := decodeParsePreview(t, "   \n\t ")
	require.Equal(t, false, report["ok"])
	failure, isFailure := report["error"].(map[string]any)
	require.True(t, isFailure)
	require.Equal(t, ParsePreviewCodeEmpty, failure["code"])
}

func TestParsePreviewInvalidUTF8StaysInsideTheBridge(t *testing.T) {
	// Mirrors bridge_boundary_test.go: the argument crosses the binding, so invalid UTF-8 must
	// not panic and must not come back out.
	report := decodeParsePreview(t, "trojan://password@192.0.2.10:443?sni=example.com#bad-\xff")
	require.NotNil(t, report)
}
