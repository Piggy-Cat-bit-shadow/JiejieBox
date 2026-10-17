package dialer

import (
	"net/netip"

	C "github.com/sagernet/sing-box/constant"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
)

// admissionPolicy is ONE flow's immutable answer to a single question:
//
//	"may this flow spend an attempt on this address?"
//
// It is the Candidate Admission Policy of the network-capability model, and it exists because that
// question was answered in several places from several readings of the world, at several instants.
//
// # What it is made of, and why each term is here
//
//	strategy    the operator's family policy, resolved once (Layer 0 - configuration, not a fact about
//	            the device, and it survives every network generation)
//	localFamily Phase 01's published capability SNAPSHOT, together with the network ownership that makes
//	            it meaningful (Layer 1 - a value, never a probe)
//
// The two are the whole of the decision. Everything an operator sees as "this flow was refused" is
// one of these two facts, and the model keeps them apart on purpose: a configuration decision and a
// property of the machine have different remedies, and collapsing them sends the operator to look at
// the wrong thing. See admissionVerdict.
//
// # Why it is a value and not an object with behaviour
//
// It does not listen to the network, own a goroutine, arm a timer, or refresh anything. A flow captures
// one and reads it until the flow ends; a network transition does not rewrite it - it cancels the stale
// SETUP, and the next flow captures a new one. That is the boundary the whole phase is about: the
// transition acts on the lifecycle, never on the decision.
//
// # Why the zero value is the permissive one
//
// Every field defaults to a state that refuses nothing: DomainStrategyAsIS admits both families, and the
// zero localFamilyPolicy carries Unknown capability with no ownership predicate, which cannot produce a
// hard refusal. So a policy nobody captured - a test double, a dialer built without a network source - is
// a no-op policy, and every consumer keeps its previous behaviour rather than losing a family to a struct
// someone forgot to fill in.
type admissionPolicy struct {
	strategy    C.DomainStrategy
	localFamily localFamilyPolicy
}

// captureAdmissionPolicy reads the two terms as ONE observation, for one flow.
//
// It is the only place a production per-flow policy is made. The strategy comes from
// effectiveFamilyStrategy (memoised per dialer, because it is configuration), and the capability term
// comes from dialerLocalFamilyPolicy, which walks the chain to the ONE owner that publishes Phase 01's
// snapshot. Both are pure reads: no syscall, no lock, no allocation.
func (d *resolveDialer) captureAdmissionPolicy() admissionPolicy {
	return admissionPolicy{
		strategy:    d.effectiveFamilyStrategy(),
		localFamily: d.dialerLocalFamilyPolicy(),
	}
}

// newAdmissionPolicyForTest builds a policy from its two terms, for tests and benchmarks that hold a
// verdict with no dialer to ask.
func newAdmissionPolicyForTest(strategy C.DomainStrategy, localFamily localFamilyPolicy) admissionPolicy {
	return admissionPolicy{strategy: strategy, localFamily: localFamily}
}

// generation is the network generation this policy's verdict belongs to.
//
// It is the capability's own ownership epoch, not a second reading of the manager, so a policy and the
// scheduler and the family health namespace that come out of it cannot disagree - see
// DefaultDialer.newDualStackScheduler.
func (p admissionPolicy) generation() uint64 {
	return p.localFamily.ownership.epoch
}

// strategyAllows reports whether the OPERATOR's policy admits this address.
//
// Only the two strict strategies exclude; AsIS and both preferences admit every family, so the answer is
// true for them without looking at the address.
func (p admissionPolicy) strategyAllows(address netip.Addr) bool {
	return AddressAllowedByStrategy(address, p.strategy)
}

// admits reports whether this flow may spend an attempt on this address: both terms, in one call.
//
// This is the single predicate the stage-specific gates ask. They stay separate - the initial filter, the
// recovery admission, the late-candidate guard, the scheduler's structural guard and the leaf socket gate
// all still exist, because each covers a different way a candidate can arrive - but they now ask ONE
// question of ONE value instead of each spelling the composition out again.
//
// A local-scope or site-local address is never refused for a global reason: localFamily.hardUndialable
// answers that, and it is the only place scope is consulted.
func (p admissionPolicy) admits(address netip.Addr) bool {
	if !address.IsValid() {
		return true
	}
	if !p.strategyAllows(address) {
		return false
	}
	return !p.localFamily.hardUndialable(address)
}

// restrictsNothing reports whether this policy cannot refuse anything at all, so a caller can skip the
// work of examining a list.
//
// It must agree with admits exactly: a fast path that said "nothing is restricted" while admits refused
// something is the one disagreement these two functions cannot have.
func (p admissionPolicy) restrictsNothing() bool {
	if p.strategy == C.DomainStrategyIPv4Only || p.strategy == C.DomainStrategyIPv6Only {
		return false
	}
	return p.localFamily.restrictsNothing()
}

// admitCandidates applies the whole policy to a candidate list, and reports the three outcomes a caller
// has to be able to tell apart.
//
// # Why one function and not three inline copies
//
// This exact composition - strict-strategy filter, then local-family filter, then "was the list empty
// because the POLICY emptied it, or because there was nothing to offer" - was written by hand at four
// call sites: the serial stream branch, the parallel network entry point, its packet arm, and the
// single-destination planner. Two of them got it right and two did not, in different rounds, and each
// omission was a live policy violation rather than a theoretical one. It is one function now so the
// fifth call site cannot be the one that forgets.
//
// # Why the order is fixed
//
// Strategy first, then capability, because that is what the operator-facing explanation depends on: a
// list emptied by the operator's own configuration must read as a strategy exclusion, not as "the machine
// cannot dial this". Reversing the two would make an address that is both strategy-excluded and
// locally-unavailable be reported as the second, and the operator would go looking at their adapter.
//
// # Cost
//
// Both filters return the SAME slice when they remove nothing, so the ordinary dual-stack flow allocates
// nothing and copies nothing - which is why this is not "clone, then filter" (see withoutUndialable for
// the resolver-cache ownership argument that makes the no-mutation contract load-bearing).
func (p admissionPolicy) admitCandidates(addresses []netip.Addr) (admitted []netip.Addr, refused localFamilyRefusal, strategyExcludedAll bool) {
	strategyAdmitted := withoutFamilyExcluded(addresses, p.strategy)
	strategyExcludedAll = len(strategyAdmitted) == 0 && len(addresses) > 0
	refused = refusedCandidateIn(strategyAdmitted, p.localFamily)
	return withoutUndialable(strategyAdmitted, p.localFamily), refused, strategyExcludedAll
}

// lateAdmission is the predicate a LATE candidate must satisfy after the plan was built.
//
// A late candidate never passes through planCandidates, so it needs a structural guard; this is the
// strategy half of it, and the scheduler applies the capability half itself because it holds the same
// policy. A preference admits both families and returns nil, so the hot path pays nothing - the guard is
// only armed where a forgotten filter would put the forbidden family into a race.
func (p admissionPolicy) lateAdmission() func(netip.Addr) bool {
	if p.strategy != C.DomainStrategyIPv4Only && p.strategy != C.DomainStrategyIPv6Only {
		return nil
	}
	strategy := p.strategy
	return func(address netip.Addr) bool {
		return AddressAllowedByStrategy(address, strategy)
	}
}

// emptiedListRefusal renders the error for a candidate list the policy emptied, or nil.
//
// # Three outcomes, three errors, and only the caller knows the shape
//
// The caller supplies the destination, because the message names it and only the caller has it; this
// supplies the VERDICT, because that is the part that must not be re-derived. A list emptied by the
// operator's policy and a list emptied by the machine are different facts with different remedies, and a
// list that was empty to begin with is neither - "nothing was offered" must not be reported as a refusal,
// or the package invents a policy decision the policy never made.
//
// # Why it is one function
//
// This three-way question decides which of three errors a caller returns, and it had been written by hand
// at four arms of this package. The two that were written first were correct; the arms added later were
// not, and the packet arm below was missing the whole decision - measured, with `ipv4_only` and a UDP flow
// to a hostname, a socket was created for the family the operator forbade. A composition a caller can copy
// is a composition a caller can forget; it is one function now so the fifth arm cannot be the one that
// forgets.
func (p admissionPolicy) emptiedListRefusal(destination M.Socksaddr, admitted []netip.Addr, refused localFamilyRefusal, strategyExcludedAll bool) error {
	if len(admitted) > 0 {
		return nil
	}
	if refused.refused() {
		return localFamilyRefusalError(refused, p.localFamily)
	}
	if strategyExcludedAll {
		return E.New("destination ", destination, " excluded by strategy ", strategyName(p.strategy),
			": every candidate belongs to the family this policy forbids")
	}
	return nil
}

// admissionVerdict is why a flow refused one address, or that it did not.
//
// It exists so the DECISION and the EXPLANATION cannot come from different places. Before this, the gate
// asked the capability policy and the explanation was assembled at each call site from the strategy and
// the family, which is how a refusal ends up naming a rule that did not fire.
type admissionVerdict uint8

const (
	// admissionAllowed means no term of the policy refused this address.
	admissionAllowed admissionVerdict = iota
	// admissionStrategyExcluded means the operator's family policy forbids this address's family. A
	// configuration decision.
	admissionStrategyExcluded
	// admissionNoLocalFamily means the machine has no addressing for this address's family anywhere on
	// the local dial path. A property of the device.
	admissionNoLocalFamily
	// admissionNoGlobalEgress means the machine HAS IPv6 addressing and nothing routes it to a global
	// IPv6 destination. Also a property of the device, and a different one: its remedy is the route, not
	// the address.
	admissionNoGlobalEgress
)

func (v admissionVerdict) String() string {
	switch v {
	case admissionAllowed:
		return "allowed"
	case admissionStrategyExcluded:
		return "strategy-excluded"
	case admissionNoLocalFamily:
		return "no-local-family"
	case admissionNoGlobalEgress:
		return "no-global-egress"
	default:
		return "invalid"
	}
}

// verdict reports why this flow may not spend an attempt on the address, or admissionAllowed.
//
// The strategy term is asked FIRST, for the same reason admitCandidates filters in that order: a
// configuration decision is what the operator can act on, so it must not be reported as a device
// property.
func (p admissionPolicy) verdict(address netip.Addr) admissionVerdict {
	if !address.IsValid() {
		return admissionAllowed
	}
	if !p.strategyAllows(address) {
		return admissionStrategyExcluded
	}
	if !p.localFamily.hardUndialable(address) {
		return admissionAllowed
	}
	// The address WAS refused by the capability term, so it is a global-scope one (local and site
	// destinations are never refused there). Either the family does not exist, or - for IPv6 - it exists
	// and nothing routes it out. The two have different remedies, so they get different verdicts.
	if p.localFamily.capability.HardUnavailable(address.Unmap().Is4()) {
		return admissionNoLocalFamily
	}
	return admissionNoGlobalEgress
}

// refusalError renders the verdict as an error, or nil when the address is admitted.
//
// It allocates, so it belongs on the refusal path only: the hot path asks admits or verdict, never this.
// The message is built here rather than logged, so a caller can wrap it with the destination, the
// outbound and the flow shape it was about - which is the layer that knows them.
func (p admissionPolicy) refusalError(address netip.Addr) error {
	switch p.verdict(address) {
	case admissionAllowed:
		return nil
	case admissionStrategyExcluded:
		return E.New("destination ", address, " excluded by strategy ", strategyName(p.strategy))
	case admissionNoLocalFamily:
		return hardUnavailableError(classifyAddress(address))
	default:
		// Only a refused GLOBAL IPv6 destination reaches here: the family exists and the egress term is
		// what refused it. See localFamilyPolicy.hardUndialableError, which makes the same distinction
		// from a policy that has no strategy term.
		return errLocalIPv6EgressUnavailable
	}
}
