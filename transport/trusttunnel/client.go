package trusttunnel

import (
	"context"
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
	QUIC     *QUICOptions
	Username string
	Password string
	// AppName is the application string sent in the user agent and in every UDP
	// frame; "unknown" when empty.
	AppName string
	// HealthCheck enables CONNECT _check before a dial on a session that has
	// been silent for longer than 30 s. It is never sent on a timer.
	HealthCheck bool
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
	tlsConfig := options.TLSConfig.Clone()
	if nextProtos := tlsConfig.NextProtos(); len(nextProtos) == 0 {
		tlsConfig.SetNextProtos([]string{protocol})
	} else if !common.Contains(nextProtos, protocol) {
		return nil, E.New("trusttunnel: TLS ALPN must contain ", protocol)
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
	}
	client.dialSession = client.connectSession
	return client, nil
}

// connectSession opens one session with the configured protocol. It is the
// constructor a pending offer runs, under the offer's context.
func (c *Client) connectSession(ctx context.Context) (sessionConn, error) {
	switch c.protocol {
	case ProtocolHTTP3:
		return c.connectQUIC(ctx)
	default:
		return c.connectHTTP2(ctx)
	}
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
		offerCtx, cancel := context.WithCancelCause(c.ctx)
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
	dialCtx, done := pending.trace.sessionOpen(traceCtx, c.protocol, c.server)
	c.logger.DebugContext(traceCtx, "trusttunnel: opening ", c.protocol, " session to ", c.server)
	conn, err := c.dialSession(dialCtx)
	done(err)
	pending.cancel(nil)
	var session *clientSession
	if err == nil {
		session = newClientSession(c.ctx, conn)
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
	c.logger.DebugContext(traceCtx, "trusttunnel: ", c.protocol, " session to ", c.server, " opened")
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
	retiring  atomic.Bool
	drainOnce sync.Once
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
