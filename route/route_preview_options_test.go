package route

// O01 — the preview must apply the SAME pure metadata transformations production applies.
//
// A route action's options are not decoration: override_address / override_port rewrite what every
// LATER rule sees, so a rule that matches on the rewritten destination is part of the real
// decision. The preview evaluated the rules against the pre-rewrite metadata, so the same request
// could be reported as "no rule matched" while production rejected it one rule later.
//
// The fix is not a second implementation: the preview calls applyActionRouteOptions - the same
// function the production walk calls - on its private metadata copy.

import (
	"context"
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	R "github.com/sagernet/sing-box/route/rule"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// routeOptionsRewriteRule matches a domain and rewrites the port for every rule after it, exactly as
// the reviewed configuration did (443 -> 8080).
func routeOptionsRewriteRule() *previewTestRule {
	return &previewTestRule{
		description: "route-options:override_port=8080",
		matches:     func(metadata *adapter.InboundContext) bool { return metadata.Domain == "example.org" },
		action: &R.RuleActionRouteOptions{
			OverridePort: 8080,
		},
	}
}

func port8080RejectRule() *previewTestRule {
	return &previewTestRule{
		description: "destination-port:8080",
		matches:     func(metadata *adapter.InboundContext) bool { return metadata.Destination.Port == 8080 },
		action:      &R.RuleActionReject{},
	}
}

// TestThePreviewAppliesRouteOptionsLikeProduction is the review reproduction.
func TestThePreviewAppliesRouteOptionsLikeProduction(t *testing.T) {
	router := &Router{
		logger:         log.NewNOPFactory().Logger(),
		rules:          []adapter.Rule{routeOptionsRewriteRule(), port8080RejectRule()},
		ruleNeedsPaths: []bool{false, false},
	}

	// Production, for the same request: the rewrite is applied and the port rule rejects.
	productionMetadata := adapter.InboundContext{
		InboundType: C.TypeTun,
		Network:     N.NetworkTCP,
		Source:      M.ParseSocksaddr("192.168.1.2:40000"),
		Destination: M.Socksaddr{Fqdn: "example.org", Port: 443},
		Domain:      "example.org",
	}
	selected, index, _, _, err := router.matchRule(context.Background(), &productionMetadata, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, selected)
	require.Equal(t, 1, index, "production decides at the rule that matches the REWRITTEN port")
	require.Equal(t, C.RuleActionTypeReject, selected.Action().Type())

	preview := router.PreviewRoute(context.Background(), RoutePreviewRequest{
		Network:     N.NetworkTCP,
		Source:      netip.MustParseAddrPort("192.168.1.2:40000"),
		Destination: M.Socksaddr{Fqdn: "example.org", Port: 443},
		Domain:      "example.org",
		UserId:      -1,
	})
	require.True(t, preview.Matched,
		"a rule decides this request; reporting no match would contradict production")
	require.Equal(t, 1, preview.RuleIndex, "the preview must name the same rule production does")
	require.Equal(t, C.RuleActionTypeReject, preview.ActionType)
}

// TestThePreviewKeepsTheRewriteOnItsOwnCopy guards the read-only half: applying the options must not
// touch the caller's request, and must not leak between two previews.
func TestThePreviewKeepsTheRewriteOnItsOwnCopy(t *testing.T) {
	router := &Router{
		logger:         log.NewNOPFactory().Logger(),
		rules:          []adapter.Rule{routeOptionsRewriteRule(), port8080RejectRule()},
		ruleNeedsPaths: []bool{false, false},
	}
	request := RoutePreviewRequest{
		Network:     N.NetworkTCP,
		Source:      netip.MustParseAddrPort("192.168.1.2:40000"),
		Destination: M.Socksaddr{Fqdn: "example.org", Port: 443},
		Domain:      "example.org",
		UserId:      -1,
	}
	first := router.PreviewRoute(context.Background(), request)
	require.Equal(t, 1, first.RuleIndex)
	require.Equal(t, uint16(443), request.Destination.Port,
		"the caller's own request is never mutated by a preview")

	// A second preview of the same request must reach the same answer: the rewrite is rebuilt on a
	// fresh copy, not left behind on shared state.
	second := router.PreviewRoute(context.Background(), request)
	require.Equal(t, first.RuleIndex, second.RuleIndex)
}
