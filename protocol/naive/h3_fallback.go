//go:build with_naive_outbound

package naive

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/sagernet/cronet-go"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
)

// h3FallbackWindow is how long a failed H3 attempt is remembered.
//
// It is a var, not a const, for the same reason transport/http's windows are: a
// test must be able to shrink it instead of sleeping for the real value.
//
// # Why a window at all
//
// Without one, every new CONNECT pays the failed H3 attempt before falling back,
// which on a UDP-blocked network is the whole connection-attempt budget on every
// single connection. With one, the first failure buys a quiet period during
// which the H2 path is used directly.
//
// 30 s is deliberately much shorter than the 5 min ceiling transport/http uses
// for MASQUE. This client can be the only path to the outside for a browser, and
// a network that blocks UDP often stops blocking it (a different Wi-Fi, a
// different mobile carrier, a VPN reconnecting). Being wrong for 30 s costs one
// extra H3 attempt; being wrong for 5 min costs the user H3 on a network that
// fixed itself. The failure is also cheap to detect again: the next attempt
// after the window re-arms it.
var h3FallbackWindow = 30 * time.Second

// h3Fallback remembers one thing: forced H3 failed recently.
//
// # What it is not
//
// It is not a second health system. It holds no per-target state, feeds nothing
// else, and is never consulted by any other protocol. It answers exactly one
// question - "should the next new CONNECT try H3 first, or go straight to H2?" -
// and it is reset on a network change along with the connections it describes.
//
// # Ordering, and why the attempt number exists
//
// A failure that started BEFORE a success must not arm the window after that
// success. Two dials can be in flight across the boundary, and their outcomes
// can be reported out of order. The attempt number is taken BEFORE the attempt,
// so "newer" means "started later", which is the ordering that describes the
// network rather than the scheduler.
//
// The claim and the effect are one critical section, for the reason recorded in
// transport/http/client.go: an outcome that passes the ordering check and then
// stores its effect can still lose the race against a newer reset.
type h3Fallback struct {
	access      sync.Mutex
	attempt     uint64
	outcome     uint64
	brokenUntil time.Time
}

// begin issues the sequence number for one attempt. It is called before the
// attempt, not after.
func (f *h3Fallback) begin() uint64 {
	f.access.Lock()
	defer f.access.Unlock()
	f.attempt++
	return f.attempt
}

// available reports whether a new CONNECT should try H3 first.
func (f *h3Fallback) available(now time.Time) bool {
	f.access.Lock()
	defer f.access.Unlock()
	return f.brokenUntil.IsZero() || !now.Before(f.brokenUntil)
}

// claim records this attempt as the newest outcome. An outcome older than the
// recorded one is refused.
func (f *h3Fallback) claim(attempt uint64) bool {
	if attempt <= f.outcome {
		return false
	}
	f.outcome = attempt
	return true
}

// markBroken arms the window for a failed attempt.
//
// A failure that arrives while the window is already open does NOT extend it:
// one event is one decision, and extending on every parallel dial would turn a
// single blip into a long outage.
func (f *h3Fallback) markBroken(attempt uint64, now time.Time) {
	f.access.Lock()
	defer f.access.Unlock()
	if !f.claim(attempt) {
		return
	}
	if !f.brokenUntil.IsZero() && now.Before(f.brokenUntil) {
		return
	}
	f.brokenUntil = now.Add(h3FallbackWindow)
}

// clear records a successful H3 attempt.
func (f *h3Fallback) clear(attempt uint64) {
	f.access.Lock()
	defer f.access.Unlock()
	if !f.claim(attempt) {
		return
	}
	f.brokenUntil = time.Time{}
}

// reset forgets everything. It is called on a network transition, because the
// window describes the network that failed, not the member.
func (f *h3Fallback) reset() {
	f.access.Lock()
	defer f.access.Unlock()
	// Every attempt issued so far belongs to the network being left, so it must
	// not be able to arm the window after this reset. Bumping the outcome is how
	// that is expressed without a second counter.
	f.outcome = f.attempt
	f.brokenUntil = time.Time{}
}

// h3TransportFailure reports whether an error is evidence that the H3 TRANSPORT
// failed, as opposed to the proxy answering.
//
// This is deliberately NOT the same set as naiveHandshakeFailureFacts: there, a
// QUIC failure produces no fact because a Path fact would globally penalize a
// member that is healthy over TCP. Here the question is local and cheap - "may I
// try the other transport for this one flow?" - and a QUIC handshake failure is
// exactly the answer.
//
// A 407 is not in this set, and neither is any other HTTP status: the proxy
// answered, so another transport would answer the same way.
func h3TransportFailure(err error) bool {
	if err == nil {
		return false
	}
	if adapter.IsCallerCancellation(err) {
		return false
	}
	// The code decides first, and only then the "this process closed it" test.
	//
	// The order matters because a Chromium NetError deliberately reports
	// Is(net.ErrClosed) for its CONNECTION_CLOSED / CONNECTION_RESET codes
	// (cronet-go's NetError.Is maps them onto the Go sentinel). Testing teardown
	// first would therefore classify a dead QUIC attempt as "we closed it" and
	// silently disable the retry for exactly the failure the retry exists for.
	var netError cronet.NetError
	if errors.As(err, &netError) {
		switch netError {
		case cronet.NetErrorQUICProtocolError,
			cronet.NetErrorQUICHandshakeFailed,
			cronet.NetErrorConnectionRefused,
			cronet.NetErrorAddressUnreachable,
			cronet.NetErrorConnectionClosed,
			cronet.NetErrorConnectionReset,
			cronet.NetErrorTimedOut:
			return true
		}
		return false
	}
	// Anything that is not a Chromium code: a real local teardown is still not
	// evidence about the transport.
	if adapter.IsOwnTeardown(err) {
		return false
	}
	return false
}

// h3ProbeBudget is the most one H3 attempt may spend before the H2 retry gets
// what is left of the caller's budget.
//
// # Why an H3 attempt needs its own bound
//
// The H3 attempt inherits the caller's deadline today, and Chromium's connection
// attempt budget can consume almost all of it (4.5 s measured on this host).
// With a 5 s caller deadline that leaves the H2 retry 0.5 s, which is how a
// fallback turns into a second failure - the user asked for one connection and
// got none, on a server that would have answered over H2 in milliseconds.
//
// # Why half
//
// The retry must be able to succeed, which means it needs at least as much time
// as the attempt that failed. Splitting the remaining budget in half is the
// smallest rule that guarantees that without inventing an absolute timeout for
// the whole flow. The 10 s cap exists for callers with a generous deadline: an
// H3 attempt should not inherit a 60 s budget just because the caller allows it.
//
// # Why this is not the MASQUE 3 s window
//
// transport/http gives HTTP/3 a fixed 3 s establishment window because a tunnel
// has nothing to show for a slow handshake. Here the window is derived from the
// caller's own budget, because a Naive CONNECT on a high-RTT path can legitimately
// take longer than 3 s and must not be pre-empted on a hunch.
var h3ProbeBudget = 10 * time.Second

// h3ProbeContext bounds one H3 attempt. The returned bool reports whether a
// bound was applied; false means there was no budget to split and the caller's
// own state decides.
func h3ProbeContext(ctx context.Context) (context.Context, context.CancelFunc, bool) {
	if deadline, hasDeadline := ctx.Deadline(); hasDeadline {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return ctx, func() {}, false
		}
		if half := remaining / 2; half < h3ProbeBudget {
			probeCtx, cancel := context.WithTimeout(ctx, half)
			return probeCtx, cancel, true
		}
	}
	probeCtx, cancel := context.WithTimeout(ctx, h3ProbeBudget)
	return probeCtx, cancel, true
}

// dialWithH3Fallback is the opt-in availability path used by the TCP outbound.
func (h *Outbound) dialWithH3Fallback(ctx context.Context, destination M.Socksaddr) (net.Conn, error) {
	return dialNaiveWithH3Fallback(ctx, h.client, &h.h3Fallback, h.logger, destination)
}

// dialNaiveWithH3Fallback is shared by the TCP outbound and the UoT dialer: both
// open exactly one CONNECT, so both get the same single safe retry.
//
//	H3 established            -> return it
//	H3 failed at transport    -> remember, retry the SAME destination over H2
//	H3 attempt ran out of its probe budget -> AMBIGUOUS: retry this time, do not
//	                              remember (a slow answer is not a broken path)
//	H3 failed at the proxy    -> return that error, do not retry
//	window open               -> straight to H2
//
// Every attempt runs the full two-stage dial, and that is what makes the fallback
// safe: the H3 attempt has already completed (or failed) its handshake before the
// H2 attempt starts, so no application byte can have been handed to a connection
// that is later replaced. There is no migration and no replay.
func dialNaiveWithH3Fallback(ctx context.Context, client naiveDialerClient, fallback *h3Fallback, l logger.ContextLogger, destination M.Socksaddr) (net.Conn, error) {
	attempt := fallback.begin()
	if fallback.available(time.Now()) {
		probeCtx, cancelProbe, bounded := h3ProbeContext(ctx)
		if bounded {
			conn, err := dialNaiveTunnelWithQUIC(probeCtx, client, destination, true)
			probeExpired := probeCtx.Err() != nil
			cancelProbe()
			if err == nil {
				fallback.clear(attempt)
				return conn, nil
			}
			// The CALLER withdrew. That is not evidence about H3 and nobody is
			// waiting for a second attempt.
			if ctx.Err() != nil {
				return nil, err
			}
			if probeExpired {
				// AMBIGUOUS: our own bound fired before Chromium reached a
				// verdict. The path is neither proven broken nor proven working,
				// so this flow retries over H2 but the window stays unarmed -
				// the next connection must be free to try H3 again.
				l.DebugContext(ctx, "naive H3 attempt ran out of its probe budget, retrying over HTTP/2 for this flow only: ", err)
				return dialNaiveTunnelWithQUIC(ctx, client, destination, false)
			}
			if !h3TransportFailure(err) {
				return nil, err
			}
			fallback.markBroken(attempt, time.Now())
			l.DebugContext(ctx, "naive H3 attempt failed at the transport, retrying over HTTP/2: ", err)
		}
	}
	return dialNaiveTunnelWithQUIC(ctx, client, destination, false)
}
