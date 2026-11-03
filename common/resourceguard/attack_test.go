package resourceguard

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// This file is the red-team suite for the admission guard. Every test here is written from the
// attacker's side: it drives the gate into a shape the happy path does not, and asserts the
// property that must hold anyway. Tests marked ATTACK name the failure they hunt for.

// ATTACK: "higher pressure must never mean more admission".
//
// The base plan is the only number an operator tunes, and the other levels are derived from it.
// If a derived level's floor is larger than the base plan, then arming the guard makes it admit
// MORE than normal - the policy inverts exactly when it is supposed to tighten. The floors exist
// so a degenerate config does not deadlock, and they must never cross the level above them.
func TestAttackDerivedPlansNeverExceedTheBasePlan(t *testing.T) {
	for _, config := range []Config{
		{Limit: 1, Queue: 1, Wait: 10 * time.Millisecond},
		{Limit: 2, Queue: 2, Wait: 10 * time.Millisecond},
		{Limit: 3, Queue: 8, Wait: 500 * time.Millisecond},
		{Limit: 4, Queue: 16, Wait: time.Second},
		DefaultNetworkExtensionConfig(),
	} {
		normal := config.planFor(LevelNormal)
		armed := config.planFor(LevelArmed)
		critical := config.planFor(LevelCritical)

		require.LessOrEqual(t, armed.limit, normal.limit,
			"armed limit %d exceeds normal %d for %+v", armed.limit, normal.limit, config)
		require.LessOrEqual(t, critical.limit, armed.limit,
			"critical limit %d exceeds armed %d for %+v", critical.limit, armed.limit, config)
		require.LessOrEqual(t, armed.queue, normal.queue,
			"armed queue %d exceeds normal %d for %+v", armed.queue, normal.queue, config)
		require.LessOrEqual(t, critical.queue, armed.queue,
			"critical queue %d exceeds armed %d for %+v", critical.queue, armed.queue, config)
		require.LessOrEqual(t, armed.wait, normal.wait,
			"armed wait %v exceeds normal %v for %+v", armed.wait, normal.wait, config)
		require.LessOrEqual(t, critical.wait, armed.wait,
			"critical wait %v exceeds armed %v for %+v", critical.wait, armed.wait, config)
	}
}

// ATTACK: "a priority class must not be a permanent lockout for the other class".
//
// The interactive reserve is only useful if it is bounded the other way too. This test holds the
// gate at its limit with a queued background caller and then keeps serving interactive arrivals
// that only fit in the reserve: without an aging rule the background waiter never fits, because
// every released slot is immediately re-taken by the next interactive caller.
func TestAttackInteractiveTrafficCannotStarveBackgroundForever(t *testing.T) {
	var level atomic.Uint32
	config := DefaultNetworkExtensionConfig()
	config.Limit = 2
	config.InteractiveReserve = 2
	config.StarvationAfter = 40 * time.Millisecond
	guard := NewGuard(config, func() Level { return Level(uint8(level.Load())) })

	normalOne, err := guard.Acquire(context.Background(), ClassNormal)
	require.NoError(t, err)
	normalTwo, err := guard.Acquire(context.Background(), ClassNormal)
	require.NoError(t, err)

	admitted := make(chan struct{}, 1)
	go func() {
		permit, acquireErr := guard.Acquire(context.Background(), ClassNormal)
		if acquireErr == nil {
			permit.Release()
		}
		admitted <- struct{}{}
	}()
	require.Eventually(t, func() bool { return guard.Stats().Gate.Waiters == 1 }, time.Second, time.Millisecond)

	// Keep the gate AT its limit at every instant: the reserve slot is released and immediately
	// re-taken by the interactive class, so the queued background caller never sees a moment
	// when it fits. Without an aging rule this loop is a permanent lockout.
	interactive, err := guard.Acquire(context.Background(), ClassInteractive)
	require.NoError(t, err)
	for round := 0; round < 60; round++ {
		interactive.Release()
		nextInteractive, acquireErr := guard.Acquire(context.Background(), ClassInteractive)
		require.NoError(t, acquireErr, "the interactive class must keep fitting in the reserve")
		interactive = nextInteractive
		select {
		case <-admitted:
			interactive.Release()
			normalOne.Release()
			normalTwo.Release()
			return
		case <-time.After(5 * time.Millisecond):
		}
	}
	t.Fatal("the background waiter was passed over for 60 reserve handoffs; the reserve is a lockout")
}

// ATTACK: "the gate must not buy its bound with a goroutine per waiter".
//
// The pressure this mechanism exists for is dominated by goroutine stacks, so a queue that parks
// one goroutine per waiter would recreate the problem it is meant to solve. A waiter must cost a
// channel and a struct, nothing else.
func TestAttackQueuedWaitersDoNotOwnGoroutines(t *testing.T) {
	config := DefaultNetworkExtensionConfig()
	config.Limit = 1
	config.Queue = 2048
	config.Wait = 2 * time.Second
	guard := NewGuard(config, func() Level { return LevelNormal })

	held, err := guard.Acquire(context.Background(), ClassNormal)
	require.NoError(t, err)

	baseline := runtime.NumGoroutine()
	const waiters = 500
	ctx, cancel := context.WithCancel(context.Background())
	var entered sync.WaitGroup
	var blocked sync.WaitGroup
	blocked.Add(waiters)
	for index := 0; index < waiters; index++ {
		entered.Add(1)
		go func() {
			defer blocked.Done()
			entered.Done()
			_, _ = guard.Acquire(ctx, ClassNormal)
		}()
	}
	entered.Wait()
	require.Eventually(t, func() bool { return guard.Stats().Gate.Waiters == waiters }, 5*time.Second, time.Millisecond)

	// The 500 goroutines here are the CALLERS. The claim is that the gate adds none of its own,
	// so the count must not exceed baseline + waiters.
	during := runtime.NumGoroutine()
	require.LessOrEqual(t, during, baseline+waiters+5,
		"the gate must not spawn a goroutine per waiter: baseline=%d during=%d", baseline, during)

	cancel()
	blocked.Wait()
	held.Release()
	require.Zero(t, guard.Stats().Gate.Inflight)
	require.Zero(t, guard.Stats().Gate.Waiters)
}

// ATTACK: "sustained pressure must not turn the shed hook into a per-dial action".
//
// The shed path closes idle sessions, which is cheap but not free, and it runs on the dial path.
// It must therefore be edge-triggered: one execution per entry into critical, not one per dial.
func TestAttackShedDoesNotRunPerDial(t *testing.T) {
	var level atomic.Uint32
	level.Store(uint32(LevelCritical))
	config := DefaultNetworkExtensionConfig()
	config.Limit = 64
	guard := NewGuard(config, func() Level { return Level(uint8(level.Load())) })

	var sheds atomic.Int64
	guard.RegisterShed(func() { sheds.Add(1) })

	for index := 0; index < 200; index++ {
		permit, err := guard.Acquire(context.Background(), ClassNormal)
		require.NoError(t, err)
		permit.Release()
	}
	require.Equal(t, int64(1), sheds.Load(),
		"200 dials under steady critical must shed once, not 200 times")
}

// ATTACK: "an oscillating pressure signal must not become an oscillating reclaim signal".
//
// The demote dwell is the only thing standing between a jittery footprint reading and a shed
// hook firing on every sample. With the dwell longer than the oscillation half-period, the guard
// must stay critical across the toggles and shed exactly once.
func TestAttackOscillatingPressureDoesNotAmplifyReclaim(t *testing.T) {
	var level atomic.Uint32
	config := DefaultNetworkExtensionConfig()
	config.DemoteAfter = 50 * time.Millisecond
	guard := NewGuard(config, func() Level { return Level(uint8(level.Load())) })

	var sheds atomic.Int64
	guard.RegisterShed(func() { sheds.Add(1) })

	for round := 0; round < 10; round++ {
		level.Store(uint32(LevelCritical))
		guard.Level()
		time.Sleep(5 * time.Millisecond)
		level.Store(uint32(LevelNormal))
		guard.Level()
		time.Sleep(5 * time.Millisecond)
	}
	require.Equal(t, int64(1), sheds.Load(),
		"toggling every 5 ms under a 50 ms dwell must not re-shed on each toggle")
	require.Equal(t, LevelCritical, guard.Level(), "the dwell must keep the guard critical")
}

// ATTACK: "the queue bound, not the limit, must decide who is refused at every size".
//
// 0/1/cap-1/cap/cap+1 are the boundary sizes where an off-by-one in the queue check shows up as
// either an unbounded queue or a refused caller that should have been queued.
func TestAttackQueueBoundaries(t *testing.T) {
	const limit = 4
	const queue = 8
	for _, burst := range []int{0, 1, limit - 1, limit, limit + 1, limit + queue, limit + queue + 1, 200} {
		burst := burst
		t.Run("burst", func(t *testing.T) {
			gate := NewGate(plan{limit: limit, queue: queue, wait: 30 * time.Millisecond}, 0)
			var admitted atomic.Int64
			var refused atomic.Int64
			var wg sync.WaitGroup
			for index := 0; index < burst; index++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					permit, err := gate.Acquire(context.Background(), ClassNormal)
					if err != nil {
						refused.Add(1)
						return
					}
					admitted.Add(1)
					time.Sleep(time.Millisecond)
					permit.Release()
				}()
			}
			wg.Wait()

			stats := gate.Stats()
			require.Equal(t, int64(burst), admitted.Load()+refused.Load())
			require.LessOrEqual(t, stats.MaxInflight, limit,
				"in-flight must never exceed the limit")
			require.Zero(t, stats.Inflight)
			require.Zero(t, stats.Waiters)
			require.Equal(t, int(stats.Admitted-stats.Released), stats.Inflight,
				"slot accounting drifted at burst %d", burst)
		})
	}
}
