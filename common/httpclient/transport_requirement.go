package httpclient

import (
	"github.com/sagernet/sing-box/option"
	N "github.com/sagernet/sing/common/network"
)

// TransportRequirement is what one consumer needs from the transport it dials through.
//
// # Why this is not one flat list
//
// A consumer's demand is not one fact, and flattening it into a single `Required` list is exactly
// what made this validator refuse configurations the runtime runs. Two distinctions are load-bearing:
//
//   - CONJUNCTIVE vs DISJUNCTIVE. An HTTP/3 client with its fallback enabled can serve a request
//     over EITHER transport, so requiring both refuses a working configuration - while requiring
//     neither accepts one that carries nothing. The demand is "at least one of these", which a list
//     of hard requirements cannot express.
//   - REQUIRED-FOR-ANY-SUCCESS vs CONDITIONAL. A network reachable only through a fallback or retry
//     branch is not a startup requirement when the primary path still serves.
//
// "A path may be used" is not "every valid execution requires that path".
type TransportRequirement struct {
	// AnyOf is a DISJUNCTIVE group: the detour must carry AT LEAST ONE of these, or no request can
	// be served at all. This is the set a startup refusal tests. It exists because
	// `http3FallbackTransport` can serve a request over either HTTP/3 or the HTTP/2 fallback, so
	// naming either one alone as required is wrong in one direction or the other.
	AnyOf []string
	// AllOf is a CONJUNCTIVE group: every element is needed for any request to succeed. Most
	// transports use this instead of AnyOf; a requirement uses one or the other, never both, and
	// CheckDetourCapability accepts the union so a future transport can state both if it must.
	AllOf []string
	// Conditional is needed by SOME request class rather than all - so it is never a startup
	// requirement. It is recorded so a diagnostic can explain what is lost.
	Conditional []string
}

// http3FallbackRequirement is the requirement for `version: 3` WITHOUT `disable_version_fallback`.
//
// # Why it is disjunctive, derived from the three paths in roundTripHTTP3
//
// `http3FallbackTransport.roundTripHTTP3` (common/httpclient/http3_transport.go:138-160) branches:
//
//	:139  the authority is already known broken  -> straight to the H2 fallback
//	:147  the H3 error is anything but ErrNoCachedConn -> H2 fallback, replayably
//	:151  the request is NOT replayable          -> H3 alone; its error is returned (:158)
//	:160  otherwise                              -> race H3 against H2 (:164-261)
//
// So over a detour carrying only TCP: a replayed request is served by the H2 side of the race, and a
// NON-replayable one cannot be served at all. Over a detour carrying only UDP the H3 path serves
// normally. Over one carrying neither, every path fails.
//
// That is "at least one of UDP, TCP" - not "both", and not "either one specifically".
func http3FallbackRequirement() TransportRequirement {
	return TransportRequirement{
		AnyOf: []string{N.NetworkUDP, N.NetworkTCP},
		Conditional: []string{
			// A non-replayable request body needs UDP specifically, because :151 takes the H3-only
			// path for it. Recorded rather than required: refusing a TCP-only detour would reject a
			// client that serves every ordinary request.
			N.NetworkUDP,
		},
	}
}

// HTTPClientTransportRequirement reports what an HTTP client with these options needs.
//
// Read from newTransport (client.go:89-128) and from the transports themselves, not from the option
// names:
//
//	version 1     newHTTP1Transport           -> TCP for any request
//	version 2     newHTTP2Transport           -> TCP for any request
//	              newHTTP2FallbackTransport   -> TCP for any request
//	version 3     newHTTP3Transport           -> UDP for any request: H3 with no fallback at all
//	              newHTTP3FallbackTransport   -> at least one of UDP, TCP (see above)
//	version 0     the default is 2 (client.go:91-93)
func HTTPClientTransportRequirement(options option.HTTPClientOptions) TransportRequirement {
	version := options.Version
	if version == 0 {
		version = 2
	}
	switch version {
	case 3:
		if options.DisableVersionFallback {
			// `newHTTP3Transport` has no H2 path at all, so UDP is genuinely required.
			return TransportRequirement{AllOf: []string{N.NetworkUDP}}
		}
		return http3FallbackRequirement()
	default:
		// v1, v2 and the default. Both H1 and H2 ride TCP (helpers.go:28 is the single TLS dial),
		// and neither has a UDP path.
		return TransportRequirement{AllOf: []string{N.NetworkTCP}}
	}
}
