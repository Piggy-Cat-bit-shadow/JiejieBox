package option_test

// Second-round adversarial tests for the CONFIGURATION surface of `hidden` (prompt 02 section 5).
//
// The rules this pins are the project's own strict-decoding rules, measured rather than assumed:
// a wrong JSON type is refused with the field named, an unknown key is refused on a leaf, an
// unknown outbound type is refused by the registry, and `null` means "unset" because that is what
// the decoder does for every non-pointer field in this tree - not something this key invented.

import (
	"testing"

	"github.com/sagernet/sing-box/option"

	"github.com/stretchr/testify/require"
)

// TestHiddenRejectsEveryNonBooleanValue is the "no silent coercion" half of section 5.
func TestHiddenRejectsEveryNonBooleanValue(t *testing.T) {
	for name, value := range map[string]string{
		`"true"`:  `"true"`,
		`"false"`: `"false"`,
		`1`:       `1`,
		`0`:       `0`,
		`{}`:      `{"value": true}`,
		`[]`:      `[true]`,
	} {
		t.Run(name, func(t *testing.T) {
			for _, groupType := range []string{"selector", "urltest", "loadbalance"} {
				content := `{"outbounds": [{"type": "` + groupType + `", "tag": "g", "outbounds": ["node"], "hidden": ` + value + `}]}`
				_, err := decodeOptions(t, content)
				require.Error(t, err, "a %s group must refuse hidden: %s", groupType, value)
				require.Contains(t, err.Error(), "hidden",
					"the refusal must name the field, not be a generic decode error: %v", err)
			}
		})
	}
}

// TestHiddenNullMeansUnset records what the decoder actually does with `null`, instead of inventing
// a rule for it.
//
// It is the same for every non-pointer field in this tree: `null` leaves the zero value and does not
// error. So `hidden: null` is visible, exactly like an absent key - and that is the documented
// behaviour rather than an accident of this feature.
func TestHiddenNullMeansUnset(t *testing.T) {
	options, err := decodeOptions(t, `{"outbounds": [
		{"type": "selector", "tag": "g", "outbounds": ["node"], "hidden": null}
	]}`)
	require.NoError(t, err)
	require.False(t, options.Outbounds[0].Options.(*option.SelectorOutboundOptions).Hidden,
		"null must not be read as true")
}

// TestHiddenDuplicateKeyTakesTheLastValue records the parser's duplicate-key behaviour.
//
// `encoding/json` (and the sing fork on top of it) takes the last duplicate for a struct field and
// does not error. That is a property of the decoder, shared with every other key in the tree; this
// test exists so the behaviour is written down rather than discovered in production.
func TestHiddenDuplicateKeyTakesTheLastValue(t *testing.T) {
	options, err := decodeOptions(t, `{"outbounds": [
		{"type": "selector", "tag": "g", "outbounds": ["node"], "hidden": true, "hidden": false}
	]}`)
	require.NoError(t, err)
	require.False(t, options.Outbounds[0].Options.(*option.SelectorOutboundOptions).Hidden,
		"the last duplicate wins")

	options, err = decodeOptions(t, `{"outbounds": [
		{"type": "selector", "tag": "g", "outbounds": ["node"], "hidden": false, "hidden": true}
	]}`)
	require.NoError(t, err)
	require.True(t, options.Outbounds[0].Options.(*option.SelectorOutboundOptions).Hidden,
		"and it wins in both directions")
}

// TestHiddenOnAnUnknownGroupTypeCannotBypassTheRegistry is the "unknown type cannot sneak past the
// registry" half of section 5: the type is refused before any option is applied.
func TestHiddenOnAnUnknownGroupTypeCannotBypassTheRegistry(t *testing.T) {
	_, err := decodeOptions(t, `{"outbounds": [{"type": "no-such-group", "tag": "g", "outbounds": ["node"], "hidden": true}]}`)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown outbound type")
}

// TestHiddenIsRefusedOnEveryLeafFamily extends the leaf check beyond `direct`: the key must not be
// accepted by any non-group outbound, because a leaf that stored it would be a silent no-op.
func TestHiddenIsRefusedOnEveryLeafFamily(t *testing.T) {
	for _, leafType := range []string{
		"direct", "block", "socks", "http", "shadowsocks", "trojan", "vless", "vmess", "hysteria2", "tuic",
	} {
		t.Run(leafType, func(t *testing.T) {
			content := `{"outbounds": [{"type": "` + leafType + `", "tag": "node", "hidden": true}]}`
			_, err := decodeOptions(t, content)
			require.Error(t, err, "%s must not accept hidden", leafType)
			require.Contains(t, err.Error(), "hidden", "%s refusal must name the key: %v", leafType, err)
		})
	}
}
