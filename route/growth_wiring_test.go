package route

// The relay copy-growth budget's wiring: the manager installs one budget when it
// starts, both copy directions resolve their threshold through it, and the permit is
// released when the direction's copy returns.
//
// The budget itself (common/resourceguard/growth.go) is tested at its own boundary.
// What these tests pin is the part that only the route layer can get wrong: that the
// production manager actually installs it, that the pressure source is the process
// guard when one exists, that a granted direction keeps the pre-existing thresholds,
// and that neither direction forgot to consult the budget or to release its permit.

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/resourceguard"
	"github.com/sagernet/sing-box/common/trafficclass"
	"github.com/sagernet/sing-box/log"

	"github.com/stretchr/testify/require"
)

// TestConnectionManagerInstallsTheGrowthBudget proves the production manager gets a
// budget on start, at the default policy and without a guard (LevelNormal). A manager
// that never started keeps nil and the pre-existing behaviour, which is what every
// unit test that constructs one by hand relies on.
func TestConnectionManagerInstallsTheGrowthBudget(t *testing.T) {
	manager := NewConnectionManager(log.NewNOPFactory().Logger())
	require.Nil(t, manager.growth, "a manager that has not started must not carry a budget")

	scope := adapter.NewScope(context.Background(), log.NewNOPFactory().Logger())
	require.NoError(t, manager.Start(adapter.StartStateInitialize, scope))
	require.NotNil(t, manager.growth)

	stats := manager.growth.Stats()
	require.Equal(t, resourceguard.LevelNormal, stats.Level)
	require.Equal(t, resourceguard.DefaultGrowthConfig().Flows, stats.Flows)
	require.Equal(t, resourceguard.DefaultGrowthConfig().InteractiveReserve, stats.Reserve)
}

// TestConnectionManagerGrowthBudgetUsesTheGuardPressure pins the pressure linkage: the
// budget reads the same guard the expensive-creation admission uses, so the two cannot
// disagree about the level. With no guard the budget would stay at LevelNormal and the
// manager would silently stop tightening under pressure.
func TestConnectionManagerGrowthBudgetUsesTheGuardPressure(t *testing.T) {
	var level atomic.Uint32
	config := resourceguard.DefaultNetworkExtensionConfig()
	// The guard keeps a level for DemoteAfter before it falls; shave it so the test does
	// not sleep out the production dwell.
	config.DemoteAfter = time.Millisecond
	guard := resourceguard.NewGuard(config, func() resourceguard.Level {
		return resourceguard.Level(level.Load())
	})
	level.Store(uint32(resourceguard.LevelCritical))
	ctx := resourceguard.NewContext(context.Background(), guard)
	manager := NewConnectionManager(log.NewNOPFactory().Logger())
	scope := adapter.NewScope(ctx, log.NewNOPFactory().Logger())
	require.NoError(t, manager.Start(adapter.StartStateInitialize, scope))
	require.NotNil(t, manager.growth)
	require.Equal(t, resourceguard.LevelCritical, manager.growth.Stats().Level)

	// At Critical the growth policy admits no new ordinary direction.
	threshold, permit := manager.growthThreshold(trafficclass.ClassBulk, nil, untunedConn{}, untunedConn{})
	require.Equal(t, resourceguard.CopyGrowthNever, threshold)
	require.Nil(t, permit)
	require.NotPanics(t, permit.Release)

	level.Store(uint32(resourceguard.LevelNormal))
	require.Eventually(t, func() bool {
		return manager.growth.Stats().Level == resourceguard.LevelNormal
	}, time.Second, time.Millisecond, "the budget must follow the guard down after its dwell")
	threshold, permit = manager.growthThreshold(trafficclass.ClassBulk, nil, untunedConn{}, untunedConn{})
	require.Equal(t, resourceguard.CopyGrowthDefault, threshold)
	require.NotNil(t, permit)
	permit.Release()
}

// TestConnectionManagerGrowthThresholdKeepsPerFlowDecisions is the compatibility half:
// a granted direction receives exactly the threshold connectionIncreaseBufferAfter
// answered, the budget is consulted per direction (so the second direction can be
// refused without touching the first), and a geometry-required writer keeps its early
// growth without consuming a shared slot.
func TestConnectionManagerGrowthThresholdKeepsPerFlowDecisions(t *testing.T) {
	manager := NewConnectionManager(log.NewNOPFactory().Logger())
	manager.growth = resourceguard.NewGrowthBudget(resourceguard.GrowthConfig{Flows: 1}, nil)

	firstThreshold, firstPermit := manager.growthThreshold(trafficclass.ClassDefault, nil, untunedConn{}, untunedConn{})
	require.Equal(t, int64(1)*resourceguard.CopyGrowthDefault, firstThreshold)
	require.NotNil(t, firstPermit)

	// One slot: the other direction of the same connection is refused, which is the
	// bound doing its job rather than an inconsistency between the two directions.
	secondThreshold, secondPermit := manager.growthThreshold(trafficclass.ClassDefault, nil, untunedConn{}, untunedConn{})
	require.Equal(t, resourceguard.CopyGrowthNever, secondThreshold)
	require.Nil(t, secondPermit)

	// A writer whose framing needs the larger buffer keeps it and takes no slot.
	geometryThreshold, geometryPermit := manager.growthThreshold(trafficclass.ClassDefault, nil, fakeTunedConn{early: true}, untunedConn{})
	require.Equal(t, resourceguard.CopyGrowthImmediate, geometryThreshold)
	require.Nil(t, geometryPermit)

	firstPermit.Release()
	stats := manager.growth.Stats()
	require.Equal(t, 0, stats.InUse)
	require.Equal(t, uint64(1), stats.Granted)
	require.Equal(t, uint64(1), stats.Refused)
	require.Equal(t, uint64(1), stats.GeometryNeeded)
	require.Equal(t, stats.Granted, stats.Released)
	require.Equal(t, uint64(0), stats.DoubleReleases)
}

// TestBothCopyDirectionsConsumeAndReleaseTheBudget is the structural guard for the
// call site: the two directions of a connection are independent permits, each must be
// resolved through the budget, and each must be released in the goroutine that owns
// that direction's copy. A future edit that drops one of the four statements would
// leave the pool with a permit nobody returns, and this fails first.
func TestBothCopyDirectionsConsumeAndReleaseTheBudget(t *testing.T) {
	content, err := os.ReadFile("conn.go")
	require.NoError(t, err)
	source := string(content)

	require.Contains(t, source, "uploadIncreaseBufferAfter, uploadGrowthPermit := m.growthThreshold(metadata.TrafficClass, this, conn, remoteConn)")
	require.Contains(t, source, "downloadIncreaseBufferAfter, downloadGrowthPermit := m.growthThreshold(metadata.TrafficClass, this, remoteConn, conn)")
	require.Contains(t, source, "defer uploadGrowthPermit.Release()")
	require.Contains(t, source, "defer downloadGrowthPermit.Release()")
	require.Contains(t, source, "m.growth = resourceguard.NewGrowthBudget(resourceguard.DefaultGrowthConfig(), pressure)")
}
