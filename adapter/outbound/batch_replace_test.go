package outbound

// The batch publication's contract at the manager: one graph verdict, one table swap, one event,
// per-item failure isolation, and the same retirement rule a sequence of ReplaceOwned calls starts.

import (
	"context"
	"errors"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound/batch"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/common/observable"
	"github.com/stretchr/testify/require"
)

func leafReplacement(tag string) batch.Replacement {
	return batch.Replacement{Tag: tag, Type: "test", Options: &graphNodeOptions{}}
}

func batchLogger() log.ContextLogger { return log.NewNOPFactory().NewLogger("test") }

// TestReplaceOwnedBatchEqualsPerNodeStateAndRetirement pins the observable equivalence: after one
// batch the published table, the slice order and the retirement rule are the same as N ReplaceOwned
// calls, but the graph event is emitted once for the whole batch.
func TestReplaceOwnedBatchEqualsPerNodeStateAndRetirement(t *testing.T) {
	manager := NewManager(&graphRegistry{}, nil, "")
	olds := map[string]*graphNode{
		"p/a": {tag: "p/a"},
		"p/b": {tag: "p/b"},
		"p/c": {tag: "p/c"},
	}
	// Installed in a fixed order: the slice order assertion below is part of the contract, so the
	// fixture must not depend on map iteration order.
	installOutbound(t, manager, olds["p/a"])
	installOutbound(t, manager, olds["p/b"])
	installOutbound(t, manager, olds["p/c"])
	hook := observable.NewSubscriber[struct{}](8)
	t.Cleanup(func() { _ = hook.Close() })
	manager.AddGraphUpdateHook(hook)
	subscription, _ := hook.Subscription()
	drainEvents(subscription)

	outcomes := manager.ReplaceOwnedBatch(context.Background(), nil, batchLogger(),
		[]batch.Replacement{leafReplacement("p/a"), leafReplacement("p/b"), leafReplacement("p/d")}, "p")
	require.Len(t, outcomes, 3)
	for index, outcome := range outcomes {
		require.NoError(t, outcome, "item %d", index)
	}

	published := map[string]adapter.Outbound{}
	for _, object := range manager.Outbounds() {
		published[object.Tag()] = object
	}
	require.Len(t, published, 4, "p/a, p/b, p/c and the appended p/d")
	require.NotSame(t, olds["p/a"], published["p/a"])
	require.NotSame(t, olds["p/b"], published["p/b"])
	require.Same(t, olds["p/c"], published["p/c"], "a tag outside the batch must not move")
	require.NotNil(t, published["p/d"], "a tag the batch adds must be published")

	// The two replaced objects are RETIRED with the owner, not closed at the swap: a dial in the
	// group-callback window must still find a working object.
	require.Zero(t, olds["p/a"].closed.Load())
	require.Zero(t, olds["p/b"].closed.Load())
	require.Equal(t, 1, pendingEvents(subscription),
		"a batch emits ONE graph event, not one per replacement")

	require.NoError(t, manager.CompletePublication("p", []string{"p/a", "p/b"}))
	require.EqualValues(t, 1, olds["p/a"].closed.Load())
	require.EqualValues(t, 1, olds["p/b"].closed.Load())
	require.Zero(t, olds["p/c"].closed.Load())

	// The slice order is the previous order with substitutions in place: a consumer that keys on
	// position cannot be silently reordered by a batch.
	tags := make([]string, 0, len(manager.Outbounds()))
	for _, object := range manager.Outbounds() {
		tags = append(tags, object.Tag())
	}
	require.Equal(t, []string{"p/a", "p/b", "p/c", "p/d"}, tags)
}

// TestReplaceOwnedBatchKeepsThePreviousObjectsWhenOneVerdictIsRefused: an edge-bearing item is
// validated after Start, exactly like ReplaceOwned. A cycle it would close refuses THAT item, the
// previous object stays published and open, and the edge-free items in the same call are published.
func TestReplaceOwnedBatchKeepsThePreviousObjectsWhenOneVerdictIsRefused(t *testing.T) {
	manager := newGraphManager(t)
	group := &graphGroup{tag: "g"}
	group.setMembers([]string{"p/a"}, "p/a")
	installOutbound(t, manager, group)
	oldA := &graphNode{tag: "p/a"}
	oldB := &graphNode{tag: "p/b"}
	installOutbound(t, manager, oldA)
	installOutbound(t, manager, oldB)

	outcomes := manager.ReplaceOwnedBatch(context.Background(), nil, batchLogger(), []batch.Replacement{
		{Tag: "p/b", Type: "test", Options: &graphNodeOptions{}},
		{Tag: "p/a", Type: "test", Options: &graphNodeOptions{Dependencies: []string{"g"}}},
	}, "p")
	require.Len(t, outcomes, 2)
	require.NoError(t, outcomes[0], "the edge-free item must still be published")
	require.Error(t, outcomes[1])
	require.Contains(t, outcomes[1].Error(), "circular")

	currentA, _ := manager.Outbound("p/a")
	require.Same(t, oldA, currentA, "a refused replacement must leave the old object published")
	require.Zero(t, oldA.closed.Load(), "the refused object's predecessor must stay open")
	currentB, _ := manager.Outbound("p/b")
	require.NotSame(t, oldB, currentB, "the other item of the call must be published")
}

// TestReplaceOwnedBatchRollsBackWhenTheUnionVerdictIsRefused reaches the all-or-nothing branch the
// edge-free path is built around: a refused verdict about the union must release every candidate and
// leave the table, the slice and the previous objects untouched. An edge-free set cannot close a
// cycle, so the production path reaches this branch only through a cross-kind refusal; the test
// injects the refusal through the same seam the exported method fills with validateGraphLocked.
func TestReplaceOwnedBatchRollsBackWhenTheUnionVerdictIsRefused(t *testing.T) {
	registry := &graphRegistry{}
	manager := NewManager(registry, nil, "")
	old := &graphNode{tag: "p/a"}
	installOutbound(t, manager, old)
	hook := observable.NewSubscriber[struct{}](8)
	t.Cleanup(func() { _ = hook.Close() })
	manager.AddGraphUpdateHook(hook)
	subscription, _ := hook.Subscription()
	drainEvents(subscription)

	refusal := errors.New("union verdict refused")
	outcomes := manager.replaceOwnedBatch(context.Background(), nil, batchLogger(),
		[]batch.Replacement{leafReplacement("p/a")}, "p",
		func(graphProposal) error { return refusal })
	require.Len(t, outcomes, 1)
	require.ErrorIs(t, outcomes[0], refusal)

	current, _ := manager.Outbound("p/a")
	require.Same(t, old, current, "a refused union must publish nothing")
	require.Zero(t, old.closed.Load(), "the previous object must stay open")
	require.Len(t, registry.created, 1, "the refused candidate is the only constructed object")
	require.EqualValues(t, 1, registry.created[0].closed.Load(), "the refused candidate is released")
	require.Zero(t, pendingEvents(subscription), "a refused union must not emit a graph event")
}

// TestReplaceOwnedBatchKeepsTheDefaultSelection moves the default outbound with the object behind
// its tag, exactly as ReplaceOwned does.
func TestReplaceOwnedBatchKeepsTheDefaultSelection(t *testing.T) {
	manager := NewManager(&graphRegistry{}, nil, "p/a")
	old := &graphNode{tag: "p/a"}
	installOutbound(t, manager, old)
	manager.access.Lock()
	manager.defaultOutbound = old
	manager.access.Unlock()

	outcomes := manager.ReplaceOwnedBatch(context.Background(), nil, batchLogger(),
		[]batch.Replacement{leafReplacement("p/a")}, "p")
	require.NoError(t, outcomes[0])
	current, _ := manager.Outbound("p/a")
	require.NotSame(t, old, current)
	require.Same(t, current, manager.Default())
}

// TestReplaceOutboundsIsCopyOnWrite pins the property the Outbounds contract depends on: the batch
// builds ONE new slice and never writes the array a reader may still be walking.
func TestReplaceOutboundsIsCopyOnWrite(t *testing.T) {
	a, b, c := &graphNode{tag: "a"}, &graphNode{tag: "b"}, &graphNode{tag: "c"}
	replacement := &graphNode{tag: "b"}
	current := []adapter.Outbound{a, b, c}
	next := replaceOutbounds(current, map[string]adapter.Outbound{"b": replacement}, []string{"b"})
	require.Equal(t, []adapter.Outbound{a, b, c}, current, "the previous slice must not be mutated")
	require.Equal(t, []adapter.Outbound{a, replacement, c}, next)

	added := &graphNode{tag: "d"}
	next = replaceOutbounds(current, map[string]adapter.Outbound{"d": added}, []string{"d"})
	require.Equal(t, []adapter.Outbound{a, b, c, added}, next)
}
