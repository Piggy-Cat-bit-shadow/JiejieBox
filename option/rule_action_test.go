package option

import (
	"context"
	"reflect"
	"testing"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/schema"
	"github.com/sagernet/sing/common/json"

	"github.com/stretchr/testify/require"
)

func TestDNSRuleActionRespondUnmarshalJSON(t *testing.T) {
	t.Parallel()

	var action DNSRuleAction
	err := json.UnmarshalContext(context.Background(), []byte(`{"action":"respond"}`), &action)
	require.NoError(t, err)
	require.Equal(t, C.RuleActionTypeRespond, action.Action)
	require.Equal(t, DNSRouteActionOptions{}, action.RouteOptions)
}

func TestDNSRuleActionRespondRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	var action DNSRuleAction
	err := json.UnmarshalContext(context.Background(), []byte(`{"action":"respond","disable_cache":true}`), &action)
	require.ErrorContains(t, err, "unknown field")
}

func TestDNSRuleActionAdblockUnmarshalJSON(t *testing.T) {
	t.Parallel()

	var action DNSRuleAction
	err := json.UnmarshalContext(context.Background(), []byte(`{"action":"adblock"}`), &action)
	require.NoError(t, err)
	require.Equal(t, C.RuleActionTypeAdblock, action.Action)
	// The action is option-less, so nothing may be silently populated: a decoder that accepted
	// options here would be accepting configuration the action cannot honour.
	require.Equal(t, DNSRouteActionOptions{}, action.RouteOptions)
	require.Equal(t, DNSRouteActionPredefined{}, action.PredefinedOptions)
}

// TestDNSRuleActionAdblockHasNoOptions pins that adblock is a zero-configuration terminal action.
//
// Every field below is one a user might reasonably reach for - a TTL, a reject-style method, a
// cache size. None of them exists, and the point of the test is that reaching for one is an error
// with a name in it rather than a setting that is read and ignored. (`race` is deliberately NOT
// in this list: it is a field of the action envelope rather than of a variant, so the decoder
// genuinely does see it. Its rejection for adblock is enforced a layer up, in
// route/rule.validateDNSRuleAction - see TestDNSRuleActionAdblockRejectsRace there, which is
// where a config this repository accepts can be observed.)
func TestDNSRuleActionAdblockHasNoOptions(t *testing.T) {
	t.Parallel()

	for _, content := range []string{
		`{"action":"adblock","ttl":60}`,
		`{"action":"adblock","method":"drop"}`,
		`{"action":"adblock","cache_size":1024}`,
		`{"action":"adblock","rcode":"NXDOMAIN"}`,
		`{"action":"adblock","no_drop":true}`,
	} {
		var action DNSRuleAction
		err := json.UnmarshalContext(context.Background(), []byte(content), &action)
		require.ErrorContains(t, err, "unknown field", content)
	}
}

func TestDNSRuleActionAdblockRoundTrip(t *testing.T) {
	t.Parallel()

	var action DNSRuleAction
	err := json.UnmarshalContext(context.Background(), []byte(`{"action":"adblock"}`), &action)
	require.NoError(t, err)
	content, err := json.Marshal(action)
	require.NoError(t, err)
	require.JSONEq(t, `{"action":"adblock"}`, string(content))
}

// dnsRuleActionSchemaRoot is a schema root that describes exactly the DNS rule action union.
//
// options.Options cannot be the root of this test: describing it walks every option registry the
// configuration has (DNS transports, certificate providers, services) and would fail on a missing
// registry for reasons unrelated to the action. This root reaches the SAME dnsActionUnion call
// that DNSRule.DescribeSchema makes, so the variant inspected below is the shipped one.
type dnsRuleActionSchemaRoot struct{}

func (dnsRuleActionSchemaRoot) DescribeSchema(builder schema.Builder) (*schema.Node, error) {
	return builder.Define("DNSRuleAction", func() (*schema.Node, error) {
		return dnsActionUnion(builder)
	})
}

// TestDNSRuleActionAdblockSchemaShape generates the real schema node and inspects it.
//
// The distinction matters here: the schema is what a user's editor and validator read, so it is
// the first place `race` can be refused. `race` on adblock means "start the lookup this action
// exists to avoid", so the published schema must not offer it.
func TestDNSRuleActionAdblockSchemaShape(t *testing.T) {
	t.Parallel()

	content, err := schema.Generate(context.Background(), reflect.TypeFor[dnsRuleActionSchemaRoot]())
	require.NoError(t, err)

	var document struct {
		Defs map[string]struct {
			OneOf []struct {
				Properties map[string]json.RawMessage `json:"properties"`
				Required   []string                   `json:"required"`
			} `json:"oneOf"`
		} `json:"$defs"`
	}
	err = json.Unmarshal(content, &document)
	require.NoError(t, err)

	dnsRuleAction, loaded := document.Defs["DNSRuleAction"]
	require.True(t, loaded, "the generated schema must define DNSRuleAction")

	actionNameOf := func(variantProperties map[string]json.RawMessage) string {
		actionNode, hasAction := variantProperties["action"]
		if !hasAction {
			return ""
		}
		var actionProperty struct {
			Const string `json:"const"`
		}
		err := json.Unmarshal(actionNode, &actionProperty)
		require.NoError(t, err)
		return actionProperty.Const
	}

	var adblockVariant *struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	var otherActionHasRace bool
	for index := range dnsRuleAction.OneOf {
		variant := &dnsRuleAction.OneOf[index]
		actionName := actionNameOf(variant.Properties)
		if actionName == C.RuleActionTypeAdblock {
			adblockVariant = variant
			continue
		}
		if actionName == C.RuleActionTypePredefined {
			_, otherActionHasRace = variant.Properties["race"]
		}
	}
	require.NotNil(t, adblockVariant, "the DNS rule action union must declare an adblock variant")
	require.Equal(t, []string{"action"}, adblockVariant.Required, "adblock must require `action` and nothing else")
	_, hasAction := adblockVariant.Properties["action"]
	require.True(t, hasAction)
	require.Len(t, adblockVariant.Properties, 1,
		"adblock must declare no properties beyond `action`; declared: %v", adblockVariant.Properties)
	_, hasRace := adblockVariant.Properties["race"]
	require.False(t, hasRace, "adblock must not advertise `race`: it would buy an upstream lookup the action exists to avoid")
	require.True(t, otherActionHasRace, "the assertion above is only meaningful if other DNS actions do expose `race`")
}

// TestRouteRuleRejectsAdblockAction is the DNS-only boundary, checked at the decoder.
//
// `adblock` exists only in the DNS rule action union. A route rule that names it must fail to
// decode: there is no route-level implementation behind the name, and a decoder that accepted it
// would be accepting configuration the product cannot honour. The route union is also asserted
// directly, because a name that is merely absent from the decoder's switch would still be
// advertised by the published schema.
func TestRouteRuleRejectsAdblockAction(t *testing.T) {
	t.Parallel()

	var action RuleAction
	err := json.UnmarshalContext(context.Background(), []byte(`{"action":"adblock"}`), &action)
	require.ErrorContains(t, err, "unknown rule action")
	require.ErrorContains(t, err, C.RuleActionTypeAdblock)

	var rule DNSRule
	err = json.UnmarshalContext(context.Background(), []byte(`{"domain_suffix":["ads.example"],"action":"adblock"}`), &rule)
	require.NoError(t, err, "the DNS rule form is the one that must work")
	require.Equal(t, C.RuleActionTypeAdblock, rule.DefaultOptions.Action)
}
