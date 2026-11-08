package urltest

// P02B — the wiring in Measure: a leader measures and publishes, an equivalent follower
// reads the leader's answer, and a caller without a key never shares.

import (
	"context"
	"testing"

	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

func sharedKey() MeasurementKey {
	return MeasurementKey{Instance: "box", Node: "a", Target: "https://p/204", Purpose: PurposeHealth}
}

func TestMeasureSharesOneAnswerForEquivalentHealthKeys(t *testing.T) {
	share := NewMeasurementShare()
	ctx := ContextWithMeasurementShare(context.Background(), share)
	key := sharedKey()
	options := MeasureOptions{Link: "://not a url", Key: &key}

	// The leader fails on the unusable link and publishes the failure; the follower must get
	// the SAME answer instead of performing a second measurement.
	leaderMeasurement, leaderErr := Measure(ctx, options, N.SystemDialer)
	require.Error(t, leaderErr)
	_ = leaderMeasurement

	followerMeasurement, followerErr := Measure(ctx, options, N.SystemDialer)
	require.Error(t, followerErr)
	require.Equal(t, leaderErr.Error(), followerErr.Error(), "a follower reads the leader's answer")
	_ = followerMeasurement
	require.Zero(t, share.InFlight(), "the shared entry is removed when the leader publishes")
}

func TestMeasureWithoutAKeyNeverShares(t *testing.T) {
	share := NewMeasurementShare()
	ctx := ContextWithMeasurementShare(context.Background(), share)
	options := MeasureOptions{Link: "://not a url"}
	_, err := Measure(ctx, options, N.SystemDialer)
	require.Error(t, err)
	require.Zero(t, share.InFlight(), "a measurement without a key is not registered at all")
}

func TestACancelledFollowerReturnsItsOwnCancellation(t *testing.T) {
	share := NewMeasurementShare()
	key := sharedKey()
	// Register the leader's in-flight entry directly, then cancel the follower's context.
	_, isLeader := share.JoinQuestion(key)
	require.True(t, isLeader)
	ctx, cancel := context.WithCancel(ContextWithMeasurementShare(context.Background(), share))
	cancel()
	_, err := Measure(ctx, MeasureOptions{Link: "://not a url", Key: &key}, N.SystemDialer)
	require.ErrorIs(t, err, context.Canceled)
}
