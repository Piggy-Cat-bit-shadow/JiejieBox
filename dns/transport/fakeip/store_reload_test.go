package fakeip

import (
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/service"
	"github.com/stretchr/testify/require"

	"net/netip"
)

// TestAReloadWithoutACacheFileDoesNotReissueAddresses pins what a reload must not do.
//
// With no `experimental.cache_file` the storage is MemoryStorage, whose metadata is nil, so Start has
// no cursor to restore. Before this was fixed it began again at the first address of the range, so the
// first query after a reload was handed 198.18.0.2 - an address a client may still be holding for the
// domain the PREVIOUS generation resolved. route/route.go looks that address up and finds the new
// domain, so the client's connection goes to the wrong destination instead of being refused.
//
// The ledger is what makes this fixable in-process: it is owned by whoever owns the sequence of Boxes,
// so it survives the reload even when nothing on disk does.
func TestAReloadWithoutACacheFileDoesNotReissueAddresses(t *testing.T) {
	ledger := adapter.NewFakeIPIssuanceLedger()
	ctx := service.ContextWithPtr(t.Context(), ledger)
	addressRange := netip.MustParsePrefix("198.18.0.0/15")
	logger := log.NewNOPFactory().NewLogger("fakeip")

	first := NewStore(ctx, logger, addressRange, netip.Prefix{})
	require.NoError(t, first.Start())
	held, err := first.Create("held.example", false)
	require.NoError(t, err)
	require.True(t, addressRange.Contains(held))
	require.NoError(t, first.Close())

	// The reload: a new store, the same range, no cache file anywhere.
	second := NewStore(ctx, logger, addressRange, netip.Prefix{})
	require.NoError(t, second.Start())
	t.Cleanup(func() { _ = second.Close() })

	fresh, err := second.Create("fresh.example", false)
	require.NoError(t, err)
	require.True(t, addressRange.Contains(fresh))
	require.NotEqual(t, held, fresh,
		"the reload re-issued %v, which a client may still be holding for the previous domain: "+
			"the destination would be silently rewritten to the new one", held)
	require.Greater(t, fresh.Compare(held), 0,
		"the new generation must start issuing PAST the previous generation's high-water mark")

	// And the address the previous generation issued is still attributable through the ledger, which is
	// what the router refuses on.
	require.True(t, ledger.Issued(held))
}

// TestTwoSequentialCreatesForOneDomainShareOneAddress pins the reuse check: two SEQUENTIAL callers
// asking for the same name get one address. It is not a concurrency test - the audit pointed out that
// the earlier name said "concurrent" while the body called Create twice in a row - so the concurrent
// case now lives in store_boundaries_test.go's TestConcurrentCreatesForOneDomainShareOneAddress, which
// releases sixteen callers through a barrier and runs under -race.
func TestTwoSequentialCreatesForOneDomainShareOneAddress(t *testing.T) {
	ledger := adapter.NewFakeIPIssuanceLedger()
	ctx := service.ContextWithPtr(t.Context(), ledger)
	addressRange := netip.MustParsePrefix("198.18.0.0/15")
	store := NewStore(ctx, log.NewNOPFactory().NewLogger("fakeip"), addressRange, netip.Prefix{})
	require.NoError(t, store.Start())
	t.Cleanup(func() { _ = store.Close() })

	first, err := store.Create("same.example", false)
	require.NoError(t, err)
	second, err := store.Create("same.example", false)
	require.NoError(t, err)
	require.Equal(t, first, second)

	domain, loaded := store.Lookup(first)
	require.True(t, loaded)
	require.Equal(t, "same.example", domain)
}
