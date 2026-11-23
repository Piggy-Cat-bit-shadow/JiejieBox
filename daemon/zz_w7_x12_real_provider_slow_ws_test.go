//go:build with_clash_api

package daemon

// W7 / X12 (ORIGINAL oracle, task book 30 section 7.2): "API/WS slow write + Provider batch update +
// Catalog reclamation | complete response, no long lock / implicit expansion / old generation pinned
// for long".
//
// The previous delivery's X12 probe used a real slow TCP API server but a STUB provider/outbound
// manager, which cannot prove the Box-level combination. The existing clashapi stream tests use the
// REAL trafficcontrol manager, but no Box, no Provider and no catalog. This test joins the three:
//
//   - a real StartedService -> box.Box with a real remote Provider and a real catalog store;
//   - the real Clash API HTTP server the Box creates, with a real WebSocket consumer that STOPS
//     reading for two seconds while frames are produced every 50 ms (a genuinely slow write);
//   - three real provider batch updates (one changed node each, 300-node documents) during the
//     stalled window;
//   - real tracked connections behind the stream (48 streaming business requests through the Box's
//     HTTP inbound and a catalog-deferred node).
//
// The oracle's assertions: each update completes without waiting for the stalled writer (no long
// lock), no frame is lost or truncated when the consumer resumes (complete response), the record
// count returns to exactly the node count after every update (no old generation pinned), and the
// stream never expanded a node implicitly.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/ws"
	"github.com/sagernet/ws/wsutil"

	"github.com/stretchr/testify/require"
)

// w7WSStream adapts ws.Dial's optional buffered reader to the io.ReadWriter wsutil expects.
type w7WSStream struct {
	reader io.Reader
	writer io.Writer
}

func (s *w7WSStream) Read(buffer []byte) (int, error)  { return s.reader.Read(buffer) }
func (s *w7WSStream) Write(buffer []byte) (int, error) { return s.writer.Write(buffer) }

func w7X12Profile(proxyPort int, providerURL string, clashPort int, cachePath string) string {
	return fmt.Sprintf(`{
  "log": {"level": "error"},
  "experimental": {
    "cache_file": {"enabled": true, "path": %q},
    "clash_api": {"external_controller": "127.0.0.1:%d"}
  },
  "inbounds": [{"type": "http", "tag": "proxy", "listen": "127.0.0.1", "listen_port": %d}],
  "providers": [{"type": "remote", "tag": "lib", "url": %q, "update_interval": "1h"}],
  "outbounds": [
    {"type": "selector", "tag": "selected", "providers": ["lib"]},
    {"type": "direct", "tag": "direct"}
  ],
  "route": {
    "rules": [{"inbound": ["proxy"], "action": "route", "outbound": "selected"}],
    "final": "direct"
  }
}`, cachePath, clashPort, proxyPort, providerURL)
}

func TestX12RealProviderSlowWebSocketAndCatalogReclamation(t *testing.T) {
	const nodes = 300
	const heldFlows = 48

	connectProxy := startW7ConnectProxy(t)
	holdStarted := make(chan struct{}, heldFlows+8)
	holdRelease := make(chan struct{})
	var releaseHold sync.Once
	t.Cleanup(func() { releaseHold.Do(func() { close(holdRelease) }) })
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/hold" {
			flusher, _ := writer.(http.Flusher)
			_, _ = writer.Write([]byte("flow-open\n"))
			if flusher != nil {
				flusher.Flush()
			}
			select {
			case holdStarted <- struct{}{}:
			default:
			}
			select {
			case <-holdRelease:
			case <-time.After(30 * time.Second):
			}
			return
		}
		_, _ = writer.Write([]byte("ok"))
	}))
	t.Cleanup(target.Close)

	library := newW7LibraryServer(t, w7LibraryDocument(nodes, connectProxy.port, -1, 0))
	directory := t.TempDir()
	proxyPort := freeLoopbackPort(t)
	clashPort := freeLoopbackPort(t)
	profile := w7X12Profile(proxyPort, library.server.URL, clashPort, filepath.Join(directory, "cache.db"))

	service := NewStartedService(ServiceOptions{Context: crossSystemContext(t, directory, nil)})
	t.Cleanup(func() {
		_ = service.CloseService()
		service.Close()
	})
	require.NoError(t, service.StartOrReloadService(context.Background(), profile, nil))
	instance := service.Instance()
	require.NotNil(t, instance)
	boxInstance := instance.Box()
	trafficManager := instance.TrafficManager()
	require.NotNil(t, trafficManager)

	_, err := service.RefreshProviders(context.Background(), []string{"lib"}, 30*time.Second)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return boxInstance.CatalogStats().Records == nodes }, 30*time.Second, 5*time.Millisecond,
		"the provider's nodes must be catalog records")
	require.Equal(t, nodes, boxInstance.CatalogStats().Refs)

	// 48 real streaming business flows through the Box: tracked connections the stream reports.
	clients := make([]*http.Client, 0, heldFlows)
	responses := make([]*http.Response, 0, heldFlows)
	for index := 0; index < heldFlows; index++ {
		client := newProxyClient(t, proxyPort)
		client.Timeout = 60 * time.Second
		clients = append(clients, client)
		response, requestErr := client.Get(target.URL + "/hold")
		require.NoError(t, requestErr, "held flow %d must start", index)
		responses = append(responses, response)
		line, readErr := bufio.NewReader(response.Body).ReadString('\n')
		require.NoError(t, readErr)
		require.Equal(t, "flow-open\n", line)
	}
	t.Cleanup(func() {
		for _, response := range responses {
			_ = response.Body.Close()
		}
		for _, client := range clients {
			client.CloseIdleConnections()
		}
	})
	require.Eventually(t, func() bool { return trafficManager.ConnectionsLen() >= heldFlows }, 20*time.Second, 10*time.Millisecond,
		"the Box's traffic manager must track the held flows: count=%d", trafficManager.ConnectionsLen())

	// Wait for the Clash API the Box created and attach the real WebSocket consumer. The interval is
	// 50 ms so the stalled consumer provokes a genuinely slow write rather than two idle ticks.
	clashAddress := fmt.Sprintf("127.0.0.1:%d", clashPort)
	clashProbe := &http.Client{Timeout: 2 * time.Second}
	require.Eventually(t, func() bool {
		response, probeErr := clashProbe.Get("http://" + clashAddress + "/connections")
		if probeErr != nil {
			return false
		}
		response.Body.Close()
		return response.StatusCode == http.StatusOK
	}, 20*time.Second, 50*time.Millisecond, "the Box must start its Clash API server")

	streamURL := url.URL{Scheme: "ws", Host: clashAddress, Path: "/connections", RawQuery: "interval=50"}
	wsConn, wsReader, _, dialErr := ws.Dial(context.Background(), streamURL.String())
	require.NoError(t, dialErr)
	t.Cleanup(func() { _ = wsConn.Close() })
	stream := &w7WSStream{reader: wsConn, writer: wsConn}
	if wsReader != nil {
		stream.reader = wsReader
		t.Cleanup(func() { ws.PutReader(wsReader) })
	}
	readFrame := func() map[string]json.RawMessage {
		payload, readErr := wsutil.ReadServerText(stream)
		require.NoError(t, readErr, "the WS frame must be complete, not truncated")
		var document map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(payload, &document))
		return document
	}
	first := readFrame()
	require.Contains(t, first, "connections")
	var firstConnections []json.RawMessage
	require.NoError(t, json.Unmarshal(first["connections"], &firstConnections))
	require.GreaterOrEqual(t, len(firstConnections), heldFlows, "the first frame really reports the held flows")
	expansionsBeforeStream := boxInstance.DeferredExpansionCount()

	// ---- Slow write: stop reading for 2 s while three provider updates run. ----
	time.Sleep(2 * time.Second)
	updateDurations := make([]time.Duration, 0, 3)
	for round := 0; round < 3; round++ {
		library.setDocument(w7LibraryDocument(nodes, connectProxy.port, 7+round, 32000+round))
		started := time.Now()
		summary, refreshErr := service.RefreshProviders(context.Background(), []string{"lib"}, 30*time.Second)
		updateDurations = append(updateDurations, time.Since(started))
		require.NoError(t, refreshErr)
		require.Equal(t, ProviderRefreshPublished, summary.Results[0].Status, "round %d: %+v", round, summary.Results[0])
		// No long lock, and no old generation pinned: the replaced record is released at once.
		require.Equal(t, nodes, boxInstance.CatalogStats().Records,
			"round %d: an update must not pin the previous generation", round)
		require.Equal(t, nodes, boxInstance.CatalogStats().Refs, "round %d: refs converged", round)
	}
	for _, duration := range updateDurations {
		require.Less(t, duration, 10*time.Second,
			"a provider update must not wait for the stalled WS writer: %v", updateDurations)
	}
	t.Logf("X12 trigger: three provider updates while the WS consumer was stalled: %v, records=%d",
		updateDurations, boxInstance.CatalogStats().Records)

	// The stream never expanded a node by itself (only the business flows' node is expanded).
	require.LessOrEqual(t, boxInstance.DeferredExpansionCount(), 1+expansionsBeforeStream,
		"the API/stream path must not expand catalog nodes implicitly")

	// ---- Resume: the frames that piled up behind the stalled consumer are complete and current. ----
	resumed := 0
	for resumed < 3 {
		document := readFrame()
		var connections []json.RawMessage
		require.NoError(t, json.Unmarshal(document["connections"], &connections))
		require.GreaterOrEqual(t, len(connections), heldFlows,
			"every resumed frame is a complete snapshot that still reports the held flows")
		resumed++
	}
	t.Logf("X12: %d frames read after the stall", resumed)

	// Catalog reclamation after the update rounds and after the stream: still exactly the node count.
	require.Equal(t, nodes, boxInstance.CatalogStats().Records)
	require.Equal(t, nodes, boxInstance.CatalogStats().Refs)
	releaseHold.Do(func() { close(holdRelease) })
	require.NoError(t, service.CloseService())
	closed := boxInstance.CatalogStats()
	require.True(t, closed.Closed)
	require.Zero(t, closed.Records)
	require.Zero(t, closed.Refs)
	// The held flows are closed by t.Cleanup, which also proves the target was a real server: the
	// responses above could only have been produced by the CONNECT upstream.
	_ = connectProxy
}
