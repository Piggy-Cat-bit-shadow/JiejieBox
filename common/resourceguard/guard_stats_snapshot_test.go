package resourceguard

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A snapshot must not pair one transition's level with another transition's plan.
//
// # The defect this test was written against
//
// Stats() observes the state machine, applies the plan, and then reads the gate's counters. The two
// steps are separate on purpose (the plan write is under the policy lock, the shed hooks are not),
// so a second observer can complete a transition while the first is inside its own apply. The first
// caller then reports ITS level from the observation and the NEWER plan from the gate: a report that
// says "critical, limit 32" - the exact contradiction the package's own comment says the shared
// snapshot exists to prevent, and the shape a red-team report would be written from.
//
// # Why the interleaving is deterministic and uses no sleep
//
// The pause point is real production behaviour, not a test hook: apply() runs the shed hooks after
// publishing the plan and outside the policy lock. A hook that blocks therefore holds the first
// caller inside its own transition for as long as the test wants, which is exactly the window a
// descheduled goroutine would occupy. The second observer then demotes with a fake clock and
// publishes Normal, and the first caller resumes to read the gate.
func TestAttackStatsSnapshotCannotPairAStaleLevelWithANewerPlan(t *testing.T) {
	clock := newFakeClock()
	pressure := LevelCritical
	guard := NewGuard(DefaultNetworkExtensionConfig(), func() Level { return pressure })
	guard.useClock(clock.Now)

	shedEntered := make(chan struct{})
	releaseShed := make(chan struct{})
	guard.RegisterShed(func() {
		close(shedEntered)
		<-releaseShed
	})

	snapshot := make(chan GuardStats, 1)
	go func() {
		snapshot <- guard.Stats()
	}()

	select {
	case <-shedEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("the first reader never reached the shed hook, so the interleaving was not set up")
	}

	// While the first reader is inside its own Critical transition's shed, the pressure falls and a
	// second observer publishes the demote.
	pressure = LevelNormal
	guard.Level()
	clock.Advance(DefaultNetworkExtensionConfig().DemoteAfter)
	require.Equal(t, LevelNormal, guard.Level())

	close(releaseShed)
	stats := <-snapshot

	expectedLimit := DefaultNetworkExtensionConfig().planFor(stats.Level).limit
	require.Equal(t, expectedLimit, stats.Gate.Limit,
		"the snapshot reported level %s with the limit of a different transition (limit=%d, want %d)",
		stats.Level, stats.Gate.Limit, expectedLimit)
}
