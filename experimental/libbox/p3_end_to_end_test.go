package libbox

// P3 — the end-to-end trace the phase asks for, through the SAME entries the Flutter host calls.
//
// The chain under test is the product's real chain, not a shortcut:
//
//	pasted document -> ParsePreview (parser, standardisation) -> assembled profile
//	  -> AnalyzeConfig (declarations) -> ApplyServiceChangeReport (the core starts it)
//	  -> selector -> the node that was imported -> traffic actually leaves through it.
//
// What is deliberately NOT faked: the nodes are real outbounds the parser produced from a document
// (a Clash `socks5` proxy), pointed at a real local SOCKS server, and the traffic is measured by an
// HTTP client that goes through the Box's own inbound.
//
// Share links cannot carry traffic in this environment: turning a trojan/vless link into a working
// server would mean implementing that protocol server in the test, which would test the test. Those
// links are covered by TestP3ShareLinksAssembleIntoAValidProfile (parse -> profile -> CheckConfig).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sagernet/sing-box/daemon"

	"github.com/stretchr/testify/require"
)

// p3ImportProfile assembles a profile the way the host will: the imported nodes go into outbounds,
// a selector groups them, and one inbound routes to that selector. Everything else is the shipped
// template's shape (no tun: the host injects it).
func p3ImportProfile(nodeContents []string, proxyPort int, selectorMembers []string) string {
	members := `"` + strings.Join(selectorMembers, `","`) + `"`
	outbounds := strings.Join(nodeContents, ",")
	return fmt.Sprintf(`{
  "log": {"level": "error"},
  "inbounds": [{"type": "http", "tag": "proxy", "listen": "127.0.0.1", "listen_port": %d}],
  "outbounds": [
    %s,
    {"type": "direct", "tag": "direct"},
    {"type": "selector", "tag": "selected", "outbounds": [%s]}
  ],
  "route": {"final": "direct", "rules": [{"inbound": ["proxy"], "action": "route", "outbound": "selected"}]}
}`, proxyPort, outbounds, members)
}

// p3PreviewNodes returns the node documents a preview produced, which is exactly what the host
// writes into a profile.
func p3PreviewNodes(t *testing.T, content string) []string {
	t.Helper()
	box, err := ParsePreview(content)
	require.NoError(t, err)
	var report struct {
		OK    bool `json:"ok"`
		Count int  `json:"count"`
		Items []struct {
			Tag     string `json:"tag"`
			Content string `json:"content"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal([]byte(box.Value), &report), box.Value)
	require.True(t, report.OK, "the fixture document must be recognised")
	require.Equal(t, report.Count, len(report.Items))
	nodes := make([]string, 0, len(report.Items))
	for _, item := range report.Items {
		nodes = append(nodes, item.Content)
	}
	return nodes
}

// TestP3ClashImportServesTrafficThroughTheImportedNode is the full trace: a Clash document a user
// could paste becomes a running selector whose member is the imported node, and the request leaves
// through that node's real SOCKS server.
func TestP3ClashImportServesTrafficThroughTheImportedNode(t *testing.T) {
	previousWorking, previousTemp, previousBase := sWorkingPath, sTempPath, sBasePath
	previousUser, previousGroup := sUserID, sGroupID
	sWorkingPath, sTempPath, sBasePath = t.TempDir(), t.TempDir(), t.TempDir()
	sUserID, sGroupID = -1, -1
	t.Cleanup(func() {
		sWorkingPath, sTempPath, sBasePath = previousWorking, previousTemp, previousBase
		sUserID, sGroupID = previousUser, previousGroup
	})

	_, socksPort := startSocksServer(t)
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("ok"))
	}))
	t.Cleanup(target.Close)

	clash := fmt.Sprintf(`proxies:
  - name: imported-a
    type: socks5
    server: 127.0.0.1
    port: %d
  - name: imported-b
    type: socks5
    server: 127.0.0.1
    port: %d
`, socksPort, socksPort)

	// 1. The pasted document is classified as nodes, not as a profile.
	classified := detectImport(t, clash)
	require.Equal(t, ImportKindNodes, classified.Kind)
	require.Equal(t, "clash", classified.Format)
	require.Equal(t, 2, classified.NodeCount)

	// 2. The preview produces the node documents the host writes.
	nodes := p3PreviewNodes(t, clash)
	require.Len(t, nodes, 2)
	require.Contains(t, nodes[0], `"type":"socks"`)

	// 3. The assembled profile is accepted by the static analysis before it is applied.
	proxyPort := freeLibboxPort(t)
	profile := p3ImportProfile(nodes, proxyPort, []string{"imported-a", "imported-b"})
	classifiedProfile := detectImport(t, profile)
	require.Equal(t, ImportKindConfig, classifiedProfile.Kind)
	require.NotNil(t, classifiedProfile.ConfigShape)
	require.False(t, classifiedProfile.ConfigShape.NodesOnly)
	analysis, err := AnalyzeConfig(profile)
	require.NoError(t, err)
	var analysisDocument struct {
		Parsed   bool                    `json:"parsed"`
		Findings []struct{ Code string } `json:"findings"`
	}
	require.NoError(t, json.Unmarshal([]byte(analysis.Value), &analysisDocument))
	require.True(t, analysisDocument.Parsed)
	require.Empty(t, analysisDocument.Findings, "an assembled profile must have no declaration findings")

	// 4. The core starts it through the entry the host calls, and says so in the receipt.
	commandServer, serverErr := NewCommandServer(nil, refreshPlatform{})
	require.NoError(t, serverErr)
	t.Cleanup(commandServer.Close)
	outcome := decodeApplyOutcome(t, commandServer.ApplyServiceChangeReport(profile, &OverrideOptions{}))
	require.True(t, outcome.OK, "the assembled profile must start: %+v", outcome)
	require.Equal(t, "started", outcome.Status)
	require.Equal(t, classifiedProfile.Revision, outcome.Report.ActiveRevision,
		"the receipt names the very document the host assembled")

	// 5. The selector exposes the imported nodes as members, and traffic goes through each of them.
	client := liveProviderProxyClient(t, proxyPort)
	defer client.CloseIdleConnections()
	require.Eventually(t, func() bool { return liveProviderServes(t, client, target.URL) },
		5*time.Second, 20*time.Millisecond, "the default selection serves traffic")
	for _, member := range []string{"imported-b", "imported-a"} {
		client.CloseIdleConnections()
		_, selectErr := commandServer.SelectOutbound(context.Background(), &daemon.SelectOutboundRequest{
			GroupTag:    "selected",
			OutboundTag: member,
		})
		require.NoError(t, selectErr, "the selector must know the imported node %q", member)
		require.Eventually(t, func() bool { return liveProviderServes(t, client, target.URL) },
			5*time.Second, 20*time.Millisecond, "traffic through %q", member)
	}
}

// TestP3ShareLinksAssembleIntoAValidProfile covers the share-link path up to the point this
// environment can honestly verify: parse -> standardise -> assemble -> the core accepts it.
func TestP3ShareLinksAssembleIntoAValidProfile(t *testing.T) {
	links := "trojan://password@192.0.2.10:443?sni=example.com#share-a\n" +
		"vless://11111111-1111-1111-1111-111111111111@192.0.2.11:443?encryption=none#share-b"

	classified := detectImport(t, links)
	require.Equal(t, ImportKindNodes, classified.Kind)
	require.Equal(t, "links", classified.Format)
	require.Equal(t, 2, classified.NodeCount)

	nodes := p3PreviewNodes(t, links)
	require.Len(t, nodes, 2)
	require.Contains(t, nodes[0], `"type":"trojan"`)
	require.Contains(t, nodes[1], `"type":"vless"`)

	profile := p3ImportProfile(nodes, freeLibboxPort(t), []string{"share-a", "share-b"})
	require.NoError(t, CheckConfig(profile), "a profile assembled from links must validate")

	// A duplicate tag would be refused by the decoder, which is why the host de-duplicates before
	// writing; the preview reports what the document said, unchanged.
	duplicate := "trojan://p@192.0.2.10:443#same\ntrojan://p@192.0.2.11:443#same"
	duplicateNodes := p3PreviewNodes(t, duplicate)
	duplicateProfile := p3ImportProfile(duplicateNodes, freeLibboxPort(t), []string{"same"})
	require.Error(t, CheckConfig(duplicateProfile), "duplicate tags must be refused, not silently merged")
}

// TestP3UseAllProvidersResolvesAtStart proves the semantics the UI has to respect: use_all_providers
// is resolved when the Box starts, so nodes that arrive from a later provider need a reload, while a
// refresh of an already-declared provider needs none.
func TestP3UseAllProvidersResolvesAtStart(t *testing.T) {
	previousWorking, previousTemp, previousBase := sWorkingPath, sTempPath, sBasePath
	previousUser, previousGroup := sUserID, sGroupID
	sWorkingPath, sTempPath, sBasePath = t.TempDir(), t.TempDir(), t.TempDir()
	sUserID, sGroupID = -1, -1
	t.Cleanup(func() {
		sWorkingPath, sTempPath, sBasePath = previousWorking, previousTemp, previousBase
		sUserID, sGroupID = previousUser, previousGroup
	})

	_, socksPort := startSocksServer(t)
	document := func(tag string) string {
		return fmt.Sprintf(`{"outbounds":[{"type":"socks","tag":%q,"server":"127.0.0.1","server_port":%d}]}`, tag, socksPort)
	}
	first := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(document("n1")))
	}))
	t.Cleanup(first.Close)
	second := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(document("n2")))
	}))
	t.Cleanup(second.Close)
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("ok"))
	}))
	t.Cleanup(target.Close)

	proxyPort := freeLibboxPort(t)
	profile := func(extraProvider string) string {
		return fmt.Sprintf(`{
  "log": {"level": "error"},
  "inbounds": [{"type": "http", "tag": "proxy", "listen": "127.0.0.1", "listen_port": %d}],
  "providers": [
    {"type": "remote", "tag": "p1", "url": %q, "update_interval": "1h"},
    {"type": "remote", "tag": "p2", "url": %q, "update_interval": "1h"}%s
  ],
  "outbounds": [
    {"type": "direct", "tag": "direct"},
    {"type": "selector", "tag": "selected", "use_all_providers": true}
  ],
  "route": {"final": "direct", "rules": [{"inbound": ["proxy"], "action": "route", "outbound": "selected"}]}
}`, proxyPort, first.URL, second.URL, extraProvider)
	}

	commandServer, err := NewCommandServer(nil, refreshPlatform{})
	require.NoError(t, err)
	t.Cleanup(commandServer.Close)
	require.NoError(t, commandServer.StartOrReloadService(profile(""), &OverrideOptions{}))
	client := liveProviderProxyClient(t, proxyPort)
	defer client.CloseIdleConnections()

	// Refresh until every declared provider published: a provider fetch on start may still be in
	// flight, and the manual refresh is the documented way to make it deterministic.
	require.Eventually(t, func() bool {
		summary, refreshErr := commandServer.RefreshProviders("", 5000)
		return refreshErr == nil && strings.Count(summary.Value, `"status":"published"`) >= 2
	}, 10*time.Second, 50*time.Millisecond, "both declared providers publish")

	for _, member := range []string{"p1/n1", "p2/n2"} {
		client.CloseIdleConnections()
		_, selectErr := commandServer.SelectOutbound(context.Background(), &daemon.SelectOutboundRequest{
			GroupTag:    "selected",
			OutboundTag: member,
		})
		require.NoError(t, selectErr, "use_all_providers must expose %q without a reload", member)
		require.Eventually(t, func() bool { return liveProviderServes(t, client, target.URL) },
			5*time.Second, 20*time.Millisecond, "traffic through %q", member)
	}

	// A NEW provider is a document change: the Box is rebuilt, and the new provider's nodes only
	// exist after the rebuild plus a refresh. This is the constraint the UI must not paper over.
	third := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(document("n3")))
	}))
	t.Cleanup(third.Close)
	boxBefore := commandServer.Instance()
	require.NoError(t, commandServer.StartOrReloadService(profile(fmt.Sprintf(`,{"type":"remote","tag":"p3","url":%q,"update_interval":"1h"}`, third.URL)), &OverrideOptions{}))
	require.NotSame(t, boxBefore, commandServer.Instance(), "adding a provider changes the document, so it rebuilds")
	require.Eventually(t, func() bool {
		summary, refreshErr := commandServer.RefreshProviders("", 5000)
		return refreshErr == nil && strings.Contains(summary.Value, `"tag":"p3","status":"published"`)
	}, 10*time.Second, 50*time.Millisecond, "the new provider publishes after the rebuild")
	client.CloseIdleConnections()
	_, selectErr := commandServer.SelectOutbound(context.Background(), &daemon.SelectOutboundRequest{
		GroupTag:    "selected",
		OutboundTag: "p3/n3",
	})
	require.NoError(t, selectErr, "use_all_providers picked the new provider after the rebuild")
	require.Eventually(t, func() bool { return liveProviderServes(t, client, target.URL) },
		5*time.Second, 20*time.Millisecond, "traffic through the newly added provider")
}
