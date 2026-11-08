//go:build with_quic

package v2rayquic

import (
	"context"
	"net"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// V02A — the shared offer under interleavings a timing-based test cannot pin:
// Reset landing while a creation is in flight, a late success arriving after it,
// and the generation Reset must let through.
//
// The existing shared-offer tests cover the happy orderings (two waiters on one
// dial, one waiter leaving, a late success with no owner). What they do not pin is
// the network-change sequence: the hook runs Reset while a dial is in flight, and
// the next demand must start a NEW creation instead of joining the one Reset has
// already doomed.

// waitForDialCalls waits until the constructor has been entered `want` times.
func waitForDialCalls(t *testing.T, dial *fakeDial, want int) {
	t.Helper()
	require.Eventually(t, func() bool {
		return dial.callCount() == want
	}, 2*time.Second, time.Millisecond, "saw %d dials, want %d", dial.callCount(), want)
}

// pendingOffer reads the client's pending slot under its lock.
func pendingOffer(client *Client) *clientOffer {
	client.connAccess.Lock()
	defer client.connAccess.Unlock()
	return client.pending
}

// TestResetLetsANewGenerationDialWhileTheDiscardedOfferIsInFlight is the
// network-change sequence: Reset runs while a creation is in flight, and the next
// demand must dial again immediately.
//
// The failure it pins: Reset marked the in-flight offer discarded but left it in
// the pending slot, so the next demand attached to a dial that could only ever
// publish net.ErrClosed. For a transport whose InterfaceUpdated runs Reset on
// every physical network change, that is the first dial after the change failing
// — the exact "cannot dial again" failure Reset exists to prevent.
func TestResetLetsANewGenerationDialWhileTheDiscardedOfferIsInFlight(t *testing.T) {
	baseline := runtime.NumGoroutine()
	dial := newFakeDial()
	dial.ignoreCtx = true // both creations stay held until this test releases them
	staleConn, staleCloses := fakeConnection()
	nextConn, nextCloses := fakeConnection()
	dial.results = []offerResult{{conn: staleConn}, {conn: nextConn}}
	client := newTestClient(t, dial)

	staleWaiter := startOffer(client, context.Background())
	<-dial.started
	require.NoError(t, client.Reset())

	// The demand AFTER the reset: it must start a second creation while the first
	// one is still held.
	nextWaiter := startOffer(client, context.Background())
	waitForDialCalls(t, dial, 2)

	// Both creations complete now, the stale one late.
	close(dial.release)

	nextResult := <-nextWaiter
	require.NoError(t, nextResult.err, "the generation after Reset must be able to establish a connection")
	require.Same(t, nextConn, nextResult.conn)

	staleResult := <-staleWaiter
	require.ErrorIs(t, staleResult.err, net.ErrClosed,
		"the waiters of the discarded creation must learn it was discarded, not receive the connection")

	require.Eventually(t, func() bool { return staleCloses.Load() == 1 }, 2*time.Second, time.Millisecond,
		"the discarded late success must be closed exactly once")
	require.Zero(t, nextCloses.Load(), "the new generation must stay open for reuse")
	require.Same(t, nextConn, client.conn.Load(), "the discarded late success must not overwrite the new generation")
	require.Eventually(t, func() bool { return pendingOffer(client) == nil }, 2*time.Second, time.Millisecond,
		"the pending slot must be empty once every creation completed")
	requireNoExtraGoroutines(t, baseline)
}

// TestClosedClientCannotBeRevivedByALateSuccess pins the final-close ordering from
// the other side: Close discards the in-flight creation, the late success is closed
// exactly once instead of being published, and a demand afterwards still cannot
// produce a connection.
func TestClosedClientCannotBeRevivedByALateSuccess(t *testing.T) {
	baseline := runtime.NumGoroutine()
	dial := newFakeDial()
	dial.ignoreCtx = true
	lateConn, lateCloses := fakeConnection()
	// The first creation is held; the demand after Close is answered by a
	// constructor that fails, which is what a real dial does on a cancelled client
	// context. Giving each call its own answer is also what keeps this test
	// deterministic: the fake's release gate is closed by then, so a single shared
	// answer would let that select race its own context.
	dial.results = []offerResult{{conn: lateConn}, {err: context.Canceled}}
	client := newTestClient(t, dial)

	lateWaiter := startOffer(client, context.Background())
	<-dial.started
	require.NoError(t, client.Close())
	close(dial.release)

	require.ErrorIs(t, (<-lateWaiter).err, net.ErrClosed,
		"a waiter still attached when the client closed must learn the creation was discarded")
	require.Eventually(t, func() bool { return lateCloses.Load() == 1 }, 2*time.Second, time.Millisecond,
		"the late connection must be closed exactly once")
	require.Nil(t, client.conn.Load(), "a discarded late success must not be published")

	// A demand after Close cannot revive anything: its own creation fails and
	// nothing is published or closed twice.
	_, err := client.offer(context.Background())
	require.ErrorIs(t, err, context.Canceled, "a closed client must fail a later demand instead of producing a connection")
	require.Nil(t, client.conn.Load())
	require.Equal(t, int32(1), lateCloses.Load(),
		"a demand after Close must not touch the already-discarded connection")
	require.Equal(t, 2, dial.callCount(), "the failed demand must have run its own creation, not joined the discarded one")
	require.Eventually(t, func() bool { return pendingOffer(client) == nil }, 2*time.Second, time.Millisecond)
	requireNoExtraGoroutines(t, baseline)
}

// TestTheSharedOfferDialsWithoutHoldingTheClientLock pins the concurrency shape
// the whole design depends on: the constructor runs outside connAccess, so other
// demands can attach to the pending offer while the dial is in progress. A dial
// that ran under the lock would serialise every waiter behind the handshake.
func TestTheSharedOfferDialsWithoutHoldingTheClientLock(t *testing.T) {
	dial := newFakeDial()
	conn, _ := fakeConnection()
	dial.setResult(conn, nil)
	client := newTestClient(t, dial)

	probed := make(chan struct{})
	var locked bool
	dial.probe = func() {
		if client.connAccess.TryLock() {
			client.connAccess.Unlock()
		} else {
			locked = true
		}
		close(probed)
	}

	waiter := startOffer(client, context.Background())
	<-dial.started
	select {
	case <-probed:
	case <-time.After(2 * time.Second):
		t.Fatal("the dial never reached its probe")
	}
	require.False(t, locked, "the shared creation must not run while holding connAccess")

	// A second demand must be able to attach while the first dial is still held.
	attached := startOffer(client, context.Background())
	close(dial.release)
	require.NoError(t, (<-waiter).err)
	require.NoError(t, (<-attached).err)
	require.Equal(t, 1, dial.callCount(), "the second demand must attach to the in-flight creation, not dial")
}

// TestALateSuccessIsDiscardedOnlyBecauseTheOwnerMarksIt is the negative control
// for the late-success assertions above.
//
// It performs the same Reset-while-in-flight sequence and then clears the discard
// mark Reset just made — the state a Reset that forgot to mark its offer would
// leave. With the mark gone, the same late success is published and never closed,
// so the main test's two observations (not published / closed exactly once) are
// what distinguish a discarded late success from a resurrected one; they cannot
// pass vacuously.
func TestALateSuccessIsDiscardedOnlyBecauseTheOwnerMarksIt(t *testing.T) {
	dial := newFakeDial()
	dial.ignoreCtx = true
	staleConn, staleCloses := fakeConnection()
	dial.setResult(staleConn, nil)
	client := newTestClient(t, dial)

	waiter := startOffer(client, context.Background())
	<-dial.started
	created := pendingOffer(client)
	require.NotNil(t, created, "the offer must be in flight")
	require.NoError(t, client.Reset())

	client.connAccess.Lock()
	created.discarded = false
	client.connAccess.Unlock()

	close(dial.release)
	result := <-waiter
	require.NoError(t, result.err, "without the discard mark the late success is a normal success")
	require.Same(t, staleConn, result.conn)
	require.Eventually(t, func() bool { return client.conn.Load() == staleConn }, 2*time.Second, time.Millisecond,
		"without the discard mark the late success IS published")
	require.Zero(t, staleCloses.Load(), "without the discard mark the late success is NOT closed")
}

// requireNoExtraGoroutines waits for the goroutines this test started to exit: a
// creation goroutine or a waiter left behind would show up here.
func requireNoExtraGoroutines(t *testing.T, baseline int) {
	t.Helper()
	require.Eventually(t, func() bool {
		runtime.GC()
		return runtime.NumGoroutine() <= baseline+2
	}, 2*time.Second, 10*time.Millisecond,
		"goroutines: have %d, baseline %d — a creation or waiter leaked", runtime.NumGoroutine(), baseline)
}
