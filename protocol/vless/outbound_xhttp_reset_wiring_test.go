//go:build with_xhttp

package vless

// The network-change wiring for the XHTTP transport.
//
// XHTTP's connection pool already distinguishes a reuse boundary (`RetireSuspect`: no new stream on a
// pooled connection, idle ones closed, busy ones drained) from a trim and from a final close, and
// those semantics are pinned in transport/v2rayxhttp. What was missing is the PRODUCTION decision:
// the VLESS outbound's InterfaceUpdated resets a transport that offers Reset and otherwise falls back
// to Close, and until XHTTP offered Reset every network change took the Close path - the lifecycle's
// final action, which empties the pool without starting a new generation.
//
// This pins the decision at the real constructor: the transport a configuration produces for
// transport.type=xhttp must be reset on a change, not closed.
//
// The same wiring serves vmess and trojan by construction (their InterfaceUpdated bodies are the same
// contract over the same registry); the shared contract is kept identical in
// outbound_quic_transition_test.go's harness.

import (
	"context"
	"testing"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	_ "github.com/sagernet/sing-box/transport/v2rayxhttp"

	"github.com/stretchr/testify/require"
)

// xhttpWiringUUID is local to this file: the sibling transition test is built with with_quic, and
// this one must compile with with_xhttp alone.
const xhttpWiringUUID = "b831381d-6324-4d53-ad4f-8cda48b30811"

func TestTheXHTTPTransportIsResetOnANetworkChange(t *testing.T) {
	instance, err := NewOutbound(context.Background(), nil, log.NewNOPFactory().NewLogger("xhttp-reset"), "xhttp-reset",
		option.VLESSOutboundOptions{
			ServerOptions: option.ServerOptions{Server: "127.0.0.1", ServerPort: 443},
			UUID:          xhttpWiringUUID,
			OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{
				TLS: &option.OutboundTLSOptions{
					Enabled:    true,
					ServerName: "xhttp.example",
					Insecure:   true,
				},
			},
			Transport: &option.V2RayTransportOptions{
				Type:         C.V2RayTransportTypeXHTTP,
				XHTTPOptions: option.V2RayXHTTPOptions{Path: "/xhttp"},
			},
		})
	require.NoError(t, err)
	outbound, isOutbound := instance.(*Outbound)
	require.True(t, isOutbound)
	require.NotNil(t, outbound.transport, "the XHTTP transport must be registered in this build")

	resetter, isResetter := outbound.transport.(interface{ Reset() error })
	require.True(t, isResetter,
		"a network change must RESET the XHTTP transport, not close it: Close is the lifecycle's final "+
			"action and empties the pool without starting a new generation")
	_ = resetter
}
