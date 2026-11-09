package urltest

// P02B — the in-flight share's contract.

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func healthKey() MeasurementKey {
	return MeasurementKey{Instance: "box", Node: "a", Target: "https://p/204", Purpose: PurposeHealth}
}

func TestEquivalentHealthProbesShareOneMeasurement(t *testing.T) {
	share := NewMeasurementShare()
	leaderWait, isLeader := share.Join(context.Background(), healthKey())
	require.True(t, isLeader)
	require.NotNil(t, leaderWait)
	require.Equal(t, 1, share.InFlight())
	require.Equal(t, 1, share.Participants())

	followerWait, isLeader := share.Join(context.Background(), healthKey())
	require.False(t, isLeader, "an equivalent health probe must wait instead of measuring again")
	require.Same(t, leaderWait, followerWait, "equivalent callers share one wait")
	require.Equal(t, 2, share.Participants())

	share.Publish(leaderWait, Measurement{}, nil)
	select {
	case <-followerWait.Done():
	default:
		t.Fatal("publishing the outcome closes the wait every equivalent caller holds")
	}
	require.Zero(t, share.InFlight(), "a finished measurement leaves the registry")
}

func TestDisplayAndDiagnosticProbesAreNeverShared(t *testing.T) {
	share := NewMeasurementShare()
	for _, purpose := range []MeasurementPurpose{PurposeDisplay, PurposeDiagnostic} {
		key := healthKey()
		key.Purpose = purpose
		wait, isLeader := share.Join(context.Background(), key)
		require.True(t, isLeader)
		select {
		case <-wait.Done():
		default:
			t.Fatal("a non-health probe always measures for itself, and its Done must not block")
		}
		require.Nil(t, wait.Context(), "a question that is not shared has no probe lifetime to run on")
	}
	require.Zero(t, share.InFlight())
	require.Zero(t, share.Participants())
}

func TestACancelledWaiterLeavesWithoutTouchingTheSharedEntry(t *testing.T) {
	share := NewMeasurementShare()
	leaderWait, isLeader := share.Join(context.Background(), healthKey())
	require.True(t, isLeader)
	followerWait, isLeader := share.Join(context.Background(), healthKey())
	require.False(t, isLeader)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := share.Await(ctx, followerWait)
	require.ErrorIs(t, err, context.Canceled, "the waiter observes its own cancellation")
	require.Equal(t, 1, share.InFlight(), "leaving does not disturb the leader's entry")
	require.Equal(t, 1, share.Participants(), "the leader is still a participant")

	share.Publish(leaderWait, Measurement{}, nil)
	select {
	case <-leaderWait.Done():
	default:
		t.Fatal("the leader's question still completes")
	}
}

func TestPublishIsIdempotent(t *testing.T) {
	share := NewMeasurementShare()
	wait, isLeader := share.Join(context.Background(), healthKey())
	require.True(t, isLeader)
	share.Publish(wait, Measurement{}, nil)
	share.Publish(wait, Measurement{}, nil) // must not panic on a closed channel
	require.Zero(t, share.InFlight())

	// A new measurement after the publication gets a fresh entry.
	_, isLeader = share.Join(context.Background(), healthKey())
	require.True(t, isLeader)
}

func TestADifferentQuestionIsNotShared(t *testing.T) {
	share := NewMeasurementShare()
	share.Join(context.Background(), healthKey())
	other := healthKey()
	other.Node = "b"
	_, isLeader := share.Join(context.Background(), other)
	require.True(t, isLeader)
	require.Equal(t, 2, share.InFlight())
}

func TestTheRegistryTravelsOnTheBoxContext(t *testing.T) {
	share := NewMeasurementShare()
	ctx := ContextWithMeasurementShare(context.Background(), share)
	require.Same(t, share, MeasurementShareFromContext(ctx))
	require.Nil(t, MeasurementShareFromContext(context.Background()), "no Box means no sharing, not a package-level registry")
}

func TestAWaitersOutcomeIsTheLeadersAnswer(t *testing.T) {
	share := NewMeasurementShare()
	leaderWait, isLeader := share.Join(context.Background(), healthKey())
	require.True(t, isLeader)
	followerWait, isLeader := share.Join(context.Background(), healthKey())
	require.False(t, isLeader)
	require.Same(t, leaderWait, followerWait, "equivalent callers share one wait")

	probeErr := errors.New("probe failed")
	share.Publish(leaderWait, Measurement{}, probeErr)
	select {
	case <-followerWait.Done():
	default:
		t.Fatal("the leader's publication closes the wait its waiters hold")
	}
	_, err := followerWait.Outcome()
	require.ErrorIs(t, err, probeErr, "the waiter reads the leader's answer, not its own")
}
