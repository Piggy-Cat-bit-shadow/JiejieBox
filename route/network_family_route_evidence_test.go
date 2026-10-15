package route

import (
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/netcap"
	C "github.com/sagernet/sing-box/constant"

	"github.com/stretchr/testify/require"
)

// The route-evidence half of the local family capability, at the level that publishes it.
//
// common/netcap's tests prove the MODEL and common/dialer's prove the GATE. These prove the WIRING
// from a NetworkManager to both: that the verdict the manager publishes reflects the machine's
// ROUTES, that it is owned by a network generation like every other verdict, and that the platform
// reader is consulted on the event path rather than per dial.
//
// The route observation is injected because the state under test is the ABSENCE of a global IPv6
// route, and a developer's machine cannot be put into that state on request - unlike the interface
// list, where a fixture simply supplies the interfaces it wants.

// linkLocalOnlyInterfaces is the reported failing machine: an IPv4 address and a fe80:: link-local
// address, and nothing else.
func linkLocalOnlyInterfaces() []adapter.NetworkInterface {
	return []adapter.NetworkInterface{
		interfaceOn(1, "Ethernet", C.InterfaceTypeEthernet, true, "192.168.1.10/24", "fe80::a1b2:c3d4:e5f6:7890/64"),
	}
}

// TestAGenerationWithoutAGlobalIPv6RoutePublishesTheEgressVerdict is the reported failure at the
// publishing level: the capability a dialer would read must say "no public IPv6" while still saying
// "IPv6 exists".
func TestAGenerationWithoutAGlobalIPv6RoutePublishesTheEgressVerdict(t *testing.T) {
	manager, _ := newInterfaceOrderingManager(t)
	manager.routeObservation = func() netcap.RouteObservation { return netcap.RouteAbsent }
	h := &transitionWindowHarness{manager: manager}

	setInterfaces(h, "192.0.2.1", linkLocalOnlyInterfaces()...)
	h.settle(t, "ethernet-ipv4-and-link-local")

	capability := currentFamilyCapability(t, h).Capability
	require.Equal(t, netcap.StateProvisioned, capability.IPv6,
		"IPv6 exists on this machine and saying otherwise would be wrong")
	require.Equal(t, netcap.StateUnavailable, capability.GlobalIPv6Egress,
		"and no global IPv6 destination is reachable, which is what the log showed on every attempt")

	require.True(t, capability.HardUndialable(netip.MustParseAddr("2409:8a00::1")))
	require.False(t, capability.HardUndialable(netip.MustParseAddr("fe80::1")),
		"the link-local destination the fe80:: address exists for is still reachable")
	require.False(t, capability.HardUndialable(netip.MustParseAddr("192.0.2.1")))
}

// TestAGenerationWithAGlobalIPv6RouteKeepsIPv6Dialable is the control that stops the term from
// becoming a blanket IPv6 refusal.
func TestAGenerationWithAGlobalIPv6RouteKeepsIPv6Dialable(t *testing.T) {
	manager, _ := newInterfaceOrderingManager(t)
	manager.routeObservation = func() netcap.RouteObservation { return netcap.RoutePresent }
	h := &transitionWindowHarness{manager: manager}

	setInterfaces(h, "192.0.2.1", interfaceOn(1, "Ethernet", C.InterfaceTypeEthernet, true,
		"192.168.1.10/24", "2409:8a00::10/64"))
	h.settle(t, "ethernet-dual-stack")

	capability := currentFamilyCapability(t, h).Capability
	require.Equal(t, netcap.StateProvisioned, capability.GlobalIPv6Egress)
	require.False(t, capability.HardUndialable(netip.MustParseAddr("2409:8a00::1")),
		"a machine with a global IPv6 route must keep dialling global IPv6")
}

// TestAnUnreadableRouteTablePublishesNoEgressVerdict is the degradation contract, and the one that
// decides whether this feature is safe to ship on platforms whose reader does not exist.
//
// A platform with no reader, or a read that failed, must produce the SAME behaviour the code had
// before this round - not a guess in either direction.
func TestAnUnreadableRouteTablePublishesNoEgressVerdict(t *testing.T) {
	for name, observation := range map[string]netcap.RouteObservation{
		"unobserved": netcap.RouteUnobserved,
	} {
		t.Run(name, func(t *testing.T) {
			manager, _ := newInterfaceOrderingManager(t)
			manager.routeObservation = func() netcap.RouteObservation { return observation }
			h := &transitionWindowHarness{manager: manager}

			setInterfaces(h, "192.0.2.1", linkLocalOnlyInterfaces()...)
			h.settle(t, "ethernet-ipv4-and-link-local")

			capability := currentFamilyCapability(t, h).Capability
			require.Equal(t, netcap.StateUnknown, capability.GlobalIPv6Egress,
				"nothing was read, so nothing may be claimed")
			require.False(t, capability.HardUndialable(netip.MustParseAddr("2409:8a00::1")),
				"an unproven absence must not refuse a destination: this is the behaviour the tree "+
					"had before the egress term existed, and it is what every platform without a "+
					"route reader must keep")
		})
	}
}

// TestTheEgressVerdictMovesOnlyWithItsGeneration is the ownership assertion, and the reason the route
// term is safe to add to a model that already has generation semantics.
//
// A route-only change is the case that nothing else in the tree can see: the addresses are identical,
// so sing-tun's own change detection reports "unchanged" and the environment fingerprint does not
// move either. The capability must therefore be re-derived from the route observation on every
// recompute - if the verdict were cached alongside the fingerprint, a route that disappeared would
// leave the stale verdict standing for as long as the network lasted.
func TestTheEgressVerdictMovesOnlyWithItsGeneration(t *testing.T) {
	manager, _ := newInterfaceOrderingManager(t)
	routeState := netcap.RoutePresent
	manager.routeObservation = func() netcap.RouteObservation { return routeState }
	h := &transitionWindowHarness{manager: manager}

	setInterfaces(h, "192.0.2.1", interfaceOn(1, "Ethernet", C.InterfaceTypeEthernet, true,
		"192.168.1.10/24", "2409:8a00::10/64"))
	h.settle(t, "ethernet-dual-stack")

	before := currentFamilyCapability(t, h)
	require.Equal(t, netcap.StateProvisioned, before.Capability.GlobalIPv6Egress)
	epochBefore := manager.NetworkResetGeneration()

	// The route disappears and NOTHING else changes - no address, no gateway, no interface.
	routeState = netcap.RouteAbsent
	h.settle(t, "ethernet-dual-stack")

	after := currentFamilyCapability(t, h)
	require.Equal(t, netcap.StateUnavailable, after.Capability.GlobalIPv6Egress,
		"the verdict must follow the route table, not the address list")
	require.NotEqual(t, before.Capability, after.Capability,
		"and the two capabilities must not compare equal, or a publisher that detects 'nothing "+
			"changed' with == would keep the stale verdict")
	require.Equal(t, epochBefore, manager.NetworkResetGeneration(),
		"a route-only change is the same network: it must not advance the generation, because "+
			"nothing about the environment this epoch names has moved")

	// And it comes back, without the process being restarted.
	routeState = netcap.RoutePresent
	h.settle(t, "ethernet-dual-stack")
	require.Equal(t, netcap.StateProvisioned, currentFamilyCapability(t, h).Capability.GlobalIPv6Egress,
		"a restored route must restore the verdict rather than leaving IPv6 refused for the life of "+
			"the process")
}

// TestTheRouteReaderIsNotConsultedPerDial is the cost assertion.
//
// The route observation is read on the EVENT - the environment recompute - and never from a dial,
// which is what keeps this feature off the connection hot path entirely. If it were read per dial this
// test would see the counter climb on every call to the capability.
func TestTheRouteReaderIsNotConsultedPerDial(t *testing.T) {
	manager, _ := newInterfaceOrderingManager(t)
	var reads int
	manager.routeObservation = func() netcap.RouteObservation {
		reads++
		return netcap.RouteAbsent
	}
	h := &transitionWindowHarness{manager: manager}

	setInterfaces(h, "192.0.2.1", linkLocalOnlyInterfaces()...)
	h.settle(t, "ethernet-ipv4-and-link-local")
	afterSettle := reads
	require.Positive(t, afterSettle, "the event path must read the route table")

	// Reading the published verdict a thousand times must cost nothing: it is an atomic load of a
	// value that was derived on the event.
	for i := 0; i < 1000; i++ {
		_, _, capability := manager.NetworkFamilyCapabilitySnapshot()
		require.True(t, capability.Capability.HardUndialable(netip.MustParseAddr("2409:8a00::1")))
	}
	require.Equal(t, afterSettle, reads,
		"reading the verdict must not re-read the route table: the observation belongs to the "+
			"generation, not to the connection")
}
