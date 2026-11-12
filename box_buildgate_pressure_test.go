package box

// Q20 at the box boundary: the reservation is shaped by the process's ONE pressure fact.
//
// common/buildgate proves the derivation itself. What this file proves is the WIRING, which is the
// half that can silently rot: the gate the box installs must read the guard the pressure policy
// owner registers (service/oomkiller registers it while the box starts, AFTER this gate is
// created), must not invent pressure when no guard exists, and must expose the level it applied.

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/buildgate"
	"github.com/sagernet/sing-box/common/resourceguard"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// pressureSource is a registered guard whose level the test drives.
type pressureSource struct {
	level atomic.Uint32
}

func (p *pressureSource) set(level resourceguard.Level) { p.level.Store(uint32(level)) }
func (p *pressureSource) get() resourceguard.Level      { return resourceguard.Level(p.level.Load()) }

// registryContext is the production shape: one service registry for the whole box. Every later
// `service.MustRegister` on a context derived from it is visible to the earlier contexts too,
// because the registry is a pointer stored in the context.
func registryContext() context.Context {
	return service.ContextWithDefaultRegistry(context.Background())
}

// registerPressureGuard registers a guard on the shared registry, exactly as the OOM service does
// while the box starts.
func registerPressureGuard(ctx context.Context) *pressureSource {
	source := &pressureSource{}
	guard := resourceguard.NewGuard(resourceguard.Config{Limit: 2, Queue: 4, Wait: time.Second}, source.get)
	service.MustRegister[*resourceguard.Guard](ctx, guard)
	return source
}

// TestTheBoxBuildGateFollowsTheRegisteredPressureGuard is the wiring proof.
//
// The gate is created BEFORE the guard exists in a real box (withBuildGate runs at construction,
// the OOM service registers its guard while starting), so the pressure function must look the guard
// up at acquisition time. The test therefore creates the gate first and registers the guard
// afterwards - reversing that order would pass even with a snapshot taken at construction.
func TestTheBoxBuildGateFollowsTheRegisteredPressureGuard(t *testing.T) {
	originalLimit, originalQueue, originalWait := buildGateLimit, buildGateQueue, buildGateWait
	buildGateLimit, buildGateQueue, buildGateWait = 2, 4, time.Second
	t.Cleanup(func() {
		buildGateLimit, buildGateQueue, buildGateWait = originalLimit, originalQueue, originalWait
	})

	base := registryContext()
	// The box installs the gate at construction time, when no pressure policy exists yet.
	ctx := withBuildGate(base)
	require.NotNil(t, buildgate.FromContext(ctx))

	// A build with no pressure policy: the flat reservation, both slots admitted.
	firstRelease, err := buildgate.FromContext(ctx).Acquire(context.Background())
	require.NoError(t, err)
	secondRelease, err := buildgate.FromContext(ctx).Acquire(context.Background())
	require.NoError(t, err)
	firstRelease()
	secondRelease()
	require.Equal(t, resourceguard.LevelNormal, buildgate.FromContext(ctx).Level(),
		"a box with no pressure policy must read normal, not invent pressure")

	// The policy owner registers its guard - AFTER the gate was built - on the shared registry.
	source := registerPressureGuard(base)
	source.set(resourceguard.LevelCritical)

	// The function the box installed reads the guard through the registry the two contexts share,
	// even though the gate captured the context before the registration existed.
	require.Equal(t, resourceguard.LevelCritical, buildPressure(ctx)(),
		"the installed pressure function does not see the guard the policy owner registered")
	require.Equal(t, resourceguard.LevelCritical, buildgate.FromContext(ctx).Level(),
		"the reservation did not adopt the level the process reported")
}

// TestTheBuildPressureFunctionIsLazy proves the lookup happens at acquisition time rather than at
// construction: the guard is registered after buildPressure was created, and the function still
// sees it. That ordering is the production one, and a snapshot would fail here.
func TestTheBuildPressureFunctionIsLazy(t *testing.T) {
	base := registryContext()
	pressure := buildPressure(base)
	require.Equal(t, resourceguard.LevelNormal, pressure())

	source := registerPressureGuard(base)
	source.set(resourceguard.LevelArmed)
	require.Equal(t, resourceguard.LevelArmed, pressure())

	// A different process-wide context has its own registry and its own answer: two contexts cannot
	// share one pressure source by accident.
	require.Equal(t, resourceguard.LevelNormal, buildPressure(registryContext())())
}

// TestTheInstalledGateDefersOnlyAtTheDerivedLimit closes the loop: the level read above actually
// changes admission, which is what "the slots follow pressure" has to mean.
func TestTheInstalledGateDefersOnlyAtTheDerivedLimit(t *testing.T) {
	originalLimit, originalQueue, originalWait := buildGateLimit, buildGateQueue, buildGateWait
	buildGateLimit, buildGateQueue, buildGateWait = 2, 0, 20*time.Millisecond
	t.Cleanup(func() {
		buildGateLimit, buildGateQueue, buildGateWait = originalLimit, originalQueue, originalWait
	})

	base := registryContext()
	ctx := withBuildGate(base)
	gate := buildgate.FromContext(ctx)
	source := registerPressureGuard(base)

	// Normal: two concurrent builds, the third deferred (queue is zero in this fixture).
	firstRelease, err := gate.Acquire(context.Background())
	require.NoError(t, err)
	secondRelease, err := gate.Acquire(context.Background())
	require.NoError(t, err)
	_, err = gate.Acquire(context.Background())
	require.ErrorIs(t, err, buildgate.ErrDeferred)

	// Critical: the derived limit is one build, so a second one is deferred EVEN THOUGH the normal
	// capacity is still free - the slot count is what pressure changes.
	source.set(resourceguard.LevelCritical)
	require.Equal(t, resourceguard.LevelCritical, gate.Level())
	firstRelease()
	secondRelease()
	criticalRelease, err := gate.Acquire(context.Background())
	require.NoError(t, err, "one build is still allowed at critical pressure")
	_, err = gate.Acquire(context.Background())
	require.ErrorIs(t, err, buildgate.ErrDeferred, "critical pressure must not admit two builds")
	criticalRelease()
}
