package libbox

// P1 — the compatibility matrix.
//
// # What this file is
//
// Every load-bearing claim from the P0 inventory, expressed as an executable assertion and
// emitted as one machine-readable row. The point is that a reviewer can diff two runs instead of
// re-reading prose:
//
//	run A: baseline commit, go1.25.5   (the go directive's floor)
//	run B: baseline commit, go1.27.4   (the toolchain the release builds use)
//	run C: feature branch, go1.27.4    (proves the additive patch changed nothing existing)
//
// A row that flips `ok`, or changes its `count`, between A and B is a Go-version behaviour
// change and is reported as one. A row that differs between B and C is a regression introduced
// by the patch.
//
// # What it deliberately does NOT do
//
// No network. Every fixture is offline: no remote provider, no subscription URL, no DNS server
// that would be dialled. `CheckConfig` builds and closes a Box, so a fixture that carried a
// remote provider would fetch it; none does.
//
// # How to run it at the baseline
//
// The file is baseline-compatible on purpose (it uses no symbol added by the patch), so it can be
// injected into the pristine clone with `go test -overlay` exactly like the P0 probe.

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/provider/parser"
)

const compatMinimalConfig = `{"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct"}}`

const compatGroupedConfig = `{
  "outbounds": [
    {"type":"direct","tag":"direct"},
    {"type":"trojan","tag":"node-a","server":"192.0.2.10","server_port":443,"password":"p"},
    {"type":"selector","tag":"proxy","outbounds":["node-a","direct"]},
    {"type":"urltest","tag":"auto","outbounds":["node-a"]}
  ],
  "route": {"final": "proxy"}
}`

const compatDuplicateConfig = `{
  "outbounds": [
    {"type":"trojan","tag":"node-a","server":"192.0.2.10","server_port":443,"password":"p"},
    {"type":"trojan","tag":"node-a","server":"192.0.2.11","server_port":443,"password":"p"}
  ],
  "route": {"final": "node-a"}
}`

const compatCleanAnalysisConfig = `{
  "outbounds": [
    {"type":"direct","tag":"direct"},
    {"type":"trojan","tag":"node-a","server":"192.0.2.10","server_port":443,"password":"p","detour":"node-b"},
    {"type":"trojan","tag":"node-b","server":"192.0.2.11","server_port":443,"password":"p"},
    {"type":"selector","tag":"proxy","outbounds":["node-a","node-b"]},
    {"type":"urltest","tag":"auto","outbounds":["node-a"],"providers":["sub"],"use_all_providers":true}
  ],
  "providers": [{"type":"remote","tag":"sub","url":"https://example.com/sub","path":"sub.json"}],
  "route": {"final": "proxy", "rules": [{"action":"route","outbound":"does-not-exist"}]}
}`

const compatLinks = "trojan://password@192.0.2.10:443?sni=example.com#A\n" +
	"vless://11111111-1111-1111-1111-111111111111@192.0.2.11:443?encryption=none#B"

const compatClash = "proxies:\n  - name: clash-ss\n    type: ss\n    server: 192.0.2.10\n    port: 8388\n    cipher: aes-128-gcm\n    password: secret\n"

const compatSIP008 = `{"version":1,"servers":[{"remarks":"sip-ss","server":"192.0.2.10","server_port":8388,"password":"secret","method":"aes-128-gcm"}]}`

const compatBoxJSON = `{"outbounds":[{"type":"direct","tag":"direct"},{"type":"selector","tag":"proxy","outbounds":["direct"]},{"type":"trojan","tag":"t","server":"192.0.2.10","server_port":443,"password":"p"}]}`

const compatEndpointOnlyJSON = `{"endpoints":[{"type":"wireguard","tag":"wg","address":["10.0.0.2/32"],"private_key":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=","peers":[{"address":"192.0.2.10","port":51820,"public_key":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}]}]}`

const compatBothArraysJSON = `{"outbounds":[{"type":"trojan","tag":"t","server":"192.0.2.10","server_port":443,"password":"p"}],"endpoints":[{"type":"wireguard","tag":"wg","address":["10.0.0.2/32"],"private_key":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=","peers":[{"address":"192.0.2.10","port":51820,"public_key":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}]}]}`

// compatRow is one assertion's outcome. Field names are stable: the diff tool reads them.
type compatRow struct {
	ID    string         `json:"id"`
	Kind  string         `json:"kind"`
	OK    bool           `json:"ok"`
	Error string         `json:"error,omitempty"`
	Count int            `json:"count,omitempty"`
	Extra map[string]any `json:"extra,omitempty"`
}

func compatRowsToJSON(t *testing.T, rows []compatRow) []byte {
	t.Helper()
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	encoded, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		t.Fatalf("encode compat rows: %v", err)
	}
	if target := os.Getenv("JJ_COMPAT_OUT"); target != "" {
		if writeErr := os.WriteFile(target, encoded, 0o644); writeErr != nil {
			t.Fatalf("write compat rows: %v", writeErr)
		}
	}
	t.Logf("COMPAT_ROWS=%d", len(rows))
	return encoded
}

// TestFlutterCompatBaselineMatrix covers every claim that exists at the pinned baseline.
func TestFlutterCompatBaselineMatrix(t *testing.T) {
	ctx := include.Context(context.Background())
	rows := make([]compatRow, 0, 64)

	// ---- CheckConfig -------------------------------------------------------
	checkCases := []struct {
		id    string
		input string
		// expectError is a substring the error must contain; empty means "must succeed".
		expectError string
	}{
		{"cc.minimal_valid", compatMinimalConfig, ""},
		{"cc.grouped_valid", compatGroupedConfig, ""},
		{"cc.duplicate_tag", compatDuplicateConfig, "duplicate outbound/endpoint tag"},
		{"cc.unknown_field", `{"outbounds":[{"type":"trojan","tag":"t","server":"192.0.2.10","server_port":443,"password":"p","unknown_field":1}]}`, "unknown field"},
		{"cc.bad_type", `{"inbounds": "not-an-array"}`, "inbounds"},
		{"cc.html", `<html>502</html>`, "invalid character"},
		{"cc.empty", ``, "unexpected end"},
	}
	for _, testCase := range checkCases {
		err := CheckConfig(testCase.input)
		row := compatRow{ID: testCase.id, Kind: "checkConfig", OK: err == nil}
		if err != nil {
			row.Error = err.Error()
		}
		if testCase.expectError == "" {
			if err != nil {
				t.Errorf("%s: expected success, got %v", testCase.id, err)
			}
		} else {
			if err == nil {
				t.Errorf("%s: expected an error containing %q, got none", testCase.id, testCase.expectError)
			} else if !strings.Contains(err.Error(), testCase.expectError) {
				t.Errorf("%s: error %q does not contain %q", testCase.id, err.Error(), testCase.expectError)
			}
		}
		rows = append(rows, row)
	}

	// ---- AnalyzeConfig -----------------------------------------------------
	analysis, analysisErr := AnalyzeConfig(compatCleanAnalysisConfig)
	if analysisErr != nil {
		t.Fatalf("analyze clean config: %v", analysisErr)
	}
	var document struct {
		Parsed           bool `json:"parsed"`
		Checks           struct{ Parsed, DeclarationsChecked, RuntimeChecked bool } `json:"checks"`
		ConfigRevision   string `json:"configRevision"`
		BuildID          string `json:"buildId"`
		Platform         string `json:"platform"`
		BuildQUIC        bool   `json:"buildQuic"`
		Findings         []struct{ Code, Detail string } `json:"findings"`
		PendingProviders []struct{ Tag, Type string } `json:"pendingProviders"`
		References       []struct{ From, Kind, Tag, State string } `json:"references"`
		Capabilities     []struct {
			Kind  string   `json:"kind"`
			Types []string `json:"types"`
		} `json:"capabilities"`
		Outbounds int `json:"outbounds"`
		Endpoints int `json:"endpoints"`
		Providers int `json:"providers"`
	}
	if err := json.Unmarshal([]byte(analysis.Value), &document); err != nil {
		t.Fatalf("analysis is not JSON: %v", err)
	}
	if !document.Parsed || !document.Checks.Parsed || !document.Checks.DeclarationsChecked {
		t.Errorf("analysis must report parsed+declarationsChecked, got %+v", document.Checks)
	}
	if document.Checks.RuntimeChecked {
		t.Error("analysis must never claim a runtime check")
	}
	if len(document.Findings) != 1 || document.Findings[0].Code != "dangling_reference" {
		t.Errorf("expected exactly one dangling_reference finding, got %+v", document.Findings)
	}
	if len(document.PendingProviders) != 1 || document.PendingProviders[0].Tag != "sub" {
		t.Errorf("expected the remote provider to be pending, got %+v", document.PendingProviders)
	}
	dangling := 0
	for _, reference := range document.References {
		if reference.State == "dangling" {
			dangling++
		}
	}
	if dangling != 1 {
		t.Errorf("expected exactly one dangling reference, got %d", dangling)
	}
	capabilityKinds := make([]string, 0, len(document.Capabilities))
	for _, capability := range document.Capabilities {
		capabilityKinds = append(capabilityKinds, capability.Kind)
	}
	sort.Strings(capabilityKinds)
	rows = append(rows, compatRow{
		ID:   "an.clean",
		Kind: "analyzeConfig",
		OK:   true,
		Extra: map[string]any{
			"outbounds":        document.Outbounds,
			"endpoints":        document.Endpoints,
			"providers":        document.Providers,
			"findings":         len(document.Findings),
			"pendingProviders": len(document.PendingProviders),
			"danglingRefs":     dangling,
			"capabilityKinds":  strings.Join(capabilityKinds, ","),
			"configRevision":   document.ConfigRevision,
			"buildId":          document.BuildID,
			"platform":         document.Platform,
			"buildQuic":        document.BuildQUIC,
		},
	})

	// The duplicate tag is refused by the DECODER, not reported as a finding (BUGS.md B-10).
	duplicateAnalysis, duplicateErr := AnalyzeConfig(compatDuplicateConfig)
	if duplicateErr == nil {
		t.Error("AnalyzeConfig must refuse a duplicate tag instead of reporting a finding")
	}
	rows = append(rows, compatRow{
		ID:    "an.duplicate_tag",
		Kind:  "analyzeConfig",
		OK:    false,
		Error: compatErrorText(duplicateErr),
	})
	_ = duplicateAnalysis

	// The document revision identifies bytes, not a parse (A codes against it).
	revisionOne := compatRevision(t, compatCleanAnalysisConfig)
	revisionTwo := compatRevision(t, compatCleanAnalysisConfig)
	revisionOther := compatRevision(t, compatMinimalConfig)
	if revisionOne != revisionTwo {
		t.Errorf("the same bytes must produce the same revision: %q vs %q", revisionOne, revisionTwo)
	}
	if revisionOne == revisionOther {
		t.Errorf("different bytes must produce different revisions, both were %q", revisionOne)
	}
	rows = append(rows, compatRow{
		ID:    "an.revision_is_content_hash",
		Kind:  "analyzeConfig",
		OK:    revisionOne == revisionTwo && revisionOne != revisionOther,
		Count: len(revisionOne),
	})

	// ---- Share links -------------------------------------------------------
	linkCases := []struct {
		id          string
		link        string
		expectError string
		expectType  string
	}{
		{"link.ss_base64", "ss://YWVzLTEyOC1nY206cGFzc3dvcmQ@192.0.2.10:8388#SS", "", "shadowsocks"},
		{"link.ss_plain", "ss://aes-128-gcm:password@192.0.2.10:8388#SS2", "", "shadowsocks"},
		{"link.vmess", "vmess://eyJhZGQiOiIxOTIuMC4yLjEwIiwicG9ydCI6IjQ0MyIsImlkIjoiMTExMTExMTEtMTExMS0xMTExLTExMTEtMTExMTExMTExMTExIn0=", "", "vmess"},
		{"link.vless", "vless://11111111-1111-1111-1111-111111111111@192.0.2.10:443?encryption=none#VLESS", "", "vless"},
		{"link.trojan", "trojan://password@192.0.2.10:443?sni=example.com#TROJAN", "", "trojan"},
		{"link.tuic", "tuic://11111111-1111-1111-1111-111111111111:password@192.0.2.10:443#TUIC", "", "tuic"},
		{"link.hysteria", "hysteria://192.0.2.10:443?auth=secret&peer=example.com#HY", "", "hysteria"},
		{"link.hy2", "hy2://password@192.0.2.10:443?sni=example.com#HY2", "", "hysteria2"},
		{"link.hysteria2", "hysteria2://password@192.0.2.10:443?sni=example.com#HY2B", "", "hysteria2"},
		{"link.anytls", "anytls://password@192.0.2.10:443?sni=example.com#ANYTLS", "", "anytls"},
		{"link.ssr_unsupported", "ssr://MTkyLjAuMi4xMDo4Mzg4", "unsupported scheme", ""},
		{"link.wireguard_unsupported", "wireguard://cHJpdmF0ZQ@192.0.2.10:51820#WG", "unsupported scheme", ""},
		{"link.junk", "not-a-link", "invalid link", ""},
		{"link.empty", "", "invalid link", ""},
		{"link.vless_missing_uuid", "vless://@192.0.2.10:443?encryption=none#NoUUID", "missing uuid", ""},
	}
	for _, testCase := range linkCases {
		outbound, err := parser.ParseSubscriptionLink(testCase.link)
		row := compatRow{ID: testCase.id, Kind: "parseLink", OK: err == nil}
		if err != nil {
			row.Error = err.Error()
		}
		if testCase.expectError != "" {
			if err == nil || !strings.Contains(err.Error(), testCase.expectError) {
				t.Errorf("%s: expected error containing %q, got %v", testCase.id, testCase.expectError, err)
			}
		} else {
			if err != nil {
				t.Errorf("%s: expected success, got %v", testCase.id, err)
			} else {
				row.Count = 1
				row.Extra = map[string]any{"type": outbound.Type, "tag": outbound.Tag}
				if outbound.Type != testCase.expectType {
					t.Errorf("%s: expected type %q, got %q", testCase.id, testCase.expectType, outbound.Type)
				}
			}
		}
		rows = append(rows, row)
	}

	// ---- Subscription documents -------------------------------------------
	subscriptionCases := []struct {
		id          string
		content     string
		expectError string
		expectCount int
	}{
		{"sub.raw_two", compatLinks, "", 2},
		{"sub.raw_crlf", strings.ReplaceAll(compatLinks, "\n", "\r\n"), "", 2},
		{"sub.raw_base64", "dHJvamFuOi8vcGFzc3dvcmRAMTkyLjAuMi4xMDo0NDMjc25pPWV4YW1wbGUuY29tI0E=", "", 1},
		{"sub.raw_partial_bad_line", compatLinks + "\nssr://MTkyLjAuMi4xMDo4Mzg4", "", 2},
		{"sub.raw_all_bad", "garbage-one\ngarbage-two", "no servers found", 0},
		{"sub.clash", compatClash, "", 1},
		{"sub.sip008", compatSIP008, "", 1},
		// direct/selector are policy objects, not nodes: one real outbound survives.
		{"sub.box_json_filters_policy", compatBoxJSON, "", 1},
		// BUGS.md B-01: a document without an `outbounds` key is refused before endpoints decode.
		{"sub.box_endpoint_only", compatEndpointOnlyJSON, "no servers found", 0},
		{"sub.html", "<html>502</html>", "no servers found", 0},
		{"sub.empty", "", "no servers found", 0},
	}
	for _, testCase := range subscriptionCases {
		outbounds, err := parser.ParseSubscription(ctx, testCase.content, nil, "")
		row := compatRow{ID: testCase.id, Kind: "parseSubscription", OK: err == nil, Count: len(outbounds)}
		if err != nil {
			row.Error = err.Error()
		}
		if testCase.expectError != "" {
			if err == nil || !strings.Contains(err.Error(), testCase.expectError) {
				t.Errorf("%s: expected error containing %q, got %v", testCase.id, testCase.expectError, err)
			}
		} else {
			if err != nil {
				t.Errorf("%s: expected success, got %v", testCase.id, err)
			}
			if len(outbounds) != testCase.expectCount {
				t.Errorf("%s: expected %d outbounds, got %d", testCase.id, testCase.expectCount, len(outbounds))
			}
		}
		rows = append(rows, row)
	}

	// ---- Endpoint separation (the measured scope of B-01) ------------------
	outboundsBoth, endpointsBoth, errBoth := parser.ParseBoxSubscription(ctx, compatBothArraysJSON)
	if errBoth != nil || len(endpointsBoth) != 1 {
		t.Errorf("a document with outbounds must keep its endpoints: outbounds=%d endpoints=%d err=%v", len(outboundsBoth), len(endpointsBoth), errBoth)
	}
	rows = append(rows, compatRow{
		ID:   "sub.box_with_both_arrays",
		Kind: "parseSubscription",
		OK:   errBoth == nil,
		Extra: map[string]any{
			"outbounds": len(outboundsBoth),
			"endpoints": len(endpointsBoth),
		},
	})

	// ---- Size behaviour (the prompt's "giant content" case) ----------------
	// A subscription with many nodes must parse, and a huge non-subscription must fail as an
	// error rather than hang or be accepted in part. Neither case is bounded by the core; the
	// numbers are chosen so a pathological quadratic behaviour would show up as a slow test.
	var builder strings.Builder
	for index := 0; index < 500; index++ {
		builder.WriteString("trojan://password@192.0.2.10:443?sni=example.com#Node-")
		builder.WriteString(itoaForTest(index))
		builder.WriteString("\n")
	}
	largeList := builder.String()
	largeOutbounds, largeErr := parser.ParseSubscription(ctx, largeList, nil, "")
	if largeErr != nil || len(largeOutbounds) != 500 {
		t.Errorf("a 500-link list must parse into 500 nodes, got %d (err=%v)", len(largeOutbounds), largeErr)
	}
	rows = append(rows, compatRow{ID: "sub.large_list_500", Kind: "parseSubscription", OK: largeErr == nil, Count: len(largeOutbounds)})

	garbage := strings.Repeat("this line is not a link and never will be\n", 5000)
	_, garbageErr := parser.ParseSubscription(ctx, garbage, nil, "")
	if garbageErr == nil {
		t.Error("a 5000-line non-subscription must be refused")
	}
	rows = append(rows, compatRow{ID: "sub.large_garbage_5000", Kind: "parseSubscription", OK: garbageErr == nil, Error: compatErrorText(garbageErr)})

	compatRowsToJSON(t, rows)
}

// itoaForTest renders a small non-negative integer without importing strconv into the assertion
// path (the fixture must not look like the code it tests).
func itoaForTest(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [8]byte
	position := len(digits)
	for value > 0 {
		position--
		digits[position] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[position:])
}

func compatErrorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// compatRevision reads configRevision through AnalyzeConfig; it is the value A keys revisions on.
func compatRevision(t *testing.T, content string) string {
	t.Helper()
	box, err := AnalyzeConfig(content)
	if err != nil {
		t.Fatalf("analyze for revision: %v", err)
	}
	var document struct {
		ConfigRevision string `json:"configRevision"`
	}
	if err := json.Unmarshal([]byte(box.Value), &document); err != nil {
		t.Fatalf("decode analysis for revision: %v", err)
	}
	return document.ConfigRevision
}
