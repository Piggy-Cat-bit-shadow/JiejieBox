package box

import (
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/power"

	"github.com/stretchr/testify/require"
)

// These tests pin P1-C: a CURRENT-STATE read of the Apple device axis may reconcile the level and may
// never start a new reuse measurement.
//
// # The defect they exist for
//
// The Apple client re-reads `com.apple.iokit.hid.displayStatus` and `com.apple.springboard.lockstate`
// in two places that are not transitions - once at observer start, and once on every
// NetworkExtension resume - and both reads arrive through the same bridge as a real transition does.
// The core maps a sleep fact to `lifecycle.slept()`, which publishes an EDGE
// (`governor.SleepStarted()`) and then the LEVEL, so a re-read was delivered as "a new sleep began".
//
// The resume is what makes that destructive rather than merely redundant, and the ordering is not an
// accident: `ExtensionProvider.wake()` wakes the core first and re-reads second.
//
//	commandServer.wake()  -> Box.DeviceResumed() -> lifecycle.resumed() -> governor.Resumed()
//	                          ... which publishes the boundary for the sleep that just ended AND
//	                          sets `resumeNoted`, the flag `noteSleepLocked` uses to decide whether a
//	                          sleep report is a NEW sleep.
//	resync()              -> a re-read -> (before this fix) SleepStarted() -> noteSleepLocked sees
//	                          `resumeNoted == true`, so the report is honoured as a fresh sleep and
//	                          `pausedSince` is moved to now.
//
// The measured five-hour sleep is then re-anchored, and the unlock that ends it publishes the seconds
// between the resume and the re-read - a `ReuseSuspect` boundary for a phone that was asleep all
// night, instead of the `ReuseRetire` the real duration earns. The pool that should have been retired
// before the first flow after the unlock is kept instead, and that flow is handed a socket the sleep
// killed.
//
// # Why the assertions are on the boundary and on the level
//
// The boundary carries the measured duration and the verdict, which is the user-visible consequence;
// the level is the object every subsystem reads. Asserting on either alone would miss half of it: a
// fix that stopped the re-anchor by suppressing the read entirely would keep the boundary right and
// leave the "the extension restarted while the phone was locked" case unhandled.

// reconcileTestBridge is `newAppleBridge` under the shipping policy, with the deep-idle deadline
// pushed out of the way so a test about the anchor is not also racing the idle timer.
func reconcileTestBridge(t *testing.T) (*power.Governor, lifecycle, *testClock, *[]power.ReuseBoundary) {
	t.Helper()
	clock := newTestClock()
	governor, bridge, _, _ := newAppleBridge(t, bridgePolicy(), clock)
	boundaries := &[]power.ReuseBoundary{}
	governor.AddReuseObserver(func(boundary power.ReuseBoundary) {
		*boundaries = append(*boundaries, boundary)
	})
	return governor, bridge, clock, boundaries
}

// TestP1CAResumeReReadMustNotReanchorTheSleep is the regression test, stated as the sequence the
// device produces.
//
// Sleep starts at T0 through the transition path, five hours pass with the extension suspended, the
// extension resumes (which publishes the boundary for those five hours), the client re-reads both
// names and finds the phone still locked with the screen still off, and twelve seconds later the user
// unlocks.
//
// What must be true: the five hours are measured once, the verdict is the retire the policy gives
// that duration, and the unlock does not publish a second boundary for the same sleep. What must NOT
// happen is the re-read moving the anchor, which is what the twin below demonstrates.
func TestP1CAResumeReReadMustNotReanchorTheSleep(t *testing.T) {
	governor, bridge, clock, boundaries := reconcileTestBridge(t)

	// T0: the device goes to sleep. A transition the platform witnessed, so this is the edge path.
	bridge.screenState(false)
	clock.Advance(5 * time.Hour)

	// The extension resumes: `commandServer.wake()` -> `DeviceResumed()` -> `resumed()`.
	bridge.resumed()
	require.Len(t, *boundaries, 1, "the resume did not publish the boundary for the sleep it ended")
	require.Equal(t, 5*time.Hour, (*boundaries)[0].Sleep)
	require.Equal(t, power.ReuseRetire, (*boundaries)[0].Action)

	// The client re-reads both names two seconds later and finds the phone still asleep: display off,
	// locked. This is the P1-C path.
	clock.Advance(2 * time.Second)
	bridge.reconcileScreenState(false)
	bridge.reconcileLockState(true)

	// Ten seconds after that, the user actually unlocks.
	clock.Advance(10 * time.Second)
	bridge.lockState(false)

	require.Len(t, *boundaries, 1,
		"a current-state read started a second measurement, so the unlock published a second boundary")
	require.Equal(t, 5*time.Hour, (*boundaries)[0].Sleep,
		"the anchor moved: the measured sleep is no longer the five hours the device was asleep")
	require.Equal(t, power.ReuseRetire, (*boundaries)[0].Action)

	// The verdict is the real duration's, so the pool that predates the sleep is retired rather than
	// kept. This is the consequence the defect had.
	require.Equal(t, uint64(1), governor.ReuseEpoch())
}

// TestP1CTheEdgeFormIsWhatReanchors is the twin: the SAME sequence, with the re-read delivered
// through the edge entry point the bridge used to use for it.
//
// It is here for two reasons. It is the reverse-break - deleting the fix must make the test above
// fail, and this is what it fails into - and it is the standing proof that the assertions above are
// about the delivery and not about the clock. It is deliberately NOT asserting that an edge is always
// wrong: `screenState(false)` at T0 is exactly right.
func TestP1CTheEdgeFormIsWhatReanchors(t *testing.T) {
	governor, bridge, clock, boundaries := reconcileTestBridge(t)

	bridge.screenState(false)
	clock.Advance(5 * time.Hour)
	bridge.resumed()
	require.Len(t, *boundaries, 1)
	require.Equal(t, 5*time.Hour, (*boundaries)[0].Sleep)

	// The defect: the re-read takes the EDGE path, which is `slept()` -> `SleepStarted()`.
	clock.Advance(2 * time.Second)
	bridge.screenState(false)
	bridge.lockState(true)

	clock.Advance(10 * time.Second)
	bridge.lockState(false)

	require.Len(t, *boundaries, 2, "the edge form no longer re-anchors; this test is the reverse-break")
	require.Equal(t, 10*time.Second, (*boundaries)[1].Sleep,
		"the second boundary no longer measures from the re-anchor")
	require.Equal(t, power.ReuseSuspect, (*boundaries)[1].Action,
		"a five-hour sleep was reported as a ten-second suspect pause")
	require.Equal(t, uint64(2), governor.ReuseEpoch(),
		"the defect advanced the epoch a second time for one sleep")
}

// TestAReconcileOnAFreshBridgeStillEstablishesThePause is T5 at the core level: the re-read is not
// merely suppressed.
//
// The case it covers is the one the re-read exists for - the extension restarted while the phone was
// locked, so the process has no pause on record. `DevicePause()` on a level that is not held moves
// the level and, through `applyPauseEvent`, publishes the edge and the level together, so the pause
// and its measurement are established exactly as before. What a reconcile removes is the ability to
// do that again to a sleep that is already being measured.
func TestAReconcileOnAFreshBridgeStillEstablishesThePause(t *testing.T) {
	governor, bridge, clock, boundaries := reconcileTestBridge(t)

	// No transition was ever reported: this process has just started and the phone is already locked.
	bridge.reconcileScreenState(false)
	bridge.reconcileLockState(true)

	require.Equal(t, power.StateQuiescent, governor.State(),
		"a re-read on a process with no pause on record did not establish the pause")

	// And a sleep is being measured from it, so the resume that ends the sleep is a real boundary
	// rather than a no-op. That is what makes the re-read worth keeping at all.
	clock.Advance(30 * time.Minute)
	bridge.resumed()

	require.Len(t, *boundaries, 1, "the pause a re-read established was not being measured")
	require.Equal(t, 30*time.Minute, (*boundaries)[0].Sleep)
	require.Equal(t, power.ReuseRetire, (*boundaries)[0].Action)
}

// TestAReconcileNeverReleasesTheLevel pins the other direction.
//
// A current-state read that finds the device in use must not publish anything: "unlocked" and
// "display on" are claims about a transition nobody witnessed, and `notify_get_state` answers
// `NOTIFY_STATUS_OK` with `0` for a name whose state was never set - which on the lock source would
// read as an unlock at every extension start. The client's policy already refuses these; this is the
// same refusal one layer down, where it protects every client rather than one.
func TestAReconcileNeverReleasesTheLevel(t *testing.T) {
	governor, bridge, _, _ := reconcileTestBridge(t)

	bridge.screenState(false)
	require.Equal(t, power.StateQuiescent, governor.State())

	bridge.reconcileScreenState(true) // a display-on: not a fact this path may publish
	bridge.reconcileLockState(false)  // an unlock: not a fact this path may publish

	require.Equal(t, power.StateQuiescent, governor.State(),
		"a current-state read released the device pause")
}

// TestAReconcileDoesNotSwallowTheNextRealSleep is T3: the guard must not disarm the axis.
//
// After the unlock has released the level, the next display-off is a genuinely new sleep and must
// start a new measurement - otherwise the fix would trade a duplicate anchor for a missing one, and
// the pool established after the unlock would be trusted forever.
func TestAReconcileDoesNotSwallowTheNextRealSleep(t *testing.T) {
	governor, bridge, clock, boundaries := reconcileTestBridge(t)

	// One full cycle, including the re-read that used to re-anchor.
	bridge.screenState(false)
	clock.Advance(30 * time.Minute)
	bridge.resumed()
	clock.Advance(2 * time.Second)
	bridge.reconcileScreenState(false)
	bridge.lockState(false) // the unlock releases the level
	require.Len(t, *boundaries, 1)

	// A real second sleep, reported as a transition.
	clock.Advance(time.Minute)
	bridge.screenState(false)
	clock.Advance(45 * time.Minute)
	bridge.resumed()

	require.Len(t, *boundaries, 2, "the second real sleep was swallowed")
	require.Equal(t, 45*time.Minute, (*boundaries)[1].Sleep,
		"the second sleep was measured against the wrong anchor")
	require.Equal(t, power.ReuseRetire, (*boundaries)[1].Action)
	require.Equal(t, uint64(2), governor.ReuseEpoch())
}

// TestAReconcileIsIdempotent pins the property the fix rests on: the level is a level.
//
// Any number of re-reads may arrive during one sleep - the extension is resumed for every push and
// background task - and every one of them must be a no-op rather than a new measurement. The sleep
// measured at the end is the whole five hours, not the gap since the last read.
func TestAReconcileIsIdempotent(t *testing.T) {
	governor, bridge, clock, boundaries := reconcileTestBridge(t)

	bridge.screenState(false)

	// A hundred re-reads spread across the sleep, each finding the phone still locked with the screen
	// still off. Between them the clock moves, so an anchor that moved would be visible in the
	// measurement even though the sequence of calls looks the same.
	for i := 0; i < 100; i++ {
		clock.Advance(time.Minute)
		bridge.reconcileScreenState(false)
		bridge.reconcileLockState(true)
	}

	require.Empty(t, *boundaries, "a re-read published a boundary of its own")
	require.Equal(t, power.StateQuiescent, governor.State())

	clock.Advance(5 * time.Hour)
	bridge.resumed()

	require.Len(t, *boundaries, 1, "the sleep was measured in pieces")
	// 100 one-minute re-reads, then five hours: the anchor is still the transition's instant.
	require.Equal(t, 6*time.Hour+40*time.Minute, (*boundaries)[0].Sleep,
		"the anchor moved: the sleep was measured from a re-read instead of from the transition")
	require.Equal(t, power.ReuseRetire, (*boundaries)[0].Action)
	require.Equal(t, uint64(1), governor.ReuseEpoch())
}
