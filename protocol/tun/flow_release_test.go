package tun

import (
	"context"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/trafficcontrol"
	"github.com/sagernet/sing-box/log"
	routepkg "github.com/sagernet/sing-box/route"
	singtun "github.com/sagernet/sing-tun"
	"github.com/sagernet/sing-tun/gtcpip/header"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// The traffic table's release on a TUN stream, driven through the real userspace stack.
//
// # Why this file exists
//
// Every connection the phone's Activity page lists comes from common/trafficcontrol's ACTIVE table.
// An entry leaves it when the tracker is closed (connTracker.Close -> manager.leave), and for a TUN
// stream that happens when the copy directions finish. Until this file there was no test that drove
// a DEVICE-originated end (FIN or RST) through the stack and asserted the entry leaves: the existing
// TUN datapath tests assert what the relay observes, and the traffic table was never observed at all.
//
// The two shapes here are the two ways an application's socket ends:
//
//	FIN  the client finished; the engine and the peer must both complete, and the entry must leave
//	RST  the client aborted; the engine must fail the stream and the entry must leave
//
// Both are run against a flow that is eligible for the kernel splice and one that is not, because
// the splice hands the lifecycle to sing-tun's splice machinery and that path can only be pinned by
// exercising it. The two are distinguished by whether the outbound socket is registered with the
// connection manager, which is exactly what makes it a splice target in production.

// releaseProbeOutbound is the outbound metadata the tracker reads: Tag and Type.
type releaseProbeOutbound struct{}

func (releaseProbeOutbound) Type() string           { return "probe" }
func (releaseProbeOutbound) Tag() string            { return "probe" }
func (releaseProbeOutbound) Network() []string      { return []string{N.NetworkTCP} }
func (releaseProbeOutbound) Dependencies() []string { return nil }

func (releaseProbeOutbound) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return nil, net.ErrClosed
}

func (releaseProbeOutbound) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, net.ErrClosed
}

var releaseProbeOutboundChain = []adapter.Outbound{releaseProbeOutbound{}}

// releaseProbeDialer dials the fixed loopback target and, when tracker is set, registers the
// outbound socket with the connection manager exactly as common/dialer does in production.
type releaseProbeDialer struct {
	target  string
	tracker *routepkg.ConnectionManager
}

func (d releaseProbeDialer) DialContext(ctx context.Context, _ string, _ M.Socksaddr) (net.Conn, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", d.target)
	if err != nil {
		return nil, err
	}
	if d.tracker != nil {
		return d.tracker.TrackConn(conn), nil
	}
	return conn, nil
}

func (d releaseProbeDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, net.ErrClosed
}

// releaseProbeHandler is the production route path in miniature: the tracker is the real
// trafficcontrol.Manager and the copy engine is the real route.ConnectionManager.
type releaseProbeHandler struct {
	traffic  *trafficcontrol.Manager
	conns    *routepkg.ConnectionManager
	dialer   releaseProbeDialer
	accepted atomic.Int64
	closed   atomic.Int64
}

func (h *releaseProbeHandler) JudgeFlow(uint8, netip.AddrPort, netip.AddrPort, []byte) singtun.FlowVerdict {
	return singtun.FlowVerdict{Action: singtun.ActionAccept}
}

func (h *releaseProbeHandler) NewDNSPacket([]byte, M.Socksaddr, M.Socksaddr, N.PacketWriter) {}

func (h *releaseProbeHandler) NewConnectionEx(ctx context.Context, conn net.Conn, source M.Socksaddr, destination M.Socksaddr, onClose N.CloseHandlerFunc) {
	h.accepted.Add(1)
	go func() {
		metadata := adapter.InboundContext{
			Inbound:       "probe-tun",
			InboundType:   "tun",
			Network:       N.NetworkTCP,
			Source:        source,
			Destination:   destination,
			OutboundChain: releaseProbeOutboundChain,
		}
		tracked := h.traffic.RoutedConnection(ctx, conn, metadata, nil, releaseProbeOutboundChain[0])
		h.conns.NewConnection(ctx, h.dialer, tracked, metadata, func(err error) {
			h.closed.Add(1)
			if onClose != nil {
				onClose(err)
			}
		})
	}()
}

func (h *releaseProbeHandler) NewPacketConnectionEx(_ context.Context, conn N.PacketConn, _ M.Socksaddr, _ M.Socksaddr, _ N.CloseHandlerFunc) {
	conn.Close()
}

type releaseProbeFixture struct {
	device  *singtun.MemoryTun
	stack   singtun.Stack
	handler *releaseProbeHandler
	access  sync.Mutex
	frames  [][]byte
}

func newReleaseProbeFixture(t *testing.T, target string, trackOutbound bool) *releaseProbeFixture {
	t.Helper()
	traffic := trafficcontrol.NewManager()
	connectionManager := routepkg.NewConnectionManager(log.NewNOPFactory().Logger())
	handler := &releaseProbeHandler{traffic: traffic, conns: connectionManager}
	handler.dialer = releaseProbeDialer{target: target}
	if trackOutbound {
		handler.dialer.tracker = connectionManager
	}
	fixture := &releaseProbeFixture{handler: handler}
	fixture.device = singtun.NewMemoryTun(singtun.MemoryTunOptions{
		MTU: 1500,
		Outbound: func(packets []*buf.Buffer) {
			fixture.access.Lock()
			for _, packet := range packets {
				frame := make([]byte, packet.Len())
				copy(frame, packet.Bytes())
				fixture.frames = append(fixture.frames, frame)
			}
			fixture.access.Unlock()
			buf.ReleaseMulti(packets)
		},
	})
	stack, err := singtun.NewStack("go", singtun.StackOptions{
		Context: context.Background(),
		Tun:     fixture.device,
		TunOptions: singtun.Options{
			MTU:          1500,
			Inet4Address: []netip.Prefix{netip.MustParsePrefix("198.18.0.1/30")},
			Inet6Address: []netip.Prefix{netip.MustParsePrefix("fd00::1/126")},
			Logger:       logger.NOP(),
		},
		Handler:     handler,
		Logger:      logger.NOP(),
		UDPTimeout:  time.Minute,
		ICMPTimeout: time.Minute,
	})
	require.NoError(t, err)
	require.NoError(t, stack.Start())
	fixture.stack = stack
	t.Cleanup(func() {
		_ = stack.Close()
		_ = fixture.device.Close()
		_ = connectionManager.Close()
	})
	return fixture
}

func (f *releaseProbeFixture) emit(t *testing.T, packet []byte) {
	t.Helper()
	written, err := f.device.WritePackets([][]byte{packet})
	require.NoError(t, err)
	require.Equal(t, 1, written)
}

func (f *releaseProbeFixture) waitForTCPFlag(t *testing.T, sourcePort, destinationPort uint16, want header.TCPFlags, timeout time.Duration) []byte {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		f.access.Lock()
		for _, frame := range f.frames {
			tcp, ok := datapathTCP(frame)
			if !ok {
				continue
			}
			if tcp.SourcePort() != destinationPort || tcp.DestinationPort() != sourcePort {
				continue
			}
			if tcp.Flags()&want == want {
				f.access.Unlock()
				return frame
			}
		}
		f.access.Unlock()
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("the stack never emitted a frame with flags %#x for %d -> %d", want, destinationPort, sourcePort)
	return nil
}

// establish completes the device-side handshake and one echoed request, and returns the sequence
// numbers the caller needs to continue the conversation.
func (f *releaseProbeFixture) establish(t *testing.T, sourcePort, destinationPort uint16) (deviceNext, stackNext uint32) {
	t.Helper()
	f.emit(t, datapathSYNWithMSS(sourcePort, destinationPort, 1460))
	synACK := f.waitForTCPFlag(t, sourcePort, destinationPort, header.TCPFlagSyn|header.TCPFlagAck, 3*time.Second)
	tcp, ok := datapathTCP(synACK)
	require.True(t, ok)
	deviceNext = 1001
	stackNext = tcp.SequenceNumber() + 1
	payload := []byte("ping")
	for attempt := 0; attempt < 60; attempt++ {
		f.emit(t, datapathIPv4TCP(sourcePort, destinationPort, deviceNext, stackNext, header.TCPFlagAck, nil))
		f.emit(t, datapathIPv4TCP(sourcePort, destinationPort, deviceNext, stackNext, header.TCPFlagAck|header.TCPFlagPsh, payload))
		if f.sawPayload(sourcePort, destinationPort) {
			return deviceNext + uint32(len(payload)), stackNext + uint32(len(payload))
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the flow %d -> %d never reached the data phase", sourcePort, destinationPort)
	return 0, 0
}

func (f *releaseProbeFixture) sawPayload(sourcePort, destinationPort uint16) bool {
	f.access.Lock()
	defer f.access.Unlock()
	for _, frame := range f.frames {
		tcp, ok := datapathTCP(frame)
		if !ok || tcp.SourcePort() != destinationPort || tcp.DestinationPort() != sourcePort {
			continue
		}
		if len(frame) > int(frame[0]&0x0f)<<2+int(tcp.DataOffset()) {
			return true
		}
	}
	return false
}

func (f *releaseProbeFixture) waitTraffic(t *testing.T, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	last := -1
	for time.Now().Before(deadline) {
		last = f.handler.traffic.ConnectionsLen()
		if last == want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("the traffic table holds %d connections, want %d", last, want)
}

// startReleaseProbeTarget is a loopback peer that echoes and closes when the client finishes.
func startReleaseProbeTarget(t *testing.T) uint16 {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer conn.Close()
				buffer := make([]byte, 4096)
				for {
					n, readErr := conn.Read(buffer)
					if n > 0 {
						_, _ = conn.Write(buffer[:n])
					}
					if readErr != nil {
						return
					}
				}
			}()
		}
	}()
	t.Cleanup(func() { listener.Close() })
	return uint16(listener.Addr().(*net.TCPAddr).Port)
}

func releaseProbeAddr(port uint16) string { return "127.0.0.1:" + strconv.Itoa(int(port)) }

// TestTUNDeviceFinReleasesTrackedFlow: the device finishes its write side and the peer finishes its
// own, and the traffic entry is gone afterwards.
func TestTUNDeviceFinReleasesTrackedFlow(t *testing.T) {
	port := startReleaseProbeTarget(t)
	for _, trackOutbound := range []bool{false, true} {
		name := "userspace-copy"
		if trackOutbound {
			name = "splice-eligible"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newReleaseProbeFixture(t, releaseProbeAddr(port), trackOutbound)
			deviceNext, stackNext := fixture.establish(t, 40000, port)
			fixture.waitTraffic(t, 1, 3*time.Second)
			fixture.emit(t, datapathIPv4TCP(40000, port, deviceNext, stackNext, header.TCPFlagFin|header.TCPFlagAck, nil))
			fixture.waitTraffic(t, 0, 5*time.Second)
			require.Equal(t, int64(1), fixture.handler.closed.Load(), "onClose must run exactly once")
		})
	}
}

// TestTUNDeviceResetReleasesTrackedFlow: the abortive close.
func TestTUNDeviceResetReleasesTrackedFlow(t *testing.T) {
	port := startReleaseProbeTarget(t)
	for _, trackOutbound := range []bool{false, true} {
		name := "userspace-copy"
		if trackOutbound {
			name = "splice-eligible"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newReleaseProbeFixture(t, releaseProbeAddr(port), trackOutbound)
			deviceNext, stackNext := fixture.establish(t, 40001, port)
			fixture.waitTraffic(t, 1, 3*time.Second)
			fixture.emit(t, datapathIPv4TCP(40001, port, deviceNext, stackNext, header.TCPFlagRst|header.TCPFlagAck, nil))
			fixture.waitTraffic(t, 0, 5*time.Second)
			require.Equal(t, int64(1), fixture.handler.closed.Load(), "onClose must run exactly once")
		})
	}
}
