package resourceguard

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func rateTestGuard() *Guard {
	config := Config{
		Limit:           8,
		Queue:           16,
		Wait:            time.Second,
		DemoteAfter:     time.Second,
		StarvationAfter: time.Second,
	}
	return NewGuard(config, func() Level { return LevelNormal })
}

// TestFastFailuresArmARateWindowForNonInteractiveWork is the core contract: a streak of instant
// failures slows NEW non-interactive creations, and an interactive flow is not slowed by it.
func TestFastFailuresArmARateWindowForNonInteractiveWork(t *testing.T) {
	guard := rateTestGuard()
	now := time.Unix(1700000000, 0)
	guard.useClock(func() time.Time { return now })
	guard.rateConfig.RateLimitJitter = 0

	var waits []time.Duration
	guard.wait = func(ctx context.Context, delay time.Duration) error {
		waits = append(waits, delay)
		return nil
	}

	for index := 0; index < guard.rateConfig.RateLimitAfter; index++ {
		guard.ObserveOutcome(now, errors.New("handshake failed"))
	}
	stats := guard.Stats().Rate
	require.Equal(t, uint64(1), stats.Windows)
	require.Equal(t, time.Duration(guard.rateConfig.RateLimitBase), stats.Delay)

	permit, err := guard.Acquire(context.Background(), ClassNormal)
	require.NoError(t, err)
	permit.Release()
	require.Len(t, waits, 1, "a normal creation inside the window must wait for it")
	require.Equal(t, time.Duration(guard.rateConfig.RateLimitBase), waits[0])

	permit, err = guard.Acquire(context.Background(), ClassInteractive)
	require.NoError(t, err)
	permit.Release()
	require.Len(t, waits, 1, "the interactive class must not inherit a background failure storm")
}

// TestLocalRejectionsDoNotArmTheRateWindow keeps this process's own backpressure out of the far-side
// evidence. A queue refusal says nothing about whether the node is failing.
func TestLocalRejectionsDoNotArmTheRateWindow(t *testing.T) {
	guard := rateTestGuard()
	now := time.Unix(1700000000, 0)
	guard.useClock(func() time.Time { return now })

	for index := 0; index < guard.rateConfig.RateLimitAfter*4; index++ {
		guard.ObserveOutcome(now, ErrQueueFull)
		guard.ObserveOutcome(now, ErrWaitTimeout)
		guard.ObserveOutcome(now, ErrRateLimited)
		guard.ObserveOutcome(now, context.Canceled)
		guard.ObserveOutcome(now, context.DeadlineExceeded)
	}
	stats := guard.Stats().Rate
	require.Zero(t, stats.Windows)
	require.Zero(t, stats.FastFailures)
}

// TestSlowFailuresDoNotCountAsFast keeps a real attempt out of the streak: a failure that took
// longer than the fast-fail window is evidence that work happened, not that the far side refuses
// instantly.
func TestSlowFailuresDoNotCountAsFast(t *testing.T) {
	guard := rateTestGuard()
	now := time.Unix(1700000000, 0)
	guard.useClock(func() time.Time { return now })

	slowStart := now.Add(-2 * guard.rateConfig.FastFailWindow)
	for index := 0; index < guard.rateConfig.RateLimitAfter*2; index++ {
		guard.ObserveOutcome(slowStart, errors.New("timed out"))
	}
	stats := guard.Stats().Rate
	require.Zero(t, stats.Windows)
	require.Zero(t, stats.FastFailures)
}

// TestSuccessClearsTheWindow is the recovery half: one successful creation must re-open the path
// instead of leaving the process in a backoff that no longer describes the network.
func TestSuccessClearsTheWindow(t *testing.T) {
	guard := rateTestGuard()
	now := time.Unix(1700000000, 0)
	guard.useClock(func() time.Time { return now })
	guard.rateConfig.RateLimitJitter = 0

	for index := 0; index < guard.rateConfig.RateLimitAfter; index++ {
		guard.ObserveOutcome(now, errors.New("refused"))
	}
	require.NotZero(t, guard.Stats().Rate.Delay)

	guard.ObserveOutcome(now, nil)
	stats := guard.Stats().Rate
	require.Zero(t, stats.Delay)
	require.Equal(t, uint64(1), stats.Recovered)
}

// TestBackoffGrowsAndCaps proves the delay is exponential, jitter-free for the test, and bounded:
// a long outage must not turn into a queue that never moves.
func TestBackoffGrowsAndCaps(t *testing.T) {
	guard := rateTestGuard()
	now := time.Unix(1700000000, 0)
	guard.useClock(func() time.Time { return now })
	guard.rateConfig.RateLimitJitter = 0
	guard.rateConfig.RateLimitBase = 100 * time.Millisecond
	guard.rateConfig.RateLimitMax = time.Second

	var delays []time.Duration
	for window := 0; window < 6; window++ {
		for index := 0; index < guard.rateConfig.RateLimitAfter; index++ {
			guard.ObserveOutcome(now, errors.New("refused"))
		}
		delays = append(delays, guard.Stats().Rate.Delay)
		now = now.Add(guard.Stats().Rate.Delay)
	}
	require.Equal(t, []time.Duration{
		100 * time.Millisecond,
		200 * time.Millisecond,
		400 * time.Millisecond,
		800 * time.Millisecond,
		time.Second,
		time.Second,
	}, delays)
}

// TestWindowLongerThanTheCallerBudgetRefusesLocally proves the refusal path: when the wait would
// outlast the caller's deadline, Acquire returns the local rate error immediately instead of
// waiting for a deadline it is going to miss.
func TestWindowLongerThanTheCallerBudgetRefusesLocally(t *testing.T) {
	guard := rateTestGuard()
	now := time.Unix(1700000000, 0)
	guard.useClock(func() time.Time { return now })
	guard.rateConfig.RateLimitJitter = 0
	guard.rateConfig.RateLimitBase = time.Second

	for index := 0; index < guard.rateConfig.RateLimitAfter; index++ {
		guard.ObserveOutcome(now, errors.New("refused"))
	}
	ctx, cancel := context.WithDeadline(context.Background(), now.Add(50*time.Millisecond))
	defer cancel()

	permit, err := guard.Acquire(ctx, ClassNormal)
	require.Nil(t, permit)
	require.ErrorIs(t, err, ErrRateLimited)
	require.True(t, IsLocalRejection(err))
	require.Equal(t, uint64(1), guard.Stats().Rate.Refused)
}

// TestNilGuardRatePolicyIsSafe covers the no-guard platform: feedback from a seam that has no
// policy configured must cost nothing.
func TestNilGuardRatePolicyIsSafe(t *testing.T) {
	var guard *Guard
	require.NotPanics(t, func() {
		guard.ObserveOutcome(time.Now(), errors.New("boom"))
	})
	require.False(t, IsLocalRejection(nil))
}

// TestLocalRejectionClassification pins the set ADMISSION errors that must not be reported as node
// failures, including the context errors a caller causes itself.
func TestLocalRejectionClassification(t *testing.T) {
	for _, err := range []error{ErrQueueFull, ErrWaitTimeout, ErrRateLimited, context.Canceled, context.DeadlineExceeded} {
		require.True(t, IsLocalRejection(err), "%v must be local", err)
	}
	for _, err := range []error{nil, errors.New("connection refused")} {
		require.False(t, IsLocalRejection(err), "%v must not be local", err)
	}
}
