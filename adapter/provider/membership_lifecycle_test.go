package provider_test

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"

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

// Tests for the provider/membership lifecycle seams: retiring a node a group still names, surfacing
// a refused membership to the apply path, and diagnosing a download that depends on its own nodes.

// membershipGroupOptions is the constructor input for the group doubles below.
type membershipGroupOptions struct{}

// membershipGroup is a group whose published membership the test drives directly. It declares NO
// dependencies (like a provider-fed selector), so only All() names its members.
type membershipGroup struct {
	outbound.Adapter
	access  sync.RWMutex
	members []string
}

func (g *membershipGroup) All() []string {
	g.access.RLock()
	defer g.access.RUnlock()
	return append([]string(nil), g.members...)
}

func (g *membershipGroup) setMembers(members ...string) {
	g.access.Lock()
	g.members = members
	g.access.Unlock()
}

func (g *membershipGroup) Selected(string) adapter.Outbound { return nil }

func (g *membershipGroup) AttachConnection(io.Closer) func() { return func() {} }

func (g *membershipGroup) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return nil, errors.New("the membership tests must never dial")
}

func (g *membershipGroup) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("the membership tests must never dial")
}

// providerFedGroup additionally publishes its provider feed, which is what the bootstrap check
// reads. See adapter.ProviderBackedGroup.
type providerFedGroup struct {
	*membershipGroup
	providers []string
	usesAll   bool
}

func (g *providerFedGroup) ProviderMembership() ([]string, bool) {
	return g.providers, g.usesAll
}

type membershipFixture struct {
	manager  *outbound.Manager
	group    *membershipGroup
	provider *provider.Adapter
}

func newMembershipFixture(t *testing.T) *membershipFixture {
	t.Helper()
	ctx := service.ContextWithPtr(context.Background(), urltest.NewHistoryStorage())
	factory := log.NewNOPFactory()
	logger := factory.Logger()
	registry := outbound.NewRegistry()
	outbound.Register[membershipGroupOptions](registry, "group", func(_ context.Context, _ adapter.Router, _ log.ContextLogger, tag string, _ membershipGroupOptions) (adapter.Outbound, error) {
		return &membershipGroup{Adapter: outbound.NewAdapter("selector", tag, []string{"tcp", "udp"}, nil)}, nil
	})
	outbound.Register[membershipGroupOptions](registry, "provider-group", func(_ context.Context, _ adapter.Router, _ log.ContextLogger, tag string, _ membershipGroupOptions) (adapter.Outbound, error) {
		return &providerFedGroup{membershipGroup: &membershipGroup{Adapter: outbound.NewAdapter("selector", tag, []string{"tcp", "udp"}, nil)}}, nil
	})
	outbound.Register[nodeOptions](registry, "test", func(_ context.Context, _ adapter.Router, _ log.ContextLogger, tag string, options nodeOptions) (adapter.Outbound, error) {
		return &testNode{Adapter: outbound.NewAdapter("test", tag, []string{"tcp"}, nil), options: options}, nil
	})
	endpointManager := endpoint.NewManager(endpoint.NewRegistry())
	manager := outbound.NewManager(registry, endpointManager, "")
	manager.Initialize(func() (adapter.Outbound, error) {
		return &testNode{Adapter: outbound.NewAdapter("test", "direct", []string{"tcp"}, nil)}, nil
	})
	scope := adapter.NewScope(ctx, logger)
	t.Cleanup(func() { _ = scope.Close() })
	require.NoError(t, manager.Create(ctx, nil, logger, "g", "group", &membershipGroupOptions{}))
	for _, stage := range adapter.ListStartStages {
		require.NoError(t, scope.Start("endpoint", endpointManager, stage))
		require.NoError(t, scope.Start("outbound", manager, stage))
	}
	inline := provider.NewAdapter(ctx, nil, manager, factory, logger, "p", "inline", option.ProviderHealthCheckOptions{})
	require.NoError(t, inline.Start())
	t.Cleanup(func() { _ = inline.Close() })
	groupOutbound, loaded := manager.Outbound("g")
	require.True(t, loaded)
	group, isGroup := groupOutbound.(*membershipGroup)
	require.True(t, isGroup)
	return &membershipFixture{manager: manager, group: group, provider: &inline}
}

// TestConcurrentProviderUpdatesAreSerialised pins the apply lock: two refreshes of the same
// provider may not interleave their publishes. Each document is valid on its own, so both calls
// must succeed, and the published set afterwards must be exactly one document - never a mixture.
func TestConcurrentProviderUpdatesAreSerialised(t *testing.T) {
	fixture := newMembershipFixture(t)
	documents := [][]option.Outbound{
		{{Type: "test", Tag: "A", Options: &nodeOptions{Revision: 1}}},
		{{Type: "test", Tag: "B", Options: &nodeOptions{Revision: 2}}},
	}
	start := make(chan struct{})
	errs := make(chan error, len(documents))
	var waitGroup sync.WaitGroup
	for i := range documents {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			<-start
			errs <- fixture.provider.UpdateNodes(documents[index])
		}(i)
	}
	close(start)
	waitGroup.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	published := fixture.provider.Outbounds()
	require.Len(t, published, 1, "one whole document must win, not a mixture")
	require.Contains(t, []string{"p/A", "p/B"}, published[0].Tag())
}

// The provider retires `p/x` while the selector still publishes it as a member. Before the fix the
// manager unlinked and closed `p/x` immediately - the group's published membership was not part of
// the dependent scan - and the selector kept handing flows to a closed object until its own
// callback landed. The removal must be deferred instead, and retried after the membership rebuild.
func TestProviderRetainsANodeAGroupStillNames(t *testing.T) {
	fixture := newMembershipFixture(t)
	require.NoError(t, fixture.provider.UpdateNodes([]option.Outbound{
		{Type: "test", Tag: "x", Options: &nodeOptions{Revision: 1}},
	}))
	retired, loaded := fixture.provider.Outbound("p/x")
	require.True(t, loaded)
	fixture.group.setMembers("p/x")

	// The subscription now names only `y`; `x` is retired but still selected by the group.
	require.NoError(t, fixture.provider.UpdateNodes([]option.Outbound{
		{Type: "test", Tag: "y", Options: &nodeOptions{Revision: 1}},
	}))
	published, found := fixture.manager.Outbound("p/x")
	require.True(t, found, "a node a group still names must stay addressable")
	require.Same(t, retired, published)
	require.Zero(t, retired.(*testNode).closes.Load(),
		"the retired object must not be closed under the group that can still dial it")
	require.Equal(t, []string{"p/y"}, tagsOf(fixture.provider.Outbounds()))

	// Once the group has rebuilt without the member, the deferred removal completes.
	fixture.group.setMembers()
	require.NoError(t, fixture.provider.UpdateGroups())
	_, found = fixture.manager.Outbound("p/x")
	require.False(t, found, "the deferred removal must run once nothing names the node")
	require.EqualValues(t, 1, retired.(*testNode).closes.Load())
}

// TestProviderUpdateGroupsReportsARefusedMembership pins the propagation: a group refusing the new
// membership is a fact the synchronous apply path has to see, not a log line.
func TestProviderUpdateGroupsReportsARefusedMembership(t *testing.T) {
	fixture := newMembershipFixture(t)
	fixture.provider.RegisterCallback(func(string) error {
		return errors.New("membership refused by the graph")
	})
	err := fixture.provider.UpdateGroups()
	require.ErrorContains(t, err, "membership refused by the graph")
}

// stubProviderManager serves a fixed provider COUNT, which is what the bootstrap check reads for
// `use_all_providers` ("is another provider configured that could publish a member?").
type stubProviderManager struct {
	count int
}

func (m *stubProviderManager) Start(adapter.StartStage, *adapter.Scope) error { return nil }
func (m *stubProviderManager) Providers() []adapter.Provider {
	return make([]adapter.Provider, m.count)
}
func (m *stubProviderManager) Get(string) (adapter.Provider, bool) { return nil, false }
func (m *stubProviderManager) Remove(string) error                 { return nil }
func (m *stubProviderManager) Create(context.Context, adapter.Router, log.Factory, string, string, any) error {
	return nil
}

// TestProviderDownloadDetourBootstrapIsDiagnosed is the "download through my own nodes" loop.
//
// A remote provider whose HTTP client detours into a group fed by that same provider can never
// perform its first download: the group has no member until the download publishes one. The check
// must refuse it with a message naming the loop - and must NOT refuse configurations that another
// provider, a static member, or a restored document can populate.
func TestProviderDownloadDetourBootstrapIsDiagnosed(t *testing.T) {
	ctx := service.ContextWithPtr(context.Background(), urltest.NewHistoryStorage())
	factory := log.NewNOPFactory()
	logger := factory.Logger()
	registry := outbound.NewRegistry()
	var bootstrap *providerFedGroup
	outbound.Register[membershipGroupOptions](registry, "provider-group", func(_ context.Context, _ adapter.Router, _ log.ContextLogger, tag string, _ membershipGroupOptions) (adapter.Outbound, error) {
		bootstrap = &providerFedGroup{membershipGroup: &membershipGroup{Adapter: outbound.NewAdapter("selector", tag, []string{"tcp", "udp"}, nil)}}
		return bootstrap, nil
	})
	outbound.Register[membershipGroupOptions](registry, "plain-group", func(_ context.Context, _ adapter.Router, _ log.ContextLogger, tag string, _ membershipGroupOptions) (adapter.Outbound, error) {
		return &membershipGroup{Adapter: outbound.NewAdapter("selector", tag, []string{"tcp", "udp"}, nil)}, nil
	})
	manager := outbound.NewManager(registry, nil, "")
	require.NoError(t, manager.Create(ctx, nil, logger, "g", "provider-group", &membershipGroupOptions{}))
	ctx = service.ContextWith[adapter.ProviderManager](ctx, &stubProviderManager{count: 1})
	adapterUnderTest := provider.NewAdapter(ctx, nil, manager, factory, logger, "p", "inline", option.ProviderHealthCheckOptions{})

	// Fed by this provider only, no member: the download waits on itself.
	require.NotNil(t, bootstrap)
	bootstrap.providers = []string{"p"}
	err := adapterUnderTest.CheckDownloadPath("g", false)
	require.ErrorContains(t, err, "fed only by this provider")

	// A second provider could publish the member this download needs: not a certain loop.
	bootstrap.providers = []string{"p", "q"}
	require.NoError(t, adapterUnderTest.CheckDownloadPath("g", false))

	// A static member means the group is usable before the download.
	bootstrap.providers = []string{"p"}
	bootstrap.membershipGroup.setMembers("static-node")
	require.NoError(t, adapterUnderTest.CheckDownloadPath("g", false))

	// A group with no provider feed cannot be attributed to this provider.
	require.NoError(t, manager.Create(ctx, nil, logger, "plain", "plain-group", &membershipGroupOptions{}))
	require.NoError(t, adapterUnderTest.CheckDownloadPath("plain", false))
}
