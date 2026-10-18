package route

import (
	"errors"
	"net"
	"net/netip"
	"strconv"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/netcap"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing/common/control"
	"github.com/sagernet/sing/common/logger"

	"github.com/stretchr/testify/require"
)

// The lifecycle half of the entry presentment: what the startup observation reads, what it must not
// disturb, when it runs at all, and what the committed value means to a consumer.
//
// The decision itself is netcap's and is tested there. What is here is the wiring that makes it
// safe to take before the box has finished starting:
//
//   - a READ-ONLY observation: no publish, no fingerprint, no transition, no reset;
//   - an observation failure is "no opinion", never "no IPv6";
//   - no demand means no observation at all;
//   - the committed value is what every consumer reads, and a correction by the entry that applied
//     it wins.

func entryTestInterface(index int, name string, addresses ...string) control.Interface {
	parsed := make([]netip.Prefix, 0, len(addresses))
	for _, address := range addresses {
		parsed = append(parsed, netip.MustParsePrefix(address))
	}
	return control.Interface{
		Index:     index,
		Name:      name,
		Flags:     net.FlagUp | net.FlagRunning,
		Addresses: parsed,
	}
}

// countingInterfaceFinder counts refreshes, so a test can assert that a configuration with no IPv6
// demand pays for no observation at all - the zero-cost promise as an event rather than a timing.
type countingInterfaceFinder struct {
	*testInterfaceFinder
	updates int
}

func (f *countingInterfaceFinder) Update() error {
	f.updates++
	return f.testInterfaceFinder.Update()
}

// entryPresentmentManager builds a manager whose observation input is entirely a fixture: the finder
// is the fixture finder (no enumeration of the runner), the route answer is injected because a real
// machine cannot be put into "no global IPv6 route" on request, and the published interface list is
// empty so the reader takes the finder path rather than a harness's own list.
func entryPresentmentManager(t *testing.T, route netcap.RouteObservation, interfaces ...control.Interface) (*NetworkManager, *countingInterfaceFinder) {
	t.Helper()
	finder := &countingInterfaceFinder{testInterfaceFinder: newTestInterfaceFinder(interfaces)}
	return &NetworkManager{
		interfaceFinder:  finder,
		interfaceMonitor: &staticInterfaceMonitor{current: &control.Interface{Index: 1, Name: "en0"}},
		routeObservation: func() netcap.RouteObservation { return route },
		logger:           logger.NOP(),
	}, finder
}

// TestObserveEntryPresentmentDecidesFromTheFixtureNotTheMachine is the decision table at the route
// layer, where the route term and the address shape are combined.
func TestObserveEntryPresentmentDecidesFromTheFixtureNotTheMachine(t *testing.T) {
	testCases := []struct {
		name        string
		route       netcap.RouteObservation
		interfaces  []control.Interface
		presentment netcap.IPv6Presentment
		observed    bool
	}{
		{
			name:        "ipv4-only network",
			route:       netcap.RouteAbsent,
			interfaces:  []control.Interface{entryTestInterface(1, "en0", "192.0.2.10/24")},
			presentment: netcap.SuppressIPv6,
			observed:    true,
		},
		{
			name:        "ipv4-only network, route unread",
			route:       netcap.RouteUnobserved,
			interfaces:  []control.Interface{entryTestInterface(1, "en0", "192.0.2.10/24")},
			presentment: netcap.SuppressIPv6,
			observed:    true,
		},
		{
			name:        "link-local only addressing",
			route:       netcap.RouteAbsent,
			interfaces:  []control.Interface{entryTestInterface(1, "en0", "192.0.2.10/24", "fe80::1/64")},
			presentment: netcap.SuppressIPv6,
			observed:    true,
		},
		{
			name:        "dual stack",
			route:       netcap.RoutePresent,
			interfaces:  []control.Interface{entryTestInterface(1, "en0", "192.0.2.10/24", "2001:db8::10/64")},
			presentment: netcap.PresentIPv6,
			observed:    true,
		},
		{
			name:        "global address without a global route",
			route:       netcap.RouteAbsent,
			interfaces:  []control.Interface{entryTestInterface(1, "en0", "192.0.2.10/24", "2001:db8::10/64")},
			presentment: netcap.SuppressIPv6,
			observed:    true,
		},
		{
			name:        "global address, route unread",
			route:       netcap.RouteUnobserved,
			interfaces:  []control.Interface{entryTestInterface(1, "en0", "192.0.2.10/24", "2001:db8::10/64")},
			presentment: netcap.PresentIPv6,
			observed:    true,
		},
		{
			name:       "ipv4-only, route unread",
			route:      netcap.RouteUnobserved,
			interfaces: []control.Interface{entryTestInterface(1, "en0", "192.0.2.10/24")},
			// The family state is a hard negative from addressing alone, so the unread route cannot
			// turn "this machine has no IPv6" into a fail-open.
			presentment: netcap.SuppressIPv6,
			observed:    true,
		},
		{
			name:        "unique-local address without a global route",
			route:       netcap.RouteAbsent,
			interfaces:  []control.Interface{entryTestInterface(1, "en0", "192.0.2.10/24", "fd00::10/64")},
			presentment: netcap.PresentIPv6,
			observed:    true,
		},
		{
			name:        "ipv6-only network",
			route:       netcap.RoutePresent,
			interfaces:  []control.Interface{entryTestInterface(1, "eth0", "2001:db8::10/64")},
			presentment: netcap.PresentIPv6,
			observed:    true,
		},
		{
			name:       "no interfaces at all",
			route:      netcap.RouteUnobserved,
			interfaces: nil,
			observed:   false,
		},
		{
			name:  "a down interface is not evidence",
			route: netcap.RouteAbsent,
			interfaces: []control.Interface{{
				Index:     1,
				Name:      "en0",
				Flags:     0,
				Addresses: []netip.Prefix{netip.MustParsePrefix("192.0.2.10/24")},
			}},
			observed: false,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			manager, _ := entryPresentmentManager(t, testCase.route, testCase.interfaces...)
			presentment, observed := manager.observeIPv6EntryPresentment()
			require.Equal(t, testCase.observed, observed)
			if !testCase.observed {
				require.Equal(t, netcap.PresentIPv6, presentment,
					"an observation failure must answer 'present', never a negative")
				return
			}
			require.Equal(t, testCase.presentment, presentment)
		})
	}
}

// TestTheOwnTunnelIsNotEvidenceOfIPv6 is the self-reference rule, stated for the entry decision.
//
// The manager's own TUN carries an IPv6 address precisely BECAUSE the tunnel is up; counting it
// would make the entry decision approve the very presentment it is meant to remove.
func TestTheOwnTunnelIsNotEvidenceOfIPv6(t *testing.T) {
	manager, _ := entryPresentmentManager(t, netcap.RoutePresent,
		entryTestInterface(1, "en0", "192.0.2.10/24"),
		entryTestInterface(2, "tun0", "172.19.0.1/30", "fdfe:dcba:9876::1/126", "2001:db8::99/64"),
	)
	manager.interfaceMonitor = &staticInterfaceMonitor{
		current: &control.Interface{Index: 1, Name: "en0"},
		own:     []string{"tun0"},
	}

	presentment, observed := manager.observeIPv6EntryPresentment()
	require.True(t, observed)
	require.Equal(t, netcap.SuppressIPv6, presentment,
		"the tunnel's own global IPv6 address is not evidence about the network underneath it")
}

// TestTheObservationIsReadOnly is the rule that makes it legal to run before the box has started.
func TestTheObservationIsReadOnly(t *testing.T) {
	manager, _ := entryPresentmentManager(t, netcap.RouteAbsent,
		entryTestInterface(1, "en0", "192.0.2.10/24"))

	environmentBefore := manager.NetworkEnvironment()
	generationBefore := manager.NetworkResetGeneration()
	stableBefore := manager.NetworkTransitionStable()

	presentment, observed := manager.observeIPv6EntryPresentment()
	require.True(t, observed)
	require.Equal(t, netcap.SuppressIPv6, presentment)

	require.Equal(t, environmentBefore, manager.NetworkEnvironment(),
		"the environment fingerprint must not move: this is not a network event")
	require.Equal(t, generationBefore, manager.NetworkResetGeneration(),
		"the reset epoch must not move: no transition was claimed")
	require.Equal(t, stableBefore, manager.NetworkTransitionStable())
	require.Empty(t, manager.networkInterfaces.Load(),
		"the observation must not publish an interface list; the startup path publishes its own")
	_, _, capability := manager.NetworkFamilyCapabilitySnapshot()
	require.Equal(t, netcap.StateUnknown, capability.Capability.IPv6,
		"the dial verdict must be untouched: nothing was published")
}

// failingInterfaceFinder fails its refresh, which is the one observation failure a fixture cannot
// express by being empty (an empty list is a different case: it has no usable interface).
type failingInterfaceFinder struct {
	*testInterfaceFinder
}

func (f *failingInterfaceFinder) Update() error {
	return errors.New("interface enumeration failed")
}

func TestAFailedInterfaceReadIsNotANegative(t *testing.T) {
	manager, _ := entryPresentmentManager(t, netcap.RouteAbsent,
		entryTestInterface(1, "en0", "192.0.2.10/24"))
	manager.interfaceFinder = &failingInterfaceFinder{
		testInterfaceFinder: newTestInterfaceFinder([]control.Interface{
			entryTestInterface(1, "en0", "192.0.2.10/24"),
		}),
	}

	presentment, observed := manager.observeIPv6EntryPresentment()
	require.False(t, observed,
		"a failed enumeration is not evidence that the machine has no IPv6")
	require.Equal(t, netcap.PresentIPv6, presentment)
}

// TestNoDemandMeansNoObservation is the zero-cost promise as an event: a configuration with no
// application-facing IPv6 surface must not enumerate interfaces or read a route table for this
// feature at all.
func TestNoDemandMeansNoObservation(t *testing.T) {
	manager, finder := entryPresentmentManager(t, netcap.RouteAbsent,
		entryTestInterface(1, "en0", "192.0.2.10/24"))

	require.False(t, manager.entryDemand.Load(), "a manager nobody told otherwise demands nothing")
	require.NoError(t, manager.Start(adapter.StartStateStart, &adapter.Scope{}))
	require.Equal(t, 0, finder.updates, "no demand must mean no interface enumeration")
	require.Equal(t, netcap.PresentIPv6, manager.IPv6EntryPresentment())
}

// TestTheStartupObservationPublishesTheDecision is the demanded path through the real start stage.
func TestTheStartupObservationPublishesTheDecision(t *testing.T) {
	manager, finder := entryPresentmentManager(t, netcap.RouteAbsent,
		entryTestInterface(1, "en0", "192.0.2.10/24"))
	manager.SetIPv6EntryDemand(true)

	require.NoError(t, manager.Start(adapter.StartStateStart, &adapter.Scope{}))
	require.Equal(t, 1, finder.updates)
	require.Equal(t, netcap.SuppressIPv6, manager.IPv6EntryPresentment())
}

// TestAFailedStartupObservationPublishesNothing is the fail-open rule through the same entry.
func TestAFailedStartupObservationPublishesNothing(t *testing.T) {
	manager, _ := entryPresentmentManager(t, netcap.RouteAbsent)
	manager.interfaceFinder = &failingInterfaceFinder{testInterfaceFinder: newTestInterfaceFinder(nil)}
	manager.SetIPv6EntryDemand(true)

	require.NoError(t, manager.Start(adapter.StartStateStart, &adapter.Scope{}),
		"an unobservable network must not keep the box from starting")
	require.Equal(t, netcap.PresentIPv6, manager.IPv6EntryPresentment())
}

// TestTheUncommittedAnswerIsPresent covers the state every consumer sees when no decision was taken.
func TestTheUncommittedAnswerIsPresent(t *testing.T) {
	manager, _ := entryPresentmentManager(t, netcap.RouteAbsent)
	require.Equal(t, netcap.PresentIPv6, manager.IPv6EntryPresentment(),
		"an uncommitted decision is 'present': a manager that was never asked keeps the previous behaviour")
}

// TestTheLatestCommitWins is the correction rule.
func TestTheLatestCommitWins(t *testing.T) {
	manager, _ := entryPresentmentManager(t, netcap.RouteAbsent)

	manager.CommitIPv6EntryPresentment(netcap.SuppressIPv6)
	require.Equal(t, netcap.SuppressIPv6, manager.IPv6EntryPresentment())

	// The TUN could not reduce its options, so the interface still presents IPv6 and the correction
	// must be visible to the DNS entry.
	manager.CommitIPv6EntryPresentment(netcap.PresentIPv6)
	require.Equal(t, netcap.PresentIPv6, manager.IPv6EntryPresentment(),
		"the correction by the entry that applied the decision describes the interface really in use")
}

// TestAPublishedInterfaceListIsPreferred keeps the two observation sources consistent.
//
// Once the manager has published the platform's filtered view - which is what its dial verdict is
// computed from - the startup decision must read the same list rather than enumerate again and
// produce a second opinion about the same instant.
func TestAPublishedInterfaceListIsPreferred(t *testing.T) {
	manager, finder := entryPresentmentManager(t, netcap.RoutePresent,
		entryTestInterface(1, "en0", "192.0.2.10/24"))
	manager.networkInterfaces.Store([]adapter.NetworkInterface{{
		Interface: entryTestInterface(1, "en0", "192.0.2.10/24"),
		Type:      C.InterfaceTypeEthernet,
	}})

	presentment, observed := manager.observeIPv6EntryPresentment()
	require.True(t, observed)
	require.Equal(t, netcap.SuppressIPv6, presentment,
		"the published list says IPv4 only, so the decision must follow it")
	require.Equal(t, 0, finder.updates,
		"and reading the published list must not enumerate the interfaces again")
}

// TestAPlatformThatCannotExpressIPv4OnlyDoesNotSuppress is the Darwin delivery rule, stated as the
// two halves that matter.
//
// The pinned Network Extension client builds NEIPv6Settings and a default ::/0 route
// unconditionally, so a core that stripped IPv6 addressing would hand it an interface that claims
// the family and cannot carry it - or settings the system rejects. Until the client patch
// (docs/fork/ipv6-entry-apple.patch) ships in the built client, the decision must stay "present" on
// that platform: the previous behaviour, which the dial gate already covers.
func TestAPlatformThatCannotExpressIPv4OnlyDoesNotSuppress(t *testing.T) {
	manager, finder := entryPresentmentManager(t, netcap.RouteAbsent,
		entryTestInterface(1, "en0", "192.0.2.10/24"))
	manager.SetIPv6EntryDemand(true)
	manager.entryDeliveryRemovesIPv6 = func() bool { return false }

	require.NoError(t, manager.Start(adapter.StartStateStart, &adapter.Scope{}))
	require.Equal(t, 0, finder.updates,
		"a platform that cannot apply the decision must not even observe for it")
	require.Equal(t, netcap.PresentIPv6, manager.IPv6EntryPresentment(),
		"a platform that cannot deliver an ipv4-only interface must not have a suppressed decision "+
			"published: DNS would then refuse AAAA for an interface that still advertises IPv6")
}

// TestTheCommitRefusesAnUndeliverableSuppression is the invariant at the commit site itself, so a
// caller that reaches the observation directly cannot publish a decision the platform cannot carry.
func TestTheCommitRefusesAnUndeliverableSuppression(t *testing.T) {
	manager, _ := entryPresentmentManager(t, netcap.RouteAbsent,
		entryTestInterface(1, "en0", "192.0.2.10/24"))
	manager.entryDeliveryRemovesIPv6 = func() bool { return false }

	manager.observeEntryPresentment()
	require.Equal(t, netcap.PresentIPv6, manager.IPv6EntryPresentment())
}

// TestTheDeliveryAnswerIsThePlatformsOwn is the control: with the platform answer restored, the same
// observation suppresses. Without it the two tests above would pass on a core that never suppresses.
func TestTheDeliveryAnswerIsThePlatformsOwn(t *testing.T) {
	manager, _ := entryPresentmentManager(t, netcap.RouteAbsent,
		entryTestInterface(1, "en0", "192.0.2.10/24"))
	manager.SetIPv6EntryDemand(true)
	manager.entryDeliveryRemovesIPv6 = func() bool { return true }

	require.NoError(t, manager.Start(adapter.StartStateStart, &adapter.Scope{}))
	require.Equal(t, netcap.SuppressIPv6, manager.IPv6EntryPresentment())
}

// TestTheStartupObservationDoesNotResetTheNetwork is I-11 at the stage where the decision is taken.
//
// The observation runs inside NetworkManager.Start(StartStateStart), which is also where other
// per-stage work lives. If it ever reached the reset body - a recompute, a claimed transition, a
// DNS/transport reset - the box's very first query and dial would race a network change that
// describes nothing. The router's reset count is the production counter for exactly that.
func TestTheStartupObservationDoesNotResetTheNetwork(t *testing.T) {
	manager, finder := entryPresentmentManager(t, netcap.RouteAbsent,
		entryTestInterface(1, "en0", "192.0.2.10/24"))
	router := newCountingRouter()
	manager.router = router
	manager.SetIPv6EntryDemand(true)

	require.NoError(t, manager.Start(adapter.StartStateStart, &adapter.Scope{}))
	require.Equal(t, 1, finder.updates, "the observation ran")
	require.Equal(t, 0, router.count(),
		"the startup observation reset the network: the first query and dial would race a "+
			"transition that describes nothing")
	require.Equal(t, uint64(0), manager.NetworkEnvironment(),
		"and it must not publish an environment fingerprint of its own")
	require.Equal(t, uint64(0), manager.NetworkResetGeneration())
}

// TestTheCommittedDecisionDoesNotFollowALaterNetwork is the latch, stated through the production
// publish path rather than through a field write.
//
// The decision belongs to the interface that was opened, not to the network the device is on later:
// no platform in this tree can re-address an open TUN, so a reader that followed the live capability
// would approve AAAA answers for an IPv6 the interface does not carry. The property is asserted in
// both directions of the same run - the DIAL verdict must follow the new network (it is live), and
// the ENTRY decision must not.
//
// The cost is real and is recorded as an open risk: a box that starts on a network proving no local
// IPv6 keeps its IPv4-only interface until it is restarted, even if the device later moves to a
// dual-stack network. The alternative - DNS disagreeing with the interface - is worse, and
// re-addressing the interface is a platform capability this round cannot verify.
func TestTheCommittedDecisionDoesNotFollowALaterNetwork(t *testing.T) {
	harness := newTransitionWindowHarness(t)
	setInterfaces(harness, "192.0.2.1", interfaceWithAddresses(1, "en0", "192.168.1.10/24"))
	harness.settle(t, "wifi-ipv4-only")

	require.Equal(t, netcap.StateUnavailable, currentFamilyCapability(t, harness).Capability.IPv6)
	harness.manager.CommitIPv6EntryPresentment(netcap.SuppressIPv6)
	require.Equal(t, netcap.SuppressIPv6, harness.manager.IPv6EntryPresentment())

	// The device moves to a dual-stack network. The live verdict must follow it.
	setInterfaces(harness, "192.0.2.9",
		interfaceWithAddresses(1, "en0", "192.168.1.10/24", "2001:db8::10/64"))
	harness.settle(t, "cellular-dual-stack")
	require.Equal(t, netcap.StateProvisioned, currentFamilyCapability(t, harness).Capability.IPv6,
		"the dial verdict is live and must describe the network the device is on now")

	// The entry decision must not, because the interface that applied it was never re-addressed.
	require.Equal(t, netcap.SuppressIPv6, harness.manager.IPv6EntryPresentment(),
		"the entry decision followed a network change: DNS would then present an IPv6 the open "+
			"interface does not carry - the split this pipeline exists to prevent")
}

// TestALaterNetworkCannotMakeTheEntryPresentmentUnstable is the same property under repeated
// transitions: the committed value must be a fixed point no transition can move, or a network flap
// would make the DNS answer and the interface disagree transiently.
func TestALaterNetworkCannotMakeTheEntryPresentmentUnstable(t *testing.T) {
	harness := newTransitionWindowHarness(t)
	setInterfaces(harness, "192.0.2.1", interfaceWithAddresses(1, "en0", "192.168.1.10/24", "2001:db8::10/64"))
	harness.settle(t, "A")
	harness.manager.CommitIPv6EntryPresentment(netcap.PresentIPv6)

	for index, ssid := range []string{"B", "C", "D"} {
		setInterfaces(harness, "192.0.2."+strconv.Itoa(index+2),
			interfaceWithAddresses(1, "en0", "192.168.1.10/24"))
		harness.settle(t, ssid)
		require.Equal(t, netcap.PresentIPv6, harness.manager.IPv6EntryPresentment(),
			"transition ", index, " moved the committed entry decision")
	}
}
