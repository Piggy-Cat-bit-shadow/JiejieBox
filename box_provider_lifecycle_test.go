package box_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	singjson "github.com/sagernet/sing/common/json"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// Box-level acceptance tests for the outbound-provider lifecycle.
//
// # Why these use a real Box and a real Start
//
// `sing-box check` validates the configuration text; it never builds the outbound graph, registers
// a provider callback or runs the start-time dry run. The defects here live in exactly that gap: a
// configuration that checks clean and then either refuses to start (bug 1) or starts and recurses
// (bug 2). So every test below runs `box.New` + `Start`, and the assertion is about the resulting
// object graph and the dial path - never about a parsed config.

func newProviderLifecycleBox(t *testing.T, config string) (*box.Box, context.Context) {
	t.Helper()
	ctx := include.Context(context.Background())
	options, decodeErr := singjson.UnmarshalExtendedContext[option.Options](ctx, []byte(config))
	require.NoError(t, decodeErr)
	instance, newErr := box.New(box.Options{Context: ctx, Options: options})
	require.NoError(t, newErr)
	t.Cleanup(func() { _ = instance.Close() })
	return instance, ctx
}

func waitForGroupMembers(t *testing.T, group adapter.OutboundGroup, want ...string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if equalTags(group.All(), want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("group membership did not become %v; got %v", want, group.All())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func equalTags(got []string, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func outboundGroup(t *testing.T, instance *box.Box, tag string) adapter.OutboundGroup {
	t.Helper()
	object, loaded := instance.Outbound().Outbound(tag)
	require.True(t, loaded, "outbound %s must be addressable", tag)
	group, isGroup := object.(adapter.OutboundGroup)
	require.True(t, isGroup, "outbound %s must be a group", tag)
	return group
}

func providerByTag(t *testing.T, ctx context.Context, tag string) adapter.Provider {
	t.Helper()
	manager := service.FromContext[adapter.ProviderManager](ctx)
	require.NotNil(t, manager, "the Box must publish its provider manager on the context it was built from")
	provider, loaded := manager.Get(tag)
	require.True(t, loaded, "provider %s must exist", tag)
	return provider
}

func updateProvider(t *testing.T, provider adapter.Provider) error {
	t.Helper()
	updater, isUpdater := provider.(adapter.ProviderUpdater)
	require.True(t, isUpdater, "the provider must support a manual refresh")
	var err error
	for attempt := 0; attempt < 100; attempt++ {
		err = updater.Update()
		if err == nil || !strings.Contains(err.Error(), "provider is updating") {
			return err
		}
		time.Sleep(20 * time.Millisecond)
	}
	return err
}

// TestProviderOnlyGroupStartsBeforeItsProviderPublishes is the legal empty state the mislabeled old
// test never covered.
//
// The provider is declared and reachable as configuration, but its document has not arrived. A
// group fed only by it must still let the Box start (a remote download is asynchronous and a device
// may be offline), report no member, and refuse a dial with an error that NAMES the wait. The dial
// error is the runtime half of the contract: "no available member" alone would read like a broken
// group rather than a pending download.
func TestProviderOnlyGroupStartsBeforeItsProviderPublishes(t *testing.T) {
	config := `{
  "log": {"disabled": true},
  "providers": [{"type": "remote", "tag": "p", "url": "http://127.0.0.1:1/subscription"}],
  "outbounds": [
    {"type": "selector", "tag": "g", "providers": ["p"]},
    {"type": "direct", "tag": "direct"}
  ],
  "route": {"final": "direct"}
}`
	started := time.Now()
	instance, _ := newProviderLifecycleBox(t, config)
	require.NoError(t, instance.Start(), "an unreachable subscription must not refuse the start")
	require.Less(t, time.Since(started), 10*time.Second,
		"the start must not block on the provider download")

	group := outboundGroup(t, instance, "g")
	require.Empty(t, group.All())
	require.Nil(t, group.Selected("tcp"))

	dialer, isDialer := group.(adapter.Outbound)
	require.True(t, isDialer)
	_, dialErr := dialer.DialContext(context.Background(), "tcp", M.ParseSocksaddr("127.0.0.1:1"))
	require.ErrorContains(t, dialErr, "waiting for outbound provider p",
		"a dial before publication must name the provider it is waiting for")
}

// TestProviderOnlyURLTestAsDetourStartsAndPublishes is bug 1's urltest mirror.
//
// The chain `static socks -> urltest(provider-only)` was refused with "unusable outbound path
// member(s)" even though the provider file carried a valid node. The local provider reads the file
// in its own start stage - after the outbound manager's dry run - so the group is empty when the
// walk runs, and that must be a pending route rather than a structural failure.
func TestProviderOnlyURLTestAsDetourStartsAndPublishes(t *testing.T) {
	nodesPath := writeProviderFile(t, `{"outbounds": [
		{"type": "socks", "tag": "exit", "server": "127.0.0.1", "server_port": 1081, "version": "5"}
	]}`)
	config := `{
  "log": {"disabled": true},
  "providers": [{"type": "local", "tag": "p", "path": ` + quotePath(nodesPath) + `}],
  "outbounds": [
    {"type": "urltest", "tag": "auto", "providers": ["p"], "url": "http://127.0.0.1:1/"},
    {"type": "socks", "tag": "a", "server": "127.0.0.1", "server_port": 1080, "version": "5", "detour": "auto"},
    {"type": "direct", "tag": "direct"}
  ],
  "route": {"final": "direct"}
}`
	instance, _ := newProviderLifecycleBox(t, config)
	require.NoError(t, instance.Start())

	group := outboundGroup(t, instance, "auto")
	require.Equal(t, []string{"p/exit"}, group.All(),
		"the node from the provider document must be published into the group")
	// The urltest selects a member only after a successful probe; the member existence is what this
	// test can prove without a network. Its selection is covered by the urltest unit suite.
	_, loaded := instance.Outbound().Outbound("a")
	require.True(t, loaded)
}

// TestInlineProviderNodeDetouringIntoItsOwnGroupIsRefused is bug 2 at the start-time publication
// point.
//
// An inline provider publishes before the outbound manager starts, so the group's initial member
// list contains the node that detours back into it. The start-order sort sees neither edge (the
// group declares no member, the node's detour is one direction), and the dry run runs before the
// group's Start fills the membership. The refusal must happen when the group publishes.
func TestInlineProviderNodeDetouringIntoItsOwnGroupIsRefused(t *testing.T) {
	config := `{
  "log": {"disabled": true},
  "providers": [{"type": "inline", "tag": "p", "outbounds": [
    {"type": "socks", "tag": "a", "server": "127.0.0.1", "server_port": 1080, "version": "5", "detour": "g"}
  ]}],
  "outbounds": [{"type": "selector", "tag": "g", "providers": ["p"]}],
  "route": {"final": "g"}
}`
	instance, _ := newProviderLifecycleBox(t, config)
	err := instance.Start()
	require.ErrorContains(t, err, "circular outbound dependency")
	require.Contains(t, err.Error(), "p/a", "the refusal must name the published node, not only the group")
}

// TestProviderDocumentExternalDetourIsClearedByDesign pins the deliberate filter this audit found
// while reproducing the reported cycle.
//
// A document may name a SIBLING node as its detour; the tag is then rewritten into the provider's
// namespace. A detour that names anything OUTSIDE the document is CLEARED, because the provider
// cannot know whether the main configuration still has that tag at apply time (see parser.go). So
// the reported `a.detour = g` shape only reaches the graph through the provider-level
// `override_dialer.detour`, which is what the cycle tests above use. This test keeps the clearing
// from being "fixed" into a dangling external reference.
func TestProviderDocumentExternalDetourIsClearedByDesign(t *testing.T) {
	nodesPath := writeProviderFile(t, `{"outbounds": [
		{"type": "socks", "tag": "a", "server": "127.0.0.1", "server_port": 1080, "version": "5", "detour": "g"}
	]}`)
	config := `{
  "log": {"disabled": true},
  "providers": [{"type": "local", "tag": "p", "path": ` + quotePath(nodesPath) + `}],
  "outbounds": [{"type": "selector", "tag": "g", "providers": ["p"]}],
  "route": {"final": "g"}
}`
	instance, _ := newProviderLifecycleBox(t, config)
	require.NoError(t, instance.Start(), "the external detour is cleared, so no cycle is formed")
	node, loaded := instance.Outbound().Outbound("p/a")
	require.True(t, loaded)
	require.Empty(t, node.Dependencies(),
		"a document detour naming a tag outside the document must be cleared, not kept dangling")
}

// TestLocalProviderOverrideDetourCycleIsRefusedAtStartup uses the shape that DOES reach the graph:
// the provider-level `override_dialer.detour` points at a group the provider feeds.
func TestLocalProviderOverrideDetourCycleIsRefusedAtStartup(t *testing.T) {
	nodesPath := writeProviderFile(t, `{"outbounds": [
		{"type": "socks", "tag": "a", "server": "127.0.0.1", "server_port": 1080, "version": "5"}
	]}`)
	config := `{
  "log": {"disabled": true},
  "providers": [{"type": "local", "tag": "p", "path": ` + quotePath(nodesPath) + `,
    "override_dialer": {"detour": "g"}}],
  "outbounds": [{"type": "selector", "tag": "g", "providers": ["p"]}],
  "route": {"final": "g"}
}`
	instance, _ := newProviderLifecycleBox(t, config)
	require.ErrorContains(t, instance.Start(), "circular outbound dependency")
}

// TestStaticAndDynamicCyclesProduceTheSameVerdict pins the parity the fix is judged on: the same
// final graph is refused with the same reason whether the node was written statically or published
// by a provider.
func TestStaticAndDynamicCyclesProduceTheSameVerdict(t *testing.T) {
	staticInstance, _ := newProviderLifecycleBox(t, `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "selector", "tag": "g", "outbounds": ["a"]},
    {"type": "socks", "tag": "a", "server": "127.0.0.1", "server_port": 1080, "version": "5", "detour": "g"}
  ],
  "route": {"final": "g"}
}`)
	staticErr := staticInstance.Start()
	require.ErrorContains(t, staticErr, "circular outbound dependency")

	nodesPath := writeProviderFile(t, `{"outbounds": [
		{"type": "socks", "tag": "a", "server": "127.0.0.1", "server_port": 1080, "version": "5"}
	]}`)
	dynamicInstance, _ := newProviderLifecycleBox(t, `{
  "log": {"disabled": true},
  "providers": [{"type": "local", "tag": "p", "path": `+quotePath(nodesPath)+`,
    "override_dialer": {"detour": "g"}}],
  "outbounds": [{"type": "selector", "tag": "g", "providers": ["p"]}],
  "route": {"final": "g"}
}`)
	dynamicErr := dynamicInstance.Start()
	require.ErrorContains(t, dynamicErr, "circular outbound dependency")
}

// TestRemoteProviderRefreshReplacesNodes is the A -> B refresh contract.
//
// A valid document is replaced by another valid one: the membership follows, the selection follows,
// and the retired node is unlinked once the group no longer names it. The removal is deferred until
// the group callback rebuilt the membership, so the node is never closed under a selector that can
// still dial it.
func TestRemoteProviderRefreshReplacesNodes(t *testing.T) {
	var document atomic.Value
	document.Store(`{"outbounds": [{"type": "socks", "tag": "A", "server": "127.0.0.1", "server_port": 1080, "version": "5"}]}`)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(document.Load().(string)))
	}))
	t.Cleanup(server.Close)

	config := `{
  "log": {"disabled": true},
  "providers": [{"type": "remote", "tag": "p", "url": ` + strconv.Quote(server.URL+"/sub") + `, "update_interval": "24h"}],
  "outbounds": [
    {"type": "selector", "tag": "g", "providers": ["p"]},
    {"type": "direct", "tag": "direct"}
  ],
  "route": {"final": "direct"}
}`
	instance, ctx := newProviderLifecycleBox(t, config)
	require.NoError(t, instance.Start())
	group := outboundGroup(t, instance, "g")
	waitForGroupMembers(t, group, "p/A")
	require.Equal(t, "p/A", group.Selected("tcp").Tag())

	provider := providerByTag(t, ctx, "p")
	document.Store(`{"outbounds": [{"type": "socks", "tag": "B", "server": "127.0.0.1", "server_port": 1081, "version": "5"}]}`)
	require.NoError(t, updateProvider(t, provider))
	waitForGroupMembers(t, group, "p/B")
	require.Equal(t, "p/B", group.Selected("tcp").Tag())
	_, retiredStillPublished := instance.Outbound().Outbound("p/A")
	require.False(t, retiredStillPublished,
		"once the group dropped p/A, the deferred removal must complete")
}

// TestInvalidProviderRefreshKeepsThePreviousMemberAndSelection is the refusal-on-refresh contract.
//
// Provider p2's every node detours into the group p2 feeds, so publishing one would close a
// recursion. p1's node is valid and already selected. The refused update must leave the group on
// p1/X, keep the selection, leave p1/X addressable, and surface the refusal to the caller.
func TestInvalidProviderRefreshKeepsThePreviousMemberAndSelection(t *testing.T) {
	validPath := writeProviderFile(t, `{"outbounds": [
		{"type": "socks", "tag": "X", "server": "127.0.0.1", "server_port": 1080, "version": "5"}
	]}`)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"outbounds": [{"type": "socks", "tag": "Y", "server": "127.0.0.1", "server_port": 1081, "version": "5"}]}`))
	}))
	t.Cleanup(server.Close)

	config := `{
  "log": {"disabled": true},
  "providers": [
    {"type": "local", "tag": "p1", "path": ` + quotePath(validPath) + `},
    {"type": "remote", "tag": "p2", "url": ` + strconv.Quote(server.URL+"/sub") + `, "update_interval": "24h",
     "override_dialer": {"detour": "g"}}
  ],
  "outbounds": [
    {"type": "selector", "tag": "g", "providers": ["p1", "p2"]},
    {"type": "direct", "tag": "direct"}
  ],
  "route": {"final": "direct"}
}`
	instance, ctx := newProviderLifecycleBox(t, config)
	require.NoError(t, instance.Start(), "the valid provider's node must satisfy the group")
	group := outboundGroup(t, instance, "g")
	waitForGroupMembers(t, group, "p1/X")
	selectedBefore := group.Selected("tcp")
	require.NotNil(t, selectedBefore)
	require.Equal(t, "p1/X", selectedBefore.Tag())

	// The asynchronous first fetch installs p2/Y, and the membership that would select it is
	// refused. Wait until the node is installed, then assert the group did not move.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, loaded := instance.Outbound().Outbound("p2/Y"); loaded {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the blocked provider's node was never installed; the fixture did not reach the refusal")
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.Equal(t, []string{"p1/X"}, group.All(), "a refused membership must leave the previous members")
	require.Same(t, selectedBefore, group.Selected("tcp"), "and the previous selection")

	provider := providerByTag(t, ctx, "p2")
	require.ErrorContains(t, updateProvider(t, provider), "circular outbound dependency",
		"the refusal must reach the caller, not only the log")
	require.Equal(t, []string{"p1/X"}, group.All())
	stillPublished, loaded := instance.Outbound().Outbound("p1/X")
	require.True(t, loaded)
	require.Same(t, selectedBefore, stillPublished)
}

// TestProviderDownloadThroughItsOwnGroupIsRefused covers the explicit bootstrap loop: the
// provider's own HTTP client detours into the group this provider feeds, so the first download waits
// on itself. The diagnostic must name the loop instead of leaving a permanently empty group.
func TestProviderDownloadThroughItsOwnGroupIsRefused(t *testing.T) {
	config := `{
  "log": {"disabled": true},
  "providers": [{
    "type": "remote", "tag": "p", "url": "http://127.0.0.1:1/subscription",
    "http_client": {"detour": "g"}
  }],
  "outbounds": [
    {"type": "selector", "tag": "g", "providers": ["p"]},
    {"type": "direct", "tag": "direct"}
  ],
  "route": {"final": "direct"}
}`
	instance, _ := newProviderLifecycleBox(t, config)
	err := instance.Start()
	require.ErrorContains(t, err, "cannot download through outbound group g")
	require.Contains(t, err.Error(), "waiting on itself")
	require.Contains(t, err.Error(), "detour g")
}

// TestProviderDownloadThroughRouteFinalIsRefused is the bootstrap loop that needs no provider-side
// option at all.
//
// The Box's implicit HTTP client is built with `DefaultOutbound = true`, so its dialer follows
// `route.final`. When the final outbound IS the provider-fed group, the first download is routed
// through the very group it is supposed to populate. MEASURED on the unfixed tree: the Box starts,
// and every refresh logs "download provider document: ... outbound group g has no available member
// yet" - a permanently empty group with no start-time verdict. The check must name it.
func TestProviderDownloadThroughRouteFinalIsRefused(t *testing.T) {
	config := `{
  "log": {"disabled": true},
  "providers": [{"type": "remote", "tag": "p", "url": "http://127.0.0.1:1/subscription"}],
  "outbounds": [{"type": "selector", "tag": "g", "providers": ["p"]}],
  "route": {"final": "g"}
}`
	instance, _ := newProviderLifecycleBox(t, config)
	err := instance.Start()
	require.ErrorContains(t, err, "cannot download through outbound group g")
	require.Contains(t, err.Error(), "route.final",
		"the diagnostic must say the implicit default client follows route.final")
}

// TestProviderDownloadThroughAStaticMemberIsAllowed is the negative control for the bootstrap
// check: a group that a static member can serve is not a loop, and the start must proceed.
func TestProviderDownloadThroughAStaticMemberIsAllowed(t *testing.T) {
	config := `{
  "log": {"disabled": true},
  "providers": [{
    "type": "remote", "tag": "p", "url": "http://127.0.0.1:1/subscription",
    "http_client": {"detour": "g"}
  }],
  "outbounds": [
    {"type": "selector", "tag": "g", "providers": ["p"], "outbounds": ["direct"]},
    {"type": "direct", "tag": "direct"}
  ],
  "route": {"final": "direct"}
}`
	instance, _ := newProviderLifecycleBox(t, config)
	require.NoError(t, instance.Start())
	require.Equal(t, []string{"direct"}, outboundGroup(t, instance, "g").All())
}

// TestProviderOnlyChainIsUsableOnceTheProviderPublishes is the "before and after publication" half
// of bug 1 on the dial path, without any real network.
//
// Before publication, dialling the static node reaches the empty group and must fail naming the
// provider wait. After publication, the same dial reaches the published member - here a `block`
// outbound, which refuses with EPERM - which proves the chain is wired end to end. The member is
// deliberate: it makes the "reached the far end" assertion a local, deterministic fact.
func TestProviderOnlyChainIsUsableOnceTheProviderPublishes(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	var document atomic.Value
	document.Store(`{"outbounds": [{"type": "socks", "tag": "m", "server": "127.0.0.1", "server_port": 1, "version": "5"}]}`)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		<-release
		_, _ = writer.Write([]byte(document.Load().(string)))
	}))
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		server.Close()
	})

	config := `{
  "log": {"disabled": true},
  "providers": [{"type": "remote", "tag": "p", "url": ` + strconv.Quote(server.URL+"/sub") + `, "update_interval": "24h"}],
  "outbounds": [
    {"type": "selector", "tag": "g", "providers": ["p"]},
    {"type": "socks", "tag": "a", "server": "127.0.0.1", "server_port": 1080, "version": "5", "detour": "g"},
    {"type": "direct", "tag": "direct"}
  ],
  "route": {"final": "direct"}
}`
	instance, _ := newProviderLifecycleBox(t, config)
	require.NoError(t, instance.Start())
	group := outboundGroup(t, instance, "g")
	require.Empty(t, group.All(), "the first download is still blocked, so the group has no member")

	chain, loaded := instance.Outbound().Outbound("a")
	require.True(t, loaded)
	_, beforeErr := chain.DialContext(context.Background(), "tcp", M.ParseSocksaddr("127.0.0.1:1"))
	require.ErrorContains(t, beforeErr, "waiting for outbound provider p")

	releaseOnce.Do(func() { close(release) })
	waitForGroupMembers(t, group, "p/m")

	_, afterErr := chain.DialContext(context.Background(), "tcp", M.ParseSocksaddr("127.0.0.1:1"))
	require.Error(t, afterErr)
	require.NotContains(t, afterErr.Error(), "no available member",
		"after publication the dial must reach the published member instead of the empty group")
	require.NotContains(t, afterErr.Error(), "waiting for outbound provider")
}
