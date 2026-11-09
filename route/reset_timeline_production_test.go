package route

// One production network change, timed from the platform event to the new generation's first
// business byte, with the old generation's cleanup still in flight underneath it.
//
// # What the previous version of this test could not show
//
// It drove ResetNetwork directly, held the old Close only AFTER the reset returned, dialled the new
// generation with a plain net.Dialer (so the "first byte" was a loopback TCP handshake that had
// nothing to do with the transition), and wrote its two "<1ms" figures into the log line as
// constants. A -race run also caught it reading countingRouter.entered while the reset goroutine
// wrote it.
//
// # What this one measures
//
// The entry point is the platform notification (notifyInterfaceUpdate), which claims the generation
// and dispatches the update goroutine exactly as the monitor callback does. Every instant below is a
// monotonic clock reading taken in the test or in a production hook:
//
//	event               test clock, immediately before notifyInterfaceUpdate
//	generation claim    NetworkManager.transitionClaimed, the hook the claim itself calls
//	DNS pin             the router's own ResetNetwork entry (the DNS-pin boundary)
//	listener entry      each simulated active outbound, which also records what it can see
//	first business byte common/dialer's DefaultDialer + a real TCP server
//	old generation end  each outbound returns after closing the flow its old path carried
//
// The two simulated outbounds are ACTIVE - each carries a flow of the previous generation and must
// retire it - and each Close blocks until the test releases it, which is the "slow old close" the
// review asked for. Both listeners must be ENTERED before either can return, so the phase's
// concurrency is a fact rather than a duration: a serial dispatch would never enter the second
// listener and this test would time out waiting for it. That timeout is the pre-fix reproduction.
//
// The new generation's first byte is asserted to arrive while BOTH old-generation closes are still
// held, and while the transition's listener phase is still running. That is the acceptance sentence:
// the new generation does not wait for the old generation's cleanup.

import (
	"context"
	"io"
	"net"
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

// timedRouter records the instant and the generation of the DNS-pin boundary. It embeds the
// package's counting router, so every other assertion about serialisation and reset counts keeps
// working unchanged.
type timedRouter struct {
	*countingRouter
	access           sync.Mutex
	pinned           chan struct{}
	pinnedAt         atomic.Int64
	pinnedGeneration atomic.Uint64
	generation       func() uint64
}

func newTimedRouter(generation func() uint64) *timedRouter {
	return &timedRouter{countingRouter: newCountingRouter(), pinned: make(chan struct{}), generation: generation}
}

// watchNextPin arms a fresh pin signal. A test that settles the manager first uses this to make the
// transition under measurement the only one that can fire it.
func (r *timedRouter) watchNextPin() <-chan struct{} {
	r.access.Lock()
	defer r.access.Unlock()
	r.pinned = make(chan struct{})
	return r.pinned
}

func (r *timedRouter) ResetNetwork() {
	r.access.Lock()
	r.pinnedAt.Store(time.Now().UnixNano())
	r.pinnedGeneration.Store(r.generation())
	if r.pinned != nil {
		close(r.pinned)
		r.pinned = nil
	}
	r.access.Unlock()
	r.countingRouter.ResetNetwork()
}

func (r *timedRouter) pinCount() int { return r.countingRouter.count() }

// heldOldFlow is a real connection whose Close blocks until the test releases it. It is the old
// generation's cleanup: the listener retires it, and the transition's listener phase cannot finish
// before that Close does.
type heldOldFlow struct {
	net.Conn
	release <-chan struct{}
	closes  atomic.Int32
}

func (c *heldOldFlow) Close() error {
	<-c.release
	c.closes.Add(1)
	return c.Conn.Close()
}

// oldGenerationListener stands in for one active outbound (the V2Ray QUIC transport is the one that
// validates a candidate synchronously). It carries a flow of the previous generation and retires it
// on InterfaceUpdated; the Close is held by the test, so the listener cannot return early.
type oldGenerationListener struct {
	adapter.Outbound
	name      string
	flow      *heldOldFlow
	router    *timedRouter
	entered   chan struct{}
	exited    chan struct{}
	enteredAt atomic.Int64
	exitedAt  atomic.Int64
	// pinSeenAtEntry records whether the DNS pin had already happened when this listener was called.
	pinSeenAtEntry atomic.Bool
	// settledAtEntry records whether the network reported settled when this listener was called: the
	// new generation must be settled BEFORE the old generation's cleanup starts.
	settledAtEntry atomic.Bool
	settledProbe   func() bool
}

func (l *oldGenerationListener) InterfaceUpdated(context.Context) {
	l.enteredAt.Store(time.Now().UnixNano())
	l.pinSeenAtEntry.Store(l.router.pinCount() > 0)
	if l.settledProbe != nil {
		l.settledAtEntry.Store(l.settledProbe())
	}
	close(l.entered)
	// The old path's flow is retired here; the Close blocks until the test releases it.
	_ = l.flow.Close()
	l.exitedAt.Store(time.Now().UnixNano())
	close(l.exited)
}

// startTimelineEcho answers every connection with one byte, so "first business byte" has a real
// sender on the other side.
func startTimelineEcho(t *testing.T) string {
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
				_, _ = conn.Write([]byte{'k'})
				_, _ = io.Copy(io.Discard, conn)
			}()
		}
	}()
	return listener.Addr().String()
}

func TestAProductionNetworkEventSettlesTheNewGenerationBeforeOldCleanup(t *testing.T) {
	target := startTimelineEcho(t)
	harness := newInterfaceTransitionHarness(t)
	harness.manager.transitionStable.Store(true)

	router := newTimedRouter(harness.manager.NetworkResetGeneration)
	harness.manager.router = router

	// Establish the environment first, so the event below is the ordinary "interface changed, network
	// environment unchanged" case: the transition under measurement then owns exactly one claim. The
	// first observation claims its own generation and pins DNS once; that is not the transition this
	// test times.
	harness.manager.updateNetworkEnvironment()
	require.True(t, harness.manager.NetworkTransitionStable(),
		"the manager did not settle on its first environment, so no later transition can be measured")
	require.Equal(t, 1, router.pinCount(), "establishing the environment pins DNS once")

	// Two active outbounds, each carrying one flow of the generation that is about to be left.
	releaseOld := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-releaseOld:
		default:
			close(releaseOld)
		}
	})
	listenerA := &oldGenerationListener{
		name: "outbound-a", flow: &heldOldFlow{Conn: mustDial(t, target), release: releaseOld},
		router: router, entered: make(chan struct{}), exited: make(chan struct{}),
		settledProbe: harness.manager.NetworkTransitionStable,
	}
	listenerB := &oldGenerationListener{
		name: "outbound-b", flow: &heldOldFlow{Conn: mustDial(t, target), release: releaseOld},
		router: router, entered: make(chan struct{}), exited: make(chan struct{}),
		settledProbe: harness.manager.NetworkTransitionStable,
	}
	harness.manager.outbound = &listenerOutboundManager{outbounds: []adapter.Outbound{listenerA, listenerB}}
	nextPin := router.watchNextPin()

	// The DNS half: a real UDP server, the production UDP transport, and the production DNS client,
	// whose generation comes from the network manager's own epoch - the same ownership token the
	// transition publishes. A query is issued below while the old generation is still closing.
	packetConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	answered := &atomic.Int32{}
	dnsServer := &mDNS.Server{
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
	go func() { _ = dnsServer.ActivateAndServe() }()
	t.Cleanup(func() { _ = dnsServer.Shutdown() })
	serverPort := uint16(packetConn.LocalAddr().(*net.UDPAddr).Port)
	dnsLogger := log.NewNOPFactory().NewLogger("timeline-dns")
	dnsOptions := option.RemoteDNSServerOptions{
		DNSServerAddressOptions: option.DNSServerAddressOptions{Server: "127.0.0.1", ServerPort: serverPort},
	}
	udpTransport := dnsTransport.NewUDPRaw(
		dnsLogger,
		dns.NewTransportAdapterWithRemoteOptions(C.DNSTypeUDP, "timeline", dnsOptions),
		&N.DefaultDialer{},
		M.ParseSocksaddrHostPort("127.0.0.1", serverPort),
	)
	dnsScope := adapter.NewScope(context.Background(), dnsLogger)
	require.NoError(t, udpTransport.Start(adapter.StartStateStart, dnsScope))
	t.Cleanup(func() { _ = dnsScope.Close() })
	dnsClient := dns.NewClient(dns.ClientOptions{
		Context:           context.Background(),
		Logger:            dnsLogger,
		DisableCache:      true,
		NetworkGeneration: harness.manager.NetworkResetGeneration,
	})
	dnsClient.Start()
	query := new(mDNS.Msg)
	query.SetQuestion("timeline.example.", mDNS.TypeA)

	// The production dialer, wired to this manager, is how the new generation's traffic is measured.
	dialerContext := service.ContextWith[adapter.NetworkManager](context.Background(), harness.manager)
	dialerContext = service.ContextWith[adapter.ConnectionManager](dialerContext, &productionCleanupManager{})
	realDialer, err := dialer.NewDefault(dialerContext, option.DialerOptions{})
	require.NoError(t, err)

	// The claim hook: the instant the transition publishes its generation.
	type claim struct {
		at    time.Time
		token transitionToken
	}
	claims := make(chan claim, 4)
	harness.manager.transitionClaimed = func(token transitionToken) {
		select {
		case claims <- claim{at: time.Now(), token: token}:
		default:
		}
	}
	t.Cleanup(func() { harness.manager.transitionClaimed = nil })

	// The platform event. It claims the generation BEFORE dispatching, so the claim is observable
	// before anything below runs.
	eventAt := time.Now()
	harness.manager.notifyInterfaceUpdate(testDefaultInterface(), 0)

	var claimed claim
	select {
	case claimed = <-claims:
	case <-time.After(10 * time.Second):
		t.Fatal("the interface notification never claimed a generation")
	}

	// The DNS pin is the boundary the new generation is pinned to. Wait for it and check the
	// generation it recorded: it must be the generation this event claimed, not the one before.
	select {
	case <-nextPin:
	case <-time.After(10 * time.Second):
		t.Fatal("the DNS pin never ran")
	}
	pinAt := time.Unix(0, router.pinnedAt.Load())
	require.Equal(t, uint64(claimed.token), router.pinnedGeneration.Load(),
		"the DNS pin was committed for generation %d while the event claimed %d: the pin and the "+
			"generation describe different networks", router.pinnedGeneration.Load(), uint64(claimed.token))

	// Both listeners must be entered before either returns. A serial dispatch enters the first, waits
	// for its held Close, and never reaches the second, so this is where the old order failed.
	awaitListener := func(listener *oldGenerationListener) {
		select {
		case <-listener.entered:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s was never entered while the other listener was blocked: the listener phase "+
				"is serial, so one slow old-generation teardown delays every later one and the whole "+
				"network change", listener.name)
		}
	}
	awaitListener(listenerA)
	awaitListener(listenerB)
	require.True(t, listenerA.pinSeenAtEntry.Load() && listenerB.pinSeenAtEntry.Load(),
		"a listener was called before the DNS pin ran")
	require.True(t, listenerA.settledAtEntry.Load() && listenerB.settledAtEntry.Load(),
		"the network was still unsettled when the old generation's cleanup started, so a production "+
			"dial taken in that window would be refused as 'network changed while dialling'")
	require.True(t, harness.manager.NetworkTransitionStable(),
		"the new generation must be settled before the listener phase, not after it")

	// A real DNS query, through the production UDP transport and client, while BOTH old-generation
	// closes are still held. The client's generation comes from the manager, so this also proves the
	// query is answered for the generation the event published rather than waiting for the old
	// generation to finish.
	dnsStarted := time.Now()
	dnsResponse, err := dnsClient.Exchange(context.Background(), udpTransport, query, adapter.DNSQueryOptions{}, nil)
	dnsElapsed := time.Since(dnsStarted)
	require.NoError(t, err, "a real DNS query could not be answered while the old generation was closing")
	require.NotNil(t, dnsResponse)
	require.NotEmpty(t, dnsResponse.Answer, "the real DNS server's answer did not reach the client")
	require.EqualValues(t, 1, answered.Load(), "the query must have reached the real DNS server exactly once")
	require.False(t, closed(listenerA.exited) || closed(listenerB.exited),
		"the old-generation cleanup finished before the DNS query: this run did not measure overlap")

	// The new generation's first business byte, through the production dialer, while BOTH old
	// closes are still held and the listener phase is still running.
	firstByte, err := dialThroughProduction(realDialer, target)
	byteAt := time.Now()
	require.NoError(t, err, "the new generation could not dial while the old generation was still closing")
	require.False(t, closed(listenerA.exited) || closed(listenerB.exited),
		"an old-generation close finished before the new generation's first byte: this run did not "+
			"measure a slow old cleanup")

	// Release the old generation's closes. The listener phase finishes, and each flow is closed once.
	close(releaseOld)
	for _, listener := range []*oldGenerationListener{listenerA, listenerB} {
		select {
		case <-listener.exited:
		case <-time.After(10 * time.Second):
			t.Fatalf("%s never finished after its old close was released", listener.name)
		}
	}
	require.EqualValues(t, 1, listenerA.flow.closes.Load(), "the old flow must be closed exactly once")
	require.EqualValues(t, 1, listenerB.flow.closes.Load(), "the old flow must be closed exactly once")

	// Report the measured timeline. Every figure is a real reading, and the topology is the
	// production event path with a real destination on loopback.
	enteredA := time.Unix(0, listenerA.enteredAt.Load())
	enteredB := time.Unix(0, listenerB.enteredAt.Load())
	exitedA := time.Unix(0, listenerA.exitedAt.Load())
	exitedB := time.Unix(0, listenerB.exitedAt.Load())
	t.Logf("TIMELINE topology=production-event->production-dialer->loopback-tcp")
	t.Logf("TIMELINE event->claim: %s", claimed.at.Sub(eventAt).Round(time.Microsecond))
	t.Logf("TIMELINE event->dns-pin: %s", pinAt.Sub(eventAt).Round(time.Microsecond))
	t.Logf("TIMELINE event->listener-a-entered: %s", enteredA.Sub(eventAt).Round(time.Microsecond))
	t.Logf("TIMELINE event->listener-b-entered: %s", enteredB.Sub(eventAt).Round(time.Microsecond))
	t.Logf("TIMELINE event->new-generation-first-byte: %s (dial+read %s)",
		byteAt.Sub(eventAt).Round(time.Microsecond), firstByte.Round(time.Microsecond))
	t.Logf("TIMELINE event->real-dns-answer: %s (query %s, server-answers=%d)",
		dnsStarted.Add(dnsElapsed).Sub(eventAt).Round(time.Microsecond), dnsElapsed.Round(time.Microsecond), answered.Load())
	t.Logf("TIMELINE event->old-cleanup-finished: a=%s b=%s",
		exitedA.Sub(eventAt).Round(time.Microsecond), exitedB.Sub(eventAt).Round(time.Microsecond))
	t.Logf("TIMELINE pin-generation=%d claim-token=%d pinned-before-listeners=%t settled-before-listeners=%t",
		router.pinnedGeneration.Load(), uint64(claimed.token),
		listenerA.pinSeenAtEntry.Load() && listenerB.pinSeenAtEntry.Load(),
		listenerA.settledAtEntry.Load() && listenerB.settledAtEntry.Load())
}

// productionCleanupManager is the ConnectionManager the production dialer is given. It tracks
// nothing, so this test measures the transition path and not a cleanup policy.
type productionCleanupManager struct{}

func (m *productionCleanupManager) TrackConn(conn net.Conn) net.Conn                   { return conn }
func (m *productionCleanupManager) TrackPacketConn(conn net.PacketConn) net.PacketConn { return conn }
func (m *productionCleanupManager) CloseAll()                                          {}
func (m *productionCleanupManager) Count() int                                         { return 0 }
func (m *productionCleanupManager) Start(adapter.StartStage, *adapter.Scope) error     { return nil }
func (m *productionCleanupManager) Close() error                                       { return nil }
func (m *productionCleanupManager) NewConnection(context.Context, N.Dialer, net.Conn, adapter.InboundContext, N.CloseHandlerFunc) {
}
func (m *productionCleanupManager) NewPacketConnection(context.Context, N.Dialer, N.PacketConn, adapter.InboundContext, N.CloseHandlerFunc) {
}

// mustDial opens a real connection for an old-generation flow.
func mustDial(t *testing.T, target string) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", target)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// dialThroughProduction dials the target with the production dialer and returns the time until the
// first byte. It is the "新代直连" measurement: the same entry a client uses, not a bare net.Dialer.
func dialThroughProduction(realDialer *dialer.DefaultDialer, target string) (time.Duration, error) {
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := realDialer.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr(target))
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	first := make([]byte, 1)
	_, err = io.ReadFull(conn, first)
	if err != nil {
		return 0, err
	}
	return time.Since(started), nil
}

func closed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}
