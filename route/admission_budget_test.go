package route

// Admission is a real reservation, not a stale observation, and every dial entry point goes through
// it. These tests pin the allocation bound, the control-plane reservation, the generation stamp of a
// ticket consumed after a transition, and the closed-manager contract for TCP/UDP/ListenPacket.

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// TestEveryDialEntryPointRefusesAClosedManager: a manager that has been closed must not accept a TCP
// dial, a UDP dial or a ListenPacket. Each is a real dialer entry, and each must hand back an error
// rather than a usable socket.
func TestEveryDialEntryPointRefusesAClosedManager(t *testing.T) {
	harness := newInterfaceTransitionHarness(t)
	connectionManager := NewConnectionManager(log.NewNOPFactory().Logger())
	require.NoError(t, connectionManager.Close())
	realDialer := newGenerationDialer(t, harness.manager, connectionManager)

	tcpListener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = tcpListener.Close() })
	go func() {
		for {
			conn, acceptErr := tcpListener.Accept()
			if acceptErr != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	tcpConn, tcpErr := realDialer.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr(tcpListener.Addr().String()))
	if tcpConn != nil {
		_ = tcpConn.Close()
	}
	require.Error(t, tcpErr, "a closed manager admitted a TCP dial")

	udpConn, udpErr := realDialer.DialContext(ctx, N.NetworkUDP, M.ParseSocksaddr(tcpListener.Addr().String()))
	if udpConn != nil {
		_ = udpConn.Close()
	}
	require.Error(t, udpErr, "a closed manager admitted a UDP dial")

	packetConn, packetErr := realDialer.ListenPacket(ctx, M.ParseSocksaddr(tcpListener.Addr().String()))
	if packetConn != nil {
		_ = packetConn.Close()
	}
	require.Error(t, packetErr, "a closed manager admitted a ListenPacket")
	require.Equal(t, 0, connectionManager.Count())
}

// TestAdmissionSlotsAreAllocatedNotJustObserved: with a budget of two, exactly two of ten concurrent
// acquirers may hold a slot at once. A check-then-act admission would let all ten through.
func TestAdmissionSlotsAreAllocatedNotJustObserved(t *testing.T) {
	manager := NewConnectionManager(log.NewNOPFactory().Logger())
	manager.disposalBudgetOverride = 2
	t.Cleanup(func() { _ = manager.Close() })

	const callers = 10
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var held atomic.Int32
	tickets := make(chan adapter.ConnectionTicket, callers)
	var waitGroup sync.WaitGroup
	for index := 0; index < callers; index++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			ticket, err := manager.AcquireConnection(ctx)
			if err != nil {
				return
			}
			held.Add(1)
			tickets <- ticket
		}()
	}
	require.Eventually(t, func() bool { return manager.admittedConnections() == 2 },
		2*time.Second, 5*time.Millisecond, "the budget did not bound the reservations")
	time.Sleep(100 * time.Millisecond)
	require.EqualValues(t, 2, held.Load(), "more than the budget was authorized")
	require.Equal(t, 2, manager.admittedConnections())

	// Releasing one slot admits exactly one more waiter.
	(<-tickets).Release()
	require.Eventually(t, func() bool { return held.Load() == 3 }, 2*time.Second, 5*time.Millisecond)

	// Drain the authorized tickets and cancel the rest; the ledger must return to zero.
	for {
		select {
		case ticket := <-tickets:
			ticket.Release()
			continue
		default:
		}
		break
	}
	cancel()
	waitGroup.Wait()
	for len(tickets) > 0 {
		(<-tickets).Release()
	}
	require.Eventually(t, func() bool { return manager.admittedConnections() == 0 },
		2*time.Second, 5*time.Millisecond)
}

// TestControlPlaneReservationIsBounded: DNS may bypass the normal budget, but only up to its own
// reservation.
func TestControlPlaneReservationIsBounded(t *testing.T) {
	manager := NewConnectionManager(log.NewNOPFactory().Logger())
	t.Cleanup(func() { _ = manager.Close() })
	controlCtx := adapter.WithControlPlaneAdmission(context.Background())

	for index := 0; index < controlPlaneAdmissionReservation; index++ {
		ticket, err := manager.AcquireConnection(controlCtx)
		require.NoError(t, err)
		t.Cleanup(ticket.Release)
	}
	require.Equal(t, controlPlaneAdmissionReservation, manager.priorityAdmittedConnections())

	blockedCtx, cancel := context.WithTimeout(controlCtx, 100*time.Millisecond)
	defer cancel()
	_, err := manager.AcquireConnection(blockedCtx)
	require.ErrorIs(t, err, context.DeadlineExceeded,
		"the control-plane pool admitted more than its reservation")
}

// TestATicketConsumedAfterATransitionBelongsToTheNewGeneration: the ticket does not pin the old
// generation. A dial authorized before a transition but tracked after it is stamped with the new one
// and is not immediately reclaimed by the next transition.
func TestATicketConsumedAfterATransitionBelongsToTheNewGeneration(t *testing.T) {
	manager := NewConnectionManager(log.NewNOPFactory().Logger())
	t.Cleanup(func() { _ = manager.Close() })

	ticket, err := manager.AcquireConnection(context.Background())
	require.NoError(t, err)
	manager.Reclaim(ReclaimNetworkTransition)
	newGeneration := manager.generation.Load()

	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	tracked, trackErr := ticket.TrackConn(client)
	require.NoError(t, trackErr)
	trackedConnValue, isTracked := tracked.(*trackedConn)
	require.True(t, isTracked)
	state := trackedConnValue.reclaimState()
	require.Equal(t, newGeneration, state.generation,
		"a connection tracked after the transition was stamped with the old generation")
	require.Equal(t, 1, manager.Count())
}

// pausingAdmission wraps a real manager and pauses the production dialer between the ticket
// reservation and the tracking call. It is the 12-order's "authorized, not yet registered" attack
// driven through the real DefaultDialer, not the manager API alone.
type pausingAdmission struct {
	*ConnectionManager
	staged chan struct{}
	resume chan struct{}
}

func (p *pausingAdmission) AcquireConnection(ctx context.Context) (adapter.ConnectionTicket, error) {
	ticket, err := p.ConnectionManager.AcquireConnection(ctx)
	if err != nil {
		return nil, err
	}
	select {
	case <-p.staged:
	default:
		close(p.staged)
	}
	<-p.resume
	return ticket, nil
}

func TestTheDialerCannotAdmitAfterShutdownMidHandoff(t *testing.T) {
	harness := newInterfaceTransitionHarness(t)
	manager := NewConnectionManager(log.NewNOPFactory().Logger())
	pausing := &pausingAdmission{ConnectionManager: manager, staged: make(chan struct{}), resume: make(chan struct{})}
	var resumeOnce sync.Once
	releaseDialer := func() { resumeOnce.Do(func() { close(pausing.resume) }) }
	t.Cleanup(releaseDialer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	accepted := make(chan net.Conn, 1)
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			select {
			case accepted <- conn:
			default:
				_ = conn.Close()
			}
		}
	}()

	realDialer := newGenerationDialer(t, harness.manager, pausing)
	dialResult := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		conn, dialErr := realDialer.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr(listener.Addr().String()))
		if conn != nil {
			_ = conn.Close()
		}
		dialResult <- dialErr
	}()
	select {
	case <-pausing.staged:
	case <-time.After(5 * time.Second):
		t.Fatal("the dialer never reached the admission hand-off")
	}

	closeDone := make(chan struct{})
	go func() {
		defer close(closeDone)
		_ = manager.Close()
	}()
	require.Eventually(t, manager.closed.Load, 2*time.Second, time.Millisecond)
	releaseDialer()

	select {
	case dialErr := <-dialResult:
		require.ErrorIs(t, dialErr, net.ErrClosed,
			"the dialer returned from a hand-off that shutdown had revoked")
	case <-time.After(5 * time.Second):
		t.Fatal("the dial never returned")
	}
	select {
	case <-closeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return after the revoked hand-off settled")
	}
	require.Equal(t, 0, manager.Count())
	require.Equal(t, 0, manager.admittedConnections())
	if conn := <-accepted; conn != nil {
		_ = conn.Close()
	}
}

func TestPriorityTicketsAreReleasedByTrackingAndDoubleReleaseIsIdempotent(t *testing.T) {
	manager := NewConnectionManager(log.NewNOPFactory().Logger())
	t.Cleanup(func() { _ = manager.Close() })
	controlCtx := adapter.WithControlPlaneAdmission(context.Background())

	for index := 0; index < controlPlaneAdmissionReservation; index++ {
		ticket, err := manager.AcquireConnection(controlCtx)
		require.NoError(t, err)
		client, server := net.Pipe()
		tracked, trackErr := ticket.TrackConn(client)
		require.NoError(t, trackErr)
		require.NotNil(t, tracked)
		_ = server.Close()
		t.Cleanup(func() { _ = client.Close() })
	}
	require.Equal(t, 0, manager.priorityAdmittedConnections(),
		"tracking must return the control-plane reservation")

	ticket, err := manager.AcquireConnection(context.Background())
	require.NoError(t, err)
	ticket.Release()
	ticket.Release()
	require.Equal(t, 0, manager.admittedConnections(),
		"a second Release must not underflow or double-return the slot")
}
