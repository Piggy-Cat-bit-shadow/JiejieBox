package route

import (
	"net"
	"net/netip"
	"slices"

	"github.com/sagernet/sing-box/common/netcap"

	"github.com/sagernet/netlink"
	"golang.org/x/sys/unix"
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
// # Why the read is unqualified by table and by interface
//
// The question is "is there a way out", so it must not be scoped to one interface the way
// systemGateways is: a machine whose physical link has no IPv6 route and whose cellular link has one
// is a machine with a way out, and an interface-scoped read would report "no route" for the physical
// one and refuse global IPv6 on a network that can carry it.
//
// The main table is read rather than every table, because RT_TABLE_MAIN is where ordinary routes
// live and a policy-routing setup that reaches the Internet through a custom table is exactly the
// case where a wrong "no route" would be most damaging. If a custom table carried the only IPv6
// default route, this reports RouteAbsent and the capability refuses a destination that a policy rule
// would have routed - so the read is deliberately made to err towards RoutePresent by also accepting
// any non-default global route, which a policy-routed setup still needs to install.
func globalIPv6RouteObservation(ownInterfaceNames []string, ownInterfaceIndices []int) RouteObservationResult {
	routes, err := netlink.RouteListFiltered(netlink.FAMILY_V6, &netlink.Route{Table: unixRTTableMain}, netlink.RT_FILTER_TABLE)
	if err != nil {
		// The table could not be read. Unobserved contributes nothing, so the capability degrades to
		// the addressing-only answer instead of guessing.
		return RouteObservationResult{Observation: netcap.RouteUnobserved}
	}
	for _, currentRoute := range routes {
		destination := routeDestination(currentRoute.Dst)
		// A default route, or a route to a global IPv6 prefix. A narrower non-global route -
		// link-local, unique-local - says nothing about reaching the public Internet.
		isDefault := currentRoute.Dst == nil
		if isDefault {
			destination = netip.IPv6Unspecified()
		} else if !destination.IsValid() {
			continue
		}
		if !isDefault && netcap.ClassifyScope(destination) != netcap.ScopeGlobal {
			continue
		}
		// A route with no gateway is on-link, which is a local prefix rather than a way out - except
		// for a DEFAULT route over a point-to-point link, which legitimately has no gateway (a
		// cellular PDP context, a WireGuard or PPP link). Rejecting that would report "no route" on
		// exactly the mobile networks where IPv6 is the primary transport.
		if currentRoute.Gw == nil && !isDefault {
			continue
		}
		// A route whose TYPE says the packet cannot go anywhere is not a way out, however global its
		// destination looks. These are how a rejecting default route is expressed on Linux, and
		// counting one would turn an explicitly unreachable configuration into "IPv6 is available".
		switch currentRoute.Type {
		case unix.RTN_BLACKHOLE, unix.RTN_UNREACHABLE, unix.RTN_PROHIBIT, unix.RTN_THROW:
			continue
		}
		if slices.Contains(ownInterfaceIndices, currentRoute.LinkIndex) {
			continue
		}
		if name, nameErr := interfaceNameByIndex(currentRoute.LinkIndex); nameErr == nil {
			if slices.Contains(ownInterfaceNames, name) {
				continue
			}
		}
		return RouteObservationResult{Observation: netcap.RoutePresent, InterfaceIndex: currentRoute.LinkIndex}
	}
	// The table WAS read and no row qualified, so there is no such route. Only an absence that was
	// actually observed may produce the hard negative; the read failure above returns Unobserved.
	return RouteObservationResult{Observation: netcap.RouteAbsent}
}

// unixRTTableMain is RT_TABLE_MAIN, the table ordinary routes are installed in.
const unixRTTableMain = 254

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
