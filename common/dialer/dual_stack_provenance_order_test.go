package dialer

import (
	"context"
	"net/netip"
	"testing"
	"time"

	C "github.com/sagernet/sing-box/constant"

	"github.com/stretchr/testify/require"
)

// Superseded candidates are ordered behind current ones WITHIN a family and ACROSS families (§C).
//
// The first version of the provenance ordering compared the two family HEADS only, and took the head
// of the chosen family. Both are too coarse for the shapes a resolver actually produces:
//
//   - one family can hold two batches, the previous environment's and the current one's, because the
//     destination family did not change between them;
//   - a family whose head is superseded can still hold a current candidate behind it, while the
//     other family holds nothing current at all.
//
// In both cases the superseded candidate took the launch slot and the current one waited - or was
// never reached, because the superseded address connected first and won the race.

// TestASupersededCandidateDoesNotLeadACurrentOneInItsFamily is the within-family rule.
func TestASupersededCandidateDoesNotLeadACurrentOneInItsFamily(t *testing.T) {
	const fallbackDelay = 40 * time.Millisecond

	started := netip.MustParseAddr("192.0.2.1")    // current, silently blackholed, takes the first slot
	superseded := netip.MustParseAddr("192.0.2.2") // previous environment, healthy
	current := netip.MustParseAddr("192.0.2.92")   // current, healthy

	inner := newStreamOrderDialer(started)
	late := make(chan dualStackCandidate, 3)
	late <- dualStackCandidate{address: started, family: familyIPv4, sourceGeneration: 2, sourceKnown: true}
	late <- dualStackCandidate{address: superseded, family: familyIPv4, sourceGeneration: 1, sourceKnown: true}
	late <- dualStackCandidate{address: current, family: familyIPv4, sourceGeneration: 2, sourceKnown: true}
	close(late)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	scheduler := supersededScheduler(fallbackDelay, 2)
	conn, winner, err := scheduler.dialWithLateCandidates(ctx,
		planCandidates(nil, netip.Addr{}, C.DomainStrategyPreferIPv4), late, inner.attempt)
	require.NoError(t, err)
	require.Equal(t, current, winner,
		"a current-environment candidate must lead a superseded one in the SAME family; attempts: %v",
		inner.attempts())
	if conn != nil {
		_ = conn.Close()
	}
}

// TestACurrentCandidateBehindASupersededHeadStillWinsTheSlot is the cross-family rule.
func TestACurrentCandidateBehindASupersededHeadStillWinsTheSlot(t *testing.T) {
	const fallbackDelay = 40 * time.Millisecond

	started := netip.MustParseAddr("2001:db8::1") // current, silently blackholed, takes the first slot
	otherStale := netip.MustParseAddr("192.0.2.2")
	familyCurrent := netip.MustParseAddr("2001:db8::92")

	inner := newStreamOrderDialer(started)
	late := make(chan dualStackCandidate, 4)
	late <- dualStackCandidate{address: started, family: familyIPv6, sourceGeneration: 2, sourceKnown: true}
	late <- dualStackCandidate{address: netip.MustParseAddr("2001:db8::2"), family: familyIPv6, sourceGeneration: 1, sourceKnown: true}
	late <- dualStackCandidate{address: otherStale, family: familyIPv4, sourceGeneration: 1, sourceKnown: true}
	late <- dualStackCandidate{address: familyCurrent, family: familyIPv6, sourceGeneration: 2, sourceKnown: true}
	close(late)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	scheduler := supersededScheduler(fallbackDelay, 2)
	conn, winner, err := scheduler.dialWithLateCandidates(ctx,
		planCandidates(nil, netip.Addr{}, C.DomainStrategyPreferIPv6), late, inner.attempt)
	require.NoError(t, err)
	require.Equal(t, familyCurrent, winner,
		"the family that still has a CURRENT candidate must take the next slot, even when both heads "+
			"are superseded; attempts: %v", inner.attempts())
	if conn != nil {
		_ = conn.Close()
	}
}
