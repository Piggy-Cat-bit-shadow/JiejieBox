package route

// The generation boundary must not queue behind the physical Close of an old connection.
//
// # The defect this file pins
//
// `resetNetworkLocked` reclaims the previous generation through the production ConnectionManager and
// only then pins DNS and settles the transition. For a DRAINING reclaim the manager intentionally
// closes datagram flows inline: "a datagram flow is class A at a transition: its 4-tuple is the
// flow's identity, so the idleness grace cannot make it safe". A UDP socket's Close can take as long
// as the kernel makes it take (a blocked send, a thousand queued datagrams, a stuck poller), and the
// whole body - DNS pin, generation commit, the new generation's first dial - waited behind it.
//
// The same is true of a dead-path reclaim: `CloseAll` closes every tracked socket inline.
//
// # What is asserted
//
// A tracked UDP socket whose Close blocks indefinitely is in place when the reset starts. While that
// Close is still inside its critical section:
//
//   - the DNS pin has run and records THIS transition's generation;
//   - the transition has committed (the dialer will hand over a new connection);
//   - a real DNS query is answered, and a production-dialer connection carries a first byte;
//   - the reset call itself has returned (the blocked Close was handed to its own disposer).
//
// Only after the barrier is released does the old socket finish closing, and it closes exactly once.
//
// The three shapes are the ones the closeout order asks for: an ordinary network change with a slow
// UDP close, a dead-path (hard) reclaim with the same slow close, and two consecutive changes while
// the first close is still blocked.

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mDNS "github.com/miekg/dns"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/dialer"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/dns"
	dnsTransport "github.com/sagernet/sing-box/dns/transport"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// blockedPacketClose is a real UDP socket whose Close signals that it started and then waits for the
// test's release. The socket really is open until the release, so the "old generation still closing"
// window is a live socket, not a flag.
type blockedPacketClose struct {
	net.PacketConn
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	closes  atomic.Int32
}

func newBlockedPacketClose(t *testing.T) *blockedPacketClose {
	t.Helper()
	packetConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	blocked := &blockedPacketClose{
		PacketConn: packetConn,
		entered:    make(chan struct{}),
		release:    make(chan struct{}),
	}
	t.Cleanup(func() {
		select {
		case <-blocked.release:
		default:
			close(blocked.release)
		}
		_ = packetConn.Close()
	})
	return blocked
}

func (c *blockedPacketClose) Close() error {
	c.once.Do(func() {
		c.closes.Add(1)
		close(c.entered)
		<-c.release
	})
	return c.PacketConn.Close()
}

func (c *blockedPacketClose) releaseClose() {
	select {
	case <-c.release:
	default:
		close(c.release)
	}
}

// timelineDNS is the production DNS half used by the generation-boundary tests: a real UDP server, the
// production UDP transport, and the production DNS client whose generation source is this manager.
type timelineDNS struct {
	transport adapter.DNSTransport
	client    *dns.Client
	query     *mDNS.Msg
	answered  *atomic.Int32
}

func newTimelineDNS(t *testing.T, manager *NetworkManager) *timelineDNS {
	t.Helper()
	packetConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	answered := &atomic.Int32{}
	server := &mDNS.Server{
		PacketConn: packetConn,
		Handler: mDNS.HandlerFunc(func(writer mDNS.ResponseWriter, request *mDNS.Msg) {
			answered.Add(1)
			response := new(mDNS.Msg)
			response.SetReply(request)
			response.Answer = append(response.Answer, &mDNS.A{
				Hdr: mDNS.RR_Header{
					Name: request.Question[0].Name, Rrtype: mDNS.TypeA,
					Class: mDNS.ClassINET, Ttl: 60,
				},
				A: net.IPv4(127, 0, 0, 1),
			})
			_ = writer.WriteMsg(response)
		}),
	}
	go func() { _ = server.ActivateAndServe() }()
	t.Cleanup(func() { _ = server.Shutdown() })

	serverPort := uint16(packetConn.LocalAddr().(*net.UDPAddr).Port)
	logger := log.NewNOPFactory().NewLogger("generation-boundary-dns")
	options := option.RemoteDNSServerOptions{
		DNSServerAddressOptions: option.DNSServerAddressOptions{Server: "127.0.0.1", ServerPort: serverPort},
	}
	transport := dnsTransport.NewUDPRaw(
		logger,
		dns.NewTransportAdapterWithRemoteOptions(C.DNSTypeUDP, "generation-boundary", options),
		&N.DefaultDialer{},
		M.ParseSocksaddrHostPort("127.0.0.1", serverPort),
	)
	scope := adapter.NewScope(context.Background(), logger)
	require.NoError(t, transport.Start(adapter.StartStateStart, scope))
	t.Cleanup(func() { _ = scope.Close() })
	client := dns.NewClient(dns.ClientOptions{
		Context:           context.Background(),
		Logger:            logger,
		DisableCache:      true,
		NetworkGeneration: manager.NetworkResetGeneration,
	})
	client.Start()
	query := new(mDNS.Msg)
	query.SetQuestion("generation-boundary.example.", mDNS.TypeA)
	return &timelineDNS{transport: transport, client: client, query: query, answered: answered}
}

// requireAnswer issues one real DNS query and requires the answer to come back.
func (h *timelineDNS) requireAnswer(t *testing.T) time.Duration {
	t.Helper()
	started := time.Now()
	response, err := h.client.Exchange(context.Background(), h.transport, h.query, adapter.DNSQueryOptions{}, nil)
	elapsed := time.Since(started)
	require.NoError(t, err, "the DNS query could not be answered while the old socket was closing")
	require.NotNil(t, response)
	require.NotEmpty(t, response.Answer, "the real DNS server's answer did not reach the client")
	require.GreaterOrEqual(t, h.answered.Load(), int32(1))
	return elapsed
}

// newGenerationDialer is the production dialer bound to this manager and connection manager, which is
// what makes the new generation's hand-over check meaningful.
func newGenerationDialer(t *testing.T, manager *NetworkManager, connectionManager adapter.ConnectionManager) *dialer.DefaultDialer {
	t.Helper()
	dialerContext := service.ContextWith[adapter.NetworkManager](context.Background(), manager)
	dialerContext = service.ContextWith[adapter.ConnectionManager](dialerContext, connectionManager)
	realDialer, err := dialer.NewDefault(dialerContext, option.DialerOptions{})
	require.NoError(t, err)
	return realDialer
}

// startConnectProxy is a real TCP CONNECT relay: it accepts a request, dials the named target and
// tunnels both directions. It is the proxy leg of the acceptance - a real hop with a real
// handshake and real bytes - while the protocol outbounds (VLESS/VMess/Trojan and the carriers)
// keep their own tests.
func startConnectProxy(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer conn.Close()
				reader := bufio.NewReader(conn)
				request, readErr := http.ReadRequest(reader)
				if readErr != nil || request.Method != http.MethodConnect {
					return
				}
				upstream, dialErr := net.Dial("tcp", request.Host)
				if dialErr != nil {
					return
				}
				defer upstream.Close()
				if _, writeErr := conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); writeErr != nil {
					return
				}
				go func() { _, _ = io.Copy(upstream, reader) }()
				_, _ = io.Copy(conn, upstream)
			}()
		}
	}()
	return listener.Addr().String()
}

// requireProxyFirstByte dials the CONNECT relay through the production dialer, completes the
// CONNECT handshake and requires the first byte of the proxied flow.
func requireProxyFirstByte(t *testing.T, realDialer *dialer.DefaultDialer, proxyAddress, target string) time.Duration {
	t.Helper()
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := realDialer.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr(proxyAddress))
	require.NoError(t, err, "the new generation could not reach the proxy while the old socket was closing")
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err = fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target); err != nil {
		t.Fatalf("write CONNECT: %v", err)
	}
	reader := bufio.NewReader(conn)
	status, err := reader.ReadString('\n')
	require.NoError(t, err, "the proxy never answered the CONNECT")
	require.Contains(t, status, "200", "the proxy refused the CONNECT: %q", status)
	for {
		header, headerErr := reader.ReadString('\n')
		if headerErr != nil {
			t.Fatalf("read CONNECT headers: %v", headerErr)
		}
		if header == "\r\n" || header == "\n" {
			break
		}
	}
	first := make([]byte, 1)
	_, err = io.ReadFull(reader, first)
	require.NoError(t, err, "no byte arrived through the proxy")
	return time.Since(started)
}

// requireFirstByte dials the loopback echo through the production dialer and requires one byte back.
func requireFirstByte(t *testing.T, realDialer *dialer.DefaultDialer, target string) time.Duration {
	t.Helper()
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := realDialer.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr(target))
	require.NoError(t, err, "the new generation could not dial while the old socket was still closing")
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	first := make([]byte, 1)
	_, err = io.ReadFull(conn, first)
	require.NoError(t, err, "the new generation never delivered a first byte")
	return time.Since(started)
}

// awaitPin waits for the next DNS pin and returns the instant it ran.
func awaitPin(t *testing.T, router *timedRouter, pin <-chan struct{}) time.Time {
	t.Helper()
	select {
	case <-pin:
	case <-time.After(5 * time.Second):
		t.Fatal("DNS pin is held behind the old packet Close; stable=false")
	}
	return time.Unix(0, router.pinnedAt.Load())
}

// newBlockedCloseHarness builds the production manager with a real router, a real connection manager
// and one blocked UDP socket already tracked. The environment is settled first so the reset under
// measurement is a single transition.
func newBlockedCloseHarness(t *testing.T) (*interfaceTransitionHarness, *timedRouter, *ConnectionManager, *blockedPacketClose, string) {
	t.Helper()
	target := startTimelineEcho(t)
	harness := newInterfaceTransitionHarness(t)
	harness.manager.transitionStable.Store(true)
	router := newTimedRouter(harness.manager.NetworkResetGeneration)
	harness.manager.router = router
	harness.manager.updateNetworkEnvironment()
	require.True(t, harness.manager.NetworkTransitionStable(),
		"the manager did not settle on its first environment")

	connectionManager := NewConnectionManager(log.NewNOPFactory().Logger())
	t.Cleanup(func() { _ = connectionManager.Close() })
	harness.manager.connectionManager = connectionManager

	blocked := newBlockedPacketClose(t)
	connectionManager.TrackPacketConn(blocked)
	return harness, router, connectionManager, blocked, target
}

// TestAnOrdinaryNetworkChangeDoesNotQueueBehindASlowOldPacketClose is the review's reproduction: an
// ordinary ResetNetwork with a tracked UDP socket whose Close blocks.
func TestAnOrdinaryNetworkChangeDoesNotQueueBehindASlowOldPacketClose(t *testing.T) {
	harness, router, connectionManager, blocked, target := newBlockedCloseHarness(t)
	nextPin := router.watchNextPin()
	pinsBefore := router.pinCount()
	dnsProbe := newTimelineDNS(t, harness.manager)
	realDialer := newGenerationDialer(t, harness.manager, connectionManager)

	resetDone := make(chan struct{})
	go func() {
		defer close(resetDone)
		harness.manager.ResetNetwork(context.Background())
	}()

	select {
	case <-blocked.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the old packet Close never started, so this run does not measure a slow close")
	}
	// The close is now inside its critical section. Everything below must happen without it returning.
	pinnedAt := awaitPin(t, router, nextPin)
	require.True(t, harness.manager.NetworkTransitionStable(),
		"the transition was not committed while the old socket's Close was still in flight")
	// The settle above pinned once already; this transition must add exactly one pin.
	require.Equal(t, 1, router.pinCount()-pinsBefore, "one transition pins DNS exactly once")
	queryTime := dnsProbe.requireAnswer(t)
	firstByte := requireFirstByte(t, realDialer, target)
	proxyFirstByte := requireProxyFirstByte(t, realDialer, startConnectProxy(t), target)
	select {
	case <-resetDone:
	case <-time.After(5 * time.Second):
		t.Fatal("ResetNetwork did not return while the old socket's Close was still blocked: the " +
			"generation boundary is serialised behind a physical close")
	}
	require.EqualValues(t, 1, blocked.closes.Load(), "the old socket must be closed exactly once")
	t.Logf("GENERATION-BOUNDARY shape=ordinary pin-at=%s dns=%s first-byte=%s proxy-first-byte=%s close-still-blocked=true",
		pinnedAt.Format(time.RFC3339Nano), queryTime.Round(time.Microsecond), firstByte.Round(time.Microsecond), proxyFirstByte.Round(time.Microsecond))

	blocked.releaseClose()
	require.Eventually(t, func() bool { return blocked.closes.Load() == 1 },
		5*time.Second, 5*time.Millisecond, "the released socket must stay closed exactly once")
}

// TestADeadPathResetDoesNotQueueBehindASlowOldPacketClose is the hard-reset half: ReclaimDeadPath
// closes every tracked connection, and the same socket blocks that path today.
func TestADeadPathResetDoesNotQueueBehindASlowOldPacketClose(t *testing.T) {
	harness, router, connectionManager, blocked, target := newBlockedCloseHarness(t)
	nextPin := router.watchNextPin()
	dnsProbe := newTimelineDNS(t, harness.manager)
	realDialer := newGenerationDialer(t, harness.manager, connectionManager)

	resetDone := make(chan struct{})
	go func() {
		defer close(resetDone)
		harness.manager.ResetNetworkHard(context.Background())
	}()

	select {
	case <-blocked.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the dead-path reclaim never started the old socket's Close")
	}
	awaitPin(t, router, nextPin)
	require.True(t, harness.manager.NetworkTransitionStable(),
		"a dead-path reset did not commit while the old socket's Close was still in flight")
	queryTime := dnsProbe.requireAnswer(t)
	firstByte := requireFirstByte(t, realDialer, target)
	proxyFirstByte := requireProxyFirstByte(t, realDialer, startConnectProxy(t), target)
	select {
	case <-resetDone:
	case <-time.After(5 * time.Second):
		t.Fatal("ResetNetworkHard did not return while the old socket's Close was still blocked")
	}
	require.EqualValues(t, 1, blocked.closes.Load())
	t.Logf("GENERATION-BOUNDARY shape=dead-path dns=%s first-byte=%s proxy-first-byte=%s close-still-blocked=true",
		queryTime.Round(time.Microsecond), firstByte.Round(time.Microsecond), proxyFirstByte.Round(time.Microsecond))
	blocked.releaseClose()
}

// TestAConsecutiveChangeIsNotBlockedByTheFirstClose is A -> B -> C while A's socket is still closing.
func TestAConsecutiveChangeIsNotBlockedByTheFirstClose(t *testing.T) {
	harness, router, connectionManager, blocked, target := newBlockedCloseHarness(t)
	nextPin := router.watchNextPin()
	dnsProbe := newTimelineDNS(t, harness.manager)
	realDialer := newGenerationDialer(t, harness.manager, connectionManager)

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		harness.manager.ResetNetwork(context.Background())
	}()
	select {
	case <-blocked.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the first change never started the old socket's Close")
	}
	awaitPin(t, router, nextPin)
	select {
	case <-firstDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the first change did not return while the old socket was closing")
	}

	// The environment moves again while the first socket is still inside Close.
	harness.setSSID("second-network")
	secondPin := router.watchNextPin()
	secondDone := make(chan struct{})
	go func() {
		defer close(secondDone)
		harness.manager.ResetNetwork(context.Background())
	}()
	awaitPin(t, router, secondPin)
	select {
	case <-secondDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the second change did not return while the first socket was still closing")
	}
	require.True(t, harness.manager.NetworkTransitionStable())
	firstByte := requireFirstByte(t, realDialer, target)
	proxyFirstByte := requireProxyFirstByte(t, realDialer, startConnectProxy(t), target)
	_ = dnsProbe.requireAnswer(t)
	require.EqualValues(t, 1, blocked.closes.Load(),
		"the first socket must not be closed a second time by the later transition")
	t.Logf("GENERATION-BOUNDARY shape=consecutive second-commit=true first-byte=%s proxy-first-byte=%s pins=%d",
		firstByte.Round(time.Microsecond), proxyFirstByte.Round(time.Microsecond), router.pinCount())

	blocked.releaseClose()
	require.Eventually(t, func() bool { return blocked.closes.Load() == 1 }, 5*time.Second, 5*time.Millisecond)
}
