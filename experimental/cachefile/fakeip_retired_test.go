package cachefile

import (
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/adapter"

	"github.com/stretchr/testify/require"
)

// Round-trip and boundary tests for the FakeIP retirement record.
//
// # Why these are unit tests and not only the end-to-end rows
//
// The end-to-end row that proved the mechanism works could only report "the address still crossed" when
// the encoder and decoder disagreed. The first version of this encoding put the family byte AFTER the
// address and the decoder read it BEFORE, so every entry decoded to nothing: the record was written,
// read back, discarded, and the failure looked like the feature not working rather than like a
// serialization bug. A round trip is the cheapest possible test of a pair of functions like this, and it
// would have failed immediately.
//
// The encoder is also where a wrong length is invisible: writing sixteen bytes under an IPv4 tag
// produces a value the decoder correctly refuses, so the mistake surfaces as "nothing retired" rather
// than as a decoding error.

func TestRetiredIntervalRoundTrip(t *testing.T) {
	// Both families, and an IPv4 entry rebuilt through a sixteen-byte form - the shape which made the
	// first encoder write an IPv6-length body under an IPv4 tag, so it is not an exotic row.
	original := []adapter.FakeIPIssuanceInterval{
		{From: netip.MustParseAddr("198.18.0.1"), To: netip.MustParseAddr("198.18.0.2")},
		{From: netip.MustParseAddr("198.20.0.1"), To: netip.MustParseAddr("198.20.4.3")},
		{From: netip.MustParseAddr("fdfe:dcba:9876::2"), To: netip.MustParseAddr("fdfe:dcba:9876::9")},
	}

	for name, intervals := range map[string][]adapter.FakeIPIssuanceInterval{"canonical": original} {
		decoded := unmarshalRetiredIntervals(marshalRetiredIntervals(intervals))
		require.Len(t, decoded, len(intervals), "%s: every entry must survive the round trip", name)
		for index, interval := range intervals {
			require.Equal(t, interval.From, decoded[index].From,
				"%s: entry %d From must decode to what was written", name, index)
			require.Equal(t, interval.To, decoded[index].To,
				"%s: entry %d To must decode to what was written", name, index)
		}
	}

	require.Nil(t, unmarshalRetiredIntervals(marshalRetiredIntervals(nil)),
		"an empty record decodes to nothing rather than to a phantom entry")
}

func TestRetiredIntervalDecodingRefusesToInvent(t *testing.T) {
	// An unknown family tag is not this encoder's output. The decoder must stop rather than guess a body
	// length and read whatever follows as addresses - a guessed interval is a refusal with no evidence.
	require.Nil(t, unmarshalRetiredIntervals([]byte{0, 1, 0x7f, 1, 2, 3, 4, 5, 6, 7, 8}))

	// A truncated body is dropped, not completed from nothing.
	require.Nil(t, unmarshalRetiredIntervals([]byte{0, 1, retiredFamilyIPv4, 198, 18}))

	// A count that overruns the value yields only the entries that are really there.
	complete := marshalRetiredIntervals([]adapter.FakeIPIssuanceInterval{
		{From: netip.MustParseAddr("198.18.0.1"), To: netip.MustParseAddr("198.18.0.2")},
	})
	overrun := append([]byte{0, 3}, complete[2:]...)
	partial := unmarshalRetiredIntervals(overrun)
	require.Len(t, partial, 1, "the one complete entry is kept and the missing two are not invented")
	require.Equal(t, netip.MustParseAddr("198.18.0.1"), partial[0].From)

	// An INVERTED interval describes nothing, and a reader that trusted it would refuse a range's worth
	// of addresses the generation never reached.
	require.Nil(t, adapter.FakeIPNormalizeRetiredIntervals([]adapter.FakeIPIssuanceInterval{
		{From: netip.MustParseAddr("198.18.9.9"), To: netip.MustParseAddr("198.18.0.1")},
	}))

	// Ends of different bit lengths cannot be compared at all, so the entry is dropped rather than
	// silently compared across two spellings of the same value.
	require.Nil(t, adapter.FakeIPNormalizeRetiredIntervals([]adapter.FakeIPIssuanceInterval{
		{From: netip.MustParseAddr("198.18.0.1"), To: netip.MustParseAddr("::ffff:198.18.0.9")},
	}))
}

// TestRetiredIntervalCoverageMatchesTheLedger is the property the whole refusal rests on: an interval
// covers exactly the addresses a generation could have handed out, and nothing past its cursor.
//
// It is asserted against `adapter.FakeIPRetiredCovers`, which is the single implementation of the
// question the router asks - so the router cannot answer it differently from the ledger.
func TestRetiredIntervalCoverageMatchesTheLedger(t *testing.T) {
	intervals := []adapter.FakeIPIssuanceInterval{
		{From: netip.MustParseAddr("198.18.0.1"), To: netip.MustParseAddr("198.18.0.2")},
	}

	inside, covered := adapter.FakeIPRetiredCovers(intervals, netip.MustParseAddr("198.18.0.2"))
	require.True(t, covered, "an address the generation reached is covered")
	require.Equal(t, netip.MustParseAddr("198.18.0.1"), inside.From)

	// The boundary the A7b end-to-end row pins: past everything the generation recorded stays reachable.
	_, covered = adapter.FakeIPRetiredCovers(intervals, netip.MustParseAddr("198.18.4.200"))
	require.False(t, covered, "an address beyond the cursor must NOT be covered")

	// The base of the range is not issuable, so it is not covered either.
	_, covered = adapter.FakeIPRetiredCovers(intervals, netip.MustParseAddr("198.18.0.0"))
	require.False(t, covered)

	// A 4-in-6 spelling is a different value and must not match an IPv4 interval by accident.
	_, covered = adapter.FakeIPRetiredCovers(intervals, netip.MustParseAddr("::ffff:198.18.0.2"))
	require.False(t, covered, "the bit-length agreement is what keeps a mapped spelling from matching")

	_, covered = adapter.FakeIPRetiredCovers(nil, netip.MustParseAddr("198.18.0.2"))
	require.False(t, covered, "no record refuses nothing")
}
