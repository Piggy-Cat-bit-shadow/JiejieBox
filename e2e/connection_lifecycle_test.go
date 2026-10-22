package e2e

import (
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/trafficcontrol"

	"github.com/stretchr/testify/require"
)

// The traffic table's lifecycle, driven through a REAL box.
//
// # Why this is the table under test
//
// `trafficcontrol.Manager.Connections()` is the single source of the connection list every client
// shows: the daemon's SubscribeConnections seeds its first message from it, its periodic reconcile
// closes any snapshot it no longer finds there, and a closed event is what removes a row. So an
// entry that stays in this table stays on the phone as "active", with a createdAt and a rate, no
// matter what the socket is doing. That is the observation these tests are built on.
//
// # What is proven here
//
// The four ways a routed stream can end - the peer finishes in both directions, the peer resets, the
// user closes all connections, the box shuts down - each release the entry. Those are the definite
// events, and they are the regression gate for every future change to the copy loops, the trackers
// or the shutdown path.
//
// # The one shape that is NOT proven here, and why
//
// A half-closed flow whose peer then disappears is released on the TUN path by the half-close
// observer (route/conn_halfclose.go, proven in route/tun_halfclose_test.go). It is NOT released on a
// kernel-socket inbound, because the kernel reports EOF on every later read of a socket that has
// already seen FIN and only surfaces the reset to a WRITE - measured, and the reason the last test in
// this file is a characterisation rather than a gate.

// lifecycleTracker returns a REAL trafficcontrol.Manager appended to the live router.
func lifecycleTracker(chain *chain) *trafficcontrol.Manager {
	manager := trafficcontrol.NewManager()
	chain.instance.Router().AppendTracker(manager)
	return manager
}

func waitTrafficConnections(t *testing.T, manager *trafficcontrol.Manager, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last int
	for time.Now().Before(deadline) {
		last = manager.ConnectionsLen()
		if last == want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	var listed []string
	for _, metadata := range manager.Connections() {
		listed = append(listed, metadata.ID.String()+" "+metadata.Metadata.Destination.String())
	}
	t.Fatalf("the traffic table holds %d connections, want %d: %v", last, want, listed)
}

// startSilentPeer accepts, drains, and never answers and never closes: the peer that is simply there.
func startSilentPeer(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	held := make(chan net.Conn, 16)
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			held <- conn
			go func() { _, _ = io.Copy(io.Discard, conn) }()
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		for {
			select {
			case conn := <-held:
				conn.Close()
			default:
				return
			}
		}
	})
	return listener
}

func lifecycleMixedConfig(t *testing.T, listenPort uint16) string {
	t.Helper()
	return `{
  "log": {"level": "warning"},
  "inbounds": [
    {"type": "mixed", "tag": "mixed-in", "listen": "127.0.0.1", "listen_port": ` + strconv.Itoa(int(listenPort)) + `}
  ],
  "outbounds": [
    {"type": "direct", "tag": "direct"}
  ]
}`
}

func startLifecycleChain(t *testing.T) (*chain, *trafficcontrol.Manager, string) {
	t.Helper()
	port := freePort(t)
	chain := startChain(t, lifecycleMixedConfig(t, port))
	return chain, lifecycleTracker(chain), "127.0.0.1:" + strconv.Itoa(int(port))
}

func lifecyclePeerPort(t *testing.T, listener net.Listener) uint16 {
	t.Helper()
	return uint16(listener.Addr().(*net.TCPAddr).Port)
}

// TestTrafficTrackerReleasesOnCleanFIN is the positive control: a client that half-closes and a peer
// that finishes in the other direction leave the table empty.
func TestTrafficTrackerReleasesOnCleanFIN(t *testing.T) {
	echo := startEchoServer(t, "tcp", "127.0.0.1:0")
	_, manager, proxyAddress := startLifecycleChain(t)

	conn := dialSocks5(t, proxyAddress, 0x01, "127.0.0.1", lifecyclePeerPort(t, echo.listener))
	requireEcho(t, conn, "lifecycle-fin")
	waitTrafficConnections(t, manager, 1, 3*time.Second)

	require.NoError(t, conn.(*net.TCPConn).CloseWrite())
	_, _ = io.ReadAll(conn)
	waitTrafficConnections(t, manager, 0, 5*time.Second)
	conn.Close()
}

// TestTrafficTrackerReleasesOnReset is the abortive close.
func TestTrafficTrackerReleasesOnReset(t *testing.T) {
	echo := startEchoServer(t, "tcp", "127.0.0.1:0")
	_, manager, proxyAddress := startLifecycleChain(t)

	conn := dialSocks5(t, proxyAddress, 0x01, "127.0.0.1", lifecyclePeerPort(t, echo.listener))
	requireEcho(t, conn, "lifecycle-rst")
	waitTrafficConnections(t, manager, 1, 3*time.Second)

	require.NoError(t, conn.(*net.TCPConn).SetLinger(0))
	require.NoError(t, conn.Close())
	waitTrafficConnections(t, manager, 0, 5*time.Second)
}

// TestTrafficTrackerReleasesOnCloseAll is the operation the user performed, on a flow that is
// genuinely idle.
func TestTrafficTrackerReleasesOnCloseAll(t *testing.T) {
	silent := startSilentPeer(t)
	_, manager, proxyAddress := startLifecycleChain(t)

	conn := dialSocks5(t, proxyAddress, 0x01, "127.0.0.1", lifecyclePeerPort(t, silent))
	_, err := conn.Write([]byte("lifecycle-idle"))
	require.NoError(t, err)
	waitTrafficConnections(t, manager, 1, 3*time.Second)

	time.Sleep(300 * time.Millisecond)
	require.Equal(t, 1, manager.ConnectionsLen(), "an idle flow is still registered")

	manager.CloseAllConnections()
	waitTrafficConnections(t, manager, 0, 5*time.Second)
	conn.Close()
}

// TestTrafficTrackerReleasesOnShutdown asserts the table drains when the box closes.
func TestTrafficTrackerReleasesOnShutdown(t *testing.T) {
	silent := startSilentPeer(t)
	chain, manager, proxyAddress := startLifecycleChain(t)

	conn := dialSocks5(t, proxyAddress, 0x01, "127.0.0.1", lifecyclePeerPort(t, silent))
	_, err := conn.Write([]byte("lifecycle-close"))
	require.NoError(t, err)
	waitTrafficConnections(t, manager, 1, 3*time.Second)

	require.NoError(t, chain.closeNow())
	waitTrafficConnections(t, manager, 0, 10*time.Second)
	conn.Close()
}

// TestTrafficTrackerHalfClosedKernelPeerIsNotObservable pins the standing limit.
//
// The client finishes its write side and the peer says nothing. On a kernel-socket inbound the
// half-close observation is not even armed: a later read of that socket keeps returning EOF, so the
// verdict cannot be read here (only a write would surface it, and an idle server never writes). The
// manager counts the flow as `skipped (unobservable read side)` instead, which is asserted directly in
// route/conn_halfclose_contract_test.go; this test pins the consequence at the table: the entry stays
// until something else ends it - the application giving up, the peer closing, or close-all.
//
// The phone's own path is the TUN inbound, where the userspace stack DOES report the vanished peer
// to a later read; that is proven in route/tun_halfclose_test.go. This test exists so the kernel-side
// limit is a recorded fact rather than an absence, and so a future mechanism that does cover it is
// noticed when this assertion changes.
func TestTrafficTrackerHalfClosedKernelPeerIsNotObservable(t *testing.T) {
	silent := startSilentPeer(t)
	_, manager, proxyAddress := startLifecycleChain(t)

	conn := dialSocks5(t, proxyAddress, 0x01, "127.0.0.1", lifecyclePeerPort(t, silent))
	_, err := conn.Write([]byte("lifecycle-half-close"))
	require.NoError(t, err)
	waitTrafficConnections(t, manager, 1, 3*time.Second)

	require.NoError(t, conn.(*net.TCPConn).CloseWrite())
	// The peer answers nothing. Give the observation window far more time than the production
	// keepalive idle a half-closed flow is given, so this is not a race with a mechanism.
	time.Sleep(500 * time.Millisecond)
	require.Equal(t, 1, manager.ConnectionsLen(),
		"a kernel-socket inbound cannot observe a peer that disappeared after its FIN; the entry "+
			"stays until the peer closes or the user closes all connections")

	manager.CloseAllConnections()
	waitTrafficConnections(t, manager, 0, 5*time.Second)
	conn.Close()
}
