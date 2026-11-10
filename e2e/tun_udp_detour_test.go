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
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/logger"

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

	t.Run("narrow_mtu_1280", func(t *testing.T) {
		running := startChain(t, fmt.Sprintf(tunUDPDetourConfig, chainLogLevel, entryPort, exitPort, `, "detour": "entry"`, udpPort))
		fixture := newTunFixtureWithMTU(t, running, 1280)
		require.Equal(t, 1280, fixture.device.MTU())
		// A datagram that fits the narrow MTU must round-trip.
		fixture.emit(t, udpFrame(netip.MustParseAddr(tunDeviceAddress), 40031, netip.MustParseAddr(tunUDPAddress), udpPort, payload))
		reply := fixture.waitForUDPPayload(t, 40031, udpPort, 10*time.Second)
		require.Equal(t, payload, reply, "the 1,000-byte datagram must round-trip at MTU 1280")
		// An oversized datagram (2,000 B) cannot fit the device MTU; whether it is dropped or
		// fragmented, the NEXT datagram must still round-trip: no silent wedge.
		oversized := bytes.Repeat([]byte("o"), 2000)
		fixture.emit(t, udpFrame(netip.MustParseAddr(tunDeviceAddress), 40032, netip.MustParseAddr(tunUDPAddress), udpPort, oversized))
		fixture.emit(t, udpFrame(netip.MustParseAddr(tunDeviceAddress), 40033, netip.MustParseAddr(tunUDPAddress), udpPort, payload))
		recovered := fixture.waitForUDPPayload(t, 40033, udpPort, 10*time.Second)
		require.Equal(t, payload, recovered, "the datagram after the oversized one must still round-trip")
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

// newTunFixtureWithMTU is newTunFixture with the device and stack MTU chosen by the test, so
// the narrow-MTU path is exercised by the same real stack and handler.
func newTunFixtureWithMTU(t *testing.T, running *chain, mtu int) *tunFixture {
	t.Helper()
	fixture := &tunFixture{t: t, frameSignal: make(chan struct{}, 1)}
	fixture.handler = &tunStackHandler{
		router: running.router(),
		tag:    "tun-in",
		logger: &testLogger{},
	}
	fixture.device = tun.NewMemoryTun(tun.MemoryTunOptions{
		MTU: mtu,
		Outbound: func(packets []*buf.Buffer) {
			fixture.access.Lock()
			for _, packet := range packets {
				frame := make([]byte, packet.Len())
				copy(frame, packet.Bytes())
				fixture.frames = append(fixture.frames, frame)
			}
			fixture.access.Unlock()
			buf.ReleaseMulti(packets)
			select {
			case fixture.frameSignal <- struct{}{}:
			default:
			}
		},
	})
	stack, err := tun.NewStack("go", tun.StackOptions{
		Context: context.Background(),
		Tun:     fixture.device,
		TunOptions: tun.Options{
			MTU:          uint32(mtu),
			Inet4Address: []netip.Prefix{netip.MustParsePrefix(tunStackAddress)},
			Inet6Address: []netip.Prefix{netip.MustParsePrefix("fd00::1/126")},
			Logger:       logger.NOP(),
		},
		Handler:     fixture.handler,
		Logger:      logger.NOP(),
		UDPTimeout:  30 * time.Second,
		ICMPTimeout: time.Minute,
	})
	require.NoError(t, err)
	require.NoError(t, stack.Start())
	fixture.stack = stack
	t.Cleanup(func() {
		_ = stack.Close()
		_ = fixture.device.Close()
		done := make(chan struct{})
		go func() {
			fixture.handler.connections.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Errorf("a handler delegation was still running 10s after the stack closed\n%s", fixture.handler.logger)
		}
	})
	return fixture
}
