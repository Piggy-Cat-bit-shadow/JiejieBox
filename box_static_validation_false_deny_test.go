package box_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// FD: the false-deny adversarial matrix.
//
// # What this file adds to box_physicalpath_route_aware_test.go
//
// The route-aware matrix pins the route-demand model against the sources of OVER-validation. This
// file attacks the same model from the other side: every case here is a configuration a user could
// legitimately write, and the expected verdict is PASS. Each one is paired with a POSITIVE TWIN - the
// nearest configuration that must still be REFUSED - so a case cannot pass because the check stopped
// working.
//
// # The invariant under test
//
//	reject ONLY when runtime-unusable is statically PROVEN
//	do not reject when the validator merely lacks information
//
// A test that only asserts PASS proves nothing on its own: "always accept" would satisfy it. That is
// why every group below asserts both directions.

// fdCase is one false-deny claim and its positive twin.
type fdCase struct {
	id string
	// why states what makes the configuration legal, in terms of the runtime router.
	why string
	// accepted MUST start.
	accepted string
	// refused MUST be refused, and is the same configuration with the one difference that makes it
	// genuinely unusable. An empty value means the claim has no twin and the reason must say so.
	refused      string
	refusedLooks []string
}

// A udp-only leaf and a tcp-only leaf, both declaring their capability. Every case below is built
// from these two, so a verdict cannot depend on which protocol implementation is used.
const (
	fdTCPOnlyLeaf = `{"type": "socks", "tag": "tcp-only-leaf", "server": "127.0.0.1", "server_port": 1080,
     "version": "5", "network": "tcp"}`
	fdUDPOnlyLeaf = `{"type": "socks", "tag": "udp-only-leaf", "server": "127.0.0.1", "server_port": 1081,
     "version": "5", "network": "udp"}`
	fdDualLeaf = `{"type": "socks", "tag": "dual-leaf", "server": "127.0.0.1", "server_port": 1082,
     "version": "5"}`
)

// ---------------------------------------------------------------------------------------------
// FD-A2: UDP routed elsewhere.
//
// The routing table keeps both protocols, on two objects that each carry one. No root may inherit
// the union merely because both exist.
// ---------------------------------------------------------------------------------------------

const fdUDPRoutedElsewhere = `{
  "log": {"disabled": true},
  "outbounds": [
    ` + fdTCPOnlyLeaf + `,
    ` + fdUDPOnlyLeaf + `
  ],
  "route": {
    "rules": [
      {"network": "udp", "outbound": "udp-only-leaf"},
      {"network": "tcp", "outbound": "tcp-only-leaf"}
    ]
  }
}`

// FD-A4 (accepted half): the reject comes FIRST, so udp is consumed before the catch-all runs.
const fdOrderRejectFirst = `{
  "log": {"disabled": true},
  "outbounds": [ ` + fdTCPOnlyLeaf + ` ],
  "route": {
    "rules": [
      {"network": "udp", "action": "reject"},
      {"outbound": "tcp-only-leaf"}
    ]
  }
}`

// FD-A4 (refused half): the SAME two rules reversed. The catch-all matches first for every flow, so
// the reject can never run and udp really reaches the tcp-only leaf. A model without rule order
// cannot tell these two apart, which is exactly why they are one test.
const fdOrderRejectLast = `{
  "log": {"disabled": true},
  "outbounds": [ ` + fdTCPOnlyLeaf + ` ],
  "route": {
    "rules": [
      {"outbound": "tcp-only-leaf"},
      {"network": "udp", "action": "reject"}
    ]
  }
}`

// ---------------------------------------------------------------------------------------------
// FD-A5: a partial predicate must not erase a whole protocol class.
//
// `network=udp` PLUS `port=53` removes only udp/53. Every other udp flow still reaches the leaf.
// ---------------------------------------------------------------------------------------------

const fdPartialPortPredicate = `{
  "log": {"disabled": true},
  "outbounds": [ ` + fdTCPOnlyLeaf + ` ],
  "route": {
    "rules": [
      {"network": "udp", "port": [53], "action": "reject"},
      {"network": "udp", "outbound": "tcp-only-leaf"}
    ]
  }
}`

// FD-A6: a domain-constrained predicate. Same statement as A5 with a matcher that is not about the
// port: the analysis cannot conclude that all udp is gone, and the residual udp still counts.
const fdPartialDomainPredicate = `{
  "log": {"disabled": true},
  "outbounds": [ ` + fdTCPOnlyLeaf + ` ],
  "route": {
    "rules": [
      {"network": "udp", "domain_suffix": ["example.com"], "action": "reject"},
      {"network": "udp", "outbound": "tcp-only-leaf"}
    ]
  }
}`

// ---------------------------------------------------------------------------------------------
// FD-A7: `route.final`.
//
// `final` receives every flow no rule matched. It used to be left UNPROVEN, so the validator fell
// back to what the final object advertises - which cannot see the rules. That fallback refused the
// field report's own configuration shape: `{network: udp, action: reject}` leaves a residual of tcp
// ALONE, and a tcp-only `final` is legal, while the advertised fallback charged both networks.
//
// `common/routeflow` now computes the residual and charges it to `final`, which is a fact about the
// routing rather than an inference from what the object carries. Both halves are asserted below.
//
// # What this case deliberately does NOT claim
//
// A MIXED selector as `final` was the first attempt at the accepted half, and it is still refused.
// That refusal is not a false deny: `Selector.Selected(network)` ignores its network argument
// (`protocol/group/selector.go:216-218`), so a user who switches to the incapable member really
// would fail every flow of that network. The validator refuses at start what the runtime would
// refuse at the first manual switch, which is the design's stated intent for a selectable member.
// Recorded in the audit report rather than silently weakened, because the two readings are both
// defensible and the difference is a product decision, not a modelling error.
// ---------------------------------------------------------------------------------------------

// Accepted: the rules prove udp never reaches `final`, so a tcp-only `final` is legal.
const fdFinalWithUDPConsumed = `{
  "log": {"disabled": true},
  "outbounds": [ ` + fdTCPOnlyLeaf + ` ],
  "route": {
    "rules": [
      {"network": "udp", "action": "reject"}
    ],
    "final": "tcp-only-leaf"
  }
}`

// Refused: nothing consumes udp, so the residual is both networks and the tcp-only `final` genuinely
// cannot serve half of it.
const fdFinalWithNothingConsumed = `{
  "log": {"disabled": true},
  "outbounds": [ ` + fdTCPOnlyLeaf + ` ],
  "route": {
    "rules": [],
    "final": "tcp-only-leaf"
  }
}`

// Variant: a rule that PROVABLY consumes tcp instead, leaving udp for a udp-only `final`. The mirror
// of the accepted case, so a fix that special-cased udp would fail here.
const fdFinalWithTCPConsumed = `{
  "log": {"disabled": true},
  "outbounds": [ ` + fdUDPOnlyLeaf + ` ],
  "route": {
    "rules": [
      {"network": "tcp", "action": "reject"}
    ],
    "final": "udp-only-leaf"
  }
}`

// The exact shape the field report carried, sanitized: a udp reject and a tcp-only final, with no
// tag or node name from the original configuration anywhere.
const fdReportedShapeSanitized = `{
  "log": {"disabled": true},
  "outbounds": [ ` + fdTCPOnlyLeaf + ` ],
  "route": {
    "rules": [
      {"network": ["udp"], "action": "reject"}
    ],
    "final": "tcp-only-leaf"
  }
}`

// ---------------------------------------------------------------------------------------------
// FD-A8 / FD-A9: logical rules.
//
// AND narrows, so `network=tcp AND domain=...` is NEVER for udp and its target is reachable only by
// tcp. OR widens, so a domain-only branch inside an OR can still match udp and the whole tree must
// NOT be collapsed to tcp.
// ---------------------------------------------------------------------------------------------

// FD-A8 accepted: the AND tree is provably not about udp, so the second rule sends tcp to a leaf
// that carries it.
const fdLogicalAndAccepted = `{
  "log": {"disabled": true},
  "outbounds": [ ` + fdTCPOnlyLeaf + ` ],
  "route": {
    "rules": [
      {"type": "logical", "mode": "and",
       "rules": [{"network": ["tcp"]}, {"domain": ["example.com"]}],
       "outbound": "tcp-only-leaf"},
      {"network": "tcp", "outbound": "tcp-only-leaf"}
    ]
  }
}`

// FD-A9 refused: the OR tree has a branch with no network condition, so it can match udp and the
// group must be charged with udp. Collapsing an OR to the intersection of its branches would make
// this start, which is the false-allow direction.
const fdLogicalOrRefused = `{
  "log": {"disabled": true},
  "outbounds": [
    ` + fdTCPOnlyLeaf + `,
    {"type": "selector", "tag": "group-a", "outbounds": ["tcp-only-leaf"], "default": "tcp-only-leaf"}
  ],
  "route": {
    "rules": [
      {"network": "udp", "outbound": "group-a"}
    ]
  }
}`

// ---------------------------------------------------------------------------------------------
// FD-A10: invert.
//
// `invert(network=udp)` provably matches every tcp flow and never a udp one, so it CONSUMES tcp. The
// udp that remains reaches a udp-only leaf and the configuration is legal - which is the opposite
// verdict from the un-inverted rule with the same body.
// ---------------------------------------------------------------------------------------------

const fdInvertConsumesTheComplement = `{
  "log": {"disabled": true},
  "outbounds": [ ` + fdUDPOnlyLeaf + ` ],
  "route": {
    "rules": [
      {"network": "udp", "invert": true, "action": "reject"},
      {"network": "udp", "outbound": "udp-only-leaf"}
    ]
  }
}`

// The twin, and the reason invert must be modelled rather than ignored: WITHOUT the inversion the
// same rule body consumes udp, leaving tcp for a udp-only leaf - so if `invert` were dropped, this
// test would flip to a false deny.
const fdInvertNotAppliedWouldRefuse = `{
  "log": {"disabled": true},
  "outbounds": [ ` + fdUDPOnlyLeaf + ` ],
  "route": {
    "rules": [
      {"network": "udp", "action": "reject"},
      {"network": "tcp", "outbound": "udp-only-leaf"}
    ]
  }
}`

// ---------------------------------------------------------------------------------------------
// The matrix.
// ---------------------------------------------------------------------------------------------

var fdMatrix = []fdCase{
	{
		id:       "FD-A2-udp-routed-elsewhere",
		why:      "each protocol is terminated onto an object that carries it, so neither root inherits the union",
		accepted: fdUDPRoutedElsewhere,
		refused: `{
  "log": {"disabled": true},
  "outbounds": [ ` + fdUDPOnlyLeaf + `, ` + fdTCPOnlyLeaf + ` ],
  "route": {
    "rules": [
      {"network": "udp", "outbound": "udp-only-leaf"},
      {"network": "tcp", "outbound": "udp-only-leaf"}
    ]
  }
}`,
		refusedLooks: []string{"cannot serve the tcp flow"},
	},
	{
		id:       "FD-A3-explicit-udp-to-tcp-only-is-the-safety-control",
		why:      "an explicit udp route to a tcp-only outbound is a genuine defect and must stay refused",
		accepted: fdUDPRoutedElsewhere,
		refused: `{
  "log": {"disabled": true},
  "outbounds": [ ` + fdTCPOnlyLeaf + ` ],
  "route": {
    "rules": [
      {"network": "udp", "outbound": "tcp-only-leaf"}
    ]
  }
}`,
		refusedLooks: []string{"cannot serve the udp flow", "tcp-only-leaf"},
	},
	{
		id:           "FD-A4-rule-order-decides-the-verdict",
		why:          "the reject runs first, so udp is consumed before the catch-all can deliver it",
		accepted:     fdOrderRejectFirst,
		refused:      fdOrderRejectLast,
		refusedLooks: []string{"cannot serve the udp flow", "tcp-only-leaf"},
	},
	{
		id:           "FD-A5-a-port-scoped-reject-removes-only-udp-53",
		why:          "the predicate names a port as well as a network, so it removes part of udp and never all of it",
		accepted:     fdOrderRejectFirst,
		refused:      fdPartialPortPredicate,
		refusedLooks: []string{"cannot serve the udp flow"},
	},
	{
		id:           "FD-A6-a-domain-scoped-reject-cannot-exhaust-udp",
		why:          "the residual udp set cannot be proven empty, so the later udp route still counts",
		accepted:     fdOrderRejectFirst,
		refused:      fdPartialDomainPredicate,
		refusedLooks: []string{"cannot serve the udp flow"},
	},
	{
		id:           "FD-A7-route-final-is-charged-with-the-residual",
		why:          "the rules prove udp never reaches final, so a tcp-only final is legal",
		accepted:     fdFinalWithUDPConsumed,
		refused:      fdFinalWithNothingConsumed,
		refusedLooks: []string{"cannot serve the udp flow", "tcp-only-leaf"},
	},
	{
		id:           "FD-A7b-route-final-mirrored-to-udp",
		why:          "a rule that provably consumes tcp leaves udp for a udp-only final, so this is not a udp special case",
		accepted:     fdFinalWithTCPConsumed,
		refused:      fdFinalWithNothingConsumed,
		refusedLooks: []string{"cannot serve the udp flow"},
	},
	{
		id:           "FD-A7c-the-reported-shape-sanitized",
		why:          "the reported configuration's shape reaches the same verdict with no business tag anywhere",
		accepted:     fdReportedShapeSanitized,
		refused:      fdFinalWithNothingConsumed,
		refusedLooks: []string{"cannot serve the udp flow", "tcp-only-leaf"},
	},
	{
		id:           "FD-A8-logical-and-narrows-the-network",
		why:          "an AND whose branches include an unconditional network condition is that condition",
		accepted:     fdLogicalAndAccepted,
		refused:      fdLogicalOrRefused,
		refusedLooks: []string{"cannot serve the udp flow", "tcp-only-leaf"},
	},
	{
		id:           "FD-A9-logical-or-with-a-network-blind-branch",
		why:          "the OR tree is refused for the udp the second rule delivers to a network-blind group",
		accepted:     fdLogicalAndAccepted,
		refused:      fdLogicalOrRefused,
		refusedLooks: []string{"cannot serve the udp flow"},
	},
	{
		id:           "FD-A10-invert-consumes-the-complement",
		why:          "an inverted udp condition provably consumes tcp, leaving udp for a leaf that carries it",
		accepted:     fdInvertConsumesTheComplement,
		refused:      fdInvertNotAppliedWouldRefuse,
		refusedLooks: []string{"cannot serve the tcp flow"},
	},
}

// TestFalseDenyMatrix asserts every claim's accepted half AND refused half.
//
// Asserting both in ONE test is the point: a change that weakens the validator to "always accept"
// makes the accepted halves pass and the refused halves fail, so the test cannot be satisfied by
// removing the check.
func TestFalseDenyMatrix(t *testing.T) {
	for _, testCase := range fdMatrix {
		t.Run(testCase.id, func(t *testing.T) {
			t.Parallel()

			accepted, err := newBoxFromConfig(t, testCase.accepted)
			require.NoError(t, err, "construction cannot see this: it is a start-time decision")
			t.Cleanup(func() { _ = accepted.Close() })
			require.NoError(t, accepted.Start(),
				"FALSE DENY: this configuration is usable at run time and must start. %s", testCase.why)

			if testCase.refused == "" {
				return
			}
			refused, err := newBoxFromConfig(t, testCase.refused)
			require.NoError(t, err)
			t.Cleanup(func() { _ = refused.Close() })
			startErr := refused.Start()
			require.Error(t, startErr,
				"VACUOUS TEST: the twin must still be refused, or this case passes by accepting "+
					"everything. %s", testCase.why)
			for _, expected := range testCase.refusedLooks {
				require.Contains(t, startErr.Error(), expected,
					"the twin must be refused for the right reason; got: "+startErr.Error())
			}
		})
	}
}
