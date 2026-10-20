package box_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Two independent routing rules each reach their own outbound, and each outbound is broken for the
// network its rule delivers. Both must be reported.
//
// # The defect this fixture pins
//
// `adapter/outbound.Manager` used to collapse validation failures by LEAF TAG: once one root had
// reported a broken leaf, a later root whose route to the same leaf was ALSO broken could not report
// it. Two routes are two independent contracts - the same leaf is usable on one route and broken on
// another - and because the manager walks its roots in declaration order, WHICH of the two verdicts
// survived depended on the order the outbounds happened to be written in.
//
// # Why this has to be a box-level fixture
//
// The de-duplication lives in the manager, and the requirement it de-duplicates comes from the ROUTES
// (installed by `box.New` from the compiled rule list). A test that calls the validator directly has
// no route proof, so it falls back to what the objects advertise - and a group's own `Network()`
// before start is an optimistic blanket that covers both networks regardless of its members. That
// fallback is why a hand-built fixture cannot express this case: the requirement has to come from a
// real routing rule, and a real routing rule needs a real configuration.
//
// # The shape
//
//	route rule: network=udp, outbound=A          A selects udp-incapable-a
//	route rule: network=tcp, outbound=B          B selects tcp-incapable-b
//	outbound A: selector over {udp-incapable-a}
//	outbound B: selector over {tcp-incapable-b}
//
// Each rule is reachable, because the two conditions are disjoint and the runtime router evaluates
// them in order: a udp flow stops at the first, a tcp flow at the second. Each delivers a network to
// a leaf that declares it cannot carry exactly that network, so both fail, for different reasons,
// against different roots.
//
// # Why the member is the broken half rather than the group
//
// A selector does NOT filter its members by network, so it can hand a udp flow to a member that
// carries tcp only. That is the defect, and it is the member's declaration that contradicts the
// route. It also keeps the fixture about two INDEPENDENT routes: the two members are reached by
// different rules, so nothing is deduplicated and both failures have to be reported.
//
// The companion test below pins the opposite case - one root with two broken members - where the
// manager's root-keyed guard deliberately reports once.
const twoIndependentBrokenRoutes = `{
  "log": { "disabled": true },
  "inbounds": [
    { "type": "mixed", "tag": "in", "listen": "127.0.0.1", "listen_port": 0 }
  ],
  "outbounds": [
    { "type": "socks", "tag": "udp-incapable-a", "server": "127.0.0.1", "server_port": 1080,
      "version": "5", "network": "tcp" },
    { "type": "socks", "tag": "tcp-incapable-b", "server": "127.0.0.1", "server_port": 1081,
      "version": "5", "network": "udp" },
    { "type": "selector", "tag": "A", "outbounds": ["udp-incapable-a"], "default": "udp-incapable-a" },
    { "type": "selector", "tag": "B", "outbounds": ["tcp-incapable-b"], "default": "tcp-incapable-b" }
  ],
  "route": {
    "rules": [
      { "network": "udp", "outbound": "A" },
      { "network": "tcp", "outbound": "B" }
    ]
  }
}`

// TestOneLeafBrokenOnTwoRoutesIsReportedForBoth drives the whole path: decode, construct, Start.
func TestTwoIndependentBrokenRoutesAreBothReported(t *testing.T) {
	startErr, message := startBoxCapturingWarnings(t, twoIndependentBrokenRoutes)
	require.NoError(t, startErr,
		"both routes are capability mismatches: each root still serves its own network, so the "+
			"configuration starts and both flows fail at run time")
	require.Contains(t, message, "WARN",
		"the findings must be warnings at warn level; got:\n"+message)

	// Both route rules name their own root, so the two failures are two lines. Keying the
	// de-duplication on the leaf reported one and dropped the other - and which one survived
	// depended on the declaration order above.
	require.Contains(t, message, "outbound/A",
		"the failure must name the first route's root: %s", message)
	require.Contains(t, message, "outbound/B",
		"the failure must name the second route's root as well. One line for two broken routes is "+
			"how an operator fixes the first and ships the second: %s", message)

	// Both broken ROUTES are named, one line each, and each line is attributed to the root the
	// operator has to fix. The defect this pins is that the previous leaf-keyed de-duplication named
	// NEITHER route: it reported the bare leaf once and dropped both, so the report said nothing
	// about which rule was broken.
	require.Contains(t, message, "outbound/A -> A -> udp-incapable-a",
		"the first route must be reported against its own root: %s", message)
	require.Contains(t, message, "outbound/B -> B -> tcp-incapable-b",
		"and the second route against its own root: %s", message)

	// Each route's own defect, and the two are different defects: the first delivers udp to a member
	// that carries tcp, the second delivers tcp to a member that carries udp.
	require.Contains(t, message, "cannot serve the udp flow routed through it",
		"the first route's reason must be its own: %s", message)
	require.Contains(t, message, "cannot serve the tcp flow routed through it",
		"the second route's reason must be its own: %s", message)
	require.Equal(t, 1, strings.Count(message, "hop #0 udp-incapable-a"),
		"one line per broken route, not one for the whole configuration and not three: %s", message)
	require.Equal(t, 1, strings.Count(message, "hop #0 tcp-incapable-b"),
		"and the second route exactly once: %s", message)
}

// TestOneRootWithTwoBrokenMembersIsReportedOnce pins the de-duplication that DOES exist, and the
// unit it works in.
//
// # The two rules and the guard they exercise
//
// Both rules deliver to the SAME selector, and each delivers a network the selector's only member
// cannot carry. The root therefore has two independent defects of its own, and the manager reports
// it ONCE: `reported` is keyed on the failing root, and its stated job is to stop one root
// contributing the same failure twice.
//
// # Why this is the fixture that pins the guard, and why the earlier one could not
//
// It replaces a fixture that reached one broken leaf two ways - through the selector and directly -
// and asserted that BOTH were reported. That assertion cannot hold together with the guard, and it
// only held before because delivery was derived from the configuration text with no rule ORDER: the
// unreachable second rule was still charged with a network, so it produced a failure that could be
// reported. Once delivery is a routing decision, a rule the router can never reach is correctly not
// charged, and the leaf's own failure is the one the guard keeps - one line, which is the honest
// answer for one broken object.
//
// # What is deliberately NOT asserted
//
// Which of the two members is named. `Hops` decides the order it materialises a group's members in,
// so naming one of them is a statement about that order rather than about the configuration. What
// the guard guarantees is that ONE line is produced for the root, and that is what is pinned.
const oneRootWithTwoBrokenMembers = `{
  "log": { "disabled": true },
  "inbounds": [
    { "type": "mixed", "tag": "in", "listen": "127.0.0.1", "listen_port": 0 }
  ],
  "outbounds": [
    { "type": "socks", "tag": "udp-incapable", "server": "127.0.0.1", "server_port": 1080,
      "version": "5", "network": "tcp" },
    { "type": "socks", "tag": "tcp-incapable", "server": "127.0.0.1", "server_port": 1081,
      "version": "5", "network": "udp" },
    { "type": "selector", "tag": "A", "outbounds": ["udp-incapable", "tcp-incapable"],
      "default": "udp-incapable" }
  ],
  "route": {
    "rules": [
      { "network": "udp", "outbound": "A" },
      { "network": "tcp", "outbound": "A" }
    ]
  }
}`

func TestOneRootWithTwoBrokenMembersIsReportedOnce(t *testing.T) {
	startErr, message := startBoxCapturingWarnings(t, oneRootWithTwoBrokenMembers)
	require.NoError(t, startErr,
		"each member serves one of the two networks, so the selector can serve both flows through "+
			"the right member; the mismatch is a capability warning")
	require.Contains(t, message, "WARN", "the finding must be a warning; got:\n"+message)
	require.Contains(t, message, "outbound/A -> A ->",
		"the failure must be attributed to the selector root: %s", message)
	require.Equal(t, 1, strings.Count(message, "hop #0 "),
		"one line for one root, however many of its members are broken: a second line would mean the "+
			"root contributed the same failure twice, which is what the `reported` guard exists to "+
			"stop: %s", message)
}
