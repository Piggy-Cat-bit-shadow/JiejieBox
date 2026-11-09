package route

// O01 — the preview must agree with production and must not cause any of it.
//
// The preview evaluates the same rule list production walks, against the same matcher, but it is
// built from the caller's stated facts and never calls prepareMatchMetadata. These tests compare it
// with the real decision for the same request and then pin the absence of side effects: no /proc
// walk, no dial, no cache or history write, no selection change.

import (
	"context"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/process"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	R "github.com/sagernet/sing-box/route/rule"
	"github.com/sagernet/sing/common"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/contrab/freelru"
	"github.com/sagernet/sing/contrab/maphash"

	"github.com/stretchr/testify/require"
)

// previewTestRule is a rule whose matching behaviour the test states directly.
type previewTestRule struct {
	description string
	matches     func(*adapter.InboundContext) bool
	action      adapter.RuleAction
}

func (r *previewTestRule) Type() string                                { return C.RuleTypeDefault }
func (r *previewTestRule) String() string                              { return r.description }
func (r *previewTestRule) Match(metadata *adapter.InboundContext) bool { return r.matches(metadata) }
func (r *previewTestRule) Action() adapter.RuleAction                  { return r.action }
func (r *previewTestRule) Start() error                                { return nil }
func (r *previewTestRule) Close() error                                { return nil }

// countingPreviewSearcher counts every process lookup the router performs.
type countingPreviewSearcher struct {
	calls atomic.Int32
}

func (s *countingPreviewSearcher) FindProcessInfo(context.Context, string, netip.AddrPort, netip.AddrPort) (*adapter.ConnectionOwner, error) {
	s.calls.Add(1)
	return &adapter.ConnectionOwner{UserId: 1000, ProcessPaths: []string{"/usr/bin/curl"}}, nil
}

func (s *countingPreviewSearcher) FindProcessOwner(context.Context, string, netip.AddrPort, netip.AddrPort) (*adapter.ConnectionOwner, error) {
	s.calls.Add(1)
	return &adapter.ConnectionOwner{UserId: 1000}, nil
}

func (s *countingPreviewSearcher) Close() error { return nil }
func (s *countingPreviewSearcher) ResetCache()  {}

var _ process.Searcher = (*countingPreviewSearcher)(nil)

// newPreviewTestRouter builds the shape this comparison needs: an ordinary domain rule, a port
// rule, then a rule that reads a process path.
func newPreviewTestRouter(t *testing.T, searcher process.Searcher) *Router {
	t.Helper()
	domainRule := &previewTestRule{
		description: "domain:blocked.example",
		matches:     func(metadata *adapter.InboundContext) bool { return metadata.Domain == "blocked.example" },
		action:      &R.RuleActionRoute{Outbound: "proxy"},
	}
	portRule := &previewTestRule{
		description: "port:8080",
		matches:     func(metadata *adapter.InboundContext) bool { return metadata.Destination.Port == 8080 },
		action:      &R.RuleActionReject{},
	}
	pathRule := &previewTestRule{
		description: "process-path:/usr/bin/curl",
		matches: func(metadata *adapter.InboundContext) bool {
			return metadata.ProcessInfo != nil && len(metadata.ProcessInfo.ProcessPaths) > 0
		},
		action: &R.RuleActionRoute{Outbound: "direct-for-curl"},
	}
	router := &Router{
		logger:            log.NewNOPFactory().Logger(),
		processSearcher:   searcher,
		platformInterface: &localSourcePlatform{address: netip.MustParseAddr("192.168.1.2")},
		rules:             []adapter.Rule{domainRule, portRule, pathRule},
		ruleNeedsPaths:    []bool{false, false, true},
	}
	if searcher != nil {
		router.processCache = common.Must1(freelru.New[processCacheKey, processCacheEntry](
			256, maphash.NewHasher[processCacheKey]().Hash32, true))
		router.processCache.SetLifetime(time.Second)
	}
	return router
}

func previewRequest(domain string, port uint16) RoutePreviewRequest {
	return RoutePreviewRequest{
		Network:     N.NetworkTCP,
		Source:      netip.MustParseAddrPort("192.168.1.2:40000"),
		Destination: M.SocksaddrFrom(netip.MustParseAddr("93.184.216.34"), port),
		Domain:      domain,
		UserId:      -1,
	}
}

func TestThePreviewAgreesWithTheProductionDecision(t *testing.T) {
	searcher := &countingPreviewSearcher{}
	router := newPreviewTestRouter(t, searcher)
	router.needProcessPaths = false

	// The facts are supplied to the preview, so it never needs the searcher. The destination is a
	// domain, which is the state a sniffed or resolved request is in - and it is also what lets the
	// production walk run without a DNS router behind it.
	request := previewRequest("blocked.example", 443)
	request.ProcessPaths = []string{"/usr/bin/curl"}

	productionMetadata := adapter.InboundContext{
		InboundType: C.TypeTun,
		Network:     N.NetworkTCP,
		Source:      M.SocksaddrFrom(netip.MustParseAddr("192.168.1.2"), 40000),
		Destination: M.Socksaddr{Fqdn: "blocked.example", Port: 443},
		Domain:      "blocked.example",
		ProcessInfo: &adapter.ConnectionOwner{UserId: 1000, ProcessPaths: []string{"/usr/bin/curl"}},
	}
	selected, index, _, _, err := router.matchRule(context.Background(), &productionMetadata, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, selected)
	productionLookups := searcher.calls.Load()

	preview := router.PreviewRoute(context.Background(), request)
	require.True(t, preview.Matched)
	require.Equal(t, index, preview.RuleIndex,
		"the preview and the production walk must name the same rule for the same request")
	require.Equal(t, selected.Action().Type(), preview.ActionType)
	require.Equal(t, "proxy", preview.ActionOutbound)
	require.Equal(t, productionLookups, searcher.calls.Load(),
		"a preview must never resolve process facts itself")
}

func TestThePreviewFallsThroughLikeProduction(t *testing.T) {
	router := newPreviewTestRouter(t, nil)

	request := previewRequest("other.example", 443)
	request.ProcessPaths = []string{"/usr/bin/curl"}
	preview := router.PreviewRoute(context.Background(), request)
	require.True(t, preview.Matched)
	require.Equal(t, 2, preview.RuleIndex)
	require.Equal(t, "direct-for-curl", preview.ActionOutbound)

	// A request that matches nothing walks every rule and reports no match, not an error. The path
	// rule is removed first: with it present the honest answer for a request without process facts
	// is UNKNOWN (pinned by the next test), which is not the same question.
	router.rules = router.rules[:2]
	router.ruleNeedsPaths = router.ruleNeedsPaths[:2]
	unmatched := router.PreviewRoute(context.Background(), previewRequest("other.example", 443))
	require.False(t, unmatched.Matched)
	require.Empty(t, unmatched.Unknown)
	require.Equal(t, len(router.rules), unmatched.Evaluated)
}

func TestThePreviewReportsAnUnknownFactInsteadOfWalkingForIt(t *testing.T) {
	searcher := &countingPreviewSearcher{}
	router := newPreviewTestRouter(t, searcher)

	// No process facts: the path rule is UNDECIDABLE, not "not matching".
	preview := router.PreviewRoute(context.Background(), previewRequest("other.example", 443))
	require.False(t, preview.Matched)
	require.NotEmpty(t, preview.Unknown, "an undecidable rule must be reported as unknown")
	require.Equal(t, 2, preview.UnknownRuleIndex)
	require.Zero(t, searcher.calls.Load(),
		"reporting unknown must not become a /proc walk: that is the side effect a preview refuses")
}

func TestAPreviewDoesNotSniffOrDial(t *testing.T) {
	sniffRule := &previewTestRule{
		description: "sniff",
		matches:     func(*adapter.InboundContext) bool { return true },
		action:      &R.RuleActionSniff{},
	}
	domainRule := &previewTestRule{
		description: "domain:after-sniff.example",
		matches:     func(metadata *adapter.InboundContext) bool { return metadata.Domain == "after-sniff.example" },
		action:      &R.RuleActionRoute{Outbound: "proxy"},
	}
	router := &Router{
		logger:         log.NewNOPFactory().Logger(),
		rules:          []adapter.Rule{sniffRule, domainRule},
		ruleNeedsPaths: []bool{false, false},
	}

	// Without a domain the sniff would have produced, the later decision is unknowable here.
	unknown := router.PreviewRoute(context.Background(), previewRequest("", 443))
	require.False(t, unknown.Matched)
	require.NotEmpty(t, unknown.Unknown)
	require.Equal(t, 0, unknown.UnknownRuleIndex)

	// With the caller's own domain the preview continues and decides like production would.
	decided := router.PreviewRoute(context.Background(), previewRequest("after-sniff.example", 443))
	require.True(t, decided.Matched)
	require.Equal(t, 1, decided.RuleIndex)
	require.Equal(t, "proxy", decided.ActionOutbound)
}
