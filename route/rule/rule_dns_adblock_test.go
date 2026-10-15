package rule

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/require"
)

// The DNS-only boundary and the terminal semantics of `adblock`, checked where a configuration is
// accepted or refused rather than where it runs.

// adblockDNSRuleOptions is the documented user-facing form: a condition plus `action: adblock`.
func adblockDNSRuleOptions(domainSuffixes ...string) option.DNSRule {
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

func TestNewDNSRuleBuildsAdblockAction(t *testing.T) {
	t.Parallel()

	rule, err := NewDNSRule(context.Background(), log.NewNOPFactory().NewLogger("dns"),
		adblockDNSRuleOptions("ads.example"), false, false)
	require.NoError(t, err)
	require.NotNil(t, rule.Action())
	require.Equal(t, C.RuleActionTypeAdblock, rule.Action().Type())
	require.Equal(t, "adblock", rule.Action().String())

	action, isAdblock := rule.Action().(*RuleActionAdblock)
	require.True(t, isAdblock)
	// The action is a terminal policy verdict, so it ends the rule walk rather than deferring to
	// a transport the way sniff/resolve/evaluate do.
	require.True(t, adapter.IsFinalAction(action))
}

// TestAdblockResponseShape pins the response contract at the action, independent of the router.
//
// The router tests assert the same shape end to end; this one exists so a failure says whether the
// response builder or the rule walk broke.
func TestAdblockResponseShape(t *testing.T) {
	t.Parallel()

	action := &RuleActionAdblock{}
	request := &dns.Msg{
		MsgHdr:   dns.MsgHdr{Id: 0x4d2, RecursionDesired: true},
		Question: []dns.Question{{Name: "ads.example.", Qtype: dns.TypeHTTPS, Qclass: dns.ClassINET}},
	}
	response := action.Response(request)

	require.Equal(t, uint16(0x4d2), response.Id)
	require.Equal(t, dns.RcodeNameError, response.Rcode)
	require.True(t, response.Response)
	require.True(t, response.Authoritative)
	require.True(t, response.RecursionDesired)
	require.True(t, response.RecursionAvailable)
	require.False(t, response.Truncated)
	require.Empty(t, response.Answer)
	require.Equal(t, request.Question, response.Question)

	require.Len(t, response.Ns, 1)
	soa, isSOA := response.Ns[0].(*dns.SOA)
	require.True(t, isSOA, "the authority section must carry the SOA that makes the answer cacheable")
	require.Equal(t, "ads.example.", soa.Hdr.Name)
	require.Equal(t, C.DefaultDNSNegativeTTL, soa.Hdr.Ttl)
	require.Equal(t, C.DefaultDNSNegativeTTL, soa.Minttl)
	require.NotZero(t, soa.Hdr.Ttl)

	// The class of the denial follows the question: answering a CHAOS query with an IN record
	// would be a malformed response, not a stricter one.
	chaosRequest := &dns.Msg{
		MsgHdr:   dns.MsgHdr{Id: 1},
		Question: []dns.Question{{Name: "ads.example.", Qtype: dns.TypeTXT, Qclass: dns.ClassCHAOS}},
	}
	chaosResponse := action.Response(chaosRequest)
	chaosSOA, isSOA := chaosResponse.Ns[0].(*dns.SOA)
	require.True(t, isSOA)
	require.Equal(t, uint16(dns.ClassCHAOS), chaosSOA.Hdr.Class)
}

// TestAdblockResponseSurvivesAMalformedRequest keeps a hand-built rule from panicking the router.
//
// A question-less message never reaches a rule action through Router.Exchange, which rejects it
// earlier. The action is still reachable from Go, and indexing the question list without checking
// would turn a coding error into a crash in the packet path.
func TestAdblockResponseSurvivesAMalformedRequest(t *testing.T) {
	t.Parallel()

	action := &RuleActionAdblock{}
	response := action.Response(&dns.Msg{MsgHdr: dns.MsgHdr{Id: 9}})
	require.Equal(t, uint16(9), response.Id)
	require.Equal(t, dns.RcodeNameError, response.Rcode)
	require.Empty(t, response.Question)
	require.Empty(t, response.Ns, "there is no SOA to key on a name that was never given")
}

// TestDNSRuleActionAdblockRejectsRace is the runtime half of the schema rule.
//
// `race` on adblock would mean launching the upstream lookup this action exists to prevent, so the
// combination is refused rather than silently ignored. The schema omits the field; this covers a
// rule constructed in Go, or decoded by a caller that skipped schema validation.
func TestDNSRuleActionAdblockRejectsRace(t *testing.T) {
	t.Parallel()

	ruleOptions := adblockDNSRuleOptions("ads.example")
	ruleOptions.DefaultOptions.Race = true
	_, err := NewDNSRule(context.Background(), log.NewNOPFactory().NewLogger("dns"), ruleOptions, false, false)
	require.ErrorContains(t, err, "`race` is not supported by the adblock action")
}

// TestRouteRuleBuilderDoesNotKnowAdblock records what happens BELOW the decoder.
//
// `adblock` is not a route action, and a route rule cannot reach this builder with it: the option
// decoder refuses the action first (TestRouteRuleRejectsAdblockAction in option), which is where a
// user finds out. This test exists because the builder's answer is a panic rather than an error,
// and a panic in the rule builder is a fact worth pinning: it is only acceptable while nothing
// upstream of it can produce the value. If this assertion ever becomes a silent nil - or the
// decode check ever loosens - the failure mode changes from "config rejected" to "box crashes at
// startup", and that is the regression this records.
func TestRouteRuleBuilderDoesNotKnowAdblock(t *testing.T) {
	t.Parallel()

	require.Panics(t, func() {
		_, _ = NewRule(context.Background(), log.NewNOPFactory().NewLogger("router"), option.Rule{
			Type: C.RuleTypeDefault,
			DefaultOptions: option.DefaultRule{
				RawDefaultRule: option.RawDefaultRule{
					DomainSuffix: []string{"ads.example"},
				},
				RuleAction: option.RuleAction{
					Action: C.RuleActionTypeAdblock,
				},
			},
		}, false)
	}, "the route action builder must not silently accept a DNS-only action")
}

// TestAdblockLogicalRuleBuilds confirms the action is usable on the logical rule type too, since
// the union is shared and an operator may well write `{"type":"logical", ... ,"action":"adblock"}`.
func TestAdblockLogicalRuleBuilds(t *testing.T) {
	t.Parallel()

	rule, err := NewDNSRule(context.Background(), log.NewNOPFactory().NewLogger("dns"), option.DNSRule{
		Type: C.RuleTypeLogical,
		LogicalOptions: option.LogicalDNSRule{
			RawLogicalDNSRule: option.RawLogicalDNSRule{
				Mode: C.LogicalTypeOr,
				Rules: []option.DNSRule{{
					Type: C.RuleTypeDefault,
					DefaultOptions: option.DefaultDNSRule{
						RawDefaultDNSRule: option.RawDefaultDNSRule{DomainSuffix: []string{"ads.example"}},
					},
				}},
			},
			DNSRuleAction: option.DNSRuleAction{
				Action: C.RuleActionTypeAdblock,
			},
		},
	}, false, false)
	require.NoError(t, err)
	require.Equal(t, C.RuleActionTypeAdblock, rule.Action().Type())
	require.True(t, adapter.IsFinalAction(rule.Action()))
}
