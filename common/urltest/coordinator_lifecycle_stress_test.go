package urltest

// V02B — pressure × creation × close/cancel × release on the coordinator queue.
//
// Owner chain: the Box-wide pressure gate (Coordinator) and the waiters queued
// behind it. Nothing here creates a slot out of band, so the chain is exactly the
// one Measure uses: acquire (pressure), queue, grant, release.
//
// Each round fills the gate, queues an interactive and a background waiter, then
// cancels the waiters and releases the holders together, and requires the
// accounting to close:
//
//	every waiter exits with either a working slot or its own cancellation,
//	inFlight is 0 once the round is done (no slot leak),
//	queued is 0 (no queue entry leak),
//	and the coordinator still grants a fresh acquisition afterwards (progress
//	after the pressure is released).
//
// # The boundary this replay does NOT claim
//
// acquire has one branch for a waiter that is granted at the same moment its own
// context ends (coordinator.go, "The slot was granted at the same moment the
// context ended"). That branch is only reachable when the scheduler closes ready
// and ctx.Done() before the waiter's select runs; the replay below cannot force
// that. Measured with a deliberate mutation of that branch (its release removed):
// 400 rounds did not hit it, so the branch stays an UNTESTED combination rather
// than something this file proves. Forcing it needs a hook between close(ready)
// and the waiter's select, which the production type does not expose.

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// acquireAndRelease acquires a slot, returns its release if one was granted, and
// reports the outcome.
func acquireAndRelease(ctx context.Context, coordinator *Coordinator) error {
	release, err := coordinator.Acquire(ctx)
	if err != nil {
		return err
	}
	release()
	return nil
}

// TestCoordinatorSurvivesCancelAtTheGrantBoundary replays the full gate/cancel/
// release cycle and requires the accounting to close every time.
func TestCoordinatorSurvivesCancelAtTheGrantBoundary(t *testing.T) {
	const (
		limit  = 2
		rounds = 200
	)
	baseline := runtime.NumGoroutine()
	for round := 0; round < rounds; round++ {
		coordinator := NewCoordinator(limit)

		// Pressure: every slot is held.
		holders := make([]func(), 0, limit)
		for slot := 0; slot < limit; slot++ {
			release, err := coordinator.Acquire(context.Background())
			require.NoError(t, err, "round %d slot %d", round, slot)
			holders = append(holders, release)
		}

		// Creation: two waiters behind the gate, one interactive and one background,
		// each with its own cancellation.
		waiters := make([]context.CancelFunc, 0, 2)
		results := make(chan error, 2)
		for index := 0; index < 2; index++ {
			waiterCtx, cancel := context.WithCancel(context.Background())
			waiters = append(waiters, cancel)
			acquire := coordinator.Acquire
			if index == 0 {
				acquire = coordinator.AcquirePriority
			}
			go func() {
				release, err := acquire(waiterCtx)
				if err == nil {
					release()
				}
				results <- err
			}()
		}
		require.Eventually(t, func() bool { return coordinator.Queued() == 2 }, 5*time.Second, time.Millisecond,
			"round %d: the waiters never queued behind the full gate (queued=%d)", round, coordinator.Queued())
		require.LessOrEqual(t, coordinator.InFlight(), limit, "round %d: pressure exceeded the bound", round)

		// Close/cancel and release overlap: the holders free the slots while the
		// waiters cancel, which is the window where grant and cancellation meet.
		for _, release := range holders {
			release()
		}
		for _, cancel := range waiters {
			cancel()
		}
		for index := 0; index < 2; index++ {
			select {
			case err := <-results:
				if err != nil {
					require.True(t, errors.Is(err, context.Canceled),
						"round %d: a waiter failed with something other than its own cancellation: %v", round, err)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("round %d: a waiter did not exit after cancellation", round)
			}
		}

		// The round's accounting must close: every slot returned, no queue entry left.
		require.Zero(t, coordinator.InFlight(),
			"round %d: %d slot(s) leaked — a waiter that was granted at the same moment its context "+
				"ended must hand the slot back", round, coordinator.InFlight())
		require.Zero(t, coordinator.Queued(), "round %d: %d queue entry/entries leaked", round, coordinator.Queued())

		// Progress after recovery: the released pressure must grant the next
		// acquisition instead of leaving the coordinator wedged.
		release, err := coordinator.AcquirePriority(context.Background())
		require.NoError(t, err, "round %d: the coordinator stopped granting after the round", round)
		release()
		require.Zero(t, coordinator.InFlight(), "round %d: the recovery acquisition left a slot held", round)
	}
	waitForCoordinatorGoroutines(t, baseline)
}

// TestCoordinatorDoubleReleaseCannotHandTheSlotToASecondMeasurement pins the other
// half of "no double accounting": the release closure is idempotent, so a caller
// that defers it and also calls it explicitly cannot free the slot twice.
func TestCoordinatorDoubleReleaseCannotHandTheSlotToASecondMeasurement(t *testing.T) {
	coordinator := NewCoordinator(1)
	release, err := coordinator.Acquire(context.Background())
	require.NoError(t, err)

	// A second acquisition is queued behind the single slot.
	second := make(chan struct{})
	go func() {
		secondRelease, secondErr := coordinator.Acquire(context.Background())
		if secondErr == nil {
			secondRelease()
		}
		close(second)
	}()
	require.Eventually(t, func() bool { return coordinator.Queued() == 1 }, 5*time.Second, time.Millisecond)

	release()
	release() // the documented idempotent second call
	select {
	case <-second:
	case <-time.After(5 * time.Second):
		t.Fatal("the queued acquisition never ran: the double release did not corrupt the accounting")
	}
	require.Zero(t, coordinator.InFlight())
	require.Zero(t, coordinator.Queued())
}

// TestTheSlotLeakInvariantIsObservable is the negative control for the assertions
// above: a slot that is never handed back must be visible through InFlight.
// Without it, "InFlight is zero" could pass against a coordinator that never counts
// anything.
func TestTheSlotLeakInvariantIsObservable(t *testing.T) {
	coordinator := NewCoordinator(1)
	_, err := coordinator.Acquire(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, coordinator.InFlight(),
		"a held slot must be visible through InFlight; otherwise the zero assertions prove nothing")
	leaked := coordinator.InFlight()
	require.NotZero(t, leaked, "the leak detector itself is dead")
}

// waitForCoordinatorGoroutines waits for this test's goroutines to exit.
func waitForCoordinatorGoroutines(t *testing.T, baseline int) {
	t.Helper()
	require.Eventually(t, func() bool {
		runtime.GC()
		return runtime.NumGoroutine() <= baseline+3
	}, 10*time.Second, 20*time.Millisecond,
		"goroutines: have %d, baseline %d — a waiter leaked", runtime.NumGoroutine(), baseline)
}
