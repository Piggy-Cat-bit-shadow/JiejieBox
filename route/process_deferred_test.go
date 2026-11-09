package route

// P04 — the path half of the process lookup is completed per connection, on demand.
//
// A user/user_id rule is answered by the socket table alone. A process_name / process_path /
// package_name rule needs the /proc walk. The walk used to be paid for by EVERY local connection
// as soon as one such rule existed anywhere in the configuration; a flow that matched an earlier
// domain or address rule never needed it.
//
// The rule here: the walk is completed exactly when the rule walk reaches a rule that reads a path,
// and never for a flow that matched before one. The owner-only answer and the full answer are
// cached under the same tuple but are different answers - sharing one cache key would let the
// second lookup receive the first one's record.

import (
	"context"
	"net/netip"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/process"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	R "github.com/sagernet/sing-box/route/rule"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/json/badoption"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/contrab/freelru"
	"github.com/sagernet/sing/contrab/maphash"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// recordingProcessSearcher counts which lookup the router asked for.
type recordingProcessSearcher struct {
	ownerCalls atomic.Int32
	fullCalls  atomic.Int32

	owner *adapter.ConnectionOwner
	full  *adapter.ConnectionOwner
}

func (s *recordingProcessSearcher) FindProcessInfo(context.Context, string, netip.AddrPort, netip.AddrPort) (*adapter.ConnectionOwner, error) {
	s.fullCalls.Add(1)
	return s.full, nil
}

func (s *recordingProcessSearcher) FindProcessOwner(context.Context, string, netip.AddrPort, netip.AddrPort) (*adapter.ConnectionOwner, error) {
	s.ownerCalls.Add(1)
	return s.owner, nil
}

func (s *recordingProcessSearcher) Close() error { return nil }
func (s *recordingProcessSearcher) ResetCache()  {}

var _ process.Searcher = (*recordingProcessSearcher)(nil)
var _ process.OwnerSearcher = (*recordingProcessSearcher)(nil)

// deferredTestRule is a rule whose matching behaviour the test states directly.
type deferredTestRule struct {
	description string
	matches     func(*adapter.InboundContext) bool
	action      adapter.RuleAction
}

func (r *deferredTestRule) Type() string                                { return C.RuleTypeDefault }
func (r *deferredTestRule) String() string                              { return r.description }
func (r *deferredTestRule) Match(metadata *adapter.InboundContext) bool { return r.matches(metadata) }
func (r *deferredTestRule) Action() adapter.RuleAction                  { return r.action }
func (r *deferredTestRule) Start() error                                { return nil }
func (r *deferredTestRule) Close() error                                { return nil }

func newDeferredTestRouter(searcher process.Searcher, rules []adapter.Rule, ruleNeedsPaths []bool) *Router {
	router := &Router{
		logger:               log.NewNOPFactory().Logger(),
		processSearcher:      searcher,
		platformInterface:    &localSourcePlatform{address: netip.MustParseAddr("192.168.1.2")},
		rules:                rules,
		ruleNeedsPaths:       ruleNeedsPaths,
		deferredProcessPaths: slices.Contains(ruleNeedsPaths, true),
	}
	if searcher != nil {
		router.processCache = common.Must1(freelru.New[processCacheKey, processCacheEntry](
			256, maphash.NewHasher[processCacheKey]().Hash32, true))
		router.processCache.SetLifetime(time.Second)
	}
	return router
}

func deferredTestMetadata(domain string) *adapter.InboundContext {
	return &adapter.InboundContext{
		InboundType: C.TypeTun,
		Network:     N.NetworkTCP,
		Source:      M.SocksaddrFrom(netip.MustParseAddr("192.168.1.2"), 40000),
		Destination: M.Socksaddr{Fqdn: "blocked.example", Port: 443},
		Domain:      domain,
	}
}

// newDeferredTestRules builds the shape this work exists for: an ordinary domain rule first, then a
// rule that reads a process path.
func newDeferredTestRules() ([]adapter.Rule, []bool) {
	domainRule := &deferredTestRule{
		description: "domain",
		matches:     func(metadata *adapter.InboundContext) bool { return metadata.Domain == "blocked.example" },
		action:      &R.RuleActionRoute{Outbound: "proxy"},
	}
	pathRule := &deferredTestRule{
		description: "process path",
		matches: func(metadata *adapter.InboundContext) bool {
			return metadata.ProcessInfo != nil && slices.Contains(metadata.ProcessInfo.ProcessPaths, "/usr/bin/curl")
		},
		action: &R.RuleActionRoute{Outbound: "direct"},
	}
	return []adapter.Rule{domainRule, pathRule}, []bool{false, true}
}

// TestAFlowDecidedBeforeAPathRuleNeverWalksProc is the saving itself.
func TestAFlowDecidedBeforeAPathRuleNeverWalksProc(t *testing.T) {
	searcher := &recordingProcessSearcher{
		owner: &adapter.ConnectionOwner{UserId: 1000},
		full:  &adapter.ConnectionOwner{UserId: 1000, ProcessPaths: []string{"/usr/bin/curl"}},
	}
	rules, needsPaths := newDeferredTestRules()
	router := newDeferredTestRouter(searcher, rules, needsPaths)

	metadata := deferredTestMetadata("blocked.example")
	selected, index, _, _, err := router.matchRule(context.Background(), metadata, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, selected)
	require.Equal(t, 0, index)
	require.EqualValues(t, 1, searcher.ownerCalls.Load(), "the owner comes from the socket table")
	require.Zero(t, searcher.fullCalls.Load(),
		"a flow decided by the domain rule must never pay for the /proc walk")
}

// TestAFlowThatReachesAPathRuleCompletesIt is the other half: the rule still works.
func TestAFlowThatReachesAPathRuleCompletesIt(t *testing.T) {
	searcher := &recordingProcessSearcher{
		owner: &adapter.ConnectionOwner{UserId: 1000},
		full:  &adapter.ConnectionOwner{UserId: 1000, ProcessPaths: []string{"/usr/bin/curl"}},
	}
	rules, needsPaths := newDeferredTestRules()
	router := newDeferredTestRouter(searcher, rules, needsPaths)

	metadata := deferredTestMetadata("other.example")
	selected, index, _, _, err := router.matchRule(context.Background(), metadata, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, selected)
	require.Equal(t, 1, index, "the path rule is the one that decides this flow")
	require.EqualValues(t, 1, searcher.ownerCalls.Load())
	require.EqualValues(t, 1, searcher.fullCalls.Load(), "reaching the path rule completes it once")
	require.Equal(t, []string{"/usr/bin/curl"}, metadata.ProcessInfo.ProcessPaths)
}

// TestTheOwnerOnlyAnswerDoesNotSatisfyALaterPathLookup pins the cache key.
//
// Both lookups describe the same tuple. If they shared one cache entry, the second flow - the one
// that reaches the path rule - would receive the owner-only record and the rule would be evaluated
// against a record that was never asked for paths.
func TestTheOwnerOnlyAnswerDoesNotSatisfyALaterPathLookup(t *testing.T) {
	searcher := &recordingProcessSearcher{
		owner: &adapter.ConnectionOwner{UserId: 1000},
		full:  &adapter.ConnectionOwner{UserId: 1000, ProcessPaths: []string{"/usr/bin/curl"}},
	}
	rules, needsPaths := newDeferredTestRules()
	router := newDeferredTestRouter(searcher, rules, needsPaths)

	// The same connection tuple, first decided by the domain rule ...
	_, index, _, _, err := router.matchRule(context.Background(), deferredTestMetadata("blocked.example"), nil, nil)
	require.NoError(t, err)
	require.Equal(t, 0, index)

	// ... then, in the same cache window, a flow on the same tuple reaches the path rule.
	_, index, _, _, err = router.matchRule(context.Background(), deferredTestMetadata("other.example"), nil, nil)
	require.NoError(t, err)
	require.Equal(t, 1, index)
	require.EqualValues(t, 1, searcher.ownerCalls.Load(), "the owner-only answer is reused, not re-resolved")
	require.EqualValues(t, 1, searcher.fullCalls.Load(), "and it is not mistaken for the full answer")
}

// TestAFailedCompletionIsNotProofThatThePathRuleDoesNotApply pins the conservative result.
func TestAFailedCompletionIsNotProofThatThePathRuleDoesNotApply(t *testing.T) {
	searcher := &failingCompletionSearcher{owner: &adapter.ConnectionOwner{UserId: 1000}}
	rules, needsPaths := newDeferredTestRules()
	router := newDeferredTestRouter(searcher, rules, needsPaths)

	metadata := deferredTestMetadata("other.example")
	selected, _, _, _, err := router.matchRule(context.Background(), metadata, nil, nil)
	require.NoError(t, err)
	require.Nil(t, selected, "no rule matches when the path could not be read")
	require.Nil(t, metadata.ProcessInfo,
		"a failed completion must leave the metadata UNPROVEN so a bypass is refused")
	require.False(t, router.processMetadataIsProven(metadata, processTruth{}))
}

// failingCompletionSearcher answers the owner-only lookup and refuses the full one.
type failingCompletionSearcher struct {
	owner *adapter.ConnectionOwner
}

func (s *failingCompletionSearcher) FindProcessInfo(context.Context, string, netip.AddrPort, netip.AddrPort) (*adapter.ConnectionOwner, error) {
	return nil, errProcessLookupUnavailable
}

func (s *failingCompletionSearcher) FindProcessOwner(context.Context, string, netip.AddrPort, netip.AddrPort) (*adapter.ConnectionOwner, error) {
	return s.owner, nil
}

func (s *failingCompletionSearcher) Close() error { return nil }
func (s *failingCompletionSearcher) ResetCache()  {}

// ownerOnlySearcher returns an owner-only record from BOTH lookups without an error.
//
// This is the shape searcher_linux.go returns when the /proc walk is restricted: it logs the walk's
// error, keeps the uid it already resolved from the socket table, and returns nil error. The record
// is non-nil and contains no path - exactly the case "ProcessInfo != nil" used to accept as proof.
type ownerOnlySearcher struct {
	owner *adapter.ConnectionOwner
	full  *adapter.ConnectionOwner

	ownerCalls atomic.Int32
	fullCalls  atomic.Int32
}

func (s *ownerOnlySearcher) FindProcessInfo(context.Context, string, netip.AddrPort, netip.AddrPort) (*adapter.ConnectionOwner, error) {
	s.fullCalls.Add(1)
	return s.full, nil
}

func (s *ownerOnlySearcher) FindProcessOwner(context.Context, string, netip.AddrPort, netip.AddrPort) (*adapter.ConnectionOwner, error) {
	s.ownerCalls.Add(1)
	return s.owner, nil
}

func (s *ownerOnlySearcher) Close() error { return nil }
func (s *ownerOnlySearcher) ResetCache()  {}

// newBypassProcessRouter builds the PreMatch shape: a direct outbound that can bypass, a proxy that
// cannot, and the DNS fakes prepareMatchMetadata needs.
func newBypassProcessRouter(searcher process.Searcher, rules []adapter.Rule, ruleNeedsPaths []bool) *Router {
	direct := &plainDirectOutbound{}
	direct.canBypass.Store(1)
	proxy := &nonBypassableOutbound{}
	router := newDeferredTestRouter(searcher, rules, ruleNeedsPaths)
	router.ctx = context.Background()
	router.outbound = &stubOutboundManager{
		byTag: map[string]adapter.Outbound{"direct": direct, "proxy": proxy},
		def:   direct,
	}
	router.dns = &verdictDNSRouter{}
	router.dnsTransport = &verdictDNSTransport{}
	return router
}

func curlPathRule() *deferredTestRule {
	return &deferredTestRule{
		description: "process path",
		matches: func(metadata *adapter.InboundContext) bool {
			return metadata.ProcessInfo != nil && slices.Contains(metadata.ProcessInfo.ProcessPaths, "/usr/bin/curl")
		},
		action: &R.RuleActionRoute{Outbound: "proxy"},
	}
}

// portDirectRule decides the flow on a fact that does not disqualify the native fast path by itself.
//
// A rule matching a sniffed DOMAIN would make the flow ineligible for an unrelated reason
// (BypassRefusedSniffedDomain), so a test built on one could never show that the process truth is
// what refuses the bypass.
func portDirectRule() *deferredTestRule {
	return &deferredTestRule{
		description: "destination port",
		matches:     func(metadata *adapter.InboundContext) bool { return metadata.Destination.Port == 443 },
		action:      &R.RuleActionRoute{Outbound: "direct"},
	}
}

// TestAnOwnerOnlyAnswerRefusesTheNativeBypass is the review reproduction.
//
// The flow reaches a process-path rule first. The completion returns an owner-only record with no
// error, so the path rule does not match, and the next rule sends the flow to a plain direct
// outbound. A non-nil ProcessInfo used to be accepted as proof, which let the flow leave userspace
// through a rule that was never actually evaluated: "no match" meant "could not tell".
func TestAnOwnerOnlyAnswerRefusesTheNativeBypass(t *testing.T) {
	searcher := &ownerOnlySearcher{
		owner: &adapter.ConnectionOwner{UserId: 1000},
		full:  &adapter.ConnectionOwner{UserId: 1000},
	}
	router := newBypassProcessRouter(searcher, []adapter.Rule{curlPathRule(), portDirectRule()}, []bool{true, false})

	destination := M.ParseSocksaddr("93.184.216.34:443")
	metadata := fastBypassMetadata(N.NetworkTCP, destination)

	result := router.PreMatch(metadata, nil)
	require.NotEqual(t, adapter.PreMatchBypass, result.Action,
		"the path rule was evaluated against an owner-only answer, so whether it applies is UNKNOWN; "+
			"letting the flow bypass natively would decide it by missing metadata")
	require.EqualValues(t, 1, searcher.fullCalls.Load(),
		"the walk did reach the path rule, so the owner-only answer is what it evaluated")
	require.EqualValues(t, 1, searcher.ownerCalls.Load())
}

// TestAUIDRuleStillMatchesAfterAnOwnerOnlyCompletion is the other half of the same rule: refusing
// the bypass must not cost the uid that a rule was configured to match.
func TestAUIDRuleStillMatchesAfterAnOwnerOnlyCompletion(t *testing.T) {
	searcher := &ownerOnlySearcher{
		owner: &adapter.ConnectionOwner{UserId: 1000},
		full:  &adapter.ConnectionOwner{UserId: 1000},
	}
	var uidRuleMatched atomic.Bool
	uidRule := &deferredTestRule{
		description: "user id",
		matches: func(metadata *adapter.InboundContext) bool {
			matched := metadata.ProcessInfo != nil && metadata.ProcessInfo.UserId == 1000
			uidRuleMatched.Store(matched)
			return matched
		},
		action: &R.RuleActionRoute{Outbound: "direct"},
	}
	router := newBypassProcessRouter(searcher, []adapter.Rule{curlPathRule(), uidRule}, []bool{true, false})

	destination := M.ParseSocksaddr("93.184.216.34:443")
	metadata := fastBypassMetadata(N.NetworkTCP, destination)

	result := router.PreMatch(metadata, nil)
	require.True(t, uidRuleMatched.Load(),
		"the uid rule matches on the uid the owner-only answer DID prove: the known owner must keep serving UID rules")
	require.NotEqual(t, adapter.PreMatchBypass, result.Action,
		"the bypass is still refused: the path was asked for and could not be obtained")
}

// TestACompleteAnswerStillAllowsTheBypass proves the refusal above is about the missing field, not
// about the configuration: the same rules with a record that contains the path still bypass.
func TestACompleteAnswerStillAllowsTheBypass(t *testing.T) {
	searcher := &ownerOnlySearcher{
		owner: &adapter.ConnectionOwner{UserId: 1000},
		full:  &adapter.ConnectionOwner{UserId: 1000, ProcessPaths: []string{"/usr/bin/wget"}},
	}
	router := newBypassProcessRouter(searcher, []adapter.Rule{curlPathRule(), portDirectRule()}, []bool{true, false})

	destination := M.ParseSocksaddr("93.184.216.34:443")
	metadata := fastBypassMetadata(N.NetworkTCP, destination)

	result := router.PreMatch(metadata, nil)
	require.Equal(t, adapter.PreMatchBypass, result.Action,
		"the path was obtained and the rule does not apply to it, so the bypass IS provable")
}

// TestTheRouterOnlyDefersPlainPathRules pins the mode decision itself.
//
// A plain process-path rule in the ROUTING list can be completed on demand. A rule-set reference
// cannot: the document is remote and mutable, so it may carry process conditions the configuration
// never mentions. The explicit find_process option and a DNS path rule keep the up-front walk too.
func TestTheRouterOnlyDefersPlainPathRules(t *testing.T) {
	t.Parallel()

	newRouter := func(rules []option.Rule, findProcess bool) *Router {
		return NewRouter(context.Background(), log.NewNOPFactory(),
			option.RouteOptions{Rules: rules, FindProcess: findProcess}, option.DNSOptions{})
	}

	pathRule := defaultRule(option.RawDefaultRule{ProcessPath: badoption.Listable[string]{"/usr/bin/curl"}})
	plainRouter := newRouter([]option.Rule{pathRule}, false)
	require.False(t, plainRouter.needProcessPaths, "a plain path rule is completed when it is reached")
	require.True(t, plainRouter.deferredProcessPaths)

	forcedRouter := newRouter([]option.Rule{pathRule}, true)
	require.True(t, forcedRouter.needProcessPaths, "find_process asks for the process of every flow")
	require.False(t, forcedRouter.deferredProcessPaths)

	ruleSetRule := defaultRule(option.RawDefaultRule{RuleSet: badoption.Listable[string]{"ads"}})
	ruleSetRouter := newRouter([]option.Rule{ruleSetRule}, false)
	require.True(t, ruleSetRouter.needProcessPaths,
		"a rule-set document may carry process conditions, so the walk cannot be deferred past it")
	require.False(t, ruleSetRouter.deferredProcessPaths)

	// A DNS path rule no longer forces the up-front walk for connections: its own rule walk completes
	// it inside the DNS router (see completeDNSProcessPaths and TestTheRouterInstallsTheDNSCompleter).
	dnsOnlyRouter := NewRouter(context.Background(), log.NewNOPFactory(),
		option.RouteOptions{Rules: []option.Rule{pathRule}},
		option.DNSOptions{RawDNSOptions: option.RawDNSOptions{Rules: []option.DNSRule{defaultDNSRule(option.RawDefaultDNSRule{
			ProcessPath: badoption.Listable[string]{"/usr/bin/curl"},
		})}}})
	require.False(t, dnsOnlyRouter.needProcessPaths,
		"a DNS path rule must not charge every CONNECTION for a fact only a DNS QUERY needs")
	require.True(t, dnsOnlyRouter.deferredProcessPaths)
}

// recordingDNSRouter is a DNS router that only records the completer route installs.
type recordingDNSRouter struct {
	adapter.DNSRouter
	completer func(ctx context.Context, metadata *adapter.InboundContext)
}

func (r *recordingDNSRouter) SetProcessPathCompleter(complete func(ctx context.Context, metadata *adapter.InboundContext)) {
	r.completer = complete
}

// TestTheRouterInstallsTheDNSCompleter pins the wiring the deferral depends on: the DNS router gets
// a completer, and that completer is the route layer's own lookup (so the cache and the
// owner-only/full split stay in one place).
func TestTheRouterInstallsTheDNSCompleter(t *testing.T) {
	dnsRouter := &recordingDNSRouter{}
	ctx := service.ContextWith[adapter.DNSRouter](context.Background(), dnsRouter)

	router := NewRouter(ctx, log.NewNOPFactory(), option.RouteOptions{}, option.DNSOptions{})
	require.NotNil(t, dnsRouter.completer, "the DNS rule walk can only defer to a completer that exists")

	// The installed completer is the route layer's: with no searcher it is a no-op, and it must not
	// invent a process record.
	metadata := &adapter.InboundContext{}
	dnsRouter.completer(context.Background(), metadata)
	require.Nil(t, metadata.ProcessInfo)
	_ = router
}
