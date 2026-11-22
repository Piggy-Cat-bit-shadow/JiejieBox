//go:build unix

package rule

// F01-F07, F09, F10 of the remote derived-cache matrix: the functional contract of the real
// RemoteRuleSet entry points.
//
// Every case is driven through `StartContext` (F01/F02/F03/F06/F07) or through the production
// `fetch`/`restoreStoredRuleSet` functions with the production `openArtifactStore` wiring, and every
// hit is checked with exact counters: a hit may not decode the source or write an SRSM, while a miss
// must do both. The assertions compare the rules AND the metadata AND the exact source reference,
// because "the same rules" alone cannot distinguish a correct hit from a stale generation.

import (
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/artifact"
	C "github.com/sagernet/sing-box/constant"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// remoteDerivedTestBlocker stops the load at one named production interleaving point until the test
// releases it. It blocks only the FIRST occurrence: a test that starts one blocked refresh and then
// lets a second refresh complete must not have the second one stuck in the same hook.
type remoteDerivedTestBlocker struct {
	point   string
	entered chan struct{}
	release chan struct{}
	blocked atomic.Bool
	stop    sync.Once
}

// blockRemoteDerivedTestPoint installs the production hook. The returned blocker must be released
// by the test; the cleanup is only a safety net for an early Fatal.
func blockRemoteDerivedTestPoint(t *testing.T, point string) *remoteDerivedTestBlocker {
	t.Helper()
	blocker := &remoteDerivedTestBlocker{
		point:   point,
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	remoteDerivedTestHook = func(current string) {
		if current != blocker.point {
			return
		}
		// Only the first occurrence blocks. A sync.Once cannot be used here: its Do holds the
		// internal mutex while f runs, so a second load reaching the same point would wait for the
		// first one instead of proceeding - not the interleaving the test wants.
		if !blocker.blocked.CompareAndSwap(false, true) {
			return
		}
		close(blocker.entered)
		<-blocker.release
	}
	t.Cleanup(func() {
		remoteDerivedTestHook = nil
		blocker.unblock()
	})
	return blocker
}

func (b *remoteDerivedTestBlocker) wait(t *testing.T) {
	t.Helper()
	select {
	case <-b.entered:
	case <-time.After(5 * time.Second):
		t.Fatalf("the load never reached the %q interleaving point", b.point)
	}
}

func (b *remoteDerivedTestBlocker) unblock() {
	b.stop.Do(func() { close(b.release) })
}

func remoteDerivedTestInbound(address string) *adapter.InboundContext {
	return &adapter.InboundContext{Destination: M.ParseSocksaddr(address + ":443")}
}

// requireRemoteDerivedCounters asserts the exact diagnostic snapshot. Exactness is the point: a
// ">= 1 hit" assertion cannot tell a real hit from a fallback that happens to publish a conversion,
// and it cannot prove that the source was not decoded again.
func requireRemoteDerivedCounters(t *testing.T, when string, hits int64, misses int64, sourceDecodes int64, srsmWrites int64, heapBuilds int64) {
	t.Helper()
	gotHits, gotMisses, gotSourceDecodes, gotSRSMWrites, gotHeapBuilds := remoteDerivedDiagnostics()
	require.EqualValues(t, hits, gotHits, "%s: hits", when)
	require.EqualValues(t, misses, gotMisses, "%s: misses", when)
	require.EqualValues(t, sourceDecodes, gotSourceDecodes, "%s: source decodes", when)
	require.EqualValues(t, srsmWrites, gotSRSMWrites, "%s: SRSM writes", when)
	require.EqualValues(t, heapBuilds, gotHeapBuilds, "%s: heap builds", when)
}

func remoteDerivedTestDependency(t *testing.T, version remoteDerivedTestStoreVersion, prefix string) string {
	t.Helper()
	value, ok := version.dependency(prefix)
	require.True(t, ok, "manifest of %s has no %q dependency: %v", version.Dir, prefix, version.Manifest.Dependencies)
	return value
}

// artifactReferenceSet is the production reference-set constructor, named locally so the tests read
// as what they build.
func artifactReferenceSet(pinned map[string]string, retained map[string]string) *artifact.ReferenceSet {
	return artifact.NewReferenceSet(pinned, retained)
}

// TestRemoteDerivedF01RealStartContextBinaryColdThenWarm is F01 driven through the REAL start entry:
// `StartContext` opens the store, restores the stored source version, hits its conversion and
// publishes. The warm object must make zero HTTP requests, decode zero source documents, write zero
// SRSMs, produce the same rules and metadata, and record the same exact source reference.
func TestRemoteDerivedF01RealStartContextBinaryColdThenWarm(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	ctx, base := derivedTestContext(t)
	refs := artifactReferenceSet(nil, nil)
	ctx = service.ContextWith[*artifact.ReferenceSet](ctx, refs)
	payload := writeRuleSetPayload(t, "10.0.0.0/8")
	server := newRemoteDerivedTestSourceServer(t, payload, `"v1"`)
	fixture := newRemoteDerivedTestHTTP(t, ctx, server)
	key := artifactKeyFor("f01-start", fixture.server.URL)
	sourceID := ruleSetDigest(payload)

	resetRemoteDerivedDiagnostics()
	cold := startRemoteDerivedTestRuleSet(t, fixture.ctx, "f01-start", fixture.server.URL, C.RuleSetFormatBinary)
	require.True(t, remoteRuleSetMatches(t, cold, "10.1.2.3"), "the cold start must serve the rules")
	coldMetadata := cold.Metadata()
	require.True(t, coldMetadata.ContainsIPCIDRRule)
	require.Equal(t, sourceID, cold.artifactLease.ID(), "the live lease is the SOURCE version")
	require.Equal(t, sourceID, refs.Snapshot()[key], "the exact source reference is recorded")
	require.EqualValues(t, 1, fixture.requestCount())
	requireRemoteDerivedCounters(t, "cold start", 0, 1, 1, 1, 0)
	derivedVersion := remoteDerivedTestVersionForSource(t, base, sourceID)
	require.Equal(t, "binary", remoteDerivedTestDependency(t, derivedVersion, "source-format="))
	require.Len(t, remoteDerivedTestStoreDerivedVersions(t, base), 1)
	require.NoError(t, cold.Close())

	// The restart: a brand-new object on the same base directory. StartContext must restore, hit and
	// commit without going anywhere near the server.
	resetRemoteDerivedDiagnostics()
	warm := startRemoteDerivedTestRuleSet(t, fixture.ctx, "f01-start", fixture.server.URL, C.RuleSetFormatBinary)
	require.True(t, remoteRuleSetMatches(t, warm, "10.1.2.3"))
	require.Equal(t, coldMetadata, warm.Metadata(), "the warm metadata must equal the cold metadata")
	require.Equal(t, sourceID, warm.artifactLease.ID(), "the warm live lease is still the source version")
	require.Equal(t, sourceID, refs.Snapshot()[key])
	require.EqualValues(t, 1, fixture.requestCount(), "a warm start must not download")
	requireRemoteDerivedCounters(t, "warm start", 1, 0, 0, 0, 0)
	require.Len(t, remoteDerivedTestStoreDerivedVersions(t, base), 1)
}

// TestRemoteDerivedF02RealStartContextSourceJSONColdThenWarm is F02: the same contract for the
// source JSON format, proven separately so a binary result may not stand in for it. The published
// conversion must name the source format, and the stored source bytes must be the JSON document.
func TestRemoteDerivedF02RealStartContextSourceJSONColdThenWarm(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	ctx, base := derivedTestContext(t)
	payload := derivedJSONDomainSource("f02.example")
	require.NotEmpty(t, payload)
	server := newRemoteDerivedTestSourceServer(t, payload, `"json-v1"`)
	fixture := newRemoteDerivedTestHTTP(t, ctx, server)
	sourceID := ruleSetDigest(payload)

	resetRemoteDerivedDiagnostics()
	cold := startRemoteDerivedTestRuleSet(t, fixture.ctx, "f02-json", fixture.server.URL, C.RuleSetFormatSource)
	requireRemoteDerivedDomainMatch(t, cold, "f02.example", true)
	requireRemoteDerivedDomainMatch(t, cold, "other.example", false)
	coldMetadata := cold.Metadata()
	require.Equal(t, sourceID, cold.artifactLease.ID())
	require.EqualValues(t, 1, fixture.requestCount())
	requireRemoteDerivedCounters(t, "cold source-JSON start", 0, 1, 1, 1, 0)

	sources := remoteDerivedTestStoreSourceVersions(t, base)
	require.Len(t, sources, 1)
	require.Equal(t, payload, sources[0].ArtifactBytes(t), "the stored source is the JSON document")
	derivedVersion := remoteDerivedTestVersionForSource(t, base, sourceID)
	require.Equal(t, "source", remoteDerivedTestDependency(t, derivedVersion, "source-format="))
	require.NoError(t, cold.Close())

	resetRemoteDerivedDiagnostics()
	warm := startRemoteDerivedTestRuleSet(t, fixture.ctx, "f02-json", fixture.server.URL, C.RuleSetFormatSource)
	requireRemoteDerivedDomainMatch(t, warm, "f02.example", true)
	require.Equal(t, coldMetadata, warm.Metadata())
	require.Equal(t, sourceID, warm.artifactLease.ID())
	require.EqualValues(t, 1, fixture.requestCount())
	requireRemoteDerivedCounters(t, "warm source-JSON start", 1, 0, 0, 0, 0)
}

// TestRemoteDerivedF03HTTP200SameContent is F03: a real 200 carrying unchanged bytes must still be
// downloaded and staged (the source stays the authority) but must hit the conversion, whether or not
// the ETag changed. The ETag is bookkeeping, never content identity.
func TestRemoteDerivedF03HTTP200SameContent(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	t.Run("same-etag", func(t *testing.T) {
		remoteDerivedCaseF03(t, `"v1"`, `"v1"`)
	})
	t.Run("changed-etag", func(t *testing.T) {
		remoteDerivedCaseF03(t, `"v1"`, `"v2"`)
	})
}

func remoteDerivedCaseF03(t *testing.T, initialEtag string, refreshEtag string) {
	ctx, base := derivedTestContext(t)
	payload := writeRuleSetPayload(t, "10.0.0.0/8")
	server := newRemoteDerivedTestSourceServer(t, payload, initialEtag)
	fixture := newRemoteDerivedTestHTTP(t, ctx, server)
	ruleSet := startRemoteDerivedTestRuleSet(t, fixture.ctx, "f03-200", fixture.server.URL, C.RuleSetFormatBinary)
	sourceID := ruleSet.artifactLease.ID()
	require.Equal(t, ruleSetDigest(payload), sourceID)
	updatedBefore, _ := ruleSet.updateMetadata()

	server.setEtag(refreshEtag)
	resetRemoteDerivedDiagnostics()
	require.NoError(t, ruleSet.fetch(ctx, false))
	require.True(t, remoteRuleSetMatches(t, ruleSet, "10.1.2.3"))
	require.Equal(t, sourceID, ruleSet.artifactLease.ID(), "unchanged content keeps the same source version")
	require.EqualValues(t, 2, fixture.requestCount(), "the 200 must be a real, complete download")
	requireRemoteDerivedCounters(t, "same-content 200", 1, 0, 0, 0, 0)

	updatedAfter, etagAfter := ruleSet.updateMetadata()
	require.True(t, updatedAfter.After(updatedBefore), "a completed 200 refreshes the update time")
	require.Equal(t, refreshEtag, etagAfter, "the served ETag is the one the response carried")
	require.Len(t, remoteDerivedTestStoreSourceVersions(t, base), 1, "identical bytes stay one source version")
	require.Len(t, remoteDerivedTestStoreDerivedVersions(t, base), 1)
}

// TestRemoteDerivedF04HTTP200ContentChange is F04: A to B misses once, B is served, the next B
// fetch hits, and a matcher that was published from A keeps working while its generation is
// retired.
func TestRemoteDerivedF04HTTP200ContentChange(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	ctx, base := derivedTestContext(t)
	payloadA := writeRuleSetPayload(t, "10.0.0.0/8")
	payloadB := writeRuleSetPayload(t, "192.168.0.0/16")
	server := newRemoteDerivedTestSourceServer(t, payloadA, "")
	fixture := newRemoteDerivedTestHTTP(t, ctx, server)
	ruleSet := startRemoteDerivedTestRuleSet(t, fixture.ctx, "f04-change", fixture.server.URL, C.RuleSetFormatBinary)
	require.True(t, remoteRuleSetMatches(t, ruleSet, "10.1.2.3"))
	oldRules := ruleSet.currentRules()
	require.NotEmpty(t, oldRules)

	server.setPayload(payloadB)
	resetRemoteDerivedDiagnostics()
	require.NoError(t, ruleSet.fetch(ctx, false))
	require.True(t, remoteRuleSetMatches(t, ruleSet, "192.168.1.1"), "B must be served after the miss")
	require.False(t, remoteRuleSetMatches(t, ruleSet, "10.1.2.3"))
	requireRemoteDerivedCounters(t, "A to B", 0, 1, 1, 1, 0)
	require.True(t, matchAnyHeadlessRule(oldRules, remoteDerivedTestInbound("10.1.2.3")),
		"a matcher built from A must keep matching after B was published")
	require.False(t, matchAnyHeadlessRule(oldRules, remoteDerivedTestInbound("192.168.1.1")))
	require.Len(t, remoteDerivedTestStoreDerivedVersions(t, base), 1, "only the newest conversion is kept")
	remoteDerivedTestVersionForSource(t, base, ruleSetDigest(payloadB))
	require.Len(t, remoteDerivedTestStoreSourceVersions(t, base), 1, "the unleased old source version is collected")

	resetRemoteDerivedDiagnostics()
	require.NoError(t, ruleSet.fetch(ctx, false))
	require.True(t, remoteRuleSetMatches(t, ruleSet, "192.168.1.1"))
	requireRemoteDerivedCounters(t, "second B 200", 1, 0, 0, 0, 0)
	remoteDerivedTestVersionForSource(t, base, ruleSetDigest(payloadB))
}

// TestRemoteDerivedF05HTTP304SkipsParseAndConversion is F05: a 304 makes one request and changes
// neither the rules, the source lease, nor any derived counter. The per-load conversion counters are
// the observable form of "no parse, no conversion".
func TestRemoteDerivedF05HTTP304SkipsParseAndConversion(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	ctx, _ := derivedTestContext(t)
	payload := writeRuleSetPayload(t, "10.0.0.0/8")
	var requests atomic.Int32
	fixture := newRemoteDerivedTestHTTP(t, ctx, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.Header().Set("Etag", `"v1"`)
		if request.Header.Get("If-None-Match") == `"v1"` {
			writer.WriteHeader(http.StatusNotModified)
			return
		}
		_, _ = writer.Write(payload)
	}))
	ruleSet := startRemoteDerivedTestRuleSet(t, fixture.ctx, "f05-304", fixture.server.URL, C.RuleSetFormatBinary)
	sourceID := ruleSet.artifactLease.ID()
	require.True(t, remoteRuleSetMatches(t, ruleSet, "10.1.2.3"))
	updatedBefore, etagBefore := ruleSet.updateMetadata()
	require.Equal(t, `"v1"`, etagBefore)
	require.EqualValues(t, 1, requests.Load())

	resetRemoteDerivedDiagnostics()
	require.NoError(t, ruleSet.fetch(ctx, false))
	require.EqualValues(t, 2, requests.Load(), "a 304 is still a real conditional request")
	require.True(t, remoteRuleSetMatches(t, ruleSet, "10.1.2.3"), "the rules must stay continuous")
	require.Equal(t, sourceID, ruleSet.artifactLease.ID(), "a 304 must not change the source version")
	updatedAfter, etagAfter := ruleSet.updateMetadata()
	require.Equal(t, `"v1"`, etagAfter)
	require.True(t, updatedAfter.After(updatedBefore))
	requireRemoteDerivedCounters(t, "304", 0, 0, 0, 0, 0)
}

// TestRemoteDerivedF06TagAndURLOwnership is F06: the derived cache is per source ownership, not one
// global table. Two tags on one document do not share (the remote key is tag+URL scoped), and
// re-pointing a tag at another URL never serves the previous document.
func TestRemoteDerivedF06TagAndURLOwnership(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	ctx, base := derivedTestContext(t)
	payloadX := writeRuleSetPayload(t, "10.0.0.0/8")
	payloadY := writeRuleSetPayload(t, "192.168.0.0/16")

	serverX := newRemoteDerivedTestSourceServer(t, payloadX, "")
	fixtureX := newRemoteDerivedTestHTTP(t, ctx, serverX)

	resetRemoteDerivedDiagnostics()
	tagA := startRemoteDerivedTestRuleSet(t, fixtureX.ctx, "f06-a", fixtureX.server.URL, C.RuleSetFormatBinary)
	tagB := startRemoteDerivedTestRuleSet(t, fixtureX.ctx, "f06-b", fixtureX.server.URL, C.RuleSetFormatBinary)
	require.True(t, remoteRuleSetMatches(t, tagA, "10.1.2.3"))
	require.True(t, remoteRuleSetMatches(t, tagB, "10.1.2.3"))
	requireRemoteDerivedCounters(t, "two tags cold", 0, 2, 2, 2, 0)
	require.Len(t, remoteDerivedTestStoreDerivedVersions(t, base), 2, "each tag owns its conversion")
	require.NoError(t, tagA.Close())
	require.NoError(t, tagB.Close())

	resetRemoteDerivedDiagnostics()
	warmA := startRemoteDerivedTestRuleSet(t, fixtureX.ctx, "f06-a", fixtureX.server.URL, C.RuleSetFormatBinary)
	warmB := startRemoteDerivedTestRuleSet(t, fixtureX.ctx, "f06-b", fixtureX.server.URL, C.RuleSetFormatBinary)
	require.True(t, remoteRuleSetMatches(t, warmA, "10.1.2.3"))
	require.True(t, remoteRuleSetMatches(t, warmB, "10.1.2.3"))
	requireRemoteDerivedCounters(t, "two tags warm", 2, 0, 0, 0, 0)
	require.EqualValues(t, 2, fixtureX.requestCount(), "neither warm start may download")

	// Same tag, another URL, other content: a fresh owner, never the old document.
	serverY := newRemoteDerivedTestSourceServer(t, payloadY, "")
	fixtureY := newRemoteDerivedTestHTTP(t, fixtureX.ctx, serverY)
	resetRemoteDerivedDiagnostics()
	moved := startRemoteDerivedTestRuleSet(t, fixtureY.ctx, "f06-a", fixtureY.server.URL, C.RuleSetFormatBinary)
	require.True(t, remoteRuleSetMatches(t, moved, "192.168.1.1"))
	require.False(t, remoteRuleSetMatches(t, moved, "10.1.2.3"), "the re-pointed tag must not serve the old document")
	requireRemoteDerivedCounters(t, "re-pointed tag cold", 0, 1, 1, 1, 0)
	require.EqualValues(t, 1, fixtureY.requestCount())

	resetRemoteDerivedDiagnostics()
	warmMoved := startRemoteDerivedTestRuleSet(t, fixtureY.ctx, "f06-a", fixtureY.server.URL, C.RuleSetFormatBinary)
	require.True(t, remoteRuleSetMatches(t, warmMoved, "192.168.1.1"))
	requireRemoteDerivedCounters(t, "re-pointed tag warm", 1, 0, 0, 0, 0)
	require.EqualValues(t, 1, fixtureY.requestCount())

	// The original URL keeps its own document and its own warm conversion.
	resetRemoteDerivedDiagnostics()
	warmOld := startRemoteDerivedTestRuleSet(t, fixtureY.ctx, "f06-a", fixtureX.server.URL, C.RuleSetFormatBinary)
	require.True(t, remoteRuleSetMatches(t, warmOld, "10.1.2.3"))
	require.False(t, remoteRuleSetMatches(t, warmOld, "192.168.1.1"))
	requireRemoteDerivedCounters(t, "original URL warm", 1, 0, 0, 0, 0)
	require.EqualValues(t, 2, fixtureX.requestCount(), "the original URL must not be re-downloaded")
	require.EqualValues(t, 1, fixtureY.requestCount())
	require.Len(t, remoteDerivedTestStoreDerivedVersions(t, base), 3, "per-key ownership: three conversions")
}

// TestRemoteDerivedF07PinnedSourceUsesItsOwnConversion is F07 with the pinned document's
// conversion still on disk: the newest SOURCE version is B, the newest CONVERSION is A's, and a
// pinned start must load A's exact conversion (hit, no decode) and never B's rules.
func TestRemoteDerivedF07PinnedSourceUsesItsOwnConversion(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	ctx, base := derivedTestContext(t)
	payloadA := writeRuleSetPayload(t, "10.0.0.0/8")
	payloadB := writeRuleSetPayload(t, "192.168.0.0/16")
	digestA := ruleSetDigest(payloadA)
	digestB := ruleSetDigest(payloadB)

	server := newRemoteDerivedTestSourceServer(t, payloadA, "")
	fixture := newRemoteDerivedTestHTTP(t, ctx, server)
	key := artifactKeyFor("f07-present", fixture.server.URL)
	// Keep A retained on disk while B becomes the newest source: the pin must still find it.
	retained := artifactReferenceSet(nil, map[string]string{key: digestA})
	fixture.ctx = service.ContextWith[*artifact.ReferenceSet](fixture.ctx, retained)

	first := startRemoteDerivedTestRuleSet(t, fixture.ctx, "f07-present", fixture.server.URL, C.RuleSetFormatBinary)
	require.True(t, remoteRuleSetMatches(t, first, "10.1.2.3"))
	require.Equal(t, digestA, first.artifactLease.ID())
	require.NoError(t, first.Close())

	server.setPayload(payloadB)
	second := startRemoteDerivedTestRuleSet(t, fixture.ctx, "f07-present", fixture.server.URL, C.RuleSetFormatBinary)
	require.NoError(t, second.fetch(ctx, false))
	require.Equal(t, digestB, second.artifactLease.ID())
	require.NoError(t, second.Close())

	// Remove B's conversion, leaving A's as the newest conversion while B stays the newest source.
	require.NoError(t, os.RemoveAll(remoteDerivedTestVersionForSource(t, base, digestB).Dir))

	pinned := artifactReferenceSet(map[string]string{key: digestA}, map[string]string{key: digestA})
	pinnedCtx := service.ContextWith[*artifact.ReferenceSet](service.ExtendContext(fixture.ctx), pinned)
	resetRemoteDerivedDiagnostics()
	pinnedStart := startRemoteDerivedTestRuleSet(t, pinnedCtx, "f07-present", fixture.server.URL, C.RuleSetFormatBinary)
	require.True(t, remoteRuleSetMatches(t, pinnedStart, "10.1.2.3"), "the pinned A must be served")
	require.False(t, remoteRuleSetMatches(t, pinnedStart, "192.168.1.1"), "the newest source B must never be served")
	require.Equal(t, digestA, pinnedStart.artifactLease.ID())
	require.Equal(t, digestA, pinned.Snapshot()[key])
	requireRemoteDerivedCounters(t, "pinned A hit", 1, 0, 0, 0, 0)

	// Control: an UNPINNED start takes the newest source (B), so the pinned result above was the pin
	// and not a cache that always wins.
	unpinned := artifactReferenceSet(nil, nil)
	unpinnedCtx := service.ContextWith[*artifact.ReferenceSet](service.ExtendContext(fixture.ctx), unpinned)
	resetRemoteDerivedDiagnostics()
	unpinnedStart := startRemoteDerivedTestRuleSet(t, unpinnedCtx, "f07-present", fixture.server.URL, C.RuleSetFormatBinary)
	require.True(t, remoteRuleSetMatches(t, unpinnedStart, "192.168.1.1"))
	require.False(t, remoteRuleSetMatches(t, unpinnedStart, "10.1.2.3"))
	require.Equal(t, digestB, unpinnedStart.artifactLease.ID())
	requireRemoteDerivedCounters(t, "unpinned newest", 0, 1, 1, 1, 0)
}

// TestRemoteDerivedF07PinnedSourceRebuildsWhenItsConversionIsGone is the other half of F07: when
// the pinned document's conversion is absent (but a newer source and its conversion exist), the
// pinned start must MISS, parse the PINNED bytes and rebuild A - never serve B. Then the rebuilt
// conversion is the only one left, so the next pinned start hits it.
func TestRemoteDerivedF07PinnedSourceRebuildsWhenItsConversionIsGone(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	ctx, base := derivedTestContext(t)
	payloadA := writeRuleSetPayload(t, "10.0.0.0/8")
	payloadB := writeRuleSetPayload(t, "192.168.0.0/16")
	digestA := ruleSetDigest(payloadA)
	digestB := ruleSetDigest(payloadB)

	server := newRemoteDerivedTestSourceServer(t, payloadA, "")
	fixture := newRemoteDerivedTestHTTP(t, ctx, server)
	key := artifactKeyFor("f07-gone", fixture.server.URL)
	retained := artifactReferenceSet(nil, map[string]string{key: digestA})
	fixture.ctx = service.ContextWith[*artifact.ReferenceSet](fixture.ctx, retained)

	first := startRemoteDerivedTestRuleSet(t, fixture.ctx, "f07-gone", fixture.server.URL, C.RuleSetFormatBinary)
	require.Equal(t, digestA, first.artifactLease.ID())
	require.NoError(t, first.Close())
	server.setPayload(payloadB)
	second := startRemoteDerivedTestRuleSet(t, fixture.ctx, "f07-gone", fixture.server.URL, C.RuleSetFormatBinary)
	require.NoError(t, second.fetch(ctx, false))
	require.Equal(t, digestB, second.artifactLease.ID())
	require.NoError(t, second.Close())

	// Both conversions gone: the pinned source A must be rebuilt from A's bytes, not from B's.
	for _, version := range remoteDerivedTestStoreDerivedVersions(t, base) {
		require.NoError(t, os.RemoveAll(version.Dir))
	}
	pinned := artifactReferenceSet(map[string]string{key: digestA}, map[string]string{key: digestA})
	pinnedCtx := service.ContextWith[*artifact.ReferenceSet](service.ExtendContext(fixture.ctx), pinned)
	resetRemoteDerivedDiagnostics()
	rebuilt := startRemoteDerivedTestRuleSet(t, pinnedCtx, "f07-gone", fixture.server.URL, C.RuleSetFormatBinary)
	require.True(t, remoteRuleSetMatches(t, rebuilt, "10.1.2.3"), "the pinned A must be served")
	require.False(t, remoteRuleSetMatches(t, rebuilt, "192.168.1.1"), "the newest source B must never be served")
	require.Equal(t, digestA, rebuilt.artifactLease.ID(), "the live source reference is the PINNED version")
	require.Equal(t, digestA, pinned.Snapshot()[key])
	requireRemoteDerivedCounters(t, "pinned A rebuild", 0, 1, 1, 1, 0)
	remoteDerivedTestVersionForSource(t, base, digestA)

	resetRemoteDerivedDiagnostics()
	rebuiltWarm := startRemoteDerivedTestRuleSet(t, pinnedCtx, "f07-gone", fixture.server.URL, C.RuleSetFormatBinary)
	require.True(t, remoteRuleSetMatches(t, rebuiltWarm, "10.1.2.3"))
	require.False(t, remoteRuleSetMatches(t, rebuiltWarm, "192.168.1.1"))
	require.Equal(t, digestA, rebuiltWarm.artifactLease.ID())
	requireRemoteDerivedCounters(t, "pinned A rebuilt warm", 1, 0, 0, 0, 0)
}

// TestRemoteDerivedF07RollbackRebuildOutranksNewest is the rollback half of F07 (regression for the
// logical-stamp publication): A and B have each been published; the pinned A conversion is present
// on disk but ranks older than B's. The pinned start must miss, rebuild A's conversion, and the
// REBUILT conversion must outrank B's so the next pinned start hits. Without the authoritative
// publication stamp the rebuilt manifest kept its old Created, B stayed newest forever, and every
// pinned/rollback start stayed cold.
func TestRemoteDerivedF07RollbackRebuildOutranksNewest(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	ctx, base := derivedTestContext(t)
	payloadA := writeRuleSetPayload(t, "10.0.0.0/8")
	payloadB := writeRuleSetPayload(t, "192.168.0.0/16")
	digestA := ruleSetDigest(payloadA)
	digestB := ruleSetDigest(payloadB)

	server := newRemoteDerivedTestSourceServer(t, payloadA, "")
	fixture := newRemoteDerivedTestHTTP(t, ctx, server)
	key := artifactKeyFor("f07-rollback", fixture.server.URL)
	retained := artifactReferenceSet(nil, map[string]string{key: digestA})
	fixture.ctx = service.ContextWith[*artifact.ReferenceSet](fixture.ctx, retained)

	first := startRemoteDerivedTestRuleSet(t, fixture.ctx, "f07-rollback", fixture.server.URL, C.RuleSetFormatBinary)
	require.Equal(t, digestA, first.artifactLease.ID())
	derivedKey := remoteDerivedSource("f07-rollback", first.artifactKey, C.RuleSetFormatBinary, digestA, int64(len(payloadA))).key
	versionA := remoteDerivedTestVersionForSource(t, base, digestA)
	artifactDigestA := versionA.Manifest.Digest
	require.NoError(t, first.Close())

	server.setPayload(payloadB)
	box := startRemoteDerivedTestRuleSet(t, fixture.ctx, "f07-rollback", fixture.server.URL, C.RuleSetFormatBinary)
	require.Equal(t, digestA, box.artifactLease.ID())
	require.NoError(t, box.fetch(ctx, false))
	require.Equal(t, digestB, box.artifactLease.ID())
	remoteDerivedTestVersionForSource(t, base, digestB)

	// Keep A's conversion on disk but older than B's: a reader lease is the realistic reason the
	// store still has it. This is exactly the state a rollback has to rebuild from.
	aLease, loadErr := box.artifactStore.LoadExact(derivedKey, artifactDigestA)
	require.NoError(t, loadErr, "A's conversion must still be on disk for this scenario")
	t.Cleanup(aLease.Release)

	pinned := artifactReferenceSet(map[string]string{key: digestA}, map[string]string{key: digestA})
	pinnedCtx := service.ContextWith[*artifact.ReferenceSet](service.ExtendContext(fixture.ctx), pinned)
	resetRemoteDerivedDiagnostics()
	rebuild := startRemoteDerivedTestRuleSet(t, pinnedCtx, "f07-rollback", fixture.server.URL, C.RuleSetFormatBinary)
	require.True(t, remoteRuleSetMatches(t, rebuild, "10.1.2.3"))
	require.False(t, remoteRuleSetMatches(t, rebuild, "192.168.1.1"))
	require.Equal(t, digestA, rebuild.artifactLease.ID())
	requireRemoteDerivedCounters(t, "rollback A rebuild", 0, 1, 1, 1, 0)

	newest, loadErr := rebuild.derivedCache.store.Load(derivedKey)
	require.NoError(t, loadErr)
	newestManifest := newest.Manifest()
	newest.Release()
	newestSource, hasSource := remoteDerivedTestStoreVersion{Manifest: newestManifest}.dependency("source-sha256=")
	require.True(t, hasSource)
	require.Equal(t, digestA, newestSource,
		"the rebuilt conversion must outrank B's, or the rollback stays cold on every start")
	require.NoError(t, rebuild.Close())

	resetRemoteDerivedDiagnostics()
	warm := startRemoteDerivedTestRuleSet(t, pinnedCtx, "f07-rollback", fixture.server.URL, C.RuleSetFormatBinary)
	require.True(t, remoteRuleSetMatches(t, warm, "10.1.2.3"))
	require.False(t, remoteRuleSetMatches(t, warm, "192.168.1.1"))
	require.Equal(t, digestA, warm.artifactLease.ID())
	requireRemoteDerivedCounters(t, "rollback A warm", 1, 0, 0, 0, 0)
}

// TestRemoteDerivedF08LegacyMigrationReusesDerivedConversion is the derived half of F08: the
// legacy single-slot cache is a stable source identity even before migration, so a restart that
// still only has the legacy entry must hit the conversion of exactly those bytes, skip the decode,
// and republish the source version into the store.
func TestRemoteDerivedF08LegacyMigrationReusesDerivedConversion(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	ctx, base := derivedTestContext(t)
	payload := writeRuleSetPayload(t, "10.0.0.0/8")
	sourceID := ruleSetDigest(payload)
	url := "https://rules.example/f08-derived.srs"

	first := newRemoteDerivedTestRuleSet(t, ctx, "f08-derived", url, C.RuleSetFormatBinary)
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
	require.Equal(t, sourceID, first.artifactLease.ID())
	requireRemoteDerivedCounters(t, "legacy cold migration", 0, 1, 1, 1, 0)
	remoteDerivedTestVersionForSource(t, base, sourceID)
	key := first.artifactKey
	require.NoError(t, first.Close())

	// The store's source key is gone; only the legacy cache and the conversion remain. This is the
	// state a pre-store installation restarts into.
	require.NoError(t, os.RemoveAll(filepath.Join(base, derivedStoreDirectory, key)))
	restarted := newRemoteDerivedTestRuleSet(t, ctx, "f08-derived", url, C.RuleSetFormatBinary)
	restarted.openArtifactStore()
	restarted.cacheFile = cache
	restored, restoreErr := restarted.restoreStoredRuleSet()
	require.NoError(t, restoreErr)
	require.False(t, restored, "there is no stored source version to restore")
	resetRemoteDerivedDiagnostics()
	require.True(t, restarted.restoreCachedRuleSet(), "the legacy cache must restore the rule set")
	require.True(t, remoteRuleSetMatches(t, restarted, "10.1.2.3"))
	require.Equal(t, sourceID, restarted.artifactLease.ID())
	requireRemoteDerivedCounters(t, "legacy warm migration", 1, 0, 0, 0, 0)
	require.Zero(t, cache.saves, "migration writes the store, not the legacy cache")
	require.NotNil(t, cache.saved)
	sources := remoteDerivedTestStoreSourceVersions(t, base)
	require.Len(t, sources, 1, "the migration must republish the source version")
	require.Equal(t, sourceID, sources[0].Manifest.Digest)
}

// TestRemoteDerivedF09ContextValidationRunsOnAHit is F09: the persisted conversion is immutable
// compiled data, but the current Box's rule construction and metadata validation still run. A
// changed validator must reject the hit, and the load must fall back to the source (which the same
// validator rejects again) rather than publishing the old result.
func TestRemoteDerivedF09ContextValidationRunsOnAHit(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	ctx, base := derivedTestContext(t)
	var reject atomic.Bool
	validator := &fakeDNSRuleSetUpdateValidator{validate: func(tag string, metadata adapter.RuleSetMetadata) error {
		if tag != "f09-context" {
			return nil
		}
		if reject.Load() {
			return E.New("this Box's context refuses the rule set")
		}
		return nil
	}}
	ctx = service.ContextWith[adapter.DNSRuleSetUpdateValidator](ctx, validator)
	payload := derivedJSONDomainSource("f09.example")
	server := newRemoteDerivedTestSourceServer(t, payload, "")
	fixture := newRemoteDerivedTestHTTP(t, ctx, server)
	sourceID := ruleSetDigest(payload)

	cold := startRemoteDerivedTestRuleSet(t, fixture.ctx, "f09-context", fixture.server.URL, C.RuleSetFormatSource)
	requireRemoteDerivedDomainMatch(t, cold, "f09.example", true)
	coldMetadata := cold.Metadata()
	require.Equal(t, sourceID, cold.artifactLease.ID())
	require.NoError(t, cold.Close())

	// The cached conversion exists. A validator that now rejects this rule set must make the warm
	// restore fail through the validation step, not publish the cached rules.
	reject.Store(true)
	resetRemoteDerivedDiagnostics()
	refused := newRemoteDerivedTestRuleSet(t, fixture.ctx, "f09-context", fixture.server.URL, C.RuleSetFormatSource)
	refused.openArtifactStore()
	require.NotNil(t, refused.derivedCache)
	restored, restoreErr := refused.restoreStoredRuleSet()
	require.NoError(t, restoreErr)
	require.False(t, restored, "the hit must not bypass the current context validation")
	require.Nil(t, refused.currentRules(), "no rules may be published")
	require.Equal(t, adapter.RuleSetMetadata{}, refused.Metadata())
	require.Nil(t, refused.artifactLease)
	requireRemoteDerivedCounters(t, "refused hit", 1, 0, 1, 1, 0)
	require.NotEmpty(t, remoteDerivedTestStoreDerivedVersions(t, base),
		"a context refusal is not cache corruption: the conversion must stay available")

	// Control: with the validator accepting again, the same warm restore hits and publishes.
	reject.Store(false)
	resetRemoteDerivedDiagnostics()
	accepted := newRemoteDerivedTestRuleSet(t, fixture.ctx, "f09-context", fixture.server.URL, C.RuleSetFormatSource)
	accepted.openArtifactStore()
	restored, restoreErr = accepted.restoreStoredRuleSet()
	require.NoError(t, restoreErr)
	require.True(t, restored)
	requireRemoteDerivedDomainMatch(t, accepted, "f09.example", true)
	require.Equal(t, coldMetadata, accepted.Metadata())
	requireRemoteDerivedCounters(t, "accepted hit", 1, 0, 0, 0, 0)
}

// TestRemoteDerivedF10IdenticalSRSMDifferentProvenance is F10: two different source documents can
// compile to identical SRSM bytes, so the physical version is shared. The provenance must still be
// exact: a pinned restore of A may not accept the newest manifest naming B without rebuilding, and
// the rebuilt conversion must name A.
func TestRemoteDerivedF10IdenticalSRSMDifferentProvenance(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	ctx, base := derivedTestContext(t)
	payloadX := derivedJSONDomainSource("f10.example")
	// Same parsed document, different bytes: whitespace is not part of the rule set but IS part of
	// the source identity the manifest must name.
	payloadY := []byte("{\n  \"version\": 1,\n  \"rules\": [ {\"domain\": [\"f10.example\"]} ]\n}\n")
	require.NotEqual(t, string(payloadX), string(payloadY))
	require.Equal(t,
		encodeDerivedMmapRuleSet(t, parseDerivedJSONSource(t, payloadX)),
		encodeDerivedMmapRuleSet(t, parseDerivedJSONSource(t, payloadY)),
		"fixture premise: the two sources compile to identical SRSM bytes")
	digestX := ruleSetDigest(payloadX)
	digestY := ruleSetDigest(payloadY)
	require.NotEqual(t, digestX, digestY)

	server := newRemoteDerivedTestSourceServer(t, payloadX, "")
	fixture := newRemoteDerivedTestHTTP(t, ctx, server)
	key := artifactKeyFor("f10-identical", fixture.server.URL)
	retained := artifactReferenceSet(nil, map[string]string{key: digestX})
	fixture.ctx = service.ContextWith[*artifact.ReferenceSet](fixture.ctx, retained)

	first := startRemoteDerivedTestRuleSet(t, fixture.ctx, "f10-identical", fixture.server.URL, C.RuleSetFormatSource)
	requireRemoteDerivedDomainMatch(t, first, "f10.example", true)
	require.Equal(t, digestX, first.artifactLease.ID())
	versionX := remoteDerivedTestVersionForSource(t, base, digestX)
	srsmPath := versionX.ArtifactPath()
	srsmBytes := versionX.ArtifactBytes(t)

	// Y's fetch compiles to the SAME bytes: one physical version, its manifest republished to name Y.
	server.setPayload(payloadY)
	resetRemoteDerivedDiagnostics()
	require.NoError(t, first.fetch(ctx, false))
	require.Equal(t, digestY, first.artifactLease.ID())
	requireRemoteDerivedCounters(t, "identical-SRSM content change", 0, 1, 1, 1, 0)
	versionY := remoteDerivedTestVersionForSource(t, base, digestY)
	require.Equal(t, srsmPath, versionY.ArtifactPath(), "identical SRSM bytes are one physical version")
	require.Equal(t, srsmBytes, versionY.ArtifactBytes(t))
	require.Len(t, remoteDerivedTestStoreDerivedVersions(t, base), 1)
	require.NoError(t, first.Close())

	// Pinned X: the newest manifest names Y, so the conversion may not be served as X; the load must
	// reparse X and republish the manifest as X.
	pinned := artifactReferenceSet(map[string]string{key: digestX}, map[string]string{key: digestX})
	pinnedCtx := service.ContextWith[*artifact.ReferenceSet](service.ExtendContext(fixture.ctx), pinned)
	resetRemoteDerivedDiagnostics()
	pinnedFirst := startRemoteDerivedTestRuleSet(t, pinnedCtx, "f10-identical", fixture.server.URL, C.RuleSetFormatSource)
	requireRemoteDerivedDomainMatch(t, pinnedFirst, "f10.example", true)
	require.Equal(t, digestX, pinnedFirst.artifactLease.ID(), "the pinned SOURCE reference must win")
	require.Equal(t, digestX, pinned.Snapshot()[key])
	requireRemoteDerivedCounters(t, "pinned X rebuild", 0, 1, 1, 1, 0)
	repaired := remoteDerivedTestVersionForSource(t, base, digestX)
	require.Equal(t, srsmPath, repaired.ArtifactPath())
	require.Len(t, remoteDerivedTestStoreDerivedVersions(t, base), 1)
	require.NoError(t, pinnedFirst.Close())

	resetRemoteDerivedDiagnostics()
	pinnedWarm := startRemoteDerivedTestRuleSet(t, pinnedCtx, "f10-identical", fixture.server.URL, C.RuleSetFormatSource)
	requireRemoteDerivedDomainMatch(t, pinnedWarm, "f10.example", true)
	require.Equal(t, digestX, pinnedWarm.artifactLease.ID())
	requireRemoteDerivedCounters(t, "pinned X warm", 1, 0, 0, 0, 0)
}
