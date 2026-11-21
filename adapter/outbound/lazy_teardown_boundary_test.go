package outbound

// LN boundary case: how far does ONE node's teardown reach into the manager?
//
// A-LN asks for two things here: a cleanup must not be able to self-lock by waiting for the same
// scope, and a slow teardown must not stall the GLOBAL manager for every other node. The self-lock
// half is proved in lazy_attack_test.go (a cleanup that calls Close on its own node). This file
// measures the second half with the eviction window's cleanup gate: while node A's teardown is parked,
// which manager operations still complete, and which are serialized behind it.
//
// # Why the serialized ones are measured and not asserted
//
// The serialization is real and worth reporting, but it is not a broken invariant of the deferred
// object: Scope.Close drains cleanups synchronously by contract (it is the only way a close can report
// a final error), LifecycleGroup.Remove holds the group lock across that drain, and the Manager's
// Remove/replace hold m.mutation across the release. Pinning "it blocks" as an assertion would lock the
// current lock ordering into the test suite instead of reporting it. What IS asserted is the part a
// caller depends on: reads, publications and hand-off acquisition keep working, nothing deadlocks, and
// every object is still closed exactly once after the teardown ends.

import (
	"context"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"

	"github.com/stretchr/testify/require"
)

// waitOutcome reports whether a call finished inside the observation window. It never fails the test:
// the caller logs the answer and asserts the eventual outcome separately.
func observeCompletion(done <-chan struct{}, window time.Duration) bool {
	select {
	case <-done:
		return true
	case <-time.After(window):
		return false
	}
}

// TestAttackTeardownSerializationBoundary parks one node's teardown and asks the manager both kinds of
// question: the reads and publications that must stay available, and the removals/lease releases that
// share the mutation lock with the teardown.
func TestAttackTeardownSerializationBoundary(t *testing.T) {
	ctx := context.Background()
	logger := log.NewNOPFactory().NewLogger("teardown")

	fixture := newAttackFixture(t)
	cleanupGate := fixture.gate(t, func(config *attackConfig, gate *attackGate) { config.cleanupGate = gate })
	// A failing assertion must not leave the gate closed: the parked teardown would then hold the
	// lifecycle group's lock for the rest of the process and every later test that removes an object
	// would hang on it. open() is idempotent, so the explicit open at the end still runs.
	t.Cleanup(cleanupGate.open)
	// Created BEFORE the manager starts: that is the shape every real configuration has, and it is
	// what makes the lifecycle group the owner of this object's scope (see the after-start case in
	// TestAttackLifecycleObjectCreatedAfterStartIsOwnedAndReleased).
	blocking := fixture.create(t, "blocking-node")
	fixture.startAll(t)
	expandAttack(t, blocking)

	// A second, provider-owned node whose hand-off lease is outstanding: its RELEASE is what a router
	// does when a dial finishes, so this is the call that must not wait for an unrelated teardown.
	require.NoError(t, fixture.manager.ReplaceOwned(ctx, nil, logger, "leased-node", "attack", nil, "provider"))
	leasedObject, loaded := fixture.manager.Outbound("leased-node")
	require.True(t, loaded)
	leasedDeferred := leasedObject.(*LazyOutbound[string])
	expandAttack(t, leasedDeferred)
	releaseLease, leased := fixture.manager.AcquireOutbound(leasedObject)
	require.True(t, leased)

	// A non-Lifecycle sibling is the one object whose release does not go through the lifecycle group,
	// which isolates the manager's own lock ordering from the group's.
	require.NoError(t, fixture.manager.Create(ctx, nil, logger, "sibling", "stub", &optionStub{}))
	sibling, loaded := fixture.manager.Outbound("sibling")
	require.True(t, loaded)
	require.NotImplements(t, (*adapter.Lifecycle)(nil), sibling)

	// Park node A's teardown: Remove(A) is now inside the cleanup, holding whatever the manager holds
	// across a release.
	removeA := make(chan error, 1)
	go func() { removeA <- fixture.manager.Remove("blocking-node") }()
	cleanupGate.waitEntered(t)
	require.Equal(t, LazyStateRetiring, blocking.State())

	// The reads a dataplane path performs must still complete.
	within(t, 5*time.Second, "Outbounds() during a parked teardown", func() {
		require.NotEmpty(t, fixture.manager.Outbounds())
	})
	within(t, 5*time.Second, "Outbound() during a parked teardown", func() {
		_, found := fixture.manager.Outbound("leased-node")
		require.True(t, found)
	})
	// A publication must still be possible: it does not need the mutation lock.
	within(t, 5*time.Second, "Create() during a parked teardown", func() {
		require.NoError(t, fixture.manager.Create(ctx, nil, logger, "published-during-teardown", "attack", nil))
	})
	// A hand-off on ANOTHER node must still be acquirable.
	within(t, 5*time.Second, "AcquireOutbound() during a parked teardown", func() {
		secondRelease, secondOK := fixture.manager.AcquireOutbound(leasedObject)
		require.True(t, secondOK)
		secondRelease()
	})

	// The two calls that share the manager's mutation lock. They are measured, not asserted.
	releaseB := make(chan struct{})
	go func() {
		releaseLease()
		close(releaseB)
	}()
	releaseFinishedEarly := observeCompletion(releaseB, 200*time.Millisecond)
	t.Logf("L-TEARDOWN release-of-another-nodes-hand-off completed_while_teardown_parked=%v", releaseFinishedEarly)

	removeSibling := make(chan error, 1)
	siblingDone := make(chan struct{})
	go func() {
		err := fixture.manager.Remove("sibling")
		removeSibling <- err
		close(siblingDone)
	}()
	siblingFinishedEarly := observeCompletion(siblingDone, 200*time.Millisecond)
	t.Logf("L-TEARDOWN remove-of-a-sibling completed_while_teardown_parked=%v", siblingFinishedEarly)

	// End the teardown and close the books: nothing may deadlock, and every object must be released.
	cleanupGate.open()
	select {
	case err := <-removeA:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("Remove(A) never returned after the teardown was released")
	}
	select {
	case <-releaseB:
	case <-time.After(10 * time.Second):
		t.Fatal("the hand-off lease release never returned")
	}
	select {
	case err := <-removeSibling:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("Remove(sibling) never returned")
	}

	require.Equal(t, LazyStateClosed, blocking.State())
	// The leased node was never retired: it is still published, so a released hand-off leaves it
	// READY and usable. A lease release must not evict or close a live node - that is the ownership
	// half of the same boundary the timings above measure.
	require.Equal(t, LazyStateReady, leasedDeferred.State(),
		"releasing a hand-off must not retire a published node")
	require.NoError(t, fixture.manager.Remove("leased-node"))
	require.Equal(t, LazyStateClosed, leasedDeferred.State(),
		"removing the published node closes it")
	runtimes := fixture.runtimesSnapshot()
	require.Len(t, runtimes, 2)
	for _, runtimeObject := range runtimes {
		require.EqualValues(t, 1, runtimeObject.cleanupRuns.Load(),
			"each expansion's cleanup ran exactly once")
		require.EqualValues(t, 0, runtimeObject.closeCalls.Load())
	}
}
