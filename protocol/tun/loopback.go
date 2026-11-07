package tun

import "net/netip"

// SplitLoopbackAddresses canonicalises the configured `loopback_address` entries and splits them
// into the two families the TUN stack compares against.
//
// # Why canonicalisation belongs here and not only in the daemon override merge
//
// `daemon.mergeLoopbackAddresses` canonicalises only when the platform ADDS an address (the Apple
// local-signing switch is on). A user-configured address has no addition: the merge deliberately
// returns the configuration untouched, so `::ffff:10.7.0.1` reaches this boundary still mapped.
// `netip.Addr.Is6()` is true for an IPv4-mapped address, and the old `Is4`/`Is6` filters therefore
// put it in the IPv6 list while the stack unmap()s a packet destination and looks it up in the IPv4
// list - the loopback would not be answered, and the client would render an IPv4 literal from the
// IPv6 getter as an IPv6 host route.
//
// The stack's own matcher (`isLoopbackDestination`) canonicalises the destination with
// `Unmap().WithZone("")`, so the configured side must use the same spelling. Order is preserved and
// equivalent duplicate spellings are not silently merged here; that keeps the user's configured
// entries observable while giving every consumer the family it will actually match.
func SplitLoopbackAddresses(addresses []netip.Addr) (inet4 []netip.Addr, inet6 []netip.Addr) {
	for _, address := range addresses {
		address = address.Unmap().WithZone("")
		if !address.IsValid() {
			continue
		}
		if address.Is4() {
			inet4 = append(inet4, address)
		} else {
			inet6 = append(inet6, address)
		}
	}
	return
}
