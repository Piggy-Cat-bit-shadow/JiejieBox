//go:build with_quic

package v2rayquic

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/quic-go"
	"github.com/sagernet/quic-go/http3"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/resourceguard"
	"github.com/sagernet/sing-box/common/runtimecoord"
	"github.com/sagernet/sing-box/common/tls"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-quic"
	"github.com/sagernet/sing/common"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
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
	// admission bounds one SHARED creation of this connection. It is asked exactly once per
	// creation and never for a reuse, because the creation - UDP socket, TLS handshake, transport
	// state, run loop - is the expensive event, while every later demand only opens a stream on the
	// connection that already exists.
	//
	// # Why this transport needs it when the pinned sing-quic clients have it
	//
	// This client deliberately mirrors their shared offer (one creation, many waiters). Those
	// clients were given a CreationAdmission hook when the process admission policy was introduced;
	// without the same hook here, a `transport.type=quic` VLESS/VMess/Trojan connection was the one
	// shared-QUIC family member whose creation was never charged, so a NetworkExtension's 32-slot
	// budget did not bound it at all.
	//
	// Nil admits everything, which is every platform without a process budget and every test that
	// does not opt in.
	admission func(ctx context.Context, create func(context.Context) error) error
	// registration is this pool's view of the network generation. It is nil when the transport was
	// built without a runtime coordinator, and every generation check then degrades to the previous
	// behaviour (see generation.go).
	registration       *runtimecoord.Registration
	removeRegistration func()
	// residency is the object-identity ledger every connection of this client reports into. See
	// residency.go. It is a VALUE, so a connection can always point at it and a client that never
	// dials still has a (zero) ledger to answer with.
	residency clientResidency
	// coordinator is the generation clock itself. currentGeneration reads it directly: a
	// registration's Epoch() is the generation its resource was last rebuilt against, which is not
	// the same question as "what generation is current now" and cannot be used to detect a change
	// that happened DURING a dial.
	coordinator *runtimecoord.Coordinator
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
//
// # Two socket shapes, one lifetime
//
// The handshake path is a CONNECTED socket (DialContext "udp"), because that is what a QUIC
// handshake for one peer needs. A migration candidate is an UNCONNECTED packet socket
// (ListenPacket), because the library writes every candidate-path packet with an explicit
// destination sockaddr. Keeping both in this one type means the single close action, the retired
// channel and the cleanup task are shared by the two shapes instead of being duplicated per call
// site - and it is why the wrong shape cannot be reintroduced by "just dialling a second socket".
type pathSocket struct {
	socket    net.Conn
	packet    net.PacketConn
	transport *quic.Transport
	// retired is closed by close, so anything waiting for THIS path to end wakes when it does
	// instead of waiting for the connection. It is the path's own lifetime token.
	retired   chan struct{}
	closeOnce sync.Once
}

func newPathSocket(socket net.Conn) *pathSocket {
	return &pathSocket{socket: socket, retired: make(chan struct{})}
}

// newPathPacketSocket is the unconnected shape: a candidate path's socket.
func newPathPacketSocket(packet net.PacketConn) *pathSocket {
	return &pathSocket{packet: packet, retired: make(chan struct{})}
}

// close closes the socket exactly once and publishes the path's end. Several owners reach here -
// the failed attempt that created it, the migration that replaced it, and the cleanup task watching
// it - and only the first of them is the close.
func (p *pathSocket) close() {
	p.closeOnce.Do(func() {
		if p.socket != nil {
			_ = p.socket.Close()
		}
		if p.packet != nil {
			_ = p.packet.Close()
		}
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
	// drainAfterStreams marks a generation that was detached from the pool because the network
	// generation moved: it must not be handed out again, and it closes when its last stream ends.
	// It is set by generation.go and honoured by releaseStream, so a transfer that is already
	// running finishes instead of being cut by the retirement.
	drainAfterStreams atomic.Bool
	// residency is the counter set this connection reports its lifecycle into, or nil for a
	// connection a test constructed directly. See residency.go.
	residency *clientResidency
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
	// A detached generation closes itself on its last stream, exactly like an idle-policy close.
	//
	// It closes even when the caller asked the session to survive its own stream: keepSession means
	// "do not end a session other work may still use", and a DETACHED generation is no longer
	// reachable from the pool - nothing can reuse it - so honouring keepSession here would leave a
	// QUIC connection and its socket alive with no owner, which is a leak rather than a courtesy.
	idleClose := (c.closeIdle.Load() && !keepSession) || c.drainAfterStreams.Load()
	closed := idleClose && c.streams == 0 && c.markClosedLocked()
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
//
// It is also the ONE place the residency ledger's release is recorded, which is what makes
// Terminated a count of completed releases rather than of close attempts: the once-guarded body runs
// exactly once per object.
func (c *clientConnection) closeConn() {
	c.closeOnce.Do(func() {
		if c.closeFn != nil {
			c.closeFn()
		}
		c.residency.noteReleased(c.drainAfterStreams.Load())
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
	// The process admission policy, when this build has one. A nil guard keeps the client on its
	// previous path with no hook, which is what every platform outside an iOS Network Extension and
	// every unit test gets.
	if guard := resourceguard.FromContext(ctx); guard != nil {
		admissionKey := "v2rayquic/" + serverAddr.String()
		client.admission = func(admitCtx context.Context, create func(context.Context) error) error {
			return guard.AdmitCreation(admitCtx, admissionKey, create)
		}
	}
	// The generation the pool rebuilds itself against. A transport built in a test or by an
	// embedder without a coordinator gets no registration and keeps its previous behaviour.
	client.coordinator = service.FromContext[*runtimecoord.Coordinator](ctx)
	client.registration, client.removeRegistration = generationRegistration(
		client.coordinator, "v2rayquic/"+serverAddr.String())
	return client, nil
}

// offer returns the shared connection, joining the creation already in flight when there is one.
//
// The waiter's ctx is only ever used for waiting: it must be able to end THIS caller's wait without
// cancelling the shared dial, because the dial's result belongs to the client and may serve callers
// that are still there. That is why the dial's LIFETIME comes from the client below and never
// from ctx; its VALUES (traffic class, an already-held admission permit) do come from the demand that
// started the creation, because those describe the work being created and are what the admission
// hook has to see. See newOfferContext for the split.
func (c *Client) offer(ctx context.Context) (*clientConnection, error) {
	for {
		c.connAccess.Lock()
		conn := c.conn.Load()
		if conn != nil && conn.active() {
			c.connAccess.Unlock()
			if !c.generationStale() {
				return conn, nil
			}
			// The pooled generation belongs to a previous network. It is detached from the pool -
			// never handed to this caller - and the next turn of this loop starts a creation for
			// the current generation. A detach that loses the race (another caller replaced or
			// drained the pool first) also falls through: re-reading is correct in both cases, and
			// the loop terminates because each turn either publishes a generation or finds the pool
			// in a state that dials.
			c.detachStaleGeneration(conn)
			continue
		}
		pending := c.pending
		if pending != nil {
			c.connAccess.Unlock()
			return pending.wait(ctx)
		}
		offerCtx, cancel := c.newOfferContext(ctx)
		pending = &clientOffer{
			done:   make(chan struct{}),
			cancel: cancel,
		}
		c.pending = pending
		c.connAccess.Unlock()

		go c.completeOffer(pending, offerCtx)
		return pending.wait(ctx)
	}
}

// newOfferContext builds the context one shared creation runs under.
//
// # The two halves, and why they come from different contexts
//
// LIFETIME comes from the client: a waiter that gives up, or a request with a short deadline, must
// not tear down a dial whose result the client may still hand to someone else. Cancellation is
// therefore chained to c.ctx, which Close cancels with its cause, and never to the caller's
// cancellation.
//
// VALUES come from the demand that started the creation: the resolved traffic class and, when this
// creation is nested inside an already-admitted dial, the held-permit marker. Those are properties of
// the work being created, not of the caller's patience, so context.WithoutCancel keeps them while
// dropping the deadline and the cancellation, and the AfterFunc adds the client lifetime back.
//
// The registration the AfterFunc creates is removed by the release function the offer calls when the
// creation returns, so a long-lived client does not accumulate one registration per creation.
func (c *Client) newOfferContext(callerCtx context.Context) (context.Context, context.CancelCauseFunc) {
	detached := context.WithoutCancel(callerCtx)
	offerCtx, cancel := context.WithCancelCause(detached)
	stop := context.AfterFunc(c.ctx, func() {
		cancel(context.Cause(c.ctx))
	})
	return offerCtx, func(err error) {
		stop()
		cancel(err)
	}
}

// completeOffer runs one shared creation and publishes its result. The client is the owner: a
// success is accepted into the pool unless the client was closed first, in which case the late
// connection is closed here - exactly once - instead of being published or leaked.
func (c *Client) completeOffer(pending *clientOffer, offerCtx context.Context) {
	// The generation the result is being built FOR. A dial that starts before a network change and
	// finishes after it produces a socket bound to the path that just ended, and publishing that as
	// the current generation would hand every later demand a session the audit called stale. The
	// comparison is one number taken twice, and it is the same clock the dialer itself compares
	// against, so "a result from the previous generation" has one definition in the tree.
	startGeneration := c.currentGeneration()
	conn, err := c.createConnection(offerCtx)
	// The dial context is no longer needed once the dial returned; releasing it here keeps the
	// per-offer timer/context from outliving the work even when the result is discarded.
	pending.cancel(nil)

	var lateGeneration bool
	if err == nil && conn != nil {
		lateGeneration = c.coordinator != nil && c.currentGeneration() != startGeneration
		if lateGeneration {
			// The result belongs to the generation that ended while it was being established.
			// Released here, exactly once, instead of being published.
			conn.closeConn()
			err = staleGenerationError{}
		}
	}

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
	if !discard && !lateGeneration && pending.err == nil {
		// The published generation was built after the last network change this pool observed, so
		// the pool is current again. Recording it here - and only here - is what keeps Stale() from
		// reporting a generation that was just replaced, and keeps the reference to this pool's
		// epoch from drifting behind the coordinator's.
		c.acknowledgeGeneration()
	}
}

// createConnection runs the shared creation under the process admission policy, when this client has
// one.
//
// # Why the permit wraps only this call
//
// The permit bounds CREATION concurrency. It is released as soon as the constructor returns - success
// or failure - so a healthy process establishes connections as fast as the gate allows and then
// behaves exactly as before. Holding it for the connection's lifetime would additionally cap live
// connections, but that is a different policy with a different unit, and the gate's counters would
// then describe sessions instead of creations.
//
// A nil hook admits everything and is the whole point of the optional field: on every platform
// without a process budget this is one nil check around the construction.
func (c *Client) createConnection(offerCtx context.Context) (*clientConnection, error) {
	if c.admission == nil {
		return c.dialConn(offerCtx)
	}
	var created *clientConnection
	err := c.admission(offerCtx, func(admitCtx context.Context) error {
		var createErr error
		created, createErr = c.dialConn(admitCtx)
		return createErr
	})
	if err != nil {
		// A refusal returns no connection even if the constructor somehow produced one: the policy
		// owns the answer, and publishing a connection it refused would make the budget untrue.
		if created != nil {
			created.closeConn()
		}
		return nil, err
	}
	return created, nil
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
		residency: &c.residency,
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
	if c.removeRegistration != nil {
		c.removeRegistration()
		c.removeRegistration = nil
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
	return c.ResetContext(c.ctx)
}

// ResetContext is Reset with a caller-supplied outer bound for the attempt.
//
// The network manager runs its listener phase under one deadline, and that deadline is what makes a
// network change bounded as a whole rather than per outbound. Reset itself is the only listener here
// that can take seconds (validate a candidate, retire the old transport), so it accepts that
// deadline: the migration's own budgets stay as they are, but they are cut short if the phase's
// deadline is earlier. A cancelled candidate is an abandoned path - its socket is closed and the
// connection is left on the path it had - and a cancelled retirement retires the generation through
// the same "unknown" branch as any other failed switch. Both are the already-pinned failure paths.
func (c *Client) ResetContext(ctx context.Context) error {
	// A creation that is still in flight belongs to the generation that just ended. It is
	// discarded and its waiters are released, exactly as before the migration existed.
	c.connAccess.Lock()
	connAtStart := c.conn.Load()
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
			// dial that could only ever publish net.ErrClosed - the "cannot dial again
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

	// The pending slot was released above, so a demand can create and publish a NEW generation while
	// this reset is waiting for migrateAccess (which may be held by a previous migration for seconds).
	// That connection was built on the network that is current now; acting on it would retire or
	// migrate a generation this reset does not own. Only the connection observed when the reset began
	// is this reset's generation.
	conn := c.conn.Load()
	if conn == nil || conn != connAtStart {
		return nil
	}
	if !conn.hasStreams() || !conn.migrationAvailable() {
		// Nothing to preserve, or no candidate can be validated any more: retire the generation
		// the way it always was and let the next demand dial a fresh connection.
		c.retireConnection(conn)
		return nil
	}
	switch c.migrate(ctx, conn) {
	case migrationMigrated:
		// The connection now sends on a socket of the current network. That IS a rebuild against
		// the current generation, so the pool is current again and a demand arriving after this
		// point may join it.
		c.acknowledgeGeneration()
		return nil
	case migrationUnchanged:
		// The candidate failed before any switch was requested and the connection was never
		// touched. It is NOT acknowledged on purpose: the connection is still on the previous
		// path, so the next demand detaches it (it drains when its flows end) and dials a
		// generation that was established after the change.
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
// success hands the socket to the connection. parent bounds the attempt from outside; the
// migration's own probe and retire budgets bound it from inside.
func (c *Client) migrate(parent context.Context, conn *clientConnection) migrationOutcome {
	if parent == nil {
		parent = c.ctx
	}
	ctx, cancel := context.WithTimeout(parent, migrationProbeBudget+migrationRetireBudget)
	defer cancel()

	// A network change is exactly when the old socket starts failing: the route is gone and a
	// write on it returns a hard error. Open the migration window so that failure is treated as
	// the path being left instead of ending the connection the attempt is trying to save.
	stopExpectation := conn.ExpectPathSwitch(migrationProbeBudget + migrationRetireBudget + time.Second)
	defer stopExpectation()

	// The candidate is an UNCONNECTED packet socket bound to the interface this network gives us,
	// aimed at the peer address the connection is already talking to.
	//
	// # Why not a connected socket (the defect this replaces)
	//
	// A candidate used to be opened with DialContext("udp", ...) and adapted to net.PacketConn by a
	// small wrapper that ignored the address argument. That shape is wrong for any platform where
	// the pinned library can reach the socket's file descriptor: it builds its OOB transport and
	// sends each packet with an EXPLICIT destination sockaddr, which a connected socket rejects on
	// Darwin (windows never reaches that branch, which is why only the macOS job failed). The write
	// error is discarded inside the library, so the only visible symptom was a silent 10 s path
	// validation timeout - the audit's Q30.
	//
	// An unconnected socket is what the library's destination-sockaddr write is designed for, so the
	// receipt/send tuning, GSO and ECN all stay available, and the shape is identical on every
	// platform. The peer address is taken from the live connection rather than from c.serverAddr: a
	// path to a DIFFERENT server address would be a different connection's peer, and re-resolving a
	// domain here could pick one.
	candidateAddress := c.serverAddr
	if remoteAddress := conn.RemoteAddr(); remoteAddress != nil {
		candidateAddress = M.SocksaddrFromNet(remoteAddress)
	}
	candidatePacket, err := c.dialer.ListenPacket(ctx, candidateAddress)
	if err != nil {
		traceMigration("candidate listen failed: %v", err)
		return migrationUnchanged
	}
	candidate := newPathPacketSocket(candidatePacket)
	// From here the socket is owned by this attempt or by the connection, never by both: a failed
	// attempt closes it directly, and the connection's path task closes it when the path ends.
	// pathSocket.close is idempotent, which is what lets both reach it.
	watchPathCleanup(conn, candidate)

	candidateTransport := &quic.Transport{Conn: candidatePacket}
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
		// A probe that ran out of its OWN budget is a failed validation, and repeating it on every
		// network change would stall each change by the full probe budget for nothing. A probe cut
		// short by the caller's deadline (the network manager's phase deadline, or an explicit
		// ResetContext bound) is not a statement about the candidate at all: counting it would let
		// two well-behaved cancellations spend the retry budget and turn the next change into a
		// generation retirement. The distinction is the parent's error, not the probe's.
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
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
