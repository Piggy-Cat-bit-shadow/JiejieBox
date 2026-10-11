package box_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A rule whose predicate the static analysis cannot resolve must not become a requirement that both
// networks are delivered (P4).
//
// # Why this is the dangerous direction
//
// The requirement now comes from the COMPILED rules (common/routeflow), and a tag is charged only
// with what the routes PROVE reaches it. Every shape whose predicate the summary cannot resolve - a
// rule constraining something other than the network, `route.final`, a rule that pairs a network
// with such a condition - proves nothing about the network, and "nothing proven" has to stay
// "nothing proven". Reading it as "both networks" is the mistake that once made one unrelated
// TCP+UDP outbound responsible for UDP everywhere else and refused configurations that had always
// started.
//
// # What these three fixtures do and do not claim
//
// Each one is a configuration that must START, and each is a case where the old text scan and the
// new analysis agree - so they are CONTROLS for the fallback, not evidence about the routing model.
// The model's own discriminating cases live in box_physicalpath_route_aware_test.go, whose ids name
// what each one separates.
//
// One of them changed behaviour and no longer belongs here: a rule with NO match items is not
// "unproven" at all. `Match` special-cases the empty item list and returns true, so a bare
// `{outbound: x}` rule is a CATCH-ALL and really does deliver both networks. That case moved to the
// route-aware matrix (case A3 there, "catch-all rule demands both networks"), where it is asserted
// as the refusal it is.
//
// A second one changed behaviour in the same direction: `route.final` is no longer "unproven". It
// receives the RESIDUAL the rules leave, which is a fact about the routing, so a TCP-only `final`
// with no rule consuming udp is refused. See TestFinalReceivesTheResidualAndIsChargedWithIt below.
//
// The other half matters equally: where the configuration DOES prove a network reaches an object
// that cannot carry it, the answer must be a definite refusal naming the object - not an "unknown"
// that reads as "not checked". A verdict that can be neither confirmed nor denied is not a pass.

// TestFinalReceivesTheResidualAndIsChargedWithIt covers `route.final`.
//
// # What this test used to claim, and why the claim was wrong
//
// It asserted that a TCP-only outbound set as `final` "must therefore start", on the grounds that
// `final` receives everything no rule matched - which is every network - so nothing is proven about
// it and the requirement falls back to what the object advertises.
//
// The premise is right and the conclusion does not follow. `final` receives everything no rule
// matched, and with NO RULES that is every network: the residual is `{tcp,udp}`, and a TCP-only
// outbound cannot serve the udp half. So the configuration is genuinely unusable, and asserting that
// it starts pinned a FALSE DENY - one the field report's own configuration shape exposed, where a
// `{network: udp, action: reject}` rule leaves a residual of tcp ALONE and a TCP-only `final` is
// legal.
//
// The residual is now computed from the rules and charged to `final` (common/routeflow's `Reach`),
// which gets both halves right: no rules means both networks, and a terminating rule narrows it.
// What the old comment was protecting - "nothing proven must not be read as both networks" - is
// still honoured, because the residual is not a fallback: it is what the rules leave.
func TestFinalReceivesTheResidualAndIsChargedWithIt(t *testing.T) {
	t.Parallel()

	// No rules: the residual is both networks, so a TCP-only `final` is a real defect.
	unusable, err := newBoxFromConfig(t, `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "socks", "tag": "tcp-only", "server": "127.0.0.1", "server_port": 1080,
     "version": "5", "network": "tcp"}
  ],
  "route": {"final": "tcp-only"}
}`)
	require.NoError(t, err)
	t.Cleanup(func() { _ = unusable.Close() })
	startErr := unusable.Start()
	require.Error(t, startErr,
		"nothing consumes udp, so udp reaches the TCP-only `final` and the configuration is unusable")
	require.Contains(t, startErr.Error(), "cannot serve the udp flow",
		"and the refusal must name the protocol that cannot be carried; got: "+startErr.Error())

	// A terminating rule narrows the residual to tcp alone, and the same `final` is then legal.
	usable, err := newBoxFromConfig(t, `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "socks", "tag": "tcp-only", "server": "127.0.0.1", "server_port": 1080,
     "version": "5", "network": "tcp"}
  ],
  "route": {
    "rules": [
      {"network": "udp", "action": "reject"}
    ],
    "final": "tcp-only"
  }
}`)
	require.NoError(t, err)
	t.Cleanup(func() { _ = usable.Close() })
	require.NoError(t, usable.Start(),
		"the rules prove udp never reaches `final`, so a TCP-only `final` must start")
}

// TestALogicalRuleProvesNothingAboutDelivery covers a logical rule.
//
// A logical rule's conditions are a tree whose network constraints are an intersection the helper
// deliberately does not evaluate - summarising it would be a second routing engine. Its outbound is
// therefore unproven, and an unproven outbound must not be failed for a network nothing proved.
func TestALogicalRuleProvesNothingAboutDelivery(t *testing.T) {
	t.Parallel()

	instance, err := newBoxFromConfig(t, `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "socks", "tag": "tcp-only", "server": "127.0.0.1", "server_port": 1080,
     "version": "5", "network": "tcp"}
  ],
  "route": {
    "rules": [
      {"type": "logical", "mode": "and", "rules": [{"domain": ["example.com"]}],
       "outbound": "tcp-only"}
    ]
  }
}`)
	require.NoError(t, err)
	require.NoError(t, instance.Start(),
		"a logical rule's network constraints are not evaluated, so they prove nothing, and "+
			"nothing proven must not become a both-networks requirement")
	t.Cleanup(func() { _ = instance.Close() })
}

// TestAnInvertedRuleProvesNothingAboutDelivery covers an inverted rule, which matches the complement
// of its conditions and can therefore match any network.
func TestAnInvertedRuleProvesNothingAboutDelivery(t *testing.T) {
	t.Parallel()

	instance, err := newBoxFromConfig(t, `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "socks", "tag": "tcp-only", "server": "127.0.0.1", "server_port": 1080,
     "version": "5", "network": "tcp"}
  ],
  "route": {
    "rules": [
      {"invert": true, "domain": ["example.com"], "outbound": "tcp-only"}
    ]
  }
}`)
	require.NoError(t, err)
	require.NoError(t, instance.Start(),
		"an inverted rule matches the complement of its conditions, so it constrains nothing "+
			"about the network and must not become a requirement")
	t.Cleanup(func() { _ = instance.Close() })
}

// TestAProvenNetworkIsRefusedDefinitelyAndNotLeftUnverified is the other half.
//
// The rule proves TCP reaches a network-blind selector, and one of that selector's members declares
// udp-only. The verdict must be a definite refusal that names the member - never an "unknown", which
// a reader can only interpret as "not checked".
func TestAProvenNetworkIsRefusedDefinitelyAndNotLeftUnverified(t *testing.T) {
	t.Parallel()

	instance, err := newBoxFromConfig(t, `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "socks", "tag": "dual", "server": "127.0.0.1", "server_port": 1080, "version": "5"},
    {"type": "socks", "tag": "udp-only", "server": "127.0.0.1", "server_port": 1081,
     "version": "5", "network": "udp"},
    {"type": "selector", "tag": "sel", "outbounds": ["dual", "udp-only"], "default": "dual"}
  ],
  "route": {
    "rules": [
      {"network": "tcp", "outbound": "sel"}
    ]
  }
}`)
	require.NoError(t, err)
	t.Cleanup(func() { _ = instance.Close() })

	err = instance.Start()
	require.Error(t, err,
		"the route proves TCP reaches a selector that does not filter, so its udp-only member is "+
			"reachable by a TCP flow and the configuration cannot work")
	require.Contains(t, err.Error(), "udp-only",
		"the refusal must name the member that cannot carry the flow")
	require.Contains(t, err.Error(), "cannot serve the tcp flow",
		"and it must state the definite reason rather than leaving the member unverified")
	require.NotContains(t, err.Error(), "unknown",
		"a proven defect must not be reported as an unknown")
}

// TestAProvenNetworkLeavesTheCapableMemberAlone is the positive control: proving TCP reaches a
// selector must not fail the member that CAN carry TCP.
func TestAProvenNetworkLeavesTheCapableMemberAlone(t *testing.T) {
	t.Parallel()

	instance, err := newBoxFromConfig(t, `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "socks", "tag": "tcp-only", "server": "127.0.0.1", "server_port": 1080,
     "version": "5", "network": "tcp"},
    {"type": "selector", "tag": "sel", "outbounds": ["tcp-only"], "default": "tcp-only"}
  ],
  "route": {
    "rules": [
      {"network": "tcp", "outbound": "sel"}
    ]
  }
}`)
	require.NoError(t, err)
	t.Cleanup(func() { _ = instance.Close() })

	require.NoError(t, instance.Start(),
		"TCP is the only network the route proves, and every member carries it, so this must start")
}
