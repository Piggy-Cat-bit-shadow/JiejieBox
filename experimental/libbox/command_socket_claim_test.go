package libbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// claimCommandSocket is the server's answer to "may I bind this path". The failure it exists to
// prevent is not a crash: it is two live servers sharing one address, where the newer one believes
// it owns the control channel and the older one keeps the connections it already accepted. Nothing
// reports that, and every new client lands on whichever server bound last.

func TestClaimCommandSocketRemovesAStaleFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), commandSocketName)
	require.NoError(t, os.WriteFile(path, nil, 0o600))

	// Nothing answers on it: this is what a Network Extension that died without unlinking leaves.
	claimed := false
	require.NoError(t, claimCommandSocket(path, func(string) bool {
		claimed = true
		return false
	}))
	require.True(t, claimed, "liveness was never asked")
	_, err := os.Lstat(path)
	require.ErrorIs(t, err, os.ErrNotExist, "a stale socket file must be removed so bind can succeed")
}

func TestClaimCommandSocketRefusesALiveListener(t *testing.T) {
	path := filepath.Join(t.TempDir(), commandSocketName)
	require.NoError(t, os.WriteFile(path, nil, 0o600))

	err := claimCommandSocket(path, func(string) bool { return true })
	require.Error(t, err)
	require.Contains(t, err.Error(), "already listening")
	_, statErr := os.Lstat(path)
	require.NoError(t, statErr, "the file of a live listener must not be touched")
}

func TestClaimCommandSocketIgnoresAMissingPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), commandSocketName)
	require.NoError(t, claimCommandSocket(path, func(string) bool {
		t.Fatal("liveness was asked about a path that does not exist")
		return false
	}))
}

func TestClaimCommandSocketRefusesAForeignName(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "someone-elses.sock")
	require.NoError(t, os.WriteFile(path, nil, 0o600))

	// A name this package does not allocate is not ours to clear, even when nothing answers on it.
	err := claimCommandSocket(path, func(string) bool { return false })
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a name this server allocates")
	_, statErr := os.Lstat(path)
	require.NoError(t, statErr)
}

func TestClaimCommandSocketAcceptsAGeneratedName(t *testing.T) {
	// The hashed fallback name is ours; a stale one must be cleared exactly like command.sock.
	name, ok := hashedSocketName(filepath.Join(t.TempDir(), commandSocketName), 8)
	require.True(t, ok)
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, nil, 0o600))
	require.NoError(t, claimCommandSocket(path, func(string) bool { return false }))
	_, err := os.Lstat(path)
	require.ErrorIs(t, err, os.ErrNotExist)
}
