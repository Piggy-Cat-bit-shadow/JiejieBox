//go:build unix

package rule

// E01-E09 of the remote derived-cache matrix: error injection and attack cases.
//
// The invariant under every case is the same: the source is authoritative and the derived cache is
// only an accelerator. A cached conversion must never hide a truncated/cancelled/oversized download,
// never survive an invalid source, never be served when its manifest names other bytes, and a
// damaged cache must be a safe miss that falls back to the source. The diagnostic counters make the
// "no early hit" claim exact: a download that fails before publication must not increment the hit or
// miss counters at all.

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/artifact"
	"github.com/sagernet/sing-box/common/srs"
	C "github.com/sagernet/sing-box/constant"

	"github.com/stretchr/testify/require"
)

// remoteDerivedTestStore opens the shared store so a test can publish a deliberately damaged
// version or inspect what a production load sees.
func remoteDerivedTestStore(t *testing.T, base string) *artifact.Store {
	t.Helper()
	store, err := artifact.Open(filepath.Join(base, derivedStoreDirectory))
	require.NoError(t, err)
	return store
}

// remoteDerivedTestCommitManifest re-publishes one artifact's bytes under a chosen manifest. It is
// how the E04/E05 cases place a version the store's own byte verification accepts but the derived
// identity check must refuse.
func remoteDerivedTestCommitManifest(t *testing.T, base string, key string, manifest artifact.Manifest, content []byte) remoteDerivedTestStoreVersion {
	t.Helper()
	store := remoteDerivedTestStore(t, base)
	manifest.Created = time.Now().Add(time.Hour).UnixNano()
	version, err := store.Commit(key, manifest, bytes.NewReader(content))
	require.NoError(t, err)
	committed := remoteDerivedTestStoreVersion{
		Key:      key,
		Dir:      filepath.Dir(version.Path()),
		Manifest: version.Manifest(),
	}
	version.Release()
	return committed
}

func remoteDerivedTestRemoveDerivedVersions(t *testing.T, base string) {
	t.Helper()
	for _, version := range remoteDerivedTestStoreDerivedVersions(t, base) {
		require.NoError(t, os.RemoveAll(version.Dir))
	}
}

// TestRemoteDerivedE01FailedDownloadsCannotHitOrPublish is E01: with a valid cached copy present, a
// truncated, cancelled or oversized body must fail before any derived lookup or publication and
// must leave the previous generation and its artifacts exactly as they were.
func TestRemoteDerivedE01FailedDownloadsCannotHitOrPublish(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	payloadA := writeRuleSetPayload(t, "10.0.0.0/8")
	payloadB := writeRuleSetPayload(t, "192.168.0.0/16")

	t.Run("truncated", func(t *testing.T) {
		ctx, base := derivedTestContext(t)
		var truncate atomic.Bool
		fixture := newRemoteDerivedTestHTTP(t, ctx, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			if truncate.Load() {
				// Claim more bytes than will be sent: the stage's length check must reject it.
				writer.Header().Set("Content-Length", strconv.Itoa(len(payloadB)+1024))
				_, _ = writer.Write(payloadB)
				return
			}
			_, _ = writer.Write(payloadA)
		}))
		ruleSet := startRemoteDerivedTestRuleSet(t, fixture.ctx, "e01-truncated", fixture.server.URL, C.RuleSetFormatBinary)
		sourceID := ruleSet.artifactLease.ID()
		require.Equal(t, ruleSetDigest(payloadA), sourceID)

		truncate.Store(true)
		resetRemoteDerivedDiagnostics()
		err := ruleSet.fetch(ctx, false)
		require.Error(t, err, "a body shorter than its Content-Length must fail the refresh")
		require.True(t, remoteRuleSetMatches(t, ruleSet, "10.1.2.3"), "the old generation must keep serving")
		require.Equal(t, sourceID, ruleSet.artifactLease.ID())
		requireRemoteDerivedCounters(t, "truncated body", 0, 0, 0, 0, 0)
		require.Len(t, remoteDerivedTestStoreSourceVersions(t, base), 1)
		require.Len(t, remoteDerivedTestStoreDerivedVersions(t, base), 1)
		remoteDerivedTestVersionForSource(t, base, ruleSetDigest(payloadA))
	})

	t.Run("cancelled", func(t *testing.T) {
		ctx, base := derivedTestContext(t)
		var block atomic.Bool
		started := make(chan struct{})
		fixture := newRemoteDerivedTestHTTP(t, ctx, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if !block.Load() {
				_, _ = writer.Write(payloadA)
				return
			}
			writer.Header().Set("Content-Length", strconv.Itoa(len(payloadB)))
			_, _ = writer.Write(payloadB[:len(payloadB)/2])
			if flusher, isFlusher := writer.(http.Flusher); isFlusher {
				flusher.Flush()
			}
			close(started)
			<-request.Context().Done()
		}))
		ruleSet := startRemoteDerivedTestRuleSet(t, fixture.ctx, "e01-cancelled", fixture.server.URL, C.RuleSetFormatBinary)
		sourceID := ruleSet.artifactLease.ID()
		block.Store(true)
		cancelCtx, cancel := context.WithCancel(context.Background())
		defer cancel()
		resetRemoteDerivedDiagnostics()
		result := make(chan error, 1)
		go func() { result <- ruleSet.fetch(cancelCtx, false) }()
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("the refresh never reached the partial body")
		}
		cancel()
		select {
		case err := <-result:
			require.Error(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("the refresh did not return after cancellation")
		}
		require.True(t, remoteRuleSetMatches(t, ruleSet, "10.1.2.3"), "a cancelled refresh must not replace the live rules")
		require.False(t, remoteRuleSetMatches(t, ruleSet, "192.168.1.1"))
		require.Equal(t, sourceID, ruleSet.artifactLease.ID())
		requireRemoteDerivedCounters(t, "cancelled body", 0, 0, 0, 0, 0)
		require.Len(t, remoteDerivedTestStoreSourceVersions(t, base), 1)
		require.Len(t, remoteDerivedTestStoreDerivedVersions(t, base), 1)
	})

	t.Run("oversized", func(t *testing.T) {
		ctx, base := derivedTestContext(t)
		server := newRemoteDerivedTestSourceServer(t, payloadA, "")
		fixture := newRemoteDerivedTestHTTP(t, ctx, server)
		ruleSet := startRemoteDerivedTestRuleSet(t, fixture.ctx, "e01-oversized", fixture.server.URL, C.RuleSetFormatBinary)
		sourceID := ruleSet.artifactLease.ID()
		previousLimit := maxRemoteRuleSetSize
		maxRemoteRuleSetSize = 8
		t.Cleanup(func() { maxRemoteRuleSetSize = previousLimit })
		server.setPayload(payloadB)
		resetRemoteDerivedDiagnostics()
		err := ruleSet.fetch(ctx, false)
		require.Error(t, err, "a body over the limit must fail the refresh")
		require.True(t, remoteRuleSetMatches(t, ruleSet, "10.1.2.3"))
		require.Equal(t, sourceID, ruleSet.artifactLease.ID())
		requireRemoteDerivedCounters(t, "oversized body", 0, 0, 0, 0, 0)
		require.Len(t, remoteDerivedTestStoreSourceVersions(t, base), 1)
		require.Len(t, remoteDerivedTestStoreDerivedVersions(t, base), 1)
	})
}

// TestRemoteDerivedE02SameETagDifferentContent is E02: the ETag is not content identity. A 200 that
// reuses the previous ETag but carries different bytes must miss, parse and serve the NEW bytes.
func TestRemoteDerivedE02SameETagDifferentContent(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	ctx, base := derivedTestContext(t)
	payloadA := writeRuleSetPayload(t, "10.0.0.0/8")
	payloadB := writeRuleSetPayload(t, "192.168.0.0/16")
	server := newRemoteDerivedTestSourceServer(t, payloadA, `"same-tag"`)
	fixture := newRemoteDerivedTestHTTP(t, ctx, server)
	ruleSet := startRemoteDerivedTestRuleSet(t, fixture.ctx, "e02-etag", fixture.server.URL, C.RuleSetFormatBinary)
	require.Equal(t, ruleSetDigest(payloadA), ruleSet.artifactLease.ID())
	_, etagAfterCold := ruleSet.updateMetadata()
	require.Equal(t, `"same-tag"`, etagAfterCold)

	server.setPayload(payloadB)
	resetRemoteDerivedDiagnostics()
	require.NoError(t, ruleSet.fetch(ctx, false))
	require.True(t, remoteRuleSetMatches(t, ruleSet, "192.168.1.1"), "the real bytes decide, not the ETag")
	require.False(t, remoteRuleSetMatches(t, ruleSet, "10.1.2.3"), "the old ETag must not reuse the old rules")
	require.Equal(t, ruleSetDigest(payloadB), ruleSet.artifactLease.ID())
	_, etagAfterChange := ruleSet.updateMetadata()
	require.Equal(t, `"same-tag"`, etagAfterChange, "the fixture really reused the ETag")
	requireRemoteDerivedCounters(t, "same ETag new content", 0, 1, 1, 1, 0)
	remoteDerivedTestVersionForSource(t, base, ruleSetDigest(payloadB))
}

// TestRemoteDerivedE03InvalidSourceKeepsTheOriginalError is E03: an unparseable body is still the
// parser's error, the previous generation keeps serving, and nothing is published for the bad
// bytes. Both source formats are covered separately.
func TestRemoteDerivedE03InvalidSourceKeepsTheOriginalError(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	t.Run("binary", func(t *testing.T) {
		ctx, base := derivedTestContext(t)
		payload := writeRuleSetPayload(t, "10.0.0.0/8")
		server := newRemoteDerivedTestSourceServer(t, payload, "")
		fixture := newRemoteDerivedTestHTTP(t, ctx, server)
		ruleSet := startRemoteDerivedTestRuleSet(t, fixture.ctx, "e03-binary", fixture.server.URL, C.RuleSetFormatBinary)
		sourceID := ruleSet.artifactLease.ID()

		server.setPayload([]byte("this is not a binary rule set"))
		resetRemoteDerivedDiagnostics()
		err := ruleSet.fetch(ctx, false)
		require.Error(t, err, "an unparseable body must fail the refresh")
		require.Contains(t, err.Error(), "invalid sing-box rule-set file", "the parser's own error must survive")
		require.True(t, remoteRuleSetMatches(t, ruleSet, "10.1.2.3"), "the previous generation keeps serving")
		require.Equal(t, sourceID, ruleSet.artifactLease.ID())
		requireRemoteDerivedCounters(t, "invalid binary", 0, 1, 1, 0, 0)
		require.Len(t, remoteDerivedTestStoreSourceVersions(t, base), 1, "no version for the bad body")
		require.Len(t, remoteDerivedTestStoreDerivedVersions(t, base), 1)
		remoteDerivedTestVersionForSource(t, base, ruleSetDigest(payload))
		require.Zero(t, countTempArtifactDirs(t, ruleSetArtifactRoot(t, ctx)), "the staged temp must be discarded")
	})

	t.Run("source-json", func(t *testing.T) {
		ctx, base := derivedTestContext(t)
		payload := derivedJSONDomainSource("e03.example")
		server := newRemoteDerivedTestSourceServer(t, payload, "")
		fixture := newRemoteDerivedTestHTTP(t, ctx, server)
		ruleSet := startRemoteDerivedTestRuleSet(t, fixture.ctx, "e03-json", fixture.server.URL, C.RuleSetFormatSource)
		sourceID := ruleSet.artifactLease.ID()

		server.setPayload([]byte(`{"version":1,`))
		resetRemoteDerivedDiagnostics()
		err := ruleSet.fetch(ctx, false)
		require.Error(t, err, "invalid JSON must fail the refresh")
		requireRemoteDerivedDomainMatch(t, ruleSet, "e03.example", true)
		require.Equal(t, sourceID, ruleSet.artifactLease.ID())
		requireRemoteDerivedCounters(t, "invalid JSON", 0, 1, 1, 0, 0)
		require.Len(t, remoteDerivedTestStoreSourceVersions(t, base), 1)
		require.Len(t, remoteDerivedTestStoreDerivedVersions(t, base), 1)
		require.Zero(t, countTempArtifactDirs(t, ruleSetArtifactRoot(t, ctx)))
	})
}

// TestRemoteDerivedE04CorruptConversionFallsBackAndRepairs is E04: every corruption of the SRSM
// (bytes, size, layout version, fingerprint, offsets) must be refused by the store or the reader,
// fall back to the source, serve the source's rules and republish a usable conversion.
func TestRemoteDerivedE04CorruptConversionFallsBackAndRepairs(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	payload := writeRuleSetPayload(t, "10.0.0.0/8")
	sourceID := ruleSetDigest(payload)

	// patched cases commit a version whose manifest matches its damaged bytes, so only the SRSM
	// reader can reject it. repairLoads is how many fallback loads it takes before the next start
	// can hit: a version that was published under the same name (an in-place corruption) has to be
	// collected once before a correct publication can take its place.
	cases := []struct {
		name        string
		repairLoads int
		prepare     func(t *testing.T, base string, version remoteDerivedTestStoreVersion)
	}{
		{
			name:        "corrupt-bytes",
			repairLoads: 2,
			prepare: func(t *testing.T, _ string, version remoteDerivedTestStoreVersion) {
				content := version.ArtifactBytes(t)
				for index := 0; index < len(content); index += 7 {
					content[index] ^= 0xff
				}
				require.NoError(t, os.WriteFile(version.ArtifactPath(), content, 0o600))
			},
		},
		{
			name:        "truncated-bytes",
			repairLoads: 2,
			prepare: func(t *testing.T, _ string, version remoteDerivedTestStoreVersion) {
				require.NoError(t, os.Truncate(version.ArtifactPath(), int64(len(version.ArtifactBytes(t))/2)))
			},
		},
		{
			name:        "layout-version",
			repairLoads: 1,
			prepare: func(t *testing.T, base string, version remoteDerivedTestStoreVersion) {
				content := version.ArtifactBytes(t)
				binary.NativeEndian.PutUint32(content[4:8], srs.MmapFormatVersion+1)
				manifest := version.Manifest
				manifest.Size = 0
				manifest.Digest = ""
				remoteDerivedTestCommitManifest(t, base, version.Key, manifest, content)
			},
		},
		{
			name:        "fingerprint",
			repairLoads: 1,
			prepare: func(t *testing.T, base string, version remoteDerivedTestStoreVersion) {
				content := version.ArtifactBytes(t)
				copy(content[40:56], bytes.Repeat([]byte{'0'}, 16))
				manifest := version.Manifest
				manifest.Size = 0
				manifest.Digest = ""
				remoteDerivedTestCommitManifest(t, base, version.Key, manifest, content)
			},
		},
		{
			name:        "blob-offset",
			repairLoads: 1,
			prepare: func(t *testing.T, base string, version remoteDerivedTestStoreVersion) {
				content := version.ArtifactBytes(t)
				binary.NativeEndian.PutUint64(content[24:32], uint64(len(content))*4)
				manifest := version.Manifest
				manifest.Size = 0
				manifest.Digest = ""
				remoteDerivedTestCommitManifest(t, base, version.Key, manifest, content)
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			ctx, base := derivedTestContext(t)
			server := newRemoteDerivedTestSourceServer(t, payload, "")
			fixture := newRemoteDerivedTestHTTP(t, ctx, server)
			tag := "e04-" + testCase.name
			cold := startRemoteDerivedTestRuleSet(t, fixture.ctx, tag, fixture.server.URL, C.RuleSetFormatBinary)
			require.Equal(t, sourceID, cold.artifactLease.ID())
			require.NoError(t, cold.Close())
			version := remoteDerivedTestVersionForSource(t, base, sourceID)
			testCase.prepare(t, base, version)

			for load := 0; load < testCase.repairLoads; load++ {
				restoring := newRemoteDerivedTestRuleSet(t, fixture.ctx, tag, fixture.server.URL, C.RuleSetFormatBinary)
				restoring.openArtifactStore()
				resetRemoteDerivedDiagnostics()
				restored, restoreErr := restoring.restoreStoredRuleSet()
				require.NoError(t, restoreErr)
				require.True(t, restored, "a damaged conversion must fall back to the source")
				require.True(t, remoteRuleSetMatches(t, restoring, "10.1.2.3"), "the source rules must be served")
				require.Equal(t, sourceID, restoring.artifactLease.ID())
				requireRemoteDerivedCounters(t, "damaged conversion load", 0, 1, 1, 1, 0)
				require.NoError(t, restoring.Close())
			}

			repaired := newRemoteDerivedTestRuleSet(t, fixture.ctx, tag, fixture.server.URL, C.RuleSetFormatBinary)
			repaired.openArtifactStore()
			resetRemoteDerivedDiagnostics()
			restored, restoreErr := repaired.restoreStoredRuleSet()
			require.NoError(t, restoreErr)
			require.True(t, restored)
			require.True(t, remoteRuleSetMatches(t, repaired, "10.1.2.3"))
			require.Equal(t, sourceID, repaired.artifactLease.ID())
			requireRemoteDerivedCounters(t, "repaired conversion", 1, 0, 0, 0, 0)
			require.Len(t, remoteDerivedTestStoreDerivedVersions(t, base), 1, "the damaged version must not survive")
			repairedVersion := remoteDerivedTestVersionForSource(t, base, sourceID)
			require.Equal(t, srs.MmapFormatVersion, int(binary.NativeEndian.Uint32(repairedVersion.ArtifactBytes(t)[4:8])),
				"the repaired conversion must carry this build's layout")
		})
	}
}

// TestRemoteDerivedE05WrongManifestNeverHits is E05: a manifest that names the wrong source digest,
// size, format, identity generation or ABI must be a miss even though the store's own byte
// verification accepts the artifact. The load then falls back and republishes the correct account.
func TestRemoteDerivedE05WrongManifestNeverHits(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	payload := writeRuleSetPayload(t, "10.0.0.0/8")
	sourceID := ruleSetDigest(payload)

	cases := []struct {
		name   string
		mutate func(t *testing.T, base string, version remoteDerivedTestStoreVersion)
	}{
		{
			name: "source-digest",
			mutate: func(t *testing.T, base string, version remoteDerivedTestStoreVersion) {
				manifest := version.Manifest
				manifest.Dependencies = replaceDerivedDependency(manifest.Dependencies, "source-sha256=", "00")
				manifest.Size, manifest.Digest = 0, ""
				remoteDerivedTestCommitManifest(t, base, version.Key, manifest, version.ArtifactBytes(t))
			},
		},
		{
			name: "source-size",
			mutate: func(t *testing.T, base string, version remoteDerivedTestStoreVersion) {
				manifest := version.Manifest
				manifest.Dependencies = replaceDerivedDependency(manifest.Dependencies, "source-size=", "1")
				manifest.Size, manifest.Digest = 0, ""
				remoteDerivedTestCommitManifest(t, base, version.Key, manifest, version.ArtifactBytes(t))
			},
		},
		{
			name: "source-format",
			mutate: func(t *testing.T, base string, version remoteDerivedTestStoreVersion) {
				manifest := version.Manifest
				manifest.Dependencies = replaceDerivedDependency(manifest.Dependencies, "source-format=", "source")
				manifest.Size, manifest.Digest = 0, ""
				remoteDerivedTestCommitManifest(t, base, version.Key, manifest, version.ArtifactBytes(t))
			},
		},
		{
			name: "identity-generation",
			mutate: func(t *testing.T, base string, version remoteDerivedTestStoreVersion) {
				manifest := version.Manifest
				manifest.Dependencies = removeDerivedDependency(manifest.Dependencies, "identity-generation=")
				manifest.Size, manifest.Digest = 0, ""
				remoteDerivedTestCommitManifest(t, base, version.Key, manifest, version.ArtifactBytes(t))
			},
		},
		{
			name: "abi",
			mutate: func(t *testing.T, base string, version remoteDerivedTestStoreVersion) {
				manifest := version.Manifest
				manifest.ABI = "another-build"
				manifest.Size, manifest.Digest = 0, ""
				remoteDerivedTestCommitManifest(t, base, version.Key, manifest, version.ArtifactBytes(t))
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			ctx, base := derivedTestContext(t)
			server := newRemoteDerivedTestSourceServer(t, payload, "")
			fixture := newRemoteDerivedTestHTTP(t, ctx, server)
			tag := "e05-" + testCase.name
			cold := startRemoteDerivedTestRuleSet(t, fixture.ctx, tag, fixture.server.URL, C.RuleSetFormatBinary)
			require.Equal(t, sourceID, cold.artifactLease.ID())
			require.NoError(t, cold.Close())
			testCase.mutate(t, base, remoteDerivedTestVersionForSource(t, base, sourceID))

			refused := newRemoteDerivedTestRuleSet(t, fixture.ctx, tag, fixture.server.URL, C.RuleSetFormatBinary)
			refused.openArtifactStore()
			resetRemoteDerivedDiagnostics()
			restored, restoreErr := refused.restoreStoredRuleSet()
			require.NoError(t, restoreErr)
			require.True(t, restored, "a wrong manifest must be a miss and fall back to the source")
			require.True(t, remoteRuleSetMatches(t, refused, "10.1.2.3"))
			require.Equal(t, sourceID, refused.artifactLease.ID())
			requireRemoteDerivedCounters(t, "wrong manifest load", 0, 1, 1, 1, 0)
			require.NoError(t, refused.Close())

			repaired := newRemoteDerivedTestRuleSet(t, fixture.ctx, tag, fixture.server.URL, C.RuleSetFormatBinary)
			repaired.openArtifactStore()
			resetRemoteDerivedDiagnostics()
			restored, restoreErr = repaired.restoreStoredRuleSet()
			require.NoError(t, restoreErr)
			require.True(t, restored)
			requireRemoteDerivedCounters(t, "repaired manifest", 1, 0, 0, 0, 0)
			repairedVersion := remoteDerivedTestVersionForSource(t, base, sourceID)
			require.Contains(t, repairedVersion.Manifest.Dependencies, "identity-generation="+strconv.Itoa(derivedIdentityGeneration))
			require.Equal(t, "binary", remoteDerivedTestDependency(t, repairedVersion, "source-format="))
			require.NotEqual(t, "another-build", repairedVersion.Manifest.ABI)
		})
	}
}

func replaceDerivedDependency(dependencies []string, prefix string, value string) []string {
	replaced := make([]string, 0, len(dependencies))
	for _, dependency := range dependencies {
		if len(dependency) >= len(prefix) && dependency[:len(prefix)] == prefix {
			replaced = append(replaced, prefix+value)
			continue
		}
		replaced = append(replaced, dependency)
	}
	return replaced
}

func removeDerivedDependency(dependencies []string, prefix string) []string {
	removed := make([]string, 0, len(dependencies))
	for _, dependency := range dependencies {
		if len(dependency) >= len(prefix) && dependency[:len(prefix)] == prefix {
			continue
		}
		removed = append(removed, dependency)
	}
	return removed
}

// TestRemoteDerivedE06ReadOnlyDerivedStoreStillLoads is E06: the derived store is an accelerator,
// so an unwritable derived key directory must make the publication fail (counted) without failing
// the load - the source is parsed and served on the original path. The failure branch is proven by
// the shared derived-cache failure counter, not assumed from a chmod.
func TestRemoteDerivedE06ReadOnlyDerivedStoreStillLoads(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("read-only directory permissions do not fail for root")
	}
	enableDerivedRuleSetPlatform(t)
	ctx, base := derivedTestContext(t)
	payloadA := writeRuleSetPayload(t, "10.0.0.0/8")
	payloadB := writeRuleSetPayload(t, "192.168.0.0/16")
	server := newRemoteDerivedTestSourceServer(t, payloadA, "")
	fixture := newRemoteDerivedTestHTTP(t, ctx, server)
	ruleSet := startRemoteDerivedTestRuleSet(t, fixture.ctx, "e06-readonly", fixture.server.URL, C.RuleSetFormatBinary)
	require.Equal(t, ruleSetDigest(payloadA), ruleSet.artifactLease.ID())
	derivedKeyDir := filepath.Join(base, derivedStoreDirectory, func() string {
		return remoteDerivedTestVersionForSource(t, base, ruleSetDigest(payloadA)).Key
	}())
	require.NoError(t, os.Chmod(derivedKeyDir, 0o555))
	t.Cleanup(func() { _ = os.Chmod(derivedKeyDir, 0o755) })

	server.setPayload(payloadB)
	resetDerivedCacheDiagnostics()
	resetRemoteDerivedDiagnostics()
	require.NoError(t, ruleSet.fetch(ctx, false), "an unwritable derived store must not fail a valid source")
	require.True(t, remoteRuleSetMatches(t, ruleSet, "192.168.1.1"))
	require.False(t, remoteRuleSetMatches(t, ruleSet, "10.1.2.3"))
	requireRemoteDerivedCounters(t, "read-only derived store", 0, 1, 1, 1, 0)
	_, _, _, failures := derivedCacheDiagnostics()
	require.NotZero(t, failures, "the publication failure branch must really have run")
	// The failed publication adds nothing: the only conversion is still A's, and B's bytes were
	// never published.
	require.Len(t, remoteDerivedTestStoreDerivedVersions(t, base), 1)
	remoteDerivedTestVersionForSource(t, base, ruleSetDigest(payloadA))

	require.NoError(t, os.Chmod(derivedKeyDir, 0o755))
	resetRemoteDerivedDiagnostics()
	require.NoError(t, ruleSet.fetch(ctx, false))
	requireRemoteDerivedCounters(t, "writable again", 0, 1, 1, 1, 0)
	remoteDerivedTestVersionForSource(t, base, ruleSetDigest(payloadB))
	resetRemoteDerivedDiagnostics()
	require.NoError(t, ruleSet.fetch(ctx, false))
	requireRemoteDerivedCounters(t, "warm after repair", 1, 0, 0, 0, 0)
}

// TestRemoteDerivedE07LiveMappingSurvivesCollect is E07: a mapping that is still serving rules
// holds the derived version's lease, so Collect cannot remove it; once the mapping is dropped and
// finalized the next Collect may remove it, and the live source lease is never touched.
func TestRemoteDerivedE07LiveMappingSurvivesCollect(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	ctx, base := derivedTestContext(t)
	payloadA := writeRuleSetPayload(t, "10.0.0.0/8")
	payloadB := writeRuleSetPayload(t, "192.168.0.0/16")
	digestA := ruleSetDigest(payloadA)
	digestB := ruleSetDigest(payloadB)

	server := newRemoteDerivedTestSourceServer(t, payloadA, "")
	fixture := newRemoteDerivedTestHTTP(t, ctx, server)
	first := startRemoteDerivedTestRuleSet(t, fixture.ctx, "e07-lease", fixture.server.URL, C.RuleSetFormatBinary)
	require.Equal(t, digestA, first.artifactLease.ID())
	require.NoError(t, first.Close())

	// This start maps A's persisted conversion: the mapping owns A's derived lease.
	box := startRemoteDerivedTestRuleSet(t, fixture.ctx, "e07-lease", fixture.server.URL, C.RuleSetFormatBinary)
	require.Equal(t, digestA, box.artifactLease.ID())
	oldRules := box.currentRules()
	require.NotEmpty(t, oldRules)
	derivedKey := remoteDerivedTestVersionForSource(t, base, digestA).Key

	server.setPayload(payloadB)
	require.NoError(t, box.fetch(ctx, false))
	require.True(t, remoteRuleSetMatches(t, box, "192.168.1.1"))
	require.Equal(t, digestB, box.artifactLease.ID())
	require.True(t, matchAnyHeadlessRule(oldRules, remoteDerivedTestInbound("10.1.2.3")),
		"the old matcher keeps serving while it is referenced")
	require.Len(t, remoteDerivedTestStoreDerivedVersions(t, base), 2,
		"the old conversion is still mapped and the new one is newest")

	// Collect while the old mapping is live: A's version must stay, B's newest stays.
	require.NoError(t, box.artifactStore.Collect(derivedKey))
	require.Len(t, remoteDerivedTestStoreDerivedVersions(t, base), 2)
	require.Equal(t, digestB, box.artifactLease.ID(), "the live source lease is unchanged")
	source, err := box.artifactStore.LoadExact(box.artifactKey, digestB)
	require.NoError(t, err, "the live source version is collectable only after its lease is gone")
	source.Release()

	// Drop the old matcher: its mapping cleanup releases the derived lease, then Collect may remove
	// the old version. Finalization is asynchronous by contract, so the test drives GC.
	oldRules = nil
	_ = oldRules
	require.True(t, awaitDerivedState(t, 10*time.Second, func() bool {
		require.NoError(t, box.artifactStore.Collect(derivedKey))
		return len(remoteDerivedTestStoreDerivedVersions(t, base)) == 1
	}), "after the last mapping is dropped the unleased old conversion is collected")
	require.True(t, remoteRuleSetMatches(t, box, "192.168.1.1"))
}

// TestRemoteDerivedE08FuturePublicationStillWarms is E08 for clock skew: a conversion of another
// source whose Created is ahead of this host's clock must be refused (identity, not time) and the
// accurate new content must still become the newest conversion and warm.
func TestRemoteDerivedE08FuturePublicationStillWarms(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	ctx, base := derivedTestContext(t)
	payloadA := writeRuleSetPayload(t, "10.0.0.0/8")
	payloadB := writeRuleSetPayload(t, "192.168.0.0/16")
	digestA := ruleSetDigest(payloadA)
	digestB := ruleSetDigest(payloadB)

	server := newRemoteDerivedTestSourceServer(t, payloadA, "")
	fixture := newRemoteDerivedTestHTTP(t, ctx, server)
	first := startRemoteDerivedTestRuleSet(t, fixture.ctx, "e08-future", fixture.server.URL, C.RuleSetFormatBinary)
	require.Equal(t, digestA, first.artifactLease.ID())
	require.NoError(t, first.Close())

	// Hand-write a future Created for A's conversion: the store's own ranking now sees A as the
	// newest conversion even after B arrives, unless publication advances past it.
	version := remoteDerivedTestVersionForSource(t, base, digestA)
	future := time.Now().Add(time.Hour).UnixNano()
	manifest := version.Manifest
	manifest.Created = future
	encoded, err := json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(version.Dir, "manifest.json"), encoded, 0o600))

	// A fresh object restores the future-dated but CORRECT conversion of A.
	refresher := startRemoteDerivedTestRuleSet(t, fixture.ctx, "e08-future", fixture.server.URL, C.RuleSetFormatBinary)
	require.Equal(t, digestA, refresher.artifactLease.ID())
	require.True(t, remoteRuleSetMatches(t, refresher, "10.1.2.3"))

	server.setPayload(payloadB)
	resetRemoteDerivedDiagnostics()
	require.NoError(t, refresher.fetch(ctx, false))
	require.True(t, remoteRuleSetMatches(t, refresher, "192.168.1.1"))
	require.False(t, remoteRuleSetMatches(t, refresher, "10.1.2.3"))
	require.Equal(t, digestB, refresher.artifactLease.ID())
	requireRemoteDerivedCounters(t, "future-dated other conversion", 0, 1, 1, 1, 0)
	newest := remoteDerivedTestVersionForSource(t, base, digestB)
	require.Greater(t, newest.Manifest.Created, future,
		"the accurate new conversion must outrank a future-dated record")
	// The future-dated version may still be leased by the mapping that restored A; once that
	// mapping is dropped and finalized the next Collect removes it, so it does not stay the
	// store's "newest" forever. Finalization is asynchronous by contract, so the test drives GC.
	derivedKey := newest.Key
	require.True(t, awaitDerivedState(t, 10*time.Second, func() bool {
		require.NoError(t, refresher.artifactStore.Collect(derivedKey))
		return len(remoteDerivedTestStoreDerivedVersions(t, base)) == 1
	}), "the refused future-dated conversion is collected once its mapping is gone")

	resetRemoteDerivedDiagnostics()
	require.NoError(t, refresher.fetch(ctx, false))
	requireRemoteDerivedCounters(t, "accurate new content warm", 1, 0, 0, 0, 0)
}

// TestRemoteDerivedE09SourceArtifactRewriteWindow is E09 for the source artifact: the window
// between "the store verified this version" and "the loader reads the file" must not let another
// document's bytes be served or republished under the verified source digest.
//
// The current candidate reads the path again after the derived lookup; a rewrite in that window is
// served as if it were the verified version. This test states the invariant; it is expected to be
// RED until the loader verifies the bytes it actually parses against the version manifest.
func TestRemoteDerivedE09SourceArtifactRewriteWindow(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	ctx, base := derivedTestContext(t)
	payloadA := writeRuleSetPayload(t, "10.0.0.0/8")
	payloadB := writeRuleSetPayload(t, "192.168.0.0/16")
	digestA := ruleSetDigest(payloadA)

	server := newRemoteDerivedTestSourceServer(t, payloadA, "")
	fixture := newRemoteDerivedTestHTTP(t, ctx, server)
	first := startRemoteDerivedTestRuleSet(t, fixture.ctx, "e09-window", fixture.server.URL, C.RuleSetFormatBinary)
	require.Equal(t, digestA, first.artifactLease.ID())
	require.NoError(t, first.Close())
	remoteDerivedTestRemoveDerivedVersions(t, base)

	sourceKey := artifactKeyFor("e09-window", fixture.server.URL)
	var rewritten atomic.Bool
	remoteDerivedTestHook = func(point string) {
		if point != "derived-miss" || rewritten.Swap(true) {
			return
		}
		keyDir := filepath.Join(base, derivedStoreDirectory, sourceKey)
		entries, readErr := os.ReadDir(keyDir)
		require.NoError(t, readErr)
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			artifactPath := filepath.Join(keyDir, entry.Name(), "artifact.bin")
			if _, statErr := os.Stat(artifactPath); statErr == nil {
				require.NoError(t, os.WriteFile(artifactPath, payloadB, 0o600))
				return
			}
		}
		t.Errorf("no source artifact to rewrite in %s", keyDir)
	}
	t.Cleanup(func() { remoteDerivedTestHook = nil })

	rewrittenBox := newRemoteDerivedTestRuleSet(t, fixture.ctx, "e09-window", fixture.server.URL, C.RuleSetFormatBinary)
	rewrittenBox.openArtifactStore()
	restored, restoreErr := rewrittenBox.restoreStoredRuleSet()
	require.True(t, rewritten.Load(), "the fixture must land inside the window")
	require.NoError(t, restoreErr)
	if restored {
		// The verified source version names A. Whatever is served must be A's rules; serving B here
		// would mean B's result was registered under A's digest.
		require.True(t, remoteRuleSetMatches(t, rewrittenBox, "10.1.2.3"),
			"the verified source version must serve its own bytes, not the rewritten file")
		require.False(t, remoteRuleSetMatches(t, rewrittenBox, "192.168.1.1"),
			"another document's rules must never be served under the verified source digest")
		require.Equal(t, digestA, rewrittenBox.artifactLease.ID())
	} else {
		require.Nil(t, rewrittenBox.currentRules(), "a detected identity mismatch must publish nothing")
	}
}
