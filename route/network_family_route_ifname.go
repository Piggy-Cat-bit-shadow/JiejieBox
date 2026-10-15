//go:build windows || linux || darwin

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
func interfaceNameByIndex(index int) (string, error) {
	iif, err := net.InterfaceByIndex(index)
	if err != nil {
		return "", err
	}
	return iif.Name, nil
}
