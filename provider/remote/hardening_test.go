package remote

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

type remoteTestNodeOptions struct{}

type remoteTestNode struct {
	outbound.Adapter
}

func (n *remoteTestNode) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return nil, errors.New("not dialable in this test")
}

func (n *remoteTestNode) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("not dialable in this test")
}

// newRemoteHarness builds the smallest real environment a remote provider needs: an outbound
// manager with a test node type, a URL history storage, and a provider constructed through the
// production constructor. fetch is called directly, which is also how the refresh loop calls it.
func newRemoteHarness(t *testing.T, providerURL string) (*ProviderRemote, *outbound.Manager) {
	t.Helper()
	ctx := service.ContextWithPtr(context.Background(), urltest.NewHistoryStorage())
	registry := outbound.NewRegistry()
	outbound.Register[remoteTestNodeOptions](registry, "test", func(_ context.Context, _ adapter.Router, _ log.ContextLogger, tag string, _ remoteTestNodeOptions) (adapter.Outbound, error) {
		return &remoteTestNode{Adapter: outbound.NewAdapter("test", tag, []string{"tcp", "udp"}, nil)}, nil
	})
	// Node documents are decoded through the outbound options registry, exactly as the Box does.
	ctx = service.ContextWith[option.OutboundOptionsRegistry](ctx, registry)
	ctx = service.ContextWith[adapter.OutboundRegistry](ctx, registry)
	endpointManager := endpoint.NewManager(endpoint.NewRegistry())
	manager := outbound.NewManager(registry, endpointManager, "")
	service.MustRegister[adapter.OutboundManager](ctx, manager)
	logger := log.NewNOPFactory()
	provider, err := NewProviderRemote(ctx, nil, logger, "p", option.ProviderRemoteOptions{URL: providerURL})
	require.NoError(t, err)
	return provider.(*ProviderRemote), manager
}

func TestProviderRemoteRejectsInvalidURL(t *testing.T) {
	t.Parallel()
	cases := []string{
		"",
		"not a url",
		"/local/path",
		"ftp://example.com/subscription",
		"file:///tmp/subscription",
		"http://",
	}
	for _, providerURL := range cases {
		providerURL := providerURL
		t.Run(providerURL, func(t *testing.T) {
			t.Parallel()
			_, err := NewProviderRemote(context.Background(), nil, log.NewNOPFactory(), "p", option.ProviderRemoteOptions{URL: providerURL})
			require.Error(t, err)
		})
	}
}

func TestProviderRemoteRedactsCredentials(t *testing.T) {
	t.Parallel()
	provider, _ := newRemoteHarness(t, "https://example.com/subscription?token=SECRET-TOKEN&user=alice")
	require.NotContains(t, provider.logURL, "SECRET-TOKEN")
	require.NotContains(t, provider.logURL, "alice")
	require.Contains(t, provider.logURL, "example.com/subscription")

	// The shape a transport error produces: the raw URL is embedded in the message.
	transportError := `Get "https://example.com/subscription?token=SECRET-TOKEN&user=alice": dial tcp: connection refused`
	redacted := provider.redact(transportError)
	require.NotContains(t, redacted, "SECRET-TOKEN")
	require.NotContains(t, redacted, "alice")
	require.Contains(t, redacted, "connection refused")
}

func TestProviderRemoteTransportErrorIsRedacted(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	providerURL := server.URL + "/subscription?token=SECRET-TOKEN"
	provider, _ := newRemoteHarness(t, providerURL)
	provider.httpClient = server.Client()
	server.Close()
	err := provider.fetch(context.Background(), true)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "SECRET-TOKEN")
}

func TestProviderRemoteRejectsOversizedDocument(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(strings.Repeat("x", 4096)))
	}))
	t.Cleanup(server.Close)
	provider, _ := newRemoteHarness(t, server.URL)
	provider.httpClient = server.Client()
	provider.maxDocumentSize = 1024
	err := provider.fetch(context.Background(), true)
	require.ErrorContains(t, err, "byte limit")
}

func TestProviderRemoteTimesOutSlowServer(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		select {
		case <-release:
		case <-time.After(5 * time.Second):
		}
		writer.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })
	provider, _ := newRemoteHarness(t, server.URL)
	provider.httpClient = &http.Client{Timeout: 100 * time.Millisecond}
	started := time.Now()
	err := provider.fetch(context.Background(), true)
	require.Error(t, err)
	require.Less(t, time.Since(started), 2*time.Second, "the download must be bounded by the client timeout")
}

func TestProviderRemoteRejectsErrorStatuses(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout} {
		status := status
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(status)
				_, _ = writer.Write([]byte("<html>login page</html>"))
			}))
			t.Cleanup(server.Close)
			provider, _ := newRemoteHarness(t, server.URL)
			provider.httpClient = server.Client()
			err := provider.fetch(context.Background(), true)
			require.Error(t, err)
			require.Empty(t, provider.Outbounds())
		})
	}
}

func TestProviderRemotePublishesNodesAndKeepsThemOnFailure(t *testing.T) {
	t.Parallel()
	var mode atomic.Int32 // 0: valid, 1: broken JSON, 2: server error
	document := `{"outbounds": [
		{"type": "test", "tag": "a"},
		{"type": "test", "tag": "b"},
		{"type": "direct", "tag": "skipped"}
	]}`
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		switch mode.Load() {
		case 1:
			_, _ = writer.Write([]byte(`{"outbounds": [`))
		case 2:
			writer.WriteHeader(http.StatusInternalServerError)
		default:
			_, _ = writer.Write([]byte(document))
		}
	}))
	t.Cleanup(server.Close)
	provider, _ := newRemoteHarness(t, server.URL)
	provider.httpClient = server.Client()
	require.NoError(t, provider.fetch(context.Background(), true))
	require.Equal(t, []string{"p/a", "p/b"}, providerTagsOf(provider))
	before := provider.Outbounds()

	mode.Store(1)
	require.Error(t, provider.fetch(context.Background(), false))
	require.Equal(t, before, provider.Outbounds(), "a parse failure must preserve the published set")

	mode.Store(2)
	require.Error(t, provider.fetch(context.Background(), false))
	require.Equal(t, before, provider.Outbounds(), "an error status must preserve the published set")
}

func TestProviderRemoteCacheRoundTrip(t *testing.T) {
	t.Parallel()
	document := `{"outbounds": [{"type": "test", "tag": "cached"}]}`
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(document))
	}))
	providerURL := server.URL
	cachePath := t.TempDir() + "/provider.json"
	provider, _ := newRemoteHarness(t, providerURL)
	provider.httpClient = server.Client()
	provider.path = cachePath
	require.NoError(t, provider.fetch(context.Background(), true))
	require.Equal(t, []string{"p/cached"}, providerTagsOf(provider))
	server.Close()

	restored, _ := newRemoteHarness(t, providerURL)
	restored.path = cachePath
	loaded, err := restored.loadCacheFile()
	require.NoError(t, err)
	require.True(t, loaded)
	require.Equal(t, []string{"p/cached"}, providerTagsOf(restored))
}

func TestProviderRemoteDoesNotWriteCacheOnInvalidDocument(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`not json at all`))
	}))
	t.Cleanup(server.Close)
	cachePath := t.TempDir() + "/provider.json"
	provider, _ := newRemoteHarness(t, server.URL)
	provider.httpClient = server.Client()
	provider.path = cachePath
	require.Error(t, provider.fetch(context.Background(), true))
	require.NoFileExists(t, cachePath)
}

func providerTagsOf(provider *ProviderRemote) []string {
	tags := make([]string, 0)
	for _, node := range provider.Outbounds() {
		tags = append(tags, node.Tag())
	}
	return tags
}

// TestProviderRemoteCloseDuringDownload proves that Close is not blocked behind an in-flight
// download: it cancels the provider context, which cancels the request, and then joins the worker.
func TestProviderRemoteCloseDuringDownload(t *testing.T) {
	release := make(chan struct{})
	requestStarted := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		select {
		case requestStarted <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-request.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	provider, _ := newRemoteHarness(t, server.URL)
	provider.httpClient = server.Client()

	fetchErr := make(chan error, 1)
	go func() { fetchErr <- provider.fetch(provider.ctx, true) }()
	select {
	case <-requestStarted:
	case <-time.After(2 * time.Second):
		close(release)
		server.Close()
		t.Fatal("the download did not reach the server")
	}

	closed := make(chan error, 1)
	go func() { closed <- provider.Close() }()
	select {
	case err := <-closed:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("Close waited for a download it should have cancelled")
	}
	select {
	case <-fetchErr:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("the cancelled download did not return")
	}
	close(release)
	server.Close()
}

func TestProviderRemoteCloseIsIdempotent(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"outbounds": [{"type": "test", "tag": "a"}]}`))
	}))
	releaseServer := false
	defer func() {
		if releaseServer {
			server.Close()
		}
	}()
	provider, _ := newRemoteHarness(t, server.URL)
	provider.httpClient = server.Client()
	require.NoError(t, provider.fetch(context.Background(), true))
	require.NoError(t, provider.Close())
	require.NoError(t, provider.Close())
	releaseServer = true
}
