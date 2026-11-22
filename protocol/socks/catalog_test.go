package socks

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

func TestCatalogEncodeDecodeCorpus(t *testing.T) {
	corpus := []*option.SOCKSOutboundOptions{
		{
			ServerOptions: option.ServerOptions{Server: "192.0.2.1", ServerPort: 1080},
			Version:       "5",
			Username:      "user",
			Password:      "pass",
			Network:       option.NetworkList(N.NetworkTCP),
		},
		{
			ServerOptions: option.ServerOptions{Server: "192.0.2.2", ServerPort: 1080},
			Version:       "4a",
			Network:       option.NetworkList(N.NetworkTCP + "\n" + N.NetworkUDP),
		},
	}
	for i, original := range corpus {
		payload, facts, ok := CatalogProtocol().Encode(context.Background(), original)
		require.True(t, ok, "case %d must be admitted", i)
		require.NotEmpty(t, payload)
		require.Equal(t, original.Network.Build(), facts.Network)
		var wire catalogSocksWire
		require.NoError(t, json.Unmarshal(payload, &wire))
		rebuilt := wire.value()
		require.True(t, reflect.DeepEqual(*original, rebuilt), "case %d: rebuild must be value-equal", i)
	}
}

func TestCatalogRefusesIneligibleSocksShapes(t *testing.T) {
	base := func() *option.SOCKSOutboundOptions {
		return &option.SOCKSOutboundOptions{
			ServerOptions: option.ServerOptions{Server: "192.0.2.1", ServerPort: 1080},
			Version:       "5",
		}
	}
	cases := map[string]func(*option.SOCKSOutboundOptions){
		"version": func(o *option.SOCKSOutboundOptions) { o.Version = "3" },
		"preconnect": func(o *option.SOCKSOutboundOptions) {
			o.TCPPreconnect = &option.SOCKSOutboundPreconnectOptions{Enabled: true}
		},
		"uot":    func(o *option.SOCKSOutboundOptions) { o.UDPOverTCP = &option.UDPOverTCPOptions{Enabled: true} },
		"detour": func(o *option.SOCKSOutboundOptions) { o.DialerOptions.Detour = "other" },
		"domain-resolver": func(o *option.SOCKSOutboundOptions) {
			o.DialerOptions.DomainResolver = &option.DomainResolveOptions{Server: "dns"}
		},
	}
	for name, mutate := range cases {
		options := base()
		mutate(options)
		_, _, ok := CatalogProtocol().Encode(context.Background(), options)
		require.False(t, ok, "%s must fall back", name)
	}
}

func TestCatalogBuildsRuntime(t *testing.T) {
	original := socksCatalogOptions()
	payload, _, ok := CatalogProtocol().Encode(context.Background(), original)
	require.True(t, ok)
	object, err := CatalogProtocol().Build(context.Background(), nil, log.NewNOPFactory().Logger(), "catalog-build", payload)
	require.NoError(t, err)
	require.NotNil(t, object)
}

func socksCatalogOptions() *option.SOCKSOutboundOptions {
	options := &option.SOCKSOutboundOptions{
		ServerOptions: option.ServerOptions{Server: "192.0.2.1", ServerPort: 443},
	}
	options.Version = "5"
	return options
}
