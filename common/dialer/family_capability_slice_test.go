package dialer

import (
	"net/netip"
	"slices"
	"testing"

	"github.com/sagernet/sing-box/common/netcap"

	"github.com/stretchr/testify/require"
)

// Finalization round: the caller's backing array is not ours to write.
//
// # Where the addresses come from
//
// withoutUndialable is given addresses a RESOLVER produced - DNSRouter.Lookup, or a family result
// published on a stream. The interface says nothing about who owns the returned slice, and a
// cache-backed implementation is the obvious one: on a cache hit the same array is handed to every
// caller for the lifetime of the entry.
//
// Compacting that array in place would therefore rewrite the resolver's own cache as a side effect of
// ONE connection's filtering. The damage is not local to this dial: later callers receive answers that
// have been silently reordered and truncated, and the defect would present as a resolver bug.
//
// # The contract these tests pin
//
//	nothing refused -> the SAME slice: no copy, no allocation, no mutation
//	anything refused -> a NEW slice: the caller's array is not written at all
//
// The second half is the one that needs a test with an actual removal, because the no-op case passes
// trivially against the old implementation too. Every case below therefore asserts on the CONTENTS of
// the original array, not only on the filtered result.

// ipv6AbsentPolicy is the policy these tests filter with: IPv4 present, IPv6 provably absent. It is
// built owned-by-construction because these tests are about slice ownership, not network ownership.
func ipv6AbsentPolicy() localFamilyPolicy {
	return newLocalFamilyPolicy(netcap.FamilyCapability{
		IPv4: netcap.StateProvisioned,
		IPv6: netcap.StateUnavailable,
	})
}

// assertCallerSliceUntouched is the shared assertion: the original array must be byte-identical to the
// snapshot taken before the call.
func assertCallerSliceUntouched(t *testing.T, original []netip.Addr, snapshot []netip.Addr) {
	t.Helper()
	require.Equal(t, snapshot, original,
		"the caller's backing array was modified: this slice may be a resolver's cache entry, and "+
			"rewriting it corrupts answers that later callers receive")
}

func TestWithoutUndialableDoesNotWriteTheCallersArray(t *testing.T) {
	first4 := netip.MustParseAddr("192.0.2.11")
	second4 := netip.MustParseAddr("192.0.2.12")
	third4 := netip.MustParseAddr("192.0.2.13")
	address6 := netip.MustParseAddr("2001:db8::11")
	second6 := netip.MustParseAddr("2001:db8::12")

	testCases := []struct {
		name     string
		input    []netip.Addr
		expected []netip.Addr
		why      string
	}{
		{
			name:     "none removed",
			input:    []netip.Addr{first4, second4},
			expected: []netip.Addr{first4, second4},
			why: "the ordinary dual-stack connection: the verdict refuses nothing, so the slice is " +
				"returned as it was and nothing is allocated",
		},
		{
			name:     "mixed families with the refused one first",
			input:    []netip.Addr{address6, first4, second6, third4},
			expected: []netip.Addr{first4, third4},
			why: "the removal case: two of four are refused, and the surviving order must be the " +
				"resolver's",
		},
		{
			name:     "mixed families with the refused one in the middle",
			input:    []netip.Addr{first4, address6, second4},
			expected: []netip.Addr{first4, second4},
			why: "the middle position is the one an in-place compaction would silently corrupt, " +
				"because the write cursor passes the read cursor",
		},
		{
			name:     "all removed",
			input:    []netip.Addr{address6, second6},
			expected: nil,
			why: "nothing survives, which the caller reports as the machine's reason rather than as " +
				"an empty success",
		},
		{
			name: "local scope is exempt from removal",
			input: []netip.Addr{
				netip.MustParseAddr("fd00::1"),
				address6,
				netip.MustParseAddr("fe80::1"),
				first4,
			},
			expected: []netip.Addr{
				netip.MustParseAddr("fd00::1"),
				netip.MustParseAddr("fe80::1"),
				first4,
			},
			why: "a ULA or link-local destination is not reached over the public path the verdict " +
				"describes, so it survives even when its family is provably absent",
		},
		{
			name:     "empty input",
			input:    []netip.Addr{},
			expected: []netip.Addr{},
			why:      "nothing to filter, nothing to allocate",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			// The caller's slice, exactly as a resolver would hand it over.
			original := make([]netip.Addr, len(testCase.input))
			copy(original, testCase.input)
			snapshot := slices.Clone(original)

			filtered := withoutUndialable(original, ipv6AbsentPolicy())

			assertCallerSliceUntouched(t, original, snapshot)
			require.Equal(t, testCase.expected, filtered, "%s", testCase.why)
		})
	}
}

// TestWithoutUndialableAllocatesOnlyWhenItRemoves is the cost half of the contract.
//
// The no-op case runs on every ordinary dual-stack connection, so it must be free: the same slice
// header, no copy. Implementing the filter as "clone, then delete" would satisfy the ownership rule
// and quietly add an allocation to the most frequent operation in the process.
func TestWithoutUndialableAllocatesOnlyWhenItRemoves(t *testing.T) {
	addresses := []netip.Addr{
		netip.MustParseAddr("192.0.2.11"),
		netip.MustParseAddr("192.0.2.12"),
	}

	// Nothing refused: the same backing array comes back.
	same := withoutUndialable(addresses, ipv6AbsentPolicy())
	require.Equal(t, len(addresses), len(same))
	require.Same(t, &addresses[0], &same[0],
		"the no-removal path must return the caller's own slice, not a copy of it")

	// Something refused: a different array comes back, and the caller's is untouched.
	withIPv6 := []netip.Addr{netip.MustParseAddr("2001:db8::11"), addresses[0]}
	filtered := withoutUndialable(withIPv6, ipv6AbsentPolicy())
	require.Len(t, filtered, 1)
	require.NotSame(t, &withIPv6[0], &filtered[0],
		"the removal path must build its own slice rather than compacting the caller's")
	require.Equal(t, netip.MustParseAddr("2001:db8::11"), withIPv6[0],
		"and the refused address must still be in the caller's array")
}

// TestWithoutUndialableReadsThroughTheIndexedOrEmptyPath covers the slice that is empty but not nil,
// which is what a resolver returning no addresses for one family produces.
//
// It must not be turned into nil, because nil and empty are different answers for a caller deciding
// whether a lookup produced anything.
func TestWithoutUndialableIsStableForAnEmptyNonNilSlice(t *testing.T) {
	empty := make([]netip.Addr, 0, 4)
	filtered := withoutUndialable(empty, ipv6AbsentPolicy())
	require.NotNil(t, filtered, "an empty slice must stay an empty slice")
	require.Empty(t, filtered)
	// The CAPACITY is the observable proof that the caller's own slice header came back: a rebuilt
	// slice - one that went through cloning or appending into a fresh array - would carry a capacity
	// of its own rather than the caller's. Asserting on the address of a local slice VARIABLE would
	// prove nothing, because the two headers are distinct variables either way.
	require.Equal(t, cap(empty), cap(filtered),
		"the no-op path must return the caller's own slice header")
}

// TestRecoveryFilterAlsoLeavesTheResolversArrayAlone drives the same rule through the recovery entry
// point, so the contract is pinned where production actually calls it.
func TestRecoveryFilterAlsoLeavesTheResolversArrayAlone(t *testing.T) {
	leaf := newCapabilityLeaf(netcap.StateProvisioned, netcap.StateUnavailable)
	resolver := &resolveDialer{dialer: leaf}

	recovered := []netip.Addr{
		netip.MustParseAddr("2001:db8::50"),
		netip.MustParseAddr("192.0.2.50"),
	}
	snapshot := slices.Clone(recovered)

	filtered := resolver.withoutUndialableCandidates(recovered, resolver.captureAdmissionPolicy())
	require.Equal(t, []netip.Addr{netip.MustParseAddr("192.0.2.50")}, filtered)
	require.Equal(t, snapshot, recovered,
		"a recovered list is the resolver's data as much as a lookup result is")
}
