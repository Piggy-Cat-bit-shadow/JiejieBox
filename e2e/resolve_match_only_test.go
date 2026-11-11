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

// The end-to-end contract of `resolve` with `match_only: true`, measured on the wire.
//
// One rule set, three facts to separate:
//
//	the resolution RUNS                  the mock DNS server is asked for the name
//	the resolution DECIDES the rule      ip_cidr matches the documented answer and routes to remote
//	the resolution is NOT the dial        the SOCKS request carries the NAME, not the answer
//
// The last one is the only one that matters in production and the only one a metadata assertion
// cannot prove: `metadata.Destination` can still say "named.test" while the candidate planner hands
// an address to the proxy. These tests read the SOCKS request the proxy actually received - the
// address TYPE on the wire - which is the boundary the product cannot fake.
const resolveMatchOnlyConfig = `{
  "log": {"level": "%s"},
  "dns": {
    "servers": [{"tag": "dns-local", "type": "udp", "server": "127.0.0.1", "server_port": %d}],
    "final": "dns-local",
    "strategy": "ipv4_only"
  },
  "inbounds": [
    {"type": "mixed", "tag": "mixed-in", "listen": "127.0.0.1", "listen_port": %d}
  ],
  "outbounds": [
    {"type": "socks", "tag": "remote", "server": "127.0.0.1", "server_port": %d, "version": "5"},
    {"type": "direct", "tag": "direct", "domain_resolver": "dns-local"}
  ],
  "route": {
    "rules": [
      {"action": "resolve", "match_only": %t},
      {"ip_cidr": ["198.51.100.0/24"], "action": "route", "outbound": "remote"}
    ],
    "final": "direct"
  }
}`

// TestResolveMatchOnlyKeepsTheNameOnTheWire is M1/M2/B1 in one chain.
func TestResolveMatchOnlyKeepsTheNameOnTheWire(t *testing.T) {
	echo := startEchoServer(t, "tcp", "127.0.0.1:0")
	echoPort := uint16(echo.listener.Addr().(*net.TCPAddr).Port)

	sink := startSocksSink(t, "127.0.0.1:0")
	// The sink stands in for a remote node, which resolves names itself. The override keeps the far
	// side real (a real socket, real bytes) while the assertion stays on what ARRIVED.
	sink.backendOverride = map[string]string{
		"named.test:" + fmt.Sprint(echoPort): echo.listener.Addr().String(),
	}
	sinkPort := uint16(sink.listener.Addr().(*net.TCPAddr).Port)

	dnsLocal := startDNSResponder(t, "dns-local", map[string]netip.Addr{
		// In the rule's CIDR: the address must select the proxy.
		"named.test": netip.MustParseAddr("198.51.100.10"),
		// Outside it, and loopback so the fallback direct outbound can really connect.
		"plain.test": netip.MustParseAddr("127.0.0.1"),
		// Also inside the CIDR: the pre-match case below needs an ANSWER, not a lookup failure, or
		// the verdict would be a rejection for the wrong reason.
		"prematch.test": netip.MustParseAddr("198.51.100.20"),
	})

	proxyPort := freePort(t)
	running := startChain(t, fmt.Sprintf(resolveMatchOnlyConfig, chainLogLevel, dnsLocal.port(), proxyPort, sinkPort, true))
	proxyAddress := fmt.Sprintf("127.0.0.1:%d", proxyPort)

	// M1: the matching answer selects the rule, the proxy receives the name.
	t.Run("matching answer selects the proxy and the name reaches it", func(t *testing.T) {
		conn := dialSocks5(t, proxyAddress, 0x03, "named.test", echoPort)
		defer conn.Close()
		requireEcho(t, conn, "match-only-name")

		requests := sink.waitForRequests(t, 1, 10*time.Second)
		require.Equal(t, byte(0x03), requests[0].AddressType,
			"the proxy must receive a DOMAIN request; an IPv4 request here means the matching address "+
				"became the dial target, which is exactly what match_only must prevent: %s", requests[0])
		require.Equal(t, "named.test", requests[0].Host)
		require.Equal(t, echoPort, requests[0].Port)

		flows := running.tracker.waitForFlows(t, 1, 10*time.Second)
		flow := flows[len(flows)-1]
		require.Equal(t, "remote", flow.RouteOutbound,
			"the rule must have selected the proxy, not the fallback: the decision must be made BY the answer")
		require.Contains(t, flow.RouteRule, "ip_cidr=198.51.100.0/24",
			"the rule recorded for this flow must be the CIDR one; got %q", flow.RouteRule)
		require.Equal(t, []string{"198.51.100.10"}, flow.MatchAddresses,
			"the answer must be published as matching evidence")
		require.Empty(t, flow.DestinationAddresses,
			"and must NOT be a dial candidate; a non-empty list here is what made the wire form an IP")

		require.Contains(t, dnsLocal.asked(), "named.test/1",
			"the resolution must actually have been performed; a rule matching without a query is a bug")
	})

	// M2: no match, so the fallback rule decides and the name is dialled by the direct outbound.
	t.Run("non matching answer falls through without touching the proxy", func(t *testing.T) {
		sinkBefore := len(sink.seen())
		conn := dialSocks5(t, proxyAddress, 0x03, "plain.test", echoPort)
		defer conn.Close()
		requireEcho(t, conn, "no-match-name")

		flows := running.tracker.waitForFlows(t, 2, 10*time.Second)
		flow := flows[len(flows)-1]
		require.Equal(t, "direct", flow.RouteOutbound,
			"an answer outside the CIDR must not select the proxy")
		require.Empty(t, flow.DestinationAddresses,
			"still no dial candidate: match_only never produces one")
		require.Equal(t, sinkBefore, len(sink.seen()),
			"nothing may reach the proxy on the fallback path")
		require.Contains(t, dnsLocal.asked(), "plain.test/1")
	})

	// M10 boundary: a named flow must not be handed to the platform, and the pre-match pass must
	// not commit a userspace flow on match-only evidence either. This drives the production
	// PreMatch entry with ConsumesBypassVerdict - the shape the nfqueue entry presents - because a
	// bypass there would dial the literal destination instead of the name.
	t.Run("pre-match does not bypass a named destination", func(t *testing.T) {
		result := running.router().PreMatch(adapter.InboundContext{
			Inbound:               "tun-in",
			InboundType:           C.TypeTun,
			Network:               N.NetworkTCP,
			Source:                M.SocksaddrFrom(netip.MustParseAddr("198.18.0.2"), 40000),
			Destination:           M.Socksaddr{Fqdn: "prematch.test", Port: echoPort},
			FakeIP:                true,
			ConsumesBypassVerdict: true,
		}, nil)
		require.NotEqual(t, adapter.PreMatchBypass, result.Action,
			"a named destination must never be handed to the OS: the platform would resolve it with "+
				"its own resolver and the configured rule would be skipped")
		require.NotEqual(t, adapter.PreMatchFlow, result.Action,
			"and match-only evidence is not enough to commit a userspace flow")
		require.Equal(t, adapter.PreMatchContinue, result.Action,
			"the pre-match pass must fall through to the full match path with the name intact")
		require.Contains(t, dnsLocal.asked(), "prematch.test/1",
			"the pre-match walk must have executed the resolve action")
	})
}

// TestResolveWithoutMatchOnlyStillDialsCandidates is the regression control: the SAME configuration
// and the SAME DNS answer, without the option, must keep the historical behaviour - the proxy
// receives the resolved address, because that is what a resolve action publishes by default.
//
// The pair of tests is what makes the first one meaningful: the difference on the wire is the
// option, not the fixture.
func TestResolveWithoutMatchOnlyStillDialsCandidates(t *testing.T) {
	echo := startEchoServer(t, "tcp", "127.0.0.1:0")
	echoPort := uint16(echo.listener.Addr().(*net.TCPAddr).Port)

	sink := startSocksSink(t, "127.0.0.1:0")
	// The candidate form must reach the proxy, so this is the address the sink has to be able to
	// reach for the flow to complete.
	sink.backendOverride = map[string]string{
		"198.51.100.10:" + fmt.Sprint(echoPort): echo.listener.Addr().String(),
	}
	sinkPort := uint16(sink.listener.Addr().(*net.TCPAddr).Port)

	dnsLocal := startDNSResponder(t, "dns-local", map[string]netip.Addr{
		"named.test": netip.MustParseAddr("198.51.100.10"),
	})

	proxyPort := freePort(t)
	running := startChain(t, fmt.Sprintf(resolveMatchOnlyConfig, chainLogLevel, dnsLocal.port(), proxyPort, sinkPort, false))
	proxyAddress := fmt.Sprintf("127.0.0.1:%d", proxyPort)

	conn := dialSocks5(t, proxyAddress, 0x03, "named.test", echoPort)
	defer conn.Close()
	requireEcho(t, conn, "candidate-name")

	requests := sink.waitForRequests(t, 1, 10*time.Second)
	require.Equal(t, byte(0x01), requests[0].AddressType,
		"without match_only the address IS the dial target, and that is the behaviour this option "+
			"must not change: %s", requests[0])
	require.Equal(t, "198.51.100.10", requests[0].Host)

	flows := running.tracker.waitForFlows(t, 1, 10*time.Second)
	flow := flows[len(flows)-1]
	require.Equal(t, "remote", flow.RouteOutbound, "the same rule must still decide the flow")
	require.Contains(t, flow.RouteRule, "ip_cidr=198.51.100.0/24")
	require.Equal(t, []string{"198.51.100.10"}, flow.DestinationAddresses,
		"the default action must keep publishing dial candidates")
	require.Empty(t, flow.MatchAddresses,
		"and must not publish matching evidence")
}
