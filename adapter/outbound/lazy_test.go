package outbound

import (
	"context"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// Manager-level tests for the deferred-construction seam.
//
// # Why these run through the REAL Manager
//
// The object state machine is proved in protocol/trojan; what is proved here is the part the object
// cannot prove about itself: that the manager consults the seam instead of the registry, that it
// does not hold its global mutation/namespace locks across the first expansion, and that the
// existing replacement/lease/publication contract is unchanged for a deferred object. Those are
// statements about the manager, so a test that constructed a LazyOutbound directly would prove
// nothing about them.

// deferredTestRuntime is the controlled expansion used by the manager tests. It registers a scope
// cleanup on Start, which is how this tree's outbounds own teardown.
type deferredTestRuntime struct {
	adapter.Outbound
	tag string

	startCalls  atomic.Int32
	cleanupRuns atomic.Int32
	dialCalls   atomic.Int32

	gate *dialGate
}

func (r *deferredTestRuntime) Type() string           { return "deferred-test" }
func (r *deferredTestRuntime) Tag() string            { return r.tag }
func (r *deferredTestRuntime) Network() []string      { return []string{"tcp"} }
func (r *deferredTestRuntime) Dependencies() []string { return nil }

func (r *deferredTestRuntime) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	r.startCalls.Add(1)
	if stage == adapter.StartStateInitialize {
		scope.AddNamed("deferred-test["+r.tag+"]", func() error {
			r.cleanupRuns.Add(1)
			return nil
		})
	}
	return nil
}

func (r *deferredTestRuntime) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	r.dialCalls.Add(1)
	if r.gate != nil {
		r.gate.once.Do(func() { close(r.gate.entered) })
		select {
		case <-r.gate.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	client, server := net.Pipe()
	_ = server.Close()
	return client, nil
}

func (r *deferredTestRuntime) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, os.ErrInvalid
}

type dialGate struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

// deferredFixture installs the seam on a real Manager and records every expansion it builds.
type deferredFixture struct {
	manager  *Manager
	registry *stubRegistry
	gate     *dialGate
	builds   atomic.Int32

	access   sync.Mutex
	runtimes []*deferredTestRuntime
	owners   []string
	// buildBlocker, when set, parks the BUILDER, so a test can observe a first expansion in flight.
	buildBlocker *dialGate
	// buildError, when set, makes the next builder call fail.
	buildError error
}

func newDeferredFixture(t *testing.T) *deferredFixture {
	t.Helper()
	fixture := &deferredFixture{registry: &stubRegistry{}}
	fixture.manager = NewManager(fixture.registry, &cycleEndpointManager{endpoints: map[string]adapter.Endpoint{}}, "")
	fixture.manager.Initialize(func() (adapter.Outbound, error) {
		return &stubOutbound{tag: "direct"}, nil
	})
	fixture.manager.SetDeferredOutboundConstructor(func(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, outboundType string, options any, owner string) (adapter.Outbound, bool, error) {
		if outboundType != "lazy" {
			return nil, false, nil
		}
		fixture.access.Lock()
		fixture.owners = append(fixture.owners, owner)
		fixture.access.Unlock()
		lazy := NewLazyOutbound[string](NewAdapter("lazy", tag, []string{"tcp"}, nil), logger, ctx, "recipe", func(ctx context.Context, recipe string) (adapter.Outbound, error) {
			fixture.builds.Add(1)
			if fixture.buildBlocker != nil {
				fixture.buildBlocker.once.Do(func() { close(fixture.buildBlocker.entered) })
				<-fixture.buildBlocker.release
			}
			if fixture.buildError != nil {
				err := fixture.buildError
				fixture.buildError = nil
				return nil, err
			}
			runtime := &deferredTestRuntime{tag: tag, gate: fixture.gate}
			fixture.access.Lock()
			fixture.runtimes = append(fixture.runtimes, runtime)
			fixture.access.Unlock()
			return runtime, nil
		})
		return lazy, true, nil
	})
	return fixture
}

func (f *deferredFixture) runtimesSnapshot() []*deferredTestRuntime {
	f.access.Lock()
	defer f.access.Unlock()
	return append([]*deferredTestRuntime(nil), f.runtimes...)
}

func (f *deferredFixture) ownersSnapshot() []string {
	f.access.Lock()
	defer f.access.Unlock()
	return append([]string(nil), f.owners...)
}

func (f *deferredFixture) start(t *testing.T) *adapter.Scope {
	t.Helper()
	scope := adapter.NewScope(context.Background(), log.NewNOPFactory().Logger())
	t.Cleanup(func() { _ = scope.Close() })
	for _, stage := range adapter.ListStartStages {
		require.NoError(t, f.manager.Start(stage, scope))
	}
	return scope
}

func (f *deferredFixture) create(t *testing.T, tag string) *LazyOutbound[string] {
	t.Helper()
	require.NoError(t, f.manager.Create(context.Background(), nil, log.NewNOPFactory().NewLogger("test"), tag, "lazy", nil))
	created, loaded := f.manager.Outbound(tag)
	require.True(t, loaded)
	lazy, isLazy := created.(*LazyOutbound[string])
	require.True(t, isLazy)
	return lazy
}

// TestManagerDeferredConstructorAvoidsTheRegistry is the seam's first property: when the deferred
// constructor owns the node, the eager registry is not called at all, and nothing expands at
// Create or at any start stage.
func TestManagerDeferredConstructorAvoidsTheRegistry(t *testing.T) {
	fixture := newDeferredFixture(t)
	lazy := fixture.create(t, "cold-node")
	fixture.start(t)
	require.Equal(t, []string{""}, fixture.ownersSnapshot(),
		"a static configuration node must be published with an empty scope, so a policy can tell it "+
			"apart from a provider-fed node")

	require.EqualValues(t, 0, fixture.registry.constructed.Load(),
		"the deferred path must replace the eager construction, not decorate it")
	require.Equal(t, 0, lazy.Builds(), "Create and all start stages must leave the node unexpanded")

	// Metadata from the published namespace must not expand it either.
	outbounds := fixture.manager.Outbounds()
	require.NotEmpty(t, outbounds)
	for _, published := range outbounds {
		_ = published.Tag()
		_ = published.Type()
		_ = published.Network()
		_ = published.Dependencies()
	}
	_, isExpanded := lazy.Built()
	require.False(t, isExpanded)
	require.Equal(t, 0, lazy.Builds())

	conn, err := lazy.DialContext(context.Background(), "tcp", M.Socksaddr{})
	require.NoError(t, err)
	_ = conn.Close()
	require.EqualValues(t, 1, fixture.builds.Load())
	require.EqualValues(t, 0, fixture.registry.constructed.Load())
}

// TestManagerDeferredConstructorFallsBack proves the seam is truly optional: an unhandled shape
// keeps the eager path, and a handled error is reported at the same stage.
func TestManagerDeferredConstructorFallsBack(t *testing.T) {
	registry := &stubRegistry{}
	manager := NewManager(registry, nil, "")
	manager.SetDeferredOutboundConstructor(func(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, outboundType string, options any, owner string) (adapter.Outbound, bool, error) {
		if outboundType == "handled-error" {
			return nil, true, os.ErrInvalid
		}
		return nil, false, nil
	})

	require.NoError(t, manager.Create(context.Background(), nil, log.NewNOPFactory().NewLogger("t"), "eager-node", "stub", &optionStub{}))
	require.EqualValues(t, 1, registry.constructed.Load(), "an unhandled shape must use the eager registry")

	err := manager.Create(context.Background(), nil, log.NewNOPFactory().NewLogger("t"), "refused", "handled-error", nil)
	require.ErrorIs(t, err, os.ErrInvalid)
	_, loaded := manager.Outbound("refused")
	require.False(t, loaded, "a refused construction must not be published")
}

// TestManagerBuildDoesNotHoldManagerLocks is requirement 6's lock rule, observed rather than
// asserted by inspection: while the first expansion is parked inside the builder, manager reads and
// unrelated publications must still complete.
func TestManagerBuildDoesNotHoldManagerLocks(t *testing.T) {
	fixture := newDeferredFixture(t)
	fixture.buildBlocker = &dialGate{entered: make(chan struct{}), release: make(chan struct{})}
	lazy := fixture.create(t, "slow-node")
	fixture.start(t)

	dialResult := make(chan error, 1)
	go func() {
		conn, err := lazy.DialContext(context.Background(), "tcp", M.Socksaddr{})
		if conn != nil {
			_ = conn.Close()
		}
		dialResult <- err
	}()
	<-fixture.buildBlocker.entered
	require.Equal(t, LazyStateBuilding, lazy.State())

	// A read of the namespace, and a publication of an unrelated node, must not wait for the build.
	_, loaded := fixture.manager.Outbound("slow-node")
	require.True(t, loaded)
	published := make(chan error, 1)
	go func() {
		published <- fixture.manager.Create(context.Background(), nil, log.NewNOPFactory().NewLogger("t"), "other-node", "lazy", nil)
	}()
	select {
	case err := <-published:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("a manager publication blocked behind a deferred build: the first expansion must " +
			"not run inside the manager's global lock")
	}
	close(fixture.buildBlocker.release)
	require.NoError(t, <-dialResult)
}

// TestManagerReplaceOwnedKeepsInflightLazyDial pins the existing replacement contract for a deferred
// object: the old node stays alive while a lease is held, the new node is published immediately, and
// the old expansion is released exactly once when the lease ends.
func TestManagerReplaceOwnedKeepsInflightLazyDial(t *testing.T) {
	fixture := newDeferredFixture(t)
	fixture.gate = &dialGate{entered: make(chan struct{}), release: make(chan struct{})}
	fixture.start(t)

	// First publication of a provider-owned node, then a lease on it - the state a provider-fed
	// member is in when it is about to hand a flow off.
	require.NoError(t, fixture.manager.ReplaceOwned(context.Background(), nil, log.NewNOPFactory().NewLogger("t"), "node", "lazy", nil, "provider"))
	oldObject, loaded := fixture.manager.Outbound("node")
	require.True(t, loaded)
	oldLazy := oldObject.(*LazyOutbound[string])
	releaseLease, leased := fixture.manager.AcquireOutbound(oldObject)
	require.True(t, leased)

	dialResult := make(chan error, 1)
	go func() {
		conn, err := oldLazy.DialContext(context.Background(), "tcp", M.Socksaddr{})
		if conn != nil {
			_ = conn.Close()
		}
		dialResult <- err
	}()
	<-fixture.gate.entered
	require.Equal(t, 1, oldLazy.Builds())

	// Replace under the same tag. The old object is retired, the new one is addressable at once.
	require.NoError(t, fixture.manager.ReplaceOwned(context.Background(), nil, log.NewNOPFactory().NewLogger("t"), "node", "lazy", nil, "provider"))
	newObject, loaded := fixture.manager.Outbound("node")
	require.True(t, loaded)
	require.NotSame(t, oldObject, newObject)
	require.Equal(t, []string{"provider", "provider"}, fixture.ownersSnapshot(),
		"a provider-fed publication must carry its provider scope through the seam")
	newLazy := newObject.(*LazyOutbound[string])
	require.Equal(t, 0, newLazy.Builds(), "a replacement is a descriptor until something dials it")

	// A completed publication must NOT close the old expansion while the lease is held.
	require.NoError(t, fixture.manager.CompletePublication("provider", []string{"node"}))
	require.Equal(t, LazyStateReady, oldLazy.State(),
		"the publication barrier proves the groups rebuilt, not that the hand-off finished")
	require.EqualValues(t, 0, fixture.runtimesSnapshot()[0].cleanupRuns.Load())

	// Ending the lease is what releases the old object, exactly once.
	releaseLease()
	require.Equal(t, LazyStateClosed, oldLazy.State())
	require.EqualValues(t, 1, fixture.runtimesSnapshot()[0].cleanupRuns.Load())

	// The dial that was inside the object when it was retired still succeeds: its runtime is the one
	// it obtained, and the teardown released the NODE, not the connection.
	close(fixture.gate.release)
	require.NoError(t, <-dialResult)

	// The replacement stays unexpanded until its own first demand.
	conn, err := newLazy.DialContext(context.Background(), "tcp", M.Socksaddr{})
	require.NoError(t, err)
	_ = conn.Close()
	require.Equal(t, 1, newLazy.Builds())
}

// TestManagerReplaceOwnedConstructionFailureKeepsOldNode is the failed-publication rule: a
// replacement that cannot even be described leaves the previous node serving.
func TestManagerReplaceOwnedConstructionFailureKeepsOldNode(t *testing.T) {
	fixture := newDeferredFixture(t)
	fixture.start(t)
	require.NoError(t, fixture.manager.ReplaceOwned(context.Background(), nil, log.NewNOPFactory().NewLogger("t"), "node", "lazy", nil, "provider"))
	oldObject, loaded := fixture.manager.Outbound("node")
	require.True(t, loaded)

	fixture.manager.SetDeferredOutboundConstructor(func(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, outboundType string, options any, owner string) (adapter.Outbound, bool, error) {
		return nil, true, os.ErrInvalid
	})
	err := fixture.manager.ReplaceOwned(context.Background(), nil, log.NewNOPFactory().NewLogger("t"), "node", "lazy", nil, "provider")
	require.ErrorIs(t, err, os.ErrInvalid)
	stillPublished, loaded := fixture.manager.Outbound("node")
	require.True(t, loaded)
	require.Same(t, oldObject, stillPublished, "a failed replacement must leave the working node published")
	require.Equal(t, LazyStateNeverBuilt, oldObject.(*LazyOutbound[string]).State())
}

// TestManagerRemoveReleasesExpansionAndRecreateRebuilds is the eviction/removal/rebuild cycle at the
// namespace level.
func TestManagerRemoveReleasesExpansionAndRecreateRebuilds(t *testing.T) {
	fixture := newDeferredFixture(t)
	fixture.start(t)
	require.NoError(t, fixture.manager.ReplaceOwned(context.Background(), nil, log.NewNOPFactory().NewLogger("t"), "node", "lazy", nil, "provider"))
	published, loaded := fixture.manager.Outbound("node")
	require.True(t, loaded)
	lazy := published.(*LazyOutbound[string])
	conn, err := lazy.DialContext(context.Background(), "tcp", M.Socksaddr{})
	require.NoError(t, err)
	_ = conn.Close()
	require.EqualValues(t, 1, fixture.builds.Load())

	require.NoError(t, fixture.manager.Remove("node"))
	require.Equal(t, LazyStateClosed, lazy.State())
	require.EqualValues(t, 1, fixture.runtimesSnapshot()[0].cleanupRuns.Load())

	// The same tag can be published again and expands again: eviction is safe only because the
	// recipe is complete on its own.
	require.NoError(t, fixture.manager.ReplaceOwned(context.Background(), nil, log.NewNOPFactory().NewLogger("t"), "node", "lazy", nil, "provider"))
	recreatedObject, loaded := fixture.manager.Outbound("node")
	require.True(t, loaded)
	recreated := recreatedObject.(*LazyOutbound[string])
	conn, err = recreated.DialContext(context.Background(), "tcp", M.Socksaddr{})
	require.NoError(t, err)
	_ = conn.Close()
	require.Equal(t, 1, recreated.Builds())
	require.EqualValues(t, 2, fixture.builds.Load())
}

// TestManagerSameRecipeDifferentTagsAreNotShared is the sharing-scope rule made executable: two tags
// that happen to describe the same server must not silently become one object, because a shared
// runtime needs an owner-scoped key and a generation, not just equal configuration.
func TestManagerSameRecipeDifferentTagsAreNotShared(t *testing.T) {
	fixture := newDeferredFixture(t)
	first := fixture.create(t, "provider/a")
	second := fixture.create(t, "provider/b")
	fixture.start(t)
	require.NotSame(t, first, second)

	conn, err := first.DialContext(context.Background(), "tcp", M.Socksaddr{})
	require.NoError(t, err)
	_ = conn.Close()
	conn, err = second.DialContext(context.Background(), "tcp", M.Socksaddr{})
	require.NoError(t, err)
	_ = conn.Close()

	require.Equal(t, 1, first.Builds())
	require.Equal(t, 1, second.Builds())
	require.EqualValues(t, 2, fixture.builds.Load(), "identical recipes must not share one runtime")
	require.Len(t, fixture.runtimesSnapshot(), 2)
}

// TestManagerDeferredConstructorIsPerManager keeps the opt-in honest: a manager that never installs
// the seam cannot be affected by another manager that did.
func TestManagerDeferredConstructorIsPerManager(t *testing.T) {
	fixture := newDeferredFixture(t)
	_ = fixture.create(t, "deferred")
	fixture.start(t)

	plainRegistry := &stubRegistry{}
	plain := NewManager(plainRegistry, nil, "")
	require.NoError(t, plain.Create(context.Background(), nil, log.NewNOPFactory().NewLogger("t"), "eager", "stub", &optionStub{}))
	require.EqualValues(t, 1, plainRegistry.constructed.Load(),
		"the deferred seam is per manager, not a process-wide switch")
}
