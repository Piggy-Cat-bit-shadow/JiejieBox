package libbox

// A2/A3 — the configuration entry's live provider refresh, end to end.
//
// The profile is unchanged, so the entry may satisfy the reload by refreshing the remote providers
// instead of rebuilding the Box. One provider's document turns bad: the refresh reports it, the
// OTHER provider keeps serving, the old nodes of the failed one keep serving, and the Box and its
// connections are not replaced. A changed provider declaration is a document change and rebuilds.

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/daemon"

	"github.com/stretchr/testify/require"
)

func liveProviderProxyClient(t *testing.T, proxyPort int) *http.Client {
	t.Helper()
	proxyURL := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", proxyPort)}
	return &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   5 * time.Second,
	}
}

func liveProviderServes(t *testing.T, client *http.Client, target string) bool {
	t.Helper()
	response, err := client.Get(target)
	if err != nil {
		t.Logf("proxy request failed: %v", err)
		return false
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Logf("proxy request status: %s", response.Status)
	}
	return response.StatusCode == http.StatusOK
}

// startSocksServer runs a minimal SOCKS5 CONNECT server, so a provider node can be a real outbound
// that carries the probe request rather than a node type the provider parser rejects.
func startSocksServer(t *testing.T) (string, int) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func(connection net.Conn) {
				defer connection.Close()
				header := make([]byte, 2)
				if _, err := io.ReadFull(connection, header); err != nil || header[0] != 5 {
					return
				}
				methods := make([]byte, int(header[1]))
				if _, err := io.ReadFull(connection, methods); err != nil {
					return
				}
				if _, err := connection.Write([]byte{5, 0}); err != nil {
					return
				}
				request := make([]byte, 4)
				if _, err := io.ReadFull(connection, request); err != nil || request[1] != 1 {
					return
				}
				var host string
				switch request[3] {
				case 1:
					address := make([]byte, 4)
					if _, err := io.ReadFull(connection, address); err != nil {
						return
					}
					host = net.IP(address).String()
				case 3:
					length := make([]byte, 1)
					if _, err := io.ReadFull(connection, length); err != nil {
						return
					}
					name := make([]byte, int(length[0]))
					if _, err := io.ReadFull(connection, name); err != nil {
						return
					}
					host = string(name)
				default:
					return
				}
				portBytes := make([]byte, 2)
				if _, err := io.ReadFull(connection, portBytes); err != nil {
					return
				}
				destination := net.JoinHostPort(host, fmt.Sprintf("%d", int(portBytes[0])<<8|int(portBytes[1])))
				upstream, dialErr := net.Dial("tcp", destination)
				if dialErr != nil {
					_, _ = connection.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
					return
				}
				defer upstream.Close()
				_, _ = connection.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
				done := make(chan struct{}, 1)
				go func() {
					_, _ = io.Copy(upstream, connection)
					done <- struct{}{}
				}()
				_, _ = io.Copy(connection, upstream)
				<-done
			}(connection)
		}
	}()
	port := listener.Addr().(*net.TCPAddr).Port
	return fmt.Sprintf("127.0.0.1:%d", port), port
}

func TestAnUnchangedProfileRefreshesProvidersWithoutRebuildingTheBox(t *testing.T) {
	previousWorking, previousTemp := sWorkingPath, sTempPath
	previousBase := sBasePath
	previousUser, previousGroup := sUserID, sGroupID
	sWorkingPath, sTempPath, sBasePath = t.TempDir(), t.TempDir(), t.TempDir()
	sUserID, sGroupID = -1, -1
	t.Cleanup(func() {
		sWorkingPath, sTempPath = previousWorking, previousTemp
		sBasePath = previousBase
		sUserID, sGroupID = previousUser, previousGroup
	})

	socksAddress, socksPort := startSocksServer(t)
	document := func(node string) string {
		return fmt.Sprintf(`{"outbounds":[{"type":"socks","tag":%q,"server":%q,"server_port":%d}]}`, node, "127.0.0.1", socksPort)
	}
	_ = socksAddress
	var firstRequests, secondRequests atomic.Int32
	firstServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		firstRequests.Add(1)
		_, _ = writer.Write([]byte(document("n1")))
	}))
	t.Cleanup(firstServer.Close)
	var secondHealthy atomic.Bool
	secondHealthy.Store(true)
	secondServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		secondRequests.Add(1)
		if secondHealthy.Load() {
			_, _ = writer.Write([]byte(document("n2")))
			return
		}
		_, _ = writer.Write([]byte("this is not a provider document"))
	}))
	t.Cleanup(secondServer.Close)
	thirdServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(document("n3")))
	}))
	t.Cleanup(thirdServer.Close)

	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("ok"))
	}))
	t.Cleanup(target.Close)

	commandServer, err := NewCommandServer(nil, refreshPlatform{})
	require.NoError(t, err)
	t.Cleanup(commandServer.Close)

	proxyPort := freeLibboxPort(t)
	profile := func(secondURL string) string {
		return fmt.Sprintf(`{
  "log": {"level": "error"},
  "inbounds": [{"type": "http", "tag": "proxy", "listen": "127.0.0.1", "listen_port": %d}],
  "providers": [
    {"type": "remote", "tag": "p1", "url": %q, "update_interval": "1h"},
    {"type": "remote", "tag": "p2", "url": %q, "update_interval": "1h"}
  ],
  "outbounds": [
    {"type": "direct", "tag": "direct"},
    {"type": "selector", "tag": "selected", "providers": ["p1", "p2"]}
  ],
  "route": {
    "final": "direct",
    "rules": [{"inbound": ["proxy"], "action": "route", "outbound": "selected"}]
  }
}`, proxyPort, firstServer.URL, secondURL)
	}
	unchanged := profile(secondServer.URL)
	require.NoError(t, commandServer.StartOrReloadService(unchanged, &OverrideOptions{}))
	boxBefore := commandServer.Instance()
	require.NotNil(t, boxBefore)
	client := liveProviderProxyClient(t, proxyPort)
	defer client.CloseIdleConnections()

	// Remote providers fetch on their own schedule; the manual refresh is what loads the nodes in
	// this fixture. A provider's own timer can hold it, so refresh until both are published.
	require.Eventually(t, func() bool {
		summary, refreshErr := commandServer.RefreshProviders("", 5000)
		if refreshErr != nil {
			return false
		}
		return strings.Contains(summary.Value, `"tag":"p1","status":"published"`) &&
			strings.Contains(summary.Value, `"tag":"p2","status":"published"`)
	}, 5*time.Second, 50*time.Millisecond, "both provider documents are published")
	require.Eventually(t, func() bool {
		_, selectErr := commandServer.SelectOutbound(context.Background(), &daemon.SelectOutboundRequest{
			GroupTag:    "selected",
			OutboundTag: "p2/n2",
		})
		if selectErr != nil {
			t.Logf("select provider node: %v", selectErr)
		}
		return selectErr == nil
	}, 5*time.Second, 20*time.Millisecond, "the selector learns the provider's nodes")
	require.True(t, liveProviderServes(t, client, target.URL), "the selector runs on the provider's node")
	beforeFirst, beforeSecond := firstRequests.Load(), secondRequests.Load()

	// The second provider's document turns bad. The profile is unchanged: the entry must refresh
	// the providers in place, report the partial result, and keep the old nodes serving.
	secondHealthy.Store(false)
	require.NoError(t, commandServer.StartOrReloadService(unchanged, &OverrideOptions{}))
	require.Same(t, boxBefore, commandServer.Instance(), "a provider refresh must not replace the Box")
	report := commandServer.LastServiceChange()
	require.NotNil(t, report)
	require.Equal(t, "applied_live", string(report.Status))
	require.False(t, report.RestartRequired)
	require.NotNil(t, report.Provider)
	require.Equal(t, "partial", string(report.Provider.Status), "one provider failed, the other was refreshed")
	require.Greater(t, firstRequests.Load(), beforeFirst, "the healthy provider was refreshed")
	require.Greater(t, secondRequests.Load(), beforeSecond, "the failing provider was attempted")
	var failed, delivered int
	for _, result := range report.Provider.Results {
		switch string(result.Status) {
		case "failed":
			failed++
		case "published", "unchanged":
			delivered++
		}
	}
	require.Equal(t, 1, failed)
	require.Equal(t, 1, delivered)
	require.True(t, liveProviderServes(t, client, target.URL),
		"the previously published provider nodes must keep serving after a partial refresh")

	// A changed provider declaration is a document change: the entry rebuilds, as it must.
	changed := profile(thirdServer.URL)
	require.NoError(t, commandServer.StartOrReloadService(changed, &OverrideOptions{}))
	require.NotSame(t, boxBefore, commandServer.Instance(), "a provider declaration change needs a new Box")
	client.CloseIdleConnections()
	_, err = commandServer.RefreshProviders("", 5000)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		_, selectErr := commandServer.SelectOutbound(context.Background(), &daemon.SelectOutboundRequest{
			GroupTag:    "selected",
			OutboundTag: "p2/n3",
		})
		return selectErr == nil
	}, 5*time.Second, 20*time.Millisecond, "the rebuilt Box learns the new provider's nodes")
	require.Eventually(t, func() bool { return liveProviderServes(t, client, target.URL) }, 5*time.Second, 20*time.Millisecond,
		"the rebuilt Box serves through the new provider")
}

func freeLibboxPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())
	return port
}
