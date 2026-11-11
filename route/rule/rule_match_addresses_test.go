package rule

import (
	"net/netip"
	"slices"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing/common/json/badoption"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// These tests pin the MATCHING half of `resolve` with `match_only: true`: which destination-IP
// conditions read the addresses it publishes, in which precedence, and - just as important - which
// ones must not.
//
// The precedence is the semantic decision of the feature, so every case below states why:
//
//	literal destination  the address the flow actually uses; always wins
//	MatchAddresses       published for matching only (`match_only: true`)
//	DestinationAddresses the dial candidate list, unchanged fallback

func prefixable(cidr string) *badoption.Prefixable {
	prefix := badoption.Prefixable(netip.MustParsePrefix(cidr))
	return &prefix
}

func mustCIDRItem(t *testing.T, cidr string) *IPCIDRItem {
	t.Helper()
	item, err := NewIPCIDRItem(false, []*badoption.Prefixable{prefixable(cidr)})
	require.NoError(t, err)
	return item
}

func domainDestination(domain string) M.Socksaddr {
	return M.Socksaddr{Fqdn: domain, Port: 443}
}

// A named destination gets its matching evidence from the action, so an `ip_cidr` rule - and
// therefore an IP `rule_set`, which compiles to the same item - is selected by the local answer.
func TestIPCIDRMatchesMatchOnlyAddresses(t *testing.T) {
	metadata := &adapter.InboundContext{
		Destination:    domainDestination("match.example"),
		MatchAddresses: []netip.Addr{netip.MustParseAddr("198.51.100.10")},
	}

	require.True(t, mustCIDRItem(t, "198.51.100.0/24").Match(metadata),
		"the resolved address must select the rule: the rule set would never match otherwise")
	require.False(t, mustCIDRItem(t, "203.0.113.0/24").Match(metadata),
		"a non-matching answer must not match; stale or invented evidence must not make every rule fit")
}

// An answer published for matching only must be the evidence a domain destination uses even when a
// dialling resolve also left candidates behind. The newest resolution wins, and using both would
// let a rule match an address the current answer no longer returns.
func TestIPCIDRMatchOnlyAddressesWinOverCandidates(t *testing.T) {
	metadata := &adapter.InboundContext{
		Destination:          domainDestination("both.example"),
		MatchAddresses:       []netip.Addr{netip.MustParseAddr("203.0.113.9")},
		DestinationAddresses: []netip.Addr{netip.MustParseAddr("198.51.100.10")},
	}

	require.False(t, mustCIDRItem(t, "198.51.100.0/24").Match(metadata),
		"the candidate list is the OLDER answer; matching must read the newer matching evidence only")
	require.True(t, mustCIDRItem(t, "203.0.113.0/24").Match(metadata),
		"and the newer answer must be the one that matches")
}

// The literal destination is the address in use. This is the conservative boundary the feature
// documents for a sniffed name on a literal target, and it is also what a non-match-only resolve
// already does (the candidate list never displaces a literal): the two modes must not disagree
// about the same rule.
func TestIPCIDRLiteralDestinationWinsOverMatchOnlyAddresses(t *testing.T) {
	metadata := &adapter.InboundContext{
		Destination:    M.SocksaddrFrom(netip.MustParseAddr("10.1.2.3"), 443),
		Domain:         "sniffed.example",
		MatchAddresses: []netip.Addr{netip.MustParseAddr("198.51.100.10")},
	}

	require.False(t, mustCIDRItem(t, "198.51.100.0/24").Match(metadata),
		"a CDN answer must not hide the address the client really connected to")
	require.True(t, mustCIDRItem(t, "10.0.0.0/8").Match(metadata),
		"the literal destination must still be the one that matches")
}

// The DNS-response mode is a different question ("what the response said"), so it must not consume
// the route action's matching evidence.
func TestIPCIDRResponseModeIgnoresMatchOnlyAddresses(t *testing.T) {
	metadata := &adapter.InboundContext{
		Destination:                         domainDestination("response.example"),
		DestinationAddressMatchFromResponse: true,
		MatchAddresses:                      []netip.Addr{netip.MustParseAddr("198.51.100.10")},
	}

	item := mustCIDRItem(t, "198.51.100.0/24")
	require.False(t, item.Match(metadata),
		"a DNS response rule must judge the response, not the route action's evidence")

	// The legacy accept_empty behaviour still applies when the response exposes nothing, and it is
	// the only fallback - it must not be replaced by the match-only answer.
	metadata.IPCIDRAcceptEmpty = true
	require.True(t, item.Match(metadata),
		"rule_set_ip_cidr_accept_empty keeps meaning 'no response address to judge'")
}

// A source condition must never read an address that describes the DESTINATION.
func TestIPCIDRSourceModeIgnoresMatchOnlyAddresses(t *testing.T) {
	metadata := &adapter.InboundContext{
		Source:            M.SocksaddrFrom(netip.MustParseAddr("192.0.2.9"), 1234),
		Destination:       domainDestination("source.example"),
		IPCIDRMatchSource: true,
		MatchAddresses:    []netip.Addr{netip.MustParseAddr("198.51.100.10")},
	}

	require.False(t, mustCIDRItem(t, "198.51.100.0/24").Match(metadata),
		"an address resolved for the destination must never satisfy a source condition")

	sourceItem, err := NewIPCIDRItem(true, []*badoption.Prefixable{prefixable("192.0.2.0/24")})
	require.NoError(t, err)
	require.True(t, sourceItem.Match(metadata),
		"the real source must still be the one a source condition judges")
}

// No evidence at all still means "nothing to match", not "everything matches".
func TestIPCIDRWithoutAnyAddressKeepsAcceptEmpty(t *testing.T) {
	metadata := &adapter.InboundContext{Destination: domainDestination("empty.example")}
	require.False(t, mustCIDRItem(t, "198.51.100.0/24").Match(metadata))

	metadata.IPCIDRAcceptEmpty = true
	require.True(t, mustCIDRItem(t, "198.51.100.0/24").Match(metadata))
}

// `ip_is_private` has the same precedence, and the dangerous direction is a private literal that a
// public answer would otherwise hide.
func TestIPIsPrivateReadsMatchOnlyAddresses(t *testing.T) {
	privateAnswer := &adapter.InboundContext{
		Destination:    domainDestination("lan.example"),
		MatchAddresses: []netip.Addr{netip.MustParseAddr("10.0.0.7")},
	}
	require.True(t, NewIPIsPrivateItem(false).Match(privateAnswer),
		"a private answer must satisfy ip_is_private")

	publicAnswer := &adapter.InboundContext{
		Destination:    domainDestination("wan.example"),
		MatchAddresses: []netip.Addr{netip.MustParseAddr("198.51.100.10")},
	}
	require.False(t, NewIPIsPrivateItem(false).Match(publicAnswer),
		"a public answer must not satisfy ip_is_private")
}

func TestIPIsPrivateLiteralDestinationWinsOverMatchOnlyAddresses(t *testing.T) {
	// A literal private target whose sniffed name resolves to a public address: the flow is still
	// reaching a private address, and a malicious or split-horizon answer must not make it look
	// public.
	metadata := &adapter.InboundContext{
		Destination:    M.SocksaddrFrom(netip.MustParseAddr("10.1.2.3"), 443),
		Domain:         "sniffed.example",
		MatchAddresses: []netip.Addr{netip.MustParseAddr("198.51.100.10")},
	}
	require.True(t, NewIPIsPrivateItem(false).Match(metadata),
		"the private literal must keep matching; hiding it would let an answer downgrade the check")
}

func TestIPIsPrivateResponseModeIgnoresMatchOnlyAddresses(t *testing.T) {
	metadata := &adapter.InboundContext{
		Destination:                         domainDestination("response.example"),
		DestinationAddressMatchFromResponse: true,
		MatchAddresses:                      []netip.Addr{netip.MustParseAddr("10.0.0.7")},
	}
	require.False(t, NewIPIsPrivateItem(false).Match(metadata),
		"a DNS response rule must not be satisfied by the route action's evidence")
}

func TestSourceIPIsPrivateIgnoresMatchOnlyAddresses(t *testing.T) {
	metadata := &adapter.InboundContext{
		Source:         M.SocksaddrFrom(netip.MustParseAddr("198.51.100.20"), 1234),
		Destination:    domainDestination("source.example"),
		MatchAddresses: []netip.Addr{netip.MustParseAddr("10.0.0.7")},
	}
	require.False(t, NewIPIsPrivateItem(true).Match(metadata),
		"a destination answer must never make the SOURCE look private")
}

// `ip_version` is deliberately NOT part of this: it describes the destination's own family, which
// a resolve action never changes in either mode. Pinning that here keeps a later "consistency" edit
// from attaching the field to a condition the feature does not intend to affect.
func TestIPVersionIgnoresMatchOnlyAddresses(t *testing.T) {
	metadata := &adapter.InboundContext{
		Destination:    domainDestination("version.example"),
		MatchAddresses: []netip.Addr{netip.MustParseAddr("2001:db8::8")},
	}
	require.False(t, NewIPVersionItem(true).Match(metadata),
		"a named destination has no family of its own; ip_version must not read matching evidence")
}

// An IP `rule_set` compiles to the same IPCIDRItem, so the answer must select the SET too - the
// modern rule syntax is where most users express "route cn IPs directly", and a feature that only
// worked for inline `ip_cidr` would be half a feature.
func TestRuleSetIPConditionReadsMatchOnlyAddresses(t *testing.T) {
	ipItem := mustCIDRItem(t, "198.51.100.0/24")
	ruleSet := newLocalRuleSetForTest("match-only-set", headlessDefaultRule(t, func(rule *abstractDefaultRule) {
		rule.destinationIPCIDRItems = append(rule.destinationIPCIDRItems, ipItem)
		rule.allItems = append(rule.allItems, ipItem)
	}))
	item := &RuleSetItem{setList: []adapter.RuleSet{ruleSet}}

	matching := &adapter.InboundContext{
		Destination:    domainDestination("ruleset.example"),
		MatchAddresses: []netip.Addr{netip.MustParseAddr("198.51.100.10")},
	}
	require.True(t, item.Match(matching), "a matching answer must select the rule set")

	notMatching := &adapter.InboundContext{
		Destination:    domainDestination("ruleset.example"),
		MatchAddresses: []netip.Addr{netip.MustParseAddr("203.0.113.9")},
	}
	require.False(t, item.Match(notMatching), "a non-matching answer must not select it")
}

// preferredAddressOutbound is only the PreferredAddress half of an outbound; the embedded interface
// supplies the rest, which this test never reaches.
type preferredAddressOutbound struct {
	adapter.Outbound
	preferred []netip.Addr
}

func (o *preferredAddressOutbound) PreferredDomain(metadata *adapter.InboundContext, domain string) bool {
	return false
}

func (o *preferredAddressOutbound) PreferredAddress(metadata *adapter.InboundContext, address netip.Addr) bool {
	return slices.Contains(o.preferred, address)
}

func TestPreferredByReadsMatchOnlyAddresses(t *testing.T) {
	outbound := &preferredAddressOutbound{preferred: []netip.Addr{netip.MustParseAddr("198.51.100.10")}}
	item := &PreferredByItem{outbounds: []adapter.OutboundWithPreferredRoutes{outbound}}

	named := &adapter.InboundContext{
		Destination:    domainDestination("preferred.example"),
		MatchAddresses: []netip.Addr{netip.MustParseAddr("198.51.100.10")},
	}
	require.True(t, item.Match(named),
		"an address published for matching must be able to select the outbound that prefers it")

	// A literal destination is the address in use: matching evidence about a sniffed name must not
	// override it here either, while the historical candidate list stays additive.
	literal := &adapter.InboundContext{
		Destination:    M.SocksaddrFrom(netip.MustParseAddr("10.1.2.3"), 443),
		Domain:         "sniffed.example",
		MatchAddresses: []netip.Addr{netip.MustParseAddr("198.51.100.10")},
	}
	require.False(t, item.Match(literal),
		"a sniffed name's answer must not make a literal destination preferred")

	literal.DestinationAddresses = []netip.Addr{netip.MustParseAddr("198.51.100.10")}
	require.True(t, item.Match(literal),
		"the dial candidate list stays additive for a literal destination, as before")
}
