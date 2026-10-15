package route

import (
	"net"
	"net/netip"

	"github.com/sagernet/sing-box/common/netcap"

	"github.com/sagernet/netlink"
	"golang.org/x/sys/unix"
)

// linuxAllTablesFilter and linuxAllTablesMask are the netlink filter that returns EVERY IPv6 route
// table. They live here rather than beside their constants because netlink is a Linux-only dependency
// and these values are TYPED by it; the reasoning is with the constants in network_family_route.go.
//
// # The library trap they exist for
//
// Non-main tables are discarded before any filter is applied:
//
//	netlink/route_linux.go:1037
//	    if msg.Table != unix.RT_TABLE_MAIN {
//	        if filter == nil || filterMask&RT_FILTER_TABLE == 0 {
//	            continue        // <-- every non-main table is dropped HERE
//	        }
//	    }
//
// So the mask bit is mandatory to see them at all. But setting the bit is only half of it, because the
// equality test that follows treats a NAMED table very differently from an unnamed one:
//
//	netlink/route_linux.go:1049
//	    case filterMask&RT_FILTER_TABLE != 0 && filter.Table != unix.RT_TABLE_UNSPEC &&
//	         route.Table != filter.Table:
//	        continue
//
// Naming a real table keeps only that table. Naming RT_TABLE_UNSPEC - zero - short-circuits the whole
// condition, so nothing is rejected on this test and every table survives it.
//
// # Why the request also has to leave the table unset
//
// The same zero is what keeps a table selector out of the REQUEST. route_linux.go emits one only when
// the table is non-zero:
//
//	if route.Table > 0 {
//	    if route.Table >= 256 { msg.Table = RT_TABLE_UNSPEC; RTA_TABLE = ... } else { msg.Table = route.Table }
//	}
//
// With Table == 0 none of that runs, so the dump goes out with no RTA_TABLE and the kernel answers for
// every table it has. Naming any value at all - including a "sentinel" above the table space - asks
// the kernel a narrower question instead.
//
// # The wrong fix this replaces
//
// A table id above the 32-bit space (0xFFFFFFFF) was used here on the reasoning that it "matches
// nothing, so nothing is filtered". It matches nothing on the CLIENT test at :1049 - which is exactly
// the problem, because that test is what KEEPS a route. A value that never matches therefore rejected
// every real route and the read returned an empty list, while also asking the kernel for a table that
// cannot exist. The correct value is the one the library itself documents as "no table": zero.
var linuxAllTablesFilter = &netlink.Route{Table: unix.RT_TABLE_UNSPEC}

// linuxAllTablesMask is the mask that goes with linuxAllTablesFilter. RT_FILTER_TABLE must be set, or
// the early discard at route_linux.go:1038 removes every non-main table before any filter runs.
const linuxAllTablesMask = netlink.RT_FILTER_TABLE

// linuxRTFilterTable is netlink.RT_FILTER_TABLE as a plain number, so the cross-platform contract test
// can compare the Linux arm against a value it can name without importing netlink.
//
// # Why this is derived here and asserted numerically next door
//
// The number has to appear somewhere that compiles off Linux, because the contract test runs on every
// platform. It used to appear as a HAND-WRITTEN `1 << 8` in the non-Linux arm, described in a comment
// as "bit 8 of the netlink route filter mask" - and that was wrong: the library's const block is
//
//	RT_FILTER_PROTOCOL uint64 = 1 << (1 + iota)   -> 1<<1
//	RT_FILTER_SCOPE 1<<2, TYPE 1<<3, TOS 1<<4, IIF 1<<5, OIF 1<<6, DST 1<<7, SRC 1<<8,
//	GW 1<<9, TABLE 1<<10, ...
//
// so bit 8 is RT_FILTER_SRC (0x100) and RT_FILTER_TABLE is 1<<10 (0x400).
//
// The consequence is the shape this repository keeps having to re-learn: the test passed on Windows,
// where BOTH sides read the same wrong hand-written constant, and failed on Linux, where this
// constant is the real one. A check and the thing it checks agreeing with each other is not evidence
// that either is right - and a test that can only pass on the platform that does not use the code
// under test proves nothing about it.
//
// The cross-platform contract test now asserts this constant, and
// TestLinuxFilterBitMatchesTheNetlinkLibrary (linux-only, test-build) asserts THAT this equals
// netlink.RT_FILTER_TABLE. Together the two remove the hand-written number from the chain: a drift in
// either arm fails on a platform where the drift is real.
const linuxRTFilterTable = uint64(netlink.RT_FILTER_TABLE)

// linuxAllTablesRequest reports the (table, mask) pair this reader sends, as plain numbers, so the
// contract above can be asserted on every platform instead of only where netlink compiles.
func linuxAllTablesRequest() (table int, mask uint64) {
	return linuxAllTablesFilter.Table, uint64(linuxAllTablesMask)
}

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
// The library drops non-main tables before it applies any filter, unless RT_FILTER_TABLE is set. So a
// filter is unavoidable - but the VALUE has to be the one the library reads as "no table", because the
// same value decides both whether the rows survive the client-side comparison and whether a table
// selector is sent to the kernel. linuxAllTablesFilter and linuxAllTablesMask carry the whole argument;
// the short version is that the table must be RT_TABLE_UNSPEC and the mask must contain
// RT_FILTER_TABLE. The routes that survive are then filtered here, by us, on the facts that matter.
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
	routes, err := netlink.RouteListFiltered(netlink.FAMILY_V6, linuxAllTablesFilter, linuxAllTablesMask)
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
