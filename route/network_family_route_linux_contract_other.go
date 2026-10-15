//go:build !linux

package route

// linuxRTFilterTable is netlink.RT_FILTER_TABLE as a plain number: bit 10 of the netlink route filter
// mask, 0x400 / 1024.
//
// # The wrong value this used to carry, and how it survived
//
// It was `1 << 8`, with a comment calling it "bit 8 of the netlink route filter mask". Both were
// wrong. The library's const block starts at `RT_FILTER_PROTOCOL uint64 = 1 << (1 + iota)`, so with
// TABLE the tenth name in the list it is 1<<10; bit 8 is RT_FILTER_SRC. Viewing the real value on
// Linux CI is what exposed it: `expected: 0x100, actual: 0x400`.
//
// It survived because of WHICH platform the mistake could be seen from. This constant and the
// cross-platform contract test both live off Linux, so on Windows they agreed with each other, and
// the test passed - while the machine where the number is real is the machine the test could not be
// judged on. The test's own header claims it guards against exactly this ("the only test that could
// see it could not run on the machine where it was written"); it had become an instance of it.
//
// The value is now the same number the Linux arm derives from netlink.RT_FILTER_TABLE, and
// TestLinuxFilterBitMatchesTheNetlinkLibrary asserts on Linux that the derived constant really is
// that number - so the hand-written copy can no longer be self-consistently wrong on the platform
// where it is used.
func linuxAllTablesRequest() (table int, mask uint64) {
	return linuxTableUnspec, linuxRTFilterTable
}

// linuxRTFilterTable is netlink.RT_FILTER_TABLE, bit 10 of the netlink route filter mask (0x400).
const linuxRTFilterTable = uint64(1 << 10)
