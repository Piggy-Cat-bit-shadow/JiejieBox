package daemon

// C2/C3 — a recovery pins the exact rule-set artifact the saved configuration used.
//
// The store is allowed to hold a newer version than the configuration that saved it: a periodic
// refresh commits new bytes while the old document is still the active one. A recovery must load
// the version named by the last-good record, not the newest one. This test drives that through the
// real entry and a real HTTP request:
//
//	A starts (rule-set version A, rejects the loopback target), and saves A plus its reference;
//	the source changes to B and A's updater commits B while A is still running;
//	B starts with a different document, restores version B, runs, but the disk refuses to save it;
//	C fails to start; recovery starts A with the pinned digest A;
//	the request is rejected again, while the store's newest version is still B.

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/artifact"
	"github.com/sagernet/sing/service/filemanager"

	"github.com/stretchr/testify/require"
)

func recoveryReferenceConfig(proxyPort int, sourceURL string, logLevel string, updateInterval string) string {
	return fmt.Sprintf(`{
  "log": {"level": %q},
  "inbounds": [{"type": "http", "tag": "proxy", "listen": "127.0.0.1", "listen_port": %d}],
  "outbounds": [{"type": "direct", "tag": "direct"}],
  "route": {
    "rule_set": [{"type": "remote", "tag": "gate", "format": "binary", "url": %q, "update_interval": %q}],
    "rules": [{"rule_set": "gate", "action": "reject"}],
    "final": "direct"
  }
}`, logLevel, proxyPort, sourceURL, updateInterval)
}

func TestARecoveryPinsTheExactRuleSetVersion(t *testing.T) {
	ctx := recoveryBusinessContext(t)
	storeRoot := filemanager.BasePath(ctx, "rule-set-artifacts")

	rejectLoopback := binaryRuleSetForTest(t, "127.0.0.1/32")
	allowLoopback := binaryRuleSetForTest(t, "10.0.0.0/8")
	ruleSetServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write(rejectLoopback)
	}))
	t.Cleanup(ruleSetServer.Close)
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("ok"))
	}))
	t.Cleanup(target.Close)

	directory := t.TempDir()
	proxyPort := freeLoopbackPort(t)
	configA := recoveryReferenceConfig(proxyPort, ruleSetServer.URL, "error", "1h")
	configB := recoveryReferenceConfig(proxyPort, ruleSetServer.URL, "warn", "1h")
	revisionA := configRevisionOf(configA)
	revisionB := configRevisionOf(configB)

	store := recoveryStoreWithWriteFault(t, directory, []byte(configB))
	service := NewStartedService(ServiceOptions{Context: ctx})
	t.Cleanup(func() {
		_ = service.CloseService()
		service.Close()
	})
	service.ConfigureConfigRecovery(directory, true)
	installStore(service, store)

	require.NoError(t, service.StartOrReloadService(context.Background(), configA, nil))
	client := newProxyClient(t, proxyPort)
	defer client.CloseIdleConnections()
	served := func() bool {
		status, err := proxyStatus(t, client, target.URL)
		return err == nil && status == http.StatusOK
	}
	require.False(t, served(), "A's rule-set version rejects the loopback target")

	referencesA, err := store.References(revisionA)
	require.NoError(t, err)
	require.Len(t, referencesA, 1, "the saved configuration names its rule-set version")
	var artifactKey, pinnedDigest string
	for key, digest := range referencesA {
		artifactKey, pinnedDigest = key, digest
	}
	require.Equal(t, ruleSetDigest(rejectLoopback), pinnedDigest)
	require.Contains(t, artifactKey, "gate")

	// A newer version of the same artifact appears in the store, exactly as a completed refresh
	// would have committed it. A is still the saved configuration, so its reference stays A.
	opened, err := artifact.Open(storeRoot)
	require.NoError(t, err)
	newerManifest := artifact.NewManifest("rule-set/binary", "test", "tag=gate", "url="+ruleSetServer.URL)
	newerManifest.Created = time.Now().Add(time.Minute).UnixNano()
	newerVersion, err := opened.Commit(artifactKey, newerManifest, bytes.NewReader(allowLoopback))
	require.NoError(t, err)
	require.NotEqual(t, pinnedDigest, newerVersion.ID())
	newerVersion.Release()

	// B runs with the newer bytes, but the disk refuses to save B.
	require.NoError(t, service.StartOrReloadService(context.Background(), configB, nil))
	client.CloseIdleConnections()
	require.True(t, served(), "B serves through the newer rule-set version")
	bReport := service.LastServiceChange()
	require.Equal(t, persistenceDegraded, bReport.Persistence)
	referencesB, err := store.References(revisionB)
	require.NoError(t, err)
	require.Empty(t, referencesB, "B was not saved, so it must not become a recovery target")

	// C fails; recovery returns to A and pins A's digest although B is the newest stored version.
	require.NoError(t, service.StartOrReloadService(context.Background(), buildFailingConfig, nil))
	client.CloseIdleConnections()
	report := service.LastServiceChange()
	require.Equal(t, ServiceChangeStatusRecovered, report.Status)
	require.Equal(t, revisionA, report.ActiveRevision)
	require.False(t, served(), "the recovered A rule-set version is what serves, not the newest B")

	opened, err = artifact.Open(storeRoot)
	require.NoError(t, err)
	newest, err := opened.Load(artifactKey)
	require.NoError(t, err)
	require.Equal(t, ruleSetDigest(allowLoopback), newest.ID(), "the store's newest version is still B")
	newest.Release()
	exact, err := opened.LoadExact(artifactKey, pinnedDigest)
	require.NoError(t, err, "A's exact version was retained for the recovery")
	exact.Release()
}

// TestARecoveryRepairsAMissingPinnedArtifactFromTheCache is the 1C entry-level case: the pinned
// version's bytes are gone, but the legacy cache still holds exactly those bytes. Recovery may
// use them, and must not fall back to the newer version that happens to be in the store.
func TestARecoveryRepairsAMissingPinnedArtifactFromTheCache(t *testing.T) {
	ctx := recoveryBusinessContext(t)
	storeRoot := filemanager.BasePath(ctx, "rule-set-artifacts")

	rejectLoopback := binaryRuleSetForTest(t, "127.0.0.1/32")
	allowLoopback := binaryRuleSetForTest(t, "10.0.0.0/8")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write(rejectLoopback)
	}))
	t.Cleanup(server.Close)
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("ok"))
	}))
	t.Cleanup(target.Close)

	directory := t.TempDir()
	proxyPort := freeLoopbackPort(t)
	configA := recoveryReferenceConfig(proxyPort, server.URL, "error", "1h")
	configB := recoveryReferenceConfig(proxyPort, server.URL, "warn", "1h")
	revisionA := configRevisionOf(configA)

	store := recoveryStoreWithWriteFault(t, directory, []byte(configB))
	service := NewStartedService(ServiceOptions{Context: ctx})
	t.Cleanup(func() {
		_ = service.CloseService()
		service.Close()
	})
	service.ConfigureConfigRecovery(directory, true)
	installStore(service, store)

	require.NoError(t, service.StartOrReloadService(context.Background(), configA, nil))
	client := newProxyClient(t, proxyPort)
	defer client.CloseIdleConnections()
	served := func() bool {
		status, err := proxyStatus(t, client, target.URL)
		return err == nil && status == http.StatusOK
	}
	require.False(t, served(), "A rejects the loopback target")

	referencesA, err := store.References(revisionA)
	require.NoError(t, err)
	var artifactKey, pinnedDigest string
	for key, digest := range referencesA {
		artifactKey, pinnedDigest = key, digest
	}
	require.NotEmpty(t, pinnedDigest)

	opened, err := artifact.Open(storeRoot)
	require.NoError(t, err)
	manifest := artifact.NewManifest("rule-set/binary", "test", "tag=gate")
	manifest.Created = time.Now().Add(time.Minute).UnixNano()
	newer, err := opened.Commit(artifactKey, manifest, bytes.NewReader(allowLoopback))
	require.NoError(t, err)
	newer.Release()

	require.NoError(t, service.StartOrReloadService(context.Background(), configB, nil))
	client.CloseIdleConnections()
	require.True(t, served(), "B serves the newer version")
	require.Equal(t, persistenceDegraded, service.LastServiceChange().Persistence)

	// Damage the pinned version's bytes; the manifest stays, so this is the "cannot load" case.
	pinnedVersion, err := opened.LoadExact(artifactKey, pinnedDigest)
	require.NoError(t, err)
	require.NoError(t, os.Remove(pinnedVersion.Path()))
	pinnedVersion.Release()

	require.NoError(t, service.StartOrReloadService(context.Background(), buildFailingConfig, nil))
	client.CloseIdleConnections()
	require.Equal(t, ServiceChangeStatusRecovered, service.LastServiceChange().Status)
	require.False(t, served(), "recovery must serve A's rules, not the newer B version")
	repaired, err := opened.LoadExact(artifactKey, pinnedDigest)
	require.NoError(t, err, "the cache bytes repaired the damaged pinned version")
	repaired.Release()
}
