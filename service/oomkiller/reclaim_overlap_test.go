package oomkiller

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestCrisisReclaimWaitsForAnInFlightIdleTrim is the §2B regression.
//
// # The interleaving it forces
//
//	idle poll:   passes its opportunity check, starts TrimMemory, BLOCKS
//	crisis poll: samples above trigger, starts releaseMemory/ReleaseMemory CONCURRENTLY
//
// The two passes walk the same pools: TrimMemory closes idle transports and the crisis pass resets
// the network and flushes the cache. Running them at the same time is how a pool is closed while it
// is being reset, and it is also how one opportunity produces two forced GCs. The idle path had its
// own releaseAccess mutex, but the Triggered path never took it.
//
// The assertion is ordering, not mere completion: the crisis cleanup must not ENTER while the idle
// trim is still inside a pool. The hooks make both pauses deterministic.
func TestCrisisReclaimWaitsForAnInFlightIdleTrim(t *testing.T) {
	pressure := &atomic.Uint32{}
	usage := &atomic.Uint64{}
	usage.Store(33 * 1024 * 1024)

	timer := newPollStopRaceTimer(pressure, 33)
	timer.sampleFunc = func() memorySample { return memorySample{usage: usage.Load()} }
	timer.lastUsage = 35 * 1024 * 1024
	// Report-only keeps the crisis pass on the observable cleanup path without a network manager;
	// the serialization under test is the same one the real reset takes.
	timer.killerDisabled = true

	trimEntered := make(chan struct{})
	trimRelease := make(chan struct{})
	timer.trimMemory = func(context.Context) {
		close(trimEntered)
		<-trimRelease
	}

	crisisEntered := make(chan struct{})
	crisisRelease := make(chan struct{})
	timer.cleanupHook = func() {
		close(crisisEntered)
		<-crisisRelease
	}

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
		t.Fatal("the idle release never reached TrimMemory")
	}

	// A crisis sample arrives while the idle trim is inside the network walk.
	usage.Store(46 * 1024 * 1024)
	crisisDone := make(chan struct{})
	go func() {
		defer close(crisisDone)
		timer.poll()
	}()

	select {
	case <-crisisEntered:
		t.Fatal("the crisis cleanup entered while the idle TrimMemory was still running")
	case <-time.After(150 * time.Millisecond):
	}

	close(trimRelease)
	select {
	case <-crisisEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("the crisis cleanup never ran after the idle trim finished")
	}
	close(crisisRelease)

	select {
	case <-idleDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the idle poll did not finish")
	}
	select {
	case <-crisisDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the crisis poll did not finish")
	}
	require.Equal(t, pressureStateTriggered, timer.state)
}

// TestCrisisAbortsAnIdlePassBetweenLayers: the layer boundary is a cancellation point. A crisis
// observed while the cheap cache pass is running must not wait for the pool walk and the GC it does
// not need; the destructive pass takes the executor as soon as the current layer returns.
func TestCrisisAbortsAnIdlePassBetweenLayers(t *testing.T) {
	pressure := &atomic.Uint32{}
	usage := &atomic.Uint64{}
	usage.Store(33 * 1024 * 1024)
	timer := newPollStopRaceTimer(pressure, 33)
	timer.sampleFunc = func() memorySample { return memorySample{usage: usage.Load()} }
	timer.lastUsage = 35 * 1024 * 1024
	timer.freeOSMemory = func() {}
	timer.runtimeReclaimable = func() uint64 { return 0 }

	var poolTrims atomic.Int32
	timer.trimMemory = func(context.Context) { poolTrims.Add(1) }

	cacheEntered := make(chan struct{})
	cacheRelease := make(chan struct{})
	timer.trimCaches = func(context.Context) {
		close(cacheEntered)
		<-cacheRelease
	}
	var resets atomic.Int32
	timer.releaseNetwork = func(context.Context) { resets.Add(1) }

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
	case <-cacheEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("the idle pass never reached the cache layer")
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

	close(cacheRelease)
	select {
	case <-idleDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the idle poll did not finish")
	}
	select {
	case <-crisisDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the crisis poll did not finish")
	}
	waitForTimerState(t, timer, "the crisis reset to run", func() bool {
		return resets.Load() == 1
	})
	require.Zero(t, poolTrims.Load(),
		"the idle pass ran the pool layer after a crisis had already been queued")
	require.Equal(t, int32(1), resets.Load())
}
