package netcap

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
)

// The model's whole job is to separate "this machine provably cannot dial this family" from
// everything else, and to never let the second become the first. These tests are written as that
// boundary: the Unavailable cases are the ones where a hard fail-fast is allowed, and every other
// case must come back as something that races.

// observation is an interface that is up and carrying the given addresses.
//
// Up is the default because a down interface is the exception a test should have to ask for: every
// other case in this file is about addressing, and a helper that defaulted to down would make those
// cases silently about the wrong thing.
func observation(name string, addresses ...string) InterfaceObservation {
	return InterfaceObservation{Name: name, Up: true, Addresses: mustPrefixes(addresses...)}
}

// downObservation is an interface that is present in the list but cannot carry traffic.
func downObservation(name string, addresses ...string) InterfaceObservation {
	return InterfaceObservation{Name: name, Up: false, Addresses: mustPrefixes(addresses...)}
}

func mustPrefixes(addresses ...string) []netip.Prefix {
	parsed := make([]netip.Prefix, 0, len(addresses))
	for _, address := range addresses {
		parsed = append(parsed, netip.MustParsePrefix(address))
	}
	return parsed
}

func TestEvaluateSeparatesProvableFromUnproven(t *testing.T) {
	testCases := []struct {
		name       string
		interfaces []InterfaceObservation
		ipv4       FamilyState
		ipv6       FamilyState
	}{
		{
			// The ordinary IPv4-only LAN: an address, a gateway, no IPv6 anywhere.
			name:       "ipv4 only underlay",
			interfaces: []InterfaceObservation{observation("en0", "192.168.1.10/24")},
			ipv4:       StateProvisioned,
			ipv6:       StateUnavailable,
		},
		{
			name:       "ipv6 only underlay",
			interfaces: []InterfaceObservation{observation("rmnet0", "2001:db8::10/64")},
			ipv4:       StateUnavailable,
			ipv6:       StateProvisioned,
		},
		{
			name:       "healthy dual stack",
			interfaces: []InterfaceObservation{observation("en0", "192.168.1.10/24", "2001:db8::10/64")},
			ipv4:       StateProvisioned,
			ipv6:       StateProvisioned,
		},
		{
			// The campus case that must NOT be mistaken for "no IPv6". A link-local address is a
			// usable address: a link-local destination is dialable, so the family is present. It is
			// not evidence of anything further, which is why the answer is Provisioned and never
			// Validated.
			name:       "link-local ipv6 only",
			interfaces: []InterfaceObservation{observation("en0", "192.168.1.10/24", "fe80::1:2:3:4/64")},
			ipv4:       StateProvisioned,
			ipv6:       StateProvisioned,
		},
		{
			// 464XLAT: the CLAT interface carries a synthesized IPv4 address as well as the native
			// IPv6 one, so an "IPv6-only" mobile network still reports IPv4 as present. A model that
			// keyed on "is there a NATIVE IPv4 address" would delete IPv4 here.
			name: "464xlat clat interface",
			interfaces: []InterfaceObservation{
				observation("rmnet0", "2001:db8::10/64"),
				observation("clat4", "192.0.0.4/32"),
			},
			ipv4: StateProvisioned,
			ipv6: StateProvisioned,
		},
		{
			// One interface of each is enough. The capability is a property of the machine's dial
			// path, not of one link.
			name: "split across interfaces",
			interfaces: []InterfaceObservation{
				observation("en0", "10.0.0.5/8"),
				observation("wwan0", "2001:db8::5/64"),
			},
			ipv4: StateProvisioned,
			ipv6: StateProvisioned,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			capability := Evaluate(testCase.interfaces)
			require.Equal(t, testCase.ipv4, capability.IPv4)
			require.Equal(t, testCase.ipv6, capability.IPv6)
		})
	}
}

// TestEvaluateWithoutInterfacesIsUnknown is the fail-open rule, and the reason the zero value cannot
// be the refusing one.
//
// A manager with no interface monitor, a platform that cannot enumerate, and a handover that has
// momentarily emptied the list all produce this input. Concluding "neither family exists" from it
// would delete both families on the strength of an observation failure.
func TestEvaluateWithoutInterfacesIsUnknown(t *testing.T) {
	capability := Evaluate(nil)
	require.Equal(t, StateUnknown, capability.IPv4)
	require.Equal(t, StateUnknown, capability.IPv6)
	require.False(t, capability.HardUnavailable(true))
	require.False(t, capability.HardUnavailable(false))

	zero := FamilyCapability{}
	require.False(t, zero.HardUnavailable(true), "a capability nobody computed must restrict nothing")
	require.False(t, zero.HardUnavailable(false))
}

// TestEvaluateDoesNotCountAnEmptyAddressListAsAddressing pins the difference between "the interface
// exists" and "the interface carries this family".
func TestEvaluateDoesNotCountAnEmptyAddressListAsAddressing(t *testing.T) {
	capability := Evaluate([]InterfaceObservation{
		observation("en0"),
		observation("en1", "192.168.1.10/24"),
	})
	require.Equal(t, StateProvisioned, capability.IPv4)
	require.Equal(t, StateUnavailable, capability.IPv6)
}

// TestEvaluateIgnoresInvalidAddresses pins that a malformed entry cannot stand in for addressing.
//
// This test previously asserted the OPPOSITE - that an interface carrying only an invalid prefix
// proved BOTH families impossible. That was the same defect as
// TestEvaluateAddresslessInterfaceIsNotEvidence, seen from the other side: "this interface produced no
// usable address" is the absence of an observation, and an invalid entry is one more way to produce
// none. The two cases now answer identically, which is the point - the gate is "did any interface
// produce at least one valid address", and it does not care WHY an interface produced none.
func TestEvaluateIgnoresInvalidAddresses(t *testing.T) {
	capability := Evaluate([]InterfaceObservation{{
		Name:      "en0",
		Up:        true,
		Addresses: []netip.Prefix{{}},
	}})
	require.Equal(t, StateUnknown, capability.IPv4,
		"an invalid address is not evidence that IPv4 is absent")
	require.Equal(t, StateUnknown, capability.IPv6,
		"an invalid address is not evidence that IPv6 is absent")
	require.False(t, capability.HardUnavailable(true))
	require.False(t, capability.HardUnavailable(false))
	require.Equal(t, 1, capability.Observed(),
		"the interface was still looked at, and the diagnostic says so")
}

// TestEvaluateAddresslessInterfaceIsNotEvidence pins the handover case.
//
// An interface that is up and enumerable with no address yet is the ordinary state of a link that is
// still waiting for DHCP or a router advertisement. It is not evidence that the machine lacks BOTH
// families, and reading it that way declared each family provably undialable - which the scheduler
// turns into a hard refusal of every candidate, on a machine whose network is fine.
//
// The three inputs below are the three ways an interface arrives with no addressing: the field left
// out, explicitly nil, and an empty-but-non-nil slice. They must not be distinguishable here.
func TestEvaluateAddresslessInterfaceIsNotEvidence(t *testing.T) {
	capability := Evaluate([]InterfaceObservation{
		{Name: "en0"},
		{Name: "en1", Addresses: nil},
		{Name: "en2", Addresses: []netip.Prefix{}},
	})

	require.Equal(t, StateUnknown, capability.IPv4, "an address-less interface must not prove IPv4 impossible")
	require.Equal(t, StateUnknown, capability.IPv6, "an address-less interface must not prove IPv6 impossible")
	require.False(t, capability.HardUnavailable(true))
	require.False(t, capability.HardUnavailable(false))
	require.Equal(t, 3, capability.Observed())

	// The other direction, and it is the one that keeps the gate honest: ONE addressed interface
	// alongside the address-less ones is still decisive, so the gate cannot be satisfied by padding
	// the list with interfaces that say nothing.
	capability = Evaluate([]InterfaceObservation{
		{Name: "en0"},
		{Name: "en1", Addresses: []netip.Prefix{}},
		observation("en2", "192.168.1.10/24"),
	})
	require.Equal(t, StateProvisioned, capability.IPv4)
	require.Equal(t, StateUnavailable, capability.IPv6,
		"an addressed IPv4-only link still proves IPv6 absent")
}

// TestUsableInterfacesDropsAnInterfaceThatIsNotUp is the invariant that has to hold at THIS boundary
// rather than in each caller.
//
// A disconnected link keeps its address, and the platform's interface list is not required to hide it:
// one producer of that list is only ever populated with up interfaces, while the other hands over
// whatever the platform reported. A check that lived in the first caller would be dead code there and
// missing in the second - so it lives here, where both inputs arrive.
func TestUsableInterfacesDropsAnInterfaceThatIsNotUp(t *testing.T) {
	usable, ok := UsableInterfaces([]InterfaceObservation{
		downObservation("eth0", "192.168.1.10/24", "2001:db8:dead::1/64"),
	}, nil)
	require.False(t, ok, "a down interface is not something to look at")
	require.Empty(t, usable)
	require.Equal(t, StateUnknown, Evaluate(usable).IPv6,
		"and with nothing to look at the answer is Unknown, not a hard verdict")

	// Up alongside down: the down one contributes nothing, the up one decides.
	usable, ok = UsableInterfaces([]InterfaceObservation{
		downObservation("eth0", "2001:db8:dead::1/64"),
		observation("eth1", "192.168.1.10/24"),
	}, nil)
	require.True(t, ok)
	require.Equal(t, StateUnavailable, Evaluate(usable).IPv6,
		"the down interface's stale IPv6 address must not make IPv6 look present")
}

// TestEvaluateTreatsMappedIPv4AsIPv4 mirrors classifyAddress in the dialer: ::ffff:1.2.3.4 is a
// representation of an IPv4 address, and counting it as IPv6 addressing would make the machine look
// dual-stack when it is single-stack.
func TestEvaluateTreatsMappedIPv4AsIPv4(t *testing.T) {
	capability := Evaluate([]InterfaceObservation{observation("en0", "::ffff:192.168.1.10/120")})
	require.Equal(t, StateProvisioned, capability.IPv4)
	require.Equal(t, StateUnavailable, capability.IPv6)
}

// TestUsableInterfacesRemovesSplitHorizonAndLoopback is the TUN self-reference rule at the model's
// boundary.
//
// If the process's own tunnel counted, a sing-box TUN holding ::/0 and a global IPv6 address on an
// IPv4-only physical link would report IPv6 as available - the exact misreading this round exists to
// prevent. And if loopback counted, "no interface carries this family" would be unreachable on every
// machine, silently switching the hard check off everywhere instead of only where it is wrong.
func TestUsableInterfacesRemovesSplitHorizonAndLoopback(t *testing.T) {
	interfaces := []InterfaceObservation{
		observation("lo", "127.0.0.1/8", "::1/128"),
		observation("utun3", "172.19.0.1/30", "2001:db8:aaaa::1/64"),
		observation("en0", "192.168.1.10/24"),
	}
	usable, ok := UsableInterfaces(interfaces, []string{"utun3"})
	require.True(t, ok)
	require.Equal(t, []InterfaceObservation{observation("en0", "192.168.1.10/24")}, usable)

	capability := Evaluate(usable)
	require.Equal(t, StateProvisioned, capability.IPv4)
	require.Equal(t,
		StateUnavailable, capability.IPv6,
		"the tunnel's own IPv6 address is not evidence about the network under it")
}

// TestUsableInterfacesReportsNothingToLookAt is the difference between "we looked and found nothing"
// and "there was nothing to look at". Only the first may become a hard verdict; the caller
// distinguishes them with the boolean, because collapsing them would turn an empty list into a
// deleted family.
func TestUsableInterfacesReportsNothingToLookAt(t *testing.T) {
	usable, ok := UsableInterfaces(nil, nil)
	require.False(t, ok)
	require.Empty(t, usable)
	require.Equal(t, StateUnknown, Evaluate(usable).IPv6,
		"and Evaluate on that empty list must be Unknown rather than Unavailable")

	// Only loopback and only the process's own tunnel: still nothing to look at.
	usable, ok = UsableInterfaces([]InterfaceObservation{
		observation("lo", "127.0.0.1/8", "::1/128"),
		observation("tun0", "10.0.0.1/30"),
	}, []string{"tun0"})
	require.False(t, ok)
}

// TestUsableInterfacesRefusesUnnamedInterfaces records why an unnamed interface is excluded rather
// than assumed to be underlay: it cannot be matched against the caller's own list, so it cannot be
// proven not to be this process's own tunnel.
func TestUsableInterfacesRefusesUnnamedInterfaces(t *testing.T) {
	usable, ok := UsableInterfaces([]InterfaceObservation{observation("", "2001:db8::1/64")}, nil)
	require.False(t, ok)
	require.Empty(t, usable)
}

// TestUsableInterfacesKeepsAPartialLoopbackInterface covers the mixed case: an interface that has a
// loopback address among real ones is a real interface, not a loopback interface.
func TestUsableInterfacesKeepsAPartialLoopbackInterface(t *testing.T) {
	usable, ok := UsableInterfaces([]InterfaceObservation{
		observation("bridge0", "127.0.0.1/8", "192.168.5.1/24"),
	}, nil)
	require.True(t, ok)
	require.Len(t, usable, 1)
}

func TestFamilyStateNamesEveryValue(t *testing.T) {
	require.Equal(t, "unknown", StateUnknown.String())
	require.Equal(t, "unavailable", StateUnavailable.String())
	require.Equal(t, "provisioned", StateProvisioned.String())
	require.Equal(t, "validated", StateValidated.String())
	require.Equal(t, "degraded", StateDegraded.String())
	require.Equal(t, "invalid", FamilyState(200).String())
}

// TestHardUnavailableIsOnlyEverTheUnavailableState is the single assertion the whole file exists
// for. No reserved state may acquire the power to remove a candidate, because a reserved state has
// no producer and therefore no evidence behind it.
func TestHardUnavailableIsOnlyEverTheUnavailableState(t *testing.T) {
	for _, state := range []FamilyState{StateUnknown, StateProvisioned, StateValidated, StateDegraded} {
		capability := FamilyCapability{IPv4: state, IPv6: state}
		require.False(t, capability.HardUnavailable(true), "%s must not hard-filter IPv4", state)
		require.False(t, capability.HardUnavailable(false), "%s must not hard-filter IPv6", state)
	}
	unavailable := FamilyCapability{IPv4: StateUnavailable, IPv6: StateUnavailable}
	require.True(t, unavailable.HardUnavailable(true))
	require.True(t, unavailable.HardUnavailable(false))
}
