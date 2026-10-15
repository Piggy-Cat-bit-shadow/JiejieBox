//go:build !linux

package route

// linuxAllTablesRequest is the non-Linux arm of the netlink contract check.
//
// # Why this exists on platforms that do not use netlink
//
// The Linux reader's filter is a pair of values TYPED by a Linux-only dependency, so the values
// themselves cannot live in a file that compiles everywhere - which is precisely how the previous
// wrong filter (a table id above the table space, which matches no route and therefore filtered every
// route OUT) survived review: the only test that could see it could not run on the machine where it
// was written.
//
// So the EXPECTATION is restated here, and TestLinuxRouteFilterIsTheDocumentedAllTablesRequest asserts
// it on every platform. On Linux that test reads the real filter the reader passes to netlink; here it
// reads these constants. The duplication is the point: it is a tripwire that fires on any platform
// when the contract changes, and it is two integers wide.
//
// The values are unix.RT_TABLE_UNSPEC and netlink.RT_FILTER_TABLE, and the Linux arm returns those
// same constants rather than copies.
func linuxAllTablesRequest() (table int, mask uint64) {
	return linuxTableUnspec, linuxRTFilterTable
}

// linuxRTFilterTable is netlink.RT_FILTER_TABLE, bit 8 of the netlink route filter mask.
const linuxRTFilterTable = uint64(1 << 8)
