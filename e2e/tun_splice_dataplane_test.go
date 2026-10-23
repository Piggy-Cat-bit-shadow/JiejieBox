package e2e

import (
	"fmt"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/route"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// The route-splice decision, at the product level.
//
// # What this file can and cannot show, stated first
//
// The in-memory TUN cannot exercise the ACCEPTED half of a TCP handover, and that is a property of
// the fixture rather than of the product: the go stack's memory platform IO reports
// supportsSockets() == false (sing-tun stack_go_io_memory.go), and GoConn.Splice refuses on that
// first check. A real device's IO (Linux, Darwin, Windows Wintun) reports true, which is why the
// fork's own accepted-handover integration test is built for linux/darwin only.
//
// So this file proves the half that IS observable here, and names the half that is not:
//
//   - a direct TUN TCP flow really reaches the handover decision, is refused at the platform
//     boundary, and delivers every byte through the copy loops - the OFF control with the exact
//     reason recorded;
//   - its SOURCE is classified as a tun.GoConn (not as a non-TUN stream), which is the distinction
//     that separates a TUN flow from a system-proxy flow;
//   - a system-proxy flow is refused at the source, which is why the system proxy cannot use the
//     TUN splice path at all.
//
// The accepted half and the kernel's byte movement after it are NOT_VERIFIED in this environment.
// The diagnostics record Accepted only when sing-tun accepts, and do not claim what the kernel did
// afterwards; a device run is required to move that line.

// spliceDataplaneConfig routes one literal destination to one outbound. The destination is not
// rewritten, so the direct case dials it for real.
const spliceDataplaneConfig = `{
  "log": {"level": "%s"},
  "outbounds": [
    {"type": "direct", "tag": "direct"},
    {"type": "socks", "tag": "remote", "server": "127.0.0.1", "server_port": %d, "version": "5"}
  ],
  "route": {
    "rules": [
      {"ip_cidr": ["%s/32"], "port": [%d], "action": "route", "outbound": "%s"}
    ],
    "final": "direct"
  }
}`

// spliceDiagnosticsProvider is the manager's diagnostic surface. It is asserted structurally
// rather than declared in adapter, for the same reason the other diagnostics are: it is an
// observation hook, not a capability the router consumes.
type spliceDiagnosticsProvider interface {
	TCPSpliceDiagnostics() route.SpliceSnapshot
}

// tcpSpliceDiagnostics reads the live manager's stream-handover counters.
func tcpSpliceDiagnostics(t *testing.T, running *chain) route.SpliceSnapshot {
	t.Helper()
	manager := service.FromContext[adapter.ConnectionManager](running.ctx)
	require.NotNil(t, manager, "the box must register its connection manager in the service context")
	provider, isProvider := manager.(spliceDiagnosticsProvider)
	require.True(t, isProvider,
		"the connection manager must expose TCPSpliceDiagnostics; without it the splice decision "+
			"is unobservable")
	return provider.TCPSpliceDiagnostics()
}

// TestDirectTUNFlowTakesTheSpliceDecisionAndFallsBack is the OFF control at product level.
//
// The flow must reach the decision (Attempts == 1), be refused for the reason the memory IO
// implies, and still deliver both directions. "Reached the decision and fell back" is the property
// that matters for safety; the accepted branch is a device-only claim.
func TestDirectTUNFlowTakesTheSpliceDecisionAndFallsBack(t *testing.T) {
	destination := localPeerAddress(t)
	echo := startEchoServer(t, "tcp", net.JoinHostPort(destination.String(), "0"))
	echoPort := uint16(echo.listener.Addr().(*net.TCPAddr).Port)
	sink := startSocksSink(t, "127.0.0.1:0")

	config := fmt.Sprintf(spliceDataplaneConfig, chainLogLevel,
		uint16(sink.listener.Addr().(*net.TCPAddr).Port), destination.String(), echoPort, "direct")
	running := startChainUntracked(t, config)
	fixture := newTunFixture(t, running, netip.Addr{}, 0)

	connection := &tcpOverTun{sourcePort: 43001, destination: destination, destinationPort: echoPort}
	fixture.dialTCP(t, connection, 10*time.Second)
	connection.write(t, "splice-off-payload")
	connection.expectEcho(t, "splice-off-payload", 10*time.Second)
	require.GreaterOrEqual(t, echo.bytes.Load(), int64(len("splice-off-payload")),
		"the payload must have crossed before the handover counters are read")

	snapshot := tcpSpliceDiagnostics(t, running)
	require.Equal(t, uint64(1), snapshot.Attempts,
		"one TCP flow, one handover decision: %+v", snapshot)
	require.Zero(t, snapshot.Accepted,
		"the in-memory platform IO reports supportsSockets() == false, so GoConn.Splice must "+
			"refuse. If this becomes accepted, the fixture gained socket support and the ON half "+
			"can be asserted here: %+v", snapshot)
	require.Equal(t, uint64(1), snapshot.Reasons["splice_rejected"],
		"the refusal is the platform's socket check, recorded as the generic rejection because "+
			"sing-tun returns a bare bool: %+v", snapshot)
	require.Empty(t, sink.seen(), "a direct flow must not reach the proxy outbound")
}

// TestProxiedTUNFlowStillHasAGoConnSource separates the source classification from the target one.
//
// The final rejection is the same platform check as above, because the socks dialer's conn is also
// a trackedConn (a tun.SpliceSocket) - so the target walk does not fail. What must NOT happen is the
// source being classified as a non-TUN stream; that would mean the walk and the product disagree
// about what a TUN flow is.
func TestProxiedTUNFlowStillHasAGoConnSource(t *testing.T) {
	destination := netip.MustParseAddr("10.9.9.30")
	echo := startEchoServer(t, "tcp", "127.0.0.1:0")
	echoPort := uint16(echo.listener.Addr().(*net.TCPAddr).Port)
	sink := startSocksSink(t, "127.0.0.1:0")
	sink.backendOverride = map[string]string{
		net.JoinHostPort(destination.String(), fmt.Sprint(echoPort)): echo.listener.Addr().String(),
	}

	config := fmt.Sprintf(spliceDataplaneConfig, chainLogLevel,
		uint16(sink.listener.Addr().(*net.TCPAddr).Port), destination.String(), echoPort, "remote")
	running := startChainUntracked(t, config)
	fixture := newTunFixture(t, running, netip.Addr{}, 0)

	connection := &tcpOverTun{sourcePort: 43002, destination: destination, destinationPort: echoPort}
	fixture.dialTCP(t, connection, 10*time.Second)
	connection.write(t, "proxied-splice")
	connection.expectEcho(t, "proxied-splice", 10*time.Second)
	request := sink.waitForHost(t, destination.String(), 10*time.Second)
	require.Equal(t, echoPort, request.Port, "the proxied flow must reach the socks outbound")

	snapshot := tcpSpliceDiagnostics(t, running)
	require.Equal(t, uint64(1), snapshot.Attempts, "one flow, one decision: %+v", snapshot)
	require.Zero(t, snapshot.Accepted, "the memory platform cannot accept a handover: %+v", snapshot)
	require.NotContains(t, snapshot.Reasons, "source_not_go_conn",
		"a TUN flow's source IS a tun.GoConn; classifying it otherwise would mean the source walk "+
			"changed: %+v", snapshot)
}

// TestProxiedSpliceRejectionDeliversAMultiSegmentPayload pins the cached/refused half of I2.
//
// The splice attempt is refused at the platform check, and the bytes that were already read from
// the TUN (the first packet's payload and every following segment) must reach the proxy outbound
// exactly once and come back byte-identical. A handover that is refused after userspace has consumed
// bytes is the one place where a wrong fallback loses or duplicates the beginning of a stream.
func TestProxiedSpliceRejectionDeliversAMultiSegmentPayload(t *testing.T) {
	destination := netip.MustParseAddr("10.9.9.31")
	echo := startEchoServer(t, "tcp", "127.0.0.1:0")
	echoPort := uint16(echo.listener.Addr().(*net.TCPAddr).Port)
	sink := startSocksSink(t, "127.0.0.1:0")
	sink.backendOverride = map[string]string{
		net.JoinHostPort(destination.String(), fmt.Sprint(echoPort)): echo.listener.Addr().String(),
	}

	config := fmt.Sprintf(spliceDataplaneConfig, chainLogLevel,
		uint16(sink.listener.Addr().(*net.TCPAddr).Port), destination.String(), echoPort, "remote")
	running := startChainUntracked(t, config)
	fixture := newTunFixture(t, running, netip.Addr{}, 0)

	connection := &tcpOverTun{sourcePort: 43003, destination: destination, destinationPort: echoPort}
	fixture.dialTCP(t, connection, 10*time.Second)

	payload := make([]byte, 16*1024)
	for index := range payload {
		payload[index] = byte(index % 251)
	}
	connection.writeChunks(t, payload, 1200)
	echoed := connection.collectEchoBytes(t, len(payload), 15*time.Second)
	require.Equal(t, payload, echoed,
		"a refused handover must fall back to the copy loops without losing or duplicating the "+
			"cached segments")

	snapshot := tcpSpliceDiagnostics(t, running)
	require.Equal(t, uint64(1), snapshot.Attempts, "one flow, one decision: %+v", snapshot)
	require.Zero(t, snapshot.Accepted, "the memory platform cannot accept a handover: %+v", snapshot)
	require.Equal(t, uint64(1), snapshot.Reasons["splice_rejected"], "%+v", snapshot)
	require.NotContains(t, snapshot.Reasons, "source_not_go_conn",
		"the source must stay classified as a TUN stream: %+v", snapshot)
	require.NotEmpty(t, sink.seen(), "the flow must have gone through the proxy outbound")
}

// TestSystemProxyTCPSpliceIsRefusedAtTheSource is the system-proxy baseline: its inbound conn is not
// a tun.GoConn, so every flow is refused at the source. That is correct and is why the system proxy
// cannot use the TUN splice path.
func TestSystemProxyTCPSpliceIsRefusedAtTheSource(t *testing.T) {
	echo := startEchoServer(t, "tcp", "127.0.0.1:0")
	echoPort := uint16(echo.listener.Addr().(*net.TCPAddr).Port)
	proxyPort := freePort(t)
	config := fmt.Sprintf(`{
  "log": {"level": "%s"},
  "inbounds": [{"type": "mixed", "tag": "mixed-in", "listen": "127.0.0.1", "listen_port": %d}],
  "outbounds": [{"type": "direct", "tag": "direct"}],
  "route": {"final": "direct"}
}`, chainLogLevel, proxyPort)
	running := startChainUntracked(t, config)

	conn := dialSocks5(t, fmt.Sprintf("127.0.0.1:%d", proxyPort), 0x01, "127.0.0.1", echoPort)
	requireEcho(t, conn, "system-proxy")
	_ = conn.Close()

	snapshot := tcpSpliceDiagnostics(t, running)
	require.NotZero(t, snapshot.Attempts,
		"the system proxy's TCP flow must still reach the handover decision: %+v", snapshot)
	require.Zero(t, snapshot.Accepted,
		"a system-proxy flow must never be handed to a TUN socket: %+v", snapshot)
	require.Equal(t, uint64(1), snapshot.Reasons["source_not_go_conn"],
		"its source is not a tun.GoConn, which is the refusal this test exists to distinguish: %+v",
		snapshot)
}
