package urltest

// P02B — who owns a shared measurement's lifetime, and who may end it.
//
// The share exists so that two equivalent health probes do not open two sockets. That is only
// acceptable if the SECOND caller's interest cannot be destroyed by the FIRST caller's
// cancellation: a follower that joined while the leader was still dialling must still receive the
// answer after the leader has stopped waiting for it.
//
// These tests drive the real entry point (Measure) with a dialler that holds the first dial at a
// barrier, so "the probe is in flight" is an observed fact rather than a sleep.

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// heldDialer counts dial attempts and holds the FIRST one at a barrier.
//
// It records the reason the held dial was released as well, because "the probe was cancelled when
// the last participant left" is only observable through the dial's own error.
type heldDialer struct {
	inner N.Dialer
	calls atomic.Int32

	started chan struct{}
	gate    chan struct{}
	once    sync.Once

	aborted atomic.Bool
}

func newHeldDialer(inner N.Dialer) *heldDialer {
	return &heldDialer{inner: inner, started: make(chan struct{}), gate: make(chan struct{})}
}

func (d *heldDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	d.calls.Add(1)
	d.once.Do(func() { close(d.started) })
	select {
	case <-d.gate:
	case <-ctx.Done():
		d.aborted.Store(true)
		return nil, ctx.Err()
	}
	return d.inner.DialContext(ctx, network, destination)
}

func (d *heldDialer) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return d.inner.ListenPacket(ctx, destination)
}

func (d *heldDialer) openGate() { close(d.gate) }

// awaiting is one caller's Measure call, collected off the test goroutine.
type awaiting struct {
	measurement Measurement
	err         error
}

func measureAsync(ctx context.Context, options MeasureOptions, detour N.Dialer) <-chan awaiting {
	done := make(chan awaiting, 1)
	go func() {
		measurement, err := Measure(ctx, options, detour)
		done <- awaiting{measurement: measurement, err: err}
	}()
	return done
}

func TestACancelledLeaderStillAnswersTheWaiter(t *testing.T) {
	target := newCountingTarget(t, false)
	link := target.server.URL + "/generate_204"
	outbound := &passthroughOutbound{tag: "node-shared"}
	dialer := newHeldDialer(outbound)

	share := NewMeasurementShare()
	boxCtx := ContextWithMeasurementShare(context.Background(), share)
	key := sharedKey()
	key.Node = "node-shared"
	key.Target = link
	options := MeasureOptions{Link: link, Key: &key}

	leaderCtx, cancelLeader := context.WithCancel(boxCtx)
	leader := measureAsync(leaderCtx, options, dialer)

	// The leader is inside the measurement, holding the connection at the barrier.
	select {
	case <-dialer.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the leader never dialled")
	}

	follower := measureAsync(boxCtx, options, dialer)
	require.Eventually(t, func() bool { return share.Participants() == 2 }, time.Second, time.Millisecond,
		"the follower must have joined the leader's in-flight question before the leader is cancelled")

	cancelLeader()
	select {
	case result := <-leader:
		require.ErrorIs(t, result.err, context.Canceled, "the cancelled caller is told its own cancellation")
	case <-time.After(10 * time.Second):
		t.Fatal("the cancelled caller did not return")
	}

	// The waiter is still there, so the probe must not have been abandoned.
	dialer.openGate()
	select {
	case result := <-follower:
		require.NoError(t, result.err, "a waiter still waiting must be handed the answer, not the leader's cancellation")
		require.NotZero(t, result.measurement.Delay)
	case <-time.After(10 * time.Second):
		t.Fatal("the waiter never received the shared answer")
	}

	require.EqualValues(t, 1, dialer.calls.Load(), "two equivalent callers share one dial even when the first leaves")
	require.Zero(t, share.Participants())
	require.Zero(t, share.InFlight())
}

func TestTheSharedProbeStopsWhenTheLastParticipantLeaves(t *testing.T) {
	target := newCountingTarget(t, false)
	link := target.server.URL + "/generate_204"
	outbound := &passthroughOutbound{tag: "node-abandoned"}
	dialer := newHeldDialer(outbound)

	share := NewMeasurementShare()
	boxCtx := ContextWithMeasurementShare(context.Background(), share)
	key := sharedKey()
	key.Node = "node-abandoned"
	key.Target = link
	options := MeasureOptions{Link: link, Key: &key}

	leaderCtx, cancelLeader := context.WithCancel(boxCtx)
	leader := measureAsync(leaderCtx, options, dialer)
	select {
	case <-dialer.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the leader never dialled")
	}

	followerCtx, cancelFollower := context.WithCancel(boxCtx)
	follower := measureAsync(followerCtx, options, dialer)
	require.Eventually(t, func() bool { return share.Participants() == 2 }, time.Second, time.Millisecond)

	// The leader leaving alone must NOT end the probe: the follower still wants the answer.
	cancelLeader()
	<-leader
	require.Equal(t, 1, share.Participants())
	require.False(t, dialer.aborted.Load(), "one caller leaving must not cancel a probe another caller is waiting for")

	// The follower leaving too leaves nobody: the contract is then to stop the work rather than
	// keep a socket open for a question no one is asking.
	cancelFollower()
	<-follower
	require.Zero(t, share.Participants())
	require.Zero(t, share.InFlight(), "an abandoned question leaves the registry at once")
	require.Eventually(t, dialer.aborted.Load, time.Second, time.Millisecond,
		"the probe must observe the cancellation of its own context")

	dialer.openGate() // the goroutine is already unwinding; release so it cannot linger
}

func TestDifferentQuestionsDialSeparately(t *testing.T) {
	target := newCountingTarget(t, false)
	link := target.server.URL + "/generate_204"
	dialer := &countingDialer{}

	share := NewMeasurementShare()
	boxCtx := ContextWithMeasurementShare(context.Background(), share)
	first := sharedKey()
	first.Node = "node-a"
	first.Target = link
	second := first
	second.Node = "node-b"

	_, firstErr := Measure(boxCtx, MeasureOptions{Link: link, Key: &first}, dialer)
	require.NoError(t, firstErr)
	_, secondErr := Measure(boxCtx, MeasureOptions{Link: link, Key: &second}, dialer)
	require.NoError(t, secondErr)

	require.EqualValues(t, 2, dialer.dials.Load(), "different keys are different questions, so each dials")
	require.Zero(t, share.Participants())
	require.Zero(t, share.InFlight())
}

// TestAnAbandonedQuestionIsNotClosedByItsFormerLeader pins the late-publish boundary at the
// registry: identity, not the key, decides which question a leader answers.
//
// Without it a leader that was abandoned while still dialling would publish into whatever question
// with the same key a later caller had started, handing that caller a result for a probe it never
// saw - and deleting the entry the new leader still owns.
func TestAnAbandonedQuestionIsNotClosedByItsFormerLeader(t *testing.T) {
	share := NewMeasurementShare()
	key := healthKey()

	abandoned, isLeader := share.Join(context.Background(), key)
	require.True(t, isLeader)
	share.leave(abandoned)
	require.Zero(t, share.InFlight(), "nobody waits, so the question is forgotten")

	current, isLeader := share.Join(context.Background(), key)
	require.True(t, isLeader)
	require.NotSame(t, abandoned, current, "a later caller starts a new question, it does not inherit the old one")

	share.Publish(abandoned, Measurement{Delay: 1}, nil)
	select {
	case <-current.Done():
		t.Fatal("a former leader must not answer a question it no longer owns")
	default:
	}

	share.Publish(current, Measurement{Delay: 2}, nil)
	select {
	case <-current.Done():
	default:
		t.Fatal("the current leader must be able to publish")
	}
	measurement, err := current.Outcome()
	require.NoError(t, err)
	require.EqualValues(t, 2, measurement.Delay)
	require.Zero(t, share.InFlight())
}
