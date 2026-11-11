package route

// One admission ticket is consumed exactly once. The four cases here are the ones the audit found
// missing: a consumed ticket must not touch another ticket's admission, a second Release must be
// inert, a second Track must be refused instead of registering a second connection, and Close must
// still wait for a ticket that has not been consumed yet.

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/common/logger"

	"github.com/stretchr/testify/require"
)

func newTicketTestManager(t *testing.T) *ConnectionManager {
	t.Helper()
	manager := NewConnectionManager(log.NewNOPFactory().Logger())
	t.Cleanup(func() { _ = manager.Close() })
	return manager
}

// TestConsumedTicketDoesNotTouchAnotherTicketsAdmission is the audit's red case: A hands over a
// connection successfully (so its slot is returned), and a later Release(A) must not decrement the
// slot B still holds. Both protocols go through their own Track entry point.
func TestConsumedTicketDoesNotTouchAnotherTicketsAdmission(t *testing.T) {
	for _, udp := range []bool{false, true} {
		name := "TCP"
		if udp {
			name = "UDP"
		}
		t.Run(name, func(t *testing.T) {
			manager := newTicketTestManager(t)
			first, err := manager.AcquireConnection(context.Background())
			require.NoError(t, err)
			second, err := manager.AcquireConnection(context.Background())
			require.NoError(t, err)
			defer second.Release()
			require.Equal(t, 2, manager.admittedConnections())

			if udp {
				packetConn, listenErr := net.ListenPacket("udp", "127.0.0.1:0")
				require.NoError(t, listenErr)
				tracked, trackErr := first.TrackPacketConn(packetConn)
				require.NoError(t, trackErr)
				t.Cleanup(func() { _ = tracked.Close() })
			} else {
				clientConn, peerConn := net.Pipe()
				t.Cleanup(func() { _ = peerConn.Close() })
				tracked, trackErr := first.TrackConn(clientConn)
				require.NoError(t, trackErr)
				t.Cleanup(func() { _ = tracked.Close() })
			}
			require.Equal(t, 1, manager.admittedConnections(), "the hand-over returns A's slot")

			first.Release()
			require.Equal(t, 1, manager.admittedConnections(),
				"releasing the consumed ticket A must not return B's slot")
		})
	}
}

// TestRepeatedReleaseIsInert: a ticket settled by its hand-over stays settled.
func TestRepeatedReleaseIsInert(t *testing.T) {
	manager := newTicketTestManager(t)
	first, err := manager.AcquireConnection(context.Background())
	require.NoError(t, err)
	second, err := manager.AcquireConnection(context.Background())
	require.NoError(t, err)
	defer second.Release()

	clientConn, peerConn := net.Pipe()
	t.Cleanup(func() { _ = peerConn.Close() })
	tracked, err := first.TrackConn(clientConn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tracked.Close() })

	for index := 0; index < 5; index++ {
		first.Release()
	}
	require.Equal(t, 1, manager.admittedConnections())

	// The never-consumed ticket still releases exactly once.
	second.Release()
	second.Release()
	require.Equal(t, 0, manager.admittedConnections())
}

// TestSecondTrackOnAConsumedTicketIsRefused: tracking twice must not register a second connection,
// and must not hand back a socket the manager will not own.
func TestSecondTrackOnAConsumedTicketIsRefused(t *testing.T) {
	manager := newTicketTestManager(t)
	ticket, err := manager.AcquireConnection(context.Background())
	require.NoError(t, err)

	firstConn, firstPeer := net.Pipe()
	t.Cleanup(func() { _ = firstPeer.Close() })
	firstTracked, err := ticket.TrackConn(firstConn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = firstTracked.Close() })
	require.Equal(t, 1, manager.Count())

	secondConn, secondPeer := net.Pipe()
	t.Cleanup(func() { _ = secondPeer.Close() })
	secondTracked, secondErr := ticket.TrackConn(secondConn)
	require.ErrorIs(t, secondErr, net.ErrClosed, "a consumed ticket admitted a second connection")
	require.Nil(t, secondTracked)
	require.Equal(t, 1, manager.Count(), "the refused hand-over must not register anything")
	_ = secondConn.Close()
}

// TestCloseWaitsForAnUnconsumedTicket: B has not handed over and has not released, so shutdown is
// not allowed to return - a dial could still be holding a socket it is about to track.
func TestCloseWaitsForAnUnconsumedTicket(t *testing.T) {
	manager := NewConnectionManager(log.NewNOPFactory().Logger())
	pending, err := manager.AcquireConnection(context.Background())
	require.NoError(t, err)

	closed := make(chan error, 1)
	go func() { closed <- manager.Close() }()

	select {
	case err = <-closed:
		t.Fatalf("Close returned while a ticket was still outstanding: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	require.Equal(t, 1, manager.admittedConnections())

	pending.Release()
	select {
	case err = <-closed:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return after the outstanding ticket was released")
	}
	require.Equal(t, 0, manager.admittedConnections())
}

// TestConcurrentTrackAndReleaseSettleOnce: the latch is atomic, so a racing hand-over and release
// still return the slot exactly once and never below zero.
func TestConcurrentTrackAndReleaseSettleOnce(t *testing.T) {
	manager := newTicketTestManager(t)
	for attempt := 0; attempt < 200; attempt++ {
		ticket, err := manager.AcquireConnection(context.Background())
		require.NoError(t, err)
		clientConn, peerConn := net.Pipe()
		var waitGroup sync.WaitGroup
		waitGroup.Add(2)
		go func() {
			defer waitGroup.Done()
			tracked, trackErr := ticket.TrackConn(clientConn)
			if trackErr == nil {
				_ = tracked.Close()
			}
		}()
		go func() {
			defer waitGroup.Done()
			ticket.Release()
		}()
		waitGroup.Wait()
		_ = peerConn.Close()
		require.Equal(t, 0, manager.admittedConnections(), "a racing ticket settled more than once")
	}
}

var _ logger.Logger = log.NewNOPFactory().Logger()
