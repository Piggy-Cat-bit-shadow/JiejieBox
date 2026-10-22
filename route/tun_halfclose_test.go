package route

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
	singtun "github.com/sagernet/sing-tun"
	chksum "github.com/sagernet/sing-tun/gtcpip/checksum"
	"github.com/sagernet/sing-tun/gtcpip/header"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// End-to-end proof of the half-close observer on the path the phone actually uses: a TUN stream
// served by the userspace stack, a direct outbound, and the REAL traffic tracker.
//
// The scripted-connection tests in conn_halfclose_test.go prove the decision; these prove the
// MECHANISM against the real stack, which is the part that could quietly not work:
//
//  1. the finished side is a *tun.GoConn, and a keepalive armed on it must actually produce probes;
//  2. an unanswered probe must become a terminal error the stack reports ON A LATER READ - the
//     observer's only channel, since a read that has already returned EOF never blocks again;
//  3. a device that answers the probes must keep its connection, so this cannot kill a live peer.
//
// The device side is a memory TUN, so every frame the stack emits is observable and the test decides
// whether the peer answers. No privilege, device or network namespace is involved.

// halfCloseTestTimingsTUN are short enough for a test and still ordered: idle, then interval, then
// the observer's poll.
var halfCloseTestTimingsTUN = halfCloseOptions{
	keepAliveIdle:     150 * time.Millisecond,
	keepAliveInterval: 150 * time.Millisecond,
	probeInterval:     50 * time.Millisecond,
}

// tunProbeOutbound is the outbound metadata the tracker reads: Tag and Type.
type tunProbeOutbound struct{}

func (tunProbeOutbound) Type() string           { return "probe" }
func (tunProbeOutbound) Tag() string            { return "probe" }
func (tunProbeOutbound) Network() []string      { return []string{N.NetworkTCP} }
func (tunProbeOutbound) Dependencies() []string { return nil }

func (tunProbeOutbound) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return nil, net.ErrClosed
}

func (tunProbeOutbound) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, net.ErrClosed
}

var tunProbeOutboundChain = []adapter.Outbound{tunProbeOutbound{}}

// tunProbeDialer dials the fixed loopback target and registers the outbound socket with the manager
// exactly as common/dialer does, so the flow is a real managed connection.
type tunProbeDialer struct {
	target  string
	tracker *ConnectionManager
}

func (d tunProbeDialer) DialContext(ctx context.Context, _ string, _ M.Socksaddr) (net.Conn, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", d.target)
	if err != nil {
		return nil, err
	}
	if d.tracker == nil {
		// No registration: the flow is an ordinary userspace copy rather than a splice target.
		return conn, nil
	}
	return d.tracker.TrackConn(conn), nil
}

func (d tunProbeDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, net.ErrClosed
}

// tunProbeHandler wires the real tracker and the real copy engine to the stack.
type tunProbeHandler struct {
	traffic *trafficcontrol.Manager
	conns   *ConnectionManager
	// An interface, so a test can swap in an outbound whose connection hides every optional
	// capability - the shape a multiplexed proxy stream has from the route layer's point of view.
	dialer   N.Dialer
	accepted atomic.Int64
	closed   atomic.Int64
}

func (h *tunProbeHandler) JudgeFlow(uint8, netip.AddrPort, netip.AddrPort, []byte) singtun.FlowVerdict {
	return singtun.FlowVerdict{Action: singtun.ActionAccept}
}

func (h *tunProbeHandler) NewDNSPacket([]byte, M.Socksaddr, M.Socksaddr, N.PacketWriter) {}

func (h *tunProbeHandler) NewConnectionEx(ctx context.Context, conn net.Conn, source M.Socksaddr, destination M.Socksaddr, onClose N.CloseHandlerFunc) {
	h.accepted.Add(1)
	go func() {
		metadata := adapter.InboundContext{
			Inbound:       "probe-tun",
			InboundType:   "tun",
			Network:       N.NetworkTCP,
			Source:        source,
			Destination:   destination,
			OutboundChain: tunProbeOutboundChain,
		}
		tracked := h.traffic.RoutedConnection(ctx, conn, metadata, nil, tunProbeOutboundChain[0])
		h.conns.NewConnection(ctx, h.dialer, tracked, metadata, func(err error) {
			h.closed.Add(1)
			if onClose != nil {
				onClose(err)
			}
		})
	}()
}

func (h *tunProbeHandler) NewPacketConnectionEx(_ context.Context, conn N.PacketConn, _ M.Socksaddr, _ M.Socksaddr, _ N.CloseHandlerFunc) {
	conn.Close()
}

type tunHalfCloseFixture struct {
	manager *ConnectionManager
	traffic *trafficcontrol.Manager
	handler *tunProbeHandler
	device  *singtun.MemoryTun
	stack   singtun.Stack
	access  sync.Mutex
	frames  [][]byte
	// framesSeen is how many captured frames the device simulator has already answered.
	framesSeen int
}

func newTUNHalfCloseFixture(t *testing.T, target string) *tunHalfCloseFixture {
	t.Helper()
	return newTUNHalfCloseFixtureWith(t, target, nil, true)
}

// newTUNHalfCloseFixtureTracked is the same fixture with the outbound socket optionally registered
// with the connection manager. Registered means the flow is a splice target (the direct-outbound TUN
// path); unregistered means an ordinary userspace copy.
func newTUNHalfCloseFixtureTracked(t *testing.T, target string, trackOutbound bool) *tunHalfCloseFixture {
	t.Helper()
	return newTUNHalfCloseFixtureWith(t, target, nil, trackOutbound)
}

// newTUNHalfCloseFixtureLogged is the same fixture with the manager's log captured, so a test can
// assert the one line a real-device run would be read from.
func newTUNHalfCloseFixtureLogged(t *testing.T, target string, captured *stallLog) *tunHalfCloseFixture {
	t.Helper()
	return newTUNHalfCloseFixtureWith(t, target, captured, true)
}

func newTUNHalfCloseFixtureWith(t *testing.T, target string, captured *stallLog, trackOutbound bool) *tunHalfCloseFixture {
	t.Helper()
	traffic := trafficcontrol.NewManager()
	managerLogger := log.NewNOPFactory().Logger()
	if captured != nil {
		managerLogger = captured.logger()
	}
	manager := NewConnectionManager(managerLogger)
	manager.halfClose = halfCloseTestTimingsTUN
	fixture := &tunHalfCloseFixture{manager: manager, traffic: traffic}
	handler := &tunProbeHandler{traffic: traffic, conns: manager}
	probeDialer := tunProbeDialer{target: target}
	if trackOutbound {
		probeDialer.tracker = manager
	}
	handler.dialer = probeDialer
	fixture.handler = handler
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
		_ = manager.Close()
	})
	return fixture
}

func (f *tunHalfCloseFixture) emit(t *testing.T, packet []byte) {
	t.Helper()
	written, err := f.device.WritePackets([][]byte{packet})
	require.NoError(t, err)
	require.Equal(t, 1, written)
}

// framesSince returns the frames captured after index, so a simulator can answer only new ones.
func (f *tunHalfCloseFixture) framesSince(index int) [][]byte {
	f.access.Lock()
	defer f.access.Unlock()
	return append([][]byte(nil), f.frames[index:]...)
}

func (f *tunHalfCloseFixture) waitForPayload(t *testing.T, sourcePort, destinationPort uint16, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		f.access.Lock()
		for _, frame := range f.frames {
			tcp, ok := tunProbeTCP(frame)
			if !ok || tcp.SourcePort() != destinationPort || tcp.DestinationPort() != sourcePort {
				continue
			}
			if len(frame) > int(frame[0]&0x0f)<<2+int(tcp.DataOffset()) {
				f.access.Unlock()
				return
			}
		}
		f.access.Unlock()
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("the flow %d -> %d never carried a payload back", destinationPort, sourcePort)
}

func (f *tunHalfCloseFixture) waitForSYNACK(t *testing.T, sourcePort, destinationPort uint16, timeout time.Duration) uint32 {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		f.access.Lock()
		for _, frame := range f.frames {
			tcp, ok := tunProbeTCP(frame)
			if !ok || tcp.SourcePort() != destinationPort || tcp.DestinationPort() != sourcePort {
				continue
			}
			if tcp.Flags()&(header.TCPFlagSyn|header.TCPFlagAck) == header.TCPFlagSyn|header.TCPFlagAck {
				next := tcp.SequenceNumber() + 1
				f.access.Unlock()
				return next
			}
		}
		f.access.Unlock()
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("the stack never answered the SYN for %d -> %d", sourcePort, destinationPort)
	return 0
}

// establish completes the device-side handshake and one echoed request.
func (f *tunHalfCloseFixture) establish(t *testing.T, sourcePort, destinationPort uint16) (deviceNext, stackNext uint32) {
	t.Helper()
	f.emit(t, tunProbeSYN(sourcePort, destinationPort, 1460))
	stackNext = f.waitForSYNACK(t, sourcePort, destinationPort, 3*time.Second)
	deviceNext = 1001
	payload := []byte("ping")
	for attempt := 0; attempt < 60; attempt++ {
		f.emit(t, tunProbeTCPFrame(sourcePort, destinationPort, deviceNext, stackNext, header.TCPFlagAck, nil))
		f.emit(t, tunProbeTCPFrame(sourcePort, destinationPort, deviceNext, stackNext, header.TCPFlagAck|header.TCPFlagPsh, payload))
		f.access.Lock()
		echoed := false
		for _, frame := range f.frames {
			inner, ok := tunProbeTCP(frame)
			if !ok || inner.SourcePort() != destinationPort || inner.DestinationPort() != sourcePort {
				continue
			}
			if len(frame) > int(frame[0]&0x0f)<<2+int(inner.DataOffset()) {
				echoed = true
				break
			}
		}
		f.access.Unlock()
		if echoed {
			return deviceNext + uint32(len(payload)), stackNext + uint32(len(payload))
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the flow %d -> %d never reached the data phase", sourcePort, destinationPort)
	return 0, 0
}

func (f *tunHalfCloseFixture) waitTraffic(t *testing.T, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	last := -1
	for time.Now().Before(deadline) {
		last = f.traffic.ConnectionsLen()
		if last == want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("traffic manager holds %d connections, want %d", last, want)
}

// startHalfCloseTarget is a peer that echoes until the client finishes, and then says nothing and
// stays open - the unresponsive-but-alive server of the reported case.
func startHalfCloseTarget(t *testing.T) uint16 {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	held := make(chan net.Conn, 8)
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			held <- conn
			go func() {
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
	t.Cleanup(func() {
		listener.Close()
		for {
			select {
			case conn := <-held:
				conn.Close()
			default:
				return
			}
		}
	})
	return uint16(listener.Addr().(*net.TCPAddr).Port)
}

func tunProbeAddr(port uint16) string { return "127.0.0.1:" + strconv.Itoa(int(port)) }

func tunProbeTCP(frame []byte) (header.TCP, bool) {
	if len(frame) < header.IPv4MinimumSize+header.TCPMinimumSize {
		return nil, false
	}
	ip := header.IPv4(frame)
	if !ip.IsValid(len(frame)) || ip.TransportProtocol() != header.TCPProtocolNumber {
		return nil, false
	}
	return header.TCP(frame[ip.HeaderLength():]), true
}

func tunProbeTCPFrame(sourcePort, destinationPort uint16, seq, ack uint32, flags header.TCPFlags, payload []byte) []byte {
	source := netip.MustParseAddr("198.18.0.2")
	destination := netip.MustParseAddr("1.1.1.1")
	packet := make([]byte, header.IPv4MinimumSize+header.TCPMinimumSize+len(payload))
	ip := header.IPv4(packet)
	ip.Encode(&header.IPv4Fields{
		TotalLength: uint16(len(packet)),
		TTL:         64,
		Protocol:    uint8(header.TCPProtocolNumber),
		SrcAddr:     source,
		DstAddr:     destination,
	})
	ip.SetChecksum(^ip.CalculateChecksum())
	tcp := header.TCP(packet[header.IPv4MinimumSize:])
	tcp.Encode(&header.TCPFields{
		SrcPort:    sourcePort,
		DstPort:    destinationPort,
		SeqNum:     seq,
		AckNum:     ack,
		DataOffset: header.TCPMinimumSize,
		Flags:      flags,
		WindowSize: 65535,
	})
	copy(packet[header.IPv4MinimumSize+header.TCPMinimumSize:], payload)
	tcp.SetChecksum(^chksum.Checksum(tcp, header.PseudoHeaderChecksum(
		header.TCPProtocolNumber, source.AsSlice(), destination.AsSlice(), uint16(len(packet)-header.IPv4MinimumSize),
	)))
	return packet
}

func tunProbeSYN(sourcePort, destinationPort uint16, mss uint16) []byte {
	source := netip.MustParseAddr("198.18.0.2")
	destination := netip.MustParseAddr("1.1.1.1")
	options := []byte{header.TCPOptionMSS, 4, byte(mss >> 8), byte(mss)}
	packet := make([]byte, header.IPv4MinimumSize+header.TCPMinimumSize+len(options))
	ip := header.IPv4(packet)
	ip.Encode(&header.IPv4Fields{
		TotalLength: uint16(len(packet)),
		TTL:         64,
		Protocol:    uint8(header.TCPProtocolNumber),
		SrcAddr:     source,
		DstAddr:     destination,
	})
	ip.SetChecksum(^ip.CalculateChecksum())
	tcp := header.TCP(packet[header.IPv4MinimumSize:])
	tcp.Encode(&header.TCPFields{
		SrcPort:    sourcePort,
		DstPort:    destinationPort,
		SeqNum:     1000,
		DataOffset: header.TCPMinimumSize + uint8(len(options)),
		Flags:      header.TCPFlagSyn,
		WindowSize: 65535,
	})
	copy(packet[header.IPv4MinimumSize+header.TCPMinimumSize:], options)
	tcp.SetChecksum(^chksum.Checksum(tcp, header.PseudoHeaderChecksum(
		header.TCPProtocolNumber, source.AsSlice(), destination.AsSlice(), uint16(len(packet)-header.IPv4MinimumSize),
	)))
	return packet
}

// TestTUNHalfClosedPeerGoneReleasesFlow is the reported case, end to end: the device finishes its
// write side, the server stays silent, and then the device's socket is gone. The flow must leave the
// traffic table - the table the phone's connection list is built from - without close-all.
func TestTUNHalfClosedPeerGoneReleasesFlow(t *testing.T) {
	port := startHalfCloseTarget(t)
	fixture := newTUNHalfCloseFixture(t, tunProbeAddr(port))
	deviceNext, stackNext := fixture.establish(t, 40010, port)
	fixture.waitTraffic(t, 1, 3*time.Second)

	// The device finishes its write side. The server says nothing, so the connection is now
	// half-closed and legitimately waiting: it must still be listed.
	fixture.emit(t, tunProbeTCPFrame(40010, port, deviceNext, stackNext, header.TCPFlagFin|header.TCPFlagAck, nil))
	time.Sleep(300 * time.Millisecond)
	require.Equal(t, 1, fixture.traffic.ConnectionsLen(),
		"a half-closed flow that is still waiting must stay listed")

	// From here the device answers nothing - the shape of a socket that no longer exists. The
	// stack's keepalive probes go unanswered, the stack reports a terminal error, and the observer
	// releases the flow.
	fixture.waitTraffic(t, 0, 10*time.Second)
	require.Equal(t, int64(1), fixture.handler.closed.Load(), "onClose must run exactly once")
}

// TestTUNHalfClosedLiveDeviceKeepsFlow is the false-positive guard at the stack level: a device whose
// socket is still there answers the probes, so the connection survives - however long the server
// stays silent.
func TestTUNHalfClosedLiveDeviceKeepsFlow(t *testing.T) {
	port := startHalfCloseTarget(t)
	fixture := newTUNHalfCloseFixture(t, tunProbeAddr(port))
	deviceNext, stackNext := fixture.establish(t, 40011, port)
	fixture.waitTraffic(t, 1, 3*time.Second)

	fixture.emit(t, tunProbeTCPFrame(40011, port, deviceNext, stackNext, header.TCPFlagFin|header.TCPFlagAck, nil))
	deviceNext++ // the FIN consumes one sequence number

	// The device keeps acknowledging everything the stack sends, which is what a live socket does
	// and is precisely what a vanished one cannot do.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
			}
			frames := fixture.framesSince(fixture.framesSeen)
			for _, frame := range frames {
				tcp, ok := tunProbeTCP(frame)
				if !ok || tcp.SourcePort() != port || tcp.DestinationPort() != 40011 {
					continue
				}
				if _, err := fixture.device.WritePackets([][]byte{
					tunProbeTCPFrame(40011, port, deviceNext, stackNext, header.TCPFlagAck, nil),
				}); err != nil {
					return
				}
			}
			fixture.access.Lock()
			fixture.framesSeen = len(fixture.frames)
			fixture.access.Unlock()
		}
	}()

	// Ten observation intervals of silence from the server: the flow must stay.
	time.Sleep(1500 * time.Millisecond)
	require.Equal(t, 1, fixture.traffic.ConnectionsLen(),
		"a device that answers the probes must keep its connection")
	require.Equal(t, int64(0), fixture.handler.closed.Load())
}

// TestTUNHalfClosedReleaseIsReportedAtShutdown asserts the number a phone run would report.
//
// The device cannot be instrumented for this investigation, so the only evidence a real-device run
// can produce is the one line the connection manager writes when the tunnel stops. This test drives
// the mechanism through the real stack and then reads exactly that line.
func TestTUNHalfClosedReleaseIsReportedAtShutdown(t *testing.T) {
	port := startHalfCloseTarget(t)
	captured := &stallLog{}
	fixture := newTUNHalfCloseFixtureLogged(t, tunProbeAddr(port), captured)
	deviceNext, stackNext := fixture.establish(t, 40012, port)
	fixture.waitTraffic(t, 1, 3*time.Second)
	fixture.emit(t, tunProbeTCPFrame(40012, port, deviceNext, stackNext, header.TCPFlagFin|header.TCPFlagAck, nil))
	fixture.waitTraffic(t, 0, 10*time.Second)

	snapshot := fixture.manager.HalfCloseDiagnostics()
	require.Equal(t, uint64(1), snapshot.Observed)
	require.Equal(t, uint64(1), snapshot.Released)
	require.Equal(t, uint64(1), snapshot.KeepaliveTimeout,
		"the userspace stack reported its own keepalive timeout, so this is the timeout half")
	require.Equal(t, uint64(0), snapshot.PeerReset)

	require.NoError(t, fixture.manager.Close())
	require.Contains(t, captured.dump(), "half-closed flows: 1 observed",
		"the summary must be emitted when the tunnel stops, with no instrumentation of its own")
	require.Contains(t, captured.dump(), "keepalive timeout 1")
}
