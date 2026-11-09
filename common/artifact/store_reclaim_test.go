package artifact

// F04 review — the two reclamation defects a review reproduction found.
//
//  1. Collect measured "valid" with the manifest alone. A newest version whose artifact was
//     CORRUPTED (manifest readable, bytes not) therefore counted as the version to keep, and the
//     last version that actually loads was deleted as "older". The store then had no loadable
//     version at all - the corruption destroyed the fallback instead of being the thing removed.
//  2. Leases were an in-memory map owned by ONE Store value. Two Stores opened on the same
//     directory did not see each other's leases, so a collector in one removed a version a reader
//     in the other was still reading.
//
// Both are pinned here with the failure they produce: a load that must still succeed, and a file
// that must still exist while leased.

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// corruptVersion rewrites the artifact of a version with same-size garbage, so the manifest still
// reads and only the digest check can tell that the bytes are not the committed ones.
func corruptVersion(t *testing.T, version *Version) {
	t.Helper()
	manifest := version.Manifest()
	garbage := make([]byte, manifest.Size)
	for index := range garbage {
		garbage[index] = 'x'
	}
	require.NoError(t, os.WriteFile(version.Path(), garbage, 0o644))
}

func TestCollectKeepsTheNewestValidVersionWhenTheNewestIsCorrupt(t *testing.T) {
	store, err := Open(t.TempDir())
	require.NoError(t, err)

	good, err := store.Commit("geosite", manifestFor(0, ""), strings.NewReader("domain:good.example"))
	require.NoError(t, err)
	good.Release()

	newer, err := store.Commit("geosite", manifestFor(0, ""), strings.NewReader("domain:newer.example"))
	require.NoError(t, err)
	newer.Release()
	corruptVersion(t, newer)

	require.NoError(t, store.Collect("geosite"))

	// The corrupted version can never be loaded; the version that can must survive the collection.
	loaded, err := store.Load("geosite")
	require.NoError(t, err, "a corrupt newest version must not cost the store its last valid one")
	require.Equal(t, good.Manifest().Digest, loaded.Manifest().Digest)
	loaded.Release()

	// And the corrupt version is the thing removed.
	_, statErr := os.Stat(newer.dir)
	require.ErrorIs(t, statErr, os.ErrNotExist, "the unloadable version is what Collect removes")
}

func TestCollectKeepsTheCompatibleVersionWhenANewerOneIsForAnotherBuild(t *testing.T) {
	store, err := Open(t.TempDir())
	require.NoError(t, err)

	compatibleVersion, err := store.Commit("geosite", manifestFor(0, ""), strings.NewReader("domain:compatible.example"))
	require.NoError(t, err)
	compatibleVersion.Release()

	foreign := manifestFor(0, "")
	foreign.ABI = "another-os/another-arch"
	foreign.Platform = "another-os"
	foreignVersion, err := store.Commit("geosite", foreign, strings.NewReader("domain:foreign.example"))
	require.NoError(t, err)
	foreignVersion.Release()

	require.NoError(t, store.Collect("geosite"))

	loaded, err := store.Load("geosite")
	require.NoError(t, err, "another build's newer version must not evict the one this build can load")
	require.Equal(t, compatibleVersion.Manifest().Digest, loaded.Manifest().Digest)
	loaded.Release()

	// The foreign version is left alone: this build cannot judge it, and the other build needs it.
	_, statErr := os.Stat(foreignVersion.dir)
	require.NoError(t, statErr, "a version for another ABI is not this build's to delete")
}

func TestTwoStoresOnOneDirectoryShareLeases(t *testing.T) {
	directory := t.TempDir()
	readerStore, err := Open(directory)
	require.NoError(t, err)
	writerStore, err := Open(directory)
	require.NoError(t, err)

	held, err := readerStore.Commit("geosite", manifestFor(0, ""), strings.NewReader("domain:held.example"))
	require.NoError(t, err)

	newer, err := writerStore.Commit("geosite", manifestFor(0, ""), strings.NewReader("domain:newer.example"))
	require.NoError(t, err)
	newer.Release()

	require.NoError(t, writerStore.Collect("geosite"))
	require.FileExists(t, held.Path(),
		"a lease taken through one Store must protect the version from a collector in another Store")

	held.Release()
	require.NoError(t, writerStore.Collect("geosite"))
	_, statErr := os.Stat(held.dir)
	require.ErrorIs(t, statErr, os.ErrNotExist, "once released, the older version is collected")
}
