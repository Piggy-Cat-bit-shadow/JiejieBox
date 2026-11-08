package urltest

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The coordinator orders waiters: interactive probes are served before queued background work, and
// background is aged in so a continuous interactive burst cannot starve health checks.
//
// These tests drive the real coordinator. The acquisitions hold their slot until the test releases
// it, so the order of grants is observed rather than inferred from timing.

type coordinatorAcquisition struct {
	release func()
	err     error
	done    chan struct{}
}

func startAcquisition(coordinator *Coordinator, ctx context.Context, priority bool) *coordinatorAcquisition {
	acquisition := &coordinatorAcquisition{done: make(chan struct{})}
	go func() {
		defer close(acquisition.done)
		if priority {
			acquisition.release, acquisition.err = coordinator.AcquirePriority(ctx)
			return
		}
		acquisition.release, acquisition.err = coordinator.Acquire(ctx)
	}()
	return acquisition
}

func waitGranted(t *testing.T, acquisition *coordinatorAcquisition) {
	t.Helper()
	select {
	case <-acquisition.done:
		require.NoError(t, acquisition.err)
	case <-time.After(2 * time.Second):
		t.Fatal("the acquisition was never granted")
	}
}

func requireNotGranted(t *testing.T, acquisition *coordinatorAcquisition) {
	t.Helper()
	select {
	case <-acquisition.done:
		t.Fatal("a waiter was granted while the slot was held")
	case <-time.After(20 * time.Millisecond):
	}
}

// enqueueAcquisition starts one acquisition and waits until it is actually queued, so the tests
// observe a deterministic queue order instead of the goroutine scheduler's.
func enqueueAcquisition(t *testing.T, coordinator *Coordinator, priority bool) *coordinatorAcquisition {
	t.Helper()
	queued := coordinator.Queued()
	acquisition := startAcquisition(coordinator, context.Background(), priority)
	require.Eventually(t, func() bool { return coordinator.Queued() == queued+1 },
		2*time.Second, time.Millisecond, "the acquisition did not reach the queue")
	return acquisition
}

func TestForegroundAcquisitionJumpsQueuedBackground(t *testing.T) {
	coordinator := NewCoordinator(1)
	held, err := coordinator.Acquire(context.Background())
	require.NoError(t, err)

	first := enqueueAcquisition(t, coordinator, false)
	second := enqueueAcquisition(t, coordinator, false)
	require.Eventually(t, func() bool { return coordinator.Queued() == 2 }, 2*time.Second, time.Millisecond,
		"the two background measurements must be queued before the interactive one arrives")

	interactive := enqueueAcquisition(t, coordinator, true)
	require.Eventually(t, func() bool { return coordinator.Queued() == 3 }, 2*time.Second, time.Millisecond)

	held()
	waitGranted(t, interactive)
	require.Equal(t, 1, coordinator.InFlight())
	requireNotGranted(t, first)
	requireNotGranted(t, second)

	interactive.release()
	waitGranted(t, first)
	requireNotGranted(t, second)
	first.release()
	waitGranted(t, second)
	second.release()

	require.Eventually(t, func() bool {
		return coordinator.InFlight() == 0 && coordinator.Queued() == 0
	}, 2*time.Second, time.Millisecond)
}

func TestBackgroundIsAgedInAfterAnInteractiveBurst(t *testing.T) {
	coordinator := NewCoordinator(1)
	held, err := coordinator.Acquire(context.Background())
	require.NoError(t, err)

	background := make([]*coordinatorAcquisition, 4)
	for index := range background {
		background[index] = enqueueAcquisition(t, coordinator, false)
	}
	interactive := make([]*coordinatorAcquisition, 4)
	for index := range interactive {
		interactive[index] = enqueueAcquisition(t, coordinator, true)
	}
	require.Eventually(t, func() bool { return coordinator.Queued() == 8 }, 2*time.Second, time.Millisecond)

	held()

	// priorityAgingEvery interactive grants in a row, then the oldest background waiter.
	for index := 0; index < priorityAgingEvery; index++ {
		waitGranted(t, interactive[index])
		interactive[index].release()
	}
	waitGranted(t, background[0])
	requireNotGranted(t, interactive[priorityAgingEvery])
	background[0].release()

	// The streak reset on the background grant, so the remaining interactive waiters go next.
	waitGranted(t, interactive[priorityAgingEvery])
	interactive[priorityAgingEvery].release()
	waitGranted(t, background[1])
	background[1].release()

	for index := priorityAgingEvery + 1; index < len(interactive); index++ {
		waitGranted(t, interactive[index])
		interactive[index].release()
	}
	for index := 2; index < len(background); index++ {
		waitGranted(t, background[index])
		background[index].release()
	}

	require.Eventually(t, func() bool {
		return coordinator.InFlight() == 0 && coordinator.Queued() == 0
	}, 2*time.Second, time.Millisecond)
}

func TestCanceledPriorityWaiterLeavesNoSlotOrQueueEntry(t *testing.T) {
	coordinator := NewCoordinator(1)
	held, err := coordinator.Acquire(context.Background())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	interactive := startAcquisition(coordinator, ctx, true)
	require.Eventually(t, func() bool { return coordinator.Queued() == 1 }, 2*time.Second, time.Millisecond)
	cancel()
	select {
	case <-interactive.done:
		require.ErrorIs(t, interactive.err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("the canceled waiter was never released")
	}
	require.Zero(t, coordinator.Queued(), "a canceled waiter must be removed from the queue")

	background := startAcquisition(coordinator, context.Background(), false)
	require.Eventually(t, func() bool { return coordinator.Queued() == 1 }, 2*time.Second, time.Millisecond)
	held()
	waitGranted(t, background)
	background.release()
	require.Eventually(t, func() bool { return coordinator.InFlight() == 0 }, 2*time.Second, time.Millisecond)
}

func TestSlotReleaseIsIdempotent(t *testing.T) {
	coordinator := NewCoordinator(1)
	release, err := coordinator.Acquire(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, coordinator.InFlight())
	release()
	release()
	require.Equal(t, 0, coordinator.InFlight(),
		"a double release must not free a second slot or drive the count negative")
}
