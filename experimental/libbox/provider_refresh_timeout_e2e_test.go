package libbox

// F — a real download, inside a real Box, cut short by the caller's budget.
//
// The daemon-level tests use a fake provider that honours a context, and the provider-level tests
// use the real fetch but no Box. Neither alone proves the chain: that the per-provider budget the
// embedder passes reaches the HTTP request of a provider the RUNNING Box is serving, and that the
// peer connection is closed when it ends. This test does, through the production entry
// (`CommandServer.RefreshProviders`) with nothing stubbed between it and the socket.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestABudgetCutsARealProviderDownloadAndClosesTheConnection(t *testing.T) {
	previousWorking, previousTemp, previousBase := sWorkingPath, sTempPath, sBasePath
	previousUser, previousGroup := sUserID, sGroupID
	sWorkingPath, sTempPath, sBasePath = t.TempDir(), t.TempDir(), t.TempDir()
	sUserID, sGroupID = -1, -1
	t.Cleanup(func() {
		sWorkingPath, sTempPath, sBasePath = previousWorking, previousTemp, previousBase
		sUserID, sGroupID = previousUser, previousGroup
	})

	document := `{"outbounds":[{"type":"socks","tag":"n1","server":"127.0.0.1","server_port":1080}]}`
	var firstRequest atomic.Bool
	// The peer's own context ends when the client gives up: that is the observable proof that the
	// connection was closed rather than left waiting for a body nobody will read.
	var peerSawCancellation atomic.Bool
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if requests.Add(1) == 1 {
			firstRequest.Store(true)
			_, _ = writer.Write([]byte(document))
			return
		}
		// Headers never arrive: the provider is mid-download when the budget expires.
		select {
		case <-request.Context().Done():
			peerSawCancellation.Store(true)
		case <-time.After(30 * time.Second):
			_, _ = writer.Write([]byte(document))
		}
	}))
	t.Cleanup(server.Close)

	commandServer, err := NewCommandServer(nil, refreshPlatform{})
	require.NoError(t, err)
	t.Cleanup(commandServer.Close)

	profile := fmt.Sprintf(`{
  "log": {"level": "error"},
  "providers": [{"type": "remote", "tag": "p1", "url": %q, "update_interval": "1h"}],
  "outbounds": [
    {"type": "direct", "tag": "direct"},
    {"type": "selector", "tag": "selected", "providers": ["p1"]}
  ],
  "route": {"final": "direct"}
}`, server.URL)
	require.NoError(t, commandServer.StartOrReloadService(profile, &OverrideOptions{}))

	// The provider's first fetch is what publishes the document; the manual refresh below is the
	// one that must be cut short, so wait for the first one to have happened.
	require.Eventually(t, func() bool {
		return firstRequest.Load()
	}, 5*time.Second, 20*time.Millisecond, "the provider's first fetch must complete")

	started := time.Now()
	summary, err := commandServer.RefreshProviders("p1", 120)
	elapsed := time.Since(started)
	require.NoError(t, err, "a bounded refresh reports its per-provider outcomes instead of failing")
	require.Less(t, elapsed, 5*time.Second,
		"the refresh must end on the caller's budget, not on the provider's own default (%v)", elapsed)
	require.Contains(t, summary.Value, `"status":"cancelled"`,
		"a budget that expired before anything was published is a cancellation, not a failure: %s", summary.Value)
	require.True(t, peerSawCancellation.Load(),
		"the request's own context must be cancelled, which is what closes the connection")
	require.Contains(t, summary.Value, `"tag":"p1"`)
	require.False(t, strings.Contains(summary.Value, `"status":"published"`),
		"nothing was published by this attempt, so nothing may be reported as published")
}
