package oomkiller

import (
	"testing"

	"github.com/sagernet/sing-box/common/resourceguard"
	"github.com/sagernet/sing-tun"
	"github.com/stretchr/testify/require"
)

// TestGuardLevelMapsEveryPublishedPressure pins the only mapping between the pressure the timer
// publishes and the level admission reacts to. It is a total function on purpose: an unknown
// value must read as normal rather than as pressure, because refusing traffic on an unrecognised
// state would turn a wiring mistake into an outage.
func TestGuardLevelMapsEveryPublishedPressure(t *testing.T) {
	service := &Service{}
	cases := []struct {
		pressure tun.MemoryPressure
		expected resourceguard.Level
	}{
		{tun.MemoryPressureNone, resourceguard.LevelNormal},
		{tun.MemoryPressureWarning, resourceguard.LevelArmed},
		{tun.MemoryPressureCritical, resourceguard.LevelCritical},
		{tun.MemoryPressure(200), resourceguard.LevelNormal},
	}
	for _, testCase := range cases {
		service.pressure.Store(uint32(testCase.pressure))
		require.Equal(t, testCase.expected, service.guardLevel(),
			"pressure %d must map to %v", testCase.pressure, testCase.expected)
	}
}

// TestGuardSurfacesAreNilSafeWithoutAGuard is the non-NetworkExtension contract: no guard means
// no counters and no metadata key, so a desktop report is byte-for-byte what it was before.
func TestGuardSurfacesAreNilSafeWithoutAGuard(t *testing.T) {
	service := &Service{}
	require.Equal(t, resourceguard.GuardStats{}, service.GuardStats())
	require.Nil(t, service.guardMetadata())
}

// TestGuardMetadataDescribesTheLevelAndPlan proves the report key carries the numbers a reader
// needs to tell "the governor installed" from "the governor acted": the level, the limit that
// level implies, the queue depth and every outcome bucket.
func TestGuardMetadataDescribesTheLevelAndPlan(t *testing.T) {
	var pressure uint32
	service := &Service{}
	service.guard = resourceguard.NewGuard(resourceguard.DefaultNetworkExtensionConfig(), func() resourceguard.Level {
		return service.guardLevel()
	})
	service.pressure.Store(pressure)

	metadata, ok := service.guardMetadata().(map[string]any)
	require.True(t, ok)
	require.Equal(t, "normal", metadata["level"])
	require.Equal(t, 32, metadata["limit"])
	require.Equal(t, 256, metadata["queueLimit"])
	for _, key := range []string{
		"transitions", "waitBudgetMs", "inflight", "waiters", "maxInflight",
		"requested", "admitted", "queued", "refused", "timedOut", "canceled", "released",
	} {
		require.Contains(t, metadata, key, "the report must carry %s", key)
	}

	// A critical process reports the plan its level implies, in the same object.
	service.pressure.Store(uint32(tun.MemoryPressureCritical))
	metadata, ok = service.guardMetadata().(map[string]any)
	require.True(t, ok)
	require.Equal(t, "critical", metadata["level"])
	require.Equal(t, 8, metadata["limit"], "the reported limit must follow the reported level")
}
