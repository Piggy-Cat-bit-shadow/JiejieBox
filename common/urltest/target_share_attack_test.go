package urltest

// UH attack: the bounded shared-target cache (GM-5) and the epoch book it feeds.
//
// The cache is an optimisation with a semantic contract around it: sharing may hand a stored key the
// SAME string instances for an equal target, and nothing else about the entry may change. These
// tests drive the parts a single-target happy path never reaches - round-robin eviction, a target
// that leaves the cache and comes back, concurrent writers over more targets than the cache holds -
// and the boundary of the epoch book, where reclaiming the records must invalidate exactly the
// probes that started before the reclaim and no later one.
//
// The retained-cost measurement is gated because it is a measurement, not a correctness check; run
// it with D_UH_RETAINED=1 (and D_EVIDENCE_DIR to also write the TSV).

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"

	"github.com/stretchr/testify/require"
)

func uhScope(t *testing.T, link string) MeasurementScope {
	t.Helper()
	scope, err := NewMeasurementScope(link, nil)
	require.NoError(t, err)
	return scope
}

func uhHistory(at time.Time, delay uint16) *adapter.URLTestHistory {
	return &adapter.URLTestHistory{Time: at, Delay: delay}
}

// TestSharedTargetCacheSurvivesRoundRobinChurn stores far more targets than the cache holds, in an
// interleaved order, and then addresses every entry with a FRESHLY parsed scope. Eviction may only
// drop the sharing slot: an entry must keep comparing equal by value and remain readable.
func TestSharedTargetCacheSurvivesRoundRobinChurn(t *testing.T) {
	storage := NewHistoryStorage()
	defer storage.Close()
	base := time.Unix(1_700_000_000, 0)
	const targets = 64
	const rounds = 3
	links := make([]string, targets)
	for index := range links {
		links[index] = fmt.Sprintf("https://target-%03d.example/generate_204", index)
	}
	for round := 0; round < rounds; round++ {
		// Deliberately stride through the target list so consecutive stores miss the
		// last-shared fast path and exercise the slice scan and the eviction cursor.
		for offset := 0; offset < targets; offset++ {
			index := (offset*7 + round) % targets
			storage.StoreHealthHistory("node", uhScope(t, links[index]),
				uhHistory(base.Add(time.Duration(round*1000+index)*time.Microsecond), uint16(round*100+index+1)))
		}
		require.LessOrEqual(t, len(storage.sharedScopes), maxSharedScopes,
			"the sharing cache must stay bounded across rounds")
	}
	require.Equal(t, targets, storage.HealthEntryCount(), "one node, %d targets: one entry each", targets)
	for index, link := range links {
		history := storage.LoadURLTestHistoryFor("node", uhScope(t, link))
		require.NotNil(t, history, "target %d must still be readable after its cache slot was evicted", index)
		require.Equal(t, uint16(rounds*100+index+1-100), history.Delay)
	}

	// A drop addressed through a fresh scope must hit the same entry even when the target is no
	// longer in the sharing cache: identity is by value, not by the cached instance.
	storage.DeleteHealthHistory("node", uhScope(t, links[0]))
	require.Nil(t, storage.LoadURLTestHistoryFor("node", uhScope(t, links[0])))
	require.NotNil(t, storage.LoadURLTestHistoryFor("node", uhScope(t, links[1])),
		"dropping one target must not remove another")
}

// TestConcurrentTargetStoresStayBoundedAndCorrect is the writer race: many goroutines store many
// targets through the shared cache. Under -race this covers the cache's lock discipline; the
// assertions cover lost updates and an unbounded cache.
func TestConcurrentTargetStoresStayBoundedAndCorrect(t *testing.T) {
	storage := NewHistoryStorage()
	defer storage.Close()
	base := time.Unix(1_700_000_000, 0)
	const writers = 8
	const nodesPerWriter = 30
	const targets = 12
	var waitGroup sync.WaitGroup
	for writer := 0; writer < writers; writer++ {
		waitGroup.Add(1)
		go func(writer int) {
			defer waitGroup.Done()
			for node := 0; node < nodesPerWriter; node++ {
				for target := 0; target < targets; target++ {
					tag := fmt.Sprintf("w%d-n%d", writer, node)
					storage.StoreHealthHistory(tag,
						uhScope(t, fmt.Sprintf("https://t%02d.example/generate_204", target)),
						uhHistory(base.Add(time.Duration(node)*time.Microsecond), uint16(target+1)))
					storage.DeleteHealthHistory(tag, uhScope(t, fmt.Sprintf("https://t%02d.example/generate_204", target)))
					storage.StoreHealthHistory(tag,
						uhScope(t, fmt.Sprintf("https://t%02d.example/generate_204", target)),
						uhHistory(base.Add(time.Duration(node)*time.Microsecond+time.Millisecond), uint16(target+1)))
				}
			}
		}(writer)
	}
	waitGroup.Wait()
	require.Equal(t, writers*nodesPerWriter*targets, storage.HealthEntryCount())
	require.LessOrEqual(t, len(storage.sharedScopes), maxSharedScopes)
	for writer := 0; writer < writers; writer++ {
		for node := 0; node < nodesPerWriter; node++ {
			for target := 0; target < targets; target++ {
				history := storage.LoadURLTestHistoryFor(fmt.Sprintf("w%d-n%d", writer, node),
					uhScope(t, fmt.Sprintf("https://t%02d.example/generate_204", target)))
				require.NotNil(t, history)
				require.Equal(t, uint16(target+1), history.Delay)
			}
		}
	}
}

// TestTheEpochBookReclaimsAtItsExactBoundary pins the boundary itself: the records are allowed to
// fill to the bound, and the drop AFTER the bound is the one that opens a new incarnation. A probe
// that captured stamps just before the reclaim must lose its right to write; one captured just
// after must have it.
func TestTheEpochBookReclaimsAtItsExactBoundary(t *testing.T) {
	storage := NewHistoryStorage()
	defer storage.Close()
	scope := uhScope(t, "https://probe.example/generate_204")

	beforeTag, beforeScope := storage.EvidenceStamp("in-flight-node", scope)
	require.Zero(t, beforeTag)
	require.Zero(t, beforeScope)

	// Exactly the bound: every bump before the reclaim check adds a record without reclaiming.
	for index := 0; index < maxEvidenceEpochEntries; index++ {
		storage.DeleteURLTestHistory(fmt.Sprintf("retired-%d", index))
	}
	require.Equal(t, maxEvidenceEpochEntries, len(storage.tagCounts),
		"the book may fill to the bound without reclaiming")
	atBoundTag, atBoundScope := storage.EvidenceStamp("in-flight-node", scope)
	require.Equal(t, beforeTag, atBoundTag, "no reclaim happened, so an untouched node's stamp is unchanged")
	require.Equal(t, beforeScope, atBoundScope)

	// One drop past the bound: reclaim, incarnation moves, every outstanding stamp is invalid.
	storage.DeleteURLTestHistory("retired-past-bound")
	require.Equal(t, 1, len(storage.tagCounts), "reclaiming drops the whole book, then records this drop")
	afterTag, afterScope := storage.EvidenceStamp("in-flight-node", scope)
	require.NotEqual(t, atBoundTag, afterTag)
	require.NotEqual(t, atBoundScope, afterScope)
	require.False(t, storage.StoreHealthHistoryOwned("in-flight-node", scope, uhHistory(time.Now(), 5), EvidenceOwnership{
		TagEpoch: beforeTag, ScopeEpoch: beforeScope,
	}), "a probe from before the reclaim must not write")

	freshTag, freshScope := storage.EvidenceStamp("in-flight-node", scope)
	require.True(t, storage.StoreHealthHistoryOwned("in-flight-node", scope, uhHistory(time.Now(), 6), EvidenceOwnership{
		TagEpoch: freshTag, ScopeEpoch: freshScope,
	}), "a probe that starts after the reclaim owns the new generation")
}

// TestALateProbeCannotResurrectEvidenceForAReplacedNode is P12 at the storage boundary: the node is
// replaced (its tag is dropped), the replacement stores fresh evidence, and only then does the old
// probe return. The old result must neither overwrite the replacement's evidence nor remove it, and
// a different node's evidence must be untouched.
func TestALateProbeCannotResurrectEvidenceForAReplacedNode(t *testing.T) {
	storage := NewHistoryStorage()
	defer storage.Close()
	scope := uhScope(t, "https://probe.example/generate_204")
	base := time.Now()

	// The old object's last success and the stamp an in-flight probe for it captured.
	storage.StoreHealthHistory("node", scope, uhHistory(base.Add(-time.Minute), 20))
	oldTag, oldScope := storage.EvidenceStamp("node", scope)

	// The provider replaces the object with the same tag: history goes with the object.
	storage.DeleteURLTestHistory("node")
	require.Nil(t, storage.LoadURLTestHistory("node"))
	require.Nil(t, storage.LoadURLTestHistoryFor("node", scope))

	// The replacement is measured and its evidence is now current. Another node keeps its own.
	storage.StoreHealthHistory("node", scope, uhHistory(base, 10))
	storage.StoreHealthHistory("other", scope, uhHistory(base, 30))

	// The old probe returns last, carrying the pre-replacement stamps.
	require.False(t, storage.StoreHealthHistoryOwned("node", scope, uhHistory(base.Add(time.Second), 99), EvidenceOwnership{
		TagEpoch: oldTag, ScopeEpoch: oldScope,
	}), "stale evidence for a replaced object must be refused")
	require.False(t, storage.StoreDisplayHistoryOwned("node", scope, uhHistory(base.Add(time.Second), 99), EvidenceOwnership{
		TagEpoch: oldTag, ScopeEpoch: oldScope,
	}), "the display layer must refuse it too")

	require.Equal(t, uint16(10), storage.LoadURLTestHistoryFor("node", scope).Delay,
		"the replacement's health evidence must survive the late result")
	require.Equal(t, uint16(10), storage.LoadURLTestHistory("node").Delay,
		"and so must its display evidence")
	require.Equal(t, uint16(30), storage.LoadURLTestHistoryFor("other", scope).Delay,
		"another node's evidence must never be touched by one node's drop")
	require.Equal(t, uint16(30), storage.LoadURLTestHistory("other").Delay)
}

// TestAFailedTargetDoesNotRemoveAnotherTargetOrTheDisplaySuccess pins the owner split the provider
// health-check path must respect: a failure is recorded by dropping THAT target's health entry; it
// must not delete the node's other targets and must not remove the display success.
func TestAFailedTargetDoesNotRemoveAnotherTargetOrTheDisplaySuccess(t *testing.T) {
	storage := NewHistoryStorage()
	defer storage.Close()
	first := uhScope(t, "https://a.example/generate_204")
	second := uhScope(t, "https://b.example/generate_204")
	base := time.Now()

	storage.StoreHealthHistory("node", first, uhHistory(base, 11))
	storage.StoreHealthHistory("node", second, uhHistory(base.Add(time.Second), 22))

	// The failure of the FIRST target is a drop for (node, first) only.
	storage.DeleteHealthHistory("node", first)
	require.Nil(t, storage.LoadURLTestHistoryFor("node", first))
	require.Equal(t, uint16(22), storage.LoadURLTestHistoryFor("node", second).Delay,
		"the other target's health evidence is a different fact")
	require.Equal(t, uint16(22), storage.LoadURLTestHistory("node").Delay,
		"a failed probe against one URL says nothing about the displayed success")
}

// TestManualAndAutomaticEvidenceStayIsolated is the manual/display versus automatic/health boundary:
// a manual probe writes display only, an automatic failure drops health only, and a manual result
// that started before a drop cannot recreate the entry the drop removed.
func TestManualAndAutomaticEvidenceStayIsolated(t *testing.T) {
	storage := NewHistoryStorage()
	defer storage.Close()
	scope := uhScope(t, "https://manual.example/generate_204")
	base := time.Now()

	storage.StoreDisplayHistory("node", scope, uhHistory(base, 12))
	require.Zero(t, storage.HealthEntryCount(), "a manual probe must not create selection evidence")
	require.Equal(t, uint16(12), storage.LoadURLTestHistory("node").Delay)

	storage.StoreHealthHistory("node", scope, uhHistory(base.Add(time.Second), 34))
	require.Equal(t, 1, storage.HealthEntryCount())
	require.Equal(t, uint16(34), storage.LoadURLTestHistory("node").Delay, "the newest success wins display")

	// A health drop for the target leaves the display entry alone.
	storage.DeleteHealthHistory("node", scope)
	require.Nil(t, storage.LoadURLTestHistoryFor("node", scope))
	require.NotNil(t, storage.LoadURLTestHistory("node"))

	// A manual probe that started before a node drop must not recreate display evidence.
	stampTag, stampScope := storage.EvidenceStamp("node", scope)
	storage.DeleteURLTestHistory("node")
	require.False(t, storage.StoreDisplayHistoryOwned("node", scope, uhHistory(base.Add(2*time.Second), 56), EvidenceOwnership{
		TagEpoch: stampTag, ScopeEpoch: stampScope,
	}))
	require.Nil(t, storage.LoadURLTestHistory("node"))
}

// TestRetainedCostOfSharedVersusDistinctTargets is the measurement the bounded cache cannot replace:
// the cache holds at most maxSharedScopes slots, so with more distinct targets every entry retains
// its own copy of the target strings. The measurement reports the retained bytes per entry for the
// common shape (one target, N nodes) and the worst shape (N targets), and asserts the shared shape
// is materially cheaper. Skipped unless D_UH_RETAINED=1.
func TestRetainedCostOfSharedVersusDistinctTargets(t *testing.T) {
	if os.Getenv("D_UH_RETAINED") == "" {
		t.Skip("set D_UH_RETAINED=1 to run the retained-cost measurement")
	}
	const nodes = 20_000
	type sample struct {
		label      string
		heapBytes  uint64
		perEntry   float64
		sharedLen  int
		entries    int
		scopeCount int
	}
	measure := func(label string, targets int) sample {
		runtime.GC()
		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)
		storage := NewHistoryStorage()
		base := time.Unix(1_700_000_000, 0)
		for node := 0; node < nodes; node++ {
			target := 0
			if targets > 1 {
				target = node % targets
			}
			scope, err := NewMeasurementScope(fmt.Sprintf("https://target-%05d.example/generate_204", target), nil)
			if err != nil {
				t.Fatalf("scope: %v", err)
			}
			storage.StoreHealthHistory(fmt.Sprintf("node-%05d", node), scope, uhHistory(base, 1))
		}
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		used := after.HeapAlloc - before.HeapAlloc
		result := sample{
			label:      label,
			heapBytes:  used,
			perEntry:   float64(used) / float64(nodes),
			sharedLen:  len(storage.sharedScopes),
			entries:    storage.HealthEntryCount(),
			scopeCount: targets,
		}
		// Keep the storage alive until after the read.
		runtime.KeepAlive(storage)
		return result
	}
	shared := measure("one-target", 1)
	distinct := measure("many-targets", 256)
	for _, result := range []sample{shared, distinct} {
		t.Logf("MEASURED %-12s nodes=%d targets=%d entries=%d cache=%d heap=%d B per-entry=%.1f B",
			result.label, nodes, result.scopeCount, result.entries, result.sharedLen, result.heapBytes, result.perEntry)
	}
	require.LessOrEqual(t, shared.sharedLen, maxSharedScopes)
	require.LessOrEqual(t, distinct.sharedLen, maxSharedScopes)
	require.Equal(t, nodes, shared.entries)
	require.Equal(t, nodes, distinct.entries)
	require.Less(t, shared.perEntry, distinct.perEntry-20,
		"sharing one target across %d nodes must retain materially fewer bytes per entry than %d distinct targets", nodes, distinct.scopeCount)
	if evidenceDir := os.Getenv("D_EVIDENCE_DIR"); evidenceDir != "" {
		path := filepath.Join(evidenceDir, "metrics", "d-uh-retained.tsv")
		file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatalf("open metrics: %v", err)
		}
		for _, result := range []sample{shared, distinct} {
			fmt.Fprintf(file, "shape=%s\tlabels=nodes=%d,targets=%d\tentries=%d\tcache_slots=%d\theap_bytes=%d\tbytes_per_entry=%.1f\n",
				result.label, nodes, result.scopeCount, result.entries, result.sharedLen, result.heapBytes, result.perEntry)
		}
		require.NoError(t, file.Close())
	}
}
