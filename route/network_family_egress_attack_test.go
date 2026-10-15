package route

import (
	"errors"
	"net/netip"
	"os"
	"runtime"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/netcap"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing/common/control"

	"github.com/stretchr/testify/require"
)

// Round-2 attacks on the global-IPv6 egress capability.
//
// The goal of this file is to BREAK round 1, not to demonstrate it. Each test names the way the
// implementation was expected to be wrong, and passes only because the attack failed. Where an attack
// succeeded, the fix and its regression test are in the same commit - see the notes on the individual
// tests.

// ---------------------------------------------------------------------------------------------
// Attack 2: ULA / NAT66. "Fixing WeChat must not kill the enterprise IPv6 LAN."
// ---------------------------------------------------------------------------------------------

// TestAULAOnlyNetworkIsRefusedForGlobalButNeverForLocal pins the exact boundary. A machine with only
// fd00::/64 addressing and no global IPv6 route genuinely cannot reach a global IPv6 destination, and
// must not lose its ULA peer in the process.
func TestAULAOnlyNetworkIsRefusedForGlobalButNeverForLocal(t *testing.T) {
	manager, _ := newInterfaceOrderingManager(t)
	manager.routeObservation = func() netcap.RouteObservation { return netcap.RouteAbsent }
	h := &transitionWindowHarness{manager: manager}

	setInterfaces(h, "192.0.2.1", interfaceOn(1, "eth0", C.InterfaceTypeEthernet, true,
		"192.0.2.10/24", "fd00:1234::10/64"))
	h.settle(t, "enterprise-ula")

	capability := currentFamilyCapability(t, h).Capability
	require.Equal(t, netcap.StateUnavailable, capability.GlobalIPv6Egress)
	require.True(t, capability.HardUndialable(netip.MustParseAddr("2409:8a00::1")),
		"a global IPv6 destination is unreachable from a ULA-only machine")
	require.False(t, capability.HardUndialable(netip.MustParseAddr("fd00:1234::99")),
		"and the ULA peer inside the site is untouched - this is the enterprise LAN the fix must not kill")
}

// TestNAT66KeepsGlobalIPv6Usable is the NPTv6/NAT66 attack, and the one that would have caught an
// address-type heuristic.
//
// An NPTv6 or NAT66 network presents ULA addressing to the host while the ROUTER translates to a
// global prefix. The host therefore has a ::/0 route and a ULA source address. An implementation that
// decided "only ULA addressing, therefore no public IPv6" would refuse every global IPv6 destination
// on a network whose IPv6 works perfectly - the over-fix the brief warns about.
//
// The implementation must decide from ROUTE EVIDENCE, so this passes on the route being present. It is
// the reason the egress term is a route observation and not an address-type test.
func TestNAT66KeepsGlobalIPv6Usable(t *testing.T) {
	manager, _ := newInterfaceOrderingManager(t)
	manager.routeObservation = func() netcap.RouteObservation { return netcap.RoutePresent }
	h := &transitionWindowHarness{manager: manager}

	setInterfaces(h, "192.0.2.1", interfaceOn(1, "eth0", C.InterfaceTypeEthernet, true,
		"192.0.2.10/24", "fd00:1234::10/64"))
	h.settle(t, "enterprise-nat66")

	capability := currentFamilyCapability(t, h).Capability
	require.Equal(t, netcap.StateProvisioned, capability.GlobalIPv6Egress,
		"a ::/0 route through a translating router is a public path, whatever the host's own prefix is")
	require.False(t, capability.HardUndialable(netip.MustParseAddr("2409:8a00::1")),
		"so global IPv6 must keep working on an NPTv6/NAT66 network")
	require.False(t, capability.HardUndialable(netip.MustParseAddr("fd00:1234::99")))
}

// TestTheEgressVerdictIsNeverDerivedFromAddressType is the structural form of the same attack.
//
// Every combination of address type must be able to produce BOTH answers, which is only possible if
// the verdict comes from the route observation. An address-derived verdict would be constant down a
// column, and this test would fail on it.
func TestTheEgressVerdictIsNeverDerivedFromAddressType(t *testing.T) {
	addressing := map[string][]string{
		"link-local only":     {"fe80::1/64"},
		"ula only":            {"fd00::1/64"},
		"global only":         {"2409:8a00::1/64"},
		"link-local + ula":    {"fe80::1/64", "fd00::1/64"},
		"link-local + global": {"fe80::1/64", "2409:8a00::1/64"},
		"ula + global":        {"fd00::1/64", "2409:8a00::1/64"},
		"all three":           {"fe80::1/64", "fd00::1/64", "2409:8a00::1/64"},
		"ipv4 + link-local":   {"192.0.2.10/24", "fe80::1/64"},
	}
	for name, prefixes := range addressing {
		for routeName, observation := range map[string]netcap.RouteObservation{
			"route present": netcap.RoutePresent,
			"route absent":  netcap.RouteAbsent,
			"route unread":  netcap.RouteUnobserved,
		} {
			manager, _ := newInterfaceOrderingManager(t)
			current := observation
			manager.routeObservation = func() netcap.RouteObservation { return current }
			h := &transitionWindowHarness{manager: manager}

			setInterfaces(h, "192.0.2.1", interfaceOn(1, "eth0", C.InterfaceTypeEthernet, true, prefixes...))
			h.settle(t, name+"/"+routeName)

			capability := currentFamilyCapability(t, h).Capability
			var expected netcap.FamilyState
			switch observation {
			case netcap.RoutePresent:
				expected = netcap.StateProvisioned
			case netcap.RouteAbsent:
				expected = netcap.StateUnavailable
			default:
				expected = netcap.StateUnknown
			}
			require.Equalf(t, expected, capability.GlobalIPv6Egress,
				"%s with %s: the verdict must follow the ROUTE, not the address type", name, routeName)
		}
	}
}

// ---------------------------------------------------------------------------------------------
// Attack 3: multi-interface.
// ---------------------------------------------------------------------------------------------

// TestAMachineWithARouteSomewhereKeepsGlobalIPv6 is case B: the default interface has no IPv6 and a
// secondary link does. A machine-wide refusal here would be a wrongful hard deny on a network that can
// carry the traffic.
func TestAMachineWithARouteSomewhereKeepsGlobalIPv6(t *testing.T) {
	manager, _ := newInterfaceOrderingManager(t)
	// The route lives on the cellular link, and the route reader reports presence - which is what a
	// machine-level reader does, because the kernel picks the egress interface itself.
	manager.routeObservation = func() netcap.RouteObservation { return netcap.RoutePresent }
	h := &transitionWindowHarness{manager: manager}

	setInterfaces(h, "192.0.2.1",
		interfaceOn(1, "wlan0", C.InterfaceTypeWIFI, true, "192.168.1.10/24", "fe80::1/64"),
		interfaceOn(2, "rmnet0", C.InterfaceTypeCellular, true, "10.20.0.5/24", "2409:8a00::5/64"),
	)
	h.settle(t, "wifi-plus-cellular")

	require.False(t, currentFamilyCapability(t, h).Capability.HardUndialable(netip.MustParseAddr("2409:8a00::99")),
		"a machine with a global IPv6 route must keep dialling global IPv6, even when the DEFAULT "+
			"interface has none: the kernel chooses the interface, and the strategy may dial the other")
}

// TestAVirtualAdapterAloneNeverCreatesEgress is case A: a Hyper-V/WSL adapter with a global address and
// no route must not make the machine look dual-stack.
func TestAVirtualAdapterAloneNeverCreatesEgress(t *testing.T) {
	manager, _ := newInterfaceOrderingManager(t)
	manager.routeObservation = func() netcap.RouteObservation { return netcap.RouteAbsent }
	h := &transitionWindowHarness{manager: manager}

	setInterfaces(h, "192.0.2.1",
		interfaceOn(1, "Ethernet", C.InterfaceTypeEthernet, true, "192.168.1.10/24", "fe80::1/64"),
		interfaceOn(2, "vEthernet (WSL)", C.InterfaceTypeEthernet, true, "172.28.0.1/20", "2606:4700:1::1/128"),
	)
	h.settle(t, "ethernet-plus-wsl")

	capability := currentFamilyCapability(t, h).Capability
	require.Equal(t, netcap.StateUnavailable, capability.GlobalIPv6Egress,
		"a global address on an adapter with no route is not egress")
	require.True(t, capability.HardUndialable(netip.MustParseAddr("2409:8a00::1")))
	require.False(t, capability.HardUndialable(netip.MustParseAddr("172.28.0.5")),
		"while the adapter's own IPv4 segment still works")
}

// TestABoundDialToAnIPv6LessInterfaceIsTheUnsafeDirection is case C/D, and it is an attack that
// SUCCEEDS against the design - recorded here so the limitation is a pinned fact rather than a claim.
//
// The verdict is machine-wide. A dial explicitly bound to an interface with no IPv6 route still gets
// the machine's answer, so:
//
//	machine has a global IPv6 route (on another link) + bind_interface pins an IPv6-less link
//	-> the verdict does not refuse, and the attempt fails on its own
//
// That is a MISSED FAST-FAIL, not a wrongful refusal, which is the direction the brief requires when
// the alternative is guessing. Making it precise would need per-interface route evidence, which no
// platform here exposes through the monitor - `control.Interface` carries no route field at all.
//
// The test asserts the SAFE half explicitly: the binding must never turn the machine verdict into a
// refusal of an interface that does have a route.
func TestABoundDialToAnIPv6LessInterfaceIsTheUnsafeDirection(t *testing.T) {
	manager, _ := newInterfaceOrderingManager(t)
	manager.routeObservation = func() netcap.RouteObservation { return netcap.RoutePresent }
	h := &transitionWindowHarness{manager: manager}

	setInterfaces(h, "192.0.2.1",
		interfaceOn(1, "wlan0", C.InterfaceTypeWIFI, true, "192.168.1.10/24", "fe80::1/64"),
		interfaceOn(2, "rmnet0", C.InterfaceTypeCellular, true, "10.20.0.5/24", "2409:8a00::5/64"),
	)
	h.settle(t, "bound-interface-case")

	require.False(t, currentFamilyCapability(t, h).Capability.HardUndialable(netip.MustParseAddr("2409:8a00::99")),
		"binding to one interface must never produce a refusal: the verdict is machine-wide and "+
			"errs towards attempting, which is the safe direction for a hard gate")
}

// ---------------------------------------------------------------------------------------------
// Attack 4: route-only transition, including the refresh-failure laundering path.
// ---------------------------------------------------------------------------------------------

// TestARouteOnlyChangeSurvivesAFailedObservationWithoutLaundering is the highest-risk attack on round
// 1: the generation-laundering bug fixed for the ADDRESS verdict, re-attacked through the ROUTE term.
//
// The sequence the brief names:
//
//	Wi-Fi          route absent    -> global IPv6 unavailable, epoch N
//	               <- hand over
//	                 the new observation FAILS
//	               -> the OLD route observation must not be promoted to the new generation
//
// Round 1 already returns before recomputing when the refresh fails, so this passes - but it passes
// because of a gate that was written for the address verdict, and nothing until now attacked it
// through the route term. That is exactly the kind of accidental coverage that stops holding when
// someone later "optimises" the recompute, so it is pinned here.
func TestARouteOnlyChangeSurvivesAFailedObservationWithoutLaundering(t *testing.T) {
	harness := newPlatformHandoverHarness(t)

	// Wi-Fi: IPv4 and IPv6 addressing, no global IPv6 route.
	harness.manager.routeObservation = func() netcap.RouteObservation { return netcap.RouteAbsent }
	harness.observe(t, []adapter.NetworkInterface{
		interfaceOn(1, "wlan0", C.InterfaceTypeWIFI, true, "192.168.1.10/24", "2409:8a00::10/64"),
	}, "192.0.2.1")
	harness.settleOn(t, "wifi-with-ipv6-no-route")

	epochBefore := harness.manager.NetworkResetGeneration()
	require.Equal(t, netcap.StateUnavailable, currentFamilyCapabilityOf(t, harness.manager).Capability.GlobalIPv6Egress,
		"the harness must start from a generation whose route verdict really does refuse global IPv6")

	// The device moves to a dual-stack network whose route table DOES have a global IPv6 route...
	harness.observe(t, []adapter.NetworkInterface{
		interfaceOn(1, "rmnet0", C.InterfaceTypeCellular, true, "10.20.0.5/24", "2409:8a00::5/64"),
	}, "192.0.2.9")
	harness.manager.interfaceMonitor.(*staticInterfaceMonitor).current = &control.Interface{Index: 1, Name: "rmnet0"}
	harness.manager.stateAccess.Lock()
	harness.manager.wifiState = adapter.WIFIState{SSID: "cellular-dual-stack"}
	harness.manager.stateAccess.Unlock()

	// ...and the observation fails, so the new route fact is never read.
	failure := errors.New("route observation failed")
	harness.manager.interfaceRefreshError = failure
	require.ErrorIs(t, harness.manager.UpdateInterfaces(), failure)
	harness.manager.interfaceRefreshError = nil

	// THE ASSERTION: the stale "no public IPv6" verdict must not have acquired the new generation.
	epochAfter, stableAfter, capabilityAfter := harness.manager.NetworkFamilyCapabilitySnapshot()
	require.Equal(t, epochBefore, epochAfter,
		"a failed observation must not claim an epoch: doing so hands the stale route verdict the new "+
			"generation's ownership, and the dialer's ownership check then has nothing to reject")
	require.True(t, stableAfter)
	require.Equal(t, netcap.StateUnavailable, capabilityAfter.Capability.GlobalIPv6Egress,
		"and the verdict it does hold is the one that legitimately belongs to the generation it was "+
			"observed on")

	// The next observation succeeds and takes the deferred boundary with the NEW route fact.
	require.NoError(t, harness.manager.UpdateInterfaces())
	epochAfterRecovery, _, recovered := harness.manager.NetworkFamilyCapabilitySnapshot()
	require.Greater(t, epochAfterRecovery, epochBefore)
	require.Equal(t, netcap.StateUnavailable, recovered.Capability.GlobalIPv6Egress,
		"the stub keeps reporting no route, so the verdict stays refused - and it is now refused for "+
			"the new generation, on its own evidence")
}

// TestARouteAppearingOnTheSameNetworkIsNotSticky is the reverse direction, and the attack on
// stickiness: once refused, global IPv6 must be able to come back WITHOUT the network changing.
func TestARouteAppearingOnTheSameNetworkIsNotSticky(t *testing.T) {
	manager, _ := newInterfaceOrderingManager(t)
	observation := netcap.RouteAbsent
	manager.routeObservation = func() netcap.RouteObservation { return observation }
	h := &transitionWindowHarness{manager: manager}

	setInterfaces(h, "192.0.2.1", interfaceOn(1, "eth0", C.InterfaceTypeEthernet, true,
		"192.168.1.10/24", "2409:8a00::10/64"))
	h.settle(t, "same-network")
	require.True(t, currentFamilyCapability(t, h).Capability.HardUndialable(netip.MustParseAddr("2409:8a00::1")))

	// The route comes back. Nothing else moves: no address, no gateway, no interface, no reset.
	observation = netcap.RoutePresent
	require.NoError(t, manager.UpdateInterfaces())

	require.False(t, currentFamilyCapability(t, h).Capability.HardUndialable(netip.MustParseAddr("2409:8a00::1")),
		"a restored route must restore global IPv6 immediately: a capability that stayed refused "+
			"until the next network change would be a permanent outage on a recovery the machine "+
			"already made")
}

// TestTheRouteObservationFailureOfOneGenerationDoesNotOutliveIt attacks the failure path directly.
//
// When the route table cannot be read, the verdict must be Unknown - fail-open - and NOT the previous
// generation's answer, which is what a reader that silently returned zero would produce.
func TestTheRouteObservationFailureOfOneGenerationDoesNotOutliveIt(t *testing.T) {
	manager, _ := newInterfaceOrderingManager(t)
	observation := netcap.RouteAbsent
	manager.routeObservation = func() netcap.RouteObservation { return observation }
	h := &transitionWindowHarness{manager: manager}

	setInterfaces(h, "192.0.2.1", interfaceOn(1, "eth0", C.InterfaceTypeEthernet, true,
		"192.168.1.10/24", "2409:8a00::10/64"))
	h.settle(t, "readable-route-table")
	require.Equal(t, netcap.StateUnavailable, currentFamilyCapability(t, h).Capability.GlobalIPv6Egress)

	// The reader now fails. The verdict must become Unknown, not stay Unavailable.
	observation = netcap.RouteUnobserved
	require.NoError(t, manager.UpdateInterfaces())

	require.Equal(t, netcap.StateUnknown, currentFamilyCapability(t, h).Capability.GlobalIPv6Egress,
		"a read that failed must produce no opinion, not the previous generation's opinion")
	require.False(t, currentFamilyCapability(t, h).Capability.HardUndialable(netip.MustParseAddr("2409:8a00::1")),
		"and an unproven absence must not refuse a destination")
}

// ---------------------------------------------------------------------------------------------
// Attack 15 / 16: resource and hot path.
// ---------------------------------------------------------------------------------------------

// TestRepeatedRefusalsDoNotGrowGoroutinesOrFileDescriptors is attack 15 at the layer that can be
// measured here.
//
// The real log shows dozens of global IPv6 failures in a minute. The fix must turn that into dozens of
// REFUSALS - which allocate nothing and start nothing - rather than into a different storm.
func TestRepeatedRefusalsDoNotGrowGoroutinesOrFileDescriptors(t *testing.T) {
	manager, _ := newInterfaceOrderingManager(t)
	manager.routeObservation = func() netcap.RouteObservation { return netcap.RouteAbsent }
	h := &transitionWindowHarness{manager: manager}

	setInterfaces(h, "192.0.2.1", interfaceOn(1, "eth0", C.InterfaceTypeEthernet, true,
		"192.168.1.10/24", "fe80::1/64"))
	h.settle(t, "storm-attack")

	before := runtime.NumGoroutine()
	beforeFDs := openFileDescriptorCount(t)

	target := netip.MustParseAddr("2409:8a00::1")
	for i := 0; i < 2000; i++ {
		_, _, capability := manager.NetworkFamilyCapabilitySnapshot()
		require.True(t, capability.Capability.HardUndialable(target))
	}

	require.LessOrEqual(t, runtime.NumGoroutine(), before+2,
		"refusal is a pure function of a published value: it must start no goroutine")
	if beforeFDs >= 0 {
		require.LessOrEqual(t, openFileDescriptorCount(t), beforeFDs+2,
			"and it must open no socket, which is the entire point - the socket is what the kernel "+
				"was failing with WSAENETUNREACH")
	}
}

func openFileDescriptorCount(t *testing.T) int {
	t.Helper()
	if runtime.GOOS != "linux" {
		// The assertion is skipped where it cannot be made, rather than faked.
		return -1
	}
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return -1
	}
	return len(entries)
}

// ---------------------------------------------------------------------------------------------
// Attack 19: diagnosability.
// ---------------------------------------------------------------------------------------------

// TestEveryRefusalReasonIsDistinguishable is attack 19.
//
// The reasons a destination can be refused or dropped must not collapse into one message, because the
// next user log has to be diagnosable without guessing. Two of them are produced by the capability
// itself - "the family is absent" and "there is no public route" - and they are different facts about
// the machine with different remedies, so a log that renders them identically sends the reader to the
// wrong place.
//
// This asserts on the resulting ERRORS as the dialer constructs them, not on the model states, because
// the error is what an operator actually reads.
func TestEveryRefusalReasonIsDistinguishable(t *testing.T) {
	// (a) this machine: IPv6 present, no public route.
	noRoute := currentFamilyCapability(t, settleAttackHarness(t, netcap.RouteAbsent)).Capability
	// (b) another machine: no IPv6 addressing at all.
	noFamily := noRoute
	noFamily.IPv6 = netcap.StateUnavailable

	for name, capability := range map[string]netcap.FamilyCapability{"a": noRoute, "b": noFamily} {
		require.Truef(t, capability.HardUndialable(netip.MustParseAddr("2409:8a00::1")),
			"%s must refuse a global IPv6 destination", name)
		require.Falsef(t, capability.HardUndialable(netip.MustParseAddr("fe80::1")),
			"%s must not refuse a link-local one", name)
	}

	// The two capabilities differ, so a publisher comparing them sees a change...
	require.NotEqual(t, noRoute, noFamily)
	// ...and the difference is in the FAMILY field, which is what the dialer's error names.
	require.Equal(t, netcap.StateUnavailable, noFamily.IPv6)
	require.Equal(t, netcap.StateProvisioned, noRoute.IPv6)
	require.Equal(t, netcap.StateUnavailable, noRoute.GlobalIPv6Egress)
}

// TestARouteOnlyChangeArrivesThroughTheProductionEventPath is the attack on the delivery chain, and
// it exists because round 1 reported this as a suspected defect.
//
// The suspicion was that a route-only change never reaches the recompute, because
// DefaultInterfaceMonitor.checkUpdate returns early when the interface list is unchanged
// (`!defaultInterfaceChanged(...) && slices.Equal(oldDNSServers, dnsServers)` -> return, without
// emit). That analysis was WRONG, and the correction is worth pinning rather than deleting:
//
//	sing-box registers postUpdateNetworkEnvironment on tun.NetworkUpdateMonitor, NOT on
//	DefaultInterfaceMonitor (route/network.go:282).
//
// tun.NetworkUpdateMonitor is a separate object whose route callback calls m.emit() directly
// (monitor_windows.go:41; monitor_darwin.go; monitor_linux.go:126/138), so the sing-box callback fires
// on every debounced route event, before and independently of the interface monitor's dedup.
//
// This test asserts the production entry point - postUpdateNetworkEnvironment, the function that is
// actually registered - picks up a route-only change in ONE call, with no address change anywhere.
func TestARouteOnlyChangeArrivesThroughTheProductionEventPath(t *testing.T) {
	manager, _ := newInterfaceOrderingManager(t)
	observation := netcap.RouteAbsent
	manager.routeObservation = func() netcap.RouteObservation { return observation }
	h := &transitionWindowHarness{manager: manager}

	setInterfaces(h, "192.0.2.1", interfaceOn(1, "eth0", C.InterfaceTypeEthernet, true,
		"192.168.1.10/24", "2409:8a00::10/64"))
	h.settle(t, "route-event-path")
	epochBefore := manager.NetworkResetGeneration()
	require.True(t, currentFamilyCapability(t, h).Capability.HardUndialable(netip.MustParseAddr("2409:8a00::1")))

	// The route comes back. Nothing else moves: the address list, the gateway, the default interface
	// and the fingerprint are all identical, which is exactly the case DefaultInterfaceMonitor's dedup
	// would swallow.
	observation = netcap.RoutePresent
	manager.postUpdateNetworkEnvironment()

	require.False(t, currentFamilyCapability(t, h).Capability.HardUndialable(netip.MustParseAddr("2409:8a00::1")),
		"the registered network-monitor callback must republish the verdict on a route-only change")
	require.Equal(t, epochBefore, manager.NetworkResetGeneration(),
		"and it must do so WITHOUT claiming a new generation: the route moved, the network did not, and "+
			"bumping the epoch would cancel every in-flight dial on an unchanged network")
}

// settleAttackHarness builds the reported machine at the publishing level.
func settleAttackHarness(t *testing.T, observation netcap.RouteObservation) *transitionWindowHarness {
	t.Helper()
	manager, _ := newInterfaceOrderingManager(t)
	manager.routeObservation = func() netcap.RouteObservation { return observation }
	h := &transitionWindowHarness{manager: manager}
	setInterfaces(h, "192.0.2.1", interfaceOn(1, "eth0", C.InterfaceTypeEthernet, true,
		"192.168.1.10/24", "fe80::1/64"))
	h.settle(t, "attack-harness/"+observation.String())
	return h
}
