package dialer

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The family-health table must stay bounded, and its documented "the environment changing clears the state"
// must not be claimed when nothing does the clearing.
//
// # The measurement
//
// `familyHealth` is a `map[uint64]familyHealthState` keyed by network environment. Expiry is lazy: an entry is
// removed only when that SAME environment is consulted again and has aged out. An environment that is never
// revisited therefore keeps its verdict for the process lifetime. Measured before the bound: 100 001 entries
// after 100 000 distinct environments, with no cap and no eviction.
//
// The behavioural property that actually matters was measured and holds - a verdict recorded on network A
// cannot penalise network B - and it holds because of the KEY, not because of any clearing step. The defect
// was the documentation claiming a mechanism that does not exist, plus unbounded growth nobody had counted.
//
// Clearing at the cap is safe in the only direction that matters for this type: a verdict is a soft nudge
// that makes both families start together instead of waiting out the fallback delay. Losing every verdict
// costs a little latency on the next connection and can never refuse a candidate.

// TestTheFamilyHealthTableStaysBounded is the regression.
func TestTheFamilyHealthTableStaysBounded(t *testing.T) {
	health := newFamilyHealth()

	// Every environment records a failure, which is the only way an entry is created.
	for environment := uint64(1); environment <= familyHealthMaxGenerations*4; environment++ {
		health.recordStalledFamily(environment, familyIPv6)
	}

	health.access.Lock()
	entries := len(health.state)
	health.access.Unlock()

	require.LessOrEqual(t, entries, familyHealthMaxGenerations,
		"the table must not grow without bound: expiry only happens when the same environment is consulted "+
			"again, so a long roam across networks that are never revisited accumulates an entry each time")
	require.Greater(t, entries, 0, "and it must not be emptied for no reason either")
}

// TestClearingTheTableNeverRefusesACandidate is the safety direction.
//
// The cap is enforced by clearing, so this pins that a cleared table answers exactly what a fresh one answers:
// no penalty, no refusal. If clearing could ever make the scheduler MORE restrictive, bounding the map would
// trade memory for correctness.
func TestClearingTheTableNeverRefusesACandidate(t *testing.T) {
	fresh := newFamilyHealth()
	saturated := newFamilyHealth()
	for environment := uint64(1); environment <= familyHealthMaxGenerations+1; environment++ {
		saturated.recordStalledFamily(environment, familyIPv6)
	}

	const probeEnvironment = 999999
	for _, health := range []*familyHealth{fresh, saturated} {
		require.False(t, health.fallbackImmediately(probeEnvironment, familyIPv6, familyIPv4),
			"an environment with no verdict must never be treated as penalised, whether the table is fresh "+
				"or has just been cleared by the bound")
	}

	// And a cleared table still records and uses a NEW verdict, so the feature is not disabled by the bound.
	saturated.recordStalledFamily(probeEnvironment, familyIPv6)
	require.True(t, saturated.fallbackImmediately(probeEnvironment, familyIPv6, familyIPv4),
		"a verdict recorded after a clear must still be honoured")
}

// TestAFamilyVerdictCannotCrossEnvironments re-states the property the documentation claimed a mechanism for.
//
// It was measured true before this change and must stay true after it: the KEY scopes the verdict, and that is
// what keeps this type safe to hold in memory with no persistence.
func TestAFamilyVerdictCannotCrossEnvironments(t *testing.T) {
	health := newFamilyHealth()
	health.recordStalledFamily(0xA, familyIPv6)

	require.True(t, health.fallbackImmediately(0xA, familyIPv6, familyIPv4),
		"the environment that observed the failure is penalised")
	require.False(t, health.fallbackImmediately(0xB, familyIPv6, familyIPv4),
		"and a different network is not: a broken IPv6 path on one Wi-Fi says nothing about another")
}

// TestAnExpiredVerdictIsDroppedAndDeleted keeps the lazy expiry honest, since it is the only path that removes
// an entry without the bound.
func TestAnExpiredVerdictIsDroppedAndDeleted(t *testing.T) {
	health := newFamilyHealth()
	health.recordStalledFamily(0xC, familyIPv6)

	health.access.Lock()
	health.state[0xC] = familyHealthState{failedFamily: familyIPv6, failedAt: time.Now().Add(-2 * 15 * time.Second)}
	health.access.Unlock()

	require.False(t, health.fallbackImmediately(0xC, familyIPv6, familyIPv4),
		"a verdict older than the window must not penalise")

	health.access.Lock()
	_, stillThere := health.state[0xC]
	health.access.Unlock()
	require.False(t, stillThere, "and reading it must delete it, which is the only non-bound removal")
}
