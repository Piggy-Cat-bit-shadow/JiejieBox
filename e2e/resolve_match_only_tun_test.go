package e2e

import (
	"fmt"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// The TUN / FakeIP half of `match_only`, driven through the real Go stack rather than through the
// metadata: a device that has been told a placeholder address connects to it, the route layer
// reverse-maps it to the name, the resolve action answers for MATCHING ONLY, an ip_cidr rule
// selects a proxy, and the proxy must receive the NAME.
//
// Three wrong outcomes are what this test exists to exclude, and each of them would look fine in a
// metadata assertion:
//
//   - the FakeIP placeholder being dialled or handed to the proxy,
//   - the locally resolved address (198.51.100.10) replacing the name on the wire,
//   - the flow being forwarded natively (bypass) with the placeholder as its destination.
const resolveMatchOnlyTunConfig = `{
  "log": {"level": "%s"},
  "dns": {
    "servers": [
      {"tag": "local", "type": "udp", "server": "127.0.0.1", "server_port": %d},
      {"tag": "fakeip", "type": "fakeip", "inet4_range": "198.19.0.0/15"}
    ],
    "rules": [
      {"domain": ["fake-match.test"], "server": "fakeip"}
    ],
    "final": "local",
    "strategy": "ipv4_only",
    "reverse_mapping": true
  },
  "inbounds": [],
  "outbounds": [
    {"type": "socks", "tag": "remote", "server": "127.0.0.1", "server_port": %d, "version": "5"},
    {"type": "direct", "tag": "direct", "domain_resolver": "local"}
  ],
  "route": {
    "rules": [
      {"domain": ["fake-match.test"], "action": "resolve", "match_only": true},
      {"ip_cidr": ["198.51.100.0/24"], "action": "route", "outbound": "remote"}
    ],
    "final": "direct"
  }
}`

func TestResolveMatchOnlyFakeIPFlowKeepsTheMappedDomain(t *testing.T) {
	echo := startEchoServer(t, "tcp", "127.0.0.1:0")
	echoPort := uint16(echo.listener.Addr().(*net.TCPAddr).Port)

	sink := startSocksSink(t, "127.0.0.1:0")
	// The remote node resolves the name itself; the override keeps the far side a real socket while
	// the assertion stays on what arrived.
	sink.backendOverride = map[string]string{
		"fake-match.test:" + fmt.Sprint(echoPort): echo.listener.Addr().String(),
	}
	sinkPort := uint16(sink.listener.Addr().(*net.TCPAddr).Port)

	dnsLocal := startDNSResponder(t, "local", map[string]netip.Addr{
		"fake-match.test": netip.MustParseAddr("198.51.100.10"),
	})

	captured := &capturedPlatformLog{}
	running := startChainWithWriter(t,
		fmt.Sprintf(resolveMatchOnlyTunConfig, "debug", dnsLocal.port(), sinkPort),
		captured)
	fixture := newTunFixture(t, running, netip.MustParseAddr(tunDNSAddress), 53)

	// 1. The device asks for the name and is handed a placeholder from the FakeIP range.
	query := dnsQueryPayload(0x5252, "fake-match.test", 1)
	fixture.emit(t, udpFrame(netip.MustParseAddr(tunDeviceAddress), 41000, netip.MustParseAddr(tunDNSAddress), 53, query))
	reply := fixture.waitForUDPPayload(t, 41000, 53, 10*time.Second)
	fakeAddress := readDNSAddress(t, reply)
	require.True(t, netip.MustParsePrefix("198.19.0.0/15").Contains(fakeAddress),
		"the device must have been handed a placeholder from the configured range, got %s", fakeAddress)

	// 2. The SAME facts through the production pre-match entry, now that the mapping exists: the
	//    placeholder is reverse-mapped, the action offers the answer for matching, the CIDR rule
	//    selects the proxy - and the pre-match verdict must still be Continue, because only the full
	//    match path may dial and it dials the name.
	verdict := running.router().PreMatch(adapter.InboundContext{
		Inbound:               "tun-in",
		InboundType:           C.TypeTun,
		Network:               N.NetworkTCP,
		Source:                M.SocksaddrFrom(netip.MustParseAddr("198.18.0.2"), 42000),
		Destination:           M.SocksaddrFrom(fakeAddress, echoPort),
		ConsumesBypassVerdict: true,
	}, nil)
	require.Equal(t, adapter.PreMatchContinue, verdict.Action,
		"pre-match must not commit a flow or a bypass for a FakeIP flow whose rule matched on "+
			"matching-only evidence; got %v", verdict.Action)

	log := captured
	log.waitFor(t, "=> resolve(match_only)", 5*time.Second)
	log.waitFor(t, "ip_cidr=198.51.100.0/24 => route(remote)", 5*time.Second)
	log.waitFor(t, "for matching only", 5*time.Second)

	// 3. The device connects to the placeholder. The route layer must recover the name, resolve it
	//    for matching, select the rule and keep the name for the dial.
	connection := &tcpOverTun{sourcePort: 41001, destination: fakeAddress, destinationPort: echoPort}
	fixture.dialTCP(t, connection, 10*time.Second)
	connection.write(t, "match-only-fakeip")
	connection.expectEcho(t, "match-only-fakeip", 10*time.Second)

	request := sink.waitForHost(t, "fake-match.test", 10*time.Second)
	require.Equal(t, echoPort, request.Port)
	require.NotEqual(t, fakeAddress.String(), request.Host,
		"the FakeIP placeholder must never reach the proxy")
	require.NotEqual(t, "198.51.100.10", request.Host,
		"and neither must the answer that was resolved for matching only")

	flows := running.tracker.waitForFlows(t, 1, 10*time.Second)
	flow := flows[len(flows)-1]
	require.True(t, flow.FakeIP, "the flow must be recognised as a FakeIP flow")
	require.Equal(t, fakeAddress.String()+":"+fmt.Sprint(echoPort), flow.OriginDestination,
		"the placeholder is preserved as the origin, which is what it is")
	require.Equal(t, "fake-match.test:"+fmt.Sprint(echoPort), flow.Destination,
		"the flow's target is the recovered name, not the placeholder")
	require.Equal(t, "remote", flow.RouteOutbound,
		"the ip_cidr rule must be selected by the matching-only answer")
	require.Contains(t, flow.RouteRule, "ip_cidr=198.51.100.0/24")
	require.Equal(t, []string{"198.51.100.10"}, flow.MatchAddresses)
	require.Empty(t, flow.DestinationAddresses,
		"no dial candidate may be produced by a matching-only action")

	require.Contains(t, dnsLocal.asked(), "fake-match.test/1",
		"the resolve action must have queried the local resolver for the recovered name")
}
