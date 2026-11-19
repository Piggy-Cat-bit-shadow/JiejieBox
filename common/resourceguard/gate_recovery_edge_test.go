package resourceguard

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestAPlanRaiseHandsRecoveredCapacityToTheQueue is the recovery edge of the ResourceGuard x
// OOMKiller pairing, pinned deterministically.
//
// # The scenario, in the terms the two subsystems use
//
// OOMKiller demotes (pressure falls from Critical to Normal) and the guard publishes the wider plan
// through setPlan. Any caller that arrived during the crisis is still parked in the gate's queue.
// The queue is the only place capacity can be lost silently: a waiter is granted a slot by
// grantLocked from release or withdraw, and a gate that has just been widened has no release to run
// — the crisis is over precisely because nothing is in flight.
//
// So without the grant on the plan change, the limit would say 4, in-flight would be 1, three
// callers would be queued, and those three would still time out on the budget they captured at
// Critical. The failure is quiet: the counters look healthy (refused 0, inflight 1, waiters 3) and
// only the caller's own timeout reveals it.
//
// # Why this is not a sleep-based test
//
// The holder is never released; the waiters are admitted either by the plan change or not at all.
// The only timer is the fail-safe deadline, which is far longer than the assertion window, so a
// passing run does not depend on scheduling luck.
func TestAPlanRaiseHandsRecoveredCapacityToTheQueue(t *testing.T) {
	gate := NewGate(plan{limit: 1, queue: 4, wait: 5 * time.Second}, 0)
	holder, err := gate.Acquire(context.Background(), ClassNormal)
	require.NoError(t, err)
	require.NotNil(t, holder)

	admitted := make(chan *Permit, 3)
	for index := 0; index < 3; index++ {
		go func() {
			permit, acquireErr := gate.Acquire(context.Background(), ClassNormal)
			if acquireErr == nil {
				admitted <- permit
			}
		}()
	}
	require.Eventually(t, func() bool {
		return gate.Stats().Waiters == 3
	}, 2*time.Second, time.Millisecond, "the three callers never reached the queue")
	require.Equal(t, 1, gate.Stats().Inflight)

	// The demote: Critical's plan replaced by the normal one while the queue is still full.
	gate.setPlan(plan{limit: 4, queue: 4, wait: 5 * time.Second})

	for index := 0; index < 3; index++ {
		select {
		case permit := <-admitted:
			require.NotNil(t, permit)
		case <-time.After(2 * time.Second):
			stats := gate.Stats()
			t.Fatalf("a queued caller was not admitted after the plan was raised: limit=%d "+
				"inflight=%d waiters=%d. The recovered capacity is unreachable until some permit "+
				"is released, so the recovery edge the guard exists for never reaches the callers "+
				"already waiting", stats.Limit, stats.Inflight, stats.Waiters)
		}
	}
	stats := gate.Stats()
	require.Equal(t, 4, stats.Inflight, "the raised limit must be fully used, not just visible")
	require.Equal(t, 0, stats.Waiters)
	require.EqualValues(t, 0, stats.TimedOut)
	require.EqualValues(t, 0, stats.Canceled)
	require.EqualValues(t, 4, stats.Admitted)
	holder.Release()
}

// TestAPlanShrinkDoesNotDisturbTheQueueOrTheInFlightWork is the other direction of the same edge:
// the grant that makes recovery work must not turn a tighten into a revocation.
//
// A shrink has to leave both parties alone. Work in flight keeps its permit (revoking it would break
// the ownership rule a permit represents), and callers already queued keep waiting under the budget
// they captured — they are not refused, because the demand arrived before the plan changed.
func TestAPlanShrinkDoesNotDisturbTheQueueOrTheInFlightWork(t *testing.T) {
	gate := NewGate(plan{limit: 4, queue: 4, wait: 5 * time.Second}, 0)
	permits := make([]*Permit, 0, 4)
	for index := 0; index < 4; index++ {
		permit, err := gate.Acquire(context.Background(), ClassNormal)
		require.NoError(t, err)
		permits = append(permits, permit)
	}
	queued := make(chan *Permit, 1)
	go func() {
		permit, err := gate.Acquire(context.Background(), ClassNormal)
		if err == nil {
			queued <- permit
		}
	}()
	require.Eventually(t, func() bool {
		return gate.Stats().Waiters == 1
	}, 2*time.Second, time.Millisecond)

	gate.setPlan(plan{limit: 1, queue: 4, wait: 5 * time.Second})

	stats := gate.Stats()
	require.Equal(t, 4, stats.Inflight, "a shrink must not revoke a permit that is already held")
	require.Equal(t, 1, stats.Waiters, "a shrink must not drop a caller already queued")
	require.EqualValues(t, 0, stats.Refused)
	select {
	case <-queued:
		t.Fatal("a shrink admitted a caller the tighter plan does not have room for")
	case <-time.After(100 * time.Millisecond):
	}
	for _, permit := range permits {
		permit.Release()
	}
}
