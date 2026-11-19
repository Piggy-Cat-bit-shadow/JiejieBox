package tun

import (
	"go4.org/netipx"
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-tun"
	"github.com/stretchr/testify/require"
)

// retiredFakeIPStoreStub is the shape of a store whose CURRENT range is one prefix while an EARLIER
// generation issued from another. It is the case the flow guard used to answer with the wrong
// question: `Contains` says false, so the route set bypassed an address this installation minted.
type retiredFakeIPStoreStub struct {
	inet4   netip.Prefix
	retired []adapter.FakeIPIssuanceInterval
}

func (s *retiredFakeIPStoreStub) Start() error { return nil }
func (s *retiredFakeIPStoreStub) Close() error { return nil }
func (s *retiredFakeIPStoreStub) Contains(address netip.Addr) bool {
	return s.inet4.Contains(address)
}
func (s *retiredFakeIPStoreStub) Create(string, bool) (netip.Addr, error) { return netip.Addr{}, nil }
func (s *retiredFakeIPStoreStub) Lookup(netip.Addr) (string, bool)        { return "", false }
func (s *retiredFakeIPStoreStub) Reset() error                            { return nil }
func (s *retiredFakeIPStoreStub) RetiredAddresses() []adapter.FakeIPIssuanceInterval {
	return s.retired
}

// TestARetiredPlaceholderIsNotBypassedByTheRouteSet is the L0 boundary one generation later.
//
// A client that resolved a domain through fake-IP can still be holding the address when the
// configuration moves the range (or drops fake-IP altogether). That address is just as synthetic as
// a current one - no route exists for it on the platform, and only the router can decide what it
// means - but `Store().Contains` answers the CURRENT-range question, so the route-set guard used to
// bypass it and the router's refusal for an unattributable placeholder never ran.
func TestARetiredPlaceholderIsNotBypassedByTheRouteSet(t *testing.T) {
	inbound, router := hijackTestInbound(t, nil, false)
	router.bypassable.Store(false)
	// The CURRENT generation issues from a different range, so Contains() is false for the address
	// the flow carries.
	inbound.fakeIPStore = &retiredFakeIPStoreStub{
		inet4: netip.MustParsePrefix("198.20.0.0/16"),
		retired: []adapter.FakeIPIssuanceInterval{{
			From: netip.MustParseAddr("198.18.0.2"),
			To:   netip.MustParseAddr("198.18.0.9"),
		}},
	}
	// What Start resolves once for the per-flow path to read without a lock.
	inbound.fakeIPRetired = adapter.FakeIPNormalizeRetiredIntervals(inbound.fakeIPStore.RetiredAddresses())
	inbound.routeAddressSet = []*netipx.IPSet{ipSetFrom(t, "10.0.0.0/8")}

	verdict := inbound.JudgeFlow(
		uint8(headerTCP),
		netip.MustParseAddrPort("192.168.1.2:40000"),
		netip.MustParseAddrPort("198.18.0.5:443"),
		nil,
	)
	require.EqualValues(t, 1, router.preMatchCalls.Load(),
		"a placeholder from a retired generation must still reach the router: that is where the "+
			"refusal for an unattributable placeholder lives, and the platform has no route for it")
	require.NotEqual(t, tun.ActionBypass, verdict.Action)

	// The control: an address in the SAME retired range that the issuance cursor never reached is
	// NOT a placeholder this installation minted, and it must bypass exactly as before. Without this
	// the assertion above would hold just as well if retired ranges had become blanket blocks.
	router.preMatchCalls.Store(0)
	verdict = inbound.JudgeFlow(
		uint8(headerTCP),
		netip.MustParseAddrPort("192.168.1.2:40000"),
		netip.MustParseAddrPort("198.18.0.200:443"),
		nil,
	)
	require.Equal(t, tun.ActionBypass, verdict.Action,
		"an address past the issuance cursor is an ordinary literal and must keep bypassing")
	require.EqualValues(t, 0, router.preMatchCalls.Load())
}

// TestARetiredPlaceholderIsRecognizedWithoutAFakeIPTransport is the durable half: the configuration
// no longer has a fakeip server at all, so there is no store to ask, and the only surviving statement
// of what the installation issued is the cache file's record - which Start reads into the same
// snapshot.
func TestARetiredPlaceholderIsRecognizedWithoutAFakeIPTransport(t *testing.T) {
	inbound := &Inbound{}
	inbound.fakeIPRetired = adapter.FakeIPNormalizeRetiredIntervals([]adapter.FakeIPIssuanceInterval{{
		From: netip.MustParseAddr("198.18.0.2"),
		To:   netip.MustParseAddr("198.18.0.9"),
	}})
	require.True(t, inbound.fakeIPPlaceholder(netip.MustParseAddr("198.18.0.5")),
		"with no store at all, the durable record is the only way to recognize the placeholder")
	require.False(t, inbound.fakeIPPlaceholder(netip.MustParseAddr("198.18.0.200")),
		"past the cursor, an address must not be treated as ours")
	require.False(t, inbound.fakeIPPlaceholder(netip.MustParseAddr("93.184.216.34")))
}
