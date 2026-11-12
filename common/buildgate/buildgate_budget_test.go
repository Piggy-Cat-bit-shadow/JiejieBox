package buildgate

// Q20: the budget must not present the admission limit as an overlap bound, and the build lifecycle
// must be countable and must return its slot.
//
// The first test is the audit's counterexample, reproduced 3/3 against the previous cut: two builds
// are admitted under LevelNormal (limit 2), pressure rises to LevelCritical (admission limit 1) and
// the two permits are still held - the budget then reported maxOverlappingBuilds=1 while
// stats.Inflight was 2. A permit is never revoked mid-build, so the overlap bound has to be
// max(admission limit, builds already running).

import (
	"context"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/resourceguard"

	"github.com/stretchr/testify/require"
)

// TestTheBuildBudgetCannotUnderstateAlreadyAdmittedBuilds is the audit's red case.
func TestTheBuildBudgetCannotUnderstateAlreadyAdmittedBuilds(t *testing.T) {
	t.Parallel()
	gate, source := newPressureGate(t, 2, 0, 20*time.Millisecond)
	releaseA, err := gate.Acquire(context.Background())
	require.NoError(t, err)
	defer releaseA()
	releaseB, err := gate.Acquire(context.Background())
	require.NoError(t, err)
	defer releaseB()
	source.set(resourceguard.LevelCritical)
	require.Equal(t, resourceguard.LevelCritical, gate.Level())
	budget := gate.Budget()
	stats := gate.Stats()
	require.Equal(t, 1, budget.Limit, "critical pressure admits one build")
	require.Equal(t, 2, stats.Inflight, "the two permits handed out under normal pressure are not revoked")
	require.Equal(t, 2, budget.Inflight, "the budget reports the builds that are actually running")
	require.GreaterOrEqual(t, budget.MaxOverlappingBuilds, stats.Inflight,
		"the budget presented the new admission limit as an overlap bound while old builds still hold permits")
	require.Equal(t, 2, budget.MaxOverlappingBuilds)
	require.Equal(t, 2, budget.MaxOldDocuments, "an already-running build may still hold the version it replaces")
	require.GreaterOrEqual(t, budget.PeakOverlapping, stats.Inflight)
}

// TestEveryBuildOutcomeReturnsItsSlot pins the lifecycle invariant: every admitted permit is either
// still in flight or has ended in exactly one of the three terminal counters. That is what makes
// "a failed build returns its slot" checkable, and it is also what makes a leaked permit visible as
// a gap rather than as an unexplained in-flight count.
func TestEveryBuildOutcomeReturnsItsSlot(t *testing.T) {
	t.Parallel()
	gate, _ := newPressureGate(t, 2, 0, 20*time.Millisecond)
	ctx := context.Background()

	success, err := gate.AcquireBuild(ctx, BuildOptions{HasPreviousVersion: true})
	require.NoError(t, err)
	success.Succeed()

	failure, err := gate.AcquireBuild(ctx, BuildOptions{HasPreviousVersion: false})
	require.NoError(t, err)
	failure.Fail()
	// A second Fail must not hand the slot back twice or count the build twice.
	failure.Fail()

	abandoned, err := gate.AcquireBuild(ctx, BuildOptions{})
	require.NoError(t, err)
	abandoned.Release()
	abandoned.Release()

	running, err := gate.AcquireBuild(ctx, BuildOptions{HasPreviousVersion: true})
	require.NoError(t, err)

	stats := gate.Stats()
	require.EqualValues(t, 4, stats.AdmittedBuilds)
	require.EqualValues(t, 1, stats.Succeeded)
	require.EqualValues(t, 1, stats.Failed)
	require.EqualValues(t, 1, stats.Abandoned)
	require.Equal(t, 1, stats.Inflight)
	require.Equal(t, 1, stats.Gate.Inflight)
	require.EqualValues(t, 2, stats.BuildsWithPreviousVersion)
	require.EqualValues(t, 2, stats.BuildsWithoutPreviousVersion)
	require.Equal(t, stats.AdmittedBuilds, stats.Succeeded+stats.Failed+stats.Abandoned+uint64(stats.Inflight),
		"every admitted build must be in flight or classified")

	// Releasing the last one must return the reservation to zero: the failure paths above already
	// proved that a failed or abandoned build does not hold its slot.
	running.Release()
	require.Equal(t, 0, gate.Stats().Gate.Inflight)
}

// TestADeferredBuildCountsAsNeitherSuccessNorFailure keeps the pressure deferral out of the outcome
// counters: a deferral never started, so it neither succeeded nor failed.
func TestADeferredBuildCountsAsNeitherSuccessNorFailure(t *testing.T) {
	t.Parallel()
	gate, source := newPressureGate(t, 1, 0, 20*time.Millisecond)
	source.set(resourceguard.LevelCritical)
	first, err := gate.AcquireBuild(context.Background(), BuildOptions{HasPreviousVersion: true})
	require.NoError(t, err)
	defer first.Succeed()
	_, err = gate.AcquireBuild(context.Background(), BuildOptions{HasPreviousVersion: true})
	require.ErrorIs(t, err, ErrDeferred)
	stats := gate.Stats()
	require.EqualValues(t, 1, stats.Deferred)
	require.EqualValues(t, 1, stats.AdmittedBuilds)
	require.EqualValues(t, 0, stats.Succeeded)
	require.EqualValues(t, 0, stats.Failed)
	require.EqualValues(t, 0, stats.Abandoned)
}
