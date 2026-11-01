package v2rayxhttp

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	C "github.com/sagernet/sing-box/constant"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
)

// packetUpPostTimeout bounds one packet-up upload POST (lx: SPEC 072). Posts
// ride the conn-scoped context (see dialPacketUp), not the dial context, so
// without an own bound a wedged pooled connection would block a Write — and
// the WG send path behind it — forever. One post is one HTTP exchange; the
// probe budget C.TCPTimeout is the same ceiling the WG bind dial uses.
// Variable, not const: tests shrink it.
var packetUpPostTimeout = C.TCPTimeout

// transportContext is the transport-lifetime context conn-scoped request
// contexts derive from (lx: SPEC 072). NewClient always sets c.ctx; the
// Background fallback keeps literal-constructed clients (tests) valid.
func (c *Client) transportContext() context.Context {
	if c.ctx != nil {
		return c.ctx
	}
	return context.Background()
}

// applyGRPCHeader sets the streamed-body Content-Type that Xray sends on
// stream-one/stream-up requests (FillStreamRequest in
// transport/internet/splithttp/config.go). Reverse proxies and CDNs in front of
// an XHTTP server key response streaming on a gRPC content type; without it the
// download side is buffered and the dial hangs until timeout. Opt out with
// no_grpc_header, matching Xray's NoGRPCHeader.
func (c *Client) applyGRPCHeader(request *http.Request) {
	if c.noGRPCHeader || request.Body == nil {
		return
	}
	request.Header.Set("Content-Type", "application/grpc")
}

// lx: SPEC 077 — the dial-context contract of the streamed modes.
//
// DialContext hands a stream-one/stream-up conn up only once the upload stream
// is RAISED: the HTTP layer has adopted the request body — called Read on the
// upload pipe, which means the pooled connection is up, the stream is open and
// writes will be drained. The download response is NOT awaited: an Xray server
// withholds it until the first uplink bytes (SPEC 061), so waiting for it here
// would deadlock the dial. Until the raise the dial context bounds everything
// (the pooled connection's TCP/TLS dial, the stream slot, the header write);
// after DialContext returns the dial context has no effect on the conn — the
// net.Dialer contract every other transport keeps. Field case: the DNS
// transport pool (dns/transport/conn_pool.go, upstream) cancels its dial
// context the moment dial returns, and the former SPEC 050 guard — armed on
// that context until the download response, which stream-one only sends after
// the first write — tore every such conn down with "context canceled" before
// the first query left the pipe.
//
// adoptedBody is the request body handed to the HTTP layer: the first Read CALL
// closes `adopted`. The call, not its return — x/net http2 issues that Read
// right after the request headers and it blocks on the pipe until the caller's
// first Write, so waiting for it to return would revive the 061 deadlock in a
// new form.
type adoptedBody struct {
	*io.PipeReader
	once    sync.Once
	adopted chan struct{}
}

func newAdoptedBody(reader *io.PipeReader) *adoptedBody {
	return &adoptedBody{PipeReader: reader, adopted: make(chan struct{})}
}

func (b *adoptedBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.adopted) })
	return b.PipeReader.Read(p)
}

// awaitRaise parks the dial until the upload body is adopted, the raise settles
// (`created` closes on a bound download body AND on a failed raise — raiseErr
// tells which), an upload-side failure is reported, or the dial context ends.
// A raise that lands together with the cancel still wins: the conn is usable,
// and failing it would only make the caller redial the same node.
func awaitRaise(ctx context.Context, adopted, created <-chan struct{}, uploadFailed <-chan error, raiseErr func() error) error {
	select {
	case <-adopted:
		return nil
	case <-created:
		return raiseErr()
	case err := <-uploadFailed:
		return err
	case <-ctx.Done():
		select {
		case <-adopted:
			return nil
		case <-created:
			return raiseErr()
		default:
			return ctx.Err()
		}
	}
}

// abortRaise tears down a dial that did not produce a usable conn: the upload
// pipe is broken from the READ half so the parked http2 body Read (and any
// writer) sees the cause rather than a bare ErrClosedPipe, the conn context is
// cancelled so the pending RoundTrip cannot leak, and the pooled connection is
// released. Every step is idempotent — on a failed raise, fail() has already
// run them — and `created` is deliberately not touched: the RoundTrip
// goroutine is its only owner.
func abortRaise(upload *io.PipeReader, cancel context.CancelFunc, release *xmuxRelease, err error) {
	upload.CloseWithError(err)
	cancel()
	release.release()
}

// dialStreamOne opens a single bidirectional HTTP/2 stream: the request body is
// the upload direction, the response body is the download direction. With Reality
// it is also what "auto" resolves to (matching Xray).
//
// Unlike stream-up/packet-up, the request targets the BARE path with NO sessionId:
// Xray's splithttp server keys the stream-one (bidirectional) branch on an empty
// sessionId. Sending "<path>/<sessionId>" instead routes the server into the
// stream-down branch, which never pairs with a stream-up POST, so the response
// body carries non-VLESS bytes and the VLESS layer fails with "unknown version".
func (c *Client) dialStreamOne(ctx context.Context, sessionID string, xmuxClient *xmuxClient, release *xmuxRelease) (net.Conn, error) {
	_ = sessionID // intentionally unused: stream-one sends no sessionId on the wire
	pipeReader, pipeWriter := io.Pipe()
	body := newAdoptedBody(pipeReader)
	// lx: SPEC 072 — the request rides a conn-scoped context under the TRANSPORT
	// lifetime, not the dial context. http2 binds the whole stream to the request
	// context, and a dial context is allowed to carry a deadline that outlives
	// the dial (the WG bind dials under C.TCPTimeout since SPEC 071): on the dial
	// context the deadline would abort the raised stream when it fires, cycling
	// every healthy detour conn at 15 s. The dial context bounds the raise only,
	// by parking the dial itself (lx: SPEC 077); conn teardown cancels connCtx
	// via Close/fail.
	connCtx, connCancel := context.WithCancel(c.transportContext())
	// stream-one carries a body, so it uses the configured upload method (default
	// POST); the empty sessionID keeps the request on the bare path.
	request, err := c.newRequest(connCtx, c.meta.uplinkHTTPMethod, "", "", body)
	if err != nil {
		connCancel()
		return nil, err
	}
	c.applyGRPCHeader(request)

	conn := newStreamConn(pipeReader, pipeWriter, c.serverAddr, release)
	conn.cancel = connCancel
	go func() {
		response, err := xmuxClient.roundTrip(request)
		if err != nil {
			conn.fail(err)
			return
		}
		if response.StatusCode != http.StatusOK {
			response.Body.Close()
			conn.fail(E.New("v2ray-xhttp: unexpected status: ", response.Status))
			return
		}
		conn.setupReader(response.Body, nil)
	}()
	// lx: SPEC 077 — the conn leaves the dial raised or not at all.
	if err := awaitRaise(ctx, body.adopted, conn.created, nil, func() error { return conn.readerErr }); err != nil {
		abortRaise(pipeReader, connCancel, release, err)
		return nil, err
	}
	return conn, nil
}

// dialStreamUp opens a streamed POST for the upload direction and a separate GET
// whose response body is the download direction.
//
// Like packet-up, the download response is awaited asynchronously: an Xray
// server holds the stream-down response until the paired stream-up request has
// delivered bytes, so blocking on it here deadlocks the dial and the fronting
// proxy answers 504. See the note on dialPacketUp. lx: SPEC 002.
func (c *Client) dialStreamUp(ctx context.Context, sessionID string, xmuxClient *xmuxClient, release *xmuxRelease) (net.Conn, error) {
	// lx: SPEC 072 — conn-scoped request context; see dialStreamOne.
	connCtx, connCancel := context.WithCancel(c.transportContext())
	// Download: GET response body (no seq — stream mode).
	downReq, err := c.newRequest(connCtx, http.MethodGet, sessionID, "", nil)
	if err != nil {
		connCancel()
		return nil, err
	}

	// Upload: streamed body request using the configured upload method.
	pipeReader, pipeWriter := io.Pipe()
	body := newAdoptedBody(pipeReader)
	upReq, err := c.newRequest(connCtx, c.meta.uplinkHTTPMethod, sessionID, "", body)
	if err != nil {
		connCancel()
		return nil, err
	}
	c.applyGRPCHeader(upReq)
	conn := newSplitConn(pipeReader, pipeWriter, c.serverAddr, release)
	conn.cancel = connCancel
	go func() {
		downResp, err := xmuxClient.roundTrip(downReq)
		if err != nil {
			conn.fail(E.Cause(err, "open download"))
			return
		}
		if downResp.StatusCode != http.StatusOK {
			downResp.Body.Close()
			conn.fail(E.New("v2ray-xhttp: unexpected download status: ", downResp.Status))
			return
		}
		conn.setupReader(downResp.Body, nil)
	}()
	// lx: SPEC 077 — an upload raise that fails before adoption has no reader to
	// report to (the pipe is broken by uploadFailed, but nobody holds the conn
	// yet), so the dial listens for it directly.
	uploadFailed := make(chan error, 1)
	// The upload response is the server's verdict on the stream-up POST. A
	// non-200 means the server refused the stream: the download GET may still be
	// pending, so waiting for it would park the dial until the caller's context
	// and leak that request. Break the upload pipe with the status (so a writer
	// that arrives after the raise sees it too) and let the dial fail now.
	//
	// A 200 response body, by contrast, is drained and closed: it carries no
	// protocol bytes and closing it early can reset the paired download stream
	// before the first response byte is read.
	go func() {
		upResp, err := xmuxClient.roundTrip(upReq)
		if err != nil {
			conn.uploadFailed(err)
			uploadFailed <- err
			return
		}
		if upResp.StatusCode != http.StatusOK {
			if upResp.Body != nil {
				_ = upResp.Body.Close()
			}
			statusErr := E.New("v2ray-xhttp: unexpected upload status: ", upResp.Status)
			conn.uploadFailed(statusErr)
			// The stream is refused by the server, so its download half cannot
			// become usable either. Cancel the conn-scoped context so a pending
			// download RoundTrip ends and a reader blocked on an already-bound
			// body stops, instead of waiting for the caller to Close.
			if conn.cancel != nil {
				conn.cancel()
			}
			uploadFailed <- statusErr
			return
		}
		if upResp.Body != nil {
			drainAndClose(upResp.Body)
		}
	}()
	// lx: SPEC 077 — the raise this dial waits for is the UPLOAD body's
	// adoption; the download GET is not awaited (SPEC 061).
	if err := awaitRaise(ctx, body.adopted, conn.created, uploadFailed, func() error { return conn.readerErr }); err != nil {
		abortRaise(pipeReader, connCancel, release, err)
		return nil, err
	}
	return conn, nil
}

// dialPacketUp opens a GET download stream and sends uploads as sequential POST
// packets, batched by packetUplink (lx: XHTTP_OUTPERFORM — one POST carries a
// queue of writes, not one write).
//
// The download RoundTrip runs in a goroutine and the conn is handed up
// immediately, WITHOUT waiting for the response headers. Waiting for them
// deadlocks against an Xray server: it only has downlink bytes to send once the
// session has received an uplink packet, so it withholds the response until the
// first upload arrives — while we withheld the first upload until the response
// arrived. Neither side moves and the reverse proxy in front (nginx/CDN) kills
// the request, surfacing as "504 Gateway Timeout" after its upstream timeout.
// Wire-reproduced against a VK-CDN → nginx → Xray path, where the reference
// client (sing-box-extended) works: its OpenStream likewise returns as soon as
// the connection is established (httptrace GotConn) and processes the response
// asynchronously. lx: SPEC 002.
func (c *Client) dialPacketUp(ctx context.Context, sessionID string, xmuxClient *xmuxClient, release *xmuxRelease) (net.Conn, error) {
	// lx: SPEC 077 for packet-up — the dial context bounds CONSTRUCTION, and
	// construction here is entirely synchronous. There is no upload pipe to
	// adopt (every Write is its own bounded POST) and the download response may
	// not arrive until the first uplink (the deadlock note above), so unlike
	// stream-one/stream-up there is no network event this dial can wait for.
	// What remains of the net.Dialer contract is the part the caller can
	// observe: a dial context cancelled before the dial returns must fail the
	// dial, not hand back a connection the caller is no longer waiting for.
	// Before this check the function ignored ctx outright (`_ = ctx`), so an
	// already-reusable XMUX connection made a pre-cancelled dial return a conn
	// as if it had succeeded. The slot DialContext took is returned by its error
	// path exactly once (release.release is sync.Once-guarded), so this failure
	// leaves the pooled connection's openUsage at zero.
	//
	// The residual race is net.Dialer's own, not something this check can close:
	// a cancel landing between ctx.Err() and the return is indistinguishable
	// from one landing an instruction after it, and net.Dialer documents the
	// same ("once successfully connected, any expiration of the context will not
	// affect the connection"). A conn that wins that race is not "merely looking
	// successful": the pending download GET and every upload POST ride connCtx
	// under the TRANSPORT lifetime (lx: SPEC 072), not the dial context, so it
	// stays usable and the caller's Close releases the slot.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// lx: SPEC 072 — conn-scoped request context (see dialStreamOne); the
	// download GET and every upload POST ride it, and Close/fail cancel it.
	connCtx, connCancel := context.WithCancel(c.transportContext())
	// Download stream: GET with the session id but no seq (downlink).
	downReq, err := c.newRequest(connCtx, http.MethodGet, sessionID, "", nil)
	if err != nil {
		connCancel()
		return nil, err
	}
	conn := newPacketConn(connCtx, c, sessionID, c.serverAddr, release)
	conn.cancel = connCancel
	go func() {
		downResp, err := xmuxClient.roundTrip(downReq)
		if err != nil {
			conn.fail(E.Cause(err, "open download"))
			return
		}
		if downResp.StatusCode != http.StatusOK {
			downResp.Body.Close()
			conn.fail(E.New("v2ray-xhttp: unexpected download status: ", downResp.Status))
			return
		}
		conn.setupReader(downResp.Body, nil)
	}()
	return conn, nil
}

// lx:begin 050 deadline-support (SPECS/TASKS/050)
//
// A streamed body is an io.Pipe: a Write blocks until the HTTP layer reads it.
// Since SPEC 077 the dial does not return before that reader exists, but a node
// can stop draining after the raise (dead peer behind a live front, exhausted
// h2 flow-control window) and park a Write just the same. io.Pipe has no
// deadlines of its own, but CloseWithError releases a pending Write instantly —
// so a deadline is a timer that closes the pipe with os.ErrDeadlineExceeded.
// Without this the blocked goroutine is unkillable and outlives box shutdown;
// see the task for the field evidence.
//
// The read side is a late-bound response body, so it cannot be closed before it
// exists. Read therefore waits on `dead` alongside `created`, and an expired
// read deadline closes `dead` rather than touching the reader.
// The pipe is broken from the READ half on purpose: io.Pipe hands a writer only
// ErrClosedPipe for an error set via PipeWriter.CloseWithError (writeCloseError
// prefers rerr, and werr suppresses it), so closing the write half would lose
// os.ErrDeadlineExceeded. Closing the read half sets rerr, which the blocked
// Write does surface.
type writeDeadline struct {
	access sync.Mutex
	reader *io.PipeReader
	timer  *time.Timer
}

// set arms (or re-arms) the write deadline. A zero time clears it; a time in the
// past fires immediately, matching net.Conn semantics.
func (d *writeDeadline) set(t time.Time) error {
	d.access.Lock()
	defer d.access.Unlock()
	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
	if t.IsZero() || d.reader == nil {
		return nil
	}
	if delay := time.Until(t); delay <= 0 {
		d.reader.CloseWithError(os.ErrDeadlineExceeded)
	} else {
		d.timer = time.AfterFunc(delay, func() {
			d.reader.CloseWithError(os.ErrDeadlineExceeded)
		})
	}
	return nil
}

// stop releases the timer; called from Close so an armed deadline cannot outlive
// the conn.
func (d *writeDeadline) stop() {
	d.access.Lock()
	defer d.access.Unlock()
	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
}

// readDeadline unblocks a pending Read by closing `dead`. Read observes it both
// while waiting for the late-bound reader and while blocked in reader.Read —
// the latter needs the reader closed too, which the owner does via onExpire.
type readDeadline struct {
	access   sync.Mutex
	dead     chan struct{}
	expired  bool
	timer    *time.Timer
	onExpire func()
}

func newReadDeadline(onExpire func()) *readDeadline {
	return &readDeadline{dead: make(chan struct{}), onExpire: onExpire}
}

func (d *readDeadline) set(t time.Time) error {
	d.access.Lock()
	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
	if t.IsZero() || d.expired {
		d.access.Unlock()
		return nil
	}
	var fire bool
	if delay := time.Until(t); delay <= 0 {
		fire = d.expireLocked()
	} else {
		d.timer = time.AfterFunc(delay, d.expire)
	}
	d.access.Unlock()
	// lx: SPEC 074 — onExpire runs OUTSIDE the lock. It closes the HTTP/2 response
	// body, which can block (h2 waits on its own machinery); holding `access`
	// across that call deadlocks every other user of this deadline — including
	// quic-go, which calls SetReadDeadline from Transport.Close on the very path
	// that is trying to expire. Observed live: a QUIC handshake over an xhttp hop
	// hung ~20-90 s with three goroutines stacked on this mutex.
	if fire {
		d.runOnExpire()
	}
	return nil
}

func (d *readDeadline) expire() {
	d.access.Lock()
	fire := d.expireLocked()
	d.access.Unlock()
	if fire {
		d.runOnExpire()
	}
}

// expireLocked marks the deadline expired and reports whether the caller now owns
// running onExpire (exactly once). The callback itself must run unlocked.
func (d *readDeadline) expireLocked() bool {
	if d.expired {
		return false
	}
	d.expired = true
	close(d.dead)
	return d.onExpire != nil
}

func (d *readDeadline) runOnExpire() {
	if d.onExpire != nil {
		d.onExpire()
	}
}

// isExpired reports whether the deadline has fired; dead is closed only by
// expireLocked, never by stop, so a stopped deadline reads as not expired.
func (d *readDeadline) isExpired() bool {
	select {
	case <-d.dead:
		return true
	default:
		return false
	}
}

func (d *readDeadline) stop() {
	d.access.Lock()
	defer d.access.Unlock()
	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
}

// lx:end 050 deadline-support

// lx: SPEC 076 — per-conn breaker bookkeeping shared by the three conn kinds.
//
// localClosed marks teardown WE initiated (Close, expired read deadline): our
// own body-close wakes a blocked Read with an http2 "response body closed"
// error, and counting that as a remote failure would trip the breaker on
// perfectly healthy usage. It must be set BEFORE the reader is closed, so the
// woken Read already observes it.
type connBreaker struct {
	xmux        *xmuxRelease
	localClosed atomic.Bool
	readOK      atomic.Bool
}

// noteRead classifies one download-body read result for the pooled
// connection's breaker: the first successful read is the stream's proof of
// life (headers alone prove nothing — the field case behind SPEC 076 had
// 200-raises whose bodies died seconds later), a remote error counts against
// the connection. io.EOF (clean server finish) and local teardown are neutral.
func (b *connBreaker) noteRead(err error) {
	if err == nil {
		if !b.readOK.Swap(true) {
			b.xmux.noteSuccess()
		}
		return
	}
	if err != io.EOF && !b.localClosed.Load() {
		b.xmux.noteFailure()
	}
}

// outboundErr maps a read error for the caller. lx: SPEC 094 — our own
// Body.Close() (Close, expired read deadline) wakes a blocked Read with x/net's
// "http2: response body closed" (or net/http's "read on closed response
// body"); sing's IsClosedOrCanceled does not know those unexported sentinels,
// so the relay logged every normal teardown at ERROR. Under the localClosed
// gate the error becomes net.ErrClosed, or os.ErrDeadlineExceeded when it was
// the read deadline that closed the body (a timeout must stay a net.Error with
// Timeout() true for the callers that distinguish it, SPEC 050). io.EOF is
// left alone even under the gate: a clean server finish racing our Close is
// still a clean finish. Remote errors (StreamError) never see the gate armed
// and pass through untouched.
func (b *connBreaker) outboundErr(err error, deadline *readDeadline) error {
	if err == nil || err == io.EOF || !b.localClosed.Load() {
		return err
	}
	if deadline != nil && deadline.isExpired() {
		return os.ErrDeadlineExceeded
	}
	return net.ErrClosed
}

// streamConn is a net.Conn whose write side is the upload pipe and whose read
// side is a late-bound response body (download). It mirrors the late-binding
// pattern of v2rayhttp.HTTP2Conn but is self-contained here.
type streamConn struct {
	// xmux releases this stream's pooled connection when the conn closes.
	xmux *xmuxRelease
	// breaker classifies read results and local teardown (lx: SPEC 076).
	breaker    connBreaker
	writer     *io.PipeWriter
	reader     io.ReadCloser
	created    chan struct{}
	readerErr  error
	serverAddr M.Socksaddr
	closeOnce  sync.Once
	// lx: 050 — deadlines; without them a blocked Write/Read is unkillable.
	writeDeadline writeDeadline
	readDeadline  *readDeadline
	// cancel kills the conn-scoped request context (lx: SPEC 072); set by the
	// dial, invoked by Close and fail.
	cancel context.CancelFunc
}

func newStreamConn(reader *io.PipeReader, writer *io.PipeWriter, serverAddr M.Socksaddr, xmux *xmuxRelease) *streamConn {
	conn := &streamConn{
		xmux:       xmux,
		breaker:    connBreaker{xmux: xmux},
		writer:     writer,
		created:    make(chan struct{}),
		serverAddr: serverAddr,
	}
	conn.writeDeadline.reader = reader
	// lx: 050 — an expired read deadline must also close an already-bound reader,
	// otherwise Read stays blocked inside reader.Read.
	conn.readDeadline = newReadDeadline(func() {
		conn.breaker.localClosed.Store(true) // lx: SPEC 076 — our teardown, not a remote failure
		select {
		case <-conn.created:
			if conn.reader != nil {
				conn.reader.Close()
			}
		default:
		}
	})
	return conn
}

func (c *streamConn) setupReader(reader io.ReadCloser, err error) {
	c.reader = reader
	c.readerErr = hideTransportError(err) // lx: SPEC 082, 104
	close(c.created)
}

// fail marks the raise failed (lx: SPEC 072): it binds the error for readers
// AND breaks the upload pipe from the read half, so a Write blocked on (or
// arriving at) a pipe nobody will ever read surfaces the failure instead of
// hanging forever. Closing `created` alone is not enough: nothing wakes a
// parked pipe Write, and the field dump behind SPEC 072 is a VLESS handshake
// Write that hung 38 minutes exactly that way. Since SPEC 077 a raise that
// fails before adoption also fails the dial itself (awaitRaise reads the error
// bound here), but a raise can still fail after the conn was handed up — a
// pooled connection answering the upload late, a non-200 that arrives after
// the first write. The conn context and the pooled connection are released
// here too, because the error path has no guaranteed Close: sing-vmess early
// dials return the conn together with the write error and callers drop it.
func (c *streamConn) fail(err error) {
	c.setupReader(nil, err)
	c.writeDeadline.reader.CloseWithError(err)
	if c.cancel != nil {
		c.cancel()
	}
	c.xmux.release()
}

func (c *streamConn) Read(b []byte) (int, error) {
	// Always synchronise on created before touching reader/readerErr: the RoundTrip
	// goroutine writes them before close(created), so the receive is the happens-
	// before edge. The old `if c.reader == nil` fast path read reader unsynchronised
	// (a data race, -race flagged it) for no gain — a receive on an already-closed
	// channel is effectively free (SPEC 022 #7).
	//
	// lx: 050 — also wait on the read deadline: until RoundTrip binds the reader
	// there is nothing to close, so an expired deadline can only be observed here.
	select {
	case <-c.created:
	case <-c.readDeadline.dead:
		return 0, os.ErrDeadlineExceeded
	}
	if c.readerErr != nil {
		return 0, c.readerErr
	}
	n, err := c.reader.Read(b)
	c.breaker.noteRead(err)                                                  // lx: SPEC 076
	return n, c.breaker.outboundErr(hideTransportError(err), c.readDeadline) // lx: SPEC 082, 094, 104 — after noteRead: the breaker classifies the raw error
}

func (c *streamConn) Write(b []byte) (int, error) {
	return c.writer.Write(b)
}

func (c *streamConn) Close() error {
	c.closeOnce.Do(func() {
		c.breaker.localClosed.Store(true) // lx: SPEC 076 — before closing the reader
		// lx: 050 — drop armed timers first so neither can fire on a closed conn.
		c.writeDeadline.stop()
		c.readDeadline.stop()
		c.writer.Close()
		select {
		case <-c.created:
			if c.reader != nil {
				c.reader.Close()
			}
		default:
		}
		// lx: SPEC 072 — kill the conn-scoped request context so a pending or
		// live RoundTrip cannot outlive the conn.
		if c.cancel != nil {
			c.cancel()
		}
		// lx: 059 — release the pooled connection last, once nothing reads it.
		c.xmux.release()
	})
	return nil
}

func (c *streamConn) LocalAddr() net.Addr  { return M.Socksaddr{} }
func (c *streamConn) RemoteAddr() net.Addr { return c.serverAddr }

// lx: 050 — real deadlines (were os.ErrInvalid): a Write into an unread upload
// pipe is otherwise unkillable and survives box shutdown.
func (c *streamConn) SetDeadline(t time.Time) error {
	if err := c.readDeadline.set(t); err != nil {
		return err
	}
	return c.writeDeadline.set(t)
}

func (c *streamConn) SetReadDeadline(t time.Time) error  { return c.readDeadline.set(t) }
func (c *streamConn) SetWriteDeadline(t time.Time) error { return c.writeDeadline.set(t) }

// NeedAdditionalReadDeadline stays true even though SetReadDeadline now works:
// the read deadline here is one-shot (it closes the late-bound body to break a
// pending Read) and does not restore the conn for a later read, which is what
// net.Conn semantics and deadline.NewConn provide. The escape this task needs is
// on the write side, so keep the wrapper rather than claim semantics we lack.
func (c *streamConn) NeedAdditionalReadDeadline() bool { return true }

// splitConn pairs an already-open download reader with an upload pipe (stream-up
// mode). The download body is ready immediately; the upload POST is driven by
// the caller in a goroutine.
type splitConn struct {
	// xmux releases this stream's pooled connection when the conn closes.
	xmux *xmuxRelease
	// breaker classifies read results and local teardown (lx: SPEC 076).
	breaker    connBreaker
	reader     io.ReadCloser
	created    chan struct{}
	readerErr  error
	writer     *io.PipeWriter
	serverAddr M.Socksaddr
	closeOnce  sync.Once
	// lx: 050 — same unkillable-Write exposure as streamConn. The reader is
	// late-bound (see dialStreamUp), so an expired read deadline must handle
	// both the bound and the not-yet-bound case.
	writeDeadline writeDeadline
	readDeadline  *readDeadline
	// cancel kills the conn-scoped request context (lx: SPEC 072).
	cancel context.CancelFunc
}

func newSplitConn(uploadReader *io.PipeReader, writer *io.PipeWriter, serverAddr M.Socksaddr, xmux *xmuxRelease) *splitConn {
	conn := &splitConn{
		xmux:       xmux,
		breaker:    connBreaker{xmux: xmux},
		created:    make(chan struct{}),
		writer:     writer,
		serverAddr: serverAddr,
	}
	conn.writeDeadline.reader = uploadReader
	conn.readDeadline = newReadDeadline(func() {
		conn.breaker.localClosed.Store(true) // lx: SPEC 076 — our teardown, not a remote failure
		select {
		case <-conn.created:
			if conn.reader != nil {
				conn.reader.Close()
			}
		default:
		}
	})
	return conn
}

// setupReader binds the download body (or its error) and releases blocked Reads.
func (c *splitConn) setupReader(reader io.ReadCloser, err error) {
	c.reader = reader
	c.readerErr = hideTransportError(err) // lx: SPEC 082, 104
	close(c.created)
}

// fail marks the raise failed; see streamConn.fail (lx: SPEC 072). A dead
// download side kills the conn as a whole — VLESS can never read a response —
// so the upload pipe is broken too, freeing any writer parked on it.
func (c *splitConn) fail(err error) {
	c.setupReader(nil, err)
	c.writeDeadline.reader.CloseWithError(err)
	if c.cancel != nil {
		c.cancel()
	}
	c.xmux.release()
}

// uploadFailed breaks the upload pipe from the READ half, so the blocked
// writer surfaces the actual upload error — a write-half CloseWithError hands
// the writer a bare ErrClosedPipe (io.Pipe's writeCloseError prefers rerr and
// suppresses werr; see the writeDeadline note). lx: SPEC 072.
func (c *splitConn) uploadFailed(err error) {
	c.writeDeadline.reader.CloseWithError(err)
}

func (c *splitConn) Read(b []byte) (int, error) {
	// Late-bound reader: synchronise on created, exactly as streamConn.Read does.
	select {
	case <-c.created:
	case <-c.readDeadline.dead:
		return 0, os.ErrDeadlineExceeded
	}
	if c.readerErr != nil {
		return 0, c.readerErr
	}
	n, err := c.reader.Read(b)
	c.breaker.noteRead(err)                                                  // lx: SPEC 076
	return n, c.breaker.outboundErr(hideTransportError(err), c.readDeadline) // lx: SPEC 082, 094, 104 — after noteRead: the breaker classifies the raw error
}
func (c *splitConn) Write(b []byte) (int, error) { return c.writer.Write(b) }

func (c *splitConn) Close() error {
	c.closeOnce.Do(func() {
		c.breaker.localClosed.Store(true) // lx: SPEC 076 — before closing the reader
		// lx: 050 — drop armed timers before closing, as in streamConn.
		c.writeDeadline.stop()
		c.readDeadline.stop()
		c.writer.Close()
		// The reader may not be bound yet (see dialStreamUp); a pending
		// RoundTrip is torn down by the conn-context cancel below.
		select {
		case <-c.created:
			if c.reader != nil {
				c.reader.Close()
			}
		default:
		}
		// lx: SPEC 072 — kill the conn-scoped request context (aborts pending
		// download/upload RoundTrips).
		if c.cancel != nil {
			c.cancel()
		}
		// lx: 059 — release the pooled connection last, once nothing reads it.
		c.xmux.release()
	})
	return nil
}

func (c *splitConn) LocalAddr() net.Addr  { return M.Socksaddr{} }
func (c *splitConn) RemoteAddr() net.Addr { return c.serverAddr }

// lx: 050 — real deadlines (were os.ErrInvalid); see streamConn.
func (c *splitConn) SetDeadline(t time.Time) error {
	if err := c.readDeadline.set(t); err != nil {
		return err
	}
	return c.writeDeadline.set(t)
}

func (c *splitConn) SetReadDeadline(t time.Time) error  { return c.readDeadline.set(t) }
func (c *splitConn) SetWriteDeadline(t time.Time) error { return c.writeDeadline.set(t) }

// NeedAdditionalReadDeadline: see streamConn — the read deadline is one-shot.
func (c *splitConn) NeedAdditionalReadDeadline() bool { return true }

// packetConn implements packet-up: download is a GET response body, each Write
// is delivered as a sequential POST to "<path>/<sessionId>/<seq>".
//
// The reader is late-bound: dialPacketUp hands the conn up before the download
// response exists (see the deadlock note there), so Read waits on `created`
// until the RoundTrip goroutine binds either a body or an error.
type packetConn struct {
	ctx    context.Context
	client *Client
	// xmux is the pooled connection carrying every upload POST of this stream;
	// released when the conn closes.
	xmux *xmuxRelease
	// breaker classifies read results and local teardown (lx: SPEC 076).
	breaker    connBreaker
	sessionID  string
	reader     io.ReadCloser
	created    chan struct{}
	readerErr  error
	serverAddr M.Socksaddr
	access     sync.Mutex
	closed     bool
	closeOnce  sync.Once
	// lx: XHTTP_OUTPERFORM — the uplink queue and its flusher. Writes are
	// buffered there and leave as POSTs that carry a full batch instead of one
	// write each.
	uplink *packetUplink
	// lx: 050 — a late-bound reader makes a read deadline mandatory: until the
	// response arrives there is no reader to close, so a stalled Read could only
	// be released here.
	readDeadline *readDeadline
	// cancel kills the conn-scoped request context (lx: SPEC 072): the pending
	// download RoundTrip and any in-flight upload POST die with the conn.
	cancel context.CancelFunc
}

func newPacketConn(ctx context.Context, client *Client, sessionID string, serverAddr M.Socksaddr, xmux *xmuxRelease) *packetConn {
	conn := &packetConn{
		ctx:        ctx,
		client:     client,
		xmux:       xmux,
		breaker:    connBreaker{xmux: xmux},
		sessionID:  sessionID,
		created:    make(chan struct{}),
		serverAddr: serverAddr,
	}
	conn.readDeadline = newReadDeadline(func() {
		conn.breaker.localClosed.Store(true) // lx: SPEC 076 — our teardown, not a remote failure
		select {
		case <-conn.created:
			if conn.reader != nil {
				conn.reader.Close()
			}
		default:
		}
	})
	conn.uplink = newPacketUplink(conn)
	go conn.uplink.run()
	return conn
}

// setupReader binds the download body (or the error that replaces it) and
// releases readers blocked in Read. Mirrors streamConn.setupReader.
func (c *packetConn) setupReader(reader io.ReadCloser, err error) {
	c.reader = reader
	c.readerErr = hideTransportError(err) // lx: SPEC 082, 104
	close(c.created)
}

// fail marks the download raise failed; see streamConn.fail (lx: SPEC 072).
// Posts are still individual bounded requests rather than one long upload body,
// but the uplink queue is a pipe-shaped thing all the same: a writer parked on a
// full queue has to be released, and cancel makes any in-flight post fail now,
// so a dead session cannot keep posting into the void.
func (c *packetConn) fail(err error) {
	c.setupReader(nil, err)
	// lx: XHTTP_OUTPERFORM — the session is dead: release writers parked on a full
	// queue instead of leaving them to the post timeout, then cancel so anything
	// in flight fails now.
	c.uplink.abort(err)
	if c.cancel != nil {
		c.cancel()
	}
	c.xmux.release()
}

func (c *packetConn) Read(b []byte) (int, error) {
	// Synchronise on created before touching reader/readerErr — the RoundTrip
	// goroutine writes them before close(created), which is the happens-before
	// edge (same contract as streamConn.Read).
	select {
	case <-c.created:
	case <-c.readDeadline.dead:
		return 0, os.ErrDeadlineExceeded
	}
	if c.readerErr != nil {
		return 0, c.readerErr
	}
	n, err := c.reader.Read(b)
	c.breaker.noteRead(err)                                                  // lx: SPEC 076
	return n, c.breaker.outboundErr(hideTransportError(err), c.readDeadline) // lx: SPEC 082, 094, 104 — after noteRead: the breaker classifies the raw error
}

// Write queues b for delivery on the uplink and returns once it is buffered.
//
// lx: XHTTP_OUTPERFORM — this used to be one HTTP POST per write, synchronously:
// Write returned only after its POST's response headers arrived. That made every
// write cost a round trip plus sc_min_posts_interval_ms (default 30 ms) and
// capped a stream at (bytes per write)/30 ms. See packetUplink for the measured
// before/after and for why the reference client does not behave this way.
//
// The buffer is bounded: a writer whose bytes would exceed the bound waits for
// the flusher to drain, so a stalled path still becomes backpressure here rather
// than unbounded memory. Close drains what is queued before it tears the session
// down, so a buffered tail is not lost by a caller that writes and immediately
// closes — which is exactly what a relay does with the last bytes of a request.
func (c *packetConn) Write(b []byte) (int, error) {
	return c.uplink.write(b)
}

// lx: XHTTP_OUTPERFORM — packet-up upload batching.
//
// packet-up turned every Write into its own HTTP POST: build the request, round
// trip, drain the response, repeat. The reference client does not. It hands the
// bytes to an in-process pipe and drains that pipe into posts, so one POST
// carries every byte that has queued up, up to sc_max_each_post_bytes. The
// difference is not cosmetic:
//
//   - sc_min_posts_interval_ms (default 30) is a floor between POST STARTS. At
//     one post per write, that floor caps a stream at (bytes per write)/30 ms.
//   - Every write also paid a full round trip of its own, because Write returned
//     only once the POST had been answered.
//   - A protocol handshake is two writes (header, then first payload), so
//     opening a proxied connection cost two posts, the second one behind the
//     interval.
//
// MEASURED on this repository's host, Xray v26.9.30 as the server, TLS over
// HTTP/2, packet-up, one stream, three repetitions, medians:
//
//	                      before        reference
//	upload                2.12 MB/s     22.86 MB/s
//	connection setup      31.3 /s       586 /s
//	download (unaffected) 137 MB/s      136 MB/s
//
// Download is listed because it is the control: packet-up's downlink is a single
// GET response body and never had this problem, so a change that moved download
// would be measuring something else.
//
// Wire compatibility is unaffected: posts are still sequential, still carry a
// monotonic seq, and the server's reorder buffer is what reassembles them. What
// changes is how many bytes ride in one post.
//
// Failure semantics, stated because buffering changes where they are observed:
// a failed post stops the flusher and is reported by every later Write (the
// bytes it carried are lost with it — a stream cannot pretend otherwise, and
// the reference behaves the same way, interrupting its upload pipe and stopping
// the posts). It does NOT close the session by itself: the download half may be
// perfectly healthy, so teardown stays with the caller, whose Close releases the
// pooled slot exactly once. A post failure is therefore never silent, and never
// leaves a sequence that continues past the hole.
type packetUplink struct {
	conn *packetConn

	access   sync.Mutex
	notEmpty *sync.Cond
	notFull  *sync.Cond
	// pending holds the bytes accepted from Write and not yet handed to a POST.
	// It is a byte queue rather than a list of write-sized slices so that a
	// batch is a subslice of it: no copy is made on the way to the wire.
	pending []byte
	// maxPending bounds the queue. A writer that would exceed it waits, so a
	// stalled path applies backpressure instead of growing memory without limit.
	maxPending int
	closed     bool
	err        error
	done       chan struct{}

	// seq and lastPost are properties of the POST stream, not of one write.
	seq      uint64
	lastPost time.Time
	// lastAppend is when bytes last entered the queue; the flusher uses it to
	// tell "the burst is over" from "more is coming".
	lastAppend time.Time
}

const (
	// packetUpGatherQuiet is how long the queue must go untouched before the
	// flusher stops waiting and posts what it has.
	//
	// It exists because a post's size sets the stream's rate: the anti-burst
	// interval is a floor between post STARTS, so throughput is
	// (bytes per post)/interval. A flusher that posts the instant it wakes sends
	// whatever happened to be queued at that moment, and MEASURED, that costs
	// real throughput: with the window, upload runs 26.1-27.8 MB/s; with the
	// window removed and everything else identical, 23.3 MB/s. The window is
	// what lets a post fill toward sc_max_each_post_bytes before it leaves.
	//
	// The wait ends when the writer stops, so a burst of any length is captured
	// whole, and packetUpGatherCap bounds it so a producer that never pauses
	// cannot starve the post.
	//
	// It is NOT what fixes connection setup — see the seq<=1 exemption in
	// nextPostDelay. A window cannot do that job on this host: the monotonic
	// clock ticks at roughly 500 us, so "idle for 250 us" comes down to which
	// side of a tick the write landed on (measured: a two-write handshake
	// coalesced at gaps of 0/250us/1ms and did not at 100us/500us/2ms).
	packetUpGatherQuiet = 250 * time.Microsecond
	// packetUpGatherCap bounds the wait, so a producer that never pauses cannot
	// starve the post: after this, whatever is queued goes.
	packetUpGatherCap = 2 * time.Millisecond
	// packetUpGatherTick is the polling step of the quiet wait. Polling rather
	// than arming a timer per write keeps the write path free of timers.
	packetUpGatherTick = 50 * time.Microsecond
	// packetUpGatherBypass is the queue depth at which the flusher stops
	// gathering: bulk data is already flowing, and waiting would only delay it.
	packetUpGatherBypass = 64 * 1024
	// packetUpDrainTimeout bounds how long Close waits for queued bytes to leave.
	// A healthy post drains in one round trip; a wedged one is already bounded by
	// packetUpPostTimeout (15 s), which is far too long to hold a relay's Close.
	packetUpDrainTimeout = 3 * time.Second
	// packetUpPendingFloor and packetUpPendingCeiling bound the queue when the
	// configured sc_max_each_post_bytes is unusually small or large.
	packetUpPendingFloor   = 64 * 1024
	packetUpPendingCeiling = 8 * 1024 * 1024
)

func newPacketUplink(conn *packetConn) *packetUplink {
	uplink := &packetUplink{conn: conn, done: make(chan struct{})}
	uplink.notEmpty = sync.NewCond(&uplink.access)
	uplink.notFull = sync.NewCond(&uplink.access)
	// Two posts' worth of bytes: one batch can be in flight while the next
	// accumulates, and a producer faster than the path still blocks instead of
	// buffering without bound.
	bound := conn.client.meta.scMaxEachPostBytes.max * 2
	if bound < packetUpPendingFloor {
		bound = packetUpPendingFloor
	}
	if bound > packetUpPendingCeiling {
		bound = packetUpPendingCeiling
	}
	uplink.maxPending = bound
	return uplink
}

// write appends b to the queue, waiting while the queue is full.
func (u *packetUplink) write(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	u.access.Lock()
	for {
		if u.err != nil {
			err := u.err
			u.access.Unlock()
			return 0, err
		}
		if u.closed {
			u.access.Unlock()
			return 0, net.ErrClosed
		}
		// An empty queue always accepts, so a single write larger than the bound
		// is queued (and split into posts) rather than deadlocking the writer.
		if len(u.pending) == 0 || len(u.pending)+len(b) <= u.maxPending {
			break
		}
		u.notFull.Wait()
	}
	u.pending = append(u.pending, b...)
	u.lastAppend = timeNow()
	u.access.Unlock()
	u.notEmpty.Signal()
	return len(b), nil
}

// gather waits for the writer to pause before the batch is taken: the burst is
// over once the queue has gone untouched for packetUpGatherQuiet, and
// packetUpGatherCap bounds the wait so a producer that never pauses cannot
// starve the post.
func (u *packetUplink) gather() {
	deadline := time.Now().Add(packetUpGatherCap)
	for {
		u.access.Lock()
		idle := time.Since(u.lastAppend)
		size := len(u.pending)
		stalled := u.closed || u.err != nil
		u.access.Unlock()
		if stalled || size == 0 || size >= packetUpGatherBypass {
			return
		}
		if idle >= packetUpGatherQuiet || time.Now().After(deadline) {
			return
		}
		time.Sleep(packetUpGatherTick)
	}
}

// shouldGatherUpload decides whether the flusher should wait for the writer to
// pause before posting the batch it is looking at.
//
// The window exists to fill a post toward sc_max_each_post_bytes, because the
// anti-burst interval makes a post's size the stream's rate: for a queue in the
// middle band, a short wait usually buys a fuller post. Two bands skip it — a
// queue already deep enough that bulk is flowing, and the session's opening pair,
// where the two writes are a protocol handshake whose latency IS connection setup
// (see the call site for the measurements).
//
// It is a plain function so the rule can be pinned without timing: a test that
// measured the actual wait would depend on this host's ~500 us clock.
func shouldGatherUpload(nextSeq uint64, pendingBytes int) bool {
	if nextSeq <= 1 {
		return false
	}
	return pendingBytes < packetUpGatherBypass
}

// run is the flusher: take a batch, post it, repeat until closed and drained.
func (u *packetUplink) run() {
	defer close(u.done)
	for {
		u.access.Lock()
		for len(u.pending) == 0 && !u.closed && u.err == nil {
			u.notEmpty.Wait()
		}
		if u.err != nil || (u.closed && len(u.pending) == 0) {
			u.access.Unlock()
			return
		}
		// lx: XHTTP finalization — the OPENING PAIR does not gather.
		//
		// A session's first two writes are the VLESS request header and the first
		// payload: a protocol handshake, not a burst, and the server withholds its
		// download response until that first uplink arrives, so their latency is
		// connection setup. MEASURED in-process with production defaults: the pair
		// merges only when the payload lands within ~300 us; at 600 us and beyond
		// the header waits out the window and is posted alone anyway, and the
		// payload costs a second post. Either way the window only adds setup
		// latency, and the harness showed it: setup p50 0.79x the reference with
		// the window, 1.00x-1.11x without it for the opening pair, with the upload
		// win intact (1.19x -> 1.21x).
		//
		// From the third post on the window keeps doing its real job — filling a
		// post toward sc_max_each_post_bytes before the throttle makes that size
		// the stream's rate. Skipping it for EVERY small queue instead of just the
		// opening pair was measured and rejected: batching leaves small leftovers
		// (a 1 MiB cap out of a 1 MiB + 50 B queue leaves 50 B), and each leftover
		// would become its own throttled post, costing 13% of upload throughput
		// (23.3 MB/s against 26.8).
		gather := shouldGatherUpload(u.seq, len(u.pending))
		u.access.Unlock()

		if gather {
			u.gather()
		}

		maxEach := u.conn.client.meta.scMaxEachPostBytes.rand()
		u.access.Lock()
		if u.err != nil {
			u.access.Unlock()
			return
		}
		if len(u.pending) == 0 {
			u.access.Unlock()
			continue
		}
		size := len(u.pending)
		if maxEach > 0 && size > maxEach {
			size = maxEach
		}
		payload := u.pending[:size]
		u.pending = u.pending[size:]
		if len(u.pending) == 0 {
			// Drop the consumed prefix so the next burst starts a fresh array
			// instead of carrying a growing offset.
			u.pending = nil
		}
		u.access.Unlock()
		u.notFull.Broadcast()

		if err := u.sendPacket(payload); err != nil {
			u.fail(err)
			return
		}
	}
}

// sendPacket posts one batch as a single sequenced upload request.
//
// The payload is a subslice of the queue, so it must not be written to after the
// hand-off; writers only ever append past the region this post owns, and append
// reallocating the array leaves this slice pointing at the old one, which is
// still intact.
func (u *packetUplink) sendPacket(payload []byte) error {
	u.access.Lock()
	// Only a failed or aborted queue stops a post. `closed` deliberately does
	// NOT: Close stops accepting new writes and then waits for the queue to
	// drain, and a flusher that refused to post a closed queue would drop the
	// tail it was asked to deliver.
	if u.err != nil {
		err := u.err
		u.access.Unlock()
		return err
	}
	seq := u.seq
	u.seq++
	// Throttle: enforce the minimum inter-post interval since the last post.
	wait := u.nextPostDelay(seq)
	u.access.Unlock()

	if wait > 0 {
		timer := time.NewTimer(wait)
		select {
		case <-u.conn.ctx.Done():
			timer.Stop()
			return u.conn.ctx.Err()
		case <-timer.C:
		}
	}

	// lx: SPEC 072 — one post is one bounded HTTP exchange: posts ride the
	// conn-scoped context (they must not die with the dial context), so the
	// bound has to be their own. Without it a wedged pooled connection blocks
	// the flusher — and every writer behind it — indefinitely.
	postCtx, postCancel := context.WithTimeout(u.conn.ctx, packetUpPostTimeout)
	defer postCancel()
	request, err := u.conn.client.newRequest(postCtx, u.conn.client.meta.uplinkHTTPMethod, u.conn.sessionID, strconv.FormatUint(seq, 10), nil)
	if err != nil {
		return err
	}
	u.conn.client.applyUplinkData(request, payload)

	// lx: 059 — every upload POST rides this stream's pooled connection and counts
	// against its h_max_request_times.
	response, err := u.conn.xmux.roundTrip(request)
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return E.New("v2ray-xhttp: unexpected upload status: ", response.Status)
	}
	if err = drainPacketUpResponse(response.Body); err != nil {
		// The body did not finish. A 200 whose body stalls until postCtx expires is NOT a
		// successful post, and reporting it as one is worse than reporting nothing: the caller
		// below disarms the pooled connection's failure streak and the manager backoff, so a
		// wedged upload path would be recorded as genuine data flow and stop being backed off.
		//
		// The read error was discarded here before this change (`, _ = io.Copy(...)`). It is the
		// one signal that distinguishes "the server answered 200 and completed the exchange" from
		// "the server answered 200 and then went silent", and only the first is evidence the path
		// works.
		return E.Cause(err, "v2ray-xhttp: upload response body")
	}
	// lx: SPEC 076 — a drained 200 upload POST is genuine data flow: reset the
	// pooled connection's failure streak and the manager backoff.
	u.conn.xmux.noteSuccess()
	return nil
}

// nextPostDelay returns how long to wait before the next post to honor
// sc_min_posts_interval_ms, updating lastPost to the projected post time. Caller
// must hold u.access.
//
// lx: XHTTP_OUTPERFORM — the session's opening exchange (seq 1, the post that
// follows the VLESS header) is exempt.
//
// A session starts with two writes from two different goroutines: the VLESS
// request header, then the first payload, which the inbound can only relay after
// it has answered its own caller and read from it. Charging the anti-burst
// interval between them puts the payload 30 ms behind the header by default.
// MEASURED before the exemption: connection setup p99 31.7 ms against the
// reference's 1.6 ms, while the median was already 1.4 ms — the tail was exactly
// the handshakes whose two writes did not happen to coalesce.
//
// The reference does not pay it because its upload pipe hands the pair over as a
// single post. Two prompt posts are the wire-compatible equivalent, and unlike a
// timing window this does not depend on a sub-millisecond clock — which this
// transport cannot rely on: the host used for these measurements reports a
// monotonic clock that ticks at roughly 500 us, and a measured "gap < window"
// test then comes down to which side of a tick the write landed on (the gather
// window in this file was measured doing exactly that, which is why the fix for
// setup is here and the window is only used to fill posts).
func (u *packetUplink) nextPostDelay(seq uint64) time.Duration {
	interval := time.Duration(u.conn.client.meta.scMinPostsIntervalMs.rand()) * time.Millisecond
	now := timeNow()
	if u.lastPost.IsZero() || interval <= 0 || seq <= 1 {
		u.lastPost = now
		return 0
	}
	earliest := u.lastPost.Add(interval)
	if earliest.After(now) {
		u.lastPost = earliest
		return earliest.Sub(now)
	}
	u.lastPost = now
	return 0
}

// close stops accepting writes and waits for the queue to drain, so the tail of
// a session is delivered rather than dropped. Bytes still queued when the budget
// expires are abandoned: the path is already wedged, and Close cannot wait out a
// post timeout while a relay is torn down.
func (u *packetUplink) close(budget time.Duration) {
	u.access.Lock()
	u.closed = true
	u.notEmpty.Broadcast()
	u.notFull.Broadcast()
	u.access.Unlock()
	select {
	case <-u.done:
	case <-time.After(budget):
	}
}

// abort fails the queue without draining it: the session is already dead.
func (u *packetUplink) abort(err error) {
	u.access.Lock()
	if u.err == nil {
		u.err = err
	}
	u.closed = true
	u.notEmpty.Broadcast()
	u.notFull.Broadcast()
	u.access.Unlock()
}

// fail records a post failure and releases everyone waiting on the queue. The
// error is what every later Write returns; the read side classifies the conn
// independently (lx: SPEC 076, 094), so the breaker is not touched here.
func (u *packetUplink) fail(err error) {
	u.access.Lock()
	if u.err == nil {
		u.err = err
	}
	u.notEmpty.Broadcast()
	u.notFull.Broadcast()
	u.access.Unlock()
}

func (c *packetConn) Close() error {
	c.access.Lock()
	c.closed = true
	c.access.Unlock()
	// lx: XHTTP_OUTPERFORM — deliver the buffered tail before tearing the session
	// down. A relay writes the last bytes of a request and closes immediately,
	// and a transport that accepted those bytes must not drop them. The wait is
	// bounded (see packetUpDrainTimeout): Close cannot sit out a post timeout.
	c.uplink.close(packetUpDrainTimeout)
	// The reader is late-bound: a conn closed before the download response
	// arrived has nothing to close — the conn-context cancel below aborts the
	// pending RoundTrip (lx: SPEC 072; it used to lean on the dial context,
	// which an unbounded dial context never fires). Guard with closeOnce so a
	// second Close cannot double-close the body.
	var err error
	c.closeOnce.Do(func() {
		c.breaker.localClosed.Store(true) // lx: SPEC 076 — before closing the reader
		c.readDeadline.stop()
		select {
		case <-c.created:
			if c.reader != nil {
				err = c.reader.Close()
			}
		default:
		}
		if c.cancel != nil {
			c.cancel()
		}
		// lx: 059 — release the pooled connection last, once nothing reads it and
		// no further upload POST can be issued (c.closed is already set above).
		c.xmux.release()
	})
	return err
}

func (c *packetConn) LocalAddr() net.Addr  { return M.Socksaddr{} }
func (c *packetConn) RemoteAddr() net.Addr { return c.serverAddr }

// lx: 050 — read deadlines are real now that the reader is late-bound (an
// unbound reader could otherwise stall Read forever). Writes are individual
// HTTP requests each bounded by packetUpPostTimeout (lx: SPEC 072), so a write
// deadline has nothing to arm against and stays unsupported, as before.
func (c *packetConn) SetDeadline(t time.Time) error      { return c.readDeadline.set(t) }
func (c *packetConn) SetReadDeadline(t time.Time) error  { return c.readDeadline.set(t) }
func (c *packetConn) SetWriteDeadline(t time.Time) error { return os.ErrInvalid }

// NeedAdditionalReadDeadline: the read deadline is one-shot, as in streamConn.
func (c *packetConn) NeedAdditionalReadDeadline() bool { return true }

// drainPacketUpResponse consumes an upload response body without pulling a
// 32 KiB pooled buffer for it, and reports whether the body actually finished.
//
// The body must still be fully consumed — HTTP/1.1 keep-alive and HTTP/2 stream
// state both depend on it — but an XHTTP upload response carries nothing, and
// drainAndClose allocates its buffer once per post (lx: XHTTP_OUTPERFORM).
// io.Copy into io.Discard reuses a small internal buffer and allocates only if
// there is something to read.
//
// # Why the copy error is returned rather than discarded
//
// It is the only signal that separates "the server answered 200 and completed the
// exchange" from "the server answered 200 and then went silent until the post's own
// deadline expired". The caller treats a nil return as evidence the path works and
// disarms the pooled connection's failure streak and the manager backoff, so a
// discarded error lets a wedged upload path be recorded as genuine data flow and
// stop being backed off — the opposite of what SPEC 076's noteSuccess is for.
//
// The body is still closed on the error path: the copy failing does not make the
// stream state any less the transport's to clean up.
func drainPacketUpResponse(body io.ReadCloser) error {
	_, copyErr := io.Copy(io.Discard, body)
	closeErr := body.Close()
	// A read failure is the interesting one and is reported first. A close failure on an
	// otherwise fully-drained body still means the exchange did not end cleanly, so it is
	// reported when there is nothing better to say — and it is never silent, because silence
	// here is what the caller reads as "the path works".
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

// byteReader is a one-shot reader over a byte slice used as a fixed-length
// request body for packet-up uploads.
type byteReader struct {
	data []byte
	off  int
}

func (r *byteReader) Read(p []byte) (int, error) {
	if r.off >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.off:])
	r.off += n
	return n, nil
}
