package route

import (
	"context"
	"io"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/trafficclass"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// These tests cover the fast_path group policy from both sides of the chain gate, and the
// separation that the whole feature rests on: the authorisation belongs to the flow's logical
// group, never to the leaf that group selected.
//
// The chain gate's two invariants live here and in fast_bypass_test.go:
//
//	TestFastBypassGroupChainIsIneligible          unauthorised group -> still refused
//	TestFastBypassAuthorizedGroupChainIsEligible  authorised transparent group -> permitted
//
// Everything AFTER the chain check is unchanged and still refuses on its own terms; the safety half
// is covered separately below.

// transparentGroup is a logical group that declares the marker, so it is a pure selection over its
// members and may carry a fast_path policy.
type transparentGroup struct {
	adapter.Outbound
	tag      string
	selected adapter.Outbound
}

func (g *transparentGroup) Type() string      { return C.TypeSelector }
func (g *transparentGroup) Tag() string       { return g.tag }
func (g *transparentGroup) Network() []string { return []string{N.NetworkTCP, N.NetworkUDP} }
func (g *transparentGroup) Selected(network string) adapter.Outbound {
	return g.selected
}
func (g *transparentGroup) All() []string { return []string{g.selected.Tag()} }
func (g *transparentGroup) AttachConnection(io.Closer) func() {
	return func() {}
}
func (g *transparentGroup) FastPathTransparent() bool { return true }

// transparentUrltest is the same claim from the other supported group type, so the marker is
// exercised through more than one Type().
type transparentUrltest struct {
	adapter.Outbound
	tag      string
	selected adapter.Outbound
}

func (g *transparentUrltest) Type() string      { return C.TypeURLTest }
func (g *transparentUrltest) Tag() string       { return g.tag }
func (g *transparentUrltest) Network() []string { return []string{N.NetworkTCP, N.NetworkUDP} }
func (g *transparentUrltest) Selected(network string) adapter.Outbound {
	return g.selected
}
func (g *transparentUrltest) All() []string { return []string{g.selected.Tag()} }
func (g *transparentUrltest) AttachConnection(io.Closer) func() {
	return func() {}
}
func (g *transparentUrltest) FastPathTransparent() bool { return true }

// opaqueGroup is a group that does NOT declare the marker. It stands in for any group whose
// semantics a bypass would skip - `loadbalance` is the real one, because it may own the dial of the
// flow it selected.
type opaqueGroup struct {
	adapter.Outbound
	tag      string
	selected adapter.Outbound
}

func (g *opaqueGroup) Type() string      { return C.TypeLoadBalance }
func (g *opaqueGroup) Tag() string       { return g.tag }
func (g *opaqueGroup) Network() []string { return []string{N.NetworkTCP, N.NetworkUDP} }
func (g *opaqueGroup) Selected(network string) adapter.Outbound {
	return g.selected
}
func (g *opaqueGroup) All() []string { return []string{g.selected.Tag()} }
func (g *opaqueGroup) AttachConnection(io.Closer) func() {
	return func() {}
}

var (
	_ adapter.FastPathAuthorizedGroup = (*transparentGroup)(nil)
	_ adapter.FastPathAuthorizedGroup = (*transparentUrltest)(nil)
)

// TestFastPathGroupMarkerIsNotSatisfiedByAccident guards the type-identity of the marker.
//
// The first version of this feature used an EMPTY interface embedding OutboundGroup, and
// *opaqueGroup satisfied it - it has All/Selected/AttachConnection plus the Outbound surface, which
// was the entire method set. So every check that was supposed to notice an opaque group reported
// success, and the safety tests below could not fail. That is why the capability has a named
// method, and this test is the tripwire: it fails if the method is ever removed, because the
// assertion would then start succeeding for types that mean the opposite.
func TestFastPathGroupMarkerIsNotSatisfiedByAccident(t *testing.T) {
	var opaque any = (*opaqueGroup)(nil)
	_, declares := opaque.(adapter.FastPathAuthorizedGroup)
	require.False(t, declares,
		"a group that does not declare FastPathTransparent must not satisfy the capability; "+
			"if this fails, the capability has lost its named method and is matching by shape again")

	require.False(t, canCarryFastPathPolicy(&opaqueGroup{tag: "🏠 LB"}),
		"and the router's own predicate must agree")

	require.True(t, canCarryFastPathPolicy(&transparentGroup{tag: "🤖 AI"}),
		"while a group that does declare it must be a carrier")

	// A leaf must never be one, whatever it implements elsewhere. This is the group-only rule's
	// structural half.
	require.False(t, canCarryFastPathPolicy(eligibleLeaf()),
		"a leaf is not a logical group and can never carry the policy")
}

// eligibleLeaf returns a direct outbound that reports itself bypassable for everything.
func eligibleLeaf() *plainDirectOutbound {
	leaf := &plainDirectOutbound{}
	leaf.canBypass.Store(1)
	return leaf
}

func fastPathRouter(t *testing.T, defaultOutbound adapter.Outbound, policies FastPathPolicies) *Router {
	t.Helper()
	router := &Router{
		ctx:      context.Background(),
		logger:   log.NewNOPFactory().Logger(),
		outbound: &stubOutboundManager{def: defaultOutbound},
	}
	router.SetFastPathPolicies(policies)
	return router
}

// --- the chain gate's two invariants ---------------------------------------------------

// TestFastBypassAuthorizedGroupChainIsEligible is the positive half of the refinement. The chain
// reaches the gate as [group, leaf], which the original length check refused outright; with the
// group authorised and transparent, and the leaf bypassable, the flow is permitted.
func TestFastBypassAuthorizedGroupChainIsEligible(t *testing.T) {
	leaf := eligibleLeaf()
	group := &transparentGroup{tag: "🤖 AI", selected: leaf}
	router := fastPathRouter(t, group, FastPathPolicies{"🤖 AI": {Enabled: true}})

	destination := M.ParseSocksaddr("93.184.216.34:443")
	metadata := fastBypassMetadata(N.NetworkTCP, destination)

	chain, err := resolveOutbound(group, &metadata, N.NetworkTCP, true)
	require.NoError(t, err)
	require.Len(t, chain, 2, "the chain must still name the group AND the leaf")

	require.True(t, resolveFastPathRequest(chain, router.fastPathPolicies),
		"an explicitly authorised transparent group must authorise its flow")
	require.True(t,
		router.canFastBypass(&metadata, destination, chain, chain[len(chain)-1],
			resolveFastPathRequest(chain, router.fastPathPolicies)).BypassAllowed(),
		"an authorised transparent group in front of a bypassable leaf must be eligible")

	result := router.preMatchFlow(context.Background(), &metadata, destination, nil, "")
	require.Equal(t, adapter.PreMatchBypass, result.Action,
		"and the flow must actually take the bypass end to end, not merely pass the predicate")
}

// TestFastBypassUrltestGroupIsEligibleToo confirms the marker is read structurally rather than by
// type name: the same claim from a urltest must behave identically.
func TestFastBypassUrltestGroupIsEligibleToo(t *testing.T) {
	leaf := eligibleLeaf()
	group := &transparentUrltest{tag: "AI-US", selected: leaf}
	router := fastPathRouter(t, group, FastPathPolicies{"AI-US": {Enabled: true}})

	destination := M.ParseSocksaddr("93.184.216.34:443")
	metadata := fastBypassMetadata(N.NetworkTCP, destination)

	chain, err := resolveOutbound(group, &metadata, N.NetworkTCP, true)
	require.NoError(t, err)
	require.True(t, resolveFastPathRequest(chain, router.fastPathPolicies))
	require.Equal(t, adapter.PreMatchBypass,
		router.preMatchFlow(context.Background(), &metadata, destination, nil, "").Action)
}

// --- the gate is narrow, not removed ---------------------------------------------------

// TestFastBypassOpaqueGroupRefusesEvenWhenAuthorized is the most important safety test in this
// file. A group that does NOT declare transparency must still be refused by the chain gate even if
// something authorised the flow, because bypassing it would discard semantics it really has - the
// dial ownership and retry that `loadbalance` implements. Authorisation alone must never be enough.
func TestFastBypassOpaqueGroupRefusesEvenWhenAuthorized(t *testing.T) {
	leaf := eligibleLeaf()
	group := &opaqueGroup{tag: "🏠 LB", selected: leaf}
	router := fastPathRouter(t, group, FastPathPolicies{"🏠 LB": {Enabled: true}})

	destination := M.ParseSocksaddr("93.184.216.34:443")
	metadata := fastBypassMetadata(N.NetworkTCP, destination)

	chain, err := resolveOutbound(group, &metadata, N.NetworkTCP, true)
	require.NoError(t, err)
	require.Len(t, chain, 2)

	// The configuration layer refuses the key here, so a policy can never be stored under this tag
	// in a real Box; the map is populated directly to prove the READ path is independent of that
	// and cannot be talked into authorising an opaque group.
	require.False(t, canCarryFastPathPolicy(chain[0]),
		"an opaque group must never be a policy carrier")
	require.False(t, resolveFastPathRequest(chain, router.fastPathPolicies),
		"an opaque group must not authorise, whatever the map says")

	require.Equal(t, BypassRefusedOutboundChain,
		router.canFastBypass(&metadata, destination, chain, chain[len(chain)-1],
			resolveFastPathRequest(chain, router.fastPathPolicies)),
		"an opaque group keeps the original refusal")
}

// TestFastBypassNestedChainRefusesWhenAnyLevelIsOpaque checks the walk validates EVERY level in
// front of the leaf, not just the outermost. A transparent group wrapping an opaque one is still a
// chain whose semantics would be skipped.
func TestFastBypassNestedChainRefusesWhenAnyLevelIsOpaque(t *testing.T) {
	leaf := eligibleLeaf()
	inner := &opaqueGroup{tag: "🏠 inner", selected: leaf}
	outer := &transparentGroup{tag: "🤖 AI", selected: inner}
	router := fastPathRouter(t, outer, FastPathPolicies{"🤖 AI": {Enabled: true}})

	destination := M.ParseSocksaddr("93.184.216.34:443")
	metadata := fastBypassMetadata(N.NetworkTCP, destination)

	chain, err := resolveOutbound(outer, &metadata, N.NetworkTCP, true)
	require.NoError(t, err)
	require.Len(t, chain, 3, "outer, inner, leaf")
	require.False(t, fastPathChainReachesLeaf(chain),
		"one opaque level is enough to make the whole chain non-transparent")
	require.Equal(t, BypassRefusedOutboundChain,
		router.canFastBypass(&metadata, destination, chain, chain[len(chain)-1], true),
		"and the refusal must follow")
}

// TestFastBypassAuthorizedChainStillRefusesWhenLeafCannotBypass is the second narrowness proof: the
// branch only lifts the CHAIN condition. The leaf's own capability check is untouched, so an
// authorised transparent group in front of a leaf that is not bypassable is still refused - and by
// the leaf's verdict, not by the chain's.
func TestFastBypassAuthorizedChainStillRefusesWhenLeafCannotBypass(t *testing.T) {
	leaf := &plainDirectOutbound{} // canBypass stays 0: the leaf declines
	group := &transparentGroup{tag: "🤖 AI", selected: leaf}
	router := fastPathRouter(t, group, FastPathPolicies{"🤖 AI": {Enabled: true}})

	destination := M.ParseSocksaddr("93.184.216.34:443")
	metadata := fastBypassMetadata(N.NetworkTCP, destination)

	chain, err := resolveOutbound(group, &metadata, N.NetworkTCP, true)
	require.NoError(t, err)

	require.Equal(t, BypassRefusedOutboundSemantics,
		router.canFastBypass(&metadata, destination, chain, chain[len(chain)-1], true),
		"the leaf's own refusal must still apply through an authorised chain")
}

// --- the policy precedence matrix ------------------------------------------------------
//
// These are Cases A-F of §14.1. The AI matcher is the real one (trafficclass.MatchTag), not a stub,
// because the point of these cases is the interaction between an explicit decision and automatic
// detection - and that interaction is exactly what a stubbed matcher would fail to exercise.

// chainFor resolves a group to its chain so the precedence can be read off the resolved chain
// rather than off a hand-built slice.
func chainFor(t *testing.T, group adapter.Outbound) []adapter.Outbound {
	t.Helper()
	destination := M.ParseSocksaddr("93.184.216.34:443")
	metadata := fastBypassMetadata(N.NetworkTCP, destination)
	chain, err := resolveOutbound(group, &metadata, N.NetworkTCP, true)
	require.NoError(t, err)
	return chain
}

// Case A and Case C/D: an explicit true authorises, and an unset tag is decided by the matcher.
func TestFastPathCaseExplicitTrueAndAutoDetection(t *testing.T) {
	// "🤖 AI" matches the real matcher: "ai" is a whole token once the emoji splits it off.
	require.True(t, trafficclass.MatchTag("🤖 AI"), "precondition: this tag must match the AI matcher")
	// "🌐 普通代理" does not.
	require.False(t, trafficclass.MatchTag("🌐 普通代理"), "precondition: this tag must NOT match")

	for name, testCase := range map[string]struct {
		tag         string
		policies    FastPathPolicies
		authorised  bool
		explanation string
	}{
		"Case A explicit true": {
			tag: "🌐 普通代理", policies: FastPathPolicies{"🌐 普通代理": {Enabled: true}}, authorised: true,
			explanation: "an explicit true authorises even a tag the matcher would not pick up",
		},
		"Case B explicit false": {
			tag: "🌐 普通代理", policies: FastPathPolicies{"🌐 普通代理": {Enabled: false}}, authorised: false,
			explanation: "an explicit false refuses",
		},
		"Case C unset + AI tag": {
			tag: "🤖 AI", policies: nil, authorised: true,
			explanation: "with nothing explicit, an AI-looking group is authorised automatically",
		},
		"Case D unset + ordinary tag": {
			tag: "🌐 普通代理", policies: nil, authorised: false,
			explanation: "with nothing explicit, an ordinary group is not",
		},
	} {
		t.Run(name, func(t *testing.T) {
			group := &transparentGroup{tag: testCase.tag, selected: eligibleLeaf()}
			router := fastPathRouter(t, group, testCase.policies)
			require.Equal(t, testCase.authorised,
				resolveFastPathRequest(chainFor(t, group), router.fastPathPolicies),
				testCase.explanation)
		})
	}
}

// Case E is the precedence rule that matters most: an explicit false must beat automatic detection.
// Without it, an operator could not switch the fast path off for an AI-named group, and the
// override would only ever work in the permissive direction.
func TestFastPathCaseExplicitFalseBeatsTheAIMatcher(t *testing.T) {
	require.True(t, trafficclass.MatchTag("🤖 AI"), "precondition: the tag alone WOULD authorise")

	group := &transparentGroup{tag: "🤖 AI", selected: eligibleLeaf()}
	router := fastPathRouter(t, group, FastPathPolicies{"🤖 AI": {Enabled: false}})

	chain := chainFor(t, group)
	require.False(t, resolveFastPathRequest(chain, router.fastPathPolicies),
		"explicit false must win over automatic detection")

	// And the flow must really be refused at the gate, not merely reported unauthorised.
	destination := M.ParseSocksaddr("93.184.216.34:443")
	metadata := fastBypassMetadata(N.NetworkTCP, destination)
	require.Equal(t, BypassRefusedOutboundChain,
		router.canFastBypass(&metadata, destination, chain, chain[len(chain)-1], false))
}

// The outermost explicit decision wins, matching traffic_class. A nested chain is one policy
// decision, not one per level.
func TestFastPathCaseOutermostExplicitDecisionWins(t *testing.T) {
	inner := &transparentGroup{tag: "🤖 AI", selected: eligibleLeaf()}
	outer := &transparentGroup{tag: "🌐 普通代理", selected: inner}
	require.True(t, trafficclass.MatchTag(inner.tag))

	t.Run("outer false suppresses the inner AI group", func(t *testing.T) {
		router := fastPathRouter(t, outer, FastPathPolicies{"🌐 普通代理": {Enabled: false}})
		require.False(t, resolveFastPathRequest(chainFor(t, outer), router.fastPathPolicies),
			"the group the flow entered said no, so the chain is refused")
	})

	t.Run("outer true authorises even though the inner tag is the AI one", func(t *testing.T) {
		router := fastPathRouter(t, outer, FastPathPolicies{"🌐 普通代理": {Enabled: true}})
		require.True(t, resolveFastPathRequest(chainFor(t, outer), router.fastPathPolicies))
	})

	t.Run("no explicit policy anywhere falls back to the first AI-looking level", func(t *testing.T) {
		router := fastPathRouter(t, outer, nil)
		require.True(t, resolveFastPathRequest(chainFor(t, outer), router.fastPathPolicies),
			"the outer tag does not match, the inner one does, so the chain auto-authorises")
	})
}

// --- per-flow separation: the core of §2.1 ---------------------------------------------

// TestFastPathPolicyFollowsTheGroupNotTheLeaf is the single most important test in this feature.
//
// One leaf is reachable through an authorised group and through a plain group at the same time. The
// two flows must resolve DIFFERENT authorisations, and neither may mutate the leaf or anything
// keyed by the leaf's tag.
func TestFastPathPolicyFollowsTheGroupNotTheLeaf(t *testing.T) {
	leaf := eligibleLeaf()
	authorised := &transparentGroup{tag: "🤖 AI", selected: leaf}
	ordinary := &transparentGroup{tag: "🌐 普通代理", selected: leaf}

	router := fastPathRouter(t, authorised, FastPathPolicies{"🤖 AI": {Enabled: true}})

	destination := M.ParseSocksaddr("93.184.216.34:443")

	// Both flows reach the SAME leaf object. That is the precondition the test is about.
	require.Same(t, leaf, authorised.selected)
	require.Same(t, leaf, ordinary.selected)

	authorisedMetadata := fastBypassMetadata(N.NetworkTCP, destination)
	ordinaryMetadata := fastBypassMetadata(N.NetworkTCP, destination)

	authorisedChain, err := resolveOutbound(authorised, &authorisedMetadata, N.NetworkTCP, true)
	require.NoError(t, err)
	ordinaryChain, err := resolveOutbound(ordinary, &ordinaryMetadata, N.NetworkTCP, true)
	require.NoError(t, err)

	// The chains differ only in their first element; the leaf is identical.
	require.Same(t, leaf, authorisedChain[len(authorisedChain)-1])
	require.Same(t, leaf, ordinaryChain[len(ordinaryChain)-1])

	require.True(t, resolveFastPathRequest(authorisedChain, router.fastPathPolicies),
		"the AI group authorises")
	require.False(t, resolveFastPathRequest(ordinaryChain, router.fastPathPolicies),
		"the ordinary group does not, even though the leaf is the same object")

	require.Equal(t, BypassAllowed,
		router.canFastBypass(&authorisedMetadata, destination, authorisedChain,
			authorisedChain[len(authorisedChain)-1], true))
	require.Equal(t, BypassRefusedOutboundChain,
		router.canFastBypass(&ordinaryMetadata, destination, ordinaryChain,
			ordinaryChain[len(ordinaryChain)-1], false),
		"the ordinary flow must be refused, unaffected by the AI flow's authorisation")

	// Reverse order, to prove there is no ordering dependence hidden in the first pass.
	require.False(t, resolveFastPathRequest(ordinaryChain, router.fastPathPolicies))
	require.True(t, resolveFastPathRequest(authorisedChain, router.fastPathPolicies))

	// And the leaf itself was not touched: it still answers only its own capability question and
	// carries no policy of any kind.
	require.Equal(t, int32(1), leaf.canBypass.Load())
	_, leafIsPolicyCarrier := adapter.Outbound(leaf).(adapter.FastPathAuthorizedGroup)
	require.False(t, leafIsPolicyCarrier,
		"a leaf must never become a policy carrier, whatever selected it")
}

// TestFastPathPolicyIsNotCachedOnTheLeafTag proves there is no tag-keyed memo: running the same
// leaf through the authorised group first must not make a later ordinary-group flow authorised.
func TestFastPathPolicyIsNotCachedOnTheLeafTag(t *testing.T) {
	leaf := eligibleLeaf()
	authorised := &transparentGroup{tag: "🤖 AI", selected: leaf}
	ordinary := &transparentGroup{tag: "generic", selected: leaf}
	router := fastPathRouter(t, authorised, FastPathPolicies{"🤖 AI": {Enabled: true}})

	destination := M.ParseSocksaddr("93.184.216.34:443")

	// A policy stored under the LEAF's tag must have no effect at all - the reader only consults
	// elements that can carry a policy, and a leaf cannot. This is the map-side form of the bug
	// this feature must not have.
	router.SetFastPathPolicies(FastPathPolicies{
		"🤖 AI":     {Enabled: true},
		leaf.Tag(): {Enabled: true},
	})

	ordinaryMetadata := fastBypassMetadata(N.NetworkTCP, destination)
	ordinaryChain, err := resolveOutbound(ordinary, &ordinaryMetadata, N.NetworkTCP, true)
	require.NoError(t, err)
	require.False(t, resolveFastPathRequest(ordinaryChain, router.fastPathPolicies),
		"a policy keyed by the LEAF's tag must not authorise a flow that did not enter an "+
			"authorised group")
}

// TestFastPathPolicyConcurrentFlowsDoNotShareState runs the two groups concurrently and asserts the
// per-flow answer never crosses over. Under -race this also covers the shared-state question; the
// design has no shared mutable state to race on, which is the point.
func TestFastPathPolicyConcurrentFlowsDoNotShareState(t *testing.T) {
	leaf := eligibleLeaf()
	authorised := &transparentGroup{tag: "🤖 AI", selected: leaf}
	ordinary := &transparentGroup{tag: "generic", selected: leaf}
	router := fastPathRouter(t, authorised, FastPathPolicies{"🤖 AI": {Enabled: true}})

	destination := M.ParseSocksaddr("93.184.216.34:443")

	const iterations = 200
	errs := make(chan string, iterations*2)
	done := make(chan struct{})

	resolve := func(group *transparentGroup, want bool) {
		defer func() { done <- struct{}{} }()
		for i := 0; i < iterations; i++ {
			metadata := fastBypassMetadata(N.NetworkTCP, destination)
			chain, err := resolveOutbound(group, &metadata, N.NetworkTCP, true)
			if err != nil {
				errs <- "resolve: " + err.Error()
				return
			}
			if got := resolveFastPathRequest(chain, router.fastPathPolicies); got != want {
				errs <- "authorisation crossed over between concurrent flows"
				return
			}
		}
	}

	go resolve(authorised, true)
	go resolve(ordinary, false)
	<-done
	<-done
	close(errs)
	var failures []string
	for failure := range errs {
		failures = append(failures, failure)
	}
	require.Empty(t, failures)
}
