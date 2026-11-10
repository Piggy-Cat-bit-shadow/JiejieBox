package group

// P1.2 (round 3) — the remaining invalidation shapes for the convergence and commit boundaries:
// a scoped drop, a same-tag object replacement, a network reset that lands during the round, a
// reclaimed epoch table, and a late failure that must not be counted as a degradation.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/log"

	"github.com/stretchr/testify/require"
)

type convergenceOutcome struct {
	result       map[string]uint16
	publications map[string]roundPublication
	evidence     map[string]roundEvidence
}

// runConvergenceRound drives the internal round so the tests can read the publication verdicts, not
// only the result map.
func runConvergenceRound(t *testing.T, ctx context.Context, manager adapter.OutboundManager, history *urltest.HistoryStorage, outbounds []adapter.Outbound, link string) <-chan convergenceOutcome {
	t.Helper()
	outcome := make(chan convergenceOutcome, 1)
	go func() {
		result, publications, evidence := urlTestOutboundsWithTarget(ctx, manager, history,
			log.NewNOPFactory().NewLogger("convergence"), outbounds, link,
			urltest.ExpectedStatus{}, 0, true, TestHistoryHealth)
		outcome <- convergenceOutcome{result: result, publications: publications, evidence: evidence}
	}()
	return outcome
}

// TestAScopeDropRemovesTheResult: the drop is for (node, target), not for the whole node; the result
// must be removed just the same, and B's must stay.
func TestAScopeDropRemovesTheResult(t *testing.T) {
	target := probeTarget(t, 0)
	link := target.URL + "/generate_204"
	scope := scopeFor(t, link)

	history := urltest.NewHistoryStorage()
	t.Cleanup(func() { history.Close() })
	a := newStagedProbeOutbound("node-a", target.Listener.Addr().String())
	b := newStagedProbeOutbound("node-b", target.Listener.Addr().String())

	round := runConvergenceRound(t, provenanceContext(), &taggedOutboundManager{byTag: map[string]adapter.Outbound{}}, history, []adapter.Outbound{a, b}, link)
	awaitDial(t, a, 1)
	awaitDial(t, b, 1)
	a.release()
	require.Eventually(t, func() bool {
		return history.LoadURLTestHistoryFor("node-a", scope) != nil
	}, 10*time.Second, time.Millisecond)
	history.DeleteHealthHistory("node-a", scope)
	b.release()

	outcome := <-round
	require.NotContains(t, outcome.result, "node-a")
	require.Contains(t, outcome.result, "node-b")
	require.Equal(t, roundNotPublished, outcome.publications["node-a"])
	require.Equal(t, roundPublishedSuccess, outcome.publications["node-b"])
	require.NotNil(t, history.LoadURLTestHistoryFor("node-b", scope))
}

// TestASameTagReplacementRemovesTheRetiredResult: the provider publishes a NEW object under the
// same tag while B is still in flight; the retired object's result describes a socket the new one
// does not share.
func TestASameTagReplacementRemovesTheRetiredResult(t *testing.T) {
	target := probeTarget(t, 0)
	link := target.URL + "/generate_204"
	scope := scopeFor(t, link)

	history := urltest.NewHistoryStorage()
	t.Cleanup(func() { history.Close() })
	a := newStagedProbeOutbound("node-a", target.Listener.Addr().String())
	b := newStagedProbeOutbound("node-b", target.Listener.Addr().String())
	manager := &taggedOutboundManager{byTag: map[string]adapter.Outbound{"node-a": a, "node-b": b}}

	round := runConvergenceRound(t, provenanceContext(), manager, history, []adapter.Outbound{a, b}, link)
	awaitDial(t, a, 1)
	awaitDial(t, b, 1)
	a.release()
	require.Eventually(t, func() bool {
		return history.LoadURLTestHistoryFor("node-a", scope) != nil
	}, 10*time.Second, time.Millisecond)

	replacement := newStagedProbeOutbound("node-a", target.Listener.Addr().String())
	manager.byTag["node-a"] = replacement
	b.release()

	outcome := <-round
	require.NotContains(t, outcome.result, "node-a",
		"a result measured through an object the group no longer uses must not survive convergence")
	require.Equal(t, roundNotPublished, outcome.publications["node-a"])
	require.Contains(t, outcome.result, "node-b")
}

// TestANetworkResetDuringTheRoundRemovesThePublishedResults: A's result was written on the old
// network; the reset lands while B is still in flight, and neither result describes the new network.
func TestANetworkResetDuringTheRoundRemovesThePublishedResults(t *testing.T) {
	target := probeTarget(t, 0)
	link := target.URL + "/generate_204"
	scope := scopeFor(t, link)

	history := urltest.NewHistoryStorage()
	t.Cleanup(func() { history.Close() })
	a := newStagedProbeOutbound("node-a", target.Listener.Addr().String())
	b := newStagedProbeOutbound("node-b", target.Listener.Addr().String())
	facts := newNetworkFacts(0xA, 1)

	round := runConvergenceRound(t, factsContext(facts), &taggedOutboundManager{byTag: map[string]adapter.Outbound{}}, history, []adapter.Outbound{a, b}, link)
	awaitDial(t, a, 1)
	awaitDial(t, b, 1)
	a.release()
	require.Eventually(t, func() bool {
		return history.LoadURLTestHistoryFor("node-a", scope) != nil
	}, 10*time.Second, time.Millisecond)

	// A -> B while B's probe is still in flight.
	facts.environment.Store(0xB)
	facts.resetGeneration.Add(1)
	b.release()

	outcome := <-round
	require.NotContains(t, outcome.result, "node-a",
		"a result published on the previous network generation must not be reported for this one")
	require.NotContains(t, outcome.result, "node-b",
		"B's write belongs to the old generation and is refused by the publication gate")
	require.Equal(t, roundNotPublished, outcome.publications["node-a"])
	require.Equal(t, roundNotPublished, outcome.publications["node-b"])
	require.Nil(t, history.LoadURLTestHistoryFor("node-b", scope))
}

// TestAReclaimedEpochTableRemovesThePublishedResults keeps the bounded-table reclamation in the
// same story: after the table is reclaimed, the ownership captured before the reclaim no longer
// matches, so the published result is removed and the late write is refused.
func TestAReclaimedEpochTableRemovesThePublishedResults(t *testing.T) {
	target := probeTarget(t, 0)
	link := target.URL + "/generate_204"
	scope := scopeFor(t, link)

	history := urltest.NewHistoryStorage()
	t.Cleanup(func() { history.Close() })
	a := newStagedProbeOutbound("node-a", target.Listener.Addr().String())
	b := newStagedProbeOutbound("node-b", target.Listener.Addr().String())

	round := runConvergenceRound(t, provenanceContext(), &taggedOutboundManager{byTag: map[string]adapter.Outbound{}}, history, []adapter.Outbound{a, b}, link)
	awaitDial(t, a, 1)
	awaitDial(t, b, 1)
	a.release()
	require.Eventually(t, func() bool {
		return history.LoadURLTestHistoryFor("node-a", scope) != nil
	}, 10*time.Second, time.Millisecond)

	// Drop A and reclaim the epoch tables with unrelated drops.
	history.DeleteURLTestHistory("node-a")
	for index := 0; index < 5000; index++ {
		history.DeleteURLTestHistory(fmt.Sprintf("%c-retired-%d", 'a'+index%26, index))
	}
	b.release()

	outcome := <-round
	require.NotContains(t, outcome.result, "node-a")
	require.Equal(t, roundNotPublished, outcome.publications["node-a"])
	require.NotContains(t, outcome.result, "node-b",
		"B captured its stamps before the reclaim, so its write is refused")
}

// TestALateFailureAfterADropIsNotNewDegradation: a probe that fails AFTER its node was dropped has
// no right to report a failure anywhere - the removal is refused by the same ownership, so the
// round does not carry a failure observation for the policy to widen on.
func TestALateFailureAfterADropIsNotNewDegradation(t *testing.T) {
	target := probeTarget(t, 0)
	link := target.URL + "/generate_204"
	scope := scopeFor(t, link)

	history := urltest.NewHistoryStorage()
	t.Cleanup(func() { history.Close() })
	history.StoreHealthHistory("node-a", scope, &adapter.URLTestHistory{Time: time.Now().Add(-time.Minute), Delay: 20})

	failing := newHoldableProbe("node-a", target.Listener.Addr().String())
	failing.hold.Store(true)
	failing.fail.Store(true)

	round := runConvergenceRound(t, provenanceContext(), &taggedOutboundManager{byTag: map[string]adapter.Outbound{}}, history, []adapter.Outbound{failing}, link)
	awaitHeldDial(t, failing)
	history.DeleteURLTestHistory("node-a")
	close(failing.gate)

	outcome := <-round
	require.Empty(t, outcome.result)
	require.Equal(t, roundNotPublished, outcome.publications["node-a"],
		"a failure whose evidence removal no longer owns the node is not an observation")
	require.Nil(t, history.LoadURLTestHistoryFor("node-a", scope))
}
