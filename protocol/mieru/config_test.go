package mieru

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/sagernet/sing-box/option"

	mierutp "github.com/enfein/mieru/v3/apis/trafficpattern"
	mierupb "github.com/enfein/mieru/v3/pkg/appctl/appctlpb"
	"google.golang.org/protobuf/proto"

	"github.com/stretchr/testify/require"
)

func validOutboundOptions() option.MieruOutboundOptions {
	return option.MieruOutboundOptions{
		ServerOptions: option.ServerOptions{
			Server:     "203.0.113.10",
			ServerPort: 8964,
		},
		UserName: "alice",
		Password: "correct horse battery staple",
	}
}

func TestBuildClientConfigDefaults(t *testing.T) {
	config, err := buildClientConfig(validOutboundOptions(), nil, nil, nil)
	require.NoError(t, err)
	profile := config.Profile
	require.Equal(t, "sing-box", profile.GetProfileName())
	require.Equal(t, "alice", profile.GetUser().GetName())
	require.Equal(t, "correct horse battery staple", profile.GetUser().GetPassword())
	require.Len(t, profile.GetServers(), 1)
	require.Equal(t, "203.0.113.10", profile.GetServers()[0].GetIpAddress())
	require.Len(t, profile.GetServers()[0].GetPortBindings(), 1)
	require.Equal(t, int32(8964), profile.GetServers()[0].GetPortBindings()[0].GetPort())
	require.Equal(t, mierupb.TransportProtocol_TCP, profile.GetServers()[0].GetPortBindings()[0].GetProtocol())
	require.Nil(t, profile.GetMultiplexing(), "an unset multiplexing field must leave the SDK default")
	require.Nil(t, profile.HandshakeMode, "an unset handshake mode must leave the SDK default")
	require.Zero(t, profile.GetMtu())
	require.Nil(t, profile.GetTrafficPattern())
	require.True(t, config.DNSConfig.BypassDialerDNS)
	require.NotNil(t, config.DNSConfig)
	require.Nil(t, config.Dialer)
	require.Nil(t, config.PacketDialer)
	require.Nil(t, config.Resolver)
}

func TestBuildClientConfigAllFields(t *testing.T) {
	pattern := &mierupb.TrafficPattern{Seed: proto.Int32(7)}
	encodedPattern := mierutp.Encode(pattern)
	require.NotEmpty(t, encodedPattern)
	options := validOutboundOptions()
	options.Server = "example.com."
	options.ServerPortRanges = []string{"8000-8002"}
	options.Transport = "both"
	options.Multiplexing = "HIGH"
	options.HandshakeMode = "no-wait"
	options.MTU = 1400
	options.TrafficPattern = encodedPattern

	config, err := buildClientConfig(options, nil, nil, nil)
	require.NoError(t, err)
	profile := config.Profile
	server := profile.GetServers()[0]
	require.Equal(t, "example.com", server.GetDomainName(), "the server domain must be canonicalized")
	require.Empty(t, server.GetIpAddress())
	require.Len(t, server.GetPortBindings(), 4, "two transports on the primary port and on the range")
	require.Equal(t, mierupb.MultiplexingLevel_MULTIPLEXING_HIGH, profile.GetMultiplexing().GetLevel())
	require.Equal(t, mierupb.HandshakeMode_HANDSHAKE_NO_WAIT, profile.GetHandshakeMode())
	require.Equal(t, int32(1400), profile.GetMtu())
	require.NotNil(t, profile.GetTrafficPattern())
}

func TestBuildClientConfigHashedPassword(t *testing.T) {
	options := validOutboundOptions()
	options.Password = ""
	options.HashedPassword = strings.Repeat("ab", 32)
	config, err := buildClientConfig(options, nil, nil, nil)
	require.NoError(t, err)
	require.Equal(t, strings.Repeat("ab", 32), config.Profile.GetUser().GetHashedPassword())
	require.Empty(t, config.Profile.GetUser().GetPassword())
}

func TestBuildClientConfigTransportCaseCompatibility(t *testing.T) {
	for configured, want := range map[string]mierupb.TransportProtocol{
		"":     mierupb.TransportProtocol_TCP,
		"TCP":  mierupb.TransportProtocol_TCP,
		"tcp":  mierupb.TransportProtocol_TCP,
		"UDP":  mierupb.TransportProtocol_UDP,
		"udp":  mierupb.TransportProtocol_UDP,
		"bOtH": mierupb.TransportProtocol_TCP,
	} {
		options := validOutboundOptions()
		options.Transport = configured
		config, err := buildClientConfig(options, nil, nil, nil)
		require.NoError(t, err, "transport %q", configured)
		bindings := config.Profile.GetServers()[0].GetPortBindings()
		require.NotEmpty(t, bindings)
		require.Equal(t, want, bindings[0].GetProtocol(), "transport %q", configured)
		if strings.EqualFold(configured, "both") {
			require.Len(t, bindings, 2)
		}
	}
}

func TestBuildClientConfigRejectsInvalid(t *testing.T) {
	testCases := []struct {
		name    string
		mutate  func(*option.MieruOutboundOptions)
		message string
	}{
		{"empty server", func(options *option.MieruOutboundOptions) { options.Server = "" }, "server is empty"},
		{"malformed IP", func(options *option.MieruOutboundOptions) { options.Server = "999.1.1.1" }, "neither a valid IP address nor a domain"},
		{"no port", func(options *option.MieruOutboundOptions) { options.ServerPort = 0 }, "either a port or a port range"},
		{"bad range", func(options *option.MieruOutboundOptions) { options.ServerPortRanges = []string{"9000-8000"} }, "invalid port binding"},
		{"bad range syntax", func(options *option.MieruOutboundOptions) { options.ServerPortRanges = []string{"9000"} }, "invalid port binding"},
		{"bad transport", func(options *option.MieruOutboundOptions) { options.Transport = "sctp" }, "transport must be"},
		{"empty username", func(options *option.MieruOutboundOptions) { options.UserName = "" }, "user name is empty"},
		{"empty credential", func(options *option.MieruOutboundOptions) { options.Password = "" }, "neither a password nor a hashed_password"},
		{"both credentials", func(options *option.MieruOutboundOptions) { options.HashedPassword = strings.Repeat("ab", 32) }, "both password and hashed_password"},
		{"short hash", func(options *option.MieruOutboundOptions) { options.Password = ""; options.HashedPassword = "ab" }, "want 32"},
		{"non-hex hash", func(options *option.MieruOutboundOptions) {
			options.Password = ""
			options.HashedPassword = strings.Repeat("zz", 32)
		}, "not valid hexadecimal"},
		{"bad multiplexing", func(options *option.MieruOutboundOptions) { options.Multiplexing = "turbo" }, "multiplexing must be"},
		{"bad handshake", func(options *option.MieruOutboundOptions) { options.HandshakeMode = "instant" }, "handshake_mode must be"},
		{"mtu too small", func(options *option.MieruOutboundOptions) { options.MTU = 1200 }, "mtu 1200 is out of range"},
		{"mtu too large", func(options *option.MieruOutboundOptions) { options.MTU = 1600 }, "mtu 1600 is out of range"},
		{"bad traffic pattern", func(options *option.MieruOutboundOptions) { options.TrafficPattern = "not base64" }, "invalid traffic_pattern"},
		{"bad traffic pattern proto", func(options *option.MieruOutboundOptions) {
			options.TrafficPattern = base64.StdEncoding.EncodeToString([]byte{0xff, 0xff})
		}, "invalid traffic_pattern"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			options := validOutboundOptions()
			testCase.mutate(&options)
			_, err := buildClientConfig(options, nil, nil, nil)
			require.Error(t, err)
			require.Contains(t, err.Error(), testCase.message)
		})
	}
}

func validInboundOptions() option.MieruInboundOptions {
	return option.MieruInboundOptions{
		ListenOptions: option.ListenOptions{
			ListenPort: 9000,
		},
		Users: []option.MieruUser{
			{Name: "alice", Password: "alice-password"},
			{Name: "bob", Password: "bob-password"},
		},
	}
}

func TestBuildServerConfigDefaults(t *testing.T) {
	config, err := buildServerConfig(validInboundOptions())
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1", config.Config.GetListenIPAddress(), "an unset listen must use the sing-box loopback default")
	require.Len(t, config.Config.GetPortBindings(), 1)
	require.Equal(t, int32(9000), config.Config.GetPortBindings()[0].GetPort())
	require.Equal(t, mierupb.TransportProtocol_TCP, config.Config.GetPortBindings()[0].GetProtocol())
	require.Len(t, config.Config.GetUsers(), 2)
	require.Equal(t, "alice", config.Config.GetUsers()[0].GetName())
	require.Nil(t, config.Config.GetAdvancedSettings())
	require.Nil(t, config.Config.GetTrafficPattern())
	require.Zero(t, config.Config.GetMtu())
	require.Nil(t, config.StreamListenerFactory)
	require.Nil(t, config.PacketListenerFactory)
}

func TestBuildServerConfigBothTransportsAndRanges(t *testing.T) {
	options := validInboundOptions()
	options.Transport = "both"
	options.ListenPortRanges = []string{"9100-9101"}
	options.MTU = 1300
	options.UserHintIsMandatory = true
	config, err := buildServerConfig(options)
	require.NoError(t, err)
	require.Len(t, config.Config.GetPortBindings(), 4)
	require.True(t, config.Config.GetAdvancedSettings().GetUserHintIsMandatory())
	require.Equal(t, int32(1300), config.Config.GetMtu())
}

func TestBuildServerConfigRejectsInvalid(t *testing.T) {
	testCases := []struct {
		name    string
		mutate  func(*option.MieruInboundOptions)
		message string
	}{
		{"no users", func(options *option.MieruInboundOptions) { options.Users = nil }, "users is empty"},
		{"duplicate users", func(options *option.MieruInboundOptions) {
			options.Users = append(options.Users, option.MieruUser{Name: "alice", Password: "x"})
		}, "duplicate user name"},
		{"empty user name", func(options *option.MieruInboundOptions) { options.Users[0].Name = "" }, "user name is empty"},
		{"empty password", func(options *option.MieruInboundOptions) { options.Users[0].Password = "" }, "neither a password nor a hashed_password"},
		{"no port", func(options *option.MieruInboundOptions) { options.ListenPort = 0 }, "either a port or a port range"},
		{"bad transport", func(options *option.MieruInboundOptions) { options.Transport = "quic" }, "transport must be"},
		{"bad range", func(options *option.MieruInboundOptions) { options.ListenPortRanges = []string{"80-79"} }, "invalid port binding"},
		{"bad mtu", func(options *option.MieruInboundOptions) { options.MTU = 9000 }, "mtu 9000 is out of range"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			options := validInboundOptions()
			testCase.mutate(&options)
			_, err := buildServerConfig(options)
			require.Error(t, err)
			require.Contains(t, err.Error(), testCase.message)
		})
	}
}

func TestTransportParsing(t *testing.T) {
	for configured, want := range map[string]string{
		"":      "tcp",
		"tcp":   "tcp",
		"TCP":   "tcp",
		"udp":   "udp",
		"UDP":   "udp",
		"both":  "both",
		"BoTh":  "both",
		" tcp ": "tcp",
	} {
		transport, err := parseTransport(configured)
		require.NoError(t, err, "transport %q", configured)
		require.Equal(t, want, transport.String(), "transport %q", configured)
	}
	_, err := parseTransport("http")
	require.Error(t, err)
}

func TestEnumParsingCompatibility(t *testing.T) {
	for configured, want := range map[string]mierupb.MultiplexingLevel{
		"off":              mierupb.MultiplexingLevel_MULTIPLEXING_OFF,
		"MULTIPLEXING_OFF": mierupb.MultiplexingLevel_MULTIPLEXING_OFF,
		"low":              mierupb.MultiplexingLevel_MULTIPLEXING_LOW,
		"middle":           mierupb.MultiplexingLevel_MULTIPLEXING_MIDDLE,
		"medium":           mierupb.MultiplexingLevel_MULTIPLEXING_MIDDLE,
		"HIGH":             mierupb.MultiplexingLevel_MULTIPLEXING_HIGH,
	} {
		config, err := parseMultiplexing(configured)
		require.NoError(t, err, "multiplexing %q", configured)
		require.NotNil(t, config)
		require.Equal(t, want, config.GetLevel(), "multiplexing %q", configured)
	}
	defaultConfig, err := parseMultiplexing("default")
	require.NoError(t, err)
	require.Nil(t, defaultConfig)

	for configured, want := range map[string]mierupb.HandshakeMode{
		"standard":           mierupb.HandshakeMode_HANDSHAKE_STANDARD,
		"HANDSHAKE_STANDARD": mierupb.HandshakeMode_HANDSHAKE_STANDARD,
		"no_wait":            mierupb.HandshakeMode_HANDSHAKE_NO_WAIT,
		"no-wait":            mierupb.HandshakeMode_HANDSHAKE_NO_WAIT,
		"HANDSHAKE_NO_WAIT":  mierupb.HandshakeMode_HANDSHAKE_NO_WAIT,
	} {
		mode, err := parseHandshakeMode(configured)
		require.NoError(t, err, "handshake %q", configured)
		require.NotNil(t, mode)
		require.Equal(t, want, *mode, "handshake %q", configured)
	}
	defaultMode, err := parseHandshakeMode("")
	require.NoError(t, err)
	require.Nil(t, defaultMode)
}
