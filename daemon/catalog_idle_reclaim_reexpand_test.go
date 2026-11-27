package daemon

// CATALOG-01: the node that a URLTest MEASURED must be the node that reclamation RELEASES, and a
// later business request through the production entry must expand it again.
//
// The existing X02 fixture proves probe -> business takeover -> pressure reclamation, but it reclaims a
// SECOND idle node while the business node stays in use, and it closes the Box right after the tail.
// A regression where an evicted node cannot be re-dialled would still pass it. This test takes the
// complementary path: the measured node itself goes idle, is evicted, and then carries a NEW business
// request.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	outboundmgr "github.com/sagernet/sing-box/adapter/outbound"
	resourceguard "github.com/sagernet/sing-box/common/resourceguard"
	"github.com/sagernet/sing-box/common/urltest"

	"github.com/stretchr/testify/require"
)

func TestCatalogAURLTestedNodeIsReclaimedThenReexpandedByNewBusiness(t *testing.T) {
	upstream := startW7ConnectProxy(t)
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/generate_204":
			writer.WriteHeader(http.StatusNoContent)
		case "/business":
			_, _ = writer.Write([]byte("business-ok"))
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(target.Close)

	// The process pressure fact is a real guard the Box's build gate and the reclaim sweep both read.
	var pressure atomic.Int32
	guard := resourceguard.NewGuard(resourceguard.DefaultNetworkExtensionConfig(), func() resourceguard.Level {
		return resourceguard.Level(pressure.Load())
	})
	ctx := resourceguard.NewContext(crossSystemContext(t, t.TempDir(), nil), guard)

	proxyPort := freeLoopbackPort(t)
	service := NewStartedService(ServiceOptions{Context: ctx})
	t.Cleanup(func() {
		_ = service.CloseService()
		service.Close()
	})
	require.NoError(t, service.StartOrReloadService(context.Background(), w7X02Config(proxyPort, upstream.port), nil))
	instance := service.Instance()
	require.NotNil(t, instance)
	boxInstance := instance.Box()

	node, loaded := boxInstance.Outbound().Outbound("node")
	require.True(t, loaded)
	manager, isManager := boxInstance.Outbound().(*outboundmgr.Manager)
	require.True(t, isManager)
	require.Eventually(t, func() bool {
		stats := boxInstance.CatalogStats()
		return stats.Records >= 1 && stats.Refs >= 1 && !stats.Closed
	}, 10*time.Second, 10*time.Millisecond, "the provider node is a catalog record")

	// 1. A REAL URLTest measurement through the same node the business will use.
	probeURL := target.URL + "/generate_204"
	share := urltest.NewMeasurementShare()
	probeCtx := urltest.ContextWithMeasurementShare(context.Background(), share)
	key := urltest.MeasurementKey{
		Node:     "node",
		Target:   probeURL,
		Method:   http.MethodHead,
		Purpose:  urltest.PurposeHealth,
		Path:     "direct",
		Instance: instance.revision,
	}
	measurement, measureErr := urltest.Measure(probeCtx, urltest.MeasureOptions{Link: probeURL, Key: &key}, node)
	require.NoError(t, measureErr, "the URLTest measurement must complete through the catalog node")
	require.Equal(t, http.StatusNoContent, measurement.StatusCode)
	require.Eventually(t, func() bool { return boxInstance.DeferredExpansionCount() == 1 }, 10*time.Second, time.Millisecond,
		"the measurement expanded the node")
	require.Eventually(t, func() bool {
		observation := w7Observation(t, node)
		return observation.Expanded && observation.Conns == 0 && observation.Leased == 0
	}, 10*time.Second, time.Millisecond, "the measured node is fully idle: %+v", w7Observation(t, node))
	t.Logf("CATALOG-01 step1: measured through node, expansions=%d observation=%+v",
		boxInstance.DeferredExpansionCount(), w7Observation(t, node))

	// 2. Pressure reclamation must release the MEASURED node itself, not a bystander.
	manager.SetDeferredReclaimPolicy(outboundmgr.DeferredReclaimPolicy{
		MaxExpanded:           1,
		MaxBytes:              1,
		AssumedExpansionBytes: 3 * 1024,
		MaxEvictionsPerSweep:  16,
		MinInterval:           time.Hour,
	})
	pressure.Store(int32(resourceguard.LevelCritical))
	require.Eventually(t, func() bool { return guard.Level() == resourceguard.LevelCritical }, time.Second, time.Millisecond)

	sweep := manager.ReclaimDeferredExpansions(ctx, outboundmgr.ReclaimCauseExplicit)
	require.Equal(t, resourceguard.LevelCritical, sweep.Level)
	require.GreaterOrEqual(t, sweep.Evicted, 1, "the idle measured node must be reclaimed under pressure: %+v", sweep)
	require.Eventually(t, func() bool { return boxInstance.DeferredExpansionCount() == 0 }, 10*time.Second, time.Millisecond,
		"the reclaimed node holds no expansion")
	reclaimed := w7Observation(t, node)
	require.False(t, reclaimed.Expanded, "the measured node's runtime must be gone: %+v", reclaimed)
	require.Zero(t, reclaimed.Conns)
	require.Equal(t, 1, boxInstance.CatalogStats().Records, "reclamation releases the runtime, not the descriptor")
	t.Logf("CATALOG-01 step2: evicted, observation=%+v", reclaimed)

	// 3. A NEW business request through the PRODUCTION entry must expand it again and succeed.
	pressure.Store(int32(resourceguard.LevelNormal))
	require.Eventually(t, func() bool { return guard.Level() == resourceguard.LevelNormal }, 15*time.Second, 10*time.Millisecond,
		"raising the pressure ceiling must not be a one-way door")
	client := newProxyClient(t, proxyPort)
	defer client.CloseIdleConnections()
	response, requestErr := client.Get(target.URL + "/business")
	require.NoError(t, requestErr, "business must work after the measured node was reclaimed")
	body, bodyErr := io.ReadAll(response.Body)
	response.Body.Close()
	require.NoError(t, bodyErr)
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Equal(t, "business-ok", string(body))

	require.Eventually(t, func() bool { return boxInstance.DeferredExpansionCount() == 1 }, 10*time.Second, time.Millisecond,
		"the new business request must re-expand the reclaimed node")
	reexpanded := w7Observation(t, node)
	require.True(t, reexpanded.Expanded, "the node the business used is expanded again: %+v", reexpanded)
	require.GreaterOrEqual(t, reexpanded.Conns, 1, "the new business connection belongs to the new expansion: %+v", reexpanded)
	t.Logf("CATALOG-01 step3: re-expanded by new business, observation=%+v", reexpanded)

	// 4. Identity and ownership converge on Close.
	require.NoError(t, service.CloseService())
	closed := boxInstance.CatalogStats()
	require.True(t, closed.Closed)
	require.Zero(t, closed.Records)
	require.Zero(t, closed.Refs)
	require.Zero(t, boxInstance.DeferredExpansionCount())

}
