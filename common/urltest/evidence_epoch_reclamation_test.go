package urltest

// The evidence epoch tables are BOUNDED, and reclaiming an entry must not re-authorize a probe that
// was already in flight.
//
// The first fix scoped the drop epochs per node and per (node, target), which made a drop precise.
// It bounded the tables by evicting an arbitrary entry once they were full, and documented that as
// safe because "a stale probe holds a NON-ZERO stamp". That reasoning covers only a probe that
// started AFTER its node had already been dropped once. The dangerous shape is the other one:
//
//	probe starts while the node has NO epoch record    -> it captures stamp 0
//	the node is dropped                               -> its record becomes 1
//	the table fills and the node's record is evicted  -> its stamp reads 0 again
//	the probe finishes and compares 0 == 0            -> a stale result is accepted
//
// A reclaimed entry therefore has to move something else as well: reclaiming the tables moves the
// whole epoch book to a new incarnation, and a stamp carries the incarnation it was issued under. A
// probe from before the reclaim then fails on the incarnation alone, whichever counter it happens to
// read.

import (
	"fmt"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"

	"github.com/stretchr/testify/require"
)

func evidenceTestScope(t *testing.T) MeasurementScope {
	t.Helper()
	scope, err := NewMeasurementScope("https://probe.example/generate_204", nil)
	require.NoError(t, err)
	return scope
}

func evidenceTestHistory(delay uint16) *adapter.URLTestHistory {
	return &adapter.URLTestHistory{Time: time.Now(), Delay: delay}
}

// fillEvidenceEpochs performs `count` drops of OTHER nodes, which is what a provider refresh of a
// large subscription does to these tables.
func fillEvidenceEpochs(storage *HistoryStorage, count int) {
	for index := 0; index < count; index++ {
		storage.DeleteURLTestHistory(fmt.Sprintf("retired-node-%d", index))
	}
}

// fillScopeEpochs performs `count` per-target drops of OTHER nodes, which is what repeated health
// failures and target changes do to the scope table.
func fillScopeEpochs(t *testing.T, storage *HistoryStorage, count int) {
	t.Helper()
	for index := 0; index < count; index++ {
		otherScope, err := NewMeasurementScope(fmt.Sprintf("https://probe.example/target-%d", index), nil)
		require.NoError(t, err)
		storage.DeleteHealthHistory(fmt.Sprintf("retired-node-%d", index), otherScope)
	}
}

// TestAReclaimedTagEpochDoesNotReauthorizeAnInFlightProbe is the reviewed failure: a probe captures
// the zero stamp of a node that was never dropped, the node's record is reclaimed by later drops of
// other nodes, and the probe must still not be able to store.
func TestAReclaimedTagEpochDoesNotReauthorizeAnInFlightProbe(t *testing.T) {
	storage := NewHistoryStorage()
	t.Cleanup(func() { storage.Close() })
	scope := evidenceTestScope(t)

	// The probe's stamp, captured before its node was ever dropped.
	tagEpoch, scopeEpoch := storage.EvidenceStamp("reused-node", scope)
	require.Zero(t, tagEpoch, "a node with no drop carries the zero stamp")

	// The node is dropped while that probe is in flight...
	storage.DeleteURLTestHistory("reused-node")

	// ...and enough OTHER drops follow to reclaim its epoch record. The table is bounded, and every
	// drop past the bound forgets one entry (or, after the fix, reclaims the whole book), so the
	// scenario is driven well past the bound: the reviewed failure needed the target's own record to
	// be forgotten.
	fillEvidenceEpochs(storage, maxEvidenceEpochEntries*8)

	require.False(t,
		storage.StoreHealthHistoryOwned("reused-node", scope, evidenceTestHistory(5), EvidenceOwnership{
			TagEpoch: tagEpoch, ScopeEpoch: scopeEpoch,
		}),
		"a reclaimed epoch table must not re-authorize a probe that was in flight when its record was dropped")
	require.Nil(t, storage.LoadURLTestHistoryFor("reused-node", scope),
		"the invalidated result must not reappear in the health layer")
	require.Nil(t, storage.LoadURLTestHistory("reused-node"),
		"nor in the display layer")

	// The guard is not permanent: a probe that starts now captures the current stamps and stores.
	freshTagEpoch, freshScopeEpoch := storage.EvidenceStamp("reused-node", scope)
	require.True(t,
		storage.StoreHealthHistoryOwned("reused-node", scope, evidenceTestHistory(7), EvidenceOwnership{
			TagEpoch: freshTagEpoch, ScopeEpoch: freshScopeEpoch,
		}),
		"a probe that begins after the reclaim belongs to the current evidence generation")
	require.NotNil(t, storage.LoadURLTestHistoryFor("reused-node", scope))
}

// TestAReclaimedScopeEpochDoesNotReauthorizeAnInFlightProbe keeps the second table honest: the same
// reclamation must not revive a stale (node, target) stamp either.
func TestAReclaimedScopeEpochDoesNotReauthorizeAnInFlightProbe(t *testing.T) {
	storage := NewHistoryStorage()
	t.Cleanup(func() { storage.Close() })
	scope := evidenceTestScope(t)

	tagEpoch, scopeEpoch := storage.EvidenceStamp("reused-node", scope)
	require.Zero(t, scopeEpoch)

	storage.DeleteHealthHistory("reused-node", scope)
	fillScopeEpochs(t, storage, maxEvidenceEpochEntries*8)

	require.False(t,
		storage.StoreHealthHistoryOwned("reused-node", scope, evidenceTestHistory(5), EvidenceOwnership{
			TagEpoch: tagEpoch, ScopeEpoch: scopeEpoch,
		}),
		"a reclaimed scope table must not re-authorize a stale target stamp")

	freshTagEpoch, freshScopeEpoch := storage.EvidenceStamp("reused-node", scope)
	require.True(t,
		storage.StoreHealthHistoryOwned("reused-node", scope, evidenceTestHistory(9), EvidenceOwnership{
			TagEpoch: freshTagEpoch, ScopeEpoch: freshScopeEpoch,
		}))
}

// TestReclaimingTheEpochBookChangesEveryOutstandingStamp pins the mechanism itself: the current
// stamp of an untouched node must move when the tables are reclaimed, so no probe that captured an
// earlier value can match it.
func TestReclaimingTheEpochBookChangesEveryOutstandingStamp(t *testing.T) {
	storage := NewHistoryStorage()
	t.Cleanup(func() { storage.Close() })
	scope := evidenceTestScope(t)

	tagBefore, scopeBefore := storage.EvidenceStamp("untouched-node", scope)
	fillEvidenceEpochs(storage, maxEvidenceEpochEntries+1)
	tagAfter, scopeAfter := storage.EvidenceStamp("untouched-node", scope)

	require.NotEqual(t, tagBefore, tagAfter,
		"reclaiming the tables must move even an untouched node's tag stamp")
	require.NotEqual(t, scopeBefore, scopeAfter,
		"and its scope stamp")
	require.False(t,
		storage.StoreHealthHistoryOwned("untouched-node", scope, evidenceTestHistory(3), EvidenceOwnership{
			TagEpoch: tagBefore, ScopeEpoch: scopeBefore,
		}))
	require.True(t,
		storage.StoreHealthHistoryOwned("untouched-node", scope, evidenceTestHistory(3), EvidenceOwnership{
			TagEpoch: tagAfter, ScopeEpoch: scopeAfter,
		}))
}
