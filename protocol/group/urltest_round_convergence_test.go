package group

// P1.2 (round 3) — the round's convergence and the selection commit revalidate the evidence they
// report.
//
// A published result is only evidence while the state it describes still owns it. A probe's write
// can be legal when it happens and be invalidated afterwards: the node is dropped (retired, or its
// path is invalidated) while a DIFFERENT member's probe is still in flight, and the round then
// converges with a result map that still contains the dropped member. The failover policy consumes
// that map as "the member answered", and the selection commit moves traffic on it.
//
// The two boundaries are separate:
//
//   - convergence: after every probe has finished, each published result is re-checked against its
//     captured provenance (tag/scope stamps and the network reset generation); a result whose
//     evidence no longer holds is removed from the round AND its publication verdict becomes "not
//     published", so failover sees no observation at all.
//   - commit: the selection change runs while the member's evidence is still current, inside the
//     history's own lock, so a drop cannot land between the check and the store.

import (
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/urltest"

	"github.com/stretchr/testify/require"
)

// TestADroppedResultIsRemovedFromTheConvergedRound is the reviewed shape: A's probe finished and
// wrote its evidence, the node was dropped, and only then did B finish.
func TestADroppedResultIsRemovedFromTheConvergedRound(t *testing.T) {
	target := probeTarget(t, 0)
	link := target.URL + "/generate_204"

	history := urltest.NewHistoryStorage()
	t.Cleanup(func() { history.Close() })
	a := newStagedProbeOutbound("node-a", target.Listener.Addr().String())
	b := newStagedProbeOutbound("node-b", target.Listener.Addr().String())

	round := startHealthRound(provenanceContext(), history, []adapter.Outbound{a, b}, link)
	awaitDial(t, a, 1)
	awaitDial(t, b, 1)

	// A finishes first and its evidence lands legitimately.
	a.release()
	require.Eventually(t, func() bool {
		return history.LoadURLTestHistoryFor("node-a", scopeFor(t, link)) != nil
	}, 10*time.Second, time.Millisecond, "A's probe stored its evidence before the drop")

	// The node is retired while B is still in flight.
	history.DeleteURLTestHistory("node-a")

	b.release()
	result := <-round

	require.NotContains(t, result, "node-a",
		"a result whose evidence was dropped after it was written must not survive the round's convergence")
	require.Contains(t, result, "node-b", "the untouched member's result stays")
	require.Nil(t, history.LoadURLTestHistoryFor("node-a", scopeFor(t, link)))
	require.NotNil(t, history.LoadURLTestHistoryFor("node-b", scopeFor(t, link)))
}

// TestASelectionCommitRequiresCurrentEvidence is the commit boundary. The first commit has evidence
// and moves the selection; after the evidence is dropped a later commit must refuse and leave the
// selection where it was.
func TestASelectionCommitRequiresCurrentEvidence(t *testing.T) {
	a := newSwitchableProbeOutbound("a", "")
	b := newSwitchableProbeOutbound("b", "")
	group, history, server := newFailoverWiringGroup(t, failoverTestPolicy(), a, b)
	t.Cleanup(func() { history.Close() })
	target := server.Listener.Addr().String()
	a.target, b.target = target, target

	history.StoreHealthHistory("a", group.scope, &adapter.URLTestHistory{Time: time.Now(), Delay: 5})
	history.StoreHealthHistory("b", group.scope, &adapter.URLTestHistory{Time: time.Now(), Delay: 5})

	group.commitFailoverSelection("a", nil)
	require.Equal(t, "a", selectedTagOf(group), "a commit with current evidence moves the selection")

	history.DeleteURLTestHistory("b")
	group.commitFailoverSelection("b", nil)
	require.Equal(t, "a", selectedTagOf(group),
		"a commit for a member whose evidence was dropped must not move the selection")
}
