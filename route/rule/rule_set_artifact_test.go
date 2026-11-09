package rule

// B — the remote rule set's production store.
//
// A downloaded rule set is a versioned artifact, not a single-slot cache entry: it is validated and
// stored before it is published, the rule set holds a read lease on the exact bytes it serves, and
// a restart can name the version it used instead of accepting whatever is newest. The legacy
// single-slot cache is still read once and migrated, so an existing installation keeps its rules.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/service/filemanager"

	"github.com/stretchr/testify/require"
)

// fakeRuleSetCache is the legacy CacheFile surface a remote rule set uses. The embedded interface
// is nil: the store never calls anything else in these tests.
type fakeRuleSetCache struct {
	adapter.CacheFile
	saved *adapter.SavedBinary
	saves int
}

func (c *fakeRuleSetCache) LoadRuleSet(tag string) *adapter.SavedBinary { return c.saved }

func (c *fakeRuleSetCache) SaveRuleSet(tag string, set *adapter.SavedBinary) error {
	c.saves++
	c.saved = set
	return nil
}

func ruleSetDigest(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func ruleSetArtifactContext(t *testing.T) context.Context {
	t.Helper()
	directory := t.TempDir()
	return filemanager.WithDefault(context.Background(), directory, directory, -1, -1)
}

func newStoredRemoteRuleSet(t *testing.T, ctx context.Context, sourceURL string, tag string) *RemoteRuleSet {
	t.Helper()
	ruleSet, err := NewRemoteRuleSet(ctx, log.NewNOPFactory().Logger(), tag, option.RuleSet{
		Type:   constant.RuleSetTypeRemote,
		Format: constant.RuleSetFormatBinary,
		RemoteOptions: option.RemoteRuleSet{
			URL: sourceURL,
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = ruleSet.Close() })
	ruleSet.openArtifactStore()
	require.NotNil(t, ruleSet.artifactStore, "the versioned store must open for a persisted rule set")
	return ruleSet
}

// TestRemoteRuleSetServesTheNewestStoredVersionAndKeepsTheOldOneExact is the switch a restart or a
// later reader depends on: the newest committed version is what Load offers, every published
// version stays individually addressable by digest, and a collector can only remove a version once
// the rule set that served it has released its lease.
func TestRemoteRuleSetServesTheNewestStoredVersionAndKeepsTheOldOneExact(t *testing.T) {
	ctx := ruleSetArtifactContext(t)
	payloads := [2][]byte{
		writeRuleSetPayload(t, "10.0.0.0/8"),
		writeRuleSetPayload(t, "192.168.0.0/16"),
	}
	var served atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write(payloads[served.Load()])
	}))
	t.Cleanup(server.Close)

	ruleSet := newStoredRemoteRuleSet(t, ctx, server.URL, "artifact-switch")
	ruleSet.httpClient = server.Client()

	require.NoError(t, ruleSet.fetch(ctx, true))
	require.True(t, remoteRuleSetMatches(t, ruleSet, "10.1.2.3"), "the first version is live")
	firstID := ruleSet.artifactLease.ID()
	require.Equal(t, ruleSetDigest(payloads[0]), firstID, "the version reference is the content digest")

	// A reader holds the first version across the update and the collection that follows it. This
	// is the exact lease a pinned recovery would take: the bytes must stay readable.
	oldLease, err := ruleSet.artifactStore.LoadExact(ruleSet.artifactKey, firstID)
	require.NoError(t, err)

	served.Store(1)
	require.NoError(t, ruleSet.fetch(ctx, true))
	require.True(t, remoteRuleSetMatches(t, ruleSet, "192.168.1.1"), "the refreshed version is live")
	require.False(t, remoteRuleSetMatches(t, ruleSet, "10.1.2.3"), "the old matcher is no longer live")
	secondID := ruleSet.artifactLease.ID()
	require.NotEqual(t, firstID, secondID)

	// Load is the "newest" entry point: a restart must come back to the second version.
	newest, err := ruleSet.artifactStore.Load(ruleSet.artifactKey)
	require.NoError(t, err)
	require.Equal(t, secondID, newest.ID())
	newest.Release()

	// The old version was not collected while its lease was held, and its bytes are the ones that
	// were published for it.
	content, err := os.ReadFile(oldLease.Path())
	require.NoError(t, err, "a leased version must survive the update and its collection")
	require.Equal(t, payloads[0], content)
	require.Equal(t, firstID, ruleSetDigest(content))
	oldLease.Release()

	// Without the lease, the next collection removes the older version and keeps the newest. The
	// live rule set's own lease is dropped by Close first.
	require.NoError(t, ruleSet.Close())
	require.NoError(t, ruleSet.artifactStore.Collect(ruleSet.artifactKey))
	_, err = ruleSet.artifactStore.LoadExact(ruleSet.artifactKey, firstID)
	require.ErrorIs(t, err, os.ErrNotExist, "once no reader holds the first version it is collectable")
	newest, err = ruleSet.artifactStore.Load(ruleSet.artifactKey)
	require.NoError(t, err, "the newest version is kept even when unleased")
	require.Equal(t, secondID, newest.ID())
	newest.Release()
}

// TestRemoteRuleSetKeepsThePublishedVersionWhenTheNewArtifactIsBad covers the failure the store is
// for: a corrupt download must not replace the generation that is serving traffic, and it must not
// become a stored version either.
func TestRemoteRuleSetKeepsThePublishedVersionWhenTheNewArtifactIsBad(t *testing.T) {
	ctx := ruleSetArtifactContext(t)
	good := writeRuleSetPayload(t, "10.0.0.0/8")
	var body atomic.Value
	body.Store(good)
	var short atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		content := body.Load().([]byte)
		if short.Load() {
			// The body is shorter than the length the response claims.
			writer.Header().Set("Content-Length", strconv.Itoa(len(content)+1024))
		}
		_, _ = writer.Write(content)
	}))
	t.Cleanup(server.Close)

	ruleSet := newStoredRemoteRuleSet(t, ctx, server.URL, "artifact-bad")
	ruleSet.httpClient = server.Client()
	require.NoError(t, ruleSet.fetch(ctx, true))
	publishedID := ruleSet.artifactLease.ID()
	require.True(t, remoteRuleSetMatches(t, ruleSet, "10.1.2.3"))

	for name, replacement := range map[string][]byte{
		"garbage": []byte("this is not a binary rule set"),
		"empty":   {},
	} {
		body.Store(replacement)
		err := ruleSet.fetch(ctx, true)
		require.Error(t, err, "%s: an unparseable body must fail the refresh", name)
		require.True(t, remoteRuleSetMatches(t, ruleSet, "10.1.2.3"),
			"%s: the published generation must keep serving", name)
		stored, loadErr := ruleSet.artifactStore.Load(ruleSet.artifactKey)
		require.NoError(t, loadErr)
		require.Equal(t, publishedID, stored.ID(), "%s: no version may be committed for a bad body", name)
		stored.Release()
	}

	// A truncated body is a valid prefix plus fewer bytes than the HTTP framing promised. The
	// bytes parse, so only the length check can reject it.
	truncated := writeRuleSetPayload(t, "192.168.0.0/16")
	body.Store(truncated)
	short.Store(true)
	err := ruleSet.fetch(ctx, true)
	require.Error(t, err, "a body shorter than its Content-Length must fail the refresh")
	require.True(t, remoteRuleSetMatches(t, ruleSet, "10.1.2.3"), "the previous generation must stay live")
	short.Store(false)
}

// TestRemoteRuleSetMigratesTheLegacyCacheOnce pins the compatibility path: an installation that
// has only the single-slot cache keeps its rule set, the bytes are copied into the versioned store,
// and the legacy entry is left where older builds expect it.
func TestRemoteRuleSetMigratesTheLegacyCacheOnce(t *testing.T) {
	ctx := ruleSetArtifactContext(t)
	payload := writeRuleSetPayload(t, "10.0.0.0/8")

	ruleSet := newStoredRemoteRuleSet(t, ctx, "https://rules.example/legacy.srs", "legacy")
	cache := &fakeRuleSetCache{saved: &adapter.SavedBinary{
		LastUpdated: time.Now().Add(-time.Hour),
		Content:     payload,
		LastEtag:    `"legacy"`,
		URLHash:     ruleSet.urlHash[:],
	}}
	ruleSet.cacheFile = cache

	require.True(t, ruleSet.restoreCachedRuleSet(), "the legacy cache must still restore the rule set")
	require.True(t, remoteRuleSetMatches(t, ruleSet, "10.1.2.3"))
	require.Zero(t, cache.saves, "the migration writes the store, not the legacy cache")
	require.NotNil(t, ruleSet.artifactLease)
	require.Equal(t, ruleSetDigest(payload), ruleSet.artifactLease.ID())

	// A second instance with the same source finds the version in the store and does not need the
	// legacy cache at all.
	restarted := newStoredRemoteRuleSet(t, ctx, "https://rules.example/legacy.srs", "legacy")
	require.True(t, restarted.restoreStoredRuleSet())
	require.True(t, remoteRuleSetMatches(t, restarted, "10.1.2.3"))
	require.Equal(t, ruleSetDigest(payload), restarted.artifactLease.ID())
}

// TestALateRefreshDoesNotReplaceANewerOne covers the overlap the updater can produce: a refresh
// that started first but finishes last must not publish over the generation that started after it.
func TestALateRefreshDoesNotReplaceANewerOne(t *testing.T) {
	ctx := ruleSetArtifactContext(t)
	oldPayload := writeRuleSetPayload(t, "10.0.0.0/8")
	newPayload := writeRuleSetPayload(t, "192.168.0.0/16")
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			close(firstStarted)
			<-releaseFirst
			_, _ = writer.Write(oldPayload)
			return
		}
		_, _ = writer.Write(newPayload)
	}))
	t.Cleanup(server.Close)

	ruleSet := newStoredRemoteRuleSet(t, ctx, server.URL, "artifact-late")
	ruleSet.httpClient = server.Client()

	firstResult := make(chan error, 1)
	go func() {
		firstResult <- ruleSet.fetch(ctx, true)
	}()
	<-firstStarted

	require.NoError(t, ruleSet.fetch(ctx, true), "the newer refresh publishes")
	require.True(t, remoteRuleSetMatches(t, ruleSet, "192.168.1.1"))

	close(releaseFirst)
	require.Error(t, <-firstResult, "the older refresh must be refused as superseded")
	require.True(t, remoteRuleSetMatches(t, ruleSet, "192.168.1.1"),
		"the late older refresh must not replace the newer generation")
	require.False(t, remoteRuleSetMatches(t, ruleSet, "10.1.2.3"))

	// The superseded bytes may be stored (they were already committed when the generation check
	// ran), but they are older by the time the refresh STARTED, so the newest entry is the live one.
	newest, err := ruleSet.artifactStore.Load(ruleSet.artifactKey)
	require.NoError(t, err)
	require.Equal(t, ruleSetDigest(newPayload), newest.ID())
	newest.Release()
}

// TestAnUnmodifiedRefreshKeepsTheStoredVersion covers 304: the stored version and the live rules
// must not change, and the cache's update time is refreshed instead.
func TestAnUnmodifiedRefreshKeepsTheStoredVersion(t *testing.T) {
	ctx := ruleSetArtifactContext(t)
	payload := writeRuleSetPayload(t, "10.0.0.0/8")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Etag", `"v1"`)
		if request.Header.Get("If-None-Match") == `"v1"` {
			writer.WriteHeader(http.StatusNotModified)
			return
		}
		_, _ = writer.Write(payload)
	}))
	t.Cleanup(server.Close)

	ruleSet := newStoredRemoteRuleSet(t, ctx, server.URL, "artifact-304")
	ruleSet.httpClient = server.Client()
	cache := &fakeRuleSetCache{}
	ruleSet.cacheFile = cache

	require.NoError(t, ruleSet.fetch(ctx, true))
	publishedID := ruleSet.artifactLease.ID()
	require.NoError(t, ruleSet.fetch(ctx, true))
	require.Equal(t, publishedID, ruleSet.artifactLease.ID(), "a 304 must not publish another version")
	require.True(t, remoteRuleSetMatches(t, ruleSet, "10.1.2.3"))
	require.NotNil(t, cache.saved)
	require.Equal(t, `"v1"`, cache.saved.LastEtag)
}
