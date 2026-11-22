package daemon

// E3 (Box level) — the reclamation WIRING: Instance.Close clears the Box-owned URL-test history.
//
// The sweep itself is measured at the level where a sweep is a unit
// (`protocol/group/url_test_sweep_reclamation_test.go`: two full 1000-member sweeps, per-member
// requests, connection reclamation, no per-sweep growth, storage bounded). This file pins the one
// fact only the daemon can show:
//
//	Instance.Close() closes `urlTestHistoryStorage` BEFORE it closes the Box.
//
// That order matters. The Box's scope tears the URLTest groups down, and a group that is still
// writing into a live storage after its Box is gone would resurrect measurements for a Box that no
// longer exists. Closing the storage first makes every later write a no-op, and that is exactly
// what the assertions below check.
//
// The fixture is the smallest product Box with a URLTest outbound; the measurements are written
// through the public storage API rather than by a sweep, because the sweep is already covered and
// this test must not depend on a network round completing.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/protocol/group"

	"github.com/stretchr/testify/require"
)

// crossSystemHistoryProfile is a two-member URLTest Box whose target is a reserved local port that
// refuses immediately: the test never needs a real round, only the storage the Box owns.
func crossSystemHistoryProfile() string {
	return `{
  "log": {"disabled": true},
  "providers": [{"type": "inline", "tag": "nodes", "outbounds": [
    {"type": "direct", "tag": "n0"},
    {"type": "direct", "tag": "n1"}
  ]}],
  "outbounds": [
    {"type": "urltest", "tag": "auto", "providers": ["nodes"], "url": "http://127.0.0.1:1/generate_204",
     "interval": "1h", "idle_timeout": "2h"},
    {"type": "direct", "tag": "direct"}
  ],
  "route": {"final": "direct"}
}`
}

func TestInstanceCloseClearsTheBoxURLTestHistory(t *testing.T) {
	service := NewStartedService(ServiceOptions{Context: crossSystemContext(t, t.TempDir(), &crossSystemRecorder{})})
	t.Cleanup(func() {
		_ = service.CloseService()
		service.Close()
	})
	require.NoError(t, service.StartOrReloadService(context.Background(), crossSystemHistoryProfile(), nil))
	instance := service.instance
	require.NotNil(t, instance)

	// The storage under test is the Box's own: the one the URLTest group was constructed with.
	outbound, loaded := instance.Box().Outbound().Outbound("auto")
	require.True(t, loaded)
	sweepGroup, isURLTest := outbound.(*group.URLTest)
	require.True(t, isURLTest)
	require.Len(t, sweepGroup.All(), 2)

	history := instance.urlTestHistoryStorage
	require.NotNil(t, history)
	scope, err := urltest.NewMeasurementScope("http://127.0.0.1:1/generate_204", urltest.ExpectedStatus{})
	require.NoError(t, err)
	history.StoreHealthHistory("probe-node", scope, &adapter.URLTestHistory{Delay: 7, Time: time.Now()})
	history.StoreDisplayHistory("probe-node", scope, &adapter.URLTestHistory{Delay: 7, Time: time.Now()})
	require.Equal(t, 1, history.HealthEntryCount())
	require.Equal(t, 1, history.DisplayEntryCount())

	coordinator := urltest.CoordinatorFromContext(instance.ctx)
	require.NotNil(t, coordinator)

	// Instance.Close: storage first, Box second.
	require.NoError(t, service.CloseService())
	require.Zero(t, history.HealthEntryCount(),
		"Instance.Close must close the URL-test history storage: a measurement survived the Box")
	require.Zero(t, history.DisplayEntryCount(),
		"Instance.Close must close the URL-test history storage: a display entry survived the Box")

	// A group that was still writing would be refused, not resurrect the measurement.
	history.StoreHealthHistory("late-node", scope, &adapter.URLTestHistory{Delay: 9, Time: time.Now()})
	history.StoreDisplayHistory("late-node", scope, &adapter.URLTestHistory{Delay: 9, Time: time.Now()})
	require.Zero(t, history.HealthEntryCount(), "a closed history storage accepted a health write")
	require.Zero(t, history.DisplayEntryCount(), "a closed history storage accepted a display write")

	// The closed Box admits no new measurement round: the release a reload depends on.
	require.Eventually(t, func() bool {
		return coordinator.RoundsInFlight() == 0 && coordinator.InFlight() == 0
	}, 30*time.Second, 5*time.Millisecond,
		"a round was still admitted after Close: rounds=%d inflight=%d",
		coordinator.RoundsInFlight(), coordinator.InFlight())
	require.Never(t, func() bool {
		return coordinator.RoundsInFlight() > 0 || coordinator.InFlight() > 0
	}, 500*time.Millisecond, 10*time.Millisecond,
		"the closed Box admitted a new measurement round: %s", fmt.Sprint("rounds>0"))
}
