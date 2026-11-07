package cachefile

import (
	"context"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"

	"github.com/stretchr/testify/require"
)

// TestSubscriptionCacheRoundTrip covers the provider's half of the cache file: nodes survive a full
// close/reopen with their URL binding, and a provider tag that was never written reads as absent.
func TestSubscriptionCacheRoundTrip(t *testing.T) {
	path := t.TempDir() + "/cache.db"
	options := option.CacheFileOptions{Path: path}
	logger := log.NewNOPFactory().Logger()
	first := New(context.Background(), logger, options)
	firstScope := adapter.NewScope(context.Background(), logger)
	require.NoError(t, first.Start(adapter.StartStateInitialize, firstScope))
	original := &adapter.SavedBinary{
		Content:     []byte(`{"outbounds": [{"type": "socks", "tag": "a"}]}`),
		LastUpdated: time.Unix(1700000000, 0),
		LastEtag:    `"abc"`,
		URLHash:     []byte("hash"),
	}
	require.NoError(t, first.SaveSubscription("air", original))
	require.NoError(t, firstScope.Close())

	second := New(context.Background(), logger, options)
	secondScope := adapter.NewScope(context.Background(), logger)
	require.NoError(t, second.Start(adapter.StartStateInitialize, secondScope))
	t.Cleanup(func() { _ = secondScope.Close() })
	restored := second.LoadSubscription("air")
	require.NotNil(t, restored)
	require.Equal(t, original.Content, restored.Content)
	require.Equal(t, original.LastUpdated, restored.LastUpdated)
	require.Equal(t, original.LastEtag, restored.LastEtag)
	require.Equal(t, original.URLHash, restored.URLHash)
	require.Nil(t, second.LoadSubscription("never-written"))
}
