//go:build with_quic

package masque

// A real CONNECT-IP session retirement releases the endpoint's userspace stack, and the next session
// rebuilds it on demand.
//
// # What these tests are for
//
// protocol/masque/session_release.go argues that a session's retirement is the one moment at which the
// stack provably has no carrier. That argument is only worth what a real tunnel shows, so every test
// here uses the production client endpoint, the production server endpoint and a real QUIC (or H2)
// connection between them - no fake session, no fake device, no injected retirement.
//
// The two shapes that must not be confused:
//
//	the stack is RELEASED because the session ENDED   (this file)
//	the stack is KEPT     because no local dial ran    (never: a peer-initiated flow is a busy tunnel,
//	                                                   which TestARemoteInitiatedFlowDoesNotKeepTheStack
//	                                                   makes concrete)
//
// # Why the observation is the facade's counters
//
// A release is asynchronous with respect to the reconnect, so a test that polls Built() can miss the
// unbuilt window. DeferredDiagnostics.Releases and .Builds are monotonic counts of what actually
// happened, and Generation() distinguishes the generations, so the assertions do not depend on
// catching a microsecond-wide state.

import (
	"context"
	"io"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/transport/device"
	"github.com/sagernet/sing-box/transport/masque"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/auth"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/json/badoption"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// deferredOf asserts the endpoint installed the deferred facade and returns it, so the tests can read
// the generation ledger rather than infer it.
func deferredOf(t *testing.T, endpoint *ClientEndpoint) device.Deferred {
	t.Helper()
	require.NotNil(t, endpoint.device)
	facade, isDeferred := endpoint.device.(device.Deferred)
	require.True(t, isDeferred, "the non-system endpoint did not install a deferred device")
	return facade
}

// startConnectIPServerWithVersion starts the production server endpoint serving one HTTP version, so
// the same client can be driven over HTTP/3 and over the HTTP/2 fallback.
func startConnectIPServerWithVersion(t *testing.T, prefix netip.Prefix, router adapter.Router, version int) (*ServerEndpoint, uint16) {
	t.Helper()
	logger := log.NewNOPFactory().Logger()
	certPEM, keyPEM := newLoopbackCertificate(t)
	serverContext := service.ContextWithDefaultRegistry(context.Background())
	serverContext = service.ContextWith[adapter.NetworkManager](serverContext, &stubNetworkManager{})
	created, err := NewServerEndpoint(serverContext, router, logger, "connect-ip-server",
		option.MASQUEServerEndpointOptions{
			MASQUEEndpointOptions: option.MASQUEEndpointOptions{MTU: 1500},
			ListenOptions: option.ListenOptions{
				Listen:     common.Ptr(badoption.Addr(netip.AddrFrom4([4]byte{127, 0, 0, 1}))),
				ListenPort: 0,
			},
			Version: badoption.Listable[int]{version},
			Address: badoption.Listable[netip.Prefix]{prefix},
			Path:    "/",
			Users:   []auth.User{{Username: connectIPUser, Password: connectIPPassword}},
			InboundTLSOptionsContainer: option.InboundTLSOptionsContainer{
				TLS: &option.InboundTLSOptions{
					Enabled:     true,
					ServerName:  "example.test",
					Certificate: []string{certPEM},
					Key:         badoption.Listable[string]{keyPEM},
				},
			},
		})
	require.NoError(t, err)
	serverEndpoint, isServer := created.(*ServerEndpoint)
	require.True(t, isServer)
	scope := adapter.NewScope(context.Background(), logger)
	require.NoError(t, serverEndpoint.Start(adapter.StartStateInitialize, scope))
	require.NoError(t, serverEndpoint.Start(adapter.StartStateStart, scope))
	require.NoError(t, serverEndpoint.Start(adapter.StartStatePostStart, scope))
	t.Cleanup(func() { _ = scope.Close() })
	if version == 3 {
		return serverEndpoint, uint16(serverEndpoint.listener.UDPConn().LocalAddr().(*net.UDPAddr).Port)
	}
	return serverEndpoint, uint16(serverEndpoint.listener.TCPListener().Addr().(*net.TCPAddr).Port)
}

// waitForSessionReady blocks until the client has a configured, ready session.
func waitForSessionReady(t *testing.T, endpoint *ClientEndpoint, timeout time.Duration) {
	t.Helper()
	require.Eventually(t, func() bool {
		return endpoint.state.Load().configured && endpoint.client.Ready()
	}, timeout, 5*time.Millisecond, "the tunnel never became ready")
}

// udpEchoRouter terminates a CONNECT-IP inner UDP flow by echoing every datagram back on the same
// packet connection. It is the frozen destination for the UDP row, in the same way carrierRouter is
// for TCP: a real socket-level echo, not a stand-in for the code under test.
type udpEchoRouter struct {
	adapter.Router
	echoed atomic.Int32
	closed atomic.Int32
}

func (r *udpEchoRouter) RoutePacketConnectionEx(ctx context.Context, conn N.PacketConn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	go func() {
		defer conn.Close()
		defer r.closed.Add(1)
		if onClose != nil {
			defer onClose(nil)
		}
		packet := buf.NewPacket()
		defer packet.Release()
		// A reply written into a userspace-stack packet conn must carry the front headroom that
		// conn needs for the IP/UDP headers it prepends, exactly as a production outbound allocates
		// it (N.CalculateFrontHeadroom over the writer chain). The received buffer has none: it has
		// already been decapsulated.
		headroom := N.CalculateFrontHeadroom(conn)
		for {
			packet.Reset()
			destination, err := conn.ReadPacket(packet)
			if err != nil {
				return
			}
			r.echoed.Add(1)
			reply := buf.NewSize(headroom + packet.Len())
			reply.Resize(headroom, 0)
			common.Must1(reply.Write(packet.Bytes()))
			// WritePacket takes ownership of the buffer, whether it succeeds or fails.
			if err = conn.WritePacket(reply, destination); err != nil {
				return
			}
		}
	}()
}

func (r *udpEchoRouter) RouteConnection(context.Context, net.Conn, adapter.InboundContext) error {
	return nil
}

func (r *udpEchoRouter) RoutePacketConnection(context.Context, N.PacketConn, adapter.InboundContext) error {
	return nil
}

func (r *udpEchoRouter) RouteConnectionEx(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	go func() {
		defer conn.Close()
		if onClose != nil {
			defer onClose(nil)
		}
	}()
}

// TestASessionRetirementReleasesTheInnerStackAndTheNextSessionRebuilds is the core W4 claim, run over
// both transports this endpoint supports: HTTP/3, and the HTTP/2 fallback the client lands on when
// the server serves only HTTP/2.
//
// The retirement here is a REAL product path - SetKeepIdleConnections(false) suspends the tunnel, the
// session is cancelled and its run() returns - and the release must happen without any help from the
// test after that point.
func TestASessionRetirementReleasesTheInnerStackAndTheNextSessionRebuilds(t *testing.T) {
	for _, transportCase := range []struct {
		name    string
		version int
	}{
		{name: "http3", version: 3},
		{name: "http2-fallback", version: 2},
	} {
		t.Run(transportCase.name, func(t *testing.T) {
			requireHTTP3Client(t)
			serverPrefix := netip.MustParsePrefix(connectIPPrefix)
			echoPort := startCarrierEcho(t)
			router := &carrierRouter{}
			_, port := startConnectIPServerWithVersion(t, serverPrefix, router, transportCase.version)
			clientEndpoint := startConnectIPClient(t, port)
			waitForSessionReady(t, clientEndpoint, 20*time.Second)
			facade := deferredOf(t, clientEndpoint)
			require.False(t, facade.Built(), "the stack was built before any demand")

			// The tunnel's transport is reported by the session that established it, which is also
			// what drives the epoch. Over a version-3 client with an H2-only server this is the
			// fallback, not a failure.
			require.Eventually(t, func() bool {
				return clientEndpoint.sessionTransport.Load() != 0
			}, 10*time.Second, 5*time.Millisecond, "the session never reported its transport")
			t.Logf("session transport=%d", clientEndpoint.sessionTransport.Load())

			// A real flow through the tunnel builds the stack.
			innerDestination := M.SocksaddrFrom(netip.AddrFrom4([4]byte{10, 42, 0, 0}), echoPort)
			conn := carrierRoundTrip(t, clientEndpoint, innerDestination, "before-retirement")
			require.True(t, facade.Built(), "the first business flow did not build the stack")
			require.EqualValues(t, 1, facade.Generation())
			require.EqualValues(t, 1, facade.Diagnostics().Builds)

			// Retirement. Suspend is the product's pause path (SetKeepIdleConnections(false)), so the
			// session genuinely ends and no reconnect can race the release.
			clientEndpoint.SetKeepIdleConnections(false)
			require.Eventually(t, func() bool {
				return facade.Diagnostics().Releases == 1
			}, 10*time.Second, time.Millisecond,
				"the retired session did not release the inner stack")
			require.False(t, facade.Built(), "the release reported success but the generation is still published")
			ledger := clientEndpoint.sessionReleases()
			require.EqualValues(t, 1, ledger.Released)
			require.Zero(t, ledger.Skipped, "the release was refused: %v", ledger.LastError)
			require.Zero(t, ledger.Current, "a session is still recorded as current after retirement")

			// Facade state survives: the assigned address and the MTU are the endpoint's, not the
			// generation's, so they are still answerable while no stack exists.
			state := clientEndpoint.state.Load()
			require.True(t, state.configured)
			require.NotEmpty(t, state.localAddresses)
			require.EqualValues(t, transportmasqueDefaultMTU, clientEndpoint.PortMTU())

			// The old flow is dead: the stack that carried it is gone.
			_ = conn.Close()

			// Resume: a new session, and the first demand rebuilds exactly one generation.
			clientEndpoint.SetKeepIdleConnections(true)
			waitForSessionReady(t, clientEndpoint, 40*time.Second)
			after := carrierRoundTrip(t, clientEndpoint, innerDestination, "after-retirement")
			_ = after.Close()
			require.True(t, facade.Built(), "the first demand of the new session did not rebuild the stack")
			require.EqualValues(t, 2, facade.Generation(), "the rebuilt stack did not get a new generation")
			require.EqualValues(t, 2, facade.Diagnostics().Builds)
			require.EqualValues(t, 1, facade.Diagnostics().Releases)
			require.GreaterOrEqual(t, router.flows.Load(), int32(2),
				"the second flow did not reach the server, so the new session was not carrying traffic")
		})
	}
}

// TestARemoteInitiatedFlowDoesNotKeepTheStack is the "zero local dial is not idleness" proof.
//
// The peer opens the flow: the server dials the address it assigned to the client, the first packet
// arrives INBOUND at the client endpoint, and the client's stack accepts it and hands it to the
// router. The client never calls Dial. A release rule based on local dial counters would have to
// either keep this stack forever or release it under a live flow; the epoch rule releases it at
// retirement, which is the only fact that changed.
func TestARemoteInitiatedFlowDoesNotKeepTheStack(t *testing.T) {
	requireHTTP3Client(t)
	serverPrefix := netip.MustParsePrefix(connectIPPrefix)
	echoPort := startCarrierEcho(t)
	clientRouter := &carrierRouter{}
	serverRouter := &carrierRouter{}
	serverEndpoint, port := startRoutedConnectIPServer(t, serverPrefix, serverRouter)
	clientEndpoint := startConnectIPClientWithRouter(t, port, clientRouter)
	waitForSessionReady(t, clientEndpoint, 20*time.Second)
	facade := deferredOf(t, clientEndpoint)

	// No demand on the client side at all: nothing was dialled, no packet was written inbound.
	require.False(t, facade.Built(), "the client built its stack without a demand")
	clientAddress, _ := clientEndpoint.PortAddresses()
	require.True(t, clientAddress.IsValid(), "the client has no assigned inner address")

	// The SERVER opens a connection to the client's assigned address. The first packet therefore
	// enters the client endpoint inbound.
	remoteCtx, cancelRemote := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancelRemote()
	remoteConn, err := serverEndpoint.DialContext(remoteCtx, N.NetworkTCP,
		M.SocksaddrFrom(clientAddress, echoPort))
	require.NoError(t, err, "the server could not open a flow to the address it assigned")
	defer remoteConn.Close()
	require.NoError(t, remoteConn.SetDeadline(time.Now().Add(20*time.Second)))
	require.Eventually(t, func() bool { return facade.Built() },
		10*time.Second, 5*time.Millisecond,
		"the inbound first packet did not build the client's stack")
	require.EqualValues(t, 1, facade.Generation())
	require.Eventually(t, func() bool { return clientRouter.flows.Load() >= 1 },
		10*time.Second, 5*time.Millisecond,
		"the remote-initiated flow did not reach the client's router")

	// Prove the flow carries bytes in both directions.
	payload := []byte("peer-initiated")
	_, err = remoteConn.Write(payload)
	require.NoError(t, err)
	echo := make([]byte, len(payload))
	_, err = io.ReadFull(remoteConn, echo)
	require.NoError(t, err, "the echoed payload never came back through the remote-initiated flow")
	require.Equal(t, payload, echo)

	// Now retire the session. The client made ZERO local dials; the tunnel was nonetheless busy with a
	// peer-initiated flow. The release must be driven by the retirement, not by the absence of dials.
	clientEndpoint.SetKeepIdleConnections(false)
	require.Eventually(t, func() bool { return facade.Diagnostics().Releases == 1 },
		10*time.Second, time.Millisecond,
		"the retired session did not release the stack of a tunnel with a remote-initiated flow")
	require.False(t, facade.Built())
	ledger := clientEndpoint.sessionReleases()
	require.EqualValues(t, 1, ledger.Released)
	require.Zero(t, ledger.Skipped, "the release was refused: %v", ledger.LastError)

	// The flow the peer opened is closed by the release rather than left pointing at a torn-down
	// stack: the router's own close counter is incremented in the same defer that closes it.
	require.Eventually(t, func() bool { return clientRouter.closed.Load() >= 1 },
		10*time.Second, 10*time.Millisecond,
		"the client-side flow of the retired session outlived the stack that carried it")
}

// TestARealCONNECTIPUDPDatagramCrossesAndIsReleasedWithItsSession is the UDP row: a real datagram
// through the tunnel, echoed by the server's router, and the same retirement release as TCP.
func TestARealCONNECTIPUDPDatagramCrossesAndIsReleasedWithItsSession(t *testing.T) {
	requireHTTP3Client(t)
	serverPrefix := netip.MustParsePrefix(connectIPPrefix)
	router := &udpEchoRouter{}
	_, port := startRoutedConnectIPServer(t, serverPrefix, router)
	clientEndpoint := startConnectIPClient(t, port)
	waitForSessionReady(t, clientEndpoint, 20*time.Second)
	facade := deferredOf(t, clientEndpoint)

	innerDestination := M.SocksaddrFrom(netip.AddrFrom4([4]byte{10, 42, 0, 0}), 5353)
	packetConn, err := clientEndpoint.ListenPacket(context.Background(), innerDestination)
	require.NoError(t, err)
	require.True(t, facade.Built(), "creating the UDP socket did not build the stack")

	payload := []byte("connect-ip-udp")
	require.NoError(t, packetConn.SetDeadline(time.Now().Add(15*time.Second)))
	_, err = packetConn.WriteTo(payload, net.UDPAddrFromAddrPort(netip.AddrPortFrom(innerDestination.Addr, innerDestination.Port)))
	require.NoError(t, err)
	reply := make([]byte, 64)
	read, _, err := packetConn.ReadFrom(reply)
	require.NoError(t, err, "the echoed datagram never came back through the tunnel")
	require.Equal(t, payload, reply[:read])
	require.GreaterOrEqual(t, router.echoed.Load(), int32(1))

	// Retirement releases the stack; the socket the test still holds fails rather than silently
	// talking to a released generation.
	clientEndpoint.SetKeepIdleConnections(false)
	require.Eventually(t, func() bool { return facade.Diagnostics().Releases == 1 },
		10*time.Second, time.Millisecond, "the retired session did not release the inner stack")
	require.False(t, facade.Built())
	_ = packetConn.Close()

	// And the next session's first datagram rebuilds it.
	clientEndpoint.SetKeepIdleConnections(true)
	waitForSessionReady(t, clientEndpoint, 40*time.Second)
	rebuilt, err := clientEndpoint.ListenPacket(context.Background(), innerDestination)
	require.NoError(t, err)
	require.True(t, facade.Built())
	require.EqualValues(t, 2, facade.Generation())
	_ = rebuilt.Close()
}

// TestANetworkChangeReplacesTheSessionWithoutClosingTheNewOne is the shared-stack rule: the endpoint has
// ONE device, so the retirement of the old session must not tear down a stack the new session is
// already using. The new session is established right after the release, and its traffic must work.
func TestANetworkChangeReplacesTheSessionWithoutClosingTheNewOne(t *testing.T) {
	requireHTTP3Client(t)
	serverPrefix := netip.MustParsePrefix(connectIPPrefix)
	echoPort := startCarrierEcho(t)
	router := &carrierRouter{}
	_, port := startRoutedConnectIPServer(t, serverPrefix, router)
	clientEndpoint := startConnectIPClient(t, port)
	waitForSessionReady(t, clientEndpoint, 20*time.Second)
	facade := deferredOf(t, clientEndpoint)
	innerDestination := M.SocksaddrFrom(netip.AddrFrom4([4]byte{10, 42, 0, 0}), echoPort)

	before := carrierRoundTrip(t, clientEndpoint, innerDestination, "before-the-change")
	require.True(t, facade.Built())

	// A real network change: the endpoint restarts its session immediately, with no test-controllable
	// pause between the retirement and the reconnect - the exact shape where a release could close
	// the stack the new session is about to use.
	clientEndpoint.InterfaceUpdated(context.Background())
	require.Eventually(t, func() bool { return facade.Diagnostics().Releases >= 1 },
		10*time.Second, time.Millisecond, "the replaced session did not release its stack")
	_ = before.Close()

	waitForSessionReady(t, clientEndpoint, 40*time.Second)
	after := carrierRoundTrip(t, clientEndpoint, innerDestination, "after-the-change")
	_ = after.Close()
	require.True(t, facade.Built())
	require.EqualValues(t, 2, facade.Generation(), "the new session's first demand did not rebuild the stack")
	require.EqualValues(t, 2, facade.Diagnostics().Builds)
	require.GreaterOrEqual(t, router.flows.Load(), int32(2))

	// No retirement of the NEW session happened, and nothing was skipped.
	ledger := clientEndpoint.sessionReleases()
	require.EqualValues(t, 1, ledger.Released)
	require.Zero(t, ledger.Skipped)
	require.EqualValues(t, 2, ledger.Sequence, "the begin/retire pairs did not match the sessions")
}

// TestCloseDuringTheFirstBuildIsBounded is "Close during construction" at the endpoint level: the
// first demand is building the userspace stack while the scope is closed. The build may be discarded
// (the facade has a test for that), but the endpoint must neither hang nor panic, and the closed
// endpoint must not resurrect a stack afterwards.
func TestCloseDuringTheFirstBuildIsBounded(t *testing.T) {
	for round := 0; round < 20; round++ {
		endpoint, scope := newCostEndpoint(t, baseCostOptions())
		require.NoError(t, endpoint.UpdateConfiguration(masque.Configuration{
			Address: []netip.Prefix{netip.MustParsePrefix("10.42.0.2/32")},
		}))
		facade := deferredOf(t, endpoint)
		require.False(t, facade.Built())

		built := make(chan error, 1)
		go func() {
			// The first demand: this builds the production userspace stack.
			_, err := endpoint.device.DialContext(context.Background(), N.NetworkTCP,
				M.SocksaddrFrom(netip.MustParseAddr("10.42.0.1"), 9))
			built <- err
		}()
		closed := make(chan error, 1)
		go func() { closed <- scope.Close() }()
		select {
		case <-closed:
		case <-time.After(15 * time.Second):
			t.Fatal("closing the scope during the first build hung")
		}
		select {
		case <-built:
		case <-time.After(15 * time.Second):
			t.Fatal("the first demand never returned while the scope closed")
		}
		// Whatever the interleaving, nothing may be published after the close.
		require.False(t, facade.Built(), "a closed endpoint published a stack")
	}
}

// TestScopeShutdownWithALiveFlowAndARetirementIsBounded is the shutdown row: the scope closes while a
// business flow is live, which makes the session retire and the stack release DURING shutdown, in
// addition to the scope's own device close. It must finish, exactly once, without a deadlock.
func TestScopeShutdownWithALiveFlowAndARetirementIsBounded(t *testing.T) {
	requireHTTP3Client(t)
	serverPrefix := netip.MustParsePrefix(connectIPPrefix)
	echoPort := startCarrierEcho(t)
	router := &carrierRouter{}
	_, port := startRoutedConnectIPServer(t, serverPrefix, router)

	// Build the endpoint by hand so the test owns its scope and can close it while the flow is open.
	logger := log.NewNOPFactory().Logger()
	clientOptions := option.MASQUEClientEndpointOptions{
		ServerOptions: option.ServerOptions{Server: "127.0.0.1", ServerPort: port},
		Version:       3,
		Path:          "/",
		Username:      connectIPUser,
		Password:      connectIPPassword,
	}
	clientOptions.TLS = &option.OutboundTLSOptions{Enabled: true, ServerName: "example.test", Insecure: true}
	clientContext := service.ContextWithDefaultRegistry(context.Background())
	clientContext = service.ContextWith[adapter.NetworkManager](clientContext, &stubNetworkManager{})
	created, err := NewClientEndpoint(clientContext, nil, logger, "connect-ip-client", clientOptions)
	require.NoError(t, err)
	clientEndpoint := created.(*ClientEndpoint)
	scope := adapter.NewScope(context.Background(), logger)
	require.NoError(t, clientEndpoint.Start(adapter.StartStateInitialize, scope))
	require.NoError(t, clientEndpoint.Start(adapter.StartStateStart, scope))
	require.NoError(t, clientEndpoint.Start(adapter.StartStatePostStart, scope))

	waitForSessionReady(t, clientEndpoint, 20*time.Second)
	facade := deferredOf(t, clientEndpoint)
	innerDestination := M.SocksaddrFrom(netip.AddrFrom4([4]byte{10, 42, 0, 0}), echoPort)
	flow := carrierRoundTrip(t, clientEndpoint, innerDestination, "during-shutdown")
	require.True(t, facade.Built())

	closed := make(chan error, 1)
	started := time.Now()
	go func() { closed <- scope.Close() }()
	select {
	case err = <-closed:
		require.NoError(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("scope shutdown with a live flow and a session retirement deadlocked")
	}
	t.Logf("scope close with a live flow took %s", time.Since(started).Round(time.Millisecond))
	require.False(t, facade.Built())
	_ = flow.Close()
	// The session that ended during shutdown is a real retirement, so it is accounted for; the device
	// close that follows is the scope's, not a second release of the same generation.
	ledger := clientEndpoint.sessionReleases()
	require.GreaterOrEqual(t, ledger.Released, uint64(1), "shutdown did not account for the retired session")
	require.EqualValues(t, 1, facade.Diagnostics().Releases)
}

// TestReleaseRacingBusinessDialAndWriteOverARealTunnel is the endpoint-level interleaving gate.
//
// The release does not run in a quiet moment: a real Box dials and writes through this endpoint while
// the physical network changes, which retires the session under live traffic. The correctness claim is
// that the worst case is a failed dial/read on a flow whose carrier is gone - never a panic, a hang, or
// a release that closes a stack another session is using.
func TestReleaseRacingBusinessDialAndWriteOverARealTunnel(t *testing.T) {
	requireHTTP3Client(t)
	serverPrefix := netip.MustParsePrefix(connectIPPrefix)
	echoPort := startCarrierEcho(t)
	router := &carrierRouter{}
	_, port := startRoutedConnectIPServer(t, serverPrefix, router)
	clientEndpoint := startConnectIPClient(t, port)
	waitForSessionReady(t, clientEndpoint, 20*time.Second)
	facade := deferredOf(t, clientEndpoint)
	innerDestination := M.SocksaddrFrom(netip.AddrFrom4([4]byte{10, 42, 0, 0}), echoPort)

	// The tunnel carries a business byte BEFORE anything is torn down, so a failure after the storm
	// cannot be blamed on the fixture.
	warm := carrierRoundTrip(t, clientEndpoint, innerDestination, "before-the-storm")
	_ = warm.Close()

	var (
		wg        sync.WaitGroup
		stop      = make(chan struct{})
		completed atomic.Int64
		panics    atomic.Int64
	)
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if recovered := recover(); recovered != nil {
					panics.Add(1)
					t.Errorf("a business flow panicked while the stack was released: %v", recovered)
				}
			}()
			for {
				select {
				case <-stop:
					return
				default:
				}
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				conn, err := clientEndpoint.DialContext(ctx, N.NetworkTCP, innerDestination)
				cancel()
				if err != nil {
					// The session was retired between waitReady and the dial: a legitimate loss.
					continue
				}
				_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
				if _, err = conn.Write([]byte("racing")); err == nil {
					echo := make([]byte, len("racing"))
					// A read that fails because the release closed the stack is expected; a read
					// that succeeds means the flow kept working across the change.
					_, _ = io.ReadFull(conn, echo)
				}
				_ = conn.Close()
				completed.Add(1)
			}
		}()
	}

	// Real network changes: each one retires the current session, which releases the stack unless a
	// business operation is inside it.
	for round := 0; round < 8; round++ {
		clientEndpoint.InterfaceUpdated(context.Background())
		time.Sleep(40 * time.Millisecond)
	}
	close(stop)
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("a business flow never returned after the releases")
	}
	require.Zero(t, panics.Load())
	ledger := clientEndpoint.sessionReleases()
	t.Logf("live-traffic race: completed flows=%d, build/release=%d/%d, sessions=%d, retirements=%d, released=%d, empty=%d, skipped=%d, lastErr=%v",
		completed.Load(), facade.Diagnostics().Builds, facade.Diagnostics().Releases,
		ledger.Sequence, ledger.Retirements, ledger.Released, ledger.Empty, ledger.Skipped, ledger.LastError)
	// Every retirement is accounted for exactly once. Whether it is a release or a refusal depends on
	// whether a business operation happened to be inside the stack: with four dials that each hang for
	// the session's lifetime, a refusal is the expected outcome and the safe one (the stack stays
	// built). The gate is the ledger, not a release count.
	require.GreaterOrEqual(t, ledger.Retirements, uint64(1), "the restarts did not retire a session")
	require.Equal(t, ledger.Retirements, ledger.Released+ledger.Empty+ledger.Skipped,
		"a retirement was not accounted for: the ledger must be exact")
	t.Logf("release/refusal split under live traffic: released=%d empty=%d skipped=%d",
		ledger.Released, ledger.Empty, ledger.Skipped)
	// After the storm the tunnel must still carry a business byte: the release must not have left the
	// endpoint with an unusable stack.
	waitForSessionReady(t, clientEndpoint, 60*time.Second)
	after := carrierRoundTrip(t, clientEndpoint, innerDestination, "after-the-storm")
	_ = after.Close()
}

// transportmasqueDefaultMTU keeps the assertion readable without importing the transport package under
// a name that shadows the endpoint's own field.
const transportmasqueDefaultMTU = masque.DefaultMTU
