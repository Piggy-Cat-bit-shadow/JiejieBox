package rule

import (
	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
)

// RuleNetworkScoper is the static half of a rule's match predicate: what it says about the
// NETWORK dimension of a flow, without evaluating it.
//
// It exists so the start-time path validator can ask the COMPILED rule - the object the runtime
// router actually evaluates - instead of re-parsing the configuration, which would be a second
// routing engine that drifts from this one. See adapter.RuleNetworkScope for the three states and
// why all three are needed.
type RuleNetworkScoper interface {
	NetworkScope() adapter.RuleNetworkScope
}

// NetworkScopedItem is what a compiled rule ITEM answers about the network dimension, for the
// items that genuinely constrain nothing and can prove it.
//
// Only the network condition can NARROW this dimension, so NetworkItem is the one item in this
// tree that answers Never for a network. But an item that does not narrow it is not automatically
// harmless: a domain, port, process or rule-set condition is a further requirement the flow must
// satisfy, and whether it does is decided by the flow rather than by the network. This interface
// is therefore the way an item OPT IN to "I constrain nothing about the network" - an item that
// does not implement it is read as Unknown, which claims nothing.
type NetworkScopedItem interface {
	NetworkScope() adapter.RuleNetworkScope
}

// NetworkScopeOf reports what an adapter.Rule says about the network dimension.
//
// A rule type that does not implement RuleNetworkScoper answers "unknown", which adds no refusal:
// an unrecognised rule must never be read as a network filter. The concrete rules in this package
// both implement it, and a third-party rule that does not is simply not summarised.
func NetworkScopeOf(rule adapter.Rule) adapter.RuleNetworkScope {
	if scoper, isScoper := rule.(RuleNetworkScoper); isScoper {
		return scoper.NetworkScope()
	}
	return adapter.ScopeUnknown()
}

// NetworkScope reports what this rule's match predicate says about the network dimension.
//
// # How the answer is composed, and why an unrecognised item is UNKNOWN rather than neutral
//
// The predicate is the conjunction of the compiled item list, so its scope is the conjunction of
// the items' scopes, taken over the same slice `Match` iterates (abstractDefaultRule.allItems, the
// list every group and every item is appended to). An unconditional `network: [udp]` item makes
// the whole rule Never for TCP whatever else the rule names, because the conjunction has a Never
// in it - which is exactly right, and it is what makes a rule that pairs a network with other
// conditions still provable in the REJECTING direction.
//
// The identity the conjunction starts from is Always, and an item that does not implement
// NetworkScopedItem contributes UNKNOWN.
//
// # Why that second half is not a detail
//
// It used to contribute Always, and the consequence was a false PROOF: a rule whose only item is
// `domain: [example.com]` was reported as "this predicate matches every flow of either network",
// so a terminal route through it was read as delivering BOTH networks - even though the rule says
// nothing about the network at all. MEASURED, with a logical rule wrapping exactly that condition
// and routing to a TCP-only outbound: `scope={decided=true tcp=Always udp=Always}` and the demand
// became `{tcp,udp}`, which refused a configuration that has always started. A condition that
// constrains something OTHER than the network cannot prove anything about the network, so it must
// be Unknown.
//
// A rule with NO items at all matches everything - `Match` special-cases the empty slice - so its
// scope is Always. That is the shape of a bare `{"outbound": "x"}` rule, and reading it as
// "matches nothing" would delete a catch-all from the analysis.
//
// The groups `Match` evaluates separately - evaluateGroups, which owns the source/destination
// address and port items - are deliberately NOT read here. They are network-agnostic, so they
// contribute Unknown to this dimension whether or not they are satisfied, which is the same answer
// their members give individually.
//
// # Inversion
//
// `invert` complements the predicate. It is applied last, over the whole composed scope, because
// that is where Match applies it: an inverted rule with an unconditional `network: [udp]`
// condition provably matches EVERY tcp flow, and never a udp one. An item that is Unknown for a
// network stays Unknown under inversion - "I cannot prove this condition rejects tcp flows" is not
// a proof that it accepts them.
func (r *abstractDefaultRule) NetworkScope() adapter.RuleNetworkScope {
	if len(r.allItems) == 0 {
		return invertScope(adapter.ScopeAlways(), r.invert)
	}
	scope := adapter.ScopeAlways()
	for _, item := range r.allItems {
		scoped, isScoped := item.(NetworkScopedItem)
		if !isScoped {
			scope = scope.And(adapter.ScopeUnknown())
			continue
		}
		scope = scope.And(scoped.NetworkScope())
	}
	return invertScope(scope, r.invert)
}

// NetworkScope reports what this logical rule's match predicate says about the network dimension.
//
// # Why the composition is a real AND/OR rather than "unknown"
//
// A logical rule is a tree over the same conditions a leaf rule holds, so its network behaviour is
// decided by the tree: `mode: and` can only narrow what its sub-rules accept, and `mode: or` can
// only widen it. Composing the sub-scopes with And/Or is therefore strictly more informative than
// answering Unknown for the whole tree, and it is still sound - an Unknown sub-rule keeps its
// parent Unknown in the direction that matters, so nothing is claimed that a sub-rule cannot
// prove.
//
// The `deferredGroups` bookkeeping in abstractLogicalRule.Match belongs to the destination
// address/cidr items, which are network-agnostic, so it does not change this dimension's answer:
// the same tree accepts the same networks either way.
//
// A logical rule with no sub-rules follows this rule's own Match exactly rather than a guess: the
// `and` branch starts from `matched = true` over an empty loop and therefore answers TRUE, and the
// `or` branch starts from `matched = false` and answers FALSE. The composition below starts from
// the same identities (ScopeAlways for `and`, ScopeNever for `or`), so the two agree by
// construction rather than by coincidence.
func (r *abstractLogicalRule) NetworkScope() adapter.RuleNetworkScope {
	var scope adapter.RuleNetworkScope
	if r.mode == C.LogicalTypeOr {
		scope = adapter.ScopeNever()
		for _, subRule := range r.rules {
			scope = scope.Or(NetworkScopeOfHeadless(subRule))
		}
	} else {
		scope = adapter.ScopeAlways()
		for _, subRule := range r.rules {
			scope = scope.And(NetworkScopeOfHeadless(subRule))
		}
	}
	return invertScope(scope, r.invert)
}

// NetworkScopeOfHeadless reports what a nested rule says about the network dimension.
//
// A nested rule is an adapter.HeadlessRule rather than an adapter.Rule - the parser builds it with
// the action stripped - so it cannot be reached through NetworkScopeOf. Both concrete rule types
// in this tree implement RuleNetworkScoper, and a nested rule that does not answers Unknown.
func NetworkScopeOfHeadless(rule adapter.HeadlessRule) adapter.RuleNetworkScope {
	if scoper, isScoper := rule.(RuleNetworkScoper); isScoper {
		return scoper.NetworkScope()
	}
	return adapter.ScopeUnknown()
}

// NetworkScope reports this item's answer: the networks it accepts, and a proof of rejection for
// the others.
//
// # Why an item can state Never, and what that costs to be wrong about
//
// NetworkItem.Match is `networkMap[metadata.Network]`, with no other input: metadata.Network is
// filled from the flow's own socket type and is never empty, so the item answers for EVERY flow of
// a network the same way. That is what makes "this network is rejected" a proof rather than a
// guess, and it is the only condition in this tree with that property. If a future item gained a
// network-dependent answer it would have to implement NetworkScopedItem itself, and the
// composition would pick it up here rather than need a second mechanism.
func (r *NetworkItem) NetworkScope() adapter.RuleNetworkScope {
	return adapter.ScopeOfNetworks(r.networks)
}

// invertScope applies `invert` to a composed scope, and is the single place inversion is applied
// so a rule type cannot forget it.
func invertScope(scope adapter.RuleNetworkScope, invert bool) adapter.RuleNetworkScope {
	if !invert {
		return scope
	}
	return scope.Not()
}
