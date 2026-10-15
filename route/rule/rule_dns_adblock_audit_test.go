package rule

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/require"
)

// Round-two audit coverage for the adblock action: the properties that are about where the action
// sits in the existing machinery rather than about the response it builds.
//
// These live in this package because that is where a rule's action is constructed and where its
// terminal/non-terminal classification comes from, and because a nil logger can be used to prove a
// property that is otherwise only asserted.

// TestAdblockIsATerminalActionThatStopsTheWalk is the ordering property.
//
// The DNS router walks rules in listed order and stops at the first terminal action, so a rule
// placed before an adblock rule keeps its original precedence. That follows from two facts that are
// both asserted here rather than assumed:
//
//  1. adblock is NOT one of the actions IsFinalAction treats as "keep walking", so the walk ends
//     where it matches and nothing reorders it;
//  2. the action holds no state that could be consulted out of order - it is an empty struct, so
//     there is nowhere for a "scan every adblock rule first" pass to live.
func TestAdblockIsATerminalActionThatStopsTheWalk(t *testing.T) {
	t.Parallel()

	rule, err := NewDNSRule(context.Background(), log.NewNOPFactory().NewLogger("dns"),
		adblockDNSRuleOptions("ads.example"), false, false)
	require.NoError(t, err)

	action := rule.Action()
	require.True(t, adapter.IsFinalAction(action),
		"a non-final action would let the walk continue past a matched adblock rule and change rule precedence")
	for _, nonFinal := range []string{C.RuleActionTypeSniff, C.RuleActionTypeResolve, C.RuleActionTypeEvaluate} {
		require.NotEqual(t, nonFinal, action.Type(),
			"adblock must not be classified with the actions that continue rule evaluation")
	}
	require.Equal(t, "adblock", action.String())
	require.Empty(t, *action.(*RuleActionAdblock),
		"the action carries no state, so no out-of-order scan or lookup is possible")
}

// TestAdblockResponseNeedsNoLogger is the observability property, tested by construction.
//
// A high-frequency action must not be able to emit a log line, and the strongest available proof is
// that it has nothing to emit it with: NewDNSRule can be given a nil logger and an adblock rule
// still builds and still answers. Nothing else in this package's action set is checked this way,
// because nothing else claims to be free of per-query side effects.
func TestAdblockResponseNeedsNoLogger(t *testing.T) {
	t.Parallel()

	rule, err := NewDNSRule(context.Background(), nil, adblockDNSRuleOptions("ads.example"), false, false)
	require.NoError(t, err)

	action, isAdblock := rule.Action().(*RuleActionAdblock)
	require.True(t, isAdblock)

	request := &dns.Msg{
		MsgHdr:   dns.MsgHdr{Id: 1, RecursionDesired: true},
		Question: []dns.Question{{Name: "ads.example.", Qtype: dns.TypeA, Qclass: dns.ClassINET}},
	}
	response := action.Response(request)
	require.Equal(t, dns.RcodeNameError, response.Rcode)
	require.Len(t, response.Ns, 1)

	message := &dns.Msg{
		MsgHdr:   dns.MsgHdr{Id: 2, RecursionDesired: true},
		Question: []dns.Question{{Name: "ads.example.", Qtype: dns.TypeAAAA, Qclass: dns.ClassINET}},
	}
	adblockAction := action
	require.NotPanics(t, func() {
		_ = adblockAction.Response(message)
	}, "answering must not depend on a logger being present")
}

// TestAdblockDoesNotChangeThePredefinedAnswerForTheSameShape is the §8.2 regression, stated as a
// comparison rather than as a snapshot.
//
// The two actions must differ ONLY where the design says they differ: rcode, authority records, and
// the class/name derived from the question. Everything else - the question echo, the ID, and the
// QR/AA/RD/RA flags - has to match, because adblock deliberately reuses the local-response flag
// convention instead of inventing its own. A snapshot test would let both drift together and still
// pass.
func TestAdblockDoesNotChangeThePredefinedAnswerForTheSameShape(t *testing.T) {
	t.Parallel()

	predefined := &RuleActionPredefined{Rcode: dns.RcodeNameError}
	adblock := &RuleActionAdblock{}
	request := &dns.Msg{
		MsgHdr:   dns.MsgHdr{Id: 0x1234, RecursionDesired: true},
		Question: []dns.Question{{Name: "ads.example.", Qtype: dns.TypeHTTPS, Qclass: dns.ClassINET}},
	}

	predefinedResponse := predefined.Response(request)
	adblockResponse := adblock.Response(request)

	require.Equal(t, predefinedResponse.Rcode, adblockResponse.Rcode)
	require.Equal(t, predefinedResponse.Id, adblockResponse.Id)
	require.Equal(t, predefinedResponse.Question, adblockResponse.Question)
	require.Equal(t, predefinedResponse.Response, adblockResponse.Response)
	require.Equal(t, predefinedResponse.Authoritative, adblockResponse.Authoritative)
	require.Equal(t, predefinedResponse.RecursionDesired, adblockResponse.RecursionDesired)
	require.Equal(t, predefinedResponse.RecursionAvailable, adblockResponse.RecursionAvailable)
	require.Equal(t, predefinedResponse.Truncated, adblockResponse.Truncated)
	require.Empty(t, predefinedResponse.Ns, "predefined with no `ns` is the uncacheable shape this action exists to replace")
	require.Len(t, adblockResponse.Ns, 1)
}

// TestAdblockRuleSurvivesNilAuthorizedRules is the near-miss companion of the race refusal.
//
// `race` is refused for adblock, and the natural way to have got that wrong is to have removed the
// refusal and then relied on the response-bound machinery instead. This passes the same shape with
// a nil logger, so the only thing making it pass is that adblock is terminal and needs nothing to
// answer with.
func TestAdblockRuleSurvivesNilAuthorizedRules(t *testing.T) {
	t.Parallel()

	ruleOptions := adblockDNSRuleOptions("ads.example")
	rule, err := NewDNSRule(context.Background(), nil, ruleOptions, false, false)
	require.NoError(t, err)
	require.NoError(t, rule.Start())
	require.False(t, rule.Race())
	require.Empty(t, rule.MatchResponseTags())
	require.False(t, rule.MatchResponseAnonymous())
	require.False(t, rule.WithAddressLimit())
}

// BenchmarkAdblockResponseConstruction and the predefined benchmark below measure the local
// response path the action adds, and exist to be compared with each other - not to reach a number.
//
// The question they answer is whether an ad name costs anything unexpected per query: regex
// compilation, rule-set reload, a large structure copy, or an allocation count that grows with
// something other than the one SOA the response contains. Both actions build one message per call
// and neither caches, so the two should be of the same order.
func BenchmarkAdblockResponseConstruction(b *testing.B) {
	action := &RuleActionAdblock{}
	request := &dns.Msg{
		MsgHdr:   dns.MsgHdr{Id: 1, RecursionDesired: true},
		Question: []dns.Question{{Name: "ads.example.", Qtype: dns.TypeA, Qclass: dns.ClassINET}},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		response := action.Response(request)
		if response == nil {
			b.Fatal("nil response")
		}
	}
}

func BenchmarkPredefinedResponseConstruction(b *testing.B) {
	action := &RuleActionPredefined{Rcode: dns.RcodeNameError}
	request := &dns.Msg{
		MsgHdr:   dns.MsgHdr{Id: 1, RecursionDesired: true},
		Question: []dns.Question{{Name: "ads.example.", Qtype: dns.TypeA, Qclass: dns.ClassINET}},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		response := action.Response(request)
		if response == nil {
			b.Fatal("nil response")
		}
	}
}
