package libbox

// The binding's unsigned-to-signed conversion.
//
// gomobile does not bind uint64 fields: it skips them with "unsupported type", which is why the
// platform could not read the wire sequence or drop count at all. The values are exposed as int64
// accessors instead; these tests pin the conversion, especially the saturation at MaxInt64 so a
// pathological counter can never wrap into a negative number (a negative "dropped" would read as
// "nothing was lost").

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTheBindingConvertsTheWireCounters(t *testing.T) {
	batch := &ConnectionEvents{sequence: 42, dropped: 7}
	require.EqualValues(t, 42, batch.Sequence())
	require.EqualValues(t, 7, batch.Dropped())

	batch = &ConnectionEvents{sequence: math.MaxInt64, dropped: math.MaxInt64}
	require.EqualValues(t, math.MaxInt64, batch.Sequence())
	require.EqualValues(t, math.MaxInt64, batch.Dropped())

	// One past the signed range saturates instead of wrapping to a negative value.
	batch = &ConnectionEvents{sequence: math.MaxInt64 + 1, dropped: math.MaxUint64}
	require.EqualValues(t, math.MaxInt64, batch.Sequence())
	require.EqualValues(t, math.MaxInt64, batch.Dropped())
	require.Positive(t, batch.Dropped(), "a saturated drop count must stay positive")
}
