package route

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Phase 03's addendum named this surface as NOT attacked. This closes it.
//
// # Why "the manager was closed" is not the interesting question
//
// Closing a manager is easy; what matters is what happens to a setup that was ALREADY in flight when the
// close arrived, and to one that arrives immediately after. Three outcomes are unacceptable and each has
// its own way of being silent:
//
//	setup survives the close   a dial keeps running against a manager that has torn its resources down,
//	                           and its connection is tracked into a list nobody will drain;
//	setup is not cancelled     the caller's dial runs to its own connect timeout while the process is
//	                           shutting down, so teardown appears to hang;
//	a setup after the close    gets a LIVE context, resurrects itself, and the manager's Close report
//	                           becomes a statement about a set that is no longer closed.
//
// The gate's own comment claims a late `begin` gets a dead setup rather than a live one. This is the test
// that holds it to that, and it is written at the gate because that is where the claim is made.

// TestCloseCancelsASetupAlreadyInFlight is the first half.
func TestCloseCancelsASetupAlreadyInFlight(t *testing.T) {
	manager := NewConnectionManager(nil)

	// A setup is in flight: the dial has begun and has not returned.
	setupCtx, release := manager.dialSetups.begin(context.Background())
	require.NoError(t, setupCtx.Err(), "the fixture must start with a live setup")

	require.NoError(t, manager.Close())

	select {
	case <-setupCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("a setup that was in flight when the manager closed was not cancelled: its dial runs on " +
			"against a manager that has already torn its resources down")
	}
	release()
}

// TestCloseGivesALaterSetupADeadContext is the second half, and the one the gate's comment is about.
//
// A setup that begins AFTER the close must not be resurrectable. The failure it prevents is a real one:
// a component that starts a background re-dial as it is being torn down would otherwise get a live context
// and keep the process alive, and the manager's Close report would describe a set that had grown again.
func TestCloseGivesALaterSetupADeadContext(t *testing.T) {
	manager := NewConnectionManager(nil)
	require.NoError(t, manager.Close())

	lateCtx, releaseLate := manager.dialSetups.begin(context.Background())
	defer releaseLate()

	require.Error(t, lateCtx.Err(),
		"a setup begun after Close received a LIVE context: it can resurrect a resource the manager has "+
			"already reported as closed")
	require.ErrorIs(t, lateCtx.Err(), context.Canceled)
}

// TestCloseIsIdempotentForTheGate is the third shape: Close is called twice.
//
// The gate returns early when it is already closed, so the second call must not panic, must not cancel
// anything twice in a way that matters, and must leave the state exactly as the first call left it. This is
// cheap to assert and it is the shape a `defer` and an explicit shutdown path produce together.
func TestCloseIsIdempotentForTheGate(t *testing.T) {
	manager := NewConnectionManager(nil)

	require.NoError(t, manager.Close())
	require.NotPanics(t, func() { _ = manager.Close() })

	lateCtx, releaseLate := manager.dialSetups.begin(context.Background())
	defer releaseLate()
	require.Error(t, lateCtx.Err(), "the second Close changed the gate's answer")
}

// TestASetupHeldAcrossACloseDoesNotDeadlockShutdown is the teardown-hang shape.
//
// `ConnectionManager.Close` closes the gate and then drains. A setup whose release function is never called
// must not make either step block: the gate cancels what is in flight and returns, and the release is a
// no-op afterwards. Asserting on a bounded wall clock is legitimate HERE and only here - the claim is
// literally "this does not hang", and there is no event to wait for.
func TestASetupHeldAcrossACloseDoesNotDeadlockShutdown(t *testing.T) {
	manager := NewConnectionManager(nil)

	// Deliberately never released before the close.
	_, release := manager.dialSetups.begin(context.Background())

	done := make(chan error, 1)
	go func() { done <- manager.Close() }()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Close blocked on a setup whose release function had not been called yet")
	}

	// The release must still be safe to call afterwards - it is the caller's deferred call, and a
	// shutdown path that made it panic would turn a clean close into a crash.
	require.NotPanics(t, release)
}
