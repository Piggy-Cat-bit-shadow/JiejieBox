package cachefile

import (
	"context"
	"testing"

	"github.com/sagernet/bbolt"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/logger"

	"github.com/stretchr/testify/require"
)

// The cache-file startup path must not be killable by a malformed FILE.
//
// # The oracle
//
// LX 474aa5b5 is "a corrupted cache file crashed the client". The reported symptom is that
// a client which worked yesterday exits immediately today. The fork already handles the
// shapes bbolt itself rejects - a truncated file, a zero-length file, garbage bytes - by
// removing and recreating (see cache.go:204-229, and the CLEAN cases in
// corrupt_file_robustness_test.go).
//
// The remaining exposure is a file that PASSES `checkDatabase` and then breaks the code
// that walks it. `start()`'s bucket sweep indexes the first byte of every top-level key:
//
//	err = db.Batch(func(tx *bbolt.Tx) error {
//		return tx.ForEach(func(name []byte, b *bbolt.Bucket) error {
//			if name[0] == 0 {
//
// `tx.ForEach` hands the callback the raw key slice from the leaf page, so a zero-length
// key yields a non-nil empty slice and `name[0]` is an out-of-range index.
//
// # Why this is a startup kill rather than a handled error
//
// The panic happens inside `db.Batch`'s closure, and bbolt's batch retries a closure
// panic by re-running it SOLO on the CALLER'S goroutine (bbolt/db.go, the `trySolo`
// path), where `db.Update` has no recover. The only recoveries between here and main are
// the three inside this package, none of which is reached from `start()`. So the process
// dies with a panic trace and the corrupt-file reset loop never runs: every subsequent
// start fails the same way until the user deletes the file by hand.
//
// # What these tests establish
//
// The first half is an explicit CONSTRUCTIBILITY check, because the whole finding depends
// on whether such a file can exist at all. If bbolt refuses to expose a zero-length
// top-level key, the rest is unreachable and that is worth recording rather than
// assuming. The second half then drives `start()` over the best file this test can build.

// writeRawCacheFileWithEmptyTopLevelKey attempts to build a bbolt file carrying a
// zero-length top-level key, and reports whether any supported call accepted it.
//
// Only CREATE counts. A lookup is not evidence: `tx.Bucket(k)` returns `(nil, nil)` for an
// absent key, so a probe that accepted "no error" would report success for a bucket that
// does not exist - the first version of this fixture did exactly that and produced a
// false positive, which is why the check below is written against the returned bucket.
func writeRawCacheFileWithEmptyTopLevelKey(t *testing.T, path string) bool {
	t.Helper()
	db, err := bbolt.Open(path, 0o666, nil)
	require.NoError(t, err)

	accepted := false
	_ = db.Update(func(tx *bbolt.Tx) error {
		bucket, createErr := tx.CreateBucket([]byte{})
		if createErr == nil && bucket != nil {
			accepted = true
		}
		// The other shape the sweep mistakes for a namespace: a top-level VALUE whose key
		// begins with 0x00. Values are not enumerable as buckets, so only the create arm
		// above can make `name[0]` reachable with a zero-length name.
		return nil
	})
	require.NoError(t, db.Close())
	return accepted
}

// TestBboltRefusesAZeroLengthTopLevelKey records the measured precondition, and it is what
// downgrades the bucket-sweep finding from a defect to a latent one.
//
// MEASURED: `tx.CreateBucket([]byte{})` fails with "bucket name required", so bbolt cannot
// produce a zero-length top-level key and `tx.ForEach` therefore never hands the sweep a
// non-nil empty key slice. The `name[0]` index is only reachable from a file written by
// something other than bbolt.
func TestBboltRefusesAZeroLengthTopLevelKey(t *testing.T) {
	path := t.TempDir() + "/empty-key.db"
	accepted := writeRawCacheFileWithEmptyTopLevelKey(t, path)

	require.False(t, accepted,
		"bbolt accepted a zero-length top-level bucket key: `name[0]` in the startup sweep is then "+
			"reachable from supported calls and must be guarded")
	t.Logf("bbolt accepts a zero-length top-level bucket key: %v", accepted)
}

// TestStartSurvivesAZeroLengthTopLevelKey is the defensive half.
//
// The shape is not producible through bbolt (see above), so this test cannot make `start()`
// panic on the pre-fix code - it is recorded as NOT FALSIFIED rather than presented as
// coverage. What it does establish is the property that matters if the shape ever becomes
// reachable: the startup path returns rather than dying, and a recovered store is usable.
func TestStartSurvivesAZeroLengthTopLevelKey(t *testing.T) {
	path := t.TempDir() + "/empty-key.db"
	if writeRawCacheFileWithEmptyTopLevelKey(t, path) {
		t.Log("the zero-length key was accepted, so this run exercises the sweep guard")
	} else {
		t.Log("bbolt refused the zero-length key: this run exercises only the ordinary sweep")
	}

	cacheFile := New(
		context.Background(),
		logger.NOP(),
		option.CacheFileOptions{Enabled: true, Path: path, StoreFakeIP: true},
	)
	t.Cleanup(func() {
		if openDB := cacheFile.database(); openDB != nil {
			_ = openDB.Close()
		}
	})

	// The assertion is "this returns": a panic is the defect, and a non-nil error is an
	// acceptable outcome for a file carrying a key the schema does not define.
	startErr := cacheFile.start()
	if startErr != nil {
		t.Logf("start refused the file: %v", startErr)
		return
	}

	// And a recovered store must be usable, not a dead handle.
	require.NotNil(t, cacheFile.database())
	require.NoError(t, cacheFile.view(func(tx *bbolt.Tx) error { return nil }),
		"the store published after the sweep must accept reads")
}

// TestStartSurvivesAFileWithNoUserBuckets is the supported-shape control for the sweep.
//
// A file bbolt itself produced and then emptied must start cleanly: it proves the sweep
// runs on this path at all, which is what makes the guard above meaningful rather than a
// line that is never executed.
func TestStartSurvivesAFileWithNoUserBuckets(t *testing.T) {
	path := t.TempDir() + "/empty.db"

	db, err := bbolt.Open(path, 0o666, nil)
	require.NoError(t, err)
	// A stale bucket from a version that no longer exists, to exercise the sweep's delete arm.
	require.NoError(t, db.Update(func(tx *bbolt.Tx) error {
		_, createErr := tx.CreateBucket([]byte("stale_bucket_from_an_older_version"))
		return createErr
	}))
	require.NoError(t, db.Close())

	cacheFile := New(
		context.Background(),
		logger.NOP(),
		option.CacheFileOptions{Enabled: true, Path: path, StoreFakeIP: true},
	)
	t.Cleanup(func() {
		if openDB := cacheFile.database(); openDB != nil {
			_ = openDB.Close()
		}
	})

	require.NoError(t, cacheFile.start(), "a well-formed file with an unknown bucket must start")

	// The sweep's job: the unknown bucket is gone.
	require.NoError(t, cacheFile.view(func(tx *bbolt.Tx) error {
		require.Nil(t, tx.Bucket([]byte("stale_bucket_from_an_older_version")),
			"an unknown top-level bucket must be removed by the startup sweep")
		return nil
	}))
}
