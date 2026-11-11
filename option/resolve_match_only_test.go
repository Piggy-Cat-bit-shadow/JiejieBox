package option_test

import (
	"context"
	"testing"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json"

	"github.com/stretchr/testify/require"
)

// `match_only` is one optional boolean on the `resolve` action, and the three ways it can arrive are
// not interchangeable:
//
//	omitted / false   the historical action, which publishes dial candidates
//	true              publishes matching evidence and NO dial candidate
//	a wrong type      a configuration error, not a silent false
//
// The third case is the one a naive `bool` field would swallow: sing's JSON decoder rejects a type
// mismatch, and this test pins that it actually runs for this field - an action is decoded through
// two passes (the union discriminator, then the variant struct), and a field that the variant struct
// never names would be reported as an unknown field instead.
func TestResolveActionMatchOnlyDecodes(t *testing.T) {
	t.Parallel()

	t.Run("true is carried through", func(t *testing.T) {
		var action option.RuleAction
		err := json.UnmarshalContext(context.Background(), []byte(`{"action":"resolve","match_only":true}`), &action)
		require.NoError(t, err)
		require.Equal(t, C.RuleActionTypeResolve, action.Action)
		require.True(t, action.ResolveOptions.MatchOnly,
			"match_only:true must reach the action, not be dropped as an unknown field")
	})

	t.Run("false is equivalent to omitted", func(t *testing.T) {
		var withFalse, withOmitted option.RuleAction
		require.NoError(t, json.UnmarshalContext(context.Background(),
			[]byte(`{"action":"resolve","match_only":false}`), &withFalse))
		require.NoError(t, json.UnmarshalContext(context.Background(),
			[]byte(`{"action":"resolve"}`), &withOmitted))
		require.False(t, withFalse.ResolveOptions.MatchOnly)
		require.Equal(t, withOmitted, withFalse,
			"an explicit false and an omitted field must produce the same action, or the default "+
				"behaviour is not really the default")
	})

	t.Run("a scalar type mismatch is a configuration error", func(t *testing.T) {
		for _, document := range []string{
			`{"action":"resolve","match_only":"true"}`,
			`{"action":"resolve","match_only":1}`,
		} {
			var action option.RuleAction
			err := json.UnmarshalContext(context.Background(), []byte(document), &action)
			require.Error(t, err, "a non-boolean match_only must be refused, not coerced: %s", document)
		}
	})

	t.Run("empty containers follow the repository's existing lenient form", func(t *testing.T) {
		// Measured rather than assumed: this repository's decoder treats `{}`, `[]` and `null` as
		// "not set" for EVERY scalar option - `disable_cache` behaves identically - because the
		// action is decoded through badjson's object form. What matters for this field is that the
		// result is the DEFAULT and not `true`: a malformed value must never switch the mode on.
		for _, document := range []string{
			`{"action":"resolve","match_only":{}}`,
			`{"action":"resolve","match_only":[]}`,
			`{"action":"resolve","match_only":null}`,
		} {
			var action option.RuleAction
			require.NoError(t, json.UnmarshalContext(context.Background(), []byte(document), &action), document)
			require.False(t, action.ResolveOptions.MatchOnly,
				"a malformed match_only must decode to the default, never to true: %s", document)
		}
	})
}

// The roundtrip is what a configuration editor and an export/import cycle rely on: a field the
// decoder accepts but the encoder drops would silently turn `match_only: true` back into the
// default action.
func TestResolveActionMatchOnlyRoundtrips(t *testing.T) {
	t.Parallel()

	var decoded option.RuleAction
	require.NoError(t, json.UnmarshalContext(context.Background(),
		[]byte(`{"action":"resolve","server":"dns-remote","strategy":"prefer_ipv6","match_only":true,"timeout":"3s"}`), &decoded))

	encoded, err := json.Marshal(decoded)
	require.NoError(t, err)

	var redecoded option.RuleAction
	require.NoError(t, json.Unmarshal(encoded, &redecoded))
	require.True(t, redecoded.ResolveOptions.MatchOnly,
		"match_only must survive a roundtrip; encoded form: %s", encoded)

	// The neighbours must survive too: match_only is an addition, not a replacement.
	require.Equal(t, "dns-remote", redecoded.ResolveOptions.Server)
	require.EqualValues(t, C.DomainStrategyPreferIPv6, redecoded.ResolveOptions.Strategy)
	require.Equal(t, decoded.ResolveOptions.Timeout, redecoded.ResolveOptions.Timeout)
}

// An omitted or false field must not be emitted. This is not cosmetic: `match_only:false` in an
// exported configuration would be a claim the user wrote it, and every diff would carry it forever.
func TestResolveActionMatchOnlyIsOmittedWhenFalse(t *testing.T) {
	t.Parallel()

	var action option.RuleAction
	require.NoError(t, json.UnmarshalContext(context.Background(),
		[]byte(`{"action":"resolve","disable_cache":true}`), &action))

	encoded, err := json.Marshal(action)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "match_only",
		"a false match_only must not be serialised: %s", encoded)
}
