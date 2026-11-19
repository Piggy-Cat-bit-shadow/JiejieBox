package route

import (
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/stretchr/testify/require"
)

// TestARetiredPlaceholderIsRefusedWithoutAFakeIPStore is the durable half of the fake-IP refusal.
//
// `retiredFakeIPAddress` needs a LIVE fakeip transport to answer, and it used to return "not ours"
// when the current configuration has none - so a configuration that removed fake-IP, or moved it to a
// different range and dropped the server, dialled addresses the installation itself had minted as
// ordinary literals. The cache file's retirement record is the only surviving statement of that
// history, and it is now consulted whenever the live store cannot answer.
//
// The record is seeded directly here because the wiring that produces it is the cache file's, and
// this test is about the decision rather than about bolt; the branch that reads it is exercised too
// (a Router with no context resolves to nothing, which is the pre-existing behaviour for tests that
// build a Router by hand).
func TestARetiredPlaceholderIsRefusedWithoutAFakeIPStore(t *testing.T) {
	router := &Router{}
	router.fakeIPRetiredDurable = adapter.FakeIPNormalizeRetiredIntervals([]adapter.FakeIPIssuanceInterval{{
		From: netip.MustParseAddr("198.18.0.2"),
		To:   netip.MustParseAddr("198.18.0.9"),
	}})

	retired, interval := router.retiredFakeIPAddress(netip.MustParseAddr("198.18.0.5"))
	require.True(t, retired,
		"an address this installation minted must still be refused when no fakeip server is "+
			"configured any more; otherwise it is dialled as a literal")
	require.Equal(t, netip.MustParseAddr("198.18.0.2"), interval.From)
	require.Equal(t, netip.MustParseAddr("198.18.0.9"), interval.To)

	// The positive control: past the issuance cursor the address was never handed out, and the
	// refusal must stay proof-based rather than becoming a blanket block on the range.
	retired, _ = router.retiredFakeIPAddress(netip.MustParseAddr("198.18.0.200"))
	require.False(t, retired, "an address past the cursor was never issued and must keep crossing")

	// And nothing at all for a real address.
	retired, _ = router.retiredFakeIPAddress(netip.MustParseAddr("93.184.216.34"))
	require.False(t, retired)
}

// TestNoFakeIPHistoryMeansNoRefusal pins the degradation: with neither a live store nor a durable
// record, every address crosses - which is what a machine that never configured fake-IP must do.
func TestNoFakeIPHistoryMeansNoRefusal(t *testing.T) {
	router := &Router{}
	retired, interval := router.retiredFakeIPAddress(netip.MustParseAddr("198.18.0.5"))
	require.False(t, retired)
	require.Equal(t, adapter.FakeIPIssuanceInterval{}, interval)
}
