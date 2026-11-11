package e2e

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/log"

	"github.com/stretchr/testify/require"
)

// Attack-shaped tests for `resolve` with `match_only: true`.
//
// Each one states what the test can control, what it tries to break, and what counts as failure:
//
//	private answer   the DNS answer is inside RFC1918        break: the rule must still be selected
//	                 for a name, in BOTH modes                by the answer, and the wire must differ
//	failed lookup    the resolver returns nothing            break: no implicit allow / no direct
//	                                                         connection on the matching failure
//	cross-flow       many concurrent names with exclusive    break: one flow's answer selects
//	                 CIDRs and outbounds                     another flow's outbound
//
// The first is also the documented limitation in action: the match is the LOCAL resolver's view, so
// a rule can only be as accurate as that answer. The tests assert the *decision*, not a claim about
// where the far side eventually connected.

const resolveMatchOnlyPrivateConfig = `{
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
      {"ip_is_private": true, "action": "route", "outbound": "remote"}
    ],
    "final": "direct"
  }
}`

// TestResolveMatchOnlyPrivateAnswerSelectsTheRule drives `ip_is_private` - a matcher that had NO
// access to a resolve action's dial candidate list before this feature - through the real chain, in
// both modes.
func TestResolveMatchOnlyPrivateAnswerSelectsTheRule(t *testing.T) {
	echo := startEchoServer(t, "tcp", "127.0.0.1:0")
	echoPort := uint16(echo.listener.Addr().(*net.TCPAddr).Port)

	for _, testCase := range []struct {
		name      string
		matchOnly bool
		// The wire form the proxy must receive: the name in matching-only mode, the answer in the
		// historical mode.
		wantAddressType byte
		wantHost        string
	}{
		{name: "match only keeps the name", matchOnly: true, wantAddressType: 0x03, wantHost: "lan.test"},
		{name: "default mode dials the answer", matchOnly: false, wantAddressType: 0x01, wantHost: "10.0.0.7"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			sink := startSocksSink(t, "127.0.0.1:0")
			// The sink stands in for the remote node the rule selected; it resolves names itself,
			// so it must be able to reach the echo through either form.
			sink.backendOverride = map[string]string{
				"lan.test:" + fmt.Sprint(echoPort): echo.listener.Addr().String(),
				"10.0.0.7:" + fmt.Sprint(echoPort): echo.listener.Addr().String(),
			}
			sinkPort := uint16(sink.listener.Addr().(*net.TCPAddr).Port)

			dnsLocal := startDNSResponder(t, "dns-local", map[string]netip.Addr{
				"lan.test": netip.MustParseAddr("10.0.0.7"),
			})

			proxyPort := freePort(t)
			running := startChain(t, fmt.Sprintf(resolveMatchOnlyPrivateConfig, chainLogLevel,
				dnsLocal.port(), proxyPort, sinkPort, testCase.matchOnly))

			conn := dialSocks5(t, fmt.Sprintf("127.0.0.1:%d", proxyPort), 0x03, "lan.test", echoPort)
			defer conn.Close()
			requireEcho(t, conn, "private-answer")

			requests := sink.waitForRequests(t, 1, 10*time.Second)
			require.Equal(t, testCase.wantAddressType, requests[0].AddressType,
				"the private answer must select the rule; the wire form must follow the mode: %s", requests[0])
			require.Equal(t, testCase.wantHost, requests[0].Host)

			flows := running.tracker.waitForFlows(t, 1, 10*time.Second)
			flow := flows[len(flows)-1]
			require.Equal(t, "remote", flow.RouteOutbound,
				"ip_is_private must be decided BY the resolved answer in this mode")
			require.Contains(t, flow.RouteRule, "ip_is_private",
				"the rule that decided the flow must be the private-address one; got %q", flow.RouteRule)

			if testCase.matchOnly {
				require.Equal(t, []string{"10.0.0.7"}, flow.MatchAddresses)
				require.Empty(t, flow.DestinationAddresses)
			} else {
				require.Equal(t, []string{"10.0.0.7"}, flow.DestinationAddresses)
				require.Empty(t, flow.MatchAddresses)
			}
		})
	}
}

const resolveMatchOnlyLookupFailureConfig = `{
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

// capturedPlatformLog is the box's production log surface, drained by a test.
type capturedPlatformLog struct {
	mu       sync.Mutex
	messages []string
}

func (w *capturedPlatformLog) WriteMessage(level log.Level, message string) {
	w.mu.Lock()
	w.messages = append(w.messages, message)
	w.mu.Unlock()
}

func (w *capturedPlatformLog) waitFor(t *testing.T, fragment string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		w.mu.Lock()
		found := false
		for _, message := range w.messages {
			if strings.Contains(message, fragment) {
				found = true
				break
			}
		}
		w.mu.Unlock()
		if found {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %q in the box log", timeout, fragment)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// socks5ConnectOn performs the handshake on an ALREADY CONNECTED socket and returns errors instead of
// failing the test, so it is safe inside the concurrency loop below (testing's FailNow must not be
// called from a goroutine that is not the test's).
func socks5ConnectOn(conn net.Conn, host string, port uint16) error {
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		return fmt.Errorf("greeting write: %w", err)
	}
	greeting := make([]byte, 2)
	if _, err := io.ReadFull(conn, greeting); err != nil {
		return fmt.Errorf("greeting read: %w", err)
	}
	if greeting[0] != 0x05 || greeting[1] != 0x00 {
		return fmt.Errorf("greeting rejected: %x", greeting)
	}
	request := []byte{0x05, 0x01, 0x00, 0x03, byte(len(host))}
	request = append(request, host...)
	request = binary.BigEndian.AppendUint16(request, port)
	if _, err := conn.Write(request); err != nil {
		return fmt.Errorf("connect write: %w", err)
	}
	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil {
		return fmt.Errorf("connect read: %w", err)
	}
	if head[1] != 0x00 {
		return fmt.Errorf("connect refused with code 0x%02x", head[1])
	}
	switch head[3] {
	case 0x01:
		_, err := io.ReadFull(conn, make([]byte, 4+2))
		return err
	case 0x04:
		_, err := io.ReadFull(conn, make([]byte, 16+2))
		return err
	case 0x03:
		length := make([]byte, 1)
		if _, err := io.ReadFull(conn, length); err != nil {
			return err
		}
		_, err := io.ReadFull(conn, make([]byte, int(length[0])+2))
		return err
	default:
		return fmt.Errorf("unsupported reply address type %d", head[3])
	}
}

// TestResolveMatchOnlyLookupFailureIsNotAnAllow: a resolution that cannot answer must fail the
// flow. The failure must not fall back to the next rule, to the direct outbound, or to "no match
// means allow" - the three ways a matching failure could become an implicit bypass.
func TestResolveMatchOnlyLookupFailureIsNotAnAllow(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		matchOnly bool
	}{
		{name: "match only", matchOnly: true},
		{name: "default mode", matchOnly: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			echo := startEchoServer(t, "tcp", "127.0.0.1:0")
			echoPort := uint16(echo.listener.Addr().(*net.TCPAddr).Port)
			sink := startSocksSink(t, "127.0.0.1:0")
			sinkPort := uint16(sink.listener.Addr().(*net.TCPAddr).Port)
			// The responder has no answer for the name below, so the lookup ends empty.
			dnsLocal := startDNSResponder(t, "dns-local", map[string]netip.Addr{})
			captured := &capturedPlatformLog{}

			proxyPort := freePort(t)
			startChainWithWriter(t, fmt.Sprintf(resolveMatchOnlyLookupFailureConfig, chainLogLevel,
				dnsLocal.port(), proxyPort, sinkPort, testCase.matchOnly), captured)

			acceptedBefore := echo.accepts.Load()
			conn, code := socks5Request(t, fmt.Sprintf("127.0.0.1:%d", proxyPort), 0x03, "broken.test", echoPort)
			if conn != nil {
				defer conn.Close()
			}
			require.NotEqual(t, byte(0x00), code,
				"a name whose resolution fails must not be connected; got a success reply")

			require.Contains(t, dnsLocal.asked(), "broken.test/1",
				"the action must have asked the resolver - otherwise nothing was tested")
			captured.waitFor(t, "broken.test", 5*time.Second)

			require.Empty(t, sink.seen(),
				"a failed match must not reach the outbound the unmatched rule names")
			require.Equal(t, acceptedBefore, echo.accepts.Load(),
				"and must not be dialled through the fallback direct outbound either")
		})
	}
}

const resolveMatchOnlyIsolationConfig = `{
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
    {"type": "socks", "tag": "remote-a", "server": "127.0.0.1", "server_port": %d, "version": "5"},
    {"type": "socks", "tag": "remote-b", "server": "127.0.0.1", "server_port": %d, "version": "5"}
  ],
  "route": {
    "rules": [
      {"action": "resolve", "match_only": true},
      {"ip_cidr": ["198.51.100.0/24"], "action": "route", "outbound": "remote-a"},
      {"ip_cidr": ["203.0.113.0/24"], "action": "route", "outbound": "remote-b"}
    ],
    "final": "remote-a"
  }
}`

// TestResolveMatchOnlyConcurrentFlowsStayIsolated is the cross-flow attack: two names resolving into
// MUTUALLY EXCLUSIVE ranges are driven concurrently, so a shared or leaked matching list would route
// one name's traffic to the other name's outbound. The sinks are separate servers, so the assertion
// is on which server received which name - not on a metadata field.
const resolveMatchOnlyNamedResolverConfig = `{
  "log": {"level": "%s"},
  "dns": {
    "servers": [
      {"tag": "dns-default", "type": "udp", "server": "127.0.0.1", "server_port": %d},
      {"tag": "dns-named", "type": "udp", "server": "127.0.0.1", "server_port": %d}
    ],
    "final": "dns-default",
    "strategy": "ipv4_only"
  },
  "inbounds": [
    {"type": "mixed", "tag": "mixed-in", "listen": "127.0.0.1", "listen_port": %d}
  ],
  "outbounds": [
    {"type": "socks", "tag": "remote", "server": "127.0.0.1", "server_port": %d, "version": "5"},
    {"type": "direct", "tag": "direct", "domain_resolver": "dns-default"}
  ],
  "route": {
    "rules": [
      {"action": "resolve", "match_only": true, "server": "dns-named", "strategy": "ipv4_only"},
      {"ip_cidr": ["198.51.100.0/24"], "action": "route", "outbound": "remote"}
    ],
    "final": "direct"
  }
}`

// TestResolveMatchOnlyHonoursTheActionsOwnResolver is §4.1's "the existing fields still work": the
// option changes only where an answer is published, never which server answers it, which strategy is
// applied, or whether the answer is cached.
//
// The two servers are configured to disagree, so "which server was asked" is observable in the
// verdict: only the named server's answer is inside the rule's CIDR, so a resolve that ignored
// `server` could not select the proxy at all.
func TestResolveMatchOnlyHonoursTheActionsOwnResolver(t *testing.T) {
	echo := startEchoServer(t, "tcp", "127.0.0.1:0")
	echoPort := uint16(echo.listener.Addr().(*net.TCPAddr).Port)

	sink := startSocksSink(t, "127.0.0.1:0")
	sink.backendOverride = map[string]string{
		"named.test:" + fmt.Sprint(echoPort): echo.listener.Addr().String(),
	}
	sinkPort := uint16(sink.listener.Addr().(*net.TCPAddr).Port)

	// The default resolver's answer would NOT select the ip_cidr rule.
	dnsDefault := startDNSResponder(t, "dns-default", map[string]netip.Addr{
		"named.test": netip.MustParseAddr("203.0.113.9"),
	})
	// The action's named resolver's answer does.
	dnsNamed := startDNSResponder(t, "dns-named", map[string]netip.Addr{
		"named.test": netip.MustParseAddr("198.51.100.10"),
	})

	proxyPort := freePort(t)
	running := startChain(t, fmt.Sprintf(resolveMatchOnlyNamedResolverConfig, chainLogLevel,
		dnsDefault.port(), dnsNamed.port(), proxyPort, sinkPort))
	proxyAddress := fmt.Sprintf("127.0.0.1:%d", proxyPort)

	for _, attempt := range []int{1, 2} {
		conn := dialSocks5(t, proxyAddress, 0x03, "named.test", echoPort)
		requireEcho(t, conn, fmt.Sprintf("named-resolver-%d", attempt))
		conn.Close()
	}

	require.Contains(t, dnsNamed.asked(), "named.test/1",
		"the action's own `server` must still be the one asked")
	require.NotContains(t, dnsDefault.asked(), "named.test/1",
		"the default resolver must not be used instead of the action's server")
	require.NotContains(t, dnsNamed.asked(), "named.test/28",
		"the action's own `strategy` must still decide the query types (ipv4_only means A only)")

	// Two connections, one answer: `match_only` must not disable the DNS cache.
	namedQueries := 0
	for _, question := range dnsNamed.asked() {
		if question == "named.test/1" {
			namedQueries++
		}
	}
	require.Equal(t, 1, namedQueries,
		"the second connection must be served from the DNS cache; match_only changes the publication "+
			"point, not the query options")

	requests := sink.waitForRequests(t, 2, 10*time.Second)
	for _, request := range requests {
		require.Equal(t, "named.test", request.Host,
			"and both flows must still hand the proxy the name")
	}
	flows := running.tracker.waitForFlows(t, 2, 10*time.Second)
	require.Equal(t, "remote", flows[len(flows)-1].RouteOutbound)
	require.Contains(t, flows[len(flows)-1].RouteRule, "ip_cidr=198.51.100.0/24",
		"the rule must be selected by the NAMED server's answer, which is the only one inside the CIDR")
}

func TestResolveMatchOnlyConcurrentFlowsStayIsolated(t *testing.T) {
	echo := startEchoServer(t, "tcp", "127.0.0.1:0")
	echoPort := uint16(echo.listener.Addr().(*net.TCPAddr).Port)

	sinkA := startSocksSink(t, "127.0.0.1:0")
	sinkB := startSocksSink(t, "127.0.0.1:0")
	sinkA.backendOverride = map[string]string{
		"a.test:" + fmt.Sprint(echoPort): echo.listener.Addr().String(),
	}
	sinkB.backendOverride = map[string]string{
		"b.test:" + fmt.Sprint(echoPort): echo.listener.Addr().String(),
	}

	dnsLocal := startDNSResponder(t, "dns-local", map[string]netip.Addr{
		"a.test": netip.MustParseAddr("198.51.100.10"),
		"b.test": netip.MustParseAddr("203.0.113.7"),
	})

	proxyPort := freePort(t)
	running := startChain(t, fmt.Sprintf(resolveMatchOnlyIsolationConfig, chainLogLevel,
		dnsLocal.port(), proxyPort,
		uint16(sinkA.listener.Addr().(*net.TCPAddr).Port),
		uint16(sinkB.listener.Addr().(*net.TCPAddr).Port)))
	proxyAddress := fmt.Sprintf("127.0.0.1:%d", proxyPort)

	const perName = 10
	var (
		waitGroup sync.WaitGroup
		failures  = make(chan string, perName*2)
	)
	for index := 0; index < perName; index++ {
		for _, host := range []string{"a.test", "b.test"} {
			waitGroup.Add(1)
			go func(host string) {
				defer waitGroup.Done()
				conn, err := net.DialTimeout("tcp", proxyAddress, 5*time.Second)
				if err != nil {
					failures <- fmt.Sprintf("%s: dial: %v", host, err)
					return
				}
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(15 * time.Second))
				if err = socks5ConnectOn(conn, host, echoPort); err != nil {
					failures <- fmt.Sprintf("%s: %v", host, err)
					return
				}
				if _, err = conn.Write([]byte("isolation")); err != nil {
					failures <- fmt.Sprintf("%s: write: %v", host, err)
					return
				}
				payload := make([]byte, len("isolation"))
				if _, err = io.ReadFull(conn, payload); err != nil {
					failures <- fmt.Sprintf("%s: read: %v", host, err)
					return
				}
				if string(payload) != "isolation" {
					failures <- fmt.Sprintf("%s: echo mismatch %q", host, payload)
				}
			}(host)
		}
	}
	waitGroup.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
	if t.Failed() {
		return
	}

	requestsA := sinkA.waitForRequests(t, perName, 15*time.Second)
	requestsB := sinkB.waitForRequests(t, perName, 15*time.Second)
	for _, request := range requestsA {
		require.Equal(t, "a.test", request.Host,
			"remote-a is the outbound the 198.51.100.0/24 answer selects, and ONLY a.test resolves there")
	}
	for _, request := range requestsB {
		require.Equal(t, "b.test", request.Host,
			"remote-b is the outbound the 203.0.113.0/24 answer selects, and ONLY b.test resolves there")
	}
	require.Len(t, sinkA.seen(), perName)
	require.Len(t, sinkB.seen(), perName)
	require.Len(t, running.tracker.waitForFlows(t, perName*2, 15*time.Second), perName*2)
}
