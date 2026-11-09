package resourceguard

import (
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/trafficclass"

	"github.com/stretchr/testify/require"
)

// The relay copy-growth budget, tested at its own boundary: the wiring into
// route/conn.go belongs to the network window (see the wiring note on
// GrowthBudget), so these tests pin the policy the wiring will call.

func newTestGrowthBudget(flows, reserve int, level *atomic.Uint32) *GrowthBudget {
	return NewGrowthBudget(
		GrowthConfig{Flows: flows, InteractiveReserve: reserve},
		func() Level { return Level(level.Load()) },
	)
}

// TestNilGrowthBudgetKeepsTodaysThresholds pins the default: a Box without a
// budget must behave exactly as it did before the budget existed, so the route
// wiring can call Admit unconditionally.
func TestNilGrowthBudgetKeepsTodaysThresholds(t *testing.T) {
	var budget *GrowthBudget
	threshold, permit := budget.Admit(trafficclass.ClassDefault, false)
	require.Equal(t, CopyGrowthDefault, threshold)
	require.Nil(t, permit)
	require.NotPanics(t, permit.Release)

	threshold, permit = budget.Admit(trafficclass.ClassBulk, true)
	require.Equal(t, CopyGrowthImmediate, threshold)
	require.Nil(t, permit)

	require.Equal(t, GrowthStats{}, budget.Stats())
}

// TestGrowthBudgetCapsGrownDirections is the shared quota itself: once the cap is
// reached a direction keeps the small buffer, and finishing a direction returns its
// slot.
func TestGrowthBudgetCapsGrownDirections(t *testing.T) {
	var level atomic.Uint32
	budget := newTestGrowthBudget(2, 0, &level)

	firstThreshold, first := budget.Admit(trafficclass.ClassBulk, false)
	secondThreshold, second := budget.Admit(trafficclass.ClassBulk, false)
	require.Equal(t, CopyGrowthDefault, firstThreshold)
	require.Equal(t, CopyGrowthDefault, secondThreshold)
	require.NotNil(t, first)
	require.NotNil(t, second)

	refusedThreshold, refused := budget.Admit(trafficclass.ClassBulk, false)
	require.Equal(t, CopyGrowthNever, refusedThreshold, "a refused direction must not grow at all")
	require.Nil(t, refused, "a refusal hands out no permit")

	stats := budget.Stats()
	require.Equal(t, 2, stats.InUse)
	require.Equal(t, uint64(2), stats.Granted)
	require.Equal(t, uint64(1), stats.Refused)
	require.Equal(t, 2, stats.Flows)

	first.Release()
	stats = budget.Stats()
	require.Equal(t, 1, stats.InUse)
	require.Equal(t, uint64(1), stats.Released)

	thirdThreshold, third := budget.Admit(trafficclass.ClassBulk, false)
	require.Equal(t, CopyGrowthDefault, thirdThreshold)
	require.NotNil(t, third)

	// Releasing a permit twice is a defect, not a second slot.
	first.Release()
	stats = budget.Stats()
	require.Equal(t, uint64(1), stats.DoubleReleases)
	require.Equal(t, 2, stats.InUse)
	require.Equal(t, uint64(2), stats.Granted-stats.Released)
	second.Release()
	third.Release()
	require.Equal(t, 0, budget.Stats().InUse)
}

// TestGrowthBudgetReserveIsForHighPriorityOnly pins the short-flow fairness
// mechanism: a bulk burst cannot take the last slots an interactive flow needs, and
// the reserve is not a second pool for bulk traffic.
func TestGrowthBudgetReserveIsForHighPriorityOnly(t *testing.T) {
	var level atomic.Uint32
	budget := newTestGrowthBudget(2, 1, &level)

	bulkOne, bulkOnePermit := budget.Admit(trafficclass.ClassBulk, false)
	bulkTwo, bulkTwoPermit := budget.Admit(trafficclass.ClassBulk, false)
	require.Equal(t, CopyGrowthDefault, bulkOne)
	require.Equal(t, CopyGrowthDefault, bulkTwo)
	require.NotNil(t, bulkOnePermit)
	require.NotNil(t, bulkTwoPermit)

	// The ordinary cap is reached. A bulk flow may not touch the reserve...
	threshold, permit := budget.Admit(trafficclass.ClassBulk, false)
	require.Equal(t, CopyGrowthNever, threshold)
	require.Nil(t, permit)

	// ...but an interactive flow may.
	interactive, interactivePermit := budget.Admit(trafficclass.ClassInteractive, false)
	require.Equal(t, CopyGrowthDefault, interactive)
	require.NotNil(t, interactivePermit)
	stats := budget.Stats()
	require.Equal(t, 3, stats.InUse)
	require.Equal(t, 1, stats.ReservedInUse)

	// The reserve is bounded too: the next interactive flow is refused.
	threshold, permit = budget.Admit(trafficclass.ClassRealtime, false)
	require.Equal(t, CopyGrowthNever, threshold)
	require.Nil(t, permit)

	// Releasing the reserve-backed permit alone leaves the ordinary cap reached;
	// the slot returns to bulk traffic once a bulk permit is released too.
	interactivePermit.Release()
	stats = budget.Stats()
	require.Equal(t, 0, stats.ReservedInUse)
	require.Equal(t, 2, stats.InUse)
	threshold, permit = budget.Admit(trafficclass.ClassBulk, false)
	require.Equal(t, CopyGrowthNever, threshold)
	require.Nil(t, permit)
	bulkOnePermit.Release()
	threshold, permit = budget.Admit(trafficclass.ClassBulk, false)
	require.Equal(t, CopyGrowthDefault, threshold)
	require.NotNil(t, permit)
	permit.Release()
	bulkTwoPermit.Release()
	require.Equal(t, 0, budget.Stats().InUse)
}

// TestGrowthBudgetTightensWithPressure pins the pressure linkage: a rising level
// stops NEW growth without revoking what is already grown (the buffer exists; only
// the count of future ones can be bounded), and a falling level re-opens growth.
func TestGrowthBudgetTightensWithPressure(t *testing.T) {
	var level atomic.Uint32
	budget := newTestGrowthBudget(4, 2, &level)

	permits := make([]*GrowthPermit, 0, 4)
	for index := 0; index < 4; index++ {
		threshold, permit := budget.Admit(trafficclass.ClassBulk, false)
		require.Equal(t, CopyGrowthDefault, threshold)
		require.NotNil(t, permit)
		permits = append(permits, permit)
	}
	threshold, permit := budget.Admit(trafficclass.ClassBulk, false)
	require.Equal(t, CopyGrowthNever, threshold)
	require.Nil(t, permit)

	// Armed halves the cap (2). The four permits already out are still counted:
	// their buffers are already grown and pretending otherwise would make the
	// bound untrue.
	level.Store(uint32(LevelArmed))
	stats := budget.Stats()
	require.Equal(t, LevelArmed, stats.Level)
	require.Equal(t, 2, stats.Flows)
	require.Equal(t, 1, stats.Reserve)
	require.Equal(t, 4, stats.InUse)

	threshold, permit = budget.Admit(trafficclass.ClassBulk, false)
	require.Equal(t, CopyGrowthNever, threshold, "a tightened cap must refuse new growth")
	require.Nil(t, permit)

	permits[0].Release()
	permits[1].Release()
	threshold, permit = budget.Admit(trafficclass.ClassBulk, false)
	require.Equal(t, CopyGrowthNever, threshold, "two permits are still out, which is the Armed cap")
	require.Nil(t, permit)

	permits[2].Release()
	threshold, permit = budget.Admit(trafficclass.ClassBulk, false)
	require.Equal(t, CopyGrowthDefault, threshold)
	require.NotNil(t, permit)
	permits[0] = permit
	threshold, permit = budget.Admit(trafficclass.ClassBulk, false)
	require.Equal(t, CopyGrowthNever, threshold, "the Armed cap is reached again")
	require.Nil(t, permit)

	// Critical admits no new growth at all, not even from the reserve.
	level.Store(uint32(LevelCritical))
	stats = budget.Stats()
	require.Equal(t, 0, stats.Flows)
	require.Equal(t, 0, stats.Reserve)
	threshold, permit = budget.Admit(trafficclass.ClassInteractive, false)
	require.Equal(t, CopyGrowthNever, threshold)
	require.Nil(t, permit)

	// Recovery re-opens after the outstanding permits are released.
	level.Store(uint32(LevelNormal))
	permits[3].Release()
	require.Equal(t, 1, budget.Stats().InUse)
	threshold, permit = budget.Admit(trafficclass.ClassBulk, false)
	require.Equal(t, CopyGrowthDefault, threshold)
	require.NotNil(t, permit)
	permit.Release()
	permits[0].Release()
	require.Equal(t, 0, budget.Stats().InUse)
}

// TestGrowthBudgetDerivationNeverInverts walks the config space the way
// Config.planFor's test does: a degenerate small config must not admit MORE at a
// higher level.
func TestGrowthBudgetDerivationNeverInverts(t *testing.T) {
	for flows := 0; flows <= 8; flows++ {
		for reserve := 0; reserve <= 4; reserve++ {
			config := GrowthConfig{Flows: flows, InteractiveReserve: reserve}
			normal := config.growthPlanFor(LevelNormal)
			armed := config.growthPlanFor(LevelArmed)
			critical := config.growthPlanFor(LevelCritical)
			require.GreaterOrEqual(t, normal.flows, armed.flows, "flows=%d reserve=%d", flows, reserve)
			require.GreaterOrEqual(t, armed.flows, critical.flows, "flows=%d reserve=%d", flows, reserve)
			require.GreaterOrEqual(t, normal.reserve, armed.reserve, "flows=%d reserve=%d", flows, reserve)
			require.GreaterOrEqual(t, armed.reserve, critical.reserve, "flows=%d reserve=%d", flows, reserve)
			require.LessOrEqual(t, normal.reserve, normal.flows, "the reserve may not exceed the ordinary cap")
			require.GreaterOrEqual(t, critical.flows, 0)
			require.GreaterOrEqual(t, critical.reserve, 0)
		}
	}
	// A single-slot config still admits its one flow under Armed: the floor is
	// clamped so tightening cannot close everything below the configured limit.
	require.Equal(t, 1, GrowthConfig{Flows: 1}.growthPlanFor(LevelArmed).flows)
	require.Equal(t, 0, GrowthConfig{}.growthPlanFor(LevelCritical).flows)
}

// TestGrowthBudgetGeometryDirectionsAreAlwaysAdmitted pins the boundary with the
// writer-owned capability: a direction whose writer needs the larger buffer for its
// framing keeps it, and it is not charged against the shared cap.
func TestGrowthBudgetGeometryDirectionsAreAlwaysAdmitted(t *testing.T) {
	var level atomic.Uint32
	level.Store(uint32(LevelCritical))
	budget := newTestGrowthBudget(0, 0, &level)

	threshold, permit := budget.Admit(trafficclass.ClassBulk, true)
	require.Equal(t, CopyGrowthImmediate, threshold)
	require.Nil(t, permit, "a geometry-required direction takes no shared slot")
	stats := budget.Stats()
	require.Equal(t, 0, stats.InUse)
	require.Equal(t, uint64(1), stats.GeometryNeeded)
	require.Equal(t, uint64(0), stats.Refused)
}

// TestGrowthBudgetNeverGrantsAboveTheArmedTotal is the audit's P2 reproduction: four
// ordinary permits held under Flows=4, then Armed lowers the plan to 2+1. The old
// grants stay (their buffers are already grown and drain as their copies end), but a
// new interactive direction must NOT be allowed to raise InUse from 4 to 5 - the check
// has to be the current total, not only the reserve's own usage.
func TestGrowthBudgetNeverGrantsAboveTheArmedTotal(t *testing.T) {
	var level atomic.Uint32
	budget := newTestGrowthBudget(4, 2, &level)

	permits := make([]*GrowthPermit, 0, 4)
	for index := 0; index < 4; index++ {
		threshold, permit := budget.Admit(trafficclass.ClassBulk, false)
		require.Equal(t, CopyGrowthDefault, threshold)
		require.NotNil(t, permit)
		permits = append(permits, permit)
	}
	require.Equal(t, 4, budget.Stats().InUse)

	level.Store(uint32(LevelArmed))
	stats := budget.Stats()
	require.Equal(t, 2, stats.Flows)
	require.Equal(t, 1, stats.Reserve)

	threshold, permit := budget.Admit(trafficclass.ClassInteractive, false)
	require.Equal(t, CopyGrowthNever, threshold, "Armed's total is 3; InUse is already 4, so no new direction")
	require.Nil(t, permit)
	require.Equal(t, 4, budget.Stats().InUse, "a downgrade must not add occupancy above the new total")

	// Draining the old permits opens exactly the current plan, in the current order:
	// two ordinary, then one reserve for a high-priority direction.
	permits[0].Release()
	permits[1].Release()
	threshold, permit = budget.Admit(trafficclass.ClassInteractive, false)
	require.Equal(t, CopyGrowthDefault, threshold, "InUse 2 == flows 2, so a high-priority direction takes the Armed reserve")
	require.NotNil(t, permit)
	require.Equal(t, 3, budget.Stats().InUse)
	require.Equal(t, 1, budget.Stats().ReservedInUse)

	threshold, refused := budget.Admit(trafficclass.ClassInteractive, false)
	require.Equal(t, CopyGrowthNever, threshold, "the Armed total is reached")
	require.Nil(t, refused)
	permit.Release()
	permits[2].Release()
	permits[3].Release()
	require.Equal(t, 0, budget.Stats().InUse)

	// Recovery re-opens the Normal cap.
	level.Store(uint32(LevelNormal))
	threshold, permit = budget.Admit(trafficclass.ClassBulk, false)
	require.Equal(t, CopyGrowthDefault, threshold)
	require.NotNil(t, permit)
	permit.Release()
}

func TestGrowthBudgetConservesUnderConcurrency(t *testing.T) {
	var level atomic.Uint32
	budget := newTestGrowthBudget(6, 2, &level)
	const (
		workers    = 12
		iterations = 120
	)
	normalCap := 6 + 2
	var waitGroup sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		waitGroup.Add(1)
		go func(seed int) {
			defer waitGroup.Done()
			random := rand.New(rand.NewSource(int64(seed)))
			for iteration := 0; iteration < iterations; iteration++ {
				class := trafficclass.ClassBulk
				if random.Intn(3) == 0 {
					class = trafficclass.ClassInteractive
				}
				_, permit := budget.Admit(class, false)
				if permit != nil {
					time.Sleep(time.Duration(random.Intn(200)) * time.Microsecond)
					permit.Release()
				}
				if iteration%20 == 0 {
					level.Store(uint32(Level(random.Intn(3))))
				}
				stats := budget.Stats()
				if stats.InUse > normalCap || stats.InUse < 0 ||
					stats.ReservedInUse < 0 || stats.ReservedInUse > stats.InUse {
					t.Errorf("inconsistent growth snapshot: %+v", stats)
					return
				}
			}
		}(worker)
	}
	waitGroup.Wait()
	level.Store(uint32(LevelNormal))
	stats := budget.Stats()
	require.Equal(t, 0, stats.InUse)
	require.Equal(t, 0, stats.ReservedInUse)
	require.Equal(t, uint64(0), stats.DoubleReleases)
	require.Equal(t, stats.Granted, stats.Released, "every permit handed out must come back")
	require.LessOrEqual(t, stats.Peak, normalCap)
}
