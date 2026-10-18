package tun

import (
	"net/netip"
	"slices"

	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common"

	"go4.org/netipx"
)

// This file holds the effective-options half of the entry IPv6 decision: what a TUN that must not
// present IPv6 looks like, and what a TUN that cannot be reduced safely looks like.
//
// # Why the decision is applied to the options rather than to the platform call
//
// The options struct is the ONE description of the interface: sing-tun builds the address list, the
// route table, the DNS hijack address and the platform client's own settings from it, and the stack
// is built from the same struct. Removing IPv6 here therefore removes it from every consumer at
// once, including the platform clients that never see this package. A patch applied at the
// OpenInterface call site would leave the stack and the auto-redirect convinced the interface still
// has IPv6 - the exact split the architecture of this round is trying to avoid.
//
// # Why these are functions rather than methods
//
// They are the whole of the testable decision and they have no state: the inbound can be replaced by
// a struct literal in a test, and the properties that must hold - "IPv6 is gone from every field",
// "the interface is never left addressless" - are asserted without opening an interface.

// appendRouteSetPrefixes copies a rule-set's address prefixes into the effective options, split by
// family.
//
// # Why this is a function
//
// It is the second place IPv6 can enter the options, after the leading address lists, and the first
// version of this round's decision only cleared the leading lists - so an IPv4-only interface still
// received every IPv6 prefix a route_address_set contributed, and the route bypass the decision
// exists to close stayed open. Keeping the loop in one named place with an `ipv4Only` input is what
// makes that case assertable without opening an interface.
//
// # Why IPv6 prefixes are dropped rather than converted
//
// A route prefix with no IPv6 address on the interface carries no traffic; on every stack built by
// sing-tun the automatic IPv6 range is derived from the address, and the platform clients condition
// their own default branch the same way. Dropping it keeps one description of the interface instead
// of two - "IPv6 addressing is absent, but here are some IPv6 routes" - which is exactly the split
// this pipeline exists to avoid.
//
// The IPv4 prefixes of the same sets are applied unchanged, and the exclusion sets follow the same
// rule: an IPv6 exclude route on an interface with no IPv6 address is a rule about traffic that
// cannot exist.
func appendRouteSetPrefixes(options *tun.Options, routeAddressSet, routeExcludeAddressSet []*netipx.IPSet, ipv4Only bool) {
	for _, ipSet := range routeAddressSet {
		for _, prefix := range ipSet.Prefixes() {
			if prefix.Addr().Is4() {
				options.Inet4RouteAddress = append(options.Inet4RouteAddress, prefix)
			} else if !ipv4Only {
				options.Inet6RouteAddress = append(options.Inet6RouteAddress, prefix)
			}
		}
	}
	for _, ipSet := range routeExcludeAddressSet {
		for _, prefix := range ipSet.Prefixes() {
			if prefix.Addr().Is4() {
				options.Inet4RouteExcludeAddress = append(options.Inet4RouteExcludeAddress, prefix)
			} else if !ipv4Only {
				options.Inet6RouteExcludeAddress = append(options.Inet6RouteExcludeAddress, prefix)
			}
		}
	}
}

// applyIPv4OnlyPresentment removes every IPv6 fact from the effective options, in place.
//
// It reports whether the reduction was applied. False means "this interface cannot be presented as
// IPv4 only without breaking it", and the caller must keep the configured options untouched - a
// refused reduction is always safer than an interface with no address at all.
//
// # The two refusals, and why they are refusals rather than errors
//
//  1. no IPv4 address. A TUN needs one address to exist; an IPv6-only TUN on a network with no
//     local IPv6 is already a broken configuration, and deleting its last address would turn a
//     configuration problem into an interface that cannot be opened. The decision is dropped, the
//     configured IPv6 stays, and the dial path refuses the attempts it cannot make.
//  2. an explicit DNS address list with no IPv4 entry. The hijack DNS address is what makes
//     `dns_mode: hijack` work; removing the only server the user named would leave a TUN whose
//     applications have no resolver at all. Kept untouched for the same reason.
//
// # What is removed
//
//	Inet6Address               the address itself; sing-tun derives the platform addressing, the
//	                          automatic route range and the IPv6 DNS address from it
//	Inet6RouteAddress          explicit routes, including the ones route_address_set contributed
//	Inet6RouteExcludeAddress   exclude routes; an IPv6 exclude route on an IPv4-only interface is a
//	                          rule about traffic that cannot exist
//	Inet6LoopbackAddress       loopback addresses are addresses on this interface
//	Inet6Gateway               a next hop for a family with no address
//	DNSAddress (IPv6 entries)  the hijack DNS server is an address on this interface too
func applyIPv4OnlyPresentment(options *tun.Options) bool {
	if len(options.Inet4Address) == 0 {
		return false
	}
	if len(options.DNSAddress) > 0 && !slices.ContainsFunc(options.DNSAddress, netip.Addr.Is4) {
		return false
	}
	options.Inet6Address = nil
	options.Inet6RouteAddress = nil
	options.Inet6RouteExcludeAddress = nil
	options.Inet6LoopbackAddress = nil
	options.Inet6Gateway = netip.Addr{}
	if len(options.DNSAddress) > 0 {
		// Filter rather than an in-place delete: the slice is aliased from the TYPED CONFIGURATION,
		// which the box keeps for the life of the process (and a reload builds a new box from the
		// same options). Writing through it would clean the user's configuration in memory, which
		// this decision is explicitly not allowed to do - it owns the effective options only.
		options.DNSAddress = common.Filter(options.DNSAddress, netip.Addr.Is4)
	}
	return true
}
