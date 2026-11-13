package urltest

// P02C — the deterministic core of the "stable pool / failover" probing policy.
//
// The group already has the ingredients: a selected member, per-target health evidence ordered by
// attempt start, a tolerance band that prefers the current member unless a challenger is clearly
// better, and a rotation anchor keyed by tag. What it does NOT have is a policy that decides HOW
// MUCH to probe: today every member is measured every round, which is the opposite of a power
// policy on a phone.
//
// This type is that decision, isolated as a pure state machine so it can be accepted before the
// configuration surface exists (the schema and its generated artifacts belong to the
// configuration/integration window):
//
//   - a healthy stable member means ONE probe per round (the stable member);
//   - the probe set widens only after the stable member has failed or gone missing for a configured
//     number of consecutive rounds, so a single lost probe is not a reason to measure everything;
//   - a challenger replaces the stable member only after it has beaten it by MORE than the
//     tolerance for a configured number of consecutive rounds (hysteresis, matching the selection
//     rule the group already applies);
//   - the widened set narrows again once the stable member has been healthy for a configured
//     number of consecutive rounds and no challenger is winning, so recovery is not one lucky
//     probe;
//   - a member replaced by a provider keeps its tag, so its slot (the stable position) survives the
//     replacement; the policy is keyed by tag for exactly that reason.
//
// No goroutines, no clocks, no I/O: every decision is a function of the members and the round's
// observations, which is what makes the failure matrix testable and reproducible.
//
// # What is deliberately NOT here
//
// The configuration field that selects this policy, its defaults and its validation are not added
// here: they are a shared schema (configuration window). Until that lands this file is unused by
// production code, and the zero value of FailoverState keeps today's behaviour (every member in
// the probe set) so wiring it can never silently drop probes.

import (
	"sync"
)

// FailoverPolicy is the configured part: thresholds and the tolerance band.
//
// A non-positive field takes its default, so a partially configured policy cannot disable widening
// or settle instantly by accident.
type FailoverPolicy struct {
	// Disabled keeps the group's historical behaviour: every member is probed every round and the
	// policy never selects. It is the zero-value-compatible default until the configuration window
	// supplies the fields that select this policy.
	Disabled bool
	// WidenAfter is how many consecutive rounds the stable member may fail (or be missing) before
	// the probe set widens to every member.
	WidenAfter int
	// SettleAfter is how many consecutive rounds a challenger must beat the stable member by more
	// than ToleranceMs before it replaces it.
	SettleAfter int
	// RecoverAfter is how many consecutive healthy rounds the stable member needs, while the set is
	// widened, before probing narrows back to it.
	RecoverAfter int
	// ToleranceMs is the delay margin a challenger must beat by. It mirrors the group's tolerance
	// band: a node that is a few milliseconds better is not a reason to move traffic.
	ToleranceMs uint16
}

const (
	defaultFailoverWidenAfter   = 2
	defaultFailoverSettleAfter  = 3
	defaultFailoverRecoverAfter = 2
	defaultFailoverToleranceMs  = 50
)

func (p FailoverPolicy) normalized() FailoverPolicy {
	if p.WidenAfter <= 0 {
		p.WidenAfter = defaultFailoverWidenAfter
	}
	if p.SettleAfter <= 0 {
		p.SettleAfter = defaultFailoverSettleAfter
	}
	if p.RecoverAfter <= 0 {
		p.RecoverAfter = defaultFailoverRecoverAfter
	}
	if p.ToleranceMs == 0 {
		p.ToleranceMs = defaultFailoverToleranceMs
	}
	return p
}

// FailoverObservation is one member's result in one round.
type FailoverObservation struct {
	// Measured reports that the member was probed this round. A member that was not probed is not
	// a failure: it has no observation at all.
	Measured bool
	// OK reports that the probe succeeded.
	OK bool
	// DelayMs is the measured delay, valid when OK.
	DelayMs uint16
}

// FailoverDecision is one round's answer.
type FailoverDecision struct {
	// ProbeSet is the members to probe in the NEXT round, in the caller's member order.
	ProbeSet []string
	// Selected is the member this policy would use for traffic, or empty when it has no
	// measurement-based answer and the caller must keep whatever it currently uses.
	Selected string
	// Changed reports that Selected differs from the previous round's answer. A caller uses it to
	// avoid re-dialling or re-announcing a selection that did not move.
	Changed bool
	// Widened reports that the probe set is currently every member rather than just the stable one.
	Widened bool
}

// FailoverState is the per-group state. The zero value is "no policy yet": with no configured
// thresholds every member stays in the probe set and nothing is selected, which is exactly the
// behaviour the group has today.
type FailoverState struct {
	// access serialises the policy state with the read-only snapshot methods. The health round
	// mutates the state, while a diagnostics/scale sampler may call Stable/Widened/Probes at the
	// same time; without this lock the state machine and its readers race even though each side is
	// individually correct.
	access sync.Mutex

	policy FailoverPolicy

	stable      string
	lastAnswer  string
	answered    bool
	widened     bool
	stableFails int
	stableOKRun int
	challenger  string
	challengerW int
}

// NewFailoverState starts a policy with the given thresholds and initial stable member (may be
// empty when the group has not selected yet).
func NewFailoverState(policy FailoverPolicy, initialStable string) *FailoverState {
	return &FailoverState{policy: policy.normalized(), stable: initialStable}
}

// Stable reports the member the policy currently treats as stable.
func (s *FailoverState) Stable() string {
	if s == nil {
		return ""
	}
	s.access.Lock()
	defer s.access.Unlock()
	return s.stable
}

// Disabled reports whether this state is the compatibility mode (probe everything, never select).
func (s *FailoverState) Disabled() bool {
	return s == nil || s.policy.Disabled
}

// Widened reports whether the probe set is currently every member.
//
// It is the observable half of a degradation episode: a caller that samples it per round can tell
// "the stable member is healthy and only it is probed" from "the set was widened because it is
// failing", without re-deriving it from the probe set.
func (s *FailoverState) Widened() bool {
	if s == nil {
		return false
	}
	s.access.Lock()
	defer s.access.Unlock()
	if s.policy.Disabled {
		return false
	}
	return s.widened
}

// Probes returns the members this policy wants measured in the CURRENT round. A disabled or
// uninitialised policy probes everything, which is the historical behaviour.
func (s *FailoverState) Probes(members []string) []string {
	if s == nil {
		return append([]string(nil), members...)
	}
	s.access.Lock()
	defer s.access.Unlock()
	if s.policy.Disabled || s.stable == "" || s.widened {
		return append([]string(nil), members...)
	}
	for _, member := range members {
		if member == s.stable {
			return []string{s.stable}
		}
	}
	return append([]string(nil), members...)
}

// AdoptStable seeds the stable member from the group's committed selection when the policy has none
// yet. It never overrides a stable member the policy already holds.
func (s *FailoverState) AdoptStable(tag string) {
	if s == nil || tag == "" {
		return
	}
	s.access.Lock()
	defer s.access.Unlock()
	if s.policy.Disabled {
		return
	}
	if s.stable == "" {
		s.stable = tag
	}
}

// Decide processes one round's observations and returns the next probe set and selection.
//
// members is the group's current member list in its own order. A tag that disappeared is dropped
// from the state; a tag whose OBJECT was replaced keeps its slot, because the policy is keyed by
// tag and a provider publication does not change a tag's meaning.
func (s *FailoverState) Decide(members []string, observations map[string]FailoverObservation) FailoverDecision {
	if s == nil {
		return FailoverDecision{ProbeSet: append([]string(nil), members...)}
	}
	s.access.Lock()
	defer s.access.Unlock()
	if s.policy.Disabled {
		return FailoverDecision{ProbeSet: append([]string(nil), members...)}
	}
	if len(members) == 0 {
		s.stable, s.challenger, s.lastAnswer = "", "", ""
		return FailoverDecision{ProbeSet: nil}
	}
	memberSet := make(map[string]bool, len(members))
	for _, member := range members {
		memberSet[member] = true
	}
	// A removed tag takes its slot with it; a replaced object keeps the tag and therefore the slot.
	if s.stable != "" && !memberSet[s.stable] {
		s.stable = ""
		s.stableFails = 0
		s.stableOKRun = 0
		s.widened = false
	}
	if s.challenger != "" && !memberSet[s.challenger] {
		s.challenger = ""
		s.challengerW = 0
	}

	// No stable member yet: the only honest answer is to measure everything and take the best
	// measurement as the first answer.
	if s.stable == "" {
		best, _, found := bestMeasured(members, observations, "")
		decision := FailoverDecision{ProbeSet: append([]string(nil), members...)}
		if found {
			decision.Selected = best
			s.stable = best
			s.stableFails = 0
			// The initial selection is NOT a degradation episode: the next round should narrow to
			// the member just chosen.
			s.widened = false
			s.answered = true
			s.lastAnswer = best
		}
		return decision
	}

	stableObservation, present := observations[s.stable]
	stableMeasured := present && stableObservation.Measured
	if stableMeasured && stableObservation.OK {
		return s.decideHealthyStable(members, observations, stableObservation)
	}
	return s.decideUnhealthyStable(members, observations, stableMeasured)
}

// decideHealthyStable is the power-saving path: the stable member answered, so the probe set is
// just the stable member unless a degradation episode is still running.
func (s *FailoverState) decideHealthyStable(members []string, observations map[string]FailoverObservation, stableObservation FailoverObservation) FailoverDecision {
	s.stableFails = 0
	s.stableOKRun++

	// A challenger can only be observed while the set is widened. It replaces the stable member
	// only when it has been clearly better for the configured number of rounds.
	if s.widened {
		challenger, delay, found := bestMeasured(members, observations, s.stable)
		if found {
			beats := s.beatsTolerance(delay, stableObservation.DelayMs)
			switch {
			case challenger != s.challenger:
				s.challenger = challenger
				if beats {
					s.challengerW = 1
				} else {
					s.challengerW = 0
				}
			case beats:
				// A win only counts while the stable member has been healthy for the WHOLE
				// comparison: a count accumulated while it was down was never compared against a
				// measurement of it, so the first healthy round restarts the window.
				if s.stableOKRun == 1 {
					s.challengerW = 1
				} else if s.challengerW < s.policy.SettleAfter {
					s.challengerW++
				}
			default:
				s.challengerW = 0
			}
			if s.challengerW >= s.policy.SettleAfter {
				// Migration with hysteresis satisfied: the challenger becomes the stable member and
				// probing narrows to it.
				s.stable = s.challenger
				s.challenger = ""
				s.challengerW = 0
				s.stableOKRun = 0
				s.widened = false
				return s.decision([]string{s.stable}, s.stable, false)
			}
		}
		if s.challengerW == 0 && s.stableOKRun >= s.policy.RecoverAfter {
			// Recovery is not one lucky probe: the stable member has answered for the configured
			// number of rounds and no challenger is beating it.
			s.widened = false
			s.challenger = ""
			s.challengerW = 0
			return s.decision([]string{s.stable}, s.stable, false)
		}
		return s.decision(append([]string(nil), members...), s.stable, true)
	}

	return s.decision([]string{s.stable}, s.stable, false)
}

// decideUnhealthyStable widens after the configured number of failed rounds and answers with the
// best measured alternative while the stable member is down.
func (s *FailoverState) decideUnhealthyStable(members []string, observations map[string]FailoverObservation, measured bool) FailoverDecision {
	s.stableOKRun = 0
	if !measured {
		// Not probed is not a failure: the caller's probe set did not include it, which can only
		// happen when a caller ignored the previous decision. Keep the state as it is.
		if s.widened {
			return s.decision(append([]string(nil), members...), s.stable, true)
		}
		return s.decision([]string{s.stable}, s.stable, false)
	}
	s.stableFails++
	if s.stableFails < s.policy.WidenAfter && !s.widened {
		// One lost round is not degradation. Measure the stable member again next round.
		return s.decision([]string{s.stable}, s.stable, false)
	}
	s.widened = true

	challenger, _, found := bestMeasured(members, observations, s.stable)
	if !found {
		// Nothing measured better: keep the current selection rather than flapping to an
		// unmeasured member.
		return s.decision(append([]string(nil), members...), s.lastAnswer, true)
	}
	if challenger == s.challenger {
		s.challengerW++
	} else {
		s.challenger = challenger
		s.challengerW = 1
	}
	if s.challengerW >= s.policy.SettleAfter {
		// The stable member has been down for the whole settle window and the alternative has held
		// the position. It is replaced even though the two were never compared: a dead member is
		// not a comparison partner, and leaving the probe set wide forever is the power cost this
		// policy exists to avoid.
		s.stable = s.challenger
		s.challenger = ""
		s.challengerW = 0
		s.stableFails = 0
		s.widened = false
		return s.decision([]string{s.stable}, s.stable, false)
	}
	return s.decision(append([]string(nil), members...), challenger, true)
}

func (s *FailoverState) beatsTolerance(challengerDelay, stableDelay uint16) bool {
	return uint32(challengerDelay)+uint32(s.policy.ToleranceMs) < uint32(stableDelay)
}

func (s *FailoverState) decision(probeSet []string, selected string, widened bool) FailoverDecision {
	decision := FailoverDecision{
		ProbeSet: probeSet,
		Selected: selected,
		Widened:  widened,
	}
	if selected == "" {
		return decision
	}
	// The FIRST answer is not a change: there was nothing to re-dial or re-announce before it.
	decision.Changed = s.answered && selected != s.lastAnswer
	s.lastAnswer = selected
	s.answered = true
	return decision
}

// bestMeasured returns the fastest successful observation that is not the excluded member. Ties are
// broken by tag so the answer is deterministic.
func bestMeasured(members []string, observations map[string]FailoverObservation, exclude string) (string, uint16, bool) {
	best := ""
	var bestDelay uint16
	found := false
	for _, member := range members {
		if member == exclude {
			continue
		}
		observation, measured := observations[member]
		if !measured || !observation.OK {
			continue
		}
		if !found || observation.DelayMs < bestDelay || (observation.DelayMs == bestDelay && member < best) {
			best = member
			bestDelay = observation.DelayMs
			found = true
		}
	}
	return best, bestDelay, found
}
