package daemon

// The thousand-node churn test changes node n1 every round while holding a connection through n0,
// so it proves "an unchanged node's flow survives its neighbours being replaced". The independent
// review pointed out what that does NOT prove: that a node which is IN USE can be replaced, with the
// old flow staying on the old runtime and a new dial reaching the new one.
//
// This test closes that gap with observable upstreams instead of counters: two HTTP CONNECT proxies
// stand in for the same tag's old and new configuration, and the assertion is which proxy actually
// carried the bytes.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

func TestX03BTheNodeInUseIsReplacedOldFlowStaysOnTheOldUpstream(t *testing.T) {
	const nodes = 8

	connectA := startW7ConnectProxy(t)
	connectB := startW7ConnectProxy(t)
	holdStarted := make(chan struct{}, 8)
	holdRelease := make(chan struct{})
	var releaseHold func()
	releaseHold = func() { close(holdRelease) }
	t.Cleanup(func() {
		defer func() { _ = recover() }()
		releaseHold()
	})
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/hold" {
			flusher, _ := writer.(http.Flusher)
			_, _ = writer.Write([]byte("held-first\n"))
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
			_, _ = writer.Write([]byte("held-last\n"))
			if flusher != nil {
				flusher.Flush()
			}
			return
		}
		_, _ = writer.Write([]byte("ok"))
	}))
	t.Cleanup(target.Close)

	// n0 starts on upstream A.
	library := newW7LibraryServer(t, w7LibraryDocument(nodes, connectA.port, -1, 0))
	proxyPort := freeLoopbackPort(t)
	service := NewStartedService(ServiceOptions{Context: crossSystemContext(t, t.TempDir(), nil)})
	t.Cleanup(func() {
		_ = service.CloseService()
		service.Close()
	})
	require.NoError(t, service.StartOrReloadService(context.Background(),
		w7LibraryProfile(proxyPort, library.server.URL, "error", filepath.Join(t.TempDir(), "cache.db")), nil))
	instance := service.Instance()
	require.NotNil(t, instance)
	boxInstance := instance.Box()
	_, err := service.RefreshProviders(context.Background(), []string{"lib"}, 30*time.Second)
	require.NoError(t, err)

	nodeObject, loaded := boxInstance.Outbound().Outbound("lib/n0")
	require.True(t, loaded)
	heldResponse, heldReader := w7LibraryDial(t, nodeObject, target.URL)
	select {
	case <-holdStarted:
	case <-time.After(20 * time.Second):
		t.Fatal("the held connection never reached the target")
	}
	require.Equal(t, "held-first\n", heldReader.nextLine(t))
	require.Eventually(t, func() bool { return connectA.tunnels.Load() == 1 }, 5*time.Second, 5*time.Millisecond,
		"the held connection must have gone through upstream A")
	require.Zero(t, connectB.tunnels.Load())

	// Replace the node that is IN USE: same tag, new upstream.
	library.setDocument(w7LibraryDocument(nodes, connectA.port, 0, connectB.port))
	summary, refreshErr := service.RefreshProviders(context.Background(), []string{"lib"}, 30*time.Second)
	require.NoError(t, refreshErr)
	require.Equal(t, ProviderRefreshPublished, summary.Results[0].Status,
		"replacing the in-use node must publish: %+v", summary.Results[0])

	// 1. The OLD flow keeps reading its tail from the OLD upstream.
	releaseHold()
	require.Equal(t, "held-last\n", heldReader.nextLine(t),
		"the flow that was open before the replacement must keep its own runtime")
	heldResponse.Body.Close()
	require.Eventually(t, func() bool { return connectA.tunnels.Load() == 1 }, 5*time.Second, 5*time.Millisecond,
		"the held flow must not have moved to the new upstream")
	require.Zero(t, connectB.tunnels.Load(), "the old flow must never use the replacement upstream")

	// 2. A NEW dial through the same tag resolves to the NEW upstream.
	liveNode, stillLoaded := boxInstance.Outbound().Outbound("lib/n0")
	require.True(t, stillLoaded)
	dialCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	freshConn, dialErr := liveNode.DialContext(dialCtx, "tcp", M.ParseSocksaddr(target.Listener.Addr().String()))
	cancel()
	require.NoError(t, dialErr)
	_ = freshConn.Close()
	require.Eventually(t, func() bool { return connectB.tunnels.Load() == 1 }, 5*time.Second, 5*time.Millisecond,
		"a new dial through the replaced tag must use the new upstream")
	require.EqualValues(t, 1, connectA.tunnels.Load(),
		"the old upstream must not receive the new dial")

	// 3. A no-op document (same configuration) must not disturb either.
	library.setDocument(w7LibraryDocument(nodes, connectA.port, 0, connectB.port))
	summary, refreshErr = service.RefreshProviders(context.Background(), []string{"lib"}, 30*time.Second)
	require.NoError(t, refreshErr)
	require.Equal(t, ProviderRefreshPublished, summary.Results[0].Status)
	liveNode, stillLoaded = boxInstance.Outbound().Outbound("lib/n0")
	require.True(t, stillLoaded)
	dialCtx, cancel = context.WithTimeout(context.Background(), 15*time.Second)
	freshConn, dialErr = liveNode.DialContext(dialCtx, "tcp", M.ParseSocksaddr(target.Listener.Addr().String()))
	cancel()
	require.NoError(t, dialErr)
	_ = freshConn.Close()
	require.Eventually(t, func() bool { return connectB.tunnels.Load() == 2 }, 5*time.Second, 5*time.Millisecond,
		"a repeated identical document must keep resolving to the same upstream")

	// 4. Closing the Box releases the records the replacement created.
	require.NoError(t, service.CloseService())
	closed := boxInstance.CatalogStats()
	require.True(t, closed.Closed)
	require.Zero(t, closed.Records)
	require.Zero(t, closed.Refs)
}
