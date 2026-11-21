package urltest

// GM-5: the compacted management layout, tested as SEMANTICS rather than as bytes.
//
// The candidate makes two layout changes and no behavioural ones:
//
//   - the display layer stops retaining the target that produced the measurement (it was write-only);
//   - the health/epoch keys share ONE string pair for an equal target instead of one copy per entry.
//
// Those are exactly the kinds of change that can silently merge two measurements, so what is tested
// here is that they do NOT: two scopes that differ in any identity field stay two entries, an
// equivalent re-normalisation addresses the SAME entry, the sharing cache is bounded, and a late
// result is still rejected by the epoch stamps even though its scope was rebuilt from scratch.

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"

	"github.com/stretchr/testify/require"
)

func gm5Scope(t *testing.T, link string, expected string) MeasurementScope {
	t.Helper()
	status, err := ParseExpectedStatus(expected)
	require.NoError(t, err)
	scope, err := NewMeasurementScope(link, status)
	require.NoError(t, err)
	return scope
}

func gm5History(at time.Time, delay uint16) *adapter.URLTestHistory {
	return &adapter.URLTestHistory{Time: at, Delay: delay}
}

// TestDisplayHistoryKeepsTodaysSemantics is the display layer's contract after the target identity
// was removed from it: the TAG is the key, the newest success wins whatever target produced it, a
// failure never removes it, and two tags never see each other's result.
func TestDisplayHistoryKeepsTodaysSemantics(t *testing.T) {
	storage := NewHistoryStorage()
	defer storage.Close()
	base := time.Unix(1_700_000_000, 0)
	scopeA := gm5Scope(t, "https://a.example/generate_204", "")
	scopeB := gm5Scope(t, "https://b.example/generate_204", "204")

	storage.StoreDisplayHistory("node-a", scopeA, gm5History(base, 10))
	storage.StoreDisplayHistory("node-b", scopeB, gm5History(base.Add(time.Second), 20))
	require.Equal(t, uint16(10), storage.LoadURLTestHistory("node-a").Delay)
	require.Equal(t, uint16(20), storage.LoadURLTestHistory("node-b").Delay)

	// A newer measurement against a DIFFERENT target wins the tag (the display layer's documented
	// rule), because "the last thing known about this node" is exactly what a node list shows.
	storage.StoreDisplayHistory("node-a", scopeB, gm5History(base.Add(2*time.Second), 30))
	require.Equal(t, uint16(30), storage.LoadURLTestHistory("node-a").Delay)

	// An older one does not, whichever target it came from.
	storage.StoreDisplayHistory("node-a", scopeA, gm5History(base.Add(-time.Second), 40))
	require.Equal(t, uint16(30), storage.LoadURLTestHistory("node-a").Delay)

	// The health layer is a different owner: storing health updates display, deleting THAT target's
	// health evidence does not remove the display entry, and deleting the node's history removes both.
	storage.StoreHealthHistory("node-a", scopeA, gm5History(base.Add(3*time.Second), 50))
	require.Equal(t, uint16(50), storage.LoadURLTestHistory("node-a").Delay)
	storage.DeleteHealthHistory("node-a", scopeA)
	require.NotNil(t, storage.LoadURLTestHistory("node-a"),
		"deleting one target's health evidence must not remove the display entry")
	require.Nil(t, storage.LoadURLTestHistoryFor("node-a", scopeA))
	storage.DeleteURLTestHistory("node-a")
	require.Nil(t, storage.LoadURLTestHistory("node-a"))
	require.Equal(t, 1, storage.DisplayEntryCount(), "node-b is untouched")
}

// TestSharedTargetIdentityKeepsScopesDistinct is the collision test: every field of the identity
// still separates two entries, and the shared cache is keyed by the WHOLE scope.
func TestSharedTargetIdentityKeepsScopesDistinct(t *testing.T) {
	storage := NewHistoryStorage()
	defer storage.Close()
	now := time.Unix(1_700_000_000, 0)
	base := gm5Scope(t, "https://example.com/generate_204", "")
	strict := gm5Scope(t, "https://example.com/generate_204", "204")
	otherPath := gm5Scope(t, "https://example.com/other", "")
	otherHost := gm5Scope(t, "https://other.example/generate_204", "")
	// Equal spelling of base (explicit default port) must be the SAME scope, not a fourth one.
	equalToBase := gm5Scope(t, "https://example.com:443/generate_204", "")

	for index, scope := range []MeasurementScope{base, strict, otherPath, otherHost} {
		storage.StoreHealthHistory("node", scope, gm5History(now.Add(time.Duration(index)*time.Second), uint16(10+index)))
	}
	require.Equal(t, 4, storage.HealthEntryCount(), "four identities are four measurements")
	require.Equal(t, uint16(10), storage.LoadURLTestHistoryFor("node", equalToBase).Delay,
		"a re-normalised equal scope addresses the stored entry")

	// Deleting one identity leaves every other one intact and readable.
	storage.DeleteHealthHistory("node", strict)
	require.Nil(t, storage.LoadURLTestHistoryFor("node", strict))
	for index, scope := range []MeasurementScope{base, otherPath, otherHost} {
		history := storage.LoadURLTestHistoryFor("node", scope)
		require.NotNil(t, history)
		require.Equal(t, uint16(10+[]int{0, 2, 3}[index]), history.Delay)
	}
}

// TestSharedTargetIdentityIsBounded pins the cache's bound and its degradation: past it, entries are
// stored unshared and remain correct, and the cache does not grow with the number of targets.
func TestSharedTargetIdentityIsBounded(t *testing.T) {
	storage := NewHistoryStorage()
	defer storage.Close()
	now := time.Unix(1_700_000_000, 0)
	const targets = 200
	scopes := make([]MeasurementScope, targets)
	for index := 0; index < targets; index++ {
		scopes[index] = gm5Scope(t, fmt.Sprintf("https://target-%03d.example/generate_204", index), "")
		// A fresh normalisation per store, exactly as the measurement engine produces.
		storage.StoreHealthHistory("node", gm5Scope(t, fmt.Sprintf("https://target-%03d.example/generate_204", index), ""),
			gm5History(now.Add(time.Duration(index)*time.Second), uint16(index+1)))
	}
	require.Equal(t, targets, storage.HealthEntryCount())
	require.LessOrEqual(t, len(storage.sharedScopes), maxSharedScopes,
		"the sharing cache must not grow with the number of targets")
	require.LessOrEqual(t, cap(storage.sharedScopes), maxSharedScopes)

	for index, scope := range scopes {
		history := storage.LoadURLTestHistoryFor("node", scope)
		require.NotNil(t, history, "target %d", index)
		require.Equal(t, uint16(index+1), history.Delay, "target %d", index)
	}

	// Re-storing the same targets (a second refresh) must not grow the cache or the table.
	sizeBefore := len(storage.sharedScopes)
	storage.StoreHealthHistory("node", scopes[targets-1], gm5History(now.Add(time.Hour), 999))
	require.Equal(t, sizeBefore, len(storage.sharedScopes))
	require.Equal(t, targets, storage.HealthEntryCount())
	require.Equal(t, uint16(999), storage.LoadURLTestHistoryFor("node", scopes[targets-1]).Delay)
}

// TestSharedTargetIdentityPreservesEpochs is the lifecycle test that the compaction must not break:
// a drop recorded through a FRESHLY normalised scope still invalidates the probe that was requested
// before it, and it does not invalidate a different target of the same node.
func TestSharedTargetIdentityPreservesEpochs(t *testing.T) {
	storage := NewHistoryStorage()
	defer storage.Close()
	base := time.Unix(1_700_000_000, 0)
	stored := gm5Scope(t, "https://example.com/generate_204", "")
	sibling := gm5Scope(t, "https://example.com/other", "")
	storage.StoreHealthHistory("node", stored, gm5History(base, 20))
	storage.StoreHealthHistory("node", sibling, gm5History(base, 30))

	tagEpoch, scopeEpoch := storage.EvidenceStamp("node", gm5Scope(t, "https://example.com/generate_204", ""))
	ownership := EvidenceOwnership{TagEpoch: tagEpoch, ScopeEpoch: scopeEpoch}

	// The drop arrives with a DIFFERENT string instance that normalises to the same scope. If the
	// shared key made identity pointer-based, this delete would silently miss, the late write below
	// would be accepted, and evidence the drop removed would be resurrected.
	storage.DeleteHealthHistory("node", gm5Scope(t, "https://example.com/generate_204", ""))
	require.Nil(t, storage.LoadURLTestHistoryFor("node", stored))
	require.NotNil(t, storage.LoadURLTestHistoryFor("node", sibling),
		"a drop of one target must not invalidate another target of the same node")
	require.False(t, storage.StoreHealthHistoryOwned("node", stored,
		gm5History(base.Add(time.Second), 99), ownership),
		"a result whose probe started before the drop must be rejected")
	require.Nil(t, storage.LoadURLTestHistoryFor("node", stored))

	// A probe requested AFTER the drop holds the new stamps and may store.
	newTagEpoch, newScopeEpoch := storage.EvidenceStamp("node", stored)
	require.NotEqual(t, scopeEpoch, newScopeEpoch, "the drop must have moved the scope stamp")
	require.True(t, storage.StoreHealthHistoryOwned("node", stored,
		gm5History(base.Add(2*time.Second), 40),
		EvidenceOwnership{TagEpoch: newTagEpoch, ScopeEpoch: newScopeEpoch}))
	require.Equal(t, uint16(40), storage.LoadURLTestHistoryFor("node", stored).Delay)
}

// TestSharedTargetIdentityIsPerStorage pins that the cache is not process-global: two Boxes must not
// share state through it, and closing one must not affect the other.
func TestSharedTargetIdentityIsPerStorage(t *testing.T) {
	first := NewHistoryStorage()
	second := NewHistoryStorage()
	now := time.Unix(1_700_000_000, 0)
	scope := gm5Scope(t, DefaultURLTestURL, "")
	first.StoreHealthHistory("node", scope, gm5History(now, 11))
	require.Equal(t, 0, len(second.sharedScopes),
		"a second storage must not inherit the first one's shared target")
	require.Equal(t, 0, second.HealthEntryCount())
	second.StoreHealthHistory("node", scope, gm5History(now, 22))
	require.Equal(t, uint16(11), first.LoadURLTestHistoryFor("node", scope).Delay)
	require.Equal(t, uint16(22), second.LoadURLTestHistoryFor("node", scope).Delay)
	require.NoError(t, first.Close())
	require.Nil(t, first.LoadURLTestHistoryFor("node", scope))
	require.NotNil(t, second.LoadURLTestHistoryFor("node", scope))
	require.NoError(t, second.Close())
}

// TestSharedTargetIdentityConcurrentStores exercises the cache under concurrent writers: the store
// path holds the write lock, so a race would surface under -race and a lost update as a wrong count.
func TestSharedTargetIdentityConcurrentStores(t *testing.T) {
	storage := NewHistoryStorage()
	defer storage.Close()
	now := time.Unix(1_700_000_000, 0)
	const writers = 8
	const tagsPerWriter = 40
	const targets = 5
	scopes := make([]MeasurementScope, targets)
	for index := 0; index < targets; index++ {
		scopes[index] = gm5Scope(t, fmt.Sprintf("https://target-%d.example/generate_204", index), "")
	}
	var waitGroup sync.WaitGroup
	for writer := 0; writer < writers; writer++ {
		waitGroup.Add(1)
		go func(writer int) {
			defer waitGroup.Done()
			for tag := 0; tag < tagsPerWriter; tag++ {
				for target := 0; target < targets; target++ {
					storage.StoreHealthHistory(fmt.Sprintf("writer-%d-node-%d", writer, tag),
						gm5Scope(t, fmt.Sprintf("https://target-%d.example/generate_204", target), ""),
						gm5History(now.Add(time.Duration(tag)*time.Microsecond), uint16(target+1)))
				}
			}
		}(writer)
	}
	waitGroup.Wait()
	require.Equal(t, writers*tagsPerWriter*targets, storage.HealthEntryCount())
	require.LessOrEqual(t, len(storage.sharedScopes), maxSharedScopes)
	for writer := 0; writer < writers; writer++ {
		for tag := 0; tag < tagsPerWriter; tag++ {
			for target := 0; target < targets; target++ {
				history := storage.LoadURLTestHistoryFor(fmt.Sprintf("writer-%d-node-%d", writer, tag), scopes[target])
				require.NotNil(t, history)
				require.Equal(t, uint16(target+1), history.Delay)
			}
		}
	}
}
