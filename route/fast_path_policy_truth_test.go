package route

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// TestFastPathPolicyTruth is the H1 evidence, as a table over the shapes this build can actually
// produce.
//
// # The claim
//
// The authorisation bit that canFastBypass takes is INERT on every shape where the removed AI
// fallback could have set it, and decisive only where the explicit policy or the intrinsic rule
// decides. A group's tag cannot change a verdict, so removing the fallback removed a scan, not a
// behaviour.
//
// # Why that is the right way to measure it
//
// Asserting "authorised == false" after deleting the fallback would only restate the deletion. Each
// row here instead asks the gate BOTH questions - would this flow be allowed if it were authorised,
// and is it allowed when it is not - so "the bit does not matter" is a measured property of the
// shape rather than a claim about the code that sets it.
//
// The rows come from the production inventory, not from test doubles' imagination:
//
//   - `direct` is the only outbound in this tree that implements adapter.BypassableOutbound
//     (protocol/direct/outbound.go, asserted by the interface assignment there);
//   - `selector` and `urltest` are the only outbounds that declare FastPathTransparent();
//   - everything else is either opaque (loadbalance) or not bypassable at all (any proxy leaf).
//
// # What each row records
//
//	auth             resolveFastPathRequest with no explicit policy on this shape
//	allowedIfTrue    canFastBypass(..., fastPathAuthorized = true).BypassAllowed()
//	allowedIfFalse   canFastBypass(..., fastPathAuthorized = false).BypassAllowed()
//	action           what preMatchFlow actually returns for this flow
//
// A row with allowedIfTrue == allowedIfFalse is a shape where the authorisation cannot change the
// outcome; a row where they differ is a shape where it can.
func TestFastPathPolicyTruth(t *testing.T) {
	destination := M.ParseSocksaddr("93.184.216.34:443")

	const (
		aiTag       = "🤖 AI"
		ordinaryTag = "🌐 普通代理"
	)

	type shape struct {
		name string
		// build returns the root outbound and the tag that would carry a policy for it.
		build func(tag string) adapter.Outbound
	}

	shapes := []shape{
		{
			name:  "bare direct",
			build: func(string) adapter.Outbound { return eligibleLeaf() },
		},
		{
			name: "selector -> direct",
			build: func(tag string) adapter.Outbound {
				return &transparentGroup{tag: tag, selected: eligibleLeaf()}
			},
		},
		{
			name: "urltest -> direct",
			build: func(tag string) adapter.Outbound {
				return &transparentUrltest{tag: tag, selected: eligibleLeaf()}
			},
		},
		{
			name: "selector -> selector -> direct",
			build: func(tag string) adapter.Outbound {
				inner := &transparentGroup{tag: tag + " inner", selected: eligibleLeaf()}
				return &transparentGroup{tag: tag, selected: inner}
			},
		},
		{
			name: "loadbalance -> direct",
			build: func(tag string) adapter.Outbound {
				return &opaqueGroup{tag: tag, selected: eligibleLeaf()}
			},
		},
		{
			name: "selector -> loadbalance -> direct",
			build: func(tag string) adapter.Outbound {
				return &transparentGroup{tag: tag, selected: &opaqueGroup{tag: "lb", selected: eligibleLeaf()}}
			},
		},
		{
			name: "selector -> proxy leaf",
			build: func(tag string) adapter.Outbound {
				return &transparentGroup{tag: tag, selected: &nonBypassableOutbound{}}
			},
		},
	}

	type policy struct {
		name  string
		value *option.FastPathPolicy
	}

	policies := []policy{
		{"unset", nil},
		{"true", fastPathPtr(true)},
		{"false", fastPathPtr(false)},
	}

	// observe runs one (shape, policy, tag, network) and returns the four values above.
	observe := func(t *testing.T, root adapter.Outbound, tag string, value *option.FastPathPolicy, network string) (auth, allowedIfTrue, allowedIfFalse bool, action adapter.PreMatchResult) {
		t.Helper()
		var configured FastPathPolicies
		if value != nil {
			configured = FastPathPolicies{tag: *value}
		}
		router := fastPathRouter(t, root, configured)
		metadata := fastBypassMetadata(network, destination)
		chain, err := resolveOutbound(root, &metadata, network, true)
		require.NoError(t, err)
		require.NotEmpty(t, chain)

		auth = resolveFastPathRequest(chain, router.fastPathPolicies)
		allowedIfTrue = router.canFastBypass(&metadata, destination, chain, chain[len(chain)-1], true).BypassAllowed()
		allowedIfFalse = router.canFastBypass(&metadata, destination, chain, chain[len(chain)-1], false).BypassAllowed()
		action = router.preMatchFlow(context.Background(), &metadata, destination, nil, "")
		return
	}

	// want records what each shape is expected to answer, per policy and network. `auth` is what
	// resolveFastPathRequest returns with the given configuration; the two allowed values say
	// whether the gate would carry the flow with the bit forced each way.
	type want struct {
		auth                          bool
		allowedIfTrue, allowedIfFalse bool
		bypass                        bool
	}

	table := map[string]map[string]want{
		"bare direct": {
			// One element and no group in front of it: the chain gate does not apply, so the leaf's
			// own CanBypass decides and the authorisation bit is never consulted.
			"unset": {false, true, true, true},
			"true":  {false, true, true, true},
			"false": {false, true, true, true},
		},
		"selector -> direct": {
			// Intrinsic: the transparent-chain rule authorises it, and the bit is decisive (a false
			// here would refuse the chain).
			"unset": {true, true, false, true},
			"true":  {true, true, false, true},
			"false": {false, true, false, false},
		},
		"urltest -> direct": {
			"unset": {true, true, false, true},
			"true":  {true, true, false, true},
			"false": {false, true, false, false},
		},
		"selector -> selector -> direct": {
			"unset": {true, true, false, true},
			"true":  {true, true, false, true},
			"false": {false, true, false, false},
		},
		"loadbalance -> direct": {
			// Opaque: BypassRefusedOutboundChain whatever the bit says. This is the shape on which
			// the removed AI fallback could have set the bit - and the bit is dead here.
			//
			// A policy cannot be stored on an opaque group at all (box.go refuses it), which is why
			// the explicit rows also answer false: even if such a map entry existed, the carrier
			// check would ignore it. They are kept so that a future relaxation of the carrier check
			// shows up as a changed authorisation here.
			"unset": {false, false, false, false},
			"true":  {false, false, false, false},
			"false": {false, false, false, false},
		},
		"selector -> loadbalance -> direct": {
			"unset": {false, false, false, false},
			"true":  {true, false, false, false},
			"false": {false, false, false, false},
		},
		"selector -> proxy leaf": {
			// Not bypassable at all: refused before the chain gate is reached, in every combination.
			"unset": {false, false, false, false},
			"true":  {false, false, false, false},
			"false": {false, false, false, false},
		},
	}

	for _, network := range []string{N.NetworkTCP, N.NetworkUDP} {
		for _, sh := range shapes {
			for _, pol := range policies {
				expected := table[sh.name][pol.name]

				t.Run(sh.name+"/"+pol.name+"/"+network, func(t *testing.T) {
					// The same flow under an AI-looking tag and under an ordinary one. These must be
					// indistinguishable: that is exactly what removing the tag fallback bought.
					aiAuth, aiTrue, aiFalse, aiAction := observe(t, sh.build(aiTag), aiTag, pol.value, network)
					ordinaryAuth, ordinaryTrue, ordinaryFalse, ordinaryAction := observe(t, sh.build(ordinaryTag), ordinaryTag, pol.value, network)

					require.Equal(t, ordinaryAuth, aiAuth,
						"the tag must not authorise: AI %v, ordinary %v", aiAuth, ordinaryAuth)
					require.Equal(t, ordinaryTrue, aiTrue, "the gate must not depend on the tag")
					require.Equal(t, ordinaryFalse, aiFalse, "and neither must the refusal")
					require.Equal(t, ordinaryAction.Action, aiAction.Action,
						"and neither must the verdict the flow receives")

					require.Equal(t, expected.auth, aiAuth, "authorisation")
					require.Equal(t, expected.allowedIfTrue, aiTrue, "allowed when authorised")
					require.Equal(t, expected.allowedIfFalse, aiFalse, "allowed when not authorised")

					if expected.bypass {
						require.Equal(t, adapter.PreMatchBypass, aiAction.Action,
							"this shape bypasses; the verdict is its own statement")
					} else {
						require.NotEqual(t, adapter.PreMatchBypass, aiAction.Action,
							"this shape must not bypass")
					}
				})
			}
		}
	}
}

// TestTheAuthorisationBitIsInertOnEveryNonIntrinsicShape states the H1 property directly, for the
// shapes where the removed fallback would have set the bit.
//
// If a future change makes the bit matter on an opaque chain - by relaxing the chain gate, or by
// teaching canFastBypass to accept a non-transparent chain - this test fails, which is the signal
// that a tag-based authorisation would have become reachable again.
func TestTheAuthorisationBitIsInertOnEveryNonIntrinsicShape(t *testing.T) {
	destination := M.ParseSocksaddr("93.184.216.34:443")

	shapes := map[string]func(tag string) adapter.Outbound{
		"loadbalance -> direct": func(tag string) adapter.Outbound {
			return &opaqueGroup{tag: tag, selected: eligibleLeaf()}
		},
		"selector -> loadbalance -> direct": func(tag string) adapter.Outbound {
			return &transparentGroup{tag: tag, selected: &opaqueGroup{tag: "lb", selected: eligibleLeaf()}}
		},
	}

	for name, build := range shapes {
		t.Run(name, func(t *testing.T) {
			router := fastPathRouter(t, build("🤖 AI"), FastPathPolicies{})
			metadata := fastBypassMetadata(N.NetworkTCP, destination)
			chain, err := resolveOutbound(build("🤖 AI"), &metadata, N.NetworkTCP, true)
			require.NoError(t, err)

			// Force the bit on and off. The verdict must be the same refusal, and it must name the
			// chain, not the outbound - so no amount of authorisation can reach the leaf.
			for _, authorised := range []bool{false, true} {
				verdict := router.canFastBypass(&metadata, destination, chain, chain[len(chain)-1], authorised)
				require.Equal(t, BypassRefusedOutboundChain, verdict,
					"authorised=%v must still refuse the opaque chain at the chain gate", authorised)
			}
		})
	}
}
