package http

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
	corpus := []*option.HTTPOutboundOptions{
		{
			ServerOptions: option.ServerOptions{Server: "192.0.2.1", ServerPort: 8080},
			Version:       1,
			Username:      "user",
			Password:      "pass",
		},
		{
			ServerOptions: option.ServerOptions{Server: "192.0.2.2", ServerPort: 443},
			Version:       1,
			Path:          "/",
			Headers:       badoption.HTTPHeader{"X-Test": {"a", "b"}},
			OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{TLS: &option.OutboundTLSOptions{
				Enabled:    true,
				ServerName: "example.com",
				ALPN:       badoption.Listable[string]{"http/1.1"},
			}},
		},
	}
	for i, original := range corpus {
		payload, facts, ok := CatalogProtocol().Encode(context.Background(), original)
		require.True(t, ok, "case %d must be admitted", i)
		require.NotEmpty(t, payload)
		require.False(t, facts.Multiplex)
		var wire catalogHTTPWire
		require.NoError(t, json.Unmarshal(payload, &wire))
		rebuilt := wire.value()
		require.True(t, reflect.DeepEqual(*original, rebuilt), "case %d: rebuild must be value-equal", i)
	}
}

func TestCatalogRefusesIneligibleHTTPShapes(t *testing.T) {
	base := func() *option.HTTPOutboundOptions {
		return &option.HTTPOutboundOptions{
			ServerOptions: option.ServerOptions{Server: "192.0.2.1", ServerPort: 8080},
			Version:       1,
		}
	}
	cases := map[string]func(*option.HTTPOutboundOptions){
		"http2": func(o *option.HTTPOutboundOptions) { o.Version = 2 },
		"http3": func(o *option.HTTPOutboundOptions) { o.Version = 3 },
		"utls":  func(o *option.HTTPOutboundOptions) { o.TLS = &option.OutboundTLSOptions{Enabled: true, Engine: "utls"} },
		"multiplex": func(o *option.HTTPOutboundOptions) {
			o.TLS = &option.OutboundTLSOptions{Enabled: true, Reality: &option.OutboundRealityOptions{}}
		},
		"detour": func(o *option.HTTPOutboundOptions) { o.DialerOptions.Detour = "other" },
	}
	for name, mutate := range cases {
		options := base()
		mutate(options)
		_, _, ok := CatalogProtocol().Encode(context.Background(), options)
		require.False(t, ok, "%s must fall back", name)
	}
}

func TestCatalogBuildsRuntime(t *testing.T) {
	original := httpCatalogOptions()
	payload, _, ok := CatalogProtocol().Encode(context.Background(), original)
	require.True(t, ok)
	object, err := CatalogProtocol().Build(context.Background(), nil, log.NewNOPFactory().Logger(), "catalog-build", payload)
	require.NoError(t, err)
	require.NotNil(t, object)
}

func httpCatalogOptions() *option.HTTPOutboundOptions {
	options := &option.HTTPOutboundOptions{
		ServerOptions: option.ServerOptions{Server: "192.0.2.1", ServerPort: 443},
		Version:       1,
	}
	return options
}
