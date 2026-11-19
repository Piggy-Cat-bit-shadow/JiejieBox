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

// TestAPoolOfEightAddressesAllocatesDistinctAddresses is the other side of that boundary: /29 has six
// reachable addresses and the allocator hands out distinct ones.
func TestAPoolOfEightAddressesAllocatesDistinctAddresses(t *testing.T) {
	ledger := adapter.NewFakeIPIssuanceLedger()
	addressRange := netip.MustParsePrefix("198.18.0.0/29")
	store := newBoundaryStore(t, ledger, addressRange)
	t.Cleanup(func() { _ = store.Close() })

	seen := make(map[netip.Addr]string)
	for _, domain := range []string{"a.example", "b.example", "c.example", "d.example"} {
		address, err := store.Create(domain, false)
		require.NoError(t, err)
		require.True(t, store.issuableAddress(address, false))
		if previous, collided := seen[address]; collided {
			t.Fatalf("%s and %s share %v: a pool with more than one address must not collide", previous, domain, address)
		}
		seen[address] = domain
	}
}

// TestAReloadOfAnExhaustedSmallPoolRecordsWhatItDoes pins the second boundary: resumeAfterLedger can
// only resume past an interval it can see inside the range, so an exhausted two-address pool has
// nowhere left to go. The test records whether the next generation re-issues (the R-B10 wrap) or
// refuses, rather than claiming the reload guarantee holds for every pool size.
func TestAReloadOfAnExhaustedSmallPoolRecordsWhatItDoes(t *testing.T) {
	ledger := adapter.NewFakeIPIssuanceLedger()
	addressRange := netip.MustParsePrefix("198.18.0.0/30")
	first := newBoundaryStore(t, ledger, addressRange)
	firstAddress, err := first.Create("a.example", false)
	require.NoError(t, err)
	secondAddress, err := first.Create("b.example", false)
	require.NoError(t, err)
	require.NoError(t, first.Close())

	second := newBoundaryStore(t, ledger, addressRange)
	t.Cleanup(func() { _ = second.Close() })
	reissued, reissueErr := second.Create("c.example", false)
	t.Logf("reload of an exhausted /30: held=%v,%v next=%v err=%v",
		firstAddress, secondAddress, reissued, reissueErr)

	if reissueErr == nil {
		require.True(t, addressRange.Contains(reissued))
		// If it re-issued one of the two addresses the previous generation handed out, say so in the
		// failure message: that is the documented boundary, not a surprise, and a future change that
		// makes it impossible would be an improvement worth noticing.
		if reissued == firstAddress || reissued == secondAddress {
			t.Logf("the reload re-issued %v, which the previous generation had already handed out; "+
				"this is the R-B10 boundary (a two-address pool has nowhere to resume past)", reissued)
		}
	}
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
