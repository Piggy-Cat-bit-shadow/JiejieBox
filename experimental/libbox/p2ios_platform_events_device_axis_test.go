//go:build p2ios

package libbox

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file is N-9's second half, and it carries the `p2ios` tag for the same reason
// p2ios_windows_test.go does: the function under test is compiled out everywhere the suite normally
// runs.
//
// `CommandServer.restoreDeviceLevel` opens with
//
//	if !(C.IsAndroid || C.IsIos) || C.IsTvOS { return }
//
// and `constant/goos` sets both constants to 0 on windows, linux and darwin alike, so on the host this
// evidence was collected on it is dead code. The mirror write itself is NOT platform-gated - it is the
// target recording a decision the shared policy made - which is why the write, the "the manager could
// not have run" half and the live-Box control are in platform_events_device_axis_test.go where they run
// unconditionally, and why only the reload hook is here.
//
// Run with the overlay p2ios_windows_test.go documents:
//
//	# a copy of constant/goos/zgoos_windows.go with `const IsIos = 1`
//	go test -tags "…,p2ios" -overlay overlay.json -run TestThePlatform ./experimental/libbox/
//
// The two tests below are one per direction of the window, and the wake one is the important one.

// TestThePlatformPauseInTheNilInstanceWindowIsRestoredByTheReload is the pause direction end to end.
//
// The shared policy decides a pause while the service is building the next Box, the manager cannot run
// because there is no instance, and the reload hook must apply the fact to the Box that now exists. The
// replacement Box starting awake is the harm: on a locked device it refreshes providers, runs URLTests
// and stops suspending the on-demand tunnels, with no log line and no self-correction, because the
// client's screen-state observer treats an unchanged lock snapshot as a repeat and re-publishes
// nothing.
func TestThePlatformPauseInTheNilInstanceWindowIsRestoredByTheReload(t *testing.T) {
	fixture := newPlatformEventsDeviceAxisFixture(t)
	manager := fixture.started.Instance().PauseManager()
	require.NotNil(t, manager, "the running Box has no pause manager, so the device axis does not exist")
	require.False(t, manager.IsDevicePaused(), "a fresh Box must not start paused")

	finishReload := fixture.enterReloadWindow(t)

	// A screen-off fact arrives in the window. `PlatformEvents` decides DevicePause; the target has no
	// instance to apply it to and must still record it.
	//
	// The mirror observations are `assert` and the end-to-end one below is `require` on purpose: the
	// mirror is the mechanism, the manager state after the reload is the harm, and a RED run has to
	// show both rather than stopping at the first of them.
	fixture.events.SetScreenOn(false)
	paused, touched := rememberedDeviceLevel(fixture.server)
	assert.True(t, touched, "the fact was lost to the nil instance")
	assert.True(t, paused)
	assert.False(t, manager.IsDevicePaused(),
		"the manager ran while the service had no instance, so this is not the window")

	finishReload()

	after := fixture.started.Instance()
	require.NotNil(t, after, "the reload produced no running instance")
	afterManager := after.PauseManager()
	require.Same(t, manager, afterManager, "the level owner must not change across a reload")
	require.True(t, afterManager.IsDevicePaused(),
		"the replacement Box was not given the device level the platform reported while it was being "+
			"built: the pause earned in the reload window was applied to a Box in the mirror only, and "+
			"the Box that now exists runs as if the phone were in the user's hand")
}

// TestThePlatformWakeInTheNilInstanceWindowIsNotRestoredAsAPause is the wake direction, and it is the
// one that latches.
//
// The mirror says paused, the manager is paused, and the fact that lifts the pause - a user present, an
// unlock, the platform's own statement that the device is usable - arrives while the service has no
// instance. The wake cannot reach the manager, so if it does not reach the mirror either, the two agree
// after the reload and NOTHING moves: the reload finds no disagreement to reconcile, and the device
// level stays entered for a device in the user's hand. Recording the wake first is what turns that into
// a disagreement the reload hook resolves in the correct direction.
func TestThePlatformWakeInTheNilInstanceWindowIsNotRestoredAsAPause(t *testing.T) {
	fixture := newPlatformEventsDeviceAxisFixture(t)
	manager := fixture.started.Instance().PauseManager()
	require.NotNil(t, manager)

	// A live pause first, delivered the way the platform delivers it, so the manager is genuinely in
	// the paused level, and the mirror put in the same state through the product's own mirror writer.
	// The seed keeps the assertion below about the WAKE rather than about the mirror never having been
	// written at all, and it is the state a Doze-driven `CommandServer.Pause` leaves behind on Android
	// today.
	fixture.events.SetScreenOn(true)
	fixture.events.SetAppForeground(true)
	fixture.events.SetScreenOn(false)
	require.True(t, manager.IsDevicePaused(), "the fixture did not enter the paused level")
	fixture.server.rememberDeviceLevel(true)
	paused, touched := rememberedDeviceLevel(fixture.server)
	require.True(t, touched)
	require.True(t, paused)

	finishReload := fixture.enterReloadWindow(t)

	fixture.events.ReportDeviceWake()
	paused, touched = rememberedDeviceLevel(fixture.server)
	// assert, not require, for the same reason as the pause direction: the mirror is the mechanism and
	// the manager state after the reload is the harm.
	assert.True(t, touched)
	assert.False(t, paused, "the wake was lost to the nil instance")
	assert.True(t, manager.IsDevicePaused(),
		"the manager was woken while the service had no instance, so this is not the window")

	finishReload()

	after := fixture.started.Instance()
	require.NotNil(t, after, "the reload produced no running instance")
	afterManager := after.PauseManager()
	require.Same(t, manager, afterManager, "the level owner must not change across a reload")
	require.False(t, afterManager.IsDevicePaused(),
		"the wake delivered in the reload window was re-applied as a PAUSE by the reload: the mirror "+
			"still described the device as paused, it agreed with the manager the wake never reached, so "+
			"the reload hook found nothing to reconcile and the level latched on a device that is being "+
			"used")
}
