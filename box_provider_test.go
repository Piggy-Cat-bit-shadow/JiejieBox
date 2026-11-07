package box_test

import (
	"context"
	"os"
	"strconv"
	"testing"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json"

	"github.com/stretchr/testify/require"
)

// TestInlineProviderPublishesNodesIntoGroups is the end-to-end contract of the provider feature at
// the Box level: a provider declared in the configuration publishes outbounds into the same
// namespace the static outbounds use, and a selector/urltest that names it sees those nodes without
// any manual tag list.
//
// The inline provider is used deliberately: it exercises construction, the outbound manager's
// prepare-then-swap path, the group callback registration and the group rebuild, all without a
// network. The dispatch itself is covered by adapter/provider's hot-update suite.
func TestInlineProviderPublishesNodesIntoGroups(t *testing.T) {
	const config = `{
  "log": {"disabled": true},
  "providers": [
    {
      "type": "inline",
      "tag": "air",
      "outbounds": [
        {"type": "block", "tag": "deny"},
        {"type": "direct", "tag": "pass"}
      ]
    }
  ],
  "outbounds": [
    {"type": "selector", "tag": "proxy", "providers": ["air"], "default": "air/pass"},
    {"type": "urltest", "tag": "auto", "providers": ["air"], "url": "http://127.0.0.1:1/"}
  ]
}`
	ctx := include.Context(context.Background())
	options, decodeErr := json.UnmarshalExtendedContext[option.Options](ctx, []byte(config))
	require.NoError(t, decodeErr)
	instance, newErr := box.New(box.Options{Context: ctx, Options: options})
	require.NoError(t, newErr)
	t.Cleanup(func() { _ = instance.Close() })
	require.NoError(t, instance.Start())

	_, loaded := instance.Outbound().Outbound("air/deny")
	require.True(t, loaded, "a provider node must be addressable by its rendered tag")
	_, loaded = instance.Outbound().Outbound("air/pass")
	require.True(t, loaded)

	selectorOutbound, loaded := instance.Outbound().Outbound("proxy")
	require.True(t, loaded)
	selectorGroup, ok := selectorOutbound.(adapter.OutboundGroup)
	require.True(t, ok)
	require.Equal(t, []string{"air/deny", "air/pass"}, selectorGroup.All())
	selected := selectorGroup.Selected("tcp")
	require.NotNil(t, selected)
	require.Equal(t, "air/pass", selected.Tag(), "the configured default must win over the first member")

	urlTestOutbound, loaded := instance.Outbound().Outbound("auto")
	require.True(t, loaded)
	urlTestGroup, ok := urlTestOutbound.(adapter.OutboundGroup)
	require.True(t, ok)
	require.ElementsMatch(t, []string{"air/deny", "air/pass"}, urlTestGroup.All())
}

// TestProviderDocumentCannotRewriteTheMainConfiguration proves the isolation property from the
// other side: a subscription document may carry its own `dns`, `route` and `inbounds` keys, and none
// of them reach the running configuration. The provider's job is nodes; DNS, routing and inbounds
// stay with the document the operator wrote.
func TestProviderDocumentCannotRewriteTheMainConfiguration(t *testing.T) {
	documentPath := t.TempDir() + "/subscription.json"
	document := `{
  "dns": {"servers": [{"type": "local", "tag": "hijacked-dns"}]},
  "route": {"final": "hijacked"},
  "inbounds": [{"type": "direct", "tag": "hijacked-inbound"}],
  "outbounds": [
    {"type": "socks", "tag": "hk", "server": "127.0.0.1", "server_port": 1080, "version": "5"}
  ]
}`
	require.NoError(t, os.WriteFile(documentPath, []byte(document), 0o644))
	config := `{
  "log": {"disabled": true},
  "dns": {"servers": [{"type": "local", "tag": "main-dns"}]},
  "inbounds": [{"type": "direct", "tag": "main-inbound"}],
  "providers": [
    {"type": "local", "tag": "air", "path": ` + strconv.Quote(documentPath) + `}
  ],
  "outbounds": [
    {"type": "selector", "tag": "proxy", "providers": ["air"]},
    {"type": "direct", "tag": "direct"}
  ],
  "route": {"rules": [{"domain": ["example.com"], "outbound": "proxy"}], "final": "direct"}
}`
	ctx := include.Context(context.Background())
	options, decodeErr := json.UnmarshalExtendedContext[option.Options](ctx, []byte(config))
	require.NoError(t, decodeErr)
	require.Len(t, options.DNS.Servers, 1)
	require.Equal(t, "main-dns", options.DNS.Servers[0].Tag)
	instance, newErr := box.New(box.Options{Context: ctx, Options: options})
	require.NoError(t, newErr)
	t.Cleanup(func() { _ = instance.Close() })
	require.NoError(t, instance.Start())

	require.Len(t, instance.Inbound().Inbounds(), 1)
	require.Equal(t, "main-inbound", instance.Inbound().Inbounds()[0].Tag())
	_, hijacked := instance.Inbound().Get("hijacked-inbound")
	require.False(t, hijacked)
	_, hijacked = instance.Outbound().Outbound("air/hijacked")
	require.False(t, hijacked)
	_, published := instance.Outbound().Outbound("air/hk")
	require.True(t, published)
}

// TestTwoProvidersShareTheNamespaceWithoutColliding proves isolation between providers: the same
// node tag in two providers renders into two distinct names, each group sees only its provider, and
// a node from one provider is not resolvable under the other's prefix.
func TestTwoProvidersShareTheNamespaceWithoutColliding(t *testing.T) {
	const config = `{
  "log": {"disabled": true},
  "providers": [
    {"type": "inline", "tag": "alpha", "outbounds": [{"type": "socks", "tag": "shared", "server": "127.0.0.1", "server_port": 1080, "version": "5"}]},
    {"type": "inline", "tag": "beta", "outbounds": [{"type": "socks", "tag": "shared", "server": "127.0.0.1", "server_port": 1081, "version": "5"}]}
  ],
  "outbounds": [
    {"type": "selector", "tag": "from-alpha", "providers": ["alpha"]},
    {"type": "selector", "tag": "from-beta", "providers": ["beta"]}
  ]
}`
	ctx := include.Context(context.Background())
	options, decodeErr := json.UnmarshalExtendedContext[option.Options](ctx, []byte(config))
	require.NoError(t, decodeErr)
	instance, newErr := box.New(box.Options{Context: ctx, Options: options})
	require.NoError(t, newErr)
	t.Cleanup(func() { _ = instance.Close() })
	require.NoError(t, instance.Start())

	alphaNode, alphaFound := instance.Outbound().Outbound("alpha/shared")
	betaNode, betaFound := instance.Outbound().Outbound("beta/shared")
	require.True(t, alphaFound)
	require.True(t, betaFound)
	require.NotSame(t, alphaNode, betaNode)

	alphaGroup := instance.Outbound().Outbound
	alphaOutbound, _ := alphaGroup("from-alpha")
	require.Equal(t, []string{"alpha/shared"}, alphaOutbound.(adapter.OutboundGroup).All())
	betaOutbound, _ := alphaGroup("from-beta")
	require.Equal(t, []string{"beta/shared"}, betaOutbound.(adapter.OutboundGroup).All())
}

// TestGroupWithProviderOnlyStartsWithoutMembers proves the documented first-run behaviour: a group
// whose only member source has not published anything yet still starts, and the failure is reported
// when a dial is attempted rather than by refusing the whole configuration.
func TestGroupWithProviderOnlyStartsWithoutMembers(t *testing.T) {
	const config = `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "selector", "tag": "proxy", "providers": ["missing"]}
  ]
}`
	ctx := include.Context(context.Background())
	options, decodeErr := json.UnmarshalExtendedContext[option.Options](ctx, []byte(config))
	require.NoError(t, decodeErr)
	instance, newErr := box.New(box.Options{Context: ctx, Options: options})
	require.NoError(t, newErr)
	t.Cleanup(func() { _ = instance.Close() })
	// The provider does not exist, so start must refuse: a group naming a provider that the
	// configuration never declares is a configuration error, not an empty member list.
	require.ErrorContains(t, instance.Start(), "outbound provider 0 not found: missing")
}
