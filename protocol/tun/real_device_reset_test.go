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
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/trafficcontrol"
	"github.com/sagernet/sing-box/log"
	routepkg "github.com/sagernet/sing-box/route"
	singtun "github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common/logger"

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
	handler *releaseProbeHandler
	target  *drainProbeTarget
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
	handler := &releaseProbeHandler{traffic: traffic, conns: connectionManager}
	handler.dialer = releaseProbeDialer{target: target.address()}

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
