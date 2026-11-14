package route

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/trafficclass"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
)

// BenchmarkNonConnectedPacketSetup measures one ordinary proxied UDP flow setup up to the point the
// outbound's ListenPacket returns. The dialer refuses immediately, so the benchmark contains the
// setup work and not a socket, a handshake, or a copy loop.
//
// It exists to price the round-1 change: the non-connected arm now attaches the traffic class,
// enters the dial gate and derives a dial-setup context before calling the outbound. Those costs are
// per FLOW SETUP, not per packet, and this benchmark is how the claim is checked rather than
// asserted.
func BenchmarkNonConnectedPacketSetup(b *testing.B) {
	manager := NewConnectionManager(log.NewNOPFactory().Logger())
	b.Cleanup(func() { _ = manager.Close() })

	dialer := benchPacketDialer{}
	metadata := adapter.InboundContext{
		Destination:  M.ParseSocksaddrHostPort("bench.example", 3478),
		TrafficClass: trafficclass.ClassDefault,
	}

	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		manager.NewPacketConnection(context.Background(), dialer, packetConnStub{}, metadata, nil)
	}
}

// benchPacketDialer fails the setup immediately so the benchmark measures the route layer only.
type benchPacketDialer struct{}

var errBenchPacketDial = errors.New("benchmark dial refused")

func (benchPacketDialer) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return nil, errBenchPacketDial
}

func (benchPacketDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, errBenchPacketDial
}
