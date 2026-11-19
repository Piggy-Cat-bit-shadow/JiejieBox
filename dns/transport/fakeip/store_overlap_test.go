package fakeip

// The audit's follow-up: the range-shrink case it reproduced is a REAL wrong-destination defect, not a
// boundary. When the old issuance reaches past the end of the new, smaller pool, `resumeAfterLedger`
// used to skip the interval entirely (it only looked at interval.To being INSIDE the range), so
// allocation restarted at the new pool's beginning and re-bound an address a client still holds to a
// new domain.
//
// Two shapes, both tested here through the public API with a shared ledger and no mocks:
//
//	overlap reaches the end of the new pool  -> Start must REFUSE (there is no safe address above it,
//	                                            and the wrap would re-enter the overlap)
//	overlap leaves a free tail               -> Start resumes past the overlap, and the next address is
//	                                            outside the old issuance

import (
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/service"
	"github.com/stretchr/testify/require"
)

func newOverlapStore(t *testing.T, ledger *adapter.FakeIPIssuanceLedger, prefix netip.Prefix) *Store {
	t.Helper()
	ctx := service.ContextWithPtr(t.Context(), ledger)
	store := NewStore(ctx, log.NewNOPFactory().NewLogger("fakeip"), prefix, netip.Prefix{})
	return store
}

// TestAnOverlapThatFillsTheNewPoolRefusesToStart is the defect the audit reproduced: /15 with twelve
// names issued, then /29 - every address of the small pool has already been handed out.
func TestAnOverlapThatFillsTheNewPoolRefusesToStart(t *testing.T) {
	ledger := adapter.NewFakeIPIssuanceLedger()
	wide := newOverlapStore(t, ledger, netip.MustParsePrefix("198.18.0.0/15"))
	require.NoError(t, wide.Start())
	var held netip.Addr
	for index := 0; index < 12; index++ {
		address, err := wide.Create("old-"+string(rune('a'+index))+".example", false)
		require.NoError(t, err)
		if index == 0 {
			held = address
		}
	}
	require.Equal(t, netip.MustParseAddr("198.18.0.2"), held, "the first address of a /15 is .2")
	require.NoError(t, wide.Close())

	narrow := newOverlapStore(t, ledger, netip.MustParsePrefix("198.18.0.0/29"))
	startErr := narrow.Start()
	require.Error(t, startErr,
		"a /29 whose every address the previous generation already issued must be refused: starting "+
			"would hand %v, a client's held address, to a new domain", held)
	require.Contains(t, startErr.Error(), "overlaps addresses issued by a previous generation")
	_ = narrow.Close()

	// And the old address is still attributable to the OLD domain, so nothing was silently re-bound.
	require.True(t, ledger.Issued(held))
}

// TestAnOverlapWithAFreeTailResumesPastIt is the other half: /15 with two names issued, then /29 - the
// overlap covers .2-.3 and .4-.6 remain free, so the new generation must resume at .4.
func TestAnOverlapWithAFreeTailResumesPastIt(t *testing.T) {
	ledger := adapter.NewFakeIPIssuanceLedger()
	wide := newOverlapStore(t, ledger, netip.MustParsePrefix("198.18.0.0/15"))
	require.NoError(t, wide.Start())
	first, err := wide.Create("a.example", false)
	require.NoError(t, err)
	second, err := wide.Create("b.example", false)
	require.NoError(t, err)
	require.NoError(t, wide.Close())

	narrow := newOverlapStore(t, ledger, netip.MustParsePrefix("198.18.0.0/29"))
	t.Cleanup(func() { _ = narrow.Close() })
	require.NoError(t, narrow.Start(),
		"a /29 with a free tail above the overlap must start, resuming past it")
	fresh, err := narrow.Create("c.example", false)
	require.NoError(t, err)
	require.True(t, netip.MustParsePrefix("198.18.0.0/29").Contains(fresh))
	require.NotEqual(t, first, fresh)
	require.NotEqual(t, second, fresh)
	require.Greater(t, fresh.Compare(second), 0,
		"the new generation must resume past the previous generation's overlap, got %v after %v-%v",
		fresh, first, second)
}

// TestAnOverlapThatIsNotTheLastAddressResumesImmediately is the minimal case: one issued address inside
// the new pool, with the rest of the pool free.
func TestAnOverlapThatIsNotTheLastAddressResumesImmediately(t *testing.T) {
	ledger := adapter.NewFakeIPIssuanceLedger()
	wide := newOverlapStore(t, ledger, netip.MustParsePrefix("198.18.0.0/15"))
	require.NoError(t, wide.Start())
	held, err := wide.Create("held.example", false)
	require.NoError(t, err)
	require.NoError(t, wide.Close())

	// The same /24 that contains the issued address: its first issuable address is .2, which the
	// previous generation holds, so the new generation must start at .3.
	narrow := newOverlapStore(t, ledger, netip.MustParsePrefix("198.18.0.0/24"))
	t.Cleanup(func() { _ = narrow.Close() })
	require.NoError(t, narrow.Start())
	next, err := narrow.Create("next.example", false)
	require.NoError(t, err)
	require.Equal(t, netip.MustParseAddr("198.18.0.3"), next,
		"the first address of the /24 is .2, which the previous generation issued; the new one starts at .3")
	require.NotEqual(t, held, next)
}
