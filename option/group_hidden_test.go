package option_test

// `hidden` is declared on the three GROUP options and on nothing shared, so these tests pin the
// configuration surface: it decodes on every group type, it is refused on a leaf, an absent field
// means false, and the generated schema says the same thing the decoder does.
//
// The runtime and display halves are pinned in protocol/group/hidden_test.go and
// daemon/group_hidden_display_test.go.

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/schema"
	singjson "github.com/sagernet/sing/common/json"

	"github.com/stretchr/testify/require"
)

// hiddenGroupProfile declares the three group types with the three settings, plus the leaves the
// groups point at.
const hiddenGroupProfile = `{
  "outbounds": [
    {"type": "direct", "tag": "node-a"},
    {"type": "direct", "tag": "node-b"},
    {"type": "selector", "tag": "hidden-selector", "outbounds": ["node-a", "node-b"], "hidden": true},
    {"type": "urltest", "tag": "hidden-urltest", "outbounds": ["node-a", "node-b"], "hidden": true},
    {"type": "loadbalance", "tag": "hidden-loadbalance", "outbounds": ["node-a", "node-b"], "hidden": true},
    {"type": "selector", "tag": "visible-selector", "outbounds": ["node-a"], "hidden": false},
    {"type": "urltest", "tag": "default-urltest", "outbounds": ["node-a"]},
    {"type": "loadbalance", "tag": "default-loadbalance", "outbounds": ["node-a"]}
  ]
}`

// TestHiddenDecodesOnEveryGroupType covers the three settings a group can express: true, explicit
// false, and absent.
func TestHiddenDecodesOnEveryGroupType(t *testing.T) {
	options, err := decodeOptions(t, hiddenGroupProfile)
	require.NoError(t, err)
	require.Len(t, options.Outbounds, 8)

	require.True(t, options.Outbounds[2].Options.(*option.SelectorOutboundOptions).Hidden,
		"a selector must keep hidden: true")
	require.True(t, options.Outbounds[3].Options.(*option.URLTestOutboundOptions).Hidden,
		"a urltest group must keep hidden: true")
	require.True(t, options.Outbounds[4].Options.(*option.LoadBalanceOutboundOptions).Hidden,
		"a loadbalance group must keep hidden: true")

	require.False(t, options.Outbounds[5].Options.(*option.SelectorOutboundOptions).Hidden,
		"hidden: false must decode as false, not as absent-and-something-else")
	require.False(t, options.Outbounds[6].Options.(*option.URLTestOutboundOptions).Hidden,
		"an absent hidden must default to visible")
	require.False(t, options.Outbounds[7].Options.(*option.LoadBalanceOutboundOptions).Hidden,
		"an absent hidden must default to visible")
}

// TestHiddenIsNotWrittenBackWhenItIsFalse pins the serialisation side: the key is `omitempty`, so a
// configuration that never used it is written back without it, and an explicit false does not become
// a permanent, unexplained key.
func TestHiddenIsNotWrittenBackWhenItIsFalse(t *testing.T) {
	options, err := decodeOptions(t, hiddenGroupProfile)
	require.NoError(t, err)

	content, err := singjson.MarshalContext(context.Background(), options)
	require.NoError(t, err)

	// Decoded again rather than string-matched: the formatter the client writes back is the subject,
	// and its whitespace is not.
	var document struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	require.NoError(t, json.Unmarshal(content, &document))
	require.Len(t, document.Outbounds, 8)

	var hiddenCount int
	for index, outbound := range document.Outbounds {
		hiddenValue, isPresent := outbound["hidden"]
		if !isPresent {
			continue
		}
		hiddenCount++
		require.Equal(t, true, hiddenValue,
			"outbound %d was written back with hidden=%v; an unset or false hidden must be omitted", index, hiddenValue)
	}
	require.Equal(t, 3, hiddenCount,
		"exactly the three groups that asked to be hidden must be written back with the key: %s", content)
}

// TestALeafOutboundRefusesHidden is the reason the field is declared on the group options rather
// than on the shared outbound envelope.
//
// A leaf that accepted the key would be a configuration that looks like it hides something and does
// nothing - exactly the silent no-op the fast_path precedent refuses for its own key.
func TestALeafOutboundRefusesHidden(t *testing.T) {
	_, err := decodeOptions(t, `{"outbounds": [{"type": "direct", "tag": "node", "hidden": true}]}`)
	require.Error(t, err, "a leaf outbound must not accept hidden")
	require.Contains(t, err.Error(), "hidden", "the refusal must name the offending key: %v", err)

	_, err = decodeOptions(t, `{"outbounds": [{"type": "block", "tag": "node", "hidden": false}]}`)
	require.Error(t, err, "hidden: false on a leaf is the same mistake")
	require.Contains(t, err.Error(), "hidden")
}

// schemaVariant is one polymorphic variant found in the generated document.
type schemaVariant struct {
	declaredType    string
	hasHidden       bool
	hiddenIsBoolean bool
	closed          bool
}

// TestTheGeneratedSchemaDeclaresHiddenOnExactlyTheGroupTypes pins the generated schema to the same
// rule the decoder enforces, from the generated document rather than from the structs.
//
// The three group types must offer it as a boolean; every other outbound type must not offer it at
// all.
func TestTheGeneratedSchemaDeclaresHiddenOnExactlyTheGroupTypes(t *testing.T) {
	generated, err := schema.Generate(include.Context(context.Background()), reflect.TypeFor[option.Options]())
	require.NoError(t, err)

	var document any
	require.NoError(t, json.Unmarshal(generated, &document))

	var variants []schemaVariant
	collectSchemaVariants(t, document, &variants)
	require.NotEmpty(t, variants, "the generated document contains no polymorphic variants")

	groupTypes := map[string]bool{"selector": true, "urltest": true, "loadbalance": true}
	encountered := make(map[string]bool, len(groupTypes))
	for _, variant := range variants {
		if groupTypes[variant.declaredType] {
			encountered[variant.declaredType] = true
			require.True(t, variant.hasHidden,
				"the schema for %s must offer hidden, or an editor rejects a key the decoder accepts",
				variant.declaredType)
			require.True(t, variant.hiddenIsBoolean,
				"hidden on %s must be a boolean", variant.declaredType)
			continue
		}
		require.False(t, variant.hasHidden,
			"the schema offers hidden on %s, which is not a group: the key belongs on the group options only",
			variant.declaredType)
		if variant.declaredType == "direct" {
			// The leaf every configuration has. Stated for one type rather than for all of them,
			// because whether a variant is closed is the generator's business and not this rule's.
			require.True(t, variant.closed,
				"the schema for direct must stay closed, so an editor rejects hidden on a leaf")
		}
	}
	for groupType := range groupTypes {
		require.True(t, encountered[groupType], "the generated schema has no %s variant", groupType)
	}
}

// collectSchemaVariants walks the generated document and records every object that declares a
// polymorphic variant, which the generator writes as {"properties": {"type": {"const": "..."}}}.
//
// A recursive walk rather than a path into $defs/Outbound: the union is nested, its location is an
// implementation detail of the generator, and the rule under test is about every variant anywhere.
func collectSchemaVariants(t *testing.T, node any, into *[]schemaVariant) {
	t.Helper()
	switch typed := node.(type) {
	case map[string]any:
		properties, _ := typed["properties"].(map[string]any)
		if properties != nil {
			typeProperty, isObject := properties["type"].(map[string]any)
			if isObject {
				if declaredType, isConst := typeProperty["const"].(string); isConst {
					*into = append(*into, readSchemaVariant(typed, properties, declaredType))
					return
				}
			}
		}
		for _, value := range typed {
			collectSchemaVariants(t, value, into)
		}
	case []any:
		for _, value := range typed {
			collectSchemaVariants(t, value, into)
		}
	}
}

func readSchemaVariant(variant map[string]any, properties map[string]any, declaredType string) schemaVariant {
	record := schemaVariant{declaredType: declaredType}
	if hiddenProperty, hasHidden := properties["hidden"]; hasHidden {
		record.hasHidden = true
		hiddenObject, _ := hiddenProperty.(map[string]any)
		record.hiddenIsBoolean = hiddenObject["type"] == "boolean"
	}
	closed, isClosed := variant["additionalProperties"].(bool)
	record.closed = isClosed && !closed
	return record
}
