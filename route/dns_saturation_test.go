package route

// DNS's own reservation is a resource contract too: when all reserved control-plane slots are held,
// a cold query must fail with its context (not consume normal budget, not leak an admitted slot), and
// releasing one reservation must restore resolution. Also pins that a canceled admission waiter does
// not leave a reserved slot behind.

import (
	"context"
	"net"
	"testing"
	"time"

	mDNS "github.com/miekg/dns"
	"github.com/sagernet/sing-box/adapter"
	dnsTransport "github.com/sagernet/sing-box/dns/transport"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// coldDNSFixture is a real UDP DNS server plus a production UDP transport wired to manager.
type coldDNSFixture struct {
	transport adapter.DNSTransport
	query     *mDNS.Msg
	answered  chan struct{}
}

func newColdDNSFixture(t *testing.T, transportCtx context.Context, question string) *coldDNSFixture {
	t.Helper()
	packetConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	answered := make(chan struct{}, 8)
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

	transport, err := dnsTransport.NewUDP(transportCtx, log.NewNOPFactory().NewLogger("dns-fixture"), "fixture", option.RemoteDNSServerOptions{
		DNSServerAddressOptions: option.DNSServerAddressOptions{Server: "127.0.0.1", ServerPort: serverPort},
	})
	require.NoError(t, err)
	scope := adapter.NewScope(context.Background(), log.NewNOPFactory().NewLogger("dns-fixture-scope"))
	require.NoError(t, transport.Start(adapter.StartStateStart, scope))
	t.Cleanup(func() { _ = scope.Close() })

	query := new(mDNS.Msg)
	query.SetQuestion(question, mDNS.TypeA)
	return &coldDNSFixture{transport: transport, query: query, answered: answered}
}

// TestTheDNSReservationIsBoundedAndRecovers drives the reservation through the real transport.
func TestTheDNSReservationIsBoundedAndRecovers(t *testing.T) {
	harness := newInterfaceTransitionHarness(t)
	harness.manager.transitionStable.Store(true)
	router := newTimedRouter(harness.manager.NetworkResetGeneration)
	harness.manager.router = router
	harness.manager.updateNetworkEnvironment()
	require.True(t, harness.manager.NetworkTransitionStable())

	connectionManager := NewConnectionManager(log.NewNOPFactory().Logger())
	t.Cleanup(func() { _ = connectionManager.Close() })
	harness.manager.connectionManager = connectionManager

	controlCtx := adapter.WithControlPlaneAdmission(context.Background())
	tickets := make([]adapter.ConnectionTicket, 0, controlPlaneAdmissionReservation)
	for index := 0; index < controlPlaneAdmissionReservation; index++ {
		ticket, err := connectionManager.AcquireConnection(controlCtx)
		require.NoError(t, err)
		tickets = append(tickets, ticket)
	}
	require.Equal(t, controlPlaneAdmissionReservation, connectionManager.priorityAdmittedConnections())

	transportCtx := service.ContextWith[adapter.ConnectionManager](context.Background(), connectionManager)
	transportCtx = service.ContextWith[adapter.NetworkManager](transportCtx, harness.manager)
	fixture := newColdDNSFixture(t, transportCtx, "dns-reservation-exhausted.example.")

	exhaustedCtx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	_, err := fixture.transport.Exchange(exhaustedCtx, fixture.query)
	cancel()
	require.Error(t, err, "a cold query was admitted past the DNS reservation")
	select {
	case <-fixture.answered:
		t.Fatal("the DNS server saw the query even though the reservation was exhausted")
	default:
	}
	require.Equal(t, 0, connectionManager.pendingDisposals(), "the failed DNS dial leaked a disposal object")

	// Return one reservation: the same transport must resolve now.
	tickets[0].Release()
	tickets = tickets[1:]
	t.Cleanup(func() {
		for _, ticket := range tickets {
			ticket.Release()
		}
	})
	recoveryCtx, recoveryCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer recoveryCancel()
	response, err := fixture.transport.Exchange(recoveryCtx, fixture.query)
	require.NoError(t, err, "the cold query did not recover after a reservation was released")
	require.NotEmpty(t, response.Answer)
	select {
	case <-fixture.answered:
	default:
		t.Fatal("the DNS server never received the recovered query")
	}
}

// TestACanceledAdmissionWaiterDoesNotLeakASlot: a dial canceled while waiting for admission must not
// leave a reservation behind, and must not consume the slot it never acquired.
func TestACanceledAdmissionWaiterDoesNotLeakASlot(t *testing.T) {
	manager := NewConnectionManager(log.NewNOPFactory().Logger())
	manager.disposalBudgetOverride = 1
	t.Cleanup(func() { _ = manager.Close() })

	held, err := manager.AcquireConnection(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, manager.admittedConnections())

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = manager.AcquireConnection(canceled)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, manager.admittedConnections(), "the canceled waiter leaked or double-counted a slot")

	held.Release()
	require.Equal(t, 0, manager.admittedConnections())
	next, err := manager.AcquireConnection(context.Background())
	require.NoError(t, err)
	next.Release()
	require.Equal(t, 0, manager.admittedConnections())
}
