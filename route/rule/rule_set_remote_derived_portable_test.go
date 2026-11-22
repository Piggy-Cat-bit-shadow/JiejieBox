package rule

// Shared fixtures for the remote derived-cache matrix, plus the cases that must hold on EVERY
// platform.
//
// The derived cache is only active where the product serves a rule set from a mapping (iOS, plus
// the Unix test switch), so the hit/miss counter cases live in the `unix` files next to this one.
// What is left here is the contract that cannot depend on that switch:
//
//   - F11: the non-iOS default must not create a derived store or write a derived file, and a
//     context with no persistent base directory must not create one either.
//   - F08: an unopenable artifact store must leave the original streaming/heap path working, and
//     the legacy single-slot cache must still migrate and restore exactly as before.
//
// Every counter assertion here is exact. On a platform without the derived path the remote
// counters must stay at zero while `sourceDecodes` counts the one parse the original path performs
// - that is what makes "no derived work happened" observable instead of assumed.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/artifact"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/filemanager"

	"github.com/stretchr/testify/require"
)

// remoteDerivedTestHTTPManager is the seam `RemoteRuleSet.StartContext` asks for the default
// transport. The embedded interface is nil: the rule-set load only asks for DefaultTransport.
type remoteDerivedTestHTTPManager struct {
	adapter.HTTPClientManager
	transport adapter.HTTPTransport
}

func (m *remoteDerivedTestHTTPManager) DefaultTransport() adapter.HTTPTransport { return m.transport }

// remoteDerivedTestTransport is a real transport that counts every request. The count is the wire
// fact behind "a warm start did not download" and "a 304 still made a request".
type remoteDerivedTestTransport struct {
	inner    http.RoundTripper
	requests atomic.Int32
}

func (t *remoteDerivedTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	t.requests.Add(1)
	return t.inner.RoundTrip(request)
}

func (t *remoteDerivedTestTransport) CloseIdleConnections() {
	if closer, isCloser := t.inner.(interface{ CloseIdleConnections() }); isCloser {
		closer.CloseIdleConnections()
	}
}

func (t *remoteDerivedTestTransport) Reset() {}

// remoteDerivedTestHTTPFixture is one running test server plus the context that makes StartContext
// use it. Every rule-set object built from `ctx` shares the same transport (and therefore the same
// request count), which is exactly the restart shape: the second object is a new process that can
// only reach this server through the same wire.
type remoteDerivedTestHTTPFixture struct {
	server    *httptest.Server
	transport *remoteDerivedTestTransport
	ctx       context.Context
}

func newRemoteDerivedTestHTTP(t *testing.T, ctx context.Context, handler http.Handler) *remoteDerivedTestHTTPFixture {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	baseTransport, isTransport := server.Client().Transport.(*http.Transport)
	require.True(t, isTransport, "the fixture needs the real transport of the test server")
	transport := &remoteDerivedTestTransport{inner: baseTransport}
	return &remoteDerivedTestHTTPFixture{
		server:    server,
		transport: transport,
		ctx: service.ContextWith[adapter.HTTPClientManager](ctx, &remoteDerivedTestHTTPManager{
			transport: transport,
		}),
	}
}

func (f *remoteDerivedTestHTTPFixture) requestCount() int32 {
	return f.transport.requests.Load()
}

// remoteDerivedTestSourceServer is a mutable HTTP source: tests change its bytes or its ETag between
// refreshes without restarting the server, which is what an update looks like.
type remoteDerivedTestSourceServer struct {
	access   sync.RWMutex
	payload  []byte
	etag     string
	status   int
	requests atomic.Int32
}

func newRemoteDerivedTestSourceServer(t *testing.T, payload []byte, etag string) *remoteDerivedTestSourceServer {
	t.Helper()
	server := &remoteDerivedTestSourceServer{etag: etag}
	server.setPayload(payload)
	return server
}

func (s *remoteDerivedTestSourceServer) ServeHTTP(writer http.ResponseWriter, _ *http.Request) {
	s.requests.Add(1)
	s.access.RLock()
	payload, etag, status := s.payload, s.etag, s.status
	s.access.RUnlock()
	if etag != "" {
		writer.Header().Set("Etag", etag)
	}
	if status != 0 {
		writer.WriteHeader(status)
		return
	}
	_, _ = writer.Write(payload)
}

func (s *remoteDerivedTestSourceServer) setPayload(payload []byte) {
	s.access.Lock()
	s.payload = append([]byte(nil), payload...)
	s.access.Unlock()
}

func (s *remoteDerivedTestSourceServer) setEtag(etag string) {
	s.access.Lock()
	s.etag = etag
	s.access.Unlock()
}

func (s *remoteDerivedTestSourceServer) setStatus(status int) {
	s.access.Lock()
	s.status = status
	s.access.Unlock()
}

func (s *remoteDerivedTestSourceServer) requestCount() int32 { return s.requests.Load() }

// remoteDerivedTestStoreVersion is one physical version in the shared rule-set artifact store,
// found from the filesystem so a test never has to know the production key naming to inspect what
// was published.
type remoteDerivedTestStoreVersion struct {
	Key      string
	Dir      string
	Manifest artifact.Manifest
}

func (v remoteDerivedTestStoreVersion) ArtifactPath() string {
	return filepath.Join(v.Dir, "artifact.bin")
}

func (v remoteDerivedTestStoreVersion) ArtifactBytes(t *testing.T) []byte {
	t.Helper()
	content, err := os.ReadFile(v.ArtifactPath())
	require.NoError(t, err, "read artifact of %s", v.Dir)
	return content
}

// dependency returns the value of the first manifest dependency with the given prefix.
func (v remoteDerivedTestStoreVersion) dependency(prefix string) (string, bool) {
	for _, dependency := range v.Manifest.Dependencies {
		if strings.HasPrefix(dependency, prefix) {
			return strings.TrimPrefix(dependency, prefix), true
		}
	}
	return "", false
}

// remoteDerivedTestStoreVersions lists every readable version in the store under base. Crash
// residue (a directory without a manifest) is skipped: it is production-visible garbage, not a
// version.
func remoteDerivedTestStoreVersions(t *testing.T, base string) []remoteDerivedTestStoreVersion {
	t.Helper()
	root := filepath.Join(base, derivedStoreDirectory)
	keys, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		require.NoError(t, err)
	}
	var versions []remoteDerivedTestStoreVersion
	for _, key := range keys {
		if !key.IsDir() || strings.HasPrefix(key.Name(), ".") {
			continue
		}
		keyDir := filepath.Join(root, key.Name())
		entries, readErr := os.ReadDir(keyDir)
		if readErr != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "v-") {
				continue
			}
			versionDir := filepath.Join(keyDir, entry.Name())
			encoded, manifestErr := os.ReadFile(filepath.Join(versionDir, "manifest.json"))
			if manifestErr != nil {
				continue
			}
			var manifest artifact.Manifest
			if decodeErr := json.Unmarshal(encoded, &manifest); decodeErr != nil {
				continue
			}
			versions = append(versions, remoteDerivedTestStoreVersion{Key: key.Name(), Dir: versionDir, Manifest: manifest})
		}
	}
	return versions
}

func remoteDerivedTestStoreDerivedVersions(t *testing.T, base string) []remoteDerivedTestStoreVersion {
	t.Helper()
	var versions []remoteDerivedTestStoreVersion
	for _, version := range remoteDerivedTestStoreVersions(t, base) {
		if strings.HasPrefix(version.Manifest.Schema, derivedSchemaPrefix) {
			versions = append(versions, version)
		}
	}
	return versions
}

func remoteDerivedTestStoreSourceVersions(t *testing.T, base string) []remoteDerivedTestStoreVersion {
	t.Helper()
	var versions []remoteDerivedTestStoreVersion
	for _, version := range remoteDerivedTestStoreVersions(t, base) {
		if strings.HasPrefix(version.Manifest.Schema, "rule-set/") {
			versions = append(versions, version)
		}
	}
	return versions
}

// remoteDerivedTestVersionForSource returns the one derived version whose manifest names exactly
// these source bytes, or fails.
func remoteDerivedTestVersionForSource(t *testing.T, base string, digest string) remoteDerivedTestStoreVersion {
	t.Helper()
	var found []remoteDerivedTestStoreVersion
	for _, version := range remoteDerivedTestStoreDerivedVersions(t, base) {
		if source, ok := version.dependency("source-sha256="); ok && source == digest {
			found = append(found, version)
		}
	}
	require.Len(t, found, 1, "exactly one derived version must name source %s", digest)
	return found[0]
}

// newRemoteDerivedTestRuleSet builds the object the start path operates on, without starting it.
// Tests that call `openArtifactStore`/`fetch`/`restoreStoredRuleSet` directly use this.
func newRemoteDerivedTestRuleSet(t *testing.T, ctx context.Context, tag string, sourceURL string, format string) *RemoteRuleSet {
	t.Helper()
	ruleSet, err := NewRemoteRuleSet(ctx, log.NewNOPFactory().Logger(), tag, option.RuleSet{
		Type:   C.RuleSetTypeRemote,
		Format: format,
		RemoteOptions: option.RemoteRuleSet{
			URL: sourceURL,
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = ruleSet.Close() })
	return ruleSet
}

// startRemoteDerivedTestRuleSet drives the REAL start entry: open the store, restore or fetch, and
// publish. A failure is the test's failure, not a warning.
func startRemoteDerivedTestRuleSet(t *testing.T, ctx context.Context, tag string, sourceURL string, format string) *RemoteRuleSet {
	t.Helper()
	ruleSet := newRemoteDerivedTestRuleSet(t, ctx, tag, sourceURL, format)
	require.NoError(t, ruleSet.StartContext(ctx, adapter.NewHTTPStartContext()), "start remote rule-set ", tag)
	return ruleSet
}

func requireRemoteDerivedDomainMatch(t *testing.T, ruleSet *RemoteRuleSet, domain string, want bool) {
	t.Helper()
	require.Equal(t, want, ruleSet.Match(&adapter.InboundContext{Domain: domain}), "domain %s", domain)
}

// remoteDerivedTestDirectory returns a fresh persistent base for one test.
func remoteDerivedTestDirectory(t *testing.T) (context.Context, string) {
	t.Helper()
	directory := t.TempDir()
	return filemanager.WithDefault(context.Background(), directory, directory, -1, -1), directory
}

// TestRemoteDerivedF11NonIOSDefaultDoesNotWriteDerivedFiles is F11 for the platform default: a
// store exists and the original path runs, but no derived artifact may be created and the derived
// counters must stay at zero. On the Unix test host this is the unflipped default; on Windows it is
// the only shape that exists.
func TestRemoteDerivedF11NonIOSDefaultDoesNotWriteDerivedFiles(t *testing.T) {
	require.False(t, mmapRuleSetPlatform, "this case asserts the product default, not the test switch")
	ctx, base := remoteDerivedTestDirectory(t)
	payload := writeRuleSetPayload(t, "10.0.0.0/8")
	server := newRemoteDerivedTestSourceServer(t, payload, "")
	fixture := newRemoteDerivedTestHTTP(t, ctx, server)

	resetRemoteDerivedDiagnostics()
	ruleSet := startRemoteDerivedTestRuleSet(t, fixture.ctx, "f11-default", fixture.server.URL, C.RuleSetFormatBinary)
	require.True(t, remoteRuleSetMatches(t, ruleSet, "10.1.2.3"), "the original path must serve the rules")
	require.NotNil(t, ruleSet.artifactStore, "a base directory still gets the source store")
	require.Nil(t, ruleSet.derivedCache, "the derived cache must be off where the product does not map")
	require.Empty(t, remoteDerivedTestStoreDerivedVersions(t, base), "no derived artifact may be written")
	require.Len(t, remoteDerivedTestStoreSourceVersions(t, base), 1, "the source artifact is still published")
	require.EqualValues(t, 1, server.requestCount())

	hits, misses, sourceDecodes, srsmWrites, heapBuilds := remoteDerivedDiagnostics()
	require.Zero(t, hits)
	require.Zero(t, misses)
	require.EqualValues(t, 1, sourceDecodes, "the original path parses the source once")
	require.Zero(t, srsmWrites, "no SRSM may be written without the derived path")
	require.EqualValues(t, 1, heapBuilds, "the rules are built through the original helper")
}

// TestRemoteDerivedF11NoBaseDirectoryCreatesNoStore is the other half of F11: a context with no
// persistent base has nowhere to put artifacts, and the original streaming path must still work.
func TestRemoteDerivedF11NoBaseDirectoryCreatesNoStore(t *testing.T) {
	payload := writeRuleSetPayload(t, "10.0.0.0/8")
	server := newRemoteDerivedTestSourceServer(t, payload, "")
	fixture := newRemoteDerivedTestHTTP(t, context.Background(), server)

	resetRemoteDerivedDiagnostics()
	ruleSet := startRemoteDerivedTestRuleSet(t, fixture.ctx, "f11-no-base", fixture.server.URL, C.RuleSetFormatBinary)
	require.True(t, remoteRuleSetMatches(t, ruleSet, "10.1.2.3"))
	require.Nil(t, ruleSet.artifactStore, "no base directory means no store")
	require.Nil(t, ruleSet.derivedCache)
	require.Empty(t, ruleSet.artifactKey, "no store means no artifact key was created")
	require.EqualValues(t, 1, server.requestCount())

	hits, misses, sourceDecodes, srsmWrites, heapBuilds := remoteDerivedDiagnostics()
	require.Zero(t, hits)
	require.Zero(t, misses)
	require.EqualValues(t, 1, sourceDecodes)
	require.Zero(t, srsmWrites)
	require.EqualValues(t, 1, heapBuilds)
}

// TestRemoteDerivedF08UnopenableStoreKeepsTheOriginalPaths covers a base path that is not a
// directory: opening the store fails, nothing may be created there, and both a downloaded document
// and a legacy cache entry must still load on the original heap path.
func TestRemoteDerivedF08UnopenableStoreKeepsTheOriginalPaths(t *testing.T) {
	basePath := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(basePath, []byte("occupied"), 0o600))
	ctx := filemanager.WithDefault(context.Background(), basePath, t.TempDir(), -1, -1)

	payload := writeRuleSetPayload(t, "10.0.0.0/8")
	server := newRemoteDerivedTestSourceServer(t, payload, "")
	fixture := newRemoteDerivedTestHTTP(t, ctx, server)

	resetRemoteDerivedDiagnostics()
	ruleSet := startRemoteDerivedTestRuleSet(t, fixture.ctx, "f08-no-store", fixture.server.URL, C.RuleSetFormatBinary)
	require.True(t, remoteRuleSetMatches(t, ruleSet, "10.1.2.3"), "a store failure must not fail the load")
	require.Nil(t, ruleSet.artifactStore)
	require.Nil(t, ruleSet.derivedCache)
	info, statErr := os.Stat(basePath)
	require.NoError(t, statErr)
	require.False(t, info.IsDir(), "nothing may replace the file that blocked the store")

	hits, misses, sourceDecodes, srsmWrites, heapBuilds := remoteDerivedDiagnostics()
	require.Zero(t, hits)
	require.Zero(t, misses)
	require.EqualValues(t, 1, sourceDecodes)
	require.Zero(t, srsmWrites)
	require.EqualValues(t, 1, heapBuilds)
	require.EqualValues(t, 1, server.requestCount())

	// The legacy cache is the other original path, and it must work with no store as well.
	legacy := newRemoteDerivedTestRuleSet(t, fixture.ctx, "f08-no-store-legacy", fixture.server.URL, C.RuleSetFormatBinary)
	legacy.openArtifactStore()
	require.Nil(t, legacy.artifactStore)
	cache := &fakeRuleSetCache{saved: &adapter.SavedBinary{
		LastUpdated: time.Now().Add(-time.Hour),
		Content:     payload,
		LastEtag:    `"legacy"`,
		URLHash:     legacy.urlHash[:],
	}}
	legacy.cacheFile = cache
	resetRemoteDerivedDiagnostics()
	require.True(t, legacy.restoreCachedRuleSet(), "the legacy cache must restore without a store")
	require.True(t, remoteRuleSetMatches(t, legacy, "10.1.2.3"))
	require.Nil(t, legacy.artifactLease, "without a store there is no source version to lease")
	require.Zero(t, cache.saves)
	require.NotNil(t, cache.saved)
	hits, misses, sourceDecodes, srsmWrites, heapBuilds = remoteDerivedDiagnostics()
	require.Zero(t, hits)
	require.Zero(t, misses)
	require.EqualValues(t, 1, sourceDecodes, "the legacy bytes are still decoded")
	require.Zero(t, srsmWrites)
	require.EqualValues(t, 1, heapBuilds)
}

// TestRemoteDerivedF08LegacyCacheMigrationIsUnchanged pins the original migration behaviour with a
// working store: the cache restores the rules, the bytes are published as an exact source version,
// the legacy entry is left in place, and a second object restores from the store without the cache.
func TestRemoteDerivedF08LegacyCacheMigrationIsUnchanged(t *testing.T) {
	ctx, base := remoteDerivedTestDirectory(t)
	refs := artifact.NewReferenceSet(nil, nil)
	ctx = service.ContextWith[*artifact.ReferenceSet](ctx, refs)
	payload := writeRuleSetPayload(t, "10.0.0.0/8")
	url := "https://rules.example/f08-legacy.srs"
	key := artifactKeyFor("f08-legacy", url)

	first := newRemoteDerivedTestRuleSet(t, ctx, "f08-legacy", url, C.RuleSetFormatBinary)
	first.openArtifactStore()
	require.NotNil(t, first.artifactStore)
	cache := &fakeRuleSetCache{saved: &adapter.SavedBinary{
		LastUpdated: time.Now().Add(-time.Hour),
		Content:     payload,
		LastEtag:    `"legacy"`,
		URLHash:     first.urlHash[:],
	}}
	first.cacheFile = cache
	resetRemoteDerivedDiagnostics()
	require.True(t, first.restoreCachedRuleSet())
	require.True(t, remoteRuleSetMatches(t, first, "10.1.2.3"))
	require.Equal(t, ruleSetDigest(payload), first.artifactLease.ID(), "the source version is named by its digest")
	require.Zero(t, cache.saves, "migration writes the store, not the legacy cache")
	require.NotNil(t, cache.saved, "the legacy entry is not removed")
	require.Equal(t, ruleSetDigest(payload), refs.Snapshot()[key], "the migrated version is recorded exactly")
	require.Len(t, remoteDerivedTestStoreSourceVersions(t, base), 1)
	require.Empty(t, remoteDerivedTestStoreDerivedVersions(t, base))

	// A later object loads the exact stored version and needs no legacy cache.
	restarted := newRemoteDerivedTestRuleSet(t, ctx, "f08-legacy", url, C.RuleSetFormatBinary)
	restarted.openArtifactStore()
	restored, restoreErr := restarted.restoreStoredRuleSet()
	require.NoError(t, restoreErr)
	require.True(t, restored)
	require.True(t, remoteRuleSetMatches(t, restarted, "10.1.2.3"))
	require.Equal(t, ruleSetDigest(payload), restarted.artifactLease.ID())
}

// TestRemoteDerivedF08LegacyCacheFromAnotherURLIsIgnored keeps the URL ownership check intact:
// bytes cached for one URL must not be served for a tag now pointing somewhere else.
func TestRemoteDerivedF08LegacyCacheFromAnotherURLIsIgnored(t *testing.T) {
	ctx, base := remoteDerivedTestDirectory(t)
	payload := writeRuleSetPayload(t, "10.0.0.0/8")
	ruleSet := newRemoteDerivedTestRuleSet(t, ctx, "f08-moved", "https://rules.example/new.srs", C.RuleSetFormatBinary)
	ruleSet.openArtifactStore()
	require.NotNil(t, ruleSet.artifactStore)
	oldHash := ruleSet.urlHash
	oldHash[0] ^= 0xff
	ruleSet.cacheFile = &fakeRuleSetCache{saved: &adapter.SavedBinary{
		LastUpdated: time.Now().Add(-time.Hour),
		Content:     payload,
		URLHash:     oldHash[:],
	}}
	require.False(t, ruleSet.restoreCachedRuleSet(), "a cache from another URL must not be restored")
	require.False(t, remoteRuleSetMatches(t, ruleSet, "10.1.2.3"))
	require.Nil(t, ruleSet.artifactLease)
	require.Empty(t, remoteDerivedTestStoreVersions(t, base), "nothing may be published for the refused cache")
}
