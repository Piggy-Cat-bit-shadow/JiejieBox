//go:build unix

package artifact

// B3 — an mmap reader across an update and a collection.
//
// This is the Unix half of the lease contract: a version that is mapped by a live reader must
// survive Collect even though it is no longer the newest, and the mapping must stay readable after
// the lease is dropped and the directory is removed. Windows cannot run this test (an open mapping
// means the file cannot be removed at all); it is delivered for the Unix integration run:
//
//	go test ./common/artifact -run TestAnMMapReaderSurvivesUpdateAndCollection -count=1 -v
//
// Required environment: any Linux or Darwin host, Go toolchain as in the window handoff. The
// test is silent when it cannot map (e.g. an unusual filesystem); it must not be counted as a
// pass on a platform where it skipped.

import (
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAnMMapReaderSurvivesUpdateAndCollection(t *testing.T) {
	store, err := Open(t.TempDir())
	require.NoError(t, err)

	first, err := store.Commit("geosite", manifestFor(0, ""), strings.NewReader("domain:mapped.example"))
	require.NoError(t, err)
	firstID := first.ID()

	file, err := first.Open()
	require.NoError(t, err)
	info, err := file.Stat()
	require.NoError(t, err)
	mapped, err := syscall.Mmap(int(file.Fd()), 0, int(info.Size()), syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		_ = file.Close()
		t.Skipf("mmap is not available on this filesystem: %v", err)
	}
	mappedCopy := string(mapped)
	require.Equal(t, "domain:mapped.example", mappedCopy)

	// A newer version appears while the old one is mapped. The lease is what keeps the directory.
	time.Sleep(time.Millisecond)
	newer, err := store.Commit("geosite", manifestFor(0, ""), strings.NewReader("domain:newer.example"))
	require.NoError(t, err)
	newer.Release()
	require.NoError(t, store.Collect("geosite"))
	require.FileExists(t, first.Path(), "a mapped version must not be removed by Collect")

	require.Equal(t, "domain:mapped.example", string(mapped),
		"the mapped bytes must still be the published ones after an update")

	first.Release()
	require.NoError(t, store.Collect("geosite"))
	_, statErr := os.Stat(first.dir)
	require.ErrorIs(t, statErr, os.ErrNotExist, "after the lease is dropped the version is collectable")
	require.Equal(t, "domain:mapped.example", string(mapped),
		"a mapping outlives the directory name: it stays readable until munmap")

	require.NoError(t, syscall.Munmap(mapped))
	require.NoError(t, file.Close())
	require.NotEmpty(t, firstID)

	loaded, err := store.Load("geosite")
	require.NoError(t, err)
	require.NotEqual(t, firstID, loaded.ID())
	loaded.Release()
}
