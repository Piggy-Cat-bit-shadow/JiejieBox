//go:build with_quic

package v2rayquic

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

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
//
// # A network change keeps the flow
//
// Reset runs on every physical network change. Closing the shared connection there drops every
// stream on it, so the client first tries to MOVE that connection onto a socket of the new
// network: candidate socket -> AddPath -> bounded Probe -> Switch -> wait until the run loop has
// actually installed the new send path -> retire the old transport -> close the old socket. Only
// when that cannot be done is the generation retired the old way and replaced by a later dial.
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
	// migrateAccess serializes network-change migrations: one candidate is in flight per client,
	// so a burst of interface notifications cannot leave a pile of sockets behind.
	migrateAccess sync.Mutex
	// dialConn is the constructor a pending offer runs. NewClient points it at offerNew; the
	// shared-task tests replace it so they can hold a dial open and decide its result without a QUIC
	// handshake. Production never reads it from outside this package.
	dialConn func(ctx context.Context) (*clientConnection, error)
}

// The migration budgets. A candidate is validated in at most migrationProbeBudget, and the wait
// for the run loop to install the new send path is bounded separately, so a network change with
// open flows costs at most the two together before it decides to keep or retire the generation.
const (
	migrationProbeBudget  = 2 * time.Second
	migrationRetireBudget = 2 * time.Second
)

// pathSocket is one socket the shared connection sends on, the transport that owns it, and its
// one close action. The transport is nil only if the pinned library did not report the transport
// it dialed through, which it does for every client connection.
type pathSocket struct {
	socket    net.Conn
	transport *quic.Transport
	// retired is closed by close, so anything waiting for THIS path to end wakes when it does
	// instead of waiting for the connection. It is the path's own lifetime token.
	retired   chan struct{}
	closeOnce sync.Once
}

func newPathSocket(socket net.Conn) *pathSocket {
	return &pathSocket{socket: socket, retired: make(chan struct{})}
}

// close closes the socket exactly once and publishes the path's end. Several owners reach here -
// the failed attempt that created it, the migration that replaced it, and the cleanup task watching
// it - and only the first of them is the close.
func (p *pathSocket) close() {
	p.closeOnce.Do(func() {
		_ = p.socket.Close()
		close(p.retired)
	})
}

// pathCleanupTasks counts the socket-cleanup tasks that are alive right now.
//
// It exists so that the ownership of a socket's cleanup is observable without sampling stacks: a
// task belongs to exactly one PATH, so a connection that has one live path keeps exactly one task,
// whatever number of migrations it went through. Only tests read it.
var pathCleanupTasks atomic.Int64

// watchPathCleanup is the one cleanup task of one path: it closes the path's socket when the path
// ends and then exits. "Ends" means the path, not the connection - a candidate that failed and a
// socket a later migration replaced are both finished long before the connection is, and a task
// that outlives them is a goroutine held for the connection's whole lifetime.
func watchPathCleanup(conn *clientConnection, path *pathSocket) {
	pathCleanupTasks.Add(1)
	go func() {
		defer pathCleanupTasks.Add(-1)
		select {
		case <-conn.Context().Done():
			// The connection ended: its path is over even if nobody closed it explicitly.
		case <-path.retired:
			// This path was retired before the connection (a replaced or abandoned one). Its
			// socket is already closed; the task has nothing left to own.
		}
		path.close()
	}()
}

// connectedPacketConn presents the connected socket a dialer returns for "udp" as the
// net.PacketConn a path transport needs: every packet goes to the peer the socket is already
// connected to, and the address argument is ignored. quic-go's own quic.DialConn wraps a socket
// the same way; a path transport needs the same shape because a path is validated by sending to
// the destination on that socket, not by opening a second connection.
type connectedPacketConn struct {
	net.Conn
}

func (c connectedPacketConn) ReadFrom(buffer []byte) (int, net.Addr, error) {
	read, err := c.Conn.Read(buffer)
	return read, c.Conn.RemoteAddr(), err
}

func (c connectedPacketConn) WriteTo(buffer []byte, _ net.Addr) (int, error) {
	return c.Conn.Write(buffer)
}

// SyscallConn forwards the raw socket when the dialer's conn exposes one, so the path transport
// keeps the receive/send buffer tuning and batching quic-go only enables for a socket it can
// reach through syscall.RawConn. It returns an error instead of panicking for a dialer whose conn
// is not a real socket: quic-go then falls back to its basic (unoptimized) transport.
func (c connectedPacketConn) SyscallConn() (syscall.RawConn, error) {
	raw, isRaw := c.Conn.(syscall.Conn)
	if !isRaw {
		return nil, errors.New("the dialed socket is not a syscall.Conn")
	}
	return raw.SyscallConn()
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
	// failedMigrations counts candidate validations that ended at their own deadline without a
	// switch. A path that cannot be validated once is worth one retry, because a probe can be
	// lost on a bad moment of a live network; after two, the connection-ID budget this peer
	// offers for path validation is spent and every later attempt would only stall the network
	// change by the probe budget. The generation is then retired the way it was before migration.
	failedMigrations int
	// pathAccess guards the socket the connection currently sends on. It is replaced only by a
	// successful migration, under the client's migrateAccess.
	pathAccess sync.Mutex
	path       *pathSocket
}

// noteFailedMigration records a candidate that ended at its own deadline, and reports whether
// another candidate is still worth trying for this connection.
func (c *clientConnection) noteFailedMigration() bool {
	c.access.Lock()
	defer c.access.Unlock()
	c.failedMigrations++
	return c.failedMigrations < 2
}

// migrationAvailable reports whether another candidate is worth trying for this connection.
func (c *clientConnection) migrationAvailable() bool {
	c.access.Lock()
	defer c.access.Unlock()
	return c.failedMigrations < 2
}

// noteMigrated clears the failure count: the connection moved, so the next network change starts
// its validation budget over.
func (c *clientConnection) noteMigrated() {
	c.access.Lock()
	c.failedMigrations = 0
	c.access.Unlock()
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

// hasStreams reports whether the connection carries flows that a network change must preserve.
// A generation with nothing on it is retired the old way: a fresh dial is both cheaper and
// simpler than moving a socket nobody is using.
func (c *clientConnection) hasStreams() bool {
	c.access.Lock()
	defer c.access.Unlock()
	return c.streams > 0 && !c.closed
}

// currentPath returns the socket the connection sends on, as of the last applied switch.
func (c *clientConnection) currentPath() *pathSocket {
	c.pathAccess.Lock()
	defer c.pathAccess.Unlock()
	return c.path
}

// adoptPath publishes the socket a migration moved the connection to and closes nothing here:
// the caller retires the transport of the previous path first and lets adopt close its socket.
func (c *clientConnection) adoptPath(path *pathSocket) {
	c.pathAccess.Lock()
	previous := c.path
	c.path = path
	c.pathAccess.Unlock()
	if previous != nil {
		// The transport behind the previous socket has been retired already, which is what makes
		// closing its socket safe: no connection is registered on it any more.
		previous.close()
	}
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
	socket := newPathSocket(udpConn)
	socket.transport = quicConn.HandshakeTransport()
	conn := &clientConnection{
		Conn:      quicConn,
		ctx:       quicConn.Context(),
		closeIdle: &c.closeIdle,
		path:      socket,
	}
	conn.closeFn = func() { _ = quicConn.CloseWithError(0, "") }
	watchPathCleanup(conn, socket)
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

// Reset handles one physical network change, and keeps the client usable afterwards.
//
// # Why this is not Close
//
// The outbounds call InterfaceUpdated on every physical network change. Close is the final action:
// it cancels the client context, so every offer derived from it fails forever. Calling Close from
// the network hook therefore left a VLESS/VMess/Trojan transport.type=quic instance unable to dial
// again after the first change. Reset performs the same discard-and-close of the current object
// without ending the client's lifetime; Scope.Close (or the explicit Close call) still does that.
//
// # What happens to the flows
//
// A network change used to end every stream on the shared connection. Reset now tries to move the
// connection instead (see Client): with open flows it creates ONE candidate socket through the
// same dialer, validates it, switches the connection onto it, retires the old transport and closes
// the old socket. The already-open streams continue, with their sequence and both directions, on
// the same QUIC connection.
//
// The outcome decides what is left behind:
//
//   - migrated: the connection and its streams continue on the new socket. Nothing is closed.
//   - candidate failed before any switch was requested: the connection was never touched, so it
//     stays exactly as it was (the old path may still be healthy). If that path has failed in the
//     meantime the QUIC connection ends on its own, and the next demand dials a new generation.
//   - switch requested but not confirmed: the send path is in an unknown state, so the generation
//     is retired (close exactly once) and the next demand dials a bounded fresh connection.
//
// A generation with no open flows is always retired the old way: moving an idle connection keeps
// a socket alive for nothing.
func (c *Client) Reset() error {
	// A creation that is still in flight belongs to the generation that just ended. It is
	// discarded and its waiters are released, exactly as before the migration existed.
	c.connAccess.Lock()
	pending := c.pending
	if pending != nil {
		pending.discarded = true
		if pending.cause == nil {
			pending.cause = net.ErrClosed
		}
		if c.pending == pending {
			// Hand the slot back: this creation is doomed, and a demand that arrives
			// after the reset must start a NEW one instead of joining it. The offer
			// goroutine still owns its own result (its waiters get the cause above and
			// its late success is closed there), so clearing the slot only stops new
			// waiters from attaching.
			//
			// Leaving it set made the first demand after a network change wait for a
			// dial that could only ever publish net.ErrClosed — the "cannot dial again
			// after the change" failure Reset exists to prevent, one connection to the
			// left of where Close used to cause it.
			c.pending = nil
		}
	}
	c.connAccess.Unlock()
	if pending != nil && pending.cancel != nil {
		pending.cancel(net.ErrClosed)
	}

	// One candidate per client: a second change waits for the first attempt to settle instead of
	// running its own, so the socket peak stays at one extra.
	c.migrateAccess.Lock()
	defer c.migrateAccess.Unlock()

	conn := c.conn.Load()
	if conn == nil {
		return nil
	}
	if !conn.hasStreams() || !conn.migrationAvailable() {
		// Nothing to preserve, or no candidate can be validated any more: retire the generation
		// the way it always was and let the next demand dial a fresh connection.
		c.retireConnection(conn)
		return nil
	}
	switch c.migrate(conn) {
	case migrationMigrated, migrationUnchanged:
		// The connection is still the published generation, either on its new socket or on the
		// one it had. Nothing to retire.
		return nil
	default:
		c.retireConnection(conn)
		return nil
	}
}

// migrationOutcome is what one network-change attempt achieved.
type migrationOutcome int

const (
	// migrationMigrated: the connection now sends on the candidate socket.
	migrationMigrated migrationOutcome = iota
	// migrationUnchanged: the candidate failed before a switch was requested; the connection was
	// never touched.
	migrationUnchanged
	// migrationUnknown: a switch has been requested and the connection's applied send path is
	// not confirmed to be the candidate, so this generation must be retired.
	migrationUnknown
)

// migrationTrace, when set, receives one line per network-change attempt and the reason it ended.
// It is nil in production (no allocation, no formatting) and set by the namespace evidence test,
// whose failure modes are only visible from inside the attempt.
var migrationTrace func(format string, args ...any)

func traceMigration(format string, args ...any) {
	if migrationTrace != nil {
		migrationTrace(format, args...)
	}
}

// migrate moves conn onto a candidate socket created through the production dialer. It owns the
// candidate for the whole attempt: every failure closes the socket it created, and the one
// success hands the socket to the connection.
func (c *Client) migrate(conn *clientConnection) migrationOutcome {
	ctx, cancel := context.WithTimeout(c.ctx, migrationProbeBudget+migrationRetireBudget)
	defer cancel()

	// A network change is exactly when the old socket starts failing: the route is gone and a
	// write on it returns a hard error. Open the migration window so that failure is treated as
	// the path being left instead of ending the connection the attempt is trying to save.
	stopExpectation := conn.ExpectPathSwitch(migrationProbeBudget + migrationRetireBudget + time.Second)
	defer stopExpectation()

	candidateSocket, err := c.dialer.DialContext(ctx, "udp", c.serverAddr)
	if err != nil {
		traceMigration("candidate dial failed: %v", err)
		return migrationUnchanged
	}
	candidate := newPathSocket(candidateSocket)
	// From here the socket is owned by this attempt or by the connection, never by both: a failed
	// attempt closes it directly, and the connection's path task closes it when the path ends.
	// pathSocket.close is idempotent, which is what lets both reach it.
	watchPathCleanup(conn, candidate)

	candidateTransport := &quic.Transport{Conn: connectedPacketConn{Conn: candidateSocket}}
	candidate.transport = candidateTransport
	path, err := conn.AddPath(candidateTransport)
	if err != nil {
		traceMigration("add path failed: %v", err)
		candidate.close()
		return migrationUnchanged
	}
	probeCtx, probeCancel := context.WithTimeout(ctx, migrationProbeBudget)
	err = path.Probe(probeCtx)
	probeCancel()
	if err != nil {
		traceMigration("probe failed: %v", err)
		// Abandoning the path detaches the connection from the candidate transport, which is what
		// makes closing its socket safe: otherwise the socket's read error would destroy conn.
		_ = path.Close()
		candidate.close()
		if errors.Is(err, context.DeadlineExceeded) {
			conn.noteFailedMigration()
		}
		return migrationUnchanged
	}
	if err := path.Switch(); err != nil {
		traceMigration("switch failed: %v", err)
		_ = path.Close()
		candidate.close()
		return migrationUnchanged
	}

	// The switch is requested. Whatever happens next, the connection may already be sending on the
	// candidate, so a failure here cannot be reported as "nothing changed".
	previous := conn.currentPath()
	if previous == nil {
		traceMigration("no previous path recorded for this connection")
		return migrationUnknown
	}
	retireCtx, retireCancel := context.WithTimeout(ctx, migrationRetireBudget)
	err = conn.RetireTransport(retireCtx, previous.transport)
	retireCancel()
	if err != nil {
		traceMigration("retire failed: %v", err)
		return migrationUnknown
	}
	// The old transport no longer routes to this connection, so closing its socket cannot reach it.
	// adoptPath performs that close and publishes the new socket.
	conn.adoptPath(candidate)
	conn.noteMigrated()
	return migrationMigrated
}

// retireConnection removes conn from the pool (only if it is still the published generation) and
// closes it exactly once. A newer generation published in the meantime is never touched.
func (c *Client) retireConnection(conn *clientConnection) {
	c.connAccess.Lock()
	if c.conn.Load() == conn {
		c.conn.Store(nil)
	}
	c.connAccess.Unlock()
	conn.closeConn()
}
