package parser_test

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/provider/parser"

	"github.com/stretchr/testify/require"
)

const previewGoodLinks = "trojan://password@192.0.2.10:443?sni=example.com#A\nvless://11111111-1111-1111-1111-111111111111@192.0.2.11:443?encryption=none#B"

// previewContext is the context a CALLER must supply: the sing-box reader decodes through the
// option registries, which it takes from the context. A registry-less context does not fail
// loudly - it makes that one format unrecognisable - which is the same behaviour ParseSubscription
// has, and the reason this helper exists instead of context.Background() at every call site.
func previewContext() context.Context {
	return include.Context(context.Background())
}

func TestPreviewDetectsEachFormat(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name        string
		content     string
		format      string
		nodes       int
		endpoints   int
		base64      bool
		recognised  bool
		skippedLine int
	}{
		{
			name:       "link list",
			content:    previewGoodLinks,
			format:     parser.PreviewFormatLinks,
			nodes:      2,
			recognised: true,
		},
		{
			name:       "clash",
			content:    "proxies:\n  - name: clash-ss\n    type: ss\n    server: 192.0.2.10\n    port: 8388\n    cipher: aes-128-gcm\n    password: secret\n",
			format:     parser.PreviewFormatClash,
			nodes:      1,
			recognised: true,
		},
		{
			name:       "sip008",
			content:    `{"version":1,"servers":[{"remarks":"sip-ss","server":"192.0.2.10","server_port":8388,"password":"secret","method":"aes-128-gcm"}]}`,
			format:     parser.PreviewFormatSIP008,
			nodes:      1,
			recognised: true,
		},
		{
			name:       "sing-box",
			content:    `{"outbounds":[{"type":"trojan","tag":"box-trojan","server":"192.0.2.10","server_port":443,"password":"secret"}]}`,
			format:     parser.PreviewFormatSingBox,
			nodes:      1,
			recognised: true,
		},
		{
			name:       "base64 link list",
			content:    base64.StdEncoding.EncodeToString([]byte(previewGoodLinks)),
			format:     parser.PreviewFormatLinks,
			nodes:      2,
			base64:     true,
			recognised: true,
		},
		{
			// The clash reader is the only path that yields an endpoint today: the sing-box
			// reader returns before decoding `endpoints` when the document has no `outbounds`
			// key (sing_box.go:25-29). See TestPreviewSingBoxEndpointOnlyIsDroppedByTheReader.
			name:       "clash wireguard endpoint",
			content:    "proxies:\n  - name: wg-node\n    type: wireguard\n    server: 192.0.2.10\n    port: 51820\n    ip: 10.0.0.2\n    private-key: AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n    public-key: AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n    udp: true\n",
			format:     parser.PreviewFormatClash,
			nodes:      0,
			endpoints:  1,
			recognised: true,
		},
		{
			name:        "partial link list",
			content:     previewGoodLinks + "\nssr://MTkyLjAuMi4xMDo4Mzg4",
			format:      parser.PreviewFormatLinks,
			nodes:       2,
			recognised:  true,
			skippedLine: 3,
		},
		{
			name:       "html error page",
			content:    "<!DOCTYPE html><html><body>502 Bad Gateway</body></html>",
			format:     "",
			recognised: false,
		},
		{
			name:       "empty",
			content:    "",
			format:     "",
			recognised: false,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			preview := parser.ParseSubscriptionPreview(previewContext(), testCase.content)
			require.Equal(t, testCase.format, preview.Format)
			require.Len(t, preview.Outbounds, testCase.nodes)
			require.Len(t, preview.Endpoints, testCase.endpoints)
			require.Equal(t, testCase.base64, preview.Base64)
			require.Equal(t, testCase.recognised, preview.Recognised())
			if testCase.skippedLine != 0 {
				require.Len(t, preview.Skipped, 1)
				require.Equal(t, testCase.skippedLine, preview.Skipped[0].Line)
				require.Contains(t, preview.Skipped[0].Reason, "unsupported scheme")
			}
		})
	}
}

// TestPreviewCountsWhatTheReaderAccepts pins the preview against the reader it previews: where
// ParseRawSubscription succeeds, the preview must report the same number of nodes, or the two
// have drifted apart.
func TestPreviewCountsWhatTheReaderAccepts(t *testing.T) {
	t.Parallel()
	fixtures := []string{
		previewGoodLinks,
		previewGoodLinks + "\nnot-a-link",
		"trojan://password@192.0.2.10:443#Only",
		base64.StdEncoding.EncodeToString([]byte(previewGoodLinks)),
	}
	for _, fixture := range fixtures {
		outbounds, _, err := parser.ParseRawSubscription(context.Background(), fixture)
		if err != nil {
			continue
		}
		preview := parser.ParseSubscriptionPreview(previewContext(), fixture)
		require.Equal(t, parser.PreviewFormatLinks, preview.Format)
		require.Len(t, preview.Outbounds, len(outbounds))
	}
}

// TestPreviewDeclaredCountsNonEmptyLines documents what `Declared` means: whitespace-only lines
// declare nothing and are not counted, and they are not reported as skips either.
func TestPreviewDeclaredCountsNonEmptyLines(t *testing.T) {
	t.Parallel()
	preview := parser.ParseSubscriptionPreview(previewContext(), "\n\n"+previewGoodLinks+"\n   \n")
	require.Equal(t, 2, preview.Declared)
	require.Len(t, preview.Outbounds, 2)
	require.Empty(t, preview.Skipped)
}

// TestPreviewKeepsReaderDiagnostics proves a refusal still explains itself, which is the only way
// a UI can distinguish "an HTML error page" from "a document in an unsupported format".
func TestPreviewKeepsReaderDiagnostics(t *testing.T) {
	t.Parallel()
	preview := parser.ParseSubscriptionPreview(previewContext(), "<html>502</html>")
	require.False(t, preview.Recognised())
	require.NotEmpty(t, preview.Diagnostics)
	require.Contains(t, strings.Join(diagnosticValues(preview.Diagnostics), "\n"), "<html>")
}

func diagnosticValues(diagnostics map[string]string) []string {
	values := make([]string, 0, len(diagnostics))
	for _, value := range diagnostics {
		values = append(values, value)
	}
	return values
}

// TestPreviewSingBoxEndpointOnlyIsDroppedByTheReader pins a gap in the reader this preview
// reports, rather than papering over it: SingBoxDocument.UnmarshalJSONContext returns as soon as
// the document has no `outbounds` key, so `endpoints` is never decoded and an endpoint-only
// document is reported as unrecognised. When that reader is fixed, this test fails and the
// preview starts reporting sing-box endpoints - which is the intended direction.
func TestPreviewSingBoxEndpointOnlyIsDroppedByTheReader(t *testing.T) {
	t.Parallel()
	document := `{"endpoints":[{"type":"wireguard","tag":"wg","address":["10.0.0.2/32"],"private_key":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=","peers":[{"address":"192.0.2.10","port":51820,"public_key":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}]}]}`
	outbounds, endpoints, err := parser.ParseBoxSubscription(previewContext(), document)
	require.Error(t, err, "the reader refuses the document today")
	require.Empty(t, outbounds)
	require.Empty(t, endpoints)

	preview := parser.ParseSubscriptionPreview(previewContext(), document)
	require.False(t, preview.Recognised())
	require.Empty(t, preview.Format)
}
