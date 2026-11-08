package libbox

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

// Cross-language boundary checks for the existing bridge entries.
//
// The bridge is where a Go error and a user-provided string cross into another runtime. These tests
// pin what the CURRENT entries do, so a future signature change cannot silently make them panic or
// return a non-UTF-8 string:
//
//   - invalid UTF-8 in the input must not panic and must never produce invalid UTF-8 output;
//   - a nil result accompanies a non-nil error (nil-success semantics);
//   - the returned error still carries a machine-inspectable cause.

func TestFormatConfigInvalidUTF8StaysInsideTheBridge(t *testing.T) {
	input := "{\"outbounds\":[{\"type\":\"direct\",\"tag\":\"bad-\xff\"}]}"
	formatted, err := FormatConfig(input)
	if err != nil {
		require.Nil(t, formatted, "an error must not carry a partial result")
		return
	}
	require.NotNil(t, formatted)
	require.True(t, utf8.ValidString(formatted.Value),
		"the bridge must never return invalid UTF-8 to the caller")
}

func TestAnalyzeConfigInvalidUTF8StaysInsideTheBridge(t *testing.T) {
	input := "{\"outbounds\":[{\"type\":\"direct\",\"tag\":\"bad-\xff\"}]}"
	encoded, err := AnalyzeConfig(input)
	if err != nil {
		require.Nil(t, encoded, "an error must not carry a partial result (nil-success semantics)")
		return
	}
	require.NotNil(t, encoded)
	require.True(t, utf8.ValidString(encoded.Value))
	var document map[string]any
	require.NoError(t, json.Unmarshal([]byte(encoded.Value), &document))
}

func TestAnalyzeConfigErrorKeepsAMachineInspectableCause(t *testing.T) {
	_, err := AnalyzeConfig(`{"inbounds": "not-an-array"}`)
	require.Error(t, err)
	var typeError *json.UnmarshalTypeError
	require.True(t, errors.As(err, &typeError) || strings.Contains(err.Error(), "inbound"),
		"the parse error must remain inspectable or name the failing field, got %v", err)
}
