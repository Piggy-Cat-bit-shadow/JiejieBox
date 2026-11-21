package urltest

// P13 attack: leader/follower cancellation at different stages of one shared health probe.
//
// The existing lifetime tests cover one leader and one follower. The attack here is the shape a
// crowded round produces: several callers join one probe, the leader leaves, some followers leave
// before the answer, and the remainder must still receive the SAME answer. Then the abandoned
// question is left behind and a later caller must start a FRESH probe rather than join work whose
// participants are gone.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestManyFollowersShareOneProbeAndOnlyTheLastOwnerStopsIt drives one probe with a leader and six
// followers, cancels the leader and half the followers, and lets the answer arrive for the rest.
func TestManyFollowersShareOneProbeAndOnlyTheLastOwnerStopsIt(t *testing.T) {
	target := newCountingTarget(t, false)
	link := target.server.URL + "/generate_204"
	outbound := &passthroughOutbound{tag: "node-crowded"}
	dialer := newHeldDialer(outbound)

	share := NewMeasurementShare()
	boxCtx := ContextWithMeasurementShare(context.Background(), share)
	key := sharedKey()
	key.Node = "node-crowded"
	key.Target = link
	options := MeasureOptions{Link: link, Key: &key}

	leaderCtx, cancelLeader := context.WithCancel(boxCtx)
	leader := measureAsync(leaderCtx, options, dialer)
	select {
	case <-dialer.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the leader never dialled")
	}

	const followers = 6
	results := make([]<-chan awaiting, followers)
	cancels := make([]context.CancelFunc, followers)
	for index := 0; index < followers; index++ {
		followerCtx, cancel := context.WithCancel(boxCtx)
		cancels[index] = cancel
		results[index] = measureAsync(followerCtx, options, dialer)
	}
	require.Eventually(t, func() bool { return share.Participants() == followers+1 },
		5*time.Second, time.Millisecond, "every caller must have joined before any is cancelled")

	// The leader leaves; half the followers leave. Three waiters remain, so the probe must live.
	cancelLeader()
	select {
	case result := <-leader:
		require.ErrorIs(t, result.err, context.Canceled)
	case <-time.After(10 * time.Second):
		t.Fatal("the cancelled leader did not return")
	}
	for index := 0; index < followers; index += 2 {
		cancels[index]()
	}
	for index := 0; index < followers; index += 2 {
		select {
		case result := <-results[index]:
			require.ErrorIs(t, result.err, context.Canceled)
		case <-time.After(10 * time.Second):
			t.Fatal("a cancelled follower did not return")
		}
	}
	require.False(t, dialer.aborted.Load(),
		"a probe with remaining participants must not observe a cancellation")

	dialer.openGate()
	var sharedDelay uint16
	for index := 1; index < followers; index += 2 {
		select {
		case result := <-results[index]:
			require.NoError(t, result.err, "a remaining follower must receive the answer")
			require.NotZero(t, result.measurement.Delay)
			if sharedDelay == 0 {
				sharedDelay = result.measurement.Delay
			}
			require.Equal(t, sharedDelay, result.measurement.Delay, "all followers get the SAME probe's answer")
		case <-time.After(10 * time.Second):
			t.Fatal("a remaining follower never received the shared answer")
		}
	}
	require.EqualValues(t, 1, dialer.calls.Load(), "seven equivalent callers, one dial")
	require.Zero(t, share.Participants())
	require.Zero(t, share.InFlight())
}

// TestAnAbandonedQuestionIsNotJoinedByALaterCaller: when every participant has left, the question is
// forgotten and its probe context cancelled. A later caller must start a new probe (a second dial),
// and the abandoned probe's result must not be handed to it.
func TestAnAbandonedQuestionIsNotJoinedByALaterCaller(t *testing.T) {
	target := newCountingTarget(t, false)
	link := target.server.URL + "/generate_204"
	outbound := &passthroughOutbound{tag: "node-abandoned-twice"}
	dialer := newHeldDialer(outbound)

	share := NewMeasurementShare()
	boxCtx := ContextWithMeasurementShare(context.Background(), share)
	key := sharedKey()
	key.Node = "node-abandoned-twice"
	key.Target = link
	options := MeasureOptions{Link: link, Key: &key}

	leaderCtx, cancelLeader := context.WithCancel(boxCtx)
	leader := measureAsync(leaderCtx, options, dialer)
	select {
	case <-dialer.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the first probe never dialled")
	}
	followerCtx, cancelFollower := context.WithCancel(boxCtx)
	follower := measureAsync(followerCtx, options, dialer)
	require.Eventually(t, func() bool { return share.Participants() == 2 }, 5*time.Second, time.Millisecond)

	cancelLeader()
	<-leader
	cancelFollower()
	<-follower
	require.Zero(t, share.InFlight(), "the abandoned question leaves the registry at once")
	require.Eventually(t, dialer.aborted.Load, 5*time.Second, time.Millisecond,
		"the abandoned probe's own context is cancelled")

	// The second dial is a NEW question. Opening the gate lets it complete.
	dialer.openGate()
	dialer.calls.Store(0)
	measurement, err := Measure(boxCtx, options, dialer)
	require.NoError(t, err, "a caller that arrives after the abandonment must be measured")
	require.NotZero(t, measurement.Delay)
	require.EqualValues(t, 1, dialer.calls.Load(), "exactly one fresh dial for the new question")
	require.Zero(t, share.InFlight())
}

// TestCancellationBeforeTheProbeStartsLeavesNothingBehind covers the stage the measurement engine
// cannot reach deterministically: the registry has a leader, but no probe goroutine was started (a
// caller that disappears between Join and the goroutine). The registry must be empty afterwards and
// a later publish must be inert rather than a close of an already-closed channel.
func TestCancellationBeforeTheProbeStartsLeavesNothingBehind(t *testing.T) {
	share := NewMeasurementShare()
	key := healthKey()

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	wait, isLeader := share.Join(cancelled, key)
	require.True(t, isLeader)
	require.Equal(t, 1, share.Participants())

	_, err := share.Await(cancelled, wait)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, share.Participants())
	require.Zero(t, share.InFlight(), "no probe was started, so nothing may linger")

	// A publish for the abandoned wait must be inert, and a later caller must lead a new question.
	share.Publish(wait, Measurement{Delay: 1}, nil)
	current, isLeader := share.Join(context.Background(), key)
	require.True(t, isLeader)
	require.NotSame(t, wait, current)
	select {
	case <-current.Done():
		t.Fatal("a late publish must not answer a question it does not own")
	default:
	}
	share.Publish(current, Measurement{Delay: 2}, nil)
	measurement, err := current.Outcome()
	require.NoError(t, err)
	require.EqualValues(t, 2, measurement.Delay)
}
