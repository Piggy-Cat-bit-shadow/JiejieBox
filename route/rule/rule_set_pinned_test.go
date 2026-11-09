package rule

// C2 閳?a recovered configuration pins the exact rule-set version it used.
//
// The store may already hold a newer version (a refresh that happened after the document was
// saved). A Box built for recovery must load the pinned digest, not the newest one, and must
// record that digest back into the per-Box reference set.

import (
	"bytes"
	"os"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/artifact"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

func TestARecoveredRuleSetLoadsItsPinnedVersion(t *testing.T) {
	ctx := service.ContextWithDefaultRegistry(ruleSetArtifactContext(t))
	ruleSet := newStoredRemoteRuleSet(t, ctx, "https://rules.example/pinned.srs", "pinned")
	require.NotNil(t, ruleSet.artifactStore)

	pinnedPayload := writeRuleSetPayload(t, "10.0.0.0/8")
	newerPayload := writeRuleSetPayload(t, "192.168.0.0/16")
	pinnedVersion, err := ruleSet.artifactStore.Commit(ruleSet.artifactKey, ruleSet.newArtifactManifest(), bytes.NewReader(pinnedPayload))
	require.NoError(t, err)
	pinnedID := pinnedVersion.ID()
	pinnedVersion.Release()
	time.Sleep(time.Millisecond)
	newerVersion, err := ruleSet.artifactStore.Commit(ruleSet.artifactKey, ruleSet.newArtifactManifest(), bytes.NewReader(newerPayload))
	require.NoError(t, err)
	newerVersion.Release()

	references := artifact.NewReferenceSet(map[string]string{ruleSet.artifactKey: pinnedID}, nil)
	service.MustRegister[*artifact.ReferenceSet](ctx, references)
	ruleSet.cacheFile = nil

	restored, restoreErr := ruleSet.restoreStoredRuleSet()
	require.NoError(t, restoreErr)
	require.True(t, restored, "the pinned version must restore")
	require.Equal(t, pinnedID, ruleSet.artifactLease.ID())
	require.True(t, remoteRuleSetMatches(t, ruleSet, "10.1.2.3"), "the pinned rules are live")
	require.False(t, remoteRuleSetMatches(t, ruleSet, "192.168.1.1"), "the newer version is not silently used")
	recorded := references.Snapshot()
	require.Equal(t, pinnedID, recorded[ruleSet.artifactKey])

	// The pinned version is retained during collection even with no reader after Close.
	require.NoError(t, ruleSet.Close())
	require.NoError(t, ruleSet.artifactStore.Collect(ruleSet.artifactKey, pinnedID))
	exact, err := ruleSet.artifactStore.LoadExact(ruleSet.artifactKey, pinnedID)
	require.NoError(t, err, "the retained version survives collection")
	exact.Release()
}

// TestAMissingPinnedVersionWithoutMatchingCacheFailsClosed is the 1C rule: recovery must not
// silently serve the newest version when the configuration pinned another digest.
func TestAMissingPinnedVersionWithoutMatchingCacheFailsClosed(t *testing.T) {
	ctx := service.ContextWithDefaultRegistry(ruleSetArtifactContext(t))
	ruleSet := newStoredRemoteRuleSet(t, ctx, "https://rules.example/missing.srs", "missing")
	pinnedPayload := writeRuleSetPayload(t, "10.0.0.0/8")
	otherPayload := writeRuleSetPayload(t, "192.168.0.0/16")
	pinnedVersion, err := ruleSet.artifactStore.Commit(ruleSet.artifactKey, ruleSet.newArtifactManifest(), bytes.NewReader(pinnedPayload))
	require.NoError(t, err)
	pinnedID := pinnedVersion.ID()
	pinnedVersion.Release()
	require.NoError(t, ruleSet.artifactStore.Discard(ruleSet.artifactKey, pinnedID))

	references := artifact.NewReferenceSet(map[string]string{ruleSet.artifactKey: pinnedID}, nil)
	service.MustRegister[*artifact.ReferenceSet](ctx, references)
	ruleSet.cacheFile = &fakeRuleSetCache{saved: &adapter.SavedBinary{
		Content: otherPayload,
		URLHash: ruleSet.urlHash[:],
	}}
	restored, restoreErr := ruleSet.restoreStoredRuleSet()
	require.Error(t, restoreErr, "a pinned version that is gone cannot be replaced by another version")
	require.False(t, restored)
	require.Nil(t, ruleSet.artifactLease)
	require.False(t, remoteRuleSetMatches(t, ruleSet, "192.168.1.1"), "the unpinned version must not be live")
}

// TestADamagedPinnedVersionIsRepairedFromMatchingCacheBytes covers the recoverable branch: the
// cache still holds exactly the pinned bytes, so the version is recreated before serving.
func TestADamagedPinnedVersionIsRepairedFromMatchingCacheBytes(t *testing.T) {
	ctx := service.ContextWithDefaultRegistry(ruleSetArtifactContext(t))
	ruleSet := newStoredRemoteRuleSet(t, ctx, "https://rules.example/repair.srs", "repair")
	pinnedPayload := writeRuleSetPayload(t, "10.0.0.0/8")
	pinnedVersion, err := ruleSet.artifactStore.Commit(ruleSet.artifactKey, ruleSet.newArtifactManifest(), bytes.NewReader(pinnedPayload))
	require.NoError(t, err)
	pinnedID := pinnedVersion.ID()
	pinnedVersion.Release()
	require.NoError(t, os.Remove(pinnedVersion.Path()))

	references := artifact.NewReferenceSet(map[string]string{ruleSet.artifactKey: pinnedID}, nil)
	service.MustRegister[*artifact.ReferenceSet](ctx, references)
	ruleSet.cacheFile = &fakeRuleSetCache{saved: &adapter.SavedBinary{
		Content: pinnedPayload,
		URLHash: ruleSet.urlHash[:],
	}}
	restored, restoreErr := ruleSet.restoreStoredRuleSet()
	require.NoError(t, restoreErr)
	require.True(t, restored)
	require.True(t, remoteRuleSetMatches(t, ruleSet, "10.1.2.3"))
	require.Equal(t, pinnedID, ruleSet.artifactLease.ID())
	repaired, err := ruleSet.artifactStore.LoadExact(ruleSet.artifactKey, pinnedID)
	require.NoError(t, err, "the damaged version is recreated from the cache")
	repaired.Release()
}
