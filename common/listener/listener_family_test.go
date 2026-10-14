package listener

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
)

// This file pins the address-family contract for inbound listeners.
//
// # The defect it guards
//
// A wildcard listener has to say which family it means. Go treats the two wildcards differently:
//
//	"udp4" / "0.0.0.0"   IPv4 only
//	"udp6" / "::"        IPv6 only
//	"udp"  / "::"        DUAL-STACK on Linux - one socket answering both families
//
// Linux defaults `net.ipv6.bindv6only=0`, so a socket created with network "udp" on "::" accepts IPv4
// traffic as well. That is wrong for this product in two ways:
//
//  1. An operator writing `"listen": "::"` asked for the IPv6 wildcard. Silently also taking IPv4
//     means a v4 rule, a v4 firewall entry or a v4-only upstream never applies to the traffic that
//     arrived on that socket - the traffic is accepted on a socket the configuration did not describe.
//  2. A v4 listener and a v6 listener cannot coexist as siblings, because whichever binds second
//     collides with the dual-stack socket the first one created. Configuring both - the ordinary way
//     to be explicit about families - fails with "address already in use".
//
// The fix is to make the family explicit at the one place that creates the socket, for both
// families, from the address itself.

// TestNetworkWithAddressFamilyIsSymmetric is the core contract test.
//
// The rule must hold in BOTH directions. The upstream helper it replaces handled only the IPv4 case:
// `netip.IPv4Unspecified()` gained a "4" suffix while `netip.IPv6Unspecified()` fell through to the
// bare network name, so IPv6 silently kept dual-stack behaviour. A test that only checked the IPv4
// half would have passed against that helper, which is exactly how the defect survived.
func TestNetworkWithAddressFamilyIsSymmetric(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		network string
		address netip.Addr
		want    string
		why     string
	}{
		{
			name: "udp v4 wildcard", network: "udp", address: netip.IPv4Unspecified(),
			want: "udp4", why: "0.0.0.0 is the IPv4 wildcard and may only bind v4",
		},
		{
			name: "udp v6 wildcard", network: "udp", address: netip.IPv6Unspecified(),
			want: "udp6",
			why:  ":: is the IPv6 wildcard; without the suffix Linux binds dual-stack and steals the v4 wildcard",
		},
		{
			name: "tcp v4 wildcard", network: "tcp", address: netip.IPv4Unspecified(),
			want: "tcp4", why: "the same rule for the stream family",
		},
		{
			name: "tcp v6 wildcard", network: "tcp", address: netip.IPv6Unspecified(),
			want: "tcp6", why: "the same rule for the stream family",
		},
		{
			name: "udp ordinary v4 address", network: "udp", address: netip.MustParseAddr("192.0.2.1"),
			want: "udp",
			why:  "a concrete address already selects its family; adding a suffix would change nothing and is not this helper's job",
		},
		{
			name: "udp ordinary v6 address", network: "udp", address: netip.MustParseAddr("2001:db8::1"),
			want: "udp",
			why:  "a concrete address already selects its family",
		},
		{
			name: "udp loopback v4", network: "udp", address: netip.MustParseAddr("127.0.0.1"),
			want: "udp",
			why:  "loopback is a concrete address, not a wildcard",
		},
		{
			name: "udp loopback v6", network: "udp", address: netip.MustParseAddr("::1"),
			want: "udp",
			why:  "::1 is a concrete address, not the wildcard - it must NOT be suffixed as if it were",
		},
		{
			name: "v4-mapped v6 address", network: "udp", address: netip.MustParseAddr("::ffff:192.0.2.1"),
			want: "udp",
			why: "an IPv4-mapped address is NOT a wildcard - measured: IsUnspecified() is false and Is4() false, " +
				"so it takes the concrete-address branch. This helper only assigns a family to a WILDCARD; " +
				"a concrete address already carries its own",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			require.Equal(t, testCase.want, NetworkWithAddressFamily(testCase.network, testCase.address),
				"%s", testCase.why)
		})
	}
}

// TestNetworkWithAddressFamilyCombinesWithThePrefix guards the composition rather than the suffix.
//
// The helper prefixes a base family name. "udp4" must not become "udp44", and the named families must
// survive so that a caller already passing a qualified network is not corrupted.
func TestNetworkWithAddressFamilyCombinesWithThePrefix(t *testing.T) {
	require.Equal(t, "udp4", NetworkWithAddressFamily("udp", netip.IPv4Unspecified()))
	require.Equal(t, "udp6", NetworkWithAddressFamily("udp", netip.IPv6Unspecified()))
	require.Equal(t, "tcp4", NetworkWithAddressFamily("tcp", netip.IPv4Unspecified()))
	require.Equal(t, "tcp6", NetworkWithAddressFamily("tcp", netip.IPv6Unspecified()))
}

// TestNetworkWithAddressFamilyHandlesTheZeroAddress documents the one input that must not be guessed.
//
// A zero netip.Addr is "invalid": it is neither v4 nor v6, and netip's own Is4()/Is6() both report
// false. The helper must not invent a family for it, because choosing one would bind a socket the
// caller never asked for. Returning the network unchanged leaves the decision to Go, which reports a
// clear error rather than silently picking a family.
func TestNetworkWithAddressFamilyHandlesTheZeroAddress(t *testing.T) {
	var zero netip.Addr
	require.False(t, zero.IsValid(), "precondition: the zero Addr is invalid")
	require.Equal(t, "udp", NetworkWithAddressFamily("udp", zero),
		"an invalid address must not be assigned a family by this helper")
}
