package runtimecoord

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// ============================================================================================
// P2-2, the MIRROR half — read where the mirror actually lives
// ============================================================================================
//
// `experimental/libbox/device_axis_reload_window_test.go` proves the LEVEL half of P2-2 through the
// production reload path, but the mirror itself (`PlatformEvents.paused`) is unexported and the libbox
// wrapper exposes no reader for it. It is read here, in the package that owns it, so the claim "the
// mirror and the real level disagree across a reload window" is measured rather than inferred.
//
// # What the model below is, and what it is not
//
// The policy under test is the PRODUCTION `PlatformEvents`. Only the TARGET is modelled, on the one
// thing that matters for this defect: the real target resolves the running Box per call and returns
// when there is none (`experimental/libbox/platform_events.go`'s `boxPlatformTarget.live`,
// `DevicePause`, `DeviceWake`). The reload window is therefore the period in which the target has no
// current level while the policy keeps running, and the model reproduces exactly that: the level
// pointer is cleared, a replacement appears, and the policy's own `DevicePause`/`DeviceWake` are
// passed through unchanged.
//
// # The mechanism, measured
//
//	                       mirror(paused)  running level    publishes
//	screen on, app in front      false          false            -
//	reload window opens           -              -              -
//	SCREEN OFF in the window     TRUE           false        Pause -> unreachable
//	replacement appears          TRUE           false            -
//	screen on                    false          false        Wake  -> the replacement
//
// So the writer ends the cycle having published one pause and one wake against a level that was never
// paused. Two facts are wrong and one edge is spurious:
//
//   - the PAUSE was decided, recorded, and never delivered. For the whole of that screen-off the core
//     is awake: the governor permits provider refresh, statistics and probing, which is precisely what
//     the device axis exists to suppress.
//   - the WAKE is delivered to a level that was never paused, so the resume edge is spent on an awake
//     period that never had a pause.
//
// And the divergence itself is the state in the middle rows: the mirror asserts a device state the level
// the platform drives does not have.

// reloadableLevel is the level a Box holds, as the platform would be driving it.
type reloadableLevel struct {
	devicePaused bool

	pauses int
	wakes  int
}

func (l *reloadableLevel) DevicePause() {
	l.pauses++
	l.devicePaused = true
}

func (l *reloadableLevel) DeviceWake() {
	l.wakes++
	l.devicePaused = false
}

// reloadableTarget is the model of `libbox.boxPlatformTarget`: it resolves the level that is current at
// the moment of the call, and a call that finds no current level is a no-op.
type reloadableTarget struct {
	current *reloadableLevel
}

func (t *reloadableTarget) TrimMemory(TrimLevel)    {}
func (t *reloadableTarget) ReleaseMemory(TrimLevel) {}

func (t *reloadableTarget) DevicePause() {
	if t.current == nil {
		return
	}
	t.current.DevicePause()
}

func (t *reloadableTarget) DeviceWake() {
	if t.current == nil {
		return
	}
	t.current.DeviceWake()
}

// TestPlatformMirrorDivergesFromTheLevelAcrossAReloadWindow is P2-2 in both directions, with the
// mirror's own value asserted at every step.
func TestPlatformMirrorDivergesFromTheLevelAcrossAReloadWindow(t *testing.T) {
	first := &reloadableLevel{}
	target := &reloadableTarget{current: first}
	policy := NewPlatformEvents(target)
	t.Cleanup(policy.Close)

	// A user who has just unlocked: every axis agrees that the device is awake and in use.
	policy.SetScreenOn(true)
	policy.SetAppForeground(true)
	require.False(t, policy.paused)
	require.False(t, first.devicePaused)
	require.EqualValues(t, 0, policy.Stats().Pauses)
	require.EqualValues(t, 0, policy.Stats().Wakes)

	// ---- the reload window opens: no running Box, so the target cannot reach a level ----
	target.current = nil

	// DIRECTION 1 — the device locks IN the window. The decision is the writer's and it is recorded; the
	// level it names is unreachable.
	policy.SetScreenOn(false)

	require.True(t, policy.paused,
		"the policy must record the pause it decided on: the mirror is written where the level is "+
			"DECIDED, not where it is applied")
	require.False(t, first.devicePaused,
		"the old level must not move, because the writer reaches the CURRENT Box and there is none")
	require.EqualValues(t, 1, policy.Stats().Pauses,
		"the writer must have decided exactly one pause, or the rest of this test measures nothing")

	// ---- the window closes: a replacement Box appears, optimistic ----
	replacement := &reloadableLevel{}
	target.current = replacement

	// THE DIVERGENCE, as the two values themselves: the mirror asserts a device state the level the
	// platform will drive does not have.
	require.True(t, policy.paused)
	require.False(t, replacement.devicePaused)
	require.NotEqual(t, policy.paused, replacement.devicePaused,
		"the mirror and the real level disagree across the reload window")
	require.EqualValues(t, 0, replacement.pauses,
		"the pause must never have reached the replacement level: that is the lost fact")

	// ---- DIRECTION 2, and the lasting consequence ----
	//
	// The screen comes back. The wake is published - and it is delivered to a level that was never
	// paused, which is the spurious edge.
	policy.SetScreenOn(true)
	require.EqualValues(t, 1, policy.Stats().Wakes,
		"the writer publishes a wake for the pause it never delivered")
	require.EqualValues(t, 1, replacement.wakes,
		"the wake is delivered to the replacement level, which was never paused")
	require.EqualValues(t, 1, policy.Stats().Pauses,
		"no second pause may appear; the cycle is one swallowed pause and one spurious wake")

	// The pause is gone for the whole of that screen-off and nothing re-enters it: the writer's own
	// record says the pause was already published.
	require.EqualValues(t, 0, replacement.pauses,
		"for the entire duration of that lock the core ran unpaused, which is the harm")
	require.False(t, replacement.devicePaused)
}

// TestPlatformMirrorLosesALongLockEntirely is the same mechanism with the timing that makes the harm
// concrete, and it pins the property that makes repetition useless as a repair.
//
// A phone that locks and stays locked is the case the device axis exists for. Here the reload happens
// while it is already locked, and the writer's mirror is the only thing that knows.
func TestPlatformMirrorLosesALongLockEntirely(t *testing.T) {
	first := &reloadableLevel{}
	target := &reloadableTarget{current: first}
	policy := NewPlatformEvents(target)
	t.Cleanup(policy.Close)

	policy.SetScreenOn(true)
	policy.SetAppForeground(true)

	target.current = nil

	// The lock lands in the window, so the level that the reload is about to create never learns it.
	policy.SetScreenOn(false)
	replacement := &reloadableLevel{}
	target.current = replacement

	// The lock holds. Every later statement of the same fact is a repeat the mirror absorbs, so there is
	// no path back to a paused level for this device state.
	require.True(t, policy.paused)
	for range 10 {
		policy.SetScreenOn(false)
	}
	require.EqualValues(t, 1, policy.Stats().Pauses,
		"the repeated screen-off facts must be coalesced against the mirror")
	require.False(t, replacement.devicePaused,
		"the level stays lost for the whole lock, and no repetition recovers it")
	require.EqualValues(t, 0, replacement.pauses)
}

// TestPlatformReconciliationCanMoveTheLevelBack is the positive half, and the mechanism the Apple-path
// fix relies on.
//
// It states what the repository needs from a reconciliation: the level is a LEVEL, so it may be moved in
// either direction without publishing an edge, and the writer's own record must then follow the manager.
// This is the contract `restoreDeviceLevel` (experimental/libbox/command_server.go:353) implements for
// the Apple writer, and it is written here against the production policy so the mirror half of that fix
// is pinned independently of the reload machinery.
func TestPlatformReconciliationCanMoveTheLevelBack(t *testing.T) {
	level := &reloadableLevel{}
	target := &reloadableTarget{current: level}
	policy := NewPlatformEvents(target)
	t.Cleanup(policy.Close)

	policy.SetScreenOn(true)
	policy.SetAppForeground(true)
	policy.SetScreenOn(false)
	require.True(t, level.devicePaused)
	require.Equal(t, policy.paused, level.devicePaused,
		"with a reachable level the mirror and the level agree")

	// A reload: the level is replaced while the policy's mirror keeps the level it published.
	replacement := &reloadableLevel{}
	target.current = replacement
	require.True(t, policy.paused)
	require.False(t, replacement.devicePaused,
		"the replacement level starts optimistic; this is the divergence a reconciliation closes")

	// A reconciliation restored from the mirror: the same direction, applied to a level that does not
	// have it. This is what `restoreDeviceLevel` calls on the manager.
	mirror := policy.paused
	if mirror != replacement.devicePaused {
		replacement.DevicePause()
	}
	require.True(t, replacement.devicePaused,
		"a reconciliation must be able to re-enter the level the mirror asserts")
	require.EqualValues(t, 0, policy.Stats().Wakes,
		"re-entering a level is not a wake and must publish no edge")
	require.Equal(t, policy.paused, replacement.devicePaused,
		"after reconciliation the two agree again, which is the property the fix exists for")

	// And the release direction, so the same mechanism cannot leave a latched pause.
	policy.SetScreenOn(true)
	require.False(t, replacement.devicePaused)
	require.EqualValues(t, 1, replacement.wakes,
		"the release direction must reach the replacement level")
	require.Equal(t, policy.paused, replacement.devicePaused)
}

// TestPlatformDirectWriterDoesNotTouchTheMirror is the cross-package half of the asymmetry the libbox
// tests report, stated where the mirror's own field is readable.
//
// `rememberDeviceLevel` (experimental/libbox/command_server.go:295) is called from exactly four entry
// points, all of them CommandServer methods. The DIRECT writer reaches `instance.PauseManager()`
// through `libbox.boxPlatformTarget` and never calls it, so `deviceAxisTouched` stays false and
// `restoreDeviceLevel` returns at its `if !touched` guard (:359-361). On that writer the window is
// still open, and this test states the mirror-side consequence: the policy's record and the running
// level disagree with nothing in the policy able to reconcile them.
func TestPlatformDirectWriterDoesNotTouchTheMirror(t *testing.T) {
	level := &reloadableLevel{}
	target := &reloadableTarget{current: level}
	policy := NewPlatformEvents(target)
	t.Cleanup(policy.Close)

	policy.SetScreenOn(true)
	policy.SetAppForeground(true)

	// The window opens, the lock lands in it, the window closes.
	target.current = nil
	policy.SetScreenOn(false)
	replacement := &reloadableLevel{}
	target.current = replacement

	// The policy has no reconciliation entry point of its own: nothing in this package compares `paused`
	// against the target, and no target call is made when a level appears. So the disagreement persists
	// until some EXTERNAL writer moves the level - which is exactly what `restoreDeviceLevel` is, and
	// why it has to be reached for by the writer that owns the mirror.
	require.True(t, policy.paused)
	require.False(t, replacement.devicePaused)
	require.EqualValues(t, 0, replacement.pauses)

	// A repeated fact is absorbed by the mirror rather than reconciled.
	for range 5 {
		policy.SetScreenOn(false)
	}
	require.EqualValues(t, 0, replacement.pauses,
		"the direct writer's repetitions never reach the level, so the policy alone cannot repair it")
	require.False(t, replacement.devicePaused)
}
