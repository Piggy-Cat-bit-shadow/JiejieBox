package ssh

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// countingConn is a net.Conn whose only interesting method is Close.
type countingConn struct {
	closed atomic.Int32
}

func (c *countingConn) Read([]byte) (int, error)         { return 0, net.ErrClosed }
func (c *countingConn) Write(p []byte) (int, error)      { return len(p), nil }
func (c *countingConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (c *countingConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (c *countingConn) SetDeadline(time.Time) error      { return nil }
func (c *countingConn) SetReadDeadline(time.Time) error  { return nil }
func (c *countingConn) SetWriteDeadline(time.Time) error { return nil }
func (c *countingConn) Close() error {
	c.closed.Add(1)
	return nil
}

// TestInterfaceUpdatedRetiresTheConnectionUnderTheLock is the regression for the unlocked close.
//
// # The bug it pins
//
// `InterfaceUpdated` used to close s.clientConn without taking clientAccess. Every other path --
// connect, CloseIdleConnections, releaseStream, the Wait goroutine -- reads and writes that field
// under the lock, so a network change could observe the connection a concurrent dial had just
// installed and close it. That is both a data race and, functionally, the new session being retired
// instead of the old one.
//
// The dial side below is written the way `connect` writes it: lock, install, unlock. Under `-race`
// the old form fails here; with the fix the access is serialized and the first connection is closed
// exactly once.
func TestInterfaceUpdatedRetiresTheConnectionUnderTheLock(t *testing.T) {
	outbound := &Outbound{}
	first := &countingConn{}
	outbound.clientConn = first

	// Retire the installed session once with no writer running, so "exactly once" is deterministic.
	outbound.InterfaceUpdated(context.Background())
	require.Equal(t, int32(1), first.closed.Load(),
		"the session present at the interface change is closed exactly once")

	// Now the race half: a dial installing sessions the way connect does, while interface changes
	// retire them. Under -race the old unlocked read is reported against the locked write.
	var waitGroup sync.WaitGroup
	stop := make(chan struct{})
	waitGroup.Add(1)
	go func() {
		defer waitGroup.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			next := &countingConn{}
			outbound.clientAccess.Lock()
			outbound.clientConn = next
			outbound.client = nil
			outbound.clientAccess.Unlock()
		}
	}()

	for i := 0; i < 200; i++ {
		outbound.InterfaceUpdated(context.Background())
	}
	close(stop)
	waitGroup.Wait()
	// The writer may have installed one last session before it stopped; retire it, then the
	// outbound must hold nothing so the next dial builds a fresh client.
	outbound.InterfaceUpdated(context.Background())

	require.Nil(t, outbound.takeClientConn(),
		"the outbound holds no session afterwards, so the next dial builds a fresh one")
}
