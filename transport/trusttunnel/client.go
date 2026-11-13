package trusttunnel

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	aTLS "github.com/sagernet/sing/common/tls"
)

const (
	ProtocolHTTP2 = "h2"
	ProtocolHTTP3 = "h3"
)

const (
	// DefaultStaleWindow is how long a session may stay silent before the next
	// CONNECT on it is treated as a probe, when no idle timeout is set.
	DefaultStaleWindow = 5 * time.Minute

	// staleConnectBudget bounds a CONNECT on a stale session, so that a session
	// whose path died while idle (NAT expiry, sleep) costs seconds, not the
	// caller's whole deadline, before it is rebuilt.
	staleConnectBudget = 3 * time.Second

	// healthCheckThrottle is the silence after which a dial sends _check first;
	// it is also the minimum distance between two checks.
	healthCheckThrottle = 30 * time.Second

	// healthCheckTimeout is the response deadline of _check.
	healthCheckTimeout = 7 * time.Second

	// h3FallbackBudget bounds ONE HTTP/3 session attempt when quic_fallback_to_h2 is enabled, so a
	// UDP path that swallows QUIC pays seconds before HTTP/2 starts instead of the QUIC idle
	// timeout. The figure matches staleConnectBudget for the same reason: it covers a working
	// handshake several times over (4-5 round trips at 300-600 ms) while staying far below what a
	// black hole would cost. The caller's own deadline still wins when it is shorter.
	h3FallbackBudget = 3 * time.Second

	// h3FallbackCooldown is how long a failed HTTP/3 attempt keeps NEW sessions off UDP. It is a
	// provisional verdict, not a permanent one: it matches healthCheckThrottle's scale because both
	// answer "how long may silence stand for absence", and it expires by the clock so the next real
	// demand probes HTTP/3 again without any timer or periodic dialer.
	h3FallbackCooldown = 30 * time.Second

	defaultAppName = "unknown"
)

// ErrQUICNotIncluded is returned for HTTP/3 in a build without with_quic.
var ErrQUICNotIncluded = E.New("trusttunnel: HTTP/3 requires the with_quic build tag")

// QUICOptions configures the HTTP/3 transport.
type QUICOptions struct {
	// CongestionControl is "bbr" (default), "cubic" or "new_reno" ("reno" is
	// accepted as an alias).
	CongestionControl string
}

type ClientOptions struct {
	Context context.Context
	Logger  logger.ContextLogger
	// Dialer opens the TCP connection for HTTP/2 and the UDP socket for HTTP/3.
	Dialer N.Dialer
	// QUICDialHook, when set, observes every attempt to open the HTTP/3
	// session: the UDP dial, the conn before the handshake, the handshake
	// result. It may ask for another attempt.
	QUICDialHook QUICDialHook
	Server       M.Socksaddr
	// TLSConfig is the client TLS configuration. Its ALPN, when set, must contain
	// the protocol in use; when empty it is set to it. HTTP/3 needs a
	// configuration that yields a standard *tls.Config.
	TLSConfig aTLS.Config
	// Protocol is ProtocolHTTP2 (default) or ProtocolHTTP3.
	Protocol string
	// QUICFallback makes one HTTP/3 session creation fall back to HTTP/2 when the HTTP/3 attempt
	// fails on its transport or exhausts its own bounded budget. It requires ProtocolHTTP3; every
	// CONNECT still runs on the session that was published, so authentication, TLS verification and
	// the target verdict are unchanged by the fallback.
	QUICFallback bool
	QUIC         *QUICOptions
	Username     string
	Password     string
	// AppName is the application string sent in the user agent and in every UDP
	// frame; "unknown" when empty.
	AppName string
	// HealthCheck enables CONNECT _check before a dial on a session that has
	// been silent for longer than 30 s. It is never sent on a timer.
	HealthCheck bool
	// AdmitCreation, when non-nil, bounds one shared session creation. It runs
	// once before the dial and returns the context the dial runs with, plus a
	// finish called with the dial result. The returned context carries the
	// caller's values (including any acquisition marker of a nested seam); a
	// nil hook admits everything and is the path of every process without a
	// creation budget.
	AdmitCreation func(ctx context.Context) (context.Context, func(error), error)
	// IdleTimeout closes the session locally after this long without activity
	// and without open streams; zero keeps it until the server or the network
	// ends it.
	IdleTimeout time.Duration
}

// QUICDialHook observes the attempts to open the HTTP/3 session.
type QUICDialHook interface {
	// Begin starts an attempt; the UDP socket is dialled with the attempt's
	// Context.
	Begin(ctx context.Context) QUICDialAttempt
}

// QUICDialAttempt is one attempt to open the HTTP/3 session.
type QUICDialAttempt interface {
	// Context is the context the UDP dial runs with.
	Context() context.Context
	// Dialed is called with the UDP conn before the QUIC handshake starts.
	Dialed(conn net.Conn)
	// Done is called with the handshake result; redo asks for another attempt
	// right away.
	Done(err error) (redo bool)
}

// Client is a TrustTunnel client: one lazily opened session to the server,
// carrying every CONNECT stream.
type Client struct {
	ctx            context.Context
	logger         logger.ContextLogger
	dialer         N.Dialer
	server         M.Socksaddr
	tlsConfig      aTLS.Config
	protocol       string
	congestion     string
	authorization  string
	appName        string
	userAgent      string
	userAgentUDP   string
	userAgentCheck string
	healthCheck    bool
	idleTimeout    time.Duration
	staleWindow    time.Duration
	quicDialHook   QUICDialHook
	admitCreation  func(ctx context.Context) (context.Context, func(error), error)

	// quicFallback enables the HTTP/3 -> HTTP/2 recovery inside connectSession; tlsConfigH2 is the
	// same TLS configuration with the ALPN of the HTTP/2 leg, because one config cannot legitimately
	// offer the other leg's protocol (and mutating the shared config would race both legs).
	quicFallback bool
	tlsConfigH2  aTLS.Config
	// h3CordonUntil is the provisional HTTP/3 verdict per client (= one node) and per network
	// generation: Reset clears it because the outbound calls Reset on a network change, and a verdict
	// about UDP reachability belongs to the network it was observed on.
	h3CordonUntil atomic.Int64
	// h3Budget and h3Cooldown are the constants above, as fields so tests do not wait them out.
	h3Budget   time.Duration
	h3Cooldown time.Duration
	// now is the clock the cooldown reads, replaceable in tests.
	now func() time.Time
	// connectH3 and connectH2 are the two legs of one creation; tests replace them to hold an attempt
	// open or fail it without a handshake. Production always points them at connectQUIC/connectHTTP2.
	connectH3 func(ctx context.Context) (sessionConn, error)
	connectH2 func(ctx context.Context) (sessionConn, error)

	access  sync.Mutex
	session *clientSession
	// pending is the shared session creation in flight, or nil. It is set and
	// cleared under access; the offer goroutine owns the result and publishes it
	// under the same lock, so the client is the one that accepts a late success
	// or closes it exactly once.
	pending *clientOffer
	closed  bool
	// dialSession is the constructor a pending offer runs. NewClient points it
	// at connectSession; tests replace it to hold a dial open and decide its
	// result without a real handshake. Production never reads it from outside
	// this package.
	dialSession func(ctx context.Context) (sessionConn, error)
	// residency is the session-identity ledger (audit Q15). See residency.go.
	residency clientResidency
}

// clientOffer is one shared session creation in flight. Every demand that
// arrives while it runs waits on the same result instead of dialing a second
// session; each waiter keeps its own cancellation, so a short deadline stops
// this caller's wait without cancelling a dial that other callers (or a later
// request) can still use.
type clientOffer struct {
	done      chan struct{}
	session   *clientSession
	err       error
	discarded bool
	cause     error
	cancel    context.CancelCauseFunc
	trace     *ClientTrace
	traceCtx  context.Context
}

// wait waits for the shared result or for THIS waiter's own cancellation. Both
// can be ready at once; either answer is truthful - a caller that cancelled
// while the offer succeeded has no claim on the session, and the client keeps it.
func (o *clientOffer) wait(ctx context.Context) (*clientSession, error) {
	select {
	case <-o.done:
		return o.session, o.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func NewClient(options ClientOptions) (*Client, error) {
	if options.Dialer == nil {
		return nil, E.New("trusttunnel: missing dialer")
	}
	if !options.Server.IsValid() {
		return nil, E.New("trusttunnel: missing server address")
	}
	if options.TLSConfig == nil {
		return nil, E.New("trusttunnel: TLS is required")
	}
	if options.Username == "" {
		return nil, E.New("trusttunnel: missing username")
	}
	appName := options.AppName
	if appName == "" {
		appName = defaultAppName
	}
	if len(appName) > MaxAppNameSize {
		return nil, E.New("trusttunnel: application name longer than ", MaxAppNameSize, " bytes")
	}
	protocol := options.Protocol
	if protocol == "" {
		protocol = ProtocolHTTP2
	}
	var congestion string
	switch protocol {
	case ProtocolHTTP2:
	case ProtocolHTTP3:
		if !quicIncluded {
			return nil, ErrQUICNotIncluded
		}
		if options.QUIC != nil {
			congestion = options.QUIC.CongestionControl
		}
		err := validateCongestionControl(congestion)
		if err != nil {
			return nil, err
		}
	default:
		return nil, E.New("trusttunnel: unknown protocol: ", protocol)
	}
	if options.QUICFallback && protocol != ProtocolHTTP3 {
		return nil, E.New("trusttunnel: quic_fallback_to_h2 requires the HTTP/3 protocol")
	}
	tlsConfig := options.TLSConfig.Clone()
	if nextProtos := tlsConfig.NextProtos(); len(nextProtos) == 0 {
		if options.QUICFallback {
			tlsConfig.SetNextProtos([]string{ProtocolHTTP3, ProtocolHTTP2})
		} else {
			tlsConfig.SetNextProtos([]string{protocol})
		}
	} else if !common.Contains(nextProtos, protocol) {
		return nil, E.New("trusttunnel: TLS ALPN must contain ", protocol)
	}
	var tlsConfigH2 aTLS.Config
	if options.QUICFallback {
		// The HTTP/2 leg is a real HTTP/2 connection: it must offer h2 and nothing else. Sharing the
		// HTTP/3 leg's config would offer h3 as well (the server may pick it and the ALPN check below
		// would fail), and mutating it per attempt would race the two legs.
		if nextProtos := tlsConfig.NextProtos(); !common.Contains(nextProtos, ProtocolHTTP2) {
			return nil, E.New("trusttunnel: TLS ALPN must contain ", ProtocolHTTP2, " for quic_fallback_to_h2")
		}
		tlsConfigH2 = tlsConfig.Clone()
		tlsConfigH2.SetNextProtos([]string{ProtocolHTTP2})
	}
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	contextLogger := options.Logger
	if contextLogger == nil {
		contextLogger = logger.NOP()
	}
	staleWindow := options.IdleTimeout
	if staleWindow <= 0 {
		staleWindow = DefaultStaleWindow
	}
	client := &Client{
		ctx:            ctx,
		logger:         contextLogger,
		dialer:         options.Dialer,
		quicDialHook:   options.QUICDialHook,
		server:         options.Server,
		tlsConfig:      tlsConfig,
		protocol:       protocol,
		congestion:     congestion,
		authorization:  BasicAuthorization(options.Username, options.Password),
		appName:        appName,
		userAgent:      UserAgent(appName),
		userAgentUDP:   UserAgentUDP(),
		userAgentCheck: UserAgentCheck(),
		healthCheck:    options.HealthCheck,
		idleTimeout:    max(options.IdleTimeout, 0),
		staleWindow:    staleWindow,
		admitCreation:  options.AdmitCreation,
		quicFallback:   options.QUICFallback,
		tlsConfigH2:    tlsConfigH2,
		h3Budget:       h3FallbackBudget,
		h3Cooldown:     h3FallbackCooldown,
		now:            time.Now,
	}
	client.dialSession = client.connectSession
	client.connectH3 = client.connectQUIC
	client.connectH2 = client.connectHTTP2
	return client, nil
}

// connectSession opens one session: the configured protocol, or - with quic_fallback_to_h2 - a
// bounded HTTP/3 attempt followed by an HTTP/2 session when that attempt is genuinely unusable.
//
// # Why this boundary
//
// The session creation is the only place where a fallback cannot repeat business work: nothing has
// been sent to the target yet, so choosing another transport here can never produce a second CONNECT
// to the same destination. Authentication, TLS verification and the server's answer to CONNECT stay
// where they were, on whichever session was published; a fallback therefore cannot turn "the server
// refused me" into "UDP is blocked".
//
// # What a failure means here
//
// Every error from the HTTP/3 leg at this point is a transport-level establishment failure - the UDP
// dial, the QUIC handshake, the negotiated ALPN - because no HTTP request exists yet. Those are the
// only errors this fallback acts on. A creation whose own context ended is not a statement about the
// node at all, so it neither falls back nor arms the cooldown.
func (c *Client) connectSession(ctx context.Context) (sessionConn, error) {
	if !c.quicFallback || c.protocol != ProtocolHTTP3 {
		if c.protocol == ProtocolHTTP3 {
			return c.connectH3(ctx)
		}
		return c.connectH2(ctx)
	}
	if c.h3Cordoned() {
		c.logger.DebugContext(c.ctx, "trusttunnel: HTTP/3 is cordoned for ", c.server, " after a failed attempt, opening HTTP/2")
		return c.connectFallbackHTTP2(ctx)
	}
	attemptCtx, cancel := c.h3AttemptContext(ctx)
	if attemptCtx == nil {
		// The caller's deadline has already passed; there is no attempt to make and nothing to learn.
		return nil, ctx.Err()
	}
	conn, err := c.connectH3(attemptCtx)
	cancel()
	if err == nil {
		if ctx.Err() != nil {
			// The creation was aborted while the attempt was in flight (Close, or the Reset a network
			// change performs). The connection belongs to the old generation: it is closed here rather
			// than published, and it must NOT clear a verdict the new generation owns - a late success
			// from the old network is not evidence about the new one.
			conn.close()
			return nil, ctx.Err()
		}
		c.clearH3Cordon()
		return conn, nil
	}
	if ctx.Err() != nil {
		return nil, err
	}
	if !fallbackEligible(err) {
		return nil, err
	}
	c.cordonH3()
	c.logger.DebugContext(c.ctx, "trusttunnel: HTTP/3 attempt to ", c.server, " failed (", err, "), opening HTTP/2 for this session")
	fallbackConn, fallbackErr := c.connectFallbackHTTP2(ctx)
	if fallbackErr != nil {
		// Keep both causes: the HTTP/3 failure explains why HTTP/2 was tried, and the HTTP/2 error is
		// what the caller's session actually failed with.
		return nil, E.Errors(err, fallbackErr)
	}
	return fallbackConn, nil
}

// connectFallbackHTTP2 opens the HTTP/2 leg of a fallback and marks it. The marker is what lets a
// later demand retire this session once the cooldown has expired, whether the session was opened by
// the attempt that failed or by a later one that skipped HTTP/3 because of the cooldown.
func (c *Client) connectFallbackHTTP2(ctx context.Context) (sessionConn, error) {
	conn, err := c.connectH2(ctx)
	if err != nil {
		return nil, err
	}
	return fallbackHTTP2Session{conn: conn}, nil
}

// h3AttemptContext bounds one HTTP/3 attempt. The budget is this client's own cap and the caller's
// remaining time is used when it is shorter - never extended, so a short caller deadline stays short.
// A nil context means the caller's deadline had already passed.
func (c *Client) h3AttemptContext(ctx context.Context) (context.Context, context.CancelFunc) {
	budget := c.h3Budget
	if budget <= 0 {
		budget = h3FallbackBudget
	}
	if deadline, hasDeadline := ctx.Deadline(); hasDeadline {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, nil
		}
		budget = min(budget, remaining)
	}
	return context.WithTimeout(ctx, budget)
}

// fallbackEligible reports whether a failed HTTP/3 attempt is evidence that this node's UDP path is
// unusable right now.
//
// Eligible: the UDP dial, the QUIC handshake, a transport reset, the attempt's own budget expiring
// while the creation is alive, and a negotiated ALPN that is not h3 (a server that answers h2 on the
// same endpoint is exactly the case the fallback exists for).
//
// Not eligible: a cancelled attempt, a build without HTTP/3, and every TLS verdict - certificate
// verification, unknown authority, hostname mismatch, invalid or expired certificate, and a TLS alert
// from the peer. Those are statements about the peer's identity or its configuration, and the HTTP/2
// leg presents the same certificate to the same name, so retrying there repeats the refusal; remembering
// one as "HTTP/3 is down" would keep a node off UDP for a reason HTTP/2 does not share.
func fallbackEligible(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, ErrQUICNotIncluded) {
		return false
	}
	var certificateVerificationError *tls.CertificateVerificationError
	if errors.As(err, &certificateVerificationError) {
		return false
	}
	var unknownAuthorityError x509.UnknownAuthorityError
	if errors.As(err, &unknownAuthorityError) {
		return false
	}
	var hostnameError x509.HostnameError
	if errors.As(err, &hostnameError) {
		return false
	}
	var certificateInvalidError x509.CertificateInvalidError
	if errors.As(err, &certificateInvalidError) {
		return false
	}
	var alertError tls.AlertError
	if errors.As(err, &alertError) {
		return false
	}
	return true
}

// clock is the client's time source: the cooldown is the only thing that reads it, and tests replace
// it so a cooldown can expire without waiting.
func (c *Client) clock() time.Time {
	if c.now == nil {
		return time.Now()
	}
	return c.now()
}

// h3Cordoned reports whether the provisional HTTP/3 verdict is still open.
func (c *Client) h3Cordoned() bool {
	until := c.h3CordonUntil.Load()
	if until == 0 {
		return false
	}
	return c.clock().Before(time.Unix(0, until))
}

// cordonH3 arms the provisional verdict for h3Cooldown. It carries no session state: the next
// creation after it expires probes HTTP/3 again, driven by demand, never by a timer.
func (c *Client) cordonH3() {
	c.h3CordonUntil.Store(c.clock().Add(c.h3Cooldown).UnixNano())
}

func (c *Client) clearH3Cordon() {
	c.h3CordonUntil.Store(0)
}

// recoverableFallbackSession reports whether a live session should be retired so that the demand just
// arriving can try HTTP/3 again: the session came from the fallback, its carrier is healthy, and the
// cooldown has expired. Retiring (not closing) it keeps every stream that is already on it, which is
// the same boundary RetireSuspect uses.
func (c *Client) recoverableFallbackSession(session *clientSession) bool {
	return c.quicFallback && session.fallbackH2 && !c.h3Cordoned()
}

// fallbackHTTP2Session marks a session that the HTTP/2 leg of a fallback produced. The marker is what
// lets a later demand retire that session to retry HTTP/3 once the cooldown has expired; a plain
// HTTP/2 configuration must never be treated that way.
type fallbackHTTP2Session struct {
	conn sessionConn
}

func (s fallbackHTTP2Session) roundTrip(ctx context.Context, authority string, header http.Header, withBody bool) (*rawStream, error) {
	return s.conn.roundTrip(ctx, authority, header, withBody)
}

func (s fallbackHTTP2Session) alive() bool { return s.conn.alive() }

func (s fallbackHTTP2Session) close() error { return s.conn.close() }

func usedFallback(conn sessionConn) bool {
	_, isFallback := conn.(fallbackHTTP2Session)
	return isFallback
}

// DialContext opens CONNECT host:port and returns after the server's answer: a
// connection on 200, a *StatusError otherwise. A domain destination is sent
// unresolved; the server resolves it.
func (c *Client) DialContext(ctx context.Context, destination M.Socksaddr) (net.Conn, error) {
	if !destination.IsValid() || destination.Port == 0 {
		return nil, E.New("trusttunnel: invalid destination: ", destination)
	}
	session, stream, err := c.openStream(ctx, ContextClientTrace(ctx), destination.String(), c.userAgent, true)
	if err != nil {
		return nil, err
	}
	return newStreamConn(session, stream, c.server, destination), nil
}

// ListenPacket opens one _udp2 stream. Datagrams on it carry IP addresses only;
// the caller resolves domains.
func (c *Client) ListenPacket(ctx context.Context) (*PacketConn, error) {
	session, stream, err := c.openStream(ctx, ContextClientTrace(ctx), AuthorityUDP, c.userAgentUDP, true)
	if err != nil {
		return nil, err
	}
	return newPacketConn(newStreamConn(session, stream, c.server, c.server), c.appName, c.server), nil
}

// Check sends CONNECT _check on the session, opening it if needed. A failed
// check tears the session down.
func (c *Client) Check(ctx context.Context) error {
	trace := ContextClientTrace(ctx)
	for attempt := 0; ; attempt++ {
		session, _, err := c.getSession(ctx, trace)
		if err != nil {
			return err
		}
		err = c.check(ctx, trace, session)
		if errors.Is(err, errSessionRetired) && attempt < 3 {
			continue
		}
		return err
	}
}

// Reset aborts the current session and any creation in flight, but keeps the
// client usable: the next request opens a fresh session. Meant for network
// changes: QUIC migration is off on the official endpoint, and an HTTP/2
// session on the old network is dead anyway.
func (c *Client) Reset() {
	c.access.Lock()
	session := c.session
	c.session = nil
	pending := c.discardPendingLocked()
	// The verdict belongs to the network it was observed on, and the outbound calls Reset on a network
	// change. Clearing it under the same lock as the discarded creation keeps a creation that starts
	// right after this Reset from reading the old network's verdict.
	c.clearH3Cordon()
	c.access.Unlock()
	if pending != nil && pending.cancel != nil {
		pending.cancel(net.ErrClosed)
	}
	if session != nil {
		session.close()
	}
}

// Close ends the client: the session and any creation in flight are discarded.
// It does not wait for an in-flight dial; the offer goroutine closes a late
// success exactly once (see completeOffer).
func (c *Client) Close() error {
	c.access.Lock()
	if c.closed {
		c.access.Unlock()
		return nil
	}
	c.closed = true
	session := c.session
	c.session = nil
	pending := c.discardPendingLocked()
	c.access.Unlock()
	if pending != nil && pending.cancel != nil {
		pending.cancel(net.ErrClosed)
	}
	if session != nil {
		session.close()
	}
	return nil
}

// discardPendingLocked marks the creation in flight as discarded and hands it
// back, so the caller can cancel its dial; the offer goroutine still owns the
// result and closes a late success. The caller holds access.
func (c *Client) discardPendingLocked() *clientOffer {
	pending := c.pending
	if pending == nil {
		return nil
	}
	pending.discarded = true
	if pending.cause == nil {
		pending.cause = net.ErrClosed
	}
	c.pending = nil
	return pending
}

// CloseIdleConnections drops the session when no stream is using it. It is the
// TRIM action of the resource contract: it never dials and it cannot terminate
// a stream that is carrying traffic.
func (c *Client) CloseIdleConnections() {
	c.access.Lock()
	session := c.session
	if session == nil || session.streams.Load() > 0 {
		c.access.Unlock()
		return
	}
	c.session = nil
	c.access.Unlock()
	session.close()
}

// RetireSuspect is the reuse boundary: no new stream attaches to the session
// held now, while streams already on it finish. A session with no stream is
// closed here; one with streams closes itself when the last stream returns.
func (c *Client) RetireSuspect() {
	c.access.Lock()
	session := c.session
	if session != nil {
		c.session = nil
		session.retiring.Store(true)
		c.residency.noteRetired()
	}
	c.access.Unlock()
	if session != nil {
		session.drainIfUnused()
	}
}

func (c *Client) openStream(ctx context.Context, trace *ClientTrace, authority string, userAgent string, withBody bool) (*clientSession, *rawStream, error) {
	for attempt := 0; ; attempt++ {
		session, fresh, err := c.getSession(ctx, trace)
		if err != nil {
			return nil, nil, err
		}
		if session.retiring.Load() {
			// RetireSuspect removed the session between getSession and here;
			// the next demand must open a fresh one.
			if attempt < 3 {
				continue
			}
			return nil, nil, errSessionRetired
		}
		if !fresh && c.healthCheck && session.checkDue() {
			err = c.check(ctx, trace, session)
			if err != nil && errors.Is(err, errSessionRetired) && attempt < 3 {
				continue
			}
			if err != nil {
				if ctx.Err() != nil || !retryable(err) {
					return nil, nil, err
				}
				c.logger.DebugContext(ctx, "trusttunnel: health check of session to ", c.server, " failed, reconnecting: ", err)
				session, fresh, err = c.getSession(ctx, trace)
				if err != nil {
					return nil, nil, err
				}
			}
		}
		stream, err := c.roundTrip(ctx, trace, session, authority, userAgent, withBody, !fresh)
		if errors.Is(err, errSessionRetired) && attempt < 3 {
			continue
		}
		if err == nil || fresh || ctx.Err() != nil || !retryable(err) {
			return session, stream, err
		}
		c.logger.DebugContext(ctx, "trusttunnel: CONNECT on session to ", c.server, " failed, reconnecting: ", err)
		session, _, err = c.getSession(ctx, trace)
		if err != nil {
			return nil, nil, err
		}
		stream, err = c.roundTrip(ctx, trace, session, authority, userAgent, withBody, false)
		return session, stream, err
	}
}

// errSessionRetired reports a CONNECT that lost the race with RetireSuspect: the
// session is draining, so the caller opens a fresh one instead of attaching.
var errSessionRetired = E.New("trusttunnel: session retired")

// roundTrip sends one CONNECT on the session and checks the answer. A transport
// failure or an authentication refusal tears the session down; a CONNECT that
// may land on a stale session is bounded by staleConnectBudget.
func (c *Client) roundTrip(ctx context.Context, trace *ClientTrace, session *clientSession, authority string, userAgent string, withBody bool, mayBeStale bool) (_ *rawStream, err error) {
	if session.retiring.Load() {
		return nil, errSessionRetired
	}
	var status int
	done := trace.connect(ctx, authority)
	defer func() { done(status, err) }()
	attemptCtx := ctx
	if mayBeStale && session.silentFor() > c.staleWindow {
		var cancel context.CancelFunc
		attemptCtx, cancel = context.WithTimeout(ctx, staleConnectBudget)
		defer cancel()
	}
	stream, err := session.conn.roundTrip(attemptCtx, authority, c.requestHeader(userAgent), withBody)
	if err != nil {
		if ctx.Err() == nil {
			c.teardown(session)
		}
		if IsAuthenticationRequired(err) {
			return nil, E.Cause1(ErrAuthenticationRequired, err)
		}
		return nil, E.Cause(err, "trusttunnel: CONNECT ", authority)
	}
	status = stream.response.StatusCode
	if stream.response.StatusCode != http.StatusOK {
		stream.close()
		statusError := NewStatusError(stream.response.StatusCode, stream.response.Header)
		if statusError.refusesSession() {
			c.logger.DebugContext(ctx, "trusttunnel: server ", c.server, " refused the credentials (", statusError.StatusCode, "), closing session")
			c.teardown(session)
		} else {
			session.markReceived()
		}
		return nil, statusError
	}
	session.markReceived()
	session.streams.Add(1)
	return stream, nil
}

func (c *Client) check(ctx context.Context, trace *ClientTrace, session *clientSession) (err error) {
	if session.retiring.Load() {
		return errSessionRetired
	}
	var status int
	done := trace.connect(ctx, AuthorityCheck)
	defer func() { done(status, err) }()
	checkCtx, cancel := context.WithTimeout(ctx, healthCheckTimeout)
	defer cancel()
	session.checked.Store(time.Now().UnixNano())
	stream, err := session.conn.roundTrip(checkCtx, AuthorityCheck, c.requestHeader(c.userAgentCheck), false)
	if err != nil {
		if ctx.Err() == nil {
			c.teardown(session)
		}
		if IsAuthenticationRequired(err) {
			return E.Cause1(ErrAuthenticationRequired, err)
		}
		return E.Cause(err, "trusttunnel: health check")
	}
	status = stream.response.StatusCode
	stream.close()
	if stream.response.StatusCode != http.StatusOK {
		c.teardown(session)
		return NewStatusError(stream.response.StatusCode, stream.response.Header)
	}
	session.markReceived()
	return nil
}

func (c *Client) requestHeader(userAgent string) http.Header {
	return http.Header{
		headerProxyAuthorization: []string{c.authorization},
		headerUserAgent:          []string{userAgent},
	}
}

// retryable reports whether a failed CONNECT may be repeated on a new session:
// any transport failure, but not an answer of the server.
func retryable(err error) bool {
	var statusError *StatusError
	return !errors.As(err, &statusError) && !IsAuthenticationRequired(err)
}

// getSession returns the live session, joining the shared creation in flight or
// starting one when there is none. fresh reports that this call started the
// creation that produced the session.
//
// The waiter's ctx only ever bounds this caller's wait: it cannot cancel a dial
// that other callers (or a later demand) can still use. The dial runs under the
// client's lifetime, which also makes the client the owner of a late success.
func (c *Client) getSession(ctx context.Context, trace *ClientTrace) (*clientSession, bool, error) {
	for {
		c.access.Lock()
		if c.closed {
			c.access.Unlock()
			return nil, false, net.ErrClosed
		}
		if session := c.session; session != nil {
			if session.conn.alive() {
				if c.recoverableFallbackSession(session) {
					// The HTTP/3 cooldown is over on a session the fallback produced. Retire it - the
					// streams already on it finish - and let THIS demand create a fresh session, which
					// prefers HTTP/3 again. Demand-driven: no timer, no probe traffic of its own.
					c.session = nil
					session.retiring.Store(true)
					c.residency.noteRetired()
					c.access.Unlock()
					c.logger.DebugContext(ctx, "trusttunnel: HTTP/2 fallback session to ", c.server, " retired to retry HTTP/3")
					session.drainIfUnused()
					continue
				}
				session.markActivity()
				c.access.Unlock()
				trace.sessionReused(ctx, c.protocol, c.server, session.opened)
				return session, false, nil
			}
			c.session = nil
			c.access.Unlock()
			session.close()
			c.logger.DebugContext(ctx, "trusttunnel: session to ", c.server, " is closed")
			continue
		}
		if pending := c.pending; pending != nil {
			c.access.Unlock()
			session, err := pending.wait(ctx)
			if err != nil {
				return nil, false, err
			}
			trace.sessionReused(ctx, c.protocol, c.server, session.opened)
			return session, false, nil
		}
		// The offer keeps the caller's context VALUES but not its cancellation:
		// an acquisition marker (resourceguard.HeldPermit) or a traffic class
		// set by an outer seam must reach the dial and its TLS handshake, while
		// this caller stopping its wait must not cancel a creation that other
		// waiters (or a later demand) can still use. Close and Reset cancel the
		// offer explicitly.
		offerCtx, cancel := context.WithCancelCause(context.WithoutCancel(ctx))
		pending := &clientOffer{
			done:     make(chan struct{}),
			cancel:   cancel,
			trace:    trace,
			traceCtx: ctx,
		}
		c.pending = pending
		c.access.Unlock()
		go c.completeOffer(pending, offerCtx)
		session, err := pending.wait(ctx)
		if err != nil {
			return nil, false, err
		}
		return session, true, nil
	}
}

// completeOffer runs one shared creation and publishes its result. The client
// is the owner: a success is published unless the offer was discarded (Close
// or Reset while the dial ran), in which case the late connection is closed
// here, exactly once, instead of being published after the client ended.
func (c *Client) completeOffer(pending *clientOffer, offerCtx context.Context) {
	traceCtx := pending.traceCtx
	dialCtx, done := pending.trace.sessionOpen(offerCtx, c.protocol, c.server)
	c.logger.DebugContext(traceCtx, "trusttunnel: opening ", c.protocol, " session to ", c.server)
	finish := func(error) {}
	if c.admitCreation != nil {
		admittedCtx, admittedFinish, admitErr := c.admitCreation(dialCtx)
		if admitErr != nil {
			done(admitErr)
			pending.cancel(nil)
			c.publishOffer(pending, nil, admitErr)
			return
		}
		if admittedCtx != nil {
			dialCtx = admittedCtx
		}
		if admittedFinish != nil {
			finish = admittedFinish
		}
	}
	conn, err := c.dialSession(dialCtx)
	finish(err)
	done(err)
	pending.cancel(nil)
	c.publishOffer(pending, conn, err)
}

// publishOffer publishes one creation result under the client lock. A result
// that arrived after the offer was discarded is never published: the client
// closes it here, exactly once.
func (c *Client) publishOffer(pending *clientOffer, conn sessionConn, err error) {
	if err == nil && conn == nil {
		err = E.New("trusttunnel: session creation returned no connection")
	}
	var session *clientSession
	if err == nil {
		session = newClientSession(c.ctx, conn)
		session.fallbackH2 = usedFallback(conn)
		session.onClose = c.residency.noteReleased
	}
	c.access.Lock()
	discard := pending.discarded
	switch {
	case discard:
		pending.err = pending.cause
		if pending.err == nil {
			pending.err = net.ErrClosed
		}
	case err != nil:
		pending.err = E.Cause(err, "trusttunnel: open session to ", c.server)
	default:
		pending.session = session
		c.session = session
	}
	if c.pending == pending {
		c.pending = nil
	}
	close(pending.done)
	c.access.Unlock()
	if discard {
		if conn != nil {
			conn.close()
		}
		return
	}
	if session == nil {
		return
	}
	c.logger.DebugContext(pending.traceCtx, "trusttunnel: ", c.protocol, " session to ", c.server, " opened")
	if c.idleTimeout > 0 {
		go c.idleWatcher(session)
	}
}

func (c *Client) teardown(session *clientSession) {
	c.access.Lock()
	if c.session == session {
		c.session = nil
	}
	c.access.Unlock()
	session.close()
}

// idleWatcher closes the session after idleTimeout without activity and without
// open streams. It lives as long as the session and sends nothing.
func (c *Client) idleWatcher(session *clientSession) {
	interval := max(c.idleTimeout/4, time.Second)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-session.ctx.Done():
			return
		case <-ticker.C:
			if session.streams.Load() > 0 {
				continue
			}
			idleFor := time.Duration(time.Now().UnixNano() - session.activity.Load())
			if idleFor >= c.idleTimeout {
				c.logger.DebugContext(c.ctx, "trusttunnel: closing session to ", c.server, " after ", idleFor.Round(time.Second), " idle")
				c.teardown(session)
				return
			}
		}
	}
}

// sessionConn is one HTTP/2 or HTTP/3 connection to the server.
type sessionConn interface {
	// roundTrip sends CONNECT authority and returns once the response headers
	// arrived, whatever the status. ctx bounds the wait for a stream slot and for
	// the answer, not the life of the stream.
	roundTrip(ctx context.Context, authority string, header http.Header, withBody bool) (*rawStream, error)
	alive() bool
	close() error
}

// rawStream is an answered CONNECT stream.
type rawStream struct {
	response  *http.Response
	reader    io.Reader
	writer    io.Writer
	closeFunc func()
	deadline  deadlineSetter
	wrapError func(error) error
	closeOnce sync.Once
}

func (s *rawStream) close() {
	s.closeOnce.Do(s.closeFunc)
}

type deadlineSetter interface {
	SetReadDeadline(t time.Time) error
	SetWriteDeadline(t time.Time) error
}

type clientSession struct {
	conn      sessionConn
	ctx       context.Context
	cancel    context.CancelFunc
	activity  atomic.Int64
	received  atomic.Int64
	checked   atomic.Int64
	streams   atomic.Int64
	opened    time.Time
	closeOnce sync.Once
	// retiring marks a session removed by RetireSuspect: no new stream attaches
	// to it, and it closes when its last stream returns.
	retiring atomic.Bool
	// fallbackH2 marks a session the HTTP/2 leg of a fallback produced. A later demand retires it
	// once the HTTP/3 cooldown has expired, so the recovery probe is driven by real traffic.
	fallbackH2 bool
	drainOnce  sync.Once
	// onClose reports the completed release to the client's residency ledger.
	onClose func(wasDraining bool)
}

// drainIfUnused closes a retiring session once nothing is carried on it. The
// caller that took the session off the client calls it at the boundary; every
// stream close calls it too, and the once guard makes only the last of them the
// close.
func (s *clientSession) drainIfUnused() {
	if !s.retiring.Load() || s.streams.Load() > 0 {
		return
	}
	s.drainOnce.Do(func() { s.close() })
}

func newClientSession(ctx context.Context, conn sessionConn) *clientSession {
	sessionCtx, cancel := context.WithCancel(ctx)
	session := &clientSession{
		conn:   conn,
		ctx:    sessionCtx,
		cancel: cancel,
		opened: time.Now(),
	}
	session.markReceived()
	return session
}

func (s *clientSession) markActivity() {
	s.activity.Store(time.Now().UnixNano())
}

func (s *clientSession) markReceived() {
	now := time.Now().UnixNano()
	s.activity.Store(now)
	s.received.Store(now)
}

// silentFor is the time since the server last proved the session alive: data
// received or a request answered.
func (s *clientSession) silentFor() time.Duration {
	return time.Duration(time.Now().UnixNano() - s.received.Load())
}

// checkDue reports whether a health check may be sent: the session has been
// silent for the throttle and no check was sent within it.
func (s *clientSession) checkDue() bool {
	last := max(s.received.Load(), s.checked.Load())
	return time.Duration(time.Now().UnixNano()-last) > healthCheckThrottle
}

func (s *clientSession) close() {
	s.closeOnce.Do(func() {
		s.cancel()
		s.conn.close()
		if s.onClose != nil {
			s.onClose(s.retiring.Load())
		}
	})
}

// streamConn is a CONNECT stream as a net.Conn.
type streamConn struct {
	session    *clientSession
	stream     *rawStream
	localAddr  M.Socksaddr
	remoteAddr M.Socksaddr
	closeOnce  sync.Once
}

func newStreamConn(session *clientSession, stream *rawStream, localAddr M.Socksaddr, remoteAddr M.Socksaddr) *streamConn {
	return &streamConn{
		session:    session,
		stream:     stream,
		localAddr:  localAddr,
		remoteAddr: remoteAddr,
	}
}

func (c *streamConn) Read(p []byte) (int, error) {
	n, err := c.stream.reader.Read(p)
	if n > 0 {
		c.session.markReceived()
	}
	return n, c.stream.wrapError(err)
}

func (c *streamConn) Write(p []byte) (int, error) {
	n, err := c.stream.writer.Write(p)
	c.session.markActivity()
	return n, c.stream.wrapError(err)
}

func (c *streamConn) Close() error {
	c.closeOnce.Do(func() {
		c.stream.close()
		c.session.streams.Add(-1)
		c.session.drainIfUnused()
		c.session.markActivity()
	})
	return nil
}

func (c *streamConn) LocalAddr() net.Addr {
	return c.localAddr
}

func (c *streamConn) RemoteAddr() net.Addr {
	return c.remoteAddr
}

func (c *streamConn) SetDeadline(t time.Time) error {
	if c.stream.deadline == nil {
		return os.ErrInvalid
	}
	err := c.stream.deadline.SetReadDeadline(t)
	if err != nil {
		return err
	}
	return c.stream.deadline.SetWriteDeadline(t)
}

func (c *streamConn) SetReadDeadline(t time.Time) error {
	if c.stream.deadline == nil {
		return os.ErrInvalid
	}
	return c.stream.deadline.SetReadDeadline(t)
}

func (c *streamConn) SetWriteDeadline(t time.Time) error {
	if c.stream.deadline == nil {
		return os.ErrInvalid
	}
	return c.stream.deadline.SetWriteDeadline(t)
}

func (c *streamConn) NeedAdditionalReadDeadline() bool {
	return c.stream.deadline == nil
}
