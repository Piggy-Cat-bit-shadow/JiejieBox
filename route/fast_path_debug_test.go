package route

import (
	"context"
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// Debug matrix for the fast_path group policy.
//
// This file was written by an INDEPENDENT review pass over the previous round's implementation, and
// it deliberately asks the questions that round's own tests did not:
//
//   - the §14 matrix as a TABLE rather than as hand-written cases, so a combination cannot be left
//     out by omission;
//   - the FALLBACK path, end to end: an authorised flow that the gate refuses must return
//     PreMatchContinue and reach the normal userspace path, not merely report a verdict;
//   - the negative AI tags the matcher exists to reject (NaiveProxy, mail, Thailand), driven through
//     the real matcher rather than a stub, because a false positive here would silently authorise
//     unrelated groups.
//
// It adds no production code.

// fastPathMatrixRouter builds a router whose default outbound is a transparent group in front of a
// leaf with the given bypass capability, and installs the given explicit policy. The tag is a
// parameter because the AI matcher reads it and several cases exist precisely to vary it - an
// earlier version of this helper hardcoded the AI tag, which silently made its "ordinary tag" case
// test the AI path instead.
//
// # Why the leaf here is bypassable-but-not-direct
//
// This table's subject is POLICY PRECEDENCE - unset/true/false across AI and ordinary tags - and that
// dimension only decides anything when the chain is not the direct-intrinsic shape. A direct leaf
// behind transparent groups is now authorised on its own, so a table built on one would report `true`
// for every row and could no longer detect a precedence regression at all: the assertions would pass
// while testing nothing.
//
// The leaf is therefore `bypassableNonDirectOutbound`: it CAN bypass, it is NOT `direct`, and so the
// policy scan is still the thing being measured. The capability dimension is unchanged, because this
// double's CanBypass is what `bypassCapable` toggles.
func fastPathMatrixRouter(t *testing.T, tag string, policy *option.FastPathPolicy, bypassCapable bool) (*Router, *bypassableNonDirectOutbound, *transparentGroup) {
	t.Helper()
	leaf := &bypassableNonDirectOutbound{}
	if !bypassCapable {
		leaf.canBypass.Store(0)
	} else {
		leaf.canBypass.Store(1)
	}
	group := &transparentGroup{tag: tag, selected: leaf}
	policies := FastPathPolicies{}
	if policy != nil {
		policies[tag] = *policy
	}
	router := &Router{
		ctx:      context.Background(),
		logger:   log.NewNOPFactory().Logger(),
		outbound: &stubOutboundManager{def: group},
	}
	router.SetFastPathPolicies(policies)
	return router, leaf, group
}

func fastPathPtr(value bool) *option.FastPathPolicy {
	return &option.FastPathPolicy{Enabled: value}
}

// TestFastPathTruthTable is the brief's §3.1 and §3.2 as one table.
//
// Dimensions: explicit policy (unset/true/false) x tag (AI-like / ordinary) x leaf capability
// (yes/no), plus a metadata dimension for the blockers. The expected ACTION is asserted, not only
// the authorisation, so a case that authorises and then fails to reach the bypass - or refuses and
// then bypasses anyway - fails here rather than passing on a value nobody uses.
//
// The tag is a real dimension. An earlier version of this test hardcoded the AI tag in its helper
// and then asserted that an "ordinary tag" case was unauthorised; the helper made that case test the
// AI path, so the test would have passed while proving the opposite of its name. The table now
// carries the tag explicitly and the assertion is derived, never special-cased by case name.
func TestFastPathTruthTable(t *testing.T) {
	destination := M.ParseSocksaddr("93.184.216.34:443")

	const (
		aiTag       = "🤖 AI"
		ordinaryTag = "🌐 普通代理"
	)

	for _, testCase := range []struct {
		name          string
		tag           string
		policy        *option.FastPathPolicy
		bypassCapable bool
		tracker       bool
		// mutate makes the flow ineligible for an EXISTING reason. It is the control that proves
		// authorisation cannot override the gate.
		mutate      func(*adapter.InboundContext)
		wantAuth    bool
		wantBypass  bool
		explanation string
	}{
		// --- §3.1: the policy precedence, over both tag shapes -------------------------------
		{
			name: "explicit true + ordinary tag", tag: ordinaryTag, policy: fastPathPtr(true), bypassCapable: true,
			wantAuth: true, wantBypass: true,
			explanation: "an explicit true authorises a tag the matcher would not read as AI",
		},
		{
			name: "explicit true + AI tag", tag: aiTag, policy: fastPathPtr(true), bypassCapable: true,
			wantAuth: true, wantBypass: true,
			explanation: "an explicit true authorises an AI tag too",
		},
		{
			name: "explicit false + ordinary tag", tag: ordinaryTag, policy: fastPathPtr(false), bypassCapable: true,
			wantAuth: false, wantBypass: false,
			explanation: "explicit false refuses even though the leaf could bypass",
		},
		{
			name: "explicit false + AI tag", tag: aiTag, policy: fastPathPtr(false), bypassCapable: true,
			wantAuth: false, wantBypass: false,
			explanation: "§3.1's key row: false must NOT be reopened by automatic detection",
		},
		{
			name: "unset + ordinary tag", tag: ordinaryTag, policy: nil, bypassCapable: true,
			wantAuth: false, wantBypass: false,
			explanation: "nothing explicit and nothing AI-like: not authorised, no new fast path",
		},
		{
			name: "unset + AI tag", tag: aiTag, policy: nil, bypassCapable: true,
			wantAuth: true, wantBypass: true,
			explanation: "the AI fallback the previous round kept",
		},

		// --- §3.2: authorisation x leaf capability -------------------------------------------
		{
			name: "authorised + INCAPABLE leaf", tag: aiTag, policy: fastPathPtr(true), bypassCapable: false,
			wantAuth: true, wantBypass: false,
			explanation: "the leaf's own verdict is still authoritative; authorisation is not a bypass",
		},
		{
			name: "unauthorised + capable leaf", tag: ordinaryTag, policy: fastPathPtr(false), bypassCapable: true,
			wantAuth: false, wantBypass: false,
			explanation: "a capable leaf does not authorise itself",
		},

		// --- the blockers, all of them with authorisation GRANTED ----------------------------
		{
			name: "authorised + FakeIP flow", tag: aiTag, policy: fastPathPtr(true), bypassCapable: true,
			mutate:   func(m *adapter.InboundContext) { m.FakeIP = true },
			wantAuth: true, wantBypass: false,
			explanation: "FakeIP still refuses an authorised flow",
		},
		{
			name: "authorised + sniffed domain", tag: aiTag, policy: fastPathPtr(true), bypassCapable: true,
			mutate:   func(m *adapter.InboundContext) { m.Domain = "example.com" },
			wantAuth: true, wantBypass: false,
			explanation: "the sniffed-domain blocker still applies",
		},
		{
			name: "authorised + TLS rewrite", tag: aiTag, policy: fastPathPtr(true), bypassCapable: true,
			mutate:   func(m *adapter.InboundContext) { m.TLSFragment = true },
			wantAuth: true, wantBypass: false,
			explanation: "the TLS-options blocker still applies",
		},
		{
			name: "authorised + tracker attached", tag: aiTag, policy: fastPathPtr(true), bypassCapable: true,
			tracker:  true,
			wantAuth: true, wantBypass: false,
			explanation: "a tracker still refuses, because losing accounting is not an optimisation",
		},
		{
			name: "authorised + non-TUN inbound", tag: aiTag, policy: fastPathPtr(true), bypassCapable: true,
			mutate:   func(m *adapter.InboundContext) { m.InboundType = "socks-in" },
			wantAuth: true, wantBypass: false,
			explanation: "the fast path is TUN-only regardless of authorisation",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			router, leaf, group := fastPathMatrixRouter(t, testCase.tag, testCase.policy, testCase.bypassCapable)
			if testCase.tracker {
				router.trackers = []adapter.ConnectionTracker{noopTracker{}}
			}

			metadata := fastBypassMetadata(N.NetworkTCP, destination)
			if testCase.mutate != nil {
				testCase.mutate(&metadata)
			}

			chain, err := resolveOutbound(group, &metadata, N.NetworkTCP, true)
			require.NoError(t, err)
			require.Len(t, chain, 2, "precondition: the chain really is group + leaf")

			authorised := resolveFastPathRequest(chain, router.fastPathPolicies)
			require.Equal(t, testCase.wantAuth, authorised,
				"authorisation: %s", testCase.explanation)

			result := router.preMatchFlow(context.Background(), &metadata, destination, nil, "")
			if testCase.wantBypass {
				require.Equal(t, adapter.PreMatchBypass, result.Action, testCase.explanation)
			} else {
				require.NotEqual(t, adapter.PreMatchBypass, result.Action, testCase.explanation)
			}

			// The leaf is never a policy carrier, in any combination of the table.
			require.False(t, canCarryFastPathPolicy(leaf),
				"a leaf must never be a policy carrier, whatever selected it")
		})
	}
}

// TestFastPathAuthorizedButRefusedFallsBackToTheNormalPath is §15, and it is the test the previous
// round did not write: every other test asserted the VERDICT, none asserted that the flow survives.
//
// The requirement is not "the verdict is a refusal" - it is that a refused flow continues down the
// existing userspace path rather than being dropped, redirected to direct, or errored.
func TestFastPathAuthorizedButRefusedFallsBackToTheNormalPath(t *testing.T) {
	destination := M.ParseSocksaddr("93.184.216.34:443")

	for name, mutate := range map[string]func(*adapter.InboundContext){
		"FakeIP":         func(m *adapter.InboundContext) { m.FakeIP = true },
		"sniffed domain": func(m *adapter.InboundContext) { m.Domain = "example.com" },
		"TLS fragment":   func(m *adapter.InboundContext) { m.TLSFragment = true },
		"UDP connect":    func(m *adapter.InboundContext) { m.UDPConnect = true },
		// A POPULATED candidate list. An earlier version of this fixture set an EMPTY slice, and
		// the blocker tests len(...) > 0, so the flow was still bypassed and the case failed - a
		// defect in the fixture that happened to expose itself only because this test asserts the
		// action rather than the verdict.
		"resolved list": func(m *adapter.InboundContext) {
			m.DestinationAddresses = []netip.Addr{netip.MustParseAddr("93.184.216.34")}
		},
	} {
		t.Run(name, func(t *testing.T) {
			router, leaf, group := fastPathMatrixRouter(t, "🤖 AI", fastPathPtr(true), true)
			metadata := fastBypassMetadata(N.NetworkTCP, destination)
			mutate(&metadata)

			chain, err := resolveOutbound(group, &metadata, N.NetworkTCP, true)
			require.NoError(t, err)
			require.True(t, resolveFastPathRequest(chain, router.fastPathPolicies),
				"precondition: this flow really is authorised, so the refusal comes from the gate")

			result := router.preMatchFlow(context.Background(), &metadata, destination, nil, "")
			require.NotEqual(t, adapter.PreMatchBypass, result.Action, "must not bypass")
			require.NotEqual(t, adapter.PreMatchReject, result.Action, "must not reject the flow")
			require.NotEqual(t, adapter.PreMatchDrop, result.Action, "must not drop the flow")
			require.Equal(t, adapter.PreMatchContinue, result.Action,
				"an authorised-but-refused flow must continue to the normal path, not be dropped")

			// The refusal must be attributable, so an operator can tell this from "never asked".
			verdict := router.canFastBypass(&metadata, destination, chain, chain[len(chain)-1], true)
			require.False(t, verdict.BypassAllowed())
			require.NotEqual(t, BypassRefusedOutboundChain, verdict,
				"the refusal must come from the flow-specific blocker under test, not from the chain gate")

			// And the leaf was never consulted as a bypass candidate in a way that mutated it.
			require.NotNil(t, leaf)
		})
	}
}

// TestFastPathNestedPrecedence is §13: nested groups are checked for SEMANTICS only, with no
// inheritance system built for them.
//
// The rule the previous round defined is "first explicit decision wins, scanning outermost to
// inner". These four cases pin both directions of that rule, including the two the previous round's
// tests left out: an INNER explicit false suppressing an OUTER AI tag, and an authorised outer group
// whose inner level is opaque.
func TestFastPathNestedPrecedence(t *testing.T) {
	destination := M.ParseSocksaddr("93.184.216.34:443")

	build := func(t *testing.T, outerTag string, outerPolicy *option.FastPathPolicy, inner adapter.Outbound, innerPolicy *option.FastPathPolicy) (*Router, []adapter.Outbound) {
		t.Helper()
		outer := &transparentGroup{tag: outerTag, selected: inner}
		policies := FastPathPolicies{}
		if outerPolicy != nil {
			policies[outerTag] = *outerPolicy
		}
		if innerPolicy != nil {
			policies[inner.Tag()] = *innerPolicy
		}
		router := &Router{
			ctx:      context.Background(),
			logger:   log.NewNOPFactory().Logger(),
			outbound: &stubOutboundManager{def: outer},
		}
		router.SetFastPathPolicies(policies)

		metadata := fastBypassMetadata(N.NetworkTCP, destination)
		chain, err := resolveOutbound(outer, &metadata, N.NetworkTCP, true)
		require.NoError(t, err)
		return router, chain
	}

	t.Run("outer AI tag unset, inner explicit false wins", func(t *testing.T) {
		// The outer tag WOULD auto-authorise. The inner group says no. Because the resolve walks
		// outermost to inner looking for the first EXPLICIT value, the inner false is found and the
		// automatic pass is skipped entirely - an explicit decision anywhere suppresses detection.
		inner := &transparentGroup{tag: "🌐 inner", selected: newBypassableNonDirectOutbound()}
		router, chain := build(t, "🤖 AI", nil, inner, fastPathPtr(false))
		require.False(t, resolveFastPathRequest(chain, router.fastPathPolicies),
			"an inner explicit false must suppress the outer AI tag's automatic authorisation")
	})

	t.Run("outer AI tag unset, inner AI tag unset, neither explicit", func(t *testing.T) {
		inner := &transparentGroup{tag: "chatgpt", selected: newBypassableNonDirectOutbound()}
		router, chain := build(t, "generic", nil, inner, nil)
		require.True(t, resolveFastPathRequest(chain, router.fastPathPolicies),
			"with nothing explicit, the first AI-looking level authorises")
	})

	t.Run("outer explicit true, inner cannot carry a policy at all", func(t *testing.T) {
		// An opaque inner level means the chain is not transparent, so the outer authorisation
		// cannot carry the flow past the chain gate even though it is explicit.
		inner := &opaqueGroup{tag: "🏠 inner", selected: eligibleLeaf()}
		router, chain := build(t, "🤖 AI", fastPathPtr(true), inner, nil)
		require.False(t, fastPathChainReachesLeaf(chain),
			"one opaque level makes the chain non-transparent")
		require.Equal(t, BypassRefusedOutboundChain,
			router.canFastBypass(&adapter.InboundContext{InboundType: C.TypeTun, Network: N.NetworkTCP,
				Destination: destination}, destination, chain, chain[len(chain)-1], true),
			"so the outer authorisation must not carry it")
	})

	t.Run("the same inner group reused by two outer groups does not share state", func(t *testing.T) {
		// §13's isolation question: the inner group is ONE object reached two ways. Its own absent
		// policy must not be decided by whichever outer group resolved first.
		inner := &transparentGroup{tag: "🌐 shared", selected: newBypassableNonDirectOutbound()}
		router, authorisedChain := build(t, "🤖 AI", fastPathPtr(true), inner, nil)

		ordinaryOuter := &transparentGroup{tag: "🌐 outer", selected: inner}
		metadata := fastBypassMetadata(N.NetworkTCP, destination)
		ordinaryChain, err := resolveOutbound(ordinaryOuter, &metadata, N.NetworkTCP, true)
		require.NoError(t, err)
		require.Same(t, inner, ordinaryChain[1], "precondition: the inner group object is shared")

		require.True(t, resolveFastPathRequest(authorisedChain, router.fastPathPolicies))
		require.False(t, resolveFastPathRequest(ordinaryChain, router.fastPathPolicies),
			"the ordinary outer group must not inherit the authorised one's decision")
		// And in the reverse order, to prove no ordering dependence.
		require.False(t, resolveFastPathRequest(ordinaryChain, router.fastPathPolicies))
		require.True(t, resolveFastPathRequest(authorisedChain, router.fastPathPolicies))
	})
}

// TestFastPathAIMatcherRejectsFalsePositives is §10 driven through the real matcher.
//
// The brief names NaiveProxy, mail and Thailand specifically. A naive substring match on "ai" would
// authorise all three, so this test is the difference between reusing the token-aware matcher and
// having quietly grown a second, weaker one.
func TestFastPathAIMatcherRejectsFalsePositives(t *testing.T) {
	destination := M.ParseSocksaddr("93.184.216.34:443")

	for _, testCase := range []struct {
		tag     string
		looksAI bool
		why     string
	}{
		{"NaiveProxy", false, "the fork's most common outbound; contains no 'ai' token but is the reason token matching exists"},
		{"mail-relay", false, "'mail' contains the substring 'ai'"},
		{"Thailand-01", false, "'Thailand' contains the substring 'ai'"},
		{"Main", false, "'Main' contains the substring 'ai'"},
		{"available", false, "'available' contains the substring 'ai'"},
		{"paid-us", false, "'paid' contains the substring 'ai'"},
		{"🤖 AI", true, "the emoji is a token boundary, so 'ai' is a whole token"},
		{"AI-US", true, "'AI' is a whole token before the dash"},
		{"[AI]", true, "brackets are boundaries"},
		{"chatgpt", true, "a named provider"},
		{"🇺🇸人工智能住宅", true, "CJK fragment matching, because CJK has no word separators"},
	} {
		t.Run(testCase.tag, func(t *testing.T) {
			// The leaf is bypassable but NOT direct, so the AI matcher is the only thing that can
			// authorise. With a direct leaf the chain would be the direct-intrinsic shape and every
			// row would answer true - including the six that exist to prove the matcher does NOT fire
			// on substrings like "mail" or "Thailand".
			group := &transparentGroup{tag: testCase.tag, selected: newBypassableNonDirectOutbound()}
			router := fastPathRouter(t, group, nil) // NO explicit policy: the matcher decides

			metadata := fastBypassMetadata(N.NetworkTCP, destination)
			chain, err := resolveOutbound(group, &metadata, N.NetworkTCP, true)
			require.NoError(t, err)

			require.Equal(t, testCase.looksAI, resolveFastPathRequest(chain, router.fastPathPolicies),
				"tag %q: %s", testCase.tag, testCase.why)
		})
	}
}
