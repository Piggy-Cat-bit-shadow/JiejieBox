package oomkiller

// Q15/Q20 in the report document: the three ledgers must be readable from one entry, and each must
// say what it measures and where it stops being exact.

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/buildgate"
	"github.com/sagernet/sing-box/common/resourceguard"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// fakeObjectReporter answers both residency interfaces.
type fakeObjectReporter struct {
	adapter.ResidencyReporter
	objects adapter.ResidencyObjectSnapshot
}

func (r fakeObjectReporter) ObjectResidencySnapshot() adapter.ResidencyObjectSnapshot {
	return r.objects
}

// TestObjectResidencyMetadataReportsStatesCoverageAndTermination pins the document shape.
func TestObjectResidencyMetadataReportsStatesCoverageAndTermination(t *testing.T) {
	reporter := fakeObjectReporter{objects: adapter.ResidencyObjectSnapshot{
		Outbounds: adapter.ResidencyObjectCounts{
			Owners:     3,
			Reporting:  2,
			Unknown:    1,
			Estimated:  1,
			Creating:   1,
			Active:     4,
			Idle:       2,
			Draining:   1,
			Terminated: 17,
		},
	}}
	killer := &Service{ctx: service.ContextWith[adapter.ResidencyReporter](context.Background(), reporter)}
	rendered, isMap := killer.objectResidencyMetadata().(map[string]any)
	require.True(t, isMap)
	require.Equal(t, "object-identity", rendered["measure"])

	outbounds, isMap := rendered["outbounds"].(map[string]any)
	require.True(t, isMap)
	require.Equal(t, 3, outbounds["owners"])
	require.Equal(t, 2, outbounds["reportingOwners"])
	require.Equal(t, 1, outbounds["unknownOwners"])
	require.Equal(t, 1, outbounds["estimatedOwners"])
	require.Equal(t, uint64(1), outbounds["creating"])
	require.Equal(t, uint64(4), outbounds["active"])
	require.Equal(t, uint64(2), outbounds["idle"])
	require.Equal(t, uint64(1), outbounds["draining"])
	require.Equal(t, uint64(8), outbounds["liveObjects"], "live must be the four states, not the total")
	require.Equal(t, uint64(17), outbounds["terminated"])

	// The document says what the numbers are, so a reader that finds the key alone cannot read them
	// as the owner-reach counts of the other residency key.
	require.Contains(t, rendered, "unknown")
	require.Contains(t, rendered, "terminated")
}

// TestObjectResidencyMetadataIsAbsentWithoutTheCapability keeps older embedders unchanged.
func TestObjectResidencyMetadataIsAbsentWithoutTheCapability(t *testing.T) {
	require.Nil(t, (&Service{}).objectResidencyMetadata())
	require.Nil(t, (&Service{ctx: context.Background()}).objectResidencyMetadata())

	// The owner-reach-only reporter is the pre-Q15 shape: the key must stay absent rather than
	// appearing with zero objects.
	ownerOnly := &Service{ctx: service.ContextWith[adapter.ResidencyReporter](
		context.Background(), fakeOwnerReach{})}
	require.Nil(t, ownerOnly.objectResidencyMetadata())
}

type fakeOwnerReach struct{}

func (fakeOwnerReach) ResidencySnapshot() adapter.ResidencySnapshot {
	return adapter.ResidencySnapshot{}
}

// TestBuildGateMetadataReportsTheBudgetAndItsBoundary is Q20's diagnostics half.
func TestBuildGateMetadataReportsTheBudgetAndItsBoundary(t *testing.T) {
	gate := buildgate.NewWithPressure(resourceguard.Config{Limit: 2, Queue: 4, Wait: 30e9},
		func() resourceguard.Level { return resourceguard.LevelCritical })
	ctx := buildgate.ContextWithGate(context.Background(), gate)
	killer := &Service{ctx: ctx}

	rendered, isMap := killer.buildGateMetadata().(map[string]any)
	require.True(t, isMap)
	require.Equal(t, "build-count", rendered["measure"])
	require.Equal(t, "critical", rendered["level"])
	require.Equal(t, 1, rendered["limit"], "the applied limit must be the critical derivation")
	require.Equal(t, 1, rendered["maxOverlapping"])
	require.Equal(t, false, rendered["byteBoundKnown"],
		"a byte bound must never be claimed for build work")
	require.NotEmpty(t, rendered["budgetNote"])
	// The three ledgers are distinguishable in one document: admission (resourceGuard), parked
	// builds (this key) and residency (objectResidency).
	require.Contains(t, rendered, "waiters")
	require.Contains(t, rendered, "deferredBuilds")
}

// TestBuildGateMetadataIsAbsentWithoutAGate keeps an embedder's document unchanged.
func TestBuildGateMetadataIsAbsentWithoutAGate(t *testing.T) {
	require.Nil(t, (&Service{}).buildGateMetadata())
	require.Nil(t, (&Service{ctx: context.Background()}).buildGateMetadata())
}

// TestBuildGateMetadataSeparatesTheAdmissionLimitFromTheOverlap is Q20's counterexample at the
// document level: a build admitted under LevelNormal keeps its permit when pressure rises, so the
// document must not report the new admission limit as the overlap.
func TestBuildGateMetadataSeparatesTheAdmissionLimitFromTheOverlap(t *testing.T) {
	level := resourceguard.LevelNormal
	gate := buildgate.NewWithPressure(resourceguard.Config{Limit: 2, Queue: 4, Wait: 30e9},
		func() resourceguard.Level { return level })
	ctx := buildgate.ContextWithGate(context.Background(), gate)
	killer := &Service{ctx: ctx}

	release, err := gate.Acquire(context.Background())
	require.NoError(t, err)
	defer release()
	releaseSecond, err := gate.Acquire(context.Background())
	require.NoError(t, err)
	defer releaseSecond()
	level = resourceguard.LevelCritical

	rendered, isMap := killer.buildGateMetadata().(map[string]any)
	require.True(t, isMap)
	require.Equal(t, 1, rendered["limit"], "the admission limit is what the gate admits now")
	require.Equal(t, 2, rendered["inflightBuilds"], "the already-admitted build keeps its permit")
	require.Equal(t, 2, rendered["maxOverlapping"],
		"the overlap bound must not be the new admission limit while an old build still holds a permit")
	require.Equal(t, "build-count", rendered["measure"])
}

// TestQueueMetadataStatesTheRoundQueueBound is Q08's diagnostics half: the bound, the entries inside
// it and the demand past it are all readable, and the rule is stated in the document.
func TestQueueMetadataStatesTheRoundQueueBound(t *testing.T) {
	coordinator := urltest.NewCoordinator(10)
	ctx := urltest.ContextWithCoordinator(context.Background(), coordinator)
	killer := &Service{ctx: ctx}

	rendered, isMap := killer.queueMetadata().(map[string]any)
	require.True(t, isMap)
	require.Equal(t, "queue", rendered["measure"])
	require.Equal(t, 10, rendered["measurementLimit"])
	require.Equal(t, 2, rendered["roundLimit"])
	require.Equal(t, coordinator.RoundWaiterLimit(), rendered["roundQueueBound"])
	require.NotEmpty(t, rendered["roundQueueRule"])
	require.Contains(t, rendered, "roundsOverflow")
}

// TestQueueMetadataIsAbsentWithoutACoordinator keeps an embedder's document unchanged.
func TestQueueMetadataIsAbsentWithoutACoordinator(t *testing.T) {
	require.Nil(t, (&Service{}).queueMetadata())
	require.Nil(t, (&Service{ctx: context.Background()}).queueMetadata())
}
