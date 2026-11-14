package resourceguard

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// BackgroundAllowed is the single policy background owners consult. The line is Critical, not Armed:
// Armed still allows speculative work (it is a capacity reduction, not a shed), while Critical is
// where work that only improves freshness must yield to the flows being kept.
func guardContextAt(t *testing.T, level Level) context.Context {
	t.Helper()
	guard := NewGuard(DefaultNetworkExtensionConfig(), func() Level { return level })
	return NewContext(context.Background(), guard)
}

// BackgroundAllowed is the single policy background owners consult. The line is Critical, not Armed:
// Armed still allows speculative work (it is a capacity reduction, not a shed), while Critical is
// where work that only improves freshness must yield to the flows being kept.
func TestBackgroundAllowedSuppressesOnlyCritical(t *testing.T) {
	require.True(t, BackgroundAllowed(context.Background()),
		"a process without a guard must keep the historical behaviour")
	require.True(t, BackgroundAllowed(guardContextAt(t, LevelNormal)),
		"normal pressure allows background work")
	require.True(t, BackgroundAllowed(guardContextAt(t, LevelArmed)),
		"armed is a capacity reduction, not a reason to stop recovery work")
	require.False(t, BackgroundAllowed(guardContextAt(t, LevelCritical)),
		"critical pressure must stop speculative background work")
}

// MaxWaiters is the queue-side high-water mark. Sampling Waiters can miss the peak between two
// reads, which is exactly why the audit had to estimate it.
func TestGateStatsReportTheWaiterHighWaterMark(t *testing.T) {
	gate := NewBoundedGate(1, 8, time.Second)
	held, err := gate.Acquire(context.Background(), ClassNormal)
	require.NoError(t, err)

	const waiters = 3
	permits := make(chan *Permit, waiters)
	for index := 0; index < waiters; index++ {
		go func() {
			permit, acquireErr := gate.Acquire(context.Background(), ClassNormal)
			if acquireErr == nil {
				permits <- permit
			}
		}()
	}
	require.Eventually(t, func() bool {
		return gate.Stats().Waiters == waiters
	}, 2*time.Second, time.Millisecond, "the waiters never queued")

	require.Equal(t, waiters, gate.Stats().MaxWaiters,
		"the high-water mark must be recorded when the waiters queue")

	held.Release()
	for index := 0; index < waiters; index++ {
		select {
		case permit := <-permits:
			permit.Release()
		case <-time.After(2 * time.Second):
			t.Fatal("a queued waiter never received a permit")
		}
	}
	stats := gate.Stats()
	require.Equal(t, waiters, stats.MaxWaiters, "the mark is a maximum, not a current value")
	require.Zero(t, stats.Waiters)
	require.Zero(t, stats.Inflight)
}
