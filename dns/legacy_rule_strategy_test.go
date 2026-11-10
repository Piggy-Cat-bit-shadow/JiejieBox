package dns

import (
	"context"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"

	mDNS "github.com/miekg/dns"
	"github.com/stretchr/testify/require"
)

// The legacy DNS-rule family strategy must survive the streaming sub-query (§E).
//
// # The defect these pin
//
// LookupFamilies asks for one family per work item by writing BOTH LookupStrategy and Strategy. A
// legacy `dns.rules[].strategy` writes only Strategy, and Client.Lookup used to decide the QTYPE
// from LookupStrategy and then overwrite the Strategy handed to the exchange layer. So a rule
// saying ipv4_only for the AAAA item took effect nowhere: the AAAA was still sent upstream, and the
// caller believed its restriction had been honoured. The mirror case was worse - under a
// caller-forced ipv4_only the rule could make the branch query AAAA and then publish the answer
// under its own ipv4 label.
//
// These drive the real Router in legacy rule mode, a real rule, and a transport that records every
// QTYPE it receives, so the assertion is about what would have gone to the user's resolver.

// legacyFamilyStrategyRule is a DNS route rule that records a family decision the way
// `dns.rules[].strategy` does. It matches every name.
func legacyFamilyStrategyRule(server string, strategy C.DomainStrategy) option.DNSRule {
	return option.DNSRule{
		Type: C.RuleTypeDefault,
		DefaultOptions: option.DefaultDNSRule{
			DNSRuleAction: option.DNSRuleAction{
				Action: C.RuleActionTypeRoute,
				RouteOptions: option.DNSRouteActionOptions{
					Server: server,
					AbstractDNSRouteActionOptions: option.AbstractDNSRouteActionOptions{
						Strategy: option.DomainStrategy(strategy),
					},
				},
			},
		},
	}
}

// legacyRuleRouter builds a Router in legacy rule mode over a recording transport.
func legacyRuleRouter(t *testing.T, transport adapter.DNSTransport, rawRules []option.DNSRule) *Router {
	t.Helper()
	client := NewClient(ClientOptions{Context: context.Background(), Logger: log.NewNOPFactory().Logger()})
	client.Start()
	return &Router{
		ctx:    context.Background(),
		logger: log.NewNOPFactory().Logger(),
		client: client,
		transport: &fakeDNSTransportManager{
			transports:       map[string]adapter.DNSTransport{"final": transport},
			defaultTransport: transport,
		},
		rules:         raceTestRules(t, rawRules),
		legacyDNSMode: true,
	}
}

// familyResults collects what LookupFamilies published.
type familyResults struct {
	access  sync.Mutex
	results []adapter.DNSFamilyResult
}

func (f *familyResults) publish(result adapter.DNSFamilyResult) {
	f.access.Lock()
	defer f.access.Unlock()
	f.results = append(f.results, result)
}

func (f *familyResults) published() []adapter.DNSFamilyResult {
	f.access.Lock()
	defer f.access.Unlock()
	return append([]adapter.DNSFamilyResult(nil), f.results...)
}

func requireAddressFamily(t *testing.T, published []adapter.DNSFamilyResult, ipv6 bool, expected string) {
	t.Helper()
	for _, result := range published {
		if result.IPv6 != ipv6 {
			continue
		}
		require.Equal(t, []string{expected}, addressStrings(result.Addresses),
			"the %v family must be published with the address the transport answered", ipv6)
		return
	}
	t.Fatalf("no result was published for the %v family: %+v", ipv6, published)
}

func addressStrings(addresses []netip.Addr) []string {
	texts := make([]string, len(addresses))
	for index, address := range addresses {
		texts[index] = address.String()
	}
	return texts
}

// TestLegacyRuleFamilyStrategySurvivesTheStreamingSubQuery is the regression.
//
// The operator forbids IPv6 for this name through a legacy rule. The AAAA work item asks for IPv6
// by construction, so the two disagree; the item must issue nothing rather than send the AAAA.
func TestLegacyRuleFamilyStrategySurvivesTheStreamingSubQuery(t *testing.T) {
	transport := &queryRecordingTransport{addressA: "192.0.2.1", address6: "2001:db8::1"}
	router := legacyRuleRouter(t, transport, []option.DNSRule{
		legacyFamilyStrategyRule("final", C.DomainStrategyIPv4Only),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var published familyResults
	err := router.LookupFamilies(ctx, "legacy.test.", adapter.DNSQueryOptions{}, published.publish)
	require.NoError(t, err, "one family answered, so the stream succeeded")

	observed := transport.observed()
	require.Contains(t, observed, mDNS.TypeA, "the admitted family must still be queried")
	require.NotContains(t, observed, mDNS.TypeAAAA,
		"the rule forbids IPv6 for this name: the AAAA query must never reach the resolver. "+
			"Observed %v", observed)

	requireAddressFamily(t, published.published(), false, "192.0.2.1")
}

// TestLegacyRuleFamilyStrategyMirror is the reverse direction: the rule forbids IPv4, so the A item
// must issue nothing while the AAAA item is answered.
func TestLegacyRuleFamilyStrategyMirror(t *testing.T) {
	transport := &queryRecordingTransport{addressA: "192.0.2.1", address6: "2001:db8::1"}
	router := legacyRuleRouter(t, transport, []option.DNSRule{
		legacyFamilyStrategyRule("final", C.DomainStrategyIPv6Only),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var published familyResults
	err := router.LookupFamilies(ctx, "legacy.test.", adapter.DNSQueryOptions{}, published.publish)
	require.NoError(t, err)

	observed := transport.observed()
	require.Contains(t, observed, mDNS.TypeAAAA)
	require.NotContains(t, observed, mDNS.TypeA,
		"the rule forbids IPv4 for this name, so the A query must not be sent. Observed %v", observed)

	requireAddressFamily(t, published.published(), true, "2001:db8::1")
}

// TestLegacyRulePreferenceIsNotARestriction is the control that keeps the fix from overreaching.
//
// A preference orders the families; it does not forbid either one. Both families must still be
// queried, or the fix would have turned every `prefer_*` rule into a single-family policy.
func TestLegacyRulePreferenceIsNotARestriction(t *testing.T) {
	transport := &queryRecordingTransport{addressA: "192.0.2.1", address6: "2001:db8::1"}
	router := legacyRuleRouter(t, transport, []option.DNSRule{
		legacyFamilyStrategyRule("final", C.DomainStrategyPreferIPv4),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var published familyResults
	err := router.LookupFamilies(ctx, "legacy.test.", adapter.DNSQueryOptions{}, published.publish)
	require.NoError(t, err)

	observed := transport.observed()
	require.Contains(t, observed, mDNS.TypeA, "a preference must not suppress a family")
	require.Contains(t, observed, mDNS.TypeAAAA)

	requireAddressFamily(t, published.published(), false, "192.0.2.1")
	requireAddressFamily(t, published.published(), true, "2001:db8::1")
}
