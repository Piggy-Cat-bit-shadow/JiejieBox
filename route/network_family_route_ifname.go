package route

import "net"

// interfaceNameByIndex maps an interface index to its name, so a route can be matched against this
// process's own interfaces by NAME as well as by index.
//
// Both matter and neither subsumes the other. The index is what the route table reports, and it is
// stable for the life of an interface - but a TUN created by another program in this process's own
// stack may be registered with the monitor by name while the platform reports it under a different
// index space (Windows has separate IPv4 and IPv6 index spaces, which is why the monitor carries
// both). Matching on the union of the two is what makes the own-TUN exclusion hold on the platforms
// where the index spaces disagree.
//
// # Why this file carries NO build tag
//
// It used to be tagged `windows || linux || darwin`, which is the set of platforms with a route
// READER of their own - and the tag was load-bearing for the build rather than for the behaviour.
// The caller is usableGlobalIPv6Route in network_family_route.go, a file with no build tag, so on
// every other platform the package failed to COMPILE:
//
//	GOOS=freebsd go build ./route/
//	route/network_family_route.go:94:22: undefined: interfaceNameByIndex
//
// The helper itself has nothing to do with route readers: net.InterfaceByIndex is standard library
// and behaves identically on every platform Go supports, so there is no platform on which this
// implementation is wrong and none on which a stub would be more honest. Removing the tag is
// therefore the whole fix, and it is the one that cannot be silently re-tagged: the file has no
// platform suffix in its name (unlike the readers' _linux/_windows/_darwin), so a future tag here
// would have to be written on purpose.
//
// On a platform with no reader this function and usableGlobalIPv6Route are both unreachable rather
// than wrong - network_family_route_stub.go answers RouteUnobserved there, so nothing consults the
// acceptance rule. Unreachable package-level code is not an error, and it is the safe polarity: a
// stub returning an error would be a second definition of "no observation" that the reader could
// disagree with.
func interfaceNameByIndex(index int) (string, error) {
	iif, err := net.InterfaceByIndex(index)
	if err != nil {
		return "", err
	}
	return iif.Name, nil
}
