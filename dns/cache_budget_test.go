package dns

import (
	"context"
	"fmt"
	"math/rand"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	mDNS "github.com/miekg/dns"
)

// Tests for the byte budget that bounds the in-memory DNS caches.
//
// The contract in one sentence: the two caches together never retain more than the
// configured number of bytes, the entry-count limits stay in force, and the ledger equals
// the sum of the entries actually present after every kind of removal - which is the
// property that a missed release path would otherwise only show as a cache that quietly
// shrinks for the rest of the process.

func budgetTestClient(t *testing.T, capacity uint32, bytes int) *Client {
	t.Helper()
	client := &Client{
		cacheCapacity: max(capacity, defaultCacheEntries),
		cacheBytes:    bytes,
	}
	client.initializeMemoryCache()
	client.initializeNXDomainCache()
	return client
}

func budgetTestKey(name string, qtype uint16) dnsCacheKey {
	return dnsCacheKey{
		Question:     mDNS.Question{Name: name, Qtype: qtype, Qclass: mDNS.ClassINET},
		transportTag: "budget",
	}
}

// budgetTestResponse builds a response whose retained size is roughly strings*perString
// bytes, so a test can ask for a small or a large entry without hard-coding a size.
func budgetTestResponse(t *testing.T, stringsCount, perString int) *mDNS.Msg {
	t.Helper()
	message := new(mDNS.Msg)
	message.Response = true
	message.RecursionAvailable = true
	message.Question = []mDNS.Question{{Name: "budget.example.", Qtype: mDNS.TypeTXT, Qclass: mDNS.ClassINET}}
	for i := 0; i < stringsCount; i++ {
		record, err := mDNS.NewRR(fmt.Sprintf(`budget.example. 300 IN TXT %q`, strings.Repeat("x", perString)))
		require.NoError(t, err)
		message.Answer = append(message.Answer, record)
	}
	return message
}

// budgetTestContents is the sum of the charges of the entries present right now, computed
// from the cached values themselves. It must equal the ledger at every quiescent point.
func budgetTestContents(t *testing.T, client *Client) int64 {
	t.Helper()
	var total int64
	if client.cache != nil {
		for _, key := range client.cache.Keys() {
			message, loaded := client.cache.Peek(key)
			require.True(t, loaded)
			total += int64(estimateDNSResponseBytes(message))
		}
	}
	if client.nxdomainCache != nil {
		for _, key := range client.nxdomainCache.Keys() {
			entry, loaded := client.nxdomainCache.Peek(key)
			require.True(t, loaded)
			total += int64(estimateDNSResponseBytes(entry.response))
		}
	}
	return total
}

// TestTheByteBudgetRefusesBeforeTheCopy is the "reserve, then allocate" half: a response
// the budget cannot admit is not retained at all, and refusing it does not disturb what is
// already cached.
func TestTheByteBudgetRefusesBeforeTheCopy(t *testing.T) {
	small := budgetTestResponse(t, 1, 20)
	smallSize := estimateDNSResponseBytes(small)
	client := budgetTestClient(t, 64, smallSize+64)

	first := budgetTestKey("first.example.", mDNS.TypeTXT)
	client.storeCache(first, small, 300)
	require.Equal(t, 1, client.cache.Len())
	require.EqualValues(t, estimateDNSResponseBytes(small), client.cacheBudget.Used())

	// More than the remaining budget: refused, not evicted-in-place.
	big := budgetTestResponse(t, 20, 60)
	require.Greater(t, estimateDNSResponseBytes(big), 64)
	second := budgetTestKey("second.example.", mDNS.TypeTXT)
	client.storeCache(second, big, 300)

	require.Equal(t, 1, client.cache.Len(), "a refused response must not be cached")
	_, loaded := client.cache.Peek(first)
	require.True(t, loaded, "a refusal must not disturb an entry already cached")
	require.EqualValues(t, estimateDNSResponseBytes(small), client.cacheBudget.Used())
	require.NotZero(t, client.cacheBudget.Refused())
	require.Zero(t, client.cacheBudget.ExcessReleases())
}

// TestTheByteBudgetKeepsBothLimits: the entry count is still an admission limit, and the
// byte budget is a second one. Neither replaces the other.
func TestTheByteBudgetKeepsBothLimits(t *testing.T) {
	// Count binds: 2 slots, a budget that would hold far more. The client's capacity floor
	// is defaultCacheEntries, so build the small-count case directly.
	client := &Client{cacheCapacity: 2, cacheBytes: 1 << 20}
	client.initializeMemoryCache()
	for index := 0; index < 5; index++ {
		client.storeCache(budgetTestKey(fmt.Sprintf("count-%d.example.", index), mDNS.TypeTXT), budgetTestResponse(t, 1, 10), 300)
	}
	require.Equal(t, 2, client.cache.Len(), "the entry-count limit must still evict")
	require.LessOrEqual(t, client.cacheBudget.Used(), int64(1<<20))
	require.EqualValues(t, budgetTestContents(t, client), client.cacheBudget.Used())
	require.Zero(t, client.cacheBudget.ExcessReleases())

	// Bytes bind: room for one entry, a count limit that would hold many.
	response := budgetTestResponse(t, 4, 100)
	oneEntry := estimateDNSResponseBytes(response)
	client = &Client{cacheCapacity: 256, cacheBytes: oneEntry + oneEntry/2}
	client.initializeMemoryCache()
	for index := 0; index < 4; index++ {
		client.storeCache(budgetTestKey(fmt.Sprintf("bytes-%d.example.", index), mDNS.TypeTXT), budgetTestResponse(t, 4, 100), 300)
	}
	require.Equal(t, 1, client.cache.Len(), "the byte budget must stop the second entry")
	require.LessOrEqual(t, client.cacheBudget.Used(), client.cacheBudget.Limit())
}

// TestTheByteBudgetIsExactAcrossEveryRemovalPath walks the release paths one at a time and
// asserts the ledger against the cache contents after each: replacement, expiry, explicit
// removal and purge. A missed path shows up as a positive difference and would shrink the
// usable budget permanently.
func TestTheByteBudgetIsExactAcrossEveryRemovalPath(t *testing.T) {
	client := budgetTestClient(t, 256, 1<<20)

	first := budgetTestKey("replace.example.", mDNS.TypeTXT)
	second := budgetTestKey("expire.example.", mDNS.TypeTXT)
	third := budgetTestKey("keep.example.", mDNS.TypeTXT)

	client.storeCache(first, budgetTestResponse(t, 2, 40), 300)
	client.storeCache(second, budgetTestResponse(t, 3, 40), 300)
	client.storeCache(third, budgetTestResponse(t, 1, 40), 300)
	require.EqualValues(t, budgetTestContents(t, client), client.cacheBudget.Used())

	// Replacement of an existing key: the old charge must be returned, not stranded by
	// freelru's silent same-key overwrite.
	client.storeCache(first, budgetTestResponse(t, 6, 80), 300)
	require.EqualValues(t, budgetTestContents(t, client), client.cacheBudget.Used())
	require.Equal(t, 3, client.cache.Len(), "a replacement must not add an entry")

	// Expiry, via the same trim entry point a memory pass uses. The entry is stored
	// through the production path with a one-second lifetime, so its charge is real and
	// the assertion below proves the expiry released exactly it.
	client.storeCache(second, budgetTestResponse(t, 3, 40), 1)
	time.Sleep(1100 * time.Millisecond)
	require.NotZero(t, client.TrimExpiredCaches())
	require.EqualValues(t, budgetTestContents(t, client), client.cacheBudget.Used())

	// Explicit removal, the path the read side takes for a lapsed entry.
	client.cache.Remove(third)
	require.EqualValues(t, budgetTestContents(t, client), client.cacheBudget.Used())

	// Purge through the public clear, including the negative cache's entries.
	client.storeCache(third, budgetTestResponse(t, 1, 40), 300)
	client.storeNXDomain(budgetTestKey("gone.example.", mDNS.TypeA), budgetTestNXDomain(t), 300)
	require.NotZero(t, client.cacheBudget.Used())
	client.ClearCache()
	require.Zero(t, client.cacheBudget.Used())
	require.Zero(t, budgetTestContents(t, client))
	require.Zero(t, client.cacheBudget.ExcessReleases())

	// The budget is still usable after the clear: this is the assertion that a drifted
	// ledger would fail once it had accumulated a few replacements.
	client.storeCache(first, budgetTestResponse(t, 2, 40), 300)
	require.Equal(t, 1, client.cache.Len())
	require.NotZero(t, client.cacheBudget.Used())
}

// TestTheByteBudgetIsSharedWithTheNegativeCache: the two caches draw on one ledger, so a
// full exact cache does not leave a second full budget for name-level verdicts.
func TestTheByteBudgetIsSharedWithTheNegativeCache(t *testing.T) {
	verdict := budgetTestNXDomain(t)
	verdictSize := estimateDNSResponseBytes(verdict)
	client := budgetTestClient(t, 1024, verdictSize+256)

	// One exact-cache entry leaves less than a verdict's worth of room.
	client.storeCache(budgetTestKey("exact.example.", mDNS.TypeTXT), budgetTestResponse(t, 1, 20), 300)
	remaining := client.cacheBudget.Limit() - client.cacheBudget.Used()
	require.Less(t, remaining, int64(verdictSize))

	client.storeNXDomain(budgetTestKey("blocked.example.", mDNS.TypeA), verdict, 300)
	require.Zero(t, client.nxdomainCache.Len(), "the negative cache must obey the shared budget")

	// After the exact cache releases its charge, the same verdict fits.
	client.cache.Purge()
	client.storeNXDomain(budgetTestKey("blocked.example.", mDNS.TypeA), verdict, 300)
	require.Equal(t, 1, client.nxdomainCache.Len())
	require.EqualValues(t, budgetTestContents(t, client), client.cacheBudget.Used())

	// And a negative-cache eviction returns its charge.
	client.nxdomainCache.Remove(nxdomainKeyFrom(budgetTestKey("blocked.example.", mDNS.TypeA)))
	require.Zero(t, client.cacheBudget.Used())
	require.Zero(t, client.cacheBudget.ExcessReleases())
}

// TestTheByteBudgetLedgerMatchesTheCacheContents runs a deterministic random sequence of
// stores, replacements, removals and expiries, and checks the ledger against the contents
// after every step. This is the invariant a missed or doubled release cannot survive.
func TestTheByteBudgetLedgerMatchesTheCacheContents(t *testing.T) {
	client := budgetTestClient(t, 64, 64<<10)
	random := rand.New(rand.NewSource(20261128))
	for step := 0; step < 400; step++ {
		key := budgetTestKey(fmt.Sprintf("random-%d.example.", random.Intn(12)), []uint16{mDNS.TypeTXT, mDNS.TypeA}[random.Intn(2)])
		switch random.Intn(10) {
		case 0, 1, 2, 3, 4, 5:
			client.storeCache(key, budgetTestResponse(t, 1+random.Intn(6), 20+random.Intn(120)), 300)
		case 6:
			client.cache.Remove(key)
		case 7:
			// A one-second entry through the production path; the trim below only
			// removes it once it has actually lapsed, so the step stays deterministic
			// w.r.t. the ledger (a removal here is exercised in the expiry test).
			client.storeCache(key, budgetTestResponse(t, 1, 20), 1)
		case 8:
			client.storeNXDomain(key, budgetTestNXDomain(t), 300)
		case 9:
			if random.Intn(8) == 0 {
				client.ClearCache()
			}
		}
		require.EqualValues(t, budgetTestContents(t, client), client.cacheBudget.Used(), "step %d", step)
		require.LessOrEqual(t, client.cacheBudget.Used(), client.cacheBudget.Limit(), "step %d", step)
		require.Zero(t, client.cacheBudget.ExcessReleases(), "step %d", step)
	}
}

// TestTheByteBudgetUnderConcurrentStores is the same identity under contention. Run with
// -race: the ledger is atomic and the store protocol is serialised, so neither the total
// nor the excess-release counter may drift.
func TestTheByteBudgetUnderConcurrentStores(t *testing.T) {
	client := budgetTestClient(t, 128, 32<<10)
	var wait sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			random := rand.New(rand.NewSource(int64(worker) + 1))
			for iteration := 0; iteration < 200; iteration++ {
				key := budgetTestKey(fmt.Sprintf("worker-%d-%d.example.", worker, random.Intn(16)), mDNS.TypeTXT)
				client.storeCache(key, budgetTestResponse(t, 1+random.Intn(4), 20+random.Intn(100)), 300)
				if iteration%7 == 0 {
					client.cache.Remove(key)
				}
			}
		}(worker)
	}
	wait.Wait()
	require.EqualValues(t, budgetTestContents(t, client), client.cacheBudget.Used())
	require.LessOrEqual(t, client.cacheBudget.Used(), client.cacheBudget.Limit())
	client.ClearCache()
	require.Zero(t, client.cacheBudget.Used())
	require.Zero(t, client.cacheBudget.ExcessReleases())
}

// TestTheByteBudgetIsExactWhenAClearRacesTheStores closes the gap the test above leaves: it only
// clears AFTER the workers stop. ClearCache purges under the store protocol's mutex and then
// forces the ledger to zero, so a reservation taken outside that mutex can land between the purge
// and the reset and be swallowed. Returning that charge when the entry is evicted is afterwards
// counted as an excess release - the observable this test asserts on.
//
// An independent reviewer reproduced the swallow with a separate harness (ledger 0 B against 793 B
// actually cached, excess 1), and then found a SECOND instance of it: the negative cache has its
// own reserve site, so closing one store protocol closed the window for the exact cache only. The
// workers below therefore drive both caches. Run with -race.
func TestTheByteBudgetIsExactWhenAClearRacesTheStores(t *testing.T) {
	client := budgetTestClient(t, 128, 32<<10)
	var wait sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			random := rand.New(rand.NewSource(int64(worker) + 101))
			for iteration := 0; iteration < 300; iteration++ {
				key := budgetTestKey(fmt.Sprintf("race-%d-%d.example.", worker, random.Intn(16)), mDNS.TypeTXT)
				if worker%2 == 0 {
					client.storeCache(key, budgetTestResponse(t, 1+random.Intn(4), 20+random.Intn(100)), 300)
					continue
				}
				client.storeNXDomain(key, budgetTestNXDomain(t), 300)
			}
		}(worker)
	}
	wait.Add(1)
	go func() {
		defer wait.Done()
		for round := 0; round < 60; round++ {
			client.ClearCache()
			runtime.Gosched()
		}
	}()
	wait.Wait()

	client.ClearCache()
	require.Zero(t, client.cacheBudget.Used(), "the ledger holds charges after a full purge")
	require.Zero(t, client.cacheBudget.ExcessReleases(),
		"a reservation was swallowed by a purge and released afterwards")
	require.EqualValues(t, budgetTestContents(t, client), client.cacheBudget.Used())
}

// TestTheByteBudgetDefaultScalesWithCapacity pins the derivation the product uses, and the
// fact that the entry-count limit is unchanged by it.
func TestTheByteBudgetDefaultScalesWithCapacity(t *testing.T) {
	require.Equal(t, 2<<20, cacheByteLimitForCapacity(1024))
	require.Equal(t, minimumCacheBytes, cacheByteLimitForCapacity(1))
	require.Equal(t, maximumCacheBytes, cacheByteLimitForCapacity(1<<20))

	client := NewClient(ClientOptions{Context: context.Background()})
	require.NotNil(t, client.cache)
	require.EqualValues(t, 2<<20, client.cacheBudget.Limit())
	require.Equal(t, cacheBytesPerEntry*defaultCacheEntries, 2<<20)
	require.Equal(t, uint32(defaultCacheEntries), client.cacheCapacity)

	configured := NewClient(ClientOptions{Context: context.Background(), CacheCapacity: 4096, CacheBytes: 3 << 20})
	require.EqualValues(t, 3<<20, configured.cacheBudget.Limit())
	require.Equal(t, uint32(4096), configured.cacheCapacity)
}

// TestTheEstimateCoversEveryShapeItCharges keeps the estimator honest at the level a test
// can be deterministic about: it never undercuts the wire length, it grows with the record
// population, and the shapes the cache actually sees land in the documented band.
func TestTheEstimateCoversEveryShapeItCharges(t *testing.T) {
	require.Zero(t, estimateDNSResponseBytes(nil))
	empty := new(mDNS.Msg)
	require.Zero(t, estimateDNSResponseBytes(empty))

	small := budgetTestResponse(t, 1, 20)
	large := budgetTestResponse(t, 8, 60)
	smallSize := estimateDNSResponseBytes(small)
	largeSize := estimateDNSResponseBytes(large)
	require.Greater(t, smallSize, small.Len())
	require.Greater(t, largeSize, large.Len())
	require.Greater(t, largeSize, smallSize)

	// The measured retained size of these two shapes is 370 B and 1674 B (see
	// cache_budget.go); the estimate must stay within the documented band rather than
	// drifting into a number that would either never bind or bind on ordinary traffic.
	require.GreaterOrEqual(t, smallSize, 300)
	require.LessOrEqual(t, smallSize, 450)
}

func budgetTestNXDomain(t *testing.T) *mDNS.Msg {
	t.Helper()
	message := new(mDNS.Msg)
	message.Response = true
	message.Rcode = mDNS.RcodeNameError
	message.Question = []mDNS.Question{{Name: "blocked.example.", Qtype: mDNS.TypeA, Qclass: mDNS.ClassINET}}
	soa, err := mDNS.NewRR("example. 300 IN SOA ns.example. hostmaster.example. 1 7200 3600 1209600 300")
	require.NoError(t, err)
	message.Ns = append(message.Ns, soa)
	return message
}
