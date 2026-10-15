package route

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
)

// Round-3 attacks on the route-observation readers: three correctness defects, each asserted directly
// against the acceptance rule the three platform readers share.
//
// The rule is tested here rather than through a platform reader because the readers are three
// different syscalls that cannot be driven from one machine - and because the defects were all in the
// RULE's inputs, not in the syscalls. A test that called GetIpForwardTable2 could only ever run on
// Windows; this one runs everywhere and covers all three.

// TestADirectlyConnectedGlobalPrefixIsAWayOut is defect 1.
//
// A host on a routed global /64 with no gateway at all can reach every address in that prefix. An
// earlier version rejected every gateway-less route as "a local prefix, not a way out" and would have
// published RouteAbsent for such a machine, hard-refusing global IPv6 on a network that routes it -
// a wrongful refusal, the one outcome this design is required to avoid.
func TestADirectlyConnectedGlobalPrefixIsAWayOut(t *testing.T) {
	require.True(t, usableGlobalIPv6Route(
		netip.MustParseAddr("2409:8a00:1234::"),
		64,    // a /64, not a host route
		false, // NO gateway: directly connected
		0,
		3,
		nil, nil,
	), "a directly-connected global /64 is reachable without any gateway and must count as egress")
}

// TestAnOnLinkHostRouteIsNotAWayOut is the other half of defect 1, and the reason the fix is a prefix
// length rather than "accept every on-link route".
//
// The kernel installs a host route for every address an interface owns. On a machine whose only IPv6
// is a global /128 - a stub address, a CLAT or NAT64 interface - that route is the machine reaching
// ITSELF. Accepting it would report global egress on the strength of the machine's own address, which
// is exactly the "a global address is not a global route" error this whole term exists to avoid.
func TestAnOnLinkHostRouteIsNotAWayOut(t *testing.T) {
	require.False(t, usableGlobalIPv6Route(
		netip.MustParseAddr("2409:8a00:1234::5"),
		128,   // a host route
		false, // on-link
		0,
		3,
		nil, nil,
	), "an on-link host route proves only that the machine can reach itself")

	// With a gateway it is an ordinary route to one host and IS a way out - the rejection is about
	// on-link self-reachability, not about /128 as such.
	require.True(t, usableGlobalIPv6Route(
		netip.MustParseAddr("2409:8a00:1234::5"),
		128,
		true, // via a router
		0,
		3,
		nil, nil,
	))
}

// TestAnUnknownPrefixLengthIsNotARejection pins the failure direction of the fix itself.
//
// Darwin's RIB does not expose a prefix length for every entry. Treating "we do not know how wide this
// is" as "this is a host route" would invent a rejection out of missing information, and this feature
// is built to prefer a missed fast-fail over a refused connection.
func TestAnUnknownPrefixLengthIsNotARejection(t *testing.T) {
	require.True(t, usableGlobalIPv6Route(
		netip.MustParseAddr("2409:8a00:1234::"),
		-1,    // not reported
		false, // on-link
		0,
		3,
		nil, nil,
	), "an unreported prefix length must be accepted, not read as a host route")
}

// TestOnlyIPv6RoutesCanProveIPv6Egress is defect 3, and it is the one that was silently catastrophic.
//
// The Darwin reader fetches its RIB with AF_UNSPEC, so the table contains IPv4 routes. An earlier
// version never asked the destination's family, so an IPv4 0.0.0.0/0 default route - present on
// essentially every machine - satisfied it and published GlobalIPv6Egress = Provisioned on a machine
// with no IPv6 route at all. That silently disabled the fast-fail this entire feature exists for.
func TestOnlyIPv6RoutesCanProveIPv6Egress(t *testing.T) {
	for name, candidate := range map[string]struct {
		destination  netip.Addr
		prefixLength int
	}{
		"IPv4 default route":        {netip.IPv4Unspecified(), 0},
		"IPv4 public prefix route":  {netip.MustParseAddr("93.184.216.0"), 24},
		"IPv4-mapped default route": {netip.MustParseAddr("::ffff:0.0.0.0"), 0},
		"IPv4 LAN route":            {netip.MustParseAddr("192.168.1.0"), 24},
	} {
		require.Falsef(t, usableGlobalIPv6Route(
			candidate.destination,
			candidate.prefixLength,
			true, // even with a gateway, family decides
			0,
			3,
			nil, nil,
		), "%s must never prove IPv6 egress", name)
	}

	// The controls that must still pass, so this is a family filter and not a blanket refusal.
	require.True(t, usableGlobalIPv6Route(netip.IPv6Unspecified(), 0, true, 0, 3, nil, nil),
		"an IPv6 default route is egress")
	require.True(t, usableGlobalIPv6Route(netip.MustParseAddr("2409:8a00::"), 32, true, 0, 3, nil, nil),
		"and so is a routed global IPv6 prefix")
}

// TestRejectingRoutesAreNeverAWayOut pins the exclusion the brief asks to keep.
func TestRejectingRoutesAreNeverAWayOut(t *testing.T) {
	for name, routeType := range map[string]int{
		"blackhole":   rtnBlackhole,
		"unreachable": rtnUnreachable,
		"prohibit":    rtnProhibit,
		"throw":       rtnThrow,
	} {
		require.Falsef(t, usableGlobalIPv6Route(
			netip.IPv6Unspecified(), 0, true, routeType, 3, nil, nil,
		), "a %s default route is an explicit refusal, not a way out", name)
		require.Falsef(t, usableGlobalIPv6Route(
			netip.MustParseAddr("2409:8a00::"), 32, true, routeType, 3, nil, nil,
		), "nor is a %s route to a global prefix", name)
	}
}

// TestOwnInterfacesAreNeverAWayOut pins the TUN exclusion for the route term.
//
// A sing-box TUN carries ::/0 by construction, so counting its route would make the model declare
// public IPv6 reachable on the strength of sing-box's own tunnel - the same trap the interface list
// already excludes addressing for.
func TestOwnInterfacesAreNeverAWayOut(t *testing.T) {
	require.False(t, usableGlobalIPv6Route(
		netip.IPv6Unspecified(), 0, true, 0,
		7,                    // the route's interface
		[]string{"sing-box"}, // own names
		[]int{7},             // own indices
	), "a default route on this process's own TUN is not evidence about the network underneath it")

	// The same route on somebody else's interface is.
	require.True(t, usableGlobalIPv6Route(
		netip.IPv6Unspecified(), 0, true, 0,
		3,
		[]string{"sing-box"},
		[]int{7},
	))
}

// TestTheReportedFailureStillReportsAbsent is the regression guard the brief insists on: the three
// fixes above must not have made the ORIGINAL reported machine look like it has egress.
//
// The reported machine is an ethernet adapter with an IPv4 address and a fe80:: link-local address.
// The three routes such a machine actually has are: an IPv4 default route, a fe80::/64 on-link prefix,
// and a host route for its own link-local address. None of them may satisfy the rule.
func TestTheReportedFailureStillReportsAbsent(t *testing.T) {
	ownNames := []string{"sing-box"}
	ownIndices := []int{99}
	const ethernet = 3

	candidates := []struct {
		name         string
		destination  netip.Addr
		prefixLength int
		hasGateway   bool
	}{
		{"IPv4 default route (0.0.0.0/0)", netip.IPv4Unspecified(), 0, true},
		{"fe80::/64 on-link prefix", netip.MustParseAddr("fe80::"), 64, false},
		{"host route for the interface's own fe80:: address", netip.MustParseAddr("fe80::a1b2:c3d4:e5f6:7890"), 128, false},
		{"IPv4 LAN route", netip.MustParseAddr("192.168.1.0"), 24, false},
	}
	for _, candidate := range candidates {
		require.Falsef(t, usableGlobalIPv6Route(
			candidate.destination,
			candidate.prefixLength,
			candidate.hasGateway,
			0,
			ethernet,
			ownNames, ownIndices,
		), "the reported machine's %s must not count as global IPv6 egress", candidate.name)
	}

	// So the reader reaches its fall-through and reports the hard negative - which, combined with the
	// interface list having no global IPv6 addressing, is what refuses the destination.
}

// TestACustomTableRouteIsAWayOut is defect 2, and it is the one a reader of the old code would have
// believed was already handled.
//
// The old Linux reader called RouteListFiltered(FAMILY_V6, &Route{Table: RT_TABLE_MAIN}, RT_FILTER_TABLE)
// and published RouteAbsent when that came up empty. A policy-routing setup can carry the ONLY IPv6
// route in a custom table, so that published "no route" on a machine that routes global IPv6
// perfectly well - a wrongful refusal.
//
// The table is a parameter of the acceptance rule precisely so this can be asserted without a netlink
// dump of the host's real tables: the same route qualifies whichever table carries it.
func TestACustomTableRouteIsAWayOut(t *testing.T) {
	customTables := []int{
		100, // a typical `ip route add ... table 100`
		1000,
		254,                    // RT_TABLE_MAIN, the control
		linuxAllTablesSentinel, // and the sentinel itself must not be required to match
	}
	for _, table := range customTables {
		require.Truef(t, linuxRouteQualifies(
			netip.MustParseAddr("2409:8a00::"), 32, true, 0, table, 3, nil, nil,
		), "a global IPv6 route in table %d is a way out", table)
		require.Truef(t, linuxRouteQualifies(
			netip.IPv6Unspecified(), 0, true, 0, table, 3, nil, nil,
		), "and so is an IPv6 default route in table %d", table)
	}

	// A table carrying nothing usable still contributes nothing, so enumerating every table did not
	// turn into "everything qualifies".
	require.False(t, linuxRouteQualifies(
		netip.MustParseAddr("fd00::"), 64, true, 0, 100, 3, nil, nil,
	), "a unique-local route in a custom table is still not global egress")
	require.False(t, linuxRouteQualifies(
		netip.MustParseAddr("2409:8a00::"), 32, true, rtnBlackhole, 100, 3, nil, nil,
	), "nor is a blackhole route, whatever table it is in")
}

// TestLinuxEnumerationDoesNotStopAtTheMainTable pins the request the reader actually makes.
//
// Reading only RT_TABLE_MAIN was the defect. The fix is the sentinel, which sets RT_FILTER_TABLE - so
// the netlink library stops discarding non-main tables at route_linux.go:1038 - while naming a table
// id above any real one, so its equality check at :1049 rejects nothing. Both halves are load-bearing
// and both are easy to "simplify" back into the bug.
func TestLinuxEnumerationDoesNotStopAtTheMainTable(t *testing.T) {
	// The sentinel must not collide with a real table id, or the filter would select it and discard
	// everything else - the same bug in a subtler form.
	require.Greater(t, uint32(linuxAllTablesSentinel), uint32(254), "above RT_TABLE_MAIN")
	require.Greater(t, uint32(linuxAllTablesSentinel), uint32(32767), "above RT_TABLE_DEFAULT")
	require.Equal(t, uint32(0xFFFFFFFF), uint32(linuxAllTablesSentinel),
		"and above the whole 32-bit table space, so no real route can ever carry this id")

	// RT_TABLE_MAIN must remain a DIFFERENT value, which is what makes the sentinel meaningful rather
	// than a rename of the old behaviour.
	require.NotEqual(t, uint32(254), uint32(linuxAllTablesSentinel))
}
