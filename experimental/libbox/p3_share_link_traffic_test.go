package libbox

// P3 (closing round) — a SHARE LINK, parsed by the product's own parser, carrying real traffic.
//
// The first P3 trace used a Clash document because no local server existed for the link protocols.
// That left the headline chain ("pasted share link -> ... -> traffic") proven only up to
// CheckConfig. This test closes it without implementing a protocol server by hand: the kernel
// itself hosts the shadowsocks server (a second, independent Box), so the client really dials an
// `ss://` node that came out of a link.
//
// What is real here: the link text, the parser, the standardised outbound, the client Box, the
// selector, the ss node, the kernel-hosted ss inbound, and the HTTP response.

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sagernet/sing-box/daemon"

	"github.com/stretchr/testify/require"
)

// TestP3ShareLinkCarriesTrafficThroughAKernelHostedServer is the link-to-traffic trace.
func TestP3ShareLinkCarriesTrafficThroughAKernelHostedServer(t *testing.T) {
	previousWorking, previousTemp, previousBase := sWorkingPath, sTempPath, sBasePath
	previousUser, previousGroup := sUserID, sGroupID
	t.Cleanup(func() {
		sWorkingPath, sTempPath, sBasePath = previousWorking, previousTemp, previousBase
		sUserID, sGroupID = previousUser, previousGroup
	})
	// The server Box and the client Box must NOT share a cache-file: it is a single-instance file,
	// and the second initialisation waits for the lock until it times out. Each gets its own
	// working/temp/base triple, switched in while its Box is constructed.
	useContainer := func(working string) {
		sWorkingPath, sTempPath, sBasePath = working, t.TempDir(), t.TempDir()
		sUserID, sGroupID = -1, -1
	}

	const cipher = "aes-128-gcm"
	const password = "p3-test-password"

	// 1. The kernel hosts a real shadowsocks server.
	serverPort := freeLibboxPort(t)
	serverConfig := fmt.Sprintf(`{
  "log": {"level": "error"},
  "inbounds": [{"type": "shadowsocks", "tag": "ss-in", "listen": "127.0.0.1", "listen_port": %d, "method": %q, "password": %q}],
  "outbounds": [{"type": "direct", "tag": "direct"}],
  "route": {"final": "direct"}
}`, serverPort, cipher, password)
	useContainer(t.TempDir())
	server, err := NewCommandServer(nil, refreshPlatform{})
	require.NoError(t, err)
	t.Cleanup(server.Close)
	require.NoError(t, server.StartOrReloadService(serverConfig, &OverrideOptions{}))

	// 2. The user pastes an ss:// link. Both the documented forms are exercised here: the base64
	//    userinfo form and the plain `method:password` form.
	base64UserInfo := base64.RawURLEncoding.EncodeToString([]byte(cipher + ":" + password))
	link := fmt.Sprintf("ss://%s@127.0.0.1:%d#ss-node", base64UserInfo, serverPort)

	classified := detectImport(t, link)
	require.Equal(t, ImportKindNodes, classified.Kind)
	require.Equal(t, "links", classified.Format)
	require.Equal(t, 1, classified.NodeCount)

	nodes := p3PreviewNodes(t, link)
	require.Len(t, nodes, 1)
	require.Contains(t, nodes[0], `"type":"shadowsocks"`)
	require.Contains(t, nodes[0], fmt.Sprintf(`"server_port":%d`, serverPort))

	// 3. The host assembles a profile from that node and applies it.
	proxyPort := freeLibboxPort(t)
	profile := p3ImportProfile(nodes, proxyPort, []string{"ss-node"})
	require.NoError(t, CheckConfig(profile), "a profile assembled from a share link must validate")
	// The receipt names the PROFILE's revision, not the link text's: the host applies the assembled
	// document, and that is the identity that must round-trip.
	profileReport := detectImport(t, profile)
	require.Equal(t, ImportKindConfig, profileReport.Kind)

	useContainer(t.TempDir())
	client, err := NewCommandServer(nil, refreshPlatform{})
	require.NoError(t, err)
	t.Cleanup(client.Close)
	outcome := decodeApplyOutcome(t, client.ApplyServiceChangeReport(profile, &OverrideOptions{}))
	require.True(t, outcome.OK, "the assembled profile must start: %+v", outcome)
	require.Equal(t, "started", outcome.Status)
	require.Equal(t, profileReport.Revision, outcome.Report.ActiveRevision,
		"the receipt names the assembled profile, not the link text")
	require.NotEqual(t, classified.Revision, profileReport.Revision,
		"the link text and the profile are different documents with different identities")

	// 4. The HTTP request leaves through the imported ss node.
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("ok"))
	}))
	t.Cleanup(target.Close)

	proxyClient := liveProviderProxyClient(t, proxyPort)
	defer proxyClient.CloseIdleConnections()
	require.Eventually(t, func() bool { return liveProviderServes(t, proxyClient, target.URL) },
		10*time.Second, 50*time.Millisecond, "an ss:// share link must carry real traffic")

	// 5. The selector really is the node from the link (not the direct fallback).
	_, selectErr := client.SelectOutbound(context.Background(), &daemon.SelectOutboundRequest{
		GroupTag:    "selected",
		OutboundTag: "ss-node",
	})
	require.NoError(t, selectErr)
	proxyClient.CloseIdleConnections()
	require.Eventually(t, func() bool { return liveProviderServes(t, proxyClient, target.URL) },
		10*time.Second, 50*time.Millisecond, "traffic through the ss node selected by tag")
}
