package mieru

import (
	"encoding/hex"
	"fmt"
	"net/netip"
	"strings"

	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"

	mieruclient "github.com/enfein/mieru/v3/apis/client"
	apicommon "github.com/enfein/mieru/v3/apis/common"
	mieruconstant "github.com/enfein/mieru/v3/apis/constant"
	mieruserver "github.com/enfein/mieru/v3/apis/server"
	mierutp "github.com/enfein/mieru/v3/apis/trafficpattern"
	mierucommon "github.com/enfein/mieru/v3/pkg/appctl/appctlcommon"
	mierupb "github.com/enfein/mieru/v3/pkg/appctl/appctlpb"

	"google.golang.org/protobuf/proto"
)

const (
	mieruDefaultListen = "127.0.0.1"
	mieruMinMTU        = 1280
	mieruMaxMTU        = 1500
)

// mieruTransport is the set of underlay protocols a Mieru endpoint uses.
type mieruTransport struct {
	tcp bool
	udp bool
}

// parseTransport accepts the values the downstream sing-box integration uses
// ("TCP"/"UDP") as well as lowercase sing-box style, and "both" for an
// endpoint that serves both protocols on the same ports.
func parseTransport(value string) (mieruTransport, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "tcp":
		return mieruTransport{tcp: true}, nil
	case "udp":
		return mieruTransport{udp: true}, nil
	case "both":
		return mieruTransport{tcp: true, udp: true}, nil
	default:
		return mieruTransport{}, fmt.Errorf("mieru: transport must be tcp, udp or both, got %q", value)
	}
}

func (t mieruTransport) protocols() []mierupb.TransportProtocol {
	protocols := make([]mierupb.TransportProtocol, 0, 2)
	if t.tcp {
		protocols = append(protocols, mierupb.TransportProtocol_TCP)
	}
	if t.udp {
		protocols = append(protocols, mierupb.TransportProtocol_UDP)
	}
	return protocols
}

func (t mieruTransport) String() string {
	if t.tcp && t.udp {
		return "both"
	}
	if t.udp {
		return "udp"
	}
	return "tcp"
}

// resolveFallbackOptions validates the optional failover contract and returns
// the secondary endpoint in the same shape buildClientConfig consumes.
//
// This is deliberately a pure function: NewOutbound and the config tests can
// reject an invalid combination before any dialer, DNS resolver or SDK client
// is created. A missing/disabled fallback returns enabled=false and keeps the
// legacy path completely untouched.
func resolveFallbackOptions(options option.MieruOutboundOptions) (option.MieruOutboundOptions, bool, error) {
	fallback := options.Fallback
	if fallback == nil || !fallback.Enabled {
		return option.MieruOutboundOptions{}, false, nil
	}
	primaryTransport, err := parseTransport(options.Transport)
	if err != nil {
		return option.MieruOutboundOptions{}, false, err
	}
	if primaryTransport.tcp && primaryTransport.udp {
		return option.MieruOutboundOptions{}, false, fmt.Errorf("mieru: fallback is not supported with transport \"both\"; choose tcp or udp explicitly")
	}
	if strings.TrimSpace(fallback.Transport) == "" {
		return option.MieruOutboundOptions{}, false, fmt.Errorf("mieru: fallback.transport is required when fallback is enabled")
	}
	secondaryTransport, err := parseTransport(fallback.Transport)
	if err != nil {
		return option.MieruOutboundOptions{}, false, E.Cause(err, "mieru: fallback.transport")
	}
	if secondaryTransport.tcp && secondaryTransport.udp {
		return option.MieruOutboundOptions{}, false, fmt.Errorf("mieru: fallback.transport must be tcp or udp, not both")
	}
	if secondaryTransport.tcp == primaryTransport.tcp && secondaryTransport.udp == primaryTransport.udp {
		return option.MieruOutboundOptions{}, false, fmt.Errorf("mieru: fallback.transport must differ from the primary transport %q", primaryTransport.String())
	}
	switch normalizeEnum(options.HandshakeMode) {
	case "NO_WAIT", "NOWAIT":
		return option.MieruOutboundOptions{}, false, fmt.Errorf("mieru: fallback is not supported with handshake_mode \"no_wait\"; use default or standard")
	}

	secondary := options
	secondary.Fallback = nil
	secondary.Transport = secondaryTransport.String()
	secondary.Server = strings.TrimSpace(fallback.Server)
	if secondary.Server == "" {
		secondary.Server = options.Server
	}
	secondary.ServerPort = fallback.ServerPort
	if secondary.ServerPort == 0 {
		if options.ServerPort == 0 {
			return option.MieruOutboundOptions{}, false, fmt.Errorf("mieru: fallback.server_port is required when the primary endpoint has no single server_port")
		}
		secondary.ServerPort = options.ServerPort
	}
	// A fallback endpoint is one explicit port; inheriting or expanding the
	// primary's ranges would silently change which secondary servers exist.
	secondary.ServerPortRanges = nil
	return secondary, true, nil
}

// buildPortBindings turns a primary port and a list of ranges into the SDK's
// port-binding form. A zero port with no ranges is rejected by the caller.
func buildPortBindings(port uint16, ranges []string, transport mieruTransport) ([]*mierupb.PortBinding, error) {
	protocols := transport.protocols()
	if len(protocols) == 0 {
		return nil, fmt.Errorf("mieru: no transport protocol selected")
	}
	bindings := make([]*mierupb.PortBinding, 0, (1+len(ranges))*len(protocols))
	for _, protocol := range protocols {
		protocolValue := protocol
		if port != 0 {
			bindings = append(bindings, &mierupb.PortBinding{
				Port:     proto.Int32(int32(port)),
				Protocol: protocolValue.Enum(),
			})
		}
		for _, portRange := range ranges {
			bindings = append(bindings, &mierupb.PortBinding{
				PortRange: proto.String(portRange),
				Protocol:  protocolValue.Enum(),
			})
		}
	}
	if len(bindings) == 0 {
		return nil, fmt.Errorf("mieru: either a port or a port range must be configured")
	}
	if _, err := mierucommon.FlatPortBindings(bindings); err != nil {
		return nil, E.Cause(err, "mieru: invalid port binding")
	}
	return bindings, nil
}

// parseMultiplexing maps a level name to the SDK enum. An empty value and
// "default" both mean "let the SDK use its default", which is represented by
// a nil config.
func parseMultiplexing(value string) (*mierupb.MultiplexingConfig, error) {
	var level mierupb.MultiplexingLevel
	switch normalizeEnum(value) {
	case "", "DEFAULT", "MULTIPLEXING_DEFAULT":
		return nil, nil
	case "OFF", "MULTIPLEXING_OFF":
		level = mierupb.MultiplexingLevel_MULTIPLEXING_OFF
	case "LOW", "MULTIPLEXING_LOW":
		level = mierupb.MultiplexingLevel_MULTIPLEXING_LOW
	case "MIDDLE", "MEDIUM", "MULTIPLEXING_MIDDLE":
		level = mierupb.MultiplexingLevel_MULTIPLEXING_MIDDLE
	case "HIGH", "MULTIPLEXING_HIGH":
		level = mierupb.MultiplexingLevel_MULTIPLEXING_HIGH
	default:
		return nil, fmt.Errorf("mieru: multiplexing must be default, off, low, middle or high, got %q", value)
	}
	return &mierupb.MultiplexingConfig{Level: level.Enum()}, nil
}

func parseHandshakeMode(value string) (*mierupb.HandshakeMode, error) {
	var mode mierupb.HandshakeMode
	switch normalizeEnum(value) {
	case "", "DEFAULT", "HANDSHAKE_DEFAULT":
		return nil, nil
	case "STANDARD", "HANDSHAKE_STANDARD":
		mode = mierupb.HandshakeMode_HANDSHAKE_STANDARD
	case "NO_WAIT", "NOWAIT", "HANDSHAKE_NO_WAIT":
		mode = mierupb.HandshakeMode_HANDSHAKE_NO_WAIT
	default:
		return nil, fmt.Errorf("mieru: handshake_mode must be default, standard or no_wait, got %q", value)
	}
	return mode.Enum(), nil
}

func normalizeEnum(value string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(value), "-", "_"))
}

func validateMTU(mtu uint32) error {
	if mtu == 0 {
		return nil
	}
	if mtu < mieruMinMTU || mtu > mieruMaxMTU {
		return fmt.Errorf("mieru: mtu %d is out of range, valid range is [%d, %d]", mtu, mieruMinMTU, mieruMaxMTU)
	}
	return nil
}

func parseTrafficPattern(value string) (*mierupb.TrafficPattern, error) {
	if value == "" {
		return nil, nil
	}
	pattern, err := mierutp.Decode(value)
	if err != nil {
		return nil, E.Cause(err, "mieru: invalid traffic_pattern")
	}
	if err = mierutp.Validate(pattern); err != nil {
		return nil, E.Cause(err, "mieru: invalid traffic_pattern")
	}
	return pattern, nil
}

// buildUser validates one credential pair and converts it to the SDK form.
//
// Exactly one of password and hashed_password must be set. The SDK itself
// accepts either and hashes the plaintext with the user name, so the hashed
// form is the way an existing mita deployment migrates without the plaintext.
func buildUser(name string, password string, hashedPassword string) (*mierupb.User, error) {
	if name == "" {
		return nil, fmt.Errorf("mieru: user name is empty")
	}
	if len(name) > mieruconstant.MaxUserNameLen {
		return nil, fmt.Errorf("mieru: user name %q exceeds %d bytes", name, mieruconstant.MaxUserNameLen)
	}
	if password == "" && hashedPassword == "" {
		return nil, fmt.Errorf("mieru: user %q has neither a password nor a hashed_password", name)
	}
	if password != "" && hashedPassword != "" {
		return nil, fmt.Errorf("mieru: user %q sets both password and hashed_password", name)
	}
	user := &mierupb.User{Name: proto.String(name)}
	if password != "" {
		if len(password) > 64 {
			return nil, fmt.Errorf("mieru: user %q password exceeds 64 bytes", name)
		}
		user.Password = proto.String(password)
		return user, nil
	}
	decoded, err := hex.DecodeString(hashedPassword)
	if err != nil {
		return nil, E.Cause(err, "mieru: user ", name, " hashed_password is not valid hexadecimal")
	}
	if len(decoded) != 32 {
		return nil, fmt.Errorf("mieru: user %q hashed_password decodes to %d bytes, want 32", name, len(decoded))
	}
	user.HashedPassword = proto.String(hashedPassword)
	return user, nil
}

// serverEndpoint converts the configured server into the SDK form.
func serverEndpoint(options option.MieruOutboundOptions, bindings []*mierupb.PortBinding) (*mierupb.ServerEndpoint, error) {
	if options.Server == "" {
		return nil, fmt.Errorf("mieru: server is empty")
	}
	server := &mierupb.ServerEndpoint{PortBindings: bindings}
	if M.IsDomainName(options.Server) {
		server.DomainName = proto.String(apicommon.NormalizeDomainName(options.Server))
		return server, nil
	}
	if _, err := netip.ParseAddr(options.Server); err != nil {
		return nil, fmt.Errorf("mieru: server %q is neither a valid IP address nor a domain name", options.Server)
	}
	server.IpAddress = proto.String(options.Server)
	return server, nil
}

// buildClientConfig validates the outbound options and produces the official
// client configuration. It does not start anything.
func buildClientConfig(options option.MieruOutboundOptions, dialer apicommon.Dialer, packetDialer apicommon.PacketDialer, resolver apicommon.DNSResolver) (*mieruclient.ClientConfig, error) {
	transport, err := parseTransport(options.Transport)
	if err != nil {
		return nil, err
	}
	bindings, err := buildPortBindings(options.ServerPort, options.ServerPortRanges, transport)
	if err != nil {
		return nil, err
	}
	server, err := serverEndpoint(options, bindings)
	if err != nil {
		return nil, err
	}
	user, err := buildUser(options.UserName, options.Password, options.HashedPassword)
	if err != nil {
		return nil, err
	}
	multiplexing, err := parseMultiplexing(options.Multiplexing)
	if err != nil {
		return nil, err
	}
	handshakeMode, err := parseHandshakeMode(options.HandshakeMode)
	if err != nil {
		return nil, err
	}
	if err = validateMTU(options.MTU); err != nil {
		return nil, err
	}
	trafficPattern, err := parseTrafficPattern(options.TrafficPattern)
	if err != nil {
		return nil, err
	}
	profile := &mierupb.ClientProfile{
		ProfileName:    proto.String("sing-box"),
		User:           user,
		Servers:        []*mierupb.ServerEndpoint{server},
		Multiplexing:   multiplexing,
		HandshakeMode:  handshakeMode,
		TrafficPattern: trafficPattern,
	}
	if options.MTU != 0 {
		profile.Mtu = proto.Int32(int32(options.MTU))
	}
	if err = mierucommon.ValidateClientConfigSingleProfile(profile); err != nil {
		return nil, E.Cause(err, "mieru: invalid client profile")
	}
	config := &mieruclient.ClientConfig{
		Profile:      profile,
		Dialer:       dialer,
		PacketDialer: packetDialer,
		Resolver:     resolver,
		DNSConfig: &apicommon.ClientDNSConfig{
			// The fork's dialer resolves the server name through the
			// configured DNS plane. Resolving it a second time inside the SDK
			// would be the second DNS control plane this integration must not
			// create, and would lose the fork's dual-stack strategy.
			BypassDialerDNS: true,
		},
	}
	return config, nil
}

// buildServerConfig validates the inbound options and produces the official
// server configuration. Listener factories are attached by NewInbound, which
// owns the scope context they need.
func buildServerConfig(options option.MieruInboundOptions) (*mieruserver.ServerConfig, error) {
	transport, err := parseTransport(options.Transport)
	if err != nil {
		return nil, err
	}
	bindings, err := buildPortBindings(options.ListenPort, options.ListenPortRanges, transport)
	if err != nil {
		return nil, err
	}
	if len(options.Users) == 0 {
		return nil, fmt.Errorf("mieru: users is empty")
	}
	users := make([]*mierupb.User, 0, len(options.Users))
	seen := make(map[string]struct{}, len(options.Users))
	for index, configuredUser := range options.Users {
		if _, duplicate := seen[configuredUser.Name]; duplicate {
			return nil, fmt.Errorf("mieru: duplicate user name %q", configuredUser.Name)
		}
		seen[configuredUser.Name] = struct{}{}
		user, err := buildUser(configuredUser.Name, configuredUser.Password, configuredUser.HashedPassword)
		if err != nil {
			return nil, E.Cause(err, "mieru: users[", index, "]")
		}
		if err = mierucommon.ValidateServerConfigSingleUser(user); err != nil {
			return nil, E.Cause(err, "mieru: users[", index, "]")
		}
		users = append(users, user)
	}
	if err = validateMTU(options.MTU); err != nil {
		return nil, err
	}
	trafficPattern, err := parseTrafficPattern(options.TrafficPattern)
	if err != nil {
		return nil, err
	}
	listenAddress := options.Listen.Build(netip.MustParseAddr(mieruDefaultListen))
	if err = mierucommon.ValidateServerListenIPAddress(listenAddress.String()); err != nil {
		return nil, E.Cause(err, "mieru: listen address")
	}
	serverConfig := &mierupb.ServerConfig{
		ListenIPAddress: proto.String(listenAddress.String()),
		PortBindings:    bindings,
		Users:           users,
		TrafficPattern:  trafficPattern,
	}
	if options.MTU != 0 {
		serverConfig.Mtu = proto.Int32(int32(options.MTU))
	}
	if options.UserHintIsMandatory {
		serverConfig.AdvancedSettings = &mierupb.ServerAdvancedSettings{
			UserHintIsMandatory: proto.Bool(true),
		}
	}
	return &mieruserver.ServerConfig{Config: serverConfig}, nil
}
