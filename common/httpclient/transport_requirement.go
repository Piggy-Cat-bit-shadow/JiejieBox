package httpclient

import (
	"github.com/sagernet/sing-box/option"
	N "github.com/sagernet/sing/common/network"
)

// TransportRequirement is what one consumer needs from the transport it dials through, and how firm
// that need is.
//
// # Why the distinction is the whole point
//
// A consumer's demand is not one fact. `version: 3` WITHOUT `disable_version_fallback` builds an
// HTTP/3 transport that falls back to HTTP/2 for a non-`https` URL, for a websocket upgrade, and on
// any H3 failure (see http3_transport.go's RoundTrip), so it needs UDP **and** TCP. The same
// `version: 3` WITH the flag builds H3 alone and needs UDP only. Reporting either as "the HTTP client
// needs TCP" would be wrong in both directions:
//
//   - claiming TCP where the flag removed it refuses a UDP-only detour that works;
//   - claiming both where the flag kept them is right, but only because the fallback is real.
//
// So the requirement is derived from the CONFIGURED selection, never from the protocol label. This
// is what the closure prompt calls "model the configured runtime selection, not the protocol label".
type TransportRequirement struct {
	// Required are the networks the consumer provably must have. A detour that cannot carry one of
	// these is unusable and the configuration can be refused.
	Required []string
	// Optional are networks the consumer MAY use when the transport supports them. A detour missing
	// one of these is NOT a defect: the consumer takes another path. They exist so a diagnostic can
	// explain a partial capability without turning it into a refusal.
	Optional []string
}

// HTTPClientTransportRequirement reports what an HTTP client with these options needs.
//
// Read from newTransport (client.go:89-128) rather than from the option names:
//
//	version 1        newHTTP1Transport                -> TCP
//	version 2        newHTTP2Transport (flag)          -> TCP
//	                 newHTTP2FallbackTransport         -> TCP
//	version 3        newHTTP3Transport (flag)          -> UDP only: H3, no fallback at all
//	                 newHTTP3FallbackTransport         -> UDP and TCP: H3 with an H2 fallback
//	version 0/other  the default is 2 (client.go:91-93)
//
// The `disable_version_fallback` flag is what separates the last two, and it is the only case in
// this tree where an HTTP client's demand is not TCP.
func HTTPClientTransportRequirement(options option.HTTPClientOptions) TransportRequirement {
	version := options.Version
	if version == 0 {
		version = 2
	}
	switch version {
	case 3:
		if options.DisableVersionFallback {
			return TransportRequirement{Required: []string{N.NetworkUDP}}
		}
		// H3 first, then the H2 fallback: both transports are reachable at run time, so both are
		// required. This is NOT "possible means required" - the fallback is taken on a non-https
		// URL, on a websocket upgrade, and on any H3 failure, so a UDP-only detour breaks every
		// such request.
		return TransportRequirement{Required: []string{N.NetworkUDP, N.NetworkTCP}}
	default:
		// v1, v2, and the default. Both H1 and H2 ride TCP (helpers.go:28 is the single TLS dial).
		return TransportRequirement{Required: []string{N.NetworkTCP}}
	}
}
