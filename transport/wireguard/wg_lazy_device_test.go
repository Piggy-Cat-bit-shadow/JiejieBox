package wireguard

// W5: selective internal lazy release of the WireGuard HEAVY chain.
//
// The heavy part of a live endpoint is the wireguard-go Device, not the gVisor stack: its
// RoutineReadFromTUN preallocates BatchSize() x MaxMessageSize = 128 x 65,535 B = ~8.4 MB, plus the
// queues and per-peer workers (MEASURED in TestWGSelectiveLazyResourceLedger). The stack costs ~47 KB
// and owns every established flow, so only the Device is released on suspend and only the eligible
// shape takes that path. The six proofs this file has to pin are named on each test.

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/dialer"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-tun"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	wgTun "github.com/sagernet/wireguard-go/tun"

	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// fixtures
// ---------------------------------------------------------------------------

func lazyKeyPair(t *testing.T) (privateKey string, publicKey string) {
	t.Helper()
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	require.NoError(t, err)
	return base64.StdEncoding.EncodeToString(key.Bytes()), base64.StdEncoding.EncodeToString(key.PublicKey().Bytes())
}

func lazyDialer(t *testing.T) N.Dialer {
	t.Helper()
	created, err := dialer.NewDefault(context.Background(), option.DialerOptions{})
	require.NoError(t, err)
	return created
}

// lazySinglePeerEndpoint builds the eligible shape: private userspace, no listen_port, no keepalive,
// one peer with an IP endpoint.
func lazySinglePeerEndpoint(t *testing.T, tag string) *Endpoint {
	t.Helper()
	privateKey, _ := lazyKeyPair(t)
	_, peerPublicKey := lazyKeyPair(t)
	endpoint, err := NewEndpoint(EndpointOptions{
		Context:    context.Background(),
		Logger:     log.NewNOPFactory().Logger(),
		Dialer:     lazyDialer(t),
		Tag:        tag,
		PrivateKey: privateKey,
		MTU:        1408,
		Address:    []netip.Prefix{netip.MustParsePrefix("10.0.0.1/24")},
		Peers: []PeerOptions{{
			Endpoint:   M.ParseSocksaddrHostPort("192.0.2.1", 51820),
			PublicKey:  peerPublicKey,
			AllowedIPs: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")},
		}},
	})
	require.NoError(t, err)
	require.NoError(t, endpoint.Initialize(nil))
	t.Cleanup(func() { _ = endpoint.Close() })
	return endpoint
}

func lazyStart(t *testing.T, endpoint *Endpoint) {
	t.Helper()
	require.NoError(t, endpoint.Start(false), "the eligible shape must start exactly as before")
}

// lazyEchoHandler is the server side of the real pair: it accepts the tunnel's TCP flows and echoes
// every byte. It is a real tun.Handler, so the packets really cross two wireguard-go Devices.
type lazyEchoHandler struct {
	flows atomic.Int32
}

func (h *lazyEchoHandler) JudgeFlow(network uint8, source netip.AddrPort, destination netip.AddrPort, firstPacket []byte) tun.FlowVerdict {
	return tun.FlowVerdict{Action: tun.ActionAccept}
}

func (h *lazyEchoHandler) NewDNSPacket(payload []byte, source M.Socksaddr, destination M.Socksaddr, writer N.PacketWriter) {
}

func (h *lazyEchoHandler) NewConnectionEx(ctx context.Context, conn net.Conn, source M.Socksaddr, destination M.Socksaddr, onClose N.CloseHandlerFunc) {
	h.flows.Add(1)
	go func() {
		defer conn.Close()
		if onClose != nil {
			defer onClose(nil)
		}
		buffer := make([]byte, 4096)
		for {
			read, readErr := conn.Read(buffer)
			if read > 0 {
				if _, writeErr := conn.Write(buffer[:read]); writeErr != nil {
					return
				}
			}
			if readErr != nil {
				return
			}
		}
	}()
}

func (h *lazyEchoHandler) NewPacketConnectionEx(ctx context.Context, conn N.PacketConn, source M.Socksaddr, destination M.Socksaddr, onClose N.CloseHandlerFunc) {
}

// lazyPair is a real WireGuard pair through the production endpoints: client (the eligible shape)
// and server (listening, with the echo handler). No test double sits on the data plane.
type lazyPair struct {
	client      *Endpoint
	server      *Endpoint
	serverPort  uint16
	handler     *lazyEchoHandler
	destination M.Socksaddr
}

func lazyStartPair(t *testing.T, amneziaWG option.AmneziaWGOptions) *lazyPair {
	t.Helper()
	serverPrivate, serverPublic := lazyKeyPair(t)
	clientPrivate, clientPublic := lazyKeyPair(t)
	handler := &lazyEchoHandler{}
	server, err := NewEndpoint(EndpointOptions{
		Context:          context.Background(),
		Logger:           log.NewNOPFactory().Logger(),
		Dialer:           lazyDialer(t),
		Tag:              "wg-server",
		PrivateKey:       serverPrivate,
		MTU:              1408,
		UDPTimeout:       5 * time.Minute,
		ICMPTimeout:      5 * time.Minute,
		Address:          []netip.Prefix{netip.MustParsePrefix("10.66.0.1/24")},
		AmneziaWG:        amneziaWG,
		Handler:          handler,
		Peers: []PeerOptions{{
			PublicKey:  clientPublic,
			AllowedIPs: []netip.Prefix{netip.MustParsePrefix("10.66.0.2/32")},
		}},
	})
	require.NoError(t, err)
	require.NoError(t, server.Initialize(nil))
	t.Cleanup(func() { _ = server.Close() })
	require.NoError(t, server.Start(false))
	require.NotNil(t, server.device.Load(), "the server's Device must be published")
	serverPort := server.currentListenPort(server.device.Load())
	require.NotZero(t, serverPort, "the server must own a real UDP port")

	client, err := NewEndpoint(EndpointOptions{
		Context:    context.Background(),
		Logger:     log.NewNOPFactory().Logger(),
		Dialer:     lazyDialer(t),
		Tag:        "wg-client",
		PrivateKey: clientPrivate,
		MTU:        1408,
		UDPTimeout: 5 * time.Minute,
		ICMPTimeout: 5 * time.Minute,
		Address:    []netip.Prefix{netip.MustParsePrefix("10.66.0.2/24")},
		AmneziaWG:  amneziaWG,
		Peers: []PeerOptions{{
			Endpoint:   M.ParseSocksaddrHostPort("127.0.0.1", serverPort),
			PublicKey:  serverPublic,
			AllowedIPs: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")},
		}},
	})
	require.NoError(t, err)
	require.NoError(t, client.Initialize(nil))
	t.Cleanup(func() { _ = client.Close() })
	pair := &lazyPair{
		client:      client,
		server:      server,
		serverPort:  serverPort,
		handler:     handler,
		destination: M.SocksaddrFrom(netip.MustParseAddr("10.66.0.1"), 18443),
	}
	require.NoError(t, client.Start(false))
	return pair
}

func (p *lazyPair) dial(t *testing.T) net.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, err := p.client.DialContext(ctx, N.NetworkTCP, p.destination)
	require.NoError(t, err, "the WireGuard handshake never completed")
	require.NotNil(t, conn)
	return conn
}

func (p *lazyPair) exchange(t *testing.T, conn net.Conn, payload string, timeout time.Duration) {
	t.Helper()
	require.NoError(t, conn.SetDeadline(time.Now().Add(timeout)))
	_, err := conn.Write([]byte(payload))
	require.NoError(t, err)
	reply := make([]byte, len(payload))
	_, err = io.ReadFull(conn, reply)
	require.NoError(t, err, "no reply crossed the tunnel")
	require.Equal(t, payload, string(reply))
}

// waitForEcho waits for the echoed payload on an already-open flow, which is the shape a suspended
// and rebuilt tunnel has to recover: the flow lives in the stack, so the bytes must come back once
// the Device is republished.
func waitForEcho(t *testing.T, conn net.Conn, payload string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		require.NoError(t, conn.SetReadDeadline(time.Now().Add(2*time.Second)))
		reply := make([]byte, len(payload))
		read, err := io.ReadFull(conn, reply)
		if err == nil && read == len(payload) {
			require.Equal(t, payload, string(reply))
			return
		}
	}
	t.Fatalf("the flow that was open before the release never recovered")
}

// ---------------------------------------------------------------------------
// proof 6: WG and AWG real handshake and data planes, across the release/rebuild
// ---------------------------------------------------------------------------

func TestARealWireGuardPairSurvivesASuspendReleaseAndRebuild(t *testing.T) {
	pair := lazyStartPair(t, option.AmneziaWGOptions{})
	first := pair.dial(t)
	pair.exchange(t, first, "before-suspend", 10*time.Second)

	stackBefore := pair.client.tunDevice
	require.True(t, pair.client.lazyEligible, "the client shape must be eligible for the lazy release")

	// --- suspend: the Device is released, the stack and the flow stay ---
	pair.client.SetIdle(true)
	published, released := pair.client.lazyState()
	require.False(t, published, "a suspended eligible endpoint must not keep a wireguard-go Device")
	require.True(t, released, "the release must be recorded, so demand knows to rebuild")
	require.Same(t, stackBefore, pair.client.tunDevice, "the stack must survive the release")

	// The open flow is paused, not killed: its write is accepted by the stack, but with the router
	// detached there is no reply while the Device is gone.
	require.NoError(t, first.SetWriteDeadline(time.Now().Add(2*time.Second)))
	_, writeErr := first.Write([]byte("x"))
	require.NoError(t, writeErr)
	require.NoError(t, first.SetReadDeadline(time.Now().Add(400*time.Millisecond)))
	buffer := make([]byte, 1)
	if read, readErr := first.Read(buffer); readErr == nil || read != 0 {
		t.Fatalf("a released endpoint must not carry payload, got read=%d err=%v", read, readErr)
	}

	// --- a probe must not be what rebuilds it ---
	probeCtx := adapter.ContextWithBackgroundProbe(context.Background())
	_, err := pair.client.DialContext(probeCtx, N.NetworkTCP, pair.destination)
	require.ErrorIs(t, err, adapter.ErrResourceSuspended)
	published, released = pair.client.lazyState()
	require.False(t, published)
	require.True(t, released)

	// Network hooks must not rebuild it either: a rebind is refused while the chain is released.
	require.NoError(t, pair.client.BindUpdate())
	require.ErrorIs(t, pair.client.RebindStale(context.Background(), adapter.RebindHandshakeGiveUp), errRebindNotPossible)
	published, _ = pair.client.lazyState()
	require.False(t, published, "a network hook must never publish a released chain late")

	// --- wake: still lazy; the rebuild waits for a real demand ---
	pair.client.SetIdle(false)
	published, released = pair.client.lazyState()
	require.False(t, published, "SetIdle(false) must not eagerly spend 8.4 MB for a wake with no traffic")
	require.True(t, released)

	// --- demand: rebuild, real handshake again, new flow works ---
	second := pair.dial(t)
	pair.exchange(t, second, "after-rebuild", 10*time.Second)
	published, released = pair.client.lazyState()
	require.True(t, published)
	require.False(t, released)

	// --- and the flow that was open across the release also recovers: the byte written while the
	// Device was released is delivered first (the stack kept the flow), then new payload flows.
	require.NoError(t, first.SetReadDeadline(time.Now().Add(20*time.Second)))
	buffer = make([]byte, 1)
	read, readErr := io.ReadFull(first, buffer)
	require.NoError(t, readErr, "the flow that was open before the release never recovered")
	require.Equal(t, 1, read)
	require.Equal(t, byte('x'), buffer[0], "the byte written while the Device was released must be the first to arrive")
	require.NoError(t, first.SetWriteDeadline(time.Now().Add(10*time.Second)))
	_, writeErr = first.Write([]byte("recovered-flow"))
	require.NoError(t, writeErr)
	waitForEcho(t, first, "recovered-flow", 20*time.Second)

	require.GreaterOrEqual(t, pair.handler.flows.Load(), int32(2),
		"the server side must have accepted both the pre-suspend and the post-rebuild flow")
	_ = second.Close()
	_ = first.Close()
}

// ---------------------------------------------------------------------------
// proof 1: in-flight flow creation is mutually exclusive with the release
// ---------------------------------------------------------------------------

func TestReleaseIsMutuallyExclusiveWithAnInFlightFlowCreation(t *testing.T) {
	endpoint := lazySinglePeerEndpoint(t, "wg-inflight")
	lazyStart(t, endpoint)

	// Hold a use exactly as a dial does, without needing a real peer.
	release, err := endpoint.beginUse(context.Background())
	require.NoError(t, err)

	endpoint.SetIdle(true)
	published, released := endpoint.lazyState()
	require.True(t, published, "a release must not happen while a flow-creation call is in flight")
	require.False(t, released)
	require.True(t, endpoint.suspended.Load())

	// Once the call ends, the next suspend boundary is allowed to release.
	release()
	endpoint.SetIdle(false)
	endpoint.SetIdle(true)
	published, released = endpoint.lazyState()
	require.False(t, published)
	require.True(t, released)
}

// The concurrent stress version: run it under -race. Dials and suspend/resume cycles interleave;
// nothing may touch a closed Device, and the endpoint must still release when quiesced.
func TestConcurrentDialsAndSuspendCyclesDoNotRaceTheRelease(t *testing.T) {
	endpoint := lazySinglePeerEndpoint(t, "wg-stress")
	lazyStart(t, endpoint)
	destination := M.SocksaddrFrom(netip.MustParseAddr("10.0.0.1"), 80)

	var dials atomic.Int32
	var waitGroup sync.WaitGroup
	stop := make(chan struct{})
	for worker := 0; worker < 4; worker++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
				conn, dialErr := endpoint.DialContext(ctx, N.NetworkTCP, destination)
				cancel()
				if dialErr == nil && conn != nil {
					_ = conn.Close()
				}
				dials.Add(1)
			}
		}()
	}
	toggle := make(chan struct{})
	waitGroup.Add(1)
	go func() {
		defer waitGroup.Done()
		for {
			select {
			case <-toggle:
				return
			case <-time.After(20 * time.Millisecond):
			}
			endpoint.SetIdle(true)
			endpoint.SetIdle(false)
		}
	}()
	time.Sleep(1200 * time.Millisecond)
	close(toggle)
	close(stop)
	waitGroup.Wait()

	require.Greater(t, dials.Load(), int32(0))
	// Quiesce: the final suspend must release the chain now that no use is in flight.
	endpoint.SetIdle(true)
	published, released := endpoint.lazyState()
	require.False(t, published, "the endpoint must release its heavy chain once quiesced")
	require.True(t, released)
	// And a final demand still works (rebuild after the churn).
	release, err := endpoint.beginUse(context.Background())
	require.NoError(t, err)
	release()
}

// A detached attachment can no longer reach the shared stack: that is what makes closing the Device
// a different event from closing the stack.
func TestADetachedAttachmentCannotWriteIntoTheStack(t *testing.T) {
	created, err := NewDevice(DeviceOptions{
		Context: context.Background(),
		Logger:  log.NewNOPFactory().Logger(),
		MTU:     1420,
		Address: []netip.Prefix{netip.MustParsePrefix("10.0.0.1/24")},
	})
	require.NoError(t, err)
	stack, isStack := created.(*stackDevice)
	require.True(t, isStack)
	t.Cleanup(func() { _ = stack.Close() })

	attachment, isAttachment := stack.newAttachment().(*stackAttachment)
	require.True(t, isAttachment)
	require.NoError(t, attachment.Close())
	require.NoError(t, attachment.Close(), "attachment close must be idempotent")

	_, err = attachment.Write([][]byte{make([]byte, 40)}, 0)
	require.ErrorIs(t, err, os.ErrClosed, "a detached attachment must not inject a packet into the stack")
	_, err = attachment.Read([][]byte{make([]byte, 64)}, []int{0}, 0)
	require.ErrorIs(t, err, os.ErrClosed)
	// The attachment starts with one buffered EventUp; after that the channel must be closed so the
	// Device's event worker exits instead of spinning.
	if event, eventsOpen := <-attachment.Events(); eventsOpen {
		require.EqualValues(t, wgTun.EventUp, event, "the only event before Close is the buffered up event")
	}
	_, eventsOpen := <-attachment.Events()
	require.False(t, eventsOpen, "the event channel must be closed so the Device's event worker exits")
}

// ---------------------------------------------------------------------------
// proof 2: addressing queries keep their exact answer across the lifecycle
// ---------------------------------------------------------------------------

func TestLookupAndPreferredAddressAnswerAreStableAcrossTheLazyLifecycle(t *testing.T) {
	privateKey, _ := lazyKeyPair(t)
	_, firstPeerKey := lazyKeyPair(t)
	_, secondPeerKey := lazyKeyPair(t)
	endpoint, err := NewEndpoint(EndpointOptions{
		Context:    context.Background(),
		Logger:     log.NewNOPFactory().Logger(),
		Dialer:     lazyDialer(t),
		Tag:        "wg-lookup",
		PrivateKey: privateKey,
		MTU:        1408,
		Address:    []netip.Prefix{netip.MustParsePrefix("10.0.0.1/24")},
		Peers: []PeerOptions{
			{
				Endpoint:   M.ParseSocksaddrHostPort("192.0.2.1", 51820),
				PublicKey:  firstPeerKey,
				AllowedIPs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
			},
			{
				Endpoint:   M.ParseSocksaddrHostPort("192.0.2.2", 51820),
				PublicKey:  secondPeerKey,
				AllowedIPs: []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")},
			},
		},
	})
	require.NoError(t, err)
	require.NoError(t, endpoint.Initialize(nil))
	t.Cleanup(func() { _ = endpoint.Close() })

	check := func(phase string) {
		t.Helper()
		require.Nil(t, endpoint.Lookup(netip.MustParseAddr("198.51.100.1")), phase+": an address outside every configured prefix is not owned")
		require.Nil(t, endpoint.Lookup(netip.MustParseAddr("2001:db8::1")), phase+": an IPv6 address outside the prefixes is not owned")
		require.NotNil(t, endpoint.Lookup(netip.MustParseAddr("10.1.2.3")), phase+": an address in a configured prefix is owned")
		require.NotNil(t, endpoint.Lookup(netip.MustParseAddr("192.0.2.77")), phase+": a specific peer prefix is owned")
	}

	// Before the first publish the previous implementation had no address table at all.
	require.Nil(t, endpoint.Lookup(netip.MustParseAddr("10.1.2.3")), "before Start there is no address table")
	lazyStart(t, endpoint)
	check("published")
	endpoint.SetIdle(true)
	check("suspended")
	endpoint.SetIdle(false)
	check("woken")
	release, useErr := endpoint.beginUse(context.Background())
	require.NoError(t, useErr)
	release()
	check("rebuilt")
	require.NoError(t, endpoint.Close())
	require.Nil(t, endpoint.Lookup(netip.MustParseAddr("10.1.2.3")), "after Close the address table is gone, as before")
}

// ---------------------------------------------------------------------------
// proof 3/4: error stages, fail-closed rebuild, recoverability, never-built Close
// ---------------------------------------------------------------------------

func TestConfigurationErrorsKeepTheirOriginalStage(t *testing.T) {
	// Key/config validation stays in NewEndpoint.
	_, err := NewEndpoint(EndpointOptions{
		Context:    context.Background(),
		Logger:     log.NewNOPFactory().Logger(),
		Dialer:     nil,
		PrivateKey: "not-base64",
	})
	require.Error(t, err)
	require.ErrorContains(t, err, "decode private key")

	// Missing dialer stays a Start error, not a first-use error.
	privateKey, _ := lazyKeyPair(t)
	_, peerKey := lazyKeyPair(t)
	endpoint, err := NewEndpoint(EndpointOptions{
		Context:    context.Background(),
		Logger:     log.NewNOPFactory().Logger(),
		Tag:        "wg-nodialer",
		PrivateKey: privateKey,
		MTU:        1408,
		Address:    []netip.Prefix{netip.MustParsePrefix("10.0.0.1/24")},
		Peers: []PeerOptions{{
			Endpoint:   M.ParseSocksaddrHostPort("192.0.2.1", 51820),
			PublicKey:  peerKey,
			AllowedIPs: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")},
		}},
	})
	require.NoError(t, err)
	require.NoError(t, endpoint.Initialize(nil))
	t.Cleanup(func() { _ = endpoint.Close() })
	require.ErrorContains(t, endpoint.Start(false), "missing dialer for wireguard endpoint")
	published, _ := endpoint.lazyState()
	require.False(t, published, "a failed Start must publish nothing")

	// A fixed listen_port is not in the eligible shape: its bind error stays at Start (the port
	// conflict case is covered by listen_port_test, and the shape verdict is what this asserts).
	fixed, err := NewEndpoint(EndpointOptions{
		Context:    context.Background(),
		Logger:     log.NewNOPFactory().Logger(),
		Dialer:     lazyDialer(t),
		Tag:        "wg-fixed",
		PrivateKey: privateKey,
		MTU:        1408,
		Address:    []netip.Prefix{netip.MustParsePrefix("10.0.0.1/24")},
		ListenPort: 51820,
		Peers: []PeerOptions{{
			Endpoint:   M.ParseSocksaddrHostPort("192.0.2.1", 51820),
			PublicKey:  peerKey,
			AllowedIPs: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")},
		}},
	})
	require.NoError(t, err)
	require.False(t, fixed.lazyEligible, "a fixed listen_port must stay on the original path")
	require.NoError(t, fixed.Initialize(nil))
	t.Cleanup(func() { _ = fixed.Close() })
}

func TestAFailedRebuildIsRecoverableAndFailsClosed(t *testing.T) {
	endpoint := lazySinglePeerEndpoint(t, "wg-rebuild-fail")
	lazyStart(t, endpoint)
	endpoint.SetIdle(true)
	published, released := endpoint.lazyState()
	require.False(t, published)
	require.True(t, released)

	injected := errors.New("injected rebuild failure")
	endpoint.buildDeviceHook = func() error { return injected }
	_, err := endpoint.DialContext(context.Background(), N.NetworkTCP, M.SocksaddrFrom(netip.MustParseAddr("10.0.0.9"), 80))
	require.ErrorIs(t, err, injected, "the rebuild failure must reach the caller")
	published, released = endpoint.lazyState()
	require.False(t, published, "a failed rebuild must publish nothing")
	require.True(t, released, "a failed rebuild must stay retryable")

	// Recover: the next demand rebuilds and publishes.
	endpoint.buildDeviceHook = nil
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, _ = endpoint.DialContext(ctx, N.NetworkTCP, M.SocksaddrFrom(netip.MustParseAddr("10.0.0.9"), 80))
	published, released = endpoint.lazyState()
	require.True(t, published, "the retry after a transient rebuild failure must publish the chain")
	require.False(t, released)
	require.NoError(t, endpoint.Close())
	// Close stays idempotent after a rebuild.
	require.NoError(t, endpoint.Close())
}

func TestNeverBuiltCloseDoesNotLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	privateKey, _ := lazyKeyPair(t)
	_, peerKey := lazyKeyPair(t)
	endpoint, err := NewEndpoint(EndpointOptions{
		Context:    context.Background(),
		Logger:     log.NewNOPFactory().Logger(),
		Dialer:     lazyDialer(t),
		Tag:        "wg-never-built",
		PrivateKey: privateKey,
		MTU:        1408,
		Address:    []netip.Prefix{netip.MustParsePrefix("10.0.0.1/24")},
		Peers: []PeerOptions{{
			Endpoint:   M.ParseSocksaddrHostPort("192.0.2.1", 51820),
			PublicKey:  peerKey,
			AllowedIPs: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")},
		}},
	})
	require.NoError(t, err)
	require.NoError(t, endpoint.Initialize(nil))
	require.NoError(t, endpoint.Close())
	require.NoError(t, endpoint.Close(), "a never-built Close must be idempotent")
	require.Eventually(t, func() bool { return runtime.NumGoroutine() <= before+2 }, 5*time.Second, 20*time.Millisecond,
		"a never-built endpoint must not leak goroutines")
}

// ---------------------------------------------------------------------------
// proof 5: scope shutdown never publishes late
// ---------------------------------------------------------------------------

func TestCloseDuringARebuildNeverPublishesLate(t *testing.T) {
	endpoint := lazySinglePeerEndpoint(t, "wg-close-race")
	lazyStart(t, endpoint)
	endpoint.SetIdle(true)

	entered := make(chan struct{})
	unblock := make(chan struct{})
	endpoint.buildDeviceHook = func() error {
		close(entered)
		<-unblock
		return nil
	}
	dialResult := make(chan error, 1)
	go func() {
		_, err := endpoint.DialContext(context.Background(), N.NetworkTCP, M.SocksaddrFrom(netip.MustParseAddr("10.0.0.9"), 80))
		dialResult <- err
	}()
	<-entered
	require.NoError(t, endpoint.Close())
	close(unblock)
	err := <-dialResult
	require.Error(t, err, "a dial whose rebuild lost to Close must fail, not receive a connection")
	require.ErrorIs(t, err, os.ErrClosed)
	published, _ := endpoint.lazyState()
	require.False(t, published, "Close must win: nothing may be published after it")
	// Close is idempotent and the stack is gone exactly once.
	require.NoError(t, endpoint.Close())
}

// ---------------------------------------------------------------------------
// selective shape gate
// ---------------------------------------------------------------------------

func TestOnlyThePrivateUserspaceShapeIsEligibleForTheRelease(t *testing.T) {
	privateKey, _ := lazyKeyPair(t)
	_, peerKey := lazyKeyPair(t)
	base := func() EndpointOptions {
		return EndpointOptions{
			Context:    context.Background(),
			Logger:     log.NewNOPFactory().Logger(),
			Dialer:     lazyDialer(t),
			PrivateKey: privateKey,
			MTU:        1408,
			Address:    []netip.Prefix{netip.MustParsePrefix("10.0.0.1/24")},
			Peers: []PeerOptions{{
				Endpoint:   M.ParseSocksaddrHostPort("192.0.2.1", 51820),
				PublicKey:  peerKey,
				AllowedIPs: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")},
			}},
		}
	}
	eligible, err := NewEndpoint(base())
	require.NoError(t, err)
	require.True(t, eligible.lazyEligible)

	withListenPort := base()
	withListenPort.ListenPort = 51820
	created, err := NewEndpoint(withListenPort)
	require.NoError(t, err)
	require.False(t, created.lazyEligible, "a listening port is passive demand and monitoring state")

	withKeepalive := base()
	withKeepalive.Peers[0].PersistentKeepaliveInterval = 25
	created, err = NewEndpoint(withKeepalive)
	require.NoError(t, err)
	require.False(t, created.lazyEligible, "keepalive is a timer whose phase a rebuild would reset")

	withSystem := base()
	withSystem.System = true
	created, err = NewEndpoint(withSystem)
	require.NoError(t, err)
	require.False(t, created.lazyEligible, "a system stack owns host routes and ports")

	withPassivePeer := base()
	withPassivePeer.Peers = append(withPassivePeer.Peers, PeerOptions{
		PublicKey:  peerKey,
		AllowedIPs: []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")},
	})
	created, err = NewEndpoint(withPassivePeer)
	require.NoError(t, err)
	require.False(t, created.lazyEligible, "a peer without an endpoint is passive inbound demand")

	withDomainPeer := base()
	withDomainPeer.Peers[0].Endpoint = M.ParseSocksaddrHostPort("example.com", 51820)
	created, err = NewEndpoint(withDomainPeer)
	require.NoError(t, err)
	require.False(t, created.lazyEligible, "a domain peer starts at post-start and must not re-resolve on demand")
}

// ---------------------------------------------------------------------------
// resource ledger (W5 table): New / Initialize / Start / first-use / idle / rebuild / Close
// ---------------------------------------------------------------------------

type lazySample struct {
	heap       uint64
	objects    uint64
	goroutines int
	fds        int
	fdsOK      bool
}

func lazySampleNow() lazySample {
	runtime.GC()
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	fds, fdsOK := processFDCount()
	return lazySample{
		heap:       stats.HeapAlloc,
		objects:    stats.HeapObjects,
		goroutines: runtime.NumGoroutine(),
		fds:        fds,
		fdsOK:      fdsOK,
	}
}

// lazyPeakNow is the pre-GC live heap right after an operation: no forced collection between the
// allocation and the read, so it is a peak (or very close to it) rather than a settled value.
func lazyPeakNow() (uint64, uint64) {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc, stats.TotalAlloc
}

func lazyEmit(n int, phase string, sample lazySample, peak uint64, totalAlloc uint64) {
	fmt.Printf("WGLAZY n=%d phase=%s heap=%d objects=%d goroutines=%d fds=%d fds_measured=%t pre_gc_peak=%d total_alloc=%d\n",
		n, phase, sample.heap, sample.objects, sample.goroutines, sample.fds, sample.fdsOK, peak, totalAlloc)
}

func TestWGSelectiveLazyResourceLedger(t *testing.T) {
	n, err := strconv.Atoi(os.Getenv("WGLAZY_N"))
	if err != nil || n < 1 {
		n = 1
	}
	_, peerKey := lazyKeyPair(t)
	peers := []PeerOptions{{
		Endpoint:   M.ParseSocksaddrHostPort("192.0.2.1", 51820),
		PublicKey:  peerKey,
		AllowedIPs: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")},
	}}
	newEndpoint := func(index int) *Endpoint {
		privateKey, _ := lazyKeyPair(t)
		created, createErr := NewEndpoint(EndpointOptions{
			Context:    context.Background(),
			Logger:     log.NewNOPFactory().Logger(),
			Dialer:     lazyDialer(t),
			Tag:        "wg-ledger-" + strconv.Itoa(index),
			PrivateKey: privateKey,
			MTU:        1408,
			Address:    []netip.Prefix{netip.MustParsePrefix("10.0." + strconv.Itoa(index) + ".1/24")},
			Peers:      peers,
		})
		require.NoError(t, createErr)
		return created
	}
	dialAll := func(endpoints []*Endpoint) {
		var waitGroup sync.WaitGroup
		for _, endpoint := range endpoints {
			waitGroup.Add(1)
			go func(endpoint *Endpoint) {
				defer waitGroup.Done()
				ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
				defer cancel()
				conn, dialErr := endpoint.DialContext(ctx, N.NetworkTCP, M.SocksaddrFrom(netip.MustParseAddr("10.0.0.1"), 80))
				if dialErr == nil && conn != nil {
					_ = conn.Close()
				}
			}(endpoint)
		}
		waitGroup.Wait()
	}

	lazyEmit(n, "baseline", lazySampleNow(), 0, 0)
	baselineGoroutines := runtime.NumGoroutine()

	endpoints := make([]*Endpoint, 0, n)
	for index := 1; index <= n; index++ {
		endpoint := newEndpoint(index)
		require.NoError(t, endpoint.Initialize(nil))
		endpoints = append(endpoints, endpoint)
	}
	lazyEmit(n, "new_initialize", lazySampleNow(), 0, 0)

	startBegan := time.Now()
	for _, endpoint := range endpoints {
		require.NoError(t, endpoint.Start(false))
	}
	startElapsed := time.Since(startBegan)
	startPeak, startAlloc := lazyPeakNow()
	startSample := lazySampleNow()
	lazyEmit(n, "start", startSample, startPeak, startAlloc)

	dialAll(endpoints)
	firstUsePeak, firstUseAlloc := lazyPeakNow()
	firstUse := lazySampleNow()
	lazyEmit(n, "first_use", firstUse, firstUsePeak, firstUseAlloc)

	for _, endpoint := range endpoints {
		endpoint.SetIdle(true)
	}
	idle := lazySampleNow()
	for _, endpoint := range endpoints {
		published, released := endpoint.lazyState()
		require.False(t, published, "every eligible endpoint must release its Device on suspend")
		require.True(t, released)
	}
	lazyEmit(n, "idle_suspended", idle, 0, 0)
	require.Less(t, idle.goroutines, startSample.goroutines,
		"the release must return the Device's workers, not only its buffers")
	if idle.heap >= startSample.heap {
		t.Logf("ledger warning: idle heap %d did not fall below start heap %d", idle.heap, startSample.heap)
	}

	for _, endpoint := range endpoints {
		endpoint.SetIdle(false)
	}
	resumed := lazySampleNow()
	for _, endpoint := range endpoints {
		published, released := endpoint.lazyState()
		require.False(t, published)
		require.True(t, released)
	}
	lazyEmit(n, "woken_still_released", resumed, 0, 0)

	// The first endpoint is rebuilt synchronously here so the rebuild cost is measured without the
	// dial loop's own timeout wait; the loop then rebuilds the rest (and finds this one built).
	rebuildBegan := time.Now()
	rebuildRelease, rebuildErr := endpoints[0].beginUse(context.Background())
	require.NoError(t, rebuildErr)
	rebuildRelease()
	rebuildElapsed := time.Since(rebuildBegan)
	dialAll(endpoints)
	rebuildPeak, rebuildAlloc := lazyPeakNow()
	rebuilt := lazySampleNow()
	for _, endpoint := range endpoints {
		published, released := endpoint.lazyState()
		require.True(t, published, "demand must rebuild the released chain")
		require.False(t, released)
	}
	lazyEmit(n, "demand_rebuilt", rebuilt, rebuildPeak, rebuildAlloc)

	for _, endpoint := range endpoints {
		require.NoError(t, endpoint.Close())
	}
	time.Sleep(300 * time.Millisecond)
	lazyEmit(n, "close", lazySampleNow(), 0, 0)
	fmt.Printf("WGLAT n=%d start_ms=%d rebuild_ms=%d\n", n, startElapsed.Milliseconds(), rebuildElapsed.Milliseconds())

	// The A/B control: the SAME code path with the release disabled, so "Down only" is measured on
	// this machine and this revision rather than quoted from another build. It runs after the
	// released set is closed, so the peak stays at one set's cost.
	downOnly := make([]*Endpoint, 0, n)
	for index := 1; index <= n; index++ {
		endpoint := newEndpoint(index)
		endpoint.lazyEligible = false
		require.NoError(t, endpoint.Initialize(nil))
		require.NoError(t, endpoint.Start(false))
		downOnly = append(downOnly, endpoint)
	}
	downOnlyStart := lazySampleNow()
	for _, endpoint := range downOnly {
		endpoint.SetIdle(true)
	}
	downOnlyIdle := lazySampleNow()
	for _, endpoint := range downOnly {
		published, released := endpoint.lazyState()
		require.True(t, published, "the control must keep the Device published: it is the previous Down-only path")
		require.False(t, released)
		_ = endpoint.Close()
	}
	lazyEmit(n, "idle_down_only", downOnlyIdle, 0, 0)
	lazyEmit(n, "down_only_start_reference", downOnlyStart, 0, 0)

	require.Eventually(t, func() bool {
		return runtime.NumGoroutine() <= baselineGoroutines+5
	}, 10*time.Second, 50*time.Millisecond, "all endpoints closed: no worker may survive")
	require.GreaterOrEqual(t, idle.goroutines, 1)
}
