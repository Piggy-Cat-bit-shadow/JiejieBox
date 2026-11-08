package adapter

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Bounded close diagnostics for Scope.Close.
//
// # The contract these tests pin
//
// A drain that outlives closeWatchdogTimeout reports, ONCE, which cleanup it is on, how many are done
// and how many are pending, and a stack bounded to closeDiagnosticsStackBudget bytes. The report is a
// diagnostic only: the drain still waits for the blocked cleanup, the Close result is unchanged, a
// concurrent Close still joins, and the timer is stopped when the drain finishes - including when a
// cleanup panics.
//
// # Why the timeout is shrunk here
//
// The shipping timeout is constant.StopTimeout (five seconds). These tests drive the same code with a
// short value; nothing else about the path changes.

// closeDiagnosticsOf reads the diagnostic record under the lock the watchdog uses, so the assertion
// cannot race a callback that is still in flight.
func closeDiagnosticsOf(scope *Scope) *closeDiagnostics {
	scope.access.Lock()
	defer scope.access.Unlock()
	return scope.closeDiag
}

// shrinkCloseWatchdog shortens the watchdog for one test.
func shrinkCloseWatchdog(t *testing.T, timeout time.Duration) {
	t.Helper()
	previous := closeWatchdogTimeout
	closeWatchdogTimeout = timeout
	t.Cleanup(func() { closeWatchdogTimeout = previous })
}

// waitForCloseReport blocks until the watchdog has written its report, or fails the test.
func waitForCloseReport(t *testing.T, logger *lateCleanupLogger) string {
	t.Helper()
	require.Eventually(t, func() bool {
		return strings.Contains(logger.captured(), "close is still running")
	}, 5*time.Second, 5*time.Millisecond, "the watchdog never reported a drain that was still running")
	return logger.captured()
}

// TestScopeCloseWatchdogNamesTheStageAndPendingWork drives two cleanups, one instant and one blocked,
// and asserts the report names the one the drain is stuck on rather than a generic message.
func TestScopeCloseWatchdogNamesTheStageAndPendingWork(t *testing.T) {
	shrinkCloseWatchdog(t, 100*time.Millisecond)
	logger := &lateCleanupLogger{}
	scope := NewScope(context.Background(), logger)

	entered := make(chan struct{})
	release := make(chan struct{})
	// Registered first, so the reverse-order drain runs the cache cleanup first and blocks on the
	// upstream one after it.
	scope.add("close upstream", func() error {
		close(entered)
		<-release
		return nil
	})
	scope.add("close cache", func() error { return nil })

	closed := make(chan error, 1)
	go func() {
		closed <- scope.Close()
	}()

	<-entered
	captured := waitForCloseReport(t, logger)
	require.Contains(t, captured, "1/2 cleanups completed",
		"the report must count the cleanup that finished before the stuck one")
	require.Contains(t, captured, "1 pending")
	require.Contains(t, captured, "current: close upstream",
		"the report must name the owner/stage the drain is stuck on")
	require.Contains(t, captured, "close stack, bounded to",
		"the stack must be bounded, not a full process dump")
	require.Contains(t, captured, "goroutine", "the bounded stack must still identify goroutines")

	close(release)
	require.NoError(t, <-closed)
	require.Equal(t, 1, strings.Count(logger.captured(), "close is still running"),
		"the watchdog is one-shot for a drain: repeated timeouts must not repeat the report")
	require.Nil(t, closeDiagnosticsOf(scope), "the diagnostic record must be cleared before Close returns")
}

// TestScopeCloseWatchdogDoesNotChangeTheCloseResult is the "diagnostic, not a second lifecycle"
// requirement: the blocked cleanup still runs to completion, its error is still the Close result, and
// a concurrent closer still joins the same drain.
func TestScopeCloseWatchdogDoesNotChangeTheCloseResult(t *testing.T) {
	shrinkCloseWatchdog(t, 100*time.Millisecond)
	logger := &lateCleanupLogger{}
	scope := NewScope(context.Background(), logger)
	closeErr := errors.New("unmount route: operation not permitted")

	entered := make(chan struct{})
	release := make(chan struct{})
	scope.Add(func() error {
		close(entered)
		<-release
		return closeErr
	})

	first := make(chan error, 1)
	go func() {
		first <- scope.Close()
	}()
	<-entered
	second := make(chan error, 1)
	go func() {
		second <- scope.Close()
	}()

	waitForCloseReport(t, logger)

	// The second closer must still be waiting: a watchdog that skipped or unblocked the drain would
	// let it return a success for a teardown that had not finished.
	select {
	case early := <-second:
		t.Fatalf("the second Close returned while the drain was still running: %v", early)
	case <-time.After(250 * time.Millisecond):
	}

	close(release)
	require.ErrorIs(t, <-first, closeErr,
		"the watchdog must not filter or replace the Close result")
	require.ErrorIs(t, <-second, closeErr, "a joining Close must return the drain's result")
	require.Equal(t, 1, strings.Count(logger.captured(), "close is still running"))
}

// TestScopeCloseWatchdogIsStoppedAfterAFastClose is the timer-leak check: a quick drain must cancel
// the watchdog, so nothing reports a hang after the close already succeeded.
func TestScopeCloseWatchdogIsStoppedAfterAFastClose(t *testing.T) {
	shrinkCloseWatchdog(t, 100*time.Millisecond)
	logger := &lateCleanupLogger{}
	scope := NewScope(context.Background(), logger)
	scope.add("close fast", func() error { return nil })

	require.NoError(t, scope.Close())
	time.Sleep(250 * time.Millisecond)

	require.NotContains(t, logger.captured(), "close is still running",
		"a watchdog that outlives its drain reports a hang that is not happening")
	require.Nil(t, closeDiagnosticsOf(scope))
}

// TestScopeCloseWatchdogIsStoppedWhenACleanupPanics covers the path where the drain never reaches its
// normal completion: the deferred disarm must still stop the timer, or the panic would leave a
// repeating report behind on a process that is already in a bad state.
func TestScopeCloseWatchdogIsStoppedWhenACleanupPanics(t *testing.T) {
	shrinkCloseWatchdog(t, 100*time.Millisecond)
	logger := &lateCleanupLogger{}
	scope := NewScope(context.Background(), logger)
	scope.Add(func() error { panic("cleanup panic") })

	require.Panics(t, func() { _ = scope.Close() })
	time.Sleep(250 * time.Millisecond)

	require.NotContains(t, logger.captured(), "close is still running",
		"the deferred disarm must run on the panic path too")
}
