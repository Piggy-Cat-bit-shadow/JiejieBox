package trusttunnel

// The network-generation wiring for the TrustTunnel outbound, on a real HTTP/2 session.
//
// The network manager advances the generation first and runs the listener phase afterwards. A dial
// that arrives in that window used to borrow a session built for the previous network. The outbound
// owns a runtimecoord generation gate: before the first hand-out in the window it runs the same
// Reset the listener would run, and only a successful flow acknowledges the new generation.
//
// The other half is the incident's reset storm: once the first demand has applied the transition,
// the listener for the SAME generation must not apply it again and cut the session that demand just
// created.

import (
	"context"
	"io"
	"testing"

	"github.com/sagernet/sing-box/common/runtimecoord"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

func newTrustTunnelCarrierOutboundWithCoordinator(t *testing.T, port uint16, coordinator *runtimecoord.Coordinator) *Outbound {
	t.Helper()
	ctx := service.ContextWith[*runtimecoord.Coordinator](context.Background(), coordinator)
	created, err := NewOutbound(ctx, nil, log.NewNOPFactory().NewLogger("generation-gate"), "generation-gate",
		option.TrustTunnelOutboundOptions{
			ServerOptions: option.ServerOptions{Server: "127.0.0.1", ServerPort: port},
			Username:      trustTunnelCarrierUser,
			Password:      trustTunnelCarrierPassword,
			OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{
				TLS: &option.OutboundTLSOptions{
					Enabled:    true,
					ServerName: "localhost",
					Insecure:   true,
				},
			},
		})
	require.NoError(t, err)
	outbound, isOutbound := created.(*Outbound)
	require.True(t, isOutbound)
	t.Cleanup(func() { _ = outbound.Close() })
	return outbound
}

func TestTrustTunnelOutboundRegistersTheGenerationGate(t *testing.T) {
	coordinator := runtimecoord.New()
	defer coordinator.Close()
	outbound := newTrustTunnelCarrierOutboundWithCoordinator(t, 1, coordinator)
	require.NotNil(t, outbound.generation, "the outbound must register with the runtime coordinator")
	require.False(t, outbound.generation.Stale())

	coordinator.Advance(1)
	require.True(t, outbound.generation.Stale(),
		"a published generation must make the gate stale before the listener phase ran")
}

// TestTrustTunnelTransitionIsAppliedOncePerGeneration proves both halves on a real session: the
// demand in the publish-to-listener window resets the old session, and the listener that runs
// afterwards for the same generation does NOT reset the new one. The flow opened after the
// transition keeps carrying payload after the listener call; under the per-dial reset behaviour the
// listener's second reset closed that session and the flow failed.
func TestTrustTunnelTransitionIsAppliedOncePerGeneration(t *testing.T) {
	handler := &trustTunnelCarrierHandler{target: startTrustTunnelCarrierEcho(t)}
	port := startTrustTunnelCarrierServer(t, handler)
	coordinator := runtimecoord.New()
	defer coordinator.Close()
	outbound := newTrustTunnelCarrierOutboundWithCoordinator(t, port, coordinator)

	before := trustTunnelRoundTrip(t, outbound, "before-the-change")
	require.EqualValues(t, 1, handler.streams.Load())

	// The network manager publishes the generation; the listener has not run yet.
	coordinator.Advance(1)
	require.True(t, outbound.generation.Stale())

	after := trustTunnelRoundTrip(t, outbound, "the-demand-applies-the-transition")
	require.False(t, outbound.generation.Stale(), "a successful flow must acknowledge the new generation")
	require.GreaterOrEqual(t, handler.streams.Load(), int32(2))

	// The listener phase for the SAME generation: the transition was already applied, so this must
	// be a no-op. The session carrying the flow opened above has to survive it.
	outbound.InterfaceUpdated(context.Background())
	_, err := after.Write([]byte("after-the-listener"))
	require.NoError(t, err)
	echo := make([]byte, len("after-the-listener"))
	_, err = io.ReadFull(after, echo)
	require.NoError(t, err)
	require.Equal(t, "after-the-listener", string(echo),
		"the listener for an already-applied generation cut the session a demand had just established")
	_ = before.Close()
	_ = after.Close()

	// A NEW generation is a new transition and must reset the session again.
	coordinator.Advance(2)
	require.True(t, outbound.generation.Stale())
	series := trustTunnelRoundTrip(t, outbound, "after-the-second-change")
	_ = series.Close()
	require.GreaterOrEqual(t, handler.streams.Load(), int32(3))
}
