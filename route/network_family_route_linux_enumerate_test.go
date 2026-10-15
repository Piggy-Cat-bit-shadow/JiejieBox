//go:build linux

package route

import (
	"fmt"
	"net"
	"testing"

	"github.com/sagernet/netlink"

	"github.com/stretchr/testify/require"
)

// The Linux reader's table enumeration against the real kernel.
//
// # Why this is not a pure unit test, and why it is build-tagged
//
// The enumeration defect is a claim about what the NETLINK LIBRARY does with a filter, not about the
// acceptance rule - and the library's behaviour is a trap that reads as correct:
//
//	netlink/route_linux.go:1037
//	    if msg.Table != unix.RT_TABLE_MAIN {
//	        if filter == nil || filterMask&RT_FILTER_TABLE == 0 {
//	            continue
//	        }
//	    }
//
// Non-main tables are discarded BEFORE any filter runs unless RT_FILTER_TABLE is set, so a test that
// only called linuxRouteQualifies would pass against the broken reader. The call itself has to be
// exercised.
//
// It is build-tagged rather than skipped at runtime because it imports netlink, which does not compile
// on Windows or Darwin. A runtime skip would drag a Linux-only dependency into every platform's test
// binary and then decline to use it.
//
// # Why the assertions are shaped to avoid a vacuous pass
//
// The first version of this test asserted `len(all) >= len(mainOnly)`, which is satisfied by 0 >= 0.
// On a machine whose IPv6 routes all live outside RT_TABLE_MAIN - precisely the machine policy routing
// creates - it would report success while proving nothing.
//
// So the superset claim is asserted only when there is something to be a superset of, and when there
// is not, the test SKIPS and says why. A skipped test that names what it would have proved is worth
// more than a green one that measured nothing.
func TestLinuxEnumerationReturnsEveryTable(t *testing.T) {
	all, err := netlink.RouteListFiltered(netlink.FAMILY_V6, linuxAllTablesFilter, linuxAllTablesMask)
	require.NoError(t, err, "the all-tables request must be a valid netlink filter")

	mainOnly, err := netlink.RouteListFiltered(netlink.FAMILY_V6, &netlink.Route{Table: linuxTableMain}, netlink.RT_FILTER_TABLE)
	require.NoError(t, err)

	// Every table the kernel reported, for the record: the non-main counts are exactly what the old
	// main-table-only reader could not see.
	tables := map[int]int{}
	for _, route := range all {
		tables[route.Table]++
	}
	t.Logf("IPv6 routes: all-tables read = %d across tables %v; main-table read = %d",
		len(all), tables, len(mainOnly))
	for table, count := range tables {
		if table != linuxTableMain {
			t.Logf("NOTE: %d IPv6 route(s) live in non-main table %d", count, table)
		}
	}

	// THE VACUITY GUARD. Without a non-empty main table there is nothing for the all-tables read to be
	// a superset of, and 0 >= 0 would "pass".
	if len(mainOnly) == 0 {
		t.Skip("this machine has no IPv6 routes in RT_TABLE_MAIN, so a superset relation cannot be " +
			"asserted: 0 >= 0 would pass without evidence. The request SHAPE is still verified by " +
			"TestLinuxRouteFilterIsTheDocumentedAllTablesRequest, which runs on every platform, and " +
			"TestLinuxCustomTableRouteIsVisibleToTheReader proves the enumeration directly where it " +
			"can install a probe route. Run this on a host with IPv6 routes to prove it here too.")
	}

	allKeys := make(map[string]struct{}, len(all))
	for _, route := range all {
		allKeys[linuxRouteKey(route)] = struct{}{}
	}
	for _, route := range mainOnly {
		require.Containsf(t, allKeys, linuxRouteKey(route),
			"the all-tables read is missing a main-table route, so the filter narrows instead of "+
				"widening: %s", linuxRouteKey(route))
	}
	require.GreaterOrEqual(t, len(all), len(mainOnly),
		"the all-tables read cannot contain fewer routes than one of the tables it covers")
}

// TestLinuxCustomTableRouteIsVisibleToTheReader is the enumeration proof that does not depend on the
// host happening to have a policy-routing setup.
//
// It installs the shape the reader has to survive: an IPv6 route whose ONLY home is a custom table. If
// the request narrows to RT_TABLE_MAIN - or names a table that matches nothing - the reader cannot see
// this route and will publish RouteAbsent on a machine that routes global IPv6.
//
// It needs CAP_NET_ADMIN, so it skips where that is unavailable rather than failing: a test that fails
// in an unprivileged developer shell would just get deleted, and the skip names what it would prove.
func TestLinuxCustomTableRouteIsVisibleToTheReader(t *testing.T) {
	const (
		probeTable = 4242
		probeRoute = "2409:8a00:beef::/48"
		probeGW    = "fe80::1"
	)

	handle, err := netlink.NewHandle()
	if err != nil {
		t.Skipf("cannot open a netlink handle (%v), so a custom-table route cannot be installed", err)
	}
	defer handle.Close()

	linkIndex := firstIPv6CapableLinkIndex(t, handle)
	if linkIndex == 0 {
		t.Skip("no IPv6-capable interface to attach a probe route to")
	}

	// The gateway is an on-link link-local address, which is the shape a real custom-table route has.
	var gateway [16]byte
	copy(gateway[:], net.ParseIP(probeGW).To16())
	_, probePrefix, parseErr := net.ParseCIDR(probeRoute)
	if parseErr != nil {
		t.Fatalf("probe route %q is not a CIDR: %v", probeRoute, parseErr)
	}
	probe := &netlink.Route{
		LinkIndex: linkIndex,
		Dst:       probePrefix,
		Gw:        net.IP(gateway[:]),
		Table:     probeTable,
		Family:    netlink.FAMILY_V6,
	}
	if addErr := handle.RouteAdd(probe); addErr != nil {
		t.Skipf("cannot install a probe route in table %d (%v); re-run with CAP_NET_ADMIN to prove "+
			"that a custom-table IPv6 route is visible to the reader", probeTable, addErr)
	}
	defer func() {
		if delErr := handle.RouteDel(probe); delErr != nil {
			t.Logf("could not remove the probe route from table %d: %v", probeTable, delErr)
		}
	}()

	routes, err := netlink.RouteListFiltered(netlink.FAMILY_V6, linuxAllTablesFilter, linuxAllTablesMask)
	require.NoError(t, err)

	// Half one: the route is VISIBLE. An invisible route is the original defect.
	visible := 0
	for _, route := range routes {
		if route.Table == probeTable {
			visible++
		}
	}
	require.Positivef(t, visible,
		"a route installed ONLY in table %d is invisible to the reader, so it would publish "+
			"RouteAbsent on a machine that routes this prefix - the wrongful refusal this fix exists "+
			"to prevent", probeTable)

	// Half two: the route is ACCEPTED. Visible-but-rejected fails the reader just as completely.
	accepted := false
	for _, route := range routes {
		if route.Table != probeTable {
			continue
		}
		if linuxRouteQualifies(
			routeDestination(route.Dst),
			routeDestinationPrefixLength(route.Dst),
			route.Gw != nil,
			route.Type,
			route.Table,
			route.LinkIndex,
			nil, nil,
		) {
			accepted = true
			break
		}
	}
	require.True(t, accepted, "the custom-table route is visible but the acceptance rule rejects it")
}

func firstIPv6CapableLinkIndex(t *testing.T, handle *netlink.Handle) int {
	t.Helper()
	links, err := handle.LinkList()
	if err != nil {
		return 0
	}
	for _, link := range links {
		attributes := link.Attrs()
		if attributes == nil {
			continue
		}
		if attributes.Flags&net.FlagUp == 0 || attributes.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, addrErr := handle.AddrList(link, netlink.FAMILY_V6)
		if addrErr != nil || len(addresses) == 0 {
			continue
		}
		return attributes.Index
	}
	return 0
}

func linuxRouteKey(route netlink.Route) string {
	destination := "<default>"
	if route.Dst != nil {
		destination = route.Dst.String()
	}
	gateway := "<on-link>"
	if route.Gw != nil {
		gateway = route.Gw.String()
	}
	return fmt.Sprintf("%s|%s|table=%d|oif=%d", destination, gateway, route.Table, route.LinkIndex)
}
