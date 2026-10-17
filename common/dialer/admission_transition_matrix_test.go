package dialer

import (
	"context"
	"net/netip"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/netcap"
	C "github.com/sagernet/sing-box/constant"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// Phase 04, second pass: the transition MATRIX and the two LAG sequences.
//
// # Why this file exists
//
// The first Phase 04 pass ran one three-state sequence (IPv4-only → dual-stack → a route flap) and then
// mapped the brief's other sections onto tests that already existed. Two of those sections are the
// phase's headline deliverables and were not actually executed:
//
//	§3  a matrix of NINE transitions, each recording what a flow before it does, what a flow after it
//	    does, and whether the generation moved
//	§5  ADDRESS lag  - IPv4 ready, IPv6 address later, IPv6 route later - starting a new flow at EACH
//	§6  ROUTE lag    - address present, global route missing, route appears later
//	§7  the same for IPv4, and a record of where the model is asymmetric
//
// Those are not variations on the first pass's sequence: they are the shapes where a capability is
// PARTIALLY true, which is where a stale or half-built verdict shows up. So they are here, driven phase by
// phase, with a real flow at every phase.
//
// # What "special" means in the assertions
//
// Every phase's assertion is about a NEW flow - never about a flow that captured earlier - because §1.3 is
// "new flow uses new capability". The behaviour of an OLD flow is a different invariant (§1.4) and it is
// pinned in Phase 03's report and in TestTheTransitionRunKeepsProducerAndConsumerInStep.

// matrixCapability builds a published verdict from the three terms the model has.
func matrixCapability(ipv4, ipv6, egress netcap.FamilyState) adapter.NetworkFamilyCapability {
	return adapter.NetworkFamilyCapability{
		Capability: netcap.FamilyCapability{IPv4: ipv4, IPv6: ipv6, GlobalIPv6Egress: egress},
	}
}

// matrixWorld is one driven world: a manager whose publication the test advances, and a real consumer.
type matrixWorld struct {
	resolver *resolveDialer
	leaf     *scriptedLeaf
	manager  *phaseManager
	router   *strategyCountingRouter
	// answersOnce installs the router's answer before the first flow; see dialHostname's note.
	answersOnce sync.Once
}

func newMatrixWorld(t *testing.T, strategy C.DomainStrategy, epoch uint64, capability adapter.NetworkFamilyCapability) *matrixWorld {
	t.Helper()
	manager := newPhaseManager(epoch, capability)
	owner := &DefaultDialer{familyHealth: newFamilyHealth()}
	owner.network = newNetworkObserver(manager)
	inner := newStallingDialer()
	inner.packetConn = &countingPacketConn{}
	leaf := &scriptedLeaf{owner: owner, inner: inner}
	router := &strategyCountingRouter{scriptedFamilyRouter: newScriptedFamilyRouter()}
	router.strategy.Store(uint32(strategy))
	return &matrixWorld{
		resolver: &resolveDialer{router: router, dialer: leaf, parallel: true, fallbackDelay: time.Millisecond},
		leaf:     leaf,
		manager:  manager,
		router:   router,
	}
}

// reach runs one flow that is offered BOTH families and reports what the transport was asked for, and
// whether the flow failed.
func (w *matrixWorld) reach(t *testing.T, ipv4, ipv6 netip.Addr) ([]netip.Addr, bool) {
	t.Helper()
	w.leaf.inner.access.Lock()
	w.leaf.inner.tried = nil
	w.leaf.inner.access.Unlock()

	w.answersOnce.Do(func() {
		w.router.addressesA = []netip.Addr{ipv4}
		w.router.addressesAAAA = []netip.Addr{ipv6}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := w.resolver.DialContext(ctx, "tcp", M.Socksaddr{Fqdn: "matrix.example", Port: 443})
	if conn != nil {
		conn.Close()
	}
	return w.leaf.tried(), err != nil
}

// transitionRow is one row of §3's matrix.
type transitionRow struct {
	name string
	from adapter.NetworkFamilyCapability
	to   adapter.NetworkFamilyCapability
	// before is what a flow may reach under `from`; after, under `to`. nil means "the flow must fail".
	before []string
	after  []string

	// note records what this row is the only place to prove, when it is.
	note string
}

// TestTheTransitionMatrix runs §3's nine transitions, each driven with a real flow before and after.
//
// The families are named rather than asserted as raw addresses so a row reads as what it is: "before the
// transition the flow could reach v4 and nothing else; after it, v6 and nothing else". A row whose `after`
// still contains only v4 is a row where the transition changed the verdict in a way the dial does not
// show, and each of those carries a `note` saying what it does prove (usually the generation).
func TestTheTransitionMatrix(t *testing.T) {
	ipv4 := netip.MustParseAddr("112.53.55.92")
	ipv6 := netip.MustParseAddr("2409:8c54:871:1007::2c")

	const (
		v4 = "v4"
		v6 = "v6"
	)

	onlyV4 := matrixCapability(netcap.StateProvisioned, netcap.StateUnavailable, netcap.StateUnknown)
	onlyV6 := matrixCapability(netcap.StateUnavailable, netcap.StateProvisioned, netcap.StateProvisioned)
	dual := matrixCapability(netcap.StateProvisioned, netcap.StateProvisioned, netcap.StateProvisioned)
	dead := adapter.NetworkFamilyCapability{}

	rows := []transitionRow{
		{name: "IPv4-only to dual-stack", from: onlyV4, to: dual, before: []string{v4}, after: []string{v6},
			note: "the row the whole capability term exists for: the newly reachable family is used by the next flow"},
		{name: "dual-stack to IPv4-only", from: dual, to: onlyV4, before: []string{v6}, after: []string{v4}},
		{name: "IPv6-only to dual-stack", from: onlyV6, to: dual, before: []string{v6}, after: []string{v6},
			note: "both endpoints prefer v6, so the dial cannot show the difference; what the row proves is that the " +
				"transition is accepted and the generation advanced without the flow changing"},
		{name: "dual-stack to IPv6-only", from: dual, to: onlyV6, before: []string{v6}, after: []string{v6}},
		{name: "IPv4-only to IPv6-only", from: onlyV4, to: onlyV6, before: []string{v4}, after: []string{v6},
			note: "the hardest row: the usable family CHANGES, so a stale verdict in either direction is visible"},
		{name: "IPv6-only to IPv4-only", from: onlyV6, to: onlyV4, before: []string{v6}, after: []string{v4}},
		{name: "dual-stack to dead path", from: dual, to: dead, before: []string{v6}, after: nil,
			note: "a dead path publishes Unknown for every term, so it refuses nothing; what stops the flow is the " +
				"network, which is what the unreachable transport models"},
		{name: "dead path to IPv4-only", from: dead, to: onlyV4, before: nil, after: []string{v4},
			note: "recovery from a dead path must not retain the dead verdict as a hard one"},
		{name: "dead path to dual-stack", from: dead, to: dual, before: nil, after: []string{v6},
			note: "and the recovered generation is fully rebuilt, not partially"},
	}

	for index, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			world := newMatrixWorld(t, C.DomainStrategyPreferIPv6, uint64(index+1), row.from)
			// A dead path is the ZERO capability: nothing observed, so nothing refused, and the network
			// itself refuses every connect. The transport follows the WORLD rather than the row, so the row
			// cannot accidentally assert a reachable flow on a dead network.
			world.leaf.inner.unreachable = isDeadCapability(row.from)

			beforeReached, beforeFailed := world.reach(t, ipv4, ipv6)
			require.Equal(t, len(row.before) == 0, beforeFailed,
				"before the transition the flow's failure did not match the world: reached %v", beforeReached)
			// A row that expects FAILURE has no family to compare: on a dead network the flow attempts and
			// every attempt is refused, so "which family it reached first" is a question about attempt
			// order, not about capability.
			if len(row.before) > 0 {
				require.Equal(t, expectedFamilies(row.before, ipv4, ipv6), firstFamily(beforeReached, ipv4, ipv6),
					"before the transition a flow reached the wrong family")
			}

			// The transition itself, and with it the network's own reachability.
			world.manager.publish(uint64(index+2), true, row.to)
			world.leaf.inner.unreachable = isDeadCapability(row.to)

			afterReached, afterFailed := world.reach(t, ipv4, ipv6)
			require.Equal(t, len(row.after) == 0, afterFailed,
				"after the transition the flow's failure did not match the world: reached %v", afterReached)
			if len(row.after) > 0 {
				require.Equal(t, expectedFamilies(row.after, ipv4, ipv6), firstFamily(afterReached, ipv4, ipv6),
					"after the transition a flow reached the wrong family - %s", row.note)
			}

			// The generation advanced, and the new flow captured it.
			captured := world.resolver.captureAdmissionPolicy()
			require.EqualValues(t, index+2, captured.generation(),
				"the flow after the transition captured generation %d, not the published one", captured.generation())
		})
	}
}

// expectedFamilies maps a row's expectation onto the address it should reach, or zero for "none".
func expectedFamilies(families []string, ipv4, ipv6 netip.Addr) netip.Addr {
	if len(families) == 0 {
		return netip.Addr{}
	}
	if families[0] == "v4" {
		return ipv4
	}
	return ipv6
}

// firstFamily reports the first address actually reached, or zero. A flow reaches one family first; the
// assertions in this file are about which one led, not about the whole attempt list, because a loser may
// also have been launched and that is a scheduler-cadence fact rather than a capability one.
func firstFamily(reached []netip.Addr, ipv4, ipv6 netip.Addr) netip.Addr {
	if len(reached) == 0 {
		return netip.Addr{}
	}
	for _, address := range reached {
		if address == ipv4 || address == ipv6 {
			return address
		}
	}
	return netip.Addr{}
}

// TestAddressLagPhases is §5: the address appears, then the route does, and a NEW flow is started at each
// phase.
//
// The target is not to force a policy. It is to prove that no phase produces a STALE PERMANENT verdict -
// which has two halves: a phase that has not seen IPv6 yet must not hard-deny it (Unknown refuses
// nothing), and the phase that HAS seen it must be reflected in the next flow.
func TestAddressLagPhases(t *testing.T) {
	ipv4 := netip.MustParseAddr("112.53.55.92")
	ipv6 := netip.MustParseAddr("2409:8c54:871:1007::2c")

	world := newMatrixWorld(t, C.DomainStrategyPreferIPv6, 1,
		// Phase 0: the new network has IPv4 and has not said anything about IPv6 yet.
		matrixCapability(netcap.StateProvisioned, netcap.StateUnknown, netcap.StateUnknown))

	// Phase 0 - IPv4 only, IPv6 UNKNOWN (not unavailable).
	reached, failed := world.reach(t, ipv4, ipv6)
	require.False(t, failed)
	require.NotEqual(t, netip.Addr{}, firstFamily(reached, ipv4, ipv6),
		"the flow reached no family at all in the phase where IPv4 works and IPv6 is merely unobserved")

	unknownPhase := world.resolver.captureAdmissionPolicy()
	require.Equal(t, admissionAllowed, unknownPhase.verdict(ipv6),
		"an unobserved family was encoded as unavailable: Unknown must refuse nothing")

	// Phase 1 - the IPv6 ADDRESS appears; the route has not been observed yet.
	world.manager.publish(2, true, matrixCapability(netcap.StateProvisioned, netcap.StateProvisioned, netcap.StateUnknown))
	reached, failed = world.reach(t, ipv4, ipv6)
	require.False(t, failed)
	require.Equal(t, ipv6, firstFamily(reached, ipv4, ipv6),
		"the address phase did not reach the new flow")

	// Phase 2 - the ROUTE appears.
	world.manager.publish(3, true, matrixCapability(netcap.StateProvisioned, netcap.StateProvisioned, netcap.StateProvisioned))
	reached, failed = world.reach(t, ipv4, ipv6)
	require.False(t, failed)
	require.Equal(t, ipv6, firstFamily(reached, ipv4, ipv6),
		"the route phase did not reach the new flow")

	// And the phase-0 capture is superseded, so it refuses nothing - the stale verdict cannot harden.
	require.False(t, unknownPhase.localFamily.ownership.owns())
	require.Equal(t, admissionAllowed, unknownPhase.verdict(ipv6),
		"a superseded capture must fail open rather than keep the stale verdict")
}

// TestRouteLagPhases is §6: the address is there, the global route is not, and the route arrives later.
//
// The two claims are opposite in direction and both matter: the EARLY flow must not falsely succeed on a
// global IPv6 address it has no route to, and the LATER flow must not inherit the no-egress verdict.
func TestRouteLagPhases(t *testing.T) {
	ipv4 := netip.MustParseAddr("112.53.55.92")
	ipv6 := netip.MustParseAddr("2409:8a00::1")

	// Phase 0: the address exists, the global route has been observed ABSENT.
	world := newMatrixWorld(t, C.DomainStrategyPreferIPv6, 1,
		matrixCapability(netcap.StateProvisioned, netcap.StateProvisioned, netcap.StateUnavailable))

	reached, failed := world.reach(t, ipv4, ipv6)
	require.False(t, failed)
	require.Equal(t, ipv4, firstFamily(reached, ipv4, ipv6),
		"a flow succeeded on a global IPv6 address although the machine had no global IPv6 route")

	noEgress := world.resolver.captureAdmissionPolicy()
	require.Equal(t, admissionNoGlobalEgress, noEgress.verdict(ipv6),
		"the no-egress verdict must name the EGRESS term, not the family term: they have different remedies")

	// Phase 1: the route appears.
	world.manager.publish(2, true, matrixCapability(netcap.StateProvisioned, netcap.StateProvisioned, netcap.StateProvisioned))
	reached, failed = world.reach(t, ipv4, ipv6)
	require.False(t, failed)
	require.Equal(t, ipv6, firstFamily(reached, ipv4, ipv6),
		"the flow after the route appeared inherited the no-egress verdict")

	// The route change is a CAPABILITY-only change, so it need not move the epoch - and if it did not, the
	// only thing that could have prevented inheritance is the new capture. This asserts the value moved.
	after := world.resolver.captureAdmissionPolicy()
	require.Equal(t, admissionAllowed, after.verdict(ipv6),
		"the capability did not move even though the route did")
}

// TestTheIPv4RouteTransitionIsNotModelledTheSameWayAsIPv6 is §7, and its answer is a recorded asymmetry
// rather than a pass.
//
// The model has three terms: IPv4, IPv6 address, and IPv6 GLOBAL EGRESS. There is no IPv4 egress term. So an
// IPv4 default route disappearing cannot be expressed as "the address is fine but there is no route" - it
// can only reach a flow as the IPv4 term being unavailable. That is a real asymmetry, it is deliberate (see
// Phase 01 §H: IPv6 is the family whose reachability is routinely split between address and route), and a
// test that pretended otherwise would be asserting a term that does not exist.
//
// What this test DOES prove is that the IPv4 term is honoured in both directions with the same force as the
// IPv6 one, which is the part §7 can actually require.
func TestTheIPv4RouteTransitionIsNotModelledTheSameWayAsIPv6(t *testing.T) {
	ipv4 := netip.MustParseAddr("112.53.55.92")
	ipv6 := netip.MustParseAddr("2409:8a00::1")

	world := newMatrixWorld(t, C.DomainStrategyPreferIPv4, 1,
		matrixCapability(netcap.StateProvisioned, netcap.StateProvisioned, netcap.StateProvisioned))

	reached, failed := world.reach(t, ipv4, ipv6)
	require.False(t, failed)
	require.Equal(t, ipv4, firstFamily(reached, ipv4, ipv6))

	// The IPv4 route is gone. The only term that can express it is the IPv4 one.
	world.manager.publish(2, true, matrixCapability(netcap.StateUnavailable, netcap.StateProvisioned, netcap.StateProvisioned))
	reached, failed = world.reach(t, ipv4, ipv6)
	require.False(t, failed, "the flow had a usable family and must have used it")
	require.Equal(t, ipv6, firstFamily(reached, ipv4, ipv6),
		"the IPv4 term was not honoured: a flow preferred IPv4 on a machine with no IPv4")

	// And it comes back.
	world.manager.publish(3, true, matrixCapability(netcap.StateProvisioned, netcap.StateProvisioned, netcap.StateProvisioned))
	reached, failed = world.reach(t, ipv4, ipv6)
	require.False(t, failed)
	require.Equal(t, ipv4, firstFamily(reached, ipv4, ipv6),
		"the restored IPv4 term did not reach the next flow")

	// The asymmetry, asserted against the MODEL rather than against behaviour, because behaviour cannot
	// express a term that does not exist. Writing the assertion as `.GlobalIPv4Egress` is a compile error -
	// which IS the proof, and which is why this check is reflection over the type instead.
	//
	// This is not pedantry: if the capability model grows an IPv4 egress term, §7's answer changes from
	// "there is no such term, so an IPv4 route loss reaches a flow as the address term" to something that
	// has to be re-derived, and §10 of the brief forbids adding a term on elegance alone.
	capabilityType := reflect.TypeOf(netcap.FamilyCapability{})
	var exported, unexported []string
	for index := 0; index < capabilityType.NumField(); index++ {
		field := capabilityType.Field(index)
		if field.IsExported() {
			exported = append(exported, field.Name)
		} else {
			unexported = append(unexported, field.Name)
		}
	}
	// The unexported one is `reachable`, the interface COUNT the evaluation could look at - provenance, not
	// a decision term. It is named here so the distinction is not silently lost if the struct changes.
	require.Equal(t, []string{"reachable"}, unexported,
		"the capability model grew an unexported field (%v); if it is a decision term rather than provenance, "+
			"this test and §7 must be re-derived", unexported)
	require.Equal(t, []string{"IPv4", "IPv6", "GlobalIPv6Egress"}, exported,
		"the capability model changed shape (%v). §7's answer - there is no IPv4 egress term, so an IPv4 "+
			"route loss can only reach a flow as the address term - is now stale and must be re-derived "+
			"rather than assumed. §10 forbids a new term without measured evidence.", exported)
}

// isDeadCapability reports whether a published verdict describes a DEAD PATH - every term unobserved,
// which is what a machine with no usable interface publishes and what a connect on such a machine gets
// refused by.
func isDeadCapability(capability adapter.NetworkFamilyCapability) bool {
	return capability.Capability == (netcap.FamilyCapability{})
}
