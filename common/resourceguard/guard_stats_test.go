package resourceguard

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestGuardStatsReportsThePlanTheLevelImplies is the consistency rule: a snapshot must not say
// "critical" while still advertising the normal limit.
//
// The failure this prevents is subtle and expensive to debug from a device report: a reader sees
// level=critical and limit=32 in the same object, concludes admission never tightened, and looks
// for the bug in the wrong subsystem.
func TestGuardStatsReportsThePlanTheLevelImplies(t *testing.T) {
	var level atomic.Uint32
	config := DefaultNetworkExtensionConfig()
	config.DemoteAfter = 10 * time.Millisecond
	guard := NewGuard(config, func() Level { return Level(level.Load()) })

	level.Store(uint32(LevelCritical))
	stats := guard.Stats()
	require.Equal(t, LevelCritical, stats.Level)
	require.Equal(t, 8, stats.Gate.Limit, "the reported limit must be the critical one")
	require.Equal(t, 32, stats.Gate.QueueLimit)
	require.Equal(t, 100*time.Millisecond, stats.Gate.WaitBudget)
	require.Equal(t, uint64(1), stats.Transitions)

	level.Store(uint32(LevelArmed))
	// A fall waits the dwell, so an immediate read still reports critical - and still a critical
	// limit. The two must agree at every instant, including during the dwell.
	stats = guard.Stats()
	require.Equal(t, LevelCritical, stats.Level)
	require.Equal(t, 8, stats.Gate.Limit)

	require.Eventually(t, func() bool { return guard.Stats().Level == LevelArmed }, time.Second, time.Millisecond)
	stats = guard.Stats()
	require.Equal(t, 16, stats.Gate.Limit, "after the dwell the plan must have followed the level")
}

// TestGuardMetadataMapIsNilWithoutGuard pins the zero-cost default for the report: a process with
// no guard contributes nothing, so a non-NetworkExtension report is byte-for-byte what it was.
func TestGuardMetadataMapIsNilWithoutGuard(t *testing.T) {
	var guard *Guard
	require.Equal(t, GuardStats{}, guard.Stats())
	require.Equal(t, LevelNormal, guard.Level())
}
