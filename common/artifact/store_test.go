package artifact

// F04 — the artifact store's contract, with the faults a download and a crash actually
// produce: a short body, a digest that does not match, a manifest built for another ABI, an
// interrupted commit, a crash that leaves a temp directory behind, and a reader that is still
// holding the old version.
//
// Two properties are asserted everywhere: an uncommitted version is never visible, and a
// leased version is never collected.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// manifestFor builds a manifest compatible with this test process.
func manifestFor(size int64, digest string) Manifest {
	return Manifest{
		Schema:   "srs/v4",
		Compiler: "sing-box/rule",
		Policy:   "geosite",
		ABI:      currentABI(),
		Platform: currentPlatform(),
		Size:     size,
		Digest:   digest,
	}
}

func TestCommitPublishesOnlyACompleteArtifact(t *testing.T) {
	store, err := Open(t.TempDir())
	require.NoError(t, err)

	version, err := store.Commit("geosite", manifestFor(0, ""), strings.NewReader("domain:example.org"))
	require.NoError(t, err)
	defer version.Release()
	require.NotEmpty(t, version.Path())

	loaded, err := store.Load("geosite")
	require.NoError(t, err)
	defer loaded.Release()
	file, err := loaded.Open()
	require.NoError(t, err)
	defer file.Close()
	content := make([]byte, 64)
	read, _ := file.Read(content)
	require.Equal(t, "domain:example.org", string(content[:read]))
	require.NotEmpty(t, loaded.Manifest().Digest)
}

func TestAShortBodyIsRefusedAndNothingBecomesVisible(t *testing.T) {
	store, err := Open(t.TempDir())
	require.NoError(t, err)

	// The manifest declares 100 bytes; the body is 5.
	_, err = store.Commit("geosite", manifestFor(100, ""), strings.NewReader("short"))
	require.Error(t, err)
	_, loadErr := store.Load("geosite")
	require.ErrorIs(t, loadErr, os.ErrNotExist, "a refused commit must leave no visible version")
	entries, err := os.ReadDir(filepath.Join(store.dir, "geosite"))
	require.NoError(t, err)
	for _, entry := range entries {
		require.True(t, strings.HasPrefix(entry.Name(), tempPrefix) || entry.IsDir())
	}
}

func TestADigestMismatchIsRefused(t *testing.T) {
	store, err := Open(t.TempDir())
	require.NoError(t, err)

	_, err = store.Commit("geosite", manifestFor(0, strings.Repeat("0", 64)), strings.NewReader("domain:example.org"))
	require.ErrorIs(t, err, ErrDigestMismatch)
	_, loadErr := store.Load("geosite")
	require.ErrorIs(t, loadErr, os.ErrNotExist)
}

func TestAVersionForAnotherABIIsInvisibleToThisBuild(t *testing.T) {
	store, err := Open(t.TempDir())
	require.NoError(t, err)
	manifest := manifestFor(0, "")
	manifest.ABI = "another-os/another-cpu"
	_, err = store.Commit("geosite", manifest, strings.NewReader("domain:example.org"))
	require.NoError(t, err, "the store may hold another ABI's artifact")

	_, loadErr := store.Load("geosite")
	require.ErrorIs(t, loadErr, os.ErrNotExist, "a version for another ABI must not be loadable")
}

func TestAnInterruptedCommitLeavesOnlyATempEntry(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	require.NoError(t, err)
	keyDir := filepath.Join(dir, "geosite")
	require.NoError(t, os.MkdirAll(keyDir, 0o755))
	leftover := filepath.Join(keyDir, tempPrefix+"crashed")
	require.NoError(t, os.MkdirAll(leftover, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(leftover, artifactName), []byte("half"), 0o644))

	_, loadErr := store.Load("geosite")
	require.ErrorIs(t, loadErr, os.ErrNotExist, "an interrupted commit is not a version")

	require.NoError(t, store.Collect("geosite"))
	entries, err := os.ReadDir(keyDir)
	require.NoError(t, err)
	require.Empty(t, entries, "Collect must remove the temp leftovers of an interrupted commit")
}

func TestARecoveredStoreLoadsTheNewestValidVersion(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	require.NoError(t, err)

	first, err := store.Commit("geosite", manifestFor(0, ""), strings.NewReader("first"))
	require.NoError(t, err)
	firstManifest := first.Manifest()
	first.Release()
	// A second, newer version.
	time.Sleep(time.Millisecond)
	second, err := store.Commit("geosite", manifestFor(0, ""), strings.NewReader("second"))
	require.NoError(t, err)
	second.Release()

	loaded, err := store.Load("geosite")
	require.NoError(t, err)
	defer loaded.Release()
	require.NotEqual(t, firstManifest.Digest, loaded.Manifest().Digest)
	// The newest valid version survives Collect even when nobody leases it.
	require.NoError(t, store.Collect("geosite"))
	survivor, err := store.Load("geosite")
	require.NoError(t, err)
	defer survivor.Release()
	require.Equal(t, loaded.Manifest().Digest, survivor.Manifest().Digest)
}

func TestALeasedVersionIsNeverCollected(t *testing.T) {
	store, err := Open(t.TempDir())
	require.NoError(t, err)

	held, err := store.Commit("geosite", manifestFor(0, ""), strings.NewReader("held"))
	require.NoError(t, err)
	heldManifest := held.Manifest()

	// A newer version appears while the old one is leased.
	time.Sleep(time.Millisecond)
	newer, err := store.Commit("geosite", manifestFor(0, ""), strings.NewReader("newer"))
	require.NoError(t, err)
	defer newer.Release()

	require.NoError(t, store.Collect("geosite"))
	require.FileExists(t, held.Path(), "a leased version must not be removed from under its reader")

	// Once released, the old version is collectable on the next pass.
	held.Release()
	require.NoError(t, store.Collect("geosite"))
	require.NoFileExists(t, held.Path(), "after the lease is dropped the old version is collectable")
	_ = heldManifest
}

func TestReleaseIsIdempotent(t *testing.T) {
	store, err := Open(t.TempDir())
	require.NoError(t, err)
	version, err := store.Commit("geosite", manifestFor(0, ""), strings.NewReader("x"))
	require.NoError(t, err)
	version.Release()
	version.Release() // must not underflow the lease count
	// A newer version makes the older one collectable; the double release must not have made
	// the store believe it still has a reader (or believe it has a negative count).
	time.Sleep(time.Millisecond)
	newer, err := store.Commit("geosite", manifestFor(0, ""), strings.NewReader("y"))
	require.NoError(t, err)
	defer newer.Release()
	require.NoError(t, store.Collect("geosite"))
	require.NoFileExists(t, version.Path(), "the released version must be collectable")
}

func TestCommittingTheSameBytesIsOneVersion(t *testing.T) {
	store, err := Open(t.TempDir())
	require.NoError(t, err)
	first, err := store.Commit("geosite", manifestFor(0, ""), bytes.NewReader([]byte("same")))
	require.NoError(t, err)
	first.Release()
	second, err := store.Commit("geosite", manifestFor(0, ""), bytes.NewReader([]byte("same")))
	require.NoError(t, err)
	defer second.Release()
	require.Equal(t, first.Path(), second.Path(), "identical bytes collapse to one version, not a pile of copies")
}

// TestRepublishingIdenticalBytesReplacesTheProvenance is the migration contract: the version name
// is a property of the BYTES, so a corrected account of those bytes (a new cache identity
// generation, another source that compiles to the same layout) must be able to replace the stale
// account instead of being shadowed by it forever.
func TestRepublishingIdenticalBytesReplacesTheProvenance(t *testing.T) {
	store, err := Open(t.TempDir())
	require.NoError(t, err)

	stale := manifestFor(0, "")
	stale.Dependencies = []string{"source-sha256=old", "source-size=4"}
	first, err := store.Commit("derived", stale, bytes.NewReader([]byte("same")))
	require.NoError(t, err)
	firstPath := first.Path()
	first.Release()

	corrected := manifestFor(0, "")
	corrected.Dependencies = []string{"source-sha256=new", "source-size=4", "identity-generation=2"}
	second, err := store.Commit("derived", corrected, bytes.NewReader([]byte("same")))
	require.NoError(t, err)
	second.Release()
	require.Equal(t, firstPath, second.Path(), "identical bytes stay one version")

	loaded, err := store.Load("derived")
	require.NoError(t, err)
	defer loaded.Release()
	require.Equal(t, corrected.Dependencies, loaded.Manifest().Dependencies,
		"the latest account of the same bytes must be the one a reader sees")
	require.FileExists(t, loaded.Path(), "replacing the manifest must not touch the artifact bytes")
}

// TestDiscardIfEvaluatesTheManifestAtRemovalTime pins the reason DiscardIf exists: a caller's
// decision can be taken on a manifest snapshot that another publisher replaces for the SAME bytes
// before the removal runs. The predicate must therefore be evaluated at removal time, under the
// lease lock, not against the snapshot the caller saw.
func TestDiscardIfEvaluatesTheManifestAtRemovalTime(t *testing.T) {
	store, err := Open(t.TempDir())
	require.NoError(t, err)

	stale := manifestFor(0, "")
	stale.Dependencies = []string{"source-sha256=same"}
	first, err := store.Commit("derived", stale, bytes.NewReader([]byte("same")))
	require.NoError(t, err)
	id := first.ID()
	first.Release()

	// A concurrent publisher corrects the account for the SAME bytes: the version directory and the
	// digest do not change, only the manifest does.
	corrected := manifestFor(0, "")
	corrected.Dependencies = []string{"source-sha256=same", "identity-generation=2"}
	second, err := store.Commit("derived", corrected, bytes.NewReader([]byte("same")))
	require.NoError(t, err)
	second.Release()
	require.Equal(t, first.Path(), second.Path(), "the correction is the same physical version")

	// A reader that decided on the OLD snapshot must not delete the corrected version.
	require.NoError(t, store.DiscardIf("derived", id, func(manifest Manifest) bool {
		return len(manifest.Dependencies) == 1
	}))
	loaded, err := store.Load("derived")
	require.NoError(t, err, "the corrected account must survive a stale discard decision")
	require.Equal(t, corrected.Dependencies, loaded.Manifest().Dependencies)
	loaded.Release()

	// The same call DOES remove the version when the state the caller decided on is still current.
	require.NoError(t, store.DiscardIf("derived", id, func(manifest Manifest) bool {
		return len(manifest.Dependencies) == 2
	}))
	_, err = store.Load("derived")
	require.ErrorIs(t, err, os.ErrNotExist)
}

// TestDiscardIfRespectsALease keeps the pre-existing lease rule: a discard never removes a version a
// reader still holds.
func TestDiscardIfRespectsALease(t *testing.T) {
	store, err := Open(t.TempDir())
	require.NoError(t, err)
	version, err := store.Commit("derived", manifestFor(0, ""), bytes.NewReader([]byte("held")))
	require.NoError(t, err)
	defer version.Release()

	require.NoError(t, store.DiscardIf("derived", version.ID(), func(Manifest) bool { return true }))
	require.FileExists(t, version.Path(), "a leased version must not be removed")
}

// TestNewestCreatedReadsManifestsOnly pins the logical-clock probe: it must report the largest
// Created among the versions this build could load, ignore another build's manifest, and be cheap
// (manifests only, no artifact bytes, no lease).
func TestNewestCreatedReadsManifestsOnly(t *testing.T) {
	store, err := Open(t.TempDir())
	require.NoError(t, err)
	require.Equal(t, int64(0), store.NewestCreated("absent"))

	commit := func(body string, created int64) {
		t.Helper()
		manifest := manifestFor(0, "")
		manifest.Created = created
		version, commitErr := store.Commit("key", manifest, strings.NewReader(body))
		require.NoError(t, commitErr)
		version.Release()
	}
	commit("first", 100)
	commit("second", 300)
	commit("third", 200)
	require.Equal(t, int64(300), store.NewestCreated("key"),
		"the newest stamp on disk, not the most recently committed one")

	// A version another build wrote is not one this build can load, so it must not set the clock.
	foreign := manifestFor(0, "")
	foreign.Platform = "plan9"
	foreign.Created = 1 << 60
	version, err := store.Commit("key", foreign, strings.NewReader("foreign"))
	require.NoError(t, err)
	version.Release()
	require.Equal(t, int64(300), store.NewestCreated("key"),
		"an incompatible version must not drive this build's clock")
}

// TestPublishedStampReplacesAnOlderStamp pins the logical-clock escape hatch: a producer that marks
// the manifest as Published must have its newer stamp adopted for identical bytes, while an ordinary
// republish keeps the first account. Without this the derived cache would silently keep an old
// position and could never outrank a future-dated version.
func TestPublishedStampReplacesAnOlderStamp(t *testing.T) {
	store, err := Open(t.TempDir())
	require.NoError(t, err)

	old := manifestFor(0, "")
	old.Created = 100
	first, err := store.Commit("derived", old, strings.NewReader("same"))
	require.NoError(t, err)
	firstPath := first.Path()
	first.Release()

	newer := manifestFor(0, "")
	newer.Created = 500
	newer.Published = true
	second, err := store.Commit("derived", newer, strings.NewReader("same"))
	require.NoError(t, err)
	require.Equal(t, firstPath, second.Path(), "a stamp update is the same physical version")
	require.Equal(t, int64(500), second.Manifest().Created, "the published stamp must win")
	second.Release()
	loaded, err := store.Load("derived")
	require.NoError(t, err)
	require.Equal(t, int64(500), loaded.Manifest().Created)
	loaded.Release()

	// Control: without the marker an ordinary republish is not a new publication.
	ordinary := manifestFor(0, "")
	ordinary.Created = 900
	third, err := store.Commit("derived", ordinary, strings.NewReader("same"))
	require.NoError(t, err)
	defer third.Release()
	require.Equal(t, int64(500), third.Manifest().Created)
}

// TestRepublishingTheSameProvenanceKeepsThePublicationTime makes the replacement conditional: an
// unchanged account must not rewrite the manifest, so the version keeps the timestamp that ranks it
// against other versions.
func TestRepublishingTheSameProvenanceKeepsThePublicationTime(t *testing.T) {
	store, err := Open(t.TempDir())
	require.NoError(t, err)

	manifest := manifestFor(0, "")
	manifest.Dependencies = []string{"source-sha256=same"}
	first, err := store.Commit("derived", manifest, bytes.NewReader([]byte("same")))
	require.NoError(t, err)
	created := first.Manifest().Created
	first.Release()

	time.Sleep(time.Millisecond)
	second, err := store.Commit("derived", manifest, bytes.NewReader([]byte("same")))
	require.NoError(t, err)
	defer second.Release()
	require.Equal(t, created, second.Manifest().Created,
		"an identical account is not a new publication")
	loaded, err := store.Load("derived")
	require.NoError(t, err)
	defer loaded.Release()
	require.Equal(t, created, loaded.Manifest().Created)
}
