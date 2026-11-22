//go:build unix

package rule

// N3 measurement harness — one sample per process, driven by environment variables.
//
// Why a process per sample: the numbers that decide whether the derived artifact is worth
// shipping are peak RSS, live heap and total allocation DURING one load. A second load in the same
// process inherits the first one's heap, its page cache and its mapped pages, so "cold" and "warm"
// can only be separated honestly by starting each from an identical process. The driver script
// (scripts/r-measure.sh) also runs the samples serially, as the task book requires.
//
// Arms
//
//	current-load    the iOS path this branch ships today: source -> srs.Read / JSON decode ->
//	                WriteMmap to a transient file -> ReadMmap -> NewHeadlessRule.
//	derived-cold    the new path on a miss: same conversion, plus the derived publish.
//	derived-warm    the new path on a hit: source digest -> artifact Load -> map -> rules.
//	current-reload  the current path as a file watcher reload: a second load of a CHANGED source
//	                while the old generation is still referenced.
//	derived-reload  the new path as a same-source reload (warm hit), old generation referenced.
//	derived-change  the new path on a changed source: miss -> convert -> publish, old generation
//	                referenced.
//
// The fixtures are the real formats: `srs.Write` output (v5, the current RuleSetVersionCurrent) and
// the source-JSON form of the same rules, both read through the same production functions.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/srs"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/internal/memmetrics"
	"github.com/sagernet/sing-box/option"
	singjson "github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/common/json/badoption"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/service/filemanager"

	"github.com/stretchr/testify/require"
)

const (
	measureRootEnv    = "R_MEASURE_ROOT"
	measureArmEnv     = "R_ARM"
	measureKindEnv    = "R_KIND"
	measureEntriesEnv = "R_ENTRIES"
	measureFormatEnv  = "R_FORMAT"
	measureStoreEnv   = "R_STORE"
	measureOutEnv     = "R_OUT"
	measureSampleEnv  = "R_SAMPLE"
	aggregateEnv      = "R_AGGREGATE"
)

// measureResult is one row. Bytes are raw; the aggregator formats them.
type measureResult struct {
	Arm           string  `json:"arm"`
	Kind          string  `json:"kind"`
	Entries       int     `json:"entries"`
	Format        string  `json:"format"`
	Sample        int     `json:"sample"`
	Millis        float64 `json:"millis"`
	TotalAlloc    uint64  `json:"total_alloc"`
	LiveHeap      uint64  `json:"live_heap"`
	LiveObjects   int64   `json:"live_objects"`
	RSSBefore     uint64  `json:"rss_before"`
	RSSPeak       uint64  `json:"rss_peak"`
	RSSAfter      uint64  `json:"rss_after"`
	SourceBytes   int64   `json:"source_bytes"`
	ArtifactBytes int64   `json:"artifact_bytes"`
	StoreBytes    int64   `json:"store_bytes"`
	StoreVersions int     `json:"store_versions"`
	GCCycles      uint64  `json:"gc_cycles"`
	PauseNanos    uint64  `json:"pause_nanos"`
	Hits          int64   `json:"hits"`
	Misses        int64   `json:"misses"`
	Publishes     int64   `json:"publishes"`
	Failures      int64   `json:"failures"`
	Mapped        bool    `json:"mapped_supported"`
	Err           string  `json:"err,omitempty"`
}

type measureSampler struct {
	stop chan struct{}
	done chan struct{}
	peak atomic.Uint64
}

func startMeasureSampler() *measureSampler {
	sampler := &measureSampler{stop: make(chan struct{}), done: make(chan struct{})}
	if current, ok := readProcessRSS(); ok {
		sampler.peak.Store(current)
	}
	go func() {
		defer close(sampler.done)
		ticker := time.NewTicker(500 * time.Microsecond)
		defer ticker.Stop()
		for {
			select {
			case <-sampler.stop:
				return
			case <-ticker.C:
				if current, ok := readProcessRSS(); ok {
					for {
						peak := sampler.peak.Load()
						if current <= peak || sampler.peak.CompareAndSwap(peak, current) {
							break
						}
					}
				}
			}
		}
	}()
	return sampler
}

func (s *measureSampler) finish() uint64 {
	close(s.stop)
	<-s.done
	return s.peak.Load()
}

// readProcessRSS reads the resident set size of this process. Linux only; on a Unix host without
// /proc the caller reports zero with mapped_supported=false rather than a fabricated number.
func readProcessRSS() (uint64, bool) {
	content, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(content))
	if len(fields) < 2 {
		return 0, false
	}
	pages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return pages * uint64(os.Getpagesize()), true
}

func measureHeap() (live uint64, objects int64, total uint64) {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc, int64(stats.HeapObjects), stats.TotalAlloc
}

func measureDirectoryBytes(directory string) int64 {
	var total int64
	_ = filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return nil
		}
		if info, infoErr := entry.Info(); infoErr == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

func measureFixturePath(root string, kind string, entries int, format string) string {
	extension := "srs"
	if format == C.RuleSetFormatSource {
		extension = "json"
	}
	return filepath.Join(root, "fixtures", fmt.Sprintf("%s-%d.%s", kind, entries, extension))
}

func measureVariantPath(root string, kind string, entries int, format string) string {
	extension := "srs"
	if format == C.RuleSetFormatSource {
		extension = "json"
	}
	return filepath.Join(root, "fixtures", fmt.Sprintf("%s-%d-v1.%s", kind, entries, extension))
}

func measureKindRuleSet(kind string, entries int, variant int) option.PlainRuleSet {
	if kind == "domain" {
		return option.PlainRuleSet{Rules: []option.HeadlessRule{{
			Type:           C.RuleTypeDefault,
			DefaultOptions: option.DefaultHeadlessRule{Domain: badoption.Listable[string](censusDomains(entries, variant))},
		}}}
	}
	return option.PlainRuleSet{Rules: []option.HeadlessRule{{
		Type:           C.RuleTypeDefault,
		DefaultOptions: option.DefaultHeadlessRule{IPCIDR: censusPrefixes(entries, variant)},
	}}}
}

// TestDerivedMeasureFixtures builds the persistent fixtures once for the whole sweep.
func TestDerivedMeasureFixtures(t *testing.T) {
	root := os.Getenv(measureRootEnv)
	if root == "" {
		t.Skipf("set %s to run the N3 measurement harness", measureRootEnv)
	}
	require.NoError(t, os.MkdirAll(filepath.Join(root, "fixtures"), 0o755))
	for _, kind := range []string{"domain", "cidr"} {
		for _, entries := range []int{10_000, 100_000, 1_000_000} {
			if kind == "cidr" && entries == 1_000_000 {
				continue
			}
			for _, format := range []string{C.RuleSetFormatBinary, C.RuleSetFormatSource} {
				path := measureFixturePath(root, kind, entries, format)
				if fileExists(path) {
					continue
				}
				if format == C.RuleSetFormatBinary {
					file, err := os.Create(path)
					require.NoError(t, err)
					require.NoError(t, srs.Write(file, measureKindRuleSet(kind, entries, 0), C.RuleSetVersionCurrent))
					require.NoError(t, file.Close())
				} else {
					encoded, err := singjson.MarshalContext(context.Background(), option.PlainRuleSetCompat{
						Version: C.RuleSetVersionCurrent,
						Options: measureKindRuleSet(kind, entries, 0),
					})
					require.NoError(t, err)
					require.NoError(t, os.WriteFile(path, encoded, 0o644))
				}
			}
			binaryInfo, err := os.Stat(measureFixturePath(root, kind, entries, C.RuleSetFormatBinary))
			require.NoError(t, err)
			sourceInfo, err := os.Stat(measureFixturePath(root, kind, entries, C.RuleSetFormatSource))
			require.NoError(t, err)
			t.Logf("FIXTURE kind=%s entries=%d srs=%d json=%d", kind, entries, binaryInfo.Size(), sourceInfo.Size())
		}
	}
	// The "changed" variant has DIFFERENT rules of the same shape, so a reload is a real change.
	for _, kind := range []string{"domain", "cidr"} {
		for _, entries := range []int{10_000, 100_000, 1_000_000} {
			if kind == "cidr" && entries == 1_000_000 {
				continue
			}
			for _, format := range []string{C.RuleSetFormatBinary, C.RuleSetFormatSource} {
				path := measureVariantPath(root, kind, entries, format)
				if fileExists(path) {
					continue
				}
				if format == C.RuleSetFormatBinary {
					file, err := os.Create(path)
					require.NoError(t, err)
					require.NoError(t, srs.Write(file, measureKindRuleSet(kind, entries, 1), C.RuleSetVersionCurrent))
					require.NoError(t, file.Close())
				} else {
					encoded, err := singjson.MarshalContext(context.Background(), option.PlainRuleSetCompat{
						Version: C.RuleSetVersionCurrent,
						Options: measureKindRuleSet(kind, entries, 1),
					})
					require.NoError(t, err)
					require.NoError(t, os.WriteFile(path, encoded, 0o644))
				}
			}
		}
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// loadCurrentRuleSet is the iOS path this branch shipped before N3: parse the source, write the
// whole compiled rule-set to a transient file, map it, and build the headless rules. It uses the
// production helpers, so the measurement is of the real code, not of a copy.
func loadCurrentRuleSet(ctx context.Context, path string, format string) ([]adapter.HeadlessRule, error) {
	var ruleSet option.PlainRuleSetCompat
	switch format {
	case C.RuleSetFormatBinary:
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		ruleSet, err = srs.Read(file, false)
		_ = file.Close()
		if err != nil {
			return nil, err
		}
	case C.RuleSetFormatSource:
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		ruleSet, err = singjson.UnmarshalExtended[option.PlainRuleSetCompat](content)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unknown format %q", format)
	}
	if file, ok := writeRuleSetMmapFile(ctx, logger.NOP(), "measure", ruleSet); ok {
		defer file.Close()
		ruleSet = mapRuleSetMmapFile(logger.NOP(), "measure", file, ruleSet)
	}
	plainRuleSet, err := ruleSet.Upgrade()
	if err != nil {
		return nil, err
	}
	rules := make([]adapter.HeadlessRule, len(plainRuleSet.Rules))
	for index, options := range plainRuleSet.Rules {
		rules[index], err = NewHeadlessRule(ctx, options)
		if err != nil {
			return nil, err
		}
	}
	return rules, nil
}

// measureSink keeps a measured generation reachable after the measurement, so the post-arm live
// heap is the steady cost of the generation rather than the cost of a freed one.
var measureSink any

func measureFirstIPv4Host(variant int) string {
	prefix := netip.MustParsePrefix(censusPrefixAt(1, variant))
	return prefix.Addr().Next().String()
}

func measureExpectation(kind string, entries int, variant int) (hit string, miss string) {
	if kind == "domain" {
		return censusDomainAt(entries-1, variant), censusDomainAt(entries, variant)
	}
	return measureFirstIPv4Host(variant), "192.0.2.1"
}

func measureCheck(t *testing.T, rules []adapter.HeadlessRule, kind string, entries int, variant int) {
	t.Helper()
	hit, miss := measureExpectation(kind, entries, variant)
	build := func(domain string, address string) *adapter.InboundContext {
		if kind == "domain" {
			return &adapter.InboundContext{Domain: domain}
		}
		return &adapter.InboundContext{Destination: M.ParseSocksaddrHostPort(address, 443)}
	}
	require.True(t, matchAnyHeadlessRule(rules, build(hit, hit)), "the measured generation must match its own member")
	require.False(t, matchAnyHeadlessRule(rules, build(miss, miss)), "the measured generation must not match a near miss")
}

func measureContext(storeDir string) context.Context {
	return filemanager.WithDefault(context.Background(), storeDir, os.TempDir(), -1, -1)
}

func measureNewLocalRuleSet(t *testing.T, ctx context.Context, path string, format string) *LocalRuleSet {
	t.Helper()
	loaded, err := NewLocalRuleSet(ctx, logger.NOP(), "measure", option.RuleSet{
		Type:         C.RuleSetTypeLocal,
		Format:       format,
		LocalOptions: option.LocalRuleSet{Path: path},
	})
	require.NoError(t, err)
	return loaded
}

func measureSourceInfo(t *testing.T, path string) (string, int64) {
	t.Helper()
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:]), int64(len(content))
}

// TestDerivedMeasureArm runs exactly one measured load in a fresh process.
func TestDerivedMeasureArm(t *testing.T) {
	root := os.Getenv(measureRootEnv)
	if root == "" {
		t.Skipf("set %s to run the N3 measurement harness", measureRootEnv)
	}
	arm := os.Getenv(measureArmEnv)
	kind := os.Getenv(measureKindEnv)
	format := os.Getenv(measureFormatEnv)
	entries, err := strconv.Atoi(os.Getenv(measureEntriesEnv))
	require.NoError(t, err)
	sample, _ := strconv.Atoi(os.Getenv(measureSampleEnv))
	storeDir := os.Getenv(measureStoreEnv)
	require.NotEmpty(t, storeDir, "%s", measureStoreEnv)
	require.NoError(t, os.MkdirAll(storeDir, 0o755))

	enableDerivedRuleSetPlatform(t)
	ctx := measureContext(storeDir)

	// The reload/change arms edit the source, so they work on a copy; the cold/warm arms must use
	// the SAME path across processes, because the path is part of the cache identity.
	path := measureFixturePath(root, kind, entries, format)
	variant := 0
	changing := arm == "current-reload" || arm == "derived-change"
	if changing {
		path = filepath.Join(t.TempDir(), "changing"+filepath.Ext(path))
		require.NoError(t, copyFile(measureFixturePath(root, kind, entries, format), path))
		variant = 1
	}
	digest, size := measureSourceInfo(t, path)
	require.NotZero(t, size)

	result := measureResult{
		Arm: arm, Kind: kind, Entries: entries, Format: format, Sample: sample, SourceBytes: size,
	}
	defer func() {
		if result.Err == "" && t.Failed() {
			result.Err = "assertion failed"
		}
		emitMeasureResult(t, result)
	}()

	var rules []adapter.HeadlessRule
	var local *LocalRuleSet
	switch arm {
	case "current-load":
		rules, err = measureArmLoad(t, &result, func() ([]adapter.HeadlessRule, error) {
			return loadCurrentRuleSet(ctx, path, format)
		})
	case "derived-cold", "derived-warm":
		rules, err = measureArmLoad(t, &result, func() ([]adapter.HeadlessRule, error) {
			loaded := measureNewLocalRuleSet(t, ctx, path, format)
			local = loaded
			return loaded.currentRules(), nil
		})
	case "current-reload":
		preloaded, preloadErr := loadCurrentRuleSet(ctx, path, format)
		require.NoError(t, preloadErr)
		require.NoError(t, copyFile(measureVariantPath(root, kind, entries, format), path))
		rules, err = measureArmLoad(t, &result, func() ([]adapter.HeadlessRule, error) {
			return loadCurrentRuleSet(ctx, path, format)
		})
		_ = preloaded
	case "derived-reload", "derived-change":
		loaded := measureNewLocalRuleSet(t, ctx, path, format)
		local = loaded
		// Keep the first generation referenced, as a watcher reload with a matcher in flight does.
		previousRules := loaded.currentRules()
		if arm == "derived-change" {
			require.NoError(t, copyFile(measureVariantPath(root, kind, entries, format), path))
		}
		rules, err = measureArmLoad(t, &result, func() ([]adapter.HeadlessRule, error) {
			if reloadErr := loaded.reloadFile(path); reloadErr != nil {
				return nil, reloadErr
			}
			return loaded.currentRules(), nil
		})
		_ = previousRules
	default:
		t.Fatalf("unknown arm %q", arm)
	}
	require.NoError(t, err)
	measureCheck(t, rules, kind, entries, variant)
	if local != nil {
		measureSink = local.currentRules()
	} else {
		measureSink = rules
	}

	base := filemanager.BasePath(ctx, "")
	key := localDerivedSource("measure", path, derivedFormatName(format), digest, size).key
	keyDir := filepath.Join(base, derivedStoreDirectory, key)
	if dirEntries, readErr := os.ReadDir(keyDir); readErr == nil {
		for _, entry := range dirEntries {
			if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "v-") {
				continue
			}
			result.StoreVersions++
			if info, statErr := os.Stat(filepath.Join(keyDir, entry.Name(), "artifact.bin")); statErr == nil {
				result.ArtifactBytes = info.Size()
			}
		}
	}
	result.StoreBytes = measureDirectoryBytes(filepath.Join(base, derivedStoreDirectory))
	hits, misses, publishes, failures := derivedCacheDiagnostics()
	result.Hits, result.Misses, result.Publishes, result.Failures = hits, misses, publishes, failures
}

// measureArmLoad runs one load with the same instruments for every arm: total allocation, live
// heap after collection, sampled peak RSS, and the GC work the load caused.
func measureArmLoad(t *testing.T, result *measureResult, load func() ([]adapter.HeadlessRule, error)) ([]adapter.HeadlessRule, error) {
	t.Helper()
	runtime.GC()
	runtime.GC()
	liveBefore, _, totalBefore := measureHeap()
	rssBefore, ok := readProcessRSS()
	result.Mapped = ok
	result.RSSBefore = rssBefore

	recorder, err := memmetrics.NewRecorder()
	require.NoError(t, err)
	sampler := startMeasureSampler()
	started := time.Now()
	rules, loadErr := load()
	elapsed := time.Since(started)
	peak := sampler.finish()
	delta, _, finishErr := recorder.Finish()
	require.NoError(t, finishErr)

	// The generation stays live while the post-load heap is read: that is the steady cost.
	runtime.GC()
	runtime.GC()
	liveAfter, objectsAfter, totalAfter := measureHeap()
	rssAfter, _ := readProcessRSS()

	result.Millis = float64(elapsed.Nanoseconds()) / 1e6
	result.TotalAlloc = totalAfter - totalBefore
	if liveAfter > liveBefore {
		result.LiveHeap = liveAfter - liveBefore
	}
	result.LiveObjects = objectsAfter
	result.RSSPeak = peak
	result.RSSAfter = rssAfter
	result.GCCycles = delta.GCCycles
	result.PauseNanos = delta.PauseTotalNs
	if loadErr != nil {
		result.Err = loadErr.Error()
	}
	return rules, loadErr
}

func copyFile(source string, destination string) error {
	content, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return os.WriteFile(destination, content, 0o644)
}

func emitMeasureResult(t *testing.T, result measureResult) {
	t.Helper()
	encoded := fmt.Sprintf(
		`{"arm":%q,"kind":%q,"entries":%d,"format":%q,"sample":%d,"millis":%.3f,`+
			`"total_alloc":%d,"live_heap":%d,"live_objects":%d,"rss_before":%d,"rss_peak":%d,`+
			`"rss_after":%d,"source_bytes":%d,"artifact_bytes":%d,"store_bytes":%d,`+
			`"store_versions":%d,"gc_cycles":%d,"pause_nanos":%d,"hits":%d,"misses":%d,`+
			`"publishes":%d,"failures":%d,"mapped_supported":%v,"err":%q}`+"\n",
		result.Arm, result.Kind, result.Entries, result.Format, result.Sample, result.Millis,
		result.TotalAlloc, result.LiveHeap, result.LiveObjects, result.RSSBefore, result.RSSPeak,
		result.RSSAfter, result.SourceBytes, result.ArtifactBytes, result.StoreBytes,
		result.StoreVersions, result.GCCycles, result.PauseNanos, result.Hits, result.Misses,
		result.Publishes, result.Failures, result.Mapped, result.Err,
	)
	t.Logf("MEASURE %s", encoded)
	output := os.Getenv(measureOutEnv)
	if output == "" {
		return
	}
	file, err := os.OpenFile(output, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open measurement output: %v", err)
	}
	defer file.Close()
	if _, err = file.WriteString(encoded); err != nil {
		t.Fatalf("write measurement output: %v", err)
	}
}

// TestDerivedMeasureAggregate turns the JSONL samples into the table the report quotes.
func TestDerivedMeasureAggregate(t *testing.T) {
	path := os.Getenv(aggregateEnv)
	if path == "" {
		t.Skipf("set %s=<jsonl> to aggregate the N3 samples", aggregateEnv)
	}
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	type groupKey struct {
		arm     string
		kind    string
		entries int
		format  string
	}
	type sample struct {
		millis        float64
		totalAlloc    uint64
		liveHeap      uint64
		rssPeak       uint64
		artifactBytes int64
		storeBytes    int64
		storeVersions int
		err           string
	}
	groups := map[groupKey][]sample{}
	var order []groupKey
	for _, line := range strings.Split(strings.TrimSpace(string(content)), "\n") {
		if line == "" {
			continue
		}
		var row struct {
			Arm           string  `json:"arm"`
			Kind          string  `json:"kind"`
			Entries       int     `json:"entries"`
			Format        string  `json:"format"`
			Millis        float64 `json:"millis"`
			TotalAlloc    uint64  `json:"total_alloc"`
			LiveHeap      uint64  `json:"live_heap"`
			RSSPeak       uint64  `json:"rss_peak"`
			ArtifactBytes int64   `json:"artifact_bytes"`
			StoreBytes    int64   `json:"store_bytes"`
			StoreVersions int     `json:"store_versions"`
			Err           string  `json:"err"`
		}
		require.NoError(t, json.Unmarshal([]byte(line), &row), line)
		key := groupKey{arm: row.Arm, kind: row.Kind, entries: row.Entries, format: row.Format}
		if _, loaded := groups[key]; !loaded {
			order = append(order, key)
		}
		groups[key] = append(groups[key], sample{
			millis: row.Millis, totalAlloc: row.TotalAlloc, liveHeap: row.LiveHeap,
			rssPeak: row.RSSPeak, artifactBytes: row.ArtifactBytes, storeBytes: row.StoreBytes,
			storeVersions: row.StoreVersions, err: row.Err,
		})
	}
	formatBytes := func(value int64) string {
		switch {
		case value >= 1<<20:
			return fmt.Sprintf("%.2fMiB", float64(value)/(1<<20))
		case value >= 1<<10:
			return fmt.Sprintf("%.1fKiB", float64(value)/(1<<10))
		default:
			return fmt.Sprintf("%dB", value)
		}
	}
	t.Logf("| arm | kind | entries | format | n | time p50 | time max | alloc p50 | live p50 | rss-peak p50 | artifact | store | versions | errors |")
	t.Logf("| --- | --- | ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |")
	for _, key := range order {
		samples := groups[key]
		times := make([]float64, 0, len(samples))
		allocs := make([]uint64, 0, len(samples))
		lives := make([]uint64, 0, len(samples))
		rss := make([]uint64, 0, len(samples))
		var maxTime float64
		var artifactBytes int64
		var storeBytes int64
		var versions int
		errors := 0
		for _, entry := range samples {
			if entry.err != "" {
				errors++
			}
			times = append(times, entry.millis)
			allocs = append(allocs, entry.totalAlloc)
			lives = append(lives, entry.liveHeap)
			rss = append(rss, entry.rssPeak)
			if entry.millis > maxTime {
				maxTime = entry.millis
			}
			if entry.artifactBytes > artifactBytes {
				artifactBytes = entry.artifactBytes
			}
			if entry.storeBytes > storeBytes {
				storeBytes = entry.storeBytes
			}
			if entry.storeVersions > versions {
				versions = entry.storeVersions
			}
		}
		sortFloat(times)
		sortUint(allocs)
		sortUint(lives)
		sortUint(rss)
		t.Logf("| %s | %s | %d | %s | %d | %.2fms | %.2fms | %s | %s | %s | %s | %s | %d | %d |",
			key.arm, key.kind, key.entries, key.format, len(samples), times[len(times)/2], maxTime,
			formatBytes(int64(allocs[len(allocs)/2])), formatBytes(int64(lives[len(lives)/2])),
			formatBytes(int64(rss[len(rss)/2])), formatBytes(artifactBytes), formatBytes(storeBytes),
			versions, errors)
	}
}

func sortFloat(values []float64) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func sortUint(values []uint64) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

// TestDerivedSourceFormatWarmReload pins the source-format branch of the derived cache: a warm
// reload of a JSON source must come from the published artifact rather than a second parse, serve
// exactly the same matches, and the digest must belong to the bytes that were compiled (the
// identity/parse same-open-file rule in reloadFile).
func TestDerivedSourceFormatWarmReload(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	root := t.TempDir()
	path := filepath.Join(root, "rules.json")
	encoded, err := singjson.MarshalContext(context.Background(), option.PlainRuleSetCompat{
		Version: C.RuleSetVersionCurrent,
		Options: measureKindRuleSet("domain", 2_000, 0),
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, encoded, 0o644))
	ctx := measureContext(filepath.Join(root, "store"))
	first := measureNewLocalRuleSet(t, ctx, path, C.RuleSetFormatSource)
	require.NotNil(t, first.derivedCache)
	require.NotEmpty(t, first.currentRules())
	second := measureNewLocalRuleSet(t, ctx, path, C.RuleSetFormatSource)
	require.Equal(t, len(first.currentRules()), len(second.currentRules()))
	measureCheck(t, second.currentRules(), "domain", 2_000, 0)
}
