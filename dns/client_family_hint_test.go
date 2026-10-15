package dns

import (
	"context"
	"net"
	"net/netip"
	"testing"

	mDNS "github.com/miekg/dns"
	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/stretchr/testify/require"
)

// Family-policy filtering of SVCB/HTTPS ipv4hint and ipv6hint.
//
// # The defect these pin
//
// The filter that removes the hint of the family a strict strategy excludes ran only on the way INTO
// the cache. The cache key carries no strategy, so an entry stored under one strategy answered every
// later query under any other. Two holes came out of that one mechanism, and they are opposite:
//
//	store AsIS, read ipv4_only    the excluded hint came back with the cached answer, so a strict
//	                              family strategy was defeated for the entry's whole lifetime
//	store ipv4_only, read AsIS    the entry stayed hint-less even though "this name has no IPv6
//	                              hint" was never true - it was this caller's policy
//
// The first is the one that matters: a policy must not be defeatable by whichever caller happened to
// populate the entry first.
//
// # Why the assertions read the HINT, not the answer count
//
// The upstream query count proves the cache was hit at all - without it, a test that only inspected
// the returned hints would pass even if the cache never answered. Both are asserted, in that order.

// httpsHintTransport replies to every HTTPS query with a record carrying both hints.
type httpsHintTransport struct {
	queryCount int
}

func (t *httpsHintTransport) Start(stage adapter.StartStage, scope *adapter.Scope) error { return nil }
func (t *httpsHintTransport) Close() error                                               { return nil }
func (t *httpsHintTransport) Type() string                                               { return "https-hint" }
func (t *httpsHintTransport) Tag() string                                                { return "https-hint" }
func (t *httpsHintTransport) Dependencies() []string                                     { return nil }
func (t *httpsHintTransport) Reset()                                                     {}

func (t *httpsHintTransport) Exchange(_ context.Context, message *mDNS.Msg) (*mDNS.Msg, error) {
	t.queryCount++
	return httpsHintResponse(message, 300), nil
}

func (t *httpsHintTransport) ExchangeAsync(_ context.Context, message *mDNS.Msg, callback func(response *mDNS.Msg, err error)) {
	go func() {
		callback(t.Exchange(context.Background(), message))
	}()
}

// httpsHintResponse builds a NOERROR HTTPS answer carrying BOTH family hints, which is the ordinary
// shape of a real SVCB answer and the precondition for observing the filter at all.
func httpsHintResponse(request *mDNS.Msg, timeToLive uint32) *mDNS.Msg {
	response := new(mDNS.Msg)
	response.SetReply(request)
	response.Answer = []mDNS.RR{&mDNS.HTTPS{
		SVCB: mDNS.SVCB{
			Hdr: mDNS.RR_Header{
				Name:   request.Question[0].Name,
				Rrtype: mDNS.TypeHTTPS,
				Class:  mDNS.ClassINET,
				Ttl:    timeToLive,
			},
			Priority: 1,
			Target:   ".",
			Value: []mDNS.SVCBKeyValue{
				&mDNS.SVCBIPv4Hint{Hint: []net.IP{net.IPv4(192, 0, 2, 7).To4()}},
				&mDNS.SVCBIPv6Hint{Hint: []net.IP{net.ParseIP("2001:db8::7")}},
			},
		},
	}}
	return response
}

// hintKeys reports which family hints the response carries, so the assertion is about the hint set
// rather than about slice positions.
func hintKeys(t *testing.T, response *mDNS.Msg) (hasIPv4, hasIPv6 bool) {
	t.Helper()
	for _, rr := range response.Answer {
		https, isHTTPS := rr.(*mDNS.HTTPS)
		if !isHTTPS {
			continue
		}
		for _, value := range https.SVCB.Value {
			switch value.Key() {
			case mDNS.SVCB_IPV4HINT:
				hasIPv4 = true
			case mDNS.SVCB_IPV6HINT:
				hasIPv6 = true
			}
		}
	}
	return hasIPv4, hasIPv6
}

func newHTTPSHintClient(t *testing.T) *Client {
	t.Helper()
	client := NewClient(ClientOptions{
		Context: context.Background(),
		Logger:  log.NewNOPFactory().Logger(),
	})
	client.Start()
	return client
}

func httpsQuery(name string) *mDNS.Msg {
	return &mDNS.Msg{
		MsgHdr: mDNS.MsgHdr{Id: 1, RecursionDesired: true},
		Question: []mDNS.Question{{
			Name:   name,
			Qtype:  mDNS.TypeHTTPS,
			Qclass: mDNS.ClassINET,
		}},
	}
}

// TestHTTPSHint_StrictStrategyIsNotDefeatedByACacheHit is the primary regression.
//
// An entry populated with no strategy carries both hints. A later ipv4_only query must not see the
// IPv6 hint, even though the only thing that changed is who is asking.
func TestHTTPSHint_StrictStrategyIsNotDefeatedByACacheHit(t *testing.T) {
	transport := &httpsHintTransport{}
	client := newHTTPSHintClient(t)
	ctx := context.Background()

	// Populate under AsIS, the strategy that filters nothing.
	first, err := client.Exchange(ctx, transport, httpsQuery("hints.example."), adapter.DNSQueryOptions{}, nil)
	require.NoError(t, err)
	require.Equal(t, 1, transport.queryCount)
	hasIPv4, hasIPv6 := hintKeys(t, first)
	require.True(t, hasIPv4 && hasIPv6, "precondition: the upstream answer must carry both hints")

	// Read it back under ipv4_only. It must be served from the cache...
	second, err := client.Exchange(ctx, transport, httpsQuery("hints.example."), adapter.DNSQueryOptions{
		Strategy: C.DomainStrategyIPv4Only,
	}, nil)
	require.NoError(t, err)
	require.Equal(t, 1, transport.queryCount,
		"precondition: the second query must have been answered from the cache, or this test proves "+
			"nothing about the read path")

	// ...and it must have been filtered for THIS caller.
	hasIPv4, hasIPv6 = hintKeys(t, second)
	require.True(t, hasIPv4, "ipv4_only must keep the IPv4 hint")
	require.False(t, hasIPv6,
		"ipv4_only must not receive an IPv6 hint from a cache entry populated under AsIS: a strict "+
			"family strategy must not be defeated by whichever strategy filled the entry first")
}

// TestHTTPSHint_ExcludedHintIsSymmetric is the mirror direction.
func TestHTTPSHint_ExcludedHintIsSymmetric(t *testing.T) {
	transport := &httpsHintTransport{}
	client := newHTTPSHintClient(t)
	ctx := context.Background()

	first, err := client.Exchange(ctx, transport, httpsQuery("v6only.example."), adapter.DNSQueryOptions{
		Strategy: C.DomainStrategyIPv6Only,
	}, nil)
	require.NoError(t, err)
	hasIPv4, hasIPv6 := hintKeys(t, first)
	require.False(t, hasIPv4, "ipv6_only must not return an IPv4 hint")
	require.True(t, hasIPv6, "ipv6_only must keep the IPv6 hint")

	// The stored entry must not have been narrowed for everybody: a later caller with no family
	// policy is asking a different question and must get both hints back.
	second, err := client.Exchange(ctx, transport, httpsQuery("v6only.example."), adapter.DNSQueryOptions{}, nil)
	require.NoError(t, err)
	require.Equal(t, 1, transport.queryCount, "precondition: served from the cache")
	hasIPv4, hasIPv6 = hintKeys(t, second)
	require.True(t, hasIPv4,
		"a caller with no family policy must still receive the IPv4 hint: the filter is the CALLER's "+
			"policy, not a property of the name")
	require.True(t, hasIPv6)
}

// TestHTTPSHint_CacheEntryIsNotMutatedByAReader pins the ownership rule that makes the read-side
// filter safe: filtering for one caller must not rewrite the stored value for the next.
//
// It is the failure mode a naive read-side fix introduces, and it is invisible without asking twice
// with different strategies in the order that would expose it.
func TestHTTPSHint_CacheEntryIsNotMutatedByAReader(t *testing.T) {
	transport := &httpsHintTransport{}
	client := newHTTPSHintClient(t)
	ctx := context.Background()

	if _, err := client.Exchange(ctx, transport, httpsQuery("stable.example."), adapter.DNSQueryOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	// A strict reader runs first and would corrupt the entry if it filtered in place.
	narrowed, err := client.Exchange(ctx, transport, httpsQuery("stable.example."), adapter.DNSQueryOptions{
		Strategy: C.DomainStrategyIPv4Only,
	}, nil)
	require.NoError(t, err)
	require.False(t, func() bool { _, v6 := hintKeys(t, narrowed); return v6 }())

	// The next unrestricted reader must still see both hints.
	restored, err := client.Exchange(ctx, transport, httpsQuery("stable.example."), adapter.DNSQueryOptions{}, nil)
	require.NoError(t, err)
	require.Equal(t, 1, transport.queryCount)
	hasIPv4, hasIPv6 := hintKeys(t, restored)
	require.True(t, hasIPv4, "the cached entry must be unchanged for the next reader")
	require.True(t, hasIPv6,
		"a strict reader must not have narrowed the SHARED cache entry: the filter belongs to the "+
			"caller, so it must be applied to a copy")
}

// TestHTTPSHint_NonHTTPSQuestionIsUntouched pins that the read-side filter cannot disturb the
// ordinary A/AAAA paths, which is most of the traffic.
func TestHTTPSHint_NonHTTPSQuestionIsUntouched(t *testing.T) {
	transport := &negativeTransport{respond: func(request *mDNS.Msg) *mDNS.Msg {
		return FixedResponse(request.Id, request.Question[0], []netip.Addr{netip.MustParseAddr("192.0.2.7")}, 300)
	}}
	client := newNegativeClient(t, transport)
	ctx := context.Background()

	if _, err := client.Exchange(ctx, transport, negativeQuery("plain.example.", mDNS.TypeA), adapter.DNSQueryOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	second, err := client.Exchange(ctx, transport, negativeQuery("plain.example.", mDNS.TypeA), adapter.DNSQueryOptions{
		Strategy: C.DomainStrategyIPv4Only,
	}, nil)
	require.NoError(t, err)
	require.Equal(t, 1, transport.count())
	require.NotEmpty(t, second.Answer, "an A answer must be returned untouched under a strict strategy")
}
