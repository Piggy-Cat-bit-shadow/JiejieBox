package v2rayxhttp

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The pool inventory is the R01 answer for the XHTTP XMUX owner: what the pool can prove about its
// own connections at its own state points, with no claim about the HTTP/2 library's internal cache.
//
// # What these tests pin
//
//   - active and idle are POOLED connections, split by whether a stream holds them;
//   - draining is a connection the pool detached while it still carried a stream, and the release
//     confirmation for it only appears when the underlying Close actually runs - never at the retire
//     call;
//   - old-generation draining is distinguishable from connections opened after the boundary;
//   - one connection is counted once: opened == pooled + draining + released, at every step.
//
// The fakes record every underlying Close, so "released" is asserted against the real end point
// rather than against the pool's own bookkeeping.

// requireXmuxInventoryInvariant is the no-double-count / no-missed-count check: every connection the
// pool ever opened is either pooled, draining, or confirmed released.
func requireXmuxInventoryInvariant(t *testing.T, stats xmuxPoolStats) {
	t.Helper()
	require.Equal(t, stats.Opened, uint64(stats.Pooled+stats.Draining)+stats.Released,
		"the pool's live inventory and its release counter must account for every opened connection: %+v", stats)
}

// TestXMUXInventorySeparatesPooledDrainingAndReleased walks one busy and one idle connection through
// the reuse boundary and asserts each state at its own state point.
func TestXMUXInventorySeparatesPooledDrainingAndReleased(t *testing.T) {
	manager, conns := poolOf(t, xmuxConfig{maxConnections: intRange{2, 2}})
	busy, _ := manager.get()
	busy.addOpenUsage(1)
	idle, _ := manager.get()
	require.NotSame(t, busy, idle)

	stats := manager.stats()
	require.Equal(t, uint64(0), stats.Generation)
	require.Equal(t, 2, stats.Pooled)
	require.Equal(t, 1, stats.Active)
	require.Equal(t, 1, stats.Idle)
	require.Zero(t, stats.Draining)
	require.Equal(t, uint64(2), stats.Opened)
	require.Zero(t, stats.Released)
	requireXmuxInventoryInvariant(t, stats)

	manager.RetireSuspect()

	stats = manager.stats()
	require.Equal(t, uint64(1), stats.Generation)
	require.Zero(t, stats.Pooled)
	require.Equal(t, 1, stats.Draining, "the busy connection is detached but not yet released")
	require.Equal(t, 1, stats.DrainingOldGeneration)
	require.Equal(t, uint64(1), stats.Released, "the idle connection's Close ran at the boundary")
	require.Equal(t, 1, conns()[1].closes(), "the idle connection must have been closed for real")
	require.Zero(t, conns()[0].closes(),
		"the busy connection must not be closed by the boundary: its stream still owns the transport")
	requireXmuxInventoryInvariant(t, stats)

	busy.addOpenUsage(-1)

	stats = manager.stats()
	require.Zero(t, stats.Draining, "the last stream's release must remove the draining entry")
	require.Equal(t, uint64(2), stats.Released, "and must confirm the deferred close")
	require.Equal(t, 1, conns()[0].closes(), "the deferred close must run exactly once")
	requireXmuxInventoryInvariant(t, stats)

	// A connection opened after the boundary belongs to the current generation and cannot be counted
	// as old-generation draining.
	fresh, _ := manager.get()
	require.NotNil(t, fresh)
	stats = manager.stats()
	require.Equal(t, uint64(1), stats.Generation, "opening a connection must not advance the generation")
	require.Equal(t, 1, stats.Pooled)
	require.Equal(t, 1, stats.Idle)
	require.Zero(t, stats.DrainingOldGeneration)
	require.Equal(t, uint64(3), stats.Opened)
	requireXmuxInventoryInvariant(t, stats)
}

// TestXMUXTrimConfirmsOnlyTheRealRelease: the trim removes an idle pooled connection, and the
// confirmation appears as a release only once the underlying Close ran. A busy connection is neither
// removed nor released, which is the "active references are not closed early" requirement.
func TestXMUXTrimConfirmsOnlyTheRealRelease(t *testing.T) {
	manager, conns := poolOf(t, xmuxConfig{maxConnections: intRange{2, 2}})
	busy, _ := manager.get()
	busy.addOpenUsage(1)
	idle, _ := manager.get()
	require.NotSame(t, busy, idle)

	manager.CloseIdleConnections()

	stats := manager.stats()
	require.Equal(t, 1, stats.Pooled)
	require.Equal(t, 1, stats.Active)
	require.Zero(t, stats.Idle)
	require.Equal(t, uint64(1), stats.Released)
	require.Equal(t, 1, conns()[1].closes())
	require.Zero(t, conns()[0].closes(), "the trim must not touch a connection carrying a stream")
	requireXmuxInventoryInvariant(t, stats)
}

// TestXMUXBusyEvictionDrainsUntilItsStreamEnds covers the other detach path: an evicted busy
// connection is in exactly the same "not pooled, not released" state as a boundary drain, so it must
// be counted the same way rather than lost from the inventory.
func TestXMUXBusyEvictionDrainsUntilItsStreamEnds(t *testing.T) {
	manager, conns := poolOf(t, xmuxConfig{hMaxRequestTimes: intRange{1, 1}})
	busy, _ := manager.get()
	busy.addOpenUsage(1)
	busy.takeRequest() // the connection has now used its one allowed request

	next, _ := manager.get() // evicts busy, opens a replacement
	require.NotNil(t, next)

	stats := manager.stats()
	require.Equal(t, 1, stats.Pooled)
	require.Equal(t, 1, stats.Idle)
	require.Equal(t, 1, stats.Draining)
	require.Equal(t, uint64(2), stats.Opened)
	require.Zero(t, stats.Released)
	require.Zero(t, conns()[0].closes(), "the evicted connection's stream still owns its transport")
	requireXmuxInventoryInvariant(t, stats)

	busy.addOpenUsage(-1)

	stats = manager.stats()
	require.Zero(t, stats.Draining)
	require.Equal(t, uint64(1), stats.Released)
	require.Equal(t, 1, conns()[0].closes())
	requireXmuxInventoryInvariant(t, stats)
}

// TestXMUXCloseWaitsForEveryBusyStream: Close is the reset-shaped retire, and it defers the teardown
// of busy connections exactly like the boundary does. Pooled drops to zero immediately; the release
// count only reaches Opened after the last stream leaves.
func TestXMUXCloseWaitsForEveryBusyStream(t *testing.T) {
	manager, conns := poolOf(t, xmuxConfig{maxConnections: intRange{2, 2}})
	busy, _ := manager.get()
	busy.addOpenUsage(1)
	manager.get()

	manager.Close()

	stats := manager.stats()
	require.Zero(t, stats.Pooled)
	require.Equal(t, 1, stats.Draining)
	require.Equal(t, uint64(1), stats.Released, "the idle connection is released by Close")
	requireXmuxInventoryInvariant(t, stats)

	busy.addOpenUsage(-1)

	stats = manager.stats()
	require.Zero(t, stats.Draining)
	require.Equal(t, stats.Opened, stats.Released, "every opened connection is now confirmed closed")
	require.Equal(t, 1, conns()[0].closes())
	requireXmuxInventoryInvariant(t, stats)
}
