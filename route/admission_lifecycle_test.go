package route

// The three lifecycle/saturation cases from the 11-order, plus the admission ticket's own shutdown
// contract.
//
// 1. admission-to-registration: the ticket is acquired atomically with the manager's lifecycle; a
//    Close between acquisition and tracking makes the tracking fail, and the rejected socket is put
//    in the pending ledger so the final Close still waits for its physical close.
// 2. final Close vs a synchronous CloseAll: CloseAll counts the objects it is closing inline in the
//    syncing account, so Close cannot return while one of them is still inside the kernel.
// 3. production DNS under saturation: the DNS transport's real remote dialer carries the control
//    plane marker and draws from the reserved pool, so a cold query resolves while normal admission
//    is at its budget.

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	mDNS "github.com/miekg/dns"
	"github.com/sagernet/sing-box/adapter"
	dnsTransport "github.com/sagernet/sing-box/dns/transport"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// countingPacketConn counts physical closes without blocking, for lifecycle tests that must not hang
// on a barrier.
type countingPacketConn struct {
	net.PacketConn
	closes atomic.Int32
}

func (c *countingPacketConn) Close() error {
	c.closes.Add(1)
	return c.PacketConn.Close()
}

func mustListenPacket(t *testing.T) net.PacketConn {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// TestAdmissionTicketCannotOutliveTheManager is red 1 fixed: the slot is acquired before tracking,
// shutdown revokes it, and the socket the dial already owns is closed through the pending ledger.
func TestAdmissionTicketCannotOutliveTheManager(t *testing.T) {
	manager := NewConnectionManager(log.NewNOPFactory().Logger())
	ticket, err := manager.AcquireConnection(context.Background())
	require.NoError(t, err)

	closeDone := make(chan struct{})
	go func() {
		defer close(closeDone)
		_ = manager.Close()
	}()
	require.Eventually(t, manager.closed.Load, 2*time.Second, time.Millisecond,
		"shutdown never began; the tracking below would race an open manager")

	counted := &countingPacketConn{PacketConn: mustListenPacket(t)}
	tracked, trackErr := ticket.TrackPacketConn(counted)
	require.ErrorIs(t, trackErr, net.ErrClosed)
	require.Nil(t, tracked)
	require.Eventually(t, func() bool { return counted.closes.Load() == 1 },
		5*time.Second, 5*time.Millisecond, "the rejected socket was not physically closed")
	select {
	case <-closeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return after the rejected ticket settled")
	}
	require.Equal(t, 0, manager.Count(), "the closed manager accepted the connection")
	require.Equal(t, 0, manager.pendingDisposals())
}

// TestFinalCloseWaitsForAnOutstandingAdmission: a dial authorized before shutdown delays the final
// Close until it settles; release returns it without a socket.
func TestFinalCloseWaitsForAnOutstandingAdmission(t *testing.T) {
	manager := NewConnectionManager(log.NewNOPFactory().Logger())
	ticket, err := manager.AcquireConnection(context.Background())
	require.NoError(t, err)

	closeDone := make(chan struct{})
	go func() {
		defer close(closeDone)
		_ = manager.Close()
	}()
	select {
	case <-closeDone:
		t.Fatal("Close returned with an authorized dial still outstanding")
	case <-time.After(100 * time.Millisecond):
	}
	ticket.Release()
	select {
	case <-closeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return after the ticket was released")
	}
}

// TestFinalCloseWaitsForASynchronousCloseAll is red 2 fixed: the condemned object, taken out of the
// ledger by CloseAll, is counted as a synchronous close until its physical Close returns.
func TestFinalCloseWaitsForASynchronousCloseAll(t *testing.T) {
	manager := NewConnectionManager(log.NewNOPFactory().Logger())
	manager.disposalBudgetOverride = 1
	t.Cleanup(func() { _ = manager.Close() })

	conns := make([]*blockedPacketClose, 0, 2)
	for index := 0; index < 2; index++ {
		conn := newBlockedPacketClose(t)
		conns = append(conns, conn)
		manager.TrackPacketConn(conn)
	}
	manager.Reclaim(ReclaimNetworkTransition)
	require.Equal(t, 1, manager.pendingDisposals())
	require.Equal(t, 1, manager.condemnedDisposals())

	closeAllDone := make(chan struct{})
	go func() {
		defer close(closeAllDone)
		manager.CloseAll()
	}()
	select {
	case <-conns[1].entered:
	case <-time.After(5 * time.Second):
		t.Fatal("CloseAll never started closing the condemned socket")
	}
	require.Equal(t, 1, manager.synchronousCloses(),
		"the synchronous close must be visible to the final wait")

	closeDone := make(chan struct{})
	go func() {
		defer close(closeDone)
		_ = manager.Close()
	}()
	conns[0].releaseClose()
	select {
	case <-closeDone:
		t.Fatal("final Close returned while CloseAll was still inside the condemned socket's Close")
	case <-time.After(100 * time.Millisecond):
	}
	conns[1].releaseClose()
	select {
	case <-closeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("final Close did not return after both sockets were released")
	}
	<-closeAllDone
	require.Equal(t, 0, manager.pendingDisposals())
	require.Equal(t, 0, manager.synchronousCloses())
}

// TestAcquireConnectionRefusesCancelledContextWithRoom pins the fast path: a caller that has already
// withdrawn must not be admitted just because the manager has room. The old path consulted the
// context only while waiting, so a cancelled dial still consumed a slot and could install a socket
// after a drain.
func TestAcquireConnectionRefusesCancelledContextWithRoom(t *testing.T) {
	manager := NewConnectionManager(log.NewNOPFactory().Logger())
	t.Cleanup(func() { _ = manager.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ticket, err := manager.AcquireConnection(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, ticket)

	manager.disposal.mu.Lock()
	admitted := manager.disposal.admitted
	manager.disposal.mu.Unlock()
	require.Zero(t, admitted, "a cancelled context must not consume an admission slot")
}

// TestColdProductionDNSStillResolvesWhenTheDisposalBacklogIsFull is red 3 fixed.
func TestColdProductionDNSStillResolvesWhenTheDisposalBacklogIsFull(t *testing.T) {
	harness := newInterfaceTransitionHarness(t)
	harness.manager.transitionStable.Store(true)
	router := newTimedRouter(harness.manager.NetworkResetGeneration)
	harness.manager.router = router
	harness.manager.updateNetworkEnvironment()
	require.True(t, harness.manager.NetworkTransitionStable())

	connectionManager := NewConnectionManager(log.NewNOPFactory().Logger())
	t.Cleanup(func() { _ = connectionManager.Close() })
	harness.manager.connectionManager = connectionManager

	// Fill the disposal queue to the production budget with blocked real UDP sockets.
	for index := 0; index < disposalBacklogBudget; index++ {
		conn := newBlockedPacketClose(t)
		connectionManager.TrackPacketConn(conn)
	}
	connectionManager.Reclaim(ReclaimNetworkTransition)
	require.Equal(t, disposalBacklogBudget, connectionManager.pendingDisposals())

	// Normal admission is exhausted: a plain dial must fail with its context. The listener accepts so
	// the TCP dial itself succeeds and the failure can only come from admission.
	acceptListener, listenErr := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, listenErr)
	t.Cleanup(func() { _ = acceptListener.Close() })
	go func() {
		for {
			conn, acceptErr := acceptListener.Accept()
			if acceptErr != nil {
				return
			}
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()
	plainDialer := newGenerationDialer(t, harness.manager, connectionManager)
	plainCtx, plainCancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	plainConn, plainErr := plainDialer.DialContext(plainCtx, N.NetworkTCP, M.ParseSocksaddr(acceptListener.Addr().String()))
	plainCancel()
	if plainConn != nil {
		_ = plainConn.Close()
	}
	require.Error(t, plainErr, "normal admission admitted a dial while the backlog was at the budget")

	// A real DNS server and a cold production UDP transport wired to the same manager.
	packetConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	answered := make(chan struct{}, 4)
	server := &mDNS.Server{
		PacketConn: packetConn,
		Handler: mDNS.HandlerFunc(func(writer mDNS.ResponseWriter, request *mDNS.Msg) {
			select {
			case answered <- struct{}{}:
			default:
			}
			response := new(mDNS.Msg)
			response.SetReply(request)
			response.Answer = append(response.Answer, &mDNS.A{
				Hdr: mDNS.RR_Header{Name: request.Question[0].Name, Rrtype: mDNS.TypeA, Class: mDNS.ClassINET, Ttl: 60},
				A:   net.IPv4(127, 0, 0, 1),
			})
			_ = writer.WriteMsg(response)
		}),
	}
	go func() { _ = server.ActivateAndServe() }()
	t.Cleanup(func() { _ = server.Shutdown() })
	serverPort := uint16(packetConn.LocalAddr().(*net.UDPAddr).Port)

	transportCtx := service.ContextWith[adapter.ConnectionManager](context.Background(), connectionManager)
	transportCtx = service.ContextWith[adapter.NetworkManager](transportCtx, harness.manager)
	transport, err := dnsTransport.NewUDP(transportCtx, log.NewNOPFactory().NewLogger("dns-cold"), "cold", option.RemoteDNSServerOptions{
		DNSServerAddressOptions: option.DNSServerAddressOptions{Server: "127.0.0.1", ServerPort: serverPort},
	})
	require.NoError(t, err)
	scope := adapter.NewScope(context.Background(), log.NewNOPFactory().NewLogger("dns-cold-scope"))
	require.NoError(t, transport.Start(adapter.StartStateStart, scope))
	t.Cleanup(func() { _ = scope.Close() })

	query := new(mDNS.Msg)
	query.SetQuestion("cold-dns-under-saturation.example.", mDNS.TypeA)
	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	response, err := transport.Exchange(ctx, query)
	require.NoError(t, err, "the cold production DNS query failed while the disposal queue was full")
	require.NotEmpty(t, response.Answer)
	select {
	case <-answered:
	default:
		t.Fatal("the DNS server never received the query: the transport's dial never happened")
	}
	require.Equal(t, disposalBacklogBudget, connectionManager.pendingDisposals(),
		"the DNS control-plane dial must not steal the normal disposal budget")
}
