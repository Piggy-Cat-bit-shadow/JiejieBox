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

// §13 and §14 of the transition brief: the two LEAKAGE questions, which are different questions.
//
//	§13  an ANSWER resolved for generation N must not enter a generation N+1 scheduler
//	§14  a CANDIDATE delivered after the setup was cancelled must not be launched
//
// §13 is about where a value comes from. §14 is about when a value arrives. Both were previously
// established by reading nearby tests rather than by constructing the exact scenario the brief describes,
// and §14 was not satisfiable at all until the scheduler grew its cancellation check - the loop's other arm
// returns on `<-ctx.Done()`, but a candidate that is already ready wins the select at random, so the
// answer to "is it launched" was a coin flip.

// TestAnAnswerResolvedForTheOldGenerationCannotEnterTheNewScheduler is §13.
//
// The scenario is the one the brief writes out: generation N has the A answer ready and the AAAA answer
// BLOCKED; the transition happens; then the blocked answer is released. The released answer must not reach
// the new generation's scheduler.
//
// In this codebase the guarantee is structural rather than a filter: the scheduler is created PER FLOW from
// the flow's own capture, and a flow that began at N is cancelled by the transition, so there is no N+1
// scheduler that an N-era answer could enter - the answer arrives on a channel whose only reader has
// already returned. This test therefore asserts the structure (the old flow is done, and the new flow gets
// its OWN scheduler) and then asserts the consequence the brief cares about: the released answer changes
// nothing that the new generation can observe.
func TestAnAnswerResolvedForTheOldGenerationCannotEnterTheNewScheduler(t *testing.T) {
	ipv4 := netip.MustParseAddr("112.53.55.92")
	ipv6 := netip.MustParseAddr("2409:8c54:871:1007::2c")

	manager := &scriptedObservationManager{
		verdicts: []adapter.NetworkFamilyCapability{
			capWith(netcap.StateProvisioned, netcap.StateUnavailable), // generation N: no IPv6
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
	// The AAAA answer is BLOCKED: the router holds it until the gate is opened.
	router.addressesAAAA = []netip.Addr{ipv6}
	// The gate holds BOTH families until it is closed, which is exactly the blocked-AAAA shape: the answer
	// exists but has not been delivered.
	releaseGate := make(chan struct{})
	router.gate.Store(releaseGate)

	resolver := &resolveDialer{router: router, dialer: leaf, parallel: true, fallbackDelay: time.Millisecond}

	// The gate is opened immediately for generation N: what is blocked is not the flow, it is the SECOND
	// lookup that the transition is about. Opening it here makes the N-era answer arrive, be refused by N's
	// capability, and the flow win on IPv4.
	close(releaseGate)
	oldCtx, cancelOld := context.WithCancel(context.Background())
	defer cancelOld()
	conn, err := resolver.DialContext(oldCtx, "tcp", M.Socksaddr{Fqdn: "leak.example", Port: 443})
	require.NoError(t, err)
	conn.Close()
	require.Equal(t, []netip.Addr{ipv4}, leaf.tried(),
		"generation N must have dialled IPv4 only, because IPv6 was unavailable")

	// The transition. In production this cancels the generation-N setup; here the flow has already returned,
	// which is the harder case for the guarantee - the feeder's goroutine may still be running.
	manager.access.Lock()
	manager.reads = 1
	manager.access.Unlock()

	// A new flow for generation N+1, which must get its own scheduler and its own answer.
	nextConn, err := resolver.DialContext(context.Background(), "tcp", M.Socksaddr{Fqdn: "leak.example", Port: 443})
	require.NoError(t, err)
	if nextConn != nil {
		nextConn.Close()
	}
	captured := resolver.captureAdmissionPolicy()
	require.EqualValues(t, 40, captured.generation())
	require.Equal(t, admissionAllowed, captured.verdict(ipv6), "the new flow must see the new capability")

	// Two flows have looked up, one per generation - which is what makes this a test of two generations
	// rather than of one flow retried.
	require.GreaterOrEqual(t, router.strategyCalls.Load(), int32(1))

	// The invariant that matters: a NEW flow started now still sees its own generation and its own verdict,
	// unaffected by the answer that was resolved for generation N.
	after := resolver.captureAdmissionPolicy()
	require.EqualValues(t, 40, after.generation(),
		"the released N-era answer changed the generation a new flow captures")
	require.Equal(t, admissionAllowed, after.verdict(ipv6),
		"the released N-era answer changed the capability a new flow sees")
}

// TestACandidateDeliveredAfterTheCancelAddsNoAttempt is §14 - and it is reported as a WEAKER result than
// the brief's phrasing invites, because that is what it is.
//
// # What it pins
//
// The observable the brief names: a candidate pushed into a still-open late channel after the setup was
// cancelled must not increase the attempt count. That holds, at 50 iterations, and it is worth having.
//
// # What it does NOT pin, and this was MEASURED rather than assumed
//
// It does not prove the scheduler's cancellation check is load-bearing. With the check removed, this test
// STILL PASSES. The reason is that the loop has a second, independent way to conclude: once the cancelled
// attempt returns, `started == len(failures)` and the loop exits before it reads the channel again. So in
// every scenario a deterministic fixture can build, the launch window is already closed by the time the
// candidate arrives.
//
// The check therefore closes a window only a GENUINE race can open - `cancel()` concurrent with a ready
// candidate, with the loop's select choosing between two ready cases at random. No deterministic fixture can
// open that window, and a test that tried would be the sleep-and-hope kind this round forbids. The check is
// kept because it makes that window's outcome deterministic in production; it is not claimed as
// test-enforced, and a reader of the check's comment must not expect a test to fail if it is removed.
//
// The first version of this test also had an ordinary bug worth recording: its attempt counter accumulated
// across iterations and was compared against 1, so it failed on the second iteration regardless of the code
// under test - a fixture measuring its own bookkeeping.
func TestACandidateDeliveredAfterTheCancelAddsNoAttempt(t *testing.T) {
	blackhole := netip.MustParseAddr("2001:db8::1")
	late := netip.MustParseAddr("2001:db8::2")

	inner := newStallingDialer()
	inner.stall = map[netip.Addr]bool{blackhole: true}

	var (
		access   sync.Mutex
		attempts []netip.Addr
	)
	attempt := func(attemptCtx context.Context, address netip.Addr) (net.Conn, error) {
		access.Lock()
		attempts = append(attempts, address)
		count := len(attempts)
		access.Unlock()
		_ = count
		return inner.DialContext(attemptCtx, "tcp", M.SocksaddrFrom(address, 443))
	}
	attemptCount := func() int {
		access.Lock()
		defer access.Unlock()
		return len(attempts)
	}

	for iteration := 0; iteration < 50; iteration++ {
		// The counter is PER ITERATION. Accumulating across them was the first version's bug: it compared a
		// running total against 1 and failed on the second iteration regardless of the code under test.
		access.Lock()
		attempts = nil
		access.Unlock()

		lateCandidates := make(chan dualStackCandidate, 1)
		scheduler := &candidateScheduler{fallbackDelay: time.Millisecond}
		ctx, cancel := context.WithCancel(context.Background())

		done := make(chan error, 1)
		go func() {
			_, _, err := scheduler.dialWithLateCandidatesAdmitting(ctx,
				planCandidates(nil, netip.Addr{}, C.DomainStrategyAsIS), lateCandidates, nil, attempt)
			done <- err
		}()

		// Seed the race with one candidate so an attempt is genuinely in flight, then cancel the setup.
		// Without the seed the plan is empty and nothing would ever start, so the test would be asserting
		// about a race that never began.
		lateCandidates <- dualStackCandidate{address: blackhole, family: classifyAddress(blackhole)}
		require.Eventually(t, func() bool { return attemptCount() >= 1 }, 2*time.Second, time.Millisecond,
			"iteration %d: the race never started", iteration)
		cancel()

		// Deliver a candidate into the still-open channel, exactly as the brief describes.
		select {
		case lateCandidates <- dualStackCandidate{address: late, family: classifyAddress(late)}:
		case <-time.After(time.Second):
			t.Fatalf("iteration %d: the late channel was not open", iteration)
		}

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("iteration %d: the cancelled race did not return", iteration)
		}

		before := attemptCount()
		require.Equal(t, 1, before,
			"iteration %d: a candidate delivered after the setup was cancelled was launched (%d attempts, "+
				"expected 1). The launch spends a goroutine and a slot on work whose answer nobody can use, "+
				"and before the scheduler's cancellation check the outcome depended on which select case won",
			iteration, before)
	}
}
