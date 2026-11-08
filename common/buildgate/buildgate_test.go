package buildgate

// The reservation itself: bounded, deferred instead of queued forever, cancellable, and
// countable. The provider path is one consumer; these cases pin the contract any second
// consumer (a remote rule-set compile) inherits.

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTheReservationBoundsConcurrentBuilds(t *testing.T) {
	gate := New(1, 0, time.Millisecond)
	release, err := gate.Acquire(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, gate.Stats().Inflight)

	_, err = gate.Acquire(context.Background())
	require.ErrorIs(t, err, ErrDeferred, "a second build must be deferred, not queued forever")
	require.EqualValues(t, 1, gate.Stats().Deferred)

	release()
	release, err = gate.Acquire(context.Background())
	require.NoError(t, err, "the reservation must be usable again once the build finished")
	release()
	require.EqualValues(t, 2, gate.Stats().AdmittedBuilds)
}

func TestACancelledCallerDoesNotWaitForTheReservation(t *testing.T) {
	gate := New(1, 1, time.Second)
	release, err := gate.Acquire(context.Background())
	require.NoError(t, err)
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = gate.Acquire(ctx)
	require.Error(t, err, "a cancelled caller must not be admitted or left waiting")
	require.NotErrorIs(t, err, ErrDeferred, "cancellation is not pressure and must not be reported as one")
}

func TestAWaitingBuildIsServedWhenTheReservationFrees(t *testing.T) {
	gate := New(1, 16, time.Second)
	release, err := gate.Acquire(context.Background())
	require.NoError(t, err)

	admitted := make(chan struct{})
	go func() {
		releaseWaiting, waitErr := gate.Acquire(context.Background())
		if waitErr == nil {
			close(admitted)
			releaseWaiting()
		}
	}()
	require.Eventually(t, func() bool { return gate.Stats().Waiters == 1 }, 5*time.Second, time.Millisecond)
	release()
	select {
	case <-admitted:
	case <-time.After(5 * time.Second):
		t.Fatal("a waiting build was never served after the reservation freed")
	}
}

func TestNoGateMeansUngated(t *testing.T) {
	release, err := AcquireFromContext(context.Background())
	require.NoError(t, err)
	release()
	require.Nil(t, FromContext(context.Background()))
}

func TestTheGateTravelsOnTheContext(t *testing.T) {
	gate := New(1, 0, time.Millisecond)
	ctx := ContextWithGate(context.Background(), gate)
	require.Same(t, gate, FromContext(ctx))
	release, err := AcquireFromContext(ctx)
	require.NoError(t, err)
	defer release()

	_, err = AcquireFromContext(ctx)
	require.ErrorIs(t, err, ErrDeferred)
}

// TestConcurrentBuildersCannotExceedTheBound is the reservation's whole reason: many build
// owners starting at once must never exceed the limit.
func TestConcurrentBuildersCannotExceedTheBound(t *testing.T) {
	const limit = 2
	gate := New(limit, 64, 50*time.Millisecond)
	var peak atomic.Int64
	var group sync.WaitGroup
	for range 16 {
		group.Add(1)
		go func() {
			defer group.Done()
			release, err := gate.Acquire(context.Background())
			if err != nil {
				return
			}
			inflight := int64(gate.Stats().Inflight)
			for {
				current := peak.Load()
				if inflight <= current || peak.CompareAndSwap(current, inflight) {
					break
				}
			}
			time.Sleep(2 * time.Millisecond)
			release()
		}()
	}
	group.Wait()
	require.LessOrEqual(t, peak.Load(), int64(limit), "the observed concurrent builds must stay inside the reservation")
	require.LessOrEqual(t, gate.Stats().Inflight, limit)
}
