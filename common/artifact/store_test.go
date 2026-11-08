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
