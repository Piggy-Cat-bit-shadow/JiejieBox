package urltest

// P02B — the in-flight share's contract.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func healthKey() MeasurementKey {
	return MeasurementKey{Instance: "box", Node: "a", Target: "https://p/204", Purpose: PurposeHealth}
}

func TestEquivalentHealthProbesShareOneMeasurement(t *testing.T) {
	share := NewMeasurementShare()
	leaderDone, isLeader := share.Join(healthKey())
	require.True(t, isLeader)
	require.NotNil(t, leaderDone)
	require.Equal(t, 1, share.InFlight())

	followerDone, isLeader := share.Join(healthKey())
	require.False(t, isLeader, "an equivalent health probe must wait instead of measuring again")
	require.Equal(t, leaderDone, followerDone)

	share.Finish(healthKey())
	require.True(t, share.Wait(context.Background(), followerDone), "finishing publishes the answer to the waiter")
	require.Zero(t, share.InFlight(), "a finished measurement leaves the registry")
}

func TestDisplayAndDiagnosticProbesAreNeverShared(t *testing.T) {
	share := NewMeasurementShare()
	for _, purpose := range []MeasurementPurpose{PurposeDisplay, PurposeDiagnostic} {
		key := healthKey()
		key.Purpose = purpose
		done, isLeader := share.Join(key)
		require.True(t, isLeader)
		require.Nil(t, done, "a non-health probe always measures for itself")
	}
	require.Zero(t, share.InFlight())
}

func TestACancelledWaiterLeavesWithoutTouchingTheSharedEntry(t *testing.T) {
	share := NewMeasurementShare()
	leaderDone, isLeader := share.Join(healthKey())
	require.True(t, isLeader)
	followerDone, isLeader := share.Join(healthKey())
	require.False(t, isLeader)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.False(t, share.Wait(ctx, followerDone), "the waiter observes its own cancellation")
	require.Equal(t, 1, share.InFlight(), "leaving does not disturb the leader's entry")

	share.Finish(healthKey())
	require.True(t, share.Wait(context.Background(), leaderDone))
}

func TestFinishIsIdempotent(t *testing.T) {
	share := NewMeasurementShare()
	share.Join(healthKey())
	share.Finish(healthKey())
	share.Finish(healthKey()) // must not panic on a closed channel
	require.Zero(t, share.InFlight())

	// A new measurement after the finish gets a fresh entry.
	_, isLeader := share.Join(healthKey())
	require.True(t, isLeader)
}

func TestADifferentQuestionIsNotShared(t *testing.T) {
	share := NewMeasurementShare()
	share.Join(healthKey())
	other := healthKey()
	other.Node = "b"
	_, isLeader := share.Join(other)
	require.True(t, isLeader)
	require.Equal(t, 2, share.InFlight())
}
