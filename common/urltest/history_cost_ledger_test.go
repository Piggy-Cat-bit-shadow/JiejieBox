package urltest

// GM-5: the retained-cost ledger of the management data this package owns.
//
// # What it measures, and why it is a ledger rather than a benchmark
//
// A benchmark reports bytes ALLOCATED per operation. The cost this round is about is what stays
// RESIDENT after a real configuration has been measured: a display entry per node, a health entry per
// (node, target), the epoch book that makes late results fail closed, and the URL/target identity
// each of those entries carries. Those are retained bytes, so the instrument is the repository's
// existing one - two forced GCs, then `HeapAlloc` / `HeapObjects` / `/gc/scan/heap:bytes` - with
// `runtime.KeepAlive` making the reading mean something (see outbound_cold_cost_test.go).
//
// # Why the fixture is created BEFORE the baseline
//
// The tags and the configured target LINKS are fixture INPUTS, not measured data: they are built
// first, the baseline is taken with them alive, and only then is the storage populated. A baseline
// taken before the fixture would count the tag strings as storage cost; a baseline taken after the
// store would measure zero by construction - the exact mistake the cold-cost test documents.
//
// The SCOPE a store arrives with is NOT a fixture input, though. Production normalises the link
// again for every probe (measureCore calls ParseMeasurementTarget per measurement), so each store
// gets a fresh string. The ledger reproduces that: a fixture that reused one scope would share its
// URL string by accident and measure a layout production does not have.
//
// # Why entry counts are reported next to bytes
//
// "Per node" is only meaningful when the entry count is known. A health entry exists only for a
// measured (node, target) pair, so a configuration with a thousand nodes and no probe stores zero
// entries. The ledger prints the counts it actually produced so a byte figure can never be read as
// "this is what a thousand configured nodes cost".
//
// # Scope of the numbers
//
// Go live heap on the host. Not phys_footprint, not native/mmap, not Darwin. The ledger exists to
// compare two implementations of THIS package on the same host, which is what its deltas are for.

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
)

const gm5MeasureEnv = "HAKO_GM5_MEASURE"

// gm5Sample is one retained-cost reading.
type gm5Sample struct {
	Label   string
	Nodes   int
	Targets int
	// DisplayEntries / HealthEntries are the map lengths the run actually produced.
	DisplayEntries int
	HealthEntries  int
	// EpochEntries is the number of drop records in the two epoch tables.
	EpochEntries int
	// HeapBytes, HeapObjects and ScanBytes are the retained deltas.
	HeapBytes   int64
	HeapObjects int64
	ScanBytes   int64
	// PerNode is HeapBytes / Nodes, or zero when Nodes is zero.
	PerNode int64
	// PerEntry is HeapBytes / (display+health entries), for the ledger's readability.
	PerEntry int64
}

// gm5Fixture is the input side: node tags and the target links to store against.
//
// The tags are the shape a provider publishes ("node-00000"), not one-letter names, because the
// per-entry key holds the tag's string header and the entry's own strings are measured by size class.
type gm5Fixture struct {
	tags []string
	// links are the configured target strings. They are fixture INPUTS and stay alive across the
	// baseline, exactly as the configuration's strings do.
	links []string
	// scopes are the canonical identities for reads and deletes. The store path does NOT use them:
	// production builds a scope from a FRESH parse on every probe (measureCore calls
	// ParseMeasurementTarget per measurement), and a fixture that reused one scope would share its
	// URL string by accident - it would measure a layout production does not have. storeScope
	// rebuilds the identity per call for that reason.
	scopes  []MeasurementScope
	history adapter.URLTestHistory
}

func gm5NewFixture(nodes int, targetMode string) *gm5Fixture {
	fixture := &gm5Fixture{
		tags:    make([]string, nodes),
		history: adapter.URLTestHistory{Time: time.Now(), Delay: 1},
	}
	for index := 0; index < nodes; index++ {
		fixture.tags[index] = fmt.Sprintf("node-%05d", index)
	}
	switch targetMode {
	case "default":
		fixture.links = []string{DefaultURLTestURL}
	case "distinct":
		fixture.links = make([]string, nodes)
		for index := 0; index < nodes; index++ {
			fixture.links[index] = fmt.Sprintf("https://target-%05d.example/generate_204", index)
		}
	default:
		panic("unknown target mode " + targetMode)
	}
	fixture.scopes = append(fixture.scopes, gm5MustScope(fixture.links[0]))
	return fixture
}

func gm5MustScope(link string) MeasurementScope {
	scope, err := NewMeasurementScope(link, nil)
	if err != nil {
		panic(err)
	}
	return scope
}

// storeScope is the scope one store arrives with: a NEW normalisation of the configured link, which
// is what the measurement engine produces per probe.
func (f *gm5Fixture) storeScope(target int) MeasurementScope {
	return gm5MustScope(f.links[target])
}

// gm5Live reads the retained heap after two forced collections.
//
// Two collections, not one: the first may find work queued by the fixture, and a reading taken
// while a collection is still finishing is the same class of mistake as taking the baseline after
// the objects were built.
func gm5Live() (heapBytes, heapObjects, scanHeap int64) {
	runtime.GC()
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	samples := []metrics.Sample{
		{Name: "/gc/scan/heap:bytes"},
	}
	metrics.Read(samples)
	if samples[0].Value.Kind() == metrics.KindUint64 {
		scanHeap = int64(samples[0].Value.Uint64())
	} else {
		scanHeap = -1
	}
	return int64(stats.HeapAlloc), int64(stats.HeapObjects), scanHeap
}

// gm5Measure runs one scenario and returns its retained delta.
//
// The fixture must already exist: its bytes are part of the baseline, not of the measurement.
func gm5Measure(label string, fixture *gm5Fixture, apply func(*HistoryStorage)) gm5Sample {
	sample := gm5Sample{Label: label, Nodes: len(fixture.tags)}
	if len(fixture.scopes) > 0 {
		sample.Targets = len(fixture.scopes)
	}
	runtime.KeepAlive(fixture)
	baseHeap, baseObjects, baseScan := gm5Live()

	storage := NewHistoryStorage()
	if apply != nil {
		apply(storage)
	}
	storage.access.RLock()
	sample.DisplayEntries = len(storage.displayHistory)
	sample.HealthEntries = len(storage.healthHistory)
	sample.EpochEntries = len(storage.tagCounts) + len(storage.scopeCounts)
	storage.access.RUnlock()

	afterHeap, afterObjects, afterScan := gm5Live()
	runtime.KeepAlive(storage)
	runtime.KeepAlive(fixture)

	sample.HeapBytes = afterHeap - baseHeap
	sample.HeapObjects = afterObjects - baseObjects
	if baseScan >= 0 && afterScan >= 0 {
		sample.ScanBytes = afterScan - baseScan
	} else {
		sample.ScanBytes = -1
	}
	entries := sample.DisplayEntries + sample.HealthEntries
	if sample.Nodes > 0 {
		sample.PerNode = sample.HeapBytes / int64(sample.Nodes)
	}
	if entries > 0 {
		sample.PerEntry = sample.HeapBytes / int64(entries)
	}
	return sample
}

func gm5StoreDisplay(storage *HistoryStorage, fixture *gm5Fixture, target int) {
	for _, tag := range fixture.tags {
		fixture.history.Time = fixture.history.Time.Add(time.Microsecond)
		storage.StoreDisplayHistory(tag, fixture.storeScope(target), &fixture.history)
	}
}

func gm5StoreHealth(storage *HistoryStorage, fixture *gm5Fixture, target int) {
	for _, tag := range fixture.tags {
		fixture.history.Time = fixture.history.Time.Add(time.Microsecond)
		storage.StoreHealthHistory(tag, fixture.storeScope(target), &fixture.history)
	}
}

func gm5StoreHealthTargets(storage *HistoryStorage, fixture *gm5Fixture, targets int) {
	for target := 0; target < targets; target++ {
		for _, tag := range fixture.tags {
			fixture.history.Time = fixture.history.Time.Add(time.Microsecond)
			storage.StoreHealthHistory(tag, fixture.storeScope(target), &fixture.history)
		}
	}
}

func gm5BumpScopeEpoch(storage *HistoryStorage, fixture *gm5Fixture, target int) {
	for _, tag := range fixture.tags {
		// A failed health check is a delete: it removes the entry (which may not exist) and records
		// the drop. That is the production path, not a synthetic field write. The scope arrives as a
		// fresh normalisation, like the round's own measurement.
		storage.DeleteHealthHistory(tag, fixture.storeScope(target))
	}
}

func gm5BumpTagEpoch(storage *HistoryStorage, fixture *gm5Fixture) {
	for _, tag := range fixture.tags {
		storage.DeleteURLTestHistory(tag)
	}
}

// TestGM5RetainedLedger is the reproducible entry point. It is opt-in because it measures the host
// and prints a ledger; it is not a pass/fail gate.
func TestGM5RetainedLedger(t *testing.T) {
	if os.Getenv(gm5MeasureEnv) == "" {
		t.Skipf("set %s=1 to run the retained-cost ledger", gm5MeasureEnv)
	}
	evidenceDir := os.Getenv("HAKO_GM5_EVIDENCE_DIR")
	label := os.Getenv("HAKO_GM5_LABEL")
	if label == "" {
		label = "unlabelled"
	}
	var rows []gm5Sample
	record := func(sample gm5Sample) {
		rows = append(rows, sample)
		t.Logf("MEASURED %-34s nodes=%-5d targets=%-5d display=%-6d health=%-6d epoch=%-5d "+
			"heap=%+9d objects=%+7d scan=%+9d perNode=%+5d perEntry=%+5d",
			sample.Label, sample.Nodes, sample.Targets, sample.DisplayEntries, sample.HealthEntries,
			sample.EpochEntries, sample.HeapBytes, sample.HeapObjects, sample.ScanBytes,
			sample.PerNode, sample.PerEntry)
	}

	t.Run("empty-storage", func(t *testing.T) {
		fixture := gm5NewFixture(0, "default")
		record(gm5Measure("empty-storage", fixture, nil))
	})

	// The layer a node list and the Clash UI read: one entry per tag.
	t.Run("display-same-target", func(t *testing.T) {
		for _, nodes := range []int{1000, 10000} {
			fixture := gm5NewFixture(nodes, "default")
			record(gm5Measure(fmt.Sprintf("display-only-%d", nodes), fixture, func(storage *HistoryStorage) {
				gm5StoreDisplay(storage, fixture, 0)
			}))
		}
	})

	// The selection layer: one entry per (tag, target). StoreHealthHistory writes BOTH layers, so
	// the health cost is this reading minus the display reading for the same node count.
	t.Run("health-same-target", func(t *testing.T) {
		for _, nodes := range []int{1000, 10000} {
			fixture := gm5NewFixture(nodes, "default")
			record(gm5Measure(fmt.Sprintf("health-1target-%d", nodes), fixture, func(storage *HistoryStorage) {
				gm5StoreHealth(storage, fixture, 0)
			}))
		}
	})

	// A configuration with four URLTest groups or Clash delay URLs: each (node, target) is its own
	// health entry, and the same target URL is stored again for every node.
	t.Run("health-four-targets", func(t *testing.T) {
		fixture := gm5NewFixture(1000, "distinct")
		record(gm5Measure("health-4targets-1000", fixture, func(storage *HistoryStorage) {
			gm5StoreHealthTargets(storage, fixture, 4)
		}))
	})

	// Every node measured against its own URL (target-major, the shape a per-group sweep produces when
	// each group has its own target). The identity is rebuilt for every store, so this is where a
	// per-entry URL copy is the visible cost.
	t.Run("health-distinct-targets", func(t *testing.T) {
		fixture := gm5NewFixture(1000, "distinct")
		record(gm5Measure("health-distinct-1000", fixture, func(storage *HistoryStorage) {
			gm5StoreHealthTargets(storage, fixture, len(fixture.links))
		}))
	})

	// The worst case for the sharing cache, and the one that must NOT get more expensive: targets
	// interleaved so that no two consecutive stores name the same target, with more distinct targets
	// than the cache holds. Nothing can be shared here; the candidate may only add its bounded
	// bookkeeping.
	t.Run("health-interleaved-targets", func(t *testing.T) {
		fixture := gm5NewFixture(200, "distinct")
		record(gm5Measure("health-interleaved-64x200", fixture, func(storage *HistoryStorage) {
			for _, tag := range fixture.tags {
				for target := 0; target < 64; target++ {
					fixture.history.Time = fixture.history.Time.Add(time.Microsecond)
					storage.StoreHealthHistory(tag, fixture.storeScope(target), &fixture.history)
				}
			}
		}))
	})

	// The epoch book. These tables hold no measurement - only a uint32 counter per dropped
	// (node) and (node, target) - so their byte cost is pure bookkeeping.
	t.Run("epoch-tag", func(t *testing.T) {
		fixture := gm5NewFixture(1000, "default")
		record(gm5Measure("epoch-tag-1000", fixture, func(storage *HistoryStorage) {
			gm5BumpTagEpoch(storage, fixture)
		}))
	})
	t.Run("epoch-scope", func(t *testing.T) {
		fixture := gm5NewFixture(1000, "default")
		record(gm5Measure("epoch-scope-1000", fixture, func(storage *HistoryStorage) {
			gm5BumpScopeEpoch(storage, fixture, 0)
		}))
	})
	t.Run("epoch-scope-distinct", func(t *testing.T) {
		fixture := gm5NewFixture(1000, "distinct")
		record(gm5Measure("epoch-scope-distinct-1000", fixture, func(storage *HistoryStorage) {
			for index, tag := range fixture.tags {
				storage.DeleteHealthHistory(tag, fixture.storeScope(index))
			}
		}))
	})
	// The epoch books are bounded at maxEvidenceEpochEntries, so their WORST CASE is measurable
	// exactly: a churn that fills the whole bound. This is the number a device that replaces a large
	// subscription must be able to afford, and the one the sharing candidate must reduce without
	// changing the bound.
	t.Run("epoch-books-at-their-bound", func(t *testing.T) {
		fixture := gm5NewFixture(maxEvidenceEpochEntries, "default")
		record(gm5Measure("epoch-tag-4096", fixture, func(storage *HistoryStorage) {
			gm5BumpTagEpoch(storage, fixture)
		}))
		fixture = gm5NewFixture(maxEvidenceEpochEntries, "default")
		record(gm5Measure("epoch-scope-4096", fixture, func(storage *HistoryStorage) {
			gm5BumpScopeEpoch(storage, fixture, 0)
		}))
	})

	// Refresh churn: a node that fails every round is deleted and re-measured. The retained state
	// must reach a steady size, not grow with the number of rounds.
	t.Run("churn-failure-rounds", func(t *testing.T) {
		fixture := gm5NewFixture(1000, "default")
		record(gm5Measure("churn-10rounds-1000", fixture, func(storage *HistoryStorage) {
			for round := 0; round < 10; round++ {
				gm5StoreHealth(storage, fixture, 0)
				gm5BumpScopeEpoch(storage, fixture, 0)
			}
		}))
	})

	// Node removal and re-add: the tag epoch table is what makes a late result from the removed
	// incarnation unable to publish. Ten rounds of removing and re-adding every node must also
	// reach a steady size.
	t.Run("churn-remove-readd", func(t *testing.T) {
		fixture := gm5NewFixture(1000, "default")
		record(gm5Measure("churn-remove-readd-1000", fixture, func(storage *HistoryStorage) {
			for round := 0; round < 10; round++ {
				gm5BumpTagEpoch(storage, fixture)
				gm5StoreHealth(storage, fixture, 0)
			}
		}))
	})

	// A one-million-entry EPOCH table is deliberately NOT here: both epoch tables are bounded at
	// 4096 entries by construction, so the worst case is the `epoch-*-4096` pair above; the reclaim
	// path itself is covered by evidence_epoch_reclamation_test.go. (The one-million-entry case in
	// the health layer above is a different thing: it is the 1000-target diagnostic shape, not a
	// state production can reach through groups, and it is reported as such.)

	if evidenceDir != "" {
		writeGM5Ledger(t, evidenceDir, label, rows)
	}
}

// writeGM5Ledger appends the readings as TSV so three independent runs can be merged without
// re-parsing the test log.
func writeGM5Ledger(t *testing.T, evidenceDir, label string, rows []gm5Sample) {
	t.Helper()
	path := filepath.Join(evidenceDir, "metrics", "gm5-retained-ledger.tsv")
	_, statErr := os.Stat(path)
	newFile := os.IsNotExist(statErr)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer file.Close()
	if newFile {
		_, err = fmt.Fprintln(file, "run\tlabel\tnodes\ttargets\tdisplay_entries\thealth_entries\tepoch_entries\theap_bytes\theap_objects\tscan_bytes\tper_node\tper_entry")
		if err != nil {
			t.Fatalf("write ledger header: %v", err)
		}
	}
	for _, sample := range rows {
		_, err = fmt.Fprintf(file, "%s\t%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\n",
			label, sample.Label, sample.Nodes, sample.Targets, sample.DisplayEntries,
			sample.HealthEntries, sample.EpochEntries, sample.HeapBytes, sample.HeapObjects,
			sample.ScanBytes, sample.PerNode, sample.PerEntry)
		if err != nil {
			t.Fatalf("write ledger row: %v", err)
		}
	}
}

// BenchmarkGM5StoreHealth is the CPU side of the same operation: one store per measured member per
// round, with the target identity rebuilt per call (what the measurement engine hands over).
func BenchmarkGM5StoreHealth(b *testing.B) {
	fixture := gm5NewFixture(1000, "default")
	storage := NewHistoryStorage()
	history := adapter.URLTestHistory{Time: time.Now(), Delay: 1}
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		tag := fixture.tags[index%len(fixture.tags)]
		history.Time = history.Time.Add(time.Microsecond)
		storage.StoreHealthHistory(tag, fixture.storeScope(0), &history)
	}
	runtime.KeepAlive(storage)
}

// BenchmarkGM5StoreHealthSharedScope isolates the CANDIDATE's cost: the same store with a scope that
// is already normalised, so the per-call parse is not part of the number. The difference against
// BenchmarkGM5StoreHealth is the parse, not the interning; this benchmark is the one where the
// fast-path comparison must not add measurable work.
func BenchmarkGM5StoreHealthSharedScope(b *testing.B) {
	fixture := gm5NewFixture(1000, "default")
	storage := NewHistoryStorage()
	history := adapter.URLTestHistory{Time: time.Now(), Delay: 1}
	scope := fixture.scopes[0]
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		tag := fixture.tags[index%len(fixture.tags)]
		history.Time = history.Time.Add(time.Microsecond)
		storage.StoreHealthHistory(tag, scope, &history)
	}
	runtime.KeepAlive(storage)
}

// BenchmarkGM5LoadDisplay is the read path a node list uses.
func BenchmarkGM5LoadDisplay(b *testing.B) {
	fixture := gm5NewFixture(1000, "default")
	storage := NewHistoryStorage()
	gm5StoreDisplay(storage, fixture, 0)
	runtime.KeepAlive(fixture)
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		_ = storage.LoadURLTestHistory(fixture.tags[index%len(fixture.tags)])
	}
	runtime.KeepAlive(storage)
}

// BenchmarkGM5LoadHealth is the read path selection uses.
func BenchmarkGM5LoadHealth(b *testing.B) {
	fixture := gm5NewFixture(1000, "default")
	storage := NewHistoryStorage()
	gm5StoreHealth(storage, fixture, 0)
	runtime.KeepAlive(fixture)
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		_ = storage.LoadURLTestHistoryFor(fixture.tags[index%len(fixture.tags)], fixture.scopes[0])
	}
	runtime.KeepAlive(storage)
}
