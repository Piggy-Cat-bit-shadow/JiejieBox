package libbox

// PP03/PP07 — the manual refresh end to end: the platform entry, the daemon's live-Box refresh
// path, a real remote provider and a real HTTP request, with no stand-ins between them.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// refreshPlatform is the platform surface the command server constructor consumes. Everything
// it does not implement answers "no platform capability", which is the honest answer here.
type refreshPlatform struct {
	PlatformInterface
}

func (refreshPlatform) LocalDNSTransport() LocalDNSTransport { return nil }

func (refreshPlatform) UseProcFS() bool { return false }

func (refreshPlatform) UsePlatformAutoDetectInterfaceControl() bool { return false }

// The network manager asks the platform to watch the default interface once a Box starts. This
// fixture claims no platform capability, so the monitor is a no-op that reports nothing.
func (refreshPlatform) StartDefaultInterfaceMonitor(InterfaceUpdateListener) error { return nil }

func (refreshPlatform) CloseDefaultInterfaceMonitor(InterfaceUpdateListener) error { return nil }

func (refreshPlatform) UnderNetworkExtension() bool { return false }

func (refreshPlatform) IncludeAllNetworks() bool { return false }

func TestRefreshProvidersEndToEnd(t *testing.T) {
	var requests atomic.Int32
	document := `{"outbounds":[{"type":"socks","tag":"n1","server":"127.0.0.1","server_port":1080}]}`
	documentServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = writer.Write([]byte(document))
	}))
	t.Cleanup(documentServer.Close)

	// The command server derives its working directory from package state; a test gets its own
	// so the cache-file service does not wait for a directory it will never be given.-1 means
	// "do not chown", which is what every non-root platform passes.
	previousWorking, previousTemp := sWorkingPath, sTempPath
	previousUser, previousGroup := sUserID, sGroupID
	sWorkingPath, sTempPath = t.TempDir(), t.TempDir()
	sUserID, sGroupID = -1, -1
	t.Cleanup(func() {
		sWorkingPath, sTempPath = previousWorking, previousTemp
		sUserID, sGroupID = previousUser, previousGroup
	})

	commandServer, err := NewCommandServer(nil, refreshPlatform{})
	require.NoError(t, err)
	t.Cleanup(commandServer.Close)

	configContent := fmt.Sprintf(`{"log":{"level":"error"},"providers":[{"type":"remote","tag":"p","url":%q,"update_interval":"1h"}],"outbounds":[{"type":"direct","tag":"direct"},{"type":"selector","tag":"sel","providers":["p"]}],"route":{"final":"direct"}}`, documentServer.URL)
	require.NoError(t, commandServer.StartOrReloadService(configContent, &OverrideOptions{}),
		"the service must start with a remote provider and a selector over it")

	before := requests.Load()
	boxed, err := commandServer.RefreshProviders("p", 5000)
	require.NoError(t, err)
	require.NotNil(t, boxed)
	require.Greater(t, requests.Load(), before, "the manual refresh must reach the provider's document server")
	require.Contains(t, boxed.Value, `"tag":"p"`)
	require.Contains(t, boxed.Value, `"status":"published"`, "a fetched document is reported as published")
}
