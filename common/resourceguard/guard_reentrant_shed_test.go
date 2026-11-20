package resourceguard

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The reclaim/diagnostics pair (audit P12).
//
// Stats() is not read-only: it observes and, when the observation completes a transition, applies
// the plan and runs the shed hooks. That is a documented contract, not a defect - but it makes every
// diagnostic path a potential REENTRANT caller of the reclaimer: the OOM report, the guard-metadata
// map and the level getter all run on a request path and can be reached from inside a hook (a hook
// that logs its outcome, a protocol that reports its state while it reclaims).
//
// The two failure modes that would follow are deadlock (Stats re-taking a lock the shed path still
// holds) and repeated reclaim (every diagnostic read re-running the hooks of the same transition).
// Both are checked here by executing them, not by reading the lock order.

// TestShedHookCanCallStatsWithoutDeadlockOrReclaimingTwice executes the reentrant call. apply()
// publishes under `access` and then releases it before runShed(), and runShed() copies the hook list
// under `shedAccess` and releases that too, so a hook may call back into the guard. What must not
// happen is the SAME transition reclaiming again because a diagnostic read observed it.
func TestShedHookCanCallStatsWithoutDeadlockOrReclaimingTwice(t *testing.T) {
	var pressure atomic.Uint32
	config := DefaultNetworkExtensionConfig()
	config.DemoteAfter = 5 * time.Millisecond // the demote edge is exercised below
	guard := NewGuard(config, func() Level { return Level(pressure.Load()) })

	var hookCalls atomic.Int32
	var statsFromHook atomic.Uint32
	var levelFromHook atomic.Uint32
	guard.RegisterShed(func() {
		hookCalls.Add(1)
		// The reentrant diagnostic call: this is what an OOM report writer does from inside a
		// reclaimer, and it must neither deadlock nor trigger the hook again.
		stats := guard.Stats()
		statsFromHook.Store(uint32(stats.Level))
		levelFromHook.Store(uint32(guard.Level()))
	})

	pressure.Store(uint32(LevelCritical))
	done := make(chan GuardStats, 1)
	go func() { done <- guard.Stats() }()
	select {
	case stats := <-done:
		require.Equal(t, LevelCritical, stats.Level)
	case <-time.After(5 * time.Second):
		t.Fatal("Stats deadlocked while a shed hook called Stats from inside the reclaim")
	}
	require.Equal(t, int32(1), hookCalls.Load(), "the transition reclaimed more than once")
	require.Equal(t, uint32(LevelCritical), statsFromHook.Load(),
		"the reentrant Stats did not see the level the reclaim belongs to")
	require.Equal(t, uint32(LevelCritical), levelFromHook.Load())

	// Repeated diagnostics at the same level must stay free of reclaim work: a report polled every
	// second is not a reason to run the hooks again.
	for index := 0; index < 8; index++ {
		require.Equal(t, LevelCritical, guard.Stats().Level)
	}
	require.Equal(t, int32(1), hookCalls.Load(), "repeated Stats calls re-ran the shed hooks")

	// A genuinely NEW transition still reclaims exactly once.
	pressure.Store(uint32(LevelNormal))
	require.Eventually(t, func() bool { return guard.Level() == LevelNormal }, time.Second, time.Millisecond)
	pressure.Store(uint32(LevelCritical))
	require.Eventually(t, func() bool { return guard.Level() == LevelCritical }, time.Second, time.Millisecond)
	require.Equal(t, int32(2), hookCalls.Load(), "the second transition did not reclaim exactly once")
}

// TestRepeatedStatsDuringRecoveryDoesNotStallTheQueue is the second half of the pair: a diagnostic
// reader must not be able to hold the transition that hands capacity back. The recovery edge is
// driven here by Stats() alone (no Acquire in the loop) because that is exactly the shape of a
// report-only caller: if a reader could not advance the edge, a process whose load is measured only
// through reports would never demote.
func TestRepeatedStatsDuringRecoveryDoesNotStallTheQueue(t *testing.T) {
	var pressure atomic.Uint32
	pressure.Store(uint32(LevelCritical))
	config := DefaultNetworkExtensionConfig()
	config.DemoteAfter = 5 * time.Millisecond
	guard := NewGuard(config, func() Level { return Level(pressure.Load()) })

	require.Equal(t, LevelCritical, guard.Stats().Level)
	pressure.Store(uint32(LevelNormal))
	require.Eventually(t, func() bool { return guard.Stats().Level == LevelNormal }, 5*time.Second, time.Millisecond,
		"repeated report reads never completed the recovery transition")
	stats := guard.Stats()
	require.Equal(t, 32, stats.Gate.Limit, "the recovered plan is not the normal one")
}
