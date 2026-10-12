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
// `route.final` receives every flow no rule matched, and this function knows exactly which networks
// those are: they are the residual it carries at the end. The final outbound is therefore charged
// with that residual, as a PROVEN demand, because the sink is definite - `Router.matchRule` falls
// through to `outbound.Default()` for a flow that matched no rule, and `final` is what that resolves
// to when it is set.
//
// It used to be left unproven deliberately, so the validator fell back to what the final outbound
// advertises. That fallback cannot see the rules, and it refuses configurations that are precise:
// MEASURED, `{network: udp, action: reject}` with a tcp-only `final` has a residual of tcp alone and
// is legal, while the advertised fallback charged both networks. Charging a network the rules
// PROVABLY consumed asserts reachability they contradict, so the residual is the only reading
// consistent with the rest of this analysis.
//
// An empty finalOutbound changes nothing: when `final` is unset the router falls through to a
// `direct` outbound created at start, which carries both networks, so no demand can be proved about
// it and the caller's fallback remains the right answer.
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
			if !terminates(action) {
				// The router does NOT stop here, so this network still reaches every later
				// rule. Charging nothing and consuming nothing is the only sound answer.
				continue
			}
			// The decision ends here, so the network cannot reach a later rule; when the
			// action names no outbound it is simply not attributed to one. This is the half
			// the configuration-text scan could not see at all.
			live[network] = false
			if tag != "" {
				demand.Proven[tag] = appendNetwork(demand.Proven[tag], network)
			}
		}
	}
	for _, network := range networksForAnalysis {
		if live[network] {
			demand.Unclaimed = append(demand.Unclaimed, network)
		}
	}
	// `route.final` receives every flow no rule matched, and "every flow no rule matched" is not a
	// mystery: for each network it is exactly the residual this walk has been carrying. So the final
	// outbound is charged with that residual, and it is charged as PROVEN, because the sink is
	// definite - `Router.matchRule` falls through to `outbound.Default()` for a flow that matched no
	// rule, and `final` is what that resolves to when it is set.
	//
	// # Why this is not the "both networks" reading the old design refused
	//
	// It used to be left unproven on purpose, so the validator fell back to what the final outbound
	// advertises. That fallback cannot see the rules, and the two answers differ exactly where the
	// configuration is precise: with `{network: udp, action: reject}` and a tcp-only `final`, the
	// residual is tcp ALONE and the configuration is legal, while the advertised fallback charged
	// both networks and refused it. MEASURED - that configuration is the one the false-deny report
	// carried.
	//
	// The narrower reading is also the only one consistent with the rest of this analysis: a network
	// a rule PROVABLY consumed cannot arrive at `final`, and charging it anyway asserts reachability
	// the rules contradict. Nothing here is inferred from what the object advertises; the residual is
	// computed from the rules.
	//
	// A tag that does not resolve is left to the existence check rather than invented here.
	if finalOutbound != "" {
		for _, network := range demand.Unclaimed {
			demand.Proven[finalOutbound] = appendNetwork(demand.Proven[finalOutbound], network)
			demand.Possible[finalOutbound] = appendNetwork(demand.Possible[finalOutbound], network)
		}
		demand.Unclaimed = nil
	}
	return demand
}

// terminates reports whether the runtime router STOPS at this action, which is what decides whether
// a network can still reach the rules below it.
//
// # Why this is not adapter.IsFinalAction
//
// `IsFinalAction` answers a different question - "does this action end the flow's handling" - and
// two of its answers are wrong for THIS one. It is a property of the action alone, while whether the
// router breaks out of its loop depends on the action AND, for `bypass`, on whether the rule named
// an outbound.
//
// The authority is `Router.matchRule` (route/route.go), which breaks out for exactly:
//
//	route, reject, hijack-dns              - unconditionally
//	bypass                                 - ONLY when the action names an outbound;
//	                                         `bypass` with no outbound `continue match`es
//
// and falls through for `route-options`, `direct`, `sniff`, `resolve` and `evaluate`. `route-options`
// and `direct` are the two that a naive reading gets wrong, and getting them wrong is a FALSE
// NEGATIVE rather than a cosmetic difference: MEASURED, with a first rule `{"action": "direct"}`
// followed by `{network: udp, outbound: tcp-only}`, treating `direct` as terminal recorded udp as
// consumed and the configuration STARTED, while at run time the udp flow falls through to the
// tcp-only outbound and fails there. `{"action": "route-options"}` and an outbound-less
// `{"action": "bypass"}` reproduce the same hole exactly.
//
// `reject` and `hijack-dns` terminate WITHOUT naming an outbound, which is why the termination
// decision cannot be derived from `deliveredTag` either: both must be consulted, and they answer
// different halves.
func terminates(action adapter.RuleAction) bool {
	switch typed := action.(type) {
	case *R.RuleActionRoute, *R.RuleActionReject, *R.RuleActionHijackDNS:
		return true
	case *R.RuleActionBypass:
		// An outbound-less bypass is NOT terminal: the router continues to the next rule, so
		// the network must stay available to it. Reading it as terminal would withhold a
		// protocol from the only rule able to serve it.
		return typed.Outbound != ""
	default:
		// route-options, direct, sniff, resolve, evaluate, and anything this tree does not
		// ship: an unrecognised action must not consume a protocol.
		return false
	}
}

// deliveredTag reports the outbound tag this action delivers a flow to, or "" when it delivers to
// no outbound at all.
//
// A `route` action with an empty outbound names no tag: the router hands such a flow to the
// default outbound, whose tag this analysis is not given, and inventing one would either charge
// the wrong object or require resolving the default here. The network is still consumed - see
// `terminates` - so the only thing an empty tag loses is the attribution.
//
// A `bypass` action names a tag in the same way. Its EMPTY case is a separate matter and is decided
// by `terminates`, because whether the router stops is the question that governs consumption.
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
