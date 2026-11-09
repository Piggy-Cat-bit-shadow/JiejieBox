//go:build with_quic

package masque

// The old CONNECT-IP inner flow's server-side release: a measured GAP, pinned instead of asserted
// away.
//
// # What was asked and what was found
//
// The closeout review asked for "独立观测旧 CONNECT-IP 会话、用户态栈和资源最终回收" and for the
// asynchronous completion point plus its bound. The row's positive half - a new tunnel and a new flow
// carrying bytes after the change - is in carrier_connectip_datapath_test.go. This file records the
// negative half, because it is not merely "slow":
//
//	restart the client session, close the client-side old flow, then wait
//	5 s / 30 s:      the server's userspace stack has NOT terminated the old inner flow
//
// Both waits were measured while writing this test (30 s in the first version, 5 s in the pinned
// one). The server router's `closed` counter - incremented in the same defer that closes the inner
// flow - stays at zero, so the old inner flow keeps its socket and its stack resources past the
// session that carried it.
//
// # Why this is a test and not a comment
//
// A comment would rot. This test fails as soon as the endpoint's owner makes the old stack reclaim
// its flows on session end, which is the moment the gap is closed: flip it to the positive assertion
// (the wait from the test below, with the measured delay) and cite it as the release evidence. The
// production objects, the topology and the trigger are the same as the positive row's, so nothing
// else has to be rebuilt.
//
// Ownership: the endpoint's userspace stack and session lifecycle are the MASQUE endpoint's internals
// (window 05). This window observed it through the production server endpoint; it did not change it.

import (
	"context"
	"net/netip"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// TestTheOldCONNECTIPInnerFlowIsNotReleasedWithinTheCurrentBound is the reproduction.
func TestTheOldCONNECTIPInnerFlowIsNotReleasedWithinTheCurrentBound(t *testing.T) {
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
	// old path can produce. It is deliberately done BEFORE waiting, so the wait cannot be explained
	// by the test still holding the flow open.
	_ = before.Close()

	// Five seconds is already far past the point where the new tunnel is ready and serving (the
	// positive row measures that within tens of seconds); anything the old stack was going to reclaim
	// from one cancelled session has had ample time.
	time.Sleep(5 * time.Second)
	require.EqualValues(t, 0, router.closed.Load(),
		"the server RELEASED the old CONNECT-IP inner flow: the resource-release gap this test pins "+
			"has been closed. Flip this test into the positive assertion (wait for the release and "+
			"record the delay) and cite it as the old-session release evidence")
	t.Logf("MEASURED old CONNECT-IP inner flow: released=%d after %s (gap: the userspace stack "+
		"outlives the session and the client-side flow that carried it)", router.closed.Load(),
		time.Since(restartStarted).Round(time.Millisecond))

	// The new generation is unaffected by the unreleased old flow: a new flow still carries bytes.
	require.Eventually(t, func() bool {
		return clientEndpoint.state.Load().configured && clientEndpoint.client.Ready()
	}, 40*time.Second, 20*time.Millisecond, "the tunnel did not re-establish after the network change")
	after := carrierRoundTrip(t, clientEndpoint, innerDestination, "after-the-change")
	_ = after.Close()
	require.GreaterOrEqual(t, router.flows.Load(), int32(2))
}
