package w8measure

// G01 fault injection — bounded, on a temporary directory only.
//
// # What can be injected where
//
//	cacheFile interface   short save, failed save, garbage load. These are faults of the
//	                      legacy single-slot cache the product calls through an interface,
//	                      so the injected failure is at a real product seam.
//	HTTP body             a real short read: Content-Length larger than the bytes that
//	                      arrive, then the connection closes.
//	store rename/flush    NOT available on this host. The artifact store calls os.Rename
//	                      and os.File.Close directly; there is no injectable seam, strace
//	                      is not installed, and Windows has no filesystem filter driver at
//	                      the harness's disposal. See the NOT_RUN list in the report.
//	ENOSPC                measured on Linux only (see the ENOSPC section): a 1 MiB tmpfs
//	                      inside an unprivileged mount namespace. On Windows a bounded
//	                      volume needs an elevated virtual disk, which this session cannot
//	                      create.
//
// Every test below uses the sample's own $W8_ROOT; nothing writes to a real volume beyond
// a few kilobytes of rule-set fixtures.

import (
	"os"
	"runtime"
	"testing"
	"time"

	C "github.com/sagernet/sing-box/constant"

	"github.com/stretchr/testify/require"
)

// TestW8G01CacheFaults proves the legacy cache cannot turn a truncated or garbage single
// slot into a published generation.
func TestW8G01CacheFaults(t *testing.T) {
	fault := w8Env("W8_FAULT", "save_short")
	entries := w8EnvInt(t, "W8_ENTRIES", 1000)
	format := w8Env("W8_RULESET_FORMAT", C.RuleSetFormatBinary)
	baseDir := w8Dir(t, "g01-fault")

	row := map[string]any{"case": "g01", "scenario": "cachefault", "fault": fault, "entries": entries, "format": format}
	defer func() {
		if t.Failed() {
			row["failed"] = true
		}
		w8Emit(t, row)
	}()

	payloadA := w8EncodeRuleSet(t, w8RuleSetDocument(entries, 0), format)
	payloadB := w8EncodeRuleSet(t, w8RuleSetDocument(entries, 1), format)
	digestA := w8SHA256(payloadA)
	digestB := w8SHA256(payloadB)
	row["digest_a"] = digestA
	row["digest_b"] = digestB

	server := w8StartServer(t)
	server.serve(payloadA)

	// Prepare the legacy slot with document A while the cache is healthy.
	ctx, cacheFile := w8G01Context(t, baseDir, "cachefile")
	w8InstallTransport(ctx, w8NewTransport(t, server))
	prepared, err := w8StartRuleSet(t, ctx, format)
	require.NoError(t, err, "the healthy cold fetch must succeed")
	require.True(t, w8RuleSetMatches(prepared, w8Domain(0, entries-1)))
	require.Equal(t, len(payloadA), cacheFile.stored("w8-rule-set"))
	require.NoError(t, prepared.Close())
	row["prepared_cache_bytes"] = cacheFile.stored("w8-rule-set")
	row["prepared_cache_digest"] = w8SHA256(cacheFileSavedBytes(t, cacheFile, "w8-rule-set"))

	switch fault {
	case "save_short", "save_error":
		// A refresh must publish the LIVE generation from the downloaded bytes even when
		// the legacy cache cannot store them; the next start must then either restore the
		// last good document or refuse, never publish the damaged slot.
		cacheFile.setFault(w8CacheFault(w8FaultFor(fault)))
		server.serve(payloadB)
		refreshCtx, refreshCache := w8G01Context(t, baseDir, "cachefile")
		refreshCache.saved = cacheFile.saved
		refreshCache.setFault(w8CacheFault(w8FaultFor(fault)))
		w8InstallTransport(refreshCtx, w8NewTransport(t, server))
		ruleSet, startErr := w8StartRuleSetWithInterval(t, refreshCtx, format, 300*time.Millisecond)
		require.NoError(t, startErr, "the start must succeed from the healthy slot")
		ruleSet.IncRef()
		require.True(t, w8RuleSetMatches(ruleSet, w8Domain(0, entries-1)), "the healthy slot did not restore")
		updater := newRuleSetUpdater(refreshCtx, ruleSet)
		updater.Start()
		require.Eventually(t, func() bool {
			return w8RuleSetMatches(ruleSet, w8Domain(1, entries-1))
		}, 20*time.Second, 2*time.Millisecond, "the refresh never published document B")
		require.NoError(t, updater.Close())
		require.NoError(t, ruleSet.Close())
		row["faulted_cache_bytes"] = refreshCache.stored("w8-rule-set")
		row["faulted_cache_is_truncated"] = refreshCache.stored("w8-rule-set") != len(payloadB)
		row["faulted_cache_digest"] = w8SHA256(cacheFileSavedBytes(t, refreshCache, "w8-rule-set"))
		// The next offline start must not serve a corrupted slot. A short slot cannot
		// decode, so the product must refuse; a failed save leaves the last good slot,
		// which is a correct restore.
		offlineCtx, offlineCache := w8G01Context(t, baseDir, "cachefile")
		offlineCache.saved = refreshCache.saved
		w8InstallTransport(offlineCtx, w8FailingTransport(t, server))
		nextRuleSet, nextErr := w8StartRuleSet(t, offlineCtx, format)
		nextMatchesA := nextErr == nil && nextRuleSet != nil && w8RuleSetMatches(nextRuleSet, w8Domain(0, entries-1))
		nextMatchesB := nextErr == nil && nextRuleSet != nil && w8RuleSetMatches(nextRuleSet, w8Domain(1, entries-1))
		if nextErr == nil {
			_ = nextRuleSet.Close()
		}
		row["next_offline_error"] = ""
		if nextErr != nil {
			row["next_offline_error"] = nextErr.Error()
		}
		row["next_offline_matches_a"] = nextMatchesA
		row["next_offline_matches_b"] = nextMatchesB
		if fault == "save_short" {
			// A truncated slot must not decode: an offline start has no good document.
			require.Error(t, nextErr, "a truncated legacy cache slot was published as a live generation")
		} else {
			// The save failed, so the last good document is still there and is correct.
			require.NoError(t, nextErr, "a failed cache save destroyed the last good slot")
			require.True(t, nextMatchesA, "a failed cache save lost the last good document")
		}

	case "load_garbage":
		// The slot holds bytes that do not decode. A start with the network must refetch
		// and publish the correct document instead of failing permanently.
		cacheFile.setFault(w8CacheLoadGarbage)
		garbageCtx, garbageCache := w8G01Context(t, baseDir, "cachefile")
		garbageCache.saved = cacheFile.saved
		garbageCache.setFault(w8CacheLoadGarbage)
		w8InstallTransport(garbageCtx, w8NewTransport(t, server))
		recovered, recoveredErr := w8StartRuleSet(t, garbageCtx, format)
		row["garbage_start_error"] = ""
		if recoveredErr != nil {
			row["garbage_start_error"] = recoveredErr.Error()
		}
		require.NoError(t, recoveredErr, "a garbage cache slot must not make the start fail when the network is available")
		require.True(t, w8RuleSetMatches(recovered, w8Domain(0, entries-1)), "the refetched generation does not match")
		row["garbage_recovered_cache_bytes"] = garbageCache.stored("w8-rule-set")
		row["garbage_recovered_cache_digest"] = w8SHA256(cacheFileSavedBytes(t, garbageCache, "w8-rule-set"))
		require.Equal(t, len(payloadA), garbageCache.stored("w8-rule-set"), "the garbage slot was not replaced")
		require.Equal(t, digestA, w8SHA256(cacheFileSavedBytes(t, garbageCache, "w8-rule-set")))
		require.NoError(t, recovered.Close())
	default:
		t.Fatalf("unknown fault %q", fault)
	}
	row["final_goroutines"] = runtime.NumGoroutine()
	row["final_peak_rss"] = w8ReadProc().PeakRSS
}

func w8FaultFor(name string) int {
	if name == "save_error" {
		return int(w8CacheSaveError)
	}
	return int(w8CacheSaveShort)
}

// w8FailingTransport returns a transport that refuses every request.
func w8FailingTransport(t *testing.T, server *w8Server) *w8Transport {
	t.Helper()
	transport := w8NewTransport(t, server)
	transport.fail.Store(true)
	return transport
}

func cacheFileSavedBytes(t *testing.T, cacheFile *w8CacheFile, tag string) []byte {
	t.Helper()
	cacheFile.access.Lock()
	defer cacheFile.access.Unlock()
	saved := cacheFile.saved[tag]
	if saved == nil {
		return nil
	}
	return append([]byte(nil), saved.Content...)
}

// TestW8G01ShortBody is a real short read on the network: the declared Content-Length is
// larger than the bytes that arrive and the connection closes.
func TestW8G01ShortBody(t *testing.T) {
	entries := w8EnvInt(t, "W8_ENTRIES", 10000)
	format := w8Env("W8_RULESET_FORMAT", C.RuleSetFormatBinary)
	baseDir := w8Dir(t, "g01-short-body")

	row := map[string]any{"case": "g01", "scenario": "shortbody", "entries": entries, "format": format}
	defer func() {
		if t.Failed() {
			row["failed"] = true
		}
		w8Emit(t, row)
	}()

	payload := w8EncodeRuleSet(t, w8RuleSetDocument(entries, 0), format)
	require.Greater(t, len(payload), 4, "the short-body fixture needs a body longer than its half")
	server := w8StartServer(t)
	server.serve(payload)
	server.shortBody.Store(true)

	storeDir := w8ArtifactStoreDir(t, baseDir)
	ctx := w8RemoteContext(t, baseDir)
	w8InstallTransport(ctx, w8NewTransport(t, server))
	began := w8Begin()
	ruleSet, startErr := w8StartRuleSet(t, ctx, format)
	row["start_error"] = ""
	if startErr != nil {
		row["start_error"] = startErr.Error()
	}
	row["payload_bytes"] = len(payload)
	row["server_hits"] = server.hits.Load()
	census, files := w8Census(t, storeDir)
	row["store_census"] = census
	row["store_files"] = files
	row["live_matches"] = startErr == nil && ruleSet != nil && w8RuleSetMatches(ruleSet, w8Domain(0, entries-1))
	if ruleSet != nil {
		_ = ruleSet.Close()
	}
	require.Error(t, startErr, "a short body was accepted as a complete rule-set download")
	require.Equal(t, 0, census.VersionDirs, "a short body published a version")
	// No partial artifact may be left behind for the next start to find.
	digest := w8StoreDigestOrEmpty(t, storeDir)
	row["store_digest_after_short_body"] = digest
	require.Equal(t, "", digest, "a short body left a stored artifact")

	// The next round against a healthy server must succeed.
	server.shortBody.Store(false)
	hitsBefore := server.hits.Load()
	recoveryCtx := w8RemoteContext(t, baseDir)
	recoveryTransport := w8NewTransport(t, server)
	w8InstallTransport(recoveryCtx, recoveryTransport)
	recovered, recoveredErr := w8StartRuleSet(t, recoveryCtx, format)
	if recoveredErr == nil {
		require.True(t, w8RuleSetMatches(recovered, w8Domain(0, entries-1)), "the recovered generation does not match")
		require.NoError(t, recovered.Close())
	}
	row["recovery_error"] = ""
	if recoveredErr != nil {
		row["recovery_error"] = recoveredErr.Error()
	}
	row["recovery_hits"] = server.hits.Load() - hitsBefore
	row["recovery_census"] = mustCensus(t, storeDir)
	require.NoError(t, recoveredErr, "the next round after a short body did not succeed")
	require.Equal(t, w8SHA256(payload), w8StoreDigestOrEmpty(t, storeDir), "the recovered store does not hold the payload")
	row["recovered_heap_alloc"] = w8ReadMem().HeapAlloc
	row["recovered_heap_objects"] = w8ReadMem().HeapObjects
	row["recovered_peak_rss"] = w8ReadProc().PeakRSS
	proc := began.delta()
	row["short_body_read_ops"] = proc.ReadOps
	row["short_body_write_ops"] = proc.WriteOps
	row["short_body_peak_rss"] = proc.PeakRSS
}

func mustCensus(t *testing.T, storeDir string) w8DiskCensus {
	t.Helper()
	census, _ := w8Census(t, storeDir)
	return census
}

// TestW8G01ENOSPC is the controlled out-of-space fault. It only runs when the process was
// started with its base directory on a bounded filesystem (the driver mounts a 1 MiB tmpfs
// in an unprivileged user+mount namespace and points W8_ROOT there). It asserts that the
// product reports the failure and leaves no valid version behind; it never fills a real
// volume because the filesystem itself is 1 MiB.
func TestW8G01ENOSPC(t *testing.T) {
	if os.Getenv("W8_ENOSPC") != "1" {
		t.Skip("W8_ENOSPC=1 is set only by the bounded-filesystem driver")
	}
	entries := 10000
	format := C.RuleSetFormatBinary
	baseDir := w8Dir(t, "g01-enospc")
	row := map[string]any{"case": "g01", "scenario": "enospc", "entries": entries, "base_dir": baseDir}
	defer func() {
		if t.Failed() {
			row["failed"] = true
		}
		w8Emit(t, row)
	}()

	payload := w8EncodeRuleSet(t, w8RuleSetDocument(entries, 0), format)
	row["payload_bytes"] = len(payload)
	server := w8StartServer(t)
	server.serve(payload)
	ctx := w8RemoteContext(t, baseDir)
	w8InstallTransport(ctx, w8NewTransport(t, server))
	ruleSet, startErr := w8StartRuleSet(t, ctx, format)
	row["start_error"] = ""
	if startErr != nil {
		row["start_error"] = startErr.Error()
	}
	if ruleSet != nil {
		_ = ruleSet.Close()
	}
	storeDir := w8ArtifactStoreDir(t, baseDir)
	census, files := w8Census(t, storeDir)
	row["store_census"] = census
	row["store_files"] = files
	row["store_digest"] = w8StoreDigestOrEmpty(t, storeDir)
	row["disk_free_bytes"] = w8DiskFree(t, baseDir)
	require.Error(t, startErr, "a full filesystem was reported as a successful download")
	require.Equal(t, "", w8StoreDigestOrEmpty(t, storeDir), "a failed write left a loadable artifact")
	require.Zero(t, census.Temps, "a failed write left a temp directory behind")
	row["final_goroutines"] = runtime.NumGoroutine()
}

// w8DiskFree is only used to prove the bounded filesystem was really small.
func w8DiskFree(t *testing.T, path string) int64 {
	t.Helper()
	return diskFreeBytes(path)
}
