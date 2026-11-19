package fakeip

import (
	"testing"

	"github.com/sagernet/sing-box/log"
	"github.com/stretchr/testify/require"

	"net/netip"
)

// TestADegenerateRangeIssuesNothingOutsideItself pins the allocator's bound.
//
// # Why this is a leak, not a cosmetic error
//
// `route/route.go` recognises a placeholder with `Store().Contains(address)`: that is what turns the
// address back into a domain and what refuses one this installation minted but can no longer map.
// An address outside the configured range is therefore not a bad placeholder - it is an ORDINARY
// LITERAL destination, and it is dialled for real.
//
// # How it was reachable
//
// `nextAddress` wraps to `range.Addr().Next().Next()`, which assumes at least two addresses above the
// network address. On a /31 or /32 (a /127 or /128 for v6) that wrap target is outside the range, so
// the allocator returned the same out-of-range address for every domain: `Contains` was false, the
// router saw a literal, and two different domains shared one synthetic address besides.
func TestADegenerateRangeIssuesNothingOutsideItself(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		inet4 string
	}{
		{name: "v4 /32", inet4: "198.18.0.7/32"},
		{name: "v4 /31", inet4: "198.18.0.0/31"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			prefix := netip.MustParsePrefix(testCase.inet4)
			store := NewStore(t.Context(), log.NewNOPFactory().NewLogger("fakeip"), prefix, netip.Prefix{})
			require.NoError(t, store.Start())
			t.Cleanup(func() { _ = store.Close() })

			address, err := store.Create("first.example", false)
			require.Error(t, err,
				"a range with no issuable address must refuse instead of issuing %v", address)
			require.False(t, address.IsValid())
			require.NotContains(t, address.String(), "198.18.0.2",
				"the allocator wrapped outside its own prefix")
		})
	}
}

// TestEveryIssuedAddressIsInsideItsRange is the positive half: a range that can work keeps working,
// and every address it hands out is one `Contains` recognises - including after the cursor wraps,
// which is the path that produced the out-of-range address before the guard existed.
func TestEveryIssuedAddressIsInsideItsRange(t *testing.T) {
	prefix := netip.MustParsePrefix("198.18.0.0/29")
	store := NewStore(t.Context(), log.NewNOPFactory().NewLogger("fakeip"), prefix, netip.Prefix{})
	require.NoError(t, store.Start())
	t.Cleanup(func() { _ = store.Close() })

	seen := make(map[netip.Addr]bool, 16)
	for index := 0; index < 16; index++ {
		address, err := store.Create("domain-"+string(rune('a'+index))+".example", false)
		require.NoError(t, err)
		require.True(t, prefix.Contains(address),
			"issued %v, which is outside the configured range %v: route would treat it as a literal",
			address, prefix)
		require.NotEqual(t, prefix.Addr(), address, "the network address is not a host address")
		require.True(t, store.Contains(address),
			"Store.Contains must agree with the range, or the restore path never sees the address")
		seen[address] = true
	}
	require.Greater(t, len(seen), 1,
		"a /29 must be able to hand out more than one address before it wraps")
}
