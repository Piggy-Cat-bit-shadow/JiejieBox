//go:build with_naive_outbound

package naive

// TCP and UoT business over the REAL engine (audit Q34).
//
// The Naive outbound has two business paths: a TCP tunnel (one CONNECT per flow) and UoT, where a
// UDP session rides inside one CONNECT. The audit's Q34 row asks for exactly these two against the
// final native artifact, because a fallback or a creation seam that only works for TCP would still
// leave every UDP flow on the old path. The fixture is the production inbound on loopback plus the
// production outbound with the pinned archive, so this is the closest thing to "the product" that a
// unit test can build without a device.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
	"github.com/stretchr/testify/require"
)

// udpEchoRouter is the frozen destination for both halves: TCP goes to a loopback echo server, and
// a UoT packet connection is echoed straight back to its sender.
type udpEchoRouter struct {
	naiveCarrierRouter
}

// RoutePacketConnectionEx echoes every datagram back on the same packet connection. That is what a
// UoT session needs to prove: a datagram entered the CONNECT tunnel, was routed by the inbound's
// UoT router, and came back through the same tunnel.
func (r *udpEchoRouter) RoutePacketConnectionEx(ctx context.Context, conn N.PacketConn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	go func() {
		defer conn.Close()
		if onClose != nil {
			defer onClose(nil)
		}
		packet := buf.NewPacket()
		defer packet.Release()
		for {
			packet.Reset()
			destination, err := conn.ReadPacket(packet)
			if err != nil {
				return
			}
			if err = conn.WritePacket(packet, destination); err != nil {
				return
			}
		}
	}()
}

// isMissingNativeLibrary reports the one environment failure that must skip instead of fail: a
// runner without the pinned archive cannot exercise any of this.
func isMissingNativeLibrary(err error) bool {
	return err != nil && strings.Contains(err.Error(), "library not found")
}

// TestARealTCPFlowAndARealUoTSessionBothCarryBytes is the Q34 business-path test.
func TestARealTCPFlowAndARealUoTSessionBothCarryBytes(t *testing.T) {
	echo := startNaiveCarrierEcho(t)
	router := &udpEchoRouter{naiveCarrierRouter: naiveCarrierRouter{target: echo}}
	port, certificate := startNaiveCarrierInbound(t, router)

	ctx := service.ContextWithDefaultRegistry(context.Background())
	ctx = service.ContextWith[adapter.DNSRouter](ctx, &carrierDNSRouter{})
	created, err := NewOutbound(ctx, nil, log.NewNOPFactory().NewLogger("naive-business"), "naive-business",
		option.NaiveOutboundOptions{
			ServerOptions: option.ServerOptions{Server: "127.0.0.1", ServerPort: port},
			Username:      naiveCarrierUser,
			Password:      naiveCarrierPassword,
			UDPOverTCP:    &option.UDPOverTCPOptions{Enabled: true},
			OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{
				TLS: &option.OutboundTLSOptions{
					Enabled:     true,
					ServerName:  "localhost",
					Certificate: []string{certificate},
				},
			},
		})
	if err != nil {
		if isMissingNativeLibrary(err) {
			t.Skipf("the native Cronet archive is not installed on this host (%v); "+
				"the business paths cannot be exercised here", err)
		}
		require.NoError(t, err)
	}
	outbound, isOutbound := created.(*Outbound)
	require.True(t, isOutbound)
	scope := adapter.NewScope(context.Background(), log.NewNOPFactory().NewLogger("naive-business-scope"))
	require.NoError(t, outbound.Start(adapter.StartStateStart, scope))
	t.Cleanup(func() { _ = scope.Close() })
	t.Cleanup(func() { _ = outbound.client.Close() })

	// TCP: the ordinary tunnel.
	tcpConn := naiveRoundTrip(t, outbound, "tcp-business")
	_ = tcpConn.Close()

	// UoT: one CONNECT carrying a UDP session, which the inbound routes as a packet connection.
	sessionCtx, cancel := context.WithTimeout(context.Background(), naiveCarrierSettle)
	defer cancel()
	packetConn, err := outbound.ListenPacket(sessionCtx, M.ParseSocksaddr("udp-echo.test:5353"))
	require.NoError(t, err, "UoT must be available when udp_over_tcp is enabled")
	defer packetConn.Close()

	destination := M.ParseSocksaddr("udp-echo.test:5353")
	payload := []byte("uot-business")
	// The UoT session is a packet connection with its own address framing (the destination prefix
	// travels on the wire), so the test writes through that interface rather than net.PacketConn's
	// WriteTo, which cannot describe a domain destination.
	packetWriter, isPacketConn := packetConn.(N.PacketConn)
	require.True(t, isPacketConn, "the UoT session must expose the packet interface, got %T", packetConn)
	sendBuffer := buf.As(payload)
	require.NoError(t, packetWriter.WritePacket(sendBuffer, destination))

	reply := buf.NewSize(len(payload))
	defer reply.Release()
	_ = packetConn.SetReadDeadline(time.Now().Add(naiveCarrierSettle))
	_, err = packetWriter.ReadPacket(reply)
	require.NoError(t, err, "the UoT session must carry the datagram back")
	require.Equal(t, payload, reply.Bytes())

	// The creation ledger covers both business paths and still balances.
	stats := outbound.NativeCreationStats()
	require.GreaterOrEqual(t, stats.Created, uint64(1))
	require.Equal(t, stats.Attempts, stats.Accounted())
	require.Zero(t, stats.InFlight)
}
