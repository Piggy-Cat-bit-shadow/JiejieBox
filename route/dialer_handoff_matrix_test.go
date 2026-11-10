package route

// The remaining 12-order attacks through the production dialer: every entry point (TCP, UDP,
// ListenPacket) must reject a shutdown that lands in the admission hand-off; the single slot is
// handed to exactly one real dial at a time; and a Close implementation that re-enters the manager
// (Count/pending) must not deadlock the worker.

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/dialer"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

func TestEveryDialEntryPointRejectsAShutdownHandoff(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	address := listener.Addr().String()

	cases := []struct {
		name string
		dial func(*dialer.DefaultDialer, context.Context) error
	}{
		{"tcp", func(realDialer *dialer.DefaultDialer, ctx context.Context) error {
			conn, dialErr := realDialer.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr(address))
			if conn != nil {
				_ = conn.Close()
			}
			return dialErr
		}},
		{"udp", func(realDialer *dialer.DefaultDialer, ctx context.Context) error {
			conn, dialErr := realDialer.DialContext(ctx, N.NetworkUDP, M.ParseSocksaddr(address))
			if conn != nil {
				_ = conn.Close()
			}
			return dialErr
		}},
		{"listen-packet", func(realDialer *dialer.DefaultDialer, ctx context.Context) error {
			conn, dialErr := realDialer.ListenPacket(ctx, M.ParseSocksaddr(address))
			if conn != nil {
				_ = conn.Close()
			}
			return dialErr
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			harness := newInterfaceTransitionHarness(t)
			manager := NewConnectionManager(log.NewNOPFactory().Logger())
			pausing := &pausingAdmission{ConnectionManager: manager, staged: make(chan struct{}), resume: make(chan struct{})}
			release := make(chan struct{})
			go func() {
				<-pausing.staged
				// Wait for shutdown to be set, then release the dial's ticket.
				for !manager.closed.Load() {
					time.Sleep(time.Millisecond)
				}
				close(release)
			}()
			go func() {
				<-release
				close(pausing.resume)
			}()
			t.Cleanup(func() {
				select {
				case <-pausing.resume:
				default:
					close(pausing.resume)
				}
			})

			realDialer := newGenerationDialer(t, harness.manager, pausing)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- testCase.dial(realDialer, ctx) }()

			select {
			case <-pausing.staged:
			case <-time.After(5 * time.Second):
				t.Fatal("the dial never reached the admission hand-off")
			}
			closeDone := make(chan struct{})
			go func() {
				defer close(closeDone)
				_ = manager.Close()
			}()
			select {
			case dialErr := <-result:
				require.ErrorIs(t, dialErr, net.ErrClosed,
					"the dial returned a usable connection after shutdown revoked its hand-off")
			case <-time.After(5 * time.Second):
				t.Fatal("the dial never returned")
			}
			select {
			case <-closeDone:
			case <-time.After(5 * time.Second):
				t.Fatal("Close did not return after the rejected hand-off settled")
			}
			require.Equal(t, 0, manager.Count())
			require.Equal(t, 0, manager.admittedConnections())
		})
	}
}

func TestOnlyTheActualSlotIsHandedToRealDials(t *testing.T) {
	harness := newInterfaceTransitionHarness(t)
	harness.manager.transitionStable.Store(true)
	router := newTimedRouter(harness.manager.NetworkResetGeneration)
	harness.manager.router = router
	harness.manager.updateNetworkEnvironment()
	require.True(t, harness.manager.NetworkTransitionStable())

	manager := NewConnectionManager(log.NewNOPFactory().Logger())
	manager.disposalBudgetOverride = 1
	pausing := &pausingAdmission{ConnectionManager: manager, staged: make(chan struct{}), resume: make(chan struct{})}
	t.Cleanup(func() {
		select {
		case <-pausing.resume:
		default:
			close(pausing.resume)
		}
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	realDialer := newGenerationDialer(t, harness.manager, pausing)
	address := M.ParseSocksaddr(listener.Addr().String())

	conns := make(chan net.Conn, 2)
	t.Cleanup(func() {
		for {
			select {
			case conn := <-conns:
				_ = conn.Close()
			default:
				return
			}
		}
	})
	first := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		conn, dialErr := realDialer.DialContext(ctx, N.NetworkTCP, address)
		if conn != nil {
			conns <- conn
		}
		first <- dialErr
	}()
	select {
	case <-pausing.staged:
	case <-time.After(5 * time.Second):
		t.Fatal("the first dial never reached admission")
	}

	// The only slot is held by the first dial: a second dial must wait in admission, not track.
	second := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		conn, dialErr := realDialer.DialContext(ctx, N.NetworkTCP, address)
		if conn != nil {
			conns <- conn
		}
		second <- dialErr
	}()
	time.Sleep(100 * time.Millisecond)
	require.Equal(t, 1, manager.admittedConnections(), "the budget admitted more than one real dial")
	require.Equal(t, 0, manager.Count(), "a waiting dial tracked itself without a slot")

	close(pausing.resume)
	require.NoError(t, <-first)
	require.NoError(t, <-second)
	require.Eventually(t, func() bool { return manager.Count() == 2 }, 2*time.Second, time.Millisecond)
	require.Equal(t, 0, manager.admittedConnections())
}

// reentrantPacketConn calls back into the manager from its own Close. The worker must be able to
// serve that without holding the lock the callback takes, or the manager deadlocks.
type reentrantPacketConn struct {
	net.PacketConn
	manager *ConnectionManager
}

func (c *reentrantPacketConn) Close() error {
	_ = c.manager.Count()
	_ = c.manager.pendingDisposals()
	_ = c.manager.condemnedDisposals()
	return c.PacketConn.Close()
}

func TestAReentrantCloseCallbackDoesNotDeadlock(t *testing.T) {
	manager := NewConnectionManager(log.NewNOPFactory().Logger())
	t.Cleanup(func() { _ = manager.Close() })
	conn := &reentrantPacketConn{PacketConn: mustListenPacket(t), manager: manager}
	manager.TrackPacketConn(conn)
	manager.Reclaim(ReclaimNetworkTransition)
	require.Eventually(t, func() bool {
		return manager.pendingDisposals() == 0 && manager.condemnedDisposals() == 0 && manager.Count() == 0
	}, 5*time.Second, 5*time.Millisecond, "the reentrant Close deadlocked the worker")
}
