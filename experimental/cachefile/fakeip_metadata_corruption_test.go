package cachefile

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sagernet/bbolt"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/logger"

	"github.com/stretchr/testify/require"
)

// A malformed persisted VALUE must not cost the user the whole cache file.
//
// # The oracle
//
// LX 474aa5b5 ("a corrupted cache file crashed the client") and LX 080f1c56 ("adding a
// recover is not the same as being safe") are the two halves this pins. The fork handles
// the shapes bbolt itself rejects by removing and recreating the file, and the FakeIP
// bucket is reset correctly when it is merely missing (LX 886422d2).
//
// The remaining exposure is a value that parses far enough to be handed to a decoder, and
// the FakeIP metadata is decoded with a helper that PANICS on a short read:
//
//	// adapter/fakeip_metadata.go
//	for _, unmarshaler := range []encoding.BinaryUnmarshaler{&m.Inet4Range, ...} {
//		var length uint16
//		common.Must(binary.Read(reader, binary.BigEndian, &length))   // panic on EOF
//
// # Why the blast radius is the whole file
//
// The panic happens inside `FakeIPMetadata`'s closure, which runs inside `CacheFile.view`.
// That wrapper's recover responds by calling `resetDB`, and `resetDB` calls
// `filemanager.Remove(c.ctx, c.path)` - it deletes the ENTIRE cache file, not the one bad
// bucket. So a three-byte value in the FakeIP metadata key destroys the DNS cache, the
// rule-set cache, the persisted selector choices, the Clash mode and every FakeIP mapping.
//
// # Why that is worse than the panic
//
// `FakeIPMetadata()` then returns nil, the store takes the "no metadata" branch and
// resets its cursor to the start of the range WITHOUT recording a retired generation. The
// addresses it re-issues to new domains are still live for the domains that held them, so
// the reverse mapping is now ambiguous. That is the exact hazard the fork's own comment at
// experimental/cachefile/fakeip.go describes as already fixed.
//
// # What a correct outcome is
//
// The value is untrusted input, so the honest responses are "ignore this bucket" or "reset
// this bucket". Losing every unrelated cache because one value was short is not one of
// them, and neither is a panic on the startup path.

// cacheFileWithRawFakeIPMetadata writes a cache file whose FakeIP metadata value is the
// given raw bytes, and starts a CacheFile over it.
func cacheFileWithRawFakeIPMetadata(t *testing.T, raw []byte) (*CacheFile, string, []byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cache.db")

	// A file with real, unrelated cache content, so "the whole file was deleted" is
	// distinguishable from "the file was empty anyway".
	db, err := bbolt.Open(path, 0o666, nil)
	require.NoError(t, err)
	require.NoError(t, db.Update(func(tx *bbolt.Tx) error {
		selected, createErr := tx.CreateBucket([]byte("selected"))
		if createErr != nil {
			return createErr
		}
		if putErr := selected.Put([]byte("proxy"), []byte("node-a")); putErr != nil {
			return putErr
		}
		fakeIP, createErr := tx.CreateBucket([]byte(bucketFakeIP))
		if createErr != nil {
			return createErr
		}
		return fakeIP.Put([]byte(keyMetadata), raw)
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
	require.NoError(t, cacheFile.start(), "the file itself is well-formed and must start")
	return cacheFile, path, raw
}

// requireUnrelatedCacheSurvives asserts that a bucket the malformed value had nothing to do
// with is still readable.
func requireUnrelatedCacheSurvives(t *testing.T, cacheFile *CacheFile, selected string) {
	t.Helper()
	require.Equal(t, selected, cacheFile.LoadSelected("proxy"),
		"a malformed FakeIP metadata value destroyed an UNRELATED cache bucket: the recovery path "+
			"deletes the whole file instead of the one bucket it could not read")
}

// TestMalformedFakeIPMetadataDoesNotPanic is the direct statement of LX 474aa5b5 for this
// value, and it is the half a short read makes reachable.
func TestMalformedFakeIPMetadataDoesNotPanic(t *testing.T) {
	for _, testCase := range []struct {
		name string
		raw  []byte
		why  string
	}{
		{"zero length", []byte{}, "an empty value is what a torn write leaves"},
		{"one byte", []byte{0x00}, "a length prefix with no element behind it"},
		{"three bytes", []byte{0x00, 0x01, 0x00}, "the first element's length is 1 but element 2's header is missing"},
		{"first element only", []byte{0x00, 0x05, 0x00, 0x00, 0x00, 0x00, 0x00}, "cut after element 1"},
		{"two elements only", []byte{0x00, 0x05, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x05, 0x00, 0x00, 0x00, 0x00}, "cut after element 2"},
		{"length overruns value", []byte{0xFF, 0xFF, 0x01}, "a length prefix larger than the value"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cacheFile, _, _ := cacheFileWithRawFakeIPMetadata(t, testCase.raw)

			// The assertion is "this returns without panicking". A nil metadata is the
			// correct answer for an unreadable value; a panic is the defect.
			metadata := cacheFile.FakeIPMetadata()
			t.Logf("%s: metadata=%v", testCase.name, metadata)

			requireUnrelatedCacheSurvives(t, cacheFile, "node-a")
		})
	}
}

// TestMalformedFakeIPMetadataKeepsTheRestOfTheCache is the Blast-Radius half.
//
// Even where the value cannot be read, the rest of the cache has to survive: it is a
// different bucket, and the DNS cache, the rule-set cache, the persisted selections and the
// Clash mode have nothing to do with the FakeIP ledger. A recovery that deletes all of it
// turns one unreadable value into a cold start for everything.
func TestMalformedFakeIPMetadataKeepsTheRestOfTheCache(t *testing.T) {
	cacheFile, path, _ := cacheFileWithRawFakeIPMetadata(t, []byte{0x00, 0x01, 0x00})

	_ = cacheFile.FakeIPMetadata()

	requireUnrelatedCacheSurvives(t, cacheFile, "node-a")

	// The file itself must still exist: deleting it is the mechanism by which the rest of
	// the cache is lost, and a user who opens the path afterwards must not find it gone.
	_, statErr := os.Stat(path)
	require.NoError(t, statErr,
		"the cache FILE was deleted by the recovery path, so every unrelated bucket is gone too")
}

// TestEmptyFakeIPMetadataBucketIsTheSupportedShape is the positive control.
//
// It separates "the store is empty" from "the value is malformed": an absent key is the
// ordinary first-run case and must produce nil metadata with the rest of the cache intact.
func TestEmptyFakeIPMetadataBucketIsTheSupportedShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
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
	require.NoError(t, cacheFile.start())

	require.Nil(t, cacheFile.FakeIPMetadata(),
		"a store with no FakeIP metadata must report none, so the caller resets its range")
}
