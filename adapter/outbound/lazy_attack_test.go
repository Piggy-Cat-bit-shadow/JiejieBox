package outbound

// LN attack suite: the deferred state machine, the hand-off lease and the eviction window, attacked
// with BARRIERS instead of scheduling luck.
//
// # Why every collision here is produced by a barrier
//
// The defects this file is looking for are windows: a Close that lands between a successful build and
// its Start, an eviction that begins while the manager has already handed the object out, a provider
// replacement that races a first expansion. A test that merely runs two goroutines and hopes they
// collide passes for the wrong reason on a fast machine and fails on a loaded CI machine, so every
// case below pins the interleaving with a gate:
//
//	buildGate      parks the builder itself (state == building).
//	startGate      parks Start of a stage AFTER Initialize (a started object in flight).
//	cleanupGate    parks the scope cleanup (a teardown in progress: the eviction window).
//	waiterProbe    reports the first call to Done(), i.e. the exact moment a demand enters the
//	               settled/ctx select in acquireRuntime, which is how "this demand is now waiting"
//	               becomes observable instead of being inferred from a sleep.
//
// # What is asserted
//
// Only counted facts: build count, initialize count, cleanup run count, direct-close count, lease
// count, connection count, lifecycle state. No assertion depends on how long a goroutine took, except
// the two liveness bounds whose whole point is "this call must not wait for that teardown" - and even
// there the bound is a failure detector, not the oracle.

import (
	"context"
	"errors"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// attackGate is a one-shot barrier: enter() announces the first arrival and blocks every caller until
// open(). open() is idempotent and safe from a t.Cleanup, which is what keeps a failing assertion from
// hanging the package on a blocked cleanup.
type attackGate struct {
	entered     chan struct{}
	release     chan struct{}
	enterOnce   sync.Once
	releaseOnce sync.Once
}

func newAttackGate() *attackGate {
	return &attackGate{entered: make(chan struct{}), release: make(chan struct{})}
}

func (g *attackGate) enter() {
	g.enterOnce.Do(func() { close(g.entered) })
	<-g.release
}

func (g *attackGate) open() {
	g.releaseOnce.Do(func() { close(g.release) })
}

func (g *attackGate) waitEntered(t *testing.T) {
	t.Helper()
	select {
	case <-g.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the barrier was never reached: the expected transition did not happen")
	}
}

// waiterProbeContext makes "this demand is inside the wait select" observable. Done() is the channel
// the select evaluates on entry, so the first call is the exact point a demand starts waiting; Err()
// is delegated and is what the entry check reads.
type waiterProbeContext struct {
	context.Context
	once sync.Once
	done chan struct{}
}

func newWaiterProbe(parent context.Context) *waiterProbeContext {
	return &waiterProbeContext{Context: parent, done: make(chan struct{})}
}

func (c *waiterProbeContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.done) })
	return c.Context.Done()
}

func (c *waiterProbeContext) waitForWait(t *testing.T) {
	t.Helper()
	select {
	case <-c.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the demand never reached the wait select")
	}
}

// attackRuntime is the controlled expansion. It is deliberately BOTH adapter.Lifecycle and closable:
// that is the shape where the ownership rule matters ("a started object is released through its
// per-build scope; an object that was never started is closed directly"), and a test double that had
// only one of the two would not be able to tell the two rules apart.
type attackRuntime struct {
	adapter.Outbound
	tag string

	initCalls   atomic.Int32
	startCalls  atomic.Int32
	cleanupRuns atomic.Int32
	closeCalls  atomic.Int32
	dialCalls   atomic.Int32
	packetCalls atomic.Int32

	startGate      *attackGate
	startFailStage adapter.StartStage
	startFailErr   error
	cleanupGate    *attackGate
	cleanupHook    func()
	packetConn     *attackPacketConn
}

func (r *attackRuntime) Type() string           { return "attack" }
func (r *attackRuntime) Tag() string            { return r.tag }
func (r *attackRuntime) Network() []string      { return []string{"tcp", "udp"} }
func (r *attackRuntime) Dependencies() []string { return nil }

func (r *attackRuntime) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	r.startCalls.Add(1)
	if stage == adapter.StartStateInitialize {
		r.initCalls.Add(1)
		scope.AddNamed("attack-runtime["+r.tag+"]", func() error {
			if r.cleanupGate != nil {
				r.cleanupGate.enter()
			}
			if r.cleanupHook != nil {
				r.cleanupHook()
			}
			r.cleanupRuns.Add(1)
			return nil
		})
		return nil
	}
	if r.startGate != nil {
		r.startGate.enter()
	}
	if r.startFailErr != nil && r.startFailStage != 0 && stage >= r.startFailStage {
		return r.startFailErr
	}
	return nil
}

// Close is the direct-release path. It must be reached EXACTLY when the object was never started
// (no per-build scope exists), and never on top of a scope that already released the same resources.
func (r *attackRuntime) Close() error {
	r.closeCalls.Add(1)
	return nil
}

func (r *attackRuntime) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	r.dialCalls.Add(1)
	client, server := net.Pipe()
	// The peer is dropped: nothing in these cases reads or writes through the connection, they only
	// assert that the hand-off produced one. Closing the peer keeps the process handle-free (net.Pipe
	// is in memory), so a long loop's handle count cannot drift because of the fixture.
	_ = server.Close()
	return client, nil
}

func (r *attackRuntime) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	r.packetCalls.Add(1)
	if r.packetConn == nil {
		return nil, os.ErrInvalid
	}
	return r.packetConn, nil
}

// attackPacketConn is a real N.NetPacketConn: that is the interface the lazy layer's conns counter
// follows, and the point of the packet case is that an OPEN listener must pin the expansion exactly
// like an open stream does.
type attackPacketConn struct {
	closed  atomic.Int32
	onClose func()
}

var _ N.NetPacketConn = (*attackPacketConn)(nil)

func (c *attackPacketConn) ReadPacket(buffer *buf.Buffer) (M.Socksaddr, error) {
	return M.Socksaddr{}, os.ErrDeadlineExceeded
}

func (c *attackPacketConn) WritePacket(buffer *buf.Buffer, destination M.Socksaddr) error {
	return nil
}

func (c *attackPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	return 0, nil, os.ErrDeadlineExceeded
}

func (c *attackPacketConn) WriteTo(p []byte, addr net.Addr) (int, error) { return len(p), nil }

func (c *attackPacketConn) Close() error {
	c.closed.Add(1)
	if c.onClose != nil {
		c.onClose()
	}
	return nil
}

func (c *attackPacketConn) LocalAddr() net.Addr              { return &net.UDPAddr{} }
func (c *attackPacketConn) SetDeadline(time.Time) error      { return nil }
func (c *attackPacketConn) SetReadDeadline(time.Time) error  { return nil }
func (c *attackPacketConn) SetWriteDeadline(time.Time) error { return nil }

// attackConfig is the mutable half of the fixture: it is read by the builder, so every field is
// guarded by attackFixture.access.
type attackConfig struct {
	buildGate      *attackGate
	startGate      *attackGate
	cleanupGate    *attackGate
	cleanupHook    func()
	builderErr     error
	nilBuild       bool
	startFailStage adapter.StartStage
	startFailErr   error
	packetConn     *attackPacketConn
}

type attackFixture struct {
	manager  *Manager
	registry *stubRegistry

	access   sync.Mutex
	config   attackConfig
	runtimes []*attackRuntime
	builds   atomic.Int32
}

func newAttackFixture(t *testing.T) *attackFixture {
	t.Helper()
	fixture := &attackFixture{registry: &stubRegistry{}}
	fixture.manager = NewManager(fixture.registry, &cycleEndpointManager{endpoints: map[string]adapter.Endpoint{}}, "")
	fixture.manager.Initialize(func() (adapter.Outbound, error) {
		return &stubOutbound{tag: "direct"}, nil
	})
	fixture.manager.SetDeferredOutboundConstructor(func(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, outboundType string, options any, owner string) (adapter.Outbound, bool, error) {
		if outboundType != "attack" {
			return nil, false, nil
		}
		deferred := NewLazyOutbound[string](NewAdapter("attack", tag, []string{"tcp", "udp"}, nil), logger, ctx, "recipe",
			func(buildContext context.Context, recipe string) (adapter.Outbound, error) {
				return fixture.build(tag)
			})
		return deferred, true, nil
	})
	return fixture
}

func (f *attackFixture) build(tag string) (adapter.Outbound, error) {
	f.builds.Add(1)
	f.access.Lock()
	config := f.config
	f.config.builderErr = nil
	f.access.Unlock()
	if config.buildGate != nil {
		config.buildGate.enter()
	}
	if config.builderErr != nil {
		return nil, config.builderErr
	}
	if config.nilBuild {
		return nil, nil
	}
	runtime := &attackRuntime{
		tag:            tag,
		startGate:      config.startGate,
		startFailStage: config.startFailStage,
		startFailErr:   config.startFailErr,
		cleanupGate:    config.cleanupGate,
		cleanupHook:    config.cleanupHook,
		packetConn:     config.packetConn,
	}
	f.access.Lock()
	f.runtimes = append(f.runtimes, runtime)
	f.access.Unlock()
	return runtime, nil
}

func (f *attackFixture) configure(mutate func(config *attackConfig)) {
	f.access.Lock()
	mutate(&f.config)
	f.access.Unlock()
}

func (f *attackFixture) runtimesSnapshot() []*attackRuntime {
	f.access.Lock()
	defer f.access.Unlock()
	return append([]*attackRuntime(nil), f.runtimes...)
}

// gate installs a config field and registers its release as a cleanup, so a failing assertion cannot
// leave the package hanging on a blocked barrier.
func (f *attackFixture) gate(t *testing.T, install func(config *attackConfig, gate *attackGate)) *attackGate {
	t.Helper()
	gate := newAttackGate()
	f.configure(func(config *attackConfig) { install(config, gate) })
	t.Cleanup(gate.open)
	return gate
}

func (f *attackFixture) startAll(t *testing.T) *adapter.Scope {
	t.Helper()
	scope := adapter.NewScope(context.Background(), log.NewNOPFactory().Logger())
	t.Cleanup(func() { _ = scope.Close() })
	for _, stage := range adapter.ListStartStages {
		require.NoError(t, f.manager.Start(stage, scope))
	}
	return scope
}

func (f *attackFixture) create(t *testing.T, tag string) *LazyOutbound[string] {
	t.Helper()
	require.NoError(t, f.manager.Create(context.Background(), nil, log.NewNOPFactory().NewLogger("attack"), tag, "attack", nil))
	object, loaded := f.manager.Outbound(tag)
	require.True(t, loaded)
	deferred, isDeferred := object.(*LazyOutbound[string])
	require.True(t, isDeferred)
	return deferred
}

func (f *attackFixture) pending() int64 {
	return f.manager.deferredReclaim.pending.Load()
}

// expand dials once and closes the connection: the same thing a URLTest round does to a node, so the
// node ends up published, expanded and idle.
func expandAttack(t *testing.T, deferred *LazyOutbound[string]) {
	t.Helper()
	conn, err := deferred.DialContext(context.Background(), "tcp", M.Socksaddr{})
	require.NoError(t, err)
	require.NoError(t, conn.Close())
}

func dialAsync(deferred *LazyOutbound[string], ctx context.Context) <-chan dialOutcome {
	result := make(chan dialOutcome, 1)
	go func() {
		conn, err := deferred.DialContext(ctx, "tcp", M.Socksaddr{})
		result <- dialOutcome{conn: conn, err: err}
	}()
	return result
}

func awaitOutcome(t *testing.T, result <-chan dialOutcome) dialOutcome {
	t.Helper()
	select {
	case outcome := <-result:
		if outcome.conn != nil {
			require.NoError(t, outcome.conn.Close())
		}
		return outcome
	case <-time.After(10 * time.Second):
		t.Fatal("the demand never returned")
		return dialOutcome{}
	}
}

// within runs fn and fails if it does not return: the failure detector for "this call must not wait
// for that teardown". The oracle is fn's own assertions, not the duration.
func within(t *testing.T, timeout time.Duration, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatalf("%s did not return within %s: it waited for a teardown it must not wait for", what, timeout)
	}
}

// TestAttackTwoFirstDemandsShareOneBuild: two demands arrive while the first build is parked. The
// second one must WAIT (not start a second build), and both must be served by the single expansion.
func TestAttackTwoFirstDemandsShareOneBuild(t *testing.T) {
	fixture := newAttackFixture(t)
	deferred := fixture.create(t, "two-first")
	fixture.startAll(t)
	buildGate := fixture.gate(t, func(config *attackConfig, gate *attackGate) { config.buildGate = gate })

	first := dialAsync(deferred, context.Background())
	buildGate.waitEntered(t)
	require.Equal(t, LazyStateBuilding, deferred.State())

	probe := newWaiterProbe(context.Background())
	second := dialAsync(deferred, probe)
	probe.waitForWait(t)
	require.Equal(t, int32(1), fixture.builds.Load(), "the second demand must wait, not build")

	buildGate.open()
	require.NoError(t, awaitOutcome(t, first).err)
	require.NoError(t, awaitOutcome(t, second).err)
	require.EqualValues(t, 1, fixture.builds.Load(), "one expansion serves both demands")
	require.Equal(t, LazyStateReady, deferred.State())
	observation := deferred.ExpansionObservation()
	require.Equal(t, 0, observation.Dialing, "both hand-offs ended")
	require.Equal(t, 0, observation.Conns)
	require.Equal(t, 0, observation.Leased)
}

// TestAttackWaiterCancellationDoesNotAbandonTheBuild: the waiter is bounded by ITS context, and its
// cancellation must not damage the build that the other demand owns.
func TestAttackWaiterCancellationDoesNotAbandonTheBuild(t *testing.T) {
	fixture := newAttackFixture(t)
	deferred := fixture.create(t, "cancel-waiter")
	fixture.startAll(t)
	buildGate := fixture.gate(t, func(config *attackConfig, gate *attackGate) { config.buildGate = gate })

	owner := dialAsync(deferred, context.Background())
	buildGate.waitEntered(t)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	probe := newWaiterProbe(ctx)
	waiter := dialAsync(deferred, probe)
	probe.waitForWait(t)

	outcome := awaitOutcome(t, waiter)
	require.ErrorIs(t, outcome.err, context.DeadlineExceeded, "the waiter must get its own deadline")
	require.NotErrorIs(t, outcome.err, os.ErrClosed)
	require.Equal(t, LazyStateBuilding, deferred.State(), "one cancelled waiter must not abort the build")

	buildGate.open()
	require.NoError(t, awaitOutcome(t, owner).err)
	require.EqualValues(t, 1, fixture.builds.Load())
	require.Equal(t, LazyStateReady, deferred.State())
}

// TestAttackBuildFailureIsSharedAndRetryable: the first build fails while a second demand is waiting.
// The waiter must be told the SAME failure (not os.ErrClosed, and not a second build), and the next
// demand must retry successfully.
func TestAttackBuildFailureIsSharedAndRetryable(t *testing.T) {
	fixture := newAttackFixture(t)
	deferred := fixture.create(t, "shared-failure")
	fixture.startAll(t)
	buildGate := fixture.gate(t, func(config *attackConfig, gate *attackGate) { config.buildGate = gate })
	buildFailure := errors.New("attack: the first build failed")
	fixture.configure(func(config *attackConfig) { config.builderErr = buildFailure })

	builder := dialAsync(deferred, context.Background())
	buildGate.waitEntered(t)

	probe := newWaiterProbe(context.Background())
	waiter := dialAsync(deferred, probe)
	probe.waitForWait(t)

	buildGate.open()
	builderOutcome := awaitOutcome(t, builder)
	waiterOutcome := awaitOutcome(t, waiter)
	require.ErrorIs(t, builderOutcome.err, buildFailure)
	require.ErrorIs(t, waiterOutcome.err, buildFailure,
		"the waiter must receive the build's error, not a closed-node error")
	require.NotErrorIs(t, waiterOutcome.err, os.ErrClosed)
	require.EqualValues(t, 1, fixture.builds.Load(), "the waiter must not start a second doomed build")
	require.Equal(t, LazyStateNeverBuilt, deferred.State())

	retry := awaitOutcome(t, dialAsync(deferred, context.Background()))
	require.NoError(t, retry.err)
	require.EqualValues(t, 2, fixture.builds.Load())
	require.Equal(t, LazyStateReady, deferred.State())
}

// TestAttackCloseDuringStartReleasesTheStartedScopeExactlyOnce is the transition that only exists
// because a build has two halves: the constructor returned, Start is running, and Close arrives. The
// node must not be published, Close must not wait for Start, and the object that WAS started must be
// released through its per-build scope exactly once - never also through a direct close.
func TestAttackCloseDuringStartReleasesTheStartedScopeExactlyOnce(t *testing.T) {
	fixture := newAttackFixture(t)
	deferred := fixture.create(t, "close-in-start")
	fixture.startAll(t)
	startGate := fixture.gate(t, func(config *attackConfig, gate *attackGate) { config.startGate = gate })

	result := dialAsync(deferred, context.Background())
	startGate.waitEntered(t)
	require.Equal(t, LazyStateBuilding, deferred.State())
	require.Len(t, fixture.runtimesSnapshot(), 1)
	runtime := fixture.runtimesSnapshot()[0]
	require.EqualValues(t, 1, runtime.initCalls.Load(), "Initialize already registered the cleanup")
	require.EqualValues(t, 0, runtime.cleanupRuns.Load())

	// Close is accepted immediately: it must not block on a Start whose duration nothing bounds.
	within(t, 5*time.Second, "Close during Start", func() {
		require.NoError(t, deferred.Close())
	})
	require.Equal(t, LazyStateRetiring, deferred.State())

	startGate.open()
	outcome := awaitOutcome(t, result)
	require.ErrorIs(t, outcome.err, os.ErrClosed, "a build retired before Start must not publish")
	require.Nil(t, outcome.conn)
	require.Equal(t, LazyStateClosed, deferred.State())
	require.EqualValues(t, 1, runtime.cleanupRuns.Load(), "the started scope's cleanup runs exactly once")
	require.EqualValues(t, 0, runtime.closeCalls.Load(),
		"a started object is released through its scope, not also directly")
	require.EqualValues(t, 0, runtime.packetCalls.Load())

	closedDial := awaitOutcome(t, dialAsync(deferred, context.Background()))
	require.ErrorIs(t, closedDial.err, os.ErrClosed)
	require.EqualValues(t, 1, fixture.builds.Load(), "a closed node never rebuilds")
}

// TestAttackBuilderNilResultIsAnErrorAndRetryable: a builder that returns (nil, nil) is a broken
// builder, not a successful empty node. It must be an error, the state must stay retryable, and the
// retry must publish normally.
func TestAttackBuilderNilResultIsAnErrorAndRetryable(t *testing.T) {
	fixture := newAttackFixture(t)
	deferred := fixture.create(t, "nil-build")
	fixture.startAll(t)
	fixture.configure(func(config *attackConfig) { config.nilBuild = true })

	outcome := awaitOutcome(t, dialAsync(deferred, context.Background()))
	require.ErrorContains(t, outcome.err, "builder returned no outbound")
	require.NotErrorIs(t, outcome.err, os.ErrClosed, "a broken builder is not a closed node")
	require.Equal(t, LazyStateNeverBuilt, deferred.State())
	require.EqualValues(t, 1, fixture.builds.Load())

	fixture.configure(func(config *attackConfig) { config.nilBuild = false })
	retry := awaitOutcome(t, dialAsync(deferred, context.Background()))
	require.NoError(t, retry.err)
	require.Equal(t, LazyStateReady, deferred.State())
	require.EqualValues(t, 2, fixture.builds.Load())
}

// TestAttackTwoEvictsReleaseExactlyOnce: two evictions race for one expansion. Exactly one may win,
// the loser must not touch the teardown, and the cleanup must run once.
func TestAttackTwoEvictsReleaseExactlyOnce(t *testing.T) {
	fixture := newAttackFixture(t)
	deferred := fixture.create(t, "double-evict")
	fixture.startAll(t)
	// The gate has to exist BEFORE the build: the runtime snapshots the configuration when it is
	// created, and the cleanup it registers is what the gate parks.
	cleanupGate := fixture.gate(t, func(config *attackConfig, gate *attackGate) { config.cleanupGate = gate })
	expandAttack(t, deferred)
	require.EqualValues(t, 1, fixture.builds.Load())

	first := make(chan bool, 1)
	go func() { first <- deferred.Evict() }()
	cleanupGate.waitEntered(t)
	require.Equal(t, LazyStateRetiring, deferred.State())

	second := make(chan bool, 1)
	go func() { second <- deferred.Evict() }()
	select {
	case evicted := <-second:
		require.False(t, evicted, "a second eviction during the teardown must be refused")
	case <-time.After(5 * time.Second):
		t.Fatal("the second eviction blocked behind the first teardown")
	}

	cleanupGate.open()
	require.True(t, <-first)
	require.Equal(t, LazyStateNeverBuilt, deferred.State(), "an eviction returns the node to its recipe")
	runtimes := fixture.runtimesSnapshot()
	require.Len(t, runtimes, 1)
	require.EqualValues(t, 1, runtimes[0].cleanupRuns.Load(), "exactly one cleanup for one expansion")

	conn, err := deferred.DialContext(context.Background(), "tcp", M.Socksaddr{})
	require.NoError(t, err)
	require.NoError(t, conn.Close())
	require.EqualValues(t, 2, fixture.builds.Load(), "the evicted node rebuilds on demand")
}

// TestAttackEvictRefusedOnNeverBuiltOrBuilding: eviction only ever applies to a published expansion.
// A recipe-only node and a node whose build is in flight must both be refused without side effects.
func TestAttackEvictRefusedOnNeverBuiltOrBuilding(t *testing.T) {
	fixture := newAttackFixture(t)
	deferred := fixture.create(t, "evict-not-ready")
	fixture.startAll(t)
	require.False(t, deferred.Evict(), "a never-built node has nothing to evict")
	require.EqualValues(t, 0, fixture.builds.Load())

	buildGate := fixture.gate(t, func(config *attackConfig, gate *attackGate) { config.buildGate = gate })
	result := dialAsync(deferred, context.Background())
	buildGate.waitEntered(t)
	require.False(t, deferred.Evict(), "a building node must not be evicted")
	require.Equal(t, LazyStateBuilding, deferred.State())

	buildGate.open()
	require.NoError(t, awaitOutcome(t, result).err)
	require.True(t, deferred.Evict())
	require.Equal(t, LazyStateNeverBuilt, deferred.State())
}

// TestAttackCleanupReentrancyDoesNotSelfLock is the "Close waits for a cleanup in the same scope"
// trap: the cleanup calls Close on the very node whose teardown is running. It must be a no-op that
// marks the node terminal, never a second wait on the drain the cleanup is inside.
func TestAttackCleanupReentrancyDoesNotSelfLock(t *testing.T) {
	fixture := newAttackFixture(t)
	deferred := fixture.create(t, "reentrant-cleanup")
	fixture.startAll(t)
	reentry := make(chan error, 1)
	// Configured before the build: the runtime copies the hook when it is created.
	fixture.configure(func(config *attackConfig) {
		config.cleanupHook = func() {
			// The second Close marks the node terminal; it must not wait for this drain.
			reentry <- deferred.Close()
		}
	})
	expandAttack(t, deferred)

	evicted := make(chan bool, 1)
	within(t, 5*time.Second, "Evict with a re-entrant Close cleanup", func() {
		evicted <- deferred.Evict()
	})
	require.True(t, <-evicted)
	select {
	case err := <-reentry:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the cleanup's re-entrant Close never returned")
	}
	require.Equal(t, LazyStateClosed, deferred.State(),
		"the re-entrant Close is terminal: the eviction's tail must not revive the node")
	require.EqualValues(t, 1, fixture.runtimesSnapshot()[0].cleanupRuns.Load())

	closedDial := awaitOutcome(t, dialAsync(deferred, context.Background()))
	require.ErrorIs(t, closedDial.err, os.ErrClosed)
	require.EqualValues(t, 1, fixture.builds.Load(), "a closed node never rebuilds")
}

// TestAttackManagerHandoffInsideTheEvictionWindowIsServed is the contract a real caller depends on:
// the router obtains the object, takes the manager lease, and only then dials. If an eviction is in
// progress, the hand-off must WAIT for it and be served by a rebuild - never os.ErrClosed - while the
// manager stays fully usable for everything else.
func TestAttackManagerHandoffInsideTheEvictionWindowIsServed(t *testing.T) {
	fixture := newAttackFixture(t)
	deferred := fixture.create(t, "handoff-in-window")
	fixture.startAll(t)
	cleanupGate := fixture.gate(t, func(config *attackConfig, gate *attackGate) { config.cleanupGate = gate })
	expandAttack(t, deferred)

	eviction := make(chan bool, 1)
	go func() { eviction <- deferred.Evict() }()
	cleanupGate.waitEntered(t)
	require.Equal(t, LazyStateRetiring, deferred.State())

	// The manager must stay usable while one node's teardown is parked: a read, an unrelated
	// publication and a hand-off on another node all have to complete.
	within(t, 5*time.Second, "Outbounds during a dataplane teardown", func() {
		require.NotEmpty(t, fixture.manager.Outbounds())
	})
	within(t, 5*time.Second, "an unrelated publication during a dataplane teardown", func() {
		require.NoError(t, fixture.manager.Create(context.Background(), nil, log.NewNOPFactory().NewLogger("attack"), "sibling", "attack", nil))
	})
	sibling, loaded := fixture.manager.Outbound("sibling")
	require.True(t, loaded)
	within(t, 5*time.Second, "an unrelated hand-off during a dataplane teardown", func() {
		release, ok := fixture.manager.AcquireOutbound(sibling)
		require.True(t, ok)
		release()
	})

	// The real chain: read the object from the namespace, lease it, then dial.
	type chainResult struct {
		outcome dialOutcome
		release func()
	}
	chain := make(chan chainResult, 1)
	probe := newWaiterProbe(context.Background())
	go func() {
		object, ok := fixture.manager.Outbound("handoff-in-window")
		if !ok {
			chain <- chainResult{outcome: dialOutcome{err: os.ErrNotExist}}
			return
		}
		release, leased := fixture.manager.AcquireOutbound(object)
		if !leased {
			chain <- chainResult{outcome: dialOutcome{err: os.ErrClosed}}
			return
		}
		conn, err := object.DialContext(probe, "tcp", M.Socksaddr{})
		chain <- chainResult{outcome: dialOutcome{conn: conn, err: err}, release: release}
	}()

	probe.waitForWait(t)
	cleanupGate.open()
	require.True(t, <-eviction)

	result := <-chain
	if result.release != nil {
		defer result.release()
	}
	require.NoError(t, result.outcome.err, "the hand-off must be served by a rebuild, never refused")
	require.NotErrorIs(t, result.outcome.err, os.ErrClosed)
	require.NotNil(t, result.outcome.conn)
	require.NoError(t, result.outcome.conn.Close())
	require.EqualValues(t, 2, fixture.builds.Load(), "the waiting hand-off rebuilt the node")
	require.Equal(t, LazyStateReady, deferred.State())
}

// TestAttackEvictRefusedBetweenManagerAcquireAndDial pins the window that the object cannot see by
// itself: the manager has taken its lease (and already counted it on the object) but the dialer has
// not reached DialContext. The manager's own hook parks the acquisition between its read and its
// exclusive section - the exact place where the previous implementation had a hole - and eviction
// must be refused for the whole window.
func TestAttackEvictRefusedBetweenManagerAcquireAndDial(t *testing.T) {
	fixture := newAttackFixture(t)
	fixture.startAll(t)
	logger := log.NewNOPFactory().NewLogger("attack")
	require.NoError(t, fixture.manager.ReplaceOwned(context.Background(), nil, logger, "leased-window", "attack", nil, "provider"))
	published, loaded := fixture.manager.Outbound("leased-window")
	require.True(t, loaded)
	deferred := published.(*LazyOutbound[string])
	expandAttack(t, deferred)

	entered := make(chan struct{})
	resume := make(chan struct{})
	fixture.manager.testAcquireAfterRead = func() {
		close(entered)
		<-resume
	}
	defer func() { fixture.manager.testAcquireAfterRead = nil }()

	type acquireResult struct {
		release func()
		ok      bool
	}
	acquired := make(chan acquireResult, 1)
	go func() {
		release, ok := fixture.manager.AcquireOutbound(deferred)
		acquired <- acquireResult{release: release, ok: ok}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("AcquireOutbound never reached the post-read hook")
	}

	// The object lease is taken before the manager even looks at its namespace, so from this instant
	// the expansion cannot be released - even though no dial has begun and no connection exists.
	observation := deferred.ExpansionObservation()
	require.Equal(t, 1, observation.Leased, "the hand-off lease is visible on the object")
	require.Equal(t, 0, observation.Dialing)
	require.Equal(t, 0, observation.Conns)
	require.False(t, deferred.Evict(), "an outstanding hand-off lease forbids eviction (P05)")

	close(resume)
	result := <-acquired
	require.True(t, result.ok)
	require.NotNil(t, result.release)

	conn, err := deferred.DialContext(context.Background(), "tcp", M.Socksaddr{})
	require.NoError(t, err)
	require.NoError(t, conn.Close())
	require.EqualValues(t, 1, fixture.builds.Load(), "the lease must not force a rebuild")

	result.release()
	require.True(t, deferred.Evict(), "after the hand-off the expansion is idle and reclaimable")
}

// TestAttackEvictRefusedWhileAPacketListenerIsHeld is the UDP half of the pin: an open packet
// listener is not idle, and a second Close of the returned listener must not double-release.
func TestAttackEvictRefusedWhileAPacketListenerIsHeld(t *testing.T) {
	fixture := newAttackFixture(t)
	packetConn := &attackPacketConn{}
	fixture.configure(func(config *attackConfig) { config.packetConn = packetConn })
	deferred := fixture.create(t, "udp-holder")
	fixture.startAll(t)

	listener, err := deferred.ListenPacket(context.Background(), M.Socksaddr{})
	require.NoError(t, err)
	require.NotNil(t, listener)
	require.Implements(t, (*N.NetPacketConn)(nil), listener,
		"the wrapper must keep the capability the copy engine asserts on the packet path")
	observation := deferred.ExpansionObservation()
	require.True(t, observation.Expanded)
	require.Equal(t, 1, observation.Conns, "an open packet listener pins the expansion")
	require.False(t, deferred.Evict(), "a held UDP listener is not idle")

	require.NoError(t, listener.Close())
	require.EqualValues(t, 1, packetConn.closed.Load())
	require.NoError(t, listener.Close(), "closing twice is harmless")
	require.Equal(t, 0, deferred.ExpansionObservation().Conns)
	require.True(t, deferred.Evict())
}

// TestAttackEvictRefusedWhileAnOldProviderObjectDrains: during a provider refresh the old object is
// parked until its hand-off lease ends. Its expansion is being handed over, so eviction must refuse;
// the publication barrier alone is not permission to release it.
func TestAttackEvictRefusedWhileAnOldProviderObjectDrains(t *testing.T) {
	fixture := newAttackFixture(t)
	fixture.startAll(t)
	logger := log.NewNOPFactory().NewLogger("attack")
	require.NoError(t, fixture.manager.ReplaceOwned(context.Background(), nil, logger, "draining", "attack", nil, "provider"))
	published, loaded := fixture.manager.Outbound("draining")
	require.True(t, loaded)
	old := published.(*LazyOutbound[string])
	expandAttack(t, old)

	release, ok := fixture.manager.AcquireOutbound(old)
	require.True(t, ok)

	require.NoError(t, fixture.manager.ReplaceOwned(context.Background(), nil, logger, "draining", "attack", nil, "provider"))
	require.NoError(t, fixture.manager.CompletePublication("provider", []string{"draining"}))
	require.Equal(t, LazyStateReady, old.State(), "a leased hand-off keeps the retired object open")
	require.False(t, old.Evict(), "the retired object's expansion is still being handed over")

	replacementObject, loaded := fixture.manager.Outbound("draining")
	require.True(t, loaded)
	require.NotSame(t, published, replacementObject)

	release()
	require.Equal(t, LazyStateClosed, old.State(), "the last lease ends the retirement")
	require.EqualValues(t, 1, fixture.runtimesSnapshot()[0].cleanupRuns.Load())

	replacement := replacementObject.(*LazyOutbound[string])
	require.Equal(t, 0, replacement.Builds(), "the replacement is a descriptor until something dials it")
	replacementConn, err := replacement.DialContext(context.Background(), "tcp", M.Socksaddr{})
	require.NoError(t, err)
	require.NoError(t, replacementConn.Close())
	require.Equal(t, 1, replacement.Builds())
}

// TestAttackProviderReplaceDuringFirstBuildDiscardsTheLateResult: the provider replaces the tag while
// the first expansion is still being constructed. The late result must not be published, must be
// released exactly once by the ownership rule (never started -> closed directly, no scope double
// close), and the replacement must serve traffic.
func TestAttackProviderReplaceDuringFirstBuildDiscardsTheLateResult(t *testing.T) {
	fixture := newAttackFixture(t)
	fixture.startAll(t)
	buildGate := fixture.gate(t, func(config *attackConfig, gate *attackGate) { config.buildGate = gate })
	logger := log.NewNOPFactory().NewLogger("attack")

	require.NoError(t, fixture.manager.ReplaceOwned(context.Background(), nil, logger, "replaced", "attack", nil, "provider"))
	published, loaded := fixture.manager.Outbound("replaced")
	require.True(t, loaded)
	old := published.(*LazyOutbound[string])

	result := dialAsync(old, context.Background())
	buildGate.waitEntered(t)
	require.Equal(t, LazyStateBuilding, old.State())

	require.NoError(t, fixture.manager.ReplaceOwned(context.Background(), nil, logger, "replaced", "attack", nil, "provider"))
	require.NoError(t, fixture.manager.CompletePublication("provider", []string{"replaced"}))
	require.Equal(t, LazyStateRetiring, old.State(), "the barrier retired the object during its build")

	buildGate.open()
	outcome := awaitOutcome(t, result)
	require.ErrorIs(t, outcome.err, os.ErrClosed, "a late result must not be published")
	require.Nil(t, outcome.conn)
	require.Equal(t, LazyStateClosed, old.State())

	runtimes := fixture.runtimesSnapshot()
	require.Len(t, runtimes, 1)
	require.EqualValues(t, 1, runtimes[0].closeCalls.Load(),
		"an object that was never started is closed directly, exactly once")
	require.EqualValues(t, 0, runtimes[0].cleanupRuns.Load(), "no scope owns an unstarted object")
	require.EqualValues(t, 0, runtimes[0].startCalls.Load(), "Start must not run for a retired build")

	replacementObject, loaded := fixture.manager.Outbound("replaced")
	require.True(t, loaded)
	replacement := replacementObject.(*LazyOutbound[string])
	require.Equal(t, 0, replacement.Builds())
	conn, err := replacement.DialContext(context.Background(), "tcp", M.Socksaddr{})
	require.NoError(t, err)
	require.NoError(t, conn.Close())
	require.Equal(t, 1, replacement.Builds(), "the replacement expands on its own first demand")
}

// TestAttackProviderReplaceWaitsForTheLeasedHandoffInsideTheBuild is P06's correct interleaving: a
// hand-off that already obtained the old object (and is inside its build) must be allowed to finish
// before the retirement closes it, while the replacement is published immediately.
func TestAttackProviderReplaceWaitsForTheLeasedHandoffInsideTheBuild(t *testing.T) {
	fixture := newAttackFixture(t)
	fixture.startAll(t)
	buildGate := fixture.gate(t, func(config *attackConfig, gate *attackGate) { config.buildGate = gate })
	logger := log.NewNOPFactory().NewLogger("attack")

	require.NoError(t, fixture.manager.ReplaceOwned(context.Background(), nil, logger, "leased-build", "attack", nil, "provider"))
	published, loaded := fixture.manager.Outbound("leased-build")
	require.True(t, loaded)
	old := published.(*LazyOutbound[string])

	release, ok := fixture.manager.AcquireOutbound(old)
	require.True(t, ok)

	result := dialAsync(old, context.Background())
	buildGate.waitEntered(t)
	require.Equal(t, LazyStateBuilding, old.State())

	require.NoError(t, fixture.manager.ReplaceOwned(context.Background(), nil, logger, "leased-build", "attack", nil, "provider"))
	require.NoError(t, fixture.manager.CompletePublication("provider", []string{"leased-build"}))
	require.Equal(t, LazyStateBuilding, old.State(),
		"the publication barrier must not close an object a hand-off is using")

	buildGate.open()
	outcome := awaitOutcome(t, result)
	require.NoError(t, outcome.err, "the leased hand-off must finish: no closed-node error")
	require.NotErrorIs(t, outcome.err, os.ErrClosed)
	require.NotNil(t, outcome.conn)
	require.Equal(t, LazyStateReady, old.State())

	release()
	require.Equal(t, LazyStateClosed, old.State(), "the lease release is what retires the object")
	runtimes := fixture.runtimesSnapshot()
	require.Len(t, runtimes, 1)
	require.EqualValues(t, 1, runtimes[0].cleanupRuns.Load())
	require.EqualValues(t, 0, runtimes[0].closeCalls.Load())
}

// TestAttackLifecycleObjectCreatedAfterStartIsOwnedAndReleased is the ownership gap a LATE
// publication opens: Manager.Create published the object but never started it and never registered it
// with the lifecycle group, and Manager.release drains a group scope. LifecycleGroup.Remove reports
// success for a component it does not own, so "Remove returned nil" said nothing and both the object
// and its expansion were left behind. The oracle is the cleanup run count, not the return value.
func TestAttackLifecycleObjectCreatedAfterStartIsOwnedAndReleased(t *testing.T) {
	fixture := newAttackFixture(t)
	fixture.startAll(t)
	deferred := fixture.create(t, "created-after-start")
	expandAttack(t, deferred)
	require.Equal(t, LazyStateReady, deferred.State())
	require.EqualValues(t, 1, fixture.pending())

	require.NoError(t, fixture.manager.Remove("created-after-start"))
	require.Equal(t, LazyStateClosed, deferred.State(),
		"a published object must be released by the manager, not silently unlinked")
	runtimes := fixture.runtimesSnapshot()
	require.Len(t, runtimes, 1)
	require.EqualValues(t, 1, runtimes[0].cleanupRuns.Load(), "the expansion's cleanup must run")
	require.EqualValues(t, 0, runtimes[0].closeCalls.Load())
	require.Zero(t, fixture.pending(), "the published expansion was released exactly once")
}

// TestAttackControlPlaneDoesNotBuildOrWaitForATeardown is P08 at the object layer: the control path
// a protocol wrapper builds on (WithRuntime for a capability forward, NotifyIdleWalk for the existing
// idle walk) is a QUERY. It must not expand a node, must not wait for a build it cannot see, and must
// not revive a closed node.
func TestAttackControlPlaneDoesNotBuildOrWaitForATeardown(t *testing.T) {
	fixture := newAttackFixture(t)
	deferred := fixture.create(t, "control-node")
	fixture.startAll(t)

	// Never built: the capability walk visits nothing and the trim reports a no-op.
	visited := false
	require.False(t, deferred.WithRuntime(func(adapter.Outbound) { visited = true }))
	require.False(t, visited, "a never-built node has no runtime to visit")
	deferred.NotifyIdleWalk()
	require.EqualValues(t, 0, fixture.builds.Load(), "the control path must never expand a node")
	require.Equal(t, LazyStateNeverBuilt, deferred.State())

	// Building: the walk must return immediately instead of waiting for the expansion.
	buildGate := fixture.gate(t, func(config *attackConfig, gate *attackGate) { config.buildGate = gate })
	result := dialAsync(deferred, context.Background())
	buildGate.waitEntered(t)
	within(t, 5*time.Second, "WithRuntime during a build", func() {
		require.False(t, deferred.WithRuntime(func(adapter.Outbound) { visited = true }))
	})
	deferred.NotifyIdleWalk()
	require.False(t, visited, "a build in flight has nothing to forward to yet")
	require.EqualValues(t, 1, fixture.builds.Load(), "the control path must not start a second build")
	require.Equal(t, LazyStateBuilding, deferred.State())
	buildGate.open()
	require.NoError(t, awaitOutcome(t, result).err)

	// Expanded: the walk visits the published runtime without building or evicting it.
	require.True(t, deferred.WithRuntime(func(adapter.Outbound) { visited = true }))
	require.True(t, visited)
	require.EqualValues(t, 1, fixture.builds.Load())

	// Closed: the walk must not revive it.
	require.NoError(t, deferred.Close())
	require.False(t, deferred.WithRuntime(func(adapter.Outbound) { visited = true }))
	deferred.NotifyIdleWalk()
	require.Equal(t, LazyStateClosed, deferred.State())
	require.EqualValues(t, 1, fixture.builds.Load())
	closedDial := awaitOutcome(t, dialAsync(deferred, context.Background()))
	require.ErrorIs(t, closedDial.err, os.ErrClosed)
}

// TestAttackProbeReleaseCannotEvictAUserHandoff is P07 at the object layer. A URLTest probe's
// connection closing is not evidence that nobody uses the node: a user hand-off can be outstanding
// with dialing==0 and conns==0. The lease is the fact that says so, and eviction must honour it.
func TestAttackProbeReleaseCannotEvictAUserHandoff(t *testing.T) {
	fixture := newAttackFixture(t)
	deferred := fixture.create(t, "probe-vs-user")
	fixture.startAll(t)
	expandAttack(t, deferred)

	// A probe: dial and close, exactly like one measurement.
	expandAttack(t, deferred)
	require.EqualValues(t, 1, fixture.builds.Load(), "a probe reuses the published expansion")

	// The user obtains the node and is about to dial; the router's lease is the only evidence.
	releaseUser, ok := fixture.manager.AcquireOutbound(deferred)
	require.True(t, ok)

	observation := deferred.ExpansionObservation()
	require.Equal(t, 0, observation.Dialing, "no dial is in flight")
	require.Equal(t, 0, observation.Conns, "the probe's connection is gone")
	require.Equal(t, 1, observation.Leased, "the user's hand-off is outstanding")
	require.False(t, deferred.Evict(),
		"dialing==0 and conns==0 do not mean nobody is using the node (P07)")

	conn, err := deferred.DialContext(context.Background(), "tcp", M.Socksaddr{})
	require.NoError(t, err, "the user's first dial must not be broken by the probe's release")
	require.NoError(t, conn.Close())
	require.EqualValues(t, 1, fixture.builds.Load())

	releaseUser()
	require.True(t, deferred.Evict())
	require.Equal(t, LazyStateNeverBuilt, deferred.State())
}
