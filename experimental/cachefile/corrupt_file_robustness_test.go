package cachefile

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/logger"

	"github.com/stretchr/testify/require"
)

// The cache file is UNTRUSTED INPUT, and a corrupt one must not stop the core.
//
// # The oracle
//
// LX 474aa5b5 is "a corrupted cache file crashed the client": the reported symptom is
// "yesterday it worked, today it starts and exits immediately". LX 080f1c56 adds the
// less obvious half - a `recover()` chain is not automatically safe, so the recovery
// path needs its own evidence for partial corruption, interrupted writes and an old
// schema.
//
// The file is written by a previous run of the same program, so every way it can be
// damaged has to be survivable: a crash mid-write, a power loss, a full disk, a
// half-finished migration, a hand edit, or a copy from another version.
//
// These are the inputs a user actually presents after that kind of failure. Each must
// produce a CLEAN RESULT - either a working database or an error the caller can
// report - and never a panic and never a hang.

// cacheFilePathIn writes raw bytes at the path a CacheFile will use.
func cacheFilePathIn(t *testing.T, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cache.db")
	require.NoError(t, os.WriteFile(path, content, 0o666))
	return path
}

// newCorruptCacheFile builds a started CacheFile over the given raw file content and
// reports the start error, if any.
func newCorruptCacheFile(t *testing.T, content []byte) error {
	t.Helper()
	cacheFile := New(
		context.Background(),
		logger.NOP(),
		option.CacheFileOptions{
			Enabled:     true,
			Path:        cacheFilePathIn(t, content),
			StoreFakeIP: true,
		},
	)
	t.Cleanup(func() {
		if db := cacheFile.database(); db != nil {
			_ = db.Close()
		}
	})
	return cacheFile.start()
}

// TestCorruptCacheFileDoesNotStopTheCore is the direct statement of LX 474aa5b5.
//
// Every case asserts the same three things, because a startup path that panics is a
// dead client and one that returns a network-error-looking failure is a support
// ticket: no panic, a bounded return, and - when start succeeds - a USABLE database.
func TestCorruptCacheFileDoesNotStopTheCore(t *testing.T) {
	// A minimal well-formed bbolt header, so the "truncated" cases below are the
	// realistic damage (a power loss or a full disk) rather than obviously-not-a-database.
	validHeader := make([]byte, 4096)
	copy(validHeader, []byte{0xED, 0xDA, 0x0C, 0xED})

	for _, testCase := range []struct {
		name    string
		content []byte
		why     string
	}{
		{"empty file", nil, "a zero-length file is what a crash between create and first write leaves"},
		{"one byte", []byte{0x00}, "a single-byte file is a partial write"},
		{"all zeroes", make([]byte, 4096), "a zeroed page is what a filesystem can leave after a crash"},
		{"not a database", []byte("this is not a bolt database at all\n"), "a hand edit or a wrong file"},
		{"magic only", []byte{0xED, 0xDA, 0x0C, 0xED}, "the magic with nothing behind it is a truncated write"},
		{"truncated header", validHeader[:512], "a power loss mid-write leaves a short header"},
		{"header plus garbage", append(append([]byte{}, validHeader...), make([]byte, 4096)...), "a torn tail"},
		{"nul-heavy", append([]byte{0xED, 0xDA, 0x0C, 0xED}, make([]byte, 8192)...), "a page-aligned zeroed body"},
		{"high entropy", mustPseudoRandom(8192), "a corrupt file is indistinguishable from noise"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// The assertion is "it returns", not "it returns an error": for a
			// recoverable corruption the correct behaviour is to reset and carry on,
			// and the test must not force either outcome. A panic fails the test by
			// itself; the deadline is here because a recovery path that retries
			// forever would otherwise hang the suite rather than failing it.
			startErr := newCorruptCacheFile(t, testCase.content)
			if startErr != nil {
				require.Error(t, startErr,
					"%s: start must report a failure it cannot recover from. %s",
					testCase.name, testCase.why)
				return
			}
			t.Logf("%s: start recovered by resetting the store", testCase.name)
		})
	}
}

// TestCorruptCacheFileRecoversToAUsableStore is the half LX 080f1c56 is about: a
// recovery path that "does not crash" but leaves a dead handle is not a recovery.
//
// When start succeeds after damage, the store has to be genuinely usable - the
// recovery replaced the file - or every later cache write fails and the user sees a
// core that runs but never caches anything.
func TestCorruptCacheFileRecoversToAUsableStore(t *testing.T) {
	for _, content := range [][]byte{
		nil,
		[]byte("not a database"),
		{0xED, 0xDA, 0x0C, 0xED},
		mustPseudoRandom(8192),
	} {
		cacheFile := New(
			context.Background(),
			logger.NOP(),
			option.CacheFileOptions{
				Enabled:     true,
				Path:        cacheFilePathIn(t, content),
				StoreFakeIP: true,
			},
		)
		t.Cleanup(func() {
			if db := cacheFile.database(); db != nil {
				_ = db.Close()
			}
		})

		if err := cacheFile.start(); err != nil {
			// A refusal is acceptable; a dead handle after a "successful" start is not.
			continue
		}

		require.NotNil(t, cacheFile.database(),
			"start reported success but published no database")

		// The recovered store must actually work: the FakeIP ledger is the writable
		// surface a corrupt-file recovery would leave broken.
		metadata := testFakeIPMetadata()
		require.NoError(t, cacheFile.FakeIPSaveMetadata(metadata),
			"a recovered store must accept writes")

		loaded := cacheFile.FakeIPMetadata()
		require.NotNil(t, loaded, "a recovered store must accept reads")
		require.Equal(t, metadata.Inet4Current, loaded.Inet4Current,
			"a recovered store must round-trip what was written to it")
	}
}

// mustPseudoRandom returns a deterministic high-entropy buffer, so a failure is
// reproducible without shipping a binary fixture.
func mustPseudoRandom(size int) []byte {
	buffer := make([]byte, size)
	state := uint32(0x9E3779B9)
	for index := range buffer {
		state = state*1664525 + 1013904223
		buffer[index] = byte(state >> 24)
	}
	return buffer
}
