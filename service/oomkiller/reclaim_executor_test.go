package oomkiller

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// waitForTimerState polls a timer field until it becomes true. It is used only for assertions about
// an asynchronous worker reaching a state; every actual decision in these tests is made
// deterministically through a channel.
func waitForTimerState(t *testing.T, timer *adaptiveTimer, description string, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if predicate() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", description)
		}
		time.Sleep(time.Millisecond)
	}
}

func (t *adaptiveTimer) flags() (idle, pressure, crisis bool, pending reclaimKind) {
	t.access.Lock()
	defer t.access.Unlock()
	kind := reclaimNone
	if t.pendingReclaim != nil {
		kind = t.pendingReclaim.kind
	}
	return t.idleInFlight, t.pressureInFlight, t.crisisInFlight, kind
}

func (t *adaptiveTimer) appliedObservation() uint64 {
	t.access.Lock()
	defer t.access.Unlock()
	return t.lastAppliedObservation
}

func (t *adaptiveTimer) currentState() pressureState {
	t.access.Lock()
	defer t.access.Unlock()
	return t.state
}

// TestStopDropsAPendingCrisisInsteadOfResettingDuringClose is the "queued, not started" half of the
// lifecycle contract.
//
// An idle trim is inside a pool. A crisis poll observes the trigger and queues the reset. Then Close
// runs. The reset has not touched anything yet, so starting it now would reset a network for a
// service that is being torn down, and the next instance would inherit the damage. stop() must drop
// it and wait only for the work that already started.
func TestStopDropsAPendingCrisisInsteadOfResettingDuringClose(t *testing.T) {
	pressure := &atomic.Uint32{}
	usage := &atomic.Uint64{}
	usage.Store(33 * 1024 * 1024)
	timer := newPollStopRaceTimer(pressure, 33)
	timer.sampleFunc = func() memorySample { return memorySample{usage: usage.Load()} }
	timer.lastUsage = 35 * 1024 * 1024
	timer.runtimeReclaimable = func() uint64 { return 0 }

	trimEntered := make(chan struct{})
	trimRelease := make(chan struct{})
	timer.trimMemory = func(context.Context) {
		close(trimEntered)
		<-trimRelease
	}
	var releases atomic.Int32
	timer.releaseNetwork = func(context.Context) { releases.Add(1) }
	var frees atomic.Int32
	timer.freeOSMemory = func() { frees.Add(1) }

	idleDone := make(chan struct{})
	go func() {
		defer close(idleDone)
		timer.poll()
	}()
	select {
	case <-trimEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("the idle release never started")
	}

	usage.Store(46 * 1024 * 1024)
	crisisSubmitted := make(chan struct{})
	go func() {
		defer close(crisisSubmitted)
		timer.poll()
	}()
	waitForTimerState(t, timer, "the crisis request to be queued", func() bool {
		_, _, crisis, pending := timer.flags()
		return crisis && pending == reclaimCrisis
	})
	<-crisisSubmitted

	stopDone := make(chan struct{})
	go func() {
		defer close(stopDone)
		timer.stop()
	}()

	// Join on stop having actually closed the lifecycle, not on a sleep: once the stopped flag is
	// set, the worker cannot take the pending crisis even after the trim releases.
	waitForTimerState(t, timer, "stop to close the lifecycle", func() bool {
		timer.access.Lock()
		defer timer.access.Unlock()
		return timer.reclaimStopped
	})
	close(trimRelease)

	select {
	case <-idleDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the idle poll did not finish")
	}
	select {
	case <-stopDone:
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not return after the in-flight trim finished")
	}

	require.Zero(t, releases.Load(),
		"stop started a queued crisis reset for a service that was already closing")
	require.Zero(t, frees.Load(), "the idle pass below armed had no runtime evidence to collect")
	_, _, crisis, pending := timer.flags()
	require.False(t, crisis, "the dropped crisis must clear its flag")
	require.Equal(t, reclaimNone, pending)
}

// TestQueuedCrisisIsRecheckedBeforeResetting: a crisis request can wait behind an in-flight idle
// pass. If that pass released the memory the crisis was called for, a network reset now would
// terminate healthy paths for a footprint that is already back below armed. The action re-checks the
// live sample and skips the destructive step.
func TestQueuedCrisisIsRecheckedBeforeResetting(t *testing.T) {
	pressure := &atomic.Uint32{}
	usage := &atomic.Uint64{}
	usage.Store(33 * 1024 * 1024)
	timer := newPollStopRaceTimer(pressure, 33)
	timer.sampleFunc = func() memorySample { return memorySample{usage: usage.Load()} }
	timer.lastUsage = 35 * 1024 * 1024
	timer.freeOSMemory = func() {}
	timer.runtimeReclaimable = func() uint64 { return 0 }

	trimEntered := make(chan struct{})
	trimRelease := make(chan struct{})
	timer.trimMemory = func(context.Context) {
		close(trimEntered)
		<-trimRelease
	}
	var releases atomic.Int32
	timer.releaseNetwork = func(context.Context) { releases.Add(1) }

	t.Cleanup(func() {
		if timer.timer != nil {
			timer.timer.Stop()
		}
	})

	idleDone := make(chan struct{})
	go func() {
		defer close(idleDone)
		timer.poll()
	}()
	select {
	case <-trimEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("the idle pass never reached the pool layer")
	}

	usage.Store(46 * 1024 * 1024)
	crisisDone := make(chan struct{})
	go func() {
		defer close(crisisDone)
		timer.poll()
	}()
	waitForTimerState(t, timer, "the crisis request to be queued", func() bool {
		_, _, crisis, _ := timer.flags()
		return crisis
	})

	// The release finished while the crisis was queued: the footprint is no longer in the armed
	// band, so the destructive action must re-check and stand down.
	usage.Store(35 * 1024 * 1024)
	close(trimRelease)
	<-idleDone
	<-crisisDone
	waitForTimerState(t, timer, "the crisis action to finish", func() bool {
		_, _, crisis, _ := timer.flags()
		return !crisis
	})

	require.Zero(t, releases.Load(),
		"a queued crisis reset a network whose footprint had already fallen below the armed line")
}

// TestRepeatedTriggeredPollsResetOnce: a second observation while the first reset is in flight must
// not queue a second one. The state machine is already Triggered; the reset in hand covers it.
func TestRepeatedTriggeredPollsResetOnce(t *testing.T) {
	pressure := &atomic.Uint32{}
	timer := newPollStopRaceTimer(pressure, 46)
	timer.freeOSMemory = func() {}

	resetEntered := make(chan struct{})
	resetRelease := make(chan struct{})
	var once sync.Once
	var releaseCalls atomic.Int32
	timer.releaseNetwork = func(context.Context) {
		releaseCalls.Add(1)
		once.Do(func() { close(resetEntered) })
		<-resetRelease
	}

	firstPoll := make(chan struct{})
	go func() {
		defer close(firstPoll)
		timer.poll()
	}()
	select {
	case <-resetEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("the first reset never ran")
	}
	<-firstPoll

	secondDone := make(chan struct{})
	go func() {
		defer close(secondDone)
		timer.poll()
	}()
	<-secondDone
	require.Equal(t, int32(1), releaseCalls.Load(),
		"a second poll queued a second reset while the first was still in flight")

	close(resetRelease)
	waitForTimerState(t, timer, "the crisis action to finish", func() bool {
		_, _, crisis, _ := timer.flags()
		return !crisis
	})
	require.Equal(t, int32(1), releaseCalls.Load())
}

// TestRateTriggerDoesNotResetAgainWhileTriggered: the growth prediction is an EDGE into Triggered,
// not a level. A process that stays in the prediction window would otherwise reset once per poll.
func TestRateTriggerDoesNotResetAgainWhileTriggered(t *testing.T) {
	pressure := &atomic.Uint32{}
	timer := newPollStopRaceTimer(pressure, 44)
	timer.pressureBaseline = memorySample{usage: 40 * 1024 * 1024}
	timer.pressureBaselineTime = time.Now().Add(-600 * time.Millisecond)
	timer.freeOSMemory = func() {}

	resetEntered := make(chan struct{})
	resetRelease := make(chan struct{})
	var once sync.Once
	var releaseCalls atomic.Int32
	timer.releaseNetwork = func(context.Context) {
		releaseCalls.Add(1)
		once.Do(func() { close(resetEntered) })
		<-resetRelease
	}

	firstPoll := make(chan struct{})
	go func() {
		defer close(firstPoll)
		timer.poll()
	}()
	select {
	case <-resetEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("the rate prediction never escalated")
	}
	require.Equal(t, pressureStateTriggered, timer.currentState())
	<-firstPoll

	secondDone := make(chan struct{})
	go func() {
		defer close(secondDone)
		timer.poll()
	}()
	<-secondDone
	require.Equal(t, int32(1), releaseCalls.Load())

	close(resetRelease)
	waitForTimerState(t, timer, "the rate crisis action to finish", func() bool {
		_, _, crisis, _ := timer.flags()
		return !crisis
	})
	require.Equal(t, int32(1), releaseCalls.Load(),
		"the rate prediction fired a second reset while already Triggered")
}

// TestStaleObservationCannotMoveTheStateBackwards: poll reads its sample before taking the state
// lock, so a delayed goroutine can finish reading first and apply last. The observation sequence
// makes the newer sample win even when the older one reaches the lock later.
func TestStaleObservationCannotMoveTheStateBackwards(t *testing.T) {
	pressure := &atomic.Uint32{}
	timer := newPollStopRaceTimer(pressure, 41)

	var calls atomic.Int32
	firstRead := make(chan struct{})
	releaseFirst := make(chan struct{})
	timer.sampleFunc = func() memorySample {
		if calls.Add(1) == 1 {
			close(firstRead)
			<-releaseFirst
			return idleSample(46)
		}
		return idleSample(41)
	}

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		timer.poll()
	}()
	select {
	case <-firstRead:
	case <-time.After(5 * time.Second):
		t.Fatal("the first poll never sampled")
	}

	secondDone := make(chan struct{})
	go func() {
		defer close(secondDone)
		timer.poll()
	}()
	waitForTimerState(t, timer, "the newer sample to be applied", func() bool {
		return timer.currentState() == pressureStateArmed
	})

	close(releaseFirst)
	select {
	case <-firstDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the older poll did not finish")
	}
	<-secondDone

	require.Equal(t, pressureStateArmed, timer.currentState(),
		"an older observation applied after a newer one moved the state machine backwards")
	require.True(t, timer.appliedObservation() >= 2)
}

// TestCrisisCoolsDownTheFollowingIdleDrop: after a reset the footprint really does fall, and the
// very next sample sees a large drop. Without the post-crisis cooldown that drop is a fresh idle
// opportunity, so one crisis event produces a second Trim + GC immediately after its own.
func TestCrisisCoolsDownTheFollowingIdleDrop(t *testing.T) {
	pressure := &atomic.Uint32{}
	usage := &atomic.Uint64{}
	usage.Store(46 * 1024 * 1024)
	timer := newPollStopRaceTimer(pressure, 46)
	timer.sampleFunc = func() memorySample { return memorySample{usage: usage.Load()} }
	var releases atomic.Int32
	timer.releaseNetwork = func(context.Context) { releases.Add(1) }
	var frees atomic.Int32
	timer.freeOSMemory = func() { frees.Add(1) }
	var trims atomic.Int32
	timer.trimMemory = func(context.Context) { trims.Add(1) }

	timer.poll()
	waitForTimerState(t, timer, "the crisis action to finish", func() bool {
		_, _, crisis, _ := timer.flags()
		return !crisis
	})
	require.Equal(t, int32(1), releases.Load())
	require.Equal(t, int32(1), frees.Load())

	// The post-reset sample: a 13 MiB drop since the crisis observation.
	usage.Store(33 * 1024 * 1024)
	timer.access.Lock()
	timer.lastUsage = 46 * 1024 * 1024
	timer.access.Unlock()
	timer.releaseIdleMemory(idleSample(33), 10)

	require.Zero(t, trims.Load(), "the idle pass ran inside the post-crisis cooldown")
	require.Equal(t, int32(1), frees.Load(), "the crisis GC was followed by a second idle GC")
}

// TestPressureCallbacksCoalesceWhileOneIsRunning: the dispatch source can deliver repeated critical
// notifications. A second one while the first flush/GC is running must not queue another; the same
// cache and the same runtime are about to be released.
func TestPressureCallbacksCoalesceWhileOneIsRunning(t *testing.T) {
	pressure := &atomic.Uint32{}
	timer := newPollStopRaceTimer(pressure, 33)
	var cleanupCalls atomic.Int32
	cleanupEntered := make(chan struct{})
	cleanupRelease := make(chan struct{})
	var once sync.Once
	timer.cleanupHook = func() {
		cleanupCalls.Add(1)
		once.Do(func() { close(cleanupEntered) })
		<-cleanupRelease
	}
	timer.freeOSMemory = func() {}

	timer.notifyPressure()
	select {
	case <-cleanupEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("the first pressure cleanup never ran")
	}
	timer.notifyPressure()
	require.Equal(t, int32(1), cleanupCalls.Load(),
		"a second pressure notification queued a second flush while the first was running")

	close(cleanupRelease)
	waitForTimerState(t, timer, "the pressure action to finish", func() bool {
		_, pressureInFlight, _, _ := timer.flags()
		return !pressureInFlight
	})
	timer.stop()
}

// TestReloadDoesNotCarryAPendingPressureIntent: the state a reload inherits must not contain
// "the next poll should do X" flags set by a callback whose queued action stop() just dropped. A
// new instance measuring growth from the old delivery, or treating it as still pending, is exactly
// the cross-instance pollution the lifecycle contract forbids.
func TestReloadDoesNotCarryAPendingPressureIntent(t *testing.T) {
	pressure := &atomic.Uint32{}
	timer := newPollStopRaceTimer(pressure, 33)
	timer.minInterval = time.Hour
	timer.maxInterval = time.Hour
	timer.freeOSMemory = func() {}
	timer.start(nil)

	timer.notifyPressure()
	state := timer.stop()

	require.False(t, state.forceMinInterval, "a dropped pressure delivery forced the new instance to poll fast")
	require.False(t, state.pendingPressureBaseline, "a dropped pressure delivery became the new instance's baseline")
	require.True(t, state.pressureBaselineTime.IsZero())
}

// TestStopIsIdempotentAndReleasesTheExecutor: Close can be called twice and a reload creates a new
// timer. Neither may leave a worker goroutine behind, or a long-lived client accumulates one per
// reload.
func TestStopIsIdempotentAndReleasesTheExecutor(t *testing.T) {
	baseline := runtime.NumGoroutine()
	for iteration := 0; iteration < 10; iteration++ {
		timer := newPollStopRaceTimer(&atomic.Uint32{}, 33)
		timer.freeOSMemory = func() {}
		timer.start(nil)
		timer.notifyPressure()
		timer.stop()
		timer.stop()
	}
	require.Eventually(t, func() bool {
		return runtime.NumGoroutine() <= baseline+2
	}, 5*time.Second, 10*time.Millisecond,
		"the reclaim executor outlived its timer")
}
