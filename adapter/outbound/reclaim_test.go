package outbound

// The reclamation policy: who releases an expanded node, when, and under what budget.
//
// The tests are written against the DECISIONS, not against the implementation: an expansion is chosen
// only when nothing is using it, the least recently used go first, the budget is a hard upper bound,
// and a sweep never builds or dials.

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/resourceguard"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// expand dials a node once and closes the connection, which is exactly what a URLTest round does to
// it: the node ends up expanded, published, and idle.
func expand(t *testing.T, lazy *LazyOutbound[string]) {
	t.Helper()
	conn, err := lazy.DialContext(context.Background(), "tcp", M.Socksaddr{})
	require.NoError(t, err)
	require.NoError(t, conn.Close())
}

// createExpanded publishes count deferred nodes and expands every one of them.
//
// It creates them under a PERMISSIVE budget on purpose: an expansion past the budget asks for a sweep
// (that is the demand trigger, and it is what bounds a real probe round), so a test that armed its
// tight budget first would be racing that sweep while it built its corpus. The test arms the budget it
// wants to exercise AFTER the corpus exists.
func createExpanded(t *testing.T, fixture *deferredFixture, count int) []*LazyOutbound[string] {
	t.Helper()
	fixture.manager.SetDeferredReclaimPolicy(budgetPolicy(1 << 20))
	nodes := make([]*LazyOutbound[string], count)
	for index := range nodes {
		nodes[index] = fixture.create(t, fmt.Sprintf("node-%02d", index))
		expand(t, nodes[index])
	}
	require.Equal(t, count, fixture.manager.DeferredExpansionCount())
	return nodes
}

func budgetPolicy(maxExpanded int) DeferredReclaimPolicy {
	policy := DefaultDeferredReclaimPolicy()
	policy.MaxExpanded = maxExpanded
	policy.MaxEvictionsPerSweep = 32
	policy.MinInterval = 0
	return policy
}

// TestReclaimEvictsTheLeastRecentlyUsedDownToTheBudget is the core of the answer to "who, when, how
// much": one sweep takes the expansion count down to the budget, oldest use first, and the nodes that
// stay expanded are exactly the most recently used ones.
func TestReclaimEvictsTheLeastRecentlyUsedDownToTheBudget(t *testing.T) {
	fixture := newDeferredFixture(t)
	fixture.start(t)
	nodes := createExpanded(t, fixture, 10)
	fixture.manager.SetDeferredReclaimPolicy(budgetPolicy(3))

	result := fixture.manager.ReclaimDeferredExpansions(context.Background(), ReclaimCauseExplicit)
	require.Equal(t, ReclaimCauseExplicit, result.Cause)
	require.Equal(t, resourceguard.LevelNormal, result.Level)
	require.Equal(t, 10, result.Expanded)
	require.Equal(t, 3, result.Target)
	require.Equal(t, 7, result.Evicted)
	require.Equal(t, 3, fixture.manager.DeferredExpansionCount())

	for index, node := range nodes {
		_, expanded := node.Built()
		if index < 7 {
			require.False(t, expanded, "node %d was used least recently and must be the one released", index)
		} else {
			require.True(t, expanded, "node %d is inside the budget and must stay expanded", index)
		}
	}
	// A sweep releases; it never rebuilds.
	require.EqualValues(t, 10, fixture.builds.Load())
}

// TestReclaimNeverEvictsAnInUseExpansion is the rule that makes the policy safe: a manager lease, a
// dial in flight or an open connection makes a node ineligible, and the budget simply cannot be met by
// that node - it is not forced.
func TestReclaimNeverEvictsAnInUseExpansion(t *testing.T) {
	fixture := newDeferredFixture(t)
	fixture.start(t)
	nodes := createExpanded(t, fixture, 4)
	fixture.manager.SetDeferredReclaimPolicy(budgetPolicy(2))

	releaseFirst, ok := fixture.manager.AcquireOutbound(nodes[0])
	require.True(t, ok)
	defer releaseFirst()
	conn, err := nodes[1].DialContext(context.Background(), "tcp", M.Socksaddr{})
	require.NoError(t, err)
	defer conn.Close()

	result := fixture.manager.ReclaimDeferredExpansions(context.Background(), ReclaimCauseExplicit)
	require.Equal(t, 4, result.Expanded)
	require.Equal(t, 2, result.InUse, "one leased node and one node with an open connection")
	require.Equal(t, 2, result.Evicted)
	require.Equal(t, 2, fixture.manager.DeferredExpansionCount())

	_, expandedFirst := nodes[0].Built()
	_, expandedSecond := nodes[1].Built()
	require.True(t, expandedFirst, "a lease pins the expansion")
	require.True(t, expandedSecond, "an open connection pins the expansion")
	_, expandedThird := nodes[2].Built()
	require.False(t, expandedThird)
}

// TestReclaimScalesWithTheExistingPressureLevel proves the budget reuses the process's ONE pressure
// fact: the same resourceguard level the admission gate and the build reservation read. no second
// sample, no second timer.
func TestReclaimScalesWithTheExistingPressureLevel(t *testing.T) {
	fixture := newDeferredFixture(t)
	fixture.start(t)
	var level atomic.Int32
	fixture.manager.SetDeferredReclaimPressure(func(context.Context) resourceguard.Level {
		return resourceguard.Level(level.Load())
	})
	createExpanded(t, fixture, 10)
	fixture.manager.SetDeferredReclaimPolicy(budgetPolicy(8))

	normal := fixture.manager.ReclaimDeferredExpansions(context.Background(), ReclaimCauseExplicit)
	require.Equal(t, resourceguard.LevelNormal, normal.Level)
	require.Equal(t, 8, normal.Target)
	require.Equal(t, 2, normal.Evicted)

	level.Store(int32(resourceguard.LevelArmed))
	armed := fixture.manager.ReclaimDeferredExpansions(context.Background(), ReclaimCauseExplicit)
	require.Equal(t, resourceguard.LevelArmed, armed.Level)
	require.Equal(t, 4, armed.Target, "the budget halves at the first pressure level")
	require.Equal(t, 4, armed.Evicted)

	level.Store(int32(resourceguard.LevelCritical))
	critical := fixture.manager.ReclaimDeferredExpansions(context.Background(), ReclaimCauseExplicit)
	require.Equal(t, resourceguard.LevelCritical, critical.Level)
	require.Equal(t, 2, critical.Target, "the budget is an eighth at the crisis level, with a floor of two")
	require.Equal(t, 2, critical.Evicted)
	require.Equal(t, 2, fixture.manager.DeferredExpansionCount())
}

// TestReclaimDoesNotBuildNeverDialledNodes is requirement 4 of the deferred design: a node that was
// never dialled has no expansion, so a sweep has nothing to do and must not construct anything.
func TestReclaimDoesNotBuildNeverDialledNodes(t *testing.T) {
	fixture := newDeferredFixture(t)
	fixture.start(t)
	fixture.manager.SetDeferredReclaimPolicy(budgetPolicy(1))
	for index := 0; index < 5; index++ {
		fixture.create(t, fmt.Sprintf("cold-%02d", index))
	}
	require.EqualValues(t, 0, fixture.builds.Load())

	result := fixture.manager.ReclaimDeferredExpansions(context.Background(), ReclaimCauseExplicit)
	require.Equal(t, 0, result.Expanded)
	require.Equal(t, 0, result.Evicted)
	require.EqualValues(t, 0, fixture.builds.Load(), "a sweep must never expand a node")
}

// TestReclaimIsBoundedPerSweep keeps one sweep from becoming an unbounded pause: the work is capped and
// the remaining overflow is reported for the next sweep.
func TestReclaimIsBoundedPerSweep(t *testing.T) {
	fixture := newDeferredFixture(t)
	fixture.start(t)
	createExpanded(t, fixture, 20)
	policy := budgetPolicy(1)
	policy.MaxEvictionsPerSweep = 5
	fixture.manager.SetDeferredReclaimPolicy(policy)

	total := 0
	sweeps := 0
	for fixture.manager.DeferredExpansionCount() > 1 {
		result := fixture.manager.ReclaimDeferredExpansions(context.Background(), ReclaimCauseExplicit)
		require.LessOrEqual(t, result.Evicted, 5, "one sweep may not exceed its eviction budget")
		require.Greater(t, result.Evicted, 0, "the sweep must make progress")
		total += result.Evicted
		sweeps++
		require.Less(t, sweeps, 20, "the sweep must converge")
	}
	require.Equal(t, 19, total)
	require.Equal(t, 4, sweeps)
}

// TestIdleWalkTriggersTheSweep is the "when" that needs no new timer: the process's existing idle walk
// (CloseIdleConnections, run by route/reference.go for DEEP_IDLE, the memory pass and the resume
// boundary) reaches the node, the node reports it, and the owner sweeps.
func TestIdleWalkTriggersTheSweep(t *testing.T) {
	fixture := newDeferredFixture(t)
	fixture.start(t)
	nodes := createExpanded(t, fixture, 5)
	policy := budgetPolicy(2)
	policy.MinInterval = 0
	fixture.manager.SetDeferredReclaimPolicy(policy)

	nodes[0].NotifyIdleWalk()
	require.Eventually(t, func() bool {
		return fixture.manager.DeferredExpansionCount() == 2
	}, 5*time.Second, 5*time.Millisecond, "the idle walk must trigger one sweep")
	require.EqualValues(t, 5, fixture.builds.Load(), "the sweep released expansions, it did not rebuild")
}

// TestDeferredReclaimDisabledReleasesNothing: the object layer can be used without a policy, and then
// nothing is released automatically - which is a decision the caller owns.
func TestDeferredReclaimDisabledReleasesNothing(t *testing.T) {
	fixture := newDeferredFixture(t)
	fixture.start(t)
	nodes := createExpanded(t, fixture, 4)
	fixture.manager.SetDeferredReclaimPolicy(budgetPolicy(1))

	fixture.manager.DisableDeferredReclaim()
	require.False(t, fixture.manager.DeferredReclaimEnabled())
	result := fixture.manager.ReclaimDeferredExpansions(context.Background(), ReclaimCauseExplicit)
	require.Equal(t, 0, result.Evicted)
	require.Equal(t, 4, fixture.manager.DeferredExpansionCount())
	for _, node := range nodes {
		_, expanded := node.Built()
		require.True(t, expanded)
	}
}

// TestDeferredSeamArmsTheDefaultBudget: installing the seam arms the default policy, because deferring
// without reclaiming is a net loss the moment anything dials every node.
func TestDeferredSeamArmsTheDefaultBudget(t *testing.T) {
	fixture := newDeferredFixture(t)
	require.True(t, fixture.manager.DeferredReclaimEnabled())
	policy := DefaultDeferredReclaimPolicy()
	require.Equal(t, policy.MaxExpanded, 64)
	require.Equal(t, policy.MaxBytes, int64(256*1024))
	require.Equal(t, policy.MinInterval, 30*time.Second)
}

// TestProviderRefreshKeepsAnOpenConnection is requirement 5: a provider reload must not disturb a
// connection that is already established. The old node is released by the existing ReplaceOwned /
// CompletePublication contract (the connection belongs to its caller, and the eligible shape's
// expansion owns no pooled object), so the connection keeps working after it is closed.
func TestProviderRefreshKeepsAnOpenConnection(t *testing.T) {
	fixture := newDeferredFixture(t)
	fixture.start(t)
	logger := log.NewNOPFactory().NewLogger("t")

	require.NoError(t, fixture.manager.ReplaceOwned(context.Background(), nil, logger, "node", "lazy", nil, "provider"))
	oldObject, loaded := fixture.manager.Outbound("node")
	require.True(t, loaded)
	oldLazy := oldObject.(*LazyOutbound[string])

	conn, err := oldLazy.DialContext(context.Background(), "tcp", M.Socksaddr{})
	require.NoError(t, err)
	require.Equal(t, 1, oldLazy.Builds())

	// The provider refreshes the same tag and completes its publication round.
	require.NoError(t, fixture.manager.ReplaceOwned(context.Background(), nil, logger, "node", "lazy", nil, "provider"))
	require.NoError(t, fixture.manager.CompletePublication("provider", []string{"node"}))
	require.Equal(t, LazyStateClosed, oldLazy.State(), "the replaced node is released by the owner contract")

	// The connection was never part of the expansion's owned resources, so it is still open: a read
	// with a deadline times out instead of reporting a closed pipe.
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(20*time.Millisecond)))
	_, readErr := conn.Read(make([]byte, 1))
	require.ErrorIs(t, readErr, os.ErrDeadlineExceeded,
		"the refresh must not close an established connection")
	require.NoError(t, conn.Close())
	t.Cleanup(func() {
		for _, runtime := range fixture.runtimesSnapshot() {
			runtime.closePeers()
		}
	})

	// And the replacement serves the next demand.
	replacement, loaded := fixture.manager.Outbound("node")
	require.True(t, loaded)
	require.NotSame(t, oldObject, replacement)
	replacementConn, err := replacement.(*LazyOutbound[string]).DialContext(context.Background(), "tcp", M.Socksaddr{})
	require.NoError(t, err)
	require.NoError(t, replacementConn.Close())
}
