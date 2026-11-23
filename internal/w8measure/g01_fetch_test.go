package w8measure

// G01 — the REAL remote fetch, at 1k and 10k entries, through the product entry points.
//
// # Modes (W8_MODE)
//
//	store      filemanager base path only: the versioned artifact store is the cache
//	cachefile  adapter.CacheFile only: the legacy single-slot cache is the only cache
//	both       both are present, which is the migration shape
//
// # Arms (W8_ARM)
//
//	cold       the first StartContext: one 200 download, parse and publish
//	update200  a product refresh (RuleSetUpdater) that receives changed content: 200,
//	           parse, publish, old version collected
//	update304  a product refresh answered 304: no parse, no publish, live generation and
//	           stored version unchanged, then a 200 refresh still succeeds
//	update500  a product refresh answered 500: the live generation keeps serving the last
//	           good document, then a 200 refresh still succeeds
//
// # What is measured
//
//	heap:    Go HeapAlloc/HeapObjects/TotalAlloc sampled naturally every 2 ms during the
//	         action (no forced collection inside the action)
//	rss:     the OS working-set delta across the action and the process peak
//	disk:    the artifact store census and published digest before/after
//	etag:    what the server actually received in If-None-Match
//	atomic:  a concurrent observer walks the store every 200 us during the action and
//	         records temp staging directories and every artifact digest visible. A digest
//	         that is neither the old nor the new one is a partially published artifact;
//	         the number of new version directories is the number of atomic publications.
//	         Raw rename(2) syscall counts are NOT available on this host: strace and perf
//	         are not installed in the WSL distribution, and Windows has no process-level
//	         rename counter. Visibility is the observable, not a syscall count.

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/route/rule"
	C "github.com/sagernet/sing-box/constant"

	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Atomic-publication observer
// ---------------------------------------------------------------------------

type w8G01Observer struct {
	storeDir string
	knownOld string
	knownNew string
	stop     chan struct{}
	done     chan struct{}

	polls     atomic.Int64
	tempsSeen atomic.Int64
	digests   sync.Map // digest -> struct{}
}

func w8StartObserver(storeDir string, knownOld string, knownNew string) *w8G01Observer {
	observer := &w8G01Observer{storeDir: storeDir, knownOld: knownOld, knownNew: knownNew, stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(observer.done)
		ticker := time.NewTicker(200 * time.Microsecond)
		defer ticker.Stop()
		for {
			select {
			case <-observer.stop:
				return
			case <-ticker.C:
				observer.poll()
			}
		}
	}()
	return observer
}

func (o *w8G01Observer) poll() {
	o.polls.Add(1)
	entries, err := os.ReadDir(o.storeDir)
	if err != nil {
		return
	}
	for _, keyEntry := range entries {
		if !keyEntry.IsDir() {
			continue
		}
		keyPath := filepath.Join(o.storeDir, keyEntry.Name())
		versions, versionErr := os.ReadDir(keyPath)
		if versionErr != nil {
			continue
		}
		for _, versionEntry := range versions {
			if !versionEntry.IsDir() {
				continue
			}
			if len(versionEntry.Name()) >= 5 && versionEntry.Name()[:5] == ".tmp-" {
				o.tempsSeen.Add(1)
				continue
			}
			content, readErr := os.ReadFile(filepath.Join(keyPath, versionEntry.Name(), "artifact.bin"))
			if readErr != nil {
				continue
			}
			o.digests.Store(w8SHA256(content), struct{}{})
		}
	}
}

func (o *w8G01Observer) finish() (polls, temps, partial int64, digests []string) {
	close(o.stop)
	<-o.done
	o.digests.Range(func(key any, _ any) bool {
		digests = append(digests, key.(string))
		return true
	})
	for _, digest := range digests {
		if digest != o.knownOld && digest != o.knownNew {
			partial++
		}
	}
	return o.polls.Load(), o.tempsSeen.Load(), partial, digests
}

// ---------------------------------------------------------------------------
// Natural heap sampler
// ---------------------------------------------------------------------------

type w8HeapSampler struct {
	stop chan struct{}
	done chan struct{}

	peakAlloc   atomic.Uint64
	peakObjects atomic.Uint64
	samples     atomic.Int64
}

func w8StartHeapSampler(interval time.Duration) *w8HeapSampler {
	sampler := &w8HeapSampler{stop: make(chan struct{}), done: make(chan struct{})}
	initial := w8ReadMem()
	sampler.peakAlloc.Store(initial.HeapAlloc)
	sampler.peakObjects.Store(initial.HeapObjects)
	go func() {
		defer close(sampler.done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-sampler.stop:
				return
			case <-ticker.C:
				mem := w8ReadMem()
				sampler.samples.Add(1)
				for {
					current := sampler.peakAlloc.Load()
					if mem.HeapAlloc <= current || sampler.peakAlloc.CompareAndSwap(current, mem.HeapAlloc) {
						break
					}
				}
				for {
					current := sampler.peakObjects.Load()
					if mem.HeapObjects <= current || sampler.peakObjects.CompareAndSwap(current, mem.HeapObjects) {
						break
					}
				}
			}
		}
	}()
	return sampler
}

func (s *w8HeapSampler) finish() (peakAlloc uint64, peakObjects uint64, samples int64) {
	close(s.stop)
	<-s.done
	return s.peakAlloc.Load(), s.peakObjects.Load(), s.samples.Load()
}

// ---------------------------------------------------------------------------
// Settings and context shaping
// ---------------------------------------------------------------------------

type w8G01Settings struct {
	mode    string
	arm     string
	entries int
	format  string
}

func w8G01ReadSettings(t *testing.T) w8G01Settings {
	t.Helper()
	settings := w8G01Settings{
		mode:    w8Env("W8_MODE", "store"),
		arm:     w8Env("W8_ARM", "cold"),
		entries: w8EnvInt(t, "W8_ENTRIES", 1000),
		format:  w8Env("W8_RULESET_FORMAT", C.RuleSetFormatBinary),
	}
	require.Contains(t, []string{"store", "cachefile", "both"}, settings.mode)
	require.Contains(t, []string{"cold", "update200", "update304", "update500"}, settings.arm)
	require.Contains(t, []int{1000, 10000}, settings.entries)
	return settings
}

func w8G01Context(t *testing.T, baseDir string, mode string) (context.Context, *w8CacheFile) {
	t.Helper()
	switch mode {
	case "store":
		return w8RemoteContext(t, baseDir), nil
	case "cachefile":
		ctx := w8RemoteContext(t, "")
		cacheFile := w8NewCacheFile()
		w8InstallCacheFile(ctx, cacheFile)
		return ctx, cacheFile
	case "both":
		ctx := w8RemoteContext(t, baseDir)
		cacheFile := w8NewCacheFile()
		w8InstallCacheFile(ctx, cacheFile)
		return ctx, cacheFile
	default:
		t.Fatalf("unknown mode %q", mode)
		return nil, nil
	}
}

// TestW8G01Fetch measures the real 200/304 fetch and refresh path.
func TestW8G01Fetch(t *testing.T) {
	settings := w8G01ReadSettings(t)
	baseDir := w8Dir(t, "g01-"+settings.mode)
	storeDir := w8ArtifactStoreDir(t, baseDir)

	row := map[string]any{
		"case":    "g01",
		"mode":    settings.mode,
		"arm":     settings.arm,
		"entries": settings.entries,
		"format":  settings.format,
	}
	defer func() {
		if t.Failed() {
			row["failed"] = true
		}
		w8Emit(t, row)
	}()

	payloadA := w8EncodeRuleSet(t, w8RuleSetDocument(settings.entries, 0), settings.format)
	payloadB := w8EncodeRuleSet(t, w8RuleSetDocument(settings.entries, 1), settings.format)
	digestA := w8SHA256(payloadA)
	digestB := w8SHA256(payloadB)
	row["payload_bytes"] = len(payloadA)
	row["digest_a"] = digestA
	row["digest_b"] = digestB

	server := w8StartServer(t)
	server.serve(payloadA)
	server.etag.Store(`"w8-etag-a"`)

	ctx, cacheFile := w8G01Context(t, baseDir, settings.mode)
	transport := w8NewTransport(t, server)
	w8InstallTransport(ctx, transport)
	// The updater interval is short so one refresh can be observed inside one process.
	const updateInterval = 500 * time.Millisecond
	ruleSet := w8NewRemoteRuleSet(t, ctx, settings.format, updateInterval)
	ruleSet.IncRef() // the Router holds a live rule set; see updateOne's refs==0 release

	// --- cold: one 200 download ------------------------------------------
	coldProcBefore := w8ReadProc()
	coldHeap := w8StartHeapSampler(2 * time.Millisecond)
	coldStarted := time.Now()
	require.NoError(t, ruleSet.StartContext(ctx, adapter.NewHTTPStartContext()), "the cold fetch must succeed")
	coldElapsed := time.Since(coldStarted)
	coldHeapAlloc, coldHeapObjects, coldHeapSamples := coldHeap.finish()
	coldProcAfter := w8ReadProc()
	coldMem := w8ReadMem()
	require.True(t, w8RuleSetMatches(ruleSet, w8Domain(0, settings.entries-1)), "the cold generation does not match")
	row["cold_ms"] = float64(coldElapsed.Nanoseconds()) / 1e6
	row["cold_server_hits"] = server.hits.Load()
	row["cold_transport_hits"] = transport.hits.Load()
	row["cold_etag_sent"] = server.lastRequestEtag()
	row["cold_heap_peak_alloc"] = coldHeapAlloc
	row["cold_heap_peak_objects"] = coldHeapObjects
	row["cold_heap_samples"] = coldHeapSamples
	row["cold_heap_after_alloc"] = coldMem.HeapAlloc
	row["cold_heap_after_objects"] = coldMem.HeapObjects
	row["cold_total_alloc"] = coldMem.TotalAlloc
	row["cold_rss_delta"] = int64(coldProcAfter.RSS) - int64(coldProcBefore.RSS)
	row["cold_peak_rss"] = coldProcAfter.PeakRSS
	row["cold_read_ops"] = coldProcAfter.ReadOps - coldProcBefore.ReadOps
	row["cold_write_ops"] = coldProcAfter.WriteOps - coldProcBefore.WriteOps
	row["goroutines_after_cold"] = runtime.NumGoroutine()

	storeCensus, storeFiles := w8Census(t, storeDir)
	row["cold_store_census"] = storeCensus
	row["cold_store_files"] = storeFiles
	coldCacheLoads, coldCacheSaves := 0, 0
	if cacheFile != nil {
		coldCacheLoads, coldCacheSaves = cacheFile.counters()
	}
	row["cold_cache_loads"] = coldCacheLoads
	row["cold_cache_saves"] = coldCacheSaves
	if settings.mode == "store" || settings.mode == "both" {
		require.Equal(t, 1, storeCensus.VersionDirs, "the cold fetch published a different number of versions")
		require.Equal(t, digestA, w8StoreDigestOrEmpty(t, storeDir))
	}
	if settings.mode == "cachefile" || settings.mode == "both" {
		require.Equal(t, len(payloadA), cacheFile.stored("w8-rule-set"), "the legacy cache stored different bytes")
	}

	if settings.arm == "cold" {
		row["final_heap_alloc"] = w8ReadMem().HeapAlloc
		row["final_heap_objects"] = w8ReadMem().HeapObjects
		row["final_peak_rss"] = w8ReadProc().PeakRSS
		require.NoError(t, ruleSet.Close())
		return
	}

	// --- the refresh arm --------------------------------------------------
	// The product refresh is the RuleSetUpdater, not a direct fetch call.
	var modeOfServer string
	switch settings.arm {
	case "update200":
		server.serve(payloadB)
		server.etag.Store(`"w8-etag-b"`)
		modeOfServer = "changed"
	case "update304":
		server.serveNotModified(`"w8-etag-a"`)
		modeOfServer = "not-modified"
	case "update500":
		server.serveStatus(500)
		modeOfServer = "failure"
	}
	row["update_server_mode"] = modeOfServer
	row["status_before_update"] = server.status.Load()

	observer := w8StartObserver(storeDir, digestA, digestB)
	updateProcBefore := w8ReadProc()
	updateHeap := w8StartHeapSampler(2 * time.Millisecond)
	updateStarted := time.Now()
	updater := rule.NewRuleSetUpdater(ctx, []adapter.RuleSet{ruleSet})
	updater.Start()
	t.Cleanup(func() { _ = updater.Close() })

	hitsBeforeUpdate := server.hits.Load()
	require.Eventually(t, func() bool {
		return server.hits.Load() > hitsBeforeUpdate
	}, 20*time.Second, 2*time.Millisecond, "the product updater never issued the refresh request")
	// Let the attempt finish. The request has been served; the product still has to
	// decode/publish (200), or decide it kept the old generation (304/500).
	settle := time.Now()
	for {
		if settings.arm == "update200" && w8RuleSetMatches(ruleSet, w8Domain(1, settings.entries-1)) {
			break
		}
		if settings.arm != "update200" && time.Since(settle) > 400*time.Millisecond {
			break
		}
		if time.Since(settle) > 20*time.Second {
			t.Fatalf("the refresh did not settle")
		}
		time.Sleep(2 * time.Millisecond)
	}
	updateElapsed := time.Since(updateStarted)
	applyElapsed := time.Since(server.lastRequestTime())
	updateHeapAlloc, updateHeapObjects, updateHeapSamples := updateHeap.finish()
	updateProcAfter := w8ReadProc()
	updateMem := w8ReadMem()
	polls, tempsSeen, partial, digests := observer.finish()

	require.True(t, w8RuleSetMatches(ruleSet, w8Domain(0, settings.entries-1)) ||
		(settings.arm == "update200" && w8RuleSetMatches(ruleSet, w8Domain(1, settings.entries-1))),
		"the refresh dropped the live generation it was supposed to keep serving")
	row["update_ms"] = float64(updateElapsed.Nanoseconds()) / 1e6
	// The interval wait is not the product's cost: this is from the server receiving the
	// request to the product having settled (published for 200, decided for 304/500).
	row["update_apply_ms"] = float64(applyElapsed.Nanoseconds()) / 1e6
	row["update_server_hits"] = server.hits.Load() - hitsBeforeUpdate
	row["update_etag_sent"] = server.lastRequestEtag()
	row["update_heap_peak_alloc"] = updateHeapAlloc
	row["update_heap_peak_objects"] = updateHeapObjects
	row["update_heap_samples"] = updateHeapSamples
	row["update_heap_after_alloc"] = updateMem.HeapAlloc
	row["update_heap_after_objects"] = updateMem.HeapObjects
	row["update_total_alloc"] = updateMem.TotalAlloc
	row["update_rss_delta"] = int64(updateProcAfter.RSS) - int64(updateProcBefore.RSS)
	row["update_peak_rss"] = updateProcAfter.PeakRSS
	row["update_read_ops"] = updateProcAfter.ReadOps - updateProcBefore.ReadOps
	row["update_write_ops"] = updateProcAfter.WriteOps - updateProcBefore.WriteOps
	row["observer_polls"] = polls
	row["observer_temp_dirs_seen"] = tempsSeen
	row["observer_partial_artifacts"] = partial
	row["observer_digests"] = digests
	require.Zero(t, partial, "a partially published artifact was visible in the store")

	updateCensus, _ := w8Census(t, storeDir)
	row["update_store_census"] = updateCensus
	row["update_store_digest"] = w8StoreDigestOrEmpty(t, storeDir)
	if settings.mode == "store" || settings.mode == "both" {
		switch settings.arm {
		case "update200":
			require.Equal(t, digestB, w8StoreDigestOrEmpty(t, storeDir), "a 200 refresh did not publish the new document")
			require.LessOrEqual(t, updateCensus.VersionDirs, 2, "the refresh left more than two versions")
		default:
			require.Equal(t, digestA, w8StoreDigestOrEmpty(t, storeDir), "a non-200 refresh changed the stored document")
			require.Equal(t, 1, updateCensus.VersionDirs, "a non-200 refresh published a version")
		}
	}
	if cacheFile != nil {
		loadsAfter, savesAfter := cacheFile.counters()
		row["update_cache_loads"] = loadsAfter - coldCacheLoads
		row["update_cache_saves"] = savesAfter - coldCacheSaves
		if settings.arm == "update200" {
			require.Equal(t, len(payloadB), cacheFile.stored("w8-rule-set"), "a 200 refresh did not store the new document in the legacy cache")
		}
	}

	// --- next round: a 200 refresh after the unsuccessful one -------------
	server.serve(payloadB)
	server.etag.Store(`"w8-etag-b"`)
	hitsBeforeRecovery := server.hits.Load()
	require.Eventually(t, func() bool {
		return w8RuleSetMatches(ruleSet, w8Domain(1, settings.entries-1))
	}, 30*time.Second, 5*time.Millisecond, "the next 200 refresh never installed the new generation")
	recoveredDigest := w8StoreDigestOrEmpty(t, storeDir)
	recoveredCensus, _ := w8Census(t, storeDir)
	row["recovery_hits"] = server.hits.Load() - hitsBeforeRecovery
	row["recovery_digest"] = recoveredDigest
	row["recovery_census"] = recoveredCensus
	row["recovery_matches"] = w8RuleSetMatches(ruleSet, w8Domain(1, settings.entries-1))
	if settings.mode == "store" || settings.mode == "both" {
		require.Equal(t, digestB, recoveredDigest, "the next round did not repair the stored generation")
	}
	if cacheFile != nil {
		require.Equal(t, len(payloadB), cacheFile.stored("w8-rule-set"), "the next round did not store the new document")
	}
	row["recovered_heap_alloc"] = w8ReadMem().HeapAlloc
	row["recovered_heap_objects"] = w8ReadMem().HeapObjects
	row["recovered_peak_rss"] = w8ReadProc().PeakRSS
	row["recovered_goroutines"] = runtime.NumGoroutine()
	require.NoError(t, updater.Close())
	require.NoError(t, ruleSet.Close())
}

// keep the constant import meaningful across format changes.
var _ = C.RuleSetFormatBinary
