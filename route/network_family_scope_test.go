package route

import (
	"net"
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/netcap"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing/common/control"

	"github.com/stretchr/testify/require"
)

// Layer 4 of the debug round: multi-interface and network_strategy scope.
//
// # The failure this layer exists to prevent
//
// One global "IPv6 is unavailable" flag is wrong the moment a device has two underlays that disagree.
// The case is ordinary rather than exotic: a phone whose Wi-Fi hands out IPv4 only while its cellular
// radio is dual-stack, or a laptop on a wired link with no IPv6 delegation and a Wi-Fi association
// that has it. A verdict derived from the default interface alone would kill IPv6 on a dial that the
// network strategy was about to send out of the OTHER interface - a capability creating the exact
// outage it exists to prevent.
//
// # What makes the machine-level verdict sound
//
// The hard verdict is not "the default interface has no IPv6". It is "NO interface on this machine
// carries an IPv6 address". That is a weaker statement, and every case below is a consequence:
//
//   - Wi-Fi IPv4-only + cellular dual-stack  -> IPv6 is PROVISIONED, and the dialer is free to race
//     it out of the cellular interface
//   - every interface IPv4-only              -> IPv6 is UNAVAILABLE, and no underlay could have
//     carried it, so the refusal cannot cost a connection
//   - "cannot say" (nothing to look at)      -> UNKNOWN, which restricts nothing
//
// The deliberately accepted cost is stated in the last test: a dial BOUND to a specific interface gets
// the machine's answer rather than that interface's, which can refuse an address a different interface
// could have reached. That is the safe direction - a missed fast-fail, never a refused connection
// that would have worked - and it is the same direction the whole model fails in.

// interfaceOn builds the list entry a test wants, with an explicit up/down state.
func interfaceOn(index int, name string, interfaceType C.InterfaceType, up bool, addresses ...string) adapter.NetworkInterface {
	parsed := make([]netip.Prefix, 0, len(addresses))
	for _, address := range addresses {
		parsed = append(parsed, netip.MustParsePrefix(address))
	}
	flags := net.Flags(0)
	if up {
		flags = net.FlagUp | net.FlagRunning
	}
	return adapter.NetworkInterface{
		Interface: control.Interface{Index: index, Name: name, Flags: flags, Addresses: parsed},
		Type:      interfaceType,
	}
}

// TestWiFiWithoutIPv6DoesNotKillIPv6OnCellular is the decisive multi-interface case.
//
// The default interface is the Wi-Fi one, and it has no IPv6. If the verdict were derived from the
// default interface - or from a single global, default-interface-shaped flag - IPv6 would be reported
// as unavailable and a strategy that prefers or falls back to cellular would never get to try it.
func TestWiFiWithoutIPv6DoesNotKillIPv6OnCellular(t *testing.T) {
	h := newTransitionWindowHarness(t)
	h.manager.interfaceMonitor = &staticInterfaceMonitor{
		// The device is currently ON the IPv4-only Wi-Fi.
		current: &control.Interface{Index: 1, Name: "wlan0"},
	}
	setInterfaces(h, "192.0.2.1",
		interfaceOn(1, "wlan0", C.InterfaceTypeWIFI, true, "192.168.1.10/24"),
		interfaceOn(2, "rmnet0", C.InterfaceTypeCellular, true, "10.20.0.5/24", "2001:db8::5/64"),
	)
	h.settle(t, "wifi-no-ipv6")

	capability := currentFamilyCapability(t, h).Capability
	require.Equal(t, netcap.StateProvisioned, capability.IPv4)
	require.Equal(t, netcap.StateProvisioned, capability.IPv6,
		"IPv6 exists on an interface this machine can dial out of, so it must stay attemptable - the "+
			"current default interface having none is not the question the hard verdict answers")
	require.False(t, capability.HardUnavailable(false))
}

// TestNoInterfaceCarryingAFamilyIsStillUnavailable is the other side: broadening the scope must not
// have made the verdict unreachable.
//
// Every interface the machine can dial out of is IPv4-only, so no underlay could carry an IPv6 attempt
// and refusing it cannot cost a connection. Without this, the multi-interface fix above could be
// "satisfied" by simply never reporting anything.
func TestNoInterfaceCarryingAFamilyIsStillUnavailable(t *testing.T) {
	h := newTransitionWindowHarness(t)
	h.manager.interfaceMonitor = &staticInterfaceMonitor{
		current: &control.Interface{Index: 1, Name: "wlan0"},
	}
	setInterfaces(h, "192.0.2.1",
		interfaceOn(1, "wlan0", C.InterfaceTypeWIFI, true, "192.168.1.10/24"),
		interfaceOn(2, "rmnet0", C.InterfaceTypeCellular, true, "10.20.0.5/24"),
	)
	h.settle(t, "neither-interface-has-ipv6")

	capability := currentFamilyCapability(t, h).Capability
	require.Equal(t, netcap.StateProvisioned, capability.IPv4)
	require.Equal(t, netcap.StateUnavailable, capability.IPv6,
		"the verdict must still be reachable, or the multi-interface scope would have disabled it")
}

// TestDownInterfacesAreNotEvidence pins that addressing counts only where it can be used.
//
// A disconnected or administratively down interface can still carry a stale address, and counting it
// would report a family as present on the strength of a link that cannot carry a packet.
func TestDownInterfacesAreNotEvidence(t *testing.T) {
	h := newTransitionWindowHarness(t)
	setInterfaces(h, "192.0.2.1",
		interfaceOn(1, "eth0", C.InterfaceTypeEthernet, true, "192.168.1.10/24"),
		// Present in the list with an IPv6 address, but not up.
		interfaceOn(2, "eth1", C.InterfaceTypeEthernet, false, "2001:db8:dead::1/64"),
	)
	h.settle(t, "one-link-down")

	require.Equal(t, netcap.StateUnavailable, currentFamilyCapability(t, h).Capability.IPv6,
		"a down interface's stale address is not a path this machine can dial")
}

// TestNonPlatformClientsPublishTheirInterfaceList is the reachability of the whole feature.
//
// The interface list is written by UpdateInterfaces, whose platform branch is the only one that used
// to store anything - and that branch is reached only when a platform interface supplies the
// interfaces. The CLI on every platform, the Windows and Linux daemons, and any build with no
// platform interface therefore kept an EMPTY list for the life of the process, which made the local
// family capability permanently Unknown and the fast-fail silently inert on exactly the clients that
// could not get it any other way.
//
// The monitor's own finder is the source here, and the monitor refreshes it before it reports, so the
// production path publishes the list on every interface event.
func TestNonPlatformClientsPublishTheirInterfaceList(t *testing.T) {
	h := newTransitionWindowHarness(t)
	// No platform interface at all: this is the plain path.
	h.manager.platformInterface = nil
	finder := control.NewDefaultInterfaceFinder()
	finder.UpdateInterfaces([]control.Interface{
		{Index: 1, Name: "eth0", Flags: net.FlagUp | net.FlagRunning, Addresses: mustPrefixes("192.168.1.10/24")},
		{Index: 2, Name: "eth1", Flags: 0, Addresses: mustPrefixes("2001:db8:dead::1/64")},
	})
	h.manager.interfaceFinder = finder
	h.manager.networkInterfaces.Store(nil)

	h.manager.observeNonPlatformInterfaces()
	require.Len(t, h.manager.NetworkInterfaces(), 1,
		"the up interface is published and the down one is not")
	require.Equal(t, "eth0", h.manager.NetworkInterfaces()[0].Name)

	// And the effect that matters: a capability now exists at all.
	h.settle(t, "plain-client")
	capability := currentFamilyCapability(t, h).Capability
	require.Equal(t, netcap.StateProvisioned, capability.IPv4)
	require.Equal(t, netcap.StateUnavailable, capability.IPv6,
		"without the list the verdict would be Unknown and this assertion would be unreachable")
}

// TestPlatformSuppliedInterfaceListIsNotOverwritten protects the richer source.
//
// A platform interface reports facts the finder does not have - interface type, metered and
// constrained flags, per-interface DNS servers - and those are what network_strategy selection and the
// dialer's interface choice are built on. Replacing that list with the finder's version would trade
// the capability's input for a worse version of everything else's.
func TestPlatformSuppliedInterfaceListIsNotOverwritten(t *testing.T) {
	h := newTransitionWindowHarness(t)
	h.manager.platformInterface = &platformNetworkOwner{}
	h.manager.interfaceFinder = control.NewDefaultInterfaceFinder()
	h.manager.interfaceFinder.UpdateInterfaces([]control.Interface{
		{Index: 9, Name: "finder-only", Flags: net.FlagUp, Addresses: mustPrefixes("10.9.9.9/32")},
	})
	platformSupplied := []adapter.NetworkInterface{
		interfaceOn(1, "rmnet0", C.InterfaceTypeCellular, true, "10.20.0.5/24", "2001:db8::5/64"),
	}
	h.manager.networkInterfaces.Store(platformSupplied)

	h.manager.observeNonPlatformInterfaces()
	require.Equal(t, platformSupplied, h.manager.NetworkInterfaces(),
		"a platform-supplied list is authoritative and must survive untouched")
}

// TestCapabilityFollowsEveryInterfaceEventNotJustTheEnvironment records that a change in ANOTHER
// interface's addressing is observed, not only changes to the default interface.
//
// The plugging-in of a second link is the ordinary way a machine gains a family while the network it
// is "on" - by gateway and SSID - does not change at all. The environment fingerprint cannot see it,
// which is why the capability is republished on every recompute rather than only on a transition.
func TestCapabilityFollowsEveryInterfaceEventNotJustTheEnvironment(t *testing.T) {
	h := newTransitionWindowHarness(t)
	setInterfaces(h, "192.0.2.1", interfaceOn(1, "eth0", C.InterfaceTypeEthernet, true, "192.168.1.10/24"))
	h.settle(t, "wired-ipv4-only")
	require.Equal(t, netcap.StateUnavailable, currentFamilyCapability(t, h).Capability.IPv6)
	epoch := h.manager.NetworkResetGeneration()

	// A second link comes up with IPv6. Same network by gateway and SSID, so no transition.
	setInterfaces(h, "192.0.2.1",
		interfaceOn(1, "eth0", C.InterfaceTypeEthernet, true, "192.168.1.10/24"),
		interfaceOn(2, "eth1", C.InterfaceTypeEthernet, true, "2001:db8::9/64"),
	)
	h.manager.updateNetworkEnvironment()

	require.Equal(t, epoch, h.manager.NetworkResetGeneration(),
		"gaining an address is not a network transition, and making it one would tear down live streams")
	require.Equal(t, netcap.StateProvisioned, currentFamilyCapability(t, h).Capability.IPv6,
		"but the verdict must follow the addressing immediately, or a working family stays refused")
}

// TestCapabilityIsNotScopedToTheDefaultInterface records the accepted cost, so it cannot be mistaken
// for an oversight later.
//
// A dial bound to one named interface gets the MACHINE's answer rather than that interface's. With
// bind_interface pinned to the IPv4-only link while another link has IPv6, the machine report says
// IPv6 is present, so an IPv6 attempt is made and fails on its own. The opposite scoping - narrowing
// the verdict to the bound interface - would refuse an IPv6 address that a strategy's fallback
// interface could have reached, and it would have to be re-derived on every dial because the binding
// is per-dialer. Erring toward "attempt it" is the direction the whole model fails in: a missed
// fast-fail costs a delay, a wrong refusal costs the connection.
func TestCapabilityIsNotScopedToTheDefaultInterface(t *testing.T) {
	h := newTransitionWindowHarness(t)
	h.manager.interfaceMonitor = &staticInterfaceMonitor{
		current: &control.Interface{Index: 1, Name: "wlan0"},
	}
	setInterfaces(h, "192.0.2.1",
		interfaceOn(1, "wlan0", C.InterfaceTypeWIFI, true, "192.168.1.10/24"),
		interfaceOn(2, "rmnet0", C.InterfaceTypeCellular, true, "10.20.0.5/24", "2001:db8::5/64"),
	)
	h.settle(t, "bound-scope")

	// The default interface is the IPv4-only one, and the verdict still permits IPv6.
	require.Equal(t, netcap.StateProvisioned, currentFamilyCapability(t, h).Capability.IPv6)
	require.False(t, currentFamilyCapability(t, h).Capability.HardUnavailable(false))
}

// TestEnvironmentIsNotTheCapability is the layer's last invariant.
//
// The environment fingerprint names a NETWORK - gateways, SSID, gateway hardware address. The
// capability describes the ADDRESSING. They move for different reasons, they are consumed by different
// code, and conflating them is what would let an SSID change be read as "IPv6 came back" or an address
// change be read as "this is a different network".
func TestEnvironmentIsNotTheCapability(t *testing.T) {
	h := newTransitionWindowHarness(t)
	setInterfaces(h, "192.0.2.1", interfaceOn(1, "eth0", C.InterfaceTypeEthernet, true, "192.168.1.10/24"))
	environmentA := h.settle(t, "network-a")
	capabilityA := currentFamilyCapability(t, h).Capability

	// Same network (same gateway, same SSID), different addressing.
	setInterfaces(h, "192.0.2.1",
		interfaceOn(1, "eth0", C.InterfaceTypeEthernet, true, "192.168.1.10/24", "2001:db8::1/64"))
	h.manager.updateNetworkEnvironment()
	require.Equal(t, environmentA, h.manager.NetworkEnvironment(),
		"the environment did not move: it is the same network")
	require.NotEqual(t, capabilityA, currentFamilyCapability(t, h).Capability,
		"while the capability did, because the addressing moved")

	// Different network, same addressing.
	setInterfaces(h, "192.0.2.9",
		interfaceOn(1, "eth0", C.InterfaceTypeEthernet, true, "192.168.1.10/24", "2001:db8::1/64"))
	h.settle(t, "network-b")
	require.NotEqual(t, environmentA, h.manager.NetworkEnvironment(),
		"the environment moved: different gateway, different network")
	require.Equal(t, netcap.StateProvisioned, currentFamilyCapability(t, h).Capability.IPv6,
		"the capability describes the addressing, which is equivalent here")
}

// platformNetworkOwner is a PlatformInterface that owns the interface list, so a test can assert the
// manager leaves it alone.
type platformNetworkOwner struct {
	adapter.PlatformInterface
}

func (p *platformNetworkOwner) UsePlatformNetworkInterfaces() bool { return true }

func (p *platformNetworkOwner) NetworkInterfaces() ([]adapter.NetworkInterface, error) {
	return nil, nil
}

func mustPrefixes(addresses ...string) []netip.Prefix {
	prefixes := make([]netip.Prefix, 0, len(addresses))
	for _, address := range addresses {
		prefixes = append(prefixes, netip.MustParsePrefix(address))
	}
	return prefixes
}
