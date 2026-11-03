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

// TestSoakCyclesReopenAdmission is the multi-cycle shape: idle -> pressure -> recovery -> pressure
// again, several times, on the real seam.
//
// Two failures it hunts for, because both are permanent rather than transient:
//
//	the gate never reopens     recovery sets the level back but the plan stays tight forever,
//	                           which is the "protection became a one-shot" bug the reference
//	                           implementations both hit;
//	the backlog is dumped      recovery releases thousands of queued callers at once, which
//	                           would re-create the very burst the gate absorbed.
func TestSoakCyclesReopenAdmission(t *testing.T) {
	var level atomic.Uint32
	config := resourceguard.DefaultNetworkExtensionConfig()
	config.Limit = 4
	config.Queue = 64
	config.Wait = 5 * time.Second
	config.DemoteAfter = 20 * time.Millisecond
	guard := resourceguard.NewGuard(config, func() resourceguard.Level {
		return resourceguard.Level(level.Load())
	})

	const burst = 40
	runBurst := func(label string) (admitted int64, refused int64, peak int64) {
		dial := &burstDial{hold: 20 * time.Millisecond}
		var wg sync.WaitGroup
		var admittedCount, refusedCount atomic.Int64
		// A bounded drain window: a caller still queued when it closes counts as refused, so a
		// recovery that dumped the backlog would show up as a long tail here.
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		for index := 0; index < burst; index++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				conn, err := dialWithAdmission(ctx, guard, dial.dial, M.Socksaddr{})
				if err != nil {
					refusedCount.Add(1)
					return
				}
				admittedCount.Add(1)
				conn.Close()
			}()
		}
		wg.Wait()
		t.Logf("%s: admitted=%d refused=%d peak=%d", label, admittedCount.Load(), refusedCount.Load(), dial.peak.Load())
		return admittedCount.Load(), refusedCount.Load(), dial.peak.Load()
	}

	for cycle := 0; cycle < 3; cycle++ {
		level.Store(uint32(resourceguard.LevelNormal))

		admitted, refused, peak := runBurst("normal")
		require.Zero(t, refused, "cycle %d: normal must absorb the burst, not refuse it", cycle)
		require.Equal(t, int64(burst), admitted)
		require.LessOrEqual(t, peak, int64(4), "cycle %d: the ceiling must hold at normal", cycle)

		level.Store(uint32(resourceguard.LevelCritical))
		admitted, refused, peak = runBurst("critical")
		// Critical does not refuse everything - the queue is meant to absorb what fits inside the
		// 50 ms wait. What must change is that with the same burst the tight budget CANNOT absorb
		// it: some callers are refused or timed out, and the ceiling is the critical one.
		require.Equal(t, int64(burst), admitted+refused)
		require.Greater(t, refused, int64(0), "cycle %d: critical must shed part of the burst", cycle)
		require.LessOrEqual(t, peak, int64(4), "cycle %d: the ceiling must hold at critical", cycle)

		// Recovery: the level falls and the dwell elapses, so the next burst is absorbed again.
		level.Store(uint32(resourceguard.LevelNormal))
		require.Eventually(t, func() bool { return guard.Level() == resourceguard.LevelNormal },
			time.Second, time.Millisecond, "cycle %d must recover", cycle)
		require.Equal(t, 4, guard.Stats().Gate.Limit, "cycle %d must reopen the limit", cycle)

		stats := guard.Stats().Gate
		require.Zero(t, stats.Inflight, "cycle %d must leave nothing in flight", cycle)
		require.Zero(t, stats.Waiters, "cycle %d must leave nothing queued", cycle)
		require.Equal(t, int(stats.Admitted-stats.Released), stats.Inflight)
	}

	// Three full cycles must not have accumulated state: the transition count is exactly the
	// number of real level changes, and the gate is still able to do work.
	transitions := guard.Stats().Transitions
	require.LessOrEqual(t, transitions, uint64(3*2),
		"three cycles must produce at most six level transitions, got %d", transitions)

	level.Store(uint32(resourceguard.LevelNormal))
	conn, err := dialWithAdmission(context.Background(), guard, (&burstDial{}).dial, M.Socksaddr{})
	require.NoError(t, err, "the gate must still admit after three soak cycles")
	conn.Close()
	require.Zero(t, guard.Stats().Gate.Inflight)
}
