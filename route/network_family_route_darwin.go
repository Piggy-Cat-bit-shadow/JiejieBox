package route

import (
	"net/netip"

	"github.com/sagernet/sing-box/common/netcap"

	"golang.org/x/net/route"
	"golang.org/x/sys/unix"
)

// globalIPv6RouteObservation reads the BSD routing table and answers whether this machine has a
// usable route to a global IPv6 destination.
//
// # Why this is a second read of a table the file next door already reads
//
// net/network_environment_darwin.go already fetches this RIB for systemGateways and already decodes
// IPv6 gateways - and then flattens the result to a []netip.Addr, dropping the family and the
// destination prefix. The information was never missing on Darwin; it was discarded on the way out.
// The read is repeated here rather than threaded through systemGateways because the two answer
// different questions and are called from different places: systemGateways serves the environment
// fingerprint, this serves the family capability, and coupling them would make one caller pay for the
// other's question.
//
// # What is accepted, and what is deliberately not
//
// The acceptance rule itself is usableGlobalIPv6Route, shared with the other two readers so the three
// cannot drift apart. Two things about the INPUT to it are Darwin-specific and both were wrong before:
//
//   - The RIB is fetched with AF_UNSPEC, so it contains IPv4 routes too. An IPv4 0.0.0.0/0 default
//     route - or any public IPv4 route - says nothing about IPv6, and an earlier version accepted one
//     because it never asked the destination's family. That published GlobalIPv6Egress = Provisioned
//     on a machine with no IPv6 route at all, which silently disabled the fast-fail this whole feature
//     exists for. The family is now checked inside the shared rule.
//
//   - The RTF_GATEWAY flag is NOT required, unlike in systemGateways: a default route or an on-link
//     prefix route over a point-to-point or directly-connected link legitimately has no gateway
//     address, and requiring one would report "no route" on exactly the mobile and directly-routed
//     networks where IPv6 is the only transport.
func globalIPv6RouteObservation(ownInterfaceNames []string, ownInterfaceIndices []int) RouteObservationResult {
	rib, err := route.FetchRIB(unix.AF_UNSPEC, route.RIBType(unix.NET_RT_FLAGS), unix.RTF_UP)
	if err != nil {
		return RouteObservationResult{Observation: netcap.RouteUnobserved}
	}
	messages, err := route.ParseRIB(route.RIBTypeRoute, rib)
	if err != nil {
		return RouteObservationResult{Observation: netcap.RouteUnobserved}
	}
	for _, message := range messages {
		routeMessage, isRouteMessage := message.(*route.RouteMessage)
		if !isRouteMessage {
			continue
		}
		destination := routeAddressAt(routeMessage.Addrs, unix.RTAX_DST)
		prefixLength := routeHostRoutePrefix(routeMessage.Flags, destination)
		// A rejecting route is expressed here as a flag rather than a type.
		routeType := 0
		if routeMessage.Flags&unix.RTF_REJECT != 0 {
			routeType = rtnUnreachable
		} else if routeMessage.Flags&unix.RTF_BLACKHOLE != 0 {
			routeType = rtnBlackhole
		}
		hasGateway := false
		if len(routeMessage.Addrs) > unix.RTAX_GATEWAY {
			_, hasGateway = routeMessage.Addrs[unix.RTAX_GATEWAY].(*route.Inet6Addr)
		}
		if !usableGlobalIPv6Route(
			destination,
			prefixLength,
			hasGateway,
			routeType,
			routeMessage.Index,
			ownInterfaceNames,
			ownInterfaceIndices,
		) {
			continue
		}
		// The table was read successfully and this row is a live route to a global destination.
		return RouteObservationResult{Observation: netcap.RoutePresent, InterfaceIndex: routeMessage.Index}
	}
	// The table WAS read and no row qualified, so there is no such route. That absence is the only
	// thing that may produce the hard negative, and it is why a read failure above returns Unobserved
	// instead: "I could not look" and "I looked and there is none" must not be the same answer.
	return RouteObservationResult{Observation: netcap.RouteAbsent}
}

// routeHostRoutePrefix maps the BSD RTF_HOST flag onto the shared rule's prefix-length vocabulary.
//
// # Why the flag rather than a length
//
// golang.org/x/net/route models a destination as a bare address: Inet6Addr carries IP and ZoneID and
// nothing else, and the prefix length lives in rt_msghdr bits the package does not expose. What BSD
// does expose is RTF_HOST, set on exactly the routes a /128 would describe - including the host route
// the kernel installs for each address an interface owns, which is the case that matters here: that
// route only proves the machine can reach ITSELF.
//
// So the flag IS the prefix length for this purpose, and mapping it to 128 keeps the shared rule
// reading identically on all three platforms. A default route is never a host route, so it is excluded
// explicitly; and unknown (-1) is not a rejection, because inventing one from missing information is
// the failure direction this feature is built to avoid.
func routeHostRoutePrefix(flags int, destination netip.Addr) int {
	if !destination.IsValid() || destination.IsUnspecified() {
		return -1
	}
	if flags&unix.RTF_HOST != 0 {
		return 128
	}
	return -1
}
