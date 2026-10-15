package route

import (
	"slices"

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
// A route counts when its destination is a default route or a global IPv6 prefix, its gateway is a
// real address, and it is not on one of this process's own interfaces. The RTF_GATEWAY flag is NOT
// required, unlike in systemGateways: a default route over a point-to-point link (a utun, a cellular
// PDP context) legitimately has no gateway address, and requiring one would report "no route" on
// exactly the mobile networks where IPv6 is the primary transport. A route with no gateway and no
// global destination is still rejected below, so the relaxation cannot admit a local prefix.
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
		if !destination.IsValid() {
			continue
		}
		// A default route, or a route to a global IPv6 prefix. Anything narrower and non-global says
		// nothing about reaching the public Internet.
		isDefault := destination.IsUnspecified()
		if !isDefault && netcap.ClassifyScope(destination) != netcap.ScopeGlobal {
			continue
		}
		// A route that cannot leave the machine is not a way out.
		if routeMessage.Flags&unix.RTF_REJECT != 0 || routeMessage.Flags&unix.RTF_BLACKHOLE != 0 {
			continue
		}
		if slices.Contains(ownInterfaceIndices, routeMessage.Index) {
			continue
		}
		if name, nameErr := interfaceNameByIndex(routeMessage.Index); nameErr == nil {
			if slices.Contains(ownInterfaceNames, name) {
				continue
			}
		}
		// The table was read successfully and this row is a live route to a global destination.
		return RouteObservationResult{Observation: netcap.RoutePresent, InterfaceIndex: routeMessage.Index}
	}
	// The table WAS read and no row qualified, so there is no such route. That absence is the only
	// thing that may produce the hard negative, and it is why a read failure above returns Unobserved
	// instead: "I could not look" and "I looked and there is none" must not be the same answer.
	return RouteObservationResult{Observation: netcap.RouteAbsent}
}
