package daemon

// C04 — a change plan has to see the edits that keep the shape.
//
// The first version of the two restart-required domains compared COUNTS:
// len(route.rules), len(dns.rules), a tag/type pair per DNS server and the bare tag list of the
// rule sets. Every document below leaves those numbers and names untouched and still changes what
// the Box does, so every one of them was reported "unchanged" — the plan promised a caller that
// an edit needed nothing, which is the one answer that makes the caller apply half of it.
//
// The cases are the ones measured against the real entry: a rule that switches network tcp->udp
// (same rule count), a DNS rule that switches ip_version 4->6 (same rule count), a server whose
// tag and type stay while its address moves, a single-domain matcher that is edited in place, a
// matcher KIND swap that leaves the domain string identical, a reordered rule list, and a remote
// rule set whose tag stays while its URL moves.

import (
	"testing"

	"github.com/sagernet/sing-box/option"

	"github.com/stretchr/testify/require"
)

func routeRuleDocument(rules ...option.Rule) *option.Options {
	return &option.Options{Route: &option.RouteOptions{Final: "direct", Rules: rules}}
}

// routeRule builds one default rule that routes a matching connection to outbound.
func routeRule(match option.RawDefaultRule, outbound string) option.Rule {
	return option.Rule{
		Type: "default",
		DefaultOptions: option.DefaultRule{
			RawDefaultRule: match,
			RuleAction: option.RuleAction{
				Action:       "route",
				RouteOptions: option.RouteActionOptions{Outbound: outbound},
			},
		},
	}
}

func dnsRuleDocument(rules ...option.DNSRule) *option.Options {
	return &option.Options{DNS: &option.DNSOptions{RawDNSOptions: option.RawDNSOptions{Rules: rules}}}
}

// dnsRule builds one default DNS rule that routes matching queries to a server.
func dnsRule(match option.RawDefaultDNSRule, server string) option.DNSRule {
	return option.DNSRule{
		Type: "default",
		DefaultOptions: option.DefaultDNSRule{
			RawDefaultDNSRule: match,
			DNSRuleAction: option.DNSRuleAction{
				Action:       "route",
				RouteOptions: option.DNSRouteActionOptions{Server: server},
			},
		},
	}
}

func dnsUDPServer(tag string, address string) option.DNSServerOptions {
	return option.DNSServerOptions{
		Type: "udp",
		Tag:  tag,
		Options: option.RemoteDNSServerOptions{
			DNSServerAddressOptions: option.DNSServerAddressOptions{Server: address},
		},
	}
}

func TestRuleDomainSeesASameCountNetworkEdit(t *testing.T) {
	current := routeRuleDocument(routeRule(option.RawDefaultRule{Network: []string{"tcp"}}, "proxy"))
	target := routeRuleDocument(routeRule(option.RawDefaultRule{Network: []string{"udp"}}, "proxy"))
	plan := PlanRuleDomain(current, "rev-a", target, "rev-b")
	require.True(t, plan.RestartRequired, "tcp -> udp keeps the rule count and changes the match")
	require.NotEmpty(t, plan.Changes)
}

func TestRuleDomainSeesASingleDomainEdit(t *testing.T) {
	current := routeRuleDocument(routeRule(option.RawDefaultRule{Domain: []string{"one.example"}}, "proxy"))
	target := routeRuleDocument(routeRule(option.RawDefaultRule{Domain: []string{"two.example"}}, "proxy"))
	plan := PlanRuleDomain(current, "rev-a", target, "rev-b")
	require.True(t, plan.RestartRequired, "one domain replaced by another is the same count")
	require.NotEmpty(t, plan.Changes)
}

func TestRuleDomainSeesAMatcherKindEdit(t *testing.T) {
	current := routeRuleDocument(routeRule(option.RawDefaultRule{DomainSuffix: []string{"example.com"}}, "proxy"))
	target := routeRuleDocument(routeRule(option.RawDefaultRule{Domain: []string{"example.com"}}, "proxy"))
	plan := PlanRuleDomain(current, "rev-a", target, "rev-b")
	require.True(t, plan.RestartRequired, "domain_suffix -> domain keeps the strings and changes the match")
	require.NotEmpty(t, plan.Changes)
}

func TestRuleDomainSeesAReorderedRuleList(t *testing.T) {
	first := routeRule(option.RawDefaultRule{Domain: []string{"one.example"}}, "proxy")
	second := routeRule(option.RawDefaultRule{Domain: []string{"two.example"}}, "direct")
	current := routeRuleDocument(first, second)
	target := routeRuleDocument(second, first)
	plan := PlanRuleDomain(current, "rev-a", target, "rev-b")
	require.True(t, plan.RestartRequired, "routing rules are ordered; swapping two changes the winner")
	require.NotEmpty(t, plan.Changes)
}

func TestRuleDomainSeesARuleSetContentEdit(t *testing.T) {
	current := &option.Options{Route: &option.RouteOptions{
		Final: "direct",
		RuleSet: []option.RuleSet{{
			Type: "remote", Tag: []string{"geosite"}, Format: "binary",
			RemoteOptions: option.RemoteRuleSet{URL: "https://example.com/one.srs"},
		}},
	}}
	target := &option.Options{Route: &option.RouteOptions{
		Final: "direct",
		RuleSet: []option.RuleSet{{
			Type: "remote", Tag: []string{"geosite"}, Format: "binary",
			RemoteOptions: option.RemoteRuleSet{URL: "https://example.com/two.srs"},
		}},
	}}
	plan := PlanRuleDomain(current, "rev-a", target, "rev-b")
	require.True(t, plan.RestartRequired, "the same tag pointing at another artifact is a different rule set")
	require.NotEmpty(t, plan.Changes)
}

func TestDNSDomainSeesASameCountConditionEdit(t *testing.T) {
	current := dnsRuleDocument(dnsRule(option.RawDefaultDNSRule{IPVersion: 4}, "local"))
	target := dnsRuleDocument(dnsRule(option.RawDefaultDNSRule{IPVersion: 6}, "local"))
	plan := PlanDNSDomain(current, "rev-a", target, "rev-b")
	require.True(t, plan.RestartRequired, "ip_version 4 -> 6 keeps the rule count")
	require.NotEmpty(t, plan.Changes)
}

func TestDNSDomainSeesADNSRuleOrderEdit(t *testing.T) {
	first := dnsRule(option.RawDefaultDNSRule{DomainSuffix: []string{"one.example"}}, "remote")
	second := dnsRule(option.RawDefaultDNSRule{DomainSuffix: []string{"two.example"}}, "local")
	current := dnsRuleDocument(first, second)
	target := dnsRuleDocument(second, first)
	plan := PlanDNSDomain(current, "rev-a", target, "rev-b")
	require.True(t, plan.RestartRequired, "DNS rules are ordered; swapping two changes which server wins")
	require.NotEmpty(t, plan.Changes)
}

func TestDNSDomainSeesAServerAddressEditUnderTheSameTagAndType(t *testing.T) {
	current := dnsDocument(dnsUDPServer("remote", "192.0.2.1"))
	target := dnsDocument(dnsUDPServer("remote", "192.0.2.2"))
	plan := PlanDNSDomain(current, "rev-a", target, "rev-b")
	require.True(t, plan.RestartRequired, "the tag and type stay; the address is what the query uses")
	require.NotEmpty(t, plan.Changes)
}

func TestDNSDomainSeesAServerAddressEditWithAnExplicitPort(t *testing.T) {
	withPort := dnsUDPServer("remote", "192.0.2.1")
	withPort.Options = option.RemoteDNSServerOptions{
		DNSServerAddressOptions: option.DNSServerAddressOptions{Server: "192.0.2.1", ServerPort: 5353},
	}
	current := dnsDocument(dnsUDPServer("remote", "192.0.2.1"))
	target := dnsDocument(withPort)
	plan := PlanDNSDomain(current, "rev-a", target, "rev-b")
	require.True(t, plan.RestartRequired, "a port is part of the address the queries go to")
}

// TestConfigRevisionDividesDocumentsThatDifferOnlyInAServerAddress pins the revision the plans
// carry. A revision that ignores the variant payload is not a document identity: a stale plan
// computed against the old address would be accepted for the new document.
func TestConfigRevisionDividesDocumentsThatDifferOnlyInAServerAddress(t *testing.T) {
	current := dnsDocument(dnsUDPServer("remote", "192.0.2.1"))
	target := dnsDocument(dnsUDPServer("remote", "192.0.2.2"))
	require.NotEqual(t, configRevision(current), configRevision(target))
}

// TestConfigRevisionDividesDocumentsThatDifferOnlyInAnOutboundAddress pins the same property for
// the connection side: an outbound's typed payload is not part of encoding/json's view either.
func TestConfigRevisionDividesDocumentsThatDifferOnlyInAnOutboundAddress(t *testing.T) {
	outboundDocument := func(server string) *option.Options {
		return &option.Options{Outbounds: []option.Outbound{{
			Type: "socks", Tag: "proxy",
			Options: option.SOCKSOutboundOptions{
				ServerOptions: option.ServerOptions{Server: server, ServerPort: 1080},
			},
		}}}
	}
	require.NotEqual(t, configRevision(outboundDocument("192.0.2.1")), configRevision(outboundDocument("192.0.2.2")))
}
