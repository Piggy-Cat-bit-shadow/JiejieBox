package route

import (
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/common/netcap"

	"github.com/stretchr/testify/require"
)

// The route readers, asserted against whatever network the test machine is actually on.
//
// The per-route DECISION is covered exhaustively by network_family_route_rule_test.go, which needs no
// platform. What is left is the seam between that decision and the reader that loops over real routes:
// a reader that ignored the rule's answer, or a rule the reader never consults, would leave every test
// in the other file passing while the feature stayed broken.

// TestTheRealReaderAgreesWithTheRuleOnThisMachine runs the actual reader and asserts only the
// properties that must hold on EVERY network.
//
// It deliberately does not assert the answer. This machine has a working global IPv6 route, so a test
// demanding RouteAbsent would fail on every developer machine and prove nothing. What it pins is that
// the reader terminates, answers with exactly one of the three states, and is reproducible - a reader
// that flapped would produce a verdict no network event explains.
func TestTheRealReaderAgreesWithTheRuleOnThisMachine(t *testing.T) {
	result := globalIPv6RouteObservation([]string{"sing-box", "tun0"}, []int{-1})

	require.Contains(t, []netcap.RouteObservation{
		netcap.RoutePresent, netcap.RouteAbsent, netcap.RouteUnobserved,
	}, result.Observation, "the reader must answer with exactly one of the three states")

	second := globalIPv6RouteObservation([]string{"sing-box", "tun0"}, []int{-1})
	require.Equal(t, result.Observation, second.Observation,
		"the reader must be a pure function of the routing table it reads")

	t.Logf("this machine: global IPv6 route observation = %s", result.Observation)
}

// TestTheRuleRejectsWhatARealTableContains is the seam guard.
//
// The reader returns RoutePresent on the FIRST route the rule accepts, so the rule is the only thing
// deciding what counts. These are the shapes a real IPv6-capable machine's table always contains, and
// getting any of them wrong is the difference between a working feature and a silent one.
func TestTheRuleRejectsWhatARealTableContains(t *testing.T) {
	// Present in essentially every table, and must never decide the verdict.
	require.False(t, usableGlobalIPv6Route(netip.IPv4Unspecified(), 0, true, 0, 1, nil, nil),
		"an IPv4 default route is on every machine and says nothing about IPv6")
	require.False(t, usableGlobalIPv6Route(netip.MustParseAddr("192.168.1.0"), 24, false, 0, 1, nil, nil),
		"nor does a LAN route")
	require.False(t, usableGlobalIPv6Route(netip.MustParseAddr("fe80::"), 64, false, 0, 1, nil, nil),
		"nor a link-local prefix, which is what the reported machine actually has")

	// And the one that must decide it.
	require.True(t, usableGlobalIPv6Route(netip.IPv6Unspecified(), 0, true, 0, 1, nil, nil))
	require.True(t, usableGlobalIPv6Route(netip.MustParseAddr("2409:8a00::"), 32, true, 0, 1, nil, nil))

	// A MULTICAST route is not a way out, and it is present on essentially every IPv6-capable
	// machine - Windows installs ff00::/8 on every such interface.
	//
	// This row is the one that was missing, and its absence made the whole egress term inert on
	// Windows. The scope test is what let it through: netcap.ClassifyScope answers ScopeGlobal for
	// anything that is not loopback, link-local, unspecified or private, and ff00:: is none of them.
	// MEASURED on the affected host, with the physical links carrying link-local addresses only:
	//
	//	ZONE-ROW ACCEPTED dest=ff00::/8 multicast=true scope=global    (7 such rows)
	//	own TUN excluded -> observation=route-present, from index 1, a multicast row
	//
	// so globalIPv6RouteObservation reported RoutePresent and GlobalIPv6Egress stayed Provisioned on
	// a machine with no unicast IPv6 route at all - defeating the commit whose entire purpose is to
	// refuse global IPv6 when there is no route to one.
	//
	// The rows below are spelled with the real prefix AND with a full multicast address, because the
	// kernel may report either as the destination of the ff00::/8 row.
	require.False(t, usableGlobalIPv6Route(netip.MustParseAddr("ff00::"), 8, false, 0, 1, nil, nil),
		"an ff00::/8 multicast route is installed on every IPv6-capable interface and is not a way "+
			"out; accepting it makes the global-IPv6-egress verdict always Provisioned")
	require.False(t, usableGlobalIPv6Route(netip.MustParseAddr("ff02::1"), 128, false, 0, 1, nil, nil),
		"nor is an all-nodes multicast destination")
	require.False(t, usableGlobalIPv6Route(netip.MustParseAddr("ff00::"), 8, true, 0, 1, nil, nil),
		"and a gateway on the row does not make a multicast route a way out either")
}
