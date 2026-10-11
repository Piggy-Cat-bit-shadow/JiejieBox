package box

import (
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/power"

	"github.com/stretchr/testify/require"
)

// These tests pin P1-C: one physical device-sleep episode produces one sleep anchor and exactly one
// reuse boundary, however many times the NetworkExtension is suspended and resumed inside it.
//
// # The defect, and the two attempts at it
//
// The first attempt routed a current-state read (`.snapshot`) to a level reconcile instead of a sleep
// edge. That is correct semantics and it is kept - see `TestAReReadReconcilesTheLevelAndNeverAnchors`
// - but the device showed it is not the path that fires: a real iPhone locked for 28m24s produced 26
// `ne-sleep`/`ne-wake` callbacks and ZERO publishable re-reads, because a re-read only publishes when
// the value changed and the value did not change.
//
// The second attempt gated the anchor move on `IsDevicePaused()`. It was reverted, and the reason is
// worth keeping: `noteSleepLocked` is not only an anchor writer, it is also the only thing that
// clears `resumeNoted`, which is the per-sleep boundary latch. Removing the anchor move removed the
// re-arm, and a real iPhone then published **zero** boundaries for a ten-minute sleep - strictly
// worse than the bug, because nothing was retired at all. See
// P1C_IPHONE_DEVICE_CLOSURE_REPORT.md.
//
// # What the fix actually is
//
// `lifecycle.resumed()` no longer calls `governor.Resumed()`. `woke()` and `applyPauseEvent` already
// publish at the level release, so the bridge was publishing a boundary in the MIDDLE of an episode,
// and `Resumed()` sets `resumeNoted`, which is also what re-arms the anchor. One premature resume
// therefore both consumed the boundary and moved the anchor for every sleep report that followed.
//
// With it gone the governor needs no new state: during an episode `pausedSince` is set and
// `resumeNoted` is false, so `noteSleepLocked` returns early and the anchor holds; at the unlock the
// one boundary is published and sets `resumeNoted`, which re-arms the anchor for the next episode.

// episodeBridge is `newAppleBridge` under the shipping policy, with the deep-idle deadline pushed out
// of the way so a test about the episode is not also racing the idle timer.
func episodeBridge(t *testing.T) (*power.Governor, lifecycle, *testClock, *[]power.ReuseBoundary) {
	t.Helper()
	clock := newTestClock()
	governor, bridge, _, _ := newAppleBridge(t, bridgePolicy(), clock)
	boundaries := &[]power.ReuseBoundary{}
	governor.AddReuseObserver(func(boundary power.ReuseBoundary) {
		*boundaries = append(*boundaries, boundary)
	})
	return governor, bridge, clock, boundaries
}

// TestManySuspendResumeCyclesInOneSleepIsOneBoundary is the device trace as a test, and the
// regression P1-C is closed on.
//
// The shape is the real one, measured on an iPhone 17 Pro Max (iOS 26.4.2) locked once for 28m24s:
// the extension was suspended and resumed 13 times inside that single locked period. The pre-fix
// core wrote 13 anchors and published 12 boundaries for it, and the boundary the unlock published -
// the only one a flow arriving after the unlock ever sees - reported 28.29 seconds for a 28-minute
// sleep.
func TestManySuspendResumeCyclesInOneSleepIsOneBoundary(t *testing.T) {
	governor, bridge, clock, boundaries := episodeBridge(t)

	// The screen goes off and the device locks: one transition, therefore one episode.
	bridge.screenState(false)
	startedAt := clock.Now()

	// Thirteen suspend/resume cycles inside the same episode. `ExtensionProvider.sleep()` maps to
	// `slept()` and `wake()` wakes the core and then re-reads both names, so this loop is the pair
	// the device produced, 26 callbacks over.
	for i := 0; i < 13; i++ {
		clock.Advance(time.Duration(45+i*20) * time.Second)
		bridge.slept() // commandServer.pause() -> Box.DeviceSlept()
		clock.Advance(time.Duration(41+i*11) * time.Second)
		bridge.resumed() // commandServer.wake() -> Box.DeviceResumed()
		bridge.reconcileScreenState(false)
		bridge.reconcileLockState(true)
	}

	require.Empty(t, *boundaries, "a suspend/resume cycle inside one episode published a boundary")

	// The user finally unlocks, 28 minutes after the screen went off.
	clock.Advance(20 * time.Second)
	bridge.lockState(false)
	elapsed := clock.Now().Sub(startedAt)

	require.Len(t, *boundaries, 1, "one sleep episode published more than one boundary")
	require.Equal(t, elapsed, (*boundaries)[0].Sleep,
		"the boundary did not span the episode: an intermediate resume consumed it")
	require.Greater(t, (*boundaries)[0].Sleep, time.Minute,
		"the boundary measured the last gap instead of the sleep")
	require.Equal(t, power.ReuseRetire, (*boundaries)[0].Action)
	require.Equal(t, uint64(1), governor.ReuseEpoch(),
		"one sleep episode advanced the reuse epoch more than once")
}

// TestATransientGlanceDoesNotConsumeTheBoundary is the case that made the second attempt fail, kept
// as a test so it cannot come back.
//
// A push lights the lock screen one second into the sleep and it goes dark again two seconds later.
// That glance is not the end of the episode, so it must not publish - and in particular it must not
// publish as `ReuseKeep`, which would leave the governor believing the episode's one boundary had
// already been spent. That is exactly what happened when the anchor was gated instead: the glance
// consumed the boundary and the ten-minute sleep that followed retired nothing.
func TestATransientGlanceDoesNotConsumeTheBoundary(t *testing.T) {
	governor, bridge, clock, boundaries := episodeBridge(t)

	bridge.screenState(false)
	startedAt := clock.Now()

	clock.Advance(time.Second)
	bridge.screenState(true) // the notification lights the lock screen
	clock.Advance(2 * time.Second)
	bridge.screenState(false) // and it goes dark again, nobody having unlocked

	// Ten more minutes locked, with the extension cycling.
	for i := 0; i < 6; i++ {
		clock.Advance(90 * time.Second)
		bridge.slept()
		clock.Advance(10 * time.Second)
		bridge.resumed()
	}

	require.Empty(t, *boundaries, "the glance consumed the episode's boundary")

	clock.Advance(time.Second)
	bridge.lockState(false)

	require.Len(t, *boundaries, 1, "the episode did not publish exactly one boundary")
	require.Equal(t, clock.Now().Sub(startedAt), (*boundaries)[0].Sleep)
	require.Equal(t, power.ReuseRetire, (*boundaries)[0].Action,
		"the glance turned a ten-minute sleep into a keep")
	require.Equal(t, uint64(1), governor.ReuseEpoch())
}

// TestOneSleepAndOneResumeIsOneBoundary is the simplest shape: a resume proves time passed, and the
// episode it belongs to owes one boundary when it ends.
func TestOneSleepAndOneResumeIsOneBoundary(t *testing.T) {
	governor, bridge, clock, boundaries := episodeBridge(t)

	bridge.slept()
	clock.Advance(30 * time.Second)
	bridge.resumed()
	require.Empty(t, *boundaries, "a resume in the middle of an episode published a boundary")

	clock.Advance(90 * time.Second)
	bridge.lockState(false) // the unlock ends the episode

	require.Len(t, *boundaries, 1)
	require.Equal(t, 2*time.Minute, (*boundaries)[0].Sleep,
		"the boundary did not span from the sleep to the unlock")
	require.Equal(t, power.ReuseRetire, (*boundaries)[0].Action)
	require.Equal(t, uint64(1), governor.ReuseEpoch())
}

// TestASecondSleepGetsItsOwnAnchorAndBoundary is the property a fix must not trade away.
//
// The re-arm is `resumeNoted`, which the boundary published at the unlock sets. A second sleep
// therefore finds the flag set, `noteSleepLocked` does not return early, and the anchor moves - so
// the second episode is measured from its own start and not from the first one's.
func TestASecondSleepGetsItsOwnAnchorAndBoundary(t *testing.T) {
	governor, bridge, clock, boundaries := episodeBridge(t)

	// First episode.
	bridge.screenState(false)
	clock.Advance(30 * time.Minute)
	bridge.lockState(false)
	require.Len(t, *boundaries, 1)
	require.Equal(t, 30*time.Minute, (*boundaries)[0].Sleep)

	// The device is used for a minute, then sleeps again.
	clock.Advance(time.Minute)
	bridge.screenState(false)
	clock.Advance(45 * time.Minute)
	bridge.lockState(false)

	require.Len(t, *boundaries, 2, "the second sleep was swallowed")
	require.Equal(t, 45*time.Minute, (*boundaries)[1].Sleep,
		"the second sleep was measured against the first episode's anchor")
	require.Equal(t, power.ReuseRetire, (*boundaries)[1].Action)
	require.Equal(t, uint64(2), governor.ReuseEpoch())
}

// TestAReReadReconcilesTheLevelAndNeverAnchors is the first half of P1-C, which stays.
//
// A current-state read is a LEVEL statement, not a transition. It may establish that the device is
// not in use and it may never move a measurement already running. The device showed this path does
// not fire in practice - the value a re-read returns is normally unchanged, so rule 3 collapses it -
// but the semantics are still the correct ones for the case where it does fire.
func TestAReReadReconcilesTheLevelAndNeverAnchors(t *testing.T) {
	governor, bridge, clock, boundaries := episodeBridge(t)

	bridge.screenState(false)
	startedAt := clock.Now()

	// A long episode with re-reads scattered through it, each finding the phone still asleep.
	for i := 0; i < 20; i++ {
		clock.Advance(time.Minute)
		bridge.reconcileScreenState(false)
		bridge.reconcileLockState(true)
	}

	require.Empty(t, *boundaries, "a re-read published a boundary of its own")

	clock.Advance(30 * time.Second)
	bridge.lockState(false)

	require.Len(t, *boundaries, 1)
	require.Equal(t, clock.Now().Sub(startedAt), (*boundaries)[0].Sleep,
		"a re-read moved the anchor")
	require.Equal(t, uint64(1), governor.ReuseEpoch())
}

// TestAReReadOnAFreshBridgeStillEstablishesThePause is T5 at the core level: the re-read is not
// merely suppressed.
//
// The case it covers is the one the re-read exists for - the extension restarted while the phone was
// locked, so the process has no pause on record. `DevicePause()` on a level that is not held moves
// the level and, through `applyPauseEvent`, publishes the edge and the level together, so the pause
// and its measurement are established exactly as before.
func TestAReReadOnAFreshBridgeStillEstablishesThePause(t *testing.T) {
	governor, bridge, clock, boundaries := episodeBridge(t)

	// No transition was ever reported: this process has just started and the phone is already locked.
	bridge.reconcileScreenState(false)
	bridge.reconcileLockState(true)

	require.Equal(t, power.StateQuiescent, governor.State(),
		"a re-read on a process with no pause on record did not establish the pause")

	clock.Advance(30 * time.Minute)
	bridge.lockState(false)

	require.Len(t, *boundaries, 1, "the pause a re-read established was not being measured")
	require.Equal(t, 30*time.Minute, (*boundaries)[0].Sleep)
	require.Equal(t, power.ReuseRetire, (*boundaries)[0].Action)
}

// TestAReReadNeverReleasesTheLevel pins the other direction.
//
// A current-state read that finds the device in use must not publish anything: "unlocked" and
// "display on" are claims about a transition nobody witnessed, and `notify_get_state` answers
// `NOTIFY_STATUS_OK` with `0` for a name whose state was never set - which on the lock source would
// read as an unlock at every extension start.
func TestAReReadNeverReleasesTheLevel(t *testing.T) {
	governor, bridge, _, _ := episodeBridge(t)

	bridge.screenState(false)
	require.Equal(t, power.StateQuiescent, governor.State())

	bridge.reconcileScreenState(true) // a display-on: not a fact this path may publish
	bridge.reconcileLockState(false)  // an unlock: not a fact this path may publish

	require.Equal(t, power.StateQuiescent, governor.State(),
		"a current-state read released the device pause")
}

// TestOneHundredEpisodesAreEachMeasuredOnce is the churn test.
//
// A boundary that fired twice per episode, or an anchor that never re-armed, is invisible in one
// cycle and obvious in a hundred: the first shows up as an epoch that outruns the episode count, the
// second as every episode after the first being measured against the first one's anchor.
func TestOneHundredEpisodesAreEachMeasuredOnce(t *testing.T) {
	governor, bridge, clock, boundaries := episodeBridge(t)

	for i := 0; i < 100; i++ {
		bridge.slept()
		// A suspend/resume cycle inside the episode, which must not add anything.
		clock.Advance(10 * time.Second)
		bridge.resumed()
		// The rest of the episode, then the unlock that ends it.
		clock.Advance(50 * time.Second)
		bridge.lockState(false)
		clock.Advance(time.Minute) // the device is used between episodes
	}

	require.Len(t, *boundaries, 100, "the episode count and the boundary count disagree")
	for i, boundary := range *boundaries {
		require.Equal(t, time.Minute, boundary.Sleep,
			"episode %d was measured against the wrong anchor", i)
		require.Equal(t, uint64(i+1), boundary.Epoch)
		require.Equal(t, power.ReuseRetire, boundary.Action)
	}
	require.Equal(t, uint64(100), governor.ReuseEpoch())
}
