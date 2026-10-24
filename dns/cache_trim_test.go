package dns

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	mDNS "github.com/miekg/dns"
)

// Tests for the L1 cache layer. The contract is narrow on purpose: only entries the read path can
// no longer serve may be dropped. These tests pin the three exceptions that make that true.

func newCacheTrimClient(optimisticTimeout time.Duration) *Client {
	client := &Client{
		cacheCapacity:     1024,
		optimisticTimeout: optimisticTimeout,
	}
	client.initializeMemoryCache()
	client.initializeNXDomainCache()
	return client
}

func cacheTrimKey(name string) dnsCacheKey {
	return dnsCacheKey{
		Question: mDNS.Question{
			Name:   name,
			Qtype:  mDNS.TypeA,
			Qclass: mDNS.ClassINET,
		},
		transportTag: "test",
	}
}

func TestTrimExpiredCachesKeepsLiveAndRemovesLapsed(t *testing.T) {
	client := newCacheTrimClient(0)
	require.NotNil(t, client.cache)

	live := cacheTrimKey("live.example.")
	lapsed := cacheTrimKey("lapsed.example.")
	client.cache.AddWithLifetime(live, &mDNS.Msg{}, time.Hour)
	client.cache.AddWithLifetime(lapsed, &mDNS.Msg{}, -time.Second)
	require.Equal(t, 2, client.cache.Len())

	require.Equal(t, 1, client.TrimExpiredCaches())
	require.Equal(t, 1, client.cache.Len())
	_, loaded := client.cache.Peek(live)
	require.True(t, loaded, "a live answer must survive the cache trim")
	_, loaded = client.cache.Peek(lapsed)
	require.False(t, loaded)
}

// Optimistic DNS serves a lapsed entry inside its window and refreshes it in the background.
// Dropping it would be correctness-preserving but would trade a free hit for an upstream query,
// which is exactly the trade a memory pass must not make silently.
func TestTrimExpiredCachesPreservesOptimisticStaleEntries(t *testing.T) {
	client := newCacheTrimClient(24 * time.Hour)
	require.NotNil(t, client.cache)

	client.cache.AddWithLifetime(cacheTrimKey("stale.example."), &mDNS.Msg{}, -time.Minute)
	require.Equal(t, 1, client.cache.Len())

	require.Zero(t, client.TrimExpiredCaches())
	require.Equal(t, 1, client.cache.Len(),
		"an optimistically served stale answer must not be purged by a memory pass")
}

// Entries stored under disable_expire carry no lifetime and are served indefinitely. They are not
// "expired" in any sense the runtime can observe, and PurgeExpired must leave them.
func TestTrimExpiredCachesKeepsEntriesWithoutALifetime(t *testing.T) {
	client := newCacheTrimClient(0)
	require.NotNil(t, client.cache)

	client.cache.Add(cacheTrimKey("permanent.example."), &mDNS.Msg{})
	require.Equal(t, 1, client.cache.Len())

	require.Zero(t, client.TrimExpiredCaches())
	require.Equal(t, 1, client.cache.Len())
}

// The negative cache has no optimistic exception: loadNXDomain removes a lapsed verdict rather than
// serving it, so an expired entry there is dead weight by construction.
func TestTrimExpiredCachesRemovesLapsedNegativeVerdicts(t *testing.T) {
	client := newCacheTrimClient(24 * time.Hour)
	require.NotNil(t, client.nxdomainCache)

	live := nxdomainCacheKey{name: "live.example.", qClass: mDNS.ClassINET, transportTag: "test"}
	lapsed := nxdomainCacheKey{name: "lapsed.example.", qClass: mDNS.ClassINET, transportTag: "test"}
	client.nxdomainCache.AddWithLifetime(live, &nxdomainCacheEntry{negativeTTL: time.Hour}, time.Hour)
	client.nxdomainCache.AddWithLifetime(lapsed, &nxdomainCacheEntry{negativeTTL: time.Second}, -time.Second)
	require.Equal(t, 2, client.nxdomainCache.Len())

	require.Equal(t, 1, client.TrimExpiredCaches())
	require.Equal(t, 1, client.nxdomainCache.Len())
	_, loaded := client.nxdomainCache.Peek(live)
	require.True(t, loaded, "a live negative verdict must survive the cache trim")
}

// No cache configured is the ordinary "cache disabled" deployment, not an error.
func TestTrimExpiredCachesWithoutCaches(t *testing.T) {
	client := &Client{}
	require.Zero(t, client.TrimExpiredCaches())
}
