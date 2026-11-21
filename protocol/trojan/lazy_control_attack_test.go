package trojan

// LN attack suite: the REAL deferred-Trojan wrapper's CONTROL PLANE.
//
// # Why this is a separate file from the generic object tests
//
// lazyTrojanOutbound is the object the manager publishes for a cold Trojan node. Its capability
// methods - MultiplexEnabled, InterfaceUpdated, SetKeepIdleConnections, CloseIdleConnections,
// ResidencyLedger - are called by route/reference.go, network.go and diagnostics on objects that may
// never have been expanded. Each of them is a place where a deferred node could silently become an
// eager one (build a TLS config to answer a question), block on a build it cannot see, or resurrect a
// closed node. P08 of the matrix is exactly this: trim/pause/resume must not dial and must not revive.
//
// The generic LazyOutbound tests pin WithRuntime and NotifyIdleWalk; these tests pin that the wrapper
// actually uses them and forwards to a live expansion instead of reimplementing the decision.

import (
	"context"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// controlGate is a one-shot barrier with an idempotent open, so a failing assertion cannot hang the
// package on a blocked builder.
type controlGate struct {
	entered     chan struct{}
	release     chan struct{}
	enterOnce   sync.Once
	releaseOnce sync.Once
}

func newControlGate() *controlGate {
	return &controlGate{entered: make(chan struct{}), release: make(chan struct{})}
}

func (g *controlGate) enter() {
	g.enterOnce.Do(func() { close(g.entered) })
	<-g.release
}

func (g *controlGate) open() { g.releaseOnce.Do(func() { close(g.release) }) }

// controlAwareRuntime is an expansion that can answer every capability the wrapper forwards, so the
// test can tell "the wrapper asked the live runtime" apart from "the wrapper decided by itself".
type controlAwareRuntime struct {
	adapter.Outbound
	tag string

	startCalls     atomic.Int32
	cleanupRuns    atomic.Int32
	dialCalls      atomic.Int32
	trimCalls      atomic.Int32
	keepCalls      atomic.Int32
	keepValue      atomic.Bool
	networkUpdates atomic.Int32

	keepIdleOnTrim bool
	ledger         adapter.ResidencyLedger
}

var (
	_ adapter.Lifecycle               = (*controlAwareRuntime)(nil)
	_ adapter.InterfaceUpdateListener = (*controlAwareRuntime)(nil)
	_ adapter.IdleConnectionKeeper    = (*controlAwareRuntime)(nil)
	_ adapter.ResidencyLedgerReporter = (*controlAwareRuntime)(nil)
	_ adapter.Outbound                = (*controlAwareRuntime)(nil)
)

func (r *controlAwareRuntime) Type() string           { return "control-aware" }
func (r *controlAwareRuntime) Tag() string            { return r.tag }
func (r *controlAwareRuntime) Network() []string      { return []string{"tcp"} }
func (r *controlAwareRuntime) Dependencies() []string { return nil }

func (r *controlAwareRuntime) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	r.startCalls.Add(1)
	if stage == adapter.StartStateInitialize {
		scope.AddNamed("control-aware["+r.tag+"]", func() error {
			r.cleanupRuns.Add(1)
			return nil
		})
	}
	return nil
}

func (r *controlAwareRuntime) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	r.dialCalls.Add(1)
	client, server := net.Pipe()
	_ = server.Close()
	return client, nil
}

func (r *controlAwareRuntime) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, os.ErrInvalid
}

func (r *controlAwareRuntime) CloseIdleConnections() {
	r.trimCalls.Add(1)
	if r.keepIdleOnTrim {
		// Nothing to trim: the point is that the wrapper reached the live runtime at all.
	}
}

func (r *controlAwareRuntime) SetKeepIdleConnections(keep bool) {
	r.keepCalls.Add(1)
	r.keepValue.Store(keep)
}

func (r *controlAwareRuntime) InterfaceUpdated(ctx context.Context) {
	r.networkUpdates.Add(1)
}

func (r *controlAwareRuntime) ResidencyLedger() adapter.ResidencyLedger {
	return r.ledger
}

type controlPlaneFixture struct {
	wrapper  *lazyTrojanOutbound
	builds   atomic.Int32
	gate     *controlGate
	access   sync.Mutex
	runtimes []*controlAwareRuntime
}

func newControlPlaneFixture(t *testing.T) *controlPlaneFixture {
	t.Helper()
	fixture := &controlPlaneFixture{}
	lazy := outbound.NewLazyOutbound[lazyRecipe](
		outbound.NewAdapter("trojan", "cold-control", []string{"tcp"}, nil),
		log.NewNOPFactory().NewLogger("control"),
		context.Background(),
		lazyRecipe{},
		func(ctx context.Context, recipe lazyRecipe) (adapter.Outbound, error) {
			fixture.builds.Add(1)
			if fixture.gate != nil {
				fixture.gate.enter()
			}
			runtime := &controlAwareRuntime{
				tag:    "cold-control",
				ledger: adapter.ResidencyLedger{Answered: true, Note: "one pooled object"},
			}
			fixture.access.Lock()
			fixture.runtimes = append(fixture.runtimes, runtime)
			fixture.access.Unlock()
			return runtime, nil
		})
	scope := adapter.NewScope(context.Background(), log.NewNOPFactory().Logger())
	require.NoError(t, lazy.Start(adapter.StartStateInitialize, scope))
	t.Cleanup(func() { _ = scope.Close() })
	fixture.wrapper = &lazyTrojanOutbound{LazyOutbound: lazy}
	return fixture
}

func (f *controlPlaneFixture) snapshot() []*controlAwareRuntime {
	f.access.Lock()
	defer f.access.Unlock()
	return append([]*controlAwareRuntime(nil), f.runtimes...)
}

// TestAttackLazyTrojanControlPlaneNeverExpands: a cold node answered every capability query without
// building a TLS object, and its ledger is an honest "unknown" instead of a construction.
func TestAttackLazyTrojanControlPlaneNeverExpands(t *testing.T) {
	fixture := newControlPlaneFixture(t)

	require.False(t, fixture.wrapper.MultiplexEnabled(), "the eligible recipe has no multiplex")
	fixture.wrapper.SetKeepIdleConnections(false)
	fixture.wrapper.CloseIdleConnections()
	fixture.wrapper.InterfaceUpdated(context.Background())
	ledger := fixture.wrapper.ResidencyLedger()
	require.False(t, ledger.Answered)
	require.Contains(t, ledger.Note, "not expanded")

	require.EqualValues(t, 0, fixture.builds.Load(),
		"no control-plane query may expand a cold node (P08: trim must not dial)")
	require.Equal(t, "never-built", fixture.wrapper.State().String())
}

// TestAttackLazyTrojanControlPlaneForwardsToALiveExpansion: once expanded, the same calls must reach
// the runtime exactly once and must not cause a second expansion.
func TestAttackLazyTrojanControlPlaneForwardsToALiveExpansion(t *testing.T) {
	fixture := newControlPlaneFixture(t)
	conn, err := fixture.wrapper.DialContext(context.Background(), "tcp", M.Socksaddr{})
	require.NoError(t, err)
	require.NoError(t, conn.Close())
	require.EqualValues(t, 1, fixture.builds.Load())
	require.Equal(t, "ready", fixture.wrapper.State().String())

	fixture.wrapper.CloseIdleConnections()
	fixture.wrapper.InterfaceUpdated(context.Background())
	fixture.wrapper.SetKeepIdleConnections(false)

	runtimes := fixture.snapshot()
	require.Len(t, runtimes, 1)
	require.EqualValues(t, 1, runtimes[0].trimCalls.Load(), "the trim reached the live runtime")
	require.EqualValues(t, 1, runtimes[0].networkUpdates.Load(), "the network change reached the live runtime")
	require.EqualValues(t, 1, runtimes[0].keepCalls.Load())
	require.False(t, runtimes[0].keepValue.Load())
	require.EqualValues(t, 1, fixture.builds.Load(), "control actions must not rebuild the node")

	ledger := fixture.wrapper.ResidencyLedger()
	require.True(t, ledger.Answered, "an expanded node reports its runtime's ledger")
	require.Equal(t, "one pooled object", ledger.Note)
}

// TestAttackLazyTrojanControlPlaneDoesNotWaitForABuild is P08's collision: the idle walk arrives while
// the first expansion is parked in its builder. The wrapper must answer immediately (a capability
// query for a node that has no runtime yet), not wait for the build.
func TestAttackLazyTrojanControlPlaneDoesNotWaitForABuild(t *testing.T) {
	fixture := newControlPlaneFixture(t)
	fixture.gate = newControlGate()
	t.Cleanup(fixture.gate.open)

	result := make(chan error, 1)
	go func() {
		conn, err := fixture.wrapper.DialContext(context.Background(), "tcp", M.Socksaddr{})
		if conn != nil {
			_ = conn.Close()
		}
		result <- err
	}()
	select {
	case <-fixture.gate.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the builder was never entered")
	}
	require.Equal(t, "building", fixture.wrapper.State().String())

	control := make(chan struct{})
	go func() {
		defer close(control)
		fixture.wrapper.CloseIdleConnections()
		fixture.wrapper.InterfaceUpdated(context.Background())
		fixture.wrapper.SetKeepIdleConnections(false)
		require.False(t, fixture.wrapper.ResidencyLedger().Answered)
	}()
	select {
	case <-control:
	case <-time.After(5 * time.Second):
		t.Fatal("a control action waited for an in-flight build")
	}
	require.EqualValues(t, 1, fixture.builds.Load(), "a control action must not start a second build")
	require.Equal(t, "building", fixture.wrapper.State().String())

	fixture.gate.open()
	select {
	case err := <-result:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the parked build never completed")
	}
	require.Equal(t, "ready", fixture.wrapper.State().String())
}

// TestAttackLazyTrojanControlPlaneDoesNotReviveAClosedNode: after Close the node is terminal. A trim or
// a network-change notification arriving late must not build anything, must not wait, and must not
// change the state.
func TestAttackLazyTrojanControlPlaneDoesNotReviveAClosedNode(t *testing.T) {
	fixture := newControlPlaneFixture(t)
	conn, err := fixture.wrapper.DialContext(context.Background(), "tcp", M.Socksaddr{})
	require.NoError(t, err)
	require.NoError(t, conn.Close())
	require.EqualValues(t, 1, fixture.builds.Load())

	require.NoError(t, fixture.wrapper.Close())
	require.Equal(t, "closed", fixture.wrapper.State().String())

	fixture.wrapper.CloseIdleConnections()
	fixture.wrapper.InterfaceUpdated(context.Background())
	fixture.wrapper.SetKeepIdleConnections(true)
	require.False(t, fixture.wrapper.ResidencyLedger().Answered)

	require.Equal(t, "closed", fixture.wrapper.State().String())
	require.EqualValues(t, 1, fixture.builds.Load(), "a closed node never rebuilds")
	runtimes := fixture.snapshot()
	require.Len(t, runtimes, 1)
	require.EqualValues(t, 1, runtimes[0].cleanupRuns.Load(), "the expansion was released exactly once")

	closedDial, closedErr := fixture.wrapper.DialContext(context.Background(), "tcp", M.Socksaddr{})
	require.Nil(t, closedDial)
	require.ErrorIs(t, closedErr, os.ErrClosed)
}
