package libbox

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/daemon"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/pause"

	"github.com/stretchr/testify/require"
)

// This file carries the evidence for the Android device axis's reload window, and it exists because
// the two halves of that path were owned by different objects with no shared state.
//
// # The defect
//
// `runtimecoord.PlatformEvents` is the shared policy: the platform reports FACTS, the policy decides
// the ACTION, and a target executes it. Android's target is `boxPlatformTarget`, and its
// `DevicePause`/`DeviceWake` resolved `StartedService.Instance()` and RETURNED EARLY when it was nil -
// which is exactly the reload window, because `StartOrReloadService` sets `s.instance = nil` and
// leaves it nil for the whole of `newInstance`.
//
// The reload mirror (`CommandServer.deviceAxisPaused`/`deviceAxisTouched`, written by
// `rememberDeviceLevel`, read by `restoreDeviceLevel`) is what survives that window, and the Android
// path never wrote it. So a device-level fact that arrived in the window was not queued, not
// remembered and not reported: the mirror kept whatever the device level had been before the window
// and `restoreDeviceLevel` applied THAT to the replacement Box. A screen-off in the window left the
// replacement Box running as if the phone were in the user's hand; a wake in the window left the
// mirror paused and the reload re-entered the level.
//
// # What is asserted here, and what is asserted in the p2ios file
//
// The mirror write and the "the manager could not have run" half are asserted here, because they need
// no platform constant. The half that requires `restoreDeviceLevel` to actually execute is in
// `p2ios_platform_events_device_axis_test.go`: that function returns immediately unless
// `C.IsAndroid || C.IsIos`, and both are compile-time 0 on every host this suite runs on, so on
// windows/amd64 it is dead code (see the comment at the top of p2ios_windows_test.go for the overlay
// that flips the constant).
//
// # Why the window is held open by a barrier and not by a sleep
//
// `reloadWindowBarrier` blocks ONE call - `IsDevicePaused`, which `box.New` makes while it builds the
// Box - and that call is strictly inside the nil-instance span. The test therefore knows it is in the
// window (it asserts `Instance() == nil` there) instead of hoping it is, and there is no timing to
// tune: no sleep, no polling, no retry loop.

// reloadWindowDeadline is a deadlock ALARM, not a synchronization wait. The test waits on the barrier
// channel; if the window never opens - because a future edit stopped asking the manager for the level
// during construction - the alarm fails the test with a message instead of hanging the suite for the
// ten minutes Go allows a stuck test.
const reloadWindowDeadline = 30 * time.Second

// reloadWindowBarrier is the session's device axis with one scheduling seam.
//
// It IS the manager `daemon.NewStartedService` finds already registered (`pause.WithDefaultManager`
// returns an existing manager unchanged), so `instance.PauseManager()` is this object and every level
// call in these tests runs the real `pause.defaultManager` underneath it.
//
// The seam is the level read. It is used for scheduling only: the value it returns is the real
// manager's, so nothing in these tests sees a level other than the one the service holds.
type reloadWindowBarrier struct {
	pause.Manager
	enter       chan struct{}
	release     chan struct{}
	releaseOnce sync.Once
	armed       atomic.Bool
}

func newReloadWindowBarrier() *reloadWindowBarrier {
	// A manager of its own to delegate to, minted the way the core mints one.
	managerContext := pause.WithDefaultManager(service.ContextWithDefaultRegistry(context.Background()))
	return &reloadWindowBarrier{
		Manager: service.FromContext[pause.Manager](managerContext),
		enter:   make(chan struct{}),
		release: make(chan struct{}),
	}
}

// arm makes the next level read block the Box construction, i.e. the next reload window stay open.
func (m *reloadWindowBarrier) arm() {
	m.armed.Store(true)
}

// entered is closed once the construction path has reached the manager. Callers must observe
// `Instance() == nil` after it fires: that is the window, not a guess about one.
func (m *reloadWindowBarrier) entered() <-chan struct{} {
	return m.enter
}

// releaseWindow lets the construction continue. Idempotent.
func (m *reloadWindowBarrier) releaseWindow() {
	m.releaseOnce.Do(func() { close(m.release) })
}

func (m *reloadWindowBarrier) IsDevicePaused() bool {
	if m.armed.CompareAndSwap(true, false) {
		close(m.enter)
		<-m.release
	}
	return m.Manager.IsDevicePaused()
}

// platformEventsDeviceAxisFixture is one running service, the session's real device axis, and the real
// `PlatformEvents` sink pointed at it.
type platformEventsDeviceAxisFixture struct {
	started *daemon.StartedService
	server  *CommandServer
	events  *PlatformEvents
	window  *reloadWindowBarrier
}

func newPlatformEventsDeviceAxisFixture(t *testing.T) *platformEventsDeviceAxisFixture {
	t.Helper()
	// The package-level path and ownership globals, set and restored exactly as
	// newPlatformEventsTestService does. `sBasePath` is here as well because this fixture drives
	// `CommandServer.StartOrReloadService`, which writes the configuration snapshot into it: with the
	// default empty value that write lands in the package directory, i.e. as an untracked file in the
	// worktree of whoever runs the suite.
	previousBase, previousWorking, previousTemp, previousUser, previousGroup := sBasePath, sWorkingPath, sTempPath, sUserID, sGroupID
	sBasePath = t.TempDir()
	sWorkingPath = t.TempDir()
	sTempPath = t.TempDir()
	sUserID = os.Getuid()
	sGroupID = os.Getgid()
	t.Cleanup(func() {
		sBasePath, sWorkingPath, sTempPath, sUserID, sGroupID = previousBase, previousWorking, previousTemp, previousUser, previousGroup
	})

	window := newReloadWindowBarrier()
	ctx := baseContext(nil)
	// Registered BEFORE the service exists, which is the only ordering that makes it the session's
	// manager: `daemon.NewStartedService` calls `pause.WithDefaultManager`, which keeps an existing
	// manager and only creates one when the context has none.
	ctx = service.ContextWith[pause.Manager](ctx, window)
	started := daemon.NewStartedService(daemon.ServiceOptions{Context: ctx})
	// The real constructor, for the power-report manager every record method touches before it looks
	// at the instance, and for the platform wrapper the product installs.
	server, err := NewCommandServer(platformEventsHandler{}, platformEventsPlatform{})
	require.NoError(t, err)
	server.StartedService = started
	t.Cleanup(func() {
		_ = started.CloseService()
		server.Close()
	})
	// The production entry point, so the first Box and every later one are built by the same code a
	// client drives.
	require.NoError(t, server.StartOrReloadService(platformEventsConfig, &OverrideOptions{}))

	events := NewPlatformEvents(server)
	t.Cleanup(events.Close)
	// Registered LAST, so it runs FIRST. A test that fails before it calls the closure returned by
	// enterReloadWindow leaves the reload goroutine parked inside the Box construction, holding the
	// service's lifecycle lock - and the cleanup that closes the service would then wait for it
	// forever. Releasing the window here is what makes a failed assertion a failed test instead of a
	// hung suite, which matters most for the two failure paths that are the point of this file: the
	// pre-fix RED run and the reverse break.
	t.Cleanup(window.releaseWindow)
	return &platformEventsDeviceAxisFixture{
		started: started,
		server:  server,
		events:  events,
		window:  window,
	}
}

// enterReloadWindow starts a reload and returns once the service is provably inside its nil-instance
// window; the returned function waits for that reload to finish.
//
// It may be called once per fixture: the barrier fires once by construction (`CompareAndSwap`), so a
// second call would not block and would silently assert nothing.
func (f *platformEventsDeviceAxisFixture) enterReloadWindow(t *testing.T) func() {
	t.Helper()
	f.window.arm()
	reloaded := make(chan error, 1)
	go func() {
		reloaded <- f.server.StartOrReloadService(platformEventsConfig, &OverrideOptions{})
	}()
	select {
	case <-f.window.entered():
	case <-time.After(reloadWindowDeadline):
		t.Fatal("the reload never reached the manager: the barrier is stale and this test proved nothing")
	}
	require.Nil(t, f.started.Instance(),
		"the service has an instance while the reload is in flight, so this is not the window the "+
			"device-level fact has to survive")
	return func() {
		f.window.releaseWindow()
		require.NoError(t, <-reloaded, "the service reload failed")
		require.NotNil(t, f.started.Instance(), "the reload produced no running instance")
	}
}

// rememberedDeviceLevel reads the reload mirror the way `restoreDeviceLevel` reads it, under the lock
// that owns it.
func rememberedDeviceLevel(server *CommandServer) (paused bool, touched bool) {
	server.axisAccess.Lock()
	defer server.axisAccess.Unlock()
	return server.deviceAxisPaused, server.deviceAxisTouched
}

// TestPlatformDevicePauseInTheNilInstanceWindowIsRemembered is the pause direction of N-9.
//
// `PlatformEvents` decides `DevicePause`, the target resolves no instance, the manager therefore
// cannot run - and the mirror must STILL record the level, because the mirror is the only thing the
// reload hook can apply to the Box that is being built right now.
func TestPlatformDevicePauseInTheNilInstanceWindowIsRemembered(t *testing.T) {
	fixture := newPlatformEventsDeviceAxisFixture(t)
	manager := fixture.started.Instance().PauseManager()
	require.NotNil(t, manager, "the running Box has no pause manager, so the device axis does not exist")
	require.False(t, manager.IsDevicePaused(), "a fresh Box must not start paused")

	paused, touched := rememberedDeviceLevel(fixture.server)
	require.False(t, touched, "a fresh CommandServer has been told nothing about the device")
	require.False(t, paused)

	finishReload := fixture.enterReloadWindow(t)

	// The fact arrives with no instance to act on. This is the window, and it is held open.
	fixture.events.SetScreenOn(false)

	paused, touched = rememberedDeviceLevel(fixture.server)
	require.True(t, touched,
		"the device-level fact was lost to the nil instance: nothing recorded that the device is "+
			"locked, so restoreDeviceLevel's `!touched` guard returns and the replacement Box starts "+
			"unpaused on a locked device")
	require.True(t, paused,
		"the mirror does not hold the level the shared policy decided")
	require.False(t, manager.IsDevicePaused(),
		"the manager was paused while the service had no instance, so this test is not exercising the "+
			"window: the fact reached a Box")

	finishReload()

	// The reload completed and the fact is still there, which is what the reload hook applies. The
	// manager half - the replacement Box ends PAUSED - is asserted by
	// TestThePlatformPauseInTheNilInstanceWindowIsRestoredByTheReload in the p2ios build, where
	// restoreDeviceLevel is not compiled out.
	paused, touched = rememberedDeviceLevel(fixture.server)
	require.True(t, touched)
	require.True(t, paused)
}

// TestPlatformDeviceWakeInTheNilInstanceWindowIsRemembered is the wake direction, and it is the
// dangerous one.
//
// The device was paused, the fact that lifts the pause arrives while there is no Box, and the manager
// therefore stays paused. If the mirror is not moved, the reload finds a mirror that still says
// "paused" and agrees with a manager that is still paused - so nothing corrects either of them, and
// the level latches on a device that is in the user's hand. With the mirror moved, the two disagree
// after the reload and `restoreDeviceLevel` releases the level.
func TestPlatformDeviceWakeInTheNilInstanceWindowIsRemembered(t *testing.T) {
	fixture := newPlatformEventsDeviceAxisFixture(t)
	manager := fixture.started.Instance().PauseManager()
	require.NotNil(t, manager)

	// A live pause, delivered the way the platform delivers it, so the manager is genuinely in the
	// paused level before the window opens - and the mirror is put in the same state through the
	// product's own mirror writer. Seeding it here rather than relying on the pause fact above is what
	// keeps this test's assertion about the WAKE: the Android path never wrote the mirror at all before
	// the fix, so without the seed the failure would say "nothing was ever recorded" instead of "the
	// wake was lost", and the second half of the harm - a reload that re-enters the level - would never
	// be reached.
	fixture.events.SetScreenOn(true)
	fixture.events.SetAppForeground(true)
	fixture.events.SetScreenOn(false)
	require.True(t, manager.IsDevicePaused(), "the fixture did not enter the paused level")
	fixture.server.rememberDeviceLevel(true)
	paused, touched := rememberedDeviceLevel(fixture.server)
	require.True(t, touched)
	require.True(t, paused)

	finishReload := fixture.enterReloadWindow(t)

	// The wake arrives in the window: the shared policy publishes it, the target finds no instance.
	fixture.events.ReportDeviceWake()

	paused, touched = rememberedDeviceLevel(fixture.server)
	require.True(t, touched)
	require.False(t, paused,
		"the wake was lost to the nil instance: the mirror still says the device is paused, so the "+
			"reload finds it in agreement with the manager the wake never reached and re-enters the "+
			"level on a device that is being used")
	require.True(t, manager.IsDevicePaused(),
		"the manager was woken while the service had no instance, so this test is not exercising the "+
			"window: the fact reached a Box")

	finishReload()

	paused, touched = rememberedDeviceLevel(fixture.server)
	require.True(t, touched)
	require.False(t, paused)
}

// TestPlatformDeviceLevelMirrorAndManagerDoNotDrift is the no-window control.
//
// With a live Box the mirror and the manager must describe the same device: the mirror is written by
// the action that moves the manager, and an action the policy withholds must leave both alone. Without
// this, a mirror that was simply always written on every fact would satisfy the two window tests while
// disagreeing with the real level.
func TestPlatformDeviceLevelMirrorAndManagerDoNotDrift(t *testing.T) {
	fixture := newPlatformEventsDeviceAxisFixture(t)
	manager := fixture.started.Instance().PauseManager()
	require.NotNil(t, manager)

	fixture.events.SetScreenOn(true)
	fixture.events.SetAppForeground(true)
	require.False(t, manager.IsDevicePaused())

	// Screen off: the action reaches the manager AND is recorded.
	fixture.events.SetScreenOn(false)
	require.True(t, manager.IsDevicePaused(), "a screen-off fact must pause the device axis")
	paused, touched := rememberedDeviceLevel(fixture.server)
	require.True(t, touched)
	require.True(t, paused, "the mirror and the manager disagree about a live pause")

	// Screen on with the app in the background: the policy defers the wake, so neither moves.
	fixture.events.SetAppForeground(false)
	fixture.events.SetScreenOn(true)
	require.True(t, manager.IsDevicePaused(), "a deferred wake must not lift the pause")
	paused, _ = rememberedDeviceLevel(fixture.server)
	require.True(t, paused, "the mirror moved for an action the policy did not publish")

	// The app comes forward: the deferred wake is released, and again both move together.
	fixture.events.SetAppForeground(true)
	require.False(t, manager.IsDevicePaused(), "the released wake must lift the pause")
	paused, touched = rememberedDeviceLevel(fixture.server)
	require.True(t, touched)
	require.False(t, paused, "the mirror and the manager disagree about a live wake")

	// An explicit user-present on an already-awake device is coalesced by the edge latch: nothing
	// moves, and the mirror must not claim otherwise.
	fixture.events.ReportDeviceWake()
	require.False(t, manager.IsDevicePaused())
	paused, _ = rememberedDeviceLevel(fixture.server)
	require.False(t, paused)
}

// platformEventsConfig is the smallest profile a Box builds from. The device axis exists before any
// inbound, outbound, endpoint or service, so the profile carries none of them and these tests cannot
// be confused by a subsystem they did not mean to start.
const platformEventsConfig = `{"log":{"level":"error"}}`

// platformEventsPlatform is the platform surface `NewCommandServer` consumes. Embedding the interface
// means every method these tests do not reach is a nil-interface call - a visible panic - rather than a
// silent zero value.
type platformEventsPlatform struct {
	PlatformInterface
}

// The assertion is the point: a new eager call in the constructor becomes a compile error here rather
// than a nil dereference in a test run.
var _ PlatformInterface = platformEventsPlatform{}

func (platformEventsPlatform) LocalDNSTransport() LocalDNSTransport { return nil }

func (platformEventsPlatform) UseProcFS() bool { return false }

func (platformEventsPlatform) UsePlatformAutoDetectInterfaceControl() bool { return false }

// platformEventsHandler is the `CommandServerHandler` half - the callbacks the command server makes
// INTO the platform. None of them is reached by a start, a reload or a device-level fact, and
// embedding the interface makes any that were reached a visible panic rather than a silent no-op.
type platformEventsHandler struct {
	CommandServerHandler
}
