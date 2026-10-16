package cachefile

import (
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/adapter"

	"github.com/stretchr/testify/require"
)

// Round-trip and boundary tests for the FakeIP retirement record.
//
// # Why these are unit tests and not only the end-to-end row
//
// The end-to-end row that proved the mechanism works could only report "the address still crossed" when
// the encoder and decoder disagreed. The first version of this encoding put the family byte AFTER the
// address and the decoder read it BEFORE, so every entry decoded to nothing: the record was written,
// read back, discarded, and the failure looked like the feature not working rather than like a
// serialization bug. A round trip is the cheapest possible test of a pair of functions like this and it
// would have failed immediately.
//
// The encoder is also where a wrong length is invisible: writing sixteen bytes under an IPv4 tag
// produces a value the decoder correctly refuses, so the mistake surfaces as "no ranges" rather than as
// a decoding error.

func TestRetiredRangeRoundTrip(t *testing.T) {
	// Both families, and an IPv4 range built the way the production normalizer builds one - through
	// `netip.PrefixFrom` on a sixteen-byte address, where `Addr().Is4()` is FALSE even though the bits
	// are 0..32. That is the shape which made the first encoder write an IPv6-length body under an IPv4
	// tag, so it is not an exotic row.
	original := []netip.Prefix{
		netip.MustParsePrefix("198.18.0.0/16"),
		netip.MustParsePrefix("198.20.0.0/16"),
		netip.MustParsePrefix("fdfe:dcba:9876::/64"),
	}
	// A round trip that only ever fed the encoder canonical IPv4 values would miss the defect above.
	mixed := original[:0:0]
	for _, addressRange := range original {
		mixed = append(mixed, netip.PrefixFrom(netip.AddrFrom16(addressRange.Addr().As16()), addressRange.Bits()))
	}

	for name, ranges := range map[string][]netip.Prefix{"canonical": original, "sixteen-byte-spelling": mixed} {
		decoded := unmarshalRetiredRanges(marshalRetiredRanges(ranges))
		require.Len(t, decoded, len(ranges), "%s: every entry must survive the round trip", name)
		for index, addressRange := range ranges {
			require.Equal(t, addressRange.Masked(), decoded[index],
				"%s: entry %d must decode to the range that was written", name, index)
		}
	}

	require.Nil(t, unmarshalRetiredRanges(marshalRetiredRanges(nil)),
		"an empty record decodes to nothing rather than to a phantom entry")
}

func TestRetiredRangeDecodingRefusesToInvent(t *testing.T) {
	// An unknown family tag is not this encoder's output. The decoder must stop rather than guess a
	// body length and read whatever follows as an address - a guessed range is a refusal with no
	// evidence behind it.
	require.Nil(t, unmarshalRetiredRanges([]byte{0, 1, 0x7f, 1, 2, 3, 4, 24}))

	// A truncated body is dropped, not completed from nothing.
	require.Nil(t, unmarshalRetiredRanges([]byte{0, 1, retiredFamilyIPv4, 198, 18}))

	// A count that overruns the value yields only the entries that are really there.
	partial := unmarshalRetiredRanges([]byte{0, 3, retiredFamilyIPv4, 198, 18, 0, 0, 16})
	require.Len(t, partial, 1, "the one complete entry is kept and the missing two are not invented")
	require.Equal(t, netip.MustParsePrefix("198.18.0.0/16"), partial[0])
}

// TestRetiredRangeNormalizationRefusesToWiden is the guard against a malformed entry turning the
// refusal into the blanket range block the whole design exists to avoid.
func TestRetiredRangeNormalizationRefusesToWiden(t *testing.T) {
	normalized := adapter.FakeIPNormalizeRetiredRanges([]netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/0"),     // the whole address space
		netip.MustParsePrefix("198.18.0.0/32"), // a single address: not a FakeIP range
		netip.MustParsePrefix("::/0"),
		netip.MustParsePrefix("198.18.0.0/16"),
	})
	require.Equal(t, []netip.Prefix{netip.MustParsePrefix("198.18.0.0/16")}, normalized,
		"only a range that could really have been a FakeIP range survives")

	// Host bits are masked, so a loosely spelled entry still matches the address it covers.
	masked := adapter.FakeIPNormalizeRetiredRanges([]netip.Prefix{netip.MustParsePrefix("198.18.4.7/16")})
	require.Equal(t, []netip.Prefix{netip.MustParsePrefix("198.18.0.0/16")}, masked)
}
