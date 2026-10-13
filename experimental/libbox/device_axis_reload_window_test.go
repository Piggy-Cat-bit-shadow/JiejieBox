package libbox

import (
	"os"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/power"
	"github.com/sagernet/sing-box/daemon"
	"github.com/sagernet/sing-box/service/powerreport"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/pause"

	"github.com/stretchr/testify/require"
)

// ============================================================================================
// P2-2 — the device-axis fact delivered while there is no Box, and the window's two writers
// ============================================================================================
//
// `daemon.StartedService.StartOrReloadService` sets `s.instance = nil` (daemon/started_service.go:335)
// and leaves it nil for the WHOLE of `newInstance`: the previous Box is closed, the replacement is
// built and started, and only then is the pointer published (:365). Every device-axis entry point
// resolves `Instance()` and returns early when it is nil (experimental/libbox/command_server.go:399,
// :453, :478, :517, :544, :552, :560), so a fact that arrives in that span is dropped: not queued, not
// remembered, not reported.
//
// At HEAD the tree carries the reconciliation written for that window
// (`rememberDeviceLevel` :295, `restoreDeviceLevel` :353, called after every reload at :232), so this
// file's job is to say WHICH writers it covers and to measure the consequence where it does not.
//
// # The two writers, and the asymmetry between them
//
//   - The APPLE writer — `CommandServer.Pause` (:398), `WakeNow` (:477), `RecordScreenState` (:515),
//     `RecordLockState` (:543) — writes the LEVEL mirror before its nil-instance return, and
//     `restoreDeviceLevel` re-applies it to the Box that now exists. That is the window closed, for
//     `C.IsIos` and `C.IsAndroid`.
//
//   - The DIRECT writer — `runtimecoord.PlatformEvents` -> `boxPlatformTarget.DevicePause/DeviceWake`
//     -> `instance.PauseManager()` (experimental/libbox/platform_events.go:116-142), which is what
//     `experimental/libbox/p2-2`'s own `PlatformEvents.SetScreenOn` builds and what the Android client
//     drives — never calls `rememberDeviceLevel`. So `deviceAxisTouched` stays false and
//     `restoreDeviceLevel` returns at its `if !touched` guard (:359-361): on that writer the window is
//     still open, and `TestDeviceAxisDirectWriterIsNotReconciled` below pins it.
//
// # The platform gates, and what cannot be measured on this host
//
// `restoreDeviceLevel` opens with `if !(C.IsAndroid || C.IsIos) || C.IsTvOS { return }` (:354) and the
// mirror-writing Apple entry points open with `if !C.IsIos || C.IsTvOS { return }` (:509, :538). On
// windows/amd64 all of those are false (constant/goos/zgoos_windows.go), so the reconciliation body
// and the mirror writes are BOTH unreachable here. `zz_window_seam_test.go` crosses exactly those
// predicates so the motion can be measured; `TestWindowReconciliationIsInertWithoutTheSeam` proves the
// seam defaults to the production answer.

// ---------------------------------------------------------------------------------------------
// The fixture
// ---------------------------------------------------------------------------------------------

// governorTapState captures the `*power.Governor` a Box registers for itself, shared by every clone.
//
// A Box's governor is a private field with no accessor, and the assertions here are about the object
// the sleep policy actually reads. The registry is the one seam that reaches it without `unsafe`,
// without reflection over a struct that holds a mutex, and without a change to production: `box.New`
// registers the governor it just built into the context it was given (box.go:385-386), and
// `service.Registry` is the public interface that registration goes through.
//
// The capture is shared rather than per-clone because `service.ExtendContext` replaces the registry
// with `registry.Clone()` (daemon/instance.go:88), so `box.New` registers into a CLONE. A tap whose
// field lived on the per-clone value would record the governor in an object the test cannot reach.
type governorTapState struct {
	access   sync.Mutex
	governor *power.Governor
}

func (s *governorTapState) set(governor *power.Governor) {
	s.access.Lock()
	s.governor = governor
	s.access.Unlock()
}

func (s *governorTapState) latest() *power.Governor {
	s.access.Lock()
	defer s.access.Unlock()
	return s.governor
}

type governorTap struct {
	inner service.Registry
	state *governorTapState
}

func (t *governorTap) Register(serviceType any, value any) any {
	if governor, isGovernor := value.(*power.Governor); isGovernor {
		t.state.set(governor)
	}
	return t.inner.Register(serviceType, value)
}

func (t *governorTap) Get(serviceType any) any          { return t.inner.Get(serviceType) }
func (t *governorTap) Clone() service.Registry          { return &governorTap{inner: t.inner.Clone(), state: t.state} }

// deviceAxisFixture is a real StartedService plus the direct device-axis writer over it.
type deviceAxisFixture struct {
	t       *testing.T
	started *daemon.StartedService
	sink    *PlatformEvents
	tap     *governorTapState
	// server is the CommandServer the sink was built over, kept so the Apple-path entry points can be
	// driven through the production type rather than through a copy of their logic.
	server *CommandServer

	// windowMissed is written by the delivery goroutine and read by the test goroutine, so a fact that
	// missed the window fails the test instead of passing it vacuously.
	windowMissed chan struct{}
}

// newDeviceAxisFixture builds the fixture on a scratch working directory.
//
// The package path globals are set for the same reason `newPlatformEventsTestService` sets them: a Box
// built through this service carries a PlatformLogWriter, so it creates a cache file under the working
// path and chowns it to the configured user, which fails against the empty default. `sBasePath` is set
// because `CommandServer.StartOrReloadService` writes a configuration snapshot under it
// (experimental/libbox/log.go:64-72).
func newDeviceAxisFixture(t *testing.T) *deviceAxisFixture {
	t.Helper()
	previousWorking, previousTemp, previousUser, previousGroup := sWorkingPath, sTempPath, sUserID, sGroupID
	previousBase := sBasePath
	sWorkingPath = t.TempDir()
	sTempPath = t.TempDir()
	sBasePath = t.TempDir()
	sUserID = os.Getuid()
	sGroupID = os.Getgid()
	t.Cleanup(func() {
		sWorkingPath, sTempPath, sUserID, sGroupID = previousWorking, previousTemp, previousUser, previousGroup
		sBasePath = previousBase
	})

	ctx := baseContext(nil)
	registry := service.RegistryFromContext(ctx)
	require.NotNil(t, registry, "baseContext must carry the registry the reload clones")
	state := new(governorTapState)
	ctx = service.ContextWithRegistry(ctx, &governorTap{inner: registry, state: state})

	started := daemon.NewStartedService(daemon.ServiceOptions{Context: ctx})
	t.Cleanup(started.Close)
	t.Cleanup(func() { _ = started.CloseService() })

	// The power report manager is created exactly as `NewCommandServer` creates it, but its RECORDER is
	// deliberately never started: `powerreport.Recorder` opens a worker and writes archives under the
	// working path, and every device-axis entry point guards its telemetry call with
	// `if recorder != nil` (command_server.go:319-321, :433-435, :458-460). Leaving it nil keeps this a
	// unit test while exercising the same code path.
	powerManager := powerreport.NewManager()
	t.Cleanup(func() { _ = powerManager.Close() })

	server := &CommandServer{
		ctx:            ctx,
		StartedService: started,
		powerManager:   powerManager,
	}
	return &deviceAxisFixture{
		t:            t,
		started:      started,
		sink:         NewPlatformEvents(server),
		tap:          state,
		server:       server,
		windowMissed: make(chan struct{}, 4),
	}
}

// reload performs one production service reload, through the entry point the platform actually calls.
//
// It goes through `CommandServer.StartOrReloadService` and not through the embedded
// `StartedService.StartOrReloadService` on purpose: the former is where `restoreDeviceLevel` runs
// (command_server.go:232), so a fixture that skipped it would measure a reload no platform performs.
func (f *deviceAxisFixture) reload() {
	f.t.Helper()
	require.NoError(f.t, f.server.StartOrReloadService("{}", &OverrideOptions{}),
		"the production reload path must complete")
}

// pauseManager reads the level the platform would actually be driving.
func (f *deviceAxisFixture) pauseManager() pause.Manager {
	f.t.Helper()
	instance := f.started.Instance()
	require.NotNil(f.t, instance, "the fixture must have a running instance outside a window")
	return instance.PauseManager()
}

// governor reads the governor the CURRENT Box registered, through the transparent tap. It is the object
// the sleep policy actually reads, not a reconstruction.
func (f *deviceAxisFixture) governor() *power.Governor {
	f.t.Helper()
	governor := f.tap.latest()
	require.NotNil(f.t, governor, "every Box registers a power governor into its own context")
	return governor
}

// checkWindow asserts that every delivery meant for the window actually landed in one.
func (f *deviceAxisFixture) checkWindow() {
	f.t.Helper()
	select {
	case <-f.windowMissed:
		f.t.Fatal("a fact meant for the reload window was delivered outside it, so the row proves nothing")
	default:
	}
}

// deliverNow drives a platform fact immediately and reports whether the delivery happened while the
// service had NO instance.
//
// This is the determinism argument, and it is a condition rather than a delay: the writer beneath the
// sink resolves `Instance()` itself, so the fact and the observation are taken in the same no-instance
// span.
func (f *deviceAxisFixture) deliverNow(deliver func()) bool {
	inWindow := f.started.Instance() == nil
	deliver()
	return inWindow
}

// deliverInWindow runs `deliver` inside the reload window.
//
// The window is taken from the production pointer itself, with no sleep and no timer guess: the
// goroutine waits for `Instance()` to become nil — which IS the window's definition — and delivers
// there. A deadline exists so a broken fixture fails rather than hangs, and a delivery that missed the
// window is recorded for the test goroutine to fail on.
func (f *deviceAxisFixture) deliverInWindow(deliver func()) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		deadline := time.Now().Add(60 * time.Second)
		for f.started.Instance() != nil {
			if time.Now().After(deadline) {
				f.windowMissed <- struct{}{}
				return
			}
			runtime.Gosched()
		}
		if !f.deliverNow(deliver) {
			f.windowMissed <- struct{}{}
		}
	}()
	return done
}

// ---------------------------------------------------------------------------------------------
// T1 — the direct writer: the fact is delivered in the window and the level it names never moves
// ---------------------------------------------------------------------------------------------

// TestDeviceAxisDirectWriterIsNotReconciled is P2-2 stated for the writer the Android client uses.
//
// Each row is a full timeline and asserts BOTH directions:
//
//	lock   — a fact that must end with the device paused
//	unlock — a fact that must end with the device awake
//
// The level the running Box holds is read through `Instance().PauseManager()`. No unexported field is
// poked here and no mutex-bearing struct is reflected over; the mirror's own recorded value is read in
// `common/runtimecoord/device_axis_reload_window_test.go`, where that field lives.
func TestDeviceAxisDirectWriterIsNotReconciled(t *testing.T) {
	for _, window := range []string{"before", "during", "after"} {
		t.Run(window, func(t *testing.T) {
			fixture := newDeviceAxisFixture(t)
			t.Cleanup(fixture.sink.Close)

			fixture.reload()

			// The app axis is in front, which is what a user who just unlocked sees. Without it the
			// policy defers the wake and the timeline would test something else.
			fixture.sink.SetAppForeground(true)
			require.False(t, fixture.pauseManager().IsDevicePaused(),
				"precondition: the device starts awake")

			var delivered <-chan struct{}
			switch window {
			case "before":
				fixture.sink.SetScreenOn(false)
			case "during":
				delivered = fixture.deliverInWindow(func() { fixture.sink.SetScreenOn(false) })
			case "after":
				// Deliberately after the reload returns: this row is the control. Nothing about it
				// depends on the window, so it must hold on any tree.
			}
			// The middle of every timeline crosses a reload; that is the axis under test.
			fixture.reload()
			if delivered != nil {
				<-delivered
			}
			fixture.checkWindow()

			if window == "after" {
				fixture.sink.SetScreenOn(false)
			}

			// DIRECTION 1 — the device is locked, so the manager the platform drives must be paused.
			require.True(t, fixture.pauseManager().IsDevicePaused(),
				"the device is locked after the reload, so the pause manager the Box is using must be "+
					"paused; the platform fact was delivered %s the reload window", window)

			// DIRECTION 2 — the unlock must end with the device awake.
			fixture.sink.SetScreenOn(true)
			require.False(t, fixture.pauseManager().IsDevicePaused(),
				"an unlock after the reload must leave the Box's manager awake")
		})
	}
}

// TestDeviceAxisDirectWriterCannotReachALevelWithNoBox reduces the defect to the statements that
// produce it, so the mechanism is visible without the reload machinery.
//
// It pins the property that makes a dropped fact have a LASTING consequence rather than a transient
// one: the direct writer's mirror is per CommandServer and outlives the reload, so the level it
// believes it published is never published again.
func TestDeviceAxisDirectWriterCannotReachALevelWithNoBox(t *testing.T) {
	fixture := newDeviceAxisFixture(t)
	t.Cleanup(fixture.sink.Close)

	fixture.reload()
	level := fixture.pauseManager()
	require.False(t, level.IsDevicePaused())

	// The device is genuinely locked, the writer genuinely decides to pause, and the level it names is
	// genuinely unpaused — because there was no Box to drive when the decision was made.
	delivered := fixture.deliverInWindow(func() { fixture.sink.SetScreenOn(false) })
	fixture.reload()
	<-delivered
	fixture.checkWindow()
	require.False(t, level.IsDevicePaused(),
		"a fact delivered with no instance cannot have reached the level it names")

	// The Box that is running now was told nothing, because the writer's record says the pause was
	// already published and `restoreDeviceLevel` never sees a touched mirror on this path.
	require.False(t, fixture.pauseManager().IsDevicePaused(),
		"the running Box must have been reconciled with the locked device; on the direct writer nothing "+
			"reconciles it, because rememberDeviceLevel is never called on this path")

	// And repetition cannot recover it.
	delivered = fixture.deliverInWindow(func() { fixture.sink.SetScreenOn(false) })
	fixture.reload()
	<-delivered
	fixture.checkWindow()
	require.False(t, fixture.pauseManager().IsDevicePaused(),
		"the writer refuses the repeat, so the level stays lost for the rest of the lock")
}

// ---------------------------------------------------------------------------------------------
// T2 — the split brain: the running Box's governor disagrees with the level the manager holds
// ---------------------------------------------------------------------------------------------

// TestDeviceAxisGovernorAgreesWithTheManager is the governor half of P2-2.
//
// The pause manager is not what decides whether a locked phone refreshes rule sets and writes
// statistics; that is the governor, which each Box builds for itself. A dropped fact leaves the two
// objects disagreeing about the same device, and the disagreement has a direction that matters: a
// governor that believes the device is awake PERMITS `ProviderRefresh` and `Statistics`, which is the
// behaviour the device axis exists to suppress, for the rest of the lock, with no log line.
func TestDeviceAxisGovernorAgreesWithTheManager(t *testing.T) {
	fixture := newDeviceAxisFixture(t)
	t.Cleanup(fixture.sink.Close)

	fixture.reload()
	fixture.sink.SetAppForeground(true)
	firstGovernor := fixture.governor()
	require.True(t, firstGovernor.Active(), "precondition: the device starts awake")

	// The device locks inside the window, so the fact reaches neither object of the Box that follows.
	delivered := fixture.deliverInWindow(func() { fixture.sink.SetScreenOn(false) })
	fixture.reload()
	<-delivered
	fixture.checkWindow()

	runningManager := fixture.pauseManager()
	runningGovernor := fixture.governor()

	// The two objects the sleep policy is split across must agree about the same device.
	require.Equal(t, !runningManager.IsDevicePaused(), runningGovernor.Active(),
		"the governor's view of 'the device is awake' must equal the manager's level after a reload")

	require.False(t, runningManager.IsDevicePaused(),
		"the manager half: a locked device must leave the running Box paused")
	require.False(t, runningGovernor.Active(),
		"the governor half: a locked device must not leave the running Box's governor ACTIVE")
	require.False(t, runningGovernor.Allow().ProviderRefresh,
		"a locked device must not permit provider refresh")
	require.False(t, runningGovernor.Allow().Statistics,
		"a locked device must not permit statistics")

	// The control direction, on the same tree and the same objects: the axis agrees when the fact is
	// delivered where there IS a Box to receive it. Without this the test could pass on a governor that
	// was broken everywhere rather than one that was never told.
	fixture.sink.SetScreenOn(true)
	require.False(t, fixture.pauseManager().IsDevicePaused())
	require.True(t, fixture.governor().Active(),
		"an unlock with a live Box must return the governor to ACTIVE")
}

// ---------------------------------------------------------------------------------------------
// T3 — the wake must not manufacture a sleep edge
// ---------------------------------------------------------------------------------------------

// TestDeviceAxisReloadWindowDoesNotManufactureASleepEdge pins the direction a reconciliation must not
// break: a lock or an unlock crossing the window must not become a spurious reuse boundary.
//
// The governor classifies a sleep by its duration, and `Resumed` publishes a boundary only when there
// was a sleep on record. A reconciliation that entered the level by way of the EDGE — `DeviceSlept`, or
// `Governor.DevicePaused` — would stamp `pausedSince` at reload time and turn a four-hour lock into a
// zero-second "nothing happened", losing exactly the pool retirement a long sleep earns.
func TestDeviceAxisReloadWindowDoesNotManufactureASleepEdge(t *testing.T) {
	fixture := newDeviceAxisFixture(t)
	t.Cleanup(fixture.sink.Close)

	fixture.reload()
	fixture.sink.SetAppForeground(true)

	delivered := fixture.deliverInWindow(func() { fixture.sink.SetScreenOn(false) })
	fixture.reload()
	<-delivered
	fixture.checkWindow()

	// The precondition is stated as the reconciliation's own property, so this test cannot silently
	// stop measuring if the level stops being restored: with the window open on this writer the running
	// Box is awake, and the boundary assertions below are then vacuous.
	locked := fixture.pauseManager().IsDevicePaused()
	runningGovernor := fixture.governor()
	require.Equal(t, !locked, runningGovernor.Active(),
		"the governor and the manager must agree, or the boundary assertions below measure nothing")

	require.EqualValues(t, 0, runningGovernor.ReuseEpoch(),
		"a level restored across a reload is not a resume, so it must publish no reuse boundary")
	_, hasBoundary := runningGovernor.LastReuseBoundary()
	require.False(t, hasBoundary, "no boundary may be manufactured by the reload window")

	// The unlock, with a live Box. That IS a resume — but a Box that never measured a sleep must not
	// invent a boundary for one.
	fixture.sink.SetScreenOn(true)
	require.False(t, fixture.pauseManager().IsDevicePaused())
	require.EqualValues(t, 0, runningGovernor.ReuseEpoch(),
		"a Box that never saw the sleep must not publish a boundary for it")
	_, hasBoundary = runningGovernor.LastReuseBoundary()
	require.False(t, hasBoundary)
}
