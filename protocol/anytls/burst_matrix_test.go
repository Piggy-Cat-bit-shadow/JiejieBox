package anytls

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/resourceguard"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/stretchr/testify/require"
)

// TestAdmissionMatrix runs the burst sizes the iOS work is required to cover through the real
// seam, and asserts the same three properties at every size: the ceiling holds, every caller is
// accounted for exactly once, and the counter balance returns to zero.
//
// The dial is the cheap fake from admission_test.go rather than a real connection, because what
// is under test here is the ADMISSION decision at scale. The real client path is covered
// separately by the session burst tests; using it here would spend seconds per case on TCP
// connects without testing anything the fake does not.
func TestAdmissionMatrix(t *testing.T) {
	for _, burst := range []int{50, 200, 500, 1000} {
		burst := burst
		t.Run("burst-"+itoa(burst), func(t *testing.T) {
			config := resourceguard.DefaultNetworkExtensionConfig()
			config.Limit = 32
			config.Queue = 256
			guard := resourceguard.NewGuard(config, func() resourceguard.Level { return resourceguard.LevelNormal })
			dial := &burstDial{hold: time.Millisecond}

			var admitted atomic.Int64
			var refused atomic.Int64
			var wg sync.WaitGroup
			for index := 0; index < burst; index++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					conn, err := dialWithAdmission(context.Background(), guard, dial.dial, M.Socksaddr{})
					if err != nil {
						refused.Add(1)
						return
					}
					admitted.Add(1)
					conn.Close()
				}()
			}
			wg.Wait()

			require.Equal(t, int64(burst), admitted.Load()+refused.Load(),
				"every caller must be either admitted or refused, never lost")
			require.LessOrEqual(t, dial.peak.Load(), int64(32), "the ceiling must hold at every size")
			require.Equal(t, int64(burst)-admitted.Load(), refused.Load(),
				"the queue bound, not the limit, decides who is refused")

			stats := guard.Stats().Gate
			require.Zero(t, stats.Inflight)
			require.Zero(t, stats.Waiters)
			require.Equal(t, uint64(admitted.Load()), stats.Admitted)
			require.Equal(t, int(stats.Admitted-stats.Released), stats.Inflight,
				"slot accounting must balance after %d callers", burst)
		})
	}
}

// TestAdmissionCancelStorm cancels the burst while it is queued.
//
// A cancel storm is the failure mode a bounded queue can turn into a leak: every waiter that
// gives up must withdraw its own slot accounting, and a waiter that was granted at the same
// instant must hand the slot on. The assertion is not "it was fast" - it is that nothing was
// lost, which is what makes the gate reusable after the storm.
func TestAdmissionCancelStorm(t *testing.T) {
	config := resourceguard.DefaultNetworkExtensionConfig()
	config.Limit = 8
	config.Queue = 512
	config.Wait = time.Minute
	guard := resourceguard.NewGuard(config, func() resourceguard.Level { return resourceguard.LevelNormal })
	dial := &burstDial{hold: 20 * time.Millisecond}

	ctx, cancel := context.WithCancel(context.Background())
	var completed atomic.Int64
	var wg sync.WaitGroup
	for index := 0; index < 500; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = dialWithAdmission(ctx, guard, dial.dial, M.Socksaddr{})
			completed.Add(1)
		}()
	}

	// Let the burst reach the queue, then cancel every waiter at once.
	require.Eventually(t, func() bool { return guard.Stats().Gate.Waiters > 100 }, 5*time.Second, time.Millisecond)
	cancel()
	wg.Wait()

	require.Equal(t, int64(500), completed.Load(), "every cancelled caller must return")
	require.Eventually(t, func() bool {
		stats := guard.Stats().Gate
		return stats.Inflight == 0 && stats.Waiters == 0
	}, 5*time.Second, time.Millisecond)

	stats := guard.Stats().Gate
	require.Equal(t, int(stats.Admitted-stats.Released), stats.Inflight,
		"a cancel storm must not leak a slot: admitted=%d released=%d", stats.Admitted, stats.Released)
	require.Greater(t, stats.Canceled, uint64(0), "the storm must actually have cancelled waiters")
}

// itoa avoids pulling strconv into a test file for one integer.
func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [8]byte
	index := len(digits)
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[index:])
}
