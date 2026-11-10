package http

import (
	"context"
	stdTLS "crypto/tls"
	"errors"
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
	entered    chan struct{}
	release    chan struct{}
	once       sync.Once
	releaseErr error
	// calls counts EVERY dial, so a test can prove the new generation entered its own dial while
	// the first one is still blocked. secondErr is what calls after the first return, which lets a
	// test observe "the new dial started" without a real handshake.
	calls     atomic.Int32
	secondErr error
}

func newBlockingTLSDialer() *blockingTLSDialer {
	return &blockingTLSDialer{entered: make(chan struct{}), release: make(chan struct{})}
}

func (d *blockingTLSDialer) DialTLSContext(ctx context.Context, _ M.Socksaddr) (aTLS.Conn, error) {
	if d.calls.Add(1) > 1 {
		if d.secondErr != nil {
			return nil, d.secondErr
		}
		return newStubTLSConn(), nil
	}
	d.once.Do(func() { close(d.entered) })
	select {
	case <-d.release:
		if d.releaseErr != nil {
			return nil, d.releaseErr
		}
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

// TestHTTP2DialSlotRespectsTheWaitersDeadline is the wait-budget half of B5.
//
// A setup that waits for the dial slot must be able to give up on ITS OWN deadline. With a plain
// mutex it could not: it would spend its tunnel window queued behind another setup's handshake and
// return to a caller that had already left.
func TestHTTP2DialSlotRespectsTheWaitersDeadline(t *testing.T) {
	dialer := newBlockingTLSDialer()
	dialer.releaseErr = errors.New("released")
	client := &Client{tlsDialer: dialer}

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		_, _, _ = client.acquireHTTP2(context.Background())
	}()
	<-dialer.entered

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, _, err := client.acquireHTTP2(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded,
		"a setup must abandon the wait for the dial slot when its own context ends")

	close(dialer.release)
	<-firstDone
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

func TestAuditH2DialCannotPublishAfterClose(t *testing.T) {
	dialer := newBlockingTLSDialer()
	client := &Client{tlsDialer: dialer, http2Transport: &http2.Transport{}}
	done := make(chan error, 1)
	go func() {
		_, _, err := client.acquireHTTP2(context.Background())
		done <- err
	}()
	<-dialer.entered
	require.NoError(t, client.Close())
	close(dialer.release)
	err := <-done
	defer client.Close()
	require.ErrorIs(t, err, net.ErrClosed)
	require.Empty(t, client.http2Conns)
}

func TestNewNetworkH2StartsItsOwnDialWhileTheOldOneIsRunning(t *testing.T) {
	dialer := newBlockingTLSDialer()
	dialer.secondErr = errors.New("new network dial entered")
	client := &Client{tlsDialer: dialer, http2Transport: &http2.Transport{}}
	done := make(chan error, 1)
	go func() {
		_, _, err := client.acquireHTTP2(context.Background())
		done <- err
	}()
	<-dialer.entered
	client.ResetConnections()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	// The new generation must not wait for the retired dial: it reaches its OWN dial while the
	// old one is still blocked. The old test only asserted "not deadline exceeded", which an
	// immediate superseded error also satisfies - that is what let the regression through.
	_, _, err := client.acquireHTTP2(ctx)
	require.ErrorContains(t, err, "new network dial entered",
		"the new network must reach its own dial even while the old dial is stuck")
	require.EqualValues(t, 2, dialer.calls.Load())
	close(dialer.release)
	require.ErrorIs(t, <-done, errHTTP2DialSuperseded,
		"the retired dial must still be discarded")
	require.Empty(t, client.http2Conns)
}

// TestH2PublishAfterCloseLeavesNoConnection is the interleaving the review held open: a publisher
// that is already past its "is the client open?" check when Close runs. The check, the epoch
// comparison and the append are one critical section now, so a publish that loses to Close must
// refuse AND close its socket, and a publish that wins is removed by Close's clear. Either way the
// pool is empty once Close has returned and no socket is left open.
func TestH2PublishAfterCloseLeavesNoConnection(t *testing.T) {
	clientConn, err := newHTTP2ClientConn(&http2.Transport{}, newStubTLSConn())
	require.NoError(t, err)
	conn := &stubNetConn{}
	client := &Client{http2Transport: &http2.Transport{}}

	// Hold the pool lock, then let Close start: it sets closed BEFORE waiting for the lock, which
	// is exactly the moment the old code's check had already passed.
	client.http2Access.Lock()
	published := make(chan bool, 1)
	go func() {
		published <- client.publishHTTP2Conn(client.networkEpoch.Load(), clientConn, conn)
	}()
	closed := make(chan struct{})
	go func() {
		_ = client.Close()
		close(closed)
	}()
	time.Sleep(50 * time.Millisecond)
	client.http2Access.Unlock()
	require.False(t, <-published, "a publish that lost to Close must refuse")
	<-closed
	require.True(t, conn.closed.Load(), "the refused socket must be closed, not leaked")
	require.Empty(t, client.http2Conns)
}
