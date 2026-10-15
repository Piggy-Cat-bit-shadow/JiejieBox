//go:build linux

package route

import (
	"testing"

	"github.com/sagernet/netlink"

	"github.com/stretchr/testify/require"
)

// TestLinuxFilterBitMatchesTheNetlinkLibrary closes the loop the cross-platform contract test cannot.
//
// # The defect this exists for
//
// `linuxRTFilterTable` has to be nameable on every platform, because
// `TestLinuxRouteFilterIsTheDocumentedAllTablesRequest` asserts it everywhere. On Linux it is derived
// from `netlink.RT_FILTER_TABLE`; off Linux it is written as a number. The off-Linux copy said
// `1 << 8` and called it "bit 8 of the netlink route filter mask".
//
// Both halves were wrong - TABLE is the tenth name in the library's `1 << (1 + iota)` block, so 1<<10,
// and bit 8 is RT_FILTER_SRC - and the mistake was invisible from the platform it was written on,
// because there the contract test and the constant read the SAME wrong number and agreed. It was
// Linux CI that exposed it:
//
//	network_family_route_rule_test.go:259: expected: 0x100, actual: 0x400
//
// So this test asserts, on the one platform where the number is real, that the derived constant is
// what the library actually defines. A hand-edit of either arm now fails here rather than passing on
// a platform that does not use the code.
//
// # Why it is a test and not a runtime check
//
// The same assertion could panic at init on Linux. It is a test because this is a CONTRACT between
// two spellings of one fact rather than a runtime invariant: nothing about serving traffic is unsafe
// if they disagree, and a panic would turn a stale expectation into an outage. The failure mode of a
// wrong bit is a filtered-empty route dump - already covered functionally by the reader's own tests -
// and this pins the number independently of that behaviour.
func TestLinuxFilterBitMatchesTheNetlinkLibrary(t *testing.T) {
	require.Equal(t, uint64(netlink.RT_FILTER_TABLE), linuxRTFilterTable,
		"linuxRTFilterTable must be netlink.RT_FILTER_TABLE on Linux, where that constant is real; "+
			"off Linux it is a hand-written number, and this is the only place the two can be compared")

	// The positive control, so this cannot pass by both sides being zero or by the library renaming
	// the symbol to something meaningless.
	require.NotZero(t, linuxRTFilterTable,
		"RT_FILTER_TABLE is a set bit, not the empty mask: a zero mask means the early discard at "+
			"netlink route_linux.go:1038 removes every non-main table before any filter runs")
	require.NotEqual(t, uint64(0), linuxRTFilterTable&(linuxRTFilterTable-1),
		"and it must be a single bit, or it is a mask of several filter fields rather than TABLE")

	// The neighbours, named so a reordering of the library's block is legible in the failure. These
	// also document WHY 1<<8 was a plausible-looking mistake: it is a real filter field, just not
	// this one.
	require.Equal(t, uint64(1<<8), uint64(netlink.RT_FILTER_SRC),
		"bit 8 is RT_FILTER_SRC - which is what the wrong constant was actually naming")
	require.Equal(t, uint64(1<<10), uint64(netlink.RT_FILTER_TABLE),
		"and TABLE is bit 10")
}
