package dns

// O02 — the DNS rule walk completes a process path only when it reaches a rule that reads one, and
// only the PRODUCTION entries may do it.
//
// # Why these cases no longer drive PreviewLookup
//
// The first version of this file tested the mechanism through PreviewLookup. Review found that the
// property it appeared to prove was false in two directions at once:
//
//   - the modern exchange walk (walkDNSRules) never called the completer, so a real query evaluated
//     a process-path rule against nil metadata and silently decided "no match". The preview was the
//     ONLY caller that triggered a completion, which is exactly backwards: a preview must be
//     read-only and an exchange is the thing that owns the lookup.
//   - the preview therefore also violated its own contract by running a /proc walk.
//
// These cases drive Router.Exchange, ExchangeAsync, Lookup, LookupFamilies and the legacy exchange,
// and the preview cases assert that it does NOT complete anything.

import (
	"context"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

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

// deferralFixture is one router plus the transports it can reach, so a case can assert WHICH server
// answered and not only that something did.
type deferralFixture struct {
	router     *Router
	blocked    *fakeDNSTransport
	curlServer *fakeDNSTransport
	eval       *fakeDNSTransport
	final      *fakeDNSTransport
}

func newDeferralFixture(t *testing.T, raw []option.DNSRule) *deferralFixture {
	t.Helper()
	fixture := &deferralFixture{
		blocked:    &fakeDNSTransport{tag: "blocked", address: netip.MustParseAddr("192.0.2.1")},
		curlServer: &fakeDNSTransport{tag: "curl-server", address: netip.MustParseAddr("192.0.2.2")},
		eval:       &fakeDNSTransport{tag: "eval", address: netip.MustParseAddr("192.0.2.3"), queried: make(chan struct{})},
		final:      &fakeDNSTransport{tag: "final", address: netip.MustParseAddr("192.0.2.4")},
	}
	fixture.router = raceTestRouter(t, fixture.blocked, fixture.curlServer, fixture.eval, fixture.final)
	rules := previewTestRules(t, raw)
	fixture.router.rules = rules
	fixture.router.ruleNeedsPaths = processPathRuleFlags(raw)
	fixture.router.ruleProcessNeeds = processNeedFlags(raw)
	return fixture
}

// deferralRecorder counts completions and answers with a fixed path.
func deferralRecorder(calls *atomic.Int32) func(ctx context.Context, metadata *adapter.InboundContext) {
	return func(ctx context.Context, metadata *adapter.InboundContext) {
		calls.Add(1)
		metadata.ProcessInfo = &adapter.ConnectionOwner{ProcessPaths: []string{"/usr/bin/curl"}}
	}
}

func deferralQuery(name string, qType uint16) *mDNS.Msg {
	return &mDNS.Msg{
		MsgHdr:   mDNS.MsgHdr{Id: 1, RecursionDesired: true},
		Question: []mDNS.Question{{Name: name, Qtype: qType, Qclass: mDNS.ClassINET}},
	}
}

// TestTheModernExchangeCompletesAPathWhenTheWalkReachesTheRule is the regression review found.
//
// The modern walk is production for every configuration that is not in legacy mode. Before the fix
// the completer was called zero times here, the process-path rule evaluated against nil metadata,
// and the query was answered by the DEFAULT transport - a silently different decision.
func TestTheModernExchangeCompletesAPathWhenTheWalkReachesTheRule(t *testing.T) {
	fixture := newDeferralFixture(t, []option.DNSRule{
		previewTestRule(t, "blocked.example", "blocked"),
		previewProcessPathRule(t, "/usr/bin/curl", "curl-server"),
	})
	var calls atomic.Int32
	fixture.router.SetProcessPathCompleter(deferralRecorder(&calls))

	response, err := fixture.router.Exchange(context.Background(), deferralQuery("race.example.org.", mDNS.TypeA), adapter.DNSQueryOptions{})
	require.NoError(t, err)
	require.NotNil(t, response)
	require.EqualValues(t, 1, calls.Load(),
		"the production walk must complete the path when it reaches the rule that reads one")
	require.EqualValues(t, 1, fixture.curlServer.queryCount.Load(), "the path rule's server answers")
	require.Zero(t, fixture.final.queryCount.Load(),
		"without the completion the walk falls through to the default server")
	require.Zero(t, fixture.blocked.queryCount.Load())
}

// TestTheModernExchangeDoesNotCompleteBeforeAPathRuleIsReached is the saving itself: a flow decided
// by an earlier rule must not pay for the walk.
func TestTheModernExchangeDoesNotCompleteBeforeAPathRuleIsReached(t *testing.T) {
	fixture := newDeferralFixture(t, []option.DNSRule{
		previewTestRule(t, "example.org", "blocked"),
		previewProcessPathRule(t, "/usr/bin/curl", "curl-server"),
	})
	var calls atomic.Int32
	fixture.router.SetProcessPathCompleter(deferralRecorder(&calls))

	response, err := fixture.router.Exchange(context.Background(), deferralQuery("race.example.org.", mDNS.TypeA), adapter.DNSQueryOptions{})
	require.NoError(t, err)
	require.NotNil(t, response)
	require.Zero(t, calls.Load(), "a rule that does not read a path must not trigger the completion")
	require.EqualValues(t, 1, fixture.blocked.queryCount.Load())
	require.Zero(t, fixture.curlServer.queryCount.Load())
}

// TestTheAsyncExchangeCompletesTheSamePath covers ExchangeAsync, which production uses for hijacked
// packets and streams.
func TestTheAsyncExchangeCompletesTheSamePath(t *testing.T) {
	fixture := newDeferralFixture(t, []option.DNSRule{
		previewTestRule(t, "blocked.example", "blocked"),
		previewProcessPathRule(t, "/usr/bin/curl", "curl-server"),
	})
	var calls atomic.Int32
	fixture.router.SetProcessPathCompleter(deferralRecorder(&calls))

	type outcome struct {
		response *mDNS.Msg
		err      error
	}
	done := make(chan outcome, 1)
	fixture.router.ExchangeAsync(context.Background(), deferralQuery("race.example.org.", mDNS.TypeA), adapter.DNSQueryOptions{},
		func(response *mDNS.Msg, err error) {
			done <- outcome{response: response, err: err}
		})
	select {
	case result := <-done:
		require.NoError(t, result.err)
		require.NotNil(t, result.response)
	case <-time.After(10 * time.Second):
		t.Fatal("the asynchronous exchange never returned")
	}
	require.EqualValues(t, 1, calls.Load())
	require.EqualValues(t, 1, fixture.curlServer.queryCount.Load())
	require.Zero(t, fixture.final.queryCount.Load())
}

// TestTheLegacyExchangeCompletesThePath covers the mode selected by a legacy address filter. It is a
// different walk (matchDNS) and must reach the same decision.
func TestTheLegacyExchangeCompletesThePath(t *testing.T) {
	fixture := newDeferralFixture(t, []option.DNSRule{
		previewTestRule(t, "blocked.example", "blocked"),
		previewProcessPathRule(t, "/usr/bin/curl", "curl-server"),
	})
	fixture.router.legacyDNSMode = true
	var calls atomic.Int32
	fixture.router.SetProcessPathCompleter(deferralRecorder(&calls))

	response, err := fixture.router.Exchange(context.Background(), deferralQuery("race.example.org.", mDNS.TypeA), adapter.DNSQueryOptions{})
	require.NoError(t, err)
	require.NotNil(t, response)
	require.EqualValues(t, 1, calls.Load())
	require.EqualValues(t, 1, fixture.curlServer.queryCount.Load())
	require.Zero(t, fixture.final.queryCount.Load())
}

// TestTheSingleRecordLookupCompletesThePath covers the dialer's lookup path, a third entry.
//
// A complete lookup is two family decisions (A and AAAA), each of which walks the rules, so the
// completion runs once per decision. The route layer's cache is what keeps the /proc walk itself
// from being paid twice for the same socket tuple.
func TestTheSingleRecordLookupCompletesThePath(t *testing.T) {
	fixture := newDeferralFixture(t, []option.DNSRule{
		previewTestRule(t, "blocked.example", "blocked"),
		previewProcessPathRule(t, "/usr/bin/curl", "curl-server"),
	})
	var calls atomic.Int32
	fixture.router.SetProcessPathCompleter(deferralRecorder(&calls))

	addresses, err := fixture.router.Lookup(context.Background(), "race.example.org", adapter.DNSQueryOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, addresses)
	require.EqualValues(t, 2, calls.Load(), "one completion per family decision")
	require.EqualValues(t, 2, fixture.curlServer.queryCount.Load())
	require.Zero(t, fixture.final.queryCount.Load())
}

// TestTheDualStackLookupCompletesEachFamilysDecision covers LookupFamilies, which issues one Lookup
// per family: each family is its own decision, so each completes (the route layer's cache is what
// makes the second one cheap).
func TestTheDualStackLookupCompletesEachFamilysDecision(t *testing.T) {
	fixture := newDeferralFixture(t, []option.DNSRule{
		previewTestRule(t, "blocked.example", "blocked"),
		previewProcessPathRule(t, "/usr/bin/curl", "curl-server"),
	})
	var calls atomic.Int32
	fixture.router.SetProcessPathCompleter(deferralRecorder(&calls))

	var published atomic.Int32
	err := fixture.router.LookupFamilies(context.Background(), "race.example.org", adapter.DNSQueryOptions{},
		func(result adapter.DNSFamilyResult) {
			published.Add(1)
		})
	require.NoError(t, err)
	require.EqualValues(t, 2, published.Load(), "both families are reported")
	require.EqualValues(t, 2, calls.Load(), "each family's decision completes the path once")
	require.EqualValues(t, 2, fixture.curlServer.queryCount.Load())
	require.Zero(t, fixture.final.queryCount.Load())
}

// TestASuspendedWalkCompletesThePathOnceAfterItResumes pins the state carrying the attempt.
//
// An `evaluate` action and a response-bound rule make the walk suspend; the process-path rule is
// only reached AFTER the resume. The completion must still happen (once), and the state must not
// lose the "already attempted" fact across the suspension.
func TestASuspendedWalkCompletesThePathOnceAfterItResumes(t *testing.T) {
	fixture := newDeferralFixture(t, []option.DNSRule{
		evaluateRule("eval", "pending", false),
		boundMismatchRule(t, "pending", "blocked.example", "blocked"),
		previewProcessPathRule(t, "/usr/bin/curl", "curl-server"),
	})
	var calls atomic.Int32
	fixture.router.SetProcessPathCompleter(deferralRecorder(&calls))
	release := make(chan struct{})
	fixture.eval.release = release

	type outcome struct {
		response *mDNS.Msg
		err      error
	}
	done := make(chan outcome, 1)
	go func() {
		response, err := fixture.router.Exchange(context.Background(), deferralQuery("race.example.org.", mDNS.TypeA), adapter.DNSQueryOptions{})
		done <- outcome{response: response, err: err}
	}()
	select {
	case <-fixture.eval.queried:
	case <-time.After(10 * time.Second):
		t.Fatal("the evaluate action never launched its query, so the walk cannot have suspended")
	}
	// The held future is the only reason this exchange cannot finish. Waiting before releasing it
	// makes the release a RESUME: the walk has parked on the response-bound rule by now.
	held := 100 * time.Millisecond
	start := time.Now()
	time.Sleep(held)
	close(release)
	select {
	case result := <-done:
		require.NoError(t, result.err)
		require.NotNil(t, result.response)
	case <-time.After(10 * time.Second):
		t.Fatal("the exchange never returned after the evaluated response was released")
	}
	require.GreaterOrEqual(t, time.Since(start), held/2,
		"the exchange must have waited for the held evaluate response, which is what makes this a resume")
	require.EqualValues(t, 1, calls.Load(), "the resumed walk completes the path exactly once")
	require.EqualValues(t, 1, fixture.curlServer.queryCount.Load(),
		"the rule reached after the resume's rule still decides the query")
}

// boundMismatchRule has a response binding (so the walk must wait for that response) and a domain
// condition that will not match, so the walk continues past it after the resume.
func boundMismatchRule(t *testing.T, responseTag string, suffix string, server string) option.DNSRule {
	t.Helper()
	return option.DNSRule{
		Type: "",
		DefaultOptions: option.DefaultDNSRule{
			RawDefaultDNSRule: option.RawDefaultDNSRule{
				DomainSuffix:  []string{suffix},
				MatchResponse: &option.DNSRuleMatchResponse{Enabled: true, Tag: responseTag},
			},
			DNSRuleAction: option.DNSRuleAction{
				Action:       C.RuleActionTypeRoute,
				RouteOptions: option.DNSRouteActionOptions{Server: server},
			},
		},
	}
}

// TestAFailedCompletionIsNotRetriedPerRule pins the once-per-walk rule on the production path.
func TestAFailedCompletionIsNotRetriedPerRule(t *testing.T) {
	fixture := newDeferralFixture(t, []option.DNSRule{
		previewTestRule(t, "blocked.example", "blocked"),
		previewProcessPathRule(t, "/usr/bin/curl", "curl-server"),
		previewProcessPathRule(t, "/usr/bin/wget", "blocked"),
	})
	var calls atomic.Int32
	fixture.router.SetProcessPathCompleter(func(ctx context.Context, metadata *adapter.InboundContext) {
		// The lookup could not answer: ProcessInfo stays empty.
		calls.Add(1)
	})

	response, err := fixture.router.Exchange(context.Background(), deferralQuery("race.example.org.", mDNS.TypeA), adapter.DNSQueryOptions{})
	require.NoError(t, err)
	require.NotNil(t, response)
	require.EqualValues(t, 1, calls.Load(),
		"a failed lookup must not be retried for every later path rule: that would turn one miss into one per rule")
	require.EqualValues(t, 1, fixture.final.queryCount.Load())
}

// TestWithoutACompleterTheExchangeIsUnchanged is the standalone-router contract: no injection, no
// walk, and the rules are evaluated against the metadata the caller gave - exactly as before the
// mechanism existed.
func TestWithoutACompleterTheExchangeIsUnchanged(t *testing.T) {
	fixture := newDeferralFixture(t, []option.DNSRule{
		previewTestRule(t, "blocked.example", "blocked"),
		previewProcessPathRule(t, "/usr/bin/curl", "curl-server"),
	})

	response, err := fixture.router.Exchange(context.Background(), deferralQuery("race.example.org.", mDNS.TypeA), adapter.DNSQueryOptions{})
	require.NoError(t, err)
	require.NotNil(t, response)
	require.EqualValues(t, 1, fixture.final.queryCount.Load())
	require.Zero(t, fixture.curlServer.queryCount.Load())
}

// TestThePreviewNeverCompletesAPath is the read-only half of the review finding.
func TestThePreviewNeverCompletesAPath(t *testing.T) {
	fixture := newDeferralFixture(t, []option.DNSRule{
		previewTestRule(t, "blocked.example", "blocked"),
		previewProcessPathRule(t, "/usr/bin/curl", "curl-server"),
	})
	var calls atomic.Int32
	fixture.router.SetProcessPathCompleter(deferralRecorder(&calls))

	preview := fixture.router.PreviewLookup(context.Background(), PreviewQuery{
		Name:  "race.example.org.",
		QType: mDNS.TypeA,
	})
	require.Zero(t, calls.Load(), "a preview must not run a /proc walk")
	require.False(t, preview.Matched)
	require.True(t, preview.Unknown, "the path rule may decide this query, and that is a fact the preview does not have")
	require.Equal(t, 1, preview.RuleIndex)
	require.Equal(t, "process_path", preview.DependsOn)
	require.Empty(t, preview.Server, "naming a server would be a guess that disagrees with the exchange")
	require.Equal(t, 2, preview.Evaluated)
}

// TestThePreviewDecidesWithTheProcessFactsTheCallerStates is the other side: a caller that knows the
// process gets a real answer, and it agrees with what the production exchange does.
func TestThePreviewDecidesWithTheProcessFactsTheCallerStates(t *testing.T) {
	fixture := newDeferralFixture(t, []option.DNSRule{
		previewTestRule(t, "blocked.example", "blocked"),
		previewProcessPathRule(t, "/usr/bin/curl", "curl-server"),
	})
	var calls atomic.Int32
	fixture.router.SetProcessPathCompleter(deferralRecorder(&calls))

	preview := fixture.router.PreviewLookup(context.Background(), PreviewQuery{
		Name:         "race.example.org.",
		QType:        mDNS.TypeA,
		ProcessPaths: []string{"/usr/bin/curl"},
	})
	require.True(t, preview.Matched)
	require.False(t, preview.Unknown)
	require.Equal(t, 1, preview.RuleIndex)
	require.Equal(t, "curl-server", preview.Server)
	require.Zero(t, calls.Load())

	// The same question through production: same rule, same server.
	_, err := fixture.router.Exchange(context.Background(), deferralQuery("race.example.org.", mDNS.TypeA), adapter.DNSQueryOptions{})
	require.NoError(t, err)
	require.EqualValues(t, 1, fixture.curlServer.queryCount.Load())
	require.Equal(t, "curl-server", preview.Server,
		"the preview's stated-facts answer must be the server the real exchange uses")
}

// TestThePreviewStillReportsADomainRuleExactly keeps the read-only change from making the common case
// (no process rule involved) vaguer than it was.
func TestThePreviewStillReportsADomainRuleExactly(t *testing.T) {
	fixture := newDeferralFixture(t, []option.DNSRule{
		previewTestRule(t, "example.org", "blocked"),
		previewProcessPathRule(t, "/usr/bin/curl", "curl-server"),
	})
	var calls atomic.Int32
	fixture.router.SetProcessPathCompleter(deferralRecorder(&calls))

	preview := fixture.router.PreviewLookup(context.Background(), PreviewQuery{
		Name:  "race.example.org.",
		QType: mDNS.TypeA,
	})
	require.True(t, preview.Matched)
	require.False(t, preview.Unknown)
	require.Equal(t, 0, preview.RuleIndex)
	require.Equal(t, "blocked", preview.Server)
	require.Zero(t, calls.Load(), "a rule that does not read a path must not trigger the completion")
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
