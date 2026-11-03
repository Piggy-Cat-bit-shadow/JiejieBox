package oomkiller

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMergeExtraMetadataKeepsExistingKeys is the additive-contract test: a consumer that knows
// nothing about the extra key must see exactly the document it saw before.
func TestMergeExtraMetadataKeepsExistingKeys(t *testing.T) {
	recorder := &Recorder{}
	recorder.SetExtraMetadata("resourceGuard", func() any {
		return map[string]any{"level": "critical", "refused": 168}
	})

	original := []byte(`{"appVersion":"1","memoryUsage":"47 MB"}`)
	merged := recorder.mergeExtraMetadata(original)

	var document map[string]any
	require.NoError(t, json.Unmarshal(merged, &document))
	require.Equal(t, "1", document["appVersion"], "existing keys must keep their meaning")
	require.Equal(t, "47 MB", document["memoryUsage"])
	guard, ok := document["resourceGuard"].(map[string]any)
	require.True(t, ok, "the extra key must be an object at the top level")
	require.Equal(t, "critical", guard["level"])
	require.EqualValues(t, 168, guard["refused"])
}

// TestMergeExtraMetadataLeavesUnparsableDocumentsAlone: an extra must never be able to destroy
// the report it was added to.
func TestMergeExtraMetadataLeavesUnparsableDocumentsAlone(t *testing.T) {
	recorder := &Recorder{}
	recorder.SetExtraMetadata("resourceGuard", func() any { return map[string]any{"level": "normal"} })

	for _, broken := range [][]byte{
		[]byte(`not json at all`),
		[]byte(`[]`),
		[]byte(`null`),
		[]byte(``),
	} {
		require.Equal(t, broken, recorder.mergeExtraMetadata(broken), "document %q must be untouched", broken)
	}
}

// TestMergeExtraMetadataSkipsNilValues keeps the non-NetworkExtension case byte-identical: the
// provider returning nil means "this process has no guard", not "write a null".
func TestMergeExtraMetadataSkipsNilValues(t *testing.T) {
	recorder := &Recorder{}
	recorder.SetExtraMetadata("resourceGuard", func() any { return nil })

	original := []byte(`{"appVersion":"1"}`)
	require.Equal(t, original, recorder.mergeExtraMetadata(original))
}

// TestMergeExtraMetadataWithoutRegistrationIsANoOp pins the fast path: an ordinary recorder with
// no extra metadata must not re-encode its document.
func TestMergeExtraMetadataWithoutRegistrationIsANoOp(t *testing.T) {
	recorder := &Recorder{}
	original := []byte(`{"appVersion":"1"}`)
	require.Equal(t, original, recorder.mergeExtraMetadata(original))
}

// TestSetExtraMetadataIsNilSafe keeps registration optional for callers that build a recorder by
// hand.
func TestSetExtraMetadataIsNilSafe(t *testing.T) {
	var recorder *Recorder
	require.NotPanics(t, func() { recorder.SetExtraMetadata("resourceGuard", func() any { return nil }) })
}
