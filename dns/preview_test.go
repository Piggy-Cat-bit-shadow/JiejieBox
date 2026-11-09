package dns

// O01 — the DNS preview must agree with the real exchange and cause none of it.
//
// The preview reuses matchDNS, so the comparison here is not "does the preview implement the walk
// the same way" but "does the walk decide the same thing for a preview as for a real query" - which
// is what catches a preview that prepares the metadata differently, or that passes a different
// allowFakeIP / address-query flag.

import (
	"context"
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	R "github.com/sagernet/sing-box/route/rule"

	mDNS "github.com/miekg/dns"

	"github.com/stretchr/testify/require"
)

func previewTestRule(t *testing.T, suffix, server string) option.DNSRule {
	t.Helper()
	return option.DNSRule{
		Type: "",
		DefaultOptions: option.DefaultDNSRule{
			RawDefaultDNSRule: option.RawDefaultDNSRule{DomainSuffix: []string{suffix}},
			DNSRuleAction: option.DNSRuleAction{
				Action:       C.RuleActionTypeRoute,
				RouteOptions: option.DNSRouteActionOptions{Server: server},
			},
		},
	}
}

func previewTestRules(t *testing.T, raw []option.DNSRule) []adapter.DNSRule {
	t.Helper()
	rules := make([]adapter.DNSRule, 0, len(raw))
	for _, rawRule := range raw {
		rule, err := R.NewDNSRule(context.Background(), log.NewNOPFactory().Logger(), rawRule, true, false)
		require.NoError(t, err)
		require.NoError(t, rule.Start())
		rules = append(rules, rule)
	}
	return rules
}

func TestTheDNSPreviewNamesTheSameServerAsTheRealExchange(t *testing.T) {
	blocked := &fakeDNSTransport{tag: "blocked"}
	final := &fakeDNSTransport{tag: "final"}
	router := raceTestRouter(t, blocked, final)
	rules := previewTestRules(t, []option.DNSRule{previewTestRule(t, "example.org", "blocked")})
	router.rules = rules

	// The real exchange helper asks about race.example.org, so the preview must ask the same
	// question; comparing two different questions would prove nothing.
	preview := router.PreviewLookup(context.Background(), PreviewQuery{
		Name:        "race.example.org.",
		QType:       mDNS.TypeA,
		AllowFakeIP: false,
	})
	require.True(t, preview.Matched)
	require.Equal(t, 0, preview.RuleIndex)
	require.Equal(t, "blocked", preview.Server)

	real := raceTestExchangeContext(context.Background(), router, rules)
	require.NotNil(t, real.transport, "the real exchange must have used a transport")
	require.Equal(t, real.transport.Tag(), preview.Server,
		"the preview must name the server the real query actually used")
}

func TestTheDNSPreviewFallsThroughToTheDefaultServer(t *testing.T) {
	blocked := &fakeDNSTransport{tag: "blocked"}
	final := &fakeDNSTransport{tag: "final"}
	router := raceTestRouter(t, blocked, final)
	rules := previewTestRules(t, []option.DNSRule{previewTestRule(t, "blocked.example", "blocked")})
	router.rules = rules

	preview := router.PreviewLookup(context.Background(), PreviewQuery{
		Name:        "race.example.org.",
		QType:       mDNS.TypeA,
		AllowFakeIP: false,
	})
	require.False(t, preview.Matched)
	require.Equal(t, -1, preview.RuleIndex)
	require.Equal(t, "final", preview.Server, "no match means the default transport answers")

	real := raceTestExchangeContext(context.Background(), router, rules)
	require.NotNil(t, real.transport)
	require.Equal(t, real.transport.Tag(), preview.Server)
	require.Equal(t, len(rules), preview.Evaluated)
}

// TestTheDNSPreviewChangesNothing pins the no-side-effect half: the same router answers the same
// real query before and after a preview, and the preview does not leave the real request's metadata
// altered.
func TestTheDNSPreviewChangesNothing(t *testing.T) {
	blocked := &fakeDNSTransport{tag: "blocked", address: mustAddress(t)}
	final := &fakeDNSTransport{tag: "final"}
	router := raceTestRouter(t, blocked, final)
	rules := previewTestRules(t, []option.DNSRule{previewTestRule(t, "example.org", "blocked")})
	router.rules = rules

	before := raceTestExchangeContext(context.Background(), router, rules)
	require.NotNil(t, before.transport)

	// The real request's metadata is provided by the caller; a preview must not mutate it.
	metadata := &adapter.InboundContext{Domain: "race.example.org", QueryType: mDNS.TypeA}
	requestContext := adapter.WithContext(context.Background(), metadata)
	preview := router.PreviewLookup(requestContext, PreviewQuery{
		Name:        "race.example.org.",
		QType:       mDNS.TypeA,
		AllowFakeIP: false,
	})
	require.Equal(t, "blocked", preview.Server)
	require.Equal(t, "race.example.org", metadata.Domain,
		"the preview evaluates its own metadata copy, never the caller's")

	after := raceTestExchangeContext(requestContext, router, rules)
	require.NotNil(t, after.transport)
	require.Equal(t, before.transport.Tag(), after.transport.Tag(),
		"a preview between two identical queries must not change what the second one does")
	require.Equal(t, before.response.Rcode, after.response.Rcode)
}

func mustAddress(t *testing.T) netip.Addr {
	t.Helper()
	return netip.MustParseAddr("192.0.2.9")
}
