package outbound

// LN attack suite: the reclamation BUDGET, the PRESSURE mapping and the sweep loop.
//
// # What this file is attacking
//
// reclaim.go has three places where a policy number becomes behaviour: the per-level compression
// (pressurized), the sweep's stop condition (count and bytes), and the per-sweep eviction cap. A
// policy layer is easy to write and hard to prove, because the failures are not crashes: a budget that
// GROWS under pressure, a sweep that never reaches its target, or a count that drifts so the demand
// trigger falls silent while the process is over budget. Each case below pins one of those with a
// counted oracle:
//
//	the effective budget must never exceed the configured one (pressure may only shrink it);
//	one sweep must never exceed its eviction cap, and a corpus must converge in the predicted number
//	of sweeps;
//	the live-expansion counter must stay exact, because it is the only thing that triggers a sweep
//	without a clock.

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/resourceguard"

	"github.com/stretchr/testify/require"
)

func attackBudgetPolicy(maxExpanded int) DeferredReclaimPolicy {
	policy := DefaultDeferredReclaimPolicy()
	policy.MaxExpanded = maxExpanded
	policy.MaxBytes = 0
	policy.MaxEvictionsPerSweep = 32
	policy.MinInterval = 0
	return policy
}

// TestAttackPressurizedBudgetNeverExceedsTheConfiguredOne is the "cap below floor" scan: the level
// mapping keeps a floor of two expansions (and two expansions' worth of bytes) so a single flow is
// not released on every pressure transition. That floor must never be able to RAISE a budget the
// operator configured lower, because then a pressure event would make the process hold MORE, not
// less - the opposite of what the trigger exists for.
func TestAttackPressurizedBudgetNeverExceedsTheConfiguredOne(t *testing.T) {
	cases := []struct {
		name   string
		policy DeferredReclaimPolicy
	}{
		{
			name:   "expanded-cap-below-the-floor",
			policy: DeferredReclaimPolicy{MaxExpanded: 1, MaxBytes: 0, AssumedExpansionBytes: 3072, MaxEvictionsPerSweep: 1},
		},
		{
			name:   "byte-cap-below-the-floor",
			policy: DeferredReclaimPolicy{MaxExpanded: 64, MaxBytes: 4096, AssumedExpansionBytes: 3072, MaxEvictionsPerSweep: 1},
		},
		{
			name:   "expanded-cap-two",
			policy: DeferredReclaimPolicy{MaxExpanded: 2, MaxBytes: 0, AssumedExpansionBytes: 3072, MaxEvictionsPerSweep: 1},
		},
		{
			name:   "expanded-cap-three",
			policy: DeferredReclaimPolicy{MaxExpanded: 3, MaxBytes: 0, AssumedExpansionBytes: 3072, MaxEvictionsPerSweep: 1},
		},
		{
			name:   "default",
			policy: DefaultDeferredReclaimPolicy(),
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			for _, level := range []resourceguard.Level{resourceguard.LevelArmed, resourceguard.LevelCritical} {
				pressed := testCase.policy.pressurized(level)
				if testCase.policy.MaxExpanded > 0 {
					require.LessOrEqual(t, pressed.MaxExpanded, testCase.policy.MaxExpanded,
						"level %s raised the expansion budget above the configured one", level)
					require.Greater(t, pressed.MaxExpanded, 0)
				}
				if testCase.policy.MaxBytes > 0 {
					require.LessOrEqual(t, pressed.MaxBytes, testCase.policy.MaxBytes,
						"level %s raised the byte budget above the configured one", level)
					require.Greater(t, pressed.MaxBytes, int64(0))
				}
			}
			require.Equal(t, testCase.policy, testCase.policy.pressurized(resourceguard.LevelNormal),
				"the normal level must return the configured policy unchanged")
		})
	}
}

// TestAttackPressureMappingSurvivesDegenerateBudgets is the "overflow, negative, NaN-adjacent" scan:
// a policy is a plain struct an embedder can fill in, so every branch has to have a defined answer for
// values that are not the default. The oracle here is only that nothing panics, nothing underflows
// into a negative bound, and a positive budget stays positive.
func TestAttackPressureMappingSurvivesDegenerateBudgets(t *testing.T) {
	cases := []DeferredReclaimPolicy{
		{MaxExpanded: -5, MaxBytes: -1, AssumedExpansionBytes: -3},
		{MaxExpanded: 0, MaxBytes: 0, AssumedExpansionBytes: 0},
		{MaxExpanded: 1, MaxBytes: math.MaxInt64, AssumedExpansionBytes: math.MaxInt64/2 + 1},
		{MaxExpanded: math.MaxInt32, MaxBytes: math.MaxInt64, AssumedExpansionBytes: math.MaxInt64},
		{MaxExpanded: 2, MaxBytes: 1, AssumedExpansionBytes: 1},
	}
	for index, policy := range cases {
		for _, level := range []resourceguard.Level{resourceguard.LevelNormal, resourceguard.LevelArmed, resourceguard.LevelCritical} {
			pressed := policy.pressurized(level)
			if policy.MaxExpanded <= 0 {
				require.Equal(t, policy.MaxExpanded, pressed.MaxExpanded,
					"case %d level %s: a disabled or negative cap must stay disabled", index, level)
			} else {
				require.Greater(t, pressed.MaxExpanded, 0, "case %d level %s", index, level)
			}
			if policy.MaxBytes <= 0 {
				require.Equal(t, policy.MaxBytes, pressed.MaxBytes,
					"case %d level %s: a disabled byte budget must stay disabled", index, level)
			} else {
				require.Greater(t, pressed.MaxBytes, int64(0), "case %d level %s", index, level)
			}
		}
	}
}

// TestAttackSweepReachesTheConfiguredTargetUnderPressure is the behavioural half of the floor scan: the
// budget the sweep actually enforces must be the configured one, so a corpus of twelve idle
// expansions must converge to exactly one at Critical pressure.
func TestAttackSweepReachesTheConfiguredTargetUnderPressure(t *testing.T) {
	fixture := newAttackFixture(t)
	fixture.startAll(t)
	fixture.manager.SetDeferredReclaimPressure(func(context.Context) resourceguard.Level {
		return resourceguard.LevelCritical
	})
	nodes := make([]*LazyOutbound[string], 12)
	fixture.manager.SetDeferredReclaimPolicy(attackBudgetPolicy(1 << 20))
	for index := range nodes {
		nodes[index] = fixture.create(t, fmt.Sprintf("pressure-%02d", index))
		expandAttack(t, nodes[index])
	}
	fixture.manager.SetDeferredReclaimPolicy(attackBudgetPolicy(1))

	result := fixture.manager.ReclaimDeferredExpansions(context.Background(), ReclaimCauseExplicit)
	require.Equal(t, resourceguard.LevelCritical, result.Level)
	require.Equal(t, 1, result.Target, "the configured budget is the enforced budget")
	require.Equal(t, 12, result.Expanded)
	require.Equal(t, 11, result.Evicted)
	require.Equal(t, 1, fixture.manager.DeferredExpansionCount())
}

// TestAttackSweepConvergesForEveryCapCombination is the "per-sweep cap vs target" scan: whatever the
// combination, one sweep must respect its cap and the sequence must converge in the number of sweeps
// the cap implies - never spin, never stall above the budget.
func TestAttackSweepConvergesForEveryCapCombination(t *testing.T) {
	const corpus = 12
	for _, maxExpanded := range []int{1, 2, 3, 8} {
		for _, perSweep := range []int{1, 2, 5, 32} {
			name := fmt.Sprintf("target-%d-per-sweep-%d", maxExpanded, perSweep)
			t.Run(name, func(t *testing.T) {
				fixture := newAttackFixture(t)
				fixture.startAll(t)
				fixture.manager.SetDeferredReclaimPolicy(attackBudgetPolicy(1 << 20))
				for index := 0; index < corpus; index++ {
					expandAttack(t, fixture.create(t, fmt.Sprintf("cap-%02d", index)))
				}
				policy := attackBudgetPolicy(maxExpanded)
				policy.MaxEvictionsPerSweep = perSweep
				fixture.manager.SetDeferredReclaimPolicy(policy)

				expectedSweeps := (corpus - maxExpanded + perSweep - 1) / perSweep
				if expectedSweeps < 1 {
					expectedSweeps = 1
				}
				require.LessOrEqual(t, expectedSweeps, corpus, "table sanity")

				sweeps := 0
				total := 0
				for fixture.manager.DeferredExpansionCount() > maxExpanded {
					result := fixture.manager.ReclaimDeferredExpansions(context.Background(), ReclaimCauseExplicit)
					require.LessOrEqual(t, result.Evicted, perSweep, "one sweep exceeded its eviction cap")
					require.Greater(t, result.Evicted, 0, "the sweep must make progress while over budget")
					total += result.Evicted
					sweeps++
					require.LessOrEqual(t, sweeps, expectedSweeps,
						"the sweep took more passes than the cap implies")
					require.Less(t, sweeps, corpus+2, "the sweep must converge")
				}
				require.Equal(t, corpus-maxExpanded, total)
				require.Equal(t, maxExpanded, fixture.manager.DeferredExpansionCount(),
					"the corpus must land exactly on the configured budget")
			})
		}
	}
}

// TestAttackSweepLoopTerminatesForDegenerateBudgets: a policy made only of degenerate values must
// still produce a terminating sweep with a defined result. The dangerous shapes are "both bounds
// disabled" (nothing may be evicted, and the sweep must say so rather than loop) and "byte bound below
// one expansion" (everything idle is evicted, and the sweep must stop at zero, not underflow).
func TestAttackSweepLoopTerminatesForDegenerateBudgets(t *testing.T) {
	cases := []struct {
		name     string
		policy   DeferredReclaimPolicy
		expected int
	}{
		{
			name:     "both-bounds-disabled",
			policy:   DeferredReclaimPolicy{MaxExpanded: 0, MaxBytes: 0, MaxEvictionsPerSweep: 4},
			expected: 5,
		},
		{
			name:     "byte-bound-below-one-expansion",
			policy:   DeferredReclaimPolicy{MaxExpanded: 100, MaxBytes: 1, AssumedExpansionBytes: 3072, MaxEvictionsPerSweep: 16},
			expected: 0,
		},
		{
			name:     "negative-per-sweep-is-uncapped-not-a-stall",
			policy:   DeferredReclaimPolicy{MaxExpanded: 1, MaxBytes: 0, MaxEvictionsPerSweep: -1},
			expected: 1,
		},
		{
			name:     "zero-per-sweep-is-uncapped",
			policy:   DeferredReclaimPolicy{MaxExpanded: 1, MaxBytes: 0, MaxEvictionsPerSweep: 0},
			expected: 1,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newAttackFixture(t)
			fixture.startAll(t)
			fixture.manager.SetDeferredReclaimPolicy(attackBudgetPolicy(1 << 20))
			for index := 0; index < 5; index++ {
				expandAttack(t, fixture.create(t, fmt.Sprintf("degenerate-%02d", index)))
			}
			fixture.manager.SetDeferredReclaimPolicy(testCase.policy)

			done := make(chan ReclaimResult, 1)
			go func() {
				done <- fixture.manager.ReclaimDeferredExpansions(context.Background(), ReclaimCauseExplicit)
			}()
			select {
			case result := <-done:
				require.Equal(t, testCase.expected, fixture.manager.DeferredExpansionCount(),
					"the sweep's stop condition has a defined answer for this policy")
				require.LessOrEqual(t, result.Evicted, 5)
				require.Equal(t, 5-result.Evicted, fixture.manager.DeferredExpansionCount())
			case <-time.After(5 * time.Second):
				t.Fatal("one sweep did not terminate for a degenerate budget")
			}
		})
	}
}

// TestAttackLiveExpansionCountStaysExactAcrossAReenable is the demand trigger's invariant. The
// counter is what bounds a probe round WITHOUT a clock, and reclaim.go documents it as "exact by
// construction (one increment per publish, one decrement per release)". A policy that is armed, then
// disarmed, then armed again must not be able to lose that exactness: if it does, the trigger falls
// silent while the process is over budget, and only the thirty-second idle walk would notice.
func TestAttackLiveExpansionCountStaysExactAcrossAReenable(t *testing.T) {
	fixture := newAttackFixture(t)
	fixture.startAll(t)

	fixture.manager.DisableDeferredReclaim()
	require.False(t, fixture.manager.DeferredReclaimEnabled())
	for index := 0; index < 3; index++ {
		expandAttack(t, fixture.create(t, fmt.Sprintf("pending-%02d", index)))
	}
	require.EqualValues(t, 3, fixture.pending(),
		"the live-expansion counter must count a publish even while sweeps are disarmed, because the "+
			"release that pairs with it is counted unconditionally")
	require.Equal(t, 3, fixture.manager.DeferredExpansionCount())

	// Re-arm with a budget of one. The NEXT publish is what must notice that the live count is
	// already over budget and ask for a sweep - no clock, no idle walk.
	fixture.manager.SetDeferredReclaimPolicy(attackBudgetPolicy(1))
	require.True(t, fixture.manager.DeferredReclaimEnabled())
	expandAttack(t, fixture.create(t, "pending-trigger"))

	require.Eventually(t, func() bool {
		return fixture.manager.DeferredExpansionCount() == 1
	}, 5*time.Second, 5*time.Millisecond,
		"the demand trigger must fire from the exact live count, not from a count that lost the "+
			"publishes made while the policy was disarmed")
}
