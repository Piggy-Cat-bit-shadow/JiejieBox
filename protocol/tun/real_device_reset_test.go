//go:build linux

package tun

// Real /dev/net/tun acceptance: the same reset contract as reset_drain_bytes_test.go, but the device
// is a KERNEL TUN interface and the traffic is real sockets instead of hand-emitted frames.
//
// # Why this file exists
//
// Every other TUN test in this tree drives a MemoryTun: the device side is a function that records
// frames. That pins the stack's state machine but says nothing about the kernel interface an
// application actually goes through. The closeout review asked for the real device ("利用 WSL
// /dev/net/tun ... 从真实入口"), and the probe showed the device IS creatable in `unshare -Urn`
// (TUNSETIFF=OK), so the gap was a fixture, not a capability.
//
// # Topology
//
//	real TCP client (this test) -> kernel route 1.1.1.1/32 dev tunprobe0
//	  -> /dev/net/tun -> production sing-tun device -> production go stack
//	  -> handler (the route path in miniature: real trafficcontrol + real ConnectionManager)
//	  -> loopback echo
//
// The stack is the same `singtun.NewStack("go", ...)` the inbound creates, and the reset is
// `stack.ResetNetwork()`, which is exactly what `Inbound.InterfaceUpdated` invokes.
//
// # What is asserted
//
//   - a flow established BEFORE the reset keeps delivering bytes after it (drain, not kill);
//   - a flow established AFTER the reset works;
//   - once the peer path is confirmed dead, the flow is released within a bound and the close path
//     runs once;
//   - a silent-but-healthy path (the negative control) is NOT released: the reset does not treat
//     "no recent bytes" as "dead".
//
// # What is deliberately not here
//
// UDP through a real device: the probe handler in this package only implements the TCP path
// (`NewPacketConnectionEx` closes), so a UDP assertion would be measuring the fixture, not the
// stack. It is recorded as the remaining gap rather than faked.

import (
	"context"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/trafficcontrol"
	"github.com/sagernet/sing-box/log"
	routepkg "github.com/sagernet/sing-box/route"
	singtun "github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

const (
	realTunDeviceEnv  = "SINGBOX_REAL_TUN_DEVICE"
	realTunDeviceName = "tunprobe0"
	// The stack owns .1; the host side of the conversation is .2, which the kernel picks as the
	// source for the route below. Keeping them different means the stack never sees a flow whose
	// source is one of its own addresses.
	realTunStackAddress    = "198.19.0.1/32"
	realTunHostAddress     = "198.19.0.2/32"
	realTunDestination     = "1.1.1.1"
	realTunDestinationPort = 80
)

type realTunFixture struct {
	device  singtun.Tun
	stack   singtun.Stack
	handler *realTunHandler
	target  *drainProbeTarget
}

// realTunHandler is the release probe plus the UDP half. The shared probe closes packet
// connections (its other callers assert TCP only), so the UDP path is added here, where a real
// device now delivers datagrams: every tunnel flow is forwarded to one loopback UDP echo and the
// reply is written back on the same flow.
//
// The write destination is the one ReadPacket returned, NOT the flow's client source: the UDP NAT
// conn's writer builds the reply with the remote as source and its template's client address as
// destination, and its allowPeer filter is keyed on that remote. Passing the client source makes
// the reply disappear without an error - measured before this comment existed.
type realTunHandler struct {
	*releaseProbeHandler
	udpTarget *net.UDPAddr
	packets   atomic.Int32
}

func (h *realTunHandler) NewPacketConnectionEx(_ context.Context, conn N.PacketConn, source M.Socksaddr, _ M.Socksaddr, onClose N.CloseHandlerFunc) {
	h.packets.Add(1)
	go func() {
		defer conn.Close()
		if onClose != nil {
			defer onClose(nil)
		}
		peer, err := net.DialUDP("udp", nil, h.udpTarget)
		if err != nil {
			return
		}
		defer peer.Close()
		var remote M.Socksaddr
		var remoteAccess sync.Mutex
		go func() {
			for {
				buffer := buf.NewSize(2048)
				destination, readErr := conn.ReadPacket(buffer)
				if readErr != nil {
					buffer.Release()
					return
				}
				remoteAccess.Lock()
				remote = destination
				remoteAccess.Unlock()
				_, _ = peer.Write(buffer.Bytes())
				buffer.Release()
			}
		}()
		reply := make([]byte, 2048)
		for {
			_ = peer.SetReadDeadline(time.Now().Add(30 * time.Second))
			read, readErr := peer.Read(reply)
			if read > 0 {
				remoteAccess.Lock()
				destination := remote
				remoteAccess.Unlock()
				if !destination.IsValid() {
					continue
				}
				headroom := N.CalculateFrontHeadroom(conn)
				if headroom < 48 {
					headroom = 48
				}
				response := buf.NewSize(headroom + read)
				response.Resize(headroom, 0)
				_, _ = response.Write(reply[:read])
				// WritePacket takes ownership of the buffer.
				if writeErr := conn.WritePacket(response, destination); writeErr != nil {
					response.Release()
					return
				}
			}
			if readErr != nil {
				return
			}
		}
	}()
}

func startUDPEcho(t *testing.T) *net.UDPAddr {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	go func() {
		buffer := make([]byte, 2048)
		for {
			read, source, readErr := conn.ReadFromUDP(buffer)
			if readErr != nil {
				return
			}
			_, _ = conn.WriteToUDP(buffer[:read], source)
		}
	}()
	return conn.LocalAddr().(*net.UDPAddr)
}

func requireRealTunDevice(t *testing.T) {
	t.Helper()
	if os.Getenv(realTunDeviceEnv) != "1" {
		t.Skip("set " + realTunDeviceEnv + "=1 and run under `unshare -Urn` to create a real /dev/net/tun device")
	}
}

func newRealTunFixture(t *testing.T) *realTunFixture {
	t.Helper()
	requireRealTunDevice(t)
	// A fresh network namespace starts with loopback DOWN; the production path needs it for the
	// loopback echo the handler dials.
	require.NoError(t, runIP("link", "set", "lo", "up"))
	target := startDrainProbeTarget(t)

	traffic := trafficcontrol.NewManager()
	connectionManager := routepkg.NewConnectionManager(log.NewNOPFactory().Logger())
	handler := &realTunHandler{
		releaseProbeHandler: &releaseProbeHandler{traffic: traffic, conns: connectionManager},
		udpTarget:           startUDPEcho(t),
	}
	handler.releaseProbeHandler.dialer = releaseProbeDialer{target: target.address()}

	options := singtun.Options{
		Name:         realTunDeviceName,
		MTU:          1500,
		Inet4Address: []netip.Prefix{netip.MustParsePrefix(realTunStackAddress)},
		Logger:       logger.NOP(),
	}
	device, err := singtun.New(options)
	require.NoError(t, err, "a real /dev/net/tun device must be creatable in this namespace")

	// The host side of the conversation: its own address on the device plus the route that sends
	// the test's destination into the tunnel. Both are kernel objects, which is the point.
	require.NoError(t, runIP("addr", "add", realTunHostAddress, "dev", realTunDeviceName))
	require.NoError(t, runIP("link", "set", realTunDeviceName, "up"))
	require.NoError(t, runIP("route", "add", realTunDestination+"/32", "dev", realTunDeviceName, "src", "198.19.0.2"))

	stack, err := singtun.NewStack("go", singtun.StackOptions{
		Context: context.Background(),
		Tun:     device,
		TunOptions: singtun.Options{
			MTU:          1500,
			Inet4Address: []netip.Prefix{netip.MustParsePrefix(realTunStackAddress)},
			Logger:       logger.NOP(),
		},
		Handler:     handler,
		Logger:      logger.NOP(),
		UDPTimeout:  time.Minute,
		ICMPTimeout: time.Minute,
	})
	require.NoError(t, err)
	require.NoError(t, stack.Start())

	fixture := &realTunFixture{device: device, stack: stack, handler: handler, target: target}
	t.Cleanup(func() {
		_ = stack.Close()
		_ = device.Close()
		_ = connectionManager.Close()
	})
	return fixture
}

func runIP(arguments ...string) error {
	command := exec.Command("ip", arguments...)
	output, err := command.CombinedOutput()
	if err != nil {
		return &ipError{arguments: arguments, output: string(output), err: err}
	}
	return nil
}

type ipError struct {
	arguments []string
	output    string
	err       error
}

func (e *ipError) Error() string { return e.err.Error() + ": " + e.output }

// dialRealTun opens a real TCP connection whose packets go through the kernel TUN interface.
func dialRealTun(t *testing.T) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(realTunDestination, strconv.Itoa(realTunDestinationPort)), 10*time.Second)
	require.NoError(t, err, "the kernel did not deliver the connection into the TUN device")
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func echoRealTun(t *testing.T, conn net.Conn, payload string) {
	t.Helper()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	_, err := conn.Write([]byte(payload))
	require.NoError(t, err)
	echo := make([]byte, len(payload))
	_, err = io.ReadFull(conn, echo)
	require.NoError(t, err)
	require.Equal(t, payload, string(echo))
}

func TestRealTunDeviceKeepsAnEstablishedFlowAcrossAReset(t *testing.T) {
	fixture := newRealTunFixture(t)

	before := dialRealTun(t)
	echoRealTun(t, before, "before-the-reset")
	require.Eventually(t, func() bool { return fixture.handler.traffic.ConnectionsLen() == 1 },
		5*time.Second, 5*time.Millisecond, "the real flow never entered the traffic table")

	// The production reset path: Inbound.InterfaceUpdated -> resetNetwork -> stack.ResetNetwork.
	fixture.stack.ResetNetwork()
	time.Sleep(20 * time.Millisecond)

	echoRealTun(t, before, "after-the-reset")
	require.EqualValues(t, 1, fixture.handler.traffic.ConnectionsLen(),
		"the reset released a healthy flow: it must drain, not kill")

	after := dialRealTun(t)
	echoRealTun(t, after, "new-flow-after-the-reset")
	require.Eventually(t, func() bool { return fixture.handler.traffic.ConnectionsLen() == 2 },
		5*time.Second, 5*time.Millisecond, "the flow created after the reset never entered the traffic table")

	// The negative control: a silent but healthy flow is not released. A reset that treated silence
	// as death would fail here, and that is exactly the误关 the stall decision is about.
	time.Sleep(500 * time.Millisecond)
	require.EqualValues(t, 2, fixture.handler.traffic.ConnectionsLen(),
		"a silent healthy flow was released: silence is not the dead-path判据")
}

func TestRealTunDeviceReleasesAFlowWhosePeerIsGone(t *testing.T) {
	fixture := newRealTunFixture(t)

	conn := dialRealTun(t)
	echoRealTun(t, conn, "established")
	require.Eventually(t, func() bool { return fixture.handler.traffic.ConnectionsLen() == 1 },
		5*time.Second, 5*time.Millisecond)

	// The peer path is confirmed dead: the loopback echo abort its socket, so the flow's outbound
	// side reports the failure. The release must land inside the same bound the MemoryTun release
	// tests use, and the close path must run once.
	fixture.target.killEstablished()
	closedAt := time.Now()
	require.Eventually(t, func() bool { return fixture.handler.traffic.ConnectionsLen() == 0 },
		10*time.Second, 5*time.Millisecond,
		"the flow whose peer is gone was not released: the real device path has no bounded reclaim")
	require.Eventually(t, func() bool { return fixture.handler.closed.Load() >= 1 },
		5*time.Second, 5*time.Millisecond, "the close path never ran for the failed flow")
	require.EqualValues(t, 1, fixture.handler.closed.Load(), "the close path must run exactly once")
	t.Logf("MEASURED real-device dead-flow release: %s", time.Since(closedAt).Round(time.Millisecond))

	// The stack still serves a new flow after the failed one was released.
	revived := dialRealTun(t)
	echoRealTun(t, revived, "new-flow-after-the-release")
	require.Eventually(t, func() bool { return fixture.handler.traffic.ConnectionsLen() == 1 },
		5*time.Second, 5*time.Millisecond)
}

// TestRealTunDeviceCarriesUDPAcrossAReset is the UDP half of the matrix row: real datagrams through
// the kernel device, across a reset, with a new flow afterwards.
func TestRealTunDeviceCarriesUDPAcrossAReset(t *testing.T) {
	fixture := newRealTunFixture(t)
	target := fixture.handler.udpTarget
	destination := &net.UDPAddr{IP: net.ParseIP(realTunDestination), Port: target.Port}

	echo := func(conn *net.UDPConn, payload string) {
		t.Helper()
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		_, err := conn.Write([]byte(payload))
		require.NoError(t, err)
		reply := make([]byte, len(payload))
		_, err = io.ReadFull(conn, reply)
		require.NoError(t, err, "no UDP datagram crossed the real TUN device")
		require.Equal(t, payload, string(reply))
	}

	first, err := net.DialUDP("udp", nil, destination)
	require.NoError(t, err)
	t.Cleanup(func() { _ = first.Close() })
	echo(first, "udp-before-the-reset")
	require.Eventually(t, func() bool { return fixture.handler.packets.Load() >= 1 },
		5*time.Second, 5*time.Millisecond, "the UDP flow never reached the handler")

	fixture.stack.ResetNetwork()
	time.Sleep(20 * time.Millisecond)

	// The same 4-tuple may keep its mapping across the reset; either way the bytes must still flow.
	echo(first, "udp-after-the-reset")

	// A new source port is a new flow, and it must work after the reset too.
	second, err := net.DialUDP("udp", nil, destination)
	require.NoError(t, err)
	t.Cleanup(func() { _ = second.Close() })
	echo(second, "udp-new-flow")
	require.Eventually(t, func() bool { return fixture.handler.packets.Load() >= 2 },
		5*time.Second, 5*time.Millisecond, "the UDP flow created after the reset never reached the handler")
}
