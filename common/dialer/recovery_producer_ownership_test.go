package dialer

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/netcap"
	C "github.com/sagernet/sing-box/constant"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// The recovery producer must be stopped AND JOINED by the code that started it, not released by the
// caller's discipline.
//
// # The gap this pins
//
// `feedRecoveryAsLateCandidates` publishes into a channel of capacity one. The scheduler reads from it
// while it races and stops reading the moment it has a winner, so the first recovered address always
// fits and a second one parks in the send's select until something cancels the context.
//
// Nothing in the producer used to cancel anything when the literal connected, and the file said it did
// ("a connection the literal established cancels it"). In production that claim happened to hold because
// `route.ConnectionManager` cancels `setupCtx` one statement after the dial returns, so the goroutine's
// extra lifetime was microseconds and the leak was invisible. The guarantee was not kept by the code that
// made it, which is how the next caller - one holding a flow-lifetime context, as a long-lived connection
// legitimately does - inherits a parked goroutine per connection.
//
// # Why the assertion is on `done` rather than on the channel
//
// A cancelled feed ends the producer either way, so "it eventually stopped" cannot tell the two
// implementations apart. What differs is whether `stop()` returns with the goroutine still inside its
// loop. The candidate channel cannot answer that: it is buffered, so reading it says nothing about
// whether it was closed, and a close is not ordered with the goroutine leaving its frame. `done` is
// closed by the producer as its last act, so `done` closed when `stop()` returns is exactly the join.

// wechatFlowLongLived is wechatFlow under a name that says why it is used here: these tests hand in a
// context that outlives the dial on purpose.
func wechatFlowLongLived(original netip.Addr, port uint16, domain string) context.Context {
	return adapter.WithContext(context.Background(), &adapter.InboundContext{
		Destination: M.SocksaddrFrom(original, port),
		Domain:      domain,
	})
}

// parkRecoveryFeed starts a producer, lets it run until it is definitely parked on its second send, and
// returns it.
//
// The two-family answer gives it two candidates, neither of them the literal, so exactly one fits in the
// buffer and the next one parks. `router.exited` proves the synchronous lookup returned; the settle after
// it covers the send loop, which is two channel operations of already-runnable work.
func parkRecoveryFeed(t *testing.T, ctx context.Context, original netip.Addr) (*parallelProductionChain, recoveryFeed) {
	t.Helper()
	chain := newParallelProductionChain(t, netcap.StateProvisioned, netcap.StateUnavailable, C.DomainStrategyAsIS)
	chain.router.addressesA = []netip.Addr{
		netip.MustParseAddr("112.53.55.92"),
		netip.MustParseAddr("112.53.55.93"),
	}
	chain.router.addressesAAAA = []netip.Addr{original}

	feed := chain.resolver.feedRecoveryAsLateCandidates(ctx, M.SocksaddrFrom(original, 80), chain.resolver.captureAdmissionPolicy())
	return chain, feed
}

// TestRecoveryProducerStopsOnRequest is the baseline: the stop does terminate the producer, and the
// caller's live context cannot be credited for it.
func TestRecoveryProducerStopsOnRequest(t *testing.T) {
	originalIPv6 := netip.MustParseAddr("2409:8c54:871:1007::2c")
	callerCtx, cancelCaller := context.WithCancel(wechatFlowLongLived(originalIPv6, 80, "szminorshort.weixin.qq.com"))
	defer cancelCaller()

	chain, feed := parkRecoveryFeed(t, callerCtx, originalIPv6)
	require.Eventually(t, func() bool {
		select {
		case <-chain.router.exited:
			return true
		default:
			return false
		}
	}, 3*time.Second, 5*time.Millisecond, "the recovery lookup must run for this test to mean anything")

	select {
	case <-feed.done:
		t.Fatal("the producer finished before the stop, so this test would not exercise termination")
	default:
	}

	feed.stop()

	select {
	case <-feed.done:
	case <-time.After(3 * time.Second):
		t.Fatal("the recovery producer did not stop when its owner asked it to: a caller holding a " +
			"long-lived context would inherit a goroutine parked on a channel nobody reads")
	}
	require.NoError(t, callerCtx.Err(),
		"the caller's context was never cancelled, so nothing outside the producer could have released it")
	require.Zero(t, chain.activeProducers(), "and no lookup goroutine may still be inside the router")
}

// TestRecoveryProducerIsJoined is the decisive one: `done` must already be closed when `stop` returns.
//
// A stop that only cancels leaves the producer to be scheduled later, so `done` is still open on return.
// Measuring it across iterations is what makes that a fact rather than a timing coincidence - the producer
// is parked in a select, and waking it costs at least one scheduler hand-off.
func TestRecoveryProducerIsJoined(t *testing.T) {
	originalIPv6 := netip.MustParseAddr("2409:8c54:871:1007::2c")

	const attempts = 10
	for i := 0; i < attempts; i++ {
		// A fresh chain each iteration: the fixture's completion signals are one-shot, and reusing one
		// would compare a lookup that never ran.
		callerCtx, cancelCaller := context.WithCancel(wechatFlowLongLived(originalIPv6, 80, "szminorshort.weixin.qq.com"))
		chain, feed := parkRecoveryFeed(t, callerCtx, originalIPv6)
		<-chain.router.exited
		time.Sleep(5 * time.Millisecond) // the send loop: one buffered, one parked

		feed.stop()

		joined := true
		select {
		case <-feed.done:
		default:
			joined = false
		}
		require.True(t, joined,
			"iteration %d: the producer had not finished when stop returned, so the stop cancelled the "+
				"feed without waiting for it", i)
		cancelCaller()
	}

	// And once, directly: the caller's live context proves nothing outside the producer released it.
	callerCtx, cancelCaller := context.WithCancel(wechatFlowLongLived(originalIPv6, 80, "szminorshort.weixin.qq.com"))
	defer cancelCaller()
	chain, feed := parkRecoveryFeed(t, callerCtx, originalIPv6)
	<-chain.router.exited
	time.Sleep(5 * time.Millisecond)
	feed.stop()
	select {
	case <-feed.done:
	case <-time.After(3 * time.Second):
		t.Fatal("joined stop returned with the producer still running")
	}
	require.NoError(t, callerCtx.Err(),
		"the caller's context is still alive, so the producer's exit cannot be attributed to it")
}

// TestDialDoesNotGainAProducerOnTheNoRecoveryPath is the half of the ownership contract that IS
// deterministic through the dial: no recovery evidence, no producer.
//
// # Why this is the assertion the dial can support
//
// The join itself cannot be observed through `DialParallelNetwork`, and pretending otherwise is how the
// first version of this test became flaky: the literal wins in microseconds, so the dial can return before
// the recovery goroutine has even been scheduled. `activeProducers() == 0` then means "never started" as
// easily as "joined", which is not a property worth asserting.
//
// `TestRecoveryProducerIsJoined` carries the join, decisively and by mutation, because it drives `stop`
// directly and can therefore guarantee the producer is parked first. What the dial CAN be held to here is
// that it starts nothing at all when there is nothing to recover - the guarantee that keeps a literal with
// no sniffed domain on exactly its previous path, with no goroutine, no channel and no timer.
func TestDialDoesNotGainAProducerOnTheNoRecoveryPath(t *testing.T) {
	chain := newParallelProductionChain(t, netcap.StateProvisioned, netcap.StateProvisioned, C.DomainStrategyAsIS)
	originalIPv4 := netip.MustParseAddr("112.53.55.92")

	// An answer IS available, so a path that looked the name up without a name to look up would use it.
	chain.router.addressesA = []netip.Addr{netip.MustParseAddr("112.53.55.93")}
	chain.router.addressesAAAA = nil

	callerCtx, cancelCaller := context.WithCancel(wechatFlowLongLived(originalIPv4, 80, "" /* no sniffed domain */))
	defer cancelCaller()

	conn, err := chain.resolver.DialParallelNetwork(callerCtx, "tcp",
		M.SocksaddrFrom(originalIPv4, 80), nil, nil, nil, nil, 0)
	require.NoError(t, err)
	require.NotNil(t, conn)
	conn.Close()

	require.Zero(t, chain.lookupCalls(),
		"no domain evidence means no lookup, and therefore no producer to join")
	require.Zero(t, chain.activeProducers(),
		"and nothing may be inside the router when the dial returns")
	require.NoError(t, callerCtx.Err(),
		"the caller's context is still alive, so nothing here can be attributed to its cancellation")
}
