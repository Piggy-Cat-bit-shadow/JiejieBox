package route

// O01/O02 - the DIFFERENTIAL between the passive preview, the production walk and the active
// diagnostic's own walk.
//
// There are now three things that answer "which rule decides this request": PreviewRoute (read-only,
// from the caller's stated facts), matchRule (production, with /proc and sniffing behind it), and
// diagnoseWalk (the active diagnostic's mirror of matchRule). They are deliberately not one function:
// the preview must not do I/O and the production walk must. That is exactly why they need a
// differential - a change to one that makes the three disagree is a diagnostic that explains a rule
// the router never applies.
//
// Every case states the answer ONCE and asserts all three, including the cases where agreement is
// impossible by construction: a rule that needs a process path is UNDECIDABLE to the preview and to
// the diagnostic (they say so) while production decides it from the real facts. Those rows are the
// most valuable ones, because they are where a "make the tests pass" edit would silently turn an
// honest unknown into a wrong decision.

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	R "github.com/sagernet/sing-box/route/rule"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// differentialDecision is the answer a case expects from every observer.
type differentialDecision struct {
	ruleIndex  int
	actionType string
	outbound   string
	// unknown reports that the observers which lack the fact must say UNKNOWN at ruleIndex instead
	// of deciding. Production is given the fact and must decide.
	unknown bool
}

// runThePreviewAndDiagnostic runs the two observers that must agree EXACTLY: both are socketless and
// both are supposed to say UNKNOWN where a fact is missing.
func runThePreviewAndDiagnostic(t *testing.T, router *Router, request RoutePreviewRequest) (RoutePreviewResult, RouteDiagnoseResult) {
	t.Helper()
	preview := router.PreviewRoute(context.Background(), request)
	diagnosticMetadata := adapter.InboundContext{
		Inbound:     "connection-test",
		Network:     N.NetworkTCP,
		Destination: request.Destination,
		Domain:      request.Domain,
	}
	if request.Domain != "" {
		diagnosticMetadata.Destination = M.Socksaddr{Fqdn: request.Domain, Port: request.Destination.Port}
	}
	if request.UserId >= 0 {
		diagnosticMetadata.ProcessInfo = &adapter.ConnectionOwner{UserId: request.UserId}
	}
	diagnosticResult := RouteDiagnoseResult{RuleIndex: -1, UnknownRuleIndex: -1}
	_, walkErr := router.diagnoseWalk(context.Background(), &diagnosticMetadata, &diagnosticResult)
	require.NoError(t, walkErr)
	return preview, diagnosticResult
}

// runTheThreeObservers runs the preview, production's walk and the diagnostic's walk on the same
// rules and the same request, and returns their answers in that order.
func runTheThreeObservers(t *testing.T, router *Router, request RoutePreviewRequest) (RoutePreviewResult, adapter.Rule, int, RouteDiagnoseResult) {
	t.Helper()

	preview, diagnosticResult := runThePreviewAndDiagnostic(t, router, request)

	// Production sees a request whose name is already known as a NAME (a sniffed or resolved
	// request): the destination is the domain, and the literal form would be a different request -
	// one whose FakeIP bookkeeping this fixture does not have.
	productionDestination := request.Destination
	if request.Domain != "" {
		productionDestination = M.Socksaddr{Fqdn: request.Domain, Port: request.Destination.Port}
	}
	productionMetadata := adapter.InboundContext{
		InboundType: "tun",
		Network:     N.NetworkTCP,
		Source:      M.SocksaddrFrom(request.Source.Addr(), request.Source.Port()),
		Destination: productionDestination,
		Domain:      request.Domain,
		Protocol:    request.Protocol,
	}
	if request.UserId >= 0 || len(request.ProcessPaths) > 0 || len(request.PackageNames) > 0 {
		productionMetadata.ProcessInfo = &adapter.ConnectionOwner{
			UserId:       request.UserId,
			ProcessPaths: append([]string(nil), request.ProcessPaths...),
			PackageNames: append([]string(nil), request.PackageNames...),
		}
	}
	selected, index, _, _, err := router.matchRule(context.Background(), &productionMetadata, nil, nil)
	require.NoError(t, err)

	return preview, selected, index, diagnosticResult
}

// differentialRule builds a rule whose matcher and action the case states directly.
func differentialRule(description string, matches func(*adapter.InboundContext) bool, action adapter.RuleAction) adapter.Rule {
	return &previewTestRule{description: description, matches: matches, action: action}
}

func TestTheThreeRouteObserversAgree(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		rules    []adapter.Rule
		needs    []bool
		request  RoutePreviewRequest
		expected differentialDecision
	}{
		{
			name: "a domain rule decides",
			rules: []adapter.Rule{
				differentialRule("domain:blocked.example", func(metadata *adapter.InboundContext) bool {
					return metadata.Domain == "blocked.example"
				}, &R.RuleActionRoute{Outbound: "proxy"}),
			},
			request:  previewRequest("blocked.example", 443),
			expected: differentialDecision{ruleIndex: 0, actionType: C.RuleActionTypeRoute, outbound: "proxy"},
		},
		{
			name: "a reject rule decides",
			rules: []adapter.Rule{
				differentialRule("port:8080", func(metadata *adapter.InboundContext) bool {
					return metadata.Destination.Port == 8080
				}, &R.RuleActionReject{}),
			},
			request:  previewRequest("example.com", 8080),
			expected: differentialDecision{ruleIndex: 0, actionType: C.RuleActionTypeReject},
		},
		{
			name: "an override rewrites the port for the rules after it",
			rules: []adapter.Rule{
				routeOptionsRewriteRule(),
				port8080RejectRule(),
			},
			request:  previewRequest("example.org", 443),
			expected: differentialDecision{ruleIndex: 1, actionType: C.RuleActionTypeReject},
		},
		{
			name: "a bypass with no outbound keeps matching",
			rules: []adapter.Rule{
				differentialRule("bypass:no-target", func(metadata *adapter.InboundContext) bool {
					return metadata.Domain == "bypass.example"
				}, &R.RuleActionBypass{}),
				differentialRule("domain:example.com", func(metadata *adapter.InboundContext) bool {
					return metadata.Domain == "example.com"
				}, &R.RuleActionRoute{Outbound: "final"}),
			},
			request:  previewRequest("example.com", 443),
			expected: differentialDecision{ruleIndex: 1, actionType: C.RuleActionTypeRoute, outbound: "final"},
		},
		{
			name: "nothing matches, so the final outbound decides",
			rules: []adapter.Rule{
				differentialRule("domain:blocked.example", func(metadata *adapter.InboundContext) bool {
					return metadata.Domain == "blocked.example"
				}, &R.RuleActionRoute{Outbound: "proxy"}),
			},
			request:  previewRequest("other.example", 443),
			expected: differentialDecision{ruleIndex: -1},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			router := &Router{
				logger:         log.NewNOPFactory().Logger(),
				rules:          testCase.rules,
				ruleNeedsPaths: make([]bool, len(testCase.rules)),
			}
			preview, selected, productionIndex, diagnostic := runTheThreeObservers(t, router, testCase.request)

			// The passive preview.
			if testCase.expected.ruleIndex < 0 {
				require.False(t, preview.Matched)
				require.Empty(t, preview.Unknown)
			} else {
				require.True(t, preview.Matched,
					"the preview must decide the same request production decides")
				require.Equal(t, testCase.expected.ruleIndex, preview.RuleIndex)
				require.Equal(t, testCase.expected.actionType, preview.ActionType)
				require.Equal(t, testCase.expected.outbound, preview.ActionOutbound)
			}

			// Production's own walk. It reports "no rule" by returning no rule at all; the index is
			// only meaningful for a decision.
			if testCase.expected.ruleIndex < 0 {
				require.Nil(t, selected)
			} else {
				require.NotNil(t, selected)
				require.Equal(t, testCase.expected.ruleIndex, productionIndex)
				require.Equal(t, testCase.expected.actionType, selected.Action().Type())
			}

			// The active diagnostic's mirror.
			if testCase.expected.ruleIndex < 0 {
				require.False(t, diagnostic.Matched)
				require.Empty(t, diagnostic.Unknown)
			} else {
				require.True(t, diagnostic.Matched,
					"the active diagnostic must decide what production decides")
				require.Equal(t, testCase.expected.ruleIndex, diagnostic.RuleIndex)
				require.Equal(t, testCase.expected.actionType, diagnostic.ActionType)
			}
		})
	}
}

// TestTheThreeObserversDivergeOnlyWhereAFactIsMissing is the honest difference: a process rule is
// undecidable without the fact, so the preview and the diagnostic must say UNKNOWN at that rule while
// production - which has the fact - decides it.
func TestTheThreeObserversDivergeOnlyWhereAFactIsMissing(t *testing.T) {
	router := newPreviewTestRouter(t, nil)
	router.needProcessPaths = false
	request := previewRequest("other.example", 443)

	preview, selected, productionIndex, diagnostic := runTheThreeObservers(t, router, request)

	// The preview: unknown at the path rule, and no invented decision.
	require.False(t, preview.Matched)
	require.NotEmpty(t, preview.Unknown)
	require.Equal(t, 2, preview.UnknownRuleIndex)

	// The diagnostic: the SAME unknown, from its own walk. If these two ever disagree, the report
	// would explain a rule the preview says it cannot name.
	require.False(t, diagnostic.Matched)
	require.NotEmpty(t, diagnostic.Unknown)
	require.Equal(t, 2, diagnostic.UnknownRuleIndex)
	require.Equal(t, preview.UnknownRuleIndex, diagnostic.UnknownRuleIndex)

	// Production, given the fact the other two lack, decides.
	request.ProcessPaths = []string{"/usr/bin/curl"}
	productionMetadata := adapter.InboundContext{
		InboundType: "tun",
		Network:     N.NetworkTCP,
		Source:      M.SocksaddrFrom(request.Source.Addr(), request.Source.Port()),
		Destination: M.Socksaddr{Fqdn: request.Domain, Port: request.Destination.Port},
		Domain:      request.Domain,
		ProcessInfo: &adapter.ConnectionOwner{UserId: 1000, ProcessPaths: []string{"/usr/bin/curl"}},
	}
	selected, productionIndex, _, _, err := router.matchRule(context.Background(), &productionMetadata, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, selected)
	require.Equal(t, 2, productionIndex)
	require.Equal(t, C.RuleActionTypeRoute, selected.Action().Type())
}

// TestTheDiagnosticReportsASniffAsUnknownLikeThePreview keeps the second undecidable fact pinned: a
// sniff rule needs a stream, and neither a preview nor a socketless diagnostic has one.
func TestTheDiagnosticReportsASniffAsUnknownLikeThePreview(t *testing.T) {
	router := &Router{
		logger: log.NewNOPFactory().Logger(),
		rules: []adapter.Rule{
			differentialRule("sniff", func(*adapter.InboundContext) bool { return true }, &R.RuleActionSniff{}),
			differentialRule("domain:after-sniff.example", func(metadata *adapter.InboundContext) bool {
				return metadata.Domain == "after-sniff.example"
			}, &R.RuleActionRoute{Outbound: "proxy"}),
		},
		ruleNeedsPaths: []bool{false, false},
	}
	preview, diagnostic := runThePreviewAndDiagnostic(t, router, previewRequest("", 443))
	require.False(t, preview.Matched)
	require.NotEmpty(t, preview.Unknown)
	require.Equal(t, 0, preview.UnknownRuleIndex)
	require.False(t, diagnostic.Matched)
	require.NotEmpty(t, diagnostic.Unknown)
	require.Equal(t, preview.UnknownRuleIndex, diagnostic.UnknownRuleIndex)

	// With the caller's own domain both continue and decide, still in agreement.
	decidedPreview, decidedDiagnostic := runThePreviewAndDiagnostic(t, router, previewRequest("after-sniff.example", 443))
	require.True(t, decidedPreview.Matched)
	require.Equal(t, 1, decidedPreview.RuleIndex)
	require.True(t, decidedDiagnostic.Matched)
	require.Equal(t, decidedPreview.RuleIndex, decidedDiagnostic.RuleIndex)
	require.Equal(t, decidedPreview.ActionType, decidedDiagnostic.ActionType)
}
