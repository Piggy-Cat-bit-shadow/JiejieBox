package listener

import (
	"net/netip"
)

// NetworkWithAddressFamily returns the network name that binds addr's address family EXPLICITLY.
//
// # Why this exists rather than the shared helper
//
// The shared `M.NetworkFromNetAddr` handles only one half of the problem:
//
//	func NetworkFromNetAddr(network string, addr netip.Addr) string {
//		if addr == netip.IPv4Unspecified() {
//			return network + "4"
//		}
//		return network
//	}
//
// An IPv4 wildcard gains the "4" suffix, but an IPv6 wildcard falls through and keeps the bare network
// name - and on Linux a socket created with network "udp" on "::" is DUAL-STACK, because the default
// `net.ipv6.bindv6only` is 0. So `"listen": "::"` silently also served IPv4, and an operator could not
// run a v4 listener and a v6 listener as siblings: whichever bound second collided with the dual-stack
// socket the first had already created.
//
// The asymmetry is the whole defect. A rule that is applied to one family and not the other looks
// deliberate and is read as deliberate, which is how it survived review.
//
// # The contract
//
//	IPv4 wildcard 0.0.0.0   -> network + "4"    bind v4 only
//	IPv6 wildcard ::        -> network + "6"    bind v6 only
//	any concrete address    -> network          the address already selects its family
//	invalid (zero) Addr     -> network          never invent a family
//
// The two wildcards are symmetric by construction. A concrete address is left alone on purpose: `::1`
// and `127.0.0.1` are not wildcards, and appending a suffix to them would be a different change with a
// different justification. The zero Addr is left alone because netip reports it as neither family, and
// choosing one here would bind a socket the caller did not ask for; Go's own error is better than a
// silent guess.
//
// Note that an IPv4-mapped address (`::ffff:192.0.2.1`) is NOT a wildcard. MEASURED: its
// IsUnspecified() is false and its Is4() is false, so it is a concrete address and takes the
// passthrough branch. That is correct - this helper assigns a family only to a wildcard, and a
// concrete address already carries its own. A test pins it, because "it looks v6 so suffix it 6" is
// the plausible-looking mistake.
func NetworkWithAddressFamily(network string, address netip.Addr) string {
	if address == netip.IPv4Unspecified() {
		return network + "4"
	}
	if address == netip.IPv6Unspecified() {
		return network + "6"
	}
	return network
}
