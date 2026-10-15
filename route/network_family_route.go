package route

import (
	"net/netip"
	"slices"

	"github.com/sagernet/sing-box/common/netcap"
)

// usableGlobalIPv6Route is the shared acceptance rule: would the kernel actually use this route to
// reach a global IPv6 destination?
//
// It lives here, in a file with no build tag, so the three platform readers cannot drift apart on what
// "a way out" means. Each of them is a different primitive - GetIpForwardTable2, a netlink dump, a BSD
// RIB - and the one thing they must agree on exactly is this predicate, because a platform that
// answered it differently would either refuse a working destination or miss the fast-fail.
//
// # What it accepts
//
//	destination     a GLOBAL IPv6 prefix, or nothing at all (a default route)
//	prefixLength    -1 for a default route or an unknown length, otherwise the route's prefix length
//	hasGateway      whether the route names a next hop; a route without one is ON-LINK
//	routeType       the kernel's route type, where the platform has one
//	linkIndex       the interface the route egresses
//
// # Why an on-link global prefix IS accepted
//
// A host on a routed global /64 with no gateway at all can reach every address in that prefix. An
// earlier version rejected every gateway-less route as "a local prefix, not a way out", which was
// wrong for exactly that topology and would have refused global IPv6 on a directly-connected network -
// a wrongful refusal, the one outcome this design is required to avoid.
//
// # Why a /128 is NOT, and why that is the same rule rather than an exception to it
//
// The kernel installs a host route for every address an interface owns. On a machine whose only IPv6
// is a global /128 - a stub address, a NAT64 or CLAT interface, an address assigned without a prefix
// route - that host route is the machine reaching ITSELF, and counting it would report global egress
// on the strength of the machine's own address. That is precisely the "a global address is not a
// global route" error this whole term exists to avoid, so an on-link route must be shorter than a host
// route to count.
//
// # Why an unknown prefix length is not a rejection
//
// -1 means the platform did not report one. Treating "we do not know how wide this is" as "this is a
// host route" would invent a rejection out of missing information, and the failure direction of this
// whole feature is chosen to be a missed fast-fail rather than a refused connection.
func usableGlobalIPv6Route(
	destination netip.Addr,
	prefixLength int,
	hasGateway bool,
	routeType int,
	linkIndex int,
	ownInterfaceNames []string,
	ownInterfaceIndices []int,
) bool {
	// # The three shapes a route destination arrives in
	//
	//	no destination at all      a default route (Linux: Dst == nil)
	//	the unspecified address    also a default route (BSD RIB and MIB report ::/0 this way)
	//	a concrete prefix          an on-link, directly-connected, or gatewayed destination
	//
	// The first two are the same fact spelled two ways, and both are ways out by definition - which is
	// why they skip the scope test rather than being measured against it.
	//
	// # The family check comes FIRST, and that ordering is the whole of defect 3
	//
	// 0.0.0.0 is "the unspecified address" just as :: is, so a rule that tested IsUnspecified() before
	// the family would read an IPv4 default route as an IPv6 one and accept it. That is precisely how
	// the Darwin reader published GlobalIPv6Egress = Provisioned on a machine with no IPv6 route at
	// all, and no amount of care later in the function recovers from getting it wrong here.
	if destination.Is4() || destination.Is4In6() {
		return false
	}
	isDefault := !destination.IsValid() || destination.IsUnspecified()
	if !isDefault {
		if netcap.ClassifyScope(destination) != netcap.ScopeGlobal {
			return false
		}
	}
	switch routeType {
	case rtnBlackhole, rtnUnreachable, rtnProhibit, rtnThrow:
		// The kernel was told to refuse this destination. It is not a way out however global it looks,
		// and counting one would turn an explicitly unreachable configuration into "IPv6 is available".
		return false
	}
	if !isDefault && !hasGateway && prefixLength == 128 {
		// An on-link HOST route: the machine reaching an address it already owns. See the note above
		// for why this is the one on-link case that proves nothing.
		return false
	}
	if slices.Contains(ownInterfaceIndices, linkIndex) {
		return false
	}
	if name, nameErr := interfaceNameByIndex(linkIndex); nameErr == nil {
		if slices.Contains(ownInterfaceNames, name) {
			return false
		}
	}
	return true
}

// Route types that mean "the kernel will not carry this", in the RTN_* numbering Linux uses. They are
// written numerically so this file stays platform-neutral; the Linux reader passes its own
// unix.RTN_* values, which are these numbers.
const (
	rtnBlackhole   = 6
	rtnUnreachable = 7
	rtnProhibit    = 8
	rtnThrow       = 9
)

// Table ids this reader reasons about, written as numbers so the netlink contract can be asserted on
// every platform rather than only where netlink compiles.
//
// netlink is a Linux-only dependency, so anything TYPED by it has to live behind a build tag - and a
// contract that can only be asserted on Linux is a contract that gets broken by whoever edits the
// reader from a Mac. These two integers are the whole contract; the Linux file builds the filter from
// them.
//
// The values are fixed by the kernel UAPI: linuxTableUnspec is unix.RT_TABLE_UNSPEC and linuxTableMain
// is unix.RT_TABLE_MAIN.
const (
	// linuxTableUnspec is the library's spelling of "no table", and it is load-bearing in two separate
	// places: it is what makes the client-side comparison at route_linux.go:1049 keep every table
	// instead of only a named one, and it is what stops a table selector being added to the request at
	// route_linux.go:872. Any other value narrows the read.
	linuxTableUnspec = 0
	linuxTableMain   = 254
)

// linuxRouteQualifies is the per-route half of the Linux read, split out so the decision is testable
// without a netlink dump of the host's real tables.
//
// table is accepted unconditionally: a policy-routing setup can carry the ONLY IPv6 route in a custom
// table, and reading RT_TABLE_MAIN alone would then publish RouteAbsent on a machine that routes
// global IPv6 perfectly well. The parameter is here so the intent is explicit at the call site rather
// than implied by the absence of a check.
func linuxRouteQualifies(
	destination netip.Addr,
	prefixLength int,
	hasGateway bool,
	routeType int,
	table int,
	linkIndex int,
	ownInterfaceNames []string,
	ownInterfaceIndices []int,
) bool {
	_ = table
	return usableGlobalIPv6Route(
		destination,
		prefixLength,
		hasGateway,
		routeType,
		linkIndex,
		ownInterfaceNames,
		ownInterfaceIndices,
	)
}

// RouteObservationResult is one platform's answer to "does this machine have a route to a global IPv6
// destination", together with which interface carries it.
//
// # Why this is not part of netcap.Evaluate's existing input
//
// It is: it arrives as netcap.InterfaceObservation.GlobalIPv6Route. What lives here is only the
// PLATFORM half - the reading of a route table - because that is genuinely three different primitives
// (GetIpForwardTable2, a netlink dump, a BSD RIB) and each one needs its own file, its own build tag
// and its own failure mode. netcap stays the model and never learns which platform it is on.
//
// # Why the zero value is "unobserved"
//
// Because every caller that cannot read a table, does not implement this, or fails mid-read must
// contribute NOTHING. A zero-value result therefore has to mean "no opinion", not "no route": the
// distinction between RouteAbsent and RouteUnobserved is the entire safety property of this feature,
// and a struct left empty is the most likely way to get it wrong.
type RouteObservationResult struct {
	Observation netcap.RouteObservation
	// InterfaceIndex is the interface the route was found on, for diagnostics. Zero when nothing was
	// found.
	InterfaceIndex int
}

// machineGlobalIPv6Route reads the route observation for the machine as a whole.
//
// The question is whether a route exists on ANY interface, because the kernel picks the interface when
// a packet is sent - so the reading is machine-level even though it is applied to every interface's
// observation. Applying one machine-level answer to all of them keeps netcap's OR over interfaces
// meaning "the machine has a way out", which is the fact being established.
func (r *NetworkManager) machineGlobalIPv6Route() netcap.RouteObservation {
	if r.routeObservation != nil {
		// Test-only injection. A real route table cannot be made to report "no global IPv6 route" on
		// request on a developer's machine, and the whole point of this term is the state where the
		// table HAS no such route - which is precisely the state a test machine cannot be put into.
		// The hook is nil in production and costs one predictable branch per network event.
		return r.routeObservation()
	}
	var ownNames []string
	var ownIndices []int
	if r.interfaceMonitor != nil {
		ownNames = r.interfaceMonitor.MyInterfaces()
	}
	if r.interfaceFinder != nil {
		for _, ownName := range ownNames {
			iif, err := r.interfaceFinder.ByName(ownName)
			if err != nil || iif == nil {
				continue
			}
			ownIndices = append(ownIndices, iif.Index)
		}
	}
	return globalIPv6RouteObservation(ownNames, ownIndices).Observation
}
