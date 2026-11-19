package fakeip

// The boundaries of the reload fix, added after the audit: the fix resumes the cursor past the previous
// generation's high-water mark for the CONFIGURED range, and it does not change the in-generation
// wrap-around, does not guarantee anything for a range that shrinks or moves, and cannot promise that an
// exhausted small pool never re-issues. These tests pin what actually happens instead of leaving the
// guarantee unqualified.

import (
	"net/netip"
	"sync"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/service"
	"github.com/stretchr/testify/require"
)

func newBoundaryStore(t *testing.T, ledger *adapter.FakeIPIssuanceLedger, addressRange netip.Prefix) *Store {
	t.Helper()
	ctx := service.ContextWithPtr(t.Context(), ledger)
	store := NewStore(ctx, log.NewNOPFactory().NewLogger("fakeip"), addressRange, netip.Prefix{})
	require.NoError(t, store.Start())
	return store
}

// TestConcurrentCreatesForOneDomainShareOneAddress is the genuinely concurrent half of the
// double-checked allocation: many callers released by a barrier at once, one address.
//
// The earlier test with a similar name called Create twice in sequence, which never exercises the
// window between the unlocked load and the locked re-check; the audit pointed that out, so this one
// uses a barrier and runs under -race.
func TestConcurrentCreatesForOneDomainShareOneAddress(t *testing.T) {
	ledger := adapter.NewFakeIPIssuanceLedger()
	store := newBoundaryStore(t, ledger, netip.MustParsePrefix("198.18.0.0/15"))
	t.Cleanup(func() { _ = store.Close() })

	const callers = 16
	start := make(chan struct{})
	addresses := make([]netip.Addr, callers)
	errs := make([]error, callers)
	var waitGroup sync.WaitGroup
	for index := 0; index < callers; index++ {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			<-start
			addresses[index], errs[index] = store.Create("same.example", false)
		}(index)
	}
	close(start)
	waitGroup.Wait()

	first := addresses[0]
	require.True(t, first.IsValid(), "the first caller must receive an address")
	for index, address := range addresses {
		require.NoError(t, errs[index], "caller %d failed", index)
		require.Equal(t, first, address,
			"caller %d received %v while the others received %v: one name must map to one address",
			index, address, first)
	}
	domain, loaded := store.Lookup(first)
	require.True(t, loaded)
	require.Equal(t, "same.example", domain, "the one address must map back to the one name")
}

// TestADegeneratePoolCollidesAndTheNewestDomainWins records the /30 boundary, which the reload fix does
// not change and which this round deliberately does not "fix" either.
//
// A /30 has four addresses: .0 (network) and .3 (last) are not issuable, and the allocator starts at
// +2 by convention, so exactly one address (.2) is reachable. Every domain then receives it, and
// `Lookup` answers with the NEWEST domain - so a client holding .2 for a.example is routed to whatever
// resolved last. That is the same class of wrong-destination failure the reload fix closed, and it is
// recorded here rather than silently left: the alternatives are rejecting a range that small at Start
// (a configuration decision a release should not take unilaterally) or special-casing the allocator's
// start offset for tiny ranges (a wire-compatibility decision). /29 and larger allocate distinct
// addresses, which the next test pins, so this only affects a pool that cannot serve more than one name.
func TestADegeneratePoolCollidesAndTheNewestDomainWins(t *testing.T) {
	ledger := adapter.NewFakeIPIssuanceLedger()
	addressRange := netip.MustParsePrefix("198.18.0.0/30")
	store := newBoundaryStore(t, ledger, addressRange)
	t.Cleanup(func() { _ = store.Close() })

	first, err := store.Create("a.example", false)
	require.NoError(t, err)
	second, err := store.Create("b.example", false)
	require.NoError(t, err)
	third, err := store.Create("c.example", false)
	require.NoError(t, err)
	require.Equal(t, netip.MustParseAddr("198.18.0.2"), first)
	require.Equal(t, first, second, "a /30 has one reachable address, so the second name collides")
	require.Equal(t, first, third)

	domain, loaded := store.Lookup(first)
	require.True(t, loaded)
	require.Equal(t, "c.example", domain,
		"the newest mapping wins for a colliding address: a client holding it for a.example would be "+
			"routed to c.example's destination")
}

// TestAPoolOfEightAddressesAllocatesDistinctAddresses pins the capacity of a /29 and its exhaustion
// boundary, which is the extent of the claim the audit asked for: four allocations proving "distinct"
// said nothing about capacity or about what happens after it.
//
// The usable capacity is `2^(32-prefix) - 3`, not `- 2`: the range's network address and its last
// address are never issuable, and the allocator starts at +2, so the first host address is skipped as
// well. A /29 therefore serves FIVE names (198.18.0.2 - .6); the sixth wraps to the start of the pool,
// which is the recorded R-B10 boundary - observed here, not assumed.
func TestAPoolOfEightAddressesAllocatesDistinctAddresses(t *testing.T) {
	ledger := adapter.NewFakeIPIssuanceLedger()
	addressRange := netip.MustParsePrefix("198.18.0.0/29")
	store := newBoundaryStore(t, ledger, addressRange)
	t.Cleanup(func() { _ = store.Close() })

	const capacity = 5
	seen := make(map[netip.Addr]string)
	for index := 0; index < capacity; index++ {
		domain := "name-" + string(rune('a'+index)) + ".example"
		address, err := store.Create(domain, false)
		require.NoError(t, err, "the pool has five usable addresses, so allocation %d must succeed", index)
		require.True(t, store.issuableAddress(address, false))
		if previous, collided := seen[address]; collided {
			t.Fatalf("%s and %s share %v before the pool is exhausted", previous, domain, address)
		}
		seen[address] = domain
	}
	require.Len(t, seen, capacity, "a /29 must hand out five distinct addresses")
	require.Contains(t, seen, netip.MustParseAddr("198.18.0.6"),
		"the last usable address must be reached before exhaustion")

	// The sixth allocation has nowhere to go: the cursor wraps to the pool's start, so the new name
	// receives an address another name already holds.
	exhausted, err := store.Create("overflow.example", false)
	require.NoError(t, err, "the current implementation wraps rather than refusing; a refusal would "+
		"also be safe and this test would need to change with it")
	require.Contains(t, seen, exhausted,
		"the sixth allocation must wrap to an address already in the pool, got %v", exhausted)
	domain, loaded := store.Lookup(exhausted)
	require.True(t, loaded)
	require.Equal(t, "overflow.example", domain,
		"the newest name wins for a wrapped address, which is why a pool this small cannot serve more "+
			"names than it has addresses")
}

// TestAReloadOfAnExhaustedSmallPoolRefusesToStart is the exhausted-pool contract, tightened after the
// audit: a /30 whose only reachable address the previous generation already issued has nowhere to
// resume, and the wrap would re-enter that address. Starting is therefore refused, with a message that
// names the overlap - it is not "records what it does" any more.
func TestAReloadOfAnExhaustedSmallPoolRefusesToStart(t *testing.T) {
	ledger := adapter.NewFakeIPIssuanceLedger()
	addressRange := netip.MustParsePrefix("198.18.0.0/30")
	first := newBoundaryStore(t, ledger, addressRange)
	firstAddress, err := first.Create("a.example", false)
	require.NoError(t, err)
	require.Equal(t, netip.MustParseAddr("198.18.0.2"), firstAddress)
	require.NoError(t, first.Close())

	second := NewStore(service.ContextWithPtr(t.Context(), ledger),
		log.NewNOPFactory().NewLogger("fakeip"), addressRange, netip.Prefix{})
	startErr := second.Start()
	require.Error(t, startErr,
		"an exhausted two-address pool must refuse to start: resuming is impossible and the wrap "+
			"would re-bind %v to a new domain", firstAddress)
	require.Contains(t, startErr.Error(), "has no free address above them")
	_ = second.Close()
}

// TestARangeThatShrinksDoesNotInheritTheOldCursor pins the third boundary: the resume only considers
// issued intervals that lie inside the NEW range, so a generation whose range moved elsewhere starts at
// its own beginning - which is correct, because the old addresses are not addressable in the new range
// at all.
func TestARangeThatShrinksDoesNotInheritTheOldCursor(t *testing.T) {
	ledger := adapter.NewFakeIPIssuanceLedger()
	wide := newBoundaryStore(t, ledger, netip.MustParsePrefix("198.18.0.0/15"))
	held, err := wide.Create("held.example", false)
	require.NoError(t, err)
	require.NoError(t, wide.Close())

	narrowRange := netip.MustParsePrefix("198.19.0.0/24")
	narrow := newBoundaryStore(t, ledger, narrowRange)
	t.Cleanup(func() { _ = narrow.Close() })
	next, err := narrow.Create("next.example", false)
	require.NoError(t, err)
	require.True(t, narrowRange.Contains(next))
	require.NotEqual(t, held, next)
	require.False(t, narrowRange.Contains(held),
		"the fixture's point: the previous address is outside the new range, so nothing can be inherited")
}
