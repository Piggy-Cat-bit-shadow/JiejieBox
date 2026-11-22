package daemon

// E2 — two Boxes, one artifact store directory: no cross-served rules, no cross-served provider
// nodes, no stolen leases, and one Close does not break the other.
//
// # Why a real pair of Boxes
//
// The artifact store's protection is not a flag on a Box: it is the PROCESS-WIDE lease table keyed
// by the store directory (`common/artifact` `leaseTableFor`). Two Boxes in one process - the shape
// a reload and a configuration check already produce - open two `Store` handles on one directory,
// and the lease table is what keeps a collector in one from removing a version the other is
// reading. A unit test of `Store` cannot show that the RULE-SET owner actually takes and holds
// that lease, and it cannot show that two Boxes with the same rule-set tag and the same provider
// tag publish independent state.
//
// # The interleaving (all barriers, no probability)
//
//	1. Box A starts with the shared subscription URL (rule-set tag "shared", provider tag "nodes")
//	   and its own rule-set tag "gate". Its update interval is one hour, so after the initial
//	   update pass its next refresh is far outside the test; the test WAITS for that pass to land
//	   before proceeding, so A's state is final and observable.
//	2. Box B starts on the same directory with the SAME shared URL and tag, a one-hour gate URL of
//	   its own, and a 50 ms update interval for the shared set. It restores the shared document
//	   from the store (the identical content A published) and then moves to a NEW document.
//	3. While A still holds the old version's lease, an INDEPENDENT store handle - what another
//	   Box's collector is - calls `Collect`. The old version must survive, because a lease, not a
//	   timestamp, protects it.
//	4. Box A closes. Its lease is released; Box B's own next collection removes the old version,
//	   and B's live version is never touched.
//
// # What this does not claim
//
// Cross-process leasing is explicitly out of contract (`common/artifact` says one writer per
// directory). This test is about one process, which is the reload/configuration-check case the
// product actually has.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/artifact"
	providerremote "github.com/sagernet/sing-box/provider/remote"
	"github.com/sagernet/sing-box/route"

	"github.com/stretchr/testify/require"
)

// crossSystemIsolationProfile builds one Box's profile.
//
// `sharedInterval` is what makes B the box that moves on while A stays on the document it started
// with. `cachePath` is per-Box on purpose: the cache file is bbolt with an EXCLUSIVE lock, so two
// concurrently running Boxes cannot share one (the second start is refused with "initialize
// cache-file: timeout" - see TestConcurrentBoxesCannotShareOneCacheFile). The artifact store is the
// state these two Boxes are meant to share, and it is shared through the file manager base
// directory, not through this path.
func crossSystemIsolationProfile(providerURL string, sharedURL string, gateURL string, sharedInterval string, cachePath string) string {
	return fmt.Sprintf(`{
  "log": {"disabled": true},
  "experimental": {"cache_file": {"enabled": true, "path": %q}},
  "providers": [{"type": "remote", "tag": "nodes", "url": %q}],
  "outbounds": [
    {"type": "selector", "tag": "proxy", "providers": ["nodes"]},
    {"type": "direct", "tag": "direct"}
  ],
  "route": {
    "rule_set": [
      {"type": "remote", "tag": "shared", "format": "binary", "url": %q, "update_interval": %q},
      {"type": "remote", "tag": "gate", "format": "binary", "url": %q, "update_interval": "1h"}
    ],
    "rules": [
      {"rule_set": ["shared", "gate"], "action": "reject"}
    ],
    "final": "direct"
  }
}`, cachePath, providerURL, sharedURL, sharedInterval, gateURL)
}

// crossSystemRuleSet returns the Box's live rule set of `tag`, read through the router's public
// accessor. The match assertions below use `adapter.RuleSet.Match`, the same entry point routing
// uses, rather than the rule-set owner's internals.
func crossSystemRuleSet(t *testing.T, instance *Instance, tag string) adapter.RuleSet {
	t.Helper()
	router, ok := instance.Box().Router().(*route.Router)
	require.True(t, ok, "the Box must serve the product router")
	ruleSet, loaded := router.RuleSet(tag)
	require.True(t, loaded, "rule-set %q is not loaded", tag)
	return ruleSet
}

func crossSystemMatches(ruleSet adapter.RuleSet, domain string) bool {
	return ruleSet.Match(&adapter.InboundContext{Domain: domain})
}

// crossSystemDigest is the same identity the store records: sha256 of the artifact bytes.
func crossSystemDigest(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// crossSystemRestartableServer serves one mutable document. `mutable` is replaced under its lock by
// the test; every request is counted so "B's updater really fetched" and "A's interval kept it
// quiet" are facts.
type crossSystemDocumentServer struct {
	document atomic.Value // []byte
	requests atomic.Int32
	server   *httptest.Server
}

func newCrossSystemDocumentServer(t *testing.T, document []byte) *crossSystemDocumentServer {
	t.Helper()
	serving := &crossSystemDocumentServer{}
	serving.document.Store(document)
	serving.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		serving.requests.Add(1)
		_, _ = writer.Write(serving.document.Load().([]byte))
	}))
	t.Cleanup(serving.server.Close)
	return serving
}

func (s *crossSystemDocumentServer) set(document []byte) { s.document.Store(document) }

func TestTwoBoxesShareAnArtifactDirectoryWithoutCrossServing(t *testing.T) {
	directory := t.TempDir()

	// The shared document: both Boxes use this URL and tag, so they name the same artifact key.
	sharedV1 := crossSystemBinaryRuleSet(t, "shared.example")
	sharedV2 := crossSystemBinaryRuleSet(t, "shared2.example")
	sharedServer := newCrossSystemDocumentServer(t, sharedV1)

	// Two different documents under the SAME rule-set tag. If either Box resolved the other's
	// key - or a process-wide cache keyed by tag alone - one of these match assertions flips.
	gateA := crossSystemBinaryRuleSet(t, "a-only.example")
	gateB := crossSystemBinaryRuleSet(t, "b-only.example")
	gateServerA := newCrossSystemDocumentServer(t, gateA)
	gateServerB := newCrossSystemDocumentServer(t, gateB)

	providerA := []byte(`{"outbounds": [
		{"type": "cross-system-node", "tag": "shared"},
		{"type": "cross-system-node", "tag": "only-a"}
	]}`)
	providerB := []byte(`{"outbounds": [
		{"type": "cross-system-node", "tag": "shared"},
		{"type": "cross-system-node", "tag": "only-b"}
	]}`)
	providerServerA := newCrossSystemDocumentServer(t, providerA)
	providerServerB := newCrossSystemDocumentServer(t, providerB)

	// Box A. One-hour shared interval: after its initial update pass it cannot move for the life
	// of the test, which is what makes "A stays on the old version" deterministic.
	serviceA := NewStartedService(ServiceOptions{Context: crossSystemContext(t, directory, &crossSystemRecorder{})})
	t.Cleanup(func() {
		_ = serviceA.CloseService()
		serviceA.Close()
	})
	require.NoError(t, serviceA.StartOrReloadService(context.Background(), crossSystemIsolationProfile(
		providerServerA.server.URL, sharedServer.server.URL, gateServerA.server.URL, "1h",
		filepath.Join(directory, "cache-a.db")), nil))
	instanceA := serviceA.instance
	require.NotNil(t, instanceA)

	sharedA := crossSystemRuleSet(t, instanceA, "shared")
	gateAOfA := crossSystemRuleSet(t, instanceA, "gate")
	require.Eventually(t, func() bool {
		return crossSystemMatches(sharedA, "shared.example") && crossSystemMatches(gateAOfA, "a-only.example")
	}, 10*time.Second, 5*time.Millisecond, "box A never loaded its rule sets")
	require.False(t, crossSystemMatches(gateAOfA, "b-only.example"),
		"box A's gate rule set matched box B's document: a rule was cross-served")

	providerAdapterA, loaded := instanceA.providerManager.Get("nodes")
	require.True(t, loaded)
	require.Eventually(t, func() bool {
		return len(providerNodesOf(t, providerAdapterA)) == 2
	}, 10*time.Second, 5*time.Millisecond, "box A's provider never published")
	require.Equal(t, []string{"nodes/only-a", "nodes/shared"}, providerNodesOf(t, providerAdapterA))
	require.Equal(t, []string{"nodes/only-a", "nodes/shared"}, crossSystemGroupMembers(t, instanceA, "proxy"),
		"box A's selector must reflect box A's provider through its own callback registration")
	nodeSharedA, loaded := instanceA.Box().Outbound().Outbound("nodes/shared")
	require.True(t, loaded)
	remoteA := providerAdapterA.(*providerremote.ProviderRemote)
	updatedAtA := remoteA.UpdatedAt()
	reportA := remoteA.LastApplyReport()
	require.NotNil(t, reportA)

	// A's shared rule set is fetched once, by StartContext. Its one-hour interval means the
	// updater's initial pass SKIPS it (`nextUpdate = lastUpdate + interval`), so there is no second
	// request to wait for and, equally important, no later one that could carry A to the new
	// document. Waiting for the count to settle at exactly one is the barrier: it is the state A
	// will still be in when the document changes.
	require.Eventually(t, func() bool {
		return sharedServer.requests.Load() == 1
	}, 10*time.Second, 5*time.Millisecond, "box A never fetched the shared document")
	require.Equal(t, int32(1), sharedServer.requests.Load(),
		"box A must not have refreshed the shared document again: its interval is one hour")

	storeRoot := filepath.Join(directory, "rule-set-artifacts")
	versionsAfterA := crossSystemArtifactVersions(t, storeRoot)
	sharedKey := crossSystemKeyForTag(t, versionsAfterA, "shared")
	require.NotEmpty(t, sharedKey, "the shared rule set must open a store key: %+v", versionsAfterA)
	sharedVersionsAfterA := crossSystemVersionsFor(t, storeRoot, sharedKey)
	require.Len(t, sharedVersionsAfterA, 1, "box A produced exactly one shared version: %+v", sharedVersionsAfterA)
	require.Equal(t, crossSystemDigest(sharedV1), sharedVersionsAfterA[0].Digest)

	// Box B, same directory, same shared URL and tag, its own gate URL, and the fast shared
	// interval that will carry it to the next document.
	serviceB := NewStartedService(ServiceOptions{Context: crossSystemContext(t, directory, &crossSystemRecorder{})})
	t.Cleanup(func() {
		_ = serviceB.CloseService()
		serviceB.Close()
	})
	require.NoError(t, serviceB.StartOrReloadService(context.Background(), crossSystemIsolationProfile(
		providerServerB.server.URL, sharedServer.server.URL, gateServerB.server.URL, "50ms",
		filepath.Join(directory, "cache-b.db")), nil))
	instanceB := serviceB.instance
	require.NotNil(t, instanceB)

	sharedB := crossSystemRuleSet(t, instanceB, "shared")
	gateBOfB := crossSystemRuleSet(t, instanceB, "gate")
	require.Eventually(t, func() bool {
		return crossSystemMatches(sharedB, "shared.example") && crossSystemMatches(gateBOfB, "b-only.example")
	}, 10*time.Second, 5*time.Millisecond, "box B never loaded its rule sets")
	require.False(t, crossSystemMatches(gateBOfB, "a-only.example"),
		"box B's gate rule set matched box A's document: a rule was cross-served")
	// Same content, independent objects/state: A's document did not leak into B's result either.
	require.True(t, crossSystemMatches(sharedA, "shared.example"))
	require.False(t, crossSystemMatches(sharedA, "shared2.example"))

	providerAdapterB, loaded := instanceB.providerManager.Get("nodes")
	require.True(t, loaded)
	require.Eventually(t, func() bool {
		return len(providerNodesOf(t, providerAdapterB)) == 2
	}, 10*time.Second, 5*time.Millisecond, "box B's provider never published")
	require.Equal(t, []string{"nodes/only-b", "nodes/shared"}, providerNodesOf(t, providerAdapterB),
		"box B published box A's nodes: the provider artifact store was cross-served")
	require.Equal(t, []string{"nodes/only-a", "nodes/shared"}, providerNodesOf(t, providerAdapterA),
		"box A's published set changed when box B started")
	require.Equal(t, []string{"nodes/only-b", "nodes/shared"}, crossSystemGroupMembers(t, instanceB, "proxy"),
		"box B's selector must reflect box B's provider, not box A's")
	require.Equal(t, []string{"nodes/only-a", "nodes/shared"}, crossSystemGroupMembers(t, instanceA, "proxy"),
		"box A's selector changed when box B started: a group callback was cross-served")
	nodeSharedB, loaded := instanceB.Box().Outbound().Outbound("nodes/shared")
	require.True(t, loaded)
	require.NotSame(t, nodeSharedA, nodeSharedB,
		"the same node tag in two Boxes must be two objects, one per provider")
	require.Len(t, crossSystemVersionsFor(t, storeRoot, sharedKey), 1,
		"identical content from two Boxes must deduplicate to one stored version")

	// Box B moves to the new shared document. A's one-hour interval keeps it on the old one.
	sharedServer.set(sharedV2)
	require.Eventually(t, func() bool {
		return crossSystemMatches(sharedB, "shared2.example") && !crossSystemMatches(sharedB, "shared.example")
	}, 15*time.Second, 5*time.Millisecond, "box B never published the new shared document")
	require.True(t, crossSystemMatches(sharedA, "shared.example"),
		"box A moved to box B's document: the two Boxes shared rule-set state")
	require.False(t, crossSystemMatches(sharedA, "shared2.example"),
		"box A's one-hour interval was bypassed by box B's refresh")

	// Both versions now exist: A's live old one and B's live new one.
	require.Eventually(t, func() bool {
		return len(crossSystemVersionsFor(t, storeRoot, sharedKey)) == 2
	}, 15*time.Second, 5*time.Millisecond, "the new version was never stored")
	versions := crossSystemVersionsFor(t, storeRoot, sharedKey)
	require.ElementsMatch(t, []string{crossSystemDigest(sharedV1), crossSystemDigest(sharedV2)}, []string{
		versions[0].Digest, versions[1].Digest,
	})

	// LEASE BARRIER: an independent store handle - what another Box's collector is - collects the
	// key while A still serves the old version. A's lease, not recency, must keep the old bytes.
	independentStore, err := artifact.Open(storeRoot)
	require.NoError(t, err)
	require.NoError(t, independentStore.Collect(sharedKey),
		"collection from another handle must succeed")
	require.Len(t, crossSystemVersionsFor(t, storeRoot, sharedKey), 2,
		"collection removed the version box A is still reading: the lease was not honoured")
	require.True(t, crossSystemMatches(sharedA, "shared.example"),
		"box A's rules vanished with the collected version")

	// Provider refresh isolation: B's document changes and B alone is refreshed. A's provider must
	// not move, and must not acquire B's new node.
	providerServerB.set([]byte(`{"outbounds": [
		{"type": "cross-system-node", "tag": "shared"},
		{"type": "cross-system-node", "tag": "only-b"},
		{"type": "cross-system-node", "tag": "only-b2"}
	]}`))
	summaryB, err := serviceB.RefreshProviders(context.Background(), []string{"nodes"}, 5*time.Second)
	require.NoError(t, err)
	require.Len(t, summaryB.Results, 1)
	require.Equal(t, ProviderRefreshPublished, summaryB.Results[0].Status, "%+v", summaryB.Results[0])
	require.Eventually(t, func() bool {
		return len(providerNodesOf(t, providerAdapterB)) == 3
	}, 10*time.Second, 5*time.Millisecond, "box B's refreshed node was never published")
	require.Equal(t, []string{"nodes/only-b", "nodes/only-b2", "nodes/shared"}, providerNodesOf(t, providerAdapterB))
	require.Equal(t, []string{"nodes/only-b", "nodes/only-b2", "nodes/shared"}, crossSystemGroupMembers(t, instanceB, "proxy"),
		"box B's selector must have received box B's refreshed membership")
	require.Equal(t, []string{"nodes/only-a", "nodes/shared"}, providerNodesOf(t, providerAdapterA),
		"box A acquired box B's refreshed node: provider state was shared")
	require.Equal(t, []string{"nodes/only-a", "nodes/shared"}, crossSystemGroupMembers(t, instanceA, "proxy"),
		"box A's selector acquired box B's refreshed membership: a group callback was cross-served")
	require.Equal(t, updatedAtA, remoteA.UpdatedAt(),
		"box A's provider timestamp moved when only box B was refreshed")
	require.Equal(t, reportA, remoteA.LastApplyReport(),
		"box A's last report changed when only box B was refreshed")

	// Box A closes. B must keep serving its document and its node, and A's released old version
	// must be reclaimed by B's OWN collection - while B's live version is never removed.
	require.NoError(t, serviceA.CloseService())
	require.True(t, crossSystemMatches(sharedB, "shared2.example"),
		"closing box A broke box B's rule set")
	require.Equal(t, []string{"nodes/only-b", "nodes/only-b2", "nodes/shared"}, providerNodesOf(t, providerAdapterB),
		"closing box A broke box B's provider")
	require.Len(t, crossSystemVersionsFor(t, storeRoot, sharedKey), 2,
		"closing box A must release a lease, not delete the other Box's version")
	require.Eventually(t, func() bool {
		versions := crossSystemVersionsFor(t, storeRoot, sharedKey)
		return len(versions) == 1 && versions[0].Digest == crossSystemDigest(sharedV2)
	}, 15*time.Second, 5*time.Millisecond,
		"box B never reclaimed the superseded, now-unleased version (or removed its live one)")
	require.True(t, crossSystemMatches(sharedB, "shared2.example"),
		"box B's live rules were collected with the old version")

	// Box B closes. Its live version is the newest and therefore retained by the documented
	// one-newest rule; no temp directory may be left behind by either close.
	require.NoError(t, serviceB.CloseService())
	require.NoError(t, independentStore.Collect(sharedKey))
	liveVersions := crossSystemVersionsFor(t, storeRoot, sharedKey)
	require.Len(t, liveVersions, 1)
	require.Equal(t, crossSystemDigest(sharedV2), liveVersions[0].Digest)
	loadedVersion, err := independentStore.Load(sharedKey)
	require.NoError(t, err, "the retained version must still be loadable")
	require.Equal(t, crossSystemDigest(sharedV2), loadedVersion.ID())
	loadedVersion.Release()

	leftovers := 0
	require.NoError(t, filepath.WalkDir(storeRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && strings.HasPrefix(entry.Name(), ".tmp-") {
			leftovers++
		}
		return nil
	}))
	require.Zero(t, leftovers, "a closed Box left a temp artifact directory behind")
}

// TestConcurrentBoxesCannotShareOneCacheFileButDoShareTheArtifactDirectory is the boundary the
// two-Box scenario must state honestly.
//
// The artifact store is designed to be shared: process-local leases keyed by the store directory.
// The cache file is NOT: it is a bbolt database opened with an exclusive lock, and a second Box on
// the same working directory is refused while the first holds it. The refusal is the correct
// behaviour (the alternative is two writers on one database), and it is asserted here so the
// evidence cannot claim "two Boxes share a working directory" when they cannot.
//
// The second half then starts the second Box the way the product can: same working directory
// (therefore the same artifact store directory) and a cache-file path of its own. Both Boxes keep
// their provider subscriptions apart.
func TestConcurrentBoxesCannotShareOneCacheFileButDoShareTheArtifactDirectory(t *testing.T) {
	directory := t.TempDir()
	documentA := []byte(`{"outbounds": [{"type": "cross-system-node", "tag": "from-a"}]}`)
	documentB := []byte(`{"outbounds": [{"type": "cross-system-node", "tag": "from-b"}]}`)
	serverA := newCrossSystemDocumentServer(t, documentA)
	serverB := newCrossSystemDocumentServer(t, documentB)

	serviceA := NewStartedService(ServiceOptions{Context: crossSystemContext(t, directory, &crossSystemRecorder{})})
	t.Cleanup(func() {
		_ = serviceA.CloseService()
		serviceA.Close()
	})
	require.NoError(t, serviceA.StartOrReloadService(context.Background(), crossSystemProviderProfile(serverA.server.URL), nil))
	providerA, loaded := serviceA.instance.providerManager.Get("nodes")
	require.True(t, loaded)
	require.Eventually(t, func() bool {
		return len(providerNodesOf(t, providerA)) == 1
	}, 10*time.Second, 5*time.Millisecond, "box A never published")
	require.Equal(t, []string{"nodes/from-a"}, providerNodesOf(t, providerA))

	// Same working directory, default cache file: the second Box must be refused, and the refusal
	// must name the cache file. bbolt's own open timeout bounds this (the product sets 1s), so the
	// call returns rather than hanging.
	refusedService := NewStartedService(ServiceOptions{Context: crossSystemContext(t, directory, &crossSystemRecorder{})})
	t.Cleanup(func() {
		_ = refusedService.CloseService()
		refusedService.Close()
	})
	err := refusedService.StartOrReloadService(context.Background(), crossSystemProviderProfile(serverB.server.URL), nil)
	require.Error(t, err, "a second Box on one cache file must not start")
	require.Contains(t, err.Error(), "cache-file",
		"the refusal must name the cache file, so an operator can tell it from a provider failure: %v", err)

	// Box A is untouched by the failed start: it still serves its own provider document.
	require.Equal(t, []string{"nodes/from-a"}, providerNodesOf(t, providerA))

	// A cache-file path of its own is what makes the two concurrent Boxes possible while the
	// artifact store directory stays shared.
	serviceB := NewStartedService(ServiceOptions{Context: crossSystemContext(t, directory, &crossSystemRecorder{})})
	t.Cleanup(func() {
		_ = serviceB.CloseService()
		serviceB.Close()
	})
	require.NoError(t, serviceB.StartOrReloadService(context.Background(),
		crossSystemProviderProfileWithCache(serverB.server.URL, filepath.Join(directory, "cache-b.db")), nil))
	providerB, loaded := serviceB.instance.providerManager.Get("nodes")
	require.True(t, loaded)
	require.Eventually(t, func() bool {
		return len(providerNodesOf(t, providerB)) == 1
	}, 10*time.Second, 5*time.Millisecond, "box B never published")
	require.Equal(t, []string{"nodes/from-b"}, providerNodesOf(t, providerB),
		"box B published box A's subscription: provider state was shared")
	require.GreaterOrEqual(t, serverB.requests.Load(), int32(1),
		"box B must have fetched its own document")
	require.Equal(t, []string{"nodes/from-a"}, providerNodesOf(t, providerA),
		"box A's published set changed when box B started")
}
