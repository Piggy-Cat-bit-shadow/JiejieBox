package resourceguard

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestFastFailWindowIsIsolatedByDestination is the reason the rate state is bucketed: a storm
// against one proxy server must not make a creation aimed at another server wait. The bad
// destination is armed; the good one is acquired with the same guard and must not see the window.
func TestFastFailWindowIsIsolatedByDestination(t *testing.T) {
	guard := rateTestGuard()
	now := time.Unix(1700000000, 0)
	guard.useClock(func() time.Time { return now })
	guard.rateConfig.RateLimitJitter = 0

	var waits []time.Duration
	guard.wait = func(ctx context.Context, delay time.Duration) error {
		waits = append(waits, delay)
		return nil
	}

	const bad = "bad.example:443"
	const good = "good.example:443"
	for index := 0; index < guard.rateConfig.RateLimitAfter; index++ {
		guard.ObserveOutcomeFor(bad, now, errors.New("handshake failed"))
	}
	stats := guard.Stats().Rate
	require.Equal(t, uint64(1), stats.Windows)
	require.Equal(t, 1, stats.ArmedBuckets)
	require.NotZero(t, stats.Delay)

	permit, err := guard.AcquireFor(context.Background(), ClassNormal, good)
	require.NoError(t, err)
	permit.Release()
	require.Empty(t, waits, "a creation aimed at another destination must not inherit the bad node's window")

	permit, err = guard.AcquireFor(context.Background(), ClassNormal, bad)
	require.NoError(t, err)
	permit.Release()
	require.Len(t, waits, 1, "the same destination must still be slowed")
	require.Equal(t, time.Duration(guard.rateConfig.RateLimitBase), waits[0])

	permit, err = guard.AcquireFor(context.Background(), ClassInteractive, bad)
	require.NoError(t, err)
	permit.Release()
	require.Len(t, waits, 1, "interactive work aimed at the failing destination is still exempt")
}

// TestSuccessForOneDestinationDoesNotClearAnother keeps recovery local too: a success at the good
// destination says nothing about the bad one, and must not reopen its window.
func TestSuccessForOneDestinationDoesNotClearAnother(t *testing.T) {
	guard := rateTestGuard()
	now := time.Unix(1700000000, 0)
	guard.useClock(func() time.Time { return now })
	guard.rateConfig.RateLimitJitter = 0

	const bad = "bad.example:443"
	const good = "good.example:443"
	for index := 0; index < guard.rateConfig.RateLimitAfter; index++ {
		guard.ObserveOutcomeFor(bad, now, errors.New("refused"))
	}
	guard.ObserveOutcomeFor(good, now, nil)

	stats := guard.Stats().Rate
	require.Equal(t, 1, stats.ArmedBuckets)
	require.NotZero(t, stats.Delay)
	require.Zero(t, stats.Recovered, "a success at a destination that was never failing clears nothing")

	var waited bool
	guard.wait = func(ctx context.Context, delay time.Duration) error {
		waited = true
		return nil
	}
	permit, err := guard.AcquireFor(context.Background(), ClassNormal, bad)
	require.NoError(t, err)
	permit.Release()
	require.True(t, waited, "the bad destination's window must survive a success elsewhere")
}

// TestABadNodeStormDoesNotDelayAGoodDestination is the A/B measurement the audit asked for: under a
// sustained bad-destination storm, every creation to the good destination is admitted without a
// rate wait, while creations to the bad destination keep paying the window. The wait function is a
// counter, so the comparison measures policy decisions rather than sleeping.
func TestABadNodeStormDoesNotDelayAGoodDestination(t *testing.T) {
	guard := rateTestGuard()
	now := time.Unix(1700000000, 0)
	guard.useClock(func() time.Time { return now })
	guard.rateConfig.RateLimitJitter = 0

	var badWaits, goodWaits int
	guard.wait = func(ctx context.Context, delay time.Duration) error {
		return nil
	}

	const bad = "bad.example:443"
	const good = "good.example:443"
	for round := 0; round < 64; round++ {
		for index := 0; index < guard.rateConfig.RateLimitAfter; index++ {
			guard.ObserveOutcomeFor(bad, now, errors.New("authentication failed"))
		}
		before := guard.Stats().Rate.Delayed
		if permit, err := guard.AcquireFor(context.Background(), ClassNormal, bad); err == nil {
			permit.Release()
		}
		if guard.Stats().Rate.Delayed > before {
			badWaits++
		}
		before = guard.Stats().Rate.Delayed
		if permit, err := guard.AcquireFor(context.Background(), ClassNormal, good); err == nil {
			permit.Release()
		}
		if guard.Stats().Rate.Delayed > before {
			goodWaits++
		}
		now = now.Add(guard.rateConfig.RateLimitBase)
	}
	require.Equal(t, 64, badWaits, "every acquisition aimed at the failing destination waited")
	require.Zero(t, goodWaits, "no acquisition aimed at the healthy destination waited")
	require.Equal(t, 1, guard.Stats().Rate.ArmedBuckets)
}

// TestRateBucketsAreBounded keeps the per-destination map from becoming an unbounded allocation in
// the path that exists to protect memory. Totals survive eviction because they are aggregate.
func TestRateBucketsAreBounded(t *testing.T) {
	guard := rateTestGuard()
	now := time.Unix(1700000000, 0)
	guard.useClock(func() time.Time { return now })
	guard.rateConfig.RateLimitJitter = 0

	for index := 0; index < maxRateBuckets*2; index++ {
		key := "destination-" + time.Duration(index).String()
		permit, err := guard.AcquireFor(context.Background(), ClassNormal, key)
		require.NoError(t, err)
		permit.Release()
	}
	stats := guard.Stats().Rate
	require.LessOrEqual(t, stats.Buckets, maxRateBuckets)

	for index := 0; index < guard.rateConfig.RateLimitAfter; index++ {
		guard.ObserveOutcomeFor("evicted-destination", now, errors.New("refused"))
	}
	require.GreaterOrEqual(t, guard.Stats().Rate.FastFailures, uint64(guard.rateConfig.RateLimitAfter))
}
