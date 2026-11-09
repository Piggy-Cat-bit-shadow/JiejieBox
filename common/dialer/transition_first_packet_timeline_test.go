package dialer

import (
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/control"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// The first-packet timeline across a generation change, with a slow old close.
//
// # What was missing
//
// The existing ownership tests pin WHEN a connection is refused or adopted (capture, track, verify,
// CloseAll). None of them measures the other half of the same contract: the new generation must be
// able to carry its first byte while the PREVIOUS generation is still being cleaned up. A reset that
// closes tracked connections sequentially can be held by one slow Close, and the user-visible
// question is not "does reset block" - it is "can the next connection still send a packet".
//
// # Why the answer must be independent
//
// Closing the old generation and serving the new one are separate work by design: the epoch advance
// publishes the new generation, and CloseAll only reclaims what the old one left behind. If the dial
// path shared a lock, a channel or an ordered wait with that reclamation, one stuck Close would delay
// every new flow - which is exactly the "slow cleanup obstructs the new generation" failure this test
// exists to rule out.

// blockingCloseConn is a tracked connection whose Close does not return until the test releases it.
// It stands in for a socket whose close is slow (a TLS shutdown, a driver's synchronous teardown).
type blockingCloseConn struct {
	net.Conn
	release chan struct{}
	entered chan struct{}
	once    sync.Once
}

func (c *blockingCloseConn) Close() error {
	c.once.Do(func() { close(c.entered) })
	<-c.release
	return nil
}

func startEchoServer(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				_, _ = io.Copy(conn, conn)
			}(conn)
		}
	}()
	return listener.Addr().String()
}

// TestTheFirstPacketOfTheNewGenerationOutlivesASlowOldClose measures the timeline the pass condition
// asks for: after a generation change, a new connection completes a round trip while the old
// connection's close is still in flight, and the old close is still the only thing waiting.
func TestTheFirstPacketOfTheNewGenerationOutlivesASlowOldClose(t *testing.T) {
	target := startEchoServer(t)

	epoch := &atomic.Uint64{}
	manager := &epochManager{}
	ctx := service.ContextWith[adapter.ConnectionManager](context.Background(), manager)
	ctx = service.ContextWith[adapter.NetworkManager](ctx,
		&epochNetworkManager{epoch: epoch, finder: control.NewDefaultInterfaceFinder()})
	dialer, err := NewDefault(ctx, option.DialerOptions{})
	require.NoError(t, err)

	// The old generation: one tracked connection whose close will not return yet.
	slow := &blockingCloseConn{
		release: make(chan struct{}),
		entered: make(chan struct{}),
	}
	manager.TrackConn(slow)

	// The generation change and its reclamation, exactly as ResetNetwork drives them.
	resetDone := make(chan struct{})
	go func() {
		epoch.Add(1)
		manager.CloseAll()
		close(resetDone)
	}()
	select {
	case <-slow.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the reset never reached the old connection")
	}

	// The new generation's first packet, timed.
	start := time.Now()
	conn, err := dialer.DialContext(context.Background(), "tcp", M.ParseSocksaddr(target))
	require.NoError(t, err)
	defer conn.Close()
	require.NoError(t, conn.SetDeadline(time.Now().Add(2*time.Second)))
	_, err = conn.Write([]byte("ping"))
	require.NoError(t, err)
	reply := make([]byte, 4)
	_, err = io.ReadFull(conn, reply)
	require.NoError(t, err)
	elapsed := time.Since(start)
	require.Equal(t, "ping", string(reply), "the new generation must carry its first byte")

	require.Less(t, elapsed, 2*time.Second,
		"the first packet of the new generation took %s while the old close was in flight", elapsed)

	// The fixture really did hold the old close across that round trip.
	select {
	case <-resetDone:
		t.Fatal("the reset finished while the old close was blocked: the fixture did not hold it")
	default:
	}
	require.Zero(t, manager.closed.Load(),
		"the old generation must still be closing when the new generation's first packet lands")

	// And the old close is bounded by its owner, not by the new generation: releasing it finishes
	// the reclamation normally.
	close(slow.release)
	select {
	case <-resetDone:
	case <-time.After(2 * time.Second):
		t.Fatal("the reset did not finish after the old close returned")
	}
	require.EqualValues(t, 1, manager.closed.Load())
}
