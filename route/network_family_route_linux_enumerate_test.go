//go:build linux

package route

import (
	"fmt"
	"testing"

	"github.com/sagernet/netlink"

	"github.com/stretchr/testify/require"
)

// The Linux reader's table enumeration, asserted against the real kernel.
//
// # Why this cannot be a pure unit test, and why it is build-tagged
//
// The custom-table defect is a claim about what the NETLINK LIBRARY does with a filter mask, not about
// the acceptance rule - and the library's behaviour is a trap that reads as correct:
//
//	netlink/route_linux.go:1037
//	    if msg.Table != unix.RT_TABLE_MAIN {
//	        if filter == nil || filterMask&RT_FILTER_TABLE == 0 {
//	            continue
//	        }
//	    }
//
// Non-main tables are discarded BEFORE any filter runs unless RT_FILTER_TABLE is set. A test that only
// called linuxRouteQualifies would pass against the buggy reader, because the bug is in which routes
// ever reach that function - so the call itself is exercised here.
//
// It is build-tagged rather than skipped at runtime because it imports netlink, which does not compile
// on Windows or Darwin. A runtime skip would drag a Linux-only dependency into every platform's test
// binary and then decline to use it.
func TestLinuxEnumerationReturnsEveryTable(t *testing.T) {
	all, err := netlink.RouteListFiltered(netlink.FAMILY_V6, &netlink.Route{Table: linuxAllTablesSentinel}, netlink.RT_FILTER_TABLE)
	require.NoError(t, err, "the sentinel request must be a valid netlink filter")

	mainOnly, err := netlink.RouteListFiltered(netlink.FAMILY_V6, &netlink.Route{Table: linuxMainTable}, netlink.RT_FILTER_TABLE)
	require.NoError(t, err)

	t.Logf("IPv6 routes: every-table read = %d, main-table-only read = %d", len(all), len(mainOnly))

	// THE PROPERTY THE BUG VIOLATED. If the sentinel behaved like RT_TABLE_MAIN, the two reads would be
	// identical and any route in a custom table would be invisible - which is exactly how a
	// policy-routed machine came to be reported as having no IPv6 route at all.
	require.GreaterOrEqual(t, len(all), len(mainOnly),
		"the every-table read must cover at least the main table")

	// Every main-table route must appear in the every-table read: it is a superset, or the sentinel is
	// filtering something out rather than widening.
	allKeys := make(map[string]struct{}, len(all))
	for _, route := range all {
		allKeys[linuxRouteKey(route)] = struct{}{}
	}
	for _, route := range mainOnly {
		require.Containsf(t, allKeys, linuxRouteKey(route),
			"the every-table read is missing a main-table route, so the sentinel filters instead of "+
				"widening: %s", linuxRouteKey(route))
	}
}

const linuxMainTable = 254

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
