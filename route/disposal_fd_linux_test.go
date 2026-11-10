//go:build linux

package route

// The ledger must match the actual kernel objects, not just its own counters. With the production
// budget and one blocked Close, every retired socket is still an open file descriptor; after release
// the descriptor count returns to the baseline.

import (
	"os"
	"testing"
	"time"

	"github.com/sagernet/sing-box/log"

	"github.com/stretchr/testify/require"
)

func openFDCountForDisposal(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	require.NoError(t, err)
	return len(entries)
}

func TestTheDisposalLedgerMatchesTheOpenFileDescriptors(t *testing.T) {
	manager := NewConnectionManager(log.NewNOPFactory().Logger())
	t.Cleanup(func() { _ = manager.Close() })
	baseline := openFDCountForDisposal(t)

	conns := make([]*blockedPacketClose, 0, disposalBacklogBudget+1)
	for index := 0; index < disposalBacklogBudget+1; index++ {
		conn := newBlockedPacketClose(t)
		conns = append(conns, conn)
		manager.TrackPacketConn(conn)
	}
	manager.Reclaim(ReclaimNetworkTransition)
	require.Equal(t, disposalBacklogBudget+1,
		manager.pendingDisposals()+manager.condemnedDisposals(),
		"the whole batch must be on the ledger")
	require.Equal(t, baseline+len(conns), openFDCountForDisposal(t),
		"each retired socket must hold exactly one live file descriptor while its Close is blocked")

	releaseAllBlocked(conns)
	require.Eventually(t, func() bool {
		return manager.pendingDisposals() == 0 && manager.condemnedDisposals() == 0
	}, 10*time.Second, 5*time.Millisecond, "the ledger never emptied")
	require.Eventually(t, func() bool {
		return openFDCountForDisposal(t) <= baseline
	}, 10*time.Second, 5*time.Millisecond, "the file descriptors were not released with the sockets")
}
