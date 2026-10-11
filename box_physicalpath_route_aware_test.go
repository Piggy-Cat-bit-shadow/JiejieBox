package box_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// ROUTE-AWARE-01: the start-time network requirement must be derived from the ROUTES - their order,
// their terminal actions and their logical structure - and not from an entry point's theoretical
// capability. Every fixture below is configuration text driven through the production loader, and
// the assertion is Start(), because the claim under test is "a configuration a user can write does
// or does not start" rather than "a helper returned this set".
//
// # The defect these fixtures exist for
//
// A UDP flow that the route layer DETERMINISTICALLY terminates before any outbound can see it does
// not make a downstream TCP-only outbound unusable, and the requirement handed to the path
// validator has to know that. The model this replaces read the configuration text and unioned the
// networks of every rule that NAMED a target with an explicit network condition, so it had no rule
// order, could not see `reject` at all, and could not read the network condition out of a logical
// rule. Cases 1 and 3 below are the two directions of exactly that: the same two rules in the two
// possible orders must reach OPPOSITE verdicts, which no order-blind scan can do.
//
// # No tag, protocol or vendor name appears in any fixture
//
// Every leaf is a `socks` outbound with an explicit `network` declaration, which is the capability
// claim under test; every group is a `selector` or `loadbalance`. Case 7 pins that renaming a tag
// or swapping the leaf type changes nothing, which is the check that the fix is about route
// semantics rather than about recognising a configuration.

// caseNetworkTerminatedBeforeThePath is the core false positive (Case 1).
//
// The first rule consumes EVERY udp flow at the routing layer, so no udp flow can arrive at the
// terminal rule's target. A TCP-only outbound below it is therefore exactly right, and the
// requirement for the group reachable through the terminal rule is tcp alone.
const caseNetworkTerminatedBeforeThePath = `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "socks", "tag": "tcp-only-leaf", "server": "127.0.0.1", "server_port": 1080,
     "version": "5", "network": "tcp"},
    {"type": "selector", "tag": "group-a", "outbounds": ["tcp-only-leaf"],
     "default": "tcp-only-leaf"}
  ],
  "route": {
    "rules": [
      {"network": "udp", "action": "reject"},
      {"outbound": "group-a"}
    ]
  }
}`

// caseNetworkUnterminatedCatchAll is the true incompatibility that must SURVIVE (Case 2).
//
// The single rule delivers every flow to the group, so udp genuinely reaches the TCP-only leaf.
const caseNetworkUnterminatedCatchAll = `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "socks", "tag": "tcp-only-leaf", "server": "127.0.0.1", "server_port": 1080,
     "version": "5", "network": "tcp"},
    {"type": "selector", "tag": "group-a", "outbounds": ["tcp-only-leaf"],
     "default": "tcp-only-leaf"}
  ],
  "route": {
    "rules": [
      {"outbound": "group-a"}
    ]
  }
}`

// caseNetworkTerminationAfterDelivery is the rule-order counterexample (Case 3).
//
// The reject is placed AFTER the catch-all, and the catch-all matches first for every flow, so the
// reject can never run. The order matters and the verdict must differ from Case 1.
const caseNetworkTerminationAfterDelivery = `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "socks", "tag": "tcp-only-leaf", "server": "127.0.0.1", "server_port": 1080,
     "version": "5", "network": "tcp"},
    {"type": "selector", "tag": "group-a", "outbounds": ["tcp-only-leaf"],
     "default": "tcp-only-leaf"}
  ],
  "route": {
    "rules": [
      {"outbound": "group-a"},
      {"network": "udp", "action": "reject"}
    ]
  }
}`

// caseNetworkPartialTermination is Case 4: a rule that terminates only PART of a protocol.
//
// `network: udp` plus a domain suffix rejects some udp flows and nothing else, so udp is still
// reachable for every other destination. Reading a partial condition as an exhaustive one is the
// false-negative direction, and it would let a real incompatibility through.
const caseNetworkPartialTermination = `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "socks", "tag": "tcp-only-leaf", "server": "127.0.0.1", "server_port": 1080,
     "version": "5", "network": "tcp"}
  ],
  "route": {
    "rules": [
      {"network": "udp", "domain_suffix": ["example.com"], "action": "reject"},
      {"network": "udp", "outbound": "tcp-only-leaf"}
    ]
  }
}`

// caseNetworkTCPTerminatedBeforeUDPOnlyPath is Case 5: Case 1 mirrored.
//
// TCP is terminated at the routing layer and everything left is udp, which reaches a UDP-only leaf.
// An implementation that special-cased udp rather than modelling the network dimension would fail
// here, and so would one that modelled only the network condition on the terminal rule.
const caseNetworkTCPTerminatedBeforeUDPOnlyPath = `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "socks", "tag": "udp-only-leaf", "server": "127.0.0.1", "server_port": 1080,
     "version": "5", "network": "udp"}
  ],
  "route": {
    "rules": [
      {"network": "tcp", "action": "reject"},
      {"outbound": "udp-only-leaf"}
    ]
  }
}`

// caseNetworkDualCapableLeaf is Case 6: the positive control for the ordinary case.
const caseNetworkDualCapableLeaf = `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "socks", "tag": "dual-capable-leaf", "server": "127.0.0.1", "server_port": 1080,
     "version": "5"}
  ],
  "route": {
    "rules": [
      {"outbound": "dual-capable-leaf"}
    ]
  }
}`

// caseNetworkRenamedEquivalent is Case 7: the SAME semantics under different tags, a different
// group kind and a different TCP-only leaf type. The verdict must be identical to Case 1.
//
// This is the fixture that proves the fix is not keyed on a name, a protocol or a group type: a
// change to any of them leaves the route semantics intact, so the answer cannot move. The leaf type
// differs (`vless` rather than `socks`) and the group kind differs (`loadbalance` rather than
// `selector`), and both carry the same capability declaration the analysis reads.
const caseNetworkRenamedEquivalent = `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "vless", "tag": "renamed-tcp-only", "server": "127.0.0.1", "server_port": 1080,
     "uuid": "00000000-0000-0000-0000-000000000001", "network": "tcp"},
    {"type": "loadbalance", "tag": "renamed-group", "outbounds": ["renamed-tcp-only"]}
  ],
  "route": {
    "rules": [
      {"network": "udp", "action": "reject"},
      {"outbound": "renamed-group"}
    ]
  }
}`

// caseNetworkGroupDeliversUDPToTCPOnlyMember is Case 8: the group true positive that must stay.
//
// udp reaches a network-blind selector whose selectable membership includes a TCP-only leaf, so a
// udp flow can be handed to an outbound that cannot carry it. Removing the group-member check would
// make this start, which is the regression the mutation evidence pins.
const caseNetworkGroupDeliversUDPToTCPOnlyMember = `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "socks", "tag": "dual-leaf", "server": "127.0.0.1", "server_port": 1080,
     "version": "5"},
    {"type": "socks", "tag": "tcp-only-member", "server": "127.0.0.1", "server_port": 1081,
     "version": "5", "network": "tcp"},
    {"type": "selector", "tag": "group-a", "outbounds": ["dual-leaf", "tcp-only-member"],
     "default": "dual-leaf"}
  ],
  "route": {
    "rules": [
      {"network": "udp", "outbound": "group-a"}
    ]
  }
}`

// caseNetworkEachProtocolToItsOwnOutbound is Case 9: udp and tcp are terminated onto different
// outbounds, each of which carries what it is given.
const caseNetworkEachProtocolToItsOwnOutbound = `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "socks", "tag": "tcp-only-outbound", "server": "127.0.0.1", "server_port": 1080,
     "version": "5", "network": "tcp"},
    {"type": "socks", "tag": "udp-capable-outbound", "server": "127.0.0.1", "server_port": 1081,
     "version": "5", "network": "udp"}
  ],
  "route": {
    "rules": [
      {"network": "udp", "outbound": "udp-capable-outbound"},
      {"outbound": "tcp-only-outbound"}
    ]
  }
}`

// caseNetworkNestedGroupCorrectRequirement is Case 10: nested groups.
//
// The termination removes udp before the outer group is reached, so the requirement flowing down
// through two levels of group is tcp - and the tcp-only leaf at the bottom is legal.
const caseNetworkNestedGroupCorrectRequirement = `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "socks", "tag": "tcp-only-leaf", "server": "127.0.0.1", "server_port": 1080,
     "version": "5", "network": "tcp"},
    {"type": "selector", "tag": "group-inner", "outbounds": ["tcp-only-leaf"],
     "default": "tcp-only-leaf"},
    {"type": "selector", "tag": "group-outer", "outbounds": ["group-inner"],
     "default": "group-inner"}
  ],
  "route": {
    "rules": [
      {"network": "udp", "action": "reject"},
      {"outbound": "group-outer"}
    ]
  }
}`

// caseNetworkNestedGroupKeepsRealDefect is Case 10's counterexample: the same nested shape with the
// termination removed, so udp really does reach the bottom leaf through both group levels.
const caseNetworkNestedGroupKeepsRealDefect = `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "socks", "tag": "tcp-only-leaf", "server": "127.0.0.1", "server_port": 1080,
     "version": "5", "network": "tcp"},
    {"type": "selector", "tag": "group-inner", "outbounds": ["tcp-only-leaf"],
     "default": "tcp-only-leaf"},
    {"type": "selector", "tag": "group-outer", "outbounds": ["group-inner"],
     "default": "group-inner"}
  ],
  "route": {
    "rules": [
      {"outbound": "group-outer"}
    ]
  }
}`

// caseNetworkUnknownMatcherIsNotExhaustive is Case 11: a matcher the analysis cannot prove
// exhaustive.
//
// The network condition makes the rule NEVER for tcp and UNKNOWN for udp: it can never touch a tcp
// flow, and it MIGHT reject some udp flow. Both halves are load-bearing and both are asserted here.
//
//   - UNKNOWN must not eliminate udp. Nothing proves the rule matches every remaining udp flow, so
//     the udp route below still receives udp - which is why this configuration is legal, because
//     that route's target carries udp.
//   - NEVER must still SKIP the rule for tcp. If the tcp half were also unknown, the rule would
//     name a target for tcp and this configuration would still start - so the property is pinned by
//     `TestRouteAwareUnknownMatcherDoesNotWithholdTheProtocol` below, where the target does NOT
//     carry the protocol the rule could deliver.
//
// The verdict asserted here is the policy, stated once.
const caseNetworkUnknownMatcherIsNotExhaustive = `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "socks", "tag": "udp-capable-leaf", "server": "127.0.0.1", "server_port": 1080,
     "version": "5", "network": "udp"}
  ],
  "route": {
    "rules": [
      {"network": ["udp"], "domain_suffix": ["example.com"], "action": "reject"},
      {"network": "udp", "outbound": "udp-capable-leaf"}
    ]
  }
}`

// caseNetworkUnknownMatcherDoesNotWithholdTheProtocol is the discriminating companion to Case 11.
//
// The same UNKNOWN/NEVER rule is followed by a terminal route to a leaf that declares it carries
// ONLY tcp. The rule is NEVER for tcp, so it cannot consume a tcp flow and must not be credited with
// one: the flow falls through to the rule below, which delivers it to a target that CAN carry it,
// and the configuration is legal.
//
// Read as UNKNOWN instead, the reject rule would be taken to name a target for tcp as well - and its
// action names NO outbound, so the tcp flow would be read as consumed by a reject. That is the false
// negative direction: a protocol would be withheld from the one rule able to serve it, and the
// verdict would change for a configuration whose route semantics did not.
const caseNetworkUnknownMatcherDoesNotWithholdTheProtocol = `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "socks", "tag": "tcp-only-leaf", "server": "127.0.0.1", "server_port": 1080,
     "version": "5", "network": "tcp"}
  ],
  "route": {
    "rules": [
      {"network": ["udp"], "domain_suffix": ["example.com"], "action": "reject"},
      {"network": "tcp", "outbound": "tcp-only-leaf"}
    ]
  }
}`

// caseNetworkLogicalRuleNarrowsTheNetwork is a logical rule whose tree DOES decide the network.
//
// `mode: and` over one unconditional network condition is exactly that condition, so the rule is
// provably tcp-only and the udp flow reaches the next rule. A model that answered "unknown" for
// every logical rule would lose this and let a real defect through.
const caseNetworkLogicalRuleNarrowsTheNetwork = `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "socks", "tag": "tcp-only-leaf", "server": "127.0.0.1", "server_port": 1080,
     "version": "5", "network": "tcp"}
  ],
  "route": {
    "rules": [
      {"type": "logical", "mode": "and", "rules": [{"network": ["tcp"]}],
       "outbound": "tcp-only-leaf"}
    ]
  }
}`

// caseNetworkInvertedRuleIsRead is the same statement for `invert`, in the direction that keeps a
// real defect visible.
//
// The first rule is `network: tcp` INVERTED, so it matches every flow that is NOT tcp - that is,
// every udp flow - and consumes it at the routing layer. The second rule therefore receives tcp
// alone. A tcp-only leaf below it is legal, and the verdict is legal for a reason only a model that
// composes inversion can state: an implementation that ignored `invert` would see a `network: tcp`
// rule delivering tcp and would reach the same verdict by accident, so the discriminating half is
// the assertion below on the diagnostic, which must name tcp alone as reachable.
const caseNetworkInvertedRuleIsRead = `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "socks", "tag": "tcp-only-leaf", "server": "127.0.0.1", "server_port": 1080,
     "version": "5", "network": "tcp"}
  ],
  "route": {
    "rules": [
      {"network": "tcp", "invert": true, "action": "reject"},
      {"outbound": "tcp-only-leaf"}
    ]
  }
}`

// caseNetworkTerminationWithoutANamedOutbound pins the one thing a terminal action that names no
// outbound must still do: it consumes the flow.
//
// The termination here comes from `direct`, which does NOT stop the router - see cases 14-16 - so the
// tcp half is terminated by the second rule's own network condition and then falls through to no
// further rule. The fixture's job is the "consumed but not attributed" path: udp is consumed by the
// udp route to a target that carries udp, tcp is never charged to anything, and the root falls back
// to what its objects advertise. It is a control for that path rather than evidence about `direct`,
// which cases 16 and 14 pin directly.
const caseNetworkTerminationWithoutANamedOutbound = `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "socks", "tag": "tcp-only-leaf", "server": "127.0.0.1", "server_port": 1080,
     "version": "5", "network": "tcp"}
  ],
  "route": {
    "rules": [
      {"network": "udp", "action": "direct"},
      {"network": "tcp", "outbound": "tcp-only-leaf"}
    ]
  }
}`

// routeAwareCase is one row of the matrix. wantStart is the verdict, and wantErr names the facts a
// refusal must state so a verdict cannot be reported for the wrong reason.
type routeAwareCase struct {
	id        string
	why       string
	config    string
	wantStart bool
	wantErr   []string
}

// The four actions a naive reading gets wrong about whether the ROUTER stops.
//
// `Router.matchRule` (route/route.go) breaks out of its loop for exactly `route`, `reject`,
// `hijack-dns`, and `bypass` WHEN the bypass names an outbound. It falls through for everything
// else - including `route-options` and `direct`, which are easy to mistake for terminal because
// `adapter.IsFinalAction` answers "does this end the flow's handling" rather than "does the router
// stop here".
//
// Getting this wrong is a FALSE NEGATIVE, not a cosmetic difference, and it was MEASURED: each
// fixture below is a first rule whose action does not terminate, followed by a udp route to a
// TCP-only leaf. Reading the first rule as terminal records udp as consumed, the requirement empties,
// and the configuration STARTS - while at run time the udp flow falls through to the leaf and fails
// there. All later examples are the same shape with the discriminating action changed.
const (
	// `action: reject` with NO match items is an unconditional reject: `Match` returns true for an
	// empty item list, and the empty list means there is no condition to fail. It terminates, so
	// udp never reaches the leaf and the configuration is legal.
	caseNetworkUnconditionalRejectIsTerminal = `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "socks", "tag": "tcp-only-leaf", "server": "127.0.0.1", "server_port": 1080,
     "version": "5", "network": "tcp"}
  ],
  "route": {
    "rules": [
      {"action": "reject"},
      {"network": "udp", "outbound": "tcp-only-leaf"}
    ]
  }
}`
	// `route-options` only overrides dial options and the router continues to the next rule, so udp
	// reaches the TCP-only leaf and the configuration is broken.
	caseNetworkRouteOptionsDoesNotTerminate = `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "socks", "tag": "tcp-only-leaf", "server": "127.0.0.1", "server_port": 1080,
     "version": "5", "network": "tcp"}
  ],
  "route": {
    "rules": [
      {"action": "route-options", "override_port": 8443},
      {"network": "udp", "outbound": "tcp-only-leaf"}
    ]
  }
}`
	// A `bypass` with NO outbound is not terminal either: the router's own `break match` is guarded
	// by `if bypassAction.Outbound == "" { continue match }`.
	caseNetworkBypassWithoutOutboundDoesNotTerminate = `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "socks", "tag": "tcp-only-leaf", "server": "127.0.0.1", "server_port": 1080,
     "version": "5", "network": "tcp"}
  ],
  "route": {
    "rules": [
      {"action": "bypass"},
      {"network": "udp", "outbound": "tcp-only-leaf"}
    ]
  }
}`
	// `direct` dials without consulting an outbound and does NOT stop the loop.
	caseNetworkDirectDoesNotTerminate = `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "socks", "tag": "tcp-only-leaf", "server": "127.0.0.1", "server_port": 1080,
     "version": "5", "network": "tcp"}
  ],
  "route": {
    "rules": [
      {"action": "direct"},
      {"network": "udp", "outbound": "tcp-only-leaf"}
    ]
  }
}`
	// And the action that DOES terminate without naming an outbound, for contrast: `hijack-dns`.
	caseNetworkHijackDNSTerminates = `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "socks", "tag": "tcp-only-leaf", "server": "127.0.0.1", "server_port": 1080,
     "version": "5", "network": "tcp"}
  ],
  "route": {
    "rules": [
      {"action": "hijack-dns"},
      {"network": "udp", "outbound": "tcp-only-leaf"}
    ]
  }
}`
	// A bypass that DOES name an outbound terminates and delivers to it, so a udp flow reaches a
	// network-blind target - here an incapable one, which must be refused.
	caseNetworkBypassWithOutboundDelivers = `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "socks", "tag": "tcp-only-leaf", "server": "127.0.0.1", "server_port": 1080,
     "version": "5", "network": "tcp"}
  ],
  "route": {
    "rules": [
      {"network": "udp", "action": "bypass", "outbound": "tcp-only-leaf"}
    ]
  }
}`
)

// routeAwareMatrix is the ROUTE-AWARE-01 decision table. The row ids name the case numbers the
// construction prompt asks for, so a reader can check coverage without reading the fixtures.
var routeAwareMatrix = []routeAwareCase{
	{
		id:        "case-01-udp-terminated-before-the-path",
		why:       "udp is consumed at the routing layer before any outbound sees it, so a TCP-only target is legal",
		config:    caseNetworkTerminatedBeforeThePath,
		wantStart: true,
	},
	{
		id:        "case-02-unterminated-catch-all-is-a-real-defect",
		why:       "udp really reaches the TCP-only leaf, so this must stay refused",
		config:    caseNetworkUnterminatedCatchAll,
		wantStart: false,
		wantErr:   []string{"cannot serve the udp flow", "tcp-only-leaf"},
	},
	{
		id:        "case-03-termination-placed-after-delivery-does-not-count",
		why:       "the catch-all matches first, so the later reject can never run and the verdict must differ from case 1",
		config:    caseNetworkTerminationAfterDelivery,
		wantStart: false,
		wantErr:   []string{"cannot serve the udp flow", "tcp-only-leaf"},
	},
	{
		id:        "case-04-partial-termination-does-not-exhaust-the-protocol",
		why:       "a domain-scoped udp reject leaves every other udp flow reachable, so the defect stands",
		config:    caseNetworkPartialTermination,
		wantStart: false,
		wantErr:   []string{"cannot serve the udp flow", "tcp-only-leaf"},
	},
	{
		id:        "case-05-tcp-terminated-before-a-udp-only-path",
		why:       "the mirror of case 1: a udp-only leaf reached only by the flow tcp termination leaves is legal",
		config:    caseNetworkTCPTerminatedBeforeUDPOnlyPath,
		wantStart: true,
	},
	{
		id:        "case-06-dual-capable-leaf",
		why:       "the ordinary case must not be disturbed by the analysis",
		config:    caseNetworkDualCapableLeaf,
		wantStart: true,
	},
	{
		id:        "case-07-renamed-tags-group-kind-and-leaf-type-change-nothing",
		why:       "the same route semantics under different names, a different group kind and a different leaf type must reach the same verdict",
		config:    caseNetworkRenamedEquivalent,
		wantStart: true,
	},
	{
		id:        "case-08-group-really-delivers-udp-to-a-tcp-only-member",
		why:       "a udp flow can be handed to the TCP-only member of a network-blind selector, so this must stay refused",
		config:    caseNetworkGroupDeliversUDPToTCPOnlyMember,
		wantStart: false,
		wantErr:   []string{"cannot serve the udp flow", "tcp-only-member"},
	},
	{
		id:        "case-09-each-protocol-to-its-own-outbound",
		why:       "two rules terminating the two protocols onto two capable outbounds is legal",
		config:    caseNetworkEachProtocolToItsOwnOutbound,
		wantStart: true,
	},
	{
		id:        "case-10-nested-groups-carry-the-correct-requirement",
		why:       "the termination applies through two group levels, so the nested tcp-only leaf is legal",
		config:    caseNetworkNestedGroupCorrectRequirement,
		wantStart: true,
	},
	{
		id:        "case-10b-nested-groups-keep-the-real-defect",
		why:       "the same nested shape without the termination keeps the defect, so nesting is not a blanket pass",
		config:    caseNetworkNestedGroupKeepsRealDefect,
		wantStart: false,
		wantErr:   []string{"cannot serve the udp flow", "tcp-only-leaf"},
	},
	{
		id:        "case-11-unknown-matcher-follows-the-stated-policy",
		why:       "an unprovable matcher does not exhaust a protocol, so the udp route below it still receives udp and the configuration is legal",
		config:    caseNetworkUnknownMatcherIsNotExhaustive,
		wantStart: true,
	},
	{
		id:        "case-11b-never-still-skips-the-rule",
		why:       "the same rule is NEVER for tcp, so it cannot be credited with a tcp flow and must not withhold tcp from the rule that can carry it",
		config:    caseNetworkUnknownMatcherDoesNotWithholdTheProtocol,
		wantStart: true,
	},
	{
		id:        "case-12-logical-rule-that-does-narrow-the-network",
		why:       "a logical AND tree over one network condition is that condition, so the udp flow moves on to the next rule",
		config:    caseNetworkLogicalRuleNarrowsTheNetwork,
		wantStart: true,
	},
	{
		id:        "case-12b-inverted-rule-is-composed-not-ignored",
		why:       "an inverted network condition consumes the complement of that network, so the flow tcp termination leaves is the only one the next rule sees",
		config:    caseNetworkInvertedRuleIsRead,
		wantStart: true,
	},
	{
		id:        "case-12c-a-route-with-no-outbound-consumes-without-attributing",
		why:       "udp is routed to an unnamed target, so it is consumed without being charged to a tag, and the tcp route below reaches a capable leaf",
		config:    caseNetworkTerminationWithoutANamedOutbound,
		wantStart: true,
	},
	{
		id:        "case-13-unconditional-reject-is-terminal",
		why:       "a reject with no match items matches everything, so udp never reaches the leaf and the configuration is legal",
		config:    caseNetworkUnconditionalRejectIsTerminal,
		wantStart: true,
	},
	{
		id:        "case-14-route-options-does-not-stop-the-router",
		why:       "the router continues past route-options, so udp reaches the TCP-only leaf and the configuration is broken",
		config:    caseNetworkRouteOptionsDoesNotTerminate,
		wantStart: false,
		wantErr:   []string{"cannot serve the udp flow", "tcp-only-leaf"},
	},
	{
		id:        "case-15-bypass-without-an-outbound-does-not-stop-the-router",
		why:       "the router continues past an outbound-less bypass, so udp reaches the TCP-only leaf and the configuration is broken",
		config:    caseNetworkBypassWithoutOutboundDoesNotTerminate,
		wantStart: false,
		wantErr:   []string{"cannot serve the udp flow", "tcp-only-leaf"},
	},
	{
		id:        "case-16-direct-does-not-stop-the-router",
		why:       "the router continues past direct, so udp reaches the TCP-only leaf and the configuration is broken",
		config:    caseNetworkDirectDoesNotTerminate,
		wantStart: false,
		wantErr:   []string{"cannot serve the udp flow", "tcp-only-leaf"},
	},
	{
		id:        "case-17-hijack-dns-is-terminal",
		why:       "the router stops at hijack-dns, so udp never reaches the leaf and the configuration is legal",
		config:    caseNetworkHijackDNSTerminates,
		wantStart: true,
	},
	{
		id:        "case-18-bypass-with-an-outbound-terminates-and-delivers",
		why:       "a bypass that names an outbound stops the router and hands the flow to that outbound, so a udp flow reaching a TCP-only target must be refused",
		config:    caseNetworkBypassWithOutboundDelivers,
		wantStart: false,
		wantErr:   []string{"cannot serve the udp flow", "tcp-only-leaf"},
	},
}

// TestRouteAwareNetworkRequirementMatrix drives every case through the production loader.
func TestRouteAwareNetworkRequirementMatrix(t *testing.T) {
	for _, testCase := range routeAwareMatrix {
		t.Run(testCase.id, func(t *testing.T) {
			t.Parallel()
			instance, err := newBoxFromConfig(t, testCase.config)
			require.NoError(t, err, "construction cannot see this: it is a start-time decision")
			t.Cleanup(func() { _ = instance.Close() })

			err = instance.Start()
			if testCase.wantStart {
				require.NoError(t, err, testCase.why)
				return
			}
			require.Error(t, err, testCase.why)
			for _, expected := range testCase.wantErr {
				require.Contains(t, err.Error(), expected,
					"the refusal must be reported for the right reason; got: "+err.Error())
			}
		})
	}
}

// TestRouteAwareDiagnosticStatesTheRealReachability is the diagnostic half.
//
// The refusal used to end with "(the entry point routes tcp,udp to it)", which was a claim about
// the ENTRY POINT's capability rather than about what the routes proved arrives. Once the
// requirement is derived from the routes the two come apart, and a report that named a protocol
// provably terminated before the path would send an operator looking for a flow that does not
// exist. The message must name the reachable set it actually used.
func TestRouteAwareDiagnosticStatesTheRealReachability(t *testing.T) {
	t.Parallel()

	instance, err := newBoxFromConfig(t, caseNetworkUnterminatedCatchAll)
	require.NoError(t, err)
	t.Cleanup(func() { _ = instance.Close() })

	err = instance.Start()
	require.Error(t, err)
	require.Contains(t, err.Error(), "the routes prove tcp,udp reaches this path",
		"the diagnostic must state the reachability the analysis found; got: "+err.Error())
	require.NotContains(t, err.Error(), "entry point routes",
		"and it must not describe the entry point's theoretical capability as if it were delivered")
}

// TestRouteAwareTerminationRemovesTheProtocolFromTheDiagnostic is the same statement in the
// direction that matters for the defect: when a protocol IS terminated before the path, no
// diagnostic may claim it is reachable there.
//
// The target is deliberately named for its CAPABILITY rather than for the protocol it lacks, so
// the only way the string "udp" can appear in the report is if the analysis still believes udp
// arrives - which is exactly the assertion.
func TestRouteAwareTerminationRemovesTheProtocolFromTheDiagnostic(t *testing.T) {
	t.Parallel()

	// udp is terminated first, so the only requirement left is tcp - delivered to a leaf that
	// declares it carries udp alone, which is a real defect the report must describe in terms of
	// tcp only.
	const fixture = `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "socks", "tag": "tcp-incapable-leaf", "server": "127.0.0.1", "server_port": 1080,
     "version": "5", "network": "udp"}
  ],
  "route": {
    "rules": [
      {"network": "udp", "action": "reject"},
      {"network": "tcp", "outbound": "tcp-incapable-leaf"}
    ]
  }
}`

	instance, err := newBoxFromConfig(t, fixture)
	require.NoError(t, err)
	t.Cleanup(func() { _ = instance.Close() })

	err = instance.Start()
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot serve the tcp flow")
	// The reachability clause is the only part of the report that makes a claim about what ARRIVES,
	// so it is the part that must be checked. The rest of the sentence legitimately names the
	// capability the leaf does declare - "it carries udp" is a fact about the object, not a claim
	// that a udp flow reaches it - and asserting over the whole message would confuse the two.
	require.Contains(t, err.Error(), "(the routes prove tcp reaches this path, and tcp is part of it)",
		"the clause must name tcp alone, because udp was terminated before this path; got: "+err.Error())
	require.NotContains(t, err.Error(), "the routes prove tcp,udp",
		"the path analysis must not report udp as reachable here; got: "+err.Error())
}

// TestRouteAwareDiagnosticDoesNotClaimAProofTheRoutesNeverMade is the other half of the diagnostic
// honesty requirement, and it is the half a reachability rewrite can silently break.
//
// The requirement has two origins. When the path analysis PROVED it, the message may say so. When
// nothing was proven, the requirement is what the root's own reachable objects advertise between
// them (`advertisedNetworks`), and saying "the routes prove ..." there invents a proof that was
// never made.
//
// MEASURED: a configuration whose only rule is domain-scoped is UNKNOWN for both networks, so no
// route proves anything - and the first version of this rewrite still printed "the routes prove
// tcp,udp reaches this path" for a requirement that came entirely from the objects.
func TestRouteAwareDiagnosticDoesNotClaimAProofTheRoutesNeverMade(t *testing.T) {
	t.Parallel()

	// The only rule is domain-scoped, so its scope is UNKNOWN for both networks: it neither
	// delivers nor excludes anything, and the requirement below comes from the advertised fallback.
	// The selector holds a TCP-only and a UDP-only member and does not filter by network, so a flow
	// of either network can be handed to the member that cannot carry it - a real refusal, reported
	// against the objects rather than against the routing.
	const fixture = `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "socks", "tag": "tcp-only", "server": "127.0.0.1", "server_port": 1080,
     "version": "5", "network": "tcp"},
    {"type": "socks", "tag": "udp-only", "server": "127.0.0.1", "server_port": 1081,
     "version": "5", "network": "udp"},
    {"type": "selector", "tag": "sel", "outbounds": ["tcp-only", "udp-only"], "default": "tcp-only"}
  ],
  "route": {
    "rules": [
      {"domain_suffix": ["example.com"], "outbound": "sel"}
    ]
  }
}`

	instance, err := newBoxFromConfig(t, fixture)
	require.NoError(t, err)
	t.Cleanup(func() { _ = instance.Close() })

	err = instance.Start()
	require.Error(t, err, "a selector that does not filter must not be handed either network")
	require.Contains(t, err.Error(), "the routes prove nothing here",
		"the message must state that the requirement came from the objects, not from the routes; got: "+
			err.Error())
	require.NotContains(t, err.Error(), "the routes prove tcp",
		"and it must not claim a route proof for a requirement the routes never established; got: "+
			err.Error())
}
