package anytls

// The publish-to-listener window for the AnyTLS pool (audit P14, window B).
//
// A physical network change publishes the coordinator's new generation first and notifies the
// listeners afterwards. A demand that arrives in between used to be handed a session built for the
// previous network: the AnyTLS session still looked healthy (TLS over TCP has no session keepalive),
// so nothing else refused it. The generation gate exists for exactly this window, and the other seven
// transport owners route their dial and listener halves through it; this pool did not, so its whole
// session pool stayed ungated.
//
// The tests run the real outbound against the real pinned AnyTLS service and count sessions at the
// server by client source port, so "the demand reused the old session" and "the late listener cut the
// session the demand built" are both observed on the wire rather than inferred from the client.

import (
	"context"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/runtimecoord"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// newGatedCarrierOutbound builds the outbound through NewOutbound with a coordinator in the
// constructor context, so the generation gate under test is the production one.
func newGatedCarrierOutbound(t *testing.T, port uint16) (*Outbound, *runtimecoord.Coordinator) {
	t.Helper()
	coordinator := runtimecoord.New()
	t.Cleanup(func() { _ = coordinator.Close() })
	ctx := service.ContextWith[*runtimecoord.Coordinator](context.Background(), coordinator)
	outbound := newCarrierOutboundIn(t, ctx, port)
	require.NotNil(t, outbound.generation,
		"the outbound did not create a generation gate from its constructor context")
	t.Cleanup(func() { outbound.generation.Close() })
	return outbound, coordinator
}

// TestADemandInThePublishToListenerWindowDoesNotReuseTheOldSession is the regression: after the epoch
// moves and before the listener runs, the first demand must not be multiplexed onto a session that
// predates the transition.
func TestADemandInThePublishToListenerWindowDoesNotReuseTheOldSession(t *testing.T) {
	handler := &carrierHandler{target: startCarrierEcho(t)}
	port := startCarrierServer(t, handler)
	outbound, coordinator := newGatedCarrierOutbound(t, port)

	before := roundTrip(t, outbound, "before-the-change")
	require.Eventually(t, func() bool { return handler.sessions() == 1 },
		carrierSettle, time.Millisecond, "the first flow did not establish a session")

	// The physical transition: the coordinator publishes the new generation, the listener phase has
	// not reached this outbound yet.
	coordinator.Advance(1)

	after := roundTrip(t, outbound, "inside-the-window")
	require.Eventually(t, func() bool { return handler.sessions() == 2 },
		carrierSettle, time.Millisecond,
		"the demand inside the publish-to-listener window reused the session built on the previous network")

	// Positive control: the transition really closed the old session rather than merely bypassing it.
	require.Eventually(t, func() bool {
		_ = before.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
		_, err := before.Write([]byte("stale"))
		return err != nil
	}, carrierSettle, 10*time.Millisecond,
		"the pre-change session still accepted a write: the transition did not close it")

	_ = before.Close()
	_ = after.Close()
}

// TestTheLateListenerDoesNotCutTheSessionTheDemandBuilt is the other half of the same contract: once a
// demand inside the window has applied the transition and built a session on the new network, the
// listener notification for that SAME generation must not run the destruction again - that would cut
// the session the demand just built.
func TestTheLateListenerDoesNotCutTheSessionTheDemandBuilt(t *testing.T) {
	handler := &carrierHandler{target: startCarrierEcho(t)}
	port := startCarrierServer(t, handler)
	outbound, coordinator := newGatedCarrierOutbound(t, port)

	before := roundTrip(t, outbound, "before-the-change")
	require.Eventually(t, func() bool { return handler.sessions() == 1 }, carrierSettle, time.Millisecond)

	coordinator.Advance(1)
	after := roundTrip(t, outbound, "inside-the-window")
	require.Eventually(t, func() bool { return handler.sessions() == 2 }, carrierSettle, time.Millisecond)

	// The listener phase arrives after the demand already ran the transition.
	outbound.InterfaceUpdated(context.Background())

	// The flow the demand built must still carry bytes.
	require.NoError(t, after.SetDeadline(time.Now().Add(carrierSettle)))
	_, err := after.Write([]byte("still-carried"))
	require.NoError(t, err, "the late listener notification cut the session the in-window demand built")

	_ = before.Close()
	_ = after.Close()
}
