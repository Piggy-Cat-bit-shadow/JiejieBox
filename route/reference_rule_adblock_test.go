package route

import (
	"testing"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"

	"github.com/stretchr/testify/require"
)

// Round-two audit coverage for the one place outside route/rule, dns and option that classifies a
// DNS rule action: the reference collector.
//
// It decides which transports a configuration still depends on by walking every rule's action, and
// an action it does not recognise is simply not a dependency. `adblock` names no server, so the two
// things that must hold are that it adds none, and that it does not abort the walk for the rules
// after it - a rule-set-only configuration whose last rule named a server would otherwise report
// that server as unreferenced, and the reference manager would be free to retire it.

func adblockReferenceRule(domainSuffixes ...string) option.DNSRule {
	return option.DNSRule{
		Type: C.RuleTypeDefault,
		DefaultOptions: option.DefaultDNSRule{
			RawDefaultDNSRule: option.RawDefaultDNSRule{DomainSuffix: domainSuffixes},
			DNSRuleAction: option.DNSRuleAction{
				Action: C.RuleActionTypeAdblock,
			},
		},
	}
}

func routeReferenceRule(server string, domainSuffixes ...string) option.DNSRule {
	return option.DNSRule{
		Type: C.RuleTypeDefault,
		DefaultOptions: option.DefaultDNSRule{
			RawDefaultDNSRule: option.RawDefaultDNSRule{DomainSuffix: domainSuffixes},
			DNSRuleAction: option.DNSRuleAction{
				Action:       C.RuleActionTypeRoute,
				RouteOptions: option.DNSRouteActionOptions{Server: server},
			},
		},
	}
}

// TestAdblockRuleAddsNoTransportReference pins that a DNS-only policy action is not mistaken for a
// dependency on some server.
func TestAdblockRuleAddsNoTransportReference(t *testing.T) {
	t.Parallel()

	var transports []string
	collectDNSRuleReferences([]option.DNSRule{adblockReferenceRule("ads.example")}, "", &transports)
	require.Empty(t, transports, "adblock resolves no server and must reference none")
}

// TestAdblockRuleDoesNotStopTheReferenceWalk pins that the rule after an adblock rule is still seen.
//
// Both orderings are checked because the collector reports a configuration as shadowed once it has
// taken a final action with an unconditional match; a rule listed after such an action is
// legitimately unreachable, but a rule after a CONDITIONAL one is not, and adblock's condition is
// what the operator wrote.
func TestAdblockRuleDoesNotStopTheReferenceWalk(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name  string
		rules []option.DNSRule
	}{
		{
			name: "adblock before a routing rule",
			rules: []option.DNSRule{
				adblockReferenceRule("ads.example"),
				routeReferenceRule("later-server", "example.com"),
			},
		},
		{
			name: "adblock after a routing rule",
			rules: []option.DNSRule{
				routeReferenceRule("earlier-server", "example.com"),
				adblockReferenceRule("ads.example"),
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			var transports []string
			shadowed := collectDNSRuleReferences(testCase.rules, "", &transports)
			require.False(t, shadowed,
				"a conditional adblock rule must not shadow the rules around it")
			require.Len(t, transports, 1,
				"the routing rule's server must still be collected, whatever order it is in")
		})
	}
}
