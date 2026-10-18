package netcap

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The entry presentment is the one answer in this package that is applied to software this process
// does not control, so its tests are written as the two failure directions rather than as a list of
// states:
//
//   - a machine that provably cannot reach any local IPv6 destination must be suppressed, or the
//     false advertisement this round exists for survives;
//   - every machine whose IPv6 might still work, and every observation that failed, must stay
//     PRESENTED, because the entry layer has no later chance to correct itself.

// TestClassifyIPv6AddressingNamesTheShapeTheStateCannot covers the four shapes the entry decision
// distinguishes. The family state is printed beside each one to make the reason the shape is
// classified separately explicit: the first and the last two rows all read "provisioned".
func TestClassifyIPv6AddressingNamesTheShapeTheStateCannot(t *testing.T) {
	testCases := []struct {
		name       string
		interfaces []InterfaceObservation
		expected   IPv6Addressing
		state      FamilyState
	}{
		{
			name:       "ipv4 only",
			interfaces: []InterfaceObservation{observation("en0", "192.0.2.10/24")},
			expected:   AddressingNone,
			state:      StateUnavailable,
		},
		{
			name:       "link-local only",
			interfaces: []InterfaceObservation{observation("en0", "192.0.2.10/24", "fe80::1/64")},
			expected:   AddressingLinkOnly,
			state:      StateProvisioned,
		},
		{
			name:       "unique-local",
			interfaces: []InterfaceObservation{observation("en0", "192.0.2.10/24", "fd00::10/64")},
			expected:   AddressingSite,
			state:      StateProvisioned,
		},
		{
			name:       "unique-local and global",
			interfaces: []InterfaceObservation{observation("en0", "192.0.2.10/24", "fd00::10/64", "2001:db8::10/64")},
			// Site wins over global: a machine with a ULA keeps LAN IPv6 presented even when the
			// public path is the thing that is missing.
			expected: AddressingSite,
			state:    StateProvisioned,
		},
		{
			name:       "global only",
			interfaces: []InterfaceObservation{observation("en0", "192.0.2.10/24", "2001:db8::10/64")},
			expected:   AddressingGlobal,
			state:      StateProvisioned,
		},
		{
			name:       "ipv4-mapped is not ipv6 addressing",
			interfaces: []InterfaceObservation{observation("en0", "192.0.2.10/24", "::ffff:192.0.2.11/128")},
			expected:   AddressingNone,
			state:      StateUnavailable,
		},
		{
			name:       "nothing to look at",
			interfaces: nil,
			expected:   AddressingUnknown,
			state:      StateUnknown,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			require.Equal(t, testCase.expected, ClassifyIPv6Addressing(testCase.interfaces))
			// The state column is an assertion, not a comment: it is what shows the shape is not
			// derivable from the state alone.
			state := Evaluate(testCase.interfaces).IPv6
			if testCase.state == StateProvisioned {
				require.Equal(t, StateProvisioned, state)
			} else {
				require.NotEqual(t, StateProvisioned, state)
			}
		})
	}
}

// TestProjectIPv6PresentmentSuppressesOnlyProvableIPv6 is the decision table.
func TestProjectIPv6PresentmentSuppressesOnlyProvableIPv6(t *testing.T) {
	testCases := []struct {
		name       string
		capability FamilyCapability
		addressing IPv6Addressing
		expected   IPv6Presentment
	}{
		{
			name:       "ipv4 only network",
			capability: FamilyCapability{IPv4: StateProvisioned, IPv6: StateUnavailable},
			addressing: AddressingNone,
			expected:   SuppressIPv6,
		},
		{
			name:       "link-local only network",
			capability: FamilyCapability{IPv4: StateProvisioned, IPv6: StateProvisioned},
			addressing: AddressingLinkOnly,
			expected:   SuppressIPv6,
		},
		{
			name:       "global address without a route",
			capability: FamilyCapability{IPv4: StateProvisioned, IPv6: StateProvisioned, GlobalIPv6Egress: StateUnavailable},
			addressing: AddressingGlobal,
			expected:   SuppressIPv6,
		},
		{
			name:       "global address with a route",
			capability: FamilyCapability{IPv4: StateProvisioned, IPv6: StateProvisioned, GlobalIPv6Egress: StateProvisioned},
			addressing: AddressingGlobal,
			expected:   PresentIPv6,
		},
		{
			name:       "global address, route unread",
			capability: FamilyCapability{IPv4: StateProvisioned, IPv6: StateProvisioned, GlobalIPv6Egress: StateUnknown},
			addressing: AddressingGlobal,
			expected:   PresentIPv6,
		},
		{
			name:       "unique-local address without a route keeps lan ipv6",
			capability: FamilyCapability{IPv4: StateProvisioned, IPv6: StateProvisioned, GlobalIPv6Egress: StateUnavailable},
			addressing: AddressingSite,
			expected:   PresentIPv6,
		},
		{
			name:       "unique-local and global with a route",
			capability: FamilyCapability{IPv4: StateProvisioned, IPv6: StateProvisioned, GlobalIPv6Egress: StateProvisioned},
			addressing: AddressingSite,
			expected:   PresentIPv6,
		},
		{
			name:       "interface up, not addressed yet",
			capability: FamilyCapability{},
			addressing: AddressingNone,
			expected:   PresentIPv6,
		},
		{
			name:       "observation failed",
			capability: FamilyCapability{},
			addressing: AddressingUnknown,
			expected:   PresentIPv6,
		},
		{
			name:       "no interfaces at all",
			capability: FamilyCapability{IPv4: StateUnknown, IPv6: StateUnknown},
			addressing: AddressingUnknown,
			expected:   PresentIPv6,
		},
		{
			name:       "ipv6-only network",
			capability: FamilyCapability{IPv4: StateUnavailable, IPv6: StateProvisioned, GlobalIPv6Egress: StateProvisioned},
			addressing: AddressingGlobal,
			expected:   PresentIPv6,
		},
		{
			name:       "nat64 or clat presents as global",
			capability: FamilyCapability{IPv4: StateProvisioned, IPv6: StateProvisioned, GlobalIPv6Egress: StateProvisioned},
			addressing: AddressingGlobal,
			expected:   PresentIPv6,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			require.Equal(t, testCase.expected, ProjectIPv6Presentment(testCase.capability, testCase.addressing))
		})
	}
}

// TestThePresentmentNeverContradictsTheSameObservation is the internal-consistency rule: one
// interface list, one observation, and the two halves of the decision derived from it.
//
// It asserts the two properties that make the entry decision defensible:
//
//   - a suppressed entry never comes from a machine with a unique-local address (that is the LAN
//     case the presentment must not delete), and never from an observation that failed;
//   - a suppressed entry that was decided by the ADDRESS SHAPE alone - the "no IPv6 address at all"
//     shape - is only legal when the family state is the hard negative, because the same shape is
//     also what an interface that has not been addressed yet looks like.
func TestThePresentmentNeverContradictsTheSameObservation(t *testing.T) {
	lists := [][]InterfaceObservation{
		{observation("en0", "192.0.2.10/24")},
		{observation("en0", "192.0.2.10/24", "fe80::1/64")},
		{observation("en0", "192.0.2.10/24", "fd00::10/64")},
		{observation("en0", "192.0.2.10/24", "fd00::10/64", "2001:db8::10/64")},
		{observation("en0", "192.0.2.10/24", "2001:db8::10/64")},
		{observation("en0", "2001:db8::10/64")},
		{{Name: "en0", Up: true}},
		nil,
	}
	for _, interfaces := range lists {
		capability := Evaluate(interfaces)
		addressing := ClassifyIPv6Addressing(interfaces)
		presentment := ProjectIPv6Presentment(capability, addressing)
		if presentment != SuppressIPv6 {
			continue
		}
		require.NotEqual(t, AddressingUnknown, addressing, "an observation failure must not suppress")
		require.NotEqual(t, AddressingSite, addressing, "a unique-local machine must keep its lan ipv6")
		if addressing == AddressingNone {
			require.Equal(t, StateUnavailable, capability.IPv6,
				"the no-address shape suppresses only through the family verdict, never on its own")
		}
	}
}
