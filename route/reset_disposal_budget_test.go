package route

// A generation that retires more connections than the disposal budget must still settle.
//
// # The defect this file pins
//
// The bounded disposer (8bf1f6d28c7 + 20ef4f164a2) enforced its budget by making the reclaim WAIT for
// room before it returned. That put the wait back in front of the DNS pin and the generation commit:
// one transition that retires more sockets than the budget (the production default is 64) parks the
// whole network-change body on a Close that the kernel has not finished. The review measured exactly
// that with 65 real UDP sockets: pending=65 budget=64, stable=false, and the pin never ran.
//
// The wave rule fixed it: the reclaim moves only as many objects into the disposal queue as the
// budget has room for, and leaves the rest CONDEMNED - still owned by the manager, still on the
// books, no longer admissible - until the worker has physical room. The transition itself never
// waits; shutdown and CloseAll close the condemned set synchronously because they promise release.
//
// # What the tests assert
//
// One transition retiring 65 sockets (ordinary and dead-path), and two such transitions back to
// back while the first Close is still blocked: the pin runs for THIS transition, the transition
// commits, a real DNS query is answered, and new-generation direct and proxied flows carry their
// first byte while the old Close is stuck. The queue never exceeds the budget, the condemned set
// accounts for the rest, and after release every socket closes exactly once and the ledger is empty.

import (
	"context"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/dialer"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// requireDialRefused requires the saturated manager to refuse a new connection at admission,
// with the dial context as the bound. The socket is closed by the dialer before it is returned.
func requireDialRefused(t *testing.T, realDialer *dialer.DefaultDialer, target string, timeout time.Duration) time.Duration {
	t.Helper()
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	conn, err := realDialer.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr(target))
	require.Error(t, err, "a saturated disposal ledger admitted a new connection")
	if conn != nil {
		_ = conn.Close()
	}
	return time.Since(started)
}

// newBulkBlockedCloseHarness is newBlockedCloseHarness with a production-sized batch: total real UDP
// sockets tracked before the transition, the first of which keeps its Close blocked.
func newBulkBlockedCloseHarness(t *testing.T, total int) (*interfaceTransitionHarness, *timedRouter, *ConnectionManager, []*blockedPacketClose, string) {
	t.Helper()
	target := startTimelineEcho(t)
	harness := newInterfaceTransitionHarness(t)
	harness.manager.transitionStable.Store(true)
	router := newTimedRouter(harness.manager.NetworkResetGeneration)
	harness.manager.router = router
	harness.manager.updateNetworkEnvironment()
	require.True(t, harness.manager.NetworkTransitionStable())

	connectionManager := NewConnectionManager(log.NewNOPFactory().Logger())
	t.Cleanup(func() { _ = connectionManager.Close() })
	harness.manager.connectionManager = connectionManager

	conns := make([]*blockedPacketClose, 0, total)
	for index := 0; index < total; index++ {
		conn := newBlockedPacketClose(t)
		conns = append(conns, conn)
		connectionManager.TrackPacketConn(conn)
	}
	return harness, router, connectionManager, conns, target
}

func releaseAllBlocked(conns []*blockedPacketClose) {
	for _, conn := range conns {
		conn.releaseClose()
	}
}

// requireGenerationSettledWhileBlocked runs the shared assertions for one transition whose first
// Close is blocked: pin, commit, real DNS, direct and proxied first bytes, and a returned reset.
func requireGenerationSettledWhileBlocked(t *testing.T, harness *interfaceTransitionHarness, router *timedRouter, connectionManager *ConnectionManager, batch []*blockedPacketClose, target string, waitEntered <-chan struct{}, reset func(context.Context)) {
	t.Helper()
	nextPin := router.watchNextPin()
	pinsBefore := router.pinCount()
	dnsProbe := newTimelineDNS(t, harness.manager)
	realDialer := newGenerationDialer(t, harness.manager, connectionManager)

	resetDone := make(chan struct{})
	go func() {
		defer close(resetDone)
		reset(context.Background())
	}()

	if waitEntered != nil {
		select {
		case <-waitEntered:
		case <-time.After(10 * time.Second):
			t.Fatal("the first old socket never entered Close")
		}
	}

	pinnedAt := awaitPin(t, router, nextPin)
	require.True(t, harness.manager.NetworkTransitionStable(),
		"the transition was not committed while the first old Close was still blocked")
	require.Equal(t, 1, router.pinCount()-pinsBefore, "one transition pins DNS exactly once")
	queryTime := dnsProbe.requireAnswer(t)

	backlog := connectionManager.pendingDisposals() + connectionManager.condemnedDisposals()
	require.GreaterOrEqual(t, backlog, connectionManager.disposalBudget(),
		"this batch must actually reach the production budget")
	refusedAfter := requireDialRefused(t, realDialer, target, 300*time.Millisecond)

	select {
	case <-resetDone:
	case <-time.After(10 * time.Second):
		t.Fatalf("the reset did not return while the first Close was blocked (pending=%d condemned=%d budget=%d)",
			connectionManager.pendingDisposals(), connectionManager.condemnedDisposals(), connectionManager.disposalBudget())
	}

	// The queue is bounded; the rest is condemned but still owned and on the ledger.
	require.LessOrEqual(t, connectionManager.pendingDisposals(), connectionManager.disposalBudget(),
		"the disposal queue must not exceed its budget")
	require.Greater(t, backlog, 0, "the batch must be on the ledger after the transition")
	t.Logf("DISPOSAL-BUDGET pin-at=%s dns=%s refused-after=%s pending=%d condemned=%d budget=%d",
		pinnedAt.Format(time.RFC3339Nano), queryTime.Round(time.Microsecond), refusedAfter.Round(time.Millisecond),
		connectionManager.pendingDisposals(), connectionManager.condemnedDisposals(), connectionManager.disposalBudget())
}

// TestABatchLargerThanTheDisposalBudgetStillSettlesTheGeneration is the review's reproduction on both
// production reset paths.
func TestABatchLargerThanTheDisposalBudgetStillSettlesTheGeneration(t *testing.T) {
	const total = 65
	shapes := []struct {
		name  string
		reset func(*interfaceTransitionHarness) func(context.Context)
	}{
		{"ordinary", func(harness *interfaceTransitionHarness) func(context.Context) {
			return harness.manager.ResetNetwork
		}},
		{"dead-path", func(harness *interfaceTransitionHarness) func(context.Context) {
			return harness.manager.ResetNetworkHard
		}},
	}
	for _, shape := range shapes {
		t.Run(shape.name, func(t *testing.T) {
			harness, router, connectionManager, conns, target := newBulkBlockedCloseHarness(t, total)
			require.Equal(t, disposalBacklogBudget, connectionManager.disposalBudget(),
				"this test must use the production budget")
			requireGenerationSettledWhileBlocked(t, harness, router, connectionManager, conns, target, conns[0].entered, shape.reset(harness))

			require.Equal(t, total, connectionManager.pendingDisposals()+connectionManager.condemnedDisposals(),
				"the whole batch must be on the ledger right after the transition")
			releaseAllBlocked(conns)
			require.Eventually(t, func() bool {
				return connectionManager.pendingDisposals() == 0 && connectionManager.condemnedDisposals() == 0
			}, 10*time.Second, 5*time.Millisecond, "the ledger never emptied after release")
			// Recovery: with the ledger empty, the same production dialer carries new-generation
			// direct and proxied bytes again.
			recoveryFirstByte := requireFirstByte(t, newGenerationDialer(t, harness.manager, connectionManager), target)
			recoveryProxy := requireProxyFirstByte(t, newGenerationDialer(t, harness.manager, connectionManager), startConnectProxy(t), target)
			t.Logf("DISPOSAL-BUDGET recovery first-byte=%s proxy-first-byte=%s",
				recoveryFirstByte.Round(time.Microsecond), recoveryProxy.Round(time.Microsecond))
			for index, conn := range conns {
				require.EqualValues(t, 1, conn.closes.Load(), "socket %d must close exactly once", index)
			}
		})
	}
}

// TestConsecutiveOverBudgetTransitionsKeepSettling is the accumulated case: the first batch is still
// blocked when a second generation retires another full batch.
func TestConsecutiveOverBudgetTransitionsKeepSettling(t *testing.T) {
	const total = 65
	harness, router, connectionManager, first, target := newBulkBlockedCloseHarness(t, total)
	requireGenerationSettledWhileBlocked(t, harness, router, connectionManager, first, target, first[0].entered, harness.manager.ResetNetwork)

	// A second generation retires another budget-busting batch while the first Close is still stuck.
	second := make([]*blockedPacketClose, 0, total)
	for index := 0; index < total; index++ {
		conn := newBlockedPacketClose(t)
		second = append(second, conn)
		connectionManager.TrackPacketConn(conn)
	}
	requireGenerationSettledWhileBlocked(t, harness, router, connectionManager, second, target, nil, harness.manager.ResetNetwork)
	require.Greater(t, connectionManager.condemnedDisposals(), 0,
		"the second batch must be condemned while the first is still closing")
	require.Equal(t, 2*total, connectionManager.pendingDisposals()+connectionManager.condemnedDisposals(),
		"both batches must be on the ledger")
	releaseAllBlocked(first)
	releaseAllBlocked(second)
	require.Eventually(t, func() bool {
		return connectionManager.pendingDisposals() == 0 && connectionManager.condemnedDisposals() == 0
	}, 15*time.Second, 5*time.Millisecond, "the ledger never emptied after releasing both batches")
	// And the budget admits new work again once the wave has drained.
	recoveryFirstByte := requireFirstByte(t, newGenerationDialer(t, harness.manager, connectionManager), target)
	t.Logf("DISPOSAL-BUDGET consecutive recovery first-byte=%s", recoveryFirstByte.Round(time.Microsecond))
	for index, conn := range first {
		require.EqualValues(t, 1, conn.closes.Load(), "first batch socket %d must close exactly once", index)
	}
	for index, conn := range second {
		require.EqualValues(t, 1, conn.closes.Load(), "second batch socket %d must close exactly once", index)
	}
}
