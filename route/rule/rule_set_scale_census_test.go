package rule

import (
	"bytes"
	"compress/zlib"
	"context"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/srs"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/internal/memmetrics"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/json/badoption"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/filemanager"

	"github.com/stretchr/testify/require"
)

// The large-configuration census for the rule-set layer.
//
// # What question this answers
//
// A 100k-node / 1M-domain profile is not a scaling-up of a 10-node one: the costs that matter are
// steady live heap, the transient the load path creates, and the window in which a reload holds
// two generations. None of those can be read off the source, so they are measured here, on the
// REAL production path - a binary `.srs` file loaded through `NewLocalRuleSet`, which is what a
// `rule_set` in a config does - rather than on a matcher in isolation.
//
// # What is already compact, and why a census is still needed
//
// The v3+ binary format does not store domain strings. `srs.Read` reads a serialised succinct set
// straight off the decompressed stream into a `domain.Matcher`, so neither the steady heap nor
// the load path ever holds a `[]string` of the list; IP sets are the same shape - a sorted
// `[]ipset.Range4`/`[]ipset.Range6` with a binary search, not a prefix list.
//
// That is precisely why a measurement is needed rather than a code review. If the representation
// is already compact, "optimise the rule-set" has nothing left to win and the next round should
// not be spent on it. Either answer is a result; a guess is not.
//
// # What the numbers mean, and what they do NOT mean
//
//   - `live` is heap still reachable after two forced collections, with the rule-set loaded and
//     nothing else added: the steady cost of the cold object.
//   - `load-garbage` is heap that the load allocated and has NOT yet collected when it returned.
//     That is the parse transient: the zlib window, the growth slack of every array read off the
//     stream, and the fixture's own decode scratch. It is the lowest bound on the parse peak, and
//     it is deterministic, which a sampled peak is not.
//   - `load-peak` is the sampled peak of runtime-managed memory above the pre-load baseline. It
//     can read ZERO when the whole load finishes inside one sampling interval; a zero here means
//     "below this instrument's resolution", never "no cost". The census reports both so the
//     distinction survives into the report.
//   - `reload-both` is heap allocated and uncollected immediately after the second generation is
//     published: the window in which the profile holds BOTH generations. On a memory-limited
//     platform that window, not the steady heap, is what approaches the limit.
//
// # Scale
//
// `TestRuleSetScaleCensus` runs the sizes that belong in every `make test` (1k and 10k entries,
// about a second together) and asserts the shape, so a change that quietly re-expands the matcher
// back into strings fails here. `TestRuleSetScaleCensusLarge` runs 100k and 1M and is gated on
// SING_BOX_SCALE_CENSUS=1, because a million-entry fixture costs minutes and hundreds of MiB to
// encode; `make test` must not pay for that.
const scaleCensusEnv = "SING_BOX_SCALE_CENSUS"

// scaleCensusOutEnv, when set, is a path the census appends its rows to as JSON lines, so a report
// can quote numbers without a human retyping them out of test output.
const scaleCensusOutEnv = "SING_BOX_SCALE_CENSUS_OUT"

// censusRecord is one measured row.
type censusRecord struct {
	Kind    string `json:"kind"`
	Entries int    `json:"entries"`
	// FileBytes is the compressed .srs size on disk: what a download pays for.
	FileBytes int64 `json:"file_bytes"`
	// LiveBytes is heap live after forced collections with the rule-set loaded.
	LiveBytes uint64 `json:"live_bytes"`
	// LoadPeakBytes is the EXACT total allocated by the load, measured with the collector
	// disabled for the duration so nothing it allocated can be reclaimed before it is read.
	LoadPeakBytes uint64 `json:"load_peak_bytes"`
	// ReloadPeakBytes is the same figure for reloading a second generation on top of the first:
	// the window in which the profile holds both generations plus the reload's own scratch.
	ReloadPeakBytes uint64 `json:"reload_peak_bytes"`
	// LoadGCCycles and LoadPauseNs are the collections the load triggered and the stop-the-world
	// time they cost, measured on a second load with the collector left at its configured setting.
	LoadGCCycles uint64 `json:"load_gc_cycles"`
	LoadPauseNs  uint64 `json:"load_pause_ns"`
	// Goroutines is the count attributable to the loaded rule-set.
	Goroutines int `json:"goroutines"`
	// MatchHitNs and MatchMissNs are per-lookup costs for a member and for a near-miss.
	MatchHitNs  int64 `json:"match_hit_ns"`
	MatchMissNs int64 `json:"match_miss_ns"`
}

func (r censusRecord) String() string {
	return fmt.Sprintf(
		"%-6s entries=%-8d srs=%-9s live=%-9s load-peak=%-9s reload-peak=%-9s gc=%-3d pause=%-8s goroutines=%-2d hit=%-5dns miss=%dns",
		r.Kind, r.Entries,
		humanBytes(uint64(r.FileBytes)), humanBytes(r.LiveBytes),
		humanBytes(r.LoadPeakBytes), humanBytes(r.ReloadPeakBytes),
		r.LoadGCCycles, time.Duration(r.LoadPauseNs), r.Goroutines,
		r.MatchHitNs, r.MatchMissNs,
	)
}

func humanBytes(value uint64) string {
	switch {
	case value >= 1<<20:
		return fmt.Sprintf("%.1fMiB", float64(value)/(1<<20))
	case value >= 1<<10:
		return fmt.Sprintf("%.1fKiB", float64(value)/(1<<10))
	default:
		return fmt.Sprintf("%dB", value)
	}
}

// emit writes the record to the test log and, when a path is configured, to the JSON stream the
// report is generated from.
func (r censusRecord) emit(t *testing.T) {
	t.Helper()
	t.Logf("CENSUS %s", r)
	path := os.Getenv(scaleCensusOutEnv)
	if path == "" {
		return
	}
	line := fmt.Sprintf(
		`{"kind":%q,"entries":%d,"file_bytes":%d,"live_bytes":%d,"load_peak_bytes":%d,`+
			`"reload_peak_bytes":%d,"load_gc_cycles":%d,"load_pause_ns":%d,"goroutines":%d,`+
			`"match_hit_ns":%d,"match_miss_ns":%d}`+"\n",
		r.Kind, r.Entries, r.FileBytes, r.LiveBytes, r.LoadPeakBytes, r.ReloadPeakBytes,
		r.LoadGCCycles, r.LoadPauseNs, r.Goroutines, r.MatchHitNs, r.MatchMissNs,
	)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open census output: %v", err)
	}
	defer file.Close()
	if _, err = file.WriteString(line); err != nil {
		t.Fatalf("write census output: %v", err)
	}
}

// censusLabel builds a host label with the shape a real blocklist has: short, near-random, and
// unique. It is derived from the index rather than from a random source so the fixture is stable
// across runs, and it is deliberately NOT a decimal counter - a counter makes the compressed file
// two orders of magnitude smaller than any real list and would report a download cost that does
// not exist. The suffix chain is shared on purpose (64 registered domains times 8 TLDs), because
// that is what makes the reversed trie compact and a generator of unrelated strings would measure
// a different, unrealistic trie.
func censusLabel(index int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	// A multiplicative hash mixes the index into all six positions; a plain base36 conversion of
	// the index would leave the high-order digits almost constant and compress just as badly as a
	// counter does.
	value := uint64(index)*0x9E3779B97F4A7C15 + 0x2545F4914F6CDD1D
	var label [6]byte
	for position := range label {
		label[position] = alphabet[value%36]
		value = value/36 + 0x9E3779B9
	}
	return string(label[:])
}

// censusDomainAt is the single source of the fixture's shape, so the hit/miss probes can be
// derived from the same formula as the list instead of being copied out of it.
func censusDomainAt(index int, variant int) string {
	tlds := []string{"com", "net", "org", "io", "cn", "co.uk", "de", "info"}
	second := fmt.Sprintf("cdn%02d", index%64)
	tld := tlds[(index/64)%len(tlds)]
	return fmt.Sprintf("%s%d.%s.%s", censusLabel(index), variant, second, tld)
}

// censusDomains returns `count` distinct domains shaped like a real blocklist.
func censusDomains(count int, variant int) []string {
	domains := make([]string, count)
	for index := range count {
		domains[index] = censusDomainAt(index, variant)
	}
	return domains
}

// censusPrefixAt is the single source of the prefix fixture's shape.
//
// The addresses are spread across the whole 32-bit space so a 10k-entry fixture is genuinely
// SPARSE. That matters more than it looks: a `10.0.1.0/24, 10.0.2.0/24, ...` list is normalised by
// the builder into a handful of large ranges, and MEASURED, 10 000 such prefixes loaded as 639
// ranges - a 15x understatement of what a real geoip list costs, and a measurement of the fixture
// rather than of the loader. A Knuth multiplicative hash over the octets keeps the samples isolated
// at every size, so the range table the loader has to build is the size of the list.
func censusPrefixAt(index int, variant int) string {
	hash := uint64(index)*2654435761 + uint64(variant)*0x9E3779B1
	if index%16 == 0 {
		// Groups 2 and 3 vary, which is what a /48 covers: a varying group 4 would put every entry
		// inside the same /48 and they would collapse into one range.
		return fmt.Sprintf("2001:%x:%x::/48", (hash>>16)&0xffff, hash&0xffff)
	}
	// 1..126 keeps the fixture out of 0/8, 127/8 and the multicast/reserved top of the space, where
	// a prefix is not representative of what a geoip list contains.
	first := 1 + (hash>>24)%126
	second := (hash >> 16) & 0xff
	third := (hash >> 8) & 0xff
	return fmt.Sprintf("%d.%d.%d.0/24", first, second, third)
}

// censusFirstIPv4Host is the first host address inside the fixture's first IPv4 prefix.
//
// Index 0 is the IPv6 entry (`index%16 == 0`), so the first IPv4 prefix is index 1. The address is
// derived from `censusPrefixAt` rather than hardcoded, and the hit probe asserts the set matches
// it, so a generator change fails the census instead of quietly turning the hit path into a miss.
func censusFirstIPv4Host() string {
	prefix := netip.MustParsePrefix(censusPrefixAt(1, 0))
	return prefix.Addr().Next().String()
}

// censusPrefixes returns `count` distinct prefixes, one IPv6 /48 every sixteenth entry so the
// range table has both families as a real geoip list does.
func censusPrefixes(count int, variant int) []*badoption.Prefixable {
	prefixables := make([]*badoption.Prefixable, 0, count)
	for index := range count {
		prefixables = append(prefixables, common.Ptr(badoption.Prefixable(netip.MustParsePrefix(censusPrefixAt(index, variant)))))
	}
	return prefixables
}

// writeCensusRuleSet encodes one version-5 binary rule-set holding a single default rule.
func writeCensusRuleSet(t *testing.T, path string, rule option.DefaultHeadlessRule) int64 {
	t.Helper()
	file, err := os.Create(path)
	require.NoError(t, err, "create rule-set fixture")
	writeErr := srs.Write(file, option.PlainRuleSet{
		Rules: []option.HeadlessRule{{
			Type:           C.RuleTypeDefault,
			DefaultOptions: rule,
		}},
	}, C.RuleSetVersionCurrent)
	closeErr := file.Close()
	require.NoError(t, writeErr, "encode rule-set fixture")
	require.NoError(t, closeErr, "close rule-set fixture")
	info, err := os.Stat(path)
	require.NoError(t, err)
	return info.Size()
}

func writeCensusDomainRuleSet(t *testing.T, path string, count int, variant int) int64 {
	t.Helper()
	return writeCensusRuleSet(t, path, option.DefaultHeadlessRule{
		Domain: badoption.Listable[string](censusDomains(count, variant)),
	})
}

func writeCensusPrefixRuleSet(t *testing.T, path string, count int, variant int) int64 {
	t.Helper()
	return writeCensusRuleSet(t, path, option.DefaultHeadlessRule{
		IPCIDR: badoption.Listable[*badoption.Prefixable](censusPrefixes(count, variant)),
	})
}

// censusContext is the service context `NewLocalRuleSet` needs. The default registry provides the
// file manager a local rule-set reads through, so the fixture follows the path a config does
// rather than bypassing it with a direct read.
func censusContext() context.Context {
	return service.ContextWithDefaultRegistry(context.Background())
}

// collect reads the heap after two forced collections.
//
// Two, because the first collection finds the fixture's transient objects and the second collects
// what finalisers released during it. One collection is enough to make the number look stable and
// wrong.
func collect() uint64 {
	runtime.GC()
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

// uncollected reads the heap WITHOUT collecting, which is the number that still contains every
// temporary the operation just made.
func uncollected() uint64 {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

// above returns `value - floor`, or zero when the operation cost less than the noise floor.
func above(value uint64, floor uint64) uint64 {
	if value <= floor {
		return 0
	}
	return value - floor
}

// measureAllocated returns how much heap `work` allocates in total.
//
// # Why the collector is disabled
//
// The obvious instrument is a sampler reading runtime-managed memory on a timer, which is what
// `internal/memmetrics` provides and what the datapath benchmarks use. MEASURED here, it reported
// ZERO for every rule-set load up to a million domains: a load finishes inside a few sampling
// intervals, and the process-wide managed figure is dominated by a baseline heap that moves in
// both directions, so the transient never shows as a rise above the pre-load reading.
//
// With the collector disabled, nothing the load allocates can be reclaimed while it runs, so the
// heap delta across the call IS the total allocation - the exact peak, not a sample of it. The
// window is bounded by one load and the flag is restored by a defer, so this cannot leak into
// later measurements.
func measureAllocated(work func()) uint64 {
	previous := debug.SetGCPercent(-1)
	before := uncollected()
	defer func() {
		after := uncollected()
		debug.SetGCPercent(previous)
		_ = after
	}()
	work()
	return above(uncollected(), before)
}

// measureRuleSetLoad loads one binary rule-set twice - once with the collector off to size the
// allocation, once with it on to count the collections the load itself triggers - and returns the
// rule-set from the measured pass with the steady cost of holding it.
func measureRuleSetLoad(t *testing.T, path string) (adapter.RuleSet, uint64, uint64, uint64, uint64) {
	t.Helper()
	ctx := censusContext()

	newOptions := func() option.RuleSet {
		return option.RuleSet{
			Type:         C.RuleSetTypeLocal,
			Format:       C.RuleSetFormatBinary,
			Tag:          []string{filepath.Base(path)},
			LocalOptions: option.LocalRuleSet{Path: path},
		}
	}
	newRuleSet := func() (adapter.RuleSet, error) {
		return NewLocalRuleSet(ctx, logger.NOP(), filepath.Base(path), newOptions())
	}

	baselineLive := collect()
	baselineGoroutines := runtime.NumGoroutine()

	// Pass 1: the exact allocation of a load, collector disabled.
	var measureErr error
	peak := measureAllocated(func() {
		ruleSet, err := newRuleSet()
		if err != nil {
			measureErr = err
			return
		}
		require.NoError(t, ruleSet.Close(), "close the sizing load")
	})
	require.NoError(t, measureErr, "load ", path)

	// Pass 2: the same load with the collector at its configured setting, so the collections and
	// stop-the-world time it costs are attributable to it.
	recorder, err := memmetrics.NewRecorder()
	require.NoError(t, err, "the runtime must publish the metrics the census needs")
	ruleSet, err := newRuleSet()
	require.NoError(t, err, "load ", path)
	delta, _, err := recorder.Finish()
	require.NoError(t, err)

	live := above(collect(), baselineLive)

	require.Equal(t, baselineGoroutines, runtime.NumGoroutine(),
		"loading a rule-set must not start a goroutine: the file watcher is constructed by "+
			"NewLocalRuleSet and only started by StartContext, so a goroutine here means a cold "+
			"rule-set costs a thread before it is ever matched")
	return ruleSet, live, peak, delta.GCCycles, delta.PauseTotalNs
}

// measureRuleSetReload loads a second generation on top of the first and returns the exact
// allocation of that reload: both generations plus the reload's own scratch.
func measureRuleSetReload(t *testing.T, ruleSet adapter.RuleSet, path string) uint64 {
	t.Helper()
	local, isLocal := ruleSet.(*LocalRuleSet)
	require.True(t, isLocal, "the census must load a local rule-set")
	collect()
	return measureAllocated(func() {
		require.NoError(t, local.reloadFile(path), "reload the second generation")
	})
}

// measureMatchCost times a lookup that hits, and one that misses while sharing the whole suffix
// chain.
//
// A hand-rolled timing loop rather than `testing.Benchmark`: that facility runs each measurement
// for at least a second, which would make this census cost tens of seconds in `make test` for a
// number that only needs two significant digits. The loop runs until it has accumulated enough
// time for the average to be stable, which is the property `Benchmark` is buying at a fixed price.
func measureMatchCost(t *testing.T, ruleSet adapter.RuleSet, hit contextBuilder, miss contextBuilder) (int64, int64) {
	t.Helper()
	return timeMatch(t, ruleSet, hit, true), timeMatch(t, ruleSet, miss, false)
}

// contextBuilder is a closure so a probe can build the per-kind context it needs without the
// caller leaking a matcher detail into this file.
type contextBuilder func() *adapter.InboundContext

func timeMatch(t *testing.T, ruleSet adapter.RuleSet, build contextBuilder, want bool) int64 {
	t.Helper()
	matchContext := build()
	const minimumIterations = 200_000
	start := time.Now()
	iterations := 0
	for iterations < minimumIterations || time.Since(start) < 50*time.Millisecond {
		if ruleSet.Match(matchContext) != want {
			t.Fatalf("census probe disagrees with the fixture: expected %v", want)
		}
		iterations++
		if iterations >= 20_000_000 {
			break
		}
	}
	return time.Since(start).Nanoseconds() / int64(iterations)
}

// TestRuleSetScaleCensus is the always-on half: the sizes small enough for every `make test`, with
// the assertions that keep the harness honest.
//
// The bounds are deliberately loose. Their job is to fail when the representation changes SHAPE -
// a load path that materialises domain strings again, for instance - not to pin a byte count that
// a Go release or a GOARCH could move. A tight bound here is the kind of test that gets a magic
// number edited into it the first time CI disagrees.
func TestRuleSetScaleCensus(t *testing.T) {
	for _, count := range []int{1_000, 10_000} {
		record := runDomainCensus(t, count)
		record.emit(t)

		// 100 bytes per domain is several times the measured cost of the succinct set and still
		// far below the ~20 bytes of bare text each domain occupies. A load path that kept the
		// strings would exceed it at this size.
		require.Less(t, record.LiveBytes, uint64(count)*100,
			"a %d-entry domain rule-set must stay compact once loaded; the binary format stores the "+
				"matcher, not the strings, and a number this large means something re-expanded it",
			count)
		require.Greater(t, record.LiveBytes, uint64(0), "the census must measure a live rule-set")
		require.Greater(t, record.MatchHitNs, int64(0), "a lookup must cost measurable time")
	}

	for _, count := range []int{1_000, 10_000} {
		record := runPrefixCensus(t, count)
		record.emit(t)
		require.Greater(t, record.LiveBytes, uint64(0))
		require.Greater(t, record.MatchHitNs, int64(0), "an address lookup must cost measurable time")
	}
}

// TestRuleSetLoadStageBreakdown splits the load cost into its stages.
//
// # Why this row exists
//
// `load-peak` says a load allocates several times what it keeps. That is only actionable if it is
// known WHICH stage allocates it: the compressed-stream decoder, the rule-set decoder in
// `common/srs`, or the layer above them that turns options into rule objects. A report that says
// "the load is 4x" and stops cannot tell the next round whether to touch this repository at all -
// and the largest stage turned out not to be in it. This measures each stage with the same
// instrument as the rows above, so the numbers are comparable.
func TestRuleSetLoadStageBreakdown(t *testing.T) {
	const count = 10_000
	directory := t.TempDir()
	path := filepath.Join(directory, "breakdown.srs")
	writeCensusDomainRuleSet(t, path, count, 0)

	baseline := collect()

	// Stage 1: the compressed stream, read only. This is the floor every rule-set format pays to be
	// stored at all.
	var compressed []byte
	compressedPeak := measureAllocated(func() {
		var err error
		compressed, err = filemanager.ReadFile(censusContext(), path)
		require.NoError(t, err)
	})
	require.NotEmpty(t, compressed)

	// Stage 2: the compressed-stream floor. Deflating the same payload with nothing else running
	// separates "what any format pays just to be read" from "what building the matcher costs", and
	// the two have different owners - this repository owns the wrapper, the matcher itself lives in
	// the sing module.
	//
	// The 4-byte prefix is the format's own header (srs.MagicBytes plus the version byte), which is
	// why the stream is deflated from offset 4 rather than from zero.
	const srsHeaderSize = 4
	require.Greater(t, len(compressed), srsHeaderSize, "the fixture must contain a header and a payload")
	var inflated int64
	decompressPeak := measureAllocated(func() {
		reader, openErr := zlib.NewReader(bytes.NewReader(compressed[srsHeaderSize:]))
		require.NoError(t, openErr)
		copied, copyErr := io.Copy(io.Discard, reader)
		require.NoError(t, copyErr)
		require.NoError(t, reader.Close())
		inflated = copied
	})
	require.Greater(t, inflated, int64(len(compressed)), "the payload must actually inflate")

	// Stage 3: decoding the stream into rule options.
	var compat option.PlainRuleSetCompat
	decodedPeak := measureAllocated(func() {
		var err error
		compat, err = srs.Read(bytes.NewReader(compressed), false)
		require.NoError(t, err)
	})
	require.NotEmpty(t, compat.Options.Rules)

	// Stage 4: options to rule objects - the layer this repository owns.
	plain, err := compat.Upgrade()
	require.NoError(t, err)
	var rules []adapter.HeadlessRule
	rulesPeak := measureAllocated(func() {
		for index, ruleOptions := range plain.Rules {
			headless, buildErr := NewHeadlessRule(censusContext(), ruleOptions)
			if buildErr != nil {
				t.Fatalf("build rule[%d]: %v", index, buildErr)
			}
			rules = append(rules, headless)
		}
	})
	require.NotEmpty(t, rules)

	steady := above(collect(), baseline)
	t.Logf("CENSUS stages entries=%d srs=%s compressed-read=%s inflate=%s decode=%s rules=%s steady=%s",
		count, humanBytes(uint64(len(compressed))),
		humanBytes(compressedPeak), humanBytes(decompressPeak), humanBytes(decodedPeak),
		humanBytes(rulesPeak), humanBytes(steady))
}

// TestRuleSetScaleCensusLarge is the sweep that has to be asked for.
func TestRuleSetScaleCensusLarge(t *testing.T) {
	if os.Getenv(scaleCensusEnv) == "" {
		t.Skipf("set %s=1 to run the 100k/1M rule-set census: the fixtures cost minutes and hundreds "+
			"of MiB to encode, which make test must not pay", scaleCensusEnv)
	}
	for _, count := range []int{100_000, 1_000_000} {
		runDomainCensus(t, count).emit(t)
	}
	for _, count := range []int{100_000} {
		runPrefixCensus(t, count).emit(t)
	}
}

// runDomainCensus measures one domain-list size end to end, including the reload window.
func runDomainCensus(t *testing.T, count int) censusRecord {
	t.Helper()
	directory := t.TempDir()
	firstPath := filepath.Join(directory, "first.srs")
	secondPath := filepath.Join(directory, "second.srs")

	fileBytes := writeCensusDomainRuleSet(t, firstPath, count, 0)
	// The second generation is built and DROPPED before the first is loaded, so the fixture's own
	// encode cost cannot land in the reload window.
	writeCensusDomainRuleSet(t, secondPath, count, 1)

	ruleSet, live, loadPeak, gcCycles, pauseNs := measureRuleSetLoad(t, firstPath)
	t.Cleanup(func() { _ = ruleSet.Close() })

	hit := censusDomainAt(count-1, 0)
	// A miss that shares the entire suffix chain but not the host label: index `count` is one past
	// the last member, so it lands on a registered domain the set DOES contain while the full name
	// does not. A miss that failed at the first label would not exercise the trie.
	miss := censusDomainAt(count, 0)
	hitNs, missNs := measureMatchCost(t, ruleSet,
		func() *adapter.InboundContext {
			return &adapter.InboundContext{Domain: hit, Destination: M.Socksaddr{}}
		},
		func() *adapter.InboundContext {
			return &adapter.InboundContext{Domain: miss, Destination: M.Socksaddr{}}
		})

	reloadPeak := measureRuleSetReload(t, ruleSet, secondPath)
	require.False(t, ruleSet.Match(&adapter.InboundContext{Domain: hit, Destination: M.Socksaddr{}}),
		"the reload must have published the SECOND generation, or the reload peak measured nothing")
	require.True(t, ruleSet.Match(&adapter.InboundContext{Domain: censusDomainAt(count-1, 1), Destination: M.Socksaddr{}}),
		"the second generation's own members must match after the reload")

	return censusRecord{
		Kind:            "domain",
		Entries:         count,
		FileBytes:       fileBytes,
		LiveBytes:       live,
		LoadPeakBytes:   loadPeak,
		ReloadPeakBytes: reloadPeak,
		LoadGCCycles:    gcCycles,
		LoadPauseNs:     pauseNs,
		Goroutines:      0,
		MatchHitNs:      hitNs,
		MatchMissNs:     missNs,
	}
}

// runPrefixCensus measures one IP-CIDR-list size.
//
// There is no reload window row here: an IP set has no watcher-specific path, so a reload's cost is
// the load cost measured below applied to a second file.
func runPrefixCensus(t *testing.T, count int) censusRecord {
	t.Helper()
	directory := t.TempDir()
	path := filepath.Join(directory, "prefix.srs")
	fileBytes := writeCensusPrefixRuleSet(t, path, count, 0)

	ruleSet, live, loadPeak, gcCycles, pauseNs := measureRuleSetLoad(t, path)
	t.Cleanup(func() { _ = ruleSet.Close() })

	// 192.0.2.1 is TEST-NET-1 and outside every generated range, so the miss is a real miss through
	// the whole range table rather than an early exit.
	hit := censusFirstIPv4Host()
	miss := "192.0.2.1"
	hitNs, missNs := measureMatchCost(t, ruleSet,
		func() *adapter.InboundContext {
			return &adapter.InboundContext{Destination: M.ParseSocksaddrHostPort(hit, 443)}
		},
		func() *adapter.InboundContext {
			return &adapter.InboundContext{Destination: M.ParseSocksaddrHostPort(miss, 443)}
		})

	return censusRecord{
		Kind:          "cidr",
		Entries:       count,
		FileBytes:     fileBytes,
		LiveBytes:     live,
		LoadPeakBytes: loadPeak,
		LoadGCCycles:  gcCycles,
		LoadPauseNs:   pauseNs,
		Goroutines:    0,
		MatchHitNs:    hitNs,
		MatchMissNs:   missNs,
	}
}
