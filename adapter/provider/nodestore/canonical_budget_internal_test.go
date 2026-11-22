package nodestore

import (
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// These tests observe the encoder's budget bookkeeping directly. They are in the same package, so
// they can run the walker against a small limit instead of allocating tens of megabytes for every
// boundary case; the public 16 MiB boundary is pinned in canonical_budget_test.go.
//
// The invariant is: every byte is charged exactly once, before it is written, by writeByte/
// writeBytes/writeString/writeZeros. `spent` is therefore the number of bytes the encoding has
// materialised, and for a successful encode it equals the finished record length - no double
// counting of the scratch-to-parent transfer. A failed encode must stop while spent is still inside
// the limit, which is what "the check happens before the copy" means in practice.

// encodeUnderLimit runs the same path as Encode but with an explicit budget. It returns the encoder
// even on failure so the test can inspect the accounting.
func encodeUnderLimit(value any, limit int) (*encoder, Payload) {
	encoder := &encoder{limit: limit, visiting: make(map[visit]bool)}
	encoder.encodeRoot(value)
	if encoder.err != nil {
		return encoder, Payload{}
	}
	return encoder, Payload{Bytes: encoder.buf, Hash: Hash(encoder.buf)}
}

// TestEncoderSpentEqualsRecordLength proves the accounting is exact, not merely an upper bound: one
// charge per byte, including map sorting scratch. If the transfer from scratch to the parent buffer
// were charged again, every map case would report spent = 2x the record length.
func TestEncoderSpentEqualsRecordLength(t *testing.T) {
	cases := []struct {
		name string
		// deterministic is false for the shapes that carry a NaN: a NaN is equal to nothing, so its
		// record deliberately receives a fresh counter on every encode.
		deterministic bool
		value         any
	}{
		{name: "nil", deterministic: true, value: nil},
		{name: "int", deterministic: true, value: 7},
		{name: "string", deterministic: true, value: "text"},
		{name: "NaN", deterministic: false, value: math.NaN()},
		{name: "positive infinity", deterministic: true, value: math.Inf(1)},
		{name: "complex with NaN and -Inf", deterministic: false, value: complex(math.NaN(), math.Inf(-1))},
		{name: "map", deterministic: true, value: map[string]string{"a": "1", "b": "2"}},
		{name: "nested map", deterministic: true, value: map[string]map[string]string{"outer": {"inner": "value"}}},
		{
			name:          "struct",
			deterministic: true,
			value: struct {
				A string
				B []int
				C map[string]bool
			}{A: "x", B: []int{1, 2, 3}, C: map[string]bool{"k": true}},
		},
		{name: "map of structs", deterministic: true, value: map[string]struct{ V string }{"k": {V: "value"}}},
		{name: "slice", deterministic: true, value: []string{"a", "bb", "ccc"}},
		{name: "array with NaN", deterministic: false, value: [2]float64{math.Inf(1), math.NaN()}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			encoder, payload := encodeUnderLimit(testCase.value, maxRecordBytes)
			require.NoError(t, encoder.err)
			require.Equal(t, len(payload.Bytes), encoder.spent)
			// The public entry point must agree on the same bytes for deterministic shapes.
			if testCase.deterministic {
				publicPayload, err := Encode(testCase.value)
				require.NoError(t, err)
				require.Equal(t, publicPayload.Bytes, payload.Bytes)
			}
		})
	}
}

// TestEncoderBudgetBoundaryUnderSmallLimit walks the exact edge on a 64-byte budget: the largest
// fitting value is accepted, its size is exactly the limit, and one more byte is refused.
func TestEncoderBudgetBoundaryUnderSmallLimit(t *testing.T) {
	const limit = 64
	sample := "abcdefgh"
	encoder, payload := encodeUnderLimit(sample, limit)
	require.NoError(t, encoder.err)
	header := len(payload.Bytes) - len(sample)
	exact := strings.Repeat("z", limit-header)

	encoder, payload = encodeUnderLimit(exact, limit)
	require.NoError(t, encoder.err)
	require.Len(t, payload.Bytes, limit)
	require.Equal(t, limit, encoder.spent)

	encoder, _ = encodeUnderLimit(exact+"z", limit)
	require.ErrorIs(t, encoder.err, ErrUnsupported)
	require.LessOrEqual(t, encoder.spent, limit, "the budget must fail while still inside the limit")
	require.LessOrEqual(t, len(encoder.buf), limit)
}

// TestMapScratchSharesBudget is the small-limit version of the review's F2 repro: two map values that
// each fit on their own must be refused when their sum passes the budget, and a map that fits
// exactly must still encode. A per-scratch check would accept the first shape and a double-charging
// transfer would refuse the second.
func TestMapScratchSharesBudget(t *testing.T) {
	const limit = 96
	small := map[string]string{"a": "1", "b": "2"}
	encoder, _ := encodeUnderLimit(small, limit)
	require.NoError(t, encoder.err)

	// Each value's encoded size is 1 kind byte + 1 length byte + bytes; two 40-byte values plus the
	// keys and header stay under the limit, so the shape is accepted.
	fitting := map[string]string{"a": strings.Repeat("v", 30), "b": strings.Repeat("v", 30)}
	encoder, _ = encodeUnderLimit(fitting, limit)
	require.NoError(t, encoder.err)
	require.LessOrEqual(t, encoder.spent, limit)

	// The same map twice as large fails even though each individual value and each individual
	// scratch buffer is far below the limit.
	overflowing := map[string]string{"a": strings.Repeat("v", 60), "b": strings.Repeat("v", 60)}
	encoder, _ = encodeUnderLimit(overflowing, limit)
	require.ErrorIs(t, encoder.err, ErrUnsupported)
	require.LessOrEqual(t, encoder.spent, limit)
	require.LessOrEqual(t, len(encoder.buf), limit)
}
