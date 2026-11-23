package route

// The relay copy-growth budget's wiring: the manager installs one budget when it
// starts, both copy directions resolve their threshold and growth seam through it, and
// the permit is acquired and released at the actual growth boundary by the copy engine.
//
// The budget itself (common/resourceguard/growth.go) is tested at its own boundary.
// What these tests pin is the part that only the route layer can get wrong: that the
// production manager actually installs it, that the pressure source is the process
// guard when one exists, that a granted direction keeps the pre-existing thresholds,
// and that neither direction forgets to hand the seam to its copy. The seam is not an
// acquired permit: resolving it must not consume capacity (see
// route/conn_growth_seam_test.go for the end-to-end quiet-flow regression).

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

	// Resolving the seam does not consume capacity; asking it at the growth boundary
	// under Critical is what refuses.
	threshold, seam := manager.growthThreshold(trafficclass.ClassBulk, nil, untunedConn{}, untunedConn{})
	require.Equal(t, resourceguard.CopyGrowthDefault, threshold)
	require.NotNil(t, seam)
	release, granted := seam()
	require.False(t, granted, "Critical must refuse an ordinary direction at the growth boundary")
	require.Nil(t, release)
	require.Zero(t, manager.growth.Stats().InUse)

	level.Store(uint32(resourceguard.LevelNormal))
	require.Eventually(t, func() bool {
		return manager.growth.Stats().Level == resourceguard.LevelNormal
	}, time.Second, time.Millisecond, "the budget must follow the guard down after its dwell")
	threshold, seam = manager.growthThreshold(trafficclass.ClassBulk, nil, untunedConn{}, untunedConn{})
	require.Equal(t, resourceguard.CopyGrowthDefault, threshold)
	require.NotNil(t, seam)
	release, granted = seam()
	require.True(t, granted)
	require.NotNil(t, release)
	require.Equal(t, 1, manager.growth.Stats().InUse)
	release()
	require.Zero(t, manager.growth.Stats().InUse)
}

// TestConnectionManagerGrowthThresholdKeepsPerFlowDecisions is the compatibility half:
// a direction receives exactly the threshold connectionIncreaseBufferAfter answered,
// the seam is consulted per direction (so the second growth can be refused without
// touching the first), resolving a seam consumes nothing, and a geometry-required
// writer keeps its early growth without consuming a shared slot.
func TestConnectionManagerGrowthThresholdKeepsPerFlowDecisions(t *testing.T) {
	manager := NewConnectionManager(log.NewNOPFactory().Logger())
	manager.growth = resourceguard.NewGrowthBudget(resourceguard.GrowthConfig{Flows: 1}, nil)

	firstThreshold, firstSeam := manager.growthThreshold(trafficclass.ClassDefault, nil, untunedConn{}, untunedConn{})
	require.Equal(t, int64(1)*resourceguard.CopyGrowthDefault, firstThreshold)
	require.NotNil(t, firstSeam)
	require.Zero(t, manager.growth.Stats().InUse,
		"resolving a direction's seam must not consume a permit; only the growth boundary may")

	secondThreshold, secondSeam := manager.growthThreshold(trafficclass.ClassDefault, nil, untunedConn{}, untunedConn{})
	require.Equal(t, resourceguard.CopyGrowthDefault, secondThreshold)
	require.NotNil(t, secondSeam)

	// The first direction actually grows: it takes the one slot.
	firstRelease, firstGranted := firstSeam()
	require.True(t, firstGranted)
	require.NotNil(t, firstRelease)
	require.Equal(t, 1, manager.growth.Stats().InUse)

	// One slot: the other direction of the same connection is refused when it reaches
	// its own growth boundary, which is the bound doing its job rather than an
	// inconsistency between the two directions.
	secondRelease, secondGranted := secondSeam()
	require.False(t, secondGranted)
	require.Nil(t, secondRelease)

	// A writer whose framing needs the larger buffer keeps it and takes no slot.
	geometryThreshold, geometrySeam := manager.growthThreshold(trafficclass.ClassDefault, nil, fakeTunedConn{early: true}, untunedConn{})
	require.Equal(t, resourceguard.CopyGrowthImmediate, geometryThreshold)
	require.NotNil(t, geometrySeam)
	geometryRelease, geometryGranted := geometrySeam()
	require.True(t, geometryGranted, "geometry-required growth must never be refused by the ordinary cap")
	require.Nil(t, geometryRelease)
	require.Equal(t, 1, manager.growth.Stats().InUse, "a geometry direction must not charge the shared cap")

	firstRelease()
	stats := manager.growth.Stats()
	require.Equal(t, 0, stats.InUse)
	require.Equal(t, uint64(1), stats.Granted)
	require.Equal(t, uint64(1), stats.Refused)
	require.Equal(t, uint64(1), stats.GeometryNeeded)
	require.Equal(t, stats.Granted, stats.Released)
	require.Equal(t, uint64(0), stats.DoubleReleases)
}

// TestBothCopyDirectionsHandTheirSeamsToTheCopy is the structural guard for the call
// site: the two directions of a connection are independent, each must resolve its own
// threshold and seam, and each must hand its seam to the copy engine that owns the
// release. A future edit that drops one of the statements would leave one direction
// ungated (or, worse, acquiring a permit nobody releases), and this fails first.
func TestBothCopyDirectionsHandTheirSeamsToTheCopy(t *testing.T) {
	content, err := os.ReadFile("conn.go")
	require.NoError(t, err)
	source := string(content)

	require.Contains(t, source, "uploadIncreaseBufferAfter, uploadGrowthBoundary := m.growthThreshold(metadata.TrafficClass, this, conn, remoteConn)")
	require.Contains(t, source, "downloadIncreaseBufferAfter, downloadGrowthBoundary := m.growthThreshold(metadata.TrafficClass, this, remoteConn, conn)")
	require.Contains(t, source, "teardown, uploadGrowthBoundary)")
	require.Contains(t, source, "teardown, downloadGrowthBoundary)")
	require.NotContains(t, source, "defer uploadGrowthBoundary")
	require.NotContains(t, source, "defer downloadGrowthBoundary")
	require.Contains(t, source, "m.growth = resourceguard.NewGrowthBudget(resourceguard.DefaultGrowthConfig(), pressure)")
}
