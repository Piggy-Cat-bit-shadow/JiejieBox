package e2e

// The Box UDP detour data path: a real IPv4/UDP datagram through the real Go stack and the
// live router of a started chain, out through a SOCKS outbound that itself goes through
// another SOCKS outbound (the detour), to a real UDP echo, and back through the device.
//
// The detour is not inferred from configuration: the ENTRY box (a second real instance) is
// observed carrying the EXIT server's dial, and the control run without `detour` must show
// the entry box seeing nothing. That contrast is what makes "the datagram took the detour"
// a measurement rather than a claim.

import (
	"bytes"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const detourSocksServerConfig = `{
  "log": {"level": "%s"},
  "inbounds": [
    {"type": "socks", "tag": "socks-in", "listen": "127.0.0.1", "listen_port": %d, "udp_timeout": "30s"}
  ],
  "outbounds": [
    {"type": "direct", "tag": "direct"}
  ]
}`

// The device chain: the tun fixture is the inbound; the exit socks outbound optionally
// carries its own connection through the entry socks outbound.
const tunUDPDetourConfig = `{
  "log": {"level": "%s"},
  "outbounds": [
    {"type": "socks", "tag": "entry", "server": "127.0.0.1", "server_port": %d, "version": "5"},
    {"type": "socks", "tag": "exit", "server": "127.0.0.1", "server_port": %d, "version": "5"%s}
  ],
  "route": {
    "rules": [
      {"ip_cidr": ["10.9.9.11/32"], "port": [%d], "action": "route", "outbound": "exit", "override_address": "127.0.0.1"}
    ],
    "final": "entry"
  }
}`

func TestTunUDPDetourChain(t *testing.T) {
	udpEcho := startUDPEchoServer(t, "udp", "127.0.0.1:0")
	udpPort := uint16(udpEcho.conn.LocalAddr().(*net.UDPAddr).Port)
	payload := bytes.Repeat([]byte("d"), 1000)

	exitPort := freePort(t)
	entryPort := freePort(t)
	exitChain := startChain(t, fmt.Sprintf(detourSocksServerConfig, chainLogLevel, exitPort))
	entryChain := startChain(t, fmt.Sprintf(detourSocksServerConfig, chainLogLevel, entryPort))

	entryBefore := entryChain.tracker.count()
	exitAddr := fmt.Sprintf("127.0.0.1:%d", exitPort)
	detour := `, "detour": "entry"`
	if os.Getenv("UDP_DETOUR_INVERT") != "" {
		// Debug seam: pretend the detour is configured while it is not. The with_detour
		// assertions must then fail (the entry hop sees nothing).
		detour = ""
	}
	t.Run("with_detour", func(t *testing.T) {
		running := startChain(t, fmt.Sprintf(tunUDPDetourConfig, chainLogLevel, entryPort, exitPort, detour, udpPort))
		fixture := newTunFixture(t, running, netip.Addr{}, 0)
		fixture.emit(t, udpFrame(netip.MustParseAddr(tunDeviceAddress), 40021, netip.MustParseAddr(tunUDPAddress), udpPort, payload))
		reply := fixture.waitForUDPPayload(t, 40021, udpPort, 10*time.Second)
		require.Equal(t, payload, reply, "the UDP datagram must return through the detour chain")
		// The detour is proven by the entry box carrying the exit server's dial: the flow's
		// destination or its original destination names the exit server.
		flows := entryChain.tracker.waitForFlows(t, entryBefore+1, 10*time.Second)
		matched := false
		for _, flow := range flows[entryBefore:] {
			require.Equal(t, "socks", flow.InboundType)
			if strings.Contains(flow.Destination, exitAddr) || strings.Contains(flow.OriginDestination, exitAddr) {
				matched = true
			}
		}
		require.True(t, matched, "the entry hop was not asked for the exit server %s: %s", exitAddr, formatFlows(flows[entryBefore:]))
	})

	t.Run("without_detour", func(t *testing.T) {
		entryBefore := entryChain.tracker.count() // captured again after the detour run
		running := startChain(t, fmt.Sprintf(tunUDPDetourConfig, chainLogLevel, entryPort, exitPort, "", udpPort))
		fixture := newTunFixture(t, running, netip.Addr{}, 0)
		fixture.emit(t, udpFrame(netip.MustParseAddr(tunDeviceAddress), 40022, netip.MustParseAddr(tunUDPAddress), udpPort, payload))
		reply := fixture.waitForUDPPayload(t, 40022, udpPort, 10*time.Second)
		require.Equal(t, payload, reply, "the control path must still carry the datagram")
		time.Sleep(300 * time.Millisecond)
		require.Equal(t, entryBefore, entryChain.tracker.count(),
			"without detour the entry hop must not be asked for anything")
		_ = exitChain
	})
}
