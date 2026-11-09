package http

import (
	"context"
	stdTLS "crypto/tls"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
	aTLS "github.com/sagernet/sing/common/tls"

	"golang.org/x/net/http2"

	"github.com/stretchr/testify/require"
)

// The HTTP/2 pool across a network change.
//
// The pool lock used to be held for the WHOLE TLS handshake, so ResetConnections - which runs on
// every network change - queued behind a handshake belonging to the network being left, up to the
// TLS timeout, before it could tear that network down. The dial now has its own serialisation and
// its result is only published if the network it was made on is still current; these tests pin
// both halves.

type blockingTLSDialer struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingTLSDialer() *blockingTLSDialer {
	return &blockingTLSDialer{entered: make(chan struct{}), release: make(chan struct{})}
}

func (d *blockingTLSDialer) DialTLSContext(ctx context.Context, _ M.Socksaddr) (aTLS.Conn, error) {
	d.once.Do(func() { close(d.entered) })
	select {
	case <-d.release:
		return newStubTLSConn(), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type stubNetConn struct {
	closed atomic.Bool
}

func (c *stubNetConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (c *stubNetConn) Write(p []byte) (int, error)      { return len(p), nil }
func (c *stubNetConn) Close() error                     { c.closed.Store(true); return nil }
func (c *stubNetConn) LocalAddr() net.Addr              { return nil }
func (c *stubNetConn) RemoteAddr() net.Addr             { return nil }
func (c *stubNetConn) SetDeadline(time.Time) error      { return nil }
func (c *stubNetConn) SetReadDeadline(time.Time) error  { return nil }
func (c *stubNetConn) SetWriteDeadline(time.Time) error { return nil }

type stubTLSConn struct {
	stubNetConn
}

func newStubTLSConn() *stubTLSConn { return &stubTLSConn{} }

func (c *stubTLSConn) NetConn() net.Conn                      { return &c.stubNetConn }
func (c *stubTLSConn) HandshakeContext(context.Context) error { return nil }
func (c *stubTLSConn) ConnectionState() stdTLS.ConnectionState {
	return stdTLS.ConnectionState{NegotiatedProtocol: http2.NextProtoTLS}
}

// TestResetConnectionsDoesNotWaitForAnInFlightHTTP2Dial is the wait half.
func TestResetConnectionsDoesNotWaitForAnInFlightHTTP2Dial(t *testing.T) {
	dialer := newBlockingTLSDialer()
	client := &Client{tlsDialer: dialer}

	dialDone := make(chan struct{})
	var dialErr error
	go func() {
		defer close(dialDone)
		_, _, dialErr = client.acquireHTTP2(context.Background())
	}()
	<-dialer.entered

	resetDone := make(chan struct{})
	go func() {
		client.ResetConnections()
		close(resetDone)
	}()
	select {
	case <-resetDone:
	case <-time.After(2 * time.Second):
		t.Fatal("ResetConnections waited for an in-flight HTTP/2 dial; a network change must not " +
			"queue behind a handshake on the network being left")
	}

	close(dialer.release)
	<-dialDone
	require.ErrorIs(t, dialErr, errHTTP2DialSuperseded,
		"a connection dialled on the network being left must be reported as superseded, not installed")

	client.http2Access.Lock()
	defer client.http2Access.Unlock()
	require.Empty(t, client.http2Conns, "and it must not enter the new network's pool")
}

// TestPublishHTTP2ConnRejectsThePreviousNetwork is the install half: the check and the append are
// one transaction against ResetConnections, which bumps the epoch and clears the pool together.
func TestPublishHTTP2ConnRejectsThePreviousNetwork(t *testing.T) {
	client := &Client{}
	epoch := client.networkEpoch.Load()
	client.ResetConnections()

	conn := &stubNetConn{}
	require.False(t, client.publishHTTP2Conn(epoch, &http2ClientConn{}, conn),
		"an observation from the network being left must not be published into the new pool")
	require.Empty(t, client.http2Conns)
	require.True(t, conn.closed.Load(), "the discarded connection must be closed, not leaked")
}
