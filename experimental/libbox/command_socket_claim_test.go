package libbox

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// claimCommandSocket is the server's answer to "may I bind this path". The failures it exists to
// prevent are not crashes: a second instance that clears a live path leaves the first serving the
// connections it already accepted while every new client lands on the second, and nobody reports it.
//
// The probes below are injected so each branch is exercised on any platform, including the ones that
// cannot leave a unix socket file behind.

func TestClaimCommandSocketRemovesAStaleFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), commandSocketName)
	require.NoError(t, os.WriteFile(path, nil, 0o600))

	stale := false
	require.NoError(t, claimCommandSocket(path, func(string) socketLiveness {
		stale = true
		return socketStale
	}))
	require.True(t, stale, "liveness was never asked")
	_, err := os.Lstat(path)
	require.ErrorIs(t, err, os.ErrNotExist, "a stale socket file must be removed so bind can succeed")
}

func TestClaimCommandSocketRefusesALiveListener(t *testing.T) {
	path := filepath.Join(t.TempDir(), commandSocketName)
	require.NoError(t, os.WriteFile(path, nil, 0o600))

	err := claimCommandSocket(path, func(string) socketLiveness { return socketLive })
	require.Error(t, err)
	require.Contains(t, err.Error(), "already listening")
	_, statErr := os.Lstat(path)
	require.NoError(t, statErr, "the file of a live listener must not be touched")
}

// TestClaimCommandSocketRefusesWhenItCannotTell covers the second half of the review finding: a probe
// that times out, is refused by permissions, or runs on a platform without unix sockets used to be
// indistinguishable from "nothing is listening", so the path was deleted on a guess. It must refuse and
// leave the file exactly where it is.
func TestClaimCommandSocketRefusesWhenItCannotTell(t *testing.T) {
	path := filepath.Join(t.TempDir(), commandSocketName)
	require.NoError(t, os.WriteFile(path, []byte("live?"), 0o600))

	err := claimCommandSocket(path, func(string) socketLiveness { return socketUnknown })
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot tell whether")
	require.Contains(t, err.Error(), "refusing to remove it")
	content, readErr := os.ReadFile(path)
	require.NoError(t, readErr, "an undecidable probe must not remove the file")
	require.Equal(t, []byte("live?"), content)
}

func TestClaimCommandSocketIgnoresAMissingPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), commandSocketName)
	require.NoError(t, claimCommandSocket(path, func(string) socketLiveness {
		t.Fatal("liveness was asked about a path that does not exist")
		return socketUnknown
	}))
}

func TestClaimCommandSocketRefusesAForeignName(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "someone-elses.sock")
	require.NoError(t, os.WriteFile(path, nil, 0o600))

	// A name this package does not allocate is not ours to clear, even when nothing answers on it.
	err := claimCommandSocket(path, func(string) socketLiveness {
		t.Fatal("liveness must not be asked about a foreign name")
		return socketStale
	})
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
	require.NoError(t, claimCommandSocket(path, func(string) socketLiveness { return socketStale }))
	_, err := os.Lstat(path)
	require.ErrorIs(t, err, os.ErrNotExist)
}

// TestTheOwnershipLockSerialisesInstances covers the first half of the same finding: probe-then-remove
// is not atomic, so a competitor could bind between the two steps and have its live socket deleted. The
// lock removes the competitor's access to that sequence entirely.
func TestTheOwnershipLockSerialisesInstances(t *testing.T) {
	path := filepath.Join(t.TempDir(), commandSocketName)
	lockPath := commandSocketLockPath(path)

	first, err := lockCommandSocketPath(lockPath)
	require.NoError(t, err, "the first instance must acquire the path's lock")
	t.Cleanup(func() { unlockCommandSocketPath(first) })

	second, err := lockCommandSocketPath(lockPath)
	require.Error(t, err, "a second instance must not acquire the same path's lock")
	require.Nil(t, second)
}

// TestASecondInstanceCannotDeleteALiveSocket is the end-to-end shape of the finding: instance one holds
// the lock and has a real listener bound; instance two's acquire fails before it can probe or remove
// anything, and instance one is still reachable afterwards.
func TestASecondInstanceCannotDeleteALiveSocket(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, commandSocketName)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Skipf("this platform has no unix sockets (%v)", err)
	}
	defer func() {
		_ = listener.Close()
	}()

	firstLock, err := lockCommandSocketPath(commandSocketLockPath(path))
	require.NoError(t, err)
	defer unlockCommandSocketPath(firstLock)

	// What a second CommandServer.Start does, in order: take the lock, then claim.
	lock, err := acquireCommandSocket(path)
	require.Error(t, err)
	require.Nil(t, lock)
	require.Contains(t, err.Error(), "another instance owns")

	_, statErr := os.Lstat(path)
	require.NoError(t, statErr, "the live instance's socket file must still exist")
	connection, dialErr := net.Dial("unix", path)
	require.NoError(t, dialErr, "the live instance must still be reachable")
	_ = connection.Close()
}

// TestAcquireCommandSocketReleasesTheLockOnClaimFailure keeps a failed start from leaving the path
// permanently owned: the next attempt must be able to take the lock.
func TestAcquireCommandSocketReleasesTheLockOnClaimFailure(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, commandSocketName)
	// A directory at the socket name makes the claim refuse without depending on any probe: the mode
	// check rejects it before liveness is consulted.
	require.NoError(t, os.Mkdir(path, 0o700))

	lock, acquireErr := acquireCommandSocket(path)
	require.Error(t, acquireErr)
	require.Nil(t, lock)

	// The lock must be free again.
	recovered, lockErr := lockCommandSocketPath(commandSocketLockPath(path))
	require.NoError(t, lockErr, "a refused claim must release the ownership lock")
	unlockCommandSocketPath(recovered)
}

func TestClaimCommandSocketRefusesADirectoryWithOurName(t *testing.T) {
	path := filepath.Join(t.TempDir(), commandSocketName)
	require.NoError(t, os.Mkdir(path, 0o700))

	err := claimCommandSocket(path, func(string) socketLiveness {
		t.Fatal("liveness must not be asked about a name that is not a socket or a file")
		return socketStale
	})
	require.Error(t, err, "a directory is not a socket this server may remove")
	require.Contains(t, err.Error(), "not a socket or a regular file")
	info, statErr := os.Stat(path)
	require.NoError(t, statErr)
	require.True(t, info.IsDir())
}
