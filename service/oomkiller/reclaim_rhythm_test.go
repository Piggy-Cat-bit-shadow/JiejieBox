package oomkiller

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// These are the time-progression checks the idle trigger exists for. The earlier regression tests
// drive five ticks; these drive the shapes that only appear after hundreds, or after the 30-second
// cooldown has expired. "Nothing happens" is the assertion, and it is the one a future change is
// most likely to break by reintroducing a level trigger.

// A stable 33 MiB footprint has no observed opportunity, ever. A thousand ticks - far more than the
// thirty-three observed in the incident - must not run a trim or a GC.
func TestStableThirtyThreeMiBOverAThousandTicks(t *testing.T) {
	timer, trims, frees := newIdleReleaseTimer(t)
	timer.lastUsage = 33 * 1024 * 1024
	for tick := 0; tick < 1000; tick++ {
		timer.releaseIdleMemory(idleSample(33), 10)
	}
	require.Zero(t, *trims)
	require.Zero(t, *frees)
}

// A stable 41 MiB armed footprint is quiet ACROSS cooldown boundaries too. The previous one-second
// loop was fixed by removing the level trigger, not by the cooldown: forcing the cooldown and the
// release interval into the past on every tick proves that the gate is opportunity, not elapsed
// time. This is the shape a future "retry after backoff" change would regress.
func TestArmedFortyOneMiBRemainsQuietAcrossCooldownBoundaries(t *testing.T) {
	timer, trims, frees := newIdleReleaseTimer(t)
	timer.lastUsage = 41 * 1024 * 1024
	for tick := 0; tick < 100; tick++ {
		timer.access.Lock()
		timer.lastRelease = time.Now().Add(-time.Minute)
		timer.idleCooldownUntil = time.Now().Add(-time.Second)
		timer.access.Unlock()
		timer.releaseIdleMemory(idleSample(41), 10)
	}
	require.Zero(t, *trims, "a stable armed footprint is not an opportunity even after the cooldown")
	require.Zero(t, *frees)
}

// The 39 <-> 41 oscillation is a real drop on the way down, so the first descent may release. The
// low-yield cooldown must then absorb the rest of the oscillation: threshold-adjacent noise is not a
// reason to close pools or force a GC every few hundred milliseconds.
func TestThirtyNineToFortyOneOscillationCoolsDown(t *testing.T) {
	timer, trims, _ := newIdleReleaseTimer(t)
	timer.lastUsage = 41 * 1024 * 1024

	timer.releaseIdleMemory(idleSample(39), 10)
	require.Equal(t, 1, *trims, "the first real descent is an opportunity")

	for round := 0; round < 20; round++ {
		timer.releaseIdleMemory(idleSample(41), 10)
		timer.releaseIdleMemory(idleSample(39), 10)
	}
	require.Equal(t, 1, *trims,
		"the oscillation kept releasing inside the cooldown; the gate is not level-based")
}

// The cooldown must expire. A later descent with a fresh opportunity is allowed to act again; a
// backoff that never re-arms would turn "stop thrashing" into "never release again".
func TestOpportunityAfterTheCooldownIsAllowedAgain(t *testing.T) {
	timer, trims, _ := newIdleReleaseTimer(t)
	timer.lastUsage = 41 * 1024 * 1024
	timer.releaseIdleMemory(idleSample(39), 10)
	require.Equal(t, 1, *trims)

	// Simulate the pass of more than the cooldown, with no change in the footprint.
	timer.access.Lock()
	timer.lastRelease = time.Now().Add(-time.Minute)
	timer.idleCooldownUntil = time.Now().Add(-time.Second)
	timer.access.Unlock()
	timer.releaseIdleMemory(idleSample(41), 10)
	timer.releaseIdleMemory(idleSample(39), 10)
	require.Equal(t, 2, *trims)
}

// A threshold crossing is a crisis, and the ordinary idle backoff must not delay it. The cooldown is
// forced into the future and the release interval is fresh; the Triggered poll must still reset.
func TestCrisisIsNotBlockedByTheIdleCooldown(t *testing.T) {
	pressure := &atomic.Uint32{}
	timer := newPollStopRaceTimer(pressure, 46)
	timer.access.Lock()
	timer.lastRelease = time.Now()
	timer.idleCooldownUntil = time.Now().Add(time.Hour)
	timer.access.Unlock()
	timer.freeOSMemory = func() {}

	reset := make(chan struct{})
	var once sync.Once
	timer.releaseNetwork = func(context.Context) {
		once.Do(func() { close(reset) })
	}
	timer.poll()
	select {
	case <-reset:
	case <-time.After(5 * time.Second):
		t.Fatal("the idle cooldown delayed a crisis reset")
	}
}
