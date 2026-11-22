package nodestore

import (
	"bytes"
	"encoding/binary"
	"math"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

// This file is the formal regression for the independent review's F3 float counterexample: a NaN was
// encoded as `kindFloat` followed by the raw unique counter, so the counter could be reinterpreted as
// the bit pattern of a finite float64. `Encode(math.NaN())` and `Encode(math.Float64frombits(token))`
// produced identical payload bytes and hashes while `reflect.DeepEqual` called the two values
// different - a false "equal" that could reuse a stale configuration.
//
// The fix removes non-finite floats from the ordinary float data space: NaN and ±Inf are written with
// their own kind marker and an explicit flag, and a NaN carries a unique counter *inside that
// dedicated space* (a NaN is equal to nothing, including itself, exactly like `==` and DeepEqual).
// A finite float can therefore never reproduce a non-finite record.

type nonFiniteHolder struct {
	Value float64
}

// TestNonFiniteFloatDoesNotCollideWithFinite is the review's repro: take whatever bytes the encoder
// writes for NaN, interpret the trailing word as a finite float64 and encode that value. The two
// payloads must differ. Implementations that reject NaN outright pass the same property because
// Encode returns ErrUnsupported and the value can never compare equal.
func TestNonFiniteFloatDoesNotCollideWithFinite(t *testing.T) {
	payload, err := Encode(math.NaN())
	if err != nil {
		require.ErrorIs(t, err, ErrUnsupported)
		require.False(t, Equivalent(math.NaN(), math.NaN()))
		return
	}
	require.GreaterOrEqual(t, len(payload.Bytes), 8)
	bits := binary.LittleEndian.Uint64(payload.Bytes[len(payload.Bytes)-8:])
	finite := math.Float64frombits(bits)
	if math.IsNaN(finite) {
		t.Skip("the encoded trailing word is not a finite float at this counter value")
	}
	finitePayload, err := Encode(finite)
	require.NoError(t, err)
	require.False(t, bytes.Equal(payload.Bytes, finitePayload.Bytes),
		"NaN and the finite float %v (%#x) produced identical payload bytes", finite, bits)
	require.False(t, Equivalent(math.NaN(), finite))
}

// TestNonFiniteFloatMatchesDeepEqual pins the whole non-finite domain, for both widths and for
// complex real/imaginary parts. DeepEqual on floats uses `==`: NaN is equal to nothing (not even
// itself), +Inf is equal to +Inf, -Inf is equal to -Inf, and +Inf is not -Inf.
func TestNonFiniteFloatMatchesDeepEqual(t *testing.T) {
	nan := math.NaN()
	positiveInfinity := math.Inf(1)
	negativeInfinity := math.Inf(-1)

	pairs := []struct {
		name  string
		left  any
		right any
	}{
		{"NaN versus itself", nan, nan},
		{"NaN versus +Inf", nan, positiveInfinity},
		{"NaN versus -Inf", nan, negativeInfinity},
		{"+Inf versus itself", positiveInfinity, positiveInfinity},
		{"-Inf versus itself", negativeInfinity, negativeInfinity},
		{"+Inf versus -Inf", positiveInfinity, negativeInfinity},
		{"NaN64 versus NaN64 from another call", math.NaN(), math.NaN()},
		{"float32 NaN versus itself", float32(math.NaN()), float32(math.NaN())},
		{"float32 +Inf versus itself", float32(math.Inf(1)), float32(math.Inf(1))},
		{"struct holding NaN versus itself", nonFiniteHolder{Value: nan}, nonFiniteHolder{Value: nan}},
		{"struct holding NaN versus finite", nonFiniteHolder{Value: nan}, nonFiniteHolder{Value: 1}},
		{"struct holding +Inf versus itself", nonFiniteHolder{Value: positiveInfinity}, nonFiniteHolder{Value: positiveInfinity}},
	}
	for _, pair := range pairs {
		t.Run(pair.name, func(t *testing.T) {
			require.Equal(t, reflect.DeepEqual(pair.left, pair.right), Equivalent(pair.left, pair.right))
		})
	}

	// The finite values a NaN record must never collide with: the smallest subnormal (bit pattern 1,
	// which is what a raw counter of 1 would be read as), zero, ordinary values and infinities.
	for _, finite := range []float64{0, math.Copysign(0, -1), math.SmallestNonzeroFloat64, 1, math.MaxFloat64, positiveInfinity, negativeInfinity} {
		require.False(t, Equivalent(nonFiniteHolder{Value: nan}, nonFiniteHolder{Value: finite}),
			"NaN must never compare equal to %v", finite)
	}
}

// TestNonFiniteComplexMatchesDeepEqual covers complex128 and complex64 with NaN and infinity in
// either component. The old encoding put one counter after `kindComplex` with no component markers,
// so a complex NaN was not even a well-formed pair of floats; the new encoding writes each component
// through the same non-finite-aware float space.
func TestNonFiniteComplexMatchesDeepEqual(t *testing.T) {
	cases := []struct {
		name  string
		left  any
		right any
	}{
		{"complex NaN real versus itself", complex(math.NaN(), 1), complex(math.NaN(), 1)},
		{"complex NaN imag versus itself", complex(1, math.NaN()), complex(1, math.NaN())},
		{"complex +Inf versus itself", complex(math.Inf(1), 1), complex(math.Inf(1), 1)},
		{"complex -Inf versus +Inf", complex(math.Inf(-1), 1), complex(math.Inf(1), 1)},
		{"complex NaN versus finite", complex(math.NaN(), 1), complex(1, 1)},
		{"complex64 NaN versus itself", complex64(complex(float32(math.NaN()), 0)), complex64(complex(float32(math.NaN()), 0))},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			require.Equal(t, reflect.DeepEqual(testCase.left, testCase.right), Equivalent(testCase.left, testCase.right))
		})
	}
}

// TestNonFiniteFloatIsNotAFiniteFloatRecord states the structural invariant directly: after the
// common version + type-key prefix, a non-finite float is marked with its own kind byte and an
// explicit flag, never with kindFloat. A finite float can therefore never reproduce a non-finite
// record from the unique counter's bits, whatever the counter value is.
func TestNonFiniteFloatIsNotAFiniteFloatRecord(t *testing.T) {
	nanPayload, err := Encode(math.NaN())
	if err != nil {
		require.ErrorIs(t, err, ErrUnsupported)
		return
	}
	finitePayload, err := Encode(1.0)
	require.NoError(t, err)
	infinityPayload, err := Encode(math.Inf(1))
	require.NoError(t, err)

	rootTypeKey := typeKey(reflect.TypeOf(float64(0)))
	var uvarint [binary.MaxVarintLen64]byte
	kindOffset := 1 + binary.PutUvarint(uvarint[:], uint64(len(rootTypeKey))) + len(rootTypeKey)
	require.Equal(t, kindFloat, finitePayload.Bytes[kindOffset])
	require.Equal(t, kindNonFiniteFloat, nanPayload.Bytes[kindOffset], "NaN must not use the finite float kind")
	require.Equal(t, kindNonFiniteFloat, infinityPayload.Bytes[kindOffset], "+Inf must not use the finite float kind")
	require.Equal(t, nonFiniteNaN, nanPayload.Bytes[kindOffset+1])
	require.Equal(t, nonFinitePositiveInfinity, infinityPayload.Bytes[kindOffset+1])
	require.False(t, bytes.Equal(nanPayload.Bytes, finitePayload.Bytes))
}
