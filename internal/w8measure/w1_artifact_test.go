package w8measure

// W1 — artifact self-healing, measured as DISK STATE, not as live rules.
//
// # Why the live rule set is not the measurement
//
// A running rule set answers Match() from memory. After the store is damaged, collected,
// or replaced, the live object keeps matching the document it parsed at start time, so a
// passing Match() says nothing about what the next process will load. That is exactly the
// gap F-CODEX-01 was: a damaged version directory could be handed back as a "successful"
// re-download. Every row here therefore records the live answer, the stored bytes on
// disk (digest and size), and the answer of a FRESH start with no network:
//
//	live     the current object's Match()
//	stored   <base>/rule-set-artifacts/<key>/<id>/artifact.bin, hashed
//	offline  a second process-equivalent start from the same directory with the
//	         transport set to fail every request
//
// # Scenarios
//
//	publish  one download, one version directory; dedupe/second start; LoadExact(pinned);
//	         Collect with and without a lease; then live vs disk vs offline.
//	damaged  a version is damaged in place (bytes, manifest, or missing artifact) and then:
//	         offline start (must NOT publish the damaged document) -> online start (must
//	         re-download and repair) -> offline start again (must restore the repaired
//	         version with zero requests).

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/artifact"
	"github.com/sagernet/sing-box/route/rule"
	C "github.com/sagernet/sing-box/constant"

	"github.com/stretchr/testify/require"
)

func w8ArtifactStoreDir(t *testing.T, baseDir string) string {
	t.Helper()
	return filepath.Join(baseDir, "rule-set-artifacts")
}

// w8StartRuleSet starts one remote rule set and returns the product object plus the error.
// It is the product entry point: no rule bytes are handed in.
func w8StartRuleSet(t *testing.T, ctx context.Context, format string) (*rule.RemoteRuleSet, error) {
	t.Helper()
	return w8StartRuleSetWithInterval(t, ctx, format, 0)
}

func w8StartRuleSetWithInterval(t *testing.T, ctx context.Context, format string, interval time.Duration) (*rule.RemoteRuleSet, error) {
	t.Helper()
	ruleSet := w8NewRemoteRuleSet(t, ctx, format, interval)
	return ruleSet, ruleSet.StartContext(ctx, adapter.NewHTTPStartContext())
}

// w8DamageArtifact damages the published version in place. The damage kinds are the three
// the W1 acceptance names: bad bytes, bad manifest, missing artifact.
func w8DamageArtifact(t *testing.T, storeDir string, kind string) (key string, versionID string) {
	t.Helper()
	key, versionID, _, _ = w8StoreVersionDigest(t, storeDir)
	versionDir := filepath.Join(storeDir, key, versionID)
	switch kind {
	case "bytes":
		artifactPath := filepath.Join(versionDir, "artifact.bin")
		content, err := os.ReadFile(artifactPath)
		require.NoError(t, err)
		garbage := make([]byte, len(content))
		_, err = rand.Read(garbage)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(artifactPath, garbage, 0o644))
	case "manifest":
		manifestPath := filepath.Join(versionDir, "manifest.json")
		require.NoError(t, os.WriteFile(manifestPath, []byte(`{"schema":"damaged"}`), 0o644))
	case "missing":
		require.NoError(t, os.Remove(filepath.Join(versionDir, "artifact.bin")))
	default:
		t.Fatalf("unknown damage kind %q", kind)
	}
	return key, versionID
}

// TestW8W1PublishRestore is the normal path plus the pinned/Collect checks.
func TestW8W1PublishRestore(t *testing.T) {
	format := w8Env("W8_RULESET_FORMAT", C.RuleSetFormatBinary)
	entries := w8EnvInt(t, "W8_ENTRIES", 1000)
	baseDir := w8Dir(t, "w1-publish")

	row := map[string]any{
		"case":     "w1",
		"scenario": "publish",
		"format":   format,
		"entries":  entries,
	}
	defer func() {
		if t.Failed() {
			row["failed"] = true
		}
		w8Emit(t, row)
	}()

	payload := w8EncodeRuleSet(t, w8RuleSetDocument(entries, 0), format)
	servedDigest := w8SHA256(payload)
	server := w8StartServer(t)
	server.serve(payload)

	// --- publish -----------------------------------------------------------
	began := w8BeginAttributed()
	publishStarted := time.Now()
	ctx := w8RemoteContext(t, baseDir)
	transport := w8NewTransport(t, server)
	w8InstallTransport(ctx, transport)
	ruleSet, err := w8StartRuleSet(t, ctx, format)
	publishElapsed := time.Since(publishStarted)
	require.NoError(t, err, "the first download must succeed")
	require.EqualValues(t, 1, server.hits.Load(), "a cold start must download exactly once")
	require.True(t, w8RuleSetMatches(ruleSet, w8Domain(0, entries-1)), "the loaded generation must match its own last member")
	require.False(t, w8RuleSetMatches(ruleSet, "absent.w8-measure.example"), "the loaded generation must not match a non-member")
	publishProc := began.delta()
	publishMem := w8ReadMem()
	row["publish_server_hits"] = server.hits.Load()
	row["publish_transport_hits"] = transport.hits.Load()
	row["publish_ms"] = float64(publishElapsed.Nanoseconds()) / 1e6
	row["publish_heap_alloc"] = publishMem.HeapAlloc
	row["publish_heap_objects"] = publishMem.HeapObjects
	row["publish_peak_rss"] = publishProc.PeakRSS
	row["publish_read_ops"] = publishProc.ReadOps
	row["publish_write_ops"] = publishProc.WriteOps
	row["publish_read_bytes"] = publishProc.ReadBytes
	row["publish_write_bytes"] = publishProc.WriteBytes
	row["goroutines_after_publish"] = runtime.NumGoroutine()

	storeDir := w8ArtifactStoreDir(t, baseDir)
	census, files := w8Census(t, storeDir)
	key, versionID, storedDigest, storedSize := w8StoreVersionDigest(t, storeDir)
	row["publish_census"] = census
	row["publish_files"] = files
	row["served_digest"] = servedDigest
	row["served_size"] = len(payload)
	row["stored_digest"] = storedDigest
	row["stored_size"] = storedSize
	row["store_key"] = key
	row["version_id"] = versionID
	row["live_matches"] = w8RuleSetMatches(ruleSet, w8Domain(0, entries-1))
	require.Equal(t, 1, census.Artifacts, "one download must publish exactly one artifact")
	require.Equal(t, 1, census.Manifests, "one download must publish exactly one manifest")
	require.Equal(t, 1, census.VersionDirs, "one download must publish exactly one version directory")
	require.Equal(t, servedDigest, storedDigest, "the stored bytes differ from the served bytes")
	require.EqualValues(t, len(payload), storedSize, "the stored size differs from the served size")
	require.Contains(t, versionID, servedDigest[:16], "the version directory does not carry the content digest")

	// Manifest identity: digest and size are the ones the store verified.
	manifest := w8ReadJSONFile(t, filepath.Join(storeDir, key, versionID, "manifest.json"))
	row["manifest"] = manifest
	require.Equal(t, servedDigest, manifest["digest"])
	require.EqualValues(t, len(payload), manifest["size"])

	// --- LoadExact(pinned) -------------------------------------------------
	// The pinned identity is the content digest (Version.ID()), not the directory name:
	// the directory carries a short prefix, the manifest carries the full digest.
	store, err := artifact.Open(storeDir)
	require.NoError(t, err)
	pinned, err := store.LoadExact(key, servedDigest)
	require.NoError(t, err, "LoadExact(pinned) must resolve a published version by identity")
	pinnedBytes, err := os.ReadFile(pinned.Path())
	require.NoError(t, err)
	row["pinned_digest"] = w8SHA256(pinnedBytes)
	row["pinned_id"] = pinned.ID()
	require.Equal(t, servedDigest, w8SHA256(pinnedBytes), "LoadExact returned different bytes")
	require.Equal(t, servedDigest, pinned.ID())
	pinned.Release()

	// --- dedupe: a second offline start must not download or add a version ---
	require.NoError(t, ruleSet.Close())
	offlineTransport := w8NewTransport(t, server)
	offlineTransport.fail.Store(true)
	offlineCtx := w8RemoteContext(t, baseDir)
	w8InstallTransport(offlineCtx, offlineTransport)
	offlineRuleSet, err := w8StartRuleSet(t, offlineCtx, format)
	require.NoError(t, err, "a stored version must restore without the network")
	require.Zero(t, offlineTransport.hits.Load(), "a stored version must restore without a request")
	require.True(t, w8RuleSetMatches(offlineRuleSet, w8Domain(0, entries-1)), "the restored generation must match")
	offlineCensus, _ := w8Census(t, storeDir)
	row["offline_census"] = offlineCensus
	row["offline_transport_hits"] = offlineTransport.hits.Load()
	row["offline_server_hits"] = server.hits.Load()
	require.Equal(t, census.Files, offlineCensus.Files, "an offline restore changed the number of files in the store")
	require.Equal(t, census.Bytes, offlineCensus.Bytes, "an offline restore changed the bytes in the store")
	require.EqualValues(t, 1, server.hits.Load(), "the offline restore contacted the server")
	require.NoError(t, offlineRuleSet.Close())

	// --- a second generation, a leased old version, and Collect ------------
	// Collect ranks versions and always keeps the NEWEST; an older one survives only while
	// a reader holds its lease. That is the protection a repair relies on: the damaged
	// directory cannot be removed under a reader, and disappears when the lease ends.
	payloadB := w8EncodeRuleSet(t, w8RuleSetDocument(entries, 1), format)
	digestB := w8SHA256(payloadB)
	held, err := store.LoadExact(key, servedDigest)
	require.NoError(t, err, "the lease on the first version must be obtainable")
	heldVersionID := filepath.Base(held.Path())
	heldBytes, err := os.ReadFile(held.Path())
	require.NoError(t, err)
	require.Equal(t, servedDigest, w8SHA256(heldBytes))

	// A second rule set on a LIVE transport: it restores A from the store, and its
	// product updater then publishes B through the real refresh path.
	refreshCtx := w8RemoteContext(t, baseDir)
	refreshTransport := w8NewTransport(t, server)
	w8InstallTransport(refreshCtx, refreshTransport)
	refreshRuleSet, err := w8StartRuleSetWithInterval(t, refreshCtx, format, 200*time.Millisecond)
	require.NoError(t, err, "the second rule set must restore the stored version")
	// The Router holds a reference to every live rule set in the product. Without it the
	// product's own post-refresh path (updateOnce: refs==0 -> rules=nil) drops the rules
	// after a successful update, which is correct product behaviour and would make this
	// measurement look like the refresh failed.
	refreshRuleSet.IncRef()
	require.Zero(t, refreshTransport.hits.Load(), "restoring A must not contact the server")
	require.True(t, w8RuleSetMatches(refreshRuleSet, w8Domain(0, entries-1)))
	server.serve(payloadB)
	updater := rule.NewRuleSetUpdater(refreshCtx, []adapter.RuleSet{refreshRuleSet})
	updater.Start()
	t.Cleanup(func() { _ = updater.Close() })
	require.Eventually(t, func() bool {
		return w8StoreDigestOrEmpty(t, storeDir) == digestB
	}, 20*time.Second, 5*time.Millisecond, "the product refresh never published the second document")
	// The disk version appears while the new generation is being installed; the live
	// answer is the observable of the swap completing.
	require.Eventually(t, func() bool {
		return w8RuleSetMatches(refreshRuleSet, w8Domain(1, entries-1))
	}, 20*time.Second, 2*time.Millisecond, "the refreshed generation never served the second document")
	require.NoError(t, store.Collect(key))
	twoVersions, _ := w8Census(t, storeDir)
	row["collect_with_leased_old_version"] = twoVersions
	row["leased_old_version_id"] = heldVersionID
	require.Equal(t, 2, twoVersions.VersionDirs,
		"Collect removed a version that a live lease protects")

	held.Release()
	require.NoError(t, store.Collect(key))
	oneVersion, oneFiles := w8Census(t, storeDir)
	row["collect_after_release"] = oneVersion
	row["collect_after_release_files"] = oneFiles
	require.Equal(t, 1, oneVersion.VersionDirs, "Collect kept the released old version")
	require.Equal(t, digestB, w8StoreDigestOrEmpty(t, storeDir), "Collect removed the newest version instead of the old one")

	// The live object still serves document B; the disk now holds one version.
	row["live_matches_after_collect"] = w8RuleSetMatches(refreshRuleSet, w8Domain(1, entries-1))

	// --- live vs disk: remove the version under the live object -----------
	// This is the point of the whole file. The running object keeps matching from memory;
	// a fresh start from the same directory cannot. A measurement that only looked at
	// Match() would call this a healthy state.
	liveMatchesBefore := w8RuleSetMatches(refreshRuleSet, w8Domain(1, entries-1))
	require.NoError(t, os.RemoveAll(filepath.Join(storeDir, key)))
	emptyCensus, emptyFiles := w8Census(t, storeDir)
	liveMatchesAfter := w8RuleSetMatches(refreshRuleSet, w8Domain(1, entries-1))
	row["disk_removed_census"] = emptyCensus
	row["disk_removed_files"] = emptyFiles
	row["live_matches_before_disk_removal"] = liveMatchesBefore
	row["live_matches_after_disk_removal"] = liveMatchesAfter
	require.True(t, liveMatchesAfter, "the in-memory generation stopped matching without being replaced")
	require.Equal(t, 0, emptyCensus.VersionDirs, "the version directory was not removed")

	freshCtx := w8RemoteContext(t, baseDir)
	freshTransport := w8NewTransport(t, server)
	freshTransport.fail.Store(true)
	w8InstallTransport(freshCtx, freshTransport)
	hitsBeforeFresh := server.hits.Load()
	freshRuleSet, freshErr := w8StartRuleSet(t, freshCtx, format)
	row["after_disk_removal_offline_error"] = ""
	if freshErr != nil {
		row["after_disk_removal_offline_error"] = freshErr.Error()
	}
	row["after_disk_removal_offline_matches"] = freshErr == nil && freshRuleSet != nil && w8RuleSetMatches(freshRuleSet, w8Domain(1, entries-1))
	row["after_disk_removal_offline_hits"] = server.hits.Load() - hitsBeforeFresh
	require.Error(t, freshErr, "a fresh offline start served a version directory that no longer exists")
	require.NoError(t, offlineRuleSet.Close())
	require.NoError(t, refreshRuleSet.Close())
	if freshRuleSet != nil {
		require.NoError(t, freshRuleSet.Close())
	}
}

// TestW8W1DamagedRepair is the F-CODEX-01 closure: damage a version in place, prove an
// offline start cannot publish the damaged document, then prove a correct re-download
// repairs the store and the next offline start restores the repaired bytes.
func TestW8W1DamagedRepair(t *testing.T) {
	format := w8Env("W8_RULESET_FORMAT", C.RuleSetFormatBinary)
	entries := w8EnvInt(t, "W8_ENTRIES", 1000)
	damage := w8Env("W8_DAMAGE", "bytes")
	baseDir := w8Dir(t, "w1-damaged")

	row := map[string]any{
		"case":     "w1",
		"scenario": "damaged",
		"format":   format,
		"entries":  entries,
		"damage":   damage,
	}
	defer func() {
		if t.Failed() {
			row["failed"] = true
		}
		w8Emit(t, row)
	}()

	payload := w8EncodeRuleSet(t, w8RuleSetDocument(entries, 0), format)
	servedDigest := w8SHA256(payload)
	server := w8StartServer(t)
	server.serve(payload)
	storeDir := w8ArtifactStoreDir(t, baseDir)

	// Prepare one good version.
	prepareCtx := w8RemoteContext(t, baseDir)
	prepareTransport := w8NewTransport(t, server)
	w8InstallTransport(prepareCtx, prepareTransport)
	prepareRuleSet, err := w8StartRuleSet(t, prepareCtx, format)
	require.NoError(t, err)
	require.True(t, w8RuleSetMatches(prepareRuleSet, w8Domain(0, entries-1)))
	require.NoError(t, prepareRuleSet.Close())
	goodCensus, _ := w8Census(t, storeDir)
	row["prepare_census"] = goodCensus
	row["prepare_hits"] = server.hits.Load()

	key, versionID := w8DamageArtifact(t, storeDir, damage)
	damagedCensus, _ := w8Census(t, storeDir)
	row["damaged_key"] = key
	row["damaged_version_id"] = versionID
	row["damaged_census"] = damagedCensus
	row["damaged_store_digest"] = w8StoreDigestOrEmpty(t, storeDir)

	// --- offline start from the damaged store -----------------------------
	offlineTransport := w8NewTransport(t, server)
	offlineTransport.fail.Store(true)
	offlineCtx := w8RemoteContext(t, baseDir)
	w8InstallTransport(offlineCtx, offlineTransport)
	offlineRuleSet, offlineErr := w8StartRuleSet(t, offlineCtx, format)
	offlineMatches := false
	if offlineErr == nil && offlineRuleSet != nil {
		offlineMatches = w8RuleSetMatches(offlineRuleSet, w8Domain(0, entries-1))
	}
	row["offline_error"] = ""
	if offlineErr != nil {
		row["offline_error"] = offlineErr.Error()
	}
	row["offline_matches"] = offlineMatches
	row["offline_path_went_live"] = offlineErr == nil
	// The fixed contract: a damaged version is never published as the live generation.
	// The reference side is recorded, not asserted, because it is the defect being
	// measured; the candidate is required to hold.
	row["contract_offline_no_corrupt_publish"] = offlineErr != nil
	if w8ExpectsFixed(t) {
		require.Error(t, offlineErr, "a damaged version was published as the live generation")
	} else if offlineErr == nil {
		t.Logf("reference behaviour: a damaged version was accepted (matches=%v)", offlineMatches)
	}

	// --- online re-download must repair -----------------------------------
	repairStart := w8Begin()
	repairStarted := time.Now()
	onlineCtx := w8RemoteContext(t, baseDir)
	onlineTransport := w8NewTransport(t, server)
	w8InstallTransport(onlineCtx, onlineTransport)
	hitsBefore := server.hits.Load()
	repairRuleSet, repairErr := w8StartRuleSet(t, onlineCtx, format)
	repairElapsed := time.Since(repairStarted)
	row["repair_error"] = ""
	if repairErr != nil {
		row["repair_error"] = repairErr.Error()
	}
	repairMatches := false
	if repairErr == nil && repairRuleSet != nil {
		repairMatches = w8RuleSetMatches(repairRuleSet, w8Domain(0, entries-1))
	}
	row["repair_matches"] = repairMatches
	repairCensus, _ := w8Census(t, storeDir)
	repairedDigest := w8StoreDigestOrEmpty(t, storeDir)
	row["repair_census"] = repairCensus
	row["repair_hits"] = server.hits.Load() - hitsBefore
	row["repair_requests_issued"] = server.hits.Load() > hitsBefore
	row["repair_store_digest"] = repairedDigest
	row["repair_contract_holds"] = repairErr == nil && repairMatches && repairedDigest == servedDigest
	row["repair_ms"] = float64(repairElapsed.Nanoseconds()) / 1e6
	if w8ExpectsFixed(t) {
		require.NoError(t, repairErr, "the re-download after damage must succeed")
		require.True(t, repairMatches, "the repaired generation does not match the served document")
		require.True(t, server.hits.Load() > hitsBefore, "the damaged version was reused without a request")
		require.Equal(t, servedDigest, repairedDigest, "the correct re-download did not repair the stored bytes")
		require.LessOrEqual(t, repairCensus.VersionDirs, 2, "the repair left an unbounded number of version directories")
	}
	if repairRuleSet != nil {
		require.NoError(t, repairRuleSet.Close())
	}
	repairProc := repairStart.delta()
	row["repair_heap_alloc"] = w8ReadMem().HeapAlloc
	row["repair_heap_objects"] = w8ReadMem().HeapObjects
	row["repair_peak_rss"] = repairProc.PeakRSS

	// --- next round: offline restore of the repaired version --------------
	nextTransport := w8NewTransport(t, server)
	nextTransport.fail.Store(true)
	nextCtx := w8RemoteContext(t, baseDir)
	w8InstallTransport(nextCtx, nextTransport)
	nextRuleSet, nextErr := w8StartRuleSet(t, nextCtx, format)
	nextMatches := false
	if nextErr == nil && nextRuleSet != nil {
		nextMatches = w8RuleSetMatches(nextRuleSet, w8Domain(0, entries-1))
	}
	nextCensus, _ := w8Census(t, storeDir)
	row["next_offline_error"] = ""
	if nextErr != nil {
		row["next_offline_error"] = nextErr.Error()
	}
	row["next_offline_matches"] = nextMatches
	row["next_offline_census"] = nextCensus
	row["next_offline_digest"] = w8StoreDigestOrEmpty(t, storeDir)
	row["next_offline_hits"] = nextTransport.hits.Load()
	row["next_offline_contract_holds"] = nextErr == nil && nextMatches && nextTransport.hits.Load() == 0
	if w8ExpectsFixed(t) {
		require.NoError(t, nextErr, "the repaired store must restore offline")
		require.True(t, nextMatches, "the offline restore of the repaired store does not serve the document")
		require.Zero(t, nextTransport.hits.Load(), "the offline restore of the repaired store issued a request")
		require.Equal(t, servedDigest, w8StoreDigestOrEmpty(t, storeDir))
	}
	if nextRuleSet != nil {
		require.NoError(t, nextRuleSet.Close())
	}

	// File/lease accounting: no temp directories are left behind by any of the above.
	finalCensus, finalFiles := w8Census(t, storeDir)
	row["final_census"] = finalCensus
	row["final_files"] = finalFiles
	row["final_goroutines"] = runtime.NumGoroutine()
	require.Zero(t, finalCensus.Temps, "a commit left a temp directory behind")
}

// w8StoreDigestOrEmpty returns the digest of the newest artifact on disk, or "" when the
// store is empty. It is the disk-side observable of "what would the next process load".
func w8StoreDigestOrEmpty(t *testing.T, storeDir string) string {
	t.Helper()
	if _, err := os.Stat(storeDir); os.IsNotExist(err) {
		return ""
	}
	keys, err := os.ReadDir(storeDir)
	if err != nil {
		return ""
	}
	newest := ""
	var newestMod time.Time
	for _, keyEntry := range keys {
		if !keyEntry.IsDir() {
			continue
		}
		versions, versionErr := os.ReadDir(filepath.Join(storeDir, keyEntry.Name()))
		if versionErr != nil {
			continue
		}
		for _, versionEntry := range versions {
			if !versionEntry.IsDir() {
				continue
			}
			artifactPath := filepath.Join(storeDir, keyEntry.Name(), versionEntry.Name(), "artifact.bin")
			content, readErr := os.ReadFile(artifactPath)
			if readErr != nil {
				continue
			}
			info, statErr := os.Stat(artifactPath)
			if statErr != nil {
				continue
			}
			if info.ModTime().After(newestMod) {
				newestMod = info.ModTime()
				newest = w8SHA256(content)
			}
		}
	}
	return newest
}

// w8ExpectsFixed tells the harness whether the run is the fixed (candidate) side. The
// reference side is a known-defective baseline: its gaps are recorded as measurements and
// reported, not turned into a failed sample that cannot produce a row.
func w8ExpectsFixed(t *testing.T) bool {
	t.Helper()
	return w8Env("W8_SIDE", "candidate") == "candidate"
}
