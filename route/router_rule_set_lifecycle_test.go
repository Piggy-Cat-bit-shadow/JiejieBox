package route

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/srs"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/json/badoption"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// Does the ROUTER hand its rule-set resources to the Scope, and do they exit when it closes?
//
// # The gap this closes
//
// The pieces were each tested and the WIRING was not: `rule_set_ref_cleanup_test.go` covers the reference
// counter on a constructed object, and `rule_set_updater_lifecycle_test.go` covers the updater goroutine
// given an updater. Neither can see the lines that matter for a reload - `scope.Add(ruleSet.Close)` and
// `scope.Add(r.ruleSetUpdater.Close)` in `Router.Start` - and deleting either of them leaves every other test
// in the tree green while an old Box's rule-sets and its refresh goroutine survive the reload.
//
// That is the shape this audit exists for: a correct object that nobody owns. So these drive the real
// `Router` through the real four start stages with a real `adapter.Scope`, and assert both ends - the rules
// are live while the Box runs, and the objects are dead once the Scope drains.
//
// # The fixture
//
// A real `*RemoteRuleSet` built by `Router.Initialize` from a rule_set option, fetched through a stub
// transport (so the fetch is a counted wire fact rather than a network dependency), plus an inline local
// rule-set so both implementations are covered.

// ruleSetTransport serves the payload for every request and counts what it was asked.
//
// `status` is what lets the same fixture drive the FAILURE path: a rule-set whose server answers 500 must
// fail Start, and the Box's rollback must then close everything Initialize had already handed to the Scope.
type ruleSetTransport struct {
	hits    *atomic.Int32
	payload []byte
	status  int
}

func (t *ruleSetTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	t.hits.Add(1)
	status := t.status
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(t.payload)),
	}, nil
}

func (t *ruleSetTransport) CloseIdleConnections() {}

func (t *ruleSetTransport) Reset() {}

// ruleSetHTTPClientManager is the one seam: `RemoteRuleSet.StartContext` asks for the default transport.
type ruleSetHTTPClientManager struct {
	adapter.HTTPClientManager
	transport adapter.HTTPTransport
}

func (m *ruleSetHTTPClientManager) DefaultTransport() adapter.HTTPTransport { return m.transport }

// ruleSetNetworkManager stands in for the network manager, which `Router.Start` only notifies of the
// rule-set metadata at this stage.
type ruleSetNetworkManager struct {
	adapter.NetworkManager
}

func (m *ruleSetNetworkManager) Initialize([]adapter.RuleSet) {}

// newRuleSetRouterFixture builds a router whose remote rule-set is served by the stub transport.
//
// `rules` is what a caller adds to reference a rule-set, which is what takes the reference that keeps its
// rules alive through the `Cleanup` pass. An empty list is the "configured but unused" shape.
func newRuleSetRouterFixture(t *testing.T, rules []option.Rule) (*Router, *atomic.Int32) {
	t.Helper()

	var payload bytes.Buffer
	require.NoError(t, srs.Write(&payload, option.PlainRuleSet{
		Rules: []option.HeadlessRule{{
			Type: C.RuleTypeDefault,
			DefaultOptions: option.DefaultHeadlessRule{
				Domain: badoption.Listable[string]{"remote.example"},
				IPCIDR: badoption.Listable[*badoption.Prefixable]{
					common.Ptr(badoption.Prefixable(netip.MustParsePrefix("10.0.0.0/8"))),
				},
			},
		}},
	}, C.RuleSetVersionCurrent))

	hits := &atomic.Int32{}
	ctx := service.ContextWithDefaultRegistry(context.Background())
	service.MustRegister[adapter.HTTPClientManager](ctx,
		&ruleSetHTTPClientManager{transport: &ruleSetTransport{hits: hits, payload: payload.Bytes()}})
	service.MustRegister[adapter.NetworkManager](ctx, &ruleSetNetworkManager{})

	router := NewRouter(ctx, log.NewNOPFactory(), option.RouteOptions{}, option.DNSOptions{})
	// A rule's `rule_set` item resolves its set through the router it registers itself with at Start, and
	// production registers the router in the registry the moment it is built (`box.go`). Without this the
	// item's Start dereferences a nil router - which is what this fixture found the first time it ran.
	service.MustRegister[adapter.Router](ctx, router)
	err := router.Initialize(rules, []option.RuleSet{
		{
			Type:   C.RuleSetTypeRemote,
			Format: C.RuleSetFormatBinary,
			Tag:    []string{"remote-set"},
			RemoteOptions: option.RemoteRuleSet{
				URL:            "http://rule-set.test/set.srs",
				UpdateInterval: badoption.Duration(5 * time.Millisecond),
			},
		},
		{
			Type: C.RuleSetTypeInline,
			Tag:  []string{"inline-set"},
			InlineOptions: option.PlainRuleSet{
				Rules: []option.HeadlessRule{{
					Type: C.RuleTypeDefault,
					DefaultOptions: option.DefaultHeadlessRule{
						Domain: badoption.Listable[string]{"inline.example"},
					},
				}},
			},
		},
	})
	require.NoError(t, err)
	return router, hits
}

// startRouterStages drives every stage a Box drives, on a Scope the caller can then close.
func startRouterStages(t *testing.T, router *Router) *adapter.Scope {
	t.Helper()
	scope := adapter.NewScope(context.Background(), log.NewNOPFactory().Logger())
	for _, stage := range []adapter.StartStage{
		adapter.StartStateInitialize,
		adapter.StartStateStart,
		adapter.StartStatePostStart,
		adapter.StartStateStarted,
	} {
		require.NoError(t, scope.Start("router", router, stage), "stage ", stage.String())
	}
	return scope
}

// ruleReferencing builds a rule whose only condition is the named rule-set, so a match proves the set's
// rules are still loaded.
func ruleReferencing(tag string) option.Rule {
	return option.Rule{
		Type: C.RuleTypeDefault,
		DefaultOptions: option.DefaultRule{
			RawDefaultRule: option.RawDefaultRule{
				RuleSet: badoption.Listable[string]{tag},
			},
			RuleAction: option.RuleAction{
				Action: C.RuleActionTypeReject,
			},
		},
	}
}

// TestTheRouterHandsItsRuleSetResourcesToTheScope is the reload-teardown contract.
func TestTheRouterHandsItsRuleSetResourcesToTheScope(t *testing.T) {
	router, hits := newRuleSetRouterFixture(t, []option.Rule{
		ruleReferencing("remote-set"),
		ruleReferencing("inline-set"),
	})

	remote, loaded := router.RuleSet("remote-set")
	require.True(t, loaded)
	inline, loaded := router.RuleSet("inline-set")
	require.True(t, loaded)

	// Before the start stages run, a remote rule-set has fetched nothing - its rules are loaded by
	// StartContext, not by the constructor.
	require.False(t, remote.Match(&adapter.InboundContext{Domain: "remote.example"}),
		"Initialize must only construct: a remote rule-set fetches at Start")

	scope := startRouterStages(t, router)

	// The count is NOT pinned to one: `StartContext` fetches, and `PostStart` immediately starts the
	// updater whose first timer fires at once, so a second request may legitimately be in flight by the
	// time this line runs. Asserting "exactly 1" here was a latent flake - the assertion that matters is
	// that the fetch happened at all, which the rule assertion below states precisely.
	require.GreaterOrEqual(t, hits.Load(), int32(1),
		"StartContext must fetch the rule-set: the rules are loaded at Start, not by the constructor")
	require.True(t, remote.Match(&adapter.InboundContext{Domain: "remote.example"}),
		"the fetched rules must be live while the Box runs")
	require.True(t, remote.Match(&adapter.InboundContext{Destination: M.SocksaddrFrom(netip.MustParseAddr("10.1.2.3"), 443)}),
		"including the IP set a TUN's route_address_set would extract")
	require.True(t, inline.Match(&adapter.InboundContext{Domain: "inline.example"}))

	// The updater refreshes on its interval, so the counter is a direct observation of the goroutine.
	require.Eventually(t, func() bool { return hits.Load() >= 3 }, 10*time.Second, 2*time.Millisecond,
		"the periodic refresh must be running before its stop can be proven")

	require.NoError(t, scope.Close())

	require.False(t, remote.Match(&adapter.InboundContext{Domain: "remote.example"}),
		"the remote rule-set still holds its rules after the Scope drained: `scope.Add(ruleSet.Close)` is "+
			"gone, and every reload would pin the old Box's parsed rule-set until the process exits")
	require.False(t, inline.Match(&adapter.InboundContext{Domain: "inline.example"}),
		"the local rule-set was not closed by the Scope either")

	stopped := hits.Load()
	time.Sleep(150 * time.Millisecond)
	require.Equal(t, stopped, hits.Load(),
		"the refresh goroutine kept running after the Scope drained: `scope.Add(updater.Close)` is gone, and "+
			"each reload would leave one behind holding the old rule-sets")
}

// TestAnUnreferencedRuleSetDropsItsRulesAtStartup pins the memory half.
//
// `StartStateStarted` calls `Cleanup` on every rule-set, which releases the rules of a set no rule
// referenced - a config may define one for a client that is not running.
func TestAnUnreferencedRuleSetDropsItsRulesAtStartup(t *testing.T) {
	router, _ := newRuleSetRouterFixture(t, nil)
	inline, loaded := router.RuleSet("inline-set")
	require.True(t, loaded)
	require.True(t, inline.Match(&adapter.InboundContext{Domain: "inline.example"}),
		"the fixture must have parsed rules before the pass")

	scope := startRouterStages(t, router)
	t.Cleanup(func() { _ = scope.Close() })

	require.False(t, inline.Match(&adapter.InboundContext{Domain: "inline.example"}),
		"an unreferenced rule-set must release its rules once every consumer has had its chance to take a "+
			"reference: this is the pass that makes a config full of unused sets cost nothing")
}

// TestTheCleanupPassRunsAfterEveryRuleTookItsReference pins the ORDER, which is the subtle half.
//
// The pass is deliberately the last thing that runs. Running it earlier - at `StartStateStart`, before
// `rule.Start()` in `PostStart` - would find `refs == 0` for every set, drop the rules, and let the rule take
// its reference on an empty set: the traffic it was written to match would silently stop matching.
func TestTheCleanupPassRunsAfterEveryRuleTookItsReference(t *testing.T) {
	router, _ := newRuleSetRouterFixture(t, []option.Rule{
		ruleReferencing("inline-set"),
	})

	scope := startRouterStages(t, router)
	t.Cleanup(func() { _ = scope.Close() })

	rules := router.Rules()
	require.Len(t, rules, 1)
	require.True(t, rules[0].Match(&adapter.InboundContext{Domain: "inline.example"}),
		"the rule matched before the pass ran, so a match now proves the pass found its reference: moving "+
			"Cleanup earlier drops the rules before rule.Start() takes the reference")

	inline, loaded := router.RuleSet("inline-set")
	require.True(t, loaded)
	require.True(t, inline.Match(&adapter.InboundContext{Domain: "inline.example"}),
		"and the set the rule references still holds its rules")
}

// newBrokenRuleSetRouter builds a router whose remote rule-set cannot load, and whose local FILE rule-set is
// constructed - watcher and all - before anything fails.
//
// The order matters: `Router.Initialize` builds every rule-set, and only `StartContext` fetches. So by the
// time the remote one fails, the local one already holds parsed rules and an `fswatch.Watcher` that
// `StartContext` has never started. That watcher is the interesting part: `Close` is the only place it is
// touched, so a Box that fails to start is the shape that would panic there.
func newBrokenRuleSetRouter(t *testing.T) (*Router, adapter.RuleSet) {
	t.Helper()

	hits := &atomic.Int32{}
	ctx := service.ContextWithDefaultRegistry(context.Background())
	service.MustRegister[adapter.HTTPClientManager](ctx,
		&ruleSetHTTPClientManager{transport: &ruleSetTransport{hits: hits, status: http.StatusInternalServerError}})
	service.MustRegister[adapter.NetworkManager](ctx, &ruleSetNetworkManager{})

	localPath := filepath.Join(t.TempDir(), "local.json")
	require.NoError(t, os.WriteFile(localPath,
		[]byte(`{"version":5,"rules":[{"domain":["file.example"]}]}`), 0o644))

	router := NewRouter(ctx, log.NewNOPFactory(), option.RouteOptions{}, option.DNSOptions{})
	service.MustRegister[adapter.Router](ctx, router)
	err := router.Initialize(nil, []option.RuleSet{
		{
			Type:   C.RuleSetTypeRemote,
			Format: C.RuleSetFormatBinary,
			Tag:    []string{"broken-set"},
			RemoteOptions: option.RemoteRuleSet{
				URL:            "http://rule-set.test/broken.srs",
				UpdateInterval: badoption.Duration(time.Hour),
			},
		},
		{
			Type:         C.RuleSetTypeLocal,
			Format:       C.RuleSetFormatSource,
			Tag:          []string{"file-set"},
			LocalOptions: option.LocalRuleSet{Path: localPath},
		},
	})
	require.NoError(t, err)

	fileSet, loaded := router.RuleSet("file-set")
	require.True(t, loaded, "the local rule-set must be built by Initialize")
	return router, fileSet
}

// TestAFailedStartClosesEveryRuleSetItAlreadyOwned is the FAILURE path.
//
// `Box.Start` closes the Scope when `start()` returns an error, and the Scope's entries were handed to it at
// `StartStateInitialize` - before the failing stage ran. So the rollback is the only thing that closes a
// rule-set the Box never finished starting, and there is no other owner for it.
//
// Two things are asserted, and they fail for different reasons:
//
//	no panic        the local rule-set's watcher was created and never started; `Close` is the only code
//	                that touches it on this path
//	nothing left    the parsed rules are released, so a config that fails to load cannot pin them (or a
//	                watcher) for the life of the process, once per failed reload
func TestAFailedStartClosesEveryRuleSetItAlreadyOwned(t *testing.T) {
	router, fileSet := newBrokenRuleSetRouter(t)

	require.True(t, fileSet.Match(&adapter.InboundContext{Domain: "file.example"}),
		"the local rule-set parsed its file at Initialize, before the remote one could fail")

	scope := adapter.NewScope(context.Background(), log.NewNOPFactory().Logger())
	require.NoError(t, scope.Start("router", router, adapter.StartStateInitialize),
		"Initialize hands the close callbacks over and does nothing that can fail for this profile")

	err := scope.Start("router", router, adapter.StartStateStart)
	require.Error(t, err, "a remote rule-set whose server answers 500 must fail the Start stage")
	require.Contains(t, err.Error(), "initial rule-set",
		"and the failure must name what could not be loaded, so the profile author can find the rule-set")

	require.NotPanics(t, func() {
		require.NoError(t, scope.Close())
	}, "the rollback must not panic: this rule-set's watcher was never started, so Close is the first and "+
		"only code to touch it")

	require.False(t, fileSet.Match(&adapter.InboundContext{Domain: "file.example"}),
		"the rollback left the rule-set open: a profile that fails to load would pin a parsed rule-set and an "+
			"unstarted watcher once per attempt, and the Box that failed has no second owner to close it")
}
