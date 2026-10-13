package option_test

import (
	"encoding/json"
	"testing"

	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"

	"github.com/stretchr/testify/require"
)

// The fast_path field is group-only and three-state. These tests pin the decoding contract that
// route.resolveFastPathRequest depends on: absent, explicit true and explicit false must arrive as
// three distinguishable things, and must survive a round trip unchanged.

// TestFastPathAbsentIsUnset pins the compatibility contract. A configuration that does not mention
// fast_path must decode exactly as before, with no policy attached - which is what keeps every
// existing configuration's behaviour identical.
func TestFastPathAbsentIsUnset(t *testing.T) {
	out := decodeOutbound(t, `{"type":"selector","tag":"🤖 AI","outbounds":["a"]}`)
	require.Nil(t, out.FastPath, "an absent fast_path must be unset, not false")
}

func TestFastPathExplicitTrueIsRecorded(t *testing.T) {
	out := decodeOutbound(t, `{"type":"selector","tag":"🤖 AI","fast_path":true,"outbounds":["a"]}`)
	require.NotNil(t, out.FastPath, "an explicit true must be recorded as SET")
	require.True(t, out.FastPath.Enabled)
}

// TestFastPathExplicitFalseIsDistinguishableFromUnset is the whole reason the field is a pointer.
// A plain bool could not tell "the operator switched this off" from "the operator said nothing", and
// the difference decides whether automatic AI tag detection is allowed to run.
func TestFastPathExplicitFalseIsDistinguishableFromUnset(t *testing.T) {
	unset := decodeOutbound(t, `{"type":"selector","tag":"🤖 AI","outbounds":["a"]}`)
	explicit := decodeOutbound(t, `{"type":"selector","tag":"🤖 AI","fast_path":false,"outbounds":["a"]}`)

	require.Nil(t, unset.FastPath, "unset")
	require.NotNil(t, explicit.FastPath, "an explicit false must be recorded as SET")
	require.False(t, explicit.FastPath.Enabled)
}

// TestFastPathRejectsANonBoolean keeps a typo from silently becoming a decision.
func TestFastPathRejectsANonBoolean(t *testing.T) {
	var out option.Outbound
	err := out.UnmarshalJSONContext(include.Context(t.Context()),
		[]byte(`{"type":"selector","tag":"🤖 AI","fast_path":"yes","outbounds":["a"]}`))
	require.Error(t, err, "fast_path must be a boolean")
}

// TestFastPathRoundTripPreservesAllThreeStates is the serialisation contract: writing a
// configuration back must not turn unset into false or true, and must not drop an explicit false.
func TestFastPathRoundTripPreservesAllThreeStates(t *testing.T) {
	for name, raw := range map[string]string{
		"unset": `{"type":"selector","tag":"🤖 AI","outbounds":["a","b"]}`,
		"true":  `{"type":"selector","tag":"🤖 AI","fast_path":true,"outbounds":["a","b"]}`,
		"false": `{"type":"selector","tag":"🤖 AI","fast_path":false,"outbounds":["a","b"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			decoded := decodeOutbound(t, raw)
			encoded, err := decoded.MarshalJSONContext(include.Context(t.Context()))
			require.NoError(t, err)

			// The key is present exactly when a decision was made.
			var asMap map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(encoded, &asMap))
			_, present := asMap["fast_path"]
			require.Equal(t, name != "unset", present,
				"omitempty must keep an unset field out of the output and keep a decision in it")

			// And decoding the output again lands on the same three-state value.
			redecoded := decodeOutbound(t, string(encoded))
			switch name {
			case "unset":
				require.Nil(t, redecoded.FastPath)
			case "true":
				require.NotNil(t, redecoded.FastPath)
				require.True(t, redecoded.FastPath.Enabled)
			case "false":
				require.NotNil(t, redecoded.FastPath, "an explicit false must survive a round trip")
				require.False(t, redecoded.FastPath.Enabled)
			}
		})
	}
}

// TestFastPathIsIndependentOfTrafficClass pins §5.2: the two fields share an AI matcher but are not
// derived from each other, so setting one must never populate the other.
func TestFastPathIsIndependentOfTrafficClass(t *testing.T) {
	out := decodeOutbound(t,
		`{"type":"selector","tag":"🤖 AI","fast_path":true,"outbounds":["a"]}`)
	require.Nil(t, out.TrafficClass,
		"fast_path must not imply a traffic class")

	withClass := decodeOutbound(t,
		`{"type":"selector","tag":"🤖 AI","traffic_class":"interactive","outbounds":["a"]}`)
	require.Nil(t, withClass.FastPath,
		"and a traffic class must not imply fast_path")
}
