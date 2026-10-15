package route

import "github.com/sagernet/sing-box/common/netcap"

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
