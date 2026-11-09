package rule

// C2 鈥?a recovered configuration pins the exact rule-set version it used.
//
// The store may already hold a newer version (a refresh that happened after the document was
// saved). A Box built for recovery must load the pinned digest, not the newest one, and must
// record that digest back into the per-Box reference set.

import (
	"bytes"
	"testing"
	"time"

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

	require.True(t, ruleSet.restoreStoredRuleSet(), "the pinned version must restore")
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
