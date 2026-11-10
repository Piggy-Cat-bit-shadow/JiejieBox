package dns

import (
	"context"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"

	"github.com/stretchr/testify/require"
)

// TestDisableCacheExchangeStillReportsItsSource is C3 through the Client entry point: a disabled
// cache must not turn a knowable environment into unknown, and it must not touch the cache either.
func TestDisableCacheExchangeStillReportsItsSource(t *testing.T) {
	generation := &atomic.Uint64{}
	generation.Store(1)
	transport := newCountingSourceTransport(netip.MustParseAddr("192.0.2.1"))
	client := sourceClient(generation)
	domain := "nocache.test."
	ctx := context.Background()

	addresses, stamp, known, err := client.lookupWithSource(ctx, transport, domain, adapter.DNSQueryOptions{LookupStrategy: C.DomainStrategyIPv4Only, DisableCache: true}, nil)
	require.NoError(t, err)
	require.Equal(t, []netip.Addr{netip.MustParseAddr("192.0.2.1")}, addresses)
	require.True(t, known, "a real exchange has an environment even when caching is disabled")
	require.EqualValues(t, 1, stamp)
	require.Equal(t, 1, transport.exchangeCount())

	// A second DisableCache lookup must exchange again: the first one neither read nor wrote a
	// cache entry, so nothing can be served from it.
	_, _, _, err = client.lookupWithSource(ctx, transport, domain, adapter.DNSQueryOptions{LookupStrategy: C.DomainStrategyIPv4Only, DisableCache: true}, nil)
	require.NoError(t, err)
	require.Equal(t, 2, transport.exchangeCount(), "DisableCache must not be served from or fill the cache")

	// A cache-enabled lookup therefore misses too - proof the disabled exchanges wrote nothing -
	// and a second one hits, proof the cache itself still works.
	_, _, _, err = client.lookupWithSource(ctx, transport, domain, adapter.DNSQueryOptions{LookupStrategy: C.DomainStrategyIPv4Only}, nil)
	require.NoError(t, err)
	require.Equal(t, 3, transport.exchangeCount())
	_, stamp, known, err = client.lookupWithSource(ctx, transport, domain, adapter.DNSQueryOptions{LookupStrategy: C.DomainStrategyIPv4Only}, nil)
	require.NoError(t, err)
	require.True(t, known)
	require.Equal(t, 3, transport.exchangeCount(), "the cache-enabled repeat must be a hit")
	require.EqualValues(t, 1, stamp)
}

// TestDisableCacheLateAnswerKeepsTheGenerationItWasAskedOn is the "after issue" direction: the query
// is on the wire when the environment moves, and the late answer keeps the old stamp.
func TestDisableCacheLateAnswerKeepsTheGenerationItWasAskedOn(t *testing.T) {
	generation := &atomic.Uint64{}
	generation.Store(1)
	transport := newCountingSourceTransport(netip.MustParseAddr("192.0.2.1"))
	transport.entered = make(chan struct{})
	transport.release = make(chan struct{})
	client := sourceClient(generation)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	type outcome struct {
		stamp uint64
		known bool
		err   error
	}
	done := make(chan outcome, 1)
	go func() {
		_, stamp, known, err := client.lookupWithSource(ctx, transport, "late-nocache.test.", adapter.DNSQueryOptions{LookupStrategy: C.DomainStrategyIPv4Only, DisableCache: true}, nil)
		done <- outcome{stamp: stamp, known: known, err: err}
	}()
	select {
	case <-transport.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the exchange never began")
	}
	// The question is on the wire; the environment moves before the answer arrives.
	generation.Add(1)
	close(transport.release)
	result := <-done
	require.NoError(t, result.err)
	require.True(t, result.known)
	require.EqualValues(t, 1, result.stamp, "a late answer keeps the generation it was asked on")
}

// TestRealRouterDisableCachePublishesItsSource is C3 through the production Router entry point: a
// strict IPv4 query over an explicit transport with caching disabled still has an environment, and
// the published family result must name it.
func TestRealRouterDisableCachePublishesItsSource(t *testing.T) {
	router, err := NewRouter(context.Background(), log.NewNOPFactory(), option.DNSOptions{RawDNSOptions: option.RawDNSOptions{
		DNSClientOptions: option.DNSClientOptions{DisableCache: true},
	}})
	require.NoError(t, err)
	require.NoError(t, router.Start(adapter.StartStateStart, &adapter.Scope{}))
	router.networkGeneration.Store(1)

	transport := newCountingSourceTransport(netip.MustParseAddr("192.0.2.1"))
	publish := func(t *testing.T, ctx context.Context) []adapter.DNSFamilyResult {
		t.Helper()
		var results []adapter.DNSFamilyResult
		err := router.LookupFamilies(ctx, "router-nocache.test.", adapter.DNSQueryOptions{
			Transport:      transport,
			LookupStrategy: C.DomainStrategyIPv4Only,
			DisableCache:   true,
		}, func(result adapter.DNSFamilyResult) { results = append(results, result) })
		require.NoError(t, err)
		require.Len(t, results, 1)
		return results
	}

	results := publish(t, context.Background())
	require.Equal(t, []netip.Addr{netip.MustParseAddr("192.0.2.1")}, results[0].Addresses)
	require.True(t, results[0].HasSourceGeneration, "the exchange has an environment")
	require.EqualValues(t, 1, results[0].SourceGeneration)
	require.Equal(t, 1, transport.exchangeCount())

	// An advance before the next query is attributed to the query that follows it.
	router.networkGeneration.Store(2)
	results = publish(t, context.Background())
	require.True(t, results[0].HasSourceGeneration)
	require.EqualValues(t, 2, results[0].SourceGeneration)
	require.Equal(t, 2, transport.exchangeCount(), "DisableCache must not reuse the first answer")

	// A late answer keeps the generation it was asked on.
	gateTransport := newCountingSourceTransport(netip.MustParseAddr("192.0.2.1"))
	gateTransport.entered = make(chan struct{})
	gateTransport.release = make(chan struct{})
	done := make(chan []adapter.DNSFamilyResult, 1)
	go func() {
		var late []adapter.DNSFamilyResult
		_ = router.LookupFamilies(context.Background(), "router-late.test.", adapter.DNSQueryOptions{
			Transport:      gateTransport,
			LookupStrategy: C.DomainStrategyIPv4Only,
			DisableCache:   true,
		}, func(result adapter.DNSFamilyResult) { late = append(late, result) })
		done <- late
	}()
	select {
	case <-gateTransport.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the exchange never began")
	}
	router.networkGeneration.Add(1)
	close(gateTransport.release)
	late := <-done
	require.Len(t, late, 1)
	require.True(t, late[0].HasSourceGeneration)
	require.EqualValues(t, 2, late[0].SourceGeneration, "the question was asked on generation 2")
}

// TestPerRequestDisableCacheLeavesTheCacheAlone covers the per-request shape on a cache-enabled
// router: the disabled query must not be served from the cache nor fill it.
func TestPerRequestDisableCacheLeavesTheCacheAlone(t *testing.T) {
	router, err := NewRouter(context.Background(), log.NewNOPFactory(), option.DNSOptions{RawDNSOptions: option.RawDNSOptions{}})
	require.NoError(t, err)
	require.NoError(t, router.Start(adapter.StartStateStart, &adapter.Scope{}))
	router.networkGeneration.Store(1)

	transport := newCountingSourceTransport(netip.MustParseAddr("192.0.2.1"))
	lookup := func(disable bool) adapter.DNSFamilyResult {
		var results []adapter.DNSFamilyResult
		err := router.LookupFamilies(context.Background(), "per-request.test.", adapter.DNSQueryOptions{
			Transport:      transport,
			LookupStrategy: C.DomainStrategyIPv4Only,
			DisableCache:   disable,
		}, func(result adapter.DNSFamilyResult) { results = append(results, result) })
		require.NoError(t, err)
		require.Len(t, results, 1)
		return results[0]
	}

	disabled := lookup(true)
	require.True(t, disabled.HasSourceGeneration)
	require.Equal(t, 1, transport.exchangeCount())

	// The disabled query wrote nothing, so this cache-enabled query must exchange.
	enabled := lookup(false)
	require.True(t, enabled.HasSourceGeneration)
	require.Equal(t, 2, transport.exchangeCount(), "a DisableCache query must not fill the cache")

	// And the cache then works normally.
	_ = lookup(false)
	require.Equal(t, 2, transport.exchangeCount(), "the second cache-enabled query must be a hit")
}
