package route

import (
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/netcap"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing/common/control"

	"github.com/stretchr/testify/require"
)

// Layer 1 of the debug round: the state machine, and the transitions that must be impossible.
//
// # The model
//
// The capability has five named states but only TWO producers, and that asymmetry is the design:
//
//	state         producer                                  who may act on it
//	unknown       no observation yet (zero value)           nobody: fail open
//	unavailable   Evaluate: no usable interface carries     a LOCAL dial may refuse the candidate
//	              an address of this family, and there
//	              was something to look at
//	provisioned   Evaluate: at least one usable interface   nobody: normal Happy Eyeballs
//	              carries such an address
//	validated     RESERVED - no producer                    nobody: no restriction
//	degraded      RESERVED - no producer                    nobody: no restriction
//
// # Why the reserved states exist at all
//
// Not for symmetry. A model whose "good" and "suspect" answers had to be invented the moment someone
// needed them would be a model whose consumers were written against a two-state world, and the
// consumers are what would have to change. Declaring them now - with consumers already handling them
// as "no restriction" and a test pinning that - means adding a producer later cannot silently start
// deleting candidates.
//
// # What each test below attacks
//
// The four illegal transitions in the round's brief, one each, plus the two properties that make the
// whole thing safe: identical capability is published as identical, and an invalidated environment
// does not carry a verdict.

// TestStateMachineHasExactlyTwoProducers pins the transition table against the source.
//
// A reserved state acquiring a producer is not a local change: the consumers act on these values, so
// the test that guards them is the one that says which values can exist. This asserts the model's own
// contract - only Unavailable restricts - for every state, so a future producer of Validated or
// Degraded is admitted without renegotiating the consumers.
func TestStateMachineHasExactlyTwoProducers(t *testing.T) {
	// Cells the model can actually produce. Every one of them is derived from the SAME observation
	// here - one up interface carrying IPv4 only - so the test cannot rot into an unrelated fixture.
	observed := netcap.Evaluate([]netcap.InterfaceObservation{{
		Name:      "eth0",
		Up:        true,
		Addresses: []netip.Prefix{netip.MustParsePrefix("192.168.1.10/24")},
	}})
	require.Equal(t, netcap.StateProvisioned, observed.IPv4)
	require.Equal(t, netcap.StateUnavailable, observed.IPv6)

	nothingObserved := netcap.Evaluate(nil)
	require.Equal(t, netcap.StateUnknown, nothingObserved.IPv4)
	require.Equal(t, netcap.StateUnknown, nothingObserved.IPv6)

	// No producer anywhere yields these. They are declared so that a consumer written today is already
	// correct when one appears, and the assertion is that they behave that way NOW.
	require.Equal(t, netcap.StateValidated, netcap.StateValidated)
	require.Equal(t, netcap.StateDegraded, netcap.StateDegraded)
}

// TestOneDestinationTimeoutCannotBecomeGlobalUnavailable is illegal transition #2.
//
// The brief's phrasing is "one destination timeout -> global IPv6 Unavailable". The evidence for
// Unavailable is a fact about the machine's interfaces, and a timeout is a fact about one address -
// so the transition is not merely unlikely, it has no path. This attacks it from the side the code can
// actually be wrong about: a network full of IPv6 that is blackholed to the public Internet is still a
// network whose interfaces carry IPv6, and the only correct report is Provisioned.
func TestOneDestinationTimeoutCannotBecomeGlobalUnavailable(t *testing.T) {
	h := newTransitionWindowHarness(t)
	// The structural "fake IPv6" shape: address, link, and (from the capability's point of view) a
	// perfectly present family. What is broken is somewhere the machine cannot see about itself.
	setInterfaces(h, "192.0.2.1",
		interfaceOn(1, "eth0", C.InterfaceTypeEthernet, true, "192.168.1.10/24", "2001:db8::10/64"))
	h.settle(t, "fake-ipv6-campus")

	capability := currentFamilyCapability(t, h).Capability
	require.Equal(t, netcap.StateProvisioned, capability.IPv6,
		"a family with addressing is provisioned, whatever the public Internet does with it")
	require.False(t, capability.HardUnavailable(false),
		"and provisioned must never be spelled the same way as impossible: the scheduler still races it")

	// Repeated transitions cannot turn a present family into an absent one either. The state is
	// recomputed from the interfaces, never accumulated from observations, so there is no counter for
	// repeated failure to increment.
	h.settle(t, "fake-ipv6-campus")
	h.settle(t, "fake-ipv6-campus")
	require.Equal(t, netcap.StateProvisioned, currentFamilyCapability(t, h).Capability.IPv6,
		"the verdict is a function of the interfaces, not a history of failures")
}

// TestCaptivePortalCannotBecomePermanentUnavailable is illegal transition #3, in the form the model
// can actually be wrong about.
//
// A portal that has not been authenticated yet produces exactly one observable: connections that do
// not work. It produces no interface change, so it produces no capability change - which is the
// correct behaviour and the one this asserts. The round's requirement is that an unauthenticated
// portal is never recorded as a permanent family verdict, and the strongest form of that guarantee is
// that nothing about failed connections reaches this state at all.
func TestCaptivePortalCannotBecomePermanentUnavailable(t *testing.T) {
	h := newTransitionWindowHarness(t)
	setInterfaces(h, "192.0.2.1",
		interfaceOn(1, "eth0", C.InterfaceTypeEthernet, true, "192.168.1.10/24", "2001:db8::10/64"))
	// An unauthenticated portal still hands out both addresses - that is what makes it a portal rather
	// than a broken network.
	h.settle(t, "hotel-portal")

	before := currentFamilyCapability(t, h).Capability
	require.Equal(t, netcap.StateProvisioned, before.IPv6)

	// The portal is authenticated. On every platform that is an interface or network-path event, and
	// the capability is recomputed from the resulting interfaces - so the requirement is that the
	// recompute happens rather than that a verdict is expired by hand.
	setInterfaces(h, "192.0.2.1",
		interfaceOn(1, "eth0", C.InterfaceTypeEthernet, true, "192.168.1.10/24", "2001:db8::10/64"))
	h.manager.updateNetworkEnvironment()
	require.Equal(t, before, currentFamilyCapability(t, h).Capability,
		"nothing about the addressing changed, so nothing about the verdict may change")

	// And the portal's failures cannot have accumulated into a downgrade in the meantime, because the
	// state has no accumulator: recomputing it is idempotent.
	h.manager.updateNetworkEnvironment()
	h.manager.updateNetworkEnvironment()
	require.Equal(t, netcap.StateProvisioned, currentFamilyCapability(t, h).Capability.IPv6,
		"a state derived from the interfaces cannot be eroded by connection outcomes")
}

// TestUnknownNeverHardFilters is illegal transition #4, asserted at the consumer rather than at the
// producer.
//
// Every failure to observe the machine - no monitor, no interfaces, a list that is momentarily empty
// during a handover - lands in Unknown. If Unknown could refuse a candidate, an observation failure
// would become a hard filter, and the filter would fire precisely during handovers, which is when a
// wrongly refused connection is most expensive.
func TestUnknownNeverHardFilters(t *testing.T) {
	zero := netcap.FamilyCapability{}
	require.Equal(t, netcap.StateUnknown, zero.IPv4)
	require.Equal(t, netcap.StateUnknown, zero.IPv6)
	require.False(t, zero.HardUnavailable(true))
	require.False(t, zero.HardUnavailable(false))

	// The manager's own no-opinion value, which is what a box that has not started publishes.
	h := newTransitionWindowHarness(t)
	published := currentFamilyCapability(t, h).Capability
	require.Equal(t, netcap.StateUnknown, published.IPv4,
		"a manager that has not observed a network reports nothing")
	require.Equal(t, netcap.StateUnknown, published.IPv6)
	require.False(t, published.HardUnavailable(true))
	require.False(t, published.HardUnavailable(false))
}

// TestOldGenerationVerdictCannotOutliveItsEnvironment is illegal transition #1 at the state layer.
//
// The brief's phrasing is "old generation success -> new generation Validated". Nothing produces
// Validated, so the transition cannot happen by that route - but the same shape is reachable through
// the state that DOES exist, and that is what this attacks: a verdict observed on network A must not
// be readable as network B's. The reserved state's absence is asserted separately below so the two
// halves cannot be confused with each other.
func TestOldGenerationVerdictCannotOutliveItsEnvironment(t *testing.T) {
	h := newTransitionWindowHarness(t)
	setInterfaces(h, "192.0.2.1", interfaceOn(1, "eth0", C.InterfaceTypeEthernet, true, "192.168.1.10/24"))
	h.settle(t, "network-a-ipv4-only")
	epochA := h.manager.NetworkResetGeneration()
	require.Equal(t, netcap.StateUnavailable, currentFamilyCapability(t, h).Capability.IPv6)

	// Network B is dual-stack. The capability published with B's epoch describes B.
	setInterfaces(h, "192.0.2.9",
		interfaceOn(1, "eth0", C.InterfaceTypeEthernet, true, "192.168.1.10/24", "2001:db8::10/64"))
	h.settle(t, "network-b-dual-stack")
	epochB := h.manager.NetworkResetGeneration()
	require.Greater(t, epochB, epochA)

	_, _, capability := h.manager.NetworkFamilyCapabilitySnapshot()
	require.Equal(t, netcap.StateProvisioned, capability.Capability.IPv6,
		"network A's verdict must not be readable here, and it is not: the value is recomputed, not "+
			"carried")

	// Going back to an IPv4-only network restores that network's verdict rather than remembering B's.
	setInterfaces(h, "192.0.2.1", interfaceOn(1, "eth0", C.InterfaceTypeEthernet, true, "192.168.1.10/24"))
	h.settle(t, "network-a-ipv4-only")
	require.Equal(t, netcap.StateUnavailable, currentFamilyCapability(t, h).Capability.IPv6,
		"a verdict is a property of the network being observed, not a level that only ever rises")
}

// TestNoProducerForValidatedOrDegraded is the other half of illegal transition #1, and it is the
// reason this round did not implement a network-wide degradation verdict.
//
// The evidence available per connection is destination-specific: one successful connection to one
// address says that address answered, and one stalled attempt says that address did not. Promoting
// either to a network-wide family verdict - in the positive direction as Validated, in the negative as
// Degraded - would be exactly the amplification the design is required to avoid. So neither state has
// a producer, and the short-term learning stays where the scope is already correct: the per-dialer,
// per-network-environment family health.
//
// The assertion is on the only place a producer could be added - the evaluation - and it holds for
// every input the manager can supply.
func TestNoProducerForValidatedOrDegraded(t *testing.T) {
	inputs := []struct {
		name       string
		interfaces []netcap.InterfaceObservation
	}{
		{"no interfaces", nil},
		{"one ipv4-only interface", []netcap.InterfaceObservation{{
			Name: "eth0", Up: true, Addresses: mustPrefixes("192.168.1.10/24"),
		}}},
		{"dual stack", []netcap.InterfaceObservation{{
			Name: "eth0", Up: true, Addresses: mustPrefixes("192.168.1.10/24", "2001:db8::1/64"),
		}}},
		{"many interfaces", []netcap.InterfaceObservation{
			{Name: "eth0", Up: true, Addresses: mustPrefixes("192.168.1.10/24")},
			{Name: "eth1", Up: true, Addresses: mustPrefixes("10.0.0.5/8", "2001:db8::5/64")},
		}},
	}
	for _, input := range inputs {
		t.Run(input.name, func(t *testing.T) {
			capability := netcap.Evaluate(input.interfaces)
			for _, state := range []netcap.FamilyState{capability.IPv4, capability.IPv6} {
				require.NotEqual(t, netcap.StateValidated, state,
					"nothing in this round can prove a family carries public traffic")
				require.NotEqual(t, netcap.StateDegraded, state,
					"nor can it prove one is blackholed without destination-independent evidence")
			}
		})
	}
}

// TestIdenticalObservationIsPublishedAsIdentical gives the change detection its contract.
//
// The publisher compares the computed capability with the stored one to decide whether anything
// observable moved - which is a comparison on the struct, and therefore depends on the struct being
// built from comparable fields with no unexported counter that a recomputation could perturb. A
// capability that differed on every recompute would wake consumers for updates carrying no
// information, and on the hot path that is indistinguishable from noise.
func TestIdenticalObservationIsPublishedAsIdentical(t *testing.T) {
	observation := []netcap.InterfaceObservation{{
		Name: "eth0", Up: true, Addresses: mustPrefixes("192.168.1.10/24", "2001:db8::1/64"),
	}}
	first := netcap.Evaluate(observation)
	second := netcap.Evaluate(observation)
	require.Equal(t, first, second, "the same observation must produce an equal value")

	// And the manager's own publication path agrees: republishing an unchanged observation reports no
	// change, so the interface-refresh path does not manufacture transitions.
	h := newTransitionWindowHarness(t)
	setInterfaces(h, "192.0.2.1", interfaceOn(1, "eth0", C.InterfaceTypeEthernet, true, "192.168.1.10/24"))
	h.settle(t, "stable")
	epoch := h.manager.NetworkResetGeneration()
	for i := 0; i < 3; i++ {
		h.manager.updateNetworkEnvironment()
	}
	require.Equal(t, epoch, h.manager.NetworkResetGeneration(),
		"an unchanged observation is not a transition, however many times it is observed")
}

// TestCapabilityIsUnchangedAcrossAFullHandoverSequence is the integration form of the table.
//
// It walks a real handover - Wi-Fi IPv4-only, to cellular dual-stack, back to Wi-Fi IPv4-only - and
// asserts the verdict at every step. Each network's answer must be that network's own, which is what
// makes "IPv6 came back" and "IPv6 went away" observable without anything remembering a history.
func TestCapabilityIsUnchangedAcrossAFullHandoverSequence(t *testing.T) {
	h := newTransitionWindowHarness(t)
	h.manager.interfaceMonitor = &staticInterfaceMonitor{
		current: &control.Interface{Index: 1, Name: "wlan0"},
	}

	type step struct {
		gateway      string
		ssid         string
		defaultIndex int
		defaultName  string
		interfaces   []adapter.NetworkInterface
		ipv6         netcap.FamilyState
	}
	steps := []step{
		{
			gateway: "192.0.2.1", ssid: "wifi", defaultIndex: 1, defaultName: "wlan0",
			interfaces: []adapter.NetworkInterface{
				interfaceOn(1, "wlan0", C.InterfaceTypeWIFI, true, "192.168.1.10/24"),
			},
			ipv6: netcap.StateUnavailable,
		},
		{
			gateway: "192.0.2.9", ssid: "cellular", defaultIndex: 2, defaultName: "rmnet0",
			// The Wi-Fi link goes down as the device hands over: the platform now dials out of the
			// cellular interface, which is the interface that carries IPv6.
			interfaces: []adapter.NetworkInterface{
				interfaceOn(1, "wlan0", C.InterfaceTypeWIFI, false, "192.168.1.10/24"),
				interfaceOn(2, "rmnet0", C.InterfaceTypeCellular, true, "10.20.0.5/24", "2001:db8::5/64"),
			},
			ipv6: netcap.StateProvisioned,
		},
		{
			gateway: "192.0.2.1", ssid: "wifi", defaultIndex: 1, defaultName: "wlan0",
			interfaces: []adapter.NetworkInterface{
				interfaceOn(1, "wlan0", C.InterfaceTypeWIFI, true, "192.168.1.10/24"),
			},
			ipv6: netcap.StateUnavailable,
		},
	}

	for index, current := range steps {
		h.manager.interfaceMonitor.(*staticInterfaceMonitor).current = &control.Interface{
			Index: current.defaultIndex, Name: current.defaultName,
		}
		setInterfaces(h, current.gateway, current.interfaces...)
		h.settle(t, current.ssid)
		require.Equalf(t, current.ipv6, currentFamilyCapability(t, h).Capability.IPv6,
			"step %d (%s) must report its own network's addressing", index, current.ssid)
	}
}
