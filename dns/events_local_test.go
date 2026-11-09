package dns

// O03 — the terminal verdicts the CLIENT never sees.
//
// The event stream was produced only by dns.Client: an upstream query, a cache hit, a joined
// exchange. A rule that rejects or answers a query locally returns before the client, so the query
// came back REFUSED (or NXDOMAIN, or an error for a drop) and both the event stream and the bounded
// history were empty. This file pins those terminations and the fact that they are recorded ONCE.

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	R "github.com/sagernet/sing-box/route/rule"

	mDNS "github.com/miekg/dns"

	"github.com/stretchr/testify/require"
)

func rejectTestRule(t *testing.T, suffix string, method string) option.DNSRule {
	t.Helper()
	return option.DNSRule{
		Type: C.RuleTypeDefault,
		DefaultOptions: option.DefaultDNSRule{
			RawDefaultDNSRule: option.RawDefaultDNSRule{DomainSuffix: []string{suffix}},
			DNSRuleAction: option.DNSRuleAction{
				Action:        C.RuleActionTypeReject,
				RejectOptions: option.RejectActionOptions{Method: method},
			},
		},
	}
}

// newVerdictFixture builds a router with the given rules and history enabled. The fake transports
// are hostile: any upstream exchange is counted, which is what makes "this verdict was local"
// observable rather than assumed.
//
// raceTestRouter builds a bare Router, while production construction installs the event bus and
// wires it into the client (NewRouter does both). The two lines below are that part of production
// wiring: without the bus r.events is nil and EnableHistory subscribes to nothing; without the
// client option the upstream attempt events never reach the same bus.
func newVerdictFixture(t *testing.T, raw []option.DNSRule) *deferralFixture {
	t.Helper()
	fixture := newDeferralFixture(t, raw)
	fixture.router.events = NewEventBus(defaultEventQueue)
	fixture.router.client = NewClient(ClientOptions{
		Context:      context.Background(),
		DisableCache: true,
		Logger:       log.NewNOPFactory().Logger(),
		EventBus:     fixture.router.events,
	})
	fixture.router.EnableHistory(HistoryOptions{})
	t.Cleanup(fixture.router.DisableHistory)
	return fixture
}

func requireNoUpstreamExchange(t *testing.T, fixture *deferralFixture) {
	t.Helper()
	require.Zero(t, fixture.blocked.queryCount.Load())
	require.Zero(t, fixture.curlServer.queryCount.Load())
	require.Zero(t, fixture.eval.queryCount.Load())
	require.Zero(t, fixture.final.queryCount.Load())
}

// awaitHistory reads the bounded history until it holds want records.
//
// The history's subscription is drained on its own goroutine by design (a slow reader must lose
// events instead of blocking a query), so a record is observable a moment after the exchange
// returns. The wait is on that fact, not on a fixed sleep.
func awaitHistory(t *testing.T, router *Router, want int) []HistoryRecord {
	t.Helper()
	var records []HistoryRecord
	require.Eventually(t, func() bool {
		var err error
		records, err = router.QueryHistory(HistoryQuery{})
		require.NoError(t, err)
		return len(records) == want
	}, 5*time.Second, time.Millisecond, "the history never held %d record(s)", want)
	return records
}

// TestALocalRejectIsRecordedAsTheQuerysFinalVerdict is the review reproduction: Router.Exchange hits
// a local reject, the caller receives REFUSED, and the history used to be empty.
func TestALocalRejectIsRecordedAsTheQuerysFinalVerdict(t *testing.T) {
	fixture := newVerdictFixture(t, []option.DNSRule{
		rejectTestRule(t, "example.org", C.RuleActionRejectMethodDefault),
	})

	events, unsubscribe := fixture.router.events.Subscribe(false, 0)
	defer unsubscribe()

	response, err := fixture.router.Exchange(context.Background(), deferralQuery("race.example.org.", mDNS.TypeA), adapter.DNSQueryOptions{})
	require.NoError(t, err)
	require.NotNil(t, response)
	require.Equal(t, mDNS.RcodeRefused, response.Rcode, "the caller receives REFUSED")
	requireNoUpstreamExchange(t, fixture)

	records := awaitHistory(t, fixture.router, 1)
	event := records[0].Event
	require.True(t, event.Final)
	require.Equal(t, QuerySourceRejected, event.Source)
	require.Equal(t, mDNS.RcodeRefused, event.RCode)
	require.Equal(t, "race.example.org.", event.Name)
	require.Equal(t, mDNS.TypeA, event.QType)
	require.NotEmpty(t, event.Rule, "the event names the rule that produced the verdict")
	require.Empty(t, event.TransportTag, "no transport answered: the path is recorded as absent, not guessed")
	require.Empty(t, event.Err)

	select {
	case streamed := <-events:
		require.True(t, streamed.Final, "the live stream carries the same terminal event")
		require.Equal(t, QuerySourceRejected, streamed.Source)
	case <-time.After(5 * time.Second):
		t.Fatal("the local reject never reached the event stream")
	}
}

// TestALocalDropIsRecordedWithoutARCode covers the no-response half: the verdict is an error, not an
// rcode, and the event must say so rather than inventing a response.
func TestALocalDropIsRecordedWithoutARCode(t *testing.T) {
	fixture := newVerdictFixture(t, []option.DNSRule{
		rejectTestRule(t, "example.org", C.RuleActionRejectMethodDrop),
	})

	response, err := fixture.router.Exchange(context.Background(), deferralQuery("race.example.org.", mDNS.TypeA), adapter.DNSQueryOptions{})
	require.Error(t, err, "a drop is reported to the caller as an error")
	require.Nil(t, response)
	require.True(t, errors.Is(err, R.ErrDrop), "the drop is the rule action's own sentinel")
	requireNoUpstreamExchange(t, fixture)

	records := awaitHistory(t, fixture.router, 1)
	event := records[0].Event
	require.True(t, event.Final)
	require.Equal(t, QuerySourceRejected, event.Source)
	require.Equal(t, -1, event.RCode, "no response was produced, so there is no rcode")
	require.NotEmpty(t, event.Err)
	require.NotEmpty(t, event.Rule)
}

// TestAnAdblockVerdictKeepsItsOwnSource distinguishes "the operator blocked this name" from "the
// rules rejected this query": same absence of transport, different decision.
func TestAnAdblockVerdictKeepsItsOwnSource(t *testing.T) {
	fixture := newVerdictFixture(t, []option.DNSRule{
		adblockTestRule(t, "example.org"),
	})

	response, err := fixture.router.Exchange(context.Background(), deferralQuery("race.example.org.", mDNS.TypeA), adapter.DNSQueryOptions{})
	require.NoError(t, err)
	require.NotNil(t, response)
	require.Equal(t, mDNS.RcodeNameError, response.Rcode)
	requireNoUpstreamExchange(t, fixture)

	records := awaitHistory(t, fixture.router, 1)
	require.Equal(t, QuerySourceBlocked, records[0].Event.Source)
	require.True(t, records[0].Event.Final)
}

// TestAnUpstreamAnswerIsNotAlsoRecordedAsAFinalVerdict is the no-double-count half.
//
// A query that reaches a transport has its client-side attempt event; the router must not add a
// final event for it, or one termination would be recorded twice and the history would read as if
// the query had two outcomes.
func TestAnUpstreamAnswerIsNotAlsoRecordedAsAFinalVerdict(t *testing.T) {
	fixture := newVerdictFixture(t, []option.DNSRule{
		previewTestRule(t, "example.org", "blocked"),
	})

	response, err := fixture.router.Exchange(context.Background(), deferralQuery("race.example.org.", mDNS.TypeA), adapter.DNSQueryOptions{})
	require.NoError(t, err)
	require.NotNil(t, response)
	require.EqualValues(t, 1, fixture.blocked.queryCount.Load())

	records := awaitHistory(t, fixture.router, 1)
	require.NotEmpty(t, records, "the client's attempt event is still recorded")
	for _, record := range records {
		require.False(t, record.Event.Final,
			"an upstream/cache answer is an attempt the client reported, not a router-level terminal verdict")
		require.Equal(t, QuerySourceUpstream, record.Event.Source)
	}
}

// TestTheAsyncLocalVerdictIsRecordedOnce is the async entry: hijacked packets and streams use it.
func TestTheAsyncLocalVerdictIsRecordedOnce(t *testing.T) {
	fixture := newVerdictFixture(t, []option.DNSRule{
		rejectTestRule(t, "example.org", C.RuleActionRejectMethodDefault),
	})

	var called atomic.Int32
	done := make(chan struct{})
	fixture.router.ExchangeAsync(context.Background(), deferralQuery("race.example.org.", mDNS.TypeA), adapter.DNSQueryOptions{},
		func(response *mDNS.Msg, err error) {
			require.NoError(t, err)
			require.NotNil(t, response)
			require.Equal(t, mDNS.RcodeRefused, response.Rcode)
			called.Add(1)
			close(done)
		})
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the async exchange never returned")
	}
	require.EqualValues(t, 1, called.Load())

	records := awaitHistory(t, fixture.router, 1)
	require.True(t, records[0].Event.Final)
	require.Equal(t, QuerySourceRejected, records[0].Event.Source)
}

// TestTheLegacyLocalRejectIsRecorded covers the legacy rule mode, which reaches the same verdict
// through a different walk.
func TestTheLegacyLocalRejectIsRecorded(t *testing.T) {
	fixture := newVerdictFixture(t, []option.DNSRule{
		rejectTestRule(t, "example.org", C.RuleActionRejectMethodDefault),
	})
	fixture.router.legacyDNSMode = true

	response, err := fixture.router.Exchange(context.Background(), deferralQuery("race.example.org.", mDNS.TypeA), adapter.DNSQueryOptions{})
	require.NoError(t, err)
	require.NotNil(t, response)
	require.Equal(t, mDNS.RcodeRefused, response.Rcode)

	records := awaitHistory(t, fixture.router, 1)
	require.True(t, records[0].Event.Final)
	require.Equal(t, QuerySourceRejected, records[0].Event.Source)
	require.Equal(t, mDNS.RcodeRefused, records[0].Event.RCode)
}

// TestAMalformedQuestionIsRecordedLocally covers the structural early answers prepareExchange
// produces before any rule runs.
func TestAMalformedQuestionIsRecordedLocally(t *testing.T) {
	fixture := newVerdictFixture(t, nil)
	message := &mDNS.Msg{
		MsgHdr: mDNS.MsgHdr{Id: 9, RecursionDesired: true},
		Question: []mDNS.Question{
			{Name: "race.example.org.", Qtype: mDNS.TypeA, Qclass: mDNS.ClassINET},
			{Name: "race.example.org.", Qtype: mDNS.TypeAAAA, Qclass: mDNS.ClassINET},
		},
	}
	response, err := fixture.router.Exchange(context.Background(), message, adapter.DNSQueryOptions{})
	require.NoError(t, err)
	require.NotNil(t, response)
	require.Equal(t, mDNS.RcodeFormatError, response.Rcode)

	records := awaitHistory(t, fixture.router, 1)
	require.True(t, records[0].Event.Final)
	require.Equal(t, mDNS.RcodeFormatError, records[0].Event.RCode)
}
