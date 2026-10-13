package libbox

import (
	"testing"

	"github.com/sagernet/sing-box/common/power"
	C "github.com/sagernet/sing-box/constant"

	"github.com/stretchr/testify/require"
)

// ============================================================================================
// SEAM — crossing the platform gates so the window's motion can be measured on Windows
// ============================================================================================
//
// This file is a `_test.go` file, so it is NEVER compiled into the shipped package, and it exists only
// because two predicates make the code under test unreachable on this host:
//
//	restoreDeviceLevel      if !(C.IsAndroid || C.IsIos) || C.IsTvOS { return }   command_server.go:354
//	RecordLockState         if !C.IsIos || C.IsTvOS { return }                   command_server.go:538
//	RecordScreenState       if !C.IsIos || C.IsTvOS { return }                   command_server.go:509
//
// On windows/amd64 `C.IsAndroid` and `C.IsIos` are both false (constant/goos/zgoos_windows.go), so the
// reconciliation body never runs. The seam below is the only way to execute it here.
//
// # What the seam is, and what it costs
//
// `seamDeviceAxis` is a package variable carrying the same THREE edits the reverse-break used:
//
//	command_server.go:354   if !(C.IsAndroid || C.IsIos) || C.IsTvOS {   ->   if !seamDeviceAxis() {
//	command_server.go:509   if !C.IsIos || C.IsTvOS {                    ->   if !seamAppleDeviceAxis() {
//	command_server.go:538   if !C.IsIos || C.IsTvOS {                    ->   if !seamAppleDeviceAxis() {
//
// It is off by default, which is the production answer on this host, and
// `TestWindowReconciliationIsInertWithoutTheSeam` asserts that default so a passing reconciliation test
// cannot be read as the gate not mattering.
//
// # What the seam does NOT establish
//
// That iOS or Android reaches these bodies in production. That is the platform gate, and the gate is
// SOURCE-PROVEN only: `C.IsAndroid`/`C.IsIos` are build-tag constants and no test on this host can
// observe the iOS or Android build of this package.

// seamDeviceAxis simulates a platform whose device axis exists. False in every test except the ones
// that measure the reconciliation.
var seamDeviceAxis bool

// seamDeviceAxisGate and seamAppleDeviceAxisGate are what the SEAMED build of command_server.go calls
// INSTEAD OF its constant predicates. They are defined only here, so the seamed file cannot compile
// without this one and the patch can never be mistaken for production.
func seamDeviceAxisGate() bool {
	return (C.IsAndroid || C.IsIos) && !C.IsTvOS || seamDeviceAxis
}

func seamAppleDeviceAxisGate() bool {
	return C.IsIos && !C.IsTvOS || seamDeviceAxis
}

// TestWindowReconciliationIsInertWithoutTheSeam is the control for the seam itself.
//
// With the seam off — the production answer on this host — the same timeline that the reconciliation
// is written for leaves the level lost, so the seam is doing exactly the work claimed for it and
// nothing else.
func TestWindowReconciliationIsInertWithoutTheSeam(t *testing.T) {
	require.False(t, seamDeviceAxis, "the seam must default to the production answer")

	fixture := newDeviceAxisFixture(t)
	t.Cleanup(fixture.sink.Close)

	fixture.reload()

	// The fact is delivered through the production Apple entry point, which on this host returns before
	// it can record anything, so the mirror is untouched and the reconciliation has nothing to restore.
	delivered := fixture.deliverInWindow(func() { fixture.server.RecordLockState(true) })
	fixture.reload()
	<-delivered
	fixture.checkWindow()

	fixture.server.axisAccess.Lock()
	touched := fixture.server.deviceAxisTouched
	fixture.server.axisAccess.Unlock()
	require.False(t, touched,
		"the mirror must be untouched with the seam off; this is what makes the gate the only blocker")

	require.False(t, fixture.pauseManager().IsDevicePaused(),
		"without the platform gate crossed neither the mirror write nor the reconciliation runs, and the "+
			"level stays lost; this is the production answer on a host that is neither iOS nor Android")
}

// TestWindowReconciliationClosesTheWindow is requirement 3 under the seam: the same fact, the same
// window, the same production reload path, with the mirror write and `restoreDeviceLevel` reachable.
//
// It is driven through the APPLE entry points, which is what the landed fix covers, and it is therefore
// also the demonstration of the asymmetry: the same test through the DIRECT writer fails
// (`TestDeviceAxisDirectWriterIsNotReconciled/during`), because that writer never touches the mirror.
func TestWindowReconciliationClosesTheWindow(t *testing.T) {
	previous := seamDeviceAxis
	seamDeviceAxis = true
	t.Cleanup(func() { seamDeviceAxis = previous })

	fixture := newDeviceAxisFixture(t)
	t.Cleanup(fixture.sink.Close)

	fixture.reload()
	require.False(t, fixture.pauseManager().IsDevicePaused())

	// The device locks IN the window. With the gate crossed, `RecordLockState` records its level before
	// the nil-instance return, so the fact survives even though no Box could receive it.
	delivered := fixture.deliverInWindow(func() { fixture.server.RecordLockState(true) })
	fixture.reload()
	<-delivered
	fixture.checkWindow()

	// DIRECTION 1 — the level was not delivered, and `restoreDeviceLevel` re-enters it, so the Box that
	// now exists is told the device is locked and its governor is built believing so.
	require.True(t, fixture.pauseManager().IsDevicePaused(),
		"the reconciliation must re-enter the level the mirror recorded while there was no Box")
	require.False(t, fixture.governor().Active(),
		"the running Box's governor must be built believing the device is asleep")

	// DIRECTION 2 — the unlock, with a live Box. It must release the level.
	//
	// The governor is asserted against the SUPPRESSING states rather than against `Active()`: a wake
	// legitimately lands on `StateWaking`, where the business path is up and the speculative categories
	// are released on the stagger (common/power's `beginWakingLocked`). Demanding ACTIVE, or demanding
	// any particular category at that instant, would be asserting that the stagger does not exist —
	// `DefaultPolicy`'s shortest stagger is 5s.
	fixture.server.RecordLockState(false)
	require.False(t, fixture.pauseManager().IsDevicePaused(),
		"the unlock must release the level the reconciliation re-entered")
	released := fixture.governor()
	require.NotEqual(t, power.StateQuiescent, released.State(),
		"the governor must leave the suppressed state once the device is usable")
	require.NotEqual(t, power.StateDeepIdle, released.State())
}

// TestWindowReconciliationDoesNotManufactureASleepEdge is requirement 3's "no spurious edge" half under
// the seam, where the reconciliation actually runs.
//
// A reconciliation that entered the level through the EDGE would stamp `pausedSince` at reload time and
// turn a four-hour lock into a zero-second "nothing happened", losing exactly the pool retirement a long
// sleep earns.
func TestWindowReconciliationDoesNotManufactureASleepEdge(t *testing.T) {
	previous := seamDeviceAxis
	seamDeviceAxis = true
	t.Cleanup(func() { seamDeviceAxis = previous })

	fixture := newDeviceAxisFixture(t)
	t.Cleanup(fixture.sink.Close)

	fixture.reload()

	delivered := fixture.deliverInWindow(func() { fixture.server.RecordLockState(true) })
	fixture.reload()
	<-delivered
	fixture.checkWindow()

	governor := fixture.governor()
	require.False(t, governor.Active(),
		"precondition: the reconciliation must have re-entered the level")

	require.EqualValues(t, 0, governor.ReuseEpoch(),
		"a level restored across a reload is not a resume, so it must publish no reuse boundary")
	_, hasBoundary := governor.LastReuseBoundary()
	require.False(t, hasBoundary, "no boundary may be manufactured by the reload window")

	fixture.server.RecordLockState(false)
	require.False(t, fixture.pauseManager().IsDevicePaused())
	require.EqualValues(t, 0, fixture.governor().ReuseEpoch(),
		"a Box that never saw the sleep must not publish a boundary for it")
	_, hasBoundary = fixture.governor().LastReuseBoundary()
	require.False(t, hasBoundary)
}
