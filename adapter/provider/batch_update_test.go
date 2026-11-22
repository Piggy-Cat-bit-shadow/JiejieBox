package provider_test

// The provider-side contract of the batch publication: an apply that replaces several nodes leaves
// the same observable state a sequence of per-node replacements does (identity, order, records), a
// single node's Start failure does not disturb the others, and the whole apply is one graph event.

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/endpoint"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/adapter/provider"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/observable"
	"github.com/sagernet/sing/service"
	"github.com/stretchr/testify/require"
)

type batchFixture struct {
	provider   *provider.Adapter
	manager    *outbound.Manager
	candidates *[]*testNode
}

func newBatchFixture(t *testing.T) *batchFixture {
	t.Helper()
	ctx := service.ContextWithPtr(context.Background(), urltest.NewHistoryStorage())
	factory := log.NewNOPFactory()
	logger := factory.Logger()
	registry := outbound.NewRegistry()
	candidates := new([]*testNode)
	outbound.Register[nodeOptions](registry, "test", func(_ context.Context, _ adapter.Router, _ log.ContextLogger, tag string, options nodeOptions) (adapter.Outbound, error) {
		node := &testNode{Adapter: outbound.NewAdapter("test", tag, []string{"tcp"}, nil), options: options}
		*candidates = append(*candidates, node)
		return node, nil
	})
	endpointManager := endpoint.NewManager(endpoint.NewRegistry())
	manager := outbound.NewManager(registry, endpointManager, "")
	require.NoError(t, manager.Create(ctx, nil, logger, "static", "test", &nodeOptions{}))
	scope := adapter.NewScope(ctx, logger)
	t.Cleanup(func() { require.NoError(t, scope.Close()) })
	for _, stage := range adapter.ListStartStages {
		require.NoError(t, scope.Start("endpoint", endpointManager, stage))
		require.NoError(t, scope.Start("outbound", manager, stage))
	}
	adapterProvider := provider.NewAdapter(ctx, nil, manager, factory, logger, "subscription", "inline", option.ProviderHealthCheckOptions{})
	require.NoError(t, adapterProvider.Start())
	t.Cleanup(func() { require.NoError(t, adapterProvider.Close()) })
	return &batchFixture{provider: &adapterProvider, manager: manager, candidates: candidates}
}

func revisionDocument(revisions map[string]int, failing map[string]bool) []option.Outbound {
	ordered := []string{"first", "second", "third", "fourth"}
	document := make([]option.Outbound, 0, len(ordered))
	for _, tag := range ordered {
		revision, present := revisions[tag]
		if !present {
			continue
		}
		document = append(document, option.Outbound{
			Type:    "test",
			Tag:     tag,
			Options: &nodeOptions{Revision: revision, Fail: failing[tag]},
		})
	}
	return document
}

// TestProviderBatchApplyKeepsUnchangedNodesAndOrder is the equivalence half: one batch apply has the
// same identity and order effects as the per-node path, and the record snapshot it leaves behind is
// the one the next apply compares against (a re-apply of the same document rebuilds nothing).
func TestProviderBatchApplyKeepsUnchangedNodesAndOrder(t *testing.T) {
	fixture := newBatchFixture(t)
	initial := revisionDocument(map[string]int{"first": 1, "second": 1, "third": 1, "fourth": 1}, nil)
	require.NoError(t, fixture.provider.UpdateNodes(initial))
	first, _ := fixture.provider.Outbound("subscription/first")
	second, _ := fixture.provider.Outbound("subscription/second")
	third, _ := fixture.provider.Outbound("subscription/third")
	fourth, _ := fixture.provider.Outbound("subscription/fourth")

	hook := observable.NewSubscriber[struct{}](16)
	t.Cleanup(func() { _ = hook.Close() })
	fixture.manager.AddGraphUpdateHook(hook)
	subscription, _ := hook.Subscription()
	drainEvents(subscription)

	next := revisionDocument(map[string]int{"first": 1, "second": 2, "third": 1, "fourth": 2}, nil)
	require.NoError(t, fixture.provider.UpdateNodes(next))
	firstAfter, _ := fixture.provider.Outbound("subscription/first")
	secondAfter, _ := fixture.provider.Outbound("subscription/second")
	fourthAfter, _ := fixture.provider.Outbound("subscription/fourth")
	require.Same(t, first, firstAfter, "an unchanged node keeps its object")
	require.NotSame(t, second, secondAfter, "a changed node is replaced")
	require.NotSame(t, fourth, fourthAfter, "a changed node is replaced")
	require.Same(t, third, mustOutbound(t, fixture.provider, "subscription/third"))

	// One batch apply is ONE graph event (the old per-node path emitted one per replacement).
	require.Equal(t, 1, pendingEvents(subscription))

	// The published order is the document order, not the replacement order.
	tags := make([]string, 0, len(fixture.provider.Outbounds()))
	for _, object := range fixture.provider.Outbounds() {
		tags = append(tags, object.Tag())
	}
	require.Equal(t, []string{
		"subscription/first", "subscription/second", "subscription/third", "subscription/fourth",
	}, tags)

	// Applying the same document again must rebuild nothing: the record snapshot describes the
	// objects that are live now.
	candidateCount := len(*fixture.candidates)
	require.NoError(t, fixture.provider.UpdateNodes(next))
	require.Len(t, *fixture.candidates, candidateCount, "a re-applied document must not rebuild a node")
	require.Zero(t, pendingEvents(subscription), "a no-op apply is not a graph change")
}

// TestProviderBatchApplyIsolatesAStartFailure is the per-item half: one node's Start failure keeps
// that node's previous object and does not stop the rest of the batch, exactly like the per-node
// path. The failed candidate is closed through its lifecycle scope, so it cannot leak.
func TestProviderBatchApplyIsolatesAStartFailure(t *testing.T) {
	fixture := newBatchFixture(t)
	initial := revisionDocument(map[string]int{"first": 1, "second": 1, "third": 1}, nil)
	require.NoError(t, fixture.provider.UpdateNodes(initial))
	first, _ := fixture.provider.Outbound("subscription/first")
	second, _ := fixture.provider.Outbound("subscription/second")

	failing := revisionDocument(map[string]int{"first": 2, "second": 3, "third": 2}, map[string]bool{"second": true})
	err := fixture.provider.UpdateNodes(failing)
	require.ErrorContains(t, err, "failed candidate")
	fixtureFirst, _ := fixture.provider.Outbound("subscription/first")
	fixtureSecond, _ := fixture.provider.Outbound("subscription/second")
	fixtureThird, _ := fixture.provider.Outbound("subscription/third")
	require.NotSame(t, first, fixtureFirst, "the other nodes of the batch are still replaced")
	require.Same(t, second, fixtureSecond, "the failed node keeps its previous object")
	require.NotNil(t, fixtureThird)
	require.Zero(t, second.(*testNode).closes.Load(), "the failed node's previous object must stay open")

	// The failed candidate is the object the lifecycle scope owns; it must be closed exactly once
	// even though it never reached the published table.
	var failed *testNode
	for _, candidate := range *fixture.candidates {
		if candidate.Tag() == "subscription/second" && candidate.options.Fail {
			failed = candidate
		}
	}
	require.NotNil(t, failed, "the failing candidate must have been constructed")
	require.EqualValues(t, 1, failed.closes.Load())

	// A later apply with the failure removed publishes the node.
	require.NoError(t, fixture.provider.UpdateNodes(revisionDocument(
		map[string]int{"first": 2, "second": 4, "third": 2}, nil)))
	recovered, _ := fixture.provider.Outbound("subscription/second")
	require.NotSame(t, second, recovered)
}

func mustOutbound(t *testing.T, adapterProvider *provider.Adapter, tag string) adapter.Outbound {
	t.Helper()
	object, found := adapterProvider.Outbound(tag)
	require.True(t, found, "outbound %s is not published", tag)
	return object
}

// drainEvents discards the pending publications so an assertion below is about one apply only.
func drainEvents(subscription <-chan struct{}) {
	for {
		select {
		case <-subscription:
		default:
			return
		}
	}
}

// pendingEvents counts the publications queued for the hook without consuming the fact that the
// subscriber coalesces: the queue length is what a consumer would re-read for.
func pendingEvents(subscription <-chan struct{}) int {
	count := 0
	for {
		select {
		case <-subscription:
			count++
		default:
			return count
		}
	}
}
