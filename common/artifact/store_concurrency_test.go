package artifact

// F04 review — reclamation and lease acquisition happen together in production: one store writes
// and collects, another loads and releases, and both are handles on one directory.
//
// The failure this file pins is a collector that reads the lease bookkeeping without the lock and
// decides what to delete from a snapshot taken before a reader arrived. It has two shapes:
//
//   - a data race on the shared lease map, which is what a `-race` run of the stress test reports;
//   - a version removed between "this looks loadable" and "the reader holds it", which makes a
//     successful Load return a file that is already gone.
//
// The interleaving tests use the store's test hook to place the reader exactly between the
// collector's listing pass and its removal pass, and to place a collector inside a commit's
// temporary write. Both used to lose.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestALeaseTakenWhileCollectIsAlreadyRunningIsHonored puts the reader inside the collector's
// window: Collect has listed the versions and is about to decide what to remove; the reader takes
// its lease before the removal. The lease is the authority, so the version must survive. The
// collector used to take its "leased" snapshot before listing and ignore this reader.
func TestALeaseTakenWhileCollectIsAlreadyRunningIsHonored(t *testing.T) {
	directory := t.TempDir()
	writerStore, err := Open(directory)
	require.NoError(t, err)
	readerStore, err := Open(directory)
	require.NoError(t, err)

	older, err := writerStore.Commit("geosite", manifestFor(0, ""), strings.NewReader("domain:older.example"))
	require.NoError(t, err)
	older.Release()
	time.Sleep(time.Millisecond)
	newer, err := writerStore.Commit("geosite", manifestFor(0, ""), strings.NewReader("domain:newer.example"))
	require.NoError(t, err)
	newer.Release()

	readerLeased := false
	writerStore.testHook = func(point string) {
		if point == "collect-listed" && !readerLeased {
			// The lease a Load would hold for the time it reads the version. Only the first
			// Collect pass takes it; the pass after the release must see no lease.
			readerStore.acquire(older.dir)
			readerLeased = true
		}
	}
	require.NoError(t, writerStore.Collect("geosite"))
	require.FileExists(t, older.Path(),
		"a lease taken while Collect is already running must still protect the version")
	require.FileExists(t, newer.Path())

	readerStore.release(older.dir)
	require.NoError(t, writerStore.Collect("geosite"))
	require.NoFileExists(t, older.Path(), "once released, the older version is collectable")
}

// TestCollectDoesNotRemoveATempArtifactThatIsStillBeingWritten runs a collector while a commit is
// between creating its temporary directory and writing the body into it. A temporary directory is
// a crash leftover only when no commit is still writing it; treating a live one as garbage makes
// the commit fail and leaves the download half applied.
func TestCollectDoesNotRemoveATempArtifactThatIsStillBeingWritten(t *testing.T) {
	directory := t.TempDir()
	writerStore, err := Open(directory)
	require.NoError(t, err)
	collectorStore, err := Open(directory)
	require.NoError(t, err)

	var collectErr error
	writerStore.testHook = func(point string) {
		if point != "commit-temp" {
			return
		}
		// The collector sees the temp directory the commit just created. It must not touch it.
		collectErr = collectorStore.Collect("geosite")
	}
	version, err := writerStore.Commit("geosite", manifestFor(0, ""), strings.NewReader("domain:written.example"))
	require.NoError(t, err, "the collector must not remove the temp directory of a live commit")
	require.NoError(t, collectErr)
	version.Release()

	loaded, err := writerStore.Load("geosite")
	require.NoError(t, err)
	defer loaded.Release()
	content, err := os.ReadFile(loaded.Path())
	require.NoError(t, err)
	require.Equal(t, "domain:written.example", string(content))
}

// TestLoadReleaseAndCollectOnOneDirectoryAreRaceFree is the shape production has: one handle
// writes new versions and collects, another repeatedly loads and releases while new versions
// appear. Every successful Load must return a version that is still there when the caller opens
// it, and the whole run must be free of data races (`go test -race`).
func TestLoadReleaseAndCollectOnOneDirectoryAreRaceFree(t *testing.T) {
	directory := t.TempDir()
	writerStore, err := Open(directory)
	require.NoError(t, err)
	readerStore, err := Open(directory)
	require.NoError(t, err)

	seed, err := writerStore.Commit("geosite", manifestFor(0, ""), strings.NewReader("domain:seed.example"))
	require.NoError(t, err)
	seed.Release()

	stop := make(chan struct{})
	var wg sync.WaitGroup
	failures := make(chan error, 32)
	report := func(format string, arguments ...any) {
		select {
		case failures <- fmt.Errorf(format, arguments...):
		default:
		}
	}

	// One writer, always publishing new content and collecting what it superseded.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for index := 0; ; index++ {
			select {
			case <-stop:
				return
			default:
			}
			version, commitErr := writerStore.Commit(
				"geosite", manifestFor(0, ""),
				strings.NewReader(fmt.Sprintf("domain:v%d.example", index)),
			)
			if commitErr != nil {
				report("commit %d: %v", index, commitErr)
				return
			}
			version.Release()
			if collectErr := writerStore.Collect("geosite"); collectErr != nil {
				report("collect: %v", collectErr)
				return
			}
			time.Sleep(100 * time.Microsecond)
		}
	}()

	// Several readers. A Load that returns must hand over a version the caller can actually read.
	for reader := 0; reader < 4; reader++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				loaded, loadErr := readerStore.Load("geosite")
				if loadErr != nil {
					report("load: %v", loadErr)
					return
				}
				file, openErr := loaded.Open()
				if openErr != nil {
					loaded.Release()
					report("open after a successful load: %v", openErr)
					return
				}
				hasher := sha256.New()
				_, copyErr := io.Copy(hasher, file)
				_ = file.Close()
				if copyErr != nil {
					loaded.Release()
					report("read after a successful load: %v", copyErr)
					return
				}
				if digest := hex.EncodeToString(hasher.Sum(nil)); digest != loaded.Manifest().Digest {
					loaded.Release()
					report("loaded bytes do not match the manifest digest")
					return
				}
				loaded.Release()
			}
		}()
	}

	time.Sleep(500 * time.Millisecond)
	close(stop)
	wg.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
}
