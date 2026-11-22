package trojan

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"

	"github.com/stretchr/testify/require"
)

func TestCatalogEncodeDecodeCorpus(t *testing.T) {
	corpus := []*option.TrojanOutboundOptions{
		{
			ServerOptions: option.ServerOptions{Server: "example.com", ServerPort: 443},
			Password:      "secret",
			Network:       option.NetworkList("tcp"),
			OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{TLS: &option.OutboundTLSOptions{
				Enabled:    true,
				ServerName: "example.com",
				ALPN:       badoption.Listable[string]{"h2", "http/1.1"},
			}},
		},
		{
			ServerOptions: option.ServerOptions{Server: "192.0.2.1", ServerPort: 8443},
			Password:      "p",
			Network:       option.NetworkList("tcp\nudp"),
			OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{TLS: &option.OutboundTLSOptions{
				Enabled:    true,
				Insecure:   true,
				DisableSNI: true,
			}},
			Multiplex: &option.OutboundMultiplexOptions{Enabled: false},
		},
	}
	for i, original := range corpus {
		payload, facts, ok := CatalogProtocol().Encode(context.Background(), original)
		require.True(t, ok, "case %d must be admitted", i)
		require.NotEmpty(t, payload)
		require.Equal(t, original.ServerIsDomain(), facts.ServerIsDomain)
		var wire catalogRecipe
		require.NoError(t, json.Unmarshal(payload, &wire))
		rebuilt := wire.value().outboundOptions()
		require.True(t, reflect.DeepEqual(*original, rebuilt), "case %d: rebuild must be value-equal", i)
	}
}

func TestCatalogRefusesIneligibleTrojanShapes(t *testing.T) {
	base := func() *option.TrojanOutboundOptions {
		return &option.TrojanOutboundOptions{
			ServerOptions: option.ServerOptions{Server: "example.com", ServerPort: 443},
			Password:      "secret",
			OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{TLS: &option.OutboundTLSOptions{
				Enabled: true,
			}},
		}
	}
	cases := map[string]func(*option.TrojanOutboundOptions){
		"transport":  func(o *option.TrojanOutboundOptions) { o.Transport = &option.V2RayTransportOptions{Type: "ws"} },
		"multiplex":  func(o *option.TrojanOutboundOptions) { o.Multiplex = &option.OutboundMultiplexOptions{Enabled: true} },
		"tls-engine": func(o *option.TrojanOutboundOptions) { o.TLS.Engine = "utls" },
		"detour":     func(o *option.TrojanOutboundOptions) { o.DialerOptions.Detour = "other" },
	}
	for name, mutate := range cases {
		options := base()
		mutate(options)
		_, _, ok := CatalogProtocol().Encode(context.Background(), options)
		require.False(t, ok, "%s must fall back", name)
	}
}

func TestCatalogBuildsRuntime(t *testing.T) {
	original := trojanCatalogOptions()
	payload, _, ok := CatalogProtocol().Encode(context.Background(), original)
	require.True(t, ok)
	object, err := CatalogProtocol().Build(context.Background(), nil, log.NewNOPFactory().Logger(), "catalog-build", payload)
	require.NoError(t, err)
	require.NotNil(t, object)
}

func trojanCatalogOptions() *option.TrojanOutboundOptions {
	options := &option.TrojanOutboundOptions{
		ServerOptions: option.ServerOptions{Server: "192.0.2.1", ServerPort: 443},
	}
	options.Password = "secret"
	options.TLS = &option.OutboundTLSOptions{Enabled: true, ServerName: "example.com"}
	return options
}
