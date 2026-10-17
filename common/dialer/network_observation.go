package dialer

import (
	"github.com/sagernet/sing-box/adapter"
)

// observationSource names which of the network manager's optional interfaces a dialer's observations
// come from.
//
// # Why the source is resolved ONCE, at construction
//
// It used to be resolved per call, independently, in two places: the ownership capture preferred
// `NetworkTransitionSnapshot` and fell back to the epoch and settled getters, while the family capture
// preferred `NetworkFamilyCapabilitySnapshot` and fell back to the ownership capture. Those two
// resolutions could therefore choose DIFFERENT interfaces for the same manager, and one of them could
// choose a narrower interface than the manager was willing to provide.
//
// That is not a bug in either resolution - each was correct for the fact it was after - it is the shape
// that produced the same defect twice, in two files, with two fixes: a manager that reports a fact and is
// then read through an accessor that cannot carry it silently loses the answer it gave. A test could only
// catch it by constructing a manager that reports an unusual SUBSET, because a manager reporting
// everything at once looks identical whether the resolution is right or wrong.
//
// With one resolution there is one place to get it right, and no call site can prefer a weaker source: the
// observer below is constructed from the strongest interface the manager implements, and every consumer of
// network facts - the family gate, the hand-over predicate, the candidate scheduler's health namespace -
// reads that one observer.
type observationSource uint8

const (
	// sourceNone means the manager reports nothing about the network lifecycle. Every consumer then
	// degrades to "no opinion", which is the same degradation every other optional capability here
	// takes: a dialer keeps its previous behaviour rather than losing connections because a struct was
	// left empty.
	sourceNone observationSource = iota
	// sourceStability is NetworkTransitionState alone: the network can say whether it is settled, and
	// nothing else.
	sourceStability
	// sourceGeneration is NetworkResetCounter alone: the network can say which reset generation it is
	// on, and nothing else.
	sourceGeneration
	// sourceTransitionPair is NetworkTransitionSnapshotter: generation and settled state as one
	// observation.
	sourceTransitionPair
	// sourceFamilyCapability is NetworkFamilyCapabilitySnapshotter: the pair plus the address-family
	// verdict, as one observation. It is the richest shape there is, and it is what the production
	// manager implements.
	sourceFamilyCapability
)

// networkObservation is one reading of the network, as a value.
//
// It is deliberately NOT the manager's own type: the point is that a dialer holds a reading, not a
// reference to something it could ask again later.
type networkObservation struct {
	// generation is the reset epoch, when the source can report one.
	generation uint64
	// settled is whether the network was settled when the reading was taken.
	settled bool
	// family is what the local dial path can do with each address family on this network.
	family adapter.NetworkFamilyCapability
}

// networkObserver is the resolved source of network observations for one dialer.
//
// # What it answers
//
//	observe()            one observation of the network
//	ownership(observed)  the captured generation, plus the means to ask whether it is still the live one
//
// Every consumer of network facts goes through these two, so "the same operation reads one observation" is
// a property of the wiring rather than a rule each call site has to remember.
//
// # Why the source's CAPABILITIES are stored, not just its function
//
// A zero is a legal value for every fact here: generation 0 is a real generation, and `false` is a real
// answer to "is the network settled". So "the source cannot report this" cannot be encoded in the value,
// and a predicate that consulted a term its source never provided would refuse every connection it was
// asked about.
type networkObserver struct {
	source observationSource
	// reportsGeneration reports whether the resolved source can answer "which generation am I on".
	reportsGeneration bool
	// reportsStability reports whether the resolved source can answer "is the network settled".
	reportsStability bool
	// capture reads the observation. Nil when no optional interface was available.
	capture func() networkObservation
}

// newNetworkObserver resolves the source of network observations from what the manager implements.
//
// The order is by what a source can DESCRIBE, strongest first, and it is the whole of the precedence:
//
//	adapter.NetworkFamilyCapabilitySnapshotter    generation + settled + verdict, one reading
//	adapter.NetworkTransitionSnapshotter          generation + settled, one reading
//	NetworkResetCounter + NetworkTransitionState  two readings
//	NetworkResetCounter                           generation only
//	NetworkTransitionState                        settled state only
//	nothing                                       no opinion
//
// A manager that implements more is always read through the richer interface. Reading it through a
// narrower one would drop a fact it did report, which is the defect this round exists to close.
func newNetworkObserver(manager adapter.NetworkManager) networkObserver {
	observer := networkObserver{}
	if manager == nil {
		return observer
	}
	if snapshotter, isSnapshotter := manager.(adapter.NetworkFamilyCapabilitySnapshotter); isSnapshotter {
		observer.source = sourceFamilyCapability
		observer.reportsGeneration = true
		observer.reportsStability = true
		observer.capture = func() networkObservation {
			generation, settled, capability := snapshotter.NetworkFamilyCapabilitySnapshot()
			return networkObservation{generation: generation, settled: settled, family: capability}
		}
		return observer
	}
	if snapshotter, isSnapshotter := manager.(adapter.NetworkTransitionSnapshotter); isSnapshotter {
		observer.source = sourceTransitionPair
		observer.reportsGeneration = true
		observer.reportsStability = true
		observer.capture = func() networkObservation {
			generation, settled := snapshotter.NetworkTransitionSnapshot()
			return networkObservation{generation: generation, settled: settled}
		}
		return observer
	}
	// The two narrower getters are resolved together rather than in a type switch, because a manager may
	// implement one, the other, or both - and a type switch can only match one case.
	counter, hasCounter := manager.(adapter.NetworkResetCounter)
	state, hasState := manager.(adapter.NetworkTransitionState)
	switch {
	case hasCounter && hasState:
		// Two readings of two interfaces. This shape can tear and there is nothing to be done about it
		// from here: it is the manager's own admission that it cannot produce the pair as one
		// observation. It is preserved because it is strictly better than having neither, and because a
		// manager that can do better implements one of the snapshotters above.
		observer.source = sourceGeneration
		observer.reportsGeneration = true
		observer.reportsStability = true
		observer.capture = func() networkObservation {
			return networkObservation{
				generation: counter.NetworkResetGeneration(),
				settled:    state.NetworkTransitionStable(),
			}
		}
	case hasCounter:
		observer.source = sourceGeneration
		observer.reportsGeneration = true
		observer.capture = func() networkObservation {
			return networkObservation{generation: counter.NetworkResetGeneration()}
		}
	case hasState:
		observer.source = sourceStability
		observer.reportsStability = true
		observer.capture = func() networkObservation {
			return networkObservation{settled: state.NetworkTransitionStable()}
		}
	default:
		observer.source = sourceNone
	}
	return observer
}

// observe returns one observation of the network from the resolved source.
func (o networkObserver) observe() networkObservation {
	if o.capture == nil {
		return networkObservation{}
	}
	return o.capture()
}

// ownership binds an observation to the means of asking whether it is still the live generation.
//
// # One formula, terms applied only when the source can report them
//
//	began settled?      a capture taken while the network was unsettled describes no stable network;
//	                    a later commit must not launder it into a valid one
//	settled now?        a transition that has begun since must not be handed a result
//	same generation?    a reset that has completed since must not be handed a result
//
// The first term is the one that is easy to omit and impossible to notice: a capture taken DURING a
// transition records the transition's OWN generation, so once it commits the generation comparison finds
// no change and the network reports settled - both later checks pass, and a connection produced while
// ownership was unresolved is handed over as though it belonged to the settled network.
//
// Every term is gated on what the resolved source can actually answer. A source that cannot report the
// settled state contributes no settled term rather than a fabricated one, and a source that cannot report
// a generation is not compared against a zero that never described anything.
func (o networkObserver) ownership(observed networkObservation) localDialOwnership {
	ownership := localDialOwnership{epoch: observed.generation}
	if o.capture == nil {
		// No source at all: the capture describes nothing, so the check does not apply and the value stays
		// ZERO rather than being filled with permissive-looking fields. That matters beyond taste - a
		// policy with no source is what a caller that could not reach an owner returns, and owns() already
		// reads a nil predicate as "unconstrained", so a zero value and an unconstrained one are the same
		// fact rather than two that have to be kept in step.
		return ownership
	}
	// A source that cannot report stability is not reporting "unsettled", so it must not be recorded as a
	// capture that began mid-transition: the predicate would then refuse every connection it was asked
	// about.
	ownership.startedStable = !o.reportsStability || observed.settled
	ownership.predicate = func() bool {
		if o.reportsStability && !observed.settled {
			return false
		}
		current := o.observe()
		if o.reportsStability && !current.settled {
			return false
		}
		if o.reportsGeneration && current.generation != observed.generation {
			return false
		}
		return true
	}
	return ownership
}
