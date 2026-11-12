package vless

// The network-generation wiring for pooled transports.
//
// The transport pools (v2rayhttp, v2raygrpclite, v2rayxhttp) are reset by the outbound's listener
// phase, which runs AFTER the network manager publishes the new generation. A dial that arrives in
// that window used to be handed a connection built for the previous network. The outbound now owns
// a runtimecoord generation gate: before the first hand-out in the window it runs the same
// InterfaceUpdated action the listener would run, and only a successful creation acknowledges the
// new generation.
//
// This pins the production wiring: an outbound built with a coordinator must register a gate, and a
// published generation must make that gate stale before any listener ran. The gate's own contract is
// tested in common/runtimecoord.

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/common/runtimecoord"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"

	"github.com/sagernet/sing/service"
	"github.com/stretchr/testify/require"
)

func TestVLESSOutboundRegistersTheGenerationGate(t *testing.T) {
	coordinator := runtimecoord.New()
	defer coordinator.Close()
	ctx := service.ContextWith[*runtimecoord.Coordinator](context.Background(), coordinator)

	instance, err := NewOutbound(ctx, nil, log.NewNOPFactory().NewLogger("generation-gate"), "generation-gate",
		option.VLESSOutboundOptions{
			ServerOptions: option.ServerOptions{Server: "127.0.0.1", ServerPort: 443},
			UUID:          "b831381d-6324-4d53-ad4f-8cda48b30811",
		})
	require.NoError(t, err)
	outbound, isOutbound := instance.(*Outbound)
	require.True(t, isOutbound)
	require.NotNil(t, outbound.generation, "the outbound must register with the runtime coordinator")
	require.False(t, outbound.generation.Stale())

	coordinator.Advance(1)
	require.True(t, outbound.generation.Stale(),
		"a published generation must make the gate stale before the listener phase ran")
}
