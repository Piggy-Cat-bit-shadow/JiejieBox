package daemon

// P01 at the product entry: a real Box with static nodes and a real remote provider must install
// catalog descriptors by default (no test-only helper), metadata must not expand them, and the first
// dial must materialize through the existing Lazy state machine.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

func catalogSocksNode(tag string, port int) string {
	return fmt.Sprintf(`{"type": "socks", "tag": %q, "server": "127.0.0.1", "server_port": %d, "version": "5"}`, tag, port)
}

func TestNodeCatalogIsInstalledOnStaticAndProviderNodes(t *testing.T) {
	directory := t.TempDir()

	const socksCount = 20
	const httpCount = 5
	const trojanCount = 5
	const providerCount = 20
	var staticNodes, providerNodes string
	for i := 0; i < socksCount; i++ {
		if len(staticNodes) > 0 {
			staticNodes += ","
		}
		staticNodes += catalogSocksNode(fmt.Sprintf("s%d", i), 20000+i)
	}
	for i := 0; i < httpCount; i++ {
		staticNodes += fmt.Sprintf(`,{"type": "http", "tag": "h%d", "server": "127.0.0.1", "server_port": %d}`, i, 21000+i)
	}
	for i := 0; i < trojanCount; i++ {
		staticNodes += fmt.Sprintf(`,{"type": "trojan", "tag": "t%d", "server": "127.0.0.1", "server_port": %d, "password": "secret", "tls": {"enabled": true, "server_name": "example.com"}}`, i, 22000+i)
	}
	for i := 0; i < providerCount; i++ {
		if len(providerNodes) > 0 {
			providerNodes += ","
		}
		providerNodes += catalogSocksNode(fmt.Sprintf("p%d", i), 30000+i)
	}
	document := `{"outbounds": [` + providerNodes + `]}`

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(document))
	}))
	t.Cleanup(server.Close)

	profile := fmt.Sprintf(`{
  "log": {"disabled": true},
  "providers": [{"type": "remote", "tag": "nodes", "url": %q}],
  "outbounds": [
    %s,
    {"type": "selector", "tag": "proxy", "providers": ["nodes"]},
    {"type": "direct", "tag": "direct"}
  ],
  "route": {"rules": [{"domain": ["unused.example"], "outbound": "proxy"}], "final": "direct"}
}`, server.URL, staticNodes)

	ctx := crossSystemContext(t, directory, nil)
	service := NewStartedService(ServiceOptions{Context: ctx})
	t.Cleanup(func() {
		_ = service.CloseService()
		service.Close()
	})
	require.NoError(t, service.StartOrReloadService(context.Background(), profile, nil))
	instance := service.Instance()
	require.NotNil(t, instance)
	box := instance.Box()
	require.NotNil(t, box)

	require.Eventually(t, func() bool {
		return box.CatalogStats().Records == socksCount+httpCount+providerCount
	}, 10*time.Second, 10*time.Millisecond, "every static and provider node must get one shared record")
	stats := box.CatalogStats()
	require.False(t, stats.Closed)

	// Metadata is answered from the descriptor; it must not expand the node or build a runtime.
	require.Zero(t, box.DeferredExpansionCount())
	tags := make([]string, 0, 40)
	for _, object := range box.Outbound().Outbounds() {
		tags = append(tags, object.Tag())
	}
	t.Logf("outbound tags: %v", tags)
	for _, tag := range []string{"s0", "s19", "h0", "t0", "nodes/p0", "nodes/p19"} {
		object, loaded := box.Outbound().Outbound(tag)
		require.True(t, loaded, tag)
		if tag == "h0" {
			require.Equal(t, "http", object.Type())
		} else if tag == "t0" {
			require.Equal(t, "trojan", object.Type())
		} else {
			require.Equal(t, "socks", object.Type())
		}
		require.Equal(t, tag, object.Tag())
		require.Equal(t, []string{"tcp", "udp"}, object.Network())
	}
	require.Zero(t, box.DeferredExpansionCount(), "a metadata walk must not expand a catalog node")
	require.Equal(t, socksCount+httpCount+providerCount, box.CatalogStats().Records)

	// The first dial materializes through the existing Lazy state machine. The local port is closed,
	// so the dial fails, but the build must have run and the node remains a ready expansion.
	destination := M.SocksaddrFrom(netip.MustParseAddr("127.0.0.1"), 1)
	ctxDial, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	object, loaded := box.Outbound().Outbound("s0")
	require.True(t, loaded)
	_, dialErr := object.DialContext(ctxDial, "tcp", destination)
	require.Error(t, dialErr)
	require.Equal(t, 1, box.DeferredExpansionCount(), "the used node is now expanded")

	// The production release path is the scope drain: every descriptor must release its record when
	// the Box closes, not only when a test calls Close directly.
	require.NoError(t, service.CloseService())
	closed := box.CatalogStats()
	require.True(t, closed.Closed, "the store is closed after the scope drain")
	require.Zero(t, closed.Records, "every descriptor released its record")
	require.Zero(t, closed.Refs)
}
