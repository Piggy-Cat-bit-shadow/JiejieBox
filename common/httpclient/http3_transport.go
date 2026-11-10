//go:build with_quic

package httpclient

import (
	"context"
	stdTLS "crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/http/httptrace"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/quic-go"
	"github.com/sagernet/quic-go/congestion"
	"github.com/sagernet/quic-go/http3"
	"github.com/sagernet/sing-box/common/tls"
	"github.com/sagernet/sing-box/option"
	congestion_meta1 "github.com/sagernet/sing-quic/congestion_meta1"
	congestion_meta2 "github.com/sagernet/sing-quic/congestion_meta2"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

type http3Transport struct {
	h3Transport *http3.Transport
}

type http3BrokenEntry struct {
	until   time.Time
	backoff time.Duration
	// outcome is the highest attempt id whose outcome was claimed FOR THIS AUTHORITY, and
	// updatedAt is when it last changed (the ladder/prune clock).
	//
	// The ordering state is per authority on purpose: attempt ids come from one global sequence so
	// they stay comparable, but a success on authority B says nothing about authority A. A shared
	// outcome counter made B's newer success refuse A's older failure, so A's verdict silently
	// disappeared.
	outcome   uint64
	updatedAt time.Time
}

// h3EstablishBudget bounds how long ONE cold HTTP/3 attempt may spend becoming usable. It is the
// same 3 s window transport/http gives a CONNECT-IP setup, for the same reason: a UDP dial plus a
// handshake plus one round trip, bounded so the fallback keeps a usable share of the caller's
// budget. A var rather than a const so a test can shrink it instead of sleeping for it.
var h3EstablishBudget = 3 * time.Second

type http3FallbackTransport struct {
	h3Transport   *http3.Transport
	h2Fallback    innerTransport
	fallbackDelay time.Duration
	// verdictAccess makes the attempt sequence number, the per-authority outcome and the memory
	// one state, so a claim and its effect cannot interleave with each other.
	verdictAccess sync.Mutex
	broken        map[string]http3BrokenEntry
	// attempt issues the sequence number stamped on each attempt. It is global so ids stay
	// comparable across authorities; the ORDERING state lives per authority (see
	// http3BrokenEntry.outcome), because an outcome for one authority says nothing about another.
	attempt uint64
}

func newHTTP3RoundTripper(
	rawDialer N.Dialer,
	baseTLSConfig tls.Config,
	options option.QUICOptions,
) *http3.Transport {
	var handshakeTimeout time.Duration
	if baseTLSConfig != nil {
		handshakeTimeout = baseTLSConfig.HandshakeTimeout()
	}
	quicConfig := NewQUICConfig(options)
	if handshakeTimeout > 0 {
		quicConfig.HandshakeIdleTimeout = handshakeTimeout
	}
	h3Transport := &http3.Transport{
		TLSClientConfig: &stdTLS.Config{},
		QUICConfig:      quicConfig,
		Dial: func(ctx context.Context, addr string, tlsConfig *stdTLS.Config, quicConfig *quic.Config) (*quic.Conn, error) {
			if handshakeTimeout > 0 && quicConfig.HandshakeIdleTimeout == 0 {
				quicConfig = quicConfig.Clone()
				quicConfig.HandshakeIdleTimeout = handshakeTimeout
			}
			if baseTLSConfig != nil {
				var err error
				tlsConfig, err = buildSTDTLSConfig(baseTLSConfig, M.ParseSocksaddr(addr), []string{http3.NextProtoH3})
				if err != nil {
					return nil, err
				}
			} else {
				tlsConfig = tlsConfig.Clone()
				tlsConfig.NextProtos = []string{http3.NextProtoH3}
			}
			conn, err := rawDialer.DialContext(ctx, N.NetworkUDP, M.ParseSocksaddr(addr))
			if err != nil {
				return nil, err
			}
			quicConn, err := quic.DialEarlyConn(ctx, conn, tlsConfig, quicConfig)
			if err != nil {
				conn.Close()
				return nil, err
			}
			go func() {
				<-quicConn.Context().Done()
				conn.Close()
			}()
			return quicConn, nil
		},
	}
	return h3Transport
}

func newHTTP3Transport(
	rawDialer N.Dialer,
	baseTLSConfig tls.Config,
	options option.QUICOptions,
) (innerTransport, error) {
	return &http3Transport{
		h3Transport: newHTTP3RoundTripper(rawDialer, baseTLSConfig, options),
	}, nil
}

func newHTTP3FallbackTransport(
	rawDialer N.Dialer,
	baseTLSConfig tls.Config,
	h2Fallback innerTransport,
	options option.QUICOptions,
	fallbackDelay time.Duration,
) (innerTransport, error) {
	return &http3FallbackTransport{
		h3Transport:   newHTTP3RoundTripper(rawDialer, baseTLSConfig, options),
		h2Fallback:    h2Fallback,
		fallbackDelay: fallbackDelay,
		broken:        make(map[string]http3BrokenEntry),
	}, nil
}

func (t *http3Transport) RoundTrip(request *http.Request) (*http.Response, error) {
	return t.h3Transport.RoundTrip(request)
}

func (t *http3Transport) CloseIdleConnections() {
	t.h3Transport.CloseIdleConnections()
}

func (t *http3Transport) Close() error {
	t.CloseIdleConnections()
	return t.h3Transport.Close()
}

func (t *http3FallbackTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Scheme != "https" || requestRequiresHTTP1(request) {
		return t.h2Fallback.RoundTrip(request)
	}
	return t.roundTripHTTP3(request)
}

// roundTripHTTP3 is the decision point: what may this request do over HTTP/3, and what may be
// re-sent when HTTP/3 does not deliver it?
//
// # The two facts that decide a retry
//
//	sent         an HTTP/3 connection was obtained for this attempt. Only then can a request
//	             header, or any body byte, have reached the peer. A connection that was never
//	             obtained is the ONLY proof this client can have that the peer saw nothing.
//	repeatable   the operation may be performed twice without changing its effect: RFC 9110
//	             §9.2.2's idempotent methods, or an explicit Idempotency-Key (see
//	             requestIdempotent).
//
// Re-sending additionally requires a body that can be produced again (GetBody). The three are
// independent questions and the old code answered only the last one: every request whose body had
// a GetBody was cloned and raced, so a POST whose HTTP/3 leg was merely SLOWER than the fallback
// delay was executed by both legs. "The body can be rebuilt" never implied "the operation may be
// repeated".
//
// The proof of "not sent" is what keeps the fallback usable for non-idempotent requests: when the
// UDP path is dead, the QUIC dial fails before a stream exists, so a POST still reaches HTTP/2
// exactly once instead of failing on an unavailable version.
func (t *http3FallbackTransport) roundTripHTTP3(request *http.Request) (*http.Response, error) {
	authority := requestAuthority(request)
	if t.h3Broken(authority) {
		return t.h2FallbackRoundTrip(request)
	}
	attempt := t.beginH3Attempt()
	// Ask the pool first and DO NOT dial. A miss returns ErrNoCachedConn before any connection,
	// stream or body read exists, so nothing was sent and the request can continue unchanged.
	probe := t.issueH3(request, true)
	switch {
	case probe.err == nil:
		t.clearH3Broken(authority, attempt)
		return probe.response, nil
	case probe.cacheProbeTimedOut:
		// The pool had an entry, but it was still DIALING and did not become usable inside this
		// request's probe window. Waiting longer would spend this request's budget on another
		// request's handshake. Nothing was sent by this one, so the fallback is safe and the
		// still-running dial is left alone.
		return t.h2FallbackRoundTrip(request)
	case errors.Is(probe.err, http3.ErrNoCachedConn):
		return t.roundTripHTTP3Cold(request, authority, attempt)
	default:
		// A cached connection existed and failed this request.
		t.recordH3AttemptFailure(request, authority, attempt, probe.err)
		return t.retryOverH2AfterSentAttempt(request, probe.err)
	}
}

// roundTripHTTP3Cold continues an attempt that found an empty pool.
func (t *http3FallbackTransport) roundTripHTTP3Cold(request *http.Request, authority string, attempt uint64) (*http.Response, error) {
	// A request that may be repeated and whose body may be rebuilt keeps the historical
	// behaviour: both versions are tried, so a dead UDP path does not cost the caller a full
	// handshake timeout. The clones are built BEFORE either goroutine starts, so a GetBody
	// failure is an ordinary error rather than a panic inside a race leg.
	if requestIdempotent(request) && requestReplayable(request) {
		h3Request, h2Request, cloneErr := cloneRequestForRace(request)
		if cloneErr == nil {
			if request.Body != nil && request.Body != http.NoBody {
				// Both legs carry rebuilt bodies; the caller's original body is never read, and
				// closing it is the transport's normal duty.
				request.Body.Close()
			}
			return t.roundTripHTTP3Race(request, authority, attempt, h3Request, h2Request)
		}
		// The body cannot be rebuilt. Fall through to a single attempt, which needs only the
		// original body - still untouched, because nothing has read it.
	}
	cold := t.issueH3(request, false)
	if cold.err == nil {
		t.clearH3Broken(authority, attempt)
		return cold.response, nil
	}
	t.recordH3AttemptFailure(request, authority, attempt, cold.err)
	if !cold.sent {
		// No connection was obtained, so no byte of this request could have reached the peer:
		// safe to re-send over HTTP/2 whatever the method or body. issueH3 left the body
		// untouched for exactly this case.
		return t.h2FallbackRoundTrip(request)
	}
	return t.retryOverH2AfterSentAttempt(request, cold.err)
}

// retryOverH2AfterSentAttempt re-sends a request whose HTTP/3 leg obtained a connection and then
// failed.
//
// Both facts are required: repeating the operation must be harmless AND the body must be
// rebuildable. When either is missing the caller gets the HTTP/3 error, because a duplicated side
// effect cannot be undone and a half-read body would be a different request.
func (t *http3FallbackTransport) retryOverH2AfterSentAttempt(request *http.Request, h3Err error) (*http.Response, error) {
	if !requestIdempotent(request) || !requestReplayable(request) {
		return nil, h3Err
	}
	cloned, err := cloneRequestForRetry(request)
	if err != nil {
		return nil, E.Errors(h3Err, err)
	}
	return t.h2FallbackRoundTrip(cloned)
}

// issueH3 performs exactly one HTTP/3 attempt and reports whether a connection was obtained.
//
// # Why the body is shielded
//
// quic-go's RoundTripOpt closes req.Body on EVERY error, including the OnlyCachedConn miss where
// no connection exists, no stream was opened, nothing was read and nothing was sent. Forwarding
// that Close destroys a one-shot body (an io.Pipe, an upload reader) before the fallback can use
// it, which turned "UDP is blocked, use HTTP/2" into a second failure on a closed body. The shield
// makes this client the owner of the body's lifetime: it is closed here only when this attempt can
// no longer be re-sent.
// h3Issue is the outcome of one HTTP/3 attempt.
type h3Issue struct {
	response *http.Response
	err      error
	// sent reports that the transport obtained a connection for THIS request, so request bytes
	// may have reached the peer.
	sent bool
	// cacheProbeTimedOut reports that the pool entry this request joined was still DIALING when
	// the probe window expired. Nothing was sent by this request; the entry was simply not
	// usable yet.
	cacheProbeTimedOut bool
}

// cacheProbeWindow is how long a request may wait for a pool entry that is still dialing before
// it uses the fallback instead.
//
// It is the same window the race path uses to decide that HTTP/3 has had its chance, because it
// answers the same question: how long may HTTP/3 spend before the fallback starts? A ready cached
// connection answers immediately, so this window is only ever paid when the entry belongs to
// another request's in-flight dial.
func (t *http3FallbackTransport) cacheProbeWindow() time.Duration {
	if t.fallbackDelay > 0 {
		return t.fallbackDelay
	}
	return 300 * time.Millisecond
}

// errH3CacheProbeWindow marks a probe that was still waiting for another request's dial when its
// window expired.
var errH3CacheProbeWindow = E.New("HTTP/3 cache probe exceeded its window while the pool entry was still dialing")

func (t *http3FallbackTransport) issueH3(request *http.Request, onlyCached bool) h3Issue {
	hasBody := request.Body != nil && request.Body != http.NoBody
	attemptRequest := request
	var owner *requestBodyOwner
	if hasBody {
		owner = &requestBodyOwner{body: request.Body}
		attemptRequest = request.Clone(request.Context())
		attemptRequest.Body = &shieldedRequestBody{ReadCloser: request.Body}
	}
	var issue h3Issue
	if onlyCached {
		var (
			established atomic.Bool
			timedOut    atomic.Bool
		)
		probeCtx, cancelProbe := context.WithCancel(attemptRequest.Context())
		timer := time.AfterFunc(t.cacheProbeWindow(), func() {
			if !established.Load() {
				timedOut.Store(true)
				cancelProbe()
			}
		})
		defer timer.Stop()
		attemptRequest = attemptRequest.WithContext(httptrace.WithClientTrace(probeCtx, &httptrace.ClientTrace{
			GotConn: func(httptrace.GotConnInfo) { established.Store(true) },
		}))
		issue.response, issue.err = t.h3Transport.RoundTripOpt(attemptRequest, http3.RoundTripOpt{OnlyCachedConn: true})
		// ErrNoCachedConn is decided before a connection or stream exists; every other outcome
		// here had a cached connection to fail on.
		issue.sent = issue.err == nil || !errors.Is(issue.err, http3.ErrNoCachedConn)
		if timedOut.Load() && !established.Load() {
			// The probe was a WAIT on somebody else's dial, not an attempt of this request.
			issue.cacheProbeTimedOut = true
			issue.sent = false
			issue.err = E.Errors(errH3CacheProbeWindow, context.Canceled)
		}
	} else {
		var established atomic.Bool
		attemptCtx, cancelAttempt := context.WithCancel(attemptRequest.Context())
		var gaveUp atomic.Bool
		// The attempt gets its own establishment bound.
		//
		// # Why a bound of its own is ownership, not impatience
		//
		// A cold attempt can spend the CALLER's whole budget inside the HTTP/3 transport: the
		// pool serialises dials per host, so this request can be waiting for a dial that belongs
		// to another request, and the fallback then has nothing left to spend. Observed shape:
		// request A opened a dial; request B joined it with a short budget; B timed out without
		// ever starting its own HTTP/2 attempt, on a healthy HTTP/2 path.
		//
		// The bound covers ESTABLISHMENT only. Once the transport reports a connection
		// (httptrace.GotConn) the timer does nothing, because from that point the request is
		// running under the caller's own context and its own deadline.
		timer := time.AfterFunc(h3EstablishBudget, func() {
			if !established.Load() {
				gaveUp.Store(true)
				cancelAttempt()
			}
		})
		defer timer.Stop()
		attemptRequest = attemptRequest.WithContext(httptrace.WithClientTrace(attemptCtx, &httptrace.ClientTrace{
			GotConn: func(httptrace.GotConnInfo) { established.Store(true) },
		}))
		issue.response, issue.err = t.h3Transport.RoundTrip(attemptRequest)
		// GotConn is reported after the connection (and its handshake) exists and before any
		// stream does. A dial that failed never reaches it, which is the proof used above.
		issue.sent = established.Load()
		if gaveUp.Load() && issue.err != nil {
			// Name the cause: the transport's own error is a context cancellation that this
			// package produced, and the caller must not read it as its own deadline.
			issue.err = E.Cause(issue.err, "HTTP/3 attempt exceeded ", h3EstablishBudget)
		}
	}
	if owner != nil {
		switch {
		case issue.err == nil:
			// The request was accepted and answered. quic-go does not close a request body on
			// success, and the upload may still be streaming; the body's owner is therefore tied
			// to the RESPONSE: when the caller releases the response, the request is over.
			issue.response.Body = &responseBodyWithOwner{ReadCloser: issue.response.Body, owner: owner}
		case issue.sent:
			// The body may have been consumed by the failed attempt, so it cannot carry a retry.
			owner.Close()
		}
		// !sent: the body is untouched and still owned by the request, which the fallback will
		// hand to HTTP/2.
	}
	return issue
}

// requestBodyOwner owns one request body and closes it exactly once.
//
// # Why an owner object instead of a direct Close
//
// The same body can be reached from two directions - the attempt that started it and the response
// the caller releases - and the HTTP/3 transport deliberately does not close it on success. A
// single owner makes "closed once" a property of the object rather than of every caller remembering
// the rule, which is what the review measured: an upload body closed zero times after a successful
// H3 request, or closed twice when both a race leg and the cleanup reached it.
type requestBodyOwner struct {
	body io.ReadCloser
	once sync.Once
}

func (o *requestBodyOwner) Close() {
	o.once.Do(func() {
		_ = o.body.Close()
	})
}

// responseBodyWithOwner releases the request body when the caller releases the response.
type responseBodyWithOwner struct {
	io.ReadCloser
	owner *requestBodyOwner
	once  sync.Once
}

func (b *responseBodyWithOwner) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.owner.Close)
	return err
}

// shieldedRequestBody absorbs the HTTP/3 transport's error-path Close while an attempt still owns
// the body. See issueH3.
type shieldedRequestBody struct {
	io.ReadCloser
}

func (b *shieldedRequestBody) Close() error {
	return nil
}

func (t *http3FallbackTransport) roundTripHTTP3Race(request *http.Request, authority string, attempt uint64, h3Request *http.Request, h2Request *http.Request) (*http.Response, error) {
	type result struct {
		response *http.Response
		err      error
		h3       bool
	}
	var (
		results  = make(chan result, 2)
		cancels  []context.CancelFunc
		received int
	)
	// Each leg gets its own shielded request body and an owner for it: the HTTP/3 transport does
	// not close a request body on success and the HTTP/2 transport's Close is shielded here, so the
	// race itself is the only party that can close both copies exactly once - including the copy of
	// a leg that never started.
	legRequest := func(base *http.Request) (*http.Request, *requestBodyOwner) {
		if base.Body == nil || base.Body == http.NoBody {
			return base, nil
		}
		owner := &requestBodyOwner{body: base.Body}
		clone := base.Clone(base.Context())
		clone.Body = &shieldedRequestBody{ReadCloser: base.Body}
		return clone, owner
	}
	h3Leg, h3Owner := legRequest(h3Request)
	h2Leg, h2Owner := legRequest(h2Request)
	startRoundTrip := func(useH3 bool, base *http.Request) {
		ctx, cancel := context.WithCancel(request.Context())
		cancels = append(cancels, cancel)
		raceRequest := base.WithContext(ctx)
		go func() {
			var (
				response *http.Response
				err      error
			)
			if useH3 {
				response, err = t.h3Transport.RoundTrip(raceRequest)
			} else {
				response, err = t.h2FallbackRoundTrip(raceRequest)
			}
			results <- result{response: response, err: err, h3: useH3}
		}()
	}
	finish := func(winner int) {
		for index, cancel := range cancels {
			if index != winner {
				cancel()
			}
		}
		if h3Owner != nil {
			h3Owner.Close()
		}
		if h2Owner != nil {
			h2Owner.Close()
		}
		for range len(cancels) - received {
			go func() {
				loser := <-results
				if loser.response != nil {
					loser.response.Body.Close()
				}
			}()
		}
	}
	startRoundTrip(true, h3Leg)
	timer := time.NewTimer(t.fallbackDelay)
	defer timer.Stop()
	var (
		h3Err       error
		fallbackErr error
	)
	for {
		select {
		case <-timer.C:
			if len(cancels) == 1 {
				startRoundTrip(false, h2Leg)
			}
		case raceResult := <-results:
			received++
			if raceResult.err == nil {
				winner := 1
				if raceResult.h3 {
					winner = 0
					t.clearH3Broken(authority, attempt)
				}
				finish(winner)
				raceResult.response.Body = &cancelOnCloseBody{ReadCloser: raceResult.response.Body, cancel: cancels[winner]}
				return raceResult.response, nil
			}
			if raceResult.h3 {
				t.recordH3AttemptFailure(request, authority, attempt, raceResult.err)
				h3Err = raceResult.err
				if len(cancels) == 1 {
					if !timer.Stop() {
						select {
						case <-timer.C:
						default:
						}
					}
					startRoundTrip(false, h2Leg)
				}
			} else {
				fallbackErr = raceResult.err
			}
			if received < len(cancels) {
				continue
			}
			finish(-1)
			switch {
			case h3Err != nil && fallbackErr != nil:
				return nil, E.Errors(h3Err, fallbackErr)
			case fallbackErr != nil:
				return nil, fallbackErr
			default:
				return nil, h3Err
			}
		}
	}
}

type cancelOnCloseBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelOnCloseBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

func (t *http3FallbackTransport) h2FallbackRoundTrip(request *http.Request) (*http.Response, error) {
	if fallback, isFallback := t.h2Fallback.(*http2FallbackTransport); isFallback {
		return fallback.roundTrip(request, true)
	}
	return t.h2Fallback.RoundTrip(request)
}

func (t *http3FallbackTransport) CloseIdleConnections() {
	t.h3Transport.CloseIdleConnections()
	t.h2Fallback.CloseIdleConnections()
}

func (t *http3FallbackTransport) Close() error {
	t.CloseIdleConnections()
	return t.h3Transport.Close()
}

func (t *http3FallbackTransport) h3Broken(authority string) bool {
	if authority == "" {
		return false
	}
	t.verdictAccess.Lock()
	defer t.verdictAccess.Unlock()
	entry, found := t.broken[authority]
	if !found {
		return false
	}
	if entry.until.IsZero() || !time.Now().Before(entry.until) {
		// The window is over, so HTTP/3 may be tried again. The entry is NOT deleted: it carries
		// the ladder rung and the outcome ordering, and deleting it here restarted the ladder at
		// five minutes on every probe and let an older failure re-arm a newer success's authority.
		return false
	}
	return true
}

// beginH3Attempt issues the sequence number that stamps one HTTP/3 attempt. It is called BEFORE
// the attempt, so "newer" means "started later" rather than "reported later".
func (t *http3FallbackTransport) beginH3Attempt() uint64 {
	t.verdictAccess.Lock()
	defer t.verdictAccess.Unlock()
	t.attempt++
	return t.attempt
}

// clearH3Broken records a successful attempt for the authority and disarms its window.
//
// The entry is KEPT, with the success's attempt id as its outcome: a failure from an older attempt
// that reports afterwards must be refused, and deleting the entry would forget that this authority
// has already been observed at that sequence number.
func (t *http3FallbackTransport) clearH3Broken(authority string, attempt uint64) {
	if authority == "" {
		return
	}
	t.verdictAccess.Lock()
	defer t.verdictAccess.Unlock()
	now := time.Now()
	entry := t.broken[authority]
	if attempt <= entry.outcome {
		return
	}
	entry.outcome = attempt
	entry.until = time.Time{}
	entry.backoff = 0
	entry.updatedAt = now
	t.broken[authority] = entry
}

// recordH3AttemptFailure arms the per-authority verdict for an attempt that failed, and does
// nothing for an attempt that was never allowed to conclude.
//
// # Why a failed attempt is not automatically evidence
//
// The verdict this arms is long-lived and expensive to be wrong about: while it is live every
// later request for the authority goes straight to the H2 fallback, so a wrong entry costs an
// H3-capable server its H3 for the length of the window. That makes "what does this attempt
// prove?" the whole question, and the answer for a request the CALLER ended is: nothing. The
// caller's context is cancelled by a closed client, an abandoned DNS query, a refresh that lost
// its consumer, a shutdown or a caller-side deadline - none of which is a fact about the server,
// and all of which used to reach `markH3Broken` and move the authority onto HTTP/2.
//
// # Why the rule is the request's context and not only the error
//
// Reading the error alone would miss the shapes where the cancellation is laundered: a transport
// whose dial is aborted can report the underlying transport error rather than
// `context.Canceled`, and in the race path the two legs can finish in either order. The caller's
// own context is the fact that cannot be laundered - if it is done, the attempt was cut short by
// the caller, whatever error surfaced. `context.Canceled` is checked as well for the case where a
// cancellation reaches the transport without the request context being the parent that stopped.
//
// # What is deliberately still evidence
//
// Everything else, including a caller-side deadline that has NOT yet expired and any error the
// transport produced on its own - a QUIC handshake timeout, a stateless reset, a version
// negotiation failure. Those are the half-dead peers this fallback exists for, and the sibling
// implementation (`transport/v2rayxhttp`, SPEC 094) takes the same position while keeping
// `context.DeadlineExceeded` a failure on purpose.
func (t *http3FallbackTransport) recordH3AttemptFailure(request *http.Request, authority string, attempt uint64, err error) {
	if err == nil {
		return
	}
	if errors.Is(err, http3.ErrNoCachedConn) {
		// Not an attempt at all: the cache was empty, so nothing was tried.
		return
	}
	if request != nil && request.Context().Err() != nil {
		return
	}
	if errors.Is(err, context.Canceled) {
		return
	}
	t.markH3Broken(authority, attempt)
}

// markH3Broken records one failed HTTP/3 episode and advances the ladder by one rung.
//
// # One episode, one rung
//
// A failure that arrives while the window it opened is still live belongs to the SAME episode. A
// burst of parallel requests on a dead path joins one dial inside the HTTP/3 transport, so they
// all report the same failure; charging each of them multiplied the ladder by 2^N in one instant
// (eleven results took 5 minutes to the 48-hour ceiling, which reads as "HTTP/3 has been down for
// two days" after one blip). The open window already covers them, so the rung is only advanced
// when the previous window has actually expired - which is also what makes the ladder mean
// something: 5m, 10m, 20m ... is a record of episodes, not of goroutines.
//
// `attempt` orders the outcome against the other attempts FOR THIS AUTHORITY. Attempt ids come
// from one global sequence (that is what makes them globally comparable), but the ordering state
// is per authority: authority B's newer success must not suppress authority A's older failure.
func (t *http3FallbackTransport) markH3Broken(authority string, attempt uint64) {
	if authority == "" {
		return
	}
	t.verdictAccess.Lock()
	defer t.verdictAccess.Unlock()
	now := time.Now()
	entry := t.broken[authority]
	if attempt <= entry.outcome {
		// A newer observation for this authority has already been recorded.
		return
	}
	entry.outcome = attempt
	entry.updatedAt = now
	if !entry.until.IsZero() && now.Before(entry.until) {
		t.broken[authority] = entry
		return
	}
	next := 5 * time.Minute
	if entry.backoff > 0 {
		next = min(entry.backoff*2, 48*time.Hour)
	}
	entry.backoff = next
	entry.until = now.Add(next)
	t.broken[authority] = entry
	if len(t.broken) > 64 {
		t.pruneH3BrokenLocked(now)
	}
}

// h3BrokenPruneAge is how long an entry is kept after its last update. The entry has to outlive
// its own window (that is the ladder memory), but not forever: an authority that has been silent
// for a day starts again at the first rung.
const h3BrokenPruneAge = 24 * time.Hour

func (t *http3FallbackTransport) pruneH3BrokenLocked(now time.Time) {
	for authority, entry := range t.broken {
		if now.Sub(entry.updatedAt) > h3BrokenPruneAge {
			delete(t.broken, authority)
		}
	}
}

func NewQUICConfig(options option.QUICOptions) *quic.Config {
	quicConfig := &quic.Config{
		InitialStreamReceiveWindow:     options.StreamReceiveWindow.Value(),
		MaxStreamReceiveWindow:         options.StreamReceiveWindow.Value(),
		InitialConnectionReceiveWindow: options.ConnectionReceiveWindow.Value(),
		MaxConnectionReceiveWindow:     options.ConnectionReceiveWindow.Value(),
		KeepAlivePeriod:                time.Duration(options.KeepAlivePeriod),
		MaxIdleTimeout:                 time.Duration(options.IdleTimeout),
		DisablePathMTUDiscovery:        options.DisablePathMTUDiscovery,
	}
	if options.InitialPacketSize > 0 {
		quicConfig.InitialPacketSize = uint16(options.InitialPacketSize)
	}
	if options.InitialPacketSizeCeiling > 0 {
		quicConfig.InitialPacketSizeCeiling = uint16(options.InitialPacketSizeCeiling)
	}
	if options.MaxConcurrentStreams > 0 {
		quicConfig.MaxIncomingStreams = int64(options.MaxConcurrentStreams)
	}
	return quicConfig
}

// NewClientCongestionControl resolves `quic_congestion_control` to a sender
// factory for a CLIENT HTTP/3 connection.
//
// It returns (nil, nil) for an unset or explicitly "default" value, which means
// "leave quic-go's own sender in place". That is the important case: the upstream
// default is CUBIC, and this fork must not silently select a different algorithm,
// because a congestion control choice changes wire behaviour and would not be
// visible in the configuration.
//
// It is a separate function from the option parsing so the VALUE VALIDATION can be
// tested without opening a QUIC connection: a test that wanted to check "bogus is
// refused" through a dial would need a live server.
//
// Matching is exact, with no trimming and no case folding: an unrecognised value is
// a configuration error the operator should see, and silently accepting "BBR" or
// " bbr" would make a typo look like it worked.
func NewClientCongestionControl(name string) (func(conn *quic.Conn) congestion.CongestionControl, error) {
	switch name {
	case "", "default":
		// nil leaves quic-go's own sender, which is CUBIC.
		return nil, nil
	case "bbr":
		// The congestion_meta2 BBR profile, the same implementation the HTTP/3
		// server selects through bbr_profile, so a client and server that both
		// name BBR get the same sender rather than two different BBRs.
		return func(conn *quic.Conn) congestion.CongestionControl {
			return congestion_meta2.NewBbrSenderWithProfile(conn.InitialPacketSize(), congestion_meta2.ProfileStandard)
		}, nil
	case "cubic":
		return func(conn *quic.Conn) congestion.CongestionControl {
			return congestion_meta1.NewCubicSender(
				congestion_meta1.DefaultClock{},
				conn.InitialPacketSize(),
				false,
			)
		}, nil
	case "reno":
		// congestion_meta1's CubicSender with `reno=true` IS the Reno
		// implementation; there is no separate constructor, and this mirrors how
		// protocol/naive/quic resolves the same option name.
		return func(conn *quic.Conn) congestion.CongestionControl {
			return congestion_meta1.NewCubicSender(
				congestion_meta1.DefaultClock{},
				conn.InitialPacketSize(),
				true,
			)
		}, nil
	default:
		return nil, E.New("unknown quic congestion control: ", name)
	}
}

// ApplyClientCongestionControl installs the configured congestion control on a
// freshly dialled client QUIC connection.
//
// A nil factory is the "leave the library default" case and is deliberately NOT an
// error: that is what an unset option resolves to, and it is the upstream
// behaviour.
func ApplyClientCongestionControl(conn *quic.Conn, factory func(conn *quic.Conn) congestion.CongestionControl) {
	if factory == nil {
		return
	}
	conn.SetCongestionControl(factory(conn))
}
