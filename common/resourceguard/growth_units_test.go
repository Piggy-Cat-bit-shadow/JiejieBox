package resourceguard

import (
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing-box/common/trafficclass"
	"github.com/stretchr/testify/require"
)

// The growth budget is charged in GROWN-BUFFER UNITS, not directions: the copy
// engine's vectorised path holds DefaultBatchSize buffers per direction, and the
// budget has to charge what the direction actually holds or its documented byte cap
// is 8x too small. P2-01 measured 8 x 65 535 B held per vectorised direction on the
// production copy call.

func TestGrowthBudgetChargesEveryGrownBuffer(t *testing.T) {
	var normal atomic.Uint32
	budget := newTestGrowthBudget(16, 0, &normal)

	threshold, first := budget.AdmitBuffers(trafficclass.ClassBulk, false, 8)
	require.Equal(t, CopyGrowthDefault, threshold)
	require.NotNil(t, first)

	_, second := budget.AdmitBuffers(trafficclass.ClassBulk, false, 8)
	require.NotNil(t, second, "two vectorised directions fit in 16 units")

	refusedThreshold, third := budget.AdmitBuffers(trafficclass.ClassBulk, false, 1)
	require.Equal(t, CopyGrowthNever, refusedThreshold, "the unit cap is reached")
	require.Nil(t, third)

	stats := budget.Stats()
	require.Equal(t, 16, stats.InUse, "InUse counts units, not directions")
	require.Equal(t, 16, stats.Peak)
	require.Equal(t, uint64(2), stats.Granted)
	require.Equal(t, uint64(1), stats.Refused)

	first.Release()
	stats = budget.Stats()
	require.Equal(t, 8, stats.InUse, "releasing a vectorised permit returns all 8 units")

	_, fourth := budget.AdmitBuffers(trafficclass.ClassBulk, false, 8)
	require.NotNil(t, fourth, "the released units are usable again")
}

// A weighted request is admitted whole or not at all: it never takes a partial charge
// and then grows into buffers it did not pay for.
func TestGrowthBudgetNeverAdmitsAPartialCharge(t *testing.T) {
	var normal atomic.Uint32
	budget := newTestGrowthBudget(12, 0, &normal)
	_, big := budget.AdmitBuffers(trafficclass.ClassBulk, false, 8)
	require.NotNil(t, big)

	threshold, second := budget.AdmitBuffers(trafficclass.ClassBulk, false, 8)
	require.Equal(t, CopyGrowthNever, threshold)
	require.Nil(t, second, "4 free units must not admit an 8-unit direction")

	stats := budget.Stats()
	require.Equal(t, 8, stats.InUse, "a refusal changes no accounting")
	require.Equal(t, uint64(1), stats.Refused)
}

// The interactive reserve is expressed in the same units.
func TestGrowthBudgetReserveIsExpressedInUnits(t *testing.T) {
	var level atomic.Uint32
	budget := newTestGrowthBudget(10, 4, &level)

	_, bulk := budget.AdmitBuffers(trafficclass.ClassBulk, false, 8)
	require.NotNil(t, bulk)

	_, reserved := budget.AdmitBuffers(trafficclass.ClassInteractive, false, 3)
	require.NotNil(t, reserved, "3 units fit in the 4-unit reserve")
	require.Equal(t, 3, budget.Stats().ReservedInUse)

	threshold, overReserve := budget.AdmitBuffers(trafficclass.ClassInteractive, false, 2)
	require.Equal(t, CopyGrowthNever, threshold)
	require.Nil(t, overReserve, "the reserve is 4 units and 3 are already out")

	// The ordinary cap alone cannot absorb a high-priority request either.
	threshold, bulkOverCap := budget.AdmitBuffers(trafficclass.ClassBulk, false, 1)
	require.Equal(t, CopyGrowthNever, threshold)
	require.Nil(t, bulkOverCap)

	stats := budget.Stats()
	require.Equal(t, 11, stats.InUse)
	require.Equal(t, 3, stats.ReservedInUse)
}

// The seam the copy engine actually calls must carry the same weight.
func TestGrowthBoundaryBuffersAdmitsTheSameUnits(t *testing.T) {
	var normal atomic.Uint32
	budget := newTestGrowthBudget(8, 0, &normal)
	seam := budget.GrowthBoundaryBuffers(trafficclass.ClassBulk, false, 8)
	require.NotNil(t, seam)

	release, granted := seam()
	require.True(t, granted)
	require.NotNil(t, release)
	require.Equal(t, 8, budget.Stats().InUse)

	_, refused := budget.GrowthBoundaryBuffers(trafficclass.ClassBulk, false, 1)()
	require.False(t, refused, "the single unit left is not enough for another direction")

	release()
	require.Equal(t, 0, budget.Stats().InUse)
}

// A plain direction keeps the old price, so the TLS/mux path is not restricted by the
// vectorised geometry.
func TestGrowthPlainDirectionsKeepTheirOldPrice(t *testing.T) {
	var normal atomic.Uint32
	budget := newTestGrowthBudget(256, 16, &normal)
	for range 256 {
		_, permit := budget.AdmitBuffers(trafficclass.ClassBulk, false, 1)
		require.NotNil(t, permit)
	}
	threshold, refused := budget.AdmitBuffers(trafficclass.ClassBulk, false, 1)
	require.Equal(t, CopyGrowthNever, threshold)
	require.Nil(t, refused)
	require.Equal(t, 256, budget.Stats().InUse)
}
