//go:build linux

package process

import (
	"context"
	"net"
	"os"
	"sync/atomic"
	"testing"

	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// P04 owner-only fast path: a user/user_id-only rule must be resolvable from
// the socket diag table without a /proc walk.
func TestFindProcessOwnerSkipsTheProcessScan(t *testing.T) {
	t.Parallel()

	var scans atomic.Int32
	searcher := newScanTestSearcher(t, func(uid uint32) (map[uint32][]string, error) {
		scans.Add(1)
		return nil, nil
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	conn, err := net.Dial("tcp", listener.Addr().String())
	require.NoError(t, err)
	defer conn.Close()

	info, err := FindProcessOwner(
		searcher,
		context.Background(),
		N.NetworkTCP,
		M.AddrPortFromNet(conn.LocalAddr()),
		M.AddrPortFromNet(listener.Addr()),
	)
	require.NoError(t, err)
	require.Equal(t, int32(os.Getuid()), info.UserId)
	require.Empty(t, info.ProcessPaths)
	require.Zero(t, scans.Load(), "the owner-only lookup must not walk /proc")
}
