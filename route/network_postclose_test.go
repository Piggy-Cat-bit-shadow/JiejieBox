package route

import (
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing/common/control"
	"github.com/sagernet/sing/common/winpowrprof"

	"github.com/stretchr/testify/require"
)

// writeCountingPauseManager counts the writes, because the defect is a WRITE to a shared object rather
// than a local state change: a dropped notification is invisible from inside the dead manager and
// only shows up in the manager the next Box is using.
type writeCountingPauseManager struct {
	trackingPauseManager
	networkPauses atomic.Int64
	networkWakes  atomic.Int64
	devicePauses  atomic.Int64
	deviceWakes   atomic.Int64
}

func (m *writeCountingPauseManager) NetworkPause() {
	m.networkPauses.Add(1)
	m.trackingPauseManager.NetworkPause()
}

func (m *writeCountingPauseManager) NetworkWake() {
	m.networkWakes.Add(1)
	m.trackingPauseManager.NetworkWake()
}

func (m *writeCountingPauseManager) DevicePause() {
	m.devicePauses.Add(1)
	m.trackingPauseManager.DevicePause()
}

func (m *writeCountingPauseManager) DeviceWake() {
	m.deviceWakes.Add(1)
	m.trackingPauseManager.DeviceWake()
}

// TestClosedNetworkManagerDoesNotWriteTheSessionSharedPauseManager is the resurrection guard for the
// interface monitor, and it is a cross-generation bug rather than a local one.
//
// # The interleaving
//
//	1. the monitor's `emit` snapshots its callback list under its own lock and RELEASES it
//	   (sing-tun monitor_shared.go:139-146), then invokes each callback;
//	2. `NetworkManager` teardown runs `closed.Store(true)` and `UnregisterCallback` - which only
//	   removes the entry from the live list, so it cannot recall a callback already in the snapshot;
//	3. the snapshot's copy still runs `notifyInterfaceUpdate`.
//
// Every check the old code had passes at step 3: `startedCtx` is never nil once set, and it is
// CANCELLED at teardown rather than cleared, so `r.startedCtx != nil` is true for a dead manager.
// `dispatchInterfaceUpdateLocked` then writes `pauseManager.NetworkPause()` / `NetworkWake()`.
//
// # Why that is not merely a stale write
//
// The pause manager is SESSION-scoped, not Box-scoped: `daemon.NewStartedService` registers one for
// the life of the service (`pause.WithDefaultManager(startedCtx)`), and `box.New` reuses whatever it
// finds. The next Box registers its own callbacks on that same manager and seeds its governor from
// it. So a torn-down Box pausing the network axis pauses the LIVE Box: outbounds stop taking
// `InterfaceUpdated`, reclaims are upgraded to destructive, and a WireGuard endpoint built on that
// manager takes its device down - "the box was reloaded and the VPN stopped carrying traffic", with
// no log line pointing at the reload.
func TestClosedNetworkManagerDoesNotWriteTheSessionSharedPauseManager(t *testing.T) {
	harness := newInterfaceTransitionHarness(t)
	paused := &writeCountingPauseManager{}
	harness.manager.pauseManager = paused
	monitor, isStatic := harness.manager.interfaceMonitor.(*staticInterfaceMonitor)
	require.True(t, isStatic)

	// A live manager writes the pause manager: this is the positive control, without which the
	// assertion below would pass on a manager that never pauses at all.
	monitor.current = nil
	harness.manager.notifyInterfaceUpdate(nil, 0)
	require.Equal(t, int64(1), paused.networkPauses.Load(),
		"a live manager must still pause the network when the platform reports no default interface")

	// Teardown, as the scope cleanup performs it.
	harness.manager.closed.Store(true)
	pauseCountBefore := paused.networkPauses.Load()
	wakeCountBefore := paused.networkWakes.Load()
	resetPendingBefore := harness.manager.networkResetPending

	// The notification that was already inside the monitor's snapshot.
	monitor.current = &control.Interface{Index: 2, Name: "pdp_ip0"}
	harness.manager.notifyInterfaceUpdate(nil, 0)
	monitor.current = nil
	harness.manager.notifyInterfaceUpdate(nil, 0)

	require.Equal(t, pauseCountBefore, paused.networkPauses.Load(),
		"a closed manager wrote NetworkPause() onto the session-shared pause manager, which is what "+
			"pauses the NEXT Box's network axis")
	require.Equal(t, wakeCountBefore, paused.networkWakes.Load(),
		"a closed manager wrote NetworkWake() onto the session-shared pause manager")

	// The reset claim must not move either: a dead manager claiming an epoch is the same defect one
	// layer down. Asserted as "unchanged by the closed notifications" rather than as "false", because
	// the live positive control above legitimately armed it - that is what a real notification does.
	require.Equal(t, resetPendingBefore, harness.manager.networkResetPending,
		"a closed manager must not arm a network reset for a Box that no longer exists")
}

// TestClosedNetworkManagerIgnoresWindowsPowerEvents is the same rule on the device axis.
//
// The Windows power listener is closed with the scope, but `PowerUnregisterSuspendResumeNotification`
// followed by `Unpin` is not a join: the C callback can still be dispatched while they run, and it
// dereferences the pinned context and calls straight through. A suspend delivered in that window
// pauses the session-shared manager, and the next Box seeds itself ACTIVE from a manager that says
// the device is asleep - so speculation stays suppressed for the rest of the lock.
func TestClosedNetworkManagerIgnoresWindowsPowerEvents(t *testing.T) {
	harness := newInterfaceTransitionHarness(t)
	paused := &writeCountingPauseManager{}
	harness.manager.pauseManager = paused

	// Positive control: the handler is reachable and does write while the manager is live.
	harness.manager.notifyWindowsPowerEvent(winpowrprof.EVENT_SUSPEND)
	require.Equal(t, int64(1), paused.devicePauses.Load(),
		"a live manager must pause the device axis on a suspend event")

	harness.manager.closed.Store(true)
	devicePauseBefore := paused.devicePauses.Load()
	deviceWakeBefore := paused.deviceWakes.Load()

	harness.manager.notifyWindowsPowerEvent(winpowrprof.EVENT_RESUME)
	harness.manager.notifyWindowsPowerEvent(winpowrprof.EVENT_SUSPEND)

	require.Equal(t, devicePauseBefore, paused.devicePauses.Load(),
		"a closed manager paused the device axis of the session-shared pause manager")
	require.Equal(t, deviceWakeBefore, paused.deviceWakes.Load(),
		"a closed manager woke the device axis of the session-shared pause manager")
}
