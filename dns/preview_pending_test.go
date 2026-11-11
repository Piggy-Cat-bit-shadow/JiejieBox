package dns

// P1.3 — the preview must report the facts it does not have instead of walking past them.
//
// Two reviewed failures:
//  1. evaluate/respond configuration: the real Exchange answered from the evaluate rule, the preview
//     reported Server=final, Unknown=false.
//  2. a process_path rule was treated as satisfied by a package list (any process fact counted).
//
// The modern walk is now previewed on its own decision points (response bindings, evaluate/respond,
// per-field process needs), and the process mask distinguishes path from package.

import (
	"context"
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"

	mDNS "github.com/miekg/dns"

	"github.com/stretchr/testify/require"
)

func previewPackageRule(t *testing.T, packageName string, server string) option.DNSRule {
	t.Helper()
	return option.DNSRule{
		Type: "",
		DefaultOptions: option.DefaultDNSRule{
			RawDefaultDNSRule: option.RawDefaultDNSRule{PackageName: badoption.Listable[string]{packageName}},
			DNSRuleAction: option.DNSRuleAction{
				Action:       C.RuleActionTypeRoute,
				RouteOptions: option.DNSRouteActionOptions{Server: server},
			},
		},
	}
}

// TestThePreviewReportsTheResponseDependencyOfEvaluate is reviewed failure 1.
func TestThePreviewReportsTheResponseDependencyOfEvaluate(t *testing.T) {
	fixture := newDeferralFixture(t, []option.DNSRule{
		evaluateRule("primary", "primary", false),
		respondRule("primary", false, true),
	})
	// Give the fixture the two evaluating transports it needs.
	primary := &fakeDNSTransport{tag: "primary", address: netip.MustParseAddr("192.0.2.17")}
	final := &fakeDNSTransport{tag: "final", address: netip.MustParseAddr("192.0.2.18")}
	fixture.router = raceTestRouter(t, primary, final)
	fixture.router.rules = previewTestRules(t, []option.DNSRule{
		evaluateRule("primary", "primary", false),
		respondRule("primary", false, true),
	})
	fixture.router.ruleNeedsPaths = processPathRuleFlags([]option.DNSRule{
		evaluateRule("primary", "primary", false),
		respondRule("primary", false, true),
	})
	fixture.router.ruleProcessNeeds = processNeedFlags([]option.DNSRule{
		evaluateRule("primary", "primary", false),
		respondRule("primary", false, true),
	})

	response, err := fixture.router.Exchange(context.Background(), deferralQuery("race.example.org.", mDNS.TypeA), adapter.DNSQueryOptions{})
	require.NoError(t, err)
	require.NotNil(t, response)
	require.Equal(t, "192.0.2.17", responseAddress(t, response).String(),
		"production decides the query at the evaluate rule")

	preview := fixture.router.PreviewLookup(context.Background(), PreviewQuery{
		Name:  "race.example.org.",
		QType: mDNS.TypeA,
	})
	require.True(t, preview.Unknown,
		"an evaluate/respond decision needs a real DNS response, which a preview does not have")
	require.Equal(t, "dns_response", preview.DependsOn)
	require.False(t, preview.Matched)
	require.Empty(t, preview.Server, "naming the default server would contradict the real exchange")
	require.Equal(t, 0, preview.RuleIndex, "the rule that needs the response is the one named")
}

// TestThePreviewReportsTheProcessDependencyPerField is reviewed failure 2.
func TestThePreviewReportsTheProcessDependencyPerField(t *testing.T) {
	raw := []option.DNSRule{
		previewTestRule(t, "blocked.example", "blocked"),
		previewProcessPathRule(t, "/usr/bin/curl", "curl-server"),
	}
	fixture := newDeferralFixture(t, raw)
	var completions int
	fixture.router.SetProcessPathCompleter(func(ctx context.Context, metadata *adapter.InboundContext) {
		completions++
		metadata.ProcessInfo = &adapter.ConnectionOwner{ProcessPaths: []string{"/usr/bin/curl"}}
	})

	// Only a PACKAGE list is supplied: it is not the path the rule reads.
	preview := fixture.router.PreviewLookup(context.Background(), PreviewQuery{
		Name:         "race.example.org.",
		QType:        mDNS.TypeA,
		PackageNames: []string{"some.app"},
	})
	require.True(t, preview.Unknown)
	require.Equal(t, "process_path", preview.DependsOn)
	require.Equal(t, 1, preview.RuleIndex)
	require.Zero(t, completions, "a preview never runs the completion")
	require.Zero(t, fixture.curlServer.queryCount.Load())

	// Supplying the PATH the rule reads decides it, without a completion.
	decided := fixture.router.PreviewLookup(context.Background(), PreviewQuery{
		Name:         "race.example.org.",
		QType:        mDNS.TypeA,
		ProcessPaths: []string{"/usr/bin/curl"},
	})
	require.False(t, decided.Unknown)
	require.True(t, decided.Matched)
	require.Equal(t, "curl-server", decided.Server)
	require.Zero(t, completions)
}

// TestThePreviewReportsThePackageDependency shows the other direction.
func TestThePreviewReportsThePackageDependency(t *testing.T) {
	fixture := newDeferralFixture(t, []option.DNSRule{
		previewPackageRule(t, "com.example.app", "curl-server"),
	})
	preview := fixture.router.PreviewLookup(context.Background(), PreviewQuery{
		Name:         "race.example.org.",
		QType:        mDNS.TypeA,
		ProcessPaths: []string{"/usr/bin/curl"},
	})
	require.True(t, preview.Unknown)
	require.Equal(t, "process_package", preview.DependsOn)

	decided := fixture.router.PreviewLookup(context.Background(), PreviewQuery{
		Name:         "race.example.org.",
		QType:        mDNS.TypeA,
		PackageNames: []string{"com.example.app"},
	})
	require.False(t, decided.Unknown)
	require.True(t, decided.Matched)
	require.Equal(t, "curl-server", decided.Server)
}

// TestTheModernPreviewDecidesPureRules pins the decisions a preview CAN make in modern mode.
func TestTheModernPreviewDecidesPureRules(t *testing.T) {
	fixture := newDeferralFixture(t, []option.DNSRule{
		previewTestRule(t, "example.org", "blocked"),
	})
	preview := fixture.router.PreviewLookup(context.Background(), PreviewQuery{Name: "race.example.org.", QType: mDNS.TypeA})
	require.False(t, preview.Unknown)
	require.True(t, preview.Matched)
	require.Equal(t, "blocked", preview.Server)
	require.Zero(t, fixture.blocked.queryCount.Load(),
		"a preview decides from the rule list and must not issue a query")
}

// TestTheLegacyPreviewKeepsTheLegacyMatcher: legacy mode still previews through matchDNS, and keeps
// the per-field process dependency.
func TestTheLegacyPreviewKeepsTheLegacyMatcher(t *testing.T) {
	fixture := newDeferralFixture(t, []option.DNSRule{
		previewTestRule(t, "example.org", "blocked"),
		previewProcessPathRule(t, "/usr/bin/curl", "curl-server"),
	})
	fixture.router.legacyDNSMode = true

	decided := fixture.router.PreviewLookup(context.Background(), PreviewQuery{Name: "race.example.org.", QType: mDNS.TypeA})
	require.True(t, decided.Matched)
	require.Equal(t, 0, decided.RuleIndex)
	require.Equal(t, "blocked", decided.Server)

	unknown := fixture.router.PreviewLookup(context.Background(), PreviewQuery{
		Name:         "other.example.",
		QType:        mDNS.TypeA,
		PackageNames: []string{"some.app"},
	})
	require.True(t, unknown.Unknown)
	require.Equal(t, "process_path", unknown.DependsOn)
	require.Equal(t, 1, unknown.RuleIndex)
}

// TestTheCompleterRunsWhenOnlyTheOtherFieldIsKnown is the production half: a package rule must still
// trigger the completion when the metadata already carries paths (and vice versa).
func TestTheCompleterRunsWhenOnlyTheOtherFieldIsKnown(t *testing.T) {
	fixture := newDeferralFixture(t, []option.DNSRule{
		previewPackageRule(t, "com.example.app", "curl-server"),
	})
	var completions int
	fixture.router.SetProcessPathCompleter(func(ctx context.Context, metadata *adapter.InboundContext) {
		completions++
		metadata.ProcessInfo = &adapter.ConnectionOwner{PackageNames: []string{"com.example.app"}}
	})

	// The caller's metadata already has a PATH; the rule needs a PACKAGE.
	base := &adapter.InboundContext{ProcessInfo: &adapter.ConnectionOwner{ProcessPaths: []string{"/usr/bin/curl"}}}
	response, err := fixture.router.Exchange(adapter.WithContext(context.Background(), base), deferralQuery("race.example.org.", mDNS.TypeA), adapter.DNSQueryOptions{})
	require.NoError(t, err)
	require.NotNil(t, response)
	require.EqualValues(t, 1, completions, "the other field does not satisfy the rule's need")
	require.EqualValues(t, 1, fixture.curlServer.queryCount.Load(), "the rule is decided after the completion")
}
