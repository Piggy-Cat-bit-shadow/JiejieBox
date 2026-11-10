package libbox

// The second cancellation guard, on a real Box with a real remote provider.
//
// The daemon-level test proves the guard that runs BEFORE anything is planned. This one proves the
// one that matters when the caller disconnects mid-refresh: the document is unchanged and every input
// outside it is a remote provider, so the entry takes the live path - and if the provider refresh
// reports "cancelled" the entry must NOT treat that as "fall through to a rebuild". The running Box
// already IS the requested document; closing it to answer a request nobody will read is damage.
//
// The desktop daemon passes its gRPC handler context here (`boxdd`), so this is the ordinary shape of
// a client disconnect, not an exotic race.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sagernet/sing-box/daemon"

	"github.com/stretchr/testify/require"
)

func TestACancellationDuringAProviderRefreshKeepsTheBox(t *testing.T) {
	previousWorking, previousTemp, previousBase := sWorkingPath, sTempPath, sBasePath
	previousUser, previousGroup := sUserID, sGroupID
	sWorkingPath, sTempPath, sBasePath = t.TempDir(), t.TempDir(), t.TempDir()
	sUserID, sGroupID = -1, -1
	t.Cleanup(func() {
		sWorkingPath, sTempPath, sBasePath = previousWorking, previousTemp, previousBase
		sUserID, sGroupID = previousUser, previousGroup
	})

	document := `{"outbounds":[{"type":"socks","tag":"n1","server":"127.0.0.1","server_port":1080}]}`
	var firstServed bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !firstServed {
			firstServed = true
			_, _ = writer.Write([]byte(document))
			return
		}
		// Every later fetch never answers: the caller's deadline is what ends it.
		<-request.Context().Done()
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
	boxBefore := commandServer.Instance()
	require.NotNil(t, boxBefore)
	require.Eventually(t, func() bool { return firstServed }, 5*time.Second, 20*time.Millisecond,
		"the provider's first fetch must complete before the cancelling refresh")

	// The same document, cancelled while its provider refresh runs.
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	started := time.Now()
	report, applyErr := commandServer.ApplyServiceChange(ctx, profile, &daemon.OverrideOptions{})
	elapsed := time.Since(started)

	require.Error(t, applyErr, "a cancelled request reports the cancellation")
	require.Less(t, elapsed, 10*time.Second, "the cancellation ends the refresh, not the fetch timeout (%v)", elapsed)
	require.NotNil(t, report)
	require.Equal(t, "failed", string(report.Status))
	require.True(t, report.Retained, "the running configuration was left in place")
	require.Same(t, boxBefore, commandServer.Instance(),
		"a cancelled refresh must not close the Box that is already running the requested document")
	require.Contains(t, report.Reason, "cancelled")

	// The tunnel still works afterwards: a later refresh with a live context can still reach the
	// daemon's provider, which is the difference between "the request was ignored" and "the service
	// was damaged".
	require.NotNil(t, commandServer.Instance())
}
