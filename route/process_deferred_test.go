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
	require.False(t, router.processMetadataIsProven(metadata))
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

	dnsRouter := NewRouter(context.Background(), log.NewNOPFactory(),
		option.RouteOptions{Rules: []option.Rule{pathRule}},
		option.DNSOptions{RawDNSOptions: option.RawDNSOptions{Rules: []option.DNSRule{defaultDNSRule(option.RawDefaultDNSRule{
			ProcessPath: badoption.Listable[string]{"/usr/bin/curl"},
		})}}})
	require.True(t, dnsRouter.needProcessPaths, "a DNS rule is evaluated before the connection's rule walk")
	require.False(t, dnsRouter.deferredProcessPaths)
}
