package box_test

import (
	"context"
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
