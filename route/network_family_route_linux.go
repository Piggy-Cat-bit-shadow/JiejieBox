package route

import (
	"net"
	"net/netip"

	"github.com/sagernet/sing-box/common/netcap"

	"github.com/sagernet/netlink"
)

// globalIPv6RouteObservation reads the kernel's IPv6 routing table and answers whether this machine
// has a usable route to a global IPv6 destination.
//
// # Why this is a second read of a table the file next door already reads
//
// net/network_environment_linux.go already dumps this table for systemGateways, with
// RouteListFiltered(FAMILY_ALL, ...) and an existing /0 selection - and then flattens the result to a
// []netip.Addr of GATEWAYS, dropping the family and the destination prefix on the way out. The
// information was never missing on Linux; it was discarded.
//
// # Why the read is unqualified by interface
//
// The question is "is there a way out", so it must not be scoped to one interface the way
// systemGateways is: a machine whose physical link has no IPv6 route and whose cellular link has one
// is a machine with a way out, and an interface-scoped read would report "no route" for the physical
// one and refuse global IPv6 on a network that can carry it.
//
// # Why every table is enumerated, and the library trap that makes it awkward
//
// A policy-routing setup can carry the ONLY IPv6 route in a custom table. Reading RT_TABLE_MAIN and
// publishing "no route" when it comes up empty would then refuse every global IPv6 destination on a
// machine that routes them perfectly well - a wrongful refusal, which is the one outcome this design
// is required to avoid.
//
// The obvious fix - RouteListFiltered(FAMILY_V6, &Route{}, 0) - DOES NOT WORK, and the reason is worth
// writing down because it is invisible from the API:
//
//	netlink/route_linux.go:1037
//	    if msg.Table != unix.RT_TABLE_MAIN {
//	        if filter == nil || filterMask&RT_FILTER_TABLE == 0 {
//	            continue        // <-- every non-main table is DISCARDED here
//	        }
//	    }
//
// The library drops non-main tables before it applies any filter, unless RT_FILTER_TABLE is set - and
// when it is set, the table comparison at :1049 then keeps only the one table named. So there is no
// filter that selects "all tables", and requesting a table by name cannot enumerate them either.
//
// The way through is to set the bit that disables the early discard while naming a table that matches
// nothing: linuxAllTablesSentinel is far above any real table id, so the kernel sends every table and
// the library's own equality check then rejects none of them on that test. The routes that survive are
// filtered here, by us, on the facts that actually matter.
//
// # What is accepted
//
// A usable route is one the kernel would actually use: IPv6, not a reject/blackhole/throw, and not on
// one of this process's own interfaces. Its destination must be a default route or a GLOBAL prefix,
// and - this is the part that matters for a directly-connected network - an on-link route to a global
// prefix counts. A host connected to a routed global /64 with no gateway at all can reach that prefix,
// and refusing it would be a wrongful refusal.
//
// The one on-link case that proves nothing is a /128: that is the host route for an address the
// interface already owns, and it says only that the machine can reach ITSELF. Refusing global IPv6 on
// the strength of a machine's own address would be exactly the "global address is not global egress"
// error this term exists to avoid, so on-link routes are required to be shorter than a host route.
func globalIPv6RouteObservation(ownInterfaceNames []string, ownInterfaceIndices []int) RouteObservationResult {
	routes, err := netlink.RouteListFiltered(netlink.FAMILY_V6, &netlink.Route{Table: linuxAllTablesSentinel}, netlink.RT_FILTER_TABLE)
	if err != nil {
		// The table could not be read. Unobserved contributes nothing, so the capability degrades to
		// the addressing-only answer instead of guessing.
		return RouteObservationResult{Observation: netcap.RouteUnobserved}
	}
	for _, currentRoute := range routes {
		destination := routeDestination(currentRoute.Dst)
		isDefault := currentRoute.Dst == nil
		prefixLength := -1
		if !isDefault {
			prefixLength = routeDestinationPrefixLength(currentRoute.Dst)
		}
		if !linuxRouteQualifies(
			destination,
			prefixLength,
			currentRoute.Gw != nil,
			currentRoute.Type,
			currentRoute.Table,
			currentRoute.LinkIndex,
			ownInterfaceNames,
			ownInterfaceIndices,
		) {
			continue
		}
		return RouteObservationResult{Observation: netcap.RoutePresent, InterfaceIndex: currentRoute.LinkIndex}
	}
	// Every table was enumerated and no row qualified, so there is no such route. Only an absence that
	// was actually observed this way may produce the hard negative.
	return RouteObservationResult{Observation: netcap.RouteAbsent}
}

// routeDestinationPrefixLength returns the prefix length of a route destination, or -1 when it cannot
// be determined - which the acceptance rule treats as "not a host route" rather than as a rejection.
func routeDestinationPrefixLength(dst *net.IPNet) int {
	if dst == nil || dst.Mask == nil {
		return -1
	}
	ones, bits := dst.Mask.Size()
	if bits != 128 {
		return -1
	}
	return ones
}

func routeDestination(dst *net.IPNet) netip.Addr {
	if dst == nil {
		return netip.Addr{}
	}
	address, valid := netip.AddrFromSlice(dst.IP)
	if !valid {
		return netip.Addr{}
	}
	return address.Unmap()
}
