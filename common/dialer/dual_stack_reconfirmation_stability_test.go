package dialer

import (
	"context"
	"net/netip"
	"testing"
	"time"

	C "github.com/sagernet/sing-box/constant"

	"github.com/stretchr/testify/require"
)

// Two self-review attacks on the re-confirmation upgrade, kept as regressions because both could
// have been broken by it.
//
// # Attack 1: does upgrading a plan entry re-order the plan?
//
// A static plan's candidates carry no provenance, so they rank as current. An upgrade turns one of
// them from "unknown" into "known current" - which must not lift it above an earlier plan entry.
// The scan that picks the next candidate treats unknown as current, so the order is unchanged; this
// pins that.
//
// # Attack 2: does a generation change manufacture retries?
//
// One flow with a fast failure, a silent blackhole, a superseded late batch and a current
// re-confirmation must still conclude on exactly one winner, dial each canonical address at most
// once, and not spend an attempt on the generation change itself.

// TestPlanOrderSurvivesAReconfirmation is attack 1.
func TestPlanOrderSurvivesAReconfirmation(t *testing.T) {
	const fallbackDelay = 40 * time.Millisecond

	started := netip.MustParseAddr("192.0.2.1")
	secondInPlan := netip.MustParseAddr("192.0.2.2")
	thirdInPlan := netip.MustParseAddr("192.0.2.3")

	inner := newStreamOrderDialer(started, thirdInPlan)
	// The plan is same-family, so its own order is the resolver order.
	plan := planCandidates([]netip.Addr{started, secondInPlan, thirdInPlan}, netip.Addr{}, C.DomainStrategyPreferIPv4)
	require.Len(t, plan.candidates, 3)

	late := make(chan dualStackCandidate, 1)
	// The current environment confirms the LAST plan entry. It is already "current" as far as the
	// ordering rule is concerned (unknown ranks as current), so nothing may move.
	late <- dualStackCandidate{address: thirdInPlan, family: familyIPv4, sourceGeneration: 2, sourceKnown: true}
	close(late)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	scheduler := supersededScheduler(fallbackDelay, 2)
	conn, winner, err := scheduler.dialWithLateCandidates(ctx, plan, late, inner.attempt)
	require.NoError(t, err)
	require.Equal(t, secondInPlan, winner,
		"a confirmation must not lift a later plan entry above an earlier one; attempts: %v",
		inner.attempts())
	require.Equal(t, []netip.Addr{started, secondInPlan}, inner.attempts())
	if conn != nil {
		_ = conn.Close()
	}
}

// TestFastFailureBlackholeAndReconfirmationStayBounded is attack 2.
func TestFastFailureBlackholeAndReconfirmationStayBounded(t *testing.T) {
	const fallbackDelay = 40 * time.Millisecond

	failing4 := netip.MustParseAddr("192.0.2.1")
	blackhole6 := netip.MustParseAddr("2001:db8::1")
	superseded4 := netip.MustParseAddr("192.0.2.2")
	confirmed4 := netip.MustParseAddr("192.0.2.3")

	inner := newStreamOrderDialer(blackhole6)
	inner.fail = map[netip.Addr]error{failing4: errRecoveryFastFailure}

	plan := planCandidates([]netip.Addr{failing4, blackhole6}, netip.Addr{}, C.DomainStrategyPreferIPv4)
	late := make(chan dualStackCandidate, 3)
	late <- dualStackCandidate{address: superseded4, family: familyIPv4, sourceGeneration: 1, sourceKnown: true}
	late <- dualStackCandidate{address: confirmed4, family: familyIPv4, sourceGeneration: 2, sourceKnown: true}
	late <- dualStackCandidate{address: confirmed4, family: familyIPv4, sourceGeneration: 2, sourceKnown: true}
	close(late)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	scheduler := supersededScheduler(fallbackDelay, 2)
	conn, winner, err := scheduler.dialWithLateCandidates(ctx, plan, late, inner.attempt)
	require.NoError(t, err)
	require.Equal(t, confirmed4, winner, "attempts: %v", inner.attempts())

	attempts := inner.attempts()
	seen := make(map[netip.Addr]int, len(attempts))
	for _, address := range attempts {
		seen[address]++
	}
	require.Equal(t, 1, seen[failing4], "the fast failure must not be retried")
	require.Equal(t, 1, seen[blackhole6], "the blackhole is one attempt, cancelled by the winner")
	require.Equal(t, 1, seen[confirmed4], "the confirmation must not duplicate the attempt")
	require.Zero(t, seen[superseded4], "a current confirmation must win the slot from a superseded address")
	require.LessOrEqual(t, len(attempts), 3,
		"a generation change must not manufacture attempts; attempts: %v", attempts)
	requireBusinessData(t, conn)
	require.NoError(t, conn.Close())
}
