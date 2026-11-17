package libbox

// P2 — the contract tests for the three things this phase must prove before anyone touches Go:
//
//	1. a single safe cross-platform DEFAULT TEMPLATE can be shipped by the client and accepted by
//	   the core as-is (so the core needs no "default config" API, which is what B-box added and
//	   what this project deliberately does not);
//	2. a large import is bounded work with a measurable cost, so the host can decide a size cap
//	   instead of the core imposing one silently;
//	3. the preview entry is safe under concurrent use (the UI can preview while a refresh runs).
//
// Every assertion here runs offline and builds no Box except where it says so.

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// p2DefaultTemplate is the single template the client may ship.
//
// # What it is, and what it deliberately is NOT
//
// It is the smallest configuration that (a) validates on every one of the five platforms,
// (b) gives the user a working direct connection on first launch, and (c) leaves a selector
// placeholder the client fills once real nodes exist.
//
// It contains NO tun inbound: the host injects its own TUN block per platform, because TUN options
// are platform facts (interface name, routes, HTTP proxy port) and a template that hard-coded them
// would be wrong on four of five platforms. It contains NO app-specific DNS server, NO reject
// rules for UDP/443/853/STUN, and NO geo routing: those are the B-box defaults this project
// refused to copy (see BBOX_VS_CORE_DIFF.md section 4.2).
const p2DefaultTemplate = `{
  "log": {"level": "info", "timestamp": true},
  "dns": {"servers": [{"tag": "dns-local", "type": "local"}], "final": "dns-local"},
  "inbounds": [{"type": "mixed", "tag": "mixed-in", "listen": "127.0.0.1", "listen_port": 2080}],
  "outbounds": [
    {"type": "direct", "tag": "direct"},
    {"type": "selector", "tag": "proxy", "outbounds": ["direct"], "default": "direct"},
    {"type": "urltest", "tag": "auto", "outbounds": ["direct"], "url": "https://www.gstatic.com/generate_204", "interval": "10m"}
  ],
  "route": {"final": "proxy"}
}`

// TestP2DefaultTemplateIsAcceptedByTheCore is the evidence that the template is a CLIENT asset,
// not a core API: the core validates it like any other document.
func TestP2DefaultTemplateIsAcceptedByTheCore(t *testing.T) {
	if target := os.Getenv("JJ_TEMPLATE_OUT"); target != "" {
		if err := os.WriteFile(target, []byte(p2DefaultTemplate), 0o644); err != nil {
			t.Fatalf("write template fixture: %v", err)
		}
	}

	if err := CheckConfig(p2DefaultTemplate); err != nil {
		t.Fatalf("the shipped default template must validate, got: %v", err)
	}

	box, err := AnalyzeConfig(p2DefaultTemplate)
	if err != nil {
		t.Fatalf("analyze template: %v", err)
	}
	var analysis struct {
		Parsed     bool                            `json:"parsed"`
		Findings   []struct{ Code, Detail string } `json:"findings"`
		Outbounds  int                             `json:"outbounds"`
		Inbounds   int                             `json:"inbounds"`
		References []struct {
			From  string `json:"from"`
			Tag   string `json:"tag"`
			State string `json:"state"`
		} `json:"references"`
	}
	if err = json.Unmarshal([]byte(box.Value), &analysis); err != nil {
		t.Fatalf("template analysis is not JSON: %v", err)
	}
	if !analysis.Parsed {
		t.Fatal("template must parse")
	}
	if len(analysis.Findings) != 0 {
		t.Fatalf("template must have no declaration findings, got %+v", analysis.Findings)
	}
	if analysis.Outbounds != 3 || analysis.Inbounds != 1 {
		t.Fatalf("template shape changed: outbounds=%d inbounds=%d", analysis.Outbounds, analysis.Inbounds)
	}
	dangling := 0
	for _, reference := range analysis.References {
		if reference.State == "dangling" {
			dangling++
		}
	}
	if dangling != 0 {
		t.Fatalf("template must have no dangling reference, got %d", dangling)
	}

	// The template is a PROXY-mode template: no TUN inbound, because the host adds one.
	hasTun, err := HasTunInbound(p2DefaultTemplate)
	if err != nil {
		t.Fatalf("has tun inbound: %v", err)
	}
	if hasTun {
		t.Fatal("the shipped template must not declare a tun inbound; the host injects one")
	}

	// The template must not carry the refused defaults.
	for _, forbidden := range []string{"geoip-cn", "geosite-cn", "\"port\": 853", "hijack-dns", "reject"} {
		if strings.Contains(p2DefaultTemplate, forbidden) {
			t.Fatalf("template must not contain the refused B-box default %q", forbidden)
		}
	}
}

// TestP2PreviewIsSafeUnderConcurrency mirrors the UI situation: a preview can run while a
// background provider refresh is in flight, and two taps can start two previews.
func TestP2PreviewIsSafeUnderConcurrency(t *testing.T) {
	inputs := []string{
		"trojan://password@192.0.2.10:443?sni=example.com#A",
		"proxies:\n  - name: c\n    type: ss\n    server: 192.0.2.10\n    port: 8388\n    cipher: aes-128-gcm\n    password: p\n",
		"vless://11111111-1111-1111-1111-111111111111@192.0.2.11:443?encryption=none#B",
		"<html>502</html>",
	}
	var waitGroup sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			for iteration := 0; iteration < 25; iteration++ {
				input := inputs[(index+iteration)%len(inputs)]
				box, err := ParsePreview(input)
				if err != nil {
					t.Errorf("concurrent preview %d/%d failed: %v", index, iteration, err)
					return
				}
				if box == nil || box.Value == "" {
					t.Errorf("concurrent preview %d/%d returned nothing", index, iteration)
					return
				}
			}
		}(worker)
	}
	waitGroup.Wait()
}

// TestP2LargeImportHasAMeasurableCost gives the host the numbers it needs to choose a size cap,
// instead of the core silently truncating a document.
func TestP2LargeImportHasAMeasurableCost(t *testing.T) {
	const nodeCount = 10000
	var builder strings.Builder
	for index := 0; index < nodeCount; index++ {
		builder.WriteString("trojan://password@192.0.2.10:443?sni=example.com#Node-")
		builder.WriteString(p2Itoa(index))
		builder.WriteString("\n")
	}
	document := builder.String()

	started := time.Now()
	box, err := ParsePreview(document)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("large import must be a result, not an error: %v", err)
	}
	var report struct {
		OK    bool `json:"ok"`
		Count int  `json:"count"`
	}
	if err = json.Unmarshal([]byte(box.Value), &report); err != nil {
		t.Fatalf("large import report is not JSON: %v", err)
	}
	if !report.OK || report.Count != nodeCount {
		t.Fatalf("large import must report all %d nodes, got ok=%v count=%d", nodeCount, report.OK, report.Count)
	}
	t.Logf("large import: nodes=%d bytes=%d elapsed=%s", report.Count, len(document), elapsed.Round(time.Millisecond))
	if elapsed > 10*time.Second {
		t.Fatalf("large import took %s; a pathological cost must be reported before it ships", elapsed)
	}
}

// p2Itoa renders a small non-negative integer without pulling strconv into the assertion path.
func p2Itoa(value int) string {
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
