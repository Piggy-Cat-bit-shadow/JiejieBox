package provider_test

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/endpoint"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/adapter/provider"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/service"
	"github.com/stretchr/testify/require"
)

type nodeOptions struct {
	option.DialerOptions
	Revision int
	Fail     bool
}
type testNode struct {
	outbound.Adapter
	options nodeOptions
	starts  atomic.Int32
	closes  atomic.Int32
	access  sync.Mutex
	conns   []net.Conn
}

func (n *testNode) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	n.starts.Add(1)
	if stage == adapter.StartStateInitialize {
		scope.Add(func() error {
			n.closes.Add(1)
			n.access.Lock()
			defer n.access.Unlock()
			for _, conn := range n.conns {
				conn.Close()
			}
			return nil
		})
	}
	if stage == adapter.StartStateStart && n.options.Fail {
		return errors.New("failed candidate")
	}
	return nil
}
func (n *testNode) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	if n.closes.Load() != 0 {
		return nil, net.ErrClosed
	}
	client, server := net.Pipe()
	n.access.Lock()
	n.conns = append(n.conns, client, server)
	n.access.Unlock()
	go func() { defer server.Close(); io.Copy(server, server) }()
	return client, nil
}
func (*testNode) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("unsupported")
}

// TestProviderHotUpdateKeepsUnchangedNodesAndConnections is the acceptance test for the update
// contract: an unchanged node keeps its object (and therefore its live connections), a changed one
// is replaced, a failed replacement leaves the previous node in service, and a node the new
// subscription no longer names is removed only after the publish.
func TestProviderHotUpdateKeepsUnchangedNodesAndConnections(t *testing.T) {
	ctx := service.ContextWithPtr(context.Background(), urltest.NewHistoryStorage())
	factory := log.NewNOPFactory()
	logger := factory.Logger()
	registry := outbound.NewRegistry()
	var candidates []*testNode
	outbound.Register[nodeOptions](registry, "test", func(_ context.Context, _ adapter.Router, _ log.ContextLogger, tag string, options nodeOptions) (adapter.Outbound, error) {
		n := &testNode{Adapter: outbound.NewAdapter("test", tag, []string{"tcp"}, nil), options: options}
		candidates = append(candidates, n)
		return n, nil
	})
	ep := endpoint.NewManager(endpoint.NewRegistry())
	manager := outbound.NewManager(registry, ep, "")
	require.NoError(t, manager.Create(ctx, nil, logger, "static", "test", &nodeOptions{}))
	scope := adapter.NewScope(ctx, logger)
	t.Cleanup(func() { require.NoError(t, scope.Close()) })
	for _, stage := range adapter.ListStartStages {
		require.NoError(t, scope.Start("endpoint", ep, stage))
		require.NoError(t, scope.Start("outbound", manager, stage))
	}
	static, _ := manager.Outbound("static")
	p := provider.NewAdapter(ctx, nil, manager, factory, logger, "subscription", "inline", option.ProviderHealthCheckOptions{})
	require.NoError(t, p.Start())
	t.Cleanup(func() { require.NoError(t, p.Close()) })
	opts := func(stableRev, changedRev int, fail bool) []option.Outbound {
		return []option.Outbound{
			{Type: "test", Tag: "stable", Options: &nodeOptions{Revision: stableRev}},
			{Type: "test", Tag: "changed", Options: &nodeOptions{Revision: changedRev, Fail: fail}},
			{Type: "test", Tag: "front", Options: &nodeOptions{DialerOptions: option.DialerOptions{Detour: "subscription/changed"}}},
		}
	}
	initial := opts(1, 1, false)
	p.UpdateNodes(initial)
	stable, _ := p.Outbound("subscription/stable")
	changed, _ := p.Outbound("subscription/changed")
	front, _ := p.Outbound("subscription/front")
	conn, err := stable.DialContext(ctx, "tcp", M.Socksaddr{})
	require.NoError(t, err)
	defer conn.Close()
	next := opts(1, 2, false)
	p.UpdateNodes(next)
	stableAgain, _ := p.Outbound("subscription/stable")
	changedAgain, _ := p.Outbound("subscription/changed")
	frontAgain, _ := p.Outbound("subscription/front")
	require.Same(t, stable, stableAgain)
	require.NotSame(t, changed, changedAgain)
	// The dependent node's own options did not change, but its cached detour still names the old
	// object, so it must be rebuilt for the replacement to be reachable.
	require.NotSame(t, front, frontAgain)
	require.EqualValues(t, 4, stable.(*testNode).starts.Load())
	require.Zero(t, stable.(*testNode).closes.Load())
	require.EqualValues(t, 1, changed.(*testNode).closes.Load())
	staticAgain, _ := manager.Outbound("static")
	require.Same(t, static, staticAgain)
	require.Zero(t, static.(*testNode).closes.Load())
	require.NoError(t, conn.SetDeadline(time.Now().Add(time.Second)))
	done := make(chan error, 1)
	go func() { _, err := conn.Write([]byte("alive")); done <- err }()
	result := make([]byte, 5)
	_, err = io.ReadFull(conn, result)
	require.NoError(t, err)
	require.NoError(t, <-done)
	require.Equal(t, "alive", string(result))
	failed := opts(1, 3, true)
	p.UpdateNodes(failed)
	retained, _ := p.Outbound("subscription/changed")
	require.Same(t, changedAgain, retained)
	require.EqualValues(t, 1, candidates[len(candidates)-1].closes.Load())
	count := len(candidates)
	p.UpdateNodes(failed)
	require.Len(t, candidates, count+1, "failed updates must be retried")
	p.UpdateNodes(failed[:1])
	_, found := manager.Outbound("subscription/changed")
	require.False(t, found)
	require.EqualValues(t, 1, changedAgain.(*testNode).closes.Load())
	require.NoError(t, p.Close())
	require.NoError(t, scope.Close())
	for _, n := range candidates {
		require.EqualValues(t, 1, n.closes.Load(), n.Tag())
	}
}

// TestProviderOrdersDependenciesAndRejectsCycles proves that a subscription may list a chain in any
// order and that a cycle is refused before any live node is disturbed.
func TestProviderOrdersDependenciesAndRejectsCycles(t *testing.T) {
	ctx := context.Background()
	factory := log.NewNOPFactory()
	logger := factory.Logger()
	outRegistry := outbound.NewRegistry()
	ep := endpoint.NewManager(endpoint.NewRegistry())
	out := outbound.NewManager(outRegistry, ep, "")
	var order []string
	makeNode := func(tag string, opts nodeOptions) *testNode {
		if opts.Detour != "" {
			_, found := out.Outbound(opts.Detour)
			require.True(t, found, "dependency must be installed before %s", tag)
		}
		order = append(order, tag)
		return &testNode{Adapter: outbound.NewAdapterWithDialerOptions("test", tag, []string{"tcp"}, opts.DialerOptions), options: opts}
	}
	outbound.Register[nodeOptions](outRegistry, "test", func(_ context.Context, _ adapter.Router, _ log.ContextLogger, tag string, opts nodeOptions) (adapter.Outbound, error) {
		return makeNode(tag, opts), nil
	})
	p := provider.NewAdapter(ctx, nil, out, factory, logger, "p", "inline", option.ProviderHealthCheckOptions{})
	defer p.Close()
	opts := func(dep string) *nodeOptions { return &nodeOptions{DialerOptions: option.DialerOptions{Detour: dep}} }
	outs := []option.Outbound{
		{Type: "test", Tag: "last", Options: opts("p/middle")},
		{Type: "test", Tag: "first", Options: opts("")},
		{Type: "test", Tag: "middle", Options: opts("p/first")},
	}
	require.NoError(t, p.UpdateNodes(outs))
	require.Equal(t, []string{"p/first", "p/middle", "p/last"}, order)
	outs[2].Options = opts("p/last")
	require.ErrorContains(t, p.UpdateNodes(outs), "circular")
	require.Len(t, order, 3, "cycle rejection must not replace existing nodes")
}

// providerFixture is the shared setup for the update-contract tests below: a running outbound
// manager with a "test" node type, and an inline provider whose nodes are plain testNode objects.
type providerFixture struct {
	provider *provider.Adapter
	outbound *outbound.Manager
	nodes    *[]*testNode
}

func newProviderFixture(t *testing.T) *providerFixture {
	t.Helper()
	ctx := service.ContextWithPtr(context.Background(), urltest.NewHistoryStorage())
	factory := log.NewNOPFactory()
	logger := factory.Logger()
	registry := outbound.NewRegistry()
	var candidates []*testNode
	outbound.Register[nodeOptions](registry, "test", func(_ context.Context, _ adapter.Router, _ log.ContextLogger, tag string, options nodeOptions) (adapter.Outbound, error) {
		n := &testNode{Adapter: outbound.NewAdapter("test", tag, []string{"tcp"}, nil), options: options}
		candidates = append(candidates, n)
		return n, nil
	})
	endpointManager := endpoint.NewManager(endpoint.NewRegistry())
	manager := outbound.NewManager(registry, endpointManager, "")
	manager.Initialize(func() (adapter.Outbound, error) {
		return &testNode{Adapter: outbound.NewAdapter("test", "default", []string{"tcp"}, nil)}, nil
	})
	fixture := &providerFixture{outbound: manager, nodes: &candidates}
	scope := adapter.NewScope(ctx, logger)
	t.Cleanup(func() { _ = scope.Close() })
	for _, stage := range adapter.ListStartStages {
		require.NoError(t, scope.Start("endpoint", endpointManager, stage))
		require.NoError(t, scope.Start("outbound", manager, stage))
	}
	inline := provider.NewAdapter(ctx, nil, manager, factory, logger, "p", "inline", option.ProviderHealthCheckOptions{})
	fixture.provider = &inline
	require.NoError(t, inline.Start())
	t.Cleanup(func() { _ = inline.Close() })
	return fixture
}

func TestProviderDuplicateTagsAreRenamedDeterministically(t *testing.T) {
	t.Parallel()
	fixture := newProviderFixture(t)
	options := []option.Outbound{
		{Type: "test", Tag: "dup", Options: &nodeOptions{Revision: 1}},
		{Type: "test", Tag: "dup", Options: &nodeOptions{Revision: 2}},
		{Type: "test", Tag: "", Options: &nodeOptions{Revision: 3}},
	}
	require.NoError(t, fixture.provider.UpdateNodes(options))
	require.Equal(t, []string{"p/dup", "p/dup (2)", "p/2"}, tagsOf(fixture.provider.Outbounds()))
	first := fixture.provider.Outbounds()
	// The same document applied again must produce the same names AND reuse the objects: a refresh
	// that does not change anything must not churn the live nodes.
	require.NoError(t, fixture.provider.UpdateNodes(options))
	require.Equal(t, first, fixture.provider.Outbounds())
}

func TestProviderEmptyUpdateDoesNotWipeNodes(t *testing.T) {
	t.Parallel()
	fixture := newProviderFixture(t)
	require.NoError(t, fixture.provider.UpdateNodes([]option.Outbound{
		{Type: "test", Tag: "a", Options: &nodeOptions{}},
	}))
	before := fixture.provider.Outbounds()
	require.Error(t, fixture.provider.UpdateNodes(nil))
	require.Equal(t, before, fixture.provider.Outbounds(), "an empty apply must not be read as 'remove everything'")
}

func TestProviderRefusesToTakeAStaticTag(t *testing.T) {
	t.Parallel()
	fixture := newProviderFixture(t)
	require.NoError(t, fixture.outbound.Create(nil, nil, log.NewNOPFactory().Logger(), "p/static", "test", &nodeOptions{}))
	static, found := fixture.outbound.Outbound("p/static")
	require.True(t, found)

	err := fixture.provider.UpdateNodes([]option.Outbound{
		{Type: "test", Tag: "static", Options: &nodeOptions{Revision: 9}},
	})
	require.ErrorContains(t, err, "already in use")
	settled, found := fixture.outbound.Outbound("p/static")
	require.True(t, found)
	require.Same(t, static, settled, "the static node must not be replaced by a provider node")
	require.Empty(t, fixture.provider.Outbounds(), "the refused node must not be published")
}

func TestProviderRemovesAChainDependentFirst(t *testing.T) {
	t.Parallel()
	fixture := newProviderFixture(t)
	chain := []option.Outbound{
		{Type: "test", Tag: "front", Options: &nodeOptions{DialerOptions: option.DialerOptions{Detour: "p/back"}}},
		{Type: "test", Tag: "back", Options: &nodeOptions{}},
	}
	require.NoError(t, fixture.provider.UpdateNodes(chain))
	// Published order is the document's order; creation order is the dependency order (back first),
	// which the construction test above asserts separately through the factory's call order.
	require.Equal(t, []string{"p/front", "p/back"}, tagsOf(fixture.provider.Outbounds()))
	// Drop the whole chain: the dependent has to be unlinked before the node it names, or the
	// manager's dependency guard refuses the second removal and leaves an orphan.
	require.NoError(t, fixture.provider.UpdateNodes([]option.Outbound{
		{Type: "test", Tag: "kept", Options: &nodeOptions{}},
	}))
	require.Equal(t, []string{"p/kept"}, tagsOf(fixture.provider.Outbounds()))
	_, found := fixture.outbound.Outbound("p/front")
	require.False(t, found)
	_, found = fixture.outbound.Outbound("p/back")
	require.False(t, found)
}

func TestProviderPartiallyFailedUpdateKeepsOldNodesForFailedTags(t *testing.T) {
	t.Parallel()
	fixture := newProviderFixture(t)
	require.NoError(t, fixture.provider.UpdateNodes([]option.Outbound{
		{Type: "test", Tag: "a", Options: &nodeOptions{Revision: 1}},
		{Type: "test", Tag: "b", Options: &nodeOptions{Revision: 1}},
	}))
	old := fixture.provider.Outbounds()
	// `a` changes and fails to start; `b` changes and succeeds.
	err := fixture.provider.UpdateNodes([]option.Outbound{
		{Type: "test", Tag: "a", Options: &nodeOptions{Revision: 2, Fail: true}},
		{Type: "test", Tag: "b", Options: &nodeOptions{Revision: 2}},
	})
	require.Error(t, err)
	after := fixture.provider.Outbounds()
	require.Len(t, after, 2)
	require.Same(t, old[0], after[0], "a failed replacement keeps the previous a")
	require.NotSame(t, old[1], after[1], "b still updates")
	require.Equal(t, 2, after[1].(*testNode).options.Revision)
}

func tagsOf(outbounds []adapter.Outbound) []string {
	tags := make([]string, 0, len(outbounds))
	for _, node := range outbounds {
		tags = append(tags, node.Tag())
	}
	return tags
}
