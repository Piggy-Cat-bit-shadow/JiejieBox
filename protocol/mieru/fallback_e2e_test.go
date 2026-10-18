package mieru

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/stretchr/testify/require"
)

func fallbackOutboundOptions(primaryPort uint16, primaryTransport string, secondaryPort uint16, secondaryTransport string) option.MieruOutboundOptions {
	options := outboundOptionsForPort(primaryPort, primaryTransport, "alice", "alice-password")
	options.Fallback = &option.MieruFallbackOptions{
		Enabled:    true,
		Transport:  secondaryTransport,
		ServerPort: secondaryPort,
	}
	return options
}

func differentFreePort(t *testing.T, first uint16) uint16 {
	t.Helper()
	for attempt := 0; attempt < 32; attempt++ {
		port := freePortForBothTransports(t)
		if port != first {
			return port
		}
	}
	t.Fatal("could not allocate a distinct free port")
	return 0
}

func readAndWriteEcho(t *testing.T, conn net.Conn, payload string) {
	t.Helper()
	require.NoError(t, conn.SetDeadline(time.Now().Add(10*time.Second)))
	_, err := conn.Write([]byte(payload))
	require.NoError(t, err)
	buffer := make([]byte, len(payload))
	_, err = conn.Read(buffer)
	require.NoError(t, err)
	require.Equal(t, payload, string(buffer))
	require.NoError(t, conn.SetDeadline(time.Time{}))
}

func TestMieruFallbackPrimaryHealthyStaysPrimary(t *testing.T) {
	port := freePortForBothTransports(t)
	startTestInbound(t, inboundOptionsForPort(port, "both"))
	outboundInstance, _ := startTestOutbound(t, fallbackOutboundOptions(port, "tcp", port, "udp"))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, err := outboundInstance.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr("192.0.2.20:443"))
	require.NoError(t, err)
	readAndWriteEcho(t, conn, "primary")
	require.NoError(t, conn.Close())

	snapshot := outboundInstance.fallback.snapshot()
	require.Equal(t, fallbackPrimary, snapshot.Preferred)
	require.Zero(t, snapshot.PrimaryFailure)
	require.Zero(t, snapshot.SecondaryWins)
	require.Zero(t, outboundInstance.secondary.packetDialer.packetConnCount(),
		"the UDP runtime must not create a socket when the primary is healthy")

	require.NoError(t, outboundInstance.Close())
	require.False(t, outboundInstance.primary.client.IsRunning())
	require.False(t, outboundInstance.secondary.client.IsRunning())
}

func TestMieruFallbackTCPRefusedUsesUDPAndRecovers(t *testing.T) {
	primaryPort := freePortForBothTransports(t)
	secondaryPort := differentFreePort(t, primaryPort)
	startTestInbound(t, inboundOptionsForPort(secondaryPort, "udp"))
	outboundInstance, _ := startTestOutbound(t, fallbackOutboundOptions(primaryPort, "tcp", secondaryPort, "udp"))

	now := time.Unix(5000, 0)
	outboundInstance.fallback.now = func() time.Time { return now }
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	destination := M.ParseSocksaddr("192.0.2.21:443")

	dialStart := time.Now()
	first, err := outboundInstance.DialContext(ctx, N.NetworkTCP, destination)
	require.NoError(t, err)
	t.Logf("TCP-refused fallback dial took %s", time.Since(dialStart))
	readAndWriteEcho(t, first, "fallback-1")
	snapshot := outboundInstance.fallback.snapshot()
	require.Equal(t, fallbackSecondary, snapshot.Preferred)
	require.GreaterOrEqual(t, snapshot.PrimaryFailure, uint64(1))
	require.GreaterOrEqual(t, snapshot.SecondaryWins, uint64(1))

	second, err := outboundInstance.DialContext(ctx, N.NetworkTCP, destination)
	require.NoError(t, err)
	readAndWriteEcho(t, second, "fallback-2")
	require.Equal(t, fallbackSecondary, outboundInstance.fallback.snapshot().Preferred)

	// Bring the primary endpoint up and move the fake clock past the
	// cooldown. The next business request is the single recovery probe.
	startTestInbound(t, inboundOptionsForPort(primaryPort, "tcp"))
	now = now.Add(fallbackCooldown + time.Second)
	recoveryStart := time.Now()
	recovered, err := outboundInstance.DialContext(ctx, N.NetworkTCP, destination)
	require.NoError(t, err)
	t.Logf("primary recovery probe dial took %s", time.Since(recoveryStart))
	readAndWriteEcho(t, recovered, "primary-again")
	require.Equal(t, fallbackPrimary, outboundInstance.fallback.snapshot().Preferred)
	require.GreaterOrEqual(t, outboundInstance.fallback.snapshot().Probes, uint64(1))

	// The session that was established on the secondary before the primary
	// recovered must still work. A preference change is not a Stop.
	readAndWriteEcho(t, first, "secondary-still-alive")

	require.NoError(t, first.Close())
	require.NoError(t, second.Close())
	require.NoError(t, recovered.Close())
	require.NoError(t, outboundInstance.Close())
	require.Zero(t, outboundInstance.secondary.packetDialer.packetConnCount(),
		"Close must release the fallback UDP socket as well as the primary one")
}

func TestMieruFallbackDisabledCreatesNoSecondaryRuntime(t *testing.T) {
	options := outboundOptionsForPort(1, "tcp", "alice", "alice-password")
	options.Server = "127.0.0.1"
	created, err := NewOutbound(context.Background(), nil, nopLogger(), "mieru-out", options)
	require.NoError(t, err)
	outboundInstance := created.(*Outbound)
	require.Nil(t, outboundInstance.fallback)
	require.Nil(t, outboundInstance.secondary)
	require.Nil(t, outboundInstance.secondarySpec)
	require.NotNil(t, outboundInstance.primary)

	scope := adapter.NewScope(context.Background(), nopLogger())
	require.NoError(t, outboundInstance.Start(adapter.StartStateStart, scope))
	require.True(t, outboundInstance.primary.client.IsRunning())
	require.NoError(t, outboundInstance.Close())
	require.NoError(t, scope.Close())
}

func TestNewOutboundRejectsFallbackConflicts(t *testing.T) {
	base := outboundOptionsForPort(8964, "tcp", "alice", "alice-password")
	base.Server = "127.0.0.1"
	base.Fallback = &option.MieruFallbackOptions{Enabled: true, Transport: "udp"}
	testCases := []struct {
		name    string
		mutate  func(*option.MieruOutboundOptions)
		message string
	}{
		{"both", func(options *option.MieruOutboundOptions) { options.Transport = "both" }, "not supported with transport \"both\""},
		{"no_wait", func(options *option.MieruOutboundOptions) {
			options.HandshakeMode = "no_wait"
		}, "not supported with handshake_mode"},
		{"same transport", func(options *option.MieruOutboundOptions) { options.Fallback.Transport = "tcp" }, "must differ"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			options := base
			fallback := *base.Fallback
			options.Fallback = &fallback
			testCase.mutate(&options)
			_, err := NewOutbound(context.Background(), nil, nopLogger(), "mieru-out", options)
			require.Error(t, err)
			require.Contains(t, err.Error(), testCase.message)
		})
	}
}

func TestMieruFallbackUDPBlackholeUsesTCP(t *testing.T) {
	primaryPort := freePortForBothTransports(t)
	secondaryPort := differentFreePort(t, primaryPort)
	startTestInbound(t, inboundOptionsForPort(secondaryPort, "tcp"))
	outboundInstance, _ := startTestOutbound(t, fallbackOutboundOptions(primaryPort, "udp", secondaryPort, "tcp"))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	destination := M.ParseSocksaddr("192.0.2.22:443")
	conn, err := outboundInstance.DialContext(ctx, N.NetworkTCP, destination)
	require.NoError(t, err, "a successful UDP socket creation must not be treated as a healthy Mieru handshake")
	readAndWriteEcho(t, conn, "udp-blackhole-fallback")
	require.Equal(t, fallbackSecondary, outboundInstance.fallback.snapshot().Preferred)
	require.NoError(t, conn.Close())
}

// startBlackholeTCP accepts TCP connections and never reads or writes them.
// The TCP connect and the Mieru session creation succeed, but the protocol
// handshake cannot complete; this is the "accept then silence" case that a
// socket-success check would misclassify as healthy.
func startBlackholeTCP(t *testing.T, port uint16) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:"+fmt.Sprint(port))
	require.NoError(t, err)
	var (
		access sync.Mutex
		conns  []net.Conn
	)
	t.Cleanup(func() {
		_ = listener.Close()
		access.Lock()
		for _, conn := range conns {
			_ = conn.Close()
		}
		access.Unlock()
	})
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			access.Lock()
			conns = append(conns, conn)
			access.Unlock()
		}
	}()
}

func TestMieruFallbackTCPHandshakeBlackholeUsesUDP(t *testing.T) {
	primaryPort := freePortForBothTransports(t)
	secondaryPort := differentFreePort(t, primaryPort)
	startBlackholeTCP(t, primaryPort)
	startTestInbound(t, inboundOptionsForPort(secondaryPort, "udp"))
	outboundInstance, _ := startTestOutbound(t, fallbackOutboundOptions(primaryPort, "tcp", secondaryPort, "udp"))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, err := outboundInstance.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr("192.0.2.23:443"))
	require.NoError(t, err, "a TCP accept without a Mieru response must fall back, not report a healthy session")
	readAndWriteEcho(t, conn, "tcp-handshake-blackhole")
	require.Equal(t, fallbackSecondary, outboundInstance.fallback.snapshot().Preferred)
	require.NoError(t, conn.Close())
}

func TestMieruFallbackWrongPasswordDoesNotSwitch(t *testing.T) {
	port := freePortForBothTransports(t)
	startTestInbound(t, inboundOptionsForPort(port, "both"))
	options := fallbackOutboundOptions(port, "tcp", port, "udp")
	options.Password = "wrong-password"
	outboundInstance, _ := startTestOutbound(t, options)
	outboundInstance.fallback.now = func() time.Time { return time.Unix(6000, 0) }

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	_, err := outboundInstance.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr("192.0.2.24:443"))
	require.Error(t, err)
	snapshot := outboundInstance.fallback.snapshot()
	require.Equal(t, fallbackPrimary, snapshot.Preferred,
		"a failed authentication/handshake must not leave the outbound preferring the secondary")
	require.GreaterOrEqual(t, snapshot.PrimaryFailure, uint64(1))
	require.Zero(t, snapshot.SecondaryWins)
	require.NoError(t, outboundInstance.Close())
	require.Zero(t, outboundInstance.secondary.packetDialer.packetConnCount(),
		"the fallback socket must still be released on Close")
}

func TestMieruFallbackInterfaceUpdateRebuildsBothGenerations(t *testing.T) {
	primaryPort := freePortForBothTransports(t)
	secondaryPort := differentFreePort(t, primaryPort)
	startTestInbound(t, inboundOptionsForPort(secondaryPort, "udp"))
	outboundInstance, _ := startTestOutbound(t, fallbackOutboundOptions(primaryPort, "tcp", secondaryPort, "udp"))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	destination := M.ParseSocksaddr("192.0.2.25:443")
	conn, err := outboundInstance.DialContext(ctx, N.NetworkTCP, destination)
	require.NoError(t, err)
	readAndWriteEcho(t, conn, "before-update")
	require.NoError(t, conn.Close())

	oldPrimary := outboundInstance.primary
	oldSecondary := outboundInstance.secondary
	oldGeneration := outboundInstance.fallback.snapshot().Generation
	outboundInstance.InterfaceUpdated(context.Background())

	require.NotSame(t, oldPrimary, outboundInstance.primary)
	require.NotSame(t, oldSecondary, outboundInstance.secondary)
	require.Greater(t, outboundInstance.fallback.snapshot().Generation, oldGeneration)
	require.Equal(t, fallbackPrimary, outboundInstance.fallback.snapshot().Preferred,
		"a network generation starts with primary preferred again")
	require.Zero(t, oldSecondary.packetDialer.packetConnCount(), "the old fallback socket registry must be closed")
	require.False(t, oldSecondary.client.IsRunning())

	updated, err := outboundInstance.DialContext(ctx, N.NetworkTCP, destination)
	require.NoError(t, err)
	readAndWriteEcho(t, updated, "after-update")
	require.Equal(t, fallbackSecondary, outboundInstance.fallback.snapshot().Preferred)
	require.NoError(t, updated.Close())
	require.NoError(t, outboundInstance.Close())
	require.False(t, outboundInstance.primary.client.IsRunning())
	require.False(t, outboundInstance.secondary.client.IsRunning())
}

func packetEcho(t *testing.T, packetConn net.PacketConn, destination M.Socksaddr, payload string) {
	t.Helper()
	require.NoError(t, packetConn.SetDeadline(time.Now().Add(10*time.Second)))
	_, err := packetConn.WriteTo([]byte(payload), destination)
	require.NoError(t, err)
	buffer := make([]byte, 2048)
	n, _, err := packetConn.ReadFrom(buffer)
	require.NoError(t, err)
	require.Equal(t, payload, string(buffer[:n]))
	require.NoError(t, packetConn.SetDeadline(time.Time{}))
}

func TestMieruFallbackUDPAssociateUsesSecondary(t *testing.T) {
	primaryPort := freePortForBothTransports(t)
	secondaryPort := differentFreePort(t, primaryPort)
	startTestInbound(t, inboundOptionsForPort(secondaryPort, "both"))
	outboundInstance, _ := startTestOutbound(t, fallbackOutboundOptions(primaryPort, "tcp", secondaryPort, "udp"))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	destination := M.ParseSocksaddr("192.0.2.26:53")
	packetConn, err := outboundInstance.ListenPacket(ctx, destination)
	require.NoError(t, err)
	packetEcho(t, packetConn, destination, "fallback-udp-associate")
	require.Equal(t, fallbackSecondary, outboundInstance.fallback.snapshot().Preferred)
	require.NoError(t, packetConn.Close())
}

func TestMieruFallbackUDPBlackholeUDPAssociateUsesTCP(t *testing.T) {
	primaryPort := freePortForBothTransports(t)
	secondaryPort := differentFreePort(t, primaryPort)
	startTestInbound(t, inboundOptionsForPort(secondaryPort, "both"))
	outboundInstance, _ := startTestOutbound(t, fallbackOutboundOptions(primaryPort, "udp", secondaryPort, "tcp"))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	destination := M.ParseSocksaddr("192.0.2.27:53")
	packetConn, err := outboundInstance.ListenPacket(ctx, destination)
	require.NoError(t, err)
	packetEcho(t, packetConn, destination, "udp-blackhole-tcp-associate")
	require.Equal(t, fallbackSecondary, outboundInstance.fallback.snapshot().Preferred)
	require.NoError(t, packetConn.Close())
}

// startTCPProxy forwards new TCP connections to backendPort and exposes a
// stopNew function that shuts only the listening socket. Existing relays stay
// alive, which lets the test separate "new primary connections fail" from
// "the primary session that is already carrying traffic dies".
func startTCPProxy(t *testing.T, backendPort uint16) (uint16, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	proxyPort := uint16(listener.Addr().(*net.TCPAddr).Port)
	var (
		access sync.Mutex
		conns  []net.Conn
	)
	t.Cleanup(func() {
		_ = listener.Close()
		access.Lock()
		for _, conn := range conns {
			_ = conn.Close()
		}
		access.Unlock()
	})
	go func() {
		for {
			clientConn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			backendConn, dialErr := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", backendPort))
			if dialErr != nil {
				_ = clientConn.Close()
				continue
			}
			access.Lock()
			conns = append(conns, clientConn, backendConn)
			access.Unlock()
			go func() {
				_, _ = io.Copy(backendConn, clientConn)
				_ = backendConn.Close()
				_ = clientConn.Close()
			}()
			go func() {
				_, _ = io.Copy(clientConn, backendConn)
				_ = clientConn.Close()
				_ = backendConn.Close()
			}()
		}
	}()
	return proxyPort, func() { _ = listener.Close() }
}

func TestMieruFallbackDoesNotStopExistingPrimarySession(t *testing.T) {
	serverPort := freePortForBothTransports(t)
	startTestInbound(t, inboundOptionsForPort(serverPort, "both"))
	proxyPort, stopNewPrimaryConnections := startTCPProxy(t, serverPort)
	options := fallbackOutboundOptions(proxyPort, "tcp", serverPort, "udp")
	options.Multiplexing = "off"
	outboundInstance, _ := startTestOutbound(t, options)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	destination := M.ParseSocksaddr("192.0.2.28:443")
	first, err := outboundInstance.DialContext(ctx, N.NetworkTCP, destination)
	require.NoError(t, err)
	readAndWriteEcho(t, first, "primary-session")

	// New connections can no longer reach the primary proxy, but the relay
	// carrying the first session is still open. With mux off, the next SDK
	// session is forced to establish a new primary underlay and fail.
	stopNewPrimaryConnections()
	second, err := outboundInstance.DialContext(ctx, N.NetworkTCP, destination)
	require.NoError(t, err)
	readAndWriteEcho(t, second, "fallback-session")
	require.Equal(t, fallbackSecondary, outboundInstance.fallback.snapshot().Preferred)

	// The ordinary fallback must not stop the primary client or migrate/kill
	// the session that was already established.
	readAndWriteEcho(t, first, "primary-session-still-alive")
	require.True(t, outboundInstance.primary.client.IsRunning())
	require.NoError(t, first.Close())
	require.NoError(t, second.Close())
	require.NoError(t, outboundInstance.Close())
}

func TestMieruFallbackConcurrentNewSessions(t *testing.T) {
	primaryPort := freePortForBothTransports(t)
	secondaryPort := differentFreePort(t, primaryPort)
	startTestInbound(t, inboundOptionsForPort(secondaryPort, "udp"))
	outboundInstance, _ := startTestOutbound(t, fallbackOutboundOptions(primaryPort, "tcp", secondaryPort, "udp"))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	destination := M.ParseSocksaddr("192.0.2.31:443")
	const connections = 50
	errCh := make(chan error, connections)
	var waitGroup sync.WaitGroup
	for i := 0; i < connections; i++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			conn, err := outboundInstance.DialContext(ctx, N.NetworkTCP, destination)
			if err != nil {
				errCh <- err
				return
			}
			defer conn.Close()
			payload := []byte("concurrent-fallback")
			if _, err = conn.Write(payload); err != nil {
				errCh <- err
				return
			}
			buffer := make([]byte, len(payload))
			if _, err = io.ReadFull(conn, buffer); err != nil {
				errCh <- err
				return
			}
			if string(buffer) != string(payload) {
				errCh <- fmt.Errorf("echo mismatch")
			}
		}()
	}
	waitGroup.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}
	snapshot := outboundInstance.fallback.snapshot()
	require.Equal(t, fallbackSecondary, snapshot.Preferred)
	require.GreaterOrEqual(t, snapshot.SecondaryWins, uint64(connections))
	require.NoError(t, outboundInstance.Close())
}

func TestMieruFallbackConcurrentRecoveryProbeSingleFlight(t *testing.T) {
	primaryPort := freePortForBothTransports(t)
	secondaryPort := differentFreePort(t, primaryPort)
	startTestInbound(t, inboundOptionsForPort(secondaryPort, "udp"))
	outboundInstance, _ := startTestOutbound(t, fallbackOutboundOptions(primaryPort, "tcp", secondaryPort, "udp"))
	now := time.Unix(7000, 0)
	outboundInstance.fallback.now = func() time.Time { return now }

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	destination := M.ParseSocksaddr("192.0.2.32:443")
	first, err := outboundInstance.DialContext(ctx, N.NetworkTCP, destination)
	require.NoError(t, err)
	require.NoError(t, first.Close())
	require.Equal(t, fallbackSecondary, outboundInstance.fallback.snapshot().Preferred)

	now = now.Add(fallbackCooldown + time.Second)
	const connections = 50
	errCh := make(chan error, connections)
	var waitGroup sync.WaitGroup
	for i := 0; i < connections; i++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			conn, dialErr := outboundInstance.DialContext(ctx, N.NetworkTCP, destination)
			if dialErr != nil {
				errCh <- dialErr
				return
			}
			_ = conn.Close()
		}()
	}
	waitGroup.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}
	require.Equal(t, uint64(1), outboundInstance.fallback.snapshot().Probes,
		"one cooldown window must allow exactly one primary recovery probe")
	require.Equal(t, fallbackSecondary, outboundInstance.fallback.snapshot().Preferred)
}

func TestMieruFallbackCloseStopsBothRuntimes(t *testing.T) {
	primaryPort := freePortForBothTransports(t)
	secondaryPort := differentFreePort(t, primaryPort)
	startTestInbound(t, inboundOptionsForPort(secondaryPort, "udp"))
	outboundInstance, _ := startTestOutbound(t, fallbackOutboundOptions(primaryPort, "tcp", secondaryPort, "udp"))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, err := outboundInstance.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr("192.0.2.29:443"))
	require.NoError(t, err)
	require.Equal(t, fallbackSecondary, outboundInstance.fallback.snapshot().Preferred)

	require.NoError(t, outboundInstance.Close())
	require.False(t, outboundInstance.primary.client.IsRunning())
	require.False(t, outboundInstance.secondary.client.IsRunning())
	require.Zero(t, outboundInstance.packetDialer.packetConnCount())
	require.Zero(t, outboundInstance.secondary.packetDialer.packetConnCount())

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(3*time.Second)))
	buffer := make([]byte, 1)
	_, err = conn.Read(buffer)
	require.Error(t, err, "Close must terminate the session that was using the fallback")
}

func TestMieruFallbackCloseWinsAgainstInFlightDial(t *testing.T) {
	primaryPort := freePortForBothTransports(t)
	secondaryPort := differentFreePort(t, primaryPort)
	startBlackholeTCP(t, primaryPort)
	startTestInbound(t, inboundOptionsForPort(secondaryPort, "udp"))
	outboundInstance, _ := startTestOutbound(t, fallbackOutboundOptions(primaryPort, "tcp", secondaryPort, "udp"))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	dialDone := make(chan error, 1)
	go func() {
		_, dialErr := outboundInstance.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr("192.0.2.30:443"))
		dialDone <- dialErr
	}()
	time.Sleep(200 * time.Millisecond)
	start := time.Now()
	require.NoError(t, outboundInstance.Close())
	require.Less(t, time.Since(start), 7*time.Second, "Close must not inherit the SDK's 10s handshake timeout")
	select {
	case dialErr := <-dialDone:
		require.Error(t, dialErr, "a dial that was in flight when Close won must not report success")
	case <-time.After(15 * time.Second):
		t.Fatal("in-flight dial did not terminate after Close")
	}
	require.False(t, outboundInstance.primary.client.IsRunning())
	require.False(t, outboundInstance.secondary.client.IsRunning())
}
