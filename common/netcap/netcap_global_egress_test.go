package netcap

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
)

// The real 0.1.10 Windows failure, modelled.
//
// A user reported WeChat images failing to send and Douyin media stalling, with dozens of unrelated
// targets in the log:
//
//	dial tcp [2409:...]:443: connectex: A socket operation was attempted to an unreachable network.
//	open UDP connection to [2001:4860:4860::8888]:443: connect: ... unreachable network.
//
// failing in 0-26ms across many different IPv6 destinations. That is the Windows kernel answering
// "there is no route", not a remote server refusing.
//
// The machine state behind it is ordinary: an IPv4 address and a fe80:: link-local address, and no
// global IPv6 route. Every family-existence test passes - IPv6 IS present, a link-local destination
// genuinely works - and not one global IPv6 destination is reachable. That gap is what these tests
// pin.

// windowsLinkLocalOnly is the failing machine: IPv4 usable, IPv6 present only as link-local, and
// routes were read and contain no navigable global IPv6 route.
func windowsLinkLocalOnly() []InterfaceObservation {
	return []InterfaceObservation{{
		Name:            "Ethernet",
		Up:              true,
		Addresses:       mustPrefixes("192.168.1.10/24", "fe80::a1b2:c3d4:e5f6:7890/64"),
		GlobalIPv6Route: RouteAbsent,
	}}
}

// TestLinkLocalOnlyIPv6CannotReachAGlobalIPv6Destination is the reproduction.
//
// Before the global-egress term existed, the capability reported IPv6 as Provisioned here - correctly,
// as a statement about the FAMILY - and a global IPv6 candidate was therefore dialled, dying in the
// kernel in under a millisecond, on every connection, for every target.
func TestLinkLocalOnlyIPv6CannotReachAGlobalIPv6Destination(t *testing.T) {
	capability := Evaluate(windowsLinkLocalOnly())

	// Both statements are true at once, and that is the whole design.
	require.Equal(t, StateProvisioned, capability.IPv6,
		"IPv6 as a FAMILY exists: a link-local destination on this machine really is reachable")
	require.Equal(t, StateUnavailable, capability.GlobalIPv6Egress,
		"and no global IPv6 destination is, which is what the kernel was saying in the log")

	// The decision, per destination.
	require.True(t, capability.HardUndialable(netip.MustParseAddr("2001:db8::1")),
		"a global IPv6 destination must not be attempted")
	require.True(t, capability.HardUndialable(netip.MustParseAddr("2001:4860:4860::8888")),
		"the exact destination from the log")
	require.True(t, capability.HardUndialable(netip.MustParseAddr("2606:4700::1")))

	require.False(t, capability.HardUndialable(netip.MustParseAddr("192.0.2.1")),
		"IPv4 is unaffected: this failure is about the IPv6 egress, not about the machine")
	require.False(t, capability.HardUndialable(netip.MustParseAddr("198.51.100.7")))
}

// TestLinkLocalAndULADestinationsSurvive is the invariant that stops the fix from becoming a
// regression of its own: the public failure must not be enlarged into the link or the LAN.
func TestLinkLocalAndULADestinationsSurvive(t *testing.T) {
	capability := Evaluate(windowsLinkLocalOnly())
	require.Equal(t, StateUnavailable, capability.GlobalIPv6Egress)

	for _, destination := range []string{
		"fe80::1",           // a peer on the same link: the fe80:: address is exactly what makes it work
		"fe80::a1b2:c3d4%3", // zoned, same link
		"fd00::1",           // unique-local: the campus-internal case
		"fd12:3456::9",
		"::1", // loopback never leaves the host
		"127.0.0.1",
		"::", // unspecified: no family to have an opinion about
		"0.0.0.0",
	} {
		parsed := netip.MustParseAddr(destination)
		require.False(t, capability.HardUndialable(parsed),
			"%s is not reached over the public path this verdict describes", destination)
	}
}

// TestGlobalEgressGateAppliesToIPv6Only pins that the new term is about the IPv6 public path and
// cannot leak onto anything else - not onto IPv4 destinations, and not onto the family-existence
// answer.
func TestGlobalEgressGateAppliesToIPv6Only(t *testing.T) {
	capability := Evaluate(windowsLinkLocalOnly())

	require.False(t, capability.HardUndialable(netip.MustParseAddr("8.8.8.8")),
		"an IPv4 destination is not gated by an IPv6 egress verdict")
	require.False(t, capability.HardUndialable(netip.MustParseAddr("::ffff:8.8.8.8")),
		"and an IPv4-mapped address is an IPv4 destination, matching the dialer's own classification")

	// A machine with NO IPv6 at all is the older, stronger case and still behaves the same way.
	noIPv6 := Evaluate([]InterfaceObservation{{
		Name: "eth0", Up: true, Addresses: mustPrefixes("192.168.1.10/24"),
	}})
	require.Equal(t, StateUnavailable, noIPv6.IPv6)
	require.True(t, noIPv6.HardUndialable(netip.MustParseAddr("2001:db8::1")))
	require.False(t, noIPv6.HardUndialable(netip.MustParseAddr("192.0.2.1")))
}

// TestUnknownEgressFailsOpen is the safety property that makes the whole term shippable: a platform
// that cannot read its route table must keep the behaviour it had before this round.
func TestUnknownEgressFailsOpen(t *testing.T) {
	// The same failing machine, but with routes that were never read - the shape every platform
	// without a route-table reader produces.
	unobserved := Evaluate([]InterfaceObservation{{
		Name:      "Ethernet",
		Up:        true,
		Addresses: mustPrefixes("192.168.1.10/24", "fe80::a1b2:c3d4:e5f6:7890/64"),
	}})
	require.Equal(t, StateUnknown, unobserved.GlobalIPv6Egress,
		"nothing was read, so nothing may be claimed")
	require.False(t, unobserved.HardUndialable(netip.MustParseAddr("2001:db8::1")),
		"and an unproven absence must not refuse a destination")

	// The zero value - a manager that never computed a capability - is the same shape.
	zero := FamilyCapability{}
	require.False(t, zero.HardUndialable(netip.MustParseAddr("2001:db8::1")))
	require.False(t, zero.HardUndialable(netip.MustParseAddr("192.0.2.1")))

	// And a machine where nothing could be observed at all.
	require.False(t, Evaluate(nil).HardUndialable(netip.MustParseAddr("2001:db8::1")))
}

// TestAnyInterfaceWithAGlobalRouteKeepsIPv6Usable is the other half of the OR, and the anti-regression
// for the multi-interface case.
//
// One interface reporting no route must not sink the machine when another has one: the dial follows
// the kernel's routing choice, and the kernel has a way out.
func TestAnyInterfaceWithAGlobalRouteKeepsIPv6Usable(t *testing.T) {
	capability := Evaluate([]InterfaceObservation{
		{
			Name: "wlan0", Up: true,
			Addresses:       mustPrefixes("192.168.1.10/24", "fe80::1/64"),
			GlobalIPv6Route: RouteAbsent,
		},
		{
			Name: "rmnet0", Up: true,
			Addresses:       mustPrefixes("10.20.0.5/24", "2409:8a00::5/64"),
			GlobalIPv6Route: RoutePresent,
		},
	})
	require.Equal(t, StateProvisioned, capability.GlobalIPv6Egress)
	require.False(t, capability.HardUndialable(netip.MustParseAddr("2409:8a00::99")),
		"a machine with a global IPv6 route anywhere must keep dialling global IPv6")
}

// theVirtualAdapterTrap is the case that defeats an address-based answer: the machine HAS a global
// IPv6 address, on an adapter that cannot route it anywhere.
//
// This is the Hyper-V / WSL / VPN-adapter shape. "Does any interface carry an IPv6 address" answers
// yes and is wrong - which is why the egress term is built from ROUTES, and why the route reader
// matters even on a machine that looks dual-stack.
func theVirtualAdapterTrap() []InterfaceObservation {
	return []InterfaceObservation{
		{
			Name: "Ethernet", Up: true,
			Addresses:       mustPrefixes("192.168.1.10/24", "fe80::a1b2:c3d4:e5f6:7890/64"),
			GlobalIPv6Route: RouteAbsent,
		},
		{
			Name: "vEthernet (WSL)", Up: true,
			Addresses:       mustPrefixes("172.28.0.1/20", "fd00:1234::1/64", "2606:4700:1::1/128"),
			GlobalIPv6Route: RouteAbsent,
		},
	}
}

// TestAGlobalAddressOnAnAdapterWithoutARouteIsNotEgress is the decisive difference between the two
// possible implementations of this term.
//
// The address-based answer would report egress available here - a global IPv6 address is present -
// and the machine would keep dialling global IPv6 into the kernel's "unreachable network". The
// route-based answer refuses it, and does so while still allowing the ULA destination that adapter
// legitimately serves.
func TestAGlobalAddressOnAnAdapterWithoutARouteIsNotEgress(t *testing.T) {
	capability := Evaluate(theVirtualAdapterTrap())

	require.Equal(t, StateProvisioned, capability.IPv6,
		"the family is present - two interfaces carry IPv6 addressing")
	require.Equal(t, StateUnavailable, capability.GlobalIPv6Egress,
		"a global ADDRESS is not a global ROUTE, and no interface here has one")

	require.True(t, capability.HardUndialable(netip.MustParseAddr("2001:db8::1")))
	require.False(t, capability.HardUndialable(netip.MustParseAddr("fd00:1234::5")),
		"while the ULA the adapter actually serves is still dialable")
	require.False(t, capability.HardUndialable(netip.MustParseAddr("172.28.0.5")))
}

// TestRouteOnlyChangeIsVisibleToTheCapability is the "route changes, addresses do not" case.
//
// A DHCPv6 lease expiring its default route, or an RA ceasing to advertise one, leaves every address
// in place. A capability derived from addressing could not see it; one derived from routes can.
func TestRouteOnlyChangeIsVisibleToTheCapability(t *testing.T) {
	withRoute := []InterfaceObservation{{
		Name:            "Ethernet",
		Up:              true,
		Addresses:       mustPrefixes("192.168.1.10/24", "2001:db8::10/64"),
		GlobalIPv6Route: RoutePresent,
	}}
	withoutRoute := []InterfaceObservation{{
		Name:            "Ethernet",
		Up:              true,
		Addresses:       mustPrefixes("192.168.1.10/24", "2001:db8::10/64"),
		GlobalIPv6Route: RouteAbsent,
	}}

	before := Evaluate(withRoute)
	after := Evaluate(withoutRoute)

	require.Equal(t, before.IPv4, after.IPv4, "no address changed")
	require.Equal(t, before.IPv6, after.IPv6, "and no address changed here either")
	require.NotEqual(t, before, after,
		"but the two capabilities must not compare equal, or a route-only change would publish as "+
			"'nothing happened' and the stale verdict would stand")
	require.False(t, before.HardUndialable(netip.MustParseAddr("2409:8a00::99")))
	require.True(t, after.HardUndialable(netip.MustParseAddr("2409:8a00::99")))
}

// TestClassifyScopeSeparatesTheThreeQuestions pins the scope table the decision is built on.
func TestClassifyScopeSeparatesTheThreeQuestions(t *testing.T) {
	testCases := map[string]DestinationScope{
		"2001:db8::1":         ScopeGlobal,
		"2001:4860:4860::8888": ScopeGlobal,
		"2606:4700::1":         ScopeGlobal,
		"8.8.8.8":              ScopeGlobal,
		"192.0.2.1":            ScopeGlobal,
		"fe80::1":              ScopeLocal,
		"169.254.1.1":          ScopeLocal,
		"::1":                  ScopeLocal,
		"127.0.0.1":            ScopeLocal,
		"::":                   ScopeLocal,
		"0.0.0.0":              ScopeLocal,
		"fd00::1":              ScopeSite,
		"fc00::1":              ScopeSite,
		"10.0.0.1":             ScopeSite,
		"192.168.1.1":          ScopeSite,
		"172.16.0.1":           ScopeSite,
		"::ffff:8.8.8.8":       ScopeGlobal,
		"::ffff:10.0.0.1":      ScopeSite,
	}
	for address, expected := range testCases {
		require.Equalf(t, expected, ClassifyScope(netip.MustParseAddr(address)),
			"scope of %s", address)
	}
	require.Equal(t, ScopeUnknown, ClassifyScope(netip.Addr{}))
	require.Equal(t, "global", ScopeGlobal.String())
	require.Equal(t, "site", ScopeSite.String())
	require.Equal(t, "local", ScopeLocal.String())
	require.Equal(t, "unknown", ScopeUnknown.String())
}

// TestEgressUnavailableNeverRemovesAnIPv4Destination is a blunt guard against the term being wired to
// the wrong family, which would be a far worse outage than the one being fixed.
func TestEgressUnavailableNeverRemovesAnIPv4Destination(t *testing.T) {
	for _, capability := range []FamilyCapability{
		{IPv4: StateProvisioned, IPv6: StateProvisioned, GlobalIPv6Egress: StateUnavailable},
		{IPv4: StateUnknown, IPv6: StateUnknown, GlobalIPv6Egress: StateUnavailable},
		{IPv4: StateProvisioned, IPv6: StateUnavailable, GlobalIPv6Egress: StateUnavailable},
	} {
		for _, destination := range []string{"8.8.8.8", "1.1.1.1", "203.0.113.9", "192.0.2.1"} {
			require.Falsef(t, capability.HardUndialable(netip.MustParseAddr(destination)),
				"IPv6 egress must never refuse %s", destination)
		}
	}
}
