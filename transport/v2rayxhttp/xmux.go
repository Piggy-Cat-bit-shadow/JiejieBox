package v2rayxhttp

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"

	"golang.org/x/net/http2"
)

// XMUX — reuse of HTTP connections across XHTTP dials. Ported from
// sing-box-extended (transport/v2rayxhttp/mux.go, tag v1.13.18-extended-2.6.4)
// over Xray's XmuxConfig; see SPECS/TASKS/059-XHTTP_XMUX for the contract and
// REFERENCE_mux.go.txt in that folder for the source we mirrored.
//
// Nothing here changes what goes on the wire: the server identifies a session by
// its session id, not by the connection carrying it, so pooling is purely a
// client-side policy. What it buys is (a) behaving like an Xray client, whose
// xmux section subscription configs routinely carry, and (b) not paying a fresh
// TCP+TLS(+Reality) handshake for every stream.

// xmuxConn is the poolable resource: one HTTP connection (for us, one
// *http2.Transport with its own dialer). It is an interface so tests can pool
// fakes instead of real transports.
type xmuxConn interface {
	// Close releases the underlying connection.
	Close()
	// IsClosed reports whether the connection died on its own (transport error,
	// server GOAWAY); such connections are evicted on the next pool lookup.
	IsClosed() bool
	// roundTripper returns the RoundTripper carrying this connection's requests.
	roundTripper() http.RoundTripper
}

// xmuxClient wraps one pooled connection with the counters that decide when it
// is retired.
//
// Closing is DEFERRED: Close on a connection that still carries streams only
// marks it, and the real teardown happens when the last stream releases it via
// addOpenUsage(-1). Retiring a connection must never tear down traffic the user
// is still running through it.
type xmuxClient struct {
	conn         xmuxConn
	manager      *xmuxManager // breaker trips report here; never nil for pooled clients
	openUsage    int          // streams currently alive on this connection
	leftUsage    int          // handouts left before retirement; -1 = unlimited
	leftRequests int32        // HTTP requests left before retirement (atomic)
	unreusableAt time.Time

	// lx: SPEC 076 — circuit breaker. consecFails counts consecutive stream
	// failures (failed round trip, non-200 response, remote death of a download
	// body); any genuine success resets it. At xmuxBreakerThreshold the
	// connection is marked failing and evicted on the next pool lookup.
	consecFails atomic.Int32
	failing     atomic.Bool

	// draining marks a connection that must not carry any NEW stream. It is set
	// by RetireSuspect, which is the reuse-boundary action: a connection that
	// existed before a sleep is no longer trusted, so new work must dial a fresh
	// path, while a stream already on it keeps its transport until it finishes.
	//
	// It is a separate flag from closed (the pool no longer holds it) and from
	// failing (the breaker retired it), because the reason shows up in the
	// eviction log and because a draining connection is deliberately still
	// reachable by the stream that is on it.
	draining atomic.Bool

	// generation is the pool boundary this connection was opened under. It is a POOL-LOCAL counter
	// (see xmuxManager.generation), not runtimecoord's network generation: it exists so the inventory
	// can say whether a connection that is still draining predates the last reuse boundary or was
	// opened after it. Those are the "old generation" and "current generation" of this pool.
	generation uint64

	// released records that the underlying connection's Close has actually run. It is the pool's
	// RELEASE CONFIRMATION, and it is deliberately separate from closed: closed only says the pool
	// stopped handing the connection out, while a connection with a live stream keeps its transport
	// until that stream releases it. A snapshot that reported closed as released would claim a
	// teardown that has not happened.
	released bool

	closed bool
	access sync.Mutex
}

// releaseConn tears the underlying connection down and records the release exactly once. Caller must
// hold c.access.
//
// The once-guard is not only about the counter: our conns are closed twice in real paths (an expired
// deadline closing the read half plus the caller's Close, see SPECS/TASKS/050), and a connection that
// was retired empty can still be handed to a racing acquire. The underlying Close is idempotent, and
// this guard makes "one connection, one confirmed release" true regardless.
func (c *xmuxClient) releaseConn() {
	if c.released {
		return
	}
	c.released = true
	c.conn.Close()
	if c.manager != nil {
		c.manager.released.Add(1)
	}
}

// isReleased reports whether this connection's teardown has completed. It is read by the inventory,
// which holds the pool lock and therefore takes the client lock in the same order get() does.
func (c *xmuxClient) isReleased() bool {
	c.access.Lock()
	defer c.access.Unlock()
	return c.released
}

// close marks the connection retired, tearing it down immediately only if no
// stream is using it.
func (c *xmuxClient) close() {
	c.access.Lock()
	defer c.access.Unlock()
	c.closed = true
	if c.openUsage <= 0 {
		c.releaseConn()
	}
}

// addOpenUsage adjusts the live-stream count, tearing the connection down when
// the last stream leaves an already-retired connection.
func (c *xmuxClient) addOpenUsage(delta int) {
	c.access.Lock()
	defer c.access.Unlock()
	c.openUsage += delta
	if c.closed && c.openUsage <= 0 {
		c.releaseConn()
	}
}

func (c *xmuxClient) getOpenUsage() int {
	c.access.Lock()
	defer c.access.Unlock()
	return c.openUsage
}

// acquireUsage reserves one live-stream slot atomically with the retired check.
//
// # Why this is separate from addOpenUsage
//
// The pool selects a client under its own lock and returns it; the caller then used to increment the
// usage outside that lock. A network change or trim could retire and RELEASE the connection between
// the two steps, and the later Add(1) then revived a client whose transport was already closed -
// dispatch on it could create a new HTTP/2 connection that no Close/trim could reach. Reserving under
// the client's lock makes the retired check and the increment one step: a retired client refuses the
// reservation, so the caller selects another one instead of resurrecting this object.
func (c *xmuxClient) acquireUsage() bool {
	c.access.Lock()
	defer c.access.Unlock()
	if c.closed {
		return false
	}
	c.openUsage++
	return true
}

// takeRequest accounts one HTTP request against h_max_request_times. It is
// called per REQUEST, not per stream: a packet-up stream issues one upload POST
// per Write, and a connection that ignored those would outlive the limit the
// server was told about.
func (c *xmuxClient) takeRequest() {
	atomic.AddInt32(&c.leftRequests, -1)
}

// http2XmuxConn adapts an *http2.Transport to the pool's resource interface.
//
// http2.Transport has no "is it dead" signal of its own — it silently redials
// on the next request — so liveness is tracked by us: the transport is
// considered closed once we tore it down. That is enough for the pool, whose
// other three eviction rules (reuse, requests, age) do the real rotation work.
type http2XmuxConn struct {
	transport *http2.Transport
	closed    atomic.Bool
}

func (c *http2XmuxConn) Close() {
	if c.closed.Swap(true) {
		return
	}
	c.transport.CloseIdleConnections()
}

func (c *http2XmuxConn) IsClosed() bool {
	return c.closed.Load()
}

func (c *http2XmuxConn) roundTripper() http.RoundTripper {
	return c.transport
}

// lx: SPEC 076 — circuit-breaker knobs. Variables, not consts: tests shrink
// the timings. Field case behind them: a CDN resetting every upload stream
// span an unthrottled dial→reset→dial loop at channel speed and pinned both
// cores of an ARM64 router until the core was restarted (issue #14).
var (
	// xmuxBreakerThreshold is how many CONSECUTIVE stream failures retire the
	// pooled connection.
	xmuxBreakerThreshold = int32(3)
	// xmuxBackoffInitial/xmuxBackoffCap bound the new-transport backoff window:
	// each breaker trip doubles the delay, any genuine success resets it.
	xmuxBackoffInitial = 100 * time.Millisecond
	xmuxBackoffCap     = 3 * time.Second
)

// noteFailure records one stream failure. Crossing the threshold marks the
// connection failing (evicted on the next pool lookup) and arms/extends the
// manager's new-transport backoff. lx: SPEC 076.
func (c *xmuxClient) noteFailure() {
	if c.consecFails.Add(1) != xmuxBreakerThreshold {
		return
	}
	c.failing.Store(true)
	if c.manager != nil {
		c.manager.noteBreakerTrip()
	}
}

// noteSuccess records genuine data flow (a drained 200 upload POST, a first
// successful download-body read) — headers alone don't count, the field case
// had 200-raises whose bodies died seconds later. Resets the failure streak
// and disarms the manager backoff. lx: SPEC 076.
func (c *xmuxClient) noteSuccess() {
	c.consecFails.Store(0)
	if c.manager != nil {
		c.manager.resetBackoff()
	}
}

// roundTrip issues one HTTP request over this pooled connection, accounting it
// against h_max_request_times. Every XHTTP request must go through here — a
// request that bypassed it would let the connection outlive the limit.
//
// It also feeds the breaker (lx: SPEC 076): an error or a non-200 status is a
// stream failure — every XHTTP endpoint expects exactly 200, all call sites
// treat anything else as fatal for the stream. Success is deliberately NOT
// noted here: response headers arriving says nothing about the stream living.
//
// lx: SPEC 094 — context.Canceled is neutral. Request contexts are cancelled
// only by us (the conn-scoped cancel in Close) or by the caller, so a
// cancelled RoundTrip says nothing about the pooled connection's health;
// counting it let a burst of Close calls (interrupt_exist_connections)
// retire a healthy connection and arm the backoff. context.DeadlineExceeded
// stays a failure on purpose: headers not arriving in time on a pooled
// connection is exactly the half-dead peer the breaker exists for.
func (c *xmuxClient) roundTrip(request *http.Request) (*http.Response, error) {
	c.takeRequest()
	response, err := c.conn.roundTripper().RoundTrip(request)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			c.noteFailure()
		}
	} else if response.StatusCode != http.StatusOK {
		c.noteFailure()
	}
	return response, err
}

// xmuxRelease returns the pooled connection to the pool exactly once, however
// many times the conn's Close is called.
//
// Idempotency is not decoration: our conns are closed twice in real paths (an
// expired deadline closing the read half plus the caller's Close, see
// SPECS/TASKS/050), and a double release would drive openUsage negative — the
// connection would then never be torn down after retirement.
type xmuxRelease struct {
	client *xmuxClient
	once   sync.Once
}

func newXmuxRelease(client *xmuxClient) *xmuxRelease {
	return &xmuxRelease{client: client}
}

func (r *xmuxRelease) release() {
	if r == nil || r.client == nil {
		return
	}
	r.once.Do(func() {
		r.client.addOpenUsage(-1)
	})
}

// roundTrip issues a request over the held connection. packetConn keeps its
// release handle for the lifetime of the stream and sends every upload POST
// through it, so uploads are accounted like any other request.
func (r *xmuxRelease) roundTrip(request *http.Request) (*http.Response, error) {
	return r.client.roundTrip(request)
}

// noteSuccess/noteFailure forward stream-health observations from the conns
// (download-body reads, drained upload POSTs) to the pooled connection's
// breaker. Nil-safe like release. lx: SPEC 076.
func (r *xmuxRelease) noteSuccess() {
	if r == nil || r.client == nil {
		return
	}
	r.client.noteSuccess()
}

func (r *xmuxRelease) noteFailure() {
	if r == nil || r.client == nil {
		return
	}
	r.client.noteFailure()
}

// singleTransportXmux builds a manager permanently bound to one RoundTripper.
// It is how a Client is assembled around a caller-supplied transport (tests,
// and any future path that wants to drive XHTTP over a fixed transport): the
// pool degenerates to a single connection that is never rotated.
func singleTransportXmux(transport http.RoundTripper) *xmuxManager {
	conn := &fixedXmuxConn{transport: transport}
	manager := newXmuxManager(xmuxConfig{}, func() xmuxConn { return conn })
	return manager
}

// fixedXmuxConn is a pool resource wrapping an already-built RoundTripper.
type fixedXmuxConn struct {
	transport http.RoundTripper
	closed    atomic.Bool
}

func (c *fixedXmuxConn) Close()                          { c.closed.Store(true) }
func (c *fixedXmuxConn) IsClosed() bool                  { return c.closed.Load() }
func (c *fixedXmuxConn) roundTripper() http.RoundTripper { return c.transport }

// xmuxConfig is the resolved (parsed, defaulted) XMUX option set.
type xmuxConfig struct {
	maxConcurrency   intRange
	maxConnections   intRange
	cMaxReuseTimes   intRange
	hMaxRequestTimes intRange
	hMaxReusableSecs intRange
	keepAlivePeriod  time.Duration
}

// xmuxManager owns the connection pool.
type xmuxManager struct {
	config      xmuxConfig
	concurrency int // rolled once at construction
	connections int // rolled once at construction
	newConn     func() xmuxConn
	clients     []*xmuxClient
	access      sync.Mutex
	// closed is set by Close and checked by get. Without it a dial racing (or following) Close
	// opened a fresh pooled connection that no later Close could reach: the pool had been emptied,
	// the transport was already closed, and the new connection had no owner left.
	closed bool
	// onEvent, when set, receives debug lines about pool activity: connection
	// opened, connection evicted (with cause). Stands in for pool metrics — the
	// pool state is fully determined by the config, so what is worth observing is
	// the transitions, not the gauge. See SPECS/TASKS/059 §8.2.
	onEvent func(format string, args ...any)

	// detached holds connections the pool has retired while they still carry streams. Their teardown
	// is deferred until the last stream releases them, which is exactly the state no logical counter
	// can see: they are no longer pooled, not yet closed, and still holding a transport. Every
	// boundary and every snapshot prunes the entries whose release has completed, so the slice is
	// bounded by the number of connections actually draining, not by the number ever retired.
	detached []*xmuxClient
	// generation is the pool's own reuse-boundary counter (NOT runtimecoord's network generation).
	// It increments when RetireSuspect retires the pool; a connection records the value current at its
	// open, so the inventory can separate draining connections that predate the boundary ("old
	// generation") from connections opened after it.
	generation uint64
	// opened and released are the pool's life counters: connections created, and connections whose
	// underlying Close actually ran. Their difference against the live inventory is what proves the
	// release confirmation comes from the real end point rather than from the retire call.
	opened   atomic.Uint64
	released atomic.Uint64

	// lx: SPEC 076 — new-transport backoff, armed by breaker trips. backoffDelay
	// doubles per trip within [xmuxBackoffInitial, xmuxBackoffCap]; blockedUntil
	// is the moment before which get() refuses to OPEN a transport (an existing
	// pooled connection is still handed out freely). backoffArmed mirrors
	// "delay != 0" atomically so resetBackoff on every successful read stays a
	// single atomic load in the healthy case. All under m.access except the gate.
	backoffDelay time.Duration
	blockedUntil time.Time
	backoffArmed atomic.Bool
}

// xmuxPoolStats is a consistent snapshot of the pool's OWN inventory: entries it created, holds, is
// draining, and has confirmed closed.
//
// # What one unit is
//
// Every field counts POOL ENTRIES, not physical sockets. One entry is one xmuxClient wrapping one HTTP
// transport: for HTTP/3 that wrapper carries its QUIC connection, while for HTTP/2 the wrapper owns an
// http2.Transport whose internal pool may hold several TCP/TLS sockets. The net/http, http2 and
// http3 layers own those sockets. Nothing here counts them, and no field may be added to another
// layer's socket count as if the units matched.
//
// # What it can and cannot prove
//
// These are the pool's facts, at the pool's state points: a connection is open once newClientLocked
// created it, pooled while get() may hand it out, draining once it was detached while still carrying
// a stream, and released once the underlying Close actually ran. The last one is the release
// confirmation the OS/transport can give; a retire call is not.
//
// The HTTP/2 library's internal connection pool is a DIFFERENT owner. Its own idle-connection cache is
// not visible here and is not counted: nothing in this package can prove when the library closes a
// connection the pool already dropped. That is NOT_VERIFIABLE_HERE by construction, and no field here
// may be read as covering it.
type xmuxPoolStats struct {
	// Generation is the current pool boundary counter (see xmuxManager.generation).
	Generation uint64
	// Pooled is the number of pool ENTRIES currently held (Active + Idle); not a socket count.
	Pooled int
	// Active is pooled entries carrying at least one stream.
	Active int
	// Idle is pooled entries carrying none. These are what CloseIdleConnections may retire.
	Idle int
	// Draining is detached entries whose teardown is deferred until their last stream releases.
	Draining int
	// DrainingOldGeneration is the subset of Draining opened before the current generation.
	DrainingOldGeneration int
	// Opened is how many ENTRIES (transport wrappers) the pool created over its life.
	Opened uint64
	// Released is how many entries had their underlying transport Close actually run over the
	// pool's life. This is the release confirmation the wrapper can give, not a socket count.
	Released uint64
}

// stats returns the pool inventory plus its life counters.
//
// It takes the pool lock and each client's lock in the same order get() does (pool, then client), so
// it cannot deadlock against an acquire or a release. The life counters are atomics read under the
// pool lock; a concurrent release is a single connection's bookkeeping and may land just after the
// snapshot, which is why the caller pairs the snapshot with the invariant (see the test) rather than
// demanding a frozen instant.
func (m *xmuxManager) stats() xmuxPoolStats {
	m.access.Lock()
	defer m.access.Unlock()
	m.pruneDetachedLocked()
	stats := xmuxPoolStats{
		Generation: m.generation,
		Pooled:     len(m.clients),
		Draining:   len(m.detached),
		Opened:     m.opened.Load(),
		Released:   m.released.Load(),
	}
	for _, client := range m.clients {
		if client.getOpenUsage() > 0 {
			stats.Active++
		} else {
			stats.Idle++
		}
	}
	for _, client := range m.detached {
		if client.generation < m.generation {
			stats.DrainingOldGeneration++
		}
	}
	return stats
}

// pruneDetachedLocked drops detached connections whose release has completed. Caller must hold
// m.access; the client lock is taken inside, in the pool-then-client order.
func (m *xmuxManager) pruneDetachedLocked() {
	kept := m.detached[:0]
	for _, client := range m.detached {
		if !client.isReleased() {
			kept = append(kept, client)
		}
	}
	m.detached = kept
}

// noteBreakerTrip doubles the new-transport backoff window. lx: SPEC 076.
func (m *xmuxManager) noteBreakerTrip() {
	m.access.Lock()
	if m.backoffDelay == 0 {
		m.backoffDelay = xmuxBackoffInitial
	} else if m.backoffDelay *= 2; m.backoffDelay > xmuxBackoffCap {
		m.backoffDelay = xmuxBackoffCap
	}
	m.blockedUntil = timeNow().Add(m.backoffDelay)
	m.backoffArmed.Store(true)
	delay := m.backoffDelay
	m.access.Unlock()
	m.logf("xmux: breaker tripped, new-transport backoff %s", delay)
}

// resetBackoff disarms the backoff window on genuine success. lx: SPEC 076.
func (m *xmuxManager) resetBackoff() {
	if !m.backoffArmed.Load() {
		return
	}
	m.access.Lock()
	m.backoffDelay = 0
	m.blockedUntil = time.Time{}
	m.backoffArmed.Store(false)
	m.access.Unlock()
	m.logf("xmux: breaker backoff reset")
}

func newXmuxManager(config xmuxConfig, newConn func() xmuxConn) *xmuxManager {
	return &xmuxManager{
		config:      config,
		concurrency: config.maxConcurrency.rand(),
		connections: config.maxConnections.rand(),
		newConn:     newConn,
		clients:     make([]*xmuxClient, 0),
	}
}

func (m *xmuxManager) logf(format string, args ...any) {
	if m.onEvent != nil {
		m.onEvent(format, args...)
	}
}

// CloseIdleConnections retires only the pooled connections that are carrying nothing.
//
// # Why the distinction matters here
//
// This is the memory-trim path, not the network-change path. A network change retires EVERYTHING
// (Close below), because every pooled connection belongs to the network being left. A trim must
// instead be unable to disturb a live stream, and must not become a reconnect trigger: retiring a
// connection with a stream on it would not break that stream - the teardown is deferred - but it
// would make the NEXT request dial, which is exactly the "trim that caused a reconnect" the
// lifecycle model forbids. So a connection is only retired when its live-stream count is zero, and
// retiring it costs nothing but a dial the next time there is real demand.
func (m *xmuxManager) CloseIdleConnections() {
	m.access.Lock()
	var idle []*xmuxClient
	kept := make([]*xmuxClient, 0, len(m.clients))
	for _, client := range m.clients {
		if client.getOpenUsage() == 0 {
			idle = append(idle, client)
			continue
		}
		kept = append(kept, client)
	}
	m.clients = kept
	m.pruneDetachedLocked()
	m.access.Unlock()
	for _, client := range idle {
		client.close()
	}
}

// Close retires every pooled connection. Live streams keep theirs alive until
// they finish (deferred close).
func (m *xmuxManager) Close() {
	m.access.Lock()
	m.closed = true
	clients := m.clients
	m.clients = nil
	for _, client := range clients {
		if client.getOpenUsage() > 0 {
			m.detached = append(m.detached, client)
		}
	}
	m.pruneDetachedLocked()
	m.access.Unlock()
	for _, client := range clients {
		client.close()
	}
}

// RetireSuspect is the reuse-boundary action: no pooled connection may carry a NEW stream, and a
// connection that is carrying nothing is closed now.
//
// # Why this is not CloseIdleConnections
//
// CloseIdleConnections is the trim, and it is deliberately unable to make the next stream dial a
// different connection - that is what keeps a memory pass from becoming a reconnect trigger. This is
// the opposite trade and it is correct at exactly one place: the sleep boundary, where a connection
// that predates the sleep is no longer trusted, and adding a new stream to it is what produces the
// reported stall when the middle of the path forgot it.
//
// # Why the busy connections are drained rather than closed
//
// A connection with streams on it cannot be closed - those streams would lose their transport, and
// the boundary exists to prevent stalls, not to cause them. It must not be left in the pool either,
// because an OPEN stream is not evidence that the path survived: a stream can be open and silent
// across a sleep (a paused response body, a packet-up session between packets), and the sleep is
// exactly the interval in which a NAT mapping or a server-side idle timer can expire. Draining is
// the only action that is neither wrong: the pool stops handing the connection out, the streams on
// it finish on the connection they started on, and the teardown happens when the last one leaves.
//
// # Why the removal happens here rather than on the next lookup
//
// The mechanism is the pool's own: the clients are detached from the list and `close()` is called on
// each, which is what Close (the reset-shaped call) already does - the teardown is deferred by
// openUsage, so a busy connection survives its streams. Detaching under m.access is what makes the
// refusal atomic with respect to get(): an earlier version only marked the clients and left them in
// the list for the next lookup to evict, which left a window in which a concurrent get() held the
// lock, saw no reason to evict, and handed a pre-boundary connection to a stream the boundary had
// just refused it. The draining flag is kept as the record of WHY, and evictCause still reports it,
// so a client that ever found its way back into the list could not be handed out again.
//
// Nothing here dials, and nothing here waits.
//
// # The first stream after a boundary
//
// It dials a fresh connection, and that is the requirement. Note the interaction with the breaker's
// backoff (SPEC 076): if the pool is emptied by a boundary while the new-transport backoff window is
// armed - which is the state a path that was dying as the device slept can leave - the fresh dial
// waits out the remainder of the window, bounded by xmuxBackoffCap. That is deliberate: the window
// exists to damp a dial storm, a boundary is not a reason to bypass it, and the wait is bounded at
// three seconds.
func (m *xmuxManager) RetireSuspect() {
	m.access.Lock()
	clients := m.clients
	m.clients = nil
	// The boundary starts a new pool generation. Any connection opened after this point is what a new
	// stream may use; everything still alive from before it is old-generation, including the busy
	// connections this call leaves draining.
	m.generation++
	idle := 0
	draining := 0
	for _, client := range clients {
		if client.getOpenUsage() == 0 {
			idle++
			continue
		}
		client.draining.Store(true)
		m.detached = append(m.detached, client)
		draining++
	}
	m.pruneDetachedLocked()
	m.access.Unlock()
	for _, client := range clients {
		client.close()
	}
	m.logf("xmux: reuse boundary, closed %d idle connection(s) and draining %d busy one(s)", idle, draining)
}

// newClientLocked opens a connection and rolls its per-connection limits. Each
// range is rolled ONCE, here — not per request.
//
// Caller must hold m.access.
func (m *xmuxManager) newClientLocked() *xmuxClient {
	client := &xmuxClient{
		conn:       m.newConn(),
		manager:    m,
		leftUsage:  -1,
		generation: m.generation,
	}
	if x := m.config.cMaxReuseTimes.rand(); x > 0 {
		client.leftUsage = x - 1
	}
	client.leftRequests = maxInt32
	if x := m.config.hMaxRequestTimes.rand(); x > 0 {
		client.leftRequests = int32(x)
	}
	if x := m.config.hMaxReusableSecs.rand(); x > 0 {
		client.unreusableAt = timeNow().Add(time.Duration(x) * time.Second)
	}
	m.clients = append(m.clients, client)
	m.opened.Add(1)
	m.logf("xmux: opened connection (pool=%d, left_usage=%d, left_requests=%d, generation=%d)",
		len(m.clients), client.leftUsage, atomic.LoadInt32(&client.leftRequests), client.generation)
	return client
}

// evictCause reports why a pooled connection must be retired, or "" if it may
// still be used.
func (c *xmuxClient) evictCause() string {
	switch {
	case c.conn.IsClosed():
		return "closed"
	// lx: REUSE BOUNDARY — the connection predates the last sleep and must not
	// carry new streams. Eviction here is the DRAIN: the pool stops handing it
	// out, and close() defers the teardown until the last stream on it leaves,
	// so the stream in flight loses nothing. Checked before the breaker and the
	// configured limits so that a boundary is reported as what it is.
	case c.draining.Load():
		return "draining"
	// lx: SPEC 076 — the breaker tripped on this connection: it failed
	// xmuxBreakerThreshold streams in a row and must not carry new ones.
	case c.failing.Load():
		return "failing"
	case c.leftUsage == 0:
		return "reuse"
	case atomic.LoadInt32(&c.leftRequests) <= 0:
		return "requests"
	case !c.unreusableAt.IsZero() && timeNow().After(c.unreusableAt):
		return "expired"
	}
	return ""
}

// getContext returns a connection for a new stream, waiting out the breaker's
// new-transport backoff window when opening one is required (lx: SPEC 076). A
// pooled connection is returned without waiting; only the open-a-transport
// path is throttled, so the dial storm the breaker exists for is damped while
// healthy reuse stays untouched.
func (m *xmuxManager) getContext(ctx context.Context) (*xmuxClient, error) {
	for {
		client, wait := m.get()
		if client != nil {
			return client, nil
		}
		// An empty result with no wait means the pool is closed, not that the breaker is backing
		// off. Waiting here would spin until the caller's deadline and turn a lifecycle error into
		// a timeout.
		if wait == 0 {
			return nil, E.New("v2ray-xhttp: connection pool is closed")
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// discardRetired removes a client whose reservation was refused because it was retired between the
// pool's selection and the reservation. The close paths detach their clients first, but a close that
// left the object in the list must not make DialContext select the same dead client forever.
func (m *xmuxManager) discardRetired(client *xmuxClient) {
	m.access.Lock()
	for index, current := range m.clients {
		if current == client {
			m.clients = append(m.clients[:index], m.clients[index+1:]...)
			break
		}
	}
	m.access.Unlock()
}

// get returns a connection for a new stream, opening one when the pool cannot
// serve it — unless the pool is empty inside the breaker's backoff window, in
// which case it returns (nil, remaining wait) and the caller must wait and
// retry (lx: SPEC 076; getContext does exactly that). The caller must reserve the returned client's
// usage with acquireUsage (production DialContext does so before dialling) and release that slot
// exactly once through the release handle.
func (m *xmuxManager) get() (*xmuxClient, time.Duration) {
	m.access.Lock()
	defer m.access.Unlock()

	// A closed pool opens nothing. getContext turns the empty result into an error, so a dial that
	// lost the race with Close fails instead of creating a connection nobody owns.
	if m.closed {
		return nil, 0
	}

	var evicted []*xmuxClient
	for i := 0; i < len(m.clients); {
		client := m.clients[i]
		if cause := client.evictCause(); cause != "" {
			m.clients = append(m.clients[:i], m.clients[i+1:]...)
			evicted = append(evicted, client)
			m.logf("xmux: evicted connection (cause=%s, pool=%d)", cause, len(m.clients))
		} else {
			i++
		}
	}
	// Deferred close: an evicted connection with live streams stays up until they
	// finish, so rotation never cuts traffic mid-flight. A busy eviction is the same "detached but
	// not yet released" state a boundary leaves behind, so it joins the same inventory.
	for _, client := range evicted {
		if client.getOpenUsage() > 0 {
			m.detached = append(m.detached, client)
		}
		client.close()
	}
	m.pruneDetachedLocked()

	if len(m.clients) == 0 {
		// lx: SPEC 076 — the only backoff-gated branch: an empty pool during the
		// window means the breaker just retired everything, and opening straight
		// away is what the dial storm is made of. The non-empty branches below
		// stay ungated: they only run when a live connection exists, and stalling
		// healthy dials over trailing errors would be worse than one extra conn.
		if m.backoffArmed.Load() {
			if wait := m.blockedUntil.Sub(timeNow()); wait > 0 {
				return nil, wait
			}
		}
		return m.newClientLocked(), 0
	}
	if m.connections > 0 && len(m.clients) < m.connections {
		return m.newClientLocked(), 0
	}

	candidates := m.clients
	if m.concurrency > 0 {
		candidates = make([]*xmuxClient, 0, len(m.clients))
		for _, client := range m.clients {
			if client.getOpenUsage() < m.concurrency {
				candidates = append(candidates, client)
			}
		}
	}
	if len(candidates) == 0 {
		return m.newClientLocked(), 0
	}

	client := candidates[randIntn(len(candidates))]
	if client.leftUsage > 0 {
		client.leftUsage--
	}
	return client, 0
}

const maxInt32 = int32(^uint32(0) >> 1)

// Defaults mirror Xray-core (infra/conf/transport_method.go, commit 18e2839,
// XTLS/Xray-core#6376): a config that says nothing about xmux should still
// produce an Xray-shaped connection pattern. Xray bounds the pool at three
// connections rather than one connection per stream (the former
// max_concurrency 1 default): a burst of parallel TLS connections to one server
// is what TSPU cuts, and three is the count reported to pass (issue #32).
var (
	defaultXmuxMaxConnections   = intRange{3, 3}
	defaultXmuxHMaxRequestTimes = intRange{600, 900}
	defaultXmuxHMaxReusableSecs = intRange{1800, 3000}
)

// normalizeXmux resolves the XMUX option set, mirroring Xray's all-or-nothing
// rule (infra/conf/transport_method.go, `if c.Xmux == (XmuxConfig{})`): the
// defaults above apply when the section is absent OR entirely empty — Xray's
// Xmux is a value field, so an empty object and a missing one are the same
// thing there, and subscription configs routinely ship the full xmux object
// with empty strings. A section with ANY field set takes every field as
// written: empty ranges stay zero (= unlimited), because per-field defaults
// would rotate connections where an Xray client would not.
func normalizeXmux(options *option.V2RayXHTTPXmuxOptions) (xmuxConfig, error) {
	if options == nil || *options == (option.V2RayXHTTPXmuxOptions{}) {
		return xmuxConfig{
			maxConnections:   defaultXmuxMaxConnections,
			hMaxRequestTimes: defaultXmuxHMaxRequestTimes,
			hMaxReusableSecs: defaultXmuxHMaxReusableSecs,
		}, nil
	}
	var (
		config xmuxConfig
		err    error
	)
	config.maxConcurrency, err = parseRangeOr(string(options.MaxConcurrency), "xmux.max_concurrency", intRange{})
	if err != nil {
		return xmuxConfig{}, err
	}
	config.maxConnections, err = parseRangeOr(string(options.MaxConnections), "xmux.max_connections", intRange{})
	if err != nil {
		return xmuxConfig{}, err
	}
	// Mutually exclusive, matching the reference: one bounds streams per
	// connection, the other bounds connections, and honouring both at once has no
	// coherent meaning.
	if config.maxConnections.max > 0 && config.maxConcurrency.max > 0 {
		return xmuxConfig{}, E.New("v2ray-xhttp: xmux.max_connections cannot be specified together with xmux.max_concurrency")
	}
	config.cMaxReuseTimes, err = parseRangeOr(string(options.CMaxReuseTimes), "xmux.c_max_reuse_times", intRange{})
	if err != nil {
		return xmuxConfig{}, err
	}
	config.hMaxRequestTimes, err = parseRangeOr(string(options.HMaxRequestTimes), "xmux.h_max_request_times", intRange{})
	if err != nil {
		return xmuxConfig{}, err
	}
	config.hMaxReusableSecs, err = parseRangeOr(string(options.HMaxReusableSecs), "xmux.h_max_reusable_secs", intRange{})
	if err != nil {
		return xmuxConfig{}, err
	}
	// Zero means "transport default"; negative disables keep-alive pings.
	config.keepAlivePeriod = time.Duration(options.HKeepAlivePeriod) * time.Second
	return config, nil
}
