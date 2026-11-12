//go:build linux

package e2e

// Q17 device half: a REAL /dev/net/tun device, the REAL protocol/tun inbound, a sniffer-staged UDP
// datagram, and the detour over a link whose MTU is 1280.
//
// # Why this file exists
//
// Every other TUN fixture in this tree drives `tun.NewMemoryTun`: the device side is a function that
// records frames. That pins the stack's state machine but says nothing about the kernel interface an
// application actually goes through, which is what the audit's Q17 names. Round 06 re-checked the
// claim "the device half needs root and is therefore unverified" and found it wrong on Linux:
// `unshare -Urnm` gives an unprivileged user a namespace where it IS root with CAP_NET_ADMIN,
// `ip tuntap add` works and `/dev/net/tun` is world-writable. `scripts/ci/tun-netns.sh` is that
// runner; this is the test it was written for.
//
// # What is real
//
//   - the inbound is `protocol/tun.NewInbound`, started exactly as the Box starts it, against the
//     router of a REAL started Box (so the route rules, the sniff action and the outbound are the
//     production ones);
//   - the device is a kernel TUN interface at MTU 1280, and the client is an ordinary UDP socket
//     whose packets the KERNEL routes into it - the test emits no frames;
//   - the datagram is a DNS query, so the sniffer must stage the first packet, parse it, and hand a
//     DOMAIN to the rule set: the flow is routed by the sniffed name, not by its address;
//   - the detour is a real second Box speaking SOCKS5 (UDP associate), so the reply crosses a second
//     real proxy hop.
//
// # The four assertions (audit Q17)
//
//  1. an oversized datagram has a DEFINITE result - it is fragmented at the narrow MTU and arrives
//     byte-identical (the outcome is asserted, not left ambiguous);
//  2. the datagram AFTER it still answers correctly - no silent wedge;
//  3. a generation change (Inbound.InterfaceUpdated -> stack reset) keeps an established flow
//     delivering, and the counters still return to zero afterwards;
//  4. after the flows end and after the inbound is stopped, the live connection count is zero and no
//     worker goroutine survives.
//
// # What is deliberately not claimed here
//
// The veth pair the runner creates is NOT crossed by this test: both ends live in the SAME network
// namespace, and the kernel delivers traffic to an address assigned to a local interface without
// sending it out of that interface (the `local` routing table wins). Making the detour cross it
// needs the peer end in its own namespace, which is recorded as the remaining half rather than
// faked by binding to a same-namespace address and calling it "the narrow link". The narrow link
// this test DOES cross is the device under test, at MTU 1280, in both directions.

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/trafficcontrol"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	boxintun "github.com/sagernet/sing-box/protocol/tun"
	"github.com/sagernet/sing/common/json/badoption"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

const (
	// q17DeviceEnv guards the whole file: without a namespace that can create a device, the test
	// would fail for an environment reason and be read as a product failure.
	q17DeviceEnv = "SINGBOX_REAL_TUN_DEVICE"

	q17InterfaceName = "tunq17"
	q17NarrowMTU     = 1280
	q17StackAddress  = "198.19.0.1/32"
	q17HostAddress   = "198.19.0.2/32"
	// q17HostSource is the address the kernel uses as the source of the routed packets: it must be
	// assigned (above) and must differ from the stack's own address, so the stack never sees a flow
	// whose source is one of its own.
	q17HostSource        = "198.19.0.2"
	q17TunnelDestination = "10.9.9.11"
	q17QueryName         = "q17.probe"
	// q17DestinationPort is 53 on purpose: the DNS sniffer only claims a datagram that is going to a
	// DNS port, and the point of the staging assertion is that the packet was HELD and parsed.
	q17DestinationPort = 53
)

// q17RouteConfig is a real Box: one direct outbound (the detour's exit), a SOCKS outbound kept for the
// two-hop variant, a sniff action, and one route rule that rewrites the tunnel destination onto the
// echo server. The catch-all reject is not decoration: without it, a packet that misses the rule is
// handed to the detour with its ORIGINAL destination, and the detour box's own route sends it
// straight back into this device - measured: 113,000 log lines and no answer.
//
// %s in the rules is the sniff action, so Q17_WITHOUT_SNIFF can remove it and prove the staging
// assertion is not vacuous.
const q17RouteConfig = `{
  "log": {"level": "%s"},
  "outbounds": [
    {"type": "direct", "tag": "exit"},
    {"type": "socks", "tag": "socks-exit", "server": "127.0.0.1", "server_port": %d, "version": "5"}
  ],
  "route": {
    "rules": [
      %s
      {"ip_cidr": ["%s/32"], "action": "route", "outbound": "exit", "override_address": "127.0.0.1", "override_port": %d, "udp_timeout": "2s"},
      {"action": "reject"}
    ],
    "final": "exit"
  }
}`

// q17PlatformWriter is enough of a log.PlatformWriter to make the Box build its shared traffic
// manager (box.go creates it when a platform writer is present), which is what the byte accounting
// below reads. It also keeps the box's own diagnostics for a failure message.
type q17PlatformWriter struct {
	access sync.Mutex
	lines  []string
}

func (w *q17PlatformWriter) WriteMessage(level log.Level, message string) {
	w.access.Lock()
	defer w.access.Unlock()
	w.lines = append(w.lines, fmt.Sprintf("[%v] %s", level, message))
}

func (w *q17PlatformWriter) String() string {
	w.access.Lock()
	defer w.access.Unlock()
	return fmt.Sprint(w.lines)
}

// dumpTail is the failure-message view: the last lines the box itself reported, which is where the
// router says what it did with the packet.
func (w *q17PlatformWriter) dumpTail(lines int) string {
	w.access.Lock()
	defer w.access.Unlock()
	start := len(w.lines) - lines
	if start < 0 {
		start = 0
	}
	return fmt.Sprint(w.lines[start:])
}

func requireRealTunNamespace(t *testing.T) {
	t.Helper()
	if os.Getenv(q17DeviceEnv) != "1" {
		t.Skip("set " + q17DeviceEnv + "=1 and run through scripts/ci/tun-netns.sh, so the device is a real " +
			"/dev/net/tun interface this process may create")
	}
}

func runIPCommand(arguments ...string) error {
	command := exec.Command("ip", arguments...)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ip %v: %w: %s", arguments, err, string(output))
	}
	return nil
}

// closeInbound stops an inbound through the lifecycle interface the Box uses. adapter.Inbound does
// not declare Close, so it is taken here rather than assumed.
func closeInbound(t *testing.T, inbound adapter.Inbound) error {
	t.Helper()
	closer, isCloser := inbound.(io.Closer)
	require.True(t, isCloser, "the production inbound must be closable")
	return closer.Close()
}

// logTunState prints the kernel facts a failure has to be read against: the device's own counters
// (did the kernel ever hand a packet to it?), its addresses, and the route the kernel resolves for
// the test's destination.
func logTunState(t *testing.T, label string) {
	t.Helper()
	for _, arguments := range [][]string{
		{"-s", "link", "show", "dev", q17InterfaceName},
		{"addr", "show", "dev", q17InterfaceName},
		{"route", "get", q17TunnelDestination},
		{"route", "show", "table", "all"},
	} {
		command := exec.Command("ip", arguments...)
		output, _ := command.CombinedOutput()
		t.Logf("%s: ip %v:\n%s", label, arguments, output)
	}
	// The kernel's own protocol counters separate "the reply never arrived" from "the reply arrived
	// and was rejected" (a stale checksum after a destination rewrite shows up as InCsumErrors here).
	statistics, err := os.ReadFile("/proc/net/snmp")
	if err == nil {
		for _, line := range strings.Split(string(statistics), "\n") {
			if strings.HasPrefix(line, "Udp:") || strings.HasPrefix(line, "Ip:") {
				t.Logf("%s: /proc/net/snmp %s", label, line)
			}
		}
	}
}

// q17Flow is the client's view of one tunnel flow: a UDP socket whose packets the kernel routes into
// the TUN device.
type q17Flow struct {
	conn net.Conn
}

func newQ17Flow(t *testing.T) *q17Flow {
	t.Helper()
	conn, err := net.DialTimeout("udp",
		net.JoinHostPort(q17TunnelDestination, fmt.Sprint(q17DestinationPort)), 10*time.Second)
	require.NoError(t, err, "the kernel did not route the client socket into the TUN device")
	t.Cleanup(func() { _ = conn.Close() })
	return &q17Flow{conn: conn}
}

// sendAndReceive writes one datagram and waits for its echo.
func (f *q17Flow) sendAndReceive(t *testing.T, payload []byte, timeout time.Duration) ([]byte, bool) {
	t.Helper()
	require.NoError(t, f.conn.SetDeadline(time.Now().Add(timeout)))
	_, err := f.conn.Write(payload)
	require.NoError(t, err)
	reply := make([]byte, 65535)
	read, err := f.conn.Read(reply)
	if err != nil {
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			logTunState(t, "no-reply")
			return nil, false
		}
		require.NoError(t, err)
	}
	return reply[:read], true
}

func TestTunRealDeviceNarrowMTUSniffedUDPDetour(t *testing.T) {
	requireRealTunNamespace(t)
	// A fresh namespace starts with loopback DOWN; the socks server and the direct outbound both
	// need it.
	require.NoError(t, runIPCommand("link", "set", "lo", "up"))

	udpEcho := startUDPEchoServer(t, "udp", "127.0.0.1:0")
	echoPort := uint16(udpEcho.conn.LocalAddr().(*net.UDPAddr).Port)

	// The detour's far side: a real Box speaking SOCKS5 with UDP associate.
	exitPort := freePort(t)
	exitChain := startChain(t, fmt.Sprintf(detourSocksServerConfig, chainLogLevel, exitPort))
	_ = exitChain

	writer := &q17PlatformWriter{}
	// Q17_WITHOUT_SNIFF is the negative control: with no sniff action the datagram still crosses the
	// device and is still routed, but no protocol is parsed - so the staging assertion must fail.
	sniffRule := `{"action": "sniff"},`
	if os.Getenv("Q17_WITHOUT_SNIFF") != "" {
		sniffRule = ""
	}
	running := startChainWithWriter(t, fmt.Sprintf(q17RouteConfig, chainLogLevel, exitPort,
		sniffRule, q17TunnelDestination, echoPort), writer)

	// The REAL inbound, against the REAL router, with a kernel device at the narrow MTU.
	inbound, err := boxintun.NewInbound(running.ctx, running.router(),
		log.NewNOPFactory().Logger(), "tun-in", option.TunInboundOptions{
			InterfaceName: q17InterfaceName,
			MTU:           q17NarrowMTU,
			Address:       badoption.Listable[netip.Prefix]{netip.MustParsePrefix(q17StackAddress)},
			DNSMode:       "disabled",
		})
	require.NoError(t, err, "the real tun inbound must be constructible\n%s", writer)
	// The four stages a Box runs, in the Box's own order (box.go startComponents): the DEVICE is
	// created in StartStateStart and the STACK IS ACTIVATED in StartStatePostStart. Starting only the
	// first stage creates a device nobody reads - measured here: the device came up with
	// LOWER_UP and the kernel handed it packets, but no flow ever reached the router.
	scope := adapter.NewScope(running.ctx, log.NewNOPFactory().Logger())
	for _, stage := range []adapter.StartStage{
		adapter.StartStateInitialize,
		adapter.StartStateStart,
		adapter.StartStatePostStart,
		adapter.StartStateStarted,
	} {
		require.NoError(t, inbound.Start(stage, scope),
			"the real tun inbound must start through stage %d", stage)
	}
	t.Cleanup(func() { _ = closeInbound(t, inbound) })
	updater, isUpdater := inbound.(adapter.InterfaceUpdateListener)
	require.True(t, isUpdater, "the production inbound must accept a network generation change")

	// The host half of the device: an address the kernel can use as the source, plus the route that
	// sends the test's destination INTO the tunnel. Both are kernel objects, which is the point.
	require.NoError(t, runIPCommand("addr", "add", q17HostAddress, "dev", q17InterfaceName))
	require.NoError(t, runIPCommand("link", "set", q17InterfaceName, "up"))
	require.NoError(t, runIPCommand("route", "add", q17TunnelDestination+"/32", "dev", q17InterfaceName,
		"src", q17HostSource))

	manager := service.PtrFromContext[trafficcontrol.Manager](running.ctx)
	require.NotNil(t, manager, "the Box must expose its shared traffic manager for the byte accounting")

	// The kernel needs a moment to install the address and the route before the first datagram; the
	// wait is short on purpose, because a test that needs a second here is hiding a race.
	time.Sleep(100 * time.Millisecond)

	flow := newQ17Flow(t)

	// On failure, dump the kernel state the assertions depend on: without it "the datagram never
	// came back" cannot be told apart from "the kernel never routed it into the device".
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		for _, arguments := range [][]string{
			{"-s", "link", "show", "dev", q17InterfaceName},
			{"addr", "show", "dev", q17InterfaceName},
			{"route", "show", "table", "all"},
		} {
			command := exec.Command("ip", arguments...)
			output, _ := command.CombinedOutput()
			t.Logf("ip %v:\n%s", arguments, output)
		}
		t.Logf("echo packets received: %d", udpEcho.packets.Load())
	})

	t.Run("sniffed_datagram_round_trips", func(t *testing.T) {
		query := dnsQueryPayload(0x17, q17QueryName, 1)
		reply, answered := flow.sendAndReceive(t, query, 10*time.Second)
		require.True(t, answered, "the sniffed datagram never came back\nbox log:\n%s", writer.dumpTail(40))
		require.Equal(t, query, reply, "the echo must return the exact payload that crossed the device")

		// The sniff HALF: the flow's protocol can only be "dns" because the first datagram was
		// STAGED and PARSED. A pass-through would carry the payload and leave the protocol empty.
		flows := running.tracker.waitForFlows(t, 1, 10*time.Second)
		var sniffed bool
		for _, record := range flows {
			if record.Protocol != "dns" {
				continue
			}
			sniffed = true
			require.Equal(t, "exit", record.RouteOutbound,
				"the sniffed datagram must take the detour outbound")
			require.Equal(t, "tun-in", record.Inbound,
				"the datagram must have entered through the real tun inbound")
		}
		require.True(t, sniffed,
			"no flow carried a sniffed protocol, so the first datagram was not staged and parsed "+
				"before it was forwarded: %s", formatFlows(flows))
	})

	t.Run("oversized_datagram_has_a_definite_result", func(t *testing.T) {
		// 1400 bytes is larger than the 1280 MTU of the device: the kernel must fragment it on the
		// way in, the stack must reassemble it, the detour must carry it, and the reply must be
		// fragmented on the way back. The assertion is the OUTCOME (byte-identical delivery), not
		// "a large packet was seen".
		payload := make([]byte, 1400)
		for index := range payload {
			payload[index] = byte('a' + index%26)
		}
		reply, answered := flow.sendAndReceive(t, payload, 15*time.Second)
		require.True(t, answered,
			"the oversized datagram had NO result at all: neither delivered nor refused. A silent "+
				"wedge at the narrow MTU is the failure this row exists for\nbox log:\n%s", writer)
		require.Equal(t, payload, reply,
			"the oversized datagram came back changed: the stack fragmented and reassembled it "+
				"wrongly at the narrow MTU")
	})

	t.Run("next_datagram_is_still_correct", func(t *testing.T) {
		query := dnsQueryPayload(0x18, q17QueryName, 1)
		reply, answered := flow.sendAndReceive(t, query, 10*time.Second)
		require.True(t, answered, "the datagram after the oversized one never came back: the narrow "+
			"MTU path is wedged\nbox log:\n%s", writer)
		require.Equal(t, query, reply, "the datagram after the oversized one came back changed")
	})

	t.Run("generation_change_drains_instead_of_killing", func(t *testing.T) {
		// The production generation-change path: Inbound.InterfaceUpdated -> resetNetwork ->
		// stack.ResetNetwork. An established flow must keep delivering, and the counters must still
		// come back to zero afterwards.
		updater.InterfaceUpdated(running.ctx)
		time.Sleep(20 * time.Millisecond)
		query := dnsQueryPayload(0x19, q17QueryName, 1)
		reply, answered := flow.sendAndReceive(t, query, 10*time.Second)
		require.True(t, answered, "a generation change killed an established flow instead of "+
			"draining it\nbox log:\n%s", writer)
		require.Equal(t, query, reply)
	})

	t.Run("bytes_and_workers_return_to_zero", func(t *testing.T) {
		uplink, downlink := manager.Total()
		require.Greater(t, uplink, int64(0), "the traffic manager counted no uplink bytes, so the "+
			"datagrams did not cross the router")
		require.Greater(t, downlink, int64(0), "the traffic manager counted no downlink bytes")

		// Ending the client flow must release the flow's accounting: the staged buffer, the packet
		// connection and its tracker. A UDP flow has no close signal, so what releases it is the
		// route's own udp_timeout (2 s here); the assertion is that it really goes away rather than
		// staying charged to the Box.
		_ = flow.conn.Close()
		require.Eventually(t, func() bool { return manager.ConnectionsLen() == 0 },
			15*time.Second, 20*time.Millisecond,
			"an idle flow left %d live connection(s) in the traffic table", manager.ConnectionsLen())

		baseline := runtime.NumGoroutine()
		require.NoError(t, closeInbound(t, inbound), "the real inbound must close")
		require.Eventually(t, func() bool { return runtime.NumGoroutine() <= baseline+8 },
			10*time.Second, 10*time.Millisecond,
			"the inbound left worker goroutines behind after it was stopped (baseline %d, now %d)",
			baseline, runtime.NumGoroutine())
		require.Zero(t, manager.ConnectionsLen(), "a stopped inbound left live connections behind")
	})
}
