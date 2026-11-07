package provider

import (
	"context"
	"errors"
	"net"
	"os"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/endpoint"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/service"
	"github.com/stretchr/testify/require"
)

type localTestNodeOptions struct{}

type localTestNode struct {
	outbound.Adapter
}

func (n *localTestNode) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return nil, errors.New("not dialable in this test")
}

func (n *localTestNode) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("not dialable in this test")
}

func newLocalHarness(t *testing.T, path string) (*ProviderLocal, *outbound.Manager) {
	t.Helper()
	ctx := service.ContextWithPtr(context.Background(), urltest.NewHistoryStorage())
	registry := outbound.NewRegistry()
	outbound.Register[localTestNodeOptions](registry, "test", func(_ context.Context, _ adapter.Router, _ log.ContextLogger, tag string, _ localTestNodeOptions) (adapter.Outbound, error) {
		return &localTestNode{Adapter: outbound.NewAdapter("test", tag, []string{"tcp"}, nil)}, nil
	})
	ctx = service.ContextWith[option.OutboundOptionsRegistry](ctx, registry)
	ctx = service.ContextWith[adapter.OutboundRegistry](ctx, registry)
	endpointManager := endpoint.NewManager(endpoint.NewRegistry())
	manager := outbound.NewManager(registry, endpointManager, "")
	manager.Initialize(func() (adapter.Outbound, error) {
		return &localTestNode{Adapter: outbound.NewAdapter("test", "default", []string{"tcp"}, nil)}, nil
	})
	service.MustRegister[adapter.OutboundManager](ctx, manager)
	scope := adapter.NewScope(ctx, log.NewNOPFactory().Logger())
	t.Cleanup(func() { _ = scope.Close() })
	for _, stage := range adapter.ListStartStages {
		require.NoError(t, scope.Start("endpoint", endpointManager, stage))
		require.NoError(t, scope.Start("outbound", manager, stage))
	}
	created, err := NewProviderLocal(ctx, nil, log.NewNOPFactory(), "p", option.ProviderLocalOptions{Path: path})
	require.NoError(t, err)
	return created.(*ProviderLocal), manager
}

// TestProviderLocalReloadsPublishedNodes is the file-watch path: a document change must reach the
// outbound manager without a restart, an invalid edit must keep the last known good set, and a
// deleted file must not empty the provider.
func TestProviderLocalReloadsPublishedNodes(t *testing.T) {
	path := t.TempDir() + "/nodes.json"
	require.NoError(t, os.WriteFile(path, []byte(`{"outbounds": [{"type": "test", "tag": "a"}]}`), 0o644))
	provider, _ := newLocalHarness(t, path)
	startContext := adapter.NewHTTPStartContext()
	require.NoError(t, provider.StartContext(context.Background(), startContext))
	startContext.Close()
	t.Cleanup(func() { _ = provider.Close() })
	require.Equal(t, []string{"p/a"}, localTags(provider))

	// The path is written with forward slashes above on purpose: on Windows fswatch compares event
	// names against the configured string byte for byte, so a watcher fed the raw configuration
	// spelling would be installed and never fire. This test is the regression guard for that.
	require.NoError(t, os.WriteFile(path, []byte(`{"outbounds": [{"type": "test", "tag": "a"}, {"type": "test", "tag": "b"}]}`), 0o644))
	require.Eventually(t, func() bool {
		return len(provider.Outbounds()) == 2
	}, 5*time.Second, 20*time.Millisecond, "the file watch did not publish the edited document")

	require.NoError(t, os.WriteFile(path, []byte(`{"outbounds": [`), 0o644))
	time.Sleep(300 * time.Millisecond)
	require.Equal(t, []string{"p/a", "p/b"}, localTags(provider), "an invalid edit must keep the published nodes")

	require.NoError(t, os.Remove(path))
	time.Sleep(300 * time.Millisecond)
	require.Equal(t, []string{"p/a", "p/b"}, localTags(provider), "a missing file must keep the published nodes")
}

func localTags(provider *ProviderLocal) []string {
	tags := make([]string, 0, len(provider.Outbounds()))
	for _, node := range provider.Outbounds() {
		tags = append(tags, node.Tag())
	}
	return tags
}
