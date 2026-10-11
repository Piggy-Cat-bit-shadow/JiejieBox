package dns

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
	// Required are the networks this transport provably must have.
	Required []string
	// Optional are networks it may use when available. Missing one is not a defect.
	Optional []string
}

// DNSTransportRequirement reports what a DNS transport of this type needs.
//
// Types that dial nothing (hosts, fakeip), delegate to another transport (group), or use a
// transport that is not the outbound namespace (local/system, dhcp, mdns link-local) report no
// requirement: their detour, if any, is not the thing carrying the lookup, so constraining it would
// be inventing a rule the runtime does not have.
func DNSTransportRequirement(transportType string, options any) TransportRequirement {
	switch transportType {
	case "udp":
		// dns/transport/udp.go: the query goes out over UDP and a TRUNCATED response is retried over
		// TCP, so both are reachable and both are required.
		return TransportRequirement{Required: []string{C.NetworkUDP, C.NetworkTCP}}
	case "tcp", "tls":
		return TransportRequirement{Required: []string{C.NetworkTCP}}
	case "quic":
		// DoQ is QUIC: UDP only.
		return TransportRequirement{Required: []string{C.NetworkUDP}}
	case "h3":
		// DoH3 is HTTP/3: UDP only.
		return TransportRequirement{Required: []string{C.NetworkUDP}}
	case "https":
		// DoH rides TCP, and ONLY TCP: dns/transport/https_transport.go dials N.NetworkTCP at :37,
		// :54 and :61 with no QUIC path in that file. `h3` is a SEPARATE transport type
		// (constant.DNSTypeHTTP3), so "https might use H3" is false for this tree and modelling it
		// as optional would be inventing a branch the implementation does not have.
		return TransportRequirement{Required: []string{C.NetworkTCP}}
	default:
		// local, hosts, fakeip, group, dhcp, mdns, resolved, tailscale, openconnect, openvpn, and
		// anything out of tree. Unmodelled rather than guessed: these either dial nothing, delegate
		// to another transport, or reach the network through an endpoint rather than through the
		// outbound namespace a detour names. Constraining their detour would be inventing a rule the
		// runtime does not have.
		return TransportRequirement{}
	}
}
