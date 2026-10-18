package dns

import (
	"context"
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/netcap"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"

	mDNS "github.com/miekg/dns"
	"github.com/stretchr/testify/require"
)

// These tests are the DNS half of the entry decision, written as the three properties that make it
// safe to ship:
//
//  1. an application AAAA is answered locally, with a protocol-correct NODATA, before any rule,
//     transport, mapper or cache is touched - and the A question for the same name is untouched;
//  2. nothing else is answered locally: a proxy inbound, a remote endpoint, an internal exchange,
//     a manager with no opinion and a decision of "present" all keep their previous behaviour;
//  3. the dialer's own resolution path (`Lookup`) is not gated, because the destination it resolves
//     may be reached through a proxy that can carry IPv6.

// presentmentReaderStub is the network manager's reader, reduced to the one method the router uses.
type presentmentReaderStub struct {
	presentment netcap.IPv6Presentment
}

func (s presentmentReaderStub) IPv6EntryPresentment() netcap.IPv6Presentment {
	return s.presentment
}

// entryGateRouter builds a router that can answer a query end to end, with a counting transport as
// the last hop. The transport answers immediately so a test never depends on timing.
func entryGateRouter(t *testing.T, reader adapter.IPv6EntryPresentmentReader) (*Router, *fakeDNSTransport) {
	t.Helper()
	transport := &fakeDNSTransport{
		tag:       "final",
		address:   netip.MustParseAddr("203.0.113.7"),
		immediate: true,
	}
	router := &Router{
		ctx:    context.Background(),
		logger: log.NewNOPFactory().Logger(),
		transport: &fakeDNSTransportManager{
			transports:       map[string]adapter.DNSTransport{transport.tag: transport},
			defaultTransport: transport,
		},
		client: NewClient(ClientOptions{
			Context:      context.Background(),
			DisableCache: true,
			Logger:       log.NewNOPFactory().Logger(),
		}),
		entryIPv6Presentment: reader,
	}
	return router, transport
}

func tunQueryContext(inboundType string) context.Context {
	return adapter.WithContext(context.Background(), &adapter.InboundContext{
		Inbound:     "test-in",
		InboundType: inboundType,
	})
}

func newQuestion(name string, qtype uint16) *mDNS.Msg {
	message := new(mDNS.Msg)
	message.SetQuestion(name, qtype)
	return message
}

// TestAnApplicationAAAAIsAnsweredWithNoDataBeforeTheTransport is the gate itself.
func TestAnApplicationAAAAIsAnsweredWithNoDataBeforeTheTransport(t *testing.T) {
	router, transport := entryGateRouter(t, presentmentReaderStub{presentment: netcap.SuppressIPv6})
	query := newQuestion("example.com.", mDNS.TypeAAAA)

	response, err := router.Exchange(tunQueryContext(C.TypeTun), query, adapter.DNSQueryOptions{})
	require.NoError(t, err)
	require.NotNil(t, response)

	require.Equal(t, 0, int(transport.queryCount.Load()),
		"the AAAA reached the transport. The point of the entry gate is that the suppression happens "+
			"before a rule, a transport, the Fake-IP store and the cache: %d exchange(s) were made", transport.queryCount.Load())
	require.Equal(t, mDNS.RcodeSuccess, response.Rcode, "the name may exist; only the record type is absent")
	require.Equal(t, query.Id, response.Id)
	require.Len(t, response.Question, 1)
	require.Equal(t, query.Question[0], response.Question[0])
	require.Empty(t, response.Answer, "a NOERROR answer with no records is the protocol's NODATA")
	require.True(t, response.Response)
	require.True(t, response.RecursionAvailable,
		"the answer is authoritative for this process exactly as far as an upstream empty answer is")
}

// TestTheGateLeavesEveryOtherQueryAlone is the scope test: everything that is not a local
// application's AAAA must reach the transport exactly as before.
func TestTheGateLeavesEveryOtherQueryAlone(t *testing.T) {
	router, transport := entryGateRouter(t, presentmentReaderStub{presentment: netcap.SuppressIPv6})

	before := transport.queryCount.Load()

	// An A query from the same application: the IPv4 half of the same name must keep working.
	response, err := router.Exchange(tunQueryContext(C.TypeTun), newQuestion("example.com.", mDNS.TypeA), adapter.DNSQueryOptions{})
	require.NoError(t, err)
	require.Len(t, response.Answer, 1, "the A record must still be delivered")
	require.Equal(t, before+1, transport.queryCount.Load())

	// An AAAA from a proxy inbound: the application chose to send DNS into a proxy, and the address
	// it gets back is one that proxy can carry.
	_, err = router.Exchange(tunQueryContext(C.TypeSOCKS), newQuestion("example.com.", mDNS.TypeAAAA), adapter.DNSQueryOptions{})
	require.NoError(t, err)
	require.Equal(t, before+2, transport.queryCount.Load())

	// An AAAA with no inbound metadata: an internal exchange (ECH, a transport, a service).
	_, err = router.Exchange(context.Background(), newQuestion("example.com.", mDNS.TypeAAAA), adapter.DNSQueryOptions{})
	require.NoError(t, err)
	require.Equal(t, before+3, transport.queryCount.Load())

	// An `hijack_dns` rule action hands the query to this same router from whatever inbound carried
	// it; a mixed inbound is the ordinary case and must not be suppressed either.
	_, err = router.Exchange(tunQueryContext(C.TypeMixed), newQuestion("example.com.", mDNS.TypeAAAA), adapter.DNSQueryOptions{})
	require.NoError(t, err)
	require.Equal(t, before+4, transport.queryCount.Load())
}

// TestTheGateFollowsTheCommittedDecision covers the two ways the decision can be absent: a manager
// that presents IPv6, and a manager that does not implement the capability at all.
func TestTheGateFollowsTheCommittedDecision(t *testing.T) {
	presentRouter, presentTransport := entryGateRouter(t, presentmentReaderStub{presentment: netcap.PresentIPv6})
	_, err := presentRouter.Exchange(tunQueryContext(C.TypeTun), newQuestion("example.com.", mDNS.TypeAAAA), adapter.DNSQueryOptions{})
	require.NoError(t, err)
	require.EqualValues(t, 1, presentTransport.queryCount.Load(),
		"a committed 'present' decision must keep the previous behaviour")

	absentRouter, absentTransport := entryGateRouter(t, nil)
	_, err = absentRouter.Exchange(tunQueryContext(C.TypeTun), newQuestion("example.com.", mDNS.TypeAAAA), adapter.DNSQueryOptions{})
	require.NoError(t, err)
	require.EqualValues(t, 1, absentTransport.queryCount.Load(),
		"a manager that cannot report the decision must keep the previous behaviour")
}

// TestTheAsynchronousInterfaceAnswersTheSameWay is the sync/async consistency rule.
//
// The UDP hijack uses ExchangeAsync and the TCP one reaches the same preparation through a
// goroutine, so a gate that only worked on the synchronous entry would suppress half the traffic
// while returning the other half to a transport that cannot carry it.
func TestTheAsynchronousInterfaceAnswersTheSameWay(t *testing.T) {
	router, transport := entryGateRouter(t, presentmentReaderStub{presentment: netcap.SuppressIPv6})
	query := newQuestion("example.com.", mDNS.TypeAAAA)

	type outcome struct {
		response *mDNS.Msg
		err      error
	}
	done := make(chan outcome, 1)
	router.ExchangeAsync(tunQueryContext(C.TypeTun), query, adapter.DNSQueryOptions{}, func(response *mDNS.Msg, err error) {
		done <- outcome{response: response, err: err}
	})

	// The early answer is delivered synchronously on this path - it never enters the goroutine the
	// transports use - so reading the channel here cannot race the callback.
	received := <-done
	require.NoError(t, received.err)
	require.NotNil(t, received.response)
	require.Equal(t, mDNS.RcodeSuccess, received.response.Rcode)
	require.Empty(t, received.response.Answer)
	require.EqualValues(t, 0, transport.queryCount.Load(),
		"the asynchronous path must not fall through to a transport either")
}

// TestTheDialerResolutionPathIsNotGated is the boundary the round is required to keep.
//
// `Lookup` resolves a destination a dialer has chosen - including a proxy's own domain and a target
// a proxy will reach on this machine's behalf - so this machine's missing public IPv6 must not
// remove the AAAA it may legitimately need.
func TestTheDialerResolutionPathIsNotGated(t *testing.T) {
	router, transport := entryGateRouter(t, presentmentReaderStub{presentment: netcap.SuppressIPv6})

	_, err := router.Lookup(context.Background(), "proxy.example.com", adapter.DNSQueryOptions{Transport: transport})
	require.NoError(t, err)
	require.Greater(t, transport.queryCount.Load(), int32(0),
		"Lookup must still reach the transport: it resolves for the dialer, not for an application")
}
