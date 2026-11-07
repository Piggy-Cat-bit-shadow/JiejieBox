package provider_test

import (
	"context"
	"errors"
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
	"github.com/sagernet/sing-box/protocol/group"
	"github.com/sagernet/sing/common/json/badoption"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// replacementProviderManager serves the real provider.Adapters by tag, which is all a group needs
// from the manager.
type replacementProviderManager struct {
	providers map[string]adapter.Provider
}

func (m *replacementProviderManager) Start(adapter.StartStage, *adapter.Scope) error { return nil }
func (m *replacementProviderManager) Providers() []adapter.Provider {
	providers := make([]adapter.Provider, 0, len(m.providers))
	for _, provider := range m.providers {
		providers = append(providers, provider)
	}
	return providers
}
func (m *replacementProviderManager) Get(tag string) (adapter.Provider, bool) {
	provider, loaded := m.providers[tag]
	return provider, loaded
}
func (m *replacementProviderManager) Remove(string) error { return nil }
func (m *replacementProviderManager) Create(context.Context, adapter.Router, log.Factory, string, string, any) error {
	return nil
}

// replacementProvider adds the timestamp method provider.Adapter itself does not carry, so the
// wrapper is the adapter.Provider the group sees while callbacks still land on the real Adapter.
type replacementProvider struct {
	*provider.Adapter
}

func (p *replacementProvider) UpdatedAt() time.Time { return time.Time{} }

// replacementWorld is a real manager + real selector fed by a real provider.Adapter.
type replacementWorld struct {
	ctx      context.Context
	manager  *outbound.Manager
	selector *group.Selector
	provider *provider.Adapter
	scope    *adapter.Scope
	nodes    *replacementNodes
}

type replacementNodes struct {
	access sync.Mutex
	all    []*testNode
}

func (n *replacementNodes) add(node *testNode) {
	n.access.Lock()
	n.all = append(n.all, node)
	n.access.Unlock()
}

func (n *replacementNodes) snapshot() []*testNode {
	n.access.Lock()
	defer n.access.Unlock()
	return append([]*testNode(nil), n.all...)
}

func newReplacementWorld(t *testing.T, selectorTag string) *replacementWorld {
	t.Helper()
	ctx := service.ContextWithPtr(context.Background(), urltest.NewHistoryStorage())
	factory := log.NewNOPFactory()
	logger := factory.Logger()
	registry := outbound.NewRegistry()
	nodes := &replacementNodes{}
	outbound.Register[nodeOptions](registry, "test", func(_ context.Context, _ adapter.Router, _ log.ContextLogger, tag string, options nodeOptions) (adapter.Outbound, error) {
		node := &testNode{Adapter: outbound.NewAdapter("test", tag, []string{"tcp"}, nil), options: options}
		nodes.add(node)
		return node, nil
	})
	endpointManager := endpoint.NewManager(endpoint.NewRegistry())
	manager := outbound.NewManager(registry, endpointManager, "")
	manager.Initialize(func() (adapter.Outbound, error) {
		return &testNode{Adapter: outbound.NewAdapter("test", "direct", []string{"tcp", "udp"}, nil)}, nil
	})
	// The production selector resolves this service from the box context and therefore sees the
	// lease capability. Without it the fixture's group dials the member directly, which is not the
	// path under test and made the concurrent test flaky for the very window the lease closes.
	ctx = service.ContextWith[adapter.OutboundManager](ctx, manager)
	scope := adapter.NewScope(ctx, logger)
	t.Cleanup(func() { _ = scope.Close() })
	for _, stage := range adapter.ListStartStages {
		require.NoError(t, scope.Start("endpoint", endpointManager, stage))
		require.NoError(t, scope.Start("outbound", manager, stage))
	}
	inline := provider.NewAdapter(ctx, nil, manager, factory, logger, "p", "inline", option.ProviderHealthCheckOptions{})
	require.NoError(t, inline.Start())
	t.Cleanup(func() { _ = inline.Close() })
	ctx = service.ContextWith[adapter.ProviderManager](ctx, &replacementProviderManager{providers: map[string]adapter.Provider{
		"p": &replacementProvider{Adapter: &inline},
	}})

	created, err := group.NewSelector(ctx, nil, logger, selectorTag, option.SelectorOutboundOptions{
		Providers: []string{"p"},
	})
	require.NoError(t, err)
	selector := created.(*group.Selector)
	require.NoError(t, selector.Start(adapter.StartStateStart, scope))
	return &replacementWorld{ctx: ctx, manager: manager, selector: selector, provider: &inline, scope: scope, nodes: nodes}
}

func (w *replacementWorld) publish(t *testing.T, revision int) {
	t.Helper()
	require.NoError(t, w.provider.UpdateNodes([]option.Outbound{{
		Type: "test", Tag: "X", Options: &nodeOptions{Revision: revision},
	}}))
	require.NoError(t, w.provider.UpdateGroups())
}

func (w *replacementWorld) dial(t *testing.T) error {
	t.Helper()
	conn, err := w.selector.DialContext(context.Background(), "tcp", M.ParseSocksaddr("127.0.0.1:1"))
	if err != nil {
		return err
	}
	_ = conn.Close()
	return nil
}

// TestSameTagReplacementNeverHandsANewDialAClosedObject is the primary review point.
//
// After `UpdateNodes` replaces the same tag, the selector still holds the previous object until its
// callback runs (`UpdateGroups`). The manager must not close that object in that window: a new dial
// would receive a closed object even though a valid replacement is already published.
func TestSameTagReplacementNeverHandsANewDialAClosedObject(t *testing.T) {
	world := newReplacementWorld(t, "g")
	world.publish(t, 1)
	first, loaded := world.manager.Outbound("p/X")
	require.True(t, loaded)
	require.NoError(t, world.dial(t), "the initial selection must be dialable")

	// Replace p/X with v2 but deliberately do NOT notify the group yet. This is exactly the window
	// between Replace's swap and the provider's UpdateGroups.
	require.NoError(t, world.provider.UpdateNodes([]option.Outbound{{
		Type: "test", Tag: "X", Options: &nodeOptions{Revision: 2},
	}}))
	second, loaded := world.manager.Outbound("p/X")
	require.True(t, loaded)
	require.NotSame(t, first, second, "the tag must already point at v2")
	require.EqualValues(t, 0, first.(*testNode).closes.Load(),
		"the replaced object must stay open until every group rebuilt its membership")
	require.NoError(t, world.dial(t),
		"a dial in the replacement window must not receive the object that is about to be retired")

	require.NoError(t, world.provider.UpdateGroups())
	require.Equal(t, "p/X", world.selector.Selected("tcp").Tag())
	require.Same(t, second, world.selector.Selected("tcp"))
	require.NoError(t, world.dial(t))
	require.GreaterOrEqual(t, first.(*testNode).closes.Load(), int32(1),
		"after the groups rebuilt, the replaced object can be closed")
}

// TestConcurrentDialsAcrossRepeatedSameTagReplacement hammers the window: no dial may observe a
// closed object, and repeated refreshes must not leak objects.
func TestConcurrentDialsAcrossRepeatedSameTagReplacement(t *testing.T) {
	world := newReplacementWorld(t, "g")
	world.publish(t, 1)

	var (
		stop     = make(chan struct{})
		wait     sync.WaitGroup
		failures atomic.Int32
		closed   atomic.Int32
	)
	for i := 0; i < 4; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				conn, err := world.selector.DialContext(context.Background(), "tcp", M.ParseSocksaddr("127.0.0.1:1"))
				if err != nil {
					if err == net.ErrClosed {
						closed.Add(1)
					} else {
						failures.Add(1)
					}
					continue
				}
				_ = conn.Close()
			}
		}()
	}
	for revision := 2; revision <= 20; revision++ {
		require.NoError(t, world.provider.UpdateNodes([]option.Outbound{{
			Type: "test", Tag: "X", Options: &nodeOptions{Revision: revision},
		}}))
		// Give the dialers a chance to run inside the window before the groups are notified.
		time.Sleep(time.Millisecond)
		require.NoError(t, world.provider.UpdateGroups())
	}
	close(stop)
	wait.Wait()
	require.Zero(t, closed.Load(), "a new dial received an object that had already been closed")
	require.Zero(t, failures.Load(), "dials in the replacement window failed")

	live := 0
	for _, object := range world.manager.Outbounds() {
		if object.Tag() == "p/X" {
			live++
		}
	}
	require.Equal(t, 1, live, "repeated replacement must leave exactly one p/X object published")
	versions := make([]*testNode, 0, 20)
	for _, node := range world.nodes.snapshot() {
		if node.Tag() == "p/X" {
			versions = append(versions, node)
		}
	}
	require.Len(t, versions, 20, "one object per published revision")
	for index, version := range versions[:len(versions)-1] {
		require.EqualValues(t, 1, version.closes.Load(),
			"retired revision %d must be closed exactly once after its publication round", index+1)
	}
	require.Zero(t, versions[len(versions)-1].closes.Load(), "the live revision must stay open")
}

// TestURLTestSameTagReplacementDefersTheClose is the urltest half: the wrapper's probe generation
// holds the previous objects, and the manager must not close them before its callback rebuilt.
func TestURLTestSameTagReplacementDefersTheClose(t *testing.T) {
	ctx := service.ContextWithPtr(context.Background(), urltest.NewHistoryStorage())
	factory := log.NewNOPFactory()
	logger := factory.Logger()
	registry := outbound.NewRegistry()
	var nodes []*testNode
	outbound.Register[nodeOptions](registry, "test", func(_ context.Context, _ adapter.Router, _ log.ContextLogger, tag string, options nodeOptions) (adapter.Outbound, error) {
		node := &testNode{Adapter: outbound.NewAdapter("test", tag, []string{"tcp"}, nil), options: options}
		nodes = append(nodes, node)
		return node, nil
	})
	endpointManager := endpoint.NewManager(endpoint.NewRegistry())
	manager := outbound.NewManager(registry, endpointManager, "")
	manager.Initialize(func() (adapter.Outbound, error) {
		return &testNode{Adapter: outbound.NewAdapter("test", "direct", []string{"tcp"}, nil)}, nil
	})
	scope := adapter.NewScope(ctx, logger)
	t.Cleanup(func() { _ = scope.Close() })
	for _, stage := range adapter.ListStartStages {
		require.NoError(t, scope.Start("endpoint", endpointManager, stage))
		require.NoError(t, scope.Start("outbound", manager, stage))
	}
	inline := provider.NewAdapter(ctx, nil, manager, factory, logger, "p", "inline", option.ProviderHealthCheckOptions{})
	require.NoError(t, inline.Start())
	t.Cleanup(func() { _ = inline.Close() })
	ctx = service.ContextWith[adapter.ProviderManager](ctx, &replacementProviderManager{providers: map[string]adapter.Provider{
		"p": &replacementProvider{Adapter: &inline},
	}})
	created, err := group.NewURLTest(ctx, nil, logger, "auto", option.URLTestOutboundOptions{
		Providers:   []string{"p"},
		URL:         "http://127.0.0.1:1/",
		Interval:    badoption.Duration(time.Hour),
		IdleTimeout: badoption.Duration(2 * time.Hour),
	})
	require.NoError(t, err)
	urlTest := created.(*group.URLTest)
	require.NoError(t, urlTest.Start(adapter.StartStateStart, scope))
	t.Cleanup(func() { _ = urlTest.Close() })

	require.NoError(t, inline.UpdateNodes([]option.Outbound{{Type: "test", Tag: "X", Options: &nodeOptions{Revision: 1}}}))
	require.NoError(t, inline.UpdateGroups())
	first, loaded := manager.Outbound("p/X")
	require.True(t, loaded)

	require.NoError(t, inline.UpdateNodes([]option.Outbound{{Type: "test", Tag: "X", Options: &nodeOptions{Revision: 2}}}))
	require.EqualValues(t, 0, first.(*testNode).closes.Load(),
		"the urltest generation still contains the previous object; closing it early would break its probes and dials")
	require.NoError(t, inline.UpdateGroups())
	require.GreaterOrEqual(t, first.(*testNode).closes.Load(), int32(1))
}

// TestMultiProviderSameTagReplacementDefersTheClose covers a selector fed by two providers: the
// replacement in one provider must not close an object the selector still holds while the other
// provider's state is untouched.
func TestMultiProviderSameTagReplacementDefersTheClose(t *testing.T) {
	world := newReplacementWorld(t, "g") // selector g currently names provider p
	second := provider.NewAdapter(world.ctx, nil, world.manager, log.NewNOPFactory(), log.NewNOPFactory().Logger(), "q", "inline", option.ProviderHealthCheckOptions{})
	require.NoError(t, second.Start())
	t.Cleanup(func() { _ = second.Close() })

	// Rebuild the selector so it names both providers. The old one is closed by the scope later.
	created, err := group.NewSelector(world.ctx, nil, log.NewNOPFactory().Logger(), "multi", option.SelectorOutboundOptions{
		Providers: []string{"p", "q"},
	})
	require.NoError(t, err)
	multi := created.(*group.Selector)
	// The context's provider manager only knows p; add q so the selector can bind both.
	manager := service.FromContext[adapter.ProviderManager](world.ctx).(*replacementProviderManager)
	manager.providers["q"] = &replacementProvider{Adapter: &second}
	require.NoError(t, multi.Start(adapter.StartStateStart, world.scope))
	t.Cleanup(func() { _ = world.scope.Close() })

	require.NoError(t, world.provider.UpdateNodes([]option.Outbound{{Type: "test", Tag: "X", Options: &nodeOptions{Revision: 1}}}))
	require.NoError(t, world.provider.UpdateGroups())
	require.NoError(t, second.UpdateNodes([]option.Outbound{{Type: "test", Tag: "Y", Options: &nodeOptions{Revision: 1}}}))
	require.NoError(t, second.UpdateGroups())
	require.ElementsMatch(t, []string{"p/X", "q/Y"}, multi.All())
	selected := multi.Selected("tcp")
	require.NotNil(t, selected)
	require.Equal(t, "p/X", selected.Tag())
	first, loaded := world.manager.Outbound("p/X")
	require.True(t, loaded)

	require.NoError(t, world.provider.UpdateNodes([]option.Outbound{{Type: "test", Tag: "X", Options: &nodeOptions{Revision: 2}}}))
	require.EqualValues(t, 0, first.(*testNode).closes.Load(),
		"another provider's node cannot make this replacement safe to close early")
	require.NoError(t, world.provider.UpdateGroups())
	require.Equal(t, "p/X", multi.Selected("tcp").Tag())
	require.NotSame(t, selected, multi.Selected("tcp"), "the selection follows the tag onto the replacement object")
}

// TestCloseWithARetiredObjectDoesNotLeak covers teardown while a replacement round never completed:
// both the published and the retired object close exactly once and the close does not deadlock.
func TestCloseWithARetiredObjectDoesNotLeak(t *testing.T) {
	world := newReplacementWorld(t, "g")
	world.publish(t, 1)
	first, loaded := world.manager.Outbound("p/X")
	require.True(t, loaded)
	require.NoError(t, world.provider.UpdateNodes([]option.Outbound{{Type: "test", Tag: "X", Options: &nodeOptions{Revision: 2}}}))
	second, loaded := world.manager.Outbound("p/X")
	require.True(t, loaded)
	require.NotSame(t, first, second)
	require.EqualValues(t, 0, first.(*testNode).closes.Load())

	// No UpdateGroups: the retired object is still held by the selector. Tear the whole world down.
	require.NoError(t, world.provider.Close())
	require.NoError(t, world.scope.Close())
	require.EqualValues(t, 1, first.(*testNode).closes.Load(), "the retired object must be closed by the scope exactly once")
	require.EqualValues(t, 1, second.(*testNode).closes.Load(), "the published object must be closed exactly once")
}

// failingHolder is a contract-following subscriber: it fails one publication round and keeps the
// previous object, then rebuilds on a later round. It is the failure path the ownership barrier must
// preserve.
type failingHolder struct {
	object atomic.Pointer[testNode]
	fail   atomic.Bool
	calls  atomic.Int32
}

func (h *failingHolder) callback(string) error {
	h.calls.Add(1)
	if h.fail.Load() {
		return errors.New("membership refused")
	}
	return nil
}

func (h *failingHolder) dial() error {
	node := h.object.Load()
	conn, err := node.DialContext(context.Background(), "tcp", M.ParseSocksaddr("127.0.0.1:1"))
	if err != nil {
		return err
	}
	return conn.Close()
}

func TestFailedPublicationKeepsTheHeldObjectUsable(t *testing.T) {
	world := newReplacementWorld(t, "g")
	world.publish(t, 1)
	first, loaded := world.manager.Outbound("p/X")
	require.True(t, loaded)
	holder := &failingHolder{}
	holder.object.Store(first.(*testNode))
	element := world.provider.RegisterCallback(holder.callback)
	t.Cleanup(func() { world.provider.UnregisterCallback(element) })

	holder.fail.Store(true)
	require.NoError(t, world.provider.UpdateNodes([]option.Outbound{{Type: "test", Tag: "X", Options: &nodeOptions{Revision: 2}}}))
	require.ErrorContains(t, world.provider.UpdateGroups(), "membership refused")
	require.EqualValues(t, 0, first.(*testNode).closes.Load(),
		"a failed publication round must not close the object the holder still serves")
	require.NoError(t, holder.dial(), "the held object must remain usable after the refused round")

	holder.fail.Store(false)
	second, loaded := world.manager.Outbound("p/X")
	require.True(t, loaded)
	holder.object.Store(second.(*testNode))
	require.NoError(t, world.provider.UpdateGroups())
	require.GreaterOrEqual(t, first.(*testNode).closes.Load(), int32(1),
		"the next successful round completes the retirement of the earlier replacement")
	require.NoError(t, holder.dial())
}
