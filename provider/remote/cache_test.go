package remote

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing-box/adapter"

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

// TestProviderRemoteNotModifiedWithoutContentLeavesCacheAlone covers a spurious 304 before any
// document was loaded (a broken server can answer one even without If-None-Match). Rewriting the
// path cache in that state used to replace the valid file with an empty outbound list.
func TestProviderRemoteNotModifiedWithoutContentLeavesCacheAlone(t *testing.T) {
	t.Parallel()
	path := t.TempDir() + "/provider.json"
	original := `{"outbounds": [{"type": "test", "tag": "a"}]}`
	require.NoError(t, os.WriteFile(path, []byte(original), 0o644))
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Etag", `"v1"`)
		writer.WriteHeader(http.StatusNotModified)
	}))
	t.Cleanup(server.Close)
	provider, _ := newRemoteHarness(t, server.URL)
	provider.httpClient = server.Client()
	provider.path = path
	require.NoError(t, provider.fetch(context.Background(), true))
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, original, string(after))
}

type failingSubscriptionCache struct {
	adapter.CacheFile
	saves int
}

func (c *failingSubscriptionCache) LoadSubscription(string) *adapter.SavedBinary { return nil }

func (c *failingSubscriptionCache) SaveSubscription(string, *adapter.SavedBinary) error {
	c.saves++
	return errors.New("disk full")
}

// TestProviderRemoteKeepsEtagWhenTheCacheWriteFails pins the other half of the cache contract: the
// ETag is a promise that the cached body matches it. If the body could not be stored, the promise
// must not be made, or every later conditional request answers 304 for content the cache lacks.
func TestProviderRemoteKeepsEtagWhenTheCacheWriteFails(t *testing.T) {
	t.Parallel()
	var conditionalRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("If-None-Match") != "" {
			conditionalRequests.Add(1)
		}
		writer.Header().Set("Etag", `"v1"`)
		_, _ = writer.Write([]byte(`{"outbounds": [{"type": "test", "tag": "a"}]}`))
	}))
	t.Cleanup(server.Close)
	provider, _ := newRemoteHarness(t, server.URL)
	provider.httpClient = server.Client()
	failing := &failingSubscriptionCache{}
	provider.cacheFile = failing
	require.NoError(t, provider.fetch(context.Background(), true))
	require.Equal(t, 1, failing.saves)
	require.Empty(t, provider.lastEtag, "the tag must not advance past a cache that was not written")

	require.NoError(t, provider.fetch(context.Background(), false))
	require.Equal(t, 0, int(conditionalRequests.Load()), "a retry must download the body, not revalidate")
}
