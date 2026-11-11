package e2e

import (
	"fmt"
	"net"
	"net/netip"
	"slices"
	"sync"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/common/uot"

	"github.com/stretchr/testify/require"
)

// The UoT session-level version of the cross-datagram question.
//
// A non-connect UoT session is authorised ONCE, from the destination in its request header, and then
// carries a DIFFERENT destination on every datagram. packetDestinationGuard re-evaluates the rules
// per datagram, and this feature's evidence belongs to the destination it was resolved for - so the
// session's matching address must never judge another datagram.
//
// The attack is exactly that inheritance: the session is opened for `a.test` (resolved to 127.0.0.1,
// which selects the allowing rule), and the attacker then sends a second datagram to `b.test`
// (resolved to 127.0.0.2, which a rejecting rule names). If the session's evidence leaked, the
// allowing rule would match `b.test` too and the datagram would be forwarded; the test asserts the
// guard's own drop line instead, and that the session survives the dropped datagram.
const resolveMatchOnlyUoTConfig = `{
  "log": {"level": "%s"},
  "dns": {
    "servers": [{"tag": "local", "type": "udp", "server": "127.0.0.1", "server_port": %d}],
    "final": "local",
    "strategy": "ipv4_only"
  },
  "inbounds": [
    {"type": "mixed", "tag": "mixed-in", "listen": "127.0.0.1", "listen_port": %d}
  ],
  "outbounds": [
    {"type": "direct", "tag": "direct", "domain_resolver": "local"}
  ],
  "route": {
    "rules": [
      {"action": "resolve", "match_only": true},
      {"ip_cidr": ["127.0.0.1/32"], "action": "route", "outbound": "direct"},
      {"ip_cidr": ["127.0.0.2/32"], "action": "reject"}
    ],
    "final": "direct"
  }
}`

// udpPayloadRecorder receives datagrams and records their payloads, so "did this datagram reach the
// network" is an observation rather than an inference.
type udpPayloadRecorder struct {
	conn     net.PacketConn
	mu       sync.Mutex
	payloads []string
}

func startUDPPayloadRecorder(t *testing.T) *udpPayloadRecorder {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	recorder := &udpPayloadRecorder{conn: conn}
	go func() {
		buffer := make([]byte, 65535)
		for {
			n, from, readErr := conn.ReadFrom(buffer)
			if readErr != nil {
				return
			}
			recorder.mu.Lock()
			recorder.payloads = append(recorder.payloads, string(buffer[:n]))
			recorder.mu.Unlock()
			_, _ = conn.WriteTo(buffer[:n], from)
		}
	}()
	t.Cleanup(func() { conn.Close() })
	return recorder
}

func (r *udpPayloadRecorder) port() uint16 {
	return uint16(r.conn.LocalAddr().(*net.UDPAddr).Port)
}

func (r *udpPayloadRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.payloads...)
}

func (r *udpPayloadRecorder) waitFor(t *testing.T, payload string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if slices.Contains(r.snapshot(), payload) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for UDP payload %q, saw %v", timeout, payload, r.snapshot())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestResolveMatchOnlyUoTSessionDoesNotJudgeOneDatagramByAnother(t *testing.T) {
	recorder := startUDPPayloadRecorder(t)
	payloadPort := recorder.port()

	dnsLocal := startDNSResponder(t, "local", map[string]netip.Addr{
		"a.test": netip.MustParseAddr("127.0.0.1"),
		"b.test": netip.MustParseAddr("127.0.0.2"),
	})

	captured := &capturedPlatformLog{}
	proxyPort := freePort(t)
	running := startChainWithWriter(t, fmt.Sprintf(resolveMatchOnlyUoTConfig, "debug", dnsLocal.port(), proxyPort), captured)
	proxyAddress := fmt.Sprintf("127.0.0.1:%d", proxyPort)

	// A real non-connect UoT session: the SOCKS5 CONNECT goes to the magic address, and the session
	// header carries the session destination. Every datagram then carries its own.
	magicAddress := uot.RequestDestination(uot.Version)
	conn, code := socks5Request(t, proxyAddress, 0x03, magicAddress.Fqdn, 443)
	require.Equal(t, byte(0x00), code, "the UoT session must be accepted")
	require.NotNil(t, conn)
	session := uot.NewLazyConn(conn, uot.Request{
		IsConnect:   false,
		Destination: M.Socksaddr{Fqdn: "a.test", Port: payloadPort},
	})
	defer session.Close()

	send := func(payload string, host string) {
		_, err := session.WriteTo([]byte(payload), M.Socksaddr{Fqdn: host, Port: payloadPort})
		require.NoError(t, err)
	}

	// The session destination itself: permitted by the rule its own answer selects.
	send("one", "a.test")
	recorder.waitFor(t, "one", 10*time.Second)

	// The attacker's datagram: a different destination, whose own answer is named by a rejecting
	// rule. The session's matching evidence must not satisfy the allowing rule for it.
	send("two", "b.test")
	captured.waitFor(t, "drop datagram to b.test", 10*time.Second)

	// And the session must survive the dropped datagram.
	send("three", "a.test")
	recorder.waitFor(t, "three", 10*time.Second)

	require.NotContains(t, recorder.snapshot(), "two",
		"a datagram the rules refuse must never reach the network")

	// The session's own route decision, for the record: the matching answer selected the outbound,
	// and no dial candidate was produced.
	flows := running.tracker.waitForFlows(t, 1, 10*time.Second)
	flow := flows[len(flows)-1]
	require.Equal(t, "a.test:"+fmt.Sprint(payloadPort), flow.Destination,
		"the session's destination is the request header's, not the magic address")
	require.Equal(t, "direct", flow.RouteOutbound)
	require.Contains(t, flow.RouteRule, "ip_cidr=127.0.0.1/32",
		"the session must be decided by the matching-only answer")
	require.Equal(t, []string{"127.0.0.1"}, flow.MatchAddresses)
	require.Empty(t, flow.DestinationAddresses)
}
