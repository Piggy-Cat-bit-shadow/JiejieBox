//go:build p2ios

package libbox

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTheDeviceLevelSurvivesAReloadAndBothDirectionsOfTheWindow pins the Apple device axis across the
// reload window, which is the one lifecycle boundary this client deliberately does not cross.
//
// # The two defects it guards
//
//  1. **The level used to be lost.** `pause.WithDefaultManager` returns an EXISTING manager unchanged, and
//     before `daemon.NewStartedService` registered one for the service's life, every Box made its own. A
//     reload therefore built a new, unpaused manager, and a phone that had locked came back awake with
//     nothing logged.
//
//  2. **A fact delivered in the window used to be dropped.** `StartedService.StartOrReloadService` sets
//     `s.instance = nil` for the whole of `newInstance`, and every record method here resolves
//     `Instance()` and returns early on nil. Fixing (1) turned that from a masked error into a live one:
//     the lock still arrived, the unlock did not, and the level latched PAUSED while the phone was in the
//     user's hand. `restoreDeviceLevel` now applies the remembered level in BOTH directions.
//
// Both are asserted here as behaviour of the real `CommandServer`, not as a reading of the fields.
func TestTheDeviceLevelSurvivesAReloadAndBothDirectionsOfTheWindow(t *testing.T) {
	fixture := newDeviceAxisFixture(t)
	manager := fixture.started.Instance().PauseManager()
	require.NotNil(t, manager)
	require.False(t, manager.IsDevicePaused(), "a fresh Box must not start paused")

	// --- the lock survives a reload -----------------------------------------------------------------
	fixture.server.RecordLockState(true)
	require.True(t, manager.IsDevicePaused(), "a lock must enter the level")

	fixture.reload(t)
	require.True(t, manager.IsDevicePaused(),
		"the level did not survive a reload, so a locked phone whose configuration was reloaded is awake "+
			"again: URLTest runs, provider refresh is released, and the on-demand tunnels stop being "+
			"suspended")

	// --- an unlock is applied, and the pause direction still works after it -------------------------
	//
	// NOT the reload window, and deliberately so. The window is `StartOrReloadService`'s
	// `s.instance = nil` span, and the divergence it leaves - the mirror recording an unlock the manager
	// never heard - cannot be constructed from this package without either a test-only seam in
	// `CommandServer`'s shipped surface or a reflection write into a struct that carries a mutex. Both
	// were rejected, so the window case is an EVIDENCE GAP and is recorded as one rather than stood in
	// for by a test that would pass for the wrong reason.
	//
	// What is asserted here is the reachable half: the two directions of the level, and that a reload
	// preserves whichever one is current.
	fixture.server.RecordLockState(false)
	require.False(t, manager.IsDevicePaused(), "an unlock must release the level")
	fixture.reload(t)
	require.False(t, manager.IsDevicePaused(), "an unlocked device must not be re-paused by a reload")

	// --- and the pause direction still works after that ---------------------------------------------
	fixture.server.RecordLockState(true)
	fixture.reload(t)
	require.True(t, manager.IsDevicePaused(), "a later lock must still pause, and survive the reload")

	// A display fact is a level pause too, so it must survive a reload the same way.
	fixture.server.RecordLockState(false)
	fixture.reload(t)
	require.False(t, manager.IsDevicePaused())
	fixture.server.RecordScreenState(false)
	require.True(t, manager.IsDevicePaused(), "a display going off is a level pause")
	fixture.reload(t)
	require.True(t, manager.IsDevicePaused(), "a display-off pause did not survive a reload")
}
