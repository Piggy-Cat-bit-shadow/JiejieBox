package routeflow

import (
	"slices"
	"strings"

	"github.com/sagernet/sing-box/adapter"
	R "github.com/sagernet/sing-box/route/rule"
	N "github.com/sagernet/sing/common/network"
)

// Reach reports, per outbound tag, the networks a flow can still be when it arrives at that tag,
// derived from the compiled routing rules AND the order they are evaluated in.
//
// # The model this replaces
//
// The start-time path validator used to be given one set per tag, built by a helper that scanned
// the configuration and unioned the networks of every rule that NAMED the tag with an explicit
// network condition. That is not a routing model:
//
//   - order did not exist, so a rule delivering everything to a tag and a LATER `network: udp`
//     reject produced the same answer as the reject first - the reject could not save a flow that
//     an earlier rule had already sent away, and a reject placed after a catch-all was read as
//     though it had filtered the catch-all;
//   - a terminal action that is NOT a route - reject, hijack-dns, direct - was invisible, so the
//     protocols it provably consumes stayed in the requirement;
//   - only leaf rules were read, so a logical rule contributed nothing at all rather than the
//     AND/OR of its tree;
//   - `invert` was not read, so an inverted rule was indistinguishable from its opposite.
//
// # What this function computes
//
// The runtime router evaluates `rules` in order and stops at the first rule whose predicate matches
// AND whose action is terminal. For each network independently, this walks the same list and asks
// the same question, using each compiled rule's static network scope (adapter.RuleNetworkScope) in
// place of evaluating its predicate:
//
//   - a rule whose scope NEVER matches a network cannot consume that network, whatever its action;
//   - a rule whose scope ALWAYS matches a network consumes it, and its action decides where it
//     goes: a route names the tag, a bypass names the tag when it has one and otherwise falls
//     through exactly as the router does, and reject / hijack-dns / direct consume it without
//     delivering it to any outbound - so it is gone from every later rule's reach;
//   - a rule whose scope is UNKNOWN for a network may or may not consume it. Nothing is proved
//     about that network at that point, so it stays available to every later rule, and it is
//     reported as possibly reaching nothing rather than being attributed to a tag.
//
// # The UNKNOWN policy, stated where a reader of this file can check it
//
// An unknown predicate does NOT become a requirement for its tag, and it does NOT remove its
// network from the remaining set. Both halves matter:
//
//   - It does not become a requirement because a rule that merely MIGHT match would demand a
//     capability of an outbound the flow may never reach. That is the false-positive direction this
//     analysis exists to remove, and it is not hypothetical: `{network: tcp, domain: [...]}` is
//     UNKNOWN for TCP, and charging every such tag with TCP would refuse configurations that have
//     always started.
//   - It does not remove its network because "I cannot prove this rule rejects the flow" is not a
//     proof that it accepts it, and a later rule may still be reached.
//
// So an unknown rule is transparent in both directions: the flow may pass through it to any later
// rule, and no tag is charged for it. What the configuration loses is only the ability to PROVE a
// requirement through such a rule, and the validator's own fallback covers that case - a tag with
// nothing proven is checked against what its own reachable objects advertise between them
// (common/physicalpath's advertisedNetworks), which is where the UNKNOWN question is answered
// instead. Nothing here is a silent skip: the runtime dial path still refuses a flow the selected
// outbound cannot carry, and the dry run still validates existence, endpoints and grouping.
//
// A network that no rule proves anything about is absent from every map, whether because the rule
// list is empty or because every rule touching it is unknown. Absent means "nothing proven", never
// "nothing reaches it" - the two are distinguished by presence everywhere this value is consumed.
//
// # finalOutbound
//
// `route.final` receives every flow no rule matched, which is every network, so a configuration
// that sets it proves nothing about WHICH networks arrive - only that something does. It is
// therefore not attributed to a tag here. Reading it as "both networks reach final" would be a
// proof this function does not have, and it is the reading the validator's own delivery fallback
// exists to avoid. It is carried on the result for diagnostics only.
//
// # Cost
//
// O(rules x networks), with the network dimension fixed at two. No recursion over the rule list, no
// enumeration of rule combinations, and no evaluation of any predicate.
func Reach(rules []adapter.Rule, finalOutbound string) Demand {
	// live reports, per network, whether some flow of that network can still reach a later rule.
	// It starts true for both, because the router starts at rule 0 with the whole flow set.
	live := map[string]bool{N.NetworkTCP: true, N.NetworkUDP: true}
	demand := Demand{
		FinalOutbound: finalOutbound,
		Proven:        make(map[string][]string),
		Possible:      make(map[string][]string),
	}
	for _, compiled := range rules {
		if compiled == nil {
			continue
		}
		demand.Reached++
		// Determined from the compiled rule, which is the object the router evaluates - never
		// from the configuration JSON, whose summary would be a second routing engine.
		scope := adapter.RuleScopeOf(compiled)
		for _, network := range networksForAnalysis {
			if !live[network] {
				continue
			}
			switch scope.Match(network) {
			case adapter.RuleMatchNever:
				// The predicate provably rejects every flow of this network, so the router
				// never reaches this action with one. Nothing is consumed and nothing is
				// proven; the network stays available to later rules.
				continue
			case adapter.RuleMatchUnknown:
				// The predicate may match. This is NOT a consumption: no later rule can be
				// proven unreachable, so the network stays live and no tag is charged for it.
				continue
			}
			action := compiled.Action()
			if action == nil {
				// A rule with no action is not reachable in this tree - the parser always
				// builds one - and an unreadable rule must not consume a protocol.
				continue
			}
			tag := deliveredTag(action)
			if tag != "" {
				demand.Possible[tag] = appendNetwork(demand.Possible[tag], network)
			}
			if adapter.IsFinalAction(action) {
				// A route or bypass with an empty outbound still ends the decision, so the
				// network cannot reach a later rule; it is simply not attributed to a tag.
				// This is the half the configuration-text scan could not see at all.
				live[network] = false
				if tag != "" {
					demand.Proven[tag] = appendNetwork(demand.Proven[tag], network)
				}
			}
		}
	}
	for _, network := range networksForAnalysis {
		if live[network] {
			demand.Unclaimed = append(demand.Unclaimed, network)
		}
	}
	return demand
}

// deliveredTag reports the outbound tag this action delivers a flow to, or "" when it delivers to
// no outbound at all.
//
// A `route` action with an empty outbound names no tag: the router hands such a flow to the
// default outbound, whose tag this analysis is not given, and inventing one would either charge
// the wrong object or require resolving the default here. The network is still consumed - the
// `IsFinalAction` check does that - so the only thing an empty tag loses is the attribution.
//
// A `bypass` action is included because it is terminal and names a tag in the same way. An empty
// `bypass` is NOT terminal at all - the router continues to the next rule - and `IsFinalAction`
// cannot express that difference, so the tag is returned here and the caller's terminal check
// stays the single owner of the "does this end the decision" question.
func deliveredTag(action adapter.RuleAction) string {
	switch typed := action.(type) {
	case *R.RuleActionRoute:
		return typed.Outbound
	case *R.RuleActionBypass:
		return typed.Outbound
	default:
		return ""
	}
}

// appendNetwork adds a network to a list once, preserving first-seen order so a report reads in the
// order the configuration wrote its conditions.
func appendNetwork(networks []string, network string) []string {
	if slices.Contains(networks, network) {
		return networks
	}
	return append(networks, network)
}

// Demand is what a routing rule list proves about where flows can arrive.
//
// # Why Proven and Possible are separate
//
// The two answer different questions and a caller that collapses them cannot be right in both
// directions:
//
//   - Proven is what a rule with a PROVABLY matching predicate delivered through a terminal action.
//     It is the set a hard failure may be built on, and it is the set the start-time validator
//     uses.
//   - Possible adds a network that a terminal rule matched with a predicate that is only PARTLY
//     understood - a rule naming a network plus further conditions. It is strictly larger, and it
//     is carried so a diagnostic can say "this may arrive" without a requirement being built on
//     it. Reading it as a requirement is the false-positive direction the UNKNOWN policy above
//     rejects.
//
// Both are plain per-tag lists rather than sets: the network dimension is closed and tiny, a list
// is what the validator already takes, and first-seen order keeps a report readable.
type Demand struct {
	// Proven maps an outbound tag to the networks a rule provably delivers to it.
	Proven map[string][]string
	// Possible maps an outbound tag to the networks a terminal rule might deliver to it. Every
	// entry of Proven is also an entry here, so this is a superset and never a substitute.
	Possible map[string][]string
	// Unclaimed is the set of networks no rule proved a home for. It is the honest answer for a
	// configuration whose rules are all unknown, and it is what tells a caller to fall back to
	// what the objects themselves advertise.
	Unclaimed []string
	// FinalOutbound is `route.final`, carried for diagnostics so a report can name the outbound an
	// unmatched flow reaches without this type claiming to know which networks those are.
	FinalOutbound string
	// Reached is the number of rules this demand was computed over, for diagnostics.
	Reached int
}

// NetworksFor reports the networks a tag must be able to carry, or nil when nothing was PROVEN
// about it.
//
// It returns Proven and never Possible: a requirement is a statement the configuration supports,
// and an UNKNOWN predicate does not support one. A tag with nothing proven is checked by the
// caller against what its own objects advertise instead - see the UNKNOWN policy above for why
// that is the right place for the question and not a skip.
func (d Demand) NetworksFor(tag string) []string {
	if tag == "" || len(d.Proven) == 0 {
		return nil
	}
	return d.Proven[tag]
}

// String renders the demand as one line per tag, for a diagnostic that has to state what the ROUTES
// proved rather than what an entry point could theoretically produce.
func (d Demand) String() string {
	var builder strings.Builder
	tags := make([]string, 0, len(d.Possible))
	for tag := range d.Possible {
		tags = append(tags, tag)
	}
	slices.Sort(tags)
	for index, tag := range tags {
		if index > 0 {
			builder.WriteString("; ")
		}
		builder.WriteString(tag)
		builder.WriteString("=")
		builder.WriteString(strings.Join(d.Possible[tag], ","))
	}
	if len(d.Unclaimed) > 0 {
		if builder.Len() > 0 {
			builder.WriteString("; ")
		}
		builder.WriteString("unclaimed=")
		builder.WriteString(strings.Join(d.Unclaimed, ","))
	}
	return builder.String()
}

// networksForAnalysis is the closed network dimension this analysis decides about, kept in one
// place so a future member cannot be added to one loop and forgotten in another.
var networksForAnalysis = []string{N.NetworkTCP, N.NetworkUDP}

// DecidedNetworks reports the networks this analysis can decide about, in its own order.
//
// icmp is deliberately absent for the reason common/physicalpath gives: an icmp flow is carried by
// a flow port rather than dialled, so no outbound carries it and a rule naming it says nothing
// about a dialable network.
func DecidedNetworks() []string {
	return slices.Clone(networksForAnalysis)
}

// IsNetworkDecided reports whether this analysis decides about a network type.
func IsNetworkDecided(network string) bool {
	return slices.Contains(networksForAnalysis, network)
}
