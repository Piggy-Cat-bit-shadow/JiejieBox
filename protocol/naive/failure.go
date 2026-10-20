//go:build with_naive_outbound

package naive

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"

	"github.com/sagernet/cronet-go"
	"github.com/sagernet/sing-box/adapter"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
)

// Naive CONNECT failures become adapter.FailureFacts.
//
// # Why the handshake, and not DialEarly, is the reporting point
//
// A Naive tunnel is established in two stages:
//
//	DialEarly        the bidirectional stream was created and Start() accepted it.
//	                 NO CONNECT has been answered yet; a 407 or a dead QUIC path
//	                 is not visible here.
//	HandshakeContext the CONNECT response arrived, or the stream failed first.
//
// dialNaiveTunnel therefore completes the handshake before it hands the
// connection over, and this file classifies what that handshake returned. A
// wrapper around DialEarly's own error would have classified the one stage that
// never carries a protocol failure.
//
// # Why the mapping is narrow
//
// FailureFacts are consumed by two different policies, and they are not the same
// decision:
//
//	RetryThisFlow          "would another member help?"
//	PenalizeMemberGlobally "is this member's first hop provably broken?"
//
// A fact that is merely plausible can demote a healthy member out of the
// rotation, so an unproven origin stays unproven and the error keeps its old
// errno-based policy. The concrete rules:
//
//	CONNECT answered 407                 {Handshake, Proxy}
//	    The proxy answered and rejected the credentials. Not the path, and
//	    emphatically not a target failure.
//
//	CONNECT answered any other status     no fact (unknown)
//	    Something answered. A 403 ACL or a 502 from a server that dials before
//	    answering cannot be told apart from a target-side refusal here, and
//	    guessing Target or Proxy would be a wrong verdict either way.
//
//	Chromium refused / address unreachable {Handshake, Path}
//	    The kernel-level statements that the member's endpoint cannot be reached,
//	    mirroring the ECONNREFUSED / EHOSTUNREACH / ENETUNREACH entries the group
//	    already trusts. These are the only NetError values promoted to Path.
//
//	name not resolved                     {Handshake, Local}
//	    DNS policy or configuration, i.e. this process, not the member. No retry
//	    and no penalty - the group's existing rules for a local failure.
//
//	TLS/certificate failure               {Handshake, Proxy}
//	    The endpoint got far enough to run TLS and failed it. Retryable, never a
//	    path penalty.
//
//	QUIC protocol/handshake errors        no fact (unknown)
//	    Deliberate. A QUIC failure proves the UDP path failed, NOT that the
//	    member is broken: the same member is very likely healthy over HTTP/2.
//	    A Path fact here would globally penalize a working node, and that is
//	    exactly the failure mode the H3 fallback exists to avoid. Until a
//	    fallback exists, an unproven transport remains unknown.
//
//	timeouts, resets, EOF, cancellations  no fact (unknown)
//	    "Something answered" or "the caller left" are already neutral for the
//	    group; a timeout is not proof that the path is permanently gone.
//
// The status text is produced by the pinned cronet-go dependency
// (naive_conn.go: `E.New("unexpected response status: ", headers[":status"])`).
// It is matched on a suffix so an outer E.Cause wrapper does not break it, and
// a dependency that changes the wording degrades to "no fact" rather than to a
// wrong fact.

const naiveUnexpectedStatusPrefix = "unexpected response status: "

// naiveDialerClient is the narrow surface the dial path needs from the Cronet
// client. It exists so the failure classification can be tested without a
// native library, which is the only way these rules can be exercised on a CI
// runner that has no Cronet build.
//
// *cronet.NaiveClient satisfies it exactly; the interface adds no wrapper to the
// production path.
type naiveDialerClient interface {
	DialEarly(ctx context.Context, destination M.Socksaddr) (cronet.NaiveConn, error)
	// DialEarlyWithQUIC chooses the transport for this request; see the
	// h3FallbackToH2 path for why the choice is per CONNECT.
	DialEarlyWithQUIC(ctx context.Context, destination M.Socksaddr, forceQUIC bool) (cronet.NaiveConn, error)
}

// dialNaiveTunnel opens one Naive tunnel and completes its CONNECT handshake.
//
// The handshake is not optional. Before this change the outbound returned
// DialEarly's connection without ever reading the CONNECT response, so:
//
//   - a 407 was never surfaced as an error at all: the application saw a
//     connection that closed on first read, with the wrong password reported as
//     "the site refused the connection";
//   - no failure fact could be attached, because the only error the caller could
//     see came from DialEarly, which cannot report a protocol failure.
//
// Waiting for the CONNECT response costs one round trip before the first write
// on a NEW tunnel. The reference server (Caddy forwardproxy) sends 200 before it
// dials the target, so that round trip is the tunnel round trip and not the
// target's. The measurement and the reasoning are in the implementation report;
// the alternative - keeping the early return - cannot observe the handshake at
// all.
func dialNaiveTunnel(ctx context.Context, client naiveDialerClient, destination M.Socksaddr) (net.Conn, error) {
	return prepareNaiveTunnel(ctx, client.DialEarly, destination)
}

// dialNaiveTunnelWithQUIC is the per-CONNECT transport choice. It exists for the
// opt-in H3->H2 fallback and for nothing else: the two ordinary modes keep using
// dialNaiveTunnel and therefore the client-level QUIC option.
func dialNaiveTunnelWithQUIC(ctx context.Context, client naiveDialerClient, destination M.Socksaddr, forceQUIC bool) (net.Conn, error) {
	return prepareNaiveTunnel(ctx, func(ctx context.Context, destination M.Socksaddr) (cronet.NaiveConn, error) {
		return client.DialEarlyWithQUIC(ctx, destination, forceQUIC)
	}, destination)
}

// prepareNaiveTunnel runs the two-stage dial and owns the handshake contract:
// nothing is returned to the caller before the CONNECT response was accepted.
func prepareNaiveTunnel(ctx context.Context, dial func(context.Context, M.Socksaddr) (cronet.NaiveConn, error), destination M.Socksaddr) (net.Conn, error) {
	conn, err := dial(ctx, destination)
	if err != nil {
		// DialEarly reports local lifecycle and state failures (a closed client,
		// a stream that could not be created). None of them is evidence about
		// the path or the endpoint, so the error keeps its own classification.
		return nil, err
	}
	if err = conn.HandshakeContext(ctx); err != nil {
		conn.Close()
		err = E.Cause(err, "naive CONNECT handshake")
		if facts := naiveHandshakeFailureFacts(ctx, err); facts.Known() {
			err = adapter.WrapFailure(err, facts)
		}
		return nil, err
	}
	return conn, nil
}

// naiveHandshakeFailureFacts maps a CONNECT handshake failure to the facts the
// group policy understands. It returns the zero value when the cause is not
// proven; callers must treat that as "no fact", not as a verdict.
func naiveHandshakeFailureFacts(ctx context.Context, err error) adapter.FailureFacts {
	if err == nil {
		return adapter.FailureFacts{}
	}
	// The caller withdrew or this process tore the connection down. Both are
	// already neutral for the group, and the caller's own context is the fact
	// that cannot be laundered by a transport that reports something else.
	if ctx.Err() != nil || adapter.IsCallerCancellation(err) || adapter.IsOwnTeardown(err) {
		return adapter.FailureFacts{}
	}
	if status, isStatus := naiveResponseStatus(err); isStatus {
		if status == 407 { // http.StatusProxyAuthRequired
			return adapter.FailureFacts{
				Stage:  adapter.FailureStageHandshake,
				Origin: adapter.FailureOriginProxy,
			}
		}
		return adapter.FailureFacts{}
	}
	var netError cronet.NetError
	if errors.As(err, &netError) {
		switch netError {
		case cronet.NetErrorConnectionRefused, cronet.NetErrorAddressUnreachable:
			// The endpoint could not be reached at all: the same statement as
			// ECONNREFUSED / EHOSTUNREACH, which the group already penalizes.
			return adapter.PathFailureFacts(adapter.FailureStageHandshake)
		case cronet.NetErrorNameNotResolved:
			// DNS policy or configuration - this process, not the member.
			return adapter.FailureFacts{
				Stage:  adapter.FailureStageHandshake,
				Origin: adapter.FailureOriginLocal,
			}
		case cronet.NetErrorSSLProtocolError, cronet.NetErrorCertCommonNameInvalid,
			cronet.NetErrorCertDateInvalid, cronet.NetErrorCertAuthorityInvalid,
			cronet.NetErrorCertWeakKey, cronet.NetErrorCertNameConstraintViolation,
			cronet.NetErrorCertValidityTooLong, cronet.NetErrorCertNonUniqueName:
			return adapter.FailureFacts{
				Stage:  adapter.FailureStageHandshake,
				Origin: adapter.FailureOriginProxy,
			}
		}
	}
	return adapter.FailureFacts{}
}

// naiveResponseStatus extracts the status code from the dependency's
// "unexpected response status: N" error, if that is what this error carries.
//
// It is a suffix match so that this package's own E.Cause prefix does not hide
// the value, and a non-numeric tail is reported as "not a status" rather than
// guessed.
func naiveResponseStatus(err error) (int, bool) {
	message := err.Error()
	index := strings.LastIndex(message, naiveUnexpectedStatusPrefix)
	if index < 0 {
		return 0, false
	}
	status, convertErr := strconv.Atoi(strings.TrimSpace(message[index+len(naiveUnexpectedStatusPrefix):]))
	if convertErr != nil || status < 100 || status > 599 {
		return 0, false
	}
	return status, true
}
