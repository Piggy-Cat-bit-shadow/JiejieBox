package http

// X02 — a blocking reader and a concurrent Close must not interlock.
//
// The connection owns two reader goroutines: one blocks in Read (the capsule stream) and
// one blocks in ReceiveDatagram (the HTTP Datagram queue). Both are released by a single
// Close, and the contract this pins is that Close returns without waiting for anything the
// readers are waiting for, and that the two facts a caller can observe - the context being
// cancelled and a reader having actually exited - are independent, not one proxy for the
// other.
//
// The shape matters on a real tunnel: a peer that has gone silent leaves both readers
// parked, and a shutdown that waits on the network to notice would hang the relay that is
// trying to close it.

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// blockingDatagramStream parks both reader paths until Close, and records when each one
// actually left - so a test can tell "the context was cancelled" from "the reader exited".
type blockingDatagramStream struct {
	closed        chan struct{}
	closeOnce     sync.Once
	readExited    chan struct{}
	readOnce      sync.Once
	receiveExited chan struct{}
	receiveOnce   sync.Once
}

func newBlockingDatagramStream() *blockingDatagramStream {
	return &blockingDatagramStream{
		closed:        make(chan struct{}),
		readExited:    make(chan struct{}),
		receiveExited: make(chan struct{}),
	}
}

func (s *blockingDatagramStream) Read([]byte) (int, error) {
	<-s.closed
	s.readOnce.Do(func() { close(s.readExited) })
	return 0, io.ErrClosedPipe
}

func (s *blockingDatagramStream) Write(payload []byte) (int, error) { return len(payload), nil }

func (s *blockingDatagramStream) Close() error {
	s.closeOnce.Do(func() { close(s.closed) })
	return nil
}

func (s *blockingDatagramStream) SendDatagram([]byte) error { return nil }

func (s *blockingDatagramStream) ReceiveDatagram(ctx context.Context) ([]byte, error) {
	select {
	case <-s.closed:
	case <-ctx.Done():
	}
	s.receiveOnce.Do(func() { close(s.receiveExited) })
	return nil, io.ErrClosedPipe
}

func (s *blockingDatagramStream) DatagramsEnabled() bool { return true }

// awaitSignal waits for one observation with a bound, so a missing release fails as a test
// failure rather than a hung binary.
func awaitSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

// TestABlockingReadAndAConcurrentCloseBothEnd is the exact interlock: ReadPacket is parked
// with nothing to deliver, both readers are parked, and Close must end all three.
func TestABlockingReadAndAConcurrentCloseBothEnd(t *testing.T) {
	stream := newBlockingDatagramStream()
	conn := newHTTP3PacketConn(stream, M.ParseSocksaddr("example.com:443"), nil)

	readDone := make(chan error, 1)
	go func() {
		buffer := buf.NewPacket()
		defer buffer.Release()
		_, err := conn.ReadPacket(buffer)
		readDone <- err
	}()

	closeDone := make(chan error, 1)
	go func() { closeDone <- conn.Close() }()

	select {
	case err := <-closeDone:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return while the readers were blocked: shutdown waits on the network")
	}
	select {
	case err := <-readDone:
		require.Error(t, err, "a parked ReadPacket must be released by Close with an error")
	case <-time.After(5 * time.Second):
		t.Fatal("ReadPacket was not released by Close")
	}

	awaitSignal(t, stream.readExited, "the capsule reader to exit")
	awaitSignal(t, stream.receiveExited, "the datagram receiver to exit")

	// The two observations are independent: the context is cancelled at Close, and the
	// reader exit is a separate fact that must also be true.
	require.Error(t, conn.ctx.Err(), "Close must cancel the connection context")
	select {
	case <-stream.readExited:
	default:
		t.Fatal("the reader had not exited although Close returned")
	}

	// Idempotent: a second Close must not block or panic on the already-closed stream.
	require.NoError(t, conn.Close())
}

// TestRepeatedBlockingReadAndCloseKeepAccounting is the repeated form: every round must end
// both readers, so a round that leaks one is caught under -race.
func TestRepeatedBlockingReadAndCloseKeepAccounting(t *testing.T) {
	for round := 0; round < 50; round++ {
		stream := newBlockingDatagramStream()
		conn := newHTTP3PacketConn(stream, M.ParseSocksaddr("example.com:443"), nil)
		readDone := make(chan struct{})
		go func() {
			buffer := buf.NewPacket()
			defer buffer.Release()
			_, _ = conn.ReadPacket(buffer)
			close(readDone)
		}()
		require.NoError(t, conn.Close())
		select {
		case <-readDone:
		case <-time.After(5 * time.Second):
			t.Fatalf("round %d: ReadPacket was not released by Close", round)
		}
		awaitSignal(t, stream.readExited, "the capsule reader to exit")
		awaitSignal(t, stream.receiveExited, "the datagram receiver to exit")
	}
}

// TestAClosedConnectionReportsTheCloseReason keeps the error a caller branches on stable:
// a closed connection reports net.ErrClosed rather than an opaque reader error.
func TestAClosedConnectionReportsTheCloseReason(t *testing.T) {
	stream := newBlockingDatagramStream()
	conn := newHTTP3PacketConn(stream, M.ParseSocksaddr("example.com:443"), nil)
	require.NoError(t, conn.Close())

	buffer := buf.NewPacket()
	defer buffer.Release()
	_, err := conn.ReadPacket(buffer)
	require.Error(t, err)
	require.ErrorIs(t, err, net.ErrClosed)
}
