package route

import (
	"net/netip"
	"syscall"
	"unsafe"

	"github.com/sagernet/sing-box/common/netcap"

	"golang.org/x/sys/windows"
)

// globalIPv6RouteObservation reads the Windows IPv6 forwarding table and answers whether this machine
// has ANY usable route to a global IPv6 destination.
//
// # Why Windows needed a new reader
//
// The other platforms already had the answer in hand and threw it away:
// route/network_environment_linux.go dumps the main table with netlink.RouteListFiltered(FAMILY_ALL,
// ...) and route/network_environment_darwin.go walks the whole BSD RIB, and both then flatten the
// result to a []netip.Addr of GATEWAYS, discarding the family and the destination prefix. Windows had
// nothing at all - systemGateways there calls GetAdaptersAddresses, which is per-adapter
// CONFIGURATION and carries no destination prefix, so it cannot even express "a ::/0 route exists".
//
// # Why the ADDRESS check alone is not enough on Windows
//
// The reported failing machine - an ethernet adapter with an IPv4 address and a fe80:: link-local
// address and no IPv6 default route - is already caught by addressing, because no interface carries a
// global IPv6 address. But that is a property of that machine, not a rule: a Hyper-V, WSL or VPN
// adapter routinely carries a global IPv6 address on an on-link-only prefix, and on such a machine an
// address-derived verdict says "IPv6 is available" while every global IPv6 connect still dies in the
// kernel with WSAENETUNREACH. The forwarding table is what separates the two. Every other way of
// asking Windows - GetAdaptersAddresses, the ND table, WSAIoctl(SIO_ROUTING_INTERFACE_QUERY) -
// answers a different question.
//
// # Why the table is read on the EVENT and not per dial
//
// This is reached from the environment recompute, which runs on a network event and never from a dial.
// The cost is one iphlpapi call beside the GetAdaptersAddresses and GetIpNetTable2 calls that path
// already makes, and the result is published with the rest of the generation - which is also the only
// way it can be owned by a generation and superseded correctly.
//
// # The own-TUN exclusion
//
// A route installed by this process's own TUN carries ::/0 and would otherwise be indistinguishable
// from a real one - the same trap the interface list already excludes addressing for. The indices come
// from the interface monitor's MyInterfaces, so the two exclusions agree by construction. An
// unregistered TUN can still be counted; that errs towards reporting the route as present, so it
// costs a missed fast-fail rather than a refused connection, and detecting a tunnel by interface name
// would be a guess about another program's naming.
func globalIPv6RouteObservation(ownInterfaceNames []string, ownInterfaceIndices []int) RouteObservationResult {
	var table *windows.MibIpForwardTable2
	err := windows.GetIpForwardTable2(windows.AF_INET6, &table)
	if err != nil || table == nil {
		// The table could not be read. Unobserved contributes nothing, so the capability degrades to
		// the addressing-only answer it had before this file existed, instead of guessing.
		return RouteObservationResult{Observation: netcap.RouteUnobserved}
	}
	defer windows.FreeMibTable(unsafe.Pointer(table))

	ownIndices := make(map[uint32]struct{}, len(ownInterfaceIndices))
	for _, index := range ownInterfaceIndices {
		ownIndices[uint32(index)] = struct{}{}
	}
	ownNames := make(map[string]struct{}, len(ownInterfaceNames))
	for _, name := range ownInterfaceNames {
		ownNames[name] = struct{}{}
	}

	for _, row := range table.Rows() {
		if row.DestinationPrefix.PrefixLength == routePrefixUnused {
			// A row the stack is not currently using. Documented sentinel in the MIB layout.
			continue
		}
		destination := prefixFromMib(&row.DestinationPrefix)
		if !destination.IsValid() {
			continue
		}
		// A default route, or a route to a global prefix. A narrower non-global route - link-local,
		// unique-local - says nothing about reaching the public Internet.
		if row.DestinationPrefix.PrefixLength != 0 && netcap.ClassifyScope(destination.Addr()) != netcap.ScopeGlobal {
			continue
		}
		nextHop := addressFromMib(&row.NextHop)
		if !nextHop.IsValid() || nextHop.IsUnspecified() || nextHop.Is4() {
			// An on-link route with no next hop is a local prefix, not a way out.
			continue
		}
		if _, isOwn := ownIndices[row.InterfaceIndex]; isOwn {
			continue
		}
		if row.InterfaceIndex != 0 {
			if name, nameErr := interfaceNameByIndex(int(row.InterfaceIndex)); nameErr == nil {
				if _, isOwn := ownNames[name]; isOwn {
					continue
				}
			}
		}
		return RouteObservationResult{Observation: netcap.RoutePresent, InterfaceIndex: int(row.InterfaceIndex)}
	}
	return RouteObservationResult{Observation: netcap.RouteAbsent}
}

// routePrefixUnused is the MIB sentinel for a destination prefix length the stack is not using.
const routePrefixUnused = 0xFF

// prefixFromMib decodes a MIB_IPFORWARD_ROW2 destination prefix.
//
// The union is reinterpreted as the IPv6 form because the table was requested for AF_INET6; the
// family field is still checked, so a row that is not IPv6 is reported as no prefix rather than
// misread as one.
func prefixFromMib(raw *windows.IpAddressPrefix) netip.Prefix {
	bits := int(raw.PrefixLength)
	if bits < 0 || bits > 128 {
		return netip.Prefix{}
	}
	address := sockaddrInetAddress(&raw.Prefix)
	if !address.IsValid() {
		return netip.Prefix{}
	}
	return netip.PrefixFrom(address, bits).Masked()
}

// addressFromMib decodes a bare address out of the RawSockaddrInet union.
func addressFromMib(raw *windows.RawSockaddrInet) netip.Addr {
	return sockaddrInetAddress(raw)
}

func sockaddrInetAddress(raw *windows.RawSockaddrInet) netip.Addr {
	switch raw.Family {
	case windows.AF_INET6:
		// RawSockaddrInet6 is the same size as the union and is the layout the kernel filled in.
		six := (*windows.RawSockaddrInet6)(unsafe.Pointer(raw))
		return netip.AddrFrom16(six.Addr)
	case windows.AF_INET:
		four := (*windows.RawSockaddrInet4)(unsafe.Pointer(raw))
		return netip.AddrFrom4(four.Addr)
	default:
		return netip.Addr{}
	}
}

// syscall import is used for the family constant on the iphlpapi path; keep the reference explicit so
// the build fails loudly if the constant ever moves.
var _ = syscall.AF_INET6
