//go:build with_quic

package v2rayquic

import (
	"context"
	"net"
	"sync"
	"sync/atomic"

	"github.com/sagernet/quic-go"
	"github.com/sagernet/quic-go/http3"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/tls"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-quic"
	"github.com/sagernet/sing/common"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

var _ adapter.V2RayMultiplexClientTransport = (*Client)(nil)

// Client owns ONE shared QUIC connection for every stream of this transport.
//
// # One creation, many waiters
//
// The first demand starts the shared offer; every other demand attaches to it instead of dialing a
// second connection. Each waiter keeps its OWN cancellation: a request with a short deadline stops
// waiting for the shared result without tearing down a dial that other requests (or a later request)
// can still use. The dial itself runs under the client lifetime context, so it is the client - the
// clear owner - that accepts a late success into the pool or closes it exactly once when the client
// was closed while the dial was in flight.
//
// The shape mirrors the QUIC shared offer in the pinned sing-quic client (pending offer + per-waiter
// select + discard-once on the owner); this transport is the in-repo owner of the same pattern.
type Client struct {
	ctx        context.Context
	cancel     context.CancelCauseFunc
	dialer     N.Dialer
	serverAddr M.Socksaddr
	tlsConfig  tls.Config
	quicConfig *quic.Config
	connAccess sync.Mutex
	conn       common.TypedValue[*clientConnection]
	// pending is the shared creation currently in flight, or nil. It is set and cleared under
	// connAccess; the offer goroutine owns the result and publishes it under the same lock.
	pending   *clientOffer
	closeIdle atomic.Bool
	// dialConn is the constructor a pending offer runs. NewClient points it at offerNew; the
	// shared-task tests replace it so they can hold a dial open and decide its result without a QUIC
	// handshake. Production never reads it from outside this package.
	dialConn func(ctx context.Context) (*clientConnection, error)
}

// clientConnection is one shared QUIC connection plus the ownership state the pool needs.
//
// ctx and closeFn exist so the connection's lifetime and its one close action are explicit fields: a
// connection is created by offerNew, which fills both from the real quic.Conn, and the shared-task
// tests can create the same state without a live QUIC handshake. The embedded *quic.Conn remains the
// transport handle used for streams and addresses.
type clientConnection struct {
	*quic.Conn
	ctx       context.Context
	closeFn   func()
	closeOnce sync.Once
	access    sync.Mutex
	streams   int
	closed    bool
	closeIdle *atomic.Bool
}

func (c *clientConnection) active() bool {
	c.access.Lock()
	closed := c.closed
	c.access.Unlock()
	return !closed && !common.Done(c.ctx)
}

func (c *clientConnection) acquireStream() bool {
	c.access.Lock()
	defer c.access.Unlock()
	if c.closed {
		return false
	}
	c.streams++
	return true
}

func (c *clientConnection) releaseStream(keepSession bool) {
	c.access.Lock()
	c.streams--
	closed := c.closeIdle.Load() && !keepSession && c.streams == 0 && c.markClosedLocked()
	c.access.Unlock()
	if closed {
		c.closeConn()
	}
}

func (c *clientConnection) markClosedLocked() bool {
	if c.closed {
		return false
	}
	c.closed = true
	return true
}

// closeConn tears the underlying QUIC connection down exactly once. The once guard is the release
// confirmation: a connection can be retired by the idle policy, a reset, or a discarded late offer,
// and only the first of them is the close.
func (c *clientConnection) closeConn() {
	c.closeOnce.Do(func() {
		if c.closeFn != nil {
			c.closeFn()
		}
	})
}

// clientOffer is one shared creation in flight.
type clientOffer struct {
	done      chan struct{}
	conn      *clientConnection
	err       error
	discarded bool
	cause     error
	cancel    context.CancelCauseFunc
}

// wait waits for the shared result or for THIS waiter's own cancellation. Both can be ready at the
// same time; either answer is truthful - a caller that cancelled while the offer succeeded has no
// claim on the connection, and the owner keeps it.
func (o *clientOffer) wait(ctx context.Context) (*clientConnection, error) {
	select {
	case <-o.done:
		return o.conn, o.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func NewClient(ctx context.Context, dialer N.Dialer, serverAddr M.Socksaddr, options option.V2RayQUICOptions, tlsConfig tls.Config) (adapter.V2RayClientTransport, error) {
	quicConfig := &quic.Config{
		DisablePathMTUDiscovery: !C.IsLinux && !C.IsWindows,
	}
	if len(tlsConfig.NextProtos()) == 0 {
		tlsConfig.SetNextProtos([]string{http3.NextProtoH3})
	}
	clientCtx, cancel := context.WithCancelCause(ctx)
	client := &Client{
		ctx:        clientCtx,
		cancel:     cancel,
		dialer:     dialer,
		serverAddr: serverAddr,
		tlsConfig:  tlsConfig,
		quicConfig: quicConfig,
	}
	client.dialConn = client.offerNew
	return client, nil
}

// offer returns the shared connection, joining the creation already in flight when there is one.
//
// The waiter's ctx is only ever used for waiting: it must be able to end THIS caller's wait without
// cancelling the shared dial, because the dial's result belongs to the client and may serve callers
// that are still there. That is why the dial context is derived from the client lifetime below and
// never from ctx.
func (c *Client) offer(ctx context.Context) (*clientConnection, error) {
	c.connAccess.Lock()
	conn := c.conn.Load()
	if conn != nil && conn.active() {
		c.connAccess.Unlock()
		return conn, nil
	}
	pending := c.pending
	if pending != nil {
		c.connAccess.Unlock()
		return pending.wait(ctx)
	}
	offerCtx, cancel := context.WithCancelCause(c.ctx)
	pending = &clientOffer{
		done:   make(chan struct{}),
		cancel: cancel,
	}
	c.pending = pending
	c.connAccess.Unlock()

	go c.completeOffer(pending, offerCtx)
	return pending.wait(ctx)
}

// completeOffer runs one shared creation and publishes its result. The client is the owner: a
// success is accepted into the pool unless the client was closed first, in which case the late
// connection is closed here - exactly once - instead of being published or leaked.
func (c *Client) completeOffer(pending *clientOffer, offerCtx context.Context) {
	conn, err := c.dialConn(offerCtx)
	// The dial context is no longer needed once the dial returned; releasing it here keeps the
	// per-offer timer/context from outliving the work even when the result is discarded.
	pending.cancel(nil)

	c.connAccess.Lock()
	discard := pending.discarded
	if discard {
		pending.err = pending.cause
		if pending.err == nil {
			pending.err = net.ErrClosed
		}
	} else {
		pending.conn = conn
		pending.err = err
		if err == nil {
			c.conn.Store(conn)
		}
	}
	if c.pending == pending {
		c.pending = nil
	}
	close(pending.done)
	c.connAccess.Unlock()

	if discard && conn != nil {
		conn.closeConn()
	}
}

func (c *Client) offerNew(ctx context.Context) (*clientConnection, error) {
	udpConn, err := c.dialer.DialContext(ctx, "udp", c.serverAddr)
	if err != nil {
		return nil, err
	}
	quicConn, err := qtls.Dial(ctx, udpConn, c.tlsConfig, c.quicConfig)
	if err != nil {
		udpConn.Close()
		return nil, err
	}
	// quic-go does not take ownership of the conn passed to Dial:
	// when the connection ends it only stops reading.
	go func() {
		<-quicConn.Context().Done()
		udpConn.Close()
	}()
	conn := &clientConnection{
		Conn:      quicConn,
		ctx:       quicConn.Context(),
		closeIdle: &c.closeIdle,
	}
	conn.closeFn = func() { _ = quicConn.CloseWithError(0, "") }
	return conn, nil
}

func (c *Client) DialContext(ctx context.Context) (net.Conn, error) {
	for {
		conn, err := c.offer(ctx)
		if err != nil {
			return nil, err
		}
		if !conn.acquireStream() {
			continue
		}
		stream, err := conn.OpenStream()
		if err != nil {
			conn.releaseStream(false)
			return nil, err
		}
		keepSession := adapter.KeepSessionFromContext(ctx)
		return &StreamWrapper{Conn: conn.Conn, Stream: stream, onClose: func() { conn.releaseStream(keepSession) }}, nil
	}
}

func (c *Client) MultiplexEnabled() bool {
	return true
}

func (c *Client) SetKeepIdleConnections(keep bool) {
	c.closeIdle.Store(!keep)
	if !keep {
		c.CloseIdleConnections()
	}
}

func (c *Client) CloseIdleConnections() {
	conn := c.conn.Load()
	if conn == nil {
		return
	}
	conn.access.Lock()
	closed := conn.streams == 0 && conn.markClosedLocked()
	conn.access.Unlock()
	if closed {
		conn.closeConn()
	}
}

// Close releases the shared connection and makes the client the discarding owner of any creation
// still in flight: the pending offer is marked discarded and cancelled, so a late success is closed
// by completeOffer instead of being published after close. It does NOT wait for that dial to finish;
// the result is owned by the client and the discard is exactly-once.
func (c *Client) Close() error {
	c.connAccess.Lock()
	conn := c.conn.Swap(nil)
	pending := c.pending
	if pending != nil {
		pending.discarded = true
		if pending.cause == nil {
			pending.cause = net.ErrClosed
		}
	}
	c.connAccess.Unlock()

	if c.cancel != nil {
		c.cancel(net.ErrClosed)
	}
	if pending != nil && pending.cancel != nil {
		pending.cancel(net.ErrClosed)
	}
	if conn != nil {
		conn.closeConn()
	}
	return nil
}

// Reset aborts the CURRENT connection and any creation in flight, but keeps the client usable:
// a later offer may establish a fresh connection from the client's still-live context.
//
// # Why this is not Close
//
// The outbounds call InterfaceUpdated on every physical network change. Close is the final action:
// it cancels the client context, so every offer derived from it fails forever. Calling Close from
// the network hook therefore left a VLESS/VMess/Trojan transport.type=quic instance unable to dial
// again after the first change. Reset performs the same discard-and-close of the current object
// without ending the client's lifetime; Scope.Close (or the explicit Close call) still does that.
func (c *Client) Reset() error {
	c.connAccess.Lock()
	conn := c.conn.Swap(nil)
	pending := c.pending
	if pending != nil {
		pending.discarded = true
		if pending.cause == nil {
			pending.cause = net.ErrClosed
		}
	}
	c.connAccess.Unlock()

	if pending != nil && pending.cancel != nil {
		pending.cancel(net.ErrClosed)
	}
	if conn != nil {
		conn.closeConn()
	}
	return nil
}
