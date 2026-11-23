package rule

// F-CODEX-01 on the product path — a damaged stored source must be repaired by the next successful
// download, so the offline restore after a network failure still works.
//
// The reviewer's probe used the real RemoteRuleSet source store, fetch, build and restore without a
// legacy CacheFile and measured:
//
//	valid re-download: liveGood=true storedGood=false offlineRestore=false err=<nil>
//
// The live rules were built from the bytes the server sent, so ordinary online use looked healthy;
// only the DURABLE source was still the damaged one, which is what a later offline start loads. The
// fix is in the shared artifact store (publishVersion no longer reuses a version directory on the
// strength of its name), and these tests keep the product-level oracle: the bytes the live rules were
// built from are the bytes the next offline restore loads.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/sagernet/sing-box/common/artifact"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// damagedSourceServer serves the given payload.
func damagedSourceServer(t *testing.T, payload []byte) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write(payload)
	}))
	t.Cleanup(server.Close)
	return server
}

// TestAReDownloadRepairsADamagedStoredSource is the reviewer's probe as a repository regression.
func TestAReDownloadRepairsADamagedStoredSource(t *testing.T) {
	ctx := ruleSetArtifactContext(t)
	good := writeRuleSetPayload(t, "10.0.0.0/8")
	bad := writeRuleSetPayload(t, "192.168.0.0/16")
	server := damagedSourceServer(t, good)

	first := newStoredRemoteRuleSet(t, ctx, server.URL, "repair")
	first.httpClient = server.Client()
	require.NoError(t, first.fetch(ctx, true))
	sourcePath := first.artifactLease.Path()
	firstID := first.artifactLease.ID()
	require.NoError(t, first.Close())

	// The published source is damaged, while its manifest still names the good bytes.
	require.NoError(t, os.WriteFile(sourcePath, bad, 0o600))

	second := newStoredRemoteRuleSet(t, ctx, server.URL, "repair")
	second.httpClient = server.Client()
	restored, restoreErr := second.restoreStoredRuleSet()
	require.NoError(t, restoreErr)
	require.False(t, restored, "control: the damaged source must not restore")

	require.NoError(t, second.fetch(ctx, true))
	require.True(t, remoteRuleSetMatches(t, second, "10.1.2.3"), "the live rules come from the good bytes")
	stored, err := os.ReadFile(second.artifactLease.Path())
	require.NoError(t, err)
	require.Equal(t, good, stored, "the successful re-download must repair the durable source")
	require.Equal(t, firstID, second.artifactLease.ID(),
		"the repaired source keeps the digest the live rules were built from")
	require.NoError(t, second.Close())

	// The next start is offline: it must load the repaired bytes, with no server contact.
	third := newStoredRemoteRuleSet(t, ctx, server.URL, "repair")
	third.httpClient = server.Client()
	restored, restoreErr = third.restoreStoredRuleSet()
	require.NoError(t, restoreErr)
	require.True(t, restored, "a repaired source must restore offline")
	require.True(t, remoteRuleSetMatches(t, third, "10.1.2.3"))
	require.False(t, remoteRuleSetMatches(t, third, "192.168.1.1"))
	require.NoError(t, third.Close())
}

// TestAPinnedReDownloadRepairsADamagedStoredSource is the same closure for a recovery configuration:
// the configuration pins the digest, the pinned bytes are damaged on disk, and the re-download
// repairs exactly that identity instead of leaving the pinned recovery broken.
func TestAPinnedReDownloadRepairsADamagedStoredSource(t *testing.T) {
	ctx := service.ContextWithDefaultRegistry(ruleSetArtifactContext(t))
	good := writeRuleSetPayload(t, "10.0.0.0/8")
	bad := writeRuleSetPayload(t, "192.168.0.0/16")
	server := damagedSourceServer(t, good)

	seed := newStoredRemoteRuleSet(t, ctx, server.URL, "pinned-repair")
	seed.httpClient = server.Client()
	require.NoError(t, seed.fetch(ctx, true))
	pinnedID := seed.artifactLease.ID()
	sourcePath := seed.artifactLease.Path()
	require.NoError(t, seed.Close())

	references := artifact.NewReferenceSet(map[string]string{seed.artifactKey: pinnedID}, nil)
	service.MustRegister[*artifact.ReferenceSet](ctx, references)
	require.NoError(t, os.WriteFile(sourcePath, bad, 0o600))

	recovering := newStoredRemoteRuleSet(t, ctx, server.URL, "pinned-repair")
	recovering.httpClient = server.Client()
	restored, restoreErr := recovering.restoreStoredRuleSet()
	require.Error(t, restoreErr, "a pinned version that is damaged and has no cache must fail closed")
	require.False(t, restored, "recovery must not silently serve another version")

	// The refresh path repairs the pinned bytes; a later start is then able to restore that exact
	// identity offline.
	require.NoError(t, recovering.fetch(ctx, true))
	require.Equal(t, pinnedID, recovering.artifactLease.ID())
	require.True(t, remoteRuleSetMatches(t, recovering, "10.1.2.3"))
	require.NoError(t, recovering.Close())

	// LoadExact is what the pinned restore uses; the repaired version must satisfy it exactly.
	exact, err := recovering.artifactStore.LoadExact(recovering.artifactKey, pinnedID)
	require.NoError(t, err, "the pinned identity must be intact after the repair")
	defer exact.Release()
	body, err := os.ReadFile(exact.Path())
	require.NoError(t, err)
	require.Equal(t, good, body)

	repaired := newStoredRemoteRuleSet(t, ctx, server.URL, "pinned-repair")
	repaired.httpClient = server.Client()
	restored, restoreErr = repaired.restoreStoredRuleSet()
	require.NoError(t, restoreErr, "the repaired pinned version must restore")
	require.True(t, restored)
	require.Equal(t, pinnedID, repaired.artifactLease.ID())
	require.True(t, remoteRuleSetMatches(t, repaired, "10.1.2.3"))
	require.NoError(t, repaired.Close())
}

// TestADamagedSourceThatCannotBeRepairedFailsTheDownloadKeepsTheLiveRules is the failure side: a body
// that is not a rule set must not damage anything, and the live generation must be untouched.
func TestADamagedSourceThatCannotBeRepairedFailsTheDownloadKeepsTheLiveRules(t *testing.T) {
	ctx := ruleSetArtifactContext(t)
	good := writeRuleSetPayload(t, "10.0.0.0/8")
	server := damagedSourceServer(t, good)

	live := newStoredRemoteRuleSet(t, ctx, server.URL, "refuse")
	live.httpClient = server.Client()
	require.NoError(t, live.fetch(ctx, true))
	liveID := live.artifactLease.ID()
	sourcePath := live.artifactLease.Path()

	// A second server that answers with bytes that are not a rule set at all.
	garbage := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("this is not a rule set"))
	}))
	defer garbage.Close()
	live.url = garbage.URL
	live.httpClient = garbage.Client()
	fetchErr := live.fetch(ctx, false)
	require.Error(t, fetchErr, "an unparseable body must fail the refresh")
	require.Equal(t, liveID, live.artifactLease.ID(), "the live generation is unchanged")
	require.True(t, remoteRuleSetMatches(t, live, "10.1.2.3"))
	require.NoError(t, live.Close())

	stored, err := os.ReadFile(sourcePath)
	require.NoError(t, err)
	require.Equal(t, good, stored, "a failed download must not damage the stored source")
}
