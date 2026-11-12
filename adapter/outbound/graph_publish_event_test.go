package outbound

// The publication event is the half of Q13 that lives in the outbound namespace.
//
// The reference manager's eligibility pass is a projection of the published graph, and the only
// runtime notifications it used to have were the URLTest history and the clash mode - both of which
// fire on a SELECTION change. That is a different event from "the graph changed": a provider
// replacing a node under the same tag changes the object behind the tag without moving any
// selection, and a group publishing a member list changes the reference edge set even when the
// member it selects is the same tag as before. These tests pin the event at its source, so the
// consumer in route/reference.go is known to be reading a real signal rather than a test double.

import (
	"testing"

	"github.com/sagernet/sing/common/observable"

	"github.com/stretchr/testify/require"
)

// TestAReplacementEmitsAGraphEvent is the same-tag case: the tag does not move, so nothing in the
// history/clash-mode hooks can observe this change, and only this event tells a consumer that the
// object behind the tag is new.
func TestAReplacementEmitsAGraphEvent(t *testing.T) {
	manager := newGraphManager(t)
	hook := observable.NewSubscriber[struct{}](1)
	t.Cleanup(func() { _ = hook.Close() })
	manager.AddGraphUpdateHook(hook)
	subscription, done := hook.Subscription()

	require.NoError(t, replaceNode(t, manager, "x"))
	require.Equal(t, 1, pendingEvents(subscription),
		"a replacement under an existing tag must emit exactly one graph event")
	select {
	case <-done:
		t.Fatal("the hook was closed by the manager: the subscriber is owned by the caller")
	default:
	}
}

// TestARemovalEmitsAGraphEvent is the shrunken side: a tag that was referenced is gone, so every
// verdict derived from the previous table has to be recomputed.
func TestARemovalEmitsAGraphEvent(t *testing.T) {
	manager := newGraphManager(t)
	hook := observable.NewSubscriber[struct{}](1)
	t.Cleanup(func() { _ = hook.Close() })
	manager.AddGraphUpdateHook(hook)
	subscription, _ := hook.Subscription()

	installOutbound(t, manager, &graphNode{tag: "x"})
	// The install above used Create, which is the static namespace and deliberately silent; drain it
	// so the assertion below is about the removal only.
	drainEvents(subscription)

	require.NoError(t, manager.Remove("x"))
	require.Equal(t, 1, pendingEvents(subscription))
}

// TestACrossKindRefusalEmitsNothing is the negative control on the event's meaning: an emission
// says a change was COMMITTED. A membership publication refused for closing a cycle changes
// nothing, keeps the previous members and selection, and must therefore stay silent - otherwise the
// consumer would recompute against a graph the manager just refused to build.
func TestACrossKindRefusalEmitsNothing(t *testing.T) {
	manager := newGraphManager(t)
	hook := observable.NewSubscriber[struct{}](1)
	t.Cleanup(func() { _ = hook.Close() })
	manager.AddGraphUpdateHook(hook)
	subscription, _ := hook.Subscription()

	installOutbound(t, manager, &graphNode{tag: "a", dependencies: []string{"g"}})
	installOutbound(t, manager, &graphGroup{tag: "g"})
	drainEvents(subscription)

	var committed bool
	err := manager.UpdateGroupMembership("g", []string{"a"}, func() { committed = true })
	require.Error(t, err, "the membership closes a cycle and must be refused")
	require.False(t, committed, "a refused membership must not run its commit")
	require.Zero(t, pendingEvents(subscription),
		"a refused publication changed nothing, so it must not look like a graph change")
}

// TestACommittedMembershipEmitsAGraphEvent is the positive half: the same publication, acyclic,
// commits and emits.
func TestACommittedMembershipEmitsAGraphEvent(t *testing.T) {
	manager := newGraphManager(t)
	hook := observable.NewSubscriber[struct{}](1)
	t.Cleanup(func() { _ = hook.Close() })
	manager.AddGraphUpdateHook(hook)
	subscription, _ := hook.Subscription()

	installOutbound(t, manager, &graphNode{tag: "a"})
	installOutbound(t, manager, &graphGroup{tag: "g"})
	drainEvents(subscription)

	require.NoError(t, manager.UpdateGroupMembership("g", []string{"a"}, func() {}))
	require.Equal(t, 1, pendingEvents(subscription))
}

// TestABurstOfReplacementsCollapsesToOneEvent is the coalescing contract a consumer relies on: it
// re-reads the whole graph, so a provider round replacing twelve nodes owes the consumer twelve
// snapshots at most, and the single-slot subscriber collapses them to one pending wake-up.
func TestABurstOfReplacementsCollapsesToOneEvent(t *testing.T) {
	manager := newGraphManager(t)
	hook := observable.NewSubscriber[struct{}](1)
	t.Cleanup(func() { _ = hook.Close() })
	manager.AddGraphUpdateHook(hook)
	subscription, _ := hook.Subscription()

	for i := 0; i < 12; i++ {
		require.NoError(t, replaceNode(t, manager, "x"))
	}
	require.Equal(t, 1, pendingEvents(subscription),
		"twelve committed replacements must leave one pending wake-up, not twelve")
}

// TestNoHookIsNotAPanic covers the build with no consumer: the manager is also used by embedders
// that never register one, and the emission points must stay free.
func TestNoHookIsNotAPanic(t *testing.T) {
	manager := newGraphManager(t)
	require.NotPanics(t, func() {
		require.NoError(t, replaceNode(t, manager, "x"))
		require.NoError(t, manager.Remove("x"))
	})
}

// TestAClosedHookStopsReceiving is the teardown half: the reference manager closes its subscriber
// with the scope, and a later publication must not panic on it.
func TestAClosedHookStopsReceiving(t *testing.T) {
	manager := newGraphManager(t)
	hook := observable.NewSubscriber[struct{}](1)
	manager.AddGraphUpdateHook(hook)
	subscription, _ := hook.Subscription()
	require.NoError(t, hook.Close())

	require.NotPanics(t, func() { require.NoError(t, replaceNode(t, manager, "x")) })
	require.Zero(t, pendingEvents(subscription))
}

// pendingEvents reports how many events are queued on a subscription without blocking, up to the
// subscriber's own capacity.
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

// drainEvents discards whatever is queued, so a test can assert about the next emission only.
func drainEvents(subscription <-chan struct{}) {
	_ = pendingEvents(subscription)
}
