package dns

// O02 — the DNS rule walk completes a path only when it reaches a rule that reads one.
//
// The completer is injected by the layer that owns the lookup, so these cases drive a router with a
// recorder instead of a process searcher: the property under test is the WALK's behaviour - when it
// calls the completer, how many times, and what it does when the completer cannot answer.

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"

	mDNS "github.com/miekg/dns"

	"github.com/stretchr/testify/require"
)

func previewProcessPathRule(t *testing.T, path string, server string) option.DNSRule {
	t.Helper()
	return option.DNSRule{
		Type: "",
		DefaultOptions: option.DefaultDNSRule{
			RawDefaultDNSRule: option.RawDefaultDNSRule{ProcessPath: badoption.Listable[string]{path}},
			DNSRuleAction: option.DNSRuleAction{
				Action:       C.RuleActionTypeRoute,
				RouteOptions: option.DNSRouteActionOptions{Server: server},
			},
		},
	}
}

// newDeferralTestRouter builds a router whose rules are the given options, with the parallel
// path-need flags computed exactly as the production builder does.
func newDeferralTestRouter(t *testing.T, raw []option.DNSRule) (*Router, []adapter.DNSRule) {
	t.Helper()
	blocked := &fakeDNSTransport{tag: "blocked"}
	curlServer := &fakeDNSTransport{tag: "curl-server"}
	final := &fakeDNSTransport{tag: "final"}
	router := raceTestRouter(t, blocked, curlServer, final)
	rules := previewTestRules(t, raw)
	router.rules = rules
	router.ruleNeedsPaths = processPathRuleFlags(raw)
	return router, rules
}

func TestADomainRuleDecidesWithoutCompletingAPath(t *testing.T) {
	raw := []option.DNSRule{
		previewTestRule(t, "example.org", "blocked"),
		previewProcessPathRule(t, "/usr/bin/curl", "curl-server"),
	}
	router, _ := newDeferralTestRouter(t, raw)
	calls := 0
	router.SetProcessPathCompleter(func(ctx context.Context, metadata *adapter.InboundContext) {
		calls++
		metadata.ProcessInfo = &adapter.ConnectionOwner{ProcessPaths: []string{"/usr/bin/curl"}}
	})

	preview := router.PreviewLookup(context.Background(), PreviewQuery{Name: "race.example.org.", QType: mDNS.TypeA})
	require.True(t, preview.Matched)
	require.Equal(t, 0, preview.RuleIndex)
	require.Zero(t, calls, "a rule that does not read a path must not trigger the completion")
}

func TestAPathRuleCompletesOnceAndThenMatches(t *testing.T) {
	raw := []option.DNSRule{
		previewTestRule(t, "blocked.example", "blocked"),
		previewProcessPathRule(t, "/usr/bin/curl", "curl-server"),
	}
	router, _ := newDeferralTestRouter(t, raw)
	calls := 0
	router.SetProcessPathCompleter(func(ctx context.Context, metadata *adapter.InboundContext) {
		calls++
		metadata.ProcessInfo = &adapter.ConnectionOwner{ProcessPaths: []string{"/usr/bin/curl"}}
	})

	preview := router.PreviewLookup(context.Background(), PreviewQuery{Name: "race.example.org.", QType: mDNS.TypeA})
	require.Equal(t, 1, calls, "the walk completes the path exactly when it reaches the rule that reads one")
	require.True(t, preview.Matched)
	require.Equal(t, 1, preview.RuleIndex)
	require.Equal(t, "curl-server", preview.Server)
}

func TestWithoutACompleterNothingChanges(t *testing.T) {
	raw := []option.DNSRule{
		previewTestRule(t, "blocked.example", "blocked"),
		previewProcessPathRule(t, "/usr/bin/curl", "curl-server"),
	}
	router, _ := newDeferralTestRouter(t, raw)

	preview := router.PreviewLookup(context.Background(), PreviewQuery{Name: "race.example.org.", QType: mDNS.TypeA})
	require.False(t, preview.Matched,
		"with no completer the rule evaluates against the metadata it was given, as before")
	require.Equal(t, "final", preview.Server)
}

func TestAFailedCompletionIsNotRetriedPerRule(t *testing.T) {
	raw := []option.DNSRule{
		previewTestRule(t, "blocked.example", "blocked"),
		previewProcessPathRule(t, "/usr/bin/curl", "curl-server"),
		previewProcessPathRule(t, "/usr/bin/wget", "wget-server"),
	}
	router, _ := newDeferralTestRouter(t, raw)
	calls := 0
	router.SetProcessPathCompleter(func(ctx context.Context, metadata *adapter.InboundContext) {
		// The lookup could not answer: ProcessInfo stays empty.
		calls++
	})

	preview := router.PreviewLookup(context.Background(), PreviewQuery{Name: "race.example.org.", QType: mDNS.TypeA})
	require.Equal(t, 1, calls,
		"a failed lookup must not be retried for every later path rule: that would turn one miss into one per rule")
	require.False(t, preview.Matched)
}

func TestProcessPathRuleFlagsFollowTheConfiguration(t *testing.T) {
	require.False(t, dnsRuleNeedsProcessPaths(previewTestRule(t, "example.org", "blocked")))
	require.True(t, dnsRuleNeedsProcessPaths(previewProcessPathRule(t, "/usr/bin/curl", "curl")))
	require.True(t, dnsRuleNeedsProcessPaths(option.DNSRule{
		Type: "",
		DefaultOptions: option.DefaultDNSRule{
			RawDefaultDNSRule: option.RawDefaultDNSRule{RuleSet: badoption.Listable[string]{"ads"}},
		},
	}), "a rule-set document may carry process conditions, so it is conservatively a path need")
}
