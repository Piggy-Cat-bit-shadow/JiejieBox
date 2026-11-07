package remote

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestProviderRemoteCacheAppliesTheSamePipeline proves that restoring a cache is the same
// operation as applying a download: filters, tag renaming and the format readers must all run. A
// cache restore that skipped them would bring back nodes the current configuration excludes.
func TestProviderRemoteCacheAppliesTheSamePipeline(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"outbounds": [
			{"type": "test", "tag": "hk"},
			{"type": "test", "tag": "expired"},
			{"type": "test", "tag": "hk"}
		]}`))
	}))
	providerURL := server.URL
	cachePath := t.TempDir() + "/provider.json"
	provider, _ := newRemoteHarness(t, providerURL)
	provider.httpClient = server.Client()
	provider.path = cachePath
	provider.include = regexp.MustCompile(`^(hk|expired)$`)
	provider.exclude = regexp.MustCompile(`expired`)
	require.NoError(t, provider.fetch(context.Background(), true))
	require.Equal(t, []string{"p/hk", "p/hk (2)"}, providerTagsOf(provider))
	server.Close()

	restored, _ := newRemoteHarness(t, providerURL)
	restored.path = cachePath
	restored.include = regexp.MustCompile(`^(hk|expired)$`)
	restored.exclude = regexp.MustCompile(`expired`)
	loaded, err := restored.loadCacheFile()
	require.NoError(t, err)
	require.True(t, loaded)
	require.Equal(t, []string{"p/hk", "p/hk (2)"}, providerTagsOf(restored),
		"a restored cache must pass through include/exclude and tag renaming")
}

func TestProviderRemoteCorruptCacheIsRefused(t *testing.T) {
	t.Parallel()
	cachePath := t.TempDir() + "/provider.json"
	require.NoError(t, os.WriteFile(cachePath, []byte("\x00\x01 not a subscription"), 0o644))
	provider, _ := newRemoteHarness(t, "https://example.com/sub")
	provider.path = cachePath
	loaded, err := provider.loadCacheFile()
	require.Error(t, err)
	require.False(t, loaded)
	require.Empty(t, provider.Outbounds())
	require.NoFileExists(t, cachePath+".tmp", "a failed restore must not leave a temp file behind")
}

// TestProviderRemoteCacheKeepsSubscriptionInfo covers the header line the cache prepends: it must be
// stripped before parsing, and the traffic figures must survive the restore.
func TestProviderRemoteCacheKeepsSubscriptionInfo(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("subscription-userinfo", "upload=11; download=22; total=33; expire=44")
		_, _ = writer.Write([]byte(`{"outbounds": [{"type": "test", "tag": "a"}]}`))
	}))
	providerURL := server.URL
	cachePath := t.TempDir() + "/provider.json"
	provider, _ := newRemoteHarness(t, providerURL)
	provider.httpClient = server.Client()
	provider.path = cachePath
	require.NoError(t, provider.fetch(context.Background(), true))
	info := provider.SubscriptionInfo()
	require.Equal(t, int64(33), info.Total)
	server.Close()

	restored, _ := newRemoteHarness(t, providerURL)
	restored.path = cachePath
	loaded, err := restored.loadCacheFile()
	require.NoError(t, err)
	require.True(t, loaded)
	require.Equal(t, []string{"p/a"}, providerTagsOf(restored))
	require.Equal(t, int64(33), restored.SubscriptionInfo().Total)
}
