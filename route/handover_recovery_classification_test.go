package route

import (
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing/common/control"

	"github.com/stretchr/testify/require"
)

// reclaimReasonCapture is a connection manager that records the reason it was asked to reclaim for.
//
// The embedded interface makes every other method a visible panic if this fixture ever starts
// depending on one: a test that silently used a zero ConnectionManager would prove nothing about the
// classification, which is the only fact under test here.
type reclaimReasonCapture struct {
	adapter.ConnectionManager
	reasons chan ReclaimReason
}

func (m *reclaimReasonCapture) Reclaim(reason ReclaimReason) int {
	m.reasons <- reason
	return 0
}

func (m *reclaimReasonCapture) await(t *testing.T) ReclaimReason {
	t.Helper()
	select {
	case reason := <-m.reasons:
		return reason
	case <-time.After(10 * time.Second):
		t.Fatal("the transition never reached a reclaim")
		return ReclaimNetworkTransition
	}
}

// TestRecoveryAfterAConfirmedGapReclaimsDestructively is the field case: Wi-Fi loses its signal, the
// device spends longer than HardTransitionConfirm with no default interface, and cellular comes up.
//
// The reclaim class must be decided BEFORE the wake clears the pause. NetworkWake is what
// dispatchInterfaceUpdateLocked runs before spawning the reset, so a body that reads IsNetworkPaused
// would see a live network no matter how long the outage actually held, and every handover would
// drain - leaving the streams that were bound to the dead path in place. On DIRECT traffic nothing
// else closes them: the outbound only refreshes its addresses, and the drain protects anything whose
// local activity is still being observed, which an application that keeps writing on the dead socket
// does.
func TestRecoveryAfterAConfirmedGapReclaimsDestructively(t *testing.T) {
	harness := newInterfaceTransitionHarness(t)
	paused := &trackingPauseManager{}
	harness.manager.pauseManager = paused
	capture := &reclaimReasonCapture{reasons: make(chan ReclaimReason, 2)}
	harness.manager.connectionManager = capture
	monitor := harness.manager.interfaceMonitor.(*staticInterfaceMonitor)

	// The gap: the platform reports no default interface, which pauses the network.
	monitor.current = nil
	harness.manager.notifyInterfaceUpdate(nil, 0)
	require.True(t, paused.IsNetworkPaused(), "the gap must pause the network")

	// The outage held past the confirmation window before cellular arrived.
	harness.manager.networkPausedSince.Store(time.Now().Add(-HardTransitionConfirm - time.Second).UnixNano())

	// Cellular comes up: this is the recovery edge, and it is the last instant the gap is visible.
	monitor.current = &control.Interface{Index: 2, Name: "pdp_ip0"}
	harness.manager.notifyInterfaceUpdate(nil, 0)
	require.False(t, paused.IsNetworkPaused(), "the recovery must wake the network")

	require.Equal(t, ReclaimDeadPath, capture.await(t),
		"a recovery from a confirmed outage drained instead of reclaiming destructively; the old "+
			"path is gone, so every socket on it is dead regardless of what its local activity counter says")
}

// TestRecoveryAfterAShortGapStillDrains is the other direction, and the reason the window exists: a
// handover that finishes inside HardTransitionConfirm is a medium transition and must not destroy
// streams that a roam within the same addressing may have preserved.
func TestRecoveryAfterAShortGapStillDrains(t *testing.T) {
	harness := newInterfaceTransitionHarness(t)
	paused := &trackingPauseManager{}
	harness.manager.pauseManager = paused
	capture := &reclaimReasonCapture{reasons: make(chan ReclaimReason, 2)}
	harness.manager.connectionManager = capture
	monitor := harness.manager.interfaceMonitor.(*staticInterfaceMonitor)

	monitor.current = nil
	harness.manager.notifyInterfaceUpdate(nil, 0)
	require.True(t, paused.IsNetworkPaused())

	// No backdating: the gap has just begun.
	monitor.current = &control.Interface{Index: 2, Name: "pdp_ip0"}
	harness.manager.notifyInterfaceUpdate(nil, 0)

	require.Equal(t, ReclaimNetworkTransition, capture.await(t),
		"a handover that finished inside the confirmation window reclaimed destructively")
}

// TestRecoveryClassificationDoesNotLeakIntoTheNextTransition guards the reset of the captured flag.
//
// The classification belongs to one pending event. If it survived into the next transition, a single
// confirmed outage would make every later roam destructive, which is the policy reversed rather than
// applied.
func TestRecoveryClassificationDoesNotLeakIntoTheNextTransition(t *testing.T) {
	harness := newInterfaceTransitionHarness(t)
	paused := &trackingPauseManager{}
	harness.manager.pauseManager = paused
	capture := &reclaimReasonCapture{reasons: make(chan ReclaimReason, 4)}
	harness.manager.connectionManager = capture
	monitor := harness.manager.interfaceMonitor.(*staticInterfaceMonitor)

	// A confirmed outage, then a recovery: hard.
	monitor.current = nil
	harness.manager.notifyInterfaceUpdate(nil, 0)
	harness.manager.networkPausedSince.Store(time.Now().Add(-HardTransitionConfirm - time.Second).UnixNano())
	monitor.current = &control.Interface{Index: 2, Name: "pdp_ip0"}
	harness.manager.notifyInterfaceUpdate(nil, 0)
	require.Equal(t, ReclaimDeadPath, capture.await(t))

	// A later, ordinary medium transition on the new network must not inherit it.
	monitor.current = &control.Interface{Index: 3, Name: "pdp_ip1"}
	harness.manager.notifyInterfaceUpdate(nil, 0)
	require.Equal(t, ReclaimNetworkTransition, capture.await(t),
		"the hard classification leaked from the previous transition")
}
