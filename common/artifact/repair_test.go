package artifact_test

// F-CODEX-01 — a damaged published version must be repaired by committing its verified bytes again.
//
// The defect these tests pin is inheritance, not a narrow race: publishVersion reused an existing
// version directory whenever its NAME existed. The name is a prefix of the digest, so a directory
// left behind by a truncated write, a manifest rewritten by a sync tool, a deleted artifact file or
// an in-place corruption was "reused" as if it still held the bytes the commit had just verified.
// Commit returned success, the live rules were built from the good bytes, and the next offline
// restore loaded the damaged ones - the reviewer's probe measured exactly
// `liveGood=true storedGood=false offlineRestore=false`.
//
// Every test below states the same contract from a different direction: a successful Commit hands
// back a lease whose file holds the bytes the commit verified, or it fails. It never reports success
// on bytes it did not verify, and it never destroys the backing of a lease it does not own.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sagernet/sing-box/common/artifact"
	"github.com/stretchr/testify/require"
)

// readLeased returns the bytes a leased version points at.
func readLeased(t *testing.T, version *artifact.Version) []byte {
	t.Helper()
	body, err := os.ReadFile(version.Path())
	require.NoError(t, err)
	return body
}

// damageArtifact rewrites the artifact file of an unleased version, leaving its manifest intact.
// This is the shape the reviewer reproduced: the manifest still names the good bytes, the file does
// not hold them.
func damageArtifact(t *testing.T, path string, body []byte) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, body, 0o600))
}

// keyDirectoryOf returns the directory that holds the version directories of a leased artifact.
func keyDirectoryOf(artifactPath string) string {
	return filepath.Dir(filepath.Dir(artifactPath))
}

// commitBytes is the ordinary Commit of a caller that already has the bytes.
func commitBytes(t *testing.T, store *artifact.Store, body []byte) *artifact.Version {
	t.Helper()
	version, err := store.Commit("source", manifestForBytes(), bytes.NewReader(body))
	require.NoError(t, err)
	return version
}

// manifestForBytes builds the producer manifest these tests publish under.
func manifestForBytes() artifact.Manifest {
	return artifact.NewManifest("srs/v4", "test", "source")
}

// TestRecommittingVerifiedBytesRepairsADamagedVersion is the reviewer's minimal probe, kept as a
// repository regression: commit A, corrupt the published file to B, commit A again. The second
// commit must return bytes that are A.
func TestRecommittingVerifiedBytesRepairsADamagedVersion(t *testing.T) {
	store, err := artifact.Open(t.TempDir())
	require.NoError(t, err)
	good := []byte("valid-source-A")
	bad := []byte("wrong-source-B")

	first := commitBytes(t, store, good)
	path := first.Path()
	first.Release()
	damageArtifact(t, path, bad)

	second := commitBytes(t, store, good)
	defer second.Release()
	require.Equal(t, good, readLeased(t, second),
		"a commit that verified these bytes must not hand back a damaged existing version")
	// The same digest, so the version keeps its identity: a pinned recovery record still resolves.
	require.Equal(t, first.ID(), second.ID())
	exact, err := store.LoadExact("source", second.ID())
	require.NoError(t, err, "a repaired version must still be the pinned identity")
	require.Equal(t, good, readLeased(t, exact))
	exact.Release()
}

// TestRecommittingRepairsADamagedManifest covers the metadata half of "damaged": the artifact bytes
// are intact but the manifest cannot be read, so no build can load the version.
func TestRecommittingRepairsADamagedManifest(t *testing.T) {
	directory := t.TempDir()
	store, err := artifact.Open(directory)
	require.NoError(t, err)
	good := []byte("valid-source-A")

	first := commitBytes(t, store, good)
	path := first.Path()
	first.Release()
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(path), "manifest.json"), []byte("{not json"), 0o644))
	_, loadErr := store.Load("source")
	require.Error(t, loadErr, "control: a version with an unreadable manifest cannot be loaded")

	second := commitBytes(t, store, good)
	defer second.Release()
	require.Equal(t, good, readLeased(t, second))
	loaded, err := store.Load("source")
	require.NoError(t, err)
	defer loaded.Release()
	require.Equal(t, good, readLeased(t, loaded))
}

// TestRecommittingRepairsAVersionMissingItsArtifact covers a published directory whose artifact file
// is gone: the manifest still records size and digest, verifyArtifact reports ErrIncomplete, and the
// name must not be trusted.
func TestRecommittingRepairsAVersionMissingItsArtifact(t *testing.T) {
	store, err := artifact.Open(t.TempDir())
	require.NoError(t, err)
	good := []byte("valid-source-A")

	first := commitBytes(t, store, good)
	path := first.Path()
	first.Release()
	require.NoError(t, os.Remove(path))

	second := commitBytes(t, store, good)
	defer second.Release()
	require.Equal(t, good, readLeased(t, second))
}

// TestRecommittingIdenticalBytesIsStillOneVersion is the negative control the fix must not break:
// an intact version is reused, not copied, so nothing accumulates and the path is stable.
func TestRecommittingIdenticalBytesIsStillOneVersion(t *testing.T) {
	store, err := artifact.Open(t.TempDir())
	require.NoError(t, err)
	good := bytes.Repeat([]byte("dedupe"), 512)

	first := commitBytes(t, store, good)
	firstPath := first.Path()
	first.Release()
	second := commitBytes(t, store, good)
	defer second.Release()
	require.Equal(t, firstPath, second.Path(), "intact identical bytes stay one version")
	require.Equal(t, good, readLeased(t, second))

	entries, err := os.ReadDir(keyDirectoryOf(firstPath))
	require.NoError(t, err)
	require.Len(t, entries, 1, "a reused version leaves no duplicate directory behind")
}

// TestRepairDoesNotTouchADamagedVersionAReaderStillHolds is the lease contract over the repair path.
//
// A reader can legitimately hold a version whose bytes were damaged underneath it: it loaded the
// version while the bytes were good. The repair must not remove, truncate or rewrite that backing.
// The commit still succeeds - by publishing its verified copy under a version name of its own - so a
// re-download is not blocked by a live reader.
func TestRepairDoesNotTouchADamagedVersionAReaderStillHolds(t *testing.T) {
	store, err := artifact.Open(t.TempDir())
	require.NoError(t, err)
	good := []byte("valid-source-A")
	bad := []byte("wrong-source-B")

	held := commitBytes(t, store, good)
	heldPath := held.Path()
	damageArtifact(t, heldPath, bad) // while the lease is still held

	repaired := commitBytes(t, store, good)
	require.Equal(t, good, readLeased(t, repaired),
		"the commit's own lease must name the bytes it verified")
	require.NotEqual(t, heldPath, repaired.Path(),
		"the live reader keeps its own backing; the verified copy is published beside it")
	require.Equal(t, bad, func() []byte {
		// The leased reader's file is untouched: no rewrite underneath it, no truncation.
		body, readErr := os.ReadFile(heldPath)
		require.NoError(t, readErr, "a leased version must not be unlinked by a repair")
		return body
	}())

	// Collect must keep the repaired copy and leave the leased damaged one alone.
	require.NoError(t, store.Collect("source", repaired.ID()))
	require.FileExists(t, repaired.Path())
	require.FileExists(t, heldPath)

	// Once the reader releases, the damaged version is collected and the repaired one remains.
	held.Release()
	require.NoError(t, store.Collect("source", repaired.ID()))
	_, statErr := os.Stat(heldPath)
	require.ErrorIs(t, statErr, os.ErrNotExist, "the damaged version is collected once unleased")
	require.FileExists(t, repaired.Path())

	loaded, err := store.Load("source")
	require.NoError(t, err)
	defer loaded.Release()
	require.Equal(t, good, readLeased(t, loaded))
}

// TestRepairKeepsAVersionUnderAnOccupiedNameLoadable is the "other identity under this name"
// branch: a complete, valid artifact of another digest sits where this digest's name belongs. It is
// not damaged, so removing it would destroy loadable data another caller may pin; the verified copy
// is published beside it and both stay loadable.
func TestRepairKeepsAVersionUnderAnOccupiedNameLoadable(t *testing.T) {
	store, err := artifact.Open(t.TempDir())
	require.NoError(t, err)
	wanted := []byte("wanted-source-A")
	other := []byte("another-source-Z")

	otherVersion := commitBytes(t, store, other)
	otherID := otherVersion.ID()
	otherDir := filepath.Dir(otherVersion.Path())
	otherVersion.Release()
	wantedVersion := commitBytes(t, store, wanted)
	wantedDir := filepath.Dir(wantedVersion.Path())
	wantedVersion.Release()
	require.NotEqual(t, otherDir, wantedDir)

	// Occupy the wanted name with the other, valid artifact: the name now lies about its content,
	// but nothing in the directory is corrupt, which is exactly the ambiguous case.
	require.NoError(t, os.RemoveAll(wantedDir))
	require.NoError(t, os.Rename(otherDir, wantedDir))

	repaired := commitBytes(t, store, wanted)
	require.Equal(t, wanted, readLeased(t, repaired))
	require.NotEqual(t, wantedDir, filepath.Dir(repaired.Path()),
		"a valid artifact under the name is not this commit's to overwrite")
	repaired.Release()

	// The displaced artifact is still loadable by its exact identity.
	exact, err := store.LoadExact("source", otherID)
	require.NoError(t, err, "a valid artifact of another identity must survive the repair")
	require.Equal(t, other, readLeased(t, exact))
	exact.Release()

	// And the newest load is the repaired copy, because the displaced artifact is the older one.
	loaded, err := store.Load("source")
	require.NoError(t, err)
	defer loaded.Release()
	require.Equal(t, wanted, readLeased(t, loaded))
}

// TestASecondStoreOnOneDirectoryRepairsTheDamage puts the repair through a different handle: two
// Store values on one directory share the lease table, and the repair must be visible to both.
func TestASecondStoreOnOneDirectoryRepairsTheDamage(t *testing.T) {
	directory := t.TempDir()
	writer, err := artifact.Open(directory)
	require.NoError(t, err)
	reader, err := artifact.Open(directory)
	require.NoError(t, err)
	good := []byte("valid-source-A")

	first := commitBytes(t, writer, good)
	path := first.Path()
	first.Release()
	damageArtifact(t, path, []byte("wrong-source-B"))

	repaired := commitBytes(t, reader, good)
	defer repaired.Release()
	visible, err := writer.Load("source")
	require.NoError(t, err, "the repair must be visible through the other handle")
	defer visible.Release()
	require.Equal(t, good, readLeased(t, visible))
}

// TestAlternatingDamageAndRecommitConverges drives the repair path repeatedly: each round damages
// whatever is published and commits the correct bytes again. Every commit must return the correct
// bytes, and the store must not accumulate directories round after round.
func TestAlternatingDamageAndRecommitConverges(t *testing.T) {
	store, err := artifact.Open(t.TempDir())
	require.NoError(t, err)
	good := []byte("valid-source-A")

	for round := 0; round < 8; round++ {
		version := commitBytes(t, store, good)
		require.Equal(t, good, readLeased(t, version), "round %d", round)
		path := version.Path()
		version.Release()
		damageArtifact(t, path, []byte(strings.Repeat("B", len(good))))
		_, loadErr := store.Load("source")
		require.Error(t, loadErr, "round %d: the damaged version must not be loadable", round)
	}

	final := commitBytes(t, store, good)
	defer final.Release()
	require.Equal(t, good, readLeased(t, final))
	require.NoError(t, store.Collect("source"))
	entries, err := os.ReadDir(keyDirectoryOf(final.Path()))
	require.NoError(t, err)
	require.Len(t, entries, 1,
		"a repaired version replaces the damaged one under its own name; total directories stay bounded")
}
