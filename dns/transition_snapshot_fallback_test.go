package dns

import (
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing/common/control"

	"github.com/stretchr/testify/require"
)

// A manager may report the settled state through the SNAPSHOT and not through the getter.
//
// # Why this is a separate shape
//
// `NetworkTransitionState` (the lock-free getter) and `NetworkFamilyCapabilitySnapshotter` (which embeds
// `NetworkTransitionSnapshotter`) are independent interfaces. The dialer reads the settled state from the
// snapshot, with one lock, because it needs the epoch and the verdict in the same instant. The DNS cache
// asked only for the getter - so a manager that implements the snapshotter and not the getter was
// believed by the dialer and disbelieved here, and this function answered "settled" for a network that
// could be mid-transition. The consequence is the one the pending-transition tests exist for: an answer
// produced during a transition is stored as though it described a settled network.
//
// # Why the production manager never showed it
//
// `route.NetworkManager` implements every optional interface, so the getter is always available and the
// defect was unreachable in production. It was reachable for any manager that reports only the richer
// observation - including the shape this package's own tests use elsewhere.
//
// No production manager has the shape today, which is why this is a test of the consumer rather than a
// reproduction of a field failure.

// transitionSnapshotOnlyManager reports the settled state through the snapshot and implements neither
// of the two narrower interfaces.
type transitionSnapshotOnlyManager struct {
	adapter.NetworkManager
	routeEpoch atomic.Uint64
	stable     atomic.Bool
}

func (m *transitionSnapshotOnlyManager) NetworkTransitionSnapshot() (uint64, bool) {
	return m.routeEpoch.Load(), m.stable.Load()
}

func (m *transitionSnapshotOnlyManager) NetworkEnvironment() uint64 { return 0 }
func (m *transitionSnapshotOnlyManager) InterfaceFinder() control.InterfaceFinder {
	return control.NewDefaultInterfaceFinder()
}

// The compile-time proof that this double is the narrow shape: it satisfies the snapshotter and neither
// of the two getters the client prefers.
var (
	_ adapter.NetworkTransitionSnapshotter = (*transitionSnapshotOnlyManager)(nil)
	_                                     = func() {
		var manager any = &transitionSnapshotOnlyManager{}
		if _, isState := manager.(adapter.NetworkTransitionState); isState {
			panic("the double must not implement NetworkTransitionState")
		}
		if _, isCounter := manager.(adapter.NetworkResetCounter); isCounter {
			panic("the double must not implement NetworkResetCounter")
		}
	}
)

func TestTheSettledStateIsReadFromTheSnapshotWhenTheGetterIsAbsent(t *testing.T) {
	manager := &transitionSnapshotOnlyManager{}
	client := &Client{networkManager: manager}

	manager.stable.Store(true)
	require.True(t, client.networkTransitionStable(),
		"a settled snapshot must be reported as settled")

	manager.stable.Store(false)
	require.False(t, client.networkTransitionStable(),
		"and an unsettled one must not be reported as settled: that answer is what lets a transition-ERA "+
			"answer be cached as a stable result, which is the defect the pending-transition fixtures in "+
			"this package were written for")

	// A manager with no lifecycle source at all keeps the previous answer: the distinction does not
	// arise for it, and inventing "unsettled" would stop every query from being cached.
	require.True(t, (&Client{}).networkTransitionStable())
}

// TestTheLockFreeGetterIsPreferredWhenBothArePresent keeps the hot path where it was.
//
// The fallback above takes the manager's ownership lock. The production manager offers the lock-free
// getter, and it must keep being asked first - otherwise this fix would put a lock on every DNS query
// against every transition, which is the cost the original comment in this function exists to avoid.
func TestTheLockFreeGetterIsPreferredWhenBothArePresent(t *testing.T) {
	manager := &countingTransitionManager{}
	manager.stable.Store(false)
	client := &Client{networkManager: manager}

	require.False(t, client.networkTransitionStable())
	require.Equal(t, int32(1), manager.stateCalls.Load(), "the lock-free getter answers")
	require.Equal(t, int32(0), manager.snapshotCalls.Load(),
		"and the snapshot's lock is never taken on the query path")
}

// countingTransitionManager offers both interfaces and counts which one was used.
type countingTransitionManager struct {
	adapter.NetworkManager
	stable        atomic.Bool
	stateCalls    atomic.Int32
	snapshotCalls atomic.Int32
}

func (m *countingTransitionManager) NetworkTransitionStable() bool {
	m.stateCalls.Add(1)
	return m.stable.Load()
}

func (m *countingTransitionManager) NetworkTransitionSnapshot() (uint64, bool) {
	m.snapshotCalls.Add(1)
	return 1, m.stable.Load()
}

var (
	_ adapter.NetworkTransitionState       = (*countingTransitionManager)(nil)
	_ adapter.NetworkTransitionSnapshotter = (*countingTransitionManager)(nil)
)
