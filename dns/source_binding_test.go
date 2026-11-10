package dns

import (
	"context"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"

	mDNS "github.com/miekg/dns"
	"github.com/stretchr/testify/require"
)

// C2 and C3: when a cached answer is selected, and which environment a real exchange belongs to.
//
// # C2
//
// `lookupToExchangeWithSource` selected a cache entry and then read the CURRENT generation to stamp
// it. A response checker that advanced the environment between selection and return therefore
// relabelled an old answer with the new generation: the dialer saw it as current and could prefer it
// over a candidate that genuinely belonged to the live environment. The stamp is now fixed
// immediately after the key and before the entry is selected, so the checker cannot move it.
//
// # C3
//
// `beginExchange` captured the generation only inside its cache branch, so a DisableCache exchange -
// which is a perfectly ordinary query with a knowable environment - published `unknown`. That made
// the source of an answer depend on a storage policy. The source is now captured for every exchange;
// the generation GUARD (which protects storage) stays exactly where it was.

// countingSourceTransport answers A records from a queue and counts exchanges, so a test can tell a
// cache hit (no exchange) from a miss without inspecting the client's cache.
type countingSourceTransport struct {
	adapter.DNSTransport
	access     sync.Mutex
	answers    []netip.Addr
	exchanges  int
	entered    chan struct{}
	release    chan struct{}
	enteredOne sync.Once
}

func newCountingSourceTransport(answers ...netip.Addr) *countingSourceTransport {
	return &countingSourceTransport{answers: answers}
}

func (t *countingSourceTransport) Type() string                                   { return "counting-source" }
func (t *countingSourceTransport) Tag() string                                    { return "counting-source" }
func (t *countingSourceTransport) Reset()                                         {}
func (t *countingSourceTransport) Close() error                                   { return nil }
func (t *countingSourceTransport) Dependencies() []string                         { return nil }
func (t *countingSourceTransport) Start(adapter.StartStage, *adapter.Scope) error { return nil }

func (t *countingSourceTransport) Exchange(ctx context.Context, message *mDNS.Msg) (*mDNS.Msg, error) {
	t.access.Lock()
	index := t.exchanges
	t.exchanges++
	answer := netip.MustParseAddr("192.0.2.1")
	if index < len(t.answers) {
		answer = t.answers[index]
	}
	t.access.Unlock()
	if t.entered != nil {
		t.enteredOne.Do(func() { close(t.entered) })
	}
	if t.release != nil {
		select {
		case <-t.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	response := new(mDNS.Msg)
	response.SetReply(message)
	switch message.Question[0].Qtype {
	case mDNS.TypeAAAA:
		response.Answer = append(response.Answer, &mDNS.AAAA{
			Hdr:  mDNS.RR_Header{Name: message.Question[0].Name, Rrtype: mDNS.TypeAAAA, Class: mDNS.ClassINET, Ttl: 60},
			AAAA: netip.MustParseAddr("2001:db8::1").AsSlice(),
		})
	case mDNS.TypeA:
		response.Answer = append(response.Answer, &mDNS.A{
			Hdr: mDNS.RR_Header{Name: message.Question[0].Name, Rrtype: mDNS.TypeA, Class: mDNS.ClassINET, Ttl: 60},
			A:   answer.AsSlice(),
		})
	}
	return response, nil
}

func (t *countingSourceTransport) ExchangeAsync(ctx context.Context, message *mDNS.Msg, callback func(*mDNS.Msg, error)) {
	go callback(t.Exchange(ctx, message))
}

func (t *countingSourceTransport) exchangeCount() int {
	t.access.Lock()
	defer t.access.Unlock()
	return t.exchanges
}

func sourceClient(generation *atomic.Uint64) *Client {
	client := NewClient(ClientOptions{
		Context:           context.Background(),
		Logger:            log.NewNOPFactory().NewLogger("source-binding"),
		NetworkGeneration: func() uint64 { return generation.Load() },
	})
	client.Start()
	return client
}

// TestDirectCacheHitKeepsTheGenerationItWasSelectedUnder is C2.
func TestDirectCacheHitKeepsTheGenerationItWasSelectedUnder(t *testing.T) {
	generation := &atomic.Uint64{}
	generation.Store(1)
	transport := newCountingSourceTransport(netip.MustParseAddr("192.0.2.1"))
	client := sourceClient(generation)
	domain := "hot.test."
	ctx := context.Background()

	// Warm at generation 1.
	addresses, stamp, known, err := client.lookupWithSource(ctx, transport, domain, adapter.DNSQueryOptions{LookupStrategy: C.DomainStrategyIPv4Only}, nil)
	require.NoError(t, err)
	require.Equal(t, []netip.Addr{netip.MustParseAddr("192.0.2.1")}, addresses)
	require.True(t, known)
	require.EqualValues(t, 1, stamp)

	// The entry is selected under generation 1, and the checker advances the environment after the
	// selection but before the caller receives the answer. The old code read "now" here and reported
	// 2: the old answer claimed to belong to the new environment.
	checkerCalled := false
	addresses, stamp, known, err = client.lookupWithSource(ctx, transport, domain, adapter.DNSQueryOptions{LookupStrategy: C.DomainStrategyIPv4Only}, func(*mDNS.Msg) bool {
		checkerCalled = true
		generation.Add(1)
		return true
	})
	require.NoError(t, err)
	require.True(t, checkerCalled)
	require.True(t, known)
	require.EqualValues(t, 1, stamp,
		"an answer selected under generation 1 must not be relabelled by a later advance")
	require.Equal(t, []netip.Addr{netip.MustParseAddr("192.0.2.1")}, addresses)
	require.Equal(t, 1, transport.exchangeCount(), "the second lookup must be a cache hit")

	// An advance BEFORE the next lookup's key is built is a different fact: the entry is selected
	// under generation 2, and the stamp says so.
	addresses, stamp, known, err = client.lookupWithSource(ctx, transport, domain, adapter.DNSQueryOptions{LookupStrategy: C.DomainStrategyIPv4Only}, nil)
	require.NoError(t, err)
	require.True(t, known)
	require.EqualValues(t, 2, stamp, "the key was built under generation 2")
	require.Equal(t, []netip.Addr{netip.MustParseAddr("192.0.2.1")}, addresses)
	require.Equal(t, 1, transport.exchangeCount())
}
