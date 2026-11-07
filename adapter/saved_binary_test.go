package adapter_test

import (
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"

	"github.com/stretchr/testify/require"
)

// TestSavedBinaryVersioning pins the cache record contract: version 2 round-trips every field,
// including the URL hash that ties a provider cache to the URL it came from, while a record written
// by any other version is refused rather than misread.
func TestSavedBinaryVersioning(t *testing.T) {
	t.Parallel()
	original := &adapter.SavedBinary{
		Content:     []byte(`{"outbounds": []}`),
		LastUpdated: time.Unix(1700000000, 0),
		LastEtag:    `"etag-value"`,
		URLHash:     []byte{1, 2, 3, 4},
	}
	encoded, err := original.MarshalBinary()
	require.NoError(t, err)
	require.Equal(t, byte(2), encoded[0])

	var decoded adapter.SavedBinary
	require.NoError(t, decoded.UnmarshalBinary(encoded))
	require.Equal(t, original.Content, decoded.Content)
	require.Equal(t, original.LastUpdated, decoded.LastUpdated)
	require.Equal(t, original.LastEtag, decoded.LastEtag)
	require.Equal(t, original.URLHash, decoded.URLHash)

	for _, version := range []byte{0, 1, 3, 255} {
		corrupted := append([]byte(nil), encoded...)
		corrupted[0] = version
		var target adapter.SavedBinary
		require.ErrorContains(t, target.UnmarshalBinary(corrupted), "unsupported cache record version")
	}

	var truncated adapter.SavedBinary
	require.Error(t, truncated.UnmarshalBinary(nil))
	require.Error(t, truncated.UnmarshalBinary(encoded[:len(encoded)-1]))
}
