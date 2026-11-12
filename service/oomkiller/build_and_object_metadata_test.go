package oomkiller

// Q15/Q20 in the report document: the three ledgers must be readable from one entry, and each must
// say what it measures and where it stops being exact.

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/buildgate"
	"github.com/sagernet/sing-box/common/resourceguard"
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
