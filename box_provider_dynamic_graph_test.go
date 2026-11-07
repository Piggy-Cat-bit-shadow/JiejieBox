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
	singjson "github.com/sagernet/sing/common/json"

	"github.com/stretchr/testify/require"
)

func newBoxFromJSON(t *testing.T, config string) (*box.Box, error) {
	t.Helper()
	ctx := include.Context(context.Background())
	options, decodeErr := singjson.UnmarshalExtendedContext[option.Options](ctx, []byte(config))
	require.NoError(t, decodeErr)
	return box.New(box.Options{Context: ctx, Options: options})
}

func writeProviderFile(t *testing.T, content string) string {
	t.Helper()
	path := t.TempDir() + "/nodes.json"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func quotePath(value string) string {
	return strconv.Quote(value)
}

// TestProviderOnlyDetourStartsOnceTheProviderPublishes is the reported defect: a static
// outbound dials through a selector whose only members come from a local provider. The
// provider has not published at the moment the start-time dry run walks the graph, so the
// route looks truncated and the Box refuses to start even though the document is valid.
func TestProviderOnlyDetourStartsOnceTheProviderPublishes(t *testing.T) {
	nodesPath := writeProviderFile(t, `{"outbounds": [
		{"type": "socks", "tag": "exit", "server": "127.0.0.1", "server_port": 1081, "version": "5"}
	]}`)
	config := `{
  "log": {"disabled": true},
  "providers": [{"type": "local", "tag": "p", "path": ` + quotePath(nodesPath) + `}],
  "outbounds": [
    {"type": "selector", "tag": "g", "providers": ["p"]},
    {"type": "socks", "tag": "a", "server": "127.0.0.1", "server_port": 1080, "version": "5", "detour": "g"},
    {"type": "direct", "tag": "direct"}
  ],
  "route": {"final": "direct"}
}`
	instance, err := newBoxFromJSON(t, config)
	require.NoError(t, err)
	t.Cleanup(func() { _ = instance.Close() })
	require.NoError(t, instance.Start())

	groupOutbound, loaded := instance.Outbound().Outbound("g")
	require.True(t, loaded)
	group, isGroup := groupOutbound.(adapter.OutboundGroup)
	require.True(t, isGroup)
	require.Equal(t, []string{"p/exit"}, group.All())
	require.NotNil(t, group.Selected("tcp"))
}

// TestDynamicProviderNodeCannotCloseACycle is the second report: the static cycle
// g -> a -> g is refused, but publishing `a` from a provider with `a.detour = g` starts.
func TestDynamicProviderNodeCannotCloseACycle(t *testing.T) {
	nodesPath := writeProviderFile(t, `{"outbounds": [
		{"type": "socks", "tag": "a", "server": "127.0.0.1", "server_port": 1080, "version": "5"}
	]}`)
	config := `{
  "log": {"disabled": true},
  "providers": [{"type": "local", "tag": "p", "path": ` + quotePath(nodesPath) + `,
    "override_dialer": {"detour": "g"}}],
  "outbounds": [
    {"type": "selector", "tag": "g", "providers": ["p"]},
    {"type": "direct", "tag": "direct"}
  ],
  "route": {"final": "direct"}
}`
	instance, err := newBoxFromJSON(t, config)
	if err == nil {
		defer instance.Close()
		err = instance.Start()
	}
	require.Error(t, err)
	require.Contains(t, err.Error(), "circular")
}

func TestStaticCycleIsRefused(t *testing.T) {
	config := `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "selector", "tag": "g", "outbounds": ["a"]},
    {"type": "socks", "tag": "a", "server": "127.0.0.1", "server_port": 1080, "version": "5", "detour": "g"},
    {"type": "direct", "tag": "direct"}
  ],
  "route": {"final": "direct"}
}`
	instance, err := newBoxFromJSON(t, config)
	if err == nil {
		defer instance.Close()
		err = instance.Start()
	}
	require.Error(t, err)
	require.Contains(t, err.Error(), "circular")
}
