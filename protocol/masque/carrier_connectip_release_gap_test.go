//go:build with_quic

package masque

// The old CONNECT-IP inner flow's server-side release, asserted positively.
//
// # What this now proves
//
//   restart the client session, close the client-side old flow, then wait
//   <= 5 s:          the server's userspace stack HAS terminated the old inner flow
//
// The router's `closed` counter is incremented in the same defer that closes the inner flow, so a
// non-zero count means the flow's socket and its stack resources are gone rather than parked past
// the session that carried them.
//
// # What it used to be
//
// This file used to pin the opposite - a measured gap where `closed` stayed at zero after 5 s and
// even after 30 s - because nothing tied an inner flow to the session that produced it: the stack
// keys flows by 4-tuple, the session keys addresses. The endpoint now records each inner flow against
// its source address (`flow_registry.go`) and closes the ones whose address belonged to a retired
// session, which is what turns this from a gap record into a release contract.

import (
	"context"
	"net/netip"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

func TestTheOldCONNECTIPInnerFlowIsReleasedWhenItsSessionRetires(t *testing.T) {
	requireHTTP3Client(t)
	serverPrefix := netip.MustParsePrefix(connectIPPrefix)
	echoPort := startCarrierEcho(t)
	router := &carrierRouter{}
	_, port := startRoutedConnectIPServer(t, serverPrefix, router)
	clientEndpoint := startConnectIPClient(t, port)

	require.Eventually(t, func() bool {
		return clientEndpoint.state.Load().configured
	}, 15*time.Second, 5*time.Millisecond, "the tunnel never established")

	innerDestination := M.SocksaddrFrom(netip.AddrFrom4([4]byte{10, 42, 0, 0}), echoPort)
	before := carrierRoundTrip(t, clientEndpoint, innerDestination, "before-the-change")
	require.EqualValues(t, 1, router.flows.Load())

	restartStarted := time.Now()
	clientEndpoint.InterfaceUpdated(context.Background())
	// Close the client-side flow: the session that carried it is gone, so this is the only signal the
	// old path can produce.
	_ = before.Close()

	// The release must arrive on its own, driven by the session retirement, not by this test's close.
	require.Eventually(t, func() bool {
		return router.closed.Load() >= 1
	}, 5*time.Second, 10*time.Millisecond,
		"the server kept the old CONNECT-IP inner flow past its session: the release contract regressed")

	releaseDelay := time.Since(restartStarted)
	t.Logf("old CONNECT-IP inner flow released=%d after %s", router.closed.Load(), releaseDelay.Round(time.Millisecond))
	require.Less(t, releaseDelay, 5*time.Second)

	// The new generation is unaffected: a new flow still carries bytes over the re-established tunnel.
	require.Eventually(t, func() bool {
		return clientEndpoint.state.Load().configured && clientEndpoint.client.Ready()
	}, 40*time.Second, 20*time.Millisecond, "the tunnel did not re-establish after the network change")
	after := carrierRoundTrip(t, clientEndpoint, innerDestination, "after-the-change")
	_ = after.Close()
	require.GreaterOrEqual(t, router.flows.Load(), int32(2))
}
