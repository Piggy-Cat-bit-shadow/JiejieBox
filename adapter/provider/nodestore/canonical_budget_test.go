package nodestore

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// This file is the formal regression for the independent review's F2 finding: the 16 MiB record
// budget was checked per buffer instead of per record. `mapValue` encoded each key and each value
// into its own scratch buffer; every scratch was measured against maxRecordBytes on its own, and the
// assembled record was never measured at all. Two 9 MiB map[string]string values therefore returned
// `nil` error and a payload of 18,874,409 bytes - 2,097,193 bytes over the declared limit.
//
// The fix charges one cumulative budget on every byte the encoder produces, including scratch bytes,
// before the byte is allocated. The tests below pin the aggregate behaviour: individually-in-budget
// pieces that sum over the limit must be refused with ErrUnsupported (the provider then records an
// Opaque record and rebuilds the node), and a record that lands exactly on the limit must still be
// encoded.

func repeatString(size int) string {
	return strings.Repeat("x", size)
}

// largeKeyMap has a key set larger than the record budget while every individual key fits easily.
// The map's keys are encoded into sorting scratch before the entries can be sorted, so this is the
// shape that proves sorting scratch is part of the same budget.
func largeKeyMap() map[string]bool {
	keys := make(map[string]bool, 17)
	for index := 0; index < 17; index++ {
		keys[strings.Repeat(string(rune('a'+index)), 1<<20)] = true
	}
	return keys
}

// TestEncodeRejectsAggregateOverBudget is the review's input plus the nested shapes the report asked
// for: top-level map, struct, array, slice, a nested map and a struct inside a map. None of these may
// produce a payload beyond maxRecordBytes, and none may return a partial payload with the error.
func TestEncodeRejectsAggregateOverBudget(t *testing.T) {
	const nineMiB = 9 << 20
	cases := []struct {
		name  string
		value any
	}{
		{
			name:  "top-level map with two large values",
			value: map[string]string{"a": repeatString(nineMiB), "b": repeatString(nineMiB)},
		},
		{
			name:  "struct with two large fields",
			value: struct{ A, B string }{A: repeatString(nineMiB), B: repeatString(nineMiB)},
		},
		{
			name:  "array of two large strings",
			value: [2]string{repeatString(nineMiB), repeatString(nineMiB)},
		},
		{
			name:  "slice of three large strings",
			value: []string{repeatString(6 << 20), repeatString(6 << 20), repeatString(6 << 20)},
		},
		{
			name:  "nested map with large leaves",
			value: map[string]map[string]string{"outer": {"a": repeatString(nineMiB), "b": repeatString(nineMiB)}},
		},
		{
			name:  "struct inside a map with large fields",
			value: map[string]struct{ A, B string }{"k": {A: repeatString(nineMiB), B: repeatString(nineMiB)}},
		},
		{
			name:  "map key and value each individually under the limit",
			value: map[string]string{repeatString(nineMiB): repeatString(nineMiB)},
		},
		{
			name:  "map whose keys alone exceed the limit",
			value: largeKeyMap(),
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			payload, err := Encode(testCase.value)
			require.ErrorIs(t, err, ErrUnsupported, "the aggregate record exceeds %d bytes", maxRecordBytes)
			require.Empty(t, payload.Bytes, "a refused value must not return a partial payload")
			require.Zero(t, payload.Hash)
			require.False(t, Equivalent(testCase.value, testCase.value), "a refused value can never compare equal")
		})
	}
}

// TestEncodeBudgetBoundary pins the exact edge: the largest record that fits is encoded and its size
// is exactly maxRecordBytes; one byte more is refused. The string case covers a single large write,
// the map cases cover scratch assembly. If the transfer from scratch to the parent buffer were
// charged a second time, the exact-size map cases would fail - so they also prove the budget is not
// double-counted.
func TestEncodeBudgetBoundary(t *testing.T) {
	t.Run("top-level string", func(t *testing.T) {
		// The sample is large enough to have the same uvarint length prefix as the boundary value.
		const sampleSize = 2 << 20
		sample := repeatString(sampleSize)
		payload, err := Encode(sample)
		require.NoError(t, err)
		header := len(payload.Bytes) - sampleSize
		exact := maxRecordBytes - header
		payload, err = Encode(repeatString(exact))
		require.NoError(t, err)
		require.Len(t, payload.Bytes, maxRecordBytes)
		_, err = Encode(repeatString(exact + 1))
		require.ErrorIs(t, err, ErrUnsupported)
	})

	t.Run("single map entry", func(t *testing.T) {
		const sampleSize = 2 << 20
		payload, err := Encode(map[string]string{"k": repeatString(sampleSize)})
		require.NoError(t, err)
		header := len(payload.Bytes) - sampleSize
		exact := maxRecordBytes - header
		payload, err = Encode(map[string]string{"k": repeatString(exact)})
		require.NoError(t, err)
		require.Len(t, payload.Bytes, maxRecordBytes)
		_, err = Encode(map[string]string{"k": repeatString(exact + 1)})
		require.ErrorIs(t, err, ErrUnsupported)
	})

	t.Run("two map entries share one budget", func(t *testing.T) {
		const sampleSize = 2 << 20
		payload, err := Encode(map[string]string{"a": repeatString(sampleSize), "b": repeatString(sampleSize)})
		require.NoError(t, err)
		header := len(payload.Bytes) - 2*sampleSize
		each := (maxRecordBytes - header) / 2
		exactTotal := header + 2*each
		payload, err = Encode(map[string]string{"a": repeatString(each), "b": repeatString(each)})
		require.NoError(t, err)
		require.Len(t, payload.Bytes, exactTotal)
		require.LessOrEqual(t, len(payload.Bytes), maxRecordBytes)
		// One byte beyond the boundary (accounting for a possibly odd header) must be refused.
		overflow := maxRecordBytes - exactTotal + 1
		_, err = Encode(map[string]string{"a": repeatString(each), "b": repeatString(each + overflow)})
		require.ErrorIs(t, err, ErrUnsupported)
	})
}
