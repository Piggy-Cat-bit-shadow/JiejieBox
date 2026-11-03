package anytls

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/resourceguard"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/stretchr/testify/require"
)

// TestDialWithAdmissionNeverDialsWhenRefused is the seam's contract under a pinned gate: a
// caller that could not get a permit must not have started the expensive work at all. A seam
// that dials first and asks afterwards would make the whole gate cosmetic.
func TestDialWithAdmissionNeverDialsWhenRefused(t *testing.T) {
	guard := resourceguard.NewGuard(resourceguard.DefaultNetworkExtensionConfig(), func() resourceguard.Level {
		return resourceguard.LevelCritical
	})
	stalled := make([]*resourceguard.Permit, 0, 8)
	for index := 0; index < 8; index++ {
		permit, err := guard.Acquire(context.Background(), resourceguard.ClassNormal)
		require.NoError(t, err)
		stalled = append(stalled, permit)
	}

	dial := &burstDial{}
	_, err := dialWithAdmission(context.Background(), guard, dial.dial, M.Socksaddr{})
	require.Error(t, err, "a pinned gate must refuse")
	require.Zero(t, dial.calls.Load(), "the refused caller must not have dialled")

	for _, permit := range stalled {
		permit.Release()
	}
}

// TestDialWithAdmissionCancelDuringQueueDoesNotDial attacks the queue: a caller that cancels
// while waiting must return its context's error and must not fall through to the dial.
func TestDialWithAdmissionCancelDuringQueueDoesNotDial(t *testing.T) {
	config := resourceguard.DefaultNetworkExtensionConfig()
	config.Limit = 1
	config.Queue = 64
	config.Wait = 5 * time.Second
	guard := resourceguard.NewGuard(config, func() resourceguard.Level { return resourceguard.LevelNormal })

	held, err := guard.Acquire(context.Background(), resourceguard.ClassNormal)
	require.NoError(t, err)

	dial := &burstDial{}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, dialErr := dialWithAdmission(ctx, guard, dial.dial, M.Socksaddr{})
		result <- dialErr
	}()
	require.Eventually(t, func() bool { return guard.Stats().Gate.Waiters == 1 }, time.Second, time.Millisecond)
	cancel()

	require.ErrorIs(t, <-result, context.Canceled)
	require.Zero(t, dial.calls.Load(), "a cancelled waiter must not dial")
	held.Release()
	require.Zero(t, guard.Stats().Gate.Inflight)
}

// TestDialWithAdmissionBoundarySizes covers the sizes where an off-by-one in the seam's
// accounting would show: nothing, one, one below the cap, at the cap, and one above it.
func TestDialWithAdmissionBoundarySizes(t *testing.T) {
	const limit = 4
	for _, burst := range []int{0, 1, limit - 1, limit, limit + 1, 100} {
		burst := burst
		t.Run("burst", func(t *testing.T) {
			config := resourceguard.DefaultNetworkExtensionConfig()
			config.Limit = limit
			config.Queue = 64
			config.Wait = 2 * time.Second
			guard := resourceguard.NewGuard(config, func() resourceguard.Level { return resourceguard.LevelNormal })
			dial := &burstDial{hold: time.Millisecond}

			var wg sync.WaitGroup
			var admitted atomic.Int64
			var refused atomic.Int64
			for index := 0; index < burst; index++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					conn, dialErr := dialWithAdmission(context.Background(), guard, dial.dial, M.Socksaddr{})
					if dialErr != nil {
						refused.Add(1)
						return
					}
					admitted.Add(1)
					conn.Close()
				}()
			}
			wg.Wait()

			require.Equal(t, int64(burst), admitted.Load()+refused.Load(),
				"every caller must be admitted or refused exactly once")
			require.Equal(t, admitted.Load(), dial.calls.Load(),
				"every admitted caller dials exactly once, and no refused caller dials")
			require.LessOrEqual(t, dial.peak.Load(), int64(limit), "the ceiling must hold at burst %d", burst)
			stats := guard.Stats().Gate
			require.Zero(t, stats.Inflight)
			require.Zero(t, stats.Waiters)
			require.Equal(t, int(stats.Admitted-stats.Released), stats.Inflight)
		})
	}
}

// BenchmarkDialWithAdmissionParallel is the contention question the hot path has to answer: if
// the gate serialised every dial, the admission would be paid for with throughput. RunParallel
// keeps many goroutines in Acquire/Release at once.
func BenchmarkDialWithAdmissionParallel(b *testing.B) {
	guard := resourceguard.NewGuard(resourceguard.DefaultNetworkExtensionConfig(), func() resourceguard.Level {
		return resourceguard.LevelNormal
	})
	dial := &burstDial{}
	destination := M.Socksaddr{}
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			conn, err := dialWithAdmission(context.Background(), guard, dial.dial, destination)
			if err != nil {
				b.Fatal(err)
			}
			conn.Close()
		}
	})
}

// BenchmarkDialWithAdmissionNoGuardParallel is the same measurement with no guard, so the
// contention number has a baseline.
func BenchmarkDialWithAdmissionNoGuardParallel(b *testing.B) {
	dial := &burstDial{}
	destination := M.Socksaddr{}
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			conn, err := dialWithAdmission(context.Background(), nil, dial.dial, destination)
			if err != nil {
				b.Fatal(err)
			}
			conn.Close()
		}
	})
}

// TestDialAdmittedSeesNoPermitLeakOnPanic proves the permit survives a panicking dial.
//
// The release is a defer, so a panic unwinds through it; a gate whose accounting depended on the
// happy path would lose one slot per panic and eventually close itself.
func TestDialAdmittedSeesNoPermitLeakOnPanic(t *testing.T) {
	config := resourceguard.DefaultNetworkExtensionConfig()
	config.Limit = 2
	guard := resourceguard.NewGuard(config, func() resourceguard.Level { return resourceguard.LevelNormal })

	panicking := func(ctx context.Context, destination M.Socksaddr) (net.Conn, error) {
		panic("dial panic")
	}
	require.Panics(t, func() {
		_, _ = dialWithAdmission(context.Background(), guard, panicking, M.Socksaddr{})
	})
	require.Zero(t, guard.Stats().Gate.Inflight, "a panicking dial must still return its permit")

	// And the gate is still usable afterwards.
	dial := &burstDial{}
	conn, err := dialWithAdmission(context.Background(), guard, dial.dial, M.Socksaddr{})
	require.NoError(t, err)
	conn.Close()
}
