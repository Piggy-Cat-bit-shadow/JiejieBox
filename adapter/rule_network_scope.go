package adapter

// RuleNetworkScope is what a rule's match predicate says about the NETWORK dimension of a flow,
// asked statically - before any traffic exists, and without evaluating the rule.
//
// # Why this type exists
//
// The start-time path validator has to answer "which networks can a flow entering this outbound
// still be" (common/routeflow). That question is decided by the routing rules, and the only correct
// source for it is the COMPILED rule, because the compiled rule is what the runtime router actually
// evaluates: a summary taken from the configuration JSON would be a second routing engine, and the
// two would drift silently. So each compiled rule reports how it narrows the network dimension, and
// the analyzer composes those reports with boolean semantics rather than re-implementing matching.
//
// # The three answers, and why all three are needed
//
// For one network N, a predicate is one of:
//
//   - RuleMatchNever: N can never reach anything past this rule, because the predicate provably
//     rejects an N flow. A `network: udp` item inside a rule that is otherwise unconditional is
//     the ordinary case.
//   - RuleMatchAlways: the predicate provably accepts EVERY N flow. Only then may N be removed
//     from the set of networks still looking for a terminal rule.
//   - RuleMatchUnknown: the predicate may or may not accept an N flow, and this package cannot
//     prove which. A domain, process, port or rule-set condition is the ordinary case.
//
// The distinction between Always and Unknown is the whole point. Reading Unknown as Always is the
// false-positive direction - it lets the analyzer claim a protocol is filtered when a rule only
// filters part of it. Reading Always as Unknown is the false-negative direction - it lets a real
// incompatibility through. Neither is acceptable, so a caller that only has a boolean match result
// cannot answer this question and must say Unknown.
//
// # What this type deliberately does NOT model
//
// It is not a general predicate summary. It answers one question about one dimension, so a rule
// that is Unknown here is still fully evaluated at run time by its own Match. Nothing in the
// runtime path consults this type; it exists only for the start-time analysis.
type RuleNetworkScope struct {
	// TCP is what the predicate says about a TCP flow.
	TCP RuleMatch
	// UDP is what the predicate says about a UDP flow. It is a separate field rather than a loop
	// because the network dimension has exactly these two members everywhere in this tree - see
	// common/physicalpath's decidedNetworks, which drops icmp for the same reason: an icmp flow is
	// carried by a flow port rather than dialled, so no outbound carries it and a rule naming it
	// says nothing about a dialable network.
	UDP RuleMatch
	// Decided reports whether this scope carries any information at all. A rule whose scope is
	// undecided contributes nothing and must not be read as "matches nothing".
	Decided bool
}

// RuleNetworkScoper is the static half of a rule's match predicate.
//
// It is declared here rather than in the rule package so that a consumer of the ANALYSIS - the
// start-time path validator - can ask the question through interfaces this package already owns,
// and so a rule implementation outside this tree can answer it without importing the parser.
type RuleNetworkScoper interface {
	NetworkScope() RuleNetworkScope
}

// RuleScopeOf reports what a compiled rule says about the network dimension.
//
// A rule that does not implement RuleNetworkScoper - a rule type this tree does not ship, or a
// headless rule built by an embedder - answers Unknown, which adds no refusal. Reading an
// unrecognised rule as a network filter would let a single unsummarised rule delete a protocol
// from the analysis.
//
// A nil rule also answers Unknown rather than panicking: the caller's rule list is configuration
// and the analysis must not be the thing that turns a malformed entry into a crash at start.
func RuleScopeOf(rule Rule) RuleNetworkScope {
	if rule == nil {
		return ScopeUnknown()
	}
	if scoper, isScoper := rule.(RuleNetworkScoper); isScoper {
		return scoper.NetworkScope()
	}
	return ScopeUnknown()
}

// RuleMatch is one network's answer in a RuleNetworkScope.
type RuleMatch uint8

const (
	// RuleMatchUnknown means the predicate may or may not accept a flow of this network, and the
	// static summary cannot prove which. It is the zero value so that a scope nobody filled in
	// answers the safe way - "I do not know" - rather than the permissive way.
	RuleMatchUnknown RuleMatch = iota
	// RuleMatchNever means the predicate provably rejects every flow of this network.
	RuleMatchNever
	// RuleMatchAlways means the predicate provably accepts every flow of this network.
	RuleMatchAlways
)

// ScopeUnknown is the scope of a predicate this package cannot summarise. It is what a rule type
// that does not implement RuleNetworkScope answers, so an unrecognised rule contributes no
// information instead of being read as a filter.
func ScopeUnknown() RuleNetworkScope {
	return RuleNetworkScope{Decided: true, TCP: RuleMatchUnknown, UDP: RuleMatchUnknown}
}

// ScopeAlways is the scope of a predicate that constrains nothing: it matches every flow of either
// network. A rule with no match items at all answers this, and it is the shape a bare
// `{"outbound": "x"}` rule has.
func ScopeAlways() RuleNetworkScope {
	return RuleNetworkScope{Decided: true, TCP: RuleMatchAlways, UDP: RuleMatchAlways}
}

// ScopeNever is the scope of a predicate that can never match, whatever the flow. It is the
// identity the conjunction of items starts from.
func ScopeNever() RuleNetworkScope {
	return RuleNetworkScope{Decided: true, TCP: RuleMatchNever, UDP: RuleMatchNever}
}

// ScopeOf networks returns the scope of a predicate that accepts exactly these networks and
// provably rejects the others, which is the shape of an unconditional `network: [...]` condition.
//
// An EMPTY network list is deliberately ScopeAlways rather than ScopeNever: the condition is only
// compiled when the configuration named at least one network, and a list that ended up empty
// constrains nothing, so the permissive reading is the one that keeps a mis-parse from deleting a
// protocol from the analysis.
func ScopeOfNetworks(networks []string) RuleNetworkScope {
	if len(networks) == 0 {
		return ScopeAlways()
	}
	scope := RuleNetworkScope{Decided: true, TCP: RuleMatchNever, UDP: RuleMatchNever}
	for _, network := range networks {
		switch network {
		case "tcp":
			scope.TCP = RuleMatchAlways
		case "udp":
			scope.UDP = RuleMatchAlways
		}
	}
	return scope
}

// Match returns this scope's answer for the named network, and RuleMatchUnknown for any network
// outside the TCP/UDP dimension - a caller asking about icmp is asking a question this type does
// not answer, and "unknown" is the answer that adds no refusal.
func (s RuleNetworkScope) Match(network string) RuleMatch {
	switch network {
	case "tcp":
		if !s.Decided {
			return RuleMatchUnknown
		}
		return s.TCP
	case "udp":
		if !s.Decided {
			return RuleMatchUnknown
		}
		return s.UDP
	default:
		return RuleMatchUnknown
	}
}

// MatchesBeyondNetwork reports whether this scope PROVES that a flow reaching the rule can get
// past its own network condition - that is, whether the scope is Always for at least one network
// this package decides about.
//
// It is used for one thing: deciding whether a rule that matched "somewhere" could match every
// remaining flow of a network. A scope that is Unknown for both networks answers false.
func (s RuleNetworkScope) MatchesBeyondNetwork() bool {
	return s.TCP == RuleMatchAlways || s.UDP == RuleMatchAlways
}

// And returns the scope of "this AND other", which is the composition a rule's item list uses:
// every item must match, so a network is rejected as soon as either side rejects it, and is proven
// accepted only when both sides prove it.
func (s RuleNetworkScope) And(other RuleNetworkScope) RuleNetworkScope {
	if !s.Decided {
		return other
	}
	if !other.Decided {
		return s
	}
	return RuleNetworkScope{
		Decided: true,
		TCP:     andMatch(s.TCP, other.TCP),
		UDP:     andMatch(s.UDP, other.UDP),
	}
}

// Or returns the scope of "this OR other", which is the composition a logical `mode: or` rule
// uses: a network is proven accepted as soon as either side proves it, and is rejected only when
// both sides reject it.
func (s RuleNetworkScope) Or(other RuleNetworkScope) RuleNetworkScope {
	if !s.Decided {
		return other
	}
	if !other.Decided {
		return s
	}
	return RuleNetworkScope{
		Decided: true,
		TCP:     orMatch(s.TCP, other.TCP),
		UDP:     orMatch(s.UDP, other.UDP),
	}
}

// Not returns the scope of the inverted predicate.
//
// Unknown stays Unknown, because "I cannot prove the predicate rejects this flow" is not a proof
// that it accepts it. Never and Always swap, because for THESE two values the statement is
// genuinely about every flow of the network: "every UDP flow is rejected here" inverts to "every
// UDP flow is accepted here".
func (s RuleNetworkScope) Not() RuleNetworkScope {
	if !s.Decided {
		return s
	}
	return RuleNetworkScope{
		Decided: true,
		TCP:     notMatch(s.TCP),
		UDP:     notMatch(s.UDP),
	}
}

func andMatch(left RuleMatch, right RuleMatch) RuleMatch {
	if left == RuleMatchNever || right == RuleMatchNever {
		return RuleMatchNever
	}
	if left == RuleMatchAlways && right == RuleMatchAlways {
		return RuleMatchAlways
	}
	return RuleMatchUnknown
}

func orMatch(left RuleMatch, right RuleMatch) RuleMatch {
	if left == RuleMatchAlways || right == RuleMatchAlways {
		return RuleMatchAlways
	}
	if left == RuleMatchNever && right == RuleMatchNever {
		return RuleMatchNever
	}
	return RuleMatchUnknown
}

func notMatch(match RuleMatch) RuleMatch {
	switch match {
	case RuleMatchNever:
		return RuleMatchAlways
	case RuleMatchAlways:
		return RuleMatchNever
	default:
		return RuleMatchUnknown
	}
}
