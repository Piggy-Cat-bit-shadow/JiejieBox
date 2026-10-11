package transport

import (
	C "github.com/sagernet/sing/common/network"
)

// TransportRequirement is what one DNS transport needs from the outbound it dials through.
//
// # Why this is per TYPE and read from the implementations
//
// A DNS transport's demand cannot be read off its name. `udp` is not "UDP only": its
// implementation retries over TCP when a response arrives truncated, so it needs both. `https` is
// TCP unless an H3 transport is configured, in which case it may use UDP too. Reading the type name
// and guessing would produce a validator that refuses working configurations in one direction and
// blesses broken ones in the other.
//
// Each entry below cites the implementation that decides it.
type TransportRequirement struct {
	// AnyOf is a DISJUNCTIVE group: at least one must be carried, or no query can be served. This is
	// the set a startup refusal tests.
	AnyOf []string
	// AllOf is a CONJUNCTIVE group: every element is needed for any query to succeed. A requirement
	// uses one or the other, never both.
	AllOf []string
	// Conditional is needed by SOME query rather than all, so it is never a startup requirement. It
	// is recorded so a diagnostic can explain what is lost - `udp`'s TCP retry is the case it exists
	// for.
	Conditional []string
}

// RequirementFor reports what a DNS transport of this type needs.
//
// Types that dial nothing (hosts, fakeip), delegate to another transport (group), or use a
// transport that is not the outbound namespace (local/system, dhcp, mdns link-local) report no
// requirement: their detour, if any, is not the thing carrying the lookup, so constraining it would
// be inventing a rule the runtime does not have.
func RequirementFor(transportType string, options any) TransportRequirement {
	switch transportType {
	case "udp":
		// # Why TCP is Conditional and NOT a startup requirement
		//
		// The query goes out over UDP and the TCP retry happens ONLY when the response arrives with
		// the truncation bit set (dns/transport/udp.go; see client_truncate.go). An ordinary response
		// completes over UDP alone, so a UDP-only detour answers every normal query and loses only the
		// retry for an oversized answer.
		//
		// Refusing that configuration at startup would reject a working resolver to demand a
		// redundancy it never had to declare - "a retry may happen" is not "the initial transport is
		// unusable without the retry network".
		return TransportRequirement{
			AllOf:       []string{C.NetworkUDP},
			Conditional: []string{C.NetworkTCP},
		}
	case "tcp", "tls":
		return TransportRequirement{AllOf: []string{C.NetworkTCP}}
	case "quic":
		// DoQ is QUIC: UDP only.
		return TransportRequirement{AllOf: []string{C.NetworkUDP}}
	case "h3":
		// DoH3 is HTTP/3: UDP only.
		return TransportRequirement{AllOf: []string{C.NetworkUDP}}
	case "https":
		// DoH rides TCP, and ONLY TCP: dns/transport/https_transport.go dials N.NetworkTCP at :37,
		// :54 and :61 with no QUIC path in that file. `h3` is a SEPARATE transport type
		// (constant.DNSTypeHTTP3), so "https might use H3" is false for this tree and modelling it
		// would be inventing a branch the implementation does not have.
		return TransportRequirement{AllOf: []string{C.NetworkTCP}}
	default:
		// local, hosts, fakeip, group, dhcp, mdns, resolved, tailscale, openconnect, openvpn, and
		// anything out of tree. Unmodelled rather than guessed: these either dial nothing, delegate
		// to another transport, or reach the network through an endpoint rather than through the
		// outbound namespace a detour names. Constraining their detour would be inventing a rule the
		// runtime does not have.
		return TransportRequirement{}
	}
}
