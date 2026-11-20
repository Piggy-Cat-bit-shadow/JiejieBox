package dns

// The complete lookup's cross-epoch refusal (window triple-wise B, T02).
//
// `Lookup` is the COMPLETE entry: A and AAAA are two exchanges, and the caller gets ONE address set
// that it uses as a single claim about the name - routing, rule matching and the candidate list
// published on metadata all read it that way. Each exchange captures the generation when its request
// is issued, so a network change between them used to yield a set whose A half was answered on the
// network that had been left and whose AAAA half on the one that was current. Both halves were
// individually well formed; the set was never true on any network.
//
// lookupComplete now captures the epoch before either family is dispatched and refuses the result if
// it moved. Nothing exercised that refusal, so a refactor could delete it and every suite would stay
// green. This file pins both halves of the contract: the refusal, and the ordinary union when the
// epoch is stable.

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"

	mDNS "github.com/miekg/dns"
	"github.com/stretchr/testify/require"
)

// familyGateTransport answers A immediately and parks AAAA on a gate, so a test can move the network
// epoch in the exact window between the two halves of a complete lookup.
type familyGateTransport struct {
	adapter.DNSTransport
	aAnswered chan struct{}
	aaaaGate  chan struct{}
	aOnce     sync.Once
}

func newFamilyGateTransport() *familyGateTransport {
	return &familyGateTransport{
		aAnswered: make(chan struct{}),
		aaaaGate:  make(chan struct{}),
	}
}

func (t *familyGateTransport) Type() string                                   { return "family-gate" }
func (t *familyGateTransport) Tag() string                                    { return "family-gate" }
func (t *familyGateTransport) Reset()                                         {}
func (t *familyGateTransport) Close() error                                   { return nil }
func (t *familyGateTransport) Dependencies() []string                         { return nil }
func (t *familyGateTransport) Start(adapter.StartStage, *adapter.Scope) error { return nil }

func (t *familyGateTransport) Exchange(ctx context.Context, message *mDNS.Msg) (*mDNS.Msg, error) {
	response := new(mDNS.Msg)
	response.SetReply(message)
	question := message.Question[0]
	switch question.Qtype {
	case mDNS.TypeA:
		response.Answer = append(response.Answer, &mDNS.A{
			Hdr: mDNS.RR_Header{Name: question.Name, Rrtype: mDNS.TypeA, Class: mDNS.ClassINET, Ttl: 60},
			A:   netip.MustParseAddr("192.0.2.1").AsSlice(),
		})
		t.aOnce.Do(func() { close(t.aAnswered) })
		return response, nil
	case mDNS.TypeAAAA:
		select {
		case <-t.aaaaGate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		response.Answer = append(response.Answer, &mDNS.AAAA{
			Hdr:  mDNS.RR_Header{Name: question.Name, Rrtype: mDNS.TypeAAAA, Class: mDNS.ClassINET, Ttl: 60},
			AAAA: netip.MustParseAddr("2001:db8::1").AsSlice(),
		})
		return response, nil
	}
	return response, nil
}

func (t *familyGateTransport) ExchangeAsync(ctx context.Context, message *mDNS.Msg, callback func(*mDNS.Msg, error)) {
	go callback(t.Exchange(ctx, message))
}

type completeResult struct {
	addresses []netip.Addr
	err       error
}

func startCompleteLookup(t *testing.T, client *Client, transport adapter.DNSTransport, options adapter.DNSQueryOptions) <-chan completeResult {
	t.Helper()
	results := make(chan completeResult, 1)
	go func() {
		addresses, _, _, err := client.lookupWithSource(context.Background(), transport, "split.test", options, nil)
		results <- completeResult{addresses: addresses, err: err}
	}()
	return results
}

func completeLookupOptions(t *testing.T) adapter.DNSQueryOptions {
	t.Helper()
	options := adapter.DNSQueryOptions{LookupStrategy: C.DomainStrategyPreferIPv4}
	require.True(t, lookupItemFor(options).complete,
		"the test must go through the complete-lookup entry, not the single-family one")
	return options
}

// TestACompleteLookupSplitAcrossAnEpochChangeIsRefused is the guard.
func TestACompleteLookupSplitAcrossAnEpochChangeIsRefused(t *testing.T) {
	generation := &atomic.Uint64{}
	generation.Store(1)
	transport := newFamilyGateTransport()
	client := sourceClient(generation)
	results := startCompleteLookup(t, client, transport, completeLookupOptions(t))

	// The A half has answered on network 1; the AAAA half is parked before it is sent.
	select {
	case <-transport.aAnswered:
	case <-time.After(5 * time.Second):
		t.Fatal("the A half never answered")
	}

	// The network changes in the window between the halves.
	generation.Add(1)
	close(transport.aaaaGate)

	select {
	case result := <-results:
		require.Error(t, result.err)
		require.ErrorContains(t, result.err, "address families belong to different networks")
		require.Nil(t, result.addresses,
			"a set assembled across an epoch change must not be published as one claim about the name")
		require.False(t, errors.Is(result.err, context.Canceled))
	case <-time.After(5 * time.Second):
		t.Fatal("the complete lookup never returned")
	}
}

// TestACompleteLookupOnOneEpochReturnsBothFamilies is the control: the same harness, no epoch change,
// so the refusal must not fire and the caller gets the union.
func TestACompleteLookupOnOneEpochReturnsBothFamilies(t *testing.T) {
	generation := &atomic.Uint64{}
	generation.Store(1)
	transport := newFamilyGateTransport()
	client := sourceClient(generation)
	results := startCompleteLookup(t, client, transport, completeLookupOptions(t))

	select {
	case <-transport.aAnswered:
	case <-time.After(5 * time.Second):
		t.Fatal("the A half never answered")
	}
	close(transport.aaaaGate)

	select {
	case result := <-results:
		require.NoError(t, result.err)
		require.ElementsMatch(t,
			[]netip.Addr{netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("2001:db8::1")},
			result.addresses)
	case <-time.After(5 * time.Second):
		t.Fatal("the complete lookup never returned")
	}
}
