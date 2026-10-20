package dialer

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/netcap"
	C "github.com/sagernet/sing-box/constant"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// DS-03: the INNER LookupFamilies goroutine, measured rather than inferred.
//
// # What the outer joins do and do not prove
//
// `feedRecoveryAsLateCandidates` joins its producer (`feed.done`), `resolve.go` joins its producers
// and literal workers, and `dialLiteralWithRecovery` joins the original attempt. None of those is a
// join of the goroutine `recoverCandidates` starts to drive `DNSDualStackRouter.LookupFamilies`:
// that goroutine sits one level below all of them, and `recoverCandidates` returns on the first
// admissible family or on cancellation without waiting for it.
//
// The tests below separate the two facts a reader needs:
//
//   - a caller does NOT wait for an inner lookup that has stopped cooperating (the plan arm and the
//     non-cooperative test), which is a property: waiting would hand a successful connection's
//     lifetime to a resolver that has not answered;
//   - a cooperative inner lookup does exit, and the cancellation the function performs is what
//     releases its send, so no goroutine, channel or socket outlives a resolver that returns.
//
// The originals are IPv4 on purpose. An IPv6 original on a machine without public IPv6 egress is
// refused by the local gate before any attempt, and the literal path then waits for RECOVERY by
// design (that is its fallback), which would make a test about the inner producer measure the
// fallback wait instead.

// TestLiteralRecoveryDoesNotWaitForTheLookupToFinish is the literal chain:
//
//	dialLiteralWithRecovery → recoverCandidates → LookupFamilies goroutine
//
// The resolver is held inside LookupFamilies for the whole test and is never released by the gate.
// The dial must still return: it cancels the recovery context on the way out, and that cancellation
// - not the resolver answering - is what releases the inner goroutine's send and lets it exit.
//
// If the literal path waited for the lookup to FINISH, this test would block until the test's own
// cleanup opened the gate, which is exactly the failure it is written to catch.
func TestLiteralRecoveryDoesNotWaitForTheLookupToFinish(t *testing.T) {
	original := netip.MustParseAddr("112.53.55.92")
	chain := newParallelProductionChain(t, netcap.StateProvisioned, netcap.StateProvisioned, C.DomainStrategyAsIS)
	chain.router.addressesA = []netip.Addr{netip.MustParseAddr("112.53.55.93")}
	chain.router.addressesAAAA = []netip.Addr{netip.MustParseAddr("2409:8c54:871:1007::2c")}

	ctx := wechatFlowLongLived(original, 80, "szminorshort.weixin.qq.com")
	admission := chain.resolver.captureAdmissionPolicy()

	chain.router.stallLookup(t)
	t.Cleanup(func() { chain.router.releaseLookup(t) })
	chain.inner.connectDelay = 100 * time.Millisecond

	type dialResult struct {
		conn net.Conn
		err  error
	}
	done := make(chan dialResult, 1)
	go func() {
		conn, err := chain.resolver.dialLiteralWithRecovery(ctx, "tcp", M.SocksaddrFrom(original, 80), admission)
		done <- dialResult{conn: conn, err: err}
	}()

	var result dialResult
	select {
	case result = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the literal dial waited for the recovery lookup to finish; the gate was never released")
	}
	require.NoError(t, result.err)
	require.NotNil(t, result.conn)
	t.Cleanup(func() { _ = result.conn.Close() })

	// The gate is still closed: nothing the resolver did released the lookup. If the inner
	// goroutine has left, the cancellation is what released it.
	require.True(t, gateIsArmed(chain.router.gate),
		"the test must not have released the gate; otherwise the exit could be the resolver's doing")
	require.Eventually(t, func() bool { return chain.router.activeLookups.Load() == 0 }, 5*time.Second, time.Millisecond,
		"the cancellation performed on return must release the inner lookup, even though the "+
			"resolver was still parked")
	select {
	case <-chain.router.exited:
	default:
		t.Fatal("the resolver returned but its exit signal was not observed")
	}
}

// TestParallelNetworkRecoveryInnerLookupExitsWithTheFeed is the parallel chain:
//
//	DialParallelNetwork → feedRecoveryAsLateCandidates → recoverCandidates → LookupFamilies goroutine
//
// A cooperative resolver exits when the feed is stopped, because the cancellation the recovery path
// performs reaches the inner lookup as well. This is the exit half; the non-cooperative test below is
// the half that shows the join is not there.
func TestParallelNetworkRecoveryInnerLookupExitsWithTheFeed(t *testing.T) {
	original := netip.MustParseAddr("112.53.55.92")
	chain := newParallelProductionChain(t, netcap.StateProvisioned, netcap.StateProvisioned, C.DomainStrategyAsIS)
	chain.router.addressesA = []netip.Addr{netip.MustParseAddr("112.53.55.93")}
	chain.router.addressesAAAA = []netip.Addr{netip.MustParseAddr("2409:8c54:871:1007::2c")}

	ctx := wechatFlowLongLived(original, 80, "szminorshort.weixin.qq.com")
	chain.router.stallLookup(t)
	t.Cleanup(func() { chain.router.releaseLookup(t) })

	feed := chain.resolver.feedRecoveryAsLateCandidates(ctx, M.SocksaddrFrom(original, 80), chain.resolver.captureAdmissionPolicy())
	t.Cleanup(feed.stop)

	require.Eventually(t, func() bool { return chain.router.activeLookups.Load() == 1 }, 5*time.Second, time.Millisecond,
		"the recovery lookup never started")

	feed.stop()
	select {
	case <-feed.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the outer producer was not joined by stop()")
	}
	require.Eventually(t, func() bool { return chain.router.activeLookups.Load() == 0 }, 5*time.Second, time.Millisecond,
		"a cooperative resolver must leave as soon as the recovery context is cancelled")
}

// TestSlowCooperatingResolverStillFallsBack covers the scheduling property the recovery path exists
// for: a fast family that the machine refuses must not end the lookup when the slow family is
// usable.
func TestSlowCooperatingResolverStillFallsBack(t *testing.T) {
	original := netip.MustParseAddr("112.53.55.92")
	chain := newParallelProductionChain(t, netcap.StateProvisioned, netcap.StateUnavailable, C.DomainStrategyAsIS)
	usableIPv4 := netip.MustParseAddr("112.53.55.94")
	chain.router.addressesA = []netip.Addr{usableIPv4}
	chain.router.addressesAAAA = []netip.Addr{netip.MustParseAddr("2001:4860:4860::8888")}
	// The refused family answers first; the usable one is slow but cooperative.
	chain.router.delayAAAA = 0
	chain.router.delayA = 50 * time.Millisecond

	ctx := wechatFlowLongLived(original, 80, "szminorshort.weixin.qq.com")
	start := time.Now()
	recovered := chain.resolver.recoverCandidates(ctx, M.SocksaddrFrom(original, 80), chain.resolver.captureAdmissionPolicy())
	elapsed := time.Since(start)

	require.Equal(t, []netip.Addr{usableIPv4}, recovered,
		"the refused fast family must not end the lookup: the admissible slow one is the answer")
	require.GreaterOrEqual(t, elapsed, 50*time.Millisecond, "the answer is the slow family's")
	require.Less(t, elapsed, 3*time.Second, "and the lookup must not wait for anything else")
	require.Eventually(t, func() bool { return chain.router.activeLookups.Load() == 0 }, 5*time.Second, time.Millisecond)
}

// ignoringCancelRouter answers nothing and ignores cancellation until the test releases it.
//
// This is the contract boundary: an implementation that violates the context contract cannot be
// force-stopped by this code, and the test below establishes what that does and does not cost the
// caller. It is a test double, not a claim about any production resolver.
type ignoringCancelRouter struct {
	*scriptedFamilyRouter
	release   chan struct{}
	entered   chan struct{}
	enterOnce sync.Once
}

func newIgnoringCancelRouter() *ignoringCancelRouter {
	return &ignoringCancelRouter{
		scriptedFamilyRouter: newScriptedFamilyRouter(),
		release:              make(chan struct{}),
		entered:              make(chan struct{}),
	}
}

func (r *ignoringCancelRouter) LookupFamilies(ctx context.Context, domain string, options adapter.DNSQueryOptions, publish func(adapter.DNSFamilyResult)) error {
	r.activeLookups.Add(1)
	defer r.activeLookups.Add(-1)
	r.enterOnce.Do(func() { close(r.entered) })
	<-r.release
	return ctx.Err()
}

// TestResolverThatIgnoresCancellationStatesTheBoundary proves what the code can and cannot promise
// for a resolver that violates the context contract: the literal dial still returns, but `stop()`
// cannot join a producer that is parked inside LookupFamilies, so it waits until the resolver
// returns. That is a boundary of the interface, not something this code can force, and the test
// releases the resolver before it ends so no goroutine is left behind.
func TestResolverThatIgnoresCancellationStatesTheBoundary(t *testing.T) {
	original := netip.MustParseAddr("112.53.55.92")
	chain := newParallelProductionChain(t, netcap.StateProvisioned, netcap.StateProvisioned, C.DomainStrategyAsIS)
	ignoring := newIgnoringCancelRouter()
	chain.resolver.router = ignoring
	released := false
	release := func() {
		if !released {
			released = true
			close(ignoring.release)
		}
	}
	t.Cleanup(release)

	ctx := wechatFlowLongLived(original, 80, "szminorshort.weixin.qq.com")
	admission := chain.resolver.captureAdmissionPolicy()
	chain.inner.connectDelay = 100 * time.Millisecond

	// Literal chain: the literal connects and the function returns while the resolver is still
	// inside. That half does not depend on the resolver's cooperation.
	dialDone := make(chan error, 1)
	go func() {
		conn, err := chain.resolver.dialLiteralWithRecovery(ctx, "tcp", M.SocksaddrFrom(original, 80), admission)
		if conn != nil {
			_ = conn.Close()
		}
		dialDone <- err
	}()
	select {
	case err := <-dialDone:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the literal dial waited for a resolver that ignores cancellation")
	}
	select {
	case <-ignoring.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the ignoring resolver was never entered, so this test proves nothing")
	}

	// Parallel chain: stop() joins the OUTER producer and returns, because recoverCandidates
	// returns on cancellation without waiting for the resolver. The inner lookup is therefore
	// still inside after stop() - the boundary stated as a fact, and the reason the caller is not
	// blocked by a resolver that will not return.
	before := ignoring.activeLookups.Load()
	feed := chain.resolver.feedRecoveryAsLateCandidates(ctx, M.SocksaddrFrom(original, 80), admission)
	require.Eventually(t, func() bool { return ignoring.activeLookups.Load() == before+1 }, 5*time.Second, time.Millisecond,
		"the feed lookup never entered the resolver")
	feed.stop()
	select {
	case <-feed.done:
	default:
		t.Fatal("stop() returned but the producer had not reported done")
	}
	require.EqualValues(t, before+1, ignoring.activeLookups.Load(),
		"the outer join is real and the inner lookup is still inside: the two are different producers")

	release()
	require.Eventually(t, func() bool { return ignoring.activeLookups.Load() == 0 }, 5*time.Second, time.Millisecond,
		"once the resolver returns, the inner goroutine must exit")
}
