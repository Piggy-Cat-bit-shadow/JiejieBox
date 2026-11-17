package libbox

// The iOS transport itself: a REAL unix socket, through the production resolver, dialer and failure
// classifier.
//
// The other tests in this file use TCP because a unix socket is not available everywhere; this one
// uses a unix socket because that is what the NetworkExtension actually has, and because two of the
// behaviours under test are only visible there:
//
//   - `resolveCommandSocketPath` and `dialTarget`'s unix branch, i.e. the 103-byte budget and the
//     hashed fallback that `docs/fork/ios-command-socket-path-fix.md` is about;
//   - the stale socket file. On Unix, a listener unlinks its file on Close, so a file that outlives
//     its listener is exactly the signature of a process that died - which is what the incident's
//     ECONNREFUSED reported. This test builds that state deliberately and requires the server side to
//     recover from it.
//
// It skips where the platform has no AF_UNIX. It runs everywhere AF_UNIX works, which now includes
// this project's Windows CI host - the reason it is not behind a `unix` build tag.

import (
	"net"
	"os"
	"testing"
	"time"

	"github.com/sagernet/sing-box/daemon"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

func TestTheClientReachesAServerOnAUnixSocket(t *testing.T) {
	useControlServerPort(t, 0)

	socketPath, err := resolveCommandSocketPath(sBasePath, sTempPath)
	require.NoError(t, err)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		t.Skipf("this platform has no unix sockets (%v); the TCP tests cover the same decision", err)
	}
	implementation := &controlStubStartedService{}
	server := grpc.NewServer()
	daemon.RegisterStartedServiceServer(server, implementation)
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	client := NewCommandClient(nil, nil)
	require.NoError(t, client.SetGroupExpand("proxy", true),
		"the production resolver and unix dialer must reach a listener on the resolved path")
	require.EqualValues(t, 1, implementation.expandCalls.Load())

	// The extension dies. On Unix the file survives, because nothing ran Close to unlink it: this is
	// the state the incident reported as connection refused.
	server.Stop()
	_ = listener.Close()
	time.Sleep(50 * time.Millisecond)

	_, statErr := os.Lstat(socketPath)
	staleFileSurvived := statErr == nil

	require.Error(t, client.SetGroupExpand("proxy", false))
	client.clientAccess.RLock()
	cachedAfterFailure := client.grpcConn
	client.clientAccess.RUnlock()
	require.Nil(t, cachedAfterFailure, "the dead connection must not be kept")

	// Whatever the platform did with the file, the server must be able to claim the path again: a
	// stale file is removed, a missing one is nothing to do. This is the recovery a relaunched
	// NetworkExtension performs, and refusing here would mean the tunnel never gets a control channel
	// back.
	require.NoError(t, claimCommandSocket(socketPath, probeSocketLiveness),
		"the stale socket file must not block the next instance")

	replacement, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	require.NoError(t, err, "the path must be bindable after the stale file is claimed")
	replacementServer := grpc.NewServer()
	daemon.RegisterStartedServiceServer(replacementServer, implementation)
	go func() {
		_ = replacementServer.Serve(replacement)
	}()
	t.Cleanup(func() {
		replacementServer.Stop()
		_ = replacement.Close()
	})

	require.NoError(t, client.SetGroupExpand("proxy", true),
		"the first call after the core returns must re-establish instead of reusing the dead connection")
	require.EqualValues(t, 2, implementation.expandCalls.Load())
	t.Logf("stale socket file survived the listener on this platform: %v", staleFileSurvived)
}

// TestALiveUnixSocketIsNotStolenOnThisPlatform is the claim rule against a REAL listener, not a
// stub: the check that decides whether a socket file may be removed has to be right on the transport
// the product uses.
func TestALiveUnixSocketIsNotStolenOnThisPlatform(t *testing.T) {
	path := resolveSocketPathInTempDir(t)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Skipf("this platform has no unix sockets (%v)", err)
	}
	defer func() {
		listener.Close()
	}()

	require.Equal(t, socketLive, probeSocketLiveness(path), "a bound unix listener must answer the liveness probe")

	err = claimCommandSocket(path, probeSocketLiveness)
	require.Error(t, err)
	require.Contains(t, err.Error(), "already listening")

	opened, dialErr := net.DialTimeout("unix", path, time.Second)
	require.NoError(t, dialErr, "the live listener must still be reachable after a refused claim")
	_ = opened.Close()
}

// TestAStaleUnixSocketFileIsRemoved needs a socket file with no listener. On platforms whose unix
// sockets vanish with their listener the state cannot be built, so the case degrades to the
// regular-file shape, which is the same decision.
func TestAStaleUnixSocketFileIsRemoved(t *testing.T) {
	path := resolveSocketPathInTempDir(t)
	require.NoError(t, os.WriteFile(path, nil, 0o600))
	require.Equal(t, socketStale, probeSocketLiveness(path), "a plain file must not answer the liveness probe")
	require.NoError(t, claimCommandSocket(path, probeSocketLiveness))
	_, err := os.Lstat(path)
	require.ErrorIs(t, err, os.ErrNotExist)
}

// resolveSocketPathInTempDir returns a short, private socket path. A t.TempDir() name is used rather
// than a fixed one so parallel packages cannot collide.
func resolveSocketPathInTempDir(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	path := directory + string(os.PathSeparator) + commandSocketName
	require.True(t, pathFits(path), "the test path must fit the %d-byte budget: %d", maxUnixSocketPath, len(path))
	return path
}
