package dns

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	R "github.com/sagernet/sing-box/route/rule"
	"github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/service"

	mDNS "github.com/miekg/dns"
	"github.com/stretchr/testify/require"
)

// What this file measures, and why it is not "the rcode is NXDOMAIN".
//
// The claim the adblock action makes is a claim about WORK: the first query for an advertising
// name must not reach a transport, and neither must the fortieth, in any record type, from any
// number of concurrent callers. A response that merely looks like the right answer can be
// produced after the query has already been sent, so every test here counts transport exchanges
// and asserts the count is exactly zero rather than inspecting the answer alone.
//
// The transports below are deliberately hostile. `panicWhenExchanged` turns any accidental
// upstream call into a loud failure instead of a silently plausible answer, which is what makes
// "the query was never sent" observable rather than inferred from timing.

const adblockTestName = "ads.example."

// adblockTestRule is the configuration this feature exists for: a rule that names advertising
// domains, with the adblock action attached.
//
// The suffixes are written the way a user writes them (no trailing dot). They are NOT FQDNs:
// `domain_suffix` is matched against the metadata domain and a trailing dot is not stripped, so
// ".ads.example" is the FQDN spelling of a different condition than "ads.example". Writing the
// user's spelling here is what keeps this test measuring the product's matcher.
func adblockTestRule(t *testing.T, domains ...string) option.DNSRule {
	t.Helper()
	return option.DNSRule{
		Type: C.RuleTypeDefault,
		DefaultOptions: option.DefaultDNSRule{
			RawDefaultDNSRule: option.RawDefaultDNSRule{DomainSuffix: domains},
			DNSRuleAction: option.DNSRuleAction{
				Action: C.RuleActionTypeAdblock,
			},
		},
	}
}

// adblockTestMessage is one query for the given name and record type.
func adblockTestMessage(name string, qType uint16, id uint16) (*mDNS.Msg, *adapter.InboundContext) {
	message := &mDNS.Msg{
		MsgHdr:   mDNS.MsgHdr{Id: id, RecursionDesired: true},
		Question: []mDNS.Question{{Name: name, Qtype: qType, Qclass: mDNS.ClassINET}},
	}
	metadata := &adapter.InboundContext{Domain: FqdnToDomain(name), QueryType: qType}
	return message, metadata
}

// adblockTestExchange runs one query through the rule walk exactly as Router.Exchange does.
func adblockTestExchange(router *Router, rules []adapter.DNSRule, name string, qType uint16, id uint16) exchangeWithRulesResult {
	message, metadata := adblockTestMessage(name, qType, id)
	requestCtx := adapter.WithContext(context.Background(), metadata)
	return router.exchangeWithRules(requestCtx, rules, message, adapter.DNSQueryOptions{}, false)
}

// requireLocalNXDomain asserts the full response shape a negative-caching client needs.
//
// The SOA assertions are the ones that matter for the feature's second half: without an SOA
// there is no negative TTL, and a resolver following RFC 2308 has nothing to cache - the answer
// would be correct and still leave the client asking again.
func requireLocalNXDomain(t *testing.T, result exchangeWithRulesResult, name string, qType uint16, id uint16) {
	t.Helper()
	require.NoError(t, result.err)
	require.NotNil(t, result.response)
	response := result.response
	require.Equal(t, mDNS.RcodeNameError, response.Rcode, "adblock must answer NXDOMAIN")
	require.Empty(t, response.Answer, "an NXDOMAIN with an answer is not a statement about the name")
	require.True(t, response.Response, "QR must be set")
	require.True(t, response.Authoritative, "the answer comes from this resolver's own policy")
	require.True(t, response.RecursionAvailable)
	require.False(t, response.Truncated)

	// The question and the ID are the CURRENT request's. A response built for a different
	// question is discarded by the client, and the retry would put the name back on the wire.
	require.Equal(t, id, response.Id, "the response must carry the current request ID")
	require.Len(t, response.Question, 1)
	require.Equal(t, name, response.Question[0].Name)
	require.Equal(t, qType, response.Question[0].Qtype)
	require.Equal(t, uint16(mDNS.ClassINET), response.Question[0].Qclass)

	timeToLive, hasSOA := extractNegativeTTL(response)
	require.True(t, hasSOA, "NXDOMAIN without an SOA carries no negative TTL and cannot be cached")
	require.NotZero(t, timeToLive, "a zero negative TTL makes the answer uncacheable by design")
	require.Equal(t, C.DefaultDNSNegativeTTL, timeToLive)

	var soa *mDNS.SOA
	for _, record := range response.Ns {
		if candidate, isSOA := record.(*mDNS.SOA); isSOA {
			soa = candidate
			break
		}
	}
	require.NotNil(t, soa)
	require.Equal(t, name, soa.Hdr.Name, "the SOA owner must be the name being denied")
	require.Equal(t, C.DefaultDNSNegativeTTL, soa.Hdr.Ttl)
	require.Equal(t, C.DefaultDNSNegativeTTL, soa.Minttl,
		"RFC 2308 uses min(SOA TTL, MINIMUM); both carry the same value so no client reads a longer life")
	require.Equal(t, uint16(mDNS.ClassINET), soa.Hdr.Class)
}

// TestAdblockAnswersEveryRecordTypeLocally is the basic semantic contract.
func TestAdblockAnswersEveryRecordTypeLocally(t *testing.T) {
	t.Parallel()

	transport := &fakeDNSTransport{tag: "final", address: netip.MustParseAddr("192.0.2.1")}
	router := raceTestRouter(t, transport)
	rules := raceTestRules(t, []option.DNSRule{adblockTestRule(t, "ads.example")})

	for index, qType := range []uint16{mDNS.TypeA, mDNS.TypeAAAA, mDNS.TypeHTTPS, mDNS.TypeSVCB} {
		result := adblockTestExchange(router, rules, adblockTestName, qType, uint16(0x1000+index))
		requireLocalNXDomain(t, result, adblockTestName, qType, uint16(0x1000+index))
	}

	require.Zero(t, transport.queryCount.Load(),
		"no record type may reach the transport: a name-wide verdict must not cost one query per QTYPE")
}

// TestAdblockFirstQueryNeverReachesTheTransport is the acceptance measurement.
//
// It is not "the first query is cached then the rest are local". The count is asserted to be zero
// after the FIRST query and to stay zero, which is the difference between an action that answers
// locally and one that answers from something it had to fetch first.
func TestAdblockFirstQueryNeverReachesTheTransport(t *testing.T) {
	t.Parallel()

	transport := &fakeDNSTransport{tag: "final", address: netip.MustParseAddr("192.0.2.1")}
	router := raceTestRouter(t, transport)
	rules := raceTestRules(t, []option.DNSRule{adblockTestRule(t, "ads.example")})

	result := adblockTestExchange(router, rules, adblockTestName, mDNS.TypeA, 1)
	requireLocalNXDomain(t, result, adblockTestName, mDNS.TypeA, 1)
	require.Zero(t, transport.queryCount.Load(), "the FIRST adblock query must already cost no upstream exchange")

	for _, qType := range []uint16{mDNS.TypeAAAA, mDNS.TypeHTTPS, mDNS.TypeSVCB, mDNS.TypeA} {
		adblockTestExchange(router, rules, adblockTestName, qType, 2)
	}
	require.Zero(t, transport.queryCount.Load())
}

// TestAdblockNeverReachesASabotagedTransport is the "really stopped early" proof.
//
// The transport answers every exchange with a fatal error rather than an address, so a query that
// reaches it cannot be mistaken for a working one. If adblock returns NXDOMAIN while this
// transport is configured, it did so without asking it: there is no other way to obtain the
// answer that does not first trip this.
func TestAdblockNeverReachesASabotagedTransport(t *testing.T) {
	t.Parallel()

	sabotage := fmt.Errorf("adblock query reached the upstream transport")
	transport := &fakeDNSTransport{tag: "final", exchangeErr: sabotage}
	router := raceTestRouter(t, transport)
	rules := raceTestRules(t, []option.DNSRule{adblockTestRule(t, "ads.example")})

	for index, qType := range []uint16{mDNS.TypeA, mDNS.TypeAAAA, mDNS.TypeHTTPS, mDNS.TypeSVCB} {
		result := adblockTestExchange(router, rules, adblockTestName, qType, uint16(index))
		requireLocalNXDomain(t, result, adblockTestName, qType, uint16(index))
	}
	require.Zero(t, transport.queryCount.Load(), "the sabotaged transport must never be entered")
}

// TestAdblockSurvivesConcurrentColdStart covers the speculative/race window.
//
// Every caller arrives with no cache, no memoized verdict and no singleflight entry, which is the
// state in which an implementation that launched work before deciding could show up as a handful
// of exchanges. The mix deliberately spans four record types so a QTYPE-keyed shortcut cannot
// hide: the name-wide claim has to hold before anything has been learned about the name.
func TestAdblockSurvivesConcurrentColdStart(t *testing.T) {
	t.Parallel()

	const callers = 128
	transport := &fakeDNSTransport{tag: "final", address: netip.MustParseAddr("192.0.2.1")}
	router := raceTestRouter(t, transport)
	rules := raceTestRules(t, []option.DNSRule{adblockTestRule(t, "ads.example", "tracker.example")})

	queryTypes := []uint16{mDNS.TypeA, mDNS.TypeAAAA, mDNS.TypeHTTPS, mDNS.TypeSVCB}
	names := []string{adblockTestName, "tracker.example."}

	var (
		waitGroup sync.WaitGroup
		failures  = make(chan string, callers)
	)
	for index := 0; index < callers; index++ {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			name := names[index%len(names)]
			qType := queryTypes[index%len(queryTypes)]
			result := adblockTestExchange(router, rules, name, qType, uint16(index))
			response := result.response
			if result.err != nil || response == nil || response.Rcode != mDNS.RcodeNameError {
				failures <- fmt.Sprintf("caller %d (%s %s): err=%v response=%v", index, name,
					mDNS.TypeToString[qType], result.err, response)
				return
			}
			if len(response.Question) != 1 || response.Question[0].Name != name || response.Question[0].Qtype != qType {
				failures <- fmt.Sprintf("caller %d: response question is %v, want %s %s", index,
					response.Question, name, mDNS.TypeToString[qType])
			}
			if _, hasSOA := extractNegativeTTL(response); !hasSOA {
				failures <- fmt.Sprintf("caller %d: response carries no SOA", index)
			}
		}(index)
	}
	waitGroup.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
	require.Zero(t, transport.queryCount.Load(),
		"concurrent cold-start queries must not launch a single upstream exchange")
}

// TestAdblockPolicyBeatsAnExistingPositiveCache is the cache-ordering hazard.
//
// The positive answer is obtained FIRST, through the same client and transport the adblock rule
// will later sit in front of, so it is a real exact-cache entry rather than a simulation of one.
// The policy is then attached, and the same question is asked again. A design that expressed
// "adblock" as a negative cache entry would lose this race to the entry that is already there.
func TestAdblockPolicyBeatsAnExistingPositiveCache(t *testing.T) {
	t.Parallel()

	const name = "cached-ads.example."
	transport := &fakeDNSTransport{tag: "final", address: netip.MustParseAddr("192.0.2.1")}
	router := &Router{
		ctx:    context.Background(),
		logger: log.NewNOPFactory().Logger(),
		transport: &fakeDNSTransportManager{
			transports:       map[string]adapter.DNSTransport{"final": transport},
			defaultTransport: transport,
		},
		client: NewClient(ClientOptions{
			Context: context.Background(),
			Logger:  log.NewNOPFactory().Logger(),
		}),
	}
	router.client.Start()

	// 1. The name resolves normally, and the answer is a genuine cache entry.
	firstResult := adblockTestExchange(router, nil, name, mDNS.TypeA, 1)
	require.NoError(t, firstResult.err)
	require.NotNil(t, firstResult.response)
	require.Equal(t, mDNS.RcodeSuccess, firstResult.response.Rcode)
	require.NotEmpty(t, firstResult.response.Answer)
	require.Equal(t, int32(1), transport.queryCount.Load())

	// 2. The policy is attached after the fact - the rule-set was updated, or the feature was
	//    switched on. Nothing is cleared, which is the point: an operator does not clear a cache
	//    when adding a rule.
	rules := raceTestRules(t, []option.DNSRule{adblockTestRule(t, "cached-ads.example")})

	// 3. The same question again.
	secondResult := adblockTestExchange(router, rules, name, mDNS.TypeA, 2)
	requireLocalNXDomain(t, secondResult, name, mDNS.TypeA, 2)
	require.Equal(t, int32(1), transport.queryCount.Load(),
		"the adblock verdict must win over the positive entry, and must not cost a new lookup")

	// The AAAA record type was never asked about, so this is not "the cache expired".
	thirdResult := adblockTestExchange(router, rules, name, mDNS.TypeAAAA, 3)
	requireLocalNXDomain(t, thirdResult, name, mDNS.TypeAAAA, 3)
	require.Equal(t, int32(1), transport.queryCount.Load())
}

// TestAdblockPolicyRemovalIsImmediate is the reverse hazard.
//
// A policy verdict that leaked into a client cache would keep answering NXDOMAIN after the rule
// was gone, and nothing an operator could do short of restarting would clear it. The rule-set
// below is swapped for one that no longer matches, which is what a rule-set update does, and the
// very next query must go upstream again.
//
// The name is a subdomain of the blocked suffix, and the replacement set blocks a different
// SUFFIX rather than dropping the entry: an update that merely removes one line must move the
// verdict just as an update that changes the file does.
func TestAdblockPolicyRemovalIsImmediate(t *testing.T) {
	t.Parallel()

	const name = "tracker.doubleclick.example."
	transport := &fakeDNSTransport{tag: "final", address: netip.MustParseAddr("192.0.2.1")}
	router := raceTestRouter(t, transport)
	ruleSets := newAdblockRuleSetSource(t, router)
	rules := raceTestRulesOn(t, ruleSets.ctx, []option.DNSRule{adblockRuleSetRule("ads")})

	// 1. Blocked: the rule-set contains the name.
	result := adblockTestExchange(router, rules, name, mDNS.TypeA, 1)
	requireLocalNXDomain(t, result, name, mDNS.TypeA, 1)
	require.Zero(t, transport.queryCount.Load())
	// 2. The rule-set is updated so the name is no longer advertising. This is the two-step update
	//    a reload performs: the new set is published, and the rules that reference the tag resolve
	//    it again.
	ruleSets.update(t, rules, "ads", "somewhere-else.example")

	// 3. The service must act on the new rules immediately, from its own state alone.
	thirdResult := adblockTestExchange(router, rules, name, mDNS.TypeA, 2)
	require.NoError(t, thirdResult.err)
	require.NotNil(t, thirdResult.response)
	require.Equal(t, mDNS.RcodeSuccess, thirdResult.response.Rcode,
		"a removed adblock rule must not leave a stale local verdict behind")
	require.NotEmpty(t, thirdResult.response.Answer)
	require.Equal(t, int32(1), transport.queryCount.Load(), "the name must be looked up again once it is not advertising")
}

// TestAdblockLeavesOtherNamesOnTheirOriginalPath is the "no new hot-path cost" contract.
//
// The counters are per-transport, so this also checks that adblock did not quietly redirect
// unmatched names somewhere else: the routing rule still decides, and the name still lands on the
// transport it named.
func TestAdblockLeavesOtherNamesOnTheirOriginalPath(t *testing.T) {
	t.Parallel()

	const ordinaryName = "www.example."
	regular := &fakeDNSTransport{tag: "final", address: netip.MustParseAddr("192.0.2.1")}
	blocked := &fakeDNSTransport{tag: "ads-server", address: netip.MustParseAddr("192.0.2.2")}
	router := &Router{
		ctx:    context.Background(),
		logger: log.NewNOPFactory().Logger(),
		transport: &fakeDNSTransportManager{
			transports:       map[string]adapter.DNSTransport{"final": regular, "ads-server": blocked},
			defaultTransport: regular,
		},
		client: NewClient(ClientOptions{
			Context: context.Background(),
			Logger:  log.NewNOPFactory().Logger(),
		}),
	}

	rules := raceTestRules(t, []option.DNSRule{
		adblockTestRule(t, "ads.example"),
		{
			Type: C.RuleTypeDefault,
			DefaultOptions: option.DefaultDNSRule{
				RawDefaultDNSRule: option.RawDefaultDNSRule{DomainSuffix: []string{"www.example"}},
				DNSRuleAction: option.DNSRuleAction{
					Action:       C.RuleActionTypeRoute,
					RouteOptions: option.DNSRouteActionOptions{Server: "ads-server"},
				},
			},
		},
	})

	blockedResult := adblockTestExchange(router, rules, adblockTestName, mDNS.TypeA, 1)
	requireLocalNXDomain(t, blockedResult, adblockTestName, mDNS.TypeA, 1)
	require.Zero(t, blocked.queryCount.Load())
	require.Zero(t, regular.queryCount.Load())

	ordinaryResult := adblockTestExchange(router, rules, ordinaryName, mDNS.TypeA, 2)
	require.NoError(t, ordinaryResult.err)
	require.NotNil(t, ordinaryResult.response)
	require.Equal(t, mDNS.RcodeSuccess, ordinaryResult.response.Rcode)
	require.Equal(t, int32(1), blocked.queryCount.Load(),
		"an unmatched name must still follow its own rule, unchanged")
	require.Zero(t, regular.queryCount.Load())
}

// TestAdblockAnswersTheAsyncExchangeFromTheCallerGoroutine covers the second entry point.
//
// ExchangeAsync takes a different route through the router, and a terminal local action must not
// hand it a suspension: the callback is expected before the call returns, with no goroutine and
// no transport involved. `answered` is set inside the callback and read after the call, so the
// assertion fails if the answer arrives later rather than sooner.
func TestAdblockAnswersTheAsyncExchangeFromTheCallerGoroutine(t *testing.T) {
	t.Parallel()

	transport := &fakeDNSTransport{tag: "final", address: netip.MustParseAddr("192.0.2.1")}
	router := raceTestRouter(t, transport)
	rules := raceTestRules(t, []option.DNSRule{adblockTestRule(t, "ads.example")})
	message, metadata := adblockTestMessage(adblockTestName, mDNS.TypeA, 7)
	requestCtx := adapter.WithContext(context.Background(), metadata)

	var (
		result   exchangeWithRulesResult
		answered bool
	)
	router.exchangeWithRulesAsync(requestCtx, rules, message, adapter.DNSQueryOptions{}, false,
		func(callbackResult exchangeWithRulesResult) {
			result = callbackResult
			answered = true
		})

	require.True(t, answered, "a local verdict must be delivered without suspending the walk")
	requireLocalNXDomain(t, result, adblockTestName, mDNS.TypeA, 7)
	require.Zero(t, transport.queryCount.Load())
}

// TestAdblockThroughACompiledRuleSet runs the configuration users actually write.
//
// `domain_suffix` in a rule is the direct form; the shipped ad rules are rule-sets, and the
// matcher that answers them is a different code path. The rule-set is a real LocalRuleSet built
// from inline source, not a stub, so the match that selects adblock is the compiled one.
func TestAdblockThroughACompiledRuleSet(t *testing.T) {
	t.Parallel()

	transport := &fakeDNSTransport{tag: "final", address: netip.MustParseAddr("192.0.2.1")}
	router := raceTestRouter(t, transport)
	ruleSets := newAdblockRuleSetSource(t, router)
	rules := raceTestRulesOn(t, ruleSets.ctx, []option.DNSRule{adblockRuleSetRule("anti-ad", "AWAvenue-Ads-Rule")})

	for index, qType := range []uint16{mDNS.TypeA, mDNS.TypeAAAA, mDNS.TypeHTTPS, mDNS.TypeSVCB} {
		result := adblockTestExchange(router, rules, "doubleclick.example.", qType, uint16(index))
		requireLocalNXDomain(t, result, "doubleclick.example.", qType, uint16(index))
	}
	require.Zero(t, transport.queryCount.Load())

	// A name outside both sets must be unaffected: a rule-set match is not a blanket verdict.
	ordinaryResult := adblockTestExchange(router, rules, "www.iana.example.", mDNS.TypeA, 9)
	require.NoError(t, ordinaryResult.err)
	require.NotNil(t, ordinaryResult.response)
	require.Equal(t, mDNS.RcodeSuccess, ordinaryResult.response.Rcode)
	require.Equal(t, int32(1), transport.queryCount.Load())
}

// TestAdblockResponsesAreNotSharedBetweenQueries pins that each answer is built per request.
//
// A cached or shared dns.Msg would be a data race under the concurrency test above and would
// answer an AAAA question with an A question. This asserts the two responses are distinct objects
// carrying their own question, which is the observable half of that property.
func TestAdblockResponsesAreNotSharedBetweenQueries(t *testing.T) {
	t.Parallel()

	transport := &fakeDNSTransport{tag: "final", address: netip.MustParseAddr("192.0.2.1")}
	router := raceTestRouter(t, transport)
	rules := raceTestRules(t, []option.DNSRule{adblockTestRule(t, "ads.example")})

	firstResult := adblockTestExchange(router, rules, adblockTestName, mDNS.TypeA, 11)
	secondResult := adblockTestExchange(router, rules, adblockTestName, mDNS.TypeAAAA, 12)
	requireLocalNXDomain(t, firstResult, adblockTestName, mDNS.TypeA, 11)
	requireLocalNXDomain(t, secondResult, adblockTestName, mDNS.TypeAAAA, 12)

	require.NotSame(t, firstResult.response, secondResult.response)
	require.NotSame(t, firstResult.response.Ns[0], secondResult.response.Ns[0])
	require.Equal(t, mDNS.TypeA, firstResult.response.Question[0].Qtype)
	require.Equal(t, mDNS.TypeAAAA, secondResult.response.Question[0].Qtype)
}

// ---------------------------------------------------------------------------
// rule-set plumbing
// ---------------------------------------------------------------------------

// adblockRuleSetSource supplies rule-sets to rule construction, and can update one afterwards.
//
// It exists because a rule-set reference is not just a matcher: the rule holds the set it
// resolved, so a test of "the rule-set was updated" has to go through the same two calls the
// router makes - resolve the tag, then start the rule - rather than reaching into the rule.
type adblockRuleSetSource struct {
	// The router interface is embedded rather than implemented: rule building only ever calls
	// RuleSet on it, and a full router in this package would be a second product to maintain.
	adapter.Router
	ctx      context.Context
	access   sync.Mutex
	ruleSets map[string]adapter.RuleSet
}

func (s *adblockRuleSetSource) RuleSet(tag string) (adapter.RuleSet, bool) {
	s.access.Lock()
	defer s.access.Unlock()
	ruleSet, loaded := s.ruleSets[tag]
	return ruleSet, loaded
}

// update replaces the rule-set behind a tag and re-resolves it on the rules that reference it.
//
// The two steps are separate on purpose. Publishing the new set in the source is what a rule-set
// reload does; re-starting the rule is what the router does with the rules it already holds. Both
// are needed for the update to be observable, and skipping either would make this test pass for
// the wrong reason.
func (s *adblockRuleSetSource) update(t *testing.T, rules []adapter.DNSRule, tag string, domains ...string) {
	t.Helper()
	s.access.Lock()
	s.ruleSets[tag] = buildAdblockRuleSet(t, s.ctx, tag, domains...)
	s.access.Unlock()
	for _, rule := range rules {
		require.NoError(t, rule.Start())
	}
}

func newAdblockRuleSetSource(t *testing.T, router *Router) *adblockRuleSetSource {
	t.Helper()
	source := &adblockRuleSetSource{ruleSets: make(map[string]adapter.RuleSet)}
	source.ctx = service.ContextWith[adapter.Router](context.Background(), source)
	// The router reads its rules from this context, so the rules a test builds and the rules the
	// router walks come from one place.
	router.ctx = source.ctx
	for _, entry := range []struct {
		tag     string
		domains []string
	}{
		{"ads", []string{"doubleclick.example", "ads.example"}},
		{"anti-ad", []string{"doubleclick.example"}},
		{"AWAvenue-Ads-Rule", []string{"doubleclick.example"}},
	} {
		source.ruleSets[entry.tag] = buildAdblockRuleSet(t, source.ctx, entry.tag, entry.domains...)
	}
	return source
}

// buildAdblockRuleSet builds a real inline rule-set, parsed from the same JSON a user would write.
func buildAdblockRuleSet(t *testing.T, ctx context.Context, tag string, domains ...string) adapter.RuleSet {
	t.Helper()
	require.NotEmpty(t, domains)
	quoted := make([]string, 0, len(domains))
	for _, domain := range domains {
		quoted = append(quoted, `"`+domain+`"`)
	}
	source := `{"version":3,"rules":[{"domain_suffix":[` + strings.Join(quoted, ",") + `]}]}`
	var plainOptions option.PlainRuleSetCompat
	err := json.UnmarshalContext(ctx, []byte(source), &plainOptions)
	require.NoError(t, err)
	ruleSet, err := R.NewLocalRuleSet(ctx, log.NewNOPFactory().Logger(), tag, option.RuleSet{
		Type:          C.RuleSetTypeInline,
		InlineOptions: plainOptions.Options,
	})
	require.NoError(t, err)
	return ruleSet
}

// adblockRuleSetRule is the documented user-facing form: rule_set + action adblock.
func adblockRuleSetRule(tags ...string) option.DNSRule {
	return option.DNSRule{
		Type: C.RuleTypeDefault,
		DefaultOptions: option.DefaultDNSRule{
			RawDefaultDNSRule: option.RawDefaultDNSRule{RuleSet: tags},
			DNSRuleAction: option.DNSRuleAction{
				Action: C.RuleActionTypeAdblock,
			},
		},
	}
}
