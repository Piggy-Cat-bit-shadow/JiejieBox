package provider_test

// A measurement is a fact about ONE object, and the URL-test history is keyed by TAG. A subscription
// update is free to keep the tag and point it at a different server - that is the ordinary case, not
// the exotic one - and when it does, the stored delay describes an object that no longer exists.
//
// Three readers then treat it as this object's measurement: the URL-test group's ordering (a dead
// node inherits the old low delay and is selected), the dashboard (a node that was never probed in
// this configuration displays a delay), and any health-based failover. The router clears history when
// a handshake FAILS (route/conn.go:190), which cannot help a node that was replaced while idle, and
// the provider's own failed health check clears it only after the next probe.
//
// These tests pin the two lifecycle edges: replaced, and removed.

import (
	"context"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/endpoint"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/adapter/provider"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/service"
	"github.com/stretchr/testify/require"
)

// newHistoryFixture is the program from hot_update_test.go, reduced to what a history test needs.
func newHistoryFixture(t *testing.T) (*provider.Adapter, *urltest.HistoryStorage) {
	t.Helper()
	history := urltest.NewHistoryStorage()
	ctx := service.ContextWithPtr(context.Background(), history)
	factory := log.NewNOPFactory()
	logger := factory.Logger()
	registry := outbound.NewRegistry()
	outbound.Register[nodeOptions](registry, "test", func(_ context.Context, _ adapter.Router, _ log.ContextLogger, tag string, options nodeOptions) (adapter.Outbound, error) {
		return &testNode{Adapter: outbound.NewAdapter("test", tag, []string{"tcp"}, nil), options: options}, nil
	})
	ep := endpoint.NewManager(endpoint.NewRegistry())
	manager := outbound.NewManager(registry, ep, "")
	require.NoError(t, manager.Create(ctx, nil, logger, "static", "test", &nodeOptions{}))
	scope := adapter.NewScope(ctx, logger)
	t.Cleanup(func() { _ = scope.Close() })
	for _, stage := range adapter.ListStartStages {
		require.NoError(t, scope.Start("endpoint", ep, stage))
		require.NoError(t, scope.Start("outbound", manager, stage))
	}
	p := provider.NewAdapter(ctx, nil, manager, factory, logger, "subscription", "inline", option.ProviderHealthCheckOptions{})
	require.NoError(t, p.Start())
	t.Cleanup(func() { _ = p.Close() })
	// The constructor returns the adapter by value; the pointer keeps the test from copying its
	// mutexes, which the fixture in hot_update_test.go avoids by using the value inline.
	return &p, history
}

func seedHistory(history *urltest.HistoryStorage, tag string, delay uint16) {
	history.StoreURLTestHistory(tag, &adapter.URLTestHistory{Time: time.Now(), Delay: delay})
}

// TestAReplacedNodeDoesNotInheritItsPredecessorsMeasurement is the replacement edge.
func TestAReplacedNodeDoesNotInheritItsPredecessorsMeasurement(t *testing.T) {
	p, history := newHistoryFixture(t)
	p.UpdateNodes([]option.Outbound{
		{Type: "test", Tag: "kept", Options: &nodeOptions{Revision: 1}},
		{Type: "test", Tag: "moved", Options: &nodeOptions{Revision: 1}},
	})
	seedHistory(history, "subscription/kept", 11)
	seedHistory(history, "subscription/moved", 22)

	p.UpdateNodes([]option.Outbound{
		{Type: "test", Tag: "kept", Options: &nodeOptions{Revision: 1}},
		{Type: "test", Tag: "moved", Options: &nodeOptions{Revision: 2}},
	})

	require.NotNil(t, history.LoadURLTestHistory("subscription/kept"),
		"an unchanged node must keep its measurement: it still describes the same object")
	require.Nil(t, history.LoadURLTestHistory("subscription/moved"),
		"a replaced node's stored delay described the object that was replaced; presenting it as "+
			"this node's measurement makes a never-probed server look measured, and lets a URL-test "+
			"group select it on evidence about a server that is gone")
}

// TestARemovedNodeLeavesNoMeasurementBehind is the removal edge. A removed node is not merely hidden:
// its tag is free to be reused by the next subscription, and a leftover delay would then be inherited
// by an unrelated object.
func TestARemovedNodeLeavesNoMeasurementBehind(t *testing.T) {
	p, history := newHistoryFixture(t)
	p.UpdateNodes([]option.Outbound{
		{Type: "test", Tag: "kept", Options: &nodeOptions{Revision: 1}},
		{Type: "test", Tag: "dropped", Options: &nodeOptions{Revision: 1}},
	})
	seedHistory(history, "subscription/kept", 11)
	seedHistory(history, "subscription/dropped", 33)

	p.UpdateNodes([]option.Outbound{
		{Type: "test", Tag: "kept", Options: &nodeOptions{Revision: 1}},
	})

	require.NotNil(t, history.LoadURLTestHistory("subscription/kept"))
	require.Nil(t, history.LoadURLTestHistory("subscription/dropped"),
		"a removed node must not leave a measurement behind for the next object that takes its tag")

	// And a reused tag starts unmeasured.
	p.UpdateNodes([]option.Outbound{
		{Type: "test", Tag: "kept", Options: &nodeOptions{Revision: 1}},
		{Type: "test", Tag: "dropped", Options: &nodeOptions{Revision: 9}},
	})
	require.Nil(t, history.LoadURLTestHistory("subscription/dropped"),
		"a tag that comes back must start unmeasured, not with the delay of the node that used it")
}
