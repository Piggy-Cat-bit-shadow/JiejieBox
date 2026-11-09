//go:build linux

package process

// The two lookups a socket owner can take, and what happens when each one cannot answer.
//
// The precise netlink query names one socket by its exact tuple. It can legitimately find nothing -
// the request is narrow - while the connection still has an owner. The socket table lists every
// socket with its owner, so a MISS falls back to it; a real ERROR does not, because a dump would
// not explain the error and would hide it.
//
// The /proc side has the mirror-image case: a refused walk (another user's process, a hardened
// /proc, a sandbox) is an UNKNOWN answer, not a wrong one. The uid is already independently
// verified from the socket table at that point, so the result must stay that uid and must not
// invent a path.

import (
	"context"
	"net"
	"net/netip"
	"os"
	"sync/atomic"
	"syscall"
	"testing"

	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

func newFallbackTestSearcher(t *testing.T) *linuxSearcher {
	t.Helper()
	return newScanTestSearcher(t, func(uid uint32) (map[uint32][]string, error) {
		return nil, nil
	})
}

// dialLoopback returns a connected client and the listener address it dialled.
func dialLoopback(t *testing.T) (net.Conn, net.Addr) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	conn, err := net.Dial("tcp", listener.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn, listener.Addr()
}

func TestAPreciseQueryMissFallsBackToTheSocketTable(t *testing.T) {
	t.Parallel()

	conn, listenerAddr := dialLoopback(t)
	source := M.AddrPortFromNet(conn.LocalAddr())
	destination := M.AddrPortFromNet(listenerAddr)

	searcher := newFallbackTestSearcher(t)
	var preciseQueries atomic.Int32
	searcher.querySocket = func(netip.AddrPort, netip.AddrPort) (uint32, uint32, error) {
		preciseQueries.Add(1)
		return 0, 0, ErrNotFound
	}

	inode, uid, err := searcher.resolveSocketByNetlink(N.NetworkTCP, source, destination)
	require.NoError(t, err, "a precise-query miss must be answered by the socket table")
	require.EqualValues(t, 1, preciseQueries.Load(), "the precise query is attempted once before the fallback")
	require.EqualValues(t, os.Getuid(), uid)
	require.Equal(t, socketInode(t, conn.(syscall.Conn)), inode,
		"the fallback must resolve the SAME socket, not merely some socket of that uid")
}

func TestAPreciseQueryErrorIsNotMaskedByTheFallback(t *testing.T) {
	t.Parallel()

	conn, listenerAddr := dialLoopback(t)
	source := M.AddrPortFromNet(conn.LocalAddr())
	destination := M.AddrPortFromNet(listenerAddr)

	searcher := newFallbackTestSearcher(t)
	netlinkErr := os.ErrPermission
	searcher.querySocket = func(netip.AddrPort, netip.AddrPort) (uint32, uint32, error) {
		return 0, 0, netlinkErr
	}

	_, _, err := searcher.resolveSocketByNetlink(N.NetworkTCP, source, destination)
	require.ErrorIs(t, err, netlinkErr,
		"a netlink failure is reported, not replaced by a table dump that cannot explain it")
}

func TestARefusedProcessScanLeavesTheOwnerWithoutAPath(t *testing.T) {
	t.Parallel()

	conn, listenerAddr := dialLoopback(t)
	refused := &os.PathError{Op: "open", Path: pathProc, Err: syscall.EACCES}
	searcher := newScanTestSearcher(t, func(uid uint32) (map[uint32][]string, error) {
		return nil, refused
	})

	info, err := FindProcessInfo(
		searcher,
		context.Background(),
		N.NetworkTCP,
		M.AddrPortFromNet(conn.LocalAddr()),
		M.AddrPortFromNet(listenerAddr),
	)
	require.NoError(t, err, "a refused walk is an unknown path, not a failed lookup")
	require.EqualValues(t, os.Getuid(), info.UserId, "the verified owner is still reported")
	require.Empty(t, info.ProcessPaths, "a refused walk must not invent a process path")
	require.Empty(t, info.PackageNames)
}
