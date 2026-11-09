package urltest

// The failure matrix for the stable/failover policy core.
//
// Every case feeds one round's observations and asserts the NEXT probe set (the power decision) and
// the selection (the failover decision). There is no clock and no goroutine here on purpose: the
// policy is a pure state machine, so a failure is reproducible byte for byte.

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func ok(delay uint16) FailoverObservation {
	return FailoverObservation{Measured: true, OK: true, DelayMs: delay}
}

func failed() FailoverObservation {
	return FailoverObservation{Measured: true, OK: false}
}

func notProbed() FailoverObservation {
	return FailoverObservation{}
}

func members(tags ...string) []string {
	return tags
}

func decisionsEqual(a, b FailoverDecision) bool {
	if a.Selected != b.Selected || a.Changed != b.Changed || a.Widened != b.Widened || len(a.ProbeSet) != len(b.ProbeSet) {
		return false
	}
	for index := range a.ProbeSet {
		if a.ProbeSet[index] != b.ProbeSet[index] {
			return false
		}
	}
	return true
}

// TestTheZeroStateKeepsTheCurrentProbeBehaviour makes the "not wired yet" case explicit: an
// unconfigured policy must not silently start dropping members from the probe set.
func TestTheZeroStateKeepsTheCurrentProbeBehaviour(t *testing.T) {
	var state *FailoverState
	decision := state.Decide(members("a", "b"), nil)
	require.Equal(t, members("a", "b"), decision.ProbeSet)
	require.Empty(t, decision.Selected)

	empty := NewFailoverState(FailoverPolicy{}, "")
	first := empty.Decide(members("a", "b"), map[string]FailoverObservation{"a": ok(30), "b": ok(80)})
	require.Equal(t, members("a", "b"), first.ProbeSet, "no stable member yet: measure everything")
	require.Equal(t, "a", first.Selected, "the fastest measured member is the first answer")
}

// TestAHealthyStableMemberCostsOneProbe is the power saving: once a stable member exists and
// answered, the round probes only that member.
func TestAHealthyStableMemberCostsOneProbe(t *testing.T) {
	state := NewFailoverState(FailoverPolicy{}, "a")
	decision := state.Decide(members("a", "b", "c"), map[string]FailoverObservation{"a": ok(40)})
	require.Equal(t, members("a"), decision.ProbeSet, "one probe per round while the stable member is healthy")
	require.Equal(t, "a", decision.Selected)
	require.False(t, decision.Widened)
	require.False(t, decision.Changed)
}

// TestOneLostRoundIsNotDegradation: a single miss is retried before the probe set widens.
func TestOneLostRoundIsNotDegradation(t *testing.T) {
	state := NewFailoverState(FailoverPolicy{WidenAfter: 3}, "a")
	require.Equal(t, members("a"), state.Decide(members("a", "b"), map[string]FailoverObservation{"a": failed()}).ProbeSet)
	require.Equal(t, members("a"), state.Decide(members("a", "b"), map[string]FailoverObservation{"a": failed()}).ProbeSet,
		"two misses are still below the widen threshold of three")
	decision := state.Decide(members("a", "b"), map[string]FailoverObservation{"a": failed()})
	require.Equal(t, members("a", "b"), decision.ProbeSet, "the third miss widens the probe set")
	require.True(t, decision.Widened)
}

// TestConfirmedDegradationWidensAndFailsOver: after the widen threshold, the stable member's failure
// sends traffic to the best measured alternative immediately.
func TestConfirmedDegradationWidensAndFailsOver(t *testing.T) {
	state := NewFailoverState(FailoverPolicy{WidenAfter: 2, SettleAfter: 2}, "a")
	first := state.Decide(members("a", "b"), map[string]FailoverObservation{"a": failed()})
	require.Equal(t, members("a"), first.ProbeSet)

	second := state.Decide(members("a", "b"), map[string]FailoverObservation{"a": failed(), "b": ok(90)})
	require.Equal(t, members("a", "b"), second.ProbeSet)
	require.Equal(t, "b", second.Selected, "while the stable member is down, the best measured alternative carries traffic")
	require.True(t, second.Changed)

	third := state.Decide(members("a", "b"), map[string]FailoverObservation{"a": failed(), "b": ok(90)})
	require.Equal(t, "b", third.Selected)
	require.Equal(t, members("b"), third.ProbeSet, "a challenger that settles becomes the stable member and narrows the set")
	require.Equal(t, "b", state.Stable())
}

// TestADownStableMemberWithoutAlternativeDoesNotFlap: no measured alternative means keep the current
// answer and keep measuring, not switch to something unmeasured.
func TestADownStableMemberWithoutAlternativeDoesNotFlap(t *testing.T) {
	state := NewFailoverState(FailoverPolicy{WidenAfter: 1}, "a")
	state.Decide(members("a", "b"), map[string]FailoverObservation{"a": ok(40)})
	decision := state.Decide(members("a", "b"), map[string]FailoverObservation{"a": failed()})
	require.Equal(t, "a", decision.Selected, "no alternative was measured, so the selection does not move")
	require.False(t, decision.Changed)
	require.Equal(t, members("a", "b"), decision.ProbeSet)
}

// TestRecoveryNarrowsOnlyAfterConsecutiveHealthyRounds: one lucky probe does not end a degradation
// episode.
func TestRecoveryNarrowsOnlyAfterConsecutiveHealthyRounds(t *testing.T) {
	state := NewFailoverState(FailoverPolicy{WidenAfter: 1, RecoverAfter: 2}, "a")
	state.Decide(members("a", "b"), map[string]FailoverObservation{"a": failed(), "b": ok(90)})

	first := state.Decide(members("a", "b"), map[string]FailoverObservation{"a": ok(40), "b": ok(90)})
	require.Equal(t, members("a", "b"), first.ProbeSet, "the first healthy round is not yet recovery")
	require.True(t, first.Widened)

	second := state.Decide(members("a", "b"), map[string]FailoverObservation{"a": ok(40), "b": ok(90)})
	require.Equal(t, members("a"), second.ProbeSet, "the second consecutive healthy round narrows the set")
	require.False(t, second.Widened)
	require.Equal(t, "a", second.Selected)
}

// TestAChallengerNeedsMoreThanTheTolerance: a few milliseconds better is not a reason to move
// traffic, and the widened episode ends by returning to the stable member.
func TestAChallengerNeedsMoreThanTheTolerance(t *testing.T) {
	state := NewFailoverState(FailoverPolicy{WidenAfter: 1, SettleAfter: 2, RecoverAfter: 1, ToleranceMs: 50}, "a")
	state.Decide(members("a", "b"), map[string]FailoverObservation{"a": failed(), "b": ok(120)})

	decision := state.Decide(members("a", "b"), map[string]FailoverObservation{"a": ok(100), "b": ok(80)})
	require.Equal(t, "a", decision.Selected,
		"80ms does not beat 100ms by more than the 50ms tolerance, so the stable member keeps traffic")
	require.Equal(t, members("a"), decision.ProbeSet, "recovery with no winning challenger narrows the set")
}

// TestHysteresisMigratesToAClearlyBetterChallenger: the swap happens only after the settle window,
// and only when the challenger is clearly better.
func TestHysteresisMigratesToAClearlyBetterChallenger(t *testing.T) {
	state := NewFailoverState(FailoverPolicy{WidenAfter: 1, SettleAfter: 3, RecoverAfter: 1, ToleranceMs: 50}, "a")
	state.Decide(members("a", "b"), map[string]FailoverObservation{"a": failed(), "b": ok(200)})

	// Round 1: A recovers at 100ms, B is clearly better at 30ms.
	first := state.Decide(members("a", "b"), map[string]FailoverObservation{"a": ok(100), "b": ok(30)})
	require.Equal(t, "a", first.Selected, "the first clearly-better round is not enough")
	require.True(t, first.Widened, "the episode continues while a challenger is winning")

	second := state.Decide(members("a", "b"), map[string]FailoverObservation{"a": ok(100), "b": ok(30)})
	require.Equal(t, "a", second.Selected)

	third := state.Decide(members("a", "b"), map[string]FailoverObservation{"a": ok(100), "b": ok(30)})
	require.Equal(t, "b", third.Selected, "after the settle window the challenger takes over")
	require.True(t, third.Changed)
	require.Equal(t, members("b"), third.ProbeSet)
	require.Equal(t, "b", state.Stable())
}

// TestANodeReplacementKeepsTheStableSlot: a provider replaces the OBJECT under the same tag; the
// policy is keyed by tag, so the slot and its power saving survive.
func TestANodeReplacementKeepsTheStableSlot(t *testing.T) {
	state := NewFailoverState(FailoverPolicy{}, "node-a")
	decision := state.Decide(members("node-a", "node-b"), map[string]FailoverObservation{"node-a": ok(40)})
	require.Equal(t, members("node-a"), decision.ProbeSet)
	require.Equal(t, "node-a", state.Stable())
}

// TestARemovedNodeReleasesTheSlot: when the tag disappears the state is dropped, and the next round
// measures everything again rather than probing a member that no longer exists.
func TestARemovedNodeReleasesTheSlot(t *testing.T) {
	state := NewFailoverState(FailoverPolicy{}, "node-a")
	state.Decide(members("node-a", "node-b"), map[string]FailoverObservation{"node-a": ok(40)})

	decision := state.Decide(members("node-b"), map[string]FailoverObservation{"node-b": ok(70)})
	require.Equal(t, "node-b", decision.Selected)
	require.Equal(t, members("node-b"), decision.ProbeSet)
	require.Equal(t, "node-b", state.Stable())
}

// TestTiesAreBrokenDeterministically keeps the answer reproducible when two members are equally fast.
func TestTiesAreBrokenDeterministically(t *testing.T) {
	first := NewFailoverState(FailoverPolicy{}, "")
	second := NewFailoverState(FailoverPolicy{}, "")
	observations := map[string]FailoverObservation{"b": ok(50), "a": ok(50), "c": ok(50)}
	one := first.Decide(members("c", "b", "a"), observations)
	two := second.Decide(members("c", "b", "a"), observations)
	require.Equal(t, "a", one.Selected, "equal delays are broken by tag, not by map order")
	require.True(t, decisionsEqual(one, two))
}

// TestTheSameSequenceIsReproducible is the determinism property the failure matrix relies on.
func TestTheSameSequenceIsReproducible(t *testing.T) {
	sequence := []map[string]FailoverObservation{
		{"a": ok(90), "b": ok(80)},
		{"a": failed(), "b": ok(80)},
		{"a": failed(), "b": ok(80)},
		{"a": ok(120), "b": ok(40)},
		{"a": ok(120), "b": ok(40)},
		{"a": ok(120), "b": ok(40)},
	}
	run := func() []FailoverDecision {
		state := NewFailoverState(FailoverPolicy{WidenAfter: 1, SettleAfter: 2, RecoverAfter: 1}, "a")
		decisions := make([]FailoverDecision, 0, len(sequence))
		for _, observations := range sequence {
			decisions = append(decisions, state.Decide(members("a", "b"), observations))
		}
		return decisions
	}
	first := run()
	second := run()
	require.Len(t, first, len(sequence))
	for index := range first {
		require.True(t, decisionsEqual(first[index], second[index]), "decision %d differed between identical runs", index)
	}
}

// TestAMemberThatWasNotProbedIsNotAFailure: a caller that ignored the probe set must not accumulate
// failures for members it did not measure.
func TestAMemberThatWasNotProbedIsNotAFailure(t *testing.T) {
	state := NewFailoverState(FailoverPolicy{WidenAfter: 1}, "a")
	state.Decide(members("a", "b"), map[string]FailoverObservation{"a": ok(40)})
	decision := state.Decide(members("a", "b"), map[string]FailoverObservation{"a": notProbed()})
	require.Equal(t, members("a"), decision.ProbeSet, "an unprobed stable member keeps the narrowed set")
	require.Equal(t, "a", decision.Selected)
}
