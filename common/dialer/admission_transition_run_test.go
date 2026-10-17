package dialer

import (
	"context"
	"net/netip"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/netcap"
	C "github.com/sagernet/sing-box/constant"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// Phase 04: the A→B run, driven phase by phase through a REAL consumer.
//
// # What this file is, and what it is not
//
// The producer is faked here on purpose. Phase 01 proved that the NetworkManager publishes one atomic
// (epoch, settled, verdict) per generation, from a real interface list and a real route table, and its
// suite drives the transition through `route`. What Phase 01 could NOT prove is the other half of the
// join: that a REAL flow — the real capture, the real planner, the real scheduler, the real leaf — reads
// each phase's publication in step, and that a flow which captured before a transition keeps its own
// worldview while a flow that starts after it gets the new one.
//
// So the driver below is a manager whose answer the test advances by PHASE, and everything on the
// consumer side is production code. That is the composition this phase exists to accept.
//
// # Why the phases, rather than a final state
//
// Every static test in the tree sets the final answer and calls a function. The dangerous window is the
// one between the answers, so the run below has a phase for each event the brief names:
//
//	Phase 0  old stable, IPv6 unavailable           (a generation-1 verdict)
//	Phase 1  the transition publishes dual-stack    (generation 2, settled)
//	Phase 2  a flow that captured at phase 0 dials  (old flow must not receive new state)
//	Phase 3  a flow that starts now dials           (new flow must use the new capability)
//	Phase 4  a route flap publishes IPv4-only again (generation 2, SAME epoch - a capability-only change)
//	Phase 5  a flow that starts now dials           (must use the flap's verdict)
//
// Phase 4 is the one worth naming: a capability-only change does not advance the epoch (Phase 01 §D), so
// ownership cannot notice it and the capture is the only thing that keeps a live flow honest. Phase 03
// established that; this run shows it inside a transition sequence rather than in isolation.

// phaseManager is a manager whose published answer the test advances.
type phaseManager struct {
	adapter.NetworkManager

	access   sync.Mutex
	epoch    uint64
	settled  bool
	current  adapter.NetworkFamilyCapability
	reads    int
	observed int
}

func newPhaseManager(epoch uint64, capability adapter.NetworkFamilyCapability) *phaseManager {
	return &phaseManager{epoch: epoch, settled: true, current: capability}
}

// publish advances the publication. settle=false models a generation whose transition has claimed the
// network but not committed - the Settling state a flow must not act on.
func (m *phaseManager) publish(epoch uint64, settled bool, capability adapter.NetworkFamilyCapability) {
	m.access.Lock()
	defer m.access.Unlock()
	m.epoch = epoch
	m.settled = settled
	m.current = capability
}

func (m *phaseManager) NetworkFamilyCapabilitySnapshot() (uint64, bool, adapter.NetworkFamilyCapability) {
	m.access.Lock()
	defer m.access.Unlock()
	m.reads++
	return m.epoch, m.settled, m.current
}

// NetworkTransitionSnapshot is part of the same optional contract; without it the observer falls back to
// "no source" and every assertion below would pass by measuring a flow that was told nothing.
func (m *phaseManager) NetworkTransitionSnapshot() (uint64, bool) {
	m.access.Lock()
	defer m.access.Unlock()
	m.observed++
	return m.epoch, m.settled
}

// transitionRun is one assembled consumer: the real leaf, the real capture, the real scheduler.
type transitionRun struct {
	resolver *resolveDialer
	leaf     *scriptedLeaf
	owner    *DefaultDialer
	manager  *phaseManager
	router   *strategyCountingRouter
}

func newTransitionRun(t *testing.T, manager *phaseManager) *transitionRun {
	t.Helper()
	owner := &DefaultDialer{familyHealth: newFamilyHealth()}
	owner.network = newNetworkObserver(manager)
	inner := newStallingDialer()
	inner.stall = nil
	inner.connectDelay = 0
	inner.packetConn = &countingPacketConn{}
	leaf := &scriptedLeaf{owner: owner, inner: inner}
	router := &strategyCountingRouter{scriptedFamilyRouter: newScriptedFamilyRouter()}
	// The preference is IPv6 for the WHOLE run, and it must be: the effective strategy is memoised per
	// dialer (Phase 03), so flipping it mid-run would silently do nothing - which is exactly what the first
	// version of this fixture did. Keeping it constant makes the capability the only variable, which is
	// what this file is about.
	router.strategy.Store(uint32(C.DomainStrategyPreferIPv6))
	// AAAA answers first, so when the family is available it is the candidate that leads. Without this the
	// two families answer concurrently and the winner is a coin flip - a fixture that passes by luck.
	router.delayAAAA = 0
	router.delayA = 30 * time.Millisecond
	return &transitionRun{
		// parallel=true takes raceResolvedName, which is the path that honours the family PREFERENCE. The
		// serial branch is a deliberate resolver-order walk and would ignore the preference entirely - the
		// first version of this fixture used it and measured the resolver's list order instead of the
		// capability, which is the same "assertion measures the wrong thing" trap Phase 02 hit with the
		// planner's family ordering.
		resolver: &resolveDialer{router: router, dialer: leaf, parallel: true, fallbackDelay: 5 * time.Millisecond},
		leaf:     leaf,
		owner:    owner,
		manager:  manager,
		router:   router,
	}
}

// dialHostname runs one flow whose name resolves to both families, and reports which addresses the
// transport was asked for.
func (r *transitionRun) dialHostname(t *testing.T, ipv4, ipv6 netip.Addr) []netip.Addr {
	t.Helper()
	r.leaf.inner.access.Lock()
	r.leaf.inner.tried = nil
	r.leaf.inner.access.Unlock()

	r.router.addressesA = []netip.Addr{ipv4}
	r.router.addressesAAAA = []netip.Addr{ipv6}

	conn, err := r.resolver.DialContext(context.Background(), "tcp", M.Socksaddr{Fqdn: "transition.example", Port: 443})
	require.NoError(t, err, "the run offers a usable family at every phase")
	if conn != nil {
		conn.Close()
	}
	return r.leaf.tried()
}

// TestTheTransitionRunKeepsProducerAndConsumerInStep is the A→B acceptance this phase exists for.
func TestTheTransitionRunKeepsProducerAndConsumerInStep(t *testing.T) {
	ipv4 := netip.MustParseAddr("112.53.55.92")
	ipv6 := netip.MustParseAddr("2409:8c54:871:1007::2c")
	dualStack := capWith(netcap.StateProvisioned, netcap.StateProvisioned)
	noIPv6 := capWith(netcap.StateProvisioned, netcap.StateUnavailable)

	manager := newPhaseManager(1, noIPv6)
	run := newTransitionRun(t, manager)

	// Fixture integrity: the run prefers IPv6 for its whole life, so any phase that produces IPv4 is the
	// CAPABILITY deciding and not the preference.
	require.Equal(t, C.DomainStrategyPreferIPv6, run.resolver.EffectiveFamilyStrategy())

	// --- Phase 0: old stable, IPv6 unavailable ------------------------------------------------
	oldPolicy := run.resolver.captureAdmissionPolicy()
	require.EqualValues(t, 1, oldPolicy.generation())
	require.Equal(t, admissionNoLocalFamily, oldPolicy.verdict(ipv6))
	require.Equal(t, []netip.Addr{ipv4}, run.dialHostname(t, ipv4, ipv6),
		"a flow on an IPv4-only machine must not attempt IPv6")

	// --- Phase 1: the transition publishes a new generation, dual-stack, settled ----------------
	manager.publish(2, true, dualStack)

	// --- Phase 2: the flow that captured at phase 0 must NOT receive phase 1's capability -------
	//
	// Its capture is superseded, so the ownership predicate refuses nothing (invariant B) and the flow
	// fails open. That is the correct behaviour for a setup whose generation has moved: the transition
	// cancels it, and a cancelled setup must not refuse a family on a network it is no longer on.
	require.False(t, oldPolicy.localFamily.ownership.owns(),
		"a capture from generation 1 must not still be current at generation 2")
	// Both answers change, and they change for the same reason: admits and verdict ASK the ownership
	// predicate, so a superseded capture refuses nothing and admits everything. This is the fail-open
	// degradation, and my first version of this assertion had it backwards - it demanded that a stale
	// capture keep hard-denying, which is the one thing the design forbids (Phase 03 §E).
	require.True(t, oldPolicy.admits(ipv6),
		"a superseded capture must refuse nothing, so the old flow fails open rather than denying a "+
			"family on a network it is no longer on")
	require.Equal(t, admissionAllowed, oldPolicy.verdict(ipv6),
		"and the verdict must agree with the gate, superseded or not")

	// --- Phase 3: a flow that starts now uses the new capability -------------------------------
	//
	// The preference is flipped to IPv6 for the rest of the run so that "the new capability admitted IPv6"
	// is demonstrated by an ATTEMPT rather than only by a verdict. The operator's preference does not
	// change across the transition - only the machine's capability does - so the two later phases differ
	// solely because the capability differs.

	newPolicy := run.resolver.captureAdmissionPolicy()
	require.EqualValues(t, 2, newPolicy.generation(), "a new flow must see the new generation")
	require.Equal(t, admissionAllowed, newPolicy.verdict(ipv6), "and the new capability")
	require.Equal(t, []netip.Addr{ipv6}, run.dialHostname(t, ipv4, ipv6),
		"the run prefers IPv6 throughout, so once the capability admits it IPv6 is the candidate attempted - "+
			"and at phase 0 the SAME preference could only produce IPv4, which is the capability deciding")

	// --- Phase 4: a ROUTE FLAP republishes under the SAME epoch ---------------------------------
	//
	// Same generation, different capability: this is what a same-interface route loss produces, and no
	// ownership term can see it.
	manager.publish(2, true, noIPv6)
	flap := run.resolver.captureAdmissionPolicy()
	require.EqualValues(t, 2, flap.generation(), "a capability-only change does not advance the epoch")
	require.Equal(t, admissionNoLocalFamily, flap.verdict(ipv6), "but the capability did change")

	// --- Phase 5: a flow that starts now obeys the flap ----------------------------------------
	require.Equal(t, []netip.Addr{ipv4}, run.dialHostname(t, ipv4, ipv6),
		"the flap's verdict must reach the next flow even though the generation did not move")

	// And the flow from phase 3 - still within the same epoch, still owned - keeps ITS verdict, which is
	// why the capture has to be the thing that holds the line when the epoch cannot move.
	require.True(t, newPolicy.localFamily.ownership.owns(),
		"generation 2's capture is still current after the flap, so ownership cannot help here")
	require.Equal(t, admissionAllowed, newPolicy.verdict(ipv6),
		"the phase-3 flow's own verdict must not move with the flap")
}

// TestASettlingGenerationCannotHardDenyANewFlow is §11 during the window itself.
//
// A transition that has claimed the network but not committed publishes its verdict with `settled=false`.
// A flow that starts in that window must not turn the transitional verdict into a hard refusal - that is
// the "Settling 期间被固化成 hard unavailable" failure the brief ranks first.
func TestASettlingGenerationCannotHardDenyANewFlow(t *testing.T) {
	ipv4 := netip.MustParseAddr("112.53.55.92")
	ipv6 := netip.MustParseAddr("2409:8c54:871:1007::2c")

	manager := newPhaseManager(1, capWith(netcap.StateProvisioned, netcap.StateUnavailable))
	run := newTransitionRun(t, manager)

	// The transition claims generation 2 and publishes a half-built verdict: IPv6 not seen yet.
	manager.publish(2, false, capWith(netcap.StateProvisioned, netcap.StateUnavailable))

	settling := run.resolver.captureAdmissionPolicy()
	require.EqualValues(t, 2, settling.generation())
	require.False(t, settling.localFamily.ownership.owns(),
		"a capture taken during Settling must not own its verdict")
	require.True(t, settling.admits(ipv6),
		"a transitional verdict was turned into a hard refusal: the window would refuse a family on a "+
			"network that has not finished coming up")

	// The flow dials the family its PREFERENCE leads with - IPv6, which is exactly the candidate the
	// settling verdict claimed the machine could not use. That is the invitation the failure is about: if
	// the transitional verdict had been actionable, this dial would have gone out on IPv4 instead.
	require.Equal(t, []netip.Addr{ipv6}, run.dialHostname(t, ipv4, ipv6),
		"the flow must dial, and the family the settling verdict doubted must be the one it dials")

	// Once the transition commits, the SAME generation becomes actionable - and the flow that started
	// during the window is superseded rather than retroactively denied.
	manager.publish(2, true, capWith(netcap.StateProvisioned, netcap.StateUnavailable))
	committed := run.resolver.captureAdmissionPolicy()
	require.True(t, committed.localFamily.ownership.owns())
	require.Equal(t, admissionNoLocalFamily, committed.verdict(ipv6),
		"the committed verdict must act again")
	require.False(t, settling.localFamily.ownership.owns(),
		"and the flow from the window is now superseded, so it still refuses nothing")
}

// TestFamilyHealthDoesNotSurviveTheTransition is §15 across a real run.
//
// A soft learned failure at generation 1 must not bias the first flow at generation 2. The mechanism is
// the epoch as the health key, taken from the flow's own capture - so this run checks the key AND the
// wiring that feeds it.
func TestFamilyHealthDoesNotSurviveTheTransition(t *testing.T) {
	manager := newPhaseManager(1, capWith(netcap.StateProvisioned, netcap.StateProvisioned))
	run := newTransitionRun(t, manager)

	// Generation 1 learns that IPv6 fails.
	run.owner.familyHealth.recordFailure(1, familyIPv6, context.DeadlineExceeded)
	require.True(t, run.owner.familyHealth.fallbackImmediately(1, familyIPv6, familyIPv4),
		"the penalty must apply on the generation that learned it")

	// The network moves.
	manager.publish(2, true, capWith(netcap.StateProvisioned, netcap.StateProvisioned))

	// The first flow of generation 2 must not inherit the penalty.
	next := run.resolver.captureAdmissionPolicy()
	require.EqualValues(t, 2, next.generation())
	require.False(t, run.owner.familyHealth.fallbackImmediately(next.generation(), familyIPv6, familyIPv4),
		"a generation-1 penalty biased the first flow of generation 2")

	// And generation 2 learns for itself.
	run.owner.familyHealth.recordFailure(2, familyIPv6, context.DeadlineExceeded)
	require.True(t, run.owner.familyHealth.fallbackImmediately(next.generation(), familyIPv6, familyIPv4))
	require.True(t, run.owner.familyHealth.fallbackImmediately(1, familyIPv6, familyIPv4),
		"generation 1 has its OWN entry and must keep it: the key isolates generations, it does not let a "+
			"newer verdict overwrite an older generation's experience")
	require.False(t, run.owner.familyHealth.fallbackImmediately(3, familyIPv6, familyIPv4),
		"and a generation that has never recorded anything starts clean")

	// A dead-path excursion in between must not resurface an older verdict either: no interfaces means
	// Unknown for everything, which refuses nothing.
	manager.publish(3, true, adapter.NetworkFamilyCapability{})
	recovered := run.resolver.captureAdmissionPolicy()
	require.EqualValues(t, 3, recovered.generation())
	require.Equal(t, admissionAllowed, recovered.verdict(netip.MustParseAddr("112.53.55.92")))
	require.Equal(t, admissionAllowed, recovered.verdict(netip.MustParseAddr("2409:8c54:871:1007::2c")))
	require.False(t, run.owner.familyHealth.fallbackImmediately(recovered.generation(), familyIPv6, familyIPv4),
		"a generation that has learned nothing must start with no bias")
}

// TestARouteFlapLeavesNoGoroutineOrTimerBehind is §27/§18 of the brief's perf list, measured rather than
// asserted: the run's phases must not start anything that outlives them.
//
// The number is the process goroutine count around a repeated flap. A scheduler per flow is bounded by the
// flow; a recovery producer per flow is joined by the flow. What must NOT appear is a permanent worker per
// transition - and the only way to show that is to cross the transition many times and look.
func TestARouteFlapLeavesNoGoroutineOrTimerBehind(t *testing.T) {
	ipv4 := netip.MustParseAddr("112.53.55.92")
	ipv6 := netip.MustParseAddr("2409:8c54:871:1007::2c")

	manager := newPhaseManager(1, capWith(netcap.StateProvisioned, netcap.StateProvisioned))
	run := newTransitionRun(t, manager)

	// Warm every path once, so the baseline includes whatever the first flow allocates permanently.
	run.dialHostname(t, ipv4, ipv6)

	before := runtime.NumGoroutine()

	for round := 0; round < 100; round++ {
		if round%2 == 0 {
			manager.publish(2, true, capWith(netcap.StateProvisioned, netcap.StateUnavailable))
		} else {
			manager.publish(2, true, capWith(netcap.StateProvisioned, netcap.StateProvisioned))
		}
		run.dialHostname(t, ipv4, ipv6)
	}

	// Give any unwinding attempt worker a moment to exit; this is a shutdown allowance, not a timing
	// assumption - the assertion below fails only if something is RETAINED.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && runtime.NumGoroutine() > before+2 {
		time.Sleep(5 * time.Millisecond)
	}
	after := runtime.NumGoroutine()
	require.LessOrEqual(t, after, before+2,
		"100 route flaps grew the goroutine count from %d to %d: a transition starts something that "+
			"outlives it", before, after)
}
