package route

import (
	"context"
	"net/netip"
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// bypassableNonDirectOutbound is a leaf that CAN bypass but is NOT `direct` - a third-party outbound
// that hands the flow to the platform itself, or a future direct-like type. It exists because
// without it the intrinsic rule cannot be told apart from "any bypassable leaf ignores its group".
//
// Its Type() is deliberately not C.TypeDirect, and it is the only fake here that separates the two
// questions the code has to keep separate: "may this leaf be bypassed at all" (adapter.BypassableOutbound)
// and "is this leaf the direct outbound" (the intrinsic rule's narrower test).
//
// It is also what makes the POLICY PRECEDENCE tests meaningful again. A direct leaf behind transparent
// groups is now authorised on its own, so a precedence table built on one would answer `true` for
// every row and could not fail. This type keeps the policy scan reachable.
type bypassableNonDirectOutbound struct {
	adapter.Outbound
	canBypass atomic.Int32
}

func (o *bypassableNonDirectOutbound) Type() string      { return "block" }
func (o *bypassableNonDirectOutbound) Tag() string       { return "🌐 普通代理" }
func (o *bypassableNonDirectOutbound) Network() []string { return []string{N.NetworkTCP, N.NetworkUDP} }
func (o *bypassableNonDirectOutbound) CanBypass(string, netip.Addr) bool {
	return o.canBypass.Load() == 1
}

// newBypassableNonDirectOutbound returns the double pre-set as capable, which is the common case.
func newBypassableNonDirectOutbound() *bypassableNonDirectOutbound {
	leaf := &bypassableNonDirectOutbound{}
	leaf.canBypass.Store(1)
	return leaf
}

// Direct Fast Path is the DEFAULT for a transparent chain over `direct`, and an explicit decision
// overrides the default.
//
// # The defect these tests pin
//
// A user wraps `direct` in a selector for grouping - one "国内直连" selector with a single direct
// member - and writes no `fast_path` anywhere, because they are not thinking about the fast path at
// all. Before this change that ABSENCE was read as "not authorised": the chain gate returned
// BypassRefusedOutboundChain and the flow silently degraded to userspace direct, losing a fast path
// the very same direct outbound takes when it is named without a group in front of it. The group did
// not turn the feature off; it said nothing, and silence was treated as refusal.
//
// # Silence is not a decision, and a decision is not silence
//
// The rule is "a transparent selection layer that says NOTHING does not switch the fast path off".
// A group that writes `"fast_path": false` HAS said something, and the explicit decision is honoured
// first: the intrinsic grant below applies only when no element of the chain states a policy. Before
// that precedence was fixed, an explicit false was found by the policy scan and then overruled,
// which made the documented tri-state unusable as an off switch on the only leaf that can bypass.
//
// # What must stay true, and is tested on the other side of these assertions
//
// Only the group AUTHORISATION gate is involved. `direct.CanBypass` and every other blocker in
// canFastBypass are reached afterwards and unchanged, an opaque group anywhere in the chain still
// refuses, and every non-direct shape keeps the previous policy precedence - explicit values, nested
// outermost-first resolution, and the AI tag matcher. The tests below assert both directions,
// because a relaxation that also relaxed eligibility would pass a one-sided test.
//
// The two tests that matter most for safety are
// TestDirectFastPathOpaqueGroupStillRefusesUnsetPolicy and
// TestDirectFastPathStillRefusesWhenDirectItselfRefuses: together they show this is a narrow
// authorisation rule and not "a chain ending in direct is bypassed".

// directIntrinsicChain resolves a default outbound and returns the chain the router would gate.
func directIntrinsicChain(t *testing.T, defaultOutbound adapter.Outbound, network string, metadata *adapter.InboundContext) []adapter.Outbound {
	t.Helper()
	chain, err := resolveOutbound(defaultOutbound, metadata, network, true)
	require.NoError(t, err)
	return chain
}

// TestDirectFastPathSurvivesTransparentSelectorWithNoPolicy is the core regression.
//
// selector (fast_path unset) -> direct must take the Direct Fast Path, because the selector is a
// pure selection layer and `direct` is reachable through it unchanged. This is the exact
// configuration that regressed.
func TestDirectFastPathSurvivesTransparentSelectorWithNoPolicy(t *testing.T) {
	leaf := eligibleLeaf()
	group := &transparentGroup{tag: "国内直连", selected: leaf}
	// No policy is installed at all: this is what "fast_path unset" means on the wire.
	router := fastPathRouter(t, group, FastPathPolicies{})

	destination := M.ParseSocksaddr("93.184.216.34:443")
	metadata := fastBypassMetadata(N.NetworkTCP, destination)

	chain := directIntrinsicChain(t, group, N.NetworkTCP, &metadata)
	require.Len(t, chain, 2, "the chain must still name the group AND the leaf, not be flattened")
	require.Equal(t, C.TypeDirect, chain[1].Type(), "the terminal element must be the direct leaf")

	require.True(t, directFastPathLeafIsIntrinsic(chain),
		"transparent selector in front of a real direct leaf is the intrinsic shape")
	require.True(t, resolveFastPathRequest(chain, router.fastPathPolicies),
		"an unset policy on a transparent selector must not withhold direct's own fast path")

	require.Equal(t, adapter.PreMatchBypass,
		router.preMatchFlow(context.Background(), &metadata, destination, nil, "").Action,
		"and the flow must actually take the bypass end to end, not merely pass the predicate")
}

// TestDirectFastPathSelectorExplicitFalseSuppressesTheAttempt is the "false is an off switch for the
// ATTEMPT" half, and it is the contract this round fixed.
//
// The shape is still the intrinsic one, and the group is still the same object; the only difference
// is that the group states a decision. The flow must fall through to the normal direct connection -
// "refuse the attempt" is not "disable direct": the connection still reaches its destination.
func TestDirectFastPathSelectorExplicitFalseSuppressesTheAttempt(t *testing.T) {
	destination := M.ParseSocksaddr("93.184.216.34:443")
	leaf := eligibleLeaf()
	group := &transparentGroup{tag: "国内直连", selected: leaf}
	falseRouter := fastPathRouter(t, group, FastPathPolicies{"国内直连": {Enabled: false}})

	metadata := fastBypassMetadata(N.NetworkTCP, destination)
	chain := directIntrinsicChain(t, group, N.NetworkTCP, &metadata)
	require.True(t, directFastPathLeafIsIntrinsic(chain), "the SHAPE is still the intrinsic one")
	require.False(t, resolveFastPathRequest(chain, falseRouter.fastPathPolicies),
		"but the explicit false must withhold the grant the shape would otherwise supply")

	falseMetadata := fastBypassMetadata(N.NetworkTCP, destination)
	require.Equal(t, adapter.PreMatchContinue,
		falseRouter.preMatchFlow(context.Background(), &falseMetadata, destination, nil, "").Action,
		"a refused attempt continues on the normal path; the flow is not dropped or redirected")

	// Control 1: the SAME shape with no policy still takes the bypass, so the false is doing the
	// work and the shape has not silently stopped resolving.
	unsetRouter := fastPathRouter(t, group, FastPathPolicies{})
	unsetMetadata := fastBypassMetadata(N.NetworkTCP, destination)
	require.Equal(t, adapter.PreMatchBypass,
		unsetRouter.preMatchFlow(context.Background(), &unsetMetadata, destination, nil, "").Action,
		"silence must keep the intrinsic grant (the regression this feature fixed)")

	// Control 2: the SAME false on the SAME group with a proxy leaf still refuses, and refuses
	// before the policy is read - the leaf-capability gate is upstream of everything here.
	proxy := &nonBypassableOutbound{}
	proxyGroup := &transparentGroup{tag: "国内直连", selected: proxy}
	proxyRouter := fastPathRouter(t, proxyGroup, FastPathPolicies{"国内直连": {Enabled: false}})
	proxyMetadata := fastBypassMetadata(N.NetworkTCP, destination)
	proxyChain := directIntrinsicChain(t, proxyGroup, N.NetworkTCP, &proxyMetadata)
	require.False(t, directFastPathLeafIsIntrinsic(proxyChain),
		"a proxy leaf is not the intrinsic shape, whatever the chain looks like")
	require.False(t, resolveFastPathRequest(proxyChain, proxyRouter.fastPathPolicies),
		"explicit false must still refuse a non-direct leaf")
	require.NotEqual(t, adapter.PreMatchBypass,
		proxyRouter.preMatchFlow(context.Background(), &proxyMetadata, destination, nil, "").Action,
		"and the proxy flow must not be bypassed")
}

// TestDirectFastPathSelectorExplicitTrueTakesExactlyOneDecision guards the "no double bypass"
// property. The authorisation is resolved from the chain and consumed once by the gate; introducing
// a second grant for the intrinsic shape would show up here as a second CanBypass call.
//
// The counter is the one already on the test double, so this needs no production instrumentation.
func TestDirectFastPathSelectorExplicitTrueTakesExactlyOneDecision(t *testing.T) {
	leaf := eligibleLeaf()
	group := &transparentGroup{tag: "国内直连", selected: leaf}
	router := fastPathRouter(t, group, FastPathPolicies{"国内直连": {Enabled: true}})

	destination := M.ParseSocksaddr("93.184.216.34:443")
	metadata := fastBypassMetadata(N.NetworkTCP, destination)

	require.Equal(t, adapter.PreMatchBypass,
		router.preMatchFlow(context.Background(), &metadata, destination, nil, "").Action)
	require.Equal(t, int32(1), leaf.calls.Load(),
		"exactly one bypass decision must be taken; two would mean the intrinsic grant and the "+
			"policy grant both ran")
}

// TestDirectFastPathSurvivesNestedTransparentGroups covers the multi-layer shape: selector ->
// selector -> urltest -> direct, all unset.
func TestDirectFastPathSurvivesNestedTransparentGroups(t *testing.T) {
	leaf := eligibleLeaf()
	inner := &transparentUrltest{tag: "urltest", selected: leaf}
	middle := &transparentGroup{tag: "selectorB", selected: inner}
	outer := &transparentGroup{tag: "selectorA", selected: middle}
	router := fastPathRouter(t, outer, FastPathPolicies{})

	destination := M.ParseSocksaddr("93.184.216.34:443")
	metadata := fastBypassMetadata(N.NetworkTCP, destination)

	chain := directIntrinsicChain(t, outer, N.NetworkTCP, &metadata)
	require.Len(t, chain, 4, "every layer must be named: selectorA, selectorB, urltest, direct")
	require.True(t, directFastPathLeafIsIntrinsic(chain))
	require.Equal(t, adapter.PreMatchBypass,
		router.preMatchFlow(context.Background(), &metadata, destination, nil, "").Action,
		"depth must not matter: a transparent chain is transparent at any length")
}

// TestDirectFastPathNestedExplicitPolicyWinsOverIntrinsic covers the precedence on the intrinsic
// shape: the outermost explicit decision wins, an inner explicit false with nothing outside is still
// a decision, and only a completely silent chain receives the intrinsic grant. The control at the
// end proves nested precedence for non-direct leaves is untouched.
func TestDirectFastPathNestedExplicitPolicyWinsOverIntrinsic(t *testing.T) {
	destination := M.ParseSocksaddr("93.184.216.34:443")

	for _, testCase := range []struct {
		name     string
		policies FastPathPolicies
		want     bool
		why      string
	}{
		{
			name:     "outer false, inner true",
			policies: FastPathPolicies{"outer": {Enabled: false}, "inner": {Enabled: true}},
			want:     false,
			why:      "the group the flow entered said no, and an explicit decision wins outermost-first",
		},
		{
			name:     "outer true, inner false",
			policies: FastPathPolicies{"outer": {Enabled: true}, "inner": {Enabled: false}},
			want:     true,
			why:      "the outermost explicit decision wins, exactly as traffic_class resolves",
		},
		{
			name:     "outer unset, inner false",
			policies: FastPathPolicies{"inner": {Enabled: false}},
			want:     false,
			why:      "an inner explicit false is still a decision, and an explicit decision beats the intrinsic default",
		},
		{
			name:     "outer unset, inner unset",
			policies: FastPathPolicies{},
			want:     true,
			why:      "silence keeps the intrinsic grant",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			leaf := eligibleLeaf()
			inner := &transparentGroup{tag: "inner", selected: leaf}
			outer := &transparentGroup{tag: "outer", selected: inner}
			router := fastPathRouter(t, outer, testCase.policies)

			metadata := fastBypassMetadata(N.NetworkTCP, destination)
			chain := directIntrinsicChain(t, outer, N.NetworkTCP, &metadata)
			require.True(t, directFastPathLeafIsIntrinsic(chain))
			require.Equal(t, testCase.want, resolveFastPathRequest(chain, router.fastPathPolicies),
				testCase.why)

			result := router.preMatchFlow(context.Background(), &metadata, destination, nil, "")
			wantAction := adapter.PreMatchContinue
			if testCase.want {
				wantAction = adapter.PreMatchBypass
			}
			require.Equal(t, wantAction, result.Action, testCase.why)
		})
	}

	// Control: the same conflicting configuration with a bypassable NON-DIRECT leaf must still resolve
	// outermost first, so the intrinsic rule has not quietly replaced nested precedence.
	//
	// The leaf has to be bypassable for this control to test what it claims. With a
	// non-bypassable proxy, resolveFastPathRequest returns false before reaching the policy scan at
	// all - correct behaviour, but it would make the assertions below pass for the wrong reason and
	// the control would prove nothing about precedence.
	t.Run("non-direct leaf keeps outermost precedence", func(t *testing.T) {
		leaf := newBypassableNonDirectOutbound()
		inner := &transparentGroup{tag: "inner", selected: leaf}
		outer := &transparentGroup{tag: "outer", selected: inner}

		outerFalse := fastPathRouter(t, outer, FastPathPolicies{
			"outer": {Enabled: false}, "inner": {Enabled: true},
		})
		outerTrue := fastPathRouter(t, outer, FastPathPolicies{
			"outer": {Enabled: true}, "inner": {Enabled: false},
		})

		metadata := fastBypassMetadata(N.NetworkTCP, destination)
		chain := directIntrinsicChain(t, outer, N.NetworkTCP, &metadata)
		require.False(t, directFastPathLeafIsIntrinsic(chain),
			"a bypassable but non-direct leaf is not the intrinsic shape, so precedence must decide")

		require.False(t, resolveFastPathRequest(chain, outerFalse.fastPathPolicies),
			"outer explicit false must still win over inner true")
		require.True(t, resolveFastPathRequest(chain, outerTrue.fastPathPolicies),
			"outer explicit true must still win over inner false")

		// And the authorisation must reach the gate with the RESOLVED leaf, so the real decision is
		// taken on the leaf rather than on the group the flow entered.
		require.Equal(t, adapter.PreMatchBypass,
			outerTrue.preMatchFlow(context.Background(), &metadata, destination, nil, "").Action,
			"an authorised chain over a bypassable leaf takes the bypass through the normal policy path")
	})
}

// TestDirectFastPathOpaqueGroupStillRefusesUnsetPolicy is the primary safety test for the
// relaxation: an opaque group must refuse even with no policy anywhere, because the intrinsic rule
// requires EVERY layer in front of the leaf to be transparent.
func TestDirectFastPathOpaqueGroupStillRefusesUnsetPolicy(t *testing.T) {
	destination := M.ParseSocksaddr("93.184.216.34:443")

	t.Run("selector -> loadbalance -> direct", func(t *testing.T) {
		leaf := eligibleLeaf()
		opaque := &opaqueGroup{tag: "🏠 LB", selected: leaf}
		outer := &transparentGroup{tag: "outer", selected: opaque}
		// The selector even asks for the fast path; the opaque group must still refuse.
		router := fastPathRouter(t, outer, FastPathPolicies{"outer": {Enabled: true}})

		metadata := fastBypassMetadata(N.NetworkTCP, destination)
		chain := directIntrinsicChain(t, outer, N.NetworkTCP, &metadata)
		require.Len(t, chain, 3, "the opaque group must be named in the chain")

		require.False(t, directFastPathLeafIsIntrinsic(chain),
			"an opaque layer anywhere in the chain disqualifies the intrinsic shape")
		require.Equal(t, BypassRefusedOutboundChain,
			router.canFastBypass(&metadata, destination, chain, chain[len(chain)-1],
				resolveFastPathRequest(chain, router.fastPathPolicies)),
			"the refusal reason must stay BypassRefusedOutboundChain")
		require.NotEqual(t, adapter.PreMatchBypass,
			router.preMatchFlow(context.Background(), &metadata, destination, nil, "").Action)
	})

	t.Run("loadbalance -> direct, unset", func(t *testing.T) {
		leaf := eligibleLeaf()
		opaque := &opaqueGroup{tag: "🏠 LB", selected: leaf}
		// Nothing is configured here at all, which is the case the relaxation could have broken.
		router := fastPathRouter(t, opaque, FastPathPolicies{})

		metadata := fastBypassMetadata(N.NetworkTCP, destination)
		chain := directIntrinsicChain(t, opaque, N.NetworkTCP, &metadata)

		require.False(t, directFastPathLeafIsIntrinsic(chain),
			"loadbalance owns the dial of the flow it selected, so it is never transparent")
		require.False(t, resolveFastPathRequest(chain, router.fastPathPolicies),
			"and an unset policy must not be read as authorisation either")
		require.NotEqual(t, adapter.PreMatchBypass,
			router.preMatchFlow(context.Background(), &metadata, destination, nil, "").Action,
			"bypassing loadbalance would discard the retry its configuration asked for")
	})
}

// TestDirectFastPathStillRefusesWhenDirectItselfRefuses proves the change stopped at the group
// authorisation gate and did not become an eligibility bypass: a configured direct outbound refuses
// even with a transparent chain and an explicit true.
func TestDirectFastPathStillRefusesWhenDirectItselfRefuses(t *testing.T) {
	destination := M.ParseSocksaddr("93.184.216.34:443")

	for _, testCase := range []struct {
		name     string
		policies FastPathPolicies
	}{
		{"policy unset", FastPathPolicies{}},
		{"policy true", FastPathPolicies{"国内直连": {Enabled: true}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// configuredDirectOutbound reports CanBypass false, modelling a direct outbound that
			// carries a bind address, a routing mark or a network strategy.
			leaf := &configuredDirectOutbound{}
			group := &transparentGroup{tag: "国内直连", selected: leaf}
			router := fastPathRouter(t, group, testCase.policies)

			metadata := fastBypassMetadata(N.NetworkTCP, destination)
			chain := directIntrinsicChain(t, group, N.NetworkTCP, &metadata)
			require.Equal(t, C.TypeDirect, chain[len(chain)-1].Type(),
				"the leaf IS direct by type, so only its own refusal can stop this")

			require.Equal(t, BypassRefusedOutboundSemantics,
				router.canFastBypass(&metadata, destination, chain, chain[len(chain)-1],
					resolveFastPathRequest(chain, router.fastPathPolicies)),
				"direct's own semantics refusal must be reached and must be the reason")
			require.NotEqual(t, adapter.PreMatchBypass,
				router.preMatchFlow(context.Background(), &metadata, destination, nil, "").Action)
		})
	}
}

// TestDirectFastPathIsNotGrantedToATagNamedDirect guards the type-based leaf test. A proxy that
// presents itself under the tag "direct" - because a user named it that - must not become bypassable.
//
// This is the test that would fail if the implementation asked the chain about a tag instead of about
// a type, so it deliberately makes the tag and the type disagree.
func TestDirectFastPathIsNotGrantedToATagNamedDirect(t *testing.T) {
	destination := M.ParseSocksaddr("93.184.216.34:443")

	proxy := &taggedNonBypassableOutbound{}
	group := &transparentGroup{tag: "direct", selected: proxy}
	router := fastPathRouter(t, group, FastPathPolicies{})

	metadata := fastBypassMetadata(N.NetworkTCP, destination)
	chain := directIntrinsicChain(t, group, N.NetworkTCP, &metadata)
	leaf := chain[len(chain)-1]

	require.Equal(t, "direct", leaf.Tag(),
		"precondition: the LEAF's tag reads 'direct', so a tag-based check would be fooled")
	require.NotEqual(t, C.TypeDirect, leaf.Type(),
		"precondition: but its type does not, which is what the implementation must consult")
	require.False(t, directFastPathLeafIsIntrinsic(chain),
		"a tag must never be what decides this: the leaf is not a direct outbound")
	require.NotEqual(t, adapter.PreMatchBypass,
		router.preMatchFlow(context.Background(), &metadata, destination, nil, "").Action)
}

// taggedNonBypassableOutbound is nonBypassableOutbound with a caller-chosen tag, used to make a tag
// and a type disagree on purpose.
type taggedNonBypassableOutbound struct {
	adapter.Outbound
}

func (o *taggedNonBypassableOutbound) Type() string      { return "socks" }
func (o *taggedNonBypassableOutbound) Tag() string       { return "direct" }
func (o *taggedNonBypassableOutbound) Network() []string { return []string{N.NetworkTCP, N.NetworkUDP} }

// TestDirectFastPathSelectorsSwitchDoesNotLeakTheIntrinsicGrant covers the runtime switch. A selector
// that served direct and now serves a proxy must be re-decided on its own terms; the intrinsic grant
// is derived per flow from the resolved chain and must not be cached on the group.
func TestDirectFastPathSelectorsSwitchDoesNotLeakTheIntrinsicGrant(t *testing.T) {
	destination := M.ParseSocksaddr("93.184.216.34:443")
	metadata := fastBypassMetadata(N.NetworkTCP, destination)

	leaf := eligibleLeaf()
	proxy := &nonBypassableOutbound{}
	group := &transparentGroup{tag: "国内直连", selected: leaf}
	router := fastPathRouter(t, group, FastPathPolicies{})

	// First flow: the selector serves direct, so the intrinsic rule applies.
	require.Equal(t, adapter.PreMatchBypass,
		router.preMatchFlow(context.Background(), &metadata, destination, nil, "").Action)

	// The selector switches to a proxy.
	group.setSelected(proxy)

	// Second flow: the same group must now be judged as a proxy flow, with no memory of the first.
	proxyMetadata := fastBypassMetadata(N.NetworkTCP, destination)
	proxyChain := directIntrinsicChain(t, group, N.NetworkTCP, &proxyMetadata)
	require.Equal(t, "socks", proxyChain[len(proxyChain)-1].Type(),
		"the chain must reflect the NEW selection")
	require.False(t, directFastPathLeafIsIntrinsic(proxyChain),
		"a grant resolved for the previous leaf must not carry over")
	require.NotEqual(t, adapter.PreMatchBypass,
		router.preMatchFlow(context.Background(), &proxyMetadata, destination, nil, "").Action,
		"the flow must fall back to the normal path")

	// And back again, so the rule is not merely "the first answer sticks".
	group.setSelected(leaf)
	backMetadata := fastBypassMetadata(N.NetworkTCP, destination)
	require.Equal(t, adapter.PreMatchBypass,
		router.preMatchFlow(context.Background(), &backMetadata, destination, nil, "").Action,
		"switching back to direct must restore the fast path")
}

// TestDirectFastPathAIMatcherStillDecidesForBypassableNonDirectLeaves confirms the AI
// auto-detection path is untouched for a leaf that can bypass but is not `direct`.
//
// The leaf here is deliberately bypassable-but-not-direct. Using a direct leaf would make this test
// vacuous under the new rule, because the intrinsic grant would supply the true result and the
// matcher would never be the reason. This shape isolates the matcher: the only thing that can
// authorise it is the tag.
func TestDirectFastPathAIMatcherStillDecidesForBypassableNonDirectLeaves(t *testing.T) {
	destination := M.ParseSocksaddr("93.184.216.34:443")
	metadata := fastBypassMetadata(N.NetworkTCP, destination)

	aiLeaf := newBypassableNonDirectOutbound()
	aiGroup := &transparentGroup{tag: "🤖 AI", selected: aiLeaf}
	autoChain := directIntrinsicChain(t, aiGroup, N.NetworkTCP, &metadata)
	require.Equal(t, "block", autoChain[len(autoChain)-1].Type(),
		"precondition: the leaf can bypass but is not direct")
	require.False(t, directFastPathLeafIsIntrinsic(autoChain),
		"so the intrinsic rule must not apply, leaving the matcher as the only possible grant")

	autoRouter := fastPathRouter(t, aiGroup, FastPathPolicies{})
	require.True(t, resolveFastPathRequest(autoChain, autoRouter.fastPathPolicies),
		"the AI tag matcher must still authorise a bypassable non-direct leaf")

	explicitFalseRouter := fastPathRouter(t, aiGroup, FastPathPolicies{"🤖 AI": {Enabled: false}})
	require.False(t, resolveFastPathRequest(autoChain, explicitFalseRouter.fastPathPolicies),
		"an explicit false must still beat automatic detection for a non-direct leaf")

	// And an explicit true on an ordinary (non-AI) tag must still authorise the same leaf, so the
	// explicit branch is not merely the matcher in disguise.
	ordinaryLeaf := newBypassableNonDirectOutbound()
	ordinaryGroup := &transparentGroup{tag: "🌐 普通代理", selected: ordinaryLeaf}
	ordinaryChain := directIntrinsicChain(t, ordinaryGroup, N.NetworkTCP, &metadata)
	ordinaryRouter := fastPathRouter(t, ordinaryGroup,
		FastPathPolicies{"🌐 普通代理": {Enabled: true}})
	require.True(t, resolveFastPathRequest(ordinaryChain, ordinaryRouter.fastPathPolicies),
		"an explicit true still authorises a non-direct bypassable leaf")
}
