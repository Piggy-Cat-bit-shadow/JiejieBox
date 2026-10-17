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

// §13 of the transition brief, rebuilt as a REAL interleaving after the first version was found to be
// weaker than its own name.
//
// # What was wrong with the first version, precisely
//
// It installed the resolver gate and then called `close(releaseGate)` BEFORE starting the old flow. The
// gate therefore never blocked anything: the old flow ran to completion against an OPEN resolver, the
// generation advance afterwards was a fake-manager index change, and "release the old answer after the
// transition" never happened. The assertions it did make were true, but they were true of a sequence that
// did not contain the interleaving its name claimed.
//
// # What is now forced, and by what barrier
//
// The resolver gate (`stallLookup` / `releaseLookup`) plus `gateEngaged` make "the old lookup is parked
// inside the router" a state the test WAITS for rather than hopes for. `activeLookups` and `exited` make
// "the old producer has finished" observable rather than assumed. No sleep decides any of it.
//
// # The two scenarios, and why they are different
//
//	released while the old flow is still alive   the answer arrives into the OLD flow's scheduler, which
//	                                             was built from the OLD capture. Invariant B says a
//	                                             superseded capture refuses nothing, so the old flow MAY
//	                                             dial that answer - and it must not reach any other flow.
//
//	released after the setup was cancelled       the producer is already gone (its `select` returns on
//	                                             `ctx.Done()`), so the answer is delivered to nobody. What
//	                                             can be asserted is the join: `activeLookups` back to zero.
//
// # What remains structurally impossible rather than tested
//
// An answer cannot enter ANOTHER flow's scheduler at all: every flow creates its own scheduler from its own
// capture, and the answer is published on a channel owned by the flow that started the lookup. So the
// leakage the brief fears is prevented by the shape of the graph, not by a filter - and the honest way to
// say that is to assert the consequence (a new flow's attempts come only from its own lookup) and to name
// the reason instead of implying a filter was tested.
func TestAnAnswerResolvedForTheOldGenerationCannotReachANewFlowsScheduler(t *testing.T) {
	ipv4 := netip.MustParseAddr("112.53.55.92")
	ipv6 := netip.MustParseAddr("2409:8c54:871:1007::2c")

	// newWorld builds a real leaf over a phase manager, and returns the pieces the barriers need.
	newWorld := func(t *testing.T) (*resolveDialer, *scriptedLeaf, *strategyCountingRouter, *scriptedObservationManager) {
		t.Helper()
		manager := &scriptedObservationManager{
			verdicts: []adapter.NetworkFamilyCapability{
				capWith(netcap.StateProvisioned, netcap.StateUnavailable), // generation N: IPv6 unavailable
				capWith(netcap.StateProvisioned, netcap.StateProvisioned), // generation N+1: dual stack
			},
			epoch:  40,
			stable: true,
		}
		owner := &DefaultDialer{familyHealth: newFamilyHealth()}
		owner.network = newNetworkObserver(manager)
		inner := newStallingDialer()
		inner.packetConn = &countingPacketConn{}
		leaf := &scriptedLeaf{owner: owner, inner: inner}
		router := &strategyCountingRouter{scriptedFamilyRouter: newScriptedFamilyRouter()}
		router.strategy.Store(uint32(C.DomainStrategyPreferIPv6))
		router.addressesA = []netip.Addr{ipv4}
		router.addressesAAAA = []netip.Addr{ipv6}
		resolver := &resolveDialer{router: router, dialer: leaf, parallel: true, fallbackDelay: time.Millisecond}
		return resolver, leaf, router, manager
	}

	// advance moves the manager to generation N+1 without a setup cancellation. It is the capability and
	// epoch change only; the cancellation is each sub-test's business, because that is the variable.
	advance := func(manager *scriptedObservationManager) {
		manager.access.Lock()
		manager.epoch = 41
		manager.reads = 1
		manager.access.Unlock()
	}

	t.Run("an answer released into a live old flow does not reach the next generation", func(t *testing.T) {
		resolver, leaf, router, manager := newWorld(t)
		router.stallLookup(t)

		oldCtx, cancelOld := context.WithCancel(context.Background())
		defer cancelOld()

		oldDone := make(chan error, 1)
		go func() {
			conn, err := resolver.DialContext(oldCtx, "tcp", M.Socksaddr{Fqdn: "leak.example", Port: 443})
			if conn != nil {
				conn.Close()
			}
			oldDone <- err
		}()

		// BARRIER: the old lookup is provably parked inside the router, holding this generation's answer.
		select {
		case <-router.gateEngaged:
		case <-time.After(5 * time.Second):
			t.Fatal("the old lookup never reached the resolver gate, so no interleaving was forced")
		}
		require.EqualValues(t, 1, router.activeLookups.Load(),
			"exactly one lookup must be in flight for this test to be about a blocked answer")

		// The generation advances WHILE the old flow is still alive and blocked. Nothing cancels it.
		advance(manager)

		// Now release the OLD answer, after the transition.
		router.releaseLookup(t)

		select {
		case err := <-oldDone:
			// The old flow is ALLOWED to succeed: its capture is superseded, so it refuses nothing, and
			// dialling the newly-arrived family is the documented fail-open. What matters is that it
			// decided for itself.
			_ = err
		case <-time.After(5 * time.Second):
			t.Fatal("the old flow did not return after its answer was released")
		}
		require.Eventually(t, func() bool { return router.activeLookups.Load() == 0 }, 5*time.Second, time.Millisecond,
			"the old producer did not exit after delivering its answer")

		// A NEW flow, at generation N+1. Its attempts must come from its own lookup and its own capture.
		leaf.inner.access.Lock()
		leaf.inner.tried = nil
		leaf.inner.access.Unlock()

		newConn, err := resolver.DialContext(context.Background(), "tcp", M.Socksaddr{Fqdn: "leak.example", Port: 443})
		require.NoError(t, err)
		if newConn != nil {
			newConn.Close()
		}
		newAttempts := leaf.tried()
		require.NotEmpty(t, newAttempts)
		for _, address := range newAttempts {
			require.True(t, address == ipv4 || address == ipv6,
				"the new flow attempted %v, which is not one of its own answers", address)
		}
		captured := resolver.captureAdmissionPolicy()
		require.EqualValues(t, 41, captured.generation(), "the new flow must capture the advanced generation")
		require.Equal(t, admissionAllowed, captured.verdict(ipv6),
			"the new flow must see generation N+1's capability, not the old one's")
		require.Positive(t, router.calls.Load(), "the new flow must have performed its own lookup")
	})

	t.Run("an answer released after the setup was cancelled reaches nobody, and the producer is joined", func(t *testing.T) {
		resolver, leaf, router, manager := newWorld(t)
		router.stallLookup(t)

		oldCtx, cancelOld := context.WithCancel(context.Background())
		defer cancelOld()

		oldDone := make(chan error, 1)
		go func() {
			conn, err := resolver.DialContext(oldCtx, "tcp", M.Socksaddr{Fqdn: "cancel.example", Port: 443})
			if conn != nil {
				conn.Close()
			}
			oldDone <- err
		}()

		select {
		case <-router.gateEngaged:
		case <-time.After(5 * time.Second):
			t.Fatal("the old lookup never reached the resolver gate")
		}

		// The transition: the generation advances AND the stale setup is cancelled, which is what the route
		// layer's transition does to a setup from the previous generation.
		advance(manager)
		cancelOld()

		select {
		case err := <-oldDone:
			require.Error(t, err, "a cancelled setup must not report success")
			require.ErrorIs(t, err, context.Canceled)
		case <-time.After(5 * time.Second):
			t.Fatal("the cancelled setup did not return")
		}

		// JOIN: the producer is gone, so the answer that is released next has no reader.
		require.Eventually(t, func() bool { return router.activeLookups.Load() == 0 }, 5*time.Second, time.Millisecond,
			"the old producer is still inside the router after the setup was cancelled")

		leaf.inner.access.Lock()
		leaf.inner.tried = nil
		leaf.inner.access.Unlock()

		// Release the old answer AFTER the cancellation. The gate's waiter is gone, so this delivers to
		// nobody - which is the point.
		router.releaseLookup(t)

		require.Empty(t, leaf.tried(),
			"releasing the old answer after the setup was cancelled caused an attempt: the answer reached "+
				"a scheduler whose owner had already given up")
		require.Eventually(t, func() bool { return router.activeLookups.Load() == 0 }, time.Second, time.Millisecond)

		// And a new flow still gets its own generation and its own capability.
		newConn, err := resolver.DialContext(context.Background(), "tcp", M.Socksaddr{Fqdn: "cancel.example", Port: 443})
		require.NoError(t, err)
		if newConn != nil {
			newConn.Close()
		}
		captured := resolver.captureAdmissionPolicy()
		require.EqualValues(t, 41, captured.generation())
		require.Equal(t, admissionAllowed, captured.verdict(ipv6))
	})
}
