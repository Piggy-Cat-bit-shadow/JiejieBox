package group

import (
	"context"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/common/x/list"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// stubProvider is the provider half of the group contract: a named set of outbounds that can
// publish a new set and notify the groups that registered for it. It runs the callbacks
// synchronously, which is what the real provider does from its own apply goroutine.
type stubProvider struct {
	tag       string
	access    sync.Mutex
	outbounds []adapter.Outbound
	callbacks list.List[adapter.ProviderUpdateCallback]
}

func (p *stubProvider) Type() string { return "inline" }
func (p *stubProvider) Tag() string  { return p.tag }

func (p *stubProvider) Outbounds() []adapter.Outbound {
	p.access.Lock()
	defer p.access.Unlock()
	outbounds := make([]adapter.Outbound, len(p.outbounds))
	copy(outbounds, p.outbounds)
	return outbounds
}

func (p *stubProvider) Outbound(tag string) (adapter.Outbound, bool) {
	for _, outbound := range p.Outbounds() {
		if outbound.Tag() == tag {
			return outbound, true
		}
	}
	return nil, false
}

func (p *stubProvider) UpdatedAt() time.Time { return time.Now() }

func (p *stubProvider) HealthCheck(context.Context) (map[string]uint16, error) { return nil, nil }

func (p *stubProvider) RegisterCallback(callback adapter.ProviderUpdateCallback) *list.Element[adapter.ProviderUpdateCallback] {
	p.access.Lock()
	defer p.access.Unlock()
	return p.callbacks.PushBack(callback)
}

func (p *stubProvider) UnregisterCallback(element *list.Element[adapter.ProviderUpdateCallback]) {
	if element == nil {
		return
	}
	p.access.Lock()
	defer p.access.Unlock()
	p.callbacks.Remove(element)
}

// publish replaces the node set and notifies the registered groups, in the order the real adapter
// does it: new set first, then callbacks.
func (p *stubProvider) publish(outbounds ...adapter.Outbound) error {
	p.access.Lock()
	p.outbounds = outbounds
	var callbacks []adapter.ProviderUpdateCallback
	for element := p.callbacks.Front(); element != nil; element = element.Next() {
		callbacks = append(callbacks, element.Value)
	}
	p.access.Unlock()
	for _, callback := range callbacks {
		if err := callback(p.tag); err != nil {
			return err
		}
	}
	return nil
}

// stubProviderManager serves one provider by tag.
type stubProviderManager struct {
	providers map[string]adapter.Provider
}

func newStubProviderManager(providers ...adapter.Provider) *stubProviderManager {
	manager := &stubProviderManager{providers: make(map[string]adapter.Provider)}
	for _, provider := range providers {
		manager.providers[provider.Tag()] = provider
	}
	return manager
}

func (m *stubProviderManager) Start(adapter.StartStage, *adapter.Scope) error { return nil }

func (m *stubProviderManager) Providers() []adapter.Provider {
	providers := make([]adapter.Provider, 0, len(m.providers))
	for _, provider := range m.providers {
		providers = append(providers, provider)
	}
	return providers
}

func (m *stubProviderManager) Get(tag string) (adapter.Provider, bool) {
	provider, found := m.providers[tag]
	return provider, found
}

func (m *stubProviderManager) Remove(tag string) error {
	delete(m.providers, tag)
	return nil
}

func (m *stubProviderManager) Create(context.Context, adapter.Router, log.Factory, string, string, any) error {
	return nil
}

func newProviderWorld(t *testing.T, provider *stubProvider) (*groupWorld, *Selector) {
	t.Helper()
	world := newGroupWorld(t)
	world.add(newEdgeLeaf("self", "tcp", "udp"))
	world.ctx = service.ContextWith[adapter.ProviderManager](world.ctx, newStubProviderManager(provider))
	created, err := NewSelector(world.ctx, nil, log.NewNOPFactory().NewLogger("selector"), "proxy", option.SelectorOutboundOptions{
		Outbounds: []string{"self"},
		Providers: []string{"prov"},
		Default:   "self",
	})
	require.NoError(t, err)
	selector, isSelector := created.(*Selector)
	require.True(t, isSelector)
	world.add(selector)
	return world, selector
}

// TestGroupsAllowARepeatedMemberDeclaration restores an upstream compatibility rule: a group may
// list one member twice. Route resolution and loadbalance define what that means; a group
// constructor must not refuse the configuration, which is what this fork's route test pins.
func TestGroupsAllowARepeatedMemberDeclaration(t *testing.T) {
	t.Parallel()
	world := newGroupWorld(t)
	world.add(newEdgeLeaf("leaf-a", "tcp", "udp"))
	createdSelector, err := NewSelector(world.ctx, nil, log.NewNOPFactory().NewLogger("selector"), "sel", option.SelectorOutboundOptions{
		Outbounds: []string{"leaf-a", "leaf-a"},
		Default:   "leaf-a",
	})
	require.NoError(t, err)
	selector := createdSelector.(*Selector)
	world.add(selector)
	world.start(selector)
	require.Equal(t, "leaf-a", selector.Selected("tcp").Tag())
	require.Contains(t, selector.All(), "leaf-a")

	createdURLTest, err := NewURLTest(world.ctx, nil, log.NewNOPFactory().NewLogger("auto"), "auto", option.URLTestOutboundOptions{
		Outbounds:   []string{"leaf-a", "leaf-a"},
		URL:         "http://127.0.0.1:1/",
		Interval:    badoption.Duration(time.Hour),
		IdleTimeout: badoption.Duration(2 * time.Hour),
	})
	require.NoError(t, err)
	urlTest := createdURLTest.(*URLTest)
	world.add(urlTest)
	world.start(urlTest)
	require.NoError(t, urlTest.Start(adapter.StartStateStarted, &adapter.Scope{}))
	t.Cleanup(func() { _ = urlTest.Close() })
	require.Contains(t, urlTest.All(), "leaf-a")
}

// TestEmptyProviderGroupFailsTheDialWithoutPanicking covers the zero-member state on the data path:
// the group exists, has no member, and a dial must come back as a named error - not a nil
// dereference and not a silent hang.
func TestEmptyProviderGroupFailsTheDialWithoutPanicking(t *testing.T) {
	t.Parallel()
	provider := &stubProvider{tag: "prov"}
	world := newGroupWorld(t)
	world.ctx = service.ContextWith[adapter.ProviderManager](world.ctx, newStubProviderManager(provider))
	createdSelector, err := NewSelector(world.ctx, nil, log.NewNOPFactory().NewLogger("selector"), "proxy", option.SelectorOutboundOptions{
		Providers: []string{"prov"},
	})
	require.NoError(t, err)
	selector := createdSelector.(*Selector)
	world.add(selector)
	world.start(selector)
	require.Empty(t, selector.All())
	require.Nil(t, selector.Selected("tcp"))
	_, err = selector.DialContext(context.Background(), "tcp", M.ParseSocksaddr("127.0.0.1:1"))
	require.Error(t, err)

	createdURLTest, err := NewURLTest(world.ctx, nil, log.NewNOPFactory().NewLogger("auto"), "auto", option.URLTestOutboundOptions{
		Providers:   []string{"prov"},
		URL:         "http://127.0.0.1:1/",
		Interval:    badoption.Duration(time.Hour),
		IdleTimeout: badoption.Duration(2 * time.Hour),
	})
	require.NoError(t, err)
	urlTest := createdURLTest.(*URLTest)
	world.add(urlTest)
	world.start(urlTest)
	t.Cleanup(func() { _ = urlTest.Close() })
	require.Empty(t, urlTest.All())
	_, err = urlTest.DialContext(context.Background(), "tcp", M.ParseSocksaddr("127.0.0.1:1"))
	require.Error(t, err)
}

// TestSelectorProviderMembershipFollowsUpdates is the selector half of the group integration: the
// member list changes with the provider, the committed member survives a refresh that still has it,
// and a refresh that removes it falls back to the configured default instead of leaving a dangling
// selection.
func TestSelectorProviderMembershipFollowsUpdates(t *testing.T) {
	t.Parallel()
	provider := &stubProvider{tag: "prov"}
	world, selector := newProviderWorld(t, provider)
	world.start(selector)
	require.Equal(t, []string{"self"}, selector.All())
	require.Equal(t, "self", selector.Selected("tcp").Tag())

	provider.publish(newEdgeLeaf("prov/a", "tcp", "udp"), newEdgeLeaf("prov/b", "tcp", "udp"))
	require.Equal(t, []string{"self", "prov/a", "prov/b"}, selector.All())
	require.Equal(t, "self", selector.Selected("tcp").Tag(), "a refresh must not reset a live selection")

	require.True(t, selector.SelectOutbound("prov/a"))
	require.Equal(t, "prov/a", selector.Selected("tcp").Tag())

	// The selected member survives a refresh that still contains it.
	provider.publish(newEdgeLeaf("prov/a", "tcp", "udp"), newEdgeLeaf("prov/c", "tcp", "udp"))
	require.Equal(t, []string{"self", "prov/a", "prov/c"}, selector.All())
	require.Equal(t, "prov/a", selector.Selected("tcp").Tag())

	// And when it disappears, the configured default is the deterministic fallback.
	provider.publish(newEdgeLeaf("prov/c", "tcp", "udp"))
	require.Equal(t, []string{"self", "prov/c"}, selector.All())
	require.Equal(t, "self", selector.Selected("tcp").Tag())
}

// TestSelectorProviderOnlyStartsEmpty proves the bootstrap state: a group whose only source has not
// published yet starts, reports no members, and accepts the first published set.
func TestSelectorProviderOnlyStartsEmpty(t *testing.T) {
	t.Parallel()
	provider := &stubProvider{tag: "prov"}
	world := newGroupWorld(t)
	world.ctx = service.ContextWith[adapter.ProviderManager](world.ctx, newStubProviderManager(provider))
	created, err := NewSelector(world.ctx, nil, log.NewNOPFactory().NewLogger("selector"), "proxy", option.SelectorOutboundOptions{
		Providers: []string{"prov"},
	})
	require.NoError(t, err)
	selector := created.(*Selector)
	world.add(selector)
	world.start(selector)
	require.Empty(t, selector.All())
	require.Nil(t, selector.Selected("tcp"))

	provider.publish(newEdgeLeaf("prov/a", "tcp", "udp"))
	require.Equal(t, []string{"prov/a"}, selector.All())
	require.Equal(t, "prov/a", selector.Selected("tcp").Tag())
}

// TestSelectorProviderFilters pins include/exclude semantics on the RENDERED tag, which is the only
// tag the group and the operator can name.
func TestSelectorProviderFilters(t *testing.T) {
	t.Parallel()
	provider := &stubProvider{tag: "prov"}
	provider.publish(
		newEdgeLeaf("prov/hk-01", "tcp", "udp"),
		newEdgeLeaf("prov/sg-01", "tcp", "udp"),
		newEdgeLeaf("prov/expired", "tcp", "udp"),
	)
	world := newGroupWorld(t)
	world.add(newEdgeLeaf("self", "tcp", "udp"))
	world.ctx = service.ContextWith[adapter.ProviderManager](world.ctx, newStubProviderManager(provider))
	include := regexp.MustCompile(`prov/(hk|sg)-`)
	exclude := regexp.MustCompile(`expired`)
	created, err := NewSelector(world.ctx, nil, log.NewNOPFactory().NewLogger("selector"), "proxy", option.SelectorOutboundOptions{
		Outbounds: []string{"self"},
		Providers: []string{"prov"},
		Include:   (*badoption.Regexp)(include),
		Exclude:   (*badoption.Regexp)(exclude),
	})
	require.NoError(t, err)
	selector := created.(*Selector)
	world.add(selector)
	world.start(selector)
	require.ElementsMatch(t, []string{"self", "prov/hk-01", "prov/sg-01"}, selector.All())
}

// TestURLTestProviderMembershipFollowsUpdates is the urltest half: the new member set is probed by a
// rebuilt group, removal shrinks it, and an empty provider leaves the wrapper alive (not closed)
// with nothing to select.
func TestURLTestProviderMembershipFollowsUpdates(t *testing.T) {
	t.Parallel()
	provider := &stubProvider{tag: "prov"}
	provider.publish(newEdgeLeaf("prov/a", "tcp", "udp"))
	world := newGroupWorld(t)
	world.ctx = service.ContextWith[adapter.ProviderManager](world.ctx, newStubProviderManager(provider))
	created, err := NewURLTest(world.ctx, nil, log.NewNOPFactory().NewLogger("auto"), "auto", option.URLTestOutboundOptions{
		Providers:   []string{"prov"},
		URL:         "http://127.0.0.1:1/",
		Interval:    badoption.Duration(time.Hour),
		IdleTimeout: badoption.Duration(2 * time.Hour),
	})
	require.NoError(t, err)
	urlTest := created.(*URLTest)
	world.add(urlTest)
	world.start(urlTest)
	require.NoError(t, urlTest.Start(adapter.StartStateStarted, &adapter.Scope{}))
	t.Cleanup(func() { _ = urlTest.Close() })
	require.Equal(t, []string{"prov/a"}, urlTest.All())

	provider.publish(newEdgeLeaf("prov/a", "tcp", "udp"), newEdgeLeaf("prov/b", "tcp", "udp"))
	require.ElementsMatch(t, []string{"prov/a", "prov/b"}, urlTest.All())

	provider.publish()
	require.Empty(t, urlTest.All())
	require.Nil(t, urlTest.Selected("tcp"))
}

// TestAProviderPublicationThatMovesTheSelectionAdvancesTheRevision is the other half of the
// review's "provider update path" question: a republication can change the selected member without
// any SelectOutbound call, and if the revision does not move with it a caller cannot tell that the
// member it stored is gone.
func TestAProviderPublicationThatMovesTheSelectionAdvancesTheRevision(t *testing.T) {
	provider := &stubProvider{tag: "prov"}
	world, selector := newProviderWorld(t, provider)
	world.start(selector)
	require.NoError(t, provider.publish(newEdgeLeaf("prov/a", "tcp", "udp"), newEdgeLeaf("prov/b", "tcp", "udp")))

	snapshot, ok := selector.SelectOutboundSnapshot("prov/a")
	require.True(t, ok)
	require.Equal(t, "prov/a", snapshot.AppliedTag)
	require.EqualValues(t, 1, snapshot.Revision, "the first change of the selection is revision 1")

	// This publication removes the selected member: the group falls back to the configured default,
	// which is a selection change the caller must be able to observe through the revision.
	require.NoError(t, provider.publish(newEdgeLeaf("prov/b", "tcp", "udp")))
	require.Equal(t, "self", selector.Selected("tcp").Tag())
	require.Greater(t, selector.Revision(), snapshot.Revision,
		"the provider moved the selection, so the revision must advance with it")

	next, ok := selector.SelectOutboundSnapshot("prov/b")
	require.True(t, ok)
	require.Equal(t, "prov/b", next.AppliedTag)
	require.Equal(t, selector.Revision(), next.Revision)
}
