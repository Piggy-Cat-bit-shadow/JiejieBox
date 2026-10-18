package option

import "github.com/sagernet/sing/common/json/badoption"

// MieruOutboundOptions is the client side of the Mieru protocol.
//
// The field names follow the sing-box Mieru integration that has existed in
// downstream forks (server / server_port / server_ports / transport /
// username / password / multiplexing / traffic_pattern). handshake_mode, mtu
// and hashed_password are additions that expose capabilities the official
// github.com/enfein/mieru/v3 client profile has always had but that the first
// downstream integration did not surface.
type MieruOutboundOptions struct {
	DialerOptions
	ServerOptions
	// ServerPortRanges are additional ports (or ranges such as "8000-8100")
	// the client may use to reach the same server. They are alternatives to
	// server_port, not a replacement for it: server_port stays the primary
	// endpoint and is the one route/diagnostics report.
	ServerPortRanges badoption.Listable[string] `json:"server_ports,omitempty"`
	// Transport selects the underlay: "tcp" (default), "udp", or "both".
	// "both" registers every configured port twice, one endpoint per
	// transport, and lets Mieru's scheduler choose.
	Transport string `json:"transport,omitempty" enum:"tcp,udp,both"`
	UserName  string `json:"username,omitempty"`
	// Password and HashedPassword are mutually exclusive. HashedPassword is
	// the hex-encoded form stored by mita/mieru, which lets an existing
	// deployment be migrated without putting the plaintext back on disk.
	Password       string `json:"password,omitempty"`
	HashedPassword string `json:"hashed_password,omitempty"`
	// Multiplexing is Mieru's OWN multiplexer, not sing-box's. Do not enable
	// a sing-box multiplex on top of this outbound.
	Multiplexing string `json:"multiplexing,omitempty" enum:"default,off,low,middle,high"`
	// HandshakeMode is "default" (standard, handshake completed before
	// DialContext returns), "standard", or "no_wait" (the request travels
	// with the first write). no_wait is not zero-RTT: the peer response is
	// still awaited before data is read.
	HandshakeMode string `json:"handshake_mode,omitempty" enum:"default,standard,no_wait"`
	// MTU is the L2 payload size used by Mieru's UDP transport. Zero means
	// the SDK default (1280).
	MTU uint32 `json:"mtu,omitempty"`
	// TrafficPattern is the base64 protobuf form accepted by mita/mieru.
	TrafficPattern string `json:"traffic_pattern,omitempty"`
	// Fallback optionally declares the other Mieru transport as a backup for
	// NEW sessions of this same outbound. It is disabled by default and must
	// not change the legacy single-transport or `both` paths.
	Fallback *MieruFallbackOptions `json:"fallback,omitempty"`
}

// MieruFallbackOptions is the optional TCP/UDP failover extension.
//
// Only the other transport can be selected. Credentials, multiplexing,
// handshake mode, MTU and traffic pattern are shared with the primary; a
// separate account or a second sing-box outbound is deliberately not part of
// this contract.
type MieruFallbackOptions struct {
	Enabled bool `json:"enabled,omitempty"`
	// Transport is the backup underlay. Required when Enabled is true, and it
	// must be the transport the primary does not use.
	Transport string `json:"transport,omitempty" enum:"tcp,udp"`
	// Server and ServerPort inherit the primary endpoint when omitted.
	// ServerPort inheritance requires the primary to have a single
	// server_port; a server_ports-only primary must set it explicitly.
	Server     string `json:"server,omitempty"`
	ServerPort uint16 `json:"server_port,omitempty"`
}

// MieruInboundOptions is the server side of the Mieru protocol.
//
// A single inbound can serve TCP and UDP on the same ports by setting
// transport to "both"; the official sing-box integration historically needed
// two inbounds for that, and two inbounds still work.
type MieruInboundOptions struct {
	ListenOptions
	Users []MieruUser `json:"users,omitempty"`
	// Transport selects the listener protocols: "tcp" (default), "udp", or
	// "both".
	Transport string `json:"transport,omitempty" enum:"tcp,udp,both"`
	// ListenPortRanges are additional ports/ranges to bind. listen_port stays
	// the primary port and the one shown in logs.
	ListenPortRanges badoption.Listable[string] `json:"listen_port_ranges,omitempty"`
	// MTU is the L2 payload size used by Mieru's UDP transport. Zero means
	// the SDK default (1280).
	MTU uint32 `json:"mtu,omitempty"`
	// TrafficPattern is the base64 protobuf form accepted by mita/mieru. It
	// must be the pattern the clients use when one is configured explicitly.
	TrafficPattern string `json:"traffic_pattern,omitempty"`
	// UserHintIsMandatory requires every client to present a user-name hint
	// that matches a configured user before a session is authenticated.
	UserHintIsMandatory bool `json:"user_hint_is_mandatory,omitempty"`
}

type MieruUser struct {
	Name string `json:"name,omitempty"`
	// Password and HashedPassword are mutually exclusive.
	Password       string `json:"password,omitempty"`
	HashedPassword string `json:"hashed_password,omitempty"`
}
