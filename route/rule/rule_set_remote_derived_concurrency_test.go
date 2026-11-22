//go:build unix

package rule

// C01-C06 of the remote derived-cache matrix: the concurrent and lifecycle interleavings.
//
// Every interleaving is driven by the production test hook or by an HTTP handler that blocks on a
// channel, never by sleeping to hit a window. A timeout is only the failure bound of a wait.

import (
	"net/http"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/artifact"
	"github.com/sagernet/sing-box/common/buildgate"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
	"go4.org/netipx"
)

func awaitRemoteDerivedLoad(t *testing.T, result <-chan error, what string) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not finish", what)
		return nil
	}
}

// TestRemoteDerivedC01LateRefreshCannotReplaceNewer is C01: refresh A starts first and is blocked
// after it downloaded and built; refresh B starts later and commits. Releasing A must leave B live,
// with B's source lease, metadata and ETag, and a restart must restore B - not A.
func TestRemoteDerivedC01LateRefreshCannotReplaceNewer(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	ctx, _ := derivedTestContext(t)
	payloadA := writeRuleSetPayload(t, "10.0.0.0/8")
	payloadB := writeRuleSetPayload(t, "192.168.0.0/16")
	digestA := ruleSetDigest(payloadA)
	digestB := ruleSetDigest(payloadB)

	server := newRemoteDerivedTestSourceServer(t, payloadA, `"etag-a"`)
	fixture := newRemoteDerivedTestHTTP(t, ctx, server)
	first := startRemoteDerivedTestRuleSet(t, fixture.ctx, "c01-late", fixture.server.URL, C.RuleSetFormatBinary)
	require.Equal(t, digestA, first.artifactLease.ID())
	require.NoError(t, first.Close())

	box := startRemoteDerivedTestRuleSet(t, fixture.ctx, "c01-late", fixture.server.URL, C.RuleSetFormatBinary)
	require.True(t, remoteRuleSetMatches(t, box, "10.1.2.3"))
	var callbacks atomic.Int32
	box.RegisterCallback(func(adapter.RuleSet) { callbacks.Add(1) })

	blocker := blockRemoteDerivedTestPoint(t, "before-source-publish")
	lateResult := make(chan error, 1)
	go func() { lateResult <- box.fetch(ctx, false) }()
	blocker.wait(t) // A is staged and built; the generation check is still ahead.

	server.setPayload(payloadB)
	server.setEtag(`"etag-b"`)
	require.NoError(t, box.fetch(ctx, false), "the newer refresh must commit while A is blocked")
	require.True(t, remoteRuleSetMatches(t, box, "192.168.1.1"))
	require.Equal(t, digestB, box.artifactLease.ID())
	updatedB, etagB := box.updateMetadata()
	require.Equal(t, `"etag-b"`, etagB)
	require.GreaterOrEqual(t, callbacks.Load(), int32(1))

	blocker.unblock()
	require.Error(t, awaitRemoteDerivedLoad(t, lateResult, "the late refresh"),
		"the late refresh must be refused as superseded")
	require.True(t, remoteRuleSetMatches(t, box, "192.168.1.1"), "B must stay live")
	require.False(t, remoteRuleSetMatches(t, box, "10.1.2.3"), "A must not be revived")
	require.Equal(t, digestB, box.artifactLease.ID())
	updatedAfter, etagAfter := box.updateMetadata()
	require.Equal(t, updatedB, updatedAfter, "the late refresh must not change the update time")
	require.Equal(t, etagB, etagAfter, "the late refresh must not change the ETag")

	// A restart restores the newest SOURCE version, which is B (started later), and hits B's
	// conversion. It must not come back to A's bytes.
	resetRemoteDerivedDiagnostics()
	restarted := startRemoteDerivedTestRuleSet(t, fixture.ctx, "c01-late", fixture.server.URL, C.RuleSetFormatBinary)
	require.True(t, remoteRuleSetMatches(t, restarted, "192.168.1.1"))
	require.False(t, remoteRuleSetMatches(t, restarted, "10.1.2.3"))
	require.Equal(t, digestB, restarted.artifactLease.ID())
	requireRemoteDerivedCounters(t, "restart after a late refresh", 1, 0, 0, 0, 0)
}

// TestRemoteDerivedC02CloseAtEveryWindow is C02: Close at the derived hit, at the miss, before the
// source publication and before the commit. No path may revive rules, run a callback or record a
// source reference after the close.
func TestRemoteDerivedC02CloseAtEveryWindow(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	payload := writeRuleSetPayload(t, "10.0.0.0/8")

	cases := []struct {
		name  string
		point string
		// restore starts the recovery path instead of a fetch.
		restore bool
	}{
		{name: "derived-hit", point: "derived-hit", restore: true},
		{name: "derived-miss", point: "derived-miss"},
		{name: "before-source-publish", point: "before-source-publish"},
		{name: "before-commit", point: "before-commit"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			ctx, _ := derivedTestContext(t)
			server := newRemoteDerivedTestSourceServer(t, payload, "")
			fixture := newRemoteDerivedTestHTTP(t, ctx, server)
			tag := "c02-" + testCase.name

			if testCase.restore {
				// A cached source version and its conversion must exist before the blocked restore.
				seed := startRemoteDerivedTestRuleSet(t, fixture.ctx, tag, fixture.server.URL, C.RuleSetFormatBinary)
				require.Equal(t, ruleSetDigest(payload), seed.artifactLease.ID())
				require.NoError(t, seed.Close())
			}
			// A fresh reference set for the box under test: the seed's own records must not be
			// mistaken for a record made after Close.
			refs := artifactReferenceSet(nil, nil)
			boxCtx := service.ContextWith[*artifact.ReferenceSet](service.ExtendContext(fixture.ctx), refs)
			box := newRemoteDerivedTestRuleSet(t, boxCtx, tag, fixture.server.URL, C.RuleSetFormatBinary)
			box.httpClient = fixture.server.Client()
			box.openArtifactStore()
			require.NotNil(t, box.artifactStore)
			var callbacks atomic.Int32
			box.RegisterCallback(func(adapter.RuleSet) { callbacks.Add(1) })

			blocker := blockRemoteDerivedTestPoint(t, testCase.point)
			if testCase.restore {
				type restoreResult struct {
					restored bool
					err      error
				}
				result := make(chan restoreResult, 1)
				go func() {
					restored, err := box.restoreStoredRuleSet()
					result <- restoreResult{restored: restored, err: err}
				}()
				blocker.wait(t)
				require.NoError(t, box.Close())
				blocker.unblock()
				select {
				case outcome := <-result:
					require.NoError(t, outcome.err)
					require.False(t, outcome.restored, "a closed rule-set must not report a restore")
				case <-time.After(5 * time.Second):
					t.Fatal("the restore did not return after Close")
				}
			} else {
				result := make(chan error, 1)
				go func() { result <- box.fetch(boxCtx, true) }()
				blocker.wait(t)
				require.NoError(t, box.Close())
				blocker.unblock()
				require.Error(t, awaitRemoteDerivedLoad(t, result, "the closed fetch"))
			}

			require.Nil(t, box.currentRules(), "a closed rule-set must not publish rules")
			require.Equal(t, adapter.RuleSetMetadata{}, box.Metadata())
			require.Nil(t, box.artifactLease, "a closed rule-set must not hold a source lease")
			require.Zero(t, callbacks.Load(), "no callback may run after Close")
			require.Empty(t, refs.Snapshot(), "no source reference may be recorded after Close")

			// A further close is a no-op and does not revive anything.
			require.NoError(t, box.Close())
			require.Nil(t, box.currentRules())
		})
	}
}

// TestRemoteDerivedC03TwoBoxesOneLifecycle is C03: two Boxes on one source and one store. Closing or
// collecting for one must leave the other serving and matching.
func TestRemoteDerivedC03TwoBoxesOneLifecycle(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	ctx, _ := derivedTestContext(t)
	payloadA := writeRuleSetPayload(t, "10.0.0.0/8")
	payloadB := writeRuleSetPayload(t, "192.168.0.0/16")
	server := newRemoteDerivedTestSourceServer(t, payloadA, "")
	fixture := newRemoteDerivedTestHTTP(t, ctx, server)

	first := startRemoteDerivedTestRuleSet(t, fixture.ctx, "c03-boxes", fixture.server.URL, C.RuleSetFormatBinary)
	require.True(t, remoteRuleSetMatches(t, first, "10.1.2.3"))
	second := startRemoteDerivedTestRuleSet(t, fixture.ctx, "c03-boxes", fixture.server.URL, C.RuleSetFormatBinary)
	require.True(t, remoteRuleSetMatches(t, second, "10.1.2.3"))
	require.EqualValues(t, 1, fixture.requestCount(), "the second Box must restore, not download")

	require.NoError(t, first.Close())
	require.True(t, remoteRuleSetMatches(t, second, "10.1.2.3"), "one Box closing must not disturb the other")
	require.NoError(t, second.artifactStore.Collect(second.artifactKey))
	require.True(t, remoteRuleSetMatches(t, second, "10.1.2.3"), "Collect must not disturb a live Box")

	server.setPayload(payloadB)
	require.NoError(t, second.fetch(ctx, false))
	require.True(t, remoteRuleSetMatches(t, second, "192.168.1.1"))
	require.Equal(t, ruleSetDigest(payloadB), second.artifactLease.ID())
	require.NoError(t, second.Close())
}

// TestRemoteDerivedC04OldRulesAndIPSetSurviveUpdate is C04: a matcher and an ExtractIPSet result
// taken before an update stay valid across the update and its collection; the mapped generation is
// released only after the last reference is dropped.
func TestRemoteDerivedC04OldRulesAndIPSetSurviveUpdate(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	ctx, base := derivedTestContext(t)
	payloadA := writeRuleSetPayload(t, "10.0.0.0/8")
	payloadB := writeRuleSetPayload(t, "192.168.0.0/16")
	digestA := ruleSetDigest(payloadA)

	server := newRemoteDerivedTestSourceServer(t, payloadA, "")
	fixture := newRemoteDerivedTestHTTP(t, ctx, server)
	seed := startRemoteDerivedTestRuleSet(t, fixture.ctx, "c04-old", fixture.server.URL, C.RuleSetFormatBinary)
	require.NoError(t, seed.Close())

	box := startRemoteDerivedTestRuleSet(t, fixture.ctx, "c04-old", fixture.server.URL, C.RuleSetFormatBinary)
	require.Equal(t, digestA, box.artifactLease.ID())
	oldRules := box.currentRules()
	oldIPSet := box.ExtractIPSet()
	require.NotEmpty(t, oldRules)
	require.NotEmpty(t, oldIPSet)
	derivedKey := remoteDerivedTestVersionForSource(t, base, digestA).Key

	server.setPayload(payloadB)
	require.NoError(t, box.fetch(ctx, false))
	require.True(t, remoteRuleSetMatches(t, box, "192.168.1.1"))
	require.True(t, matchAnyHeadlessRule(oldRules, remoteDerivedTestInbound("10.1.2.3")),
		"the old matcher must stay valid across the update")
	requireIPSetContains(t, oldIPSet, "10.1.2.3", true)
	requireIPSetContains(t, oldIPSet, "192.168.1.1", false)

	// Collect while the old rules are referenced: both the live generation and the mapped old one
	// must stay readable.
	require.NoError(t, box.artifactStore.Collect(derivedKey))
	require.Len(t, remoteDerivedTestStoreDerivedVersions(t, base), 2)
	require.True(t, matchAnyHeadlessRule(oldRules, remoteDerivedTestInbound("10.1.2.3")))

	// Drop the rule reference but keep the IPSet value: the IPSet is a copy, so it stays valid after
	// the mapped generation is collected, while the mapping lease is what delayed collection.
	oldRules = nil
	_ = oldRules
	requireIPSetContains(t, oldIPSet, "10.1.2.3", true)
	require.True(t, awaitDerivedState(t, 10*time.Second, func() bool {
		require.NoError(t, box.artifactStore.Collect(derivedKey))
		return len(remoteDerivedTestStoreDerivedVersions(t, base)) == 1
	}), "the unleased old generation is collected once its matcher is dropped")
	requireIPSetContains(t, oldIPSet, "10.1.2.3", true, "an extracted IPSet stays valid after collection")
	require.True(t, remoteRuleSetMatches(t, box, "192.168.1.1"))
}

func requireIPSetContains(t *testing.T, sets []*netipx.IPSet, address string, want bool, messages ...any) {
	t.Helper()
	ip := netip.MustParseAddr(address)
	found := false
	for _, set := range sets {
		if set != nil && set.Contains(ip) {
			found = true
			break
		}
	}
	require.Equal(t, want, found, messages...)
}

// TestRemoteDerivedC05FailedCandidateDoesNotPolluteLastGood is C05 at the rule-set boundary: a
// candidate that fails validation must not replace the last-good source version, its conversion or
// the exact reference; a recovery pinned to the last-good digest then serves those rules.
//
// The full configtx rollback runs in the daemon/config transaction layer, outside this package; this
// test covers the rule-set half of the transaction: what a failed candidate may leave behind.
func TestRemoteDerivedC05FailedCandidateDoesNotPolluteLastGood(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	ctx, base := derivedTestContext(t)
	payloadA := writeRuleSetPayload(t, "10.0.0.0/8")
	digestA := ruleSetDigest(payloadA)
	server := newRemoteDerivedTestSourceServer(t, payloadA, "")
	fixture := newRemoteDerivedTestHTTP(t, ctx, server)
	key := artifactKeyFor("c05-last-good", fixture.server.URL)
	refs := artifactReferenceSet(nil, map[string]string{key: digestA})
	fixture.ctx = service.ContextWith[*artifact.ReferenceSet](fixture.ctx, refs)

	first := startRemoteDerivedTestRuleSet(t, fixture.ctx, "c05-last-good", fixture.server.URL, C.RuleSetFormatBinary)
	require.Equal(t, digestA, first.artifactLease.ID())
	require.Equal(t, digestA, refs.Snapshot()[key])
	require.NoError(t, first.Close())

	// The candidate transaction fails: invalid bytes must publish no source version and no
	// conversion, and the last-good reference must stay exactly where it was.
	server.setPayload([]byte("not a rule set"))
	box := startRemoteDerivedTestRuleSet(t, fixture.ctx, "c05-last-good", fixture.server.URL, C.RuleSetFormatBinary)
	require.Equal(t, digestA, box.artifactLease.ID())
	require.Error(t, box.fetch(ctx, false))
	require.True(t, remoteRuleSetMatches(t, box, "10.1.2.3"), "the last-good rules keep serving")
	require.Equal(t, digestA, box.artifactLease.ID())
	require.Equal(t, digestA, refs.Snapshot()[key], "a failed candidate must not move the exact reference")
	require.Len(t, remoteDerivedTestStoreSourceVersions(t, base), 1)
	derivedVersion := remoteDerivedTestVersionForSource(t, base, digestA)
	require.Contains(t, derivedVersion.Manifest.Dependencies, "source-sha256="+digestA)
	require.NoError(t, box.Close())

	// The rollback recovery pins the last-good source digest and serves exactly those rules.
	pinned := artifactReferenceSet(map[string]string{key: digestA}, map[string]string{key: digestA})
	pinnedCtx := service.ContextWith[*artifact.ReferenceSet](service.ExtendContext(fixture.ctx), pinned)
	resetRemoteDerivedDiagnostics()
	recovered := startRemoteDerivedTestRuleSet(t, pinnedCtx, "c05-last-good", fixture.server.URL, C.RuleSetFormatBinary)
	require.True(t, remoteRuleSetMatches(t, recovered, "10.1.2.3"))
	require.Equal(t, digestA, recovered.artifactLease.ID())
	require.Equal(t, digestA, pinned.Snapshot()[key])
	requireRemoteDerivedCounters(t, "pinned last-good recovery", 1, 0, 0, 0, 0)
}

// TestRemoteDerivedC06BuildgateReservationCoversHitAndMiss is C06/pairwise for the buildgate: a
// derived hit still takes the box's build reservation, a deferred refresh downloads nothing, and
// every outcome returns the slot with the right classification.
func TestRemoteDerivedC06BuildgateReservationCoversHitAndMiss(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	gate := buildgate.New(1, 0, time.Millisecond)
	ctx, _ := derivedTestContext(t)
	ctx = buildgate.ContextWithGate(ctx, gate)
	payload := writeRuleSetPayload(t, "10.0.0.0/8")
	requestStarted := make(chan struct{}, 4)
	releaseRequest := make(chan struct{})
	var releaseOnce sync.Once
	releaseHeld := func() { releaseOnce.Do(func() { close(releaseRequest) }) }
	var servedRequests atomic.Int32
	var block atomic.Bool
	server := newRemoteDerivedTestSourceServer(t, payload, "")
	blockedHandler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		servedRequests.Add(1)
		if block.Load() {
			requestStarted <- struct{}{}
			<-releaseRequest
		}
		server.ServeHTTP(writer, request)
	})
	fixture := newRemoteDerivedTestHTTP(t, ctx, blockedHandler)
	t.Cleanup(releaseHeld)
	winner := startRemoteDerivedTestRuleSet(t, fixture.ctx, "c06-gate", fixture.server.URL, C.RuleSetFormatBinary)
	require.True(t, remoteRuleSetMatches(t, winner, "10.1.2.3"))
	// A second object on the same source and gate. It must be deferred while the first one holds
	// the only reservation; because it is a DIFFERENT rule-set object its generation cannot
	// supersede the holder.
	other := startRemoteDerivedTestRuleSet(t, fixture.ctx, "c06-gate", fixture.server.URL, C.RuleSetFormatBinary)
	require.True(t, remoteRuleSetMatches(t, other, "10.1.2.3"))

	block.Store(true)
	resetRemoteDerivedDiagnostics()
	heldResult := make(chan error, 1)
	go func() { heldResult <- winner.fetch(ctx, false) }()
	select {
	case <-requestStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("the admitted refresh never reached the server")
	}
	baselineRequests := servedRequests.Load()
	deferredErr := other.fetch(ctx, false)
	require.ErrorIs(t, deferredErr, buildgate.ErrDeferred, "the second refresh must be deferred, not downloaded")
	require.Equal(t, baselineRequests, servedRequests.Load(), "a deferred build must not reach the server")
	requireRemoteDerivedCounters(t, "deferred refresh", 0, 0, 0, 0, 0)

	releaseHeld()
	require.NoError(t, awaitRemoteDerivedLoad(t, heldResult, "the admitted refresh"))
	requireRemoteDerivedCounters(t, "admitted hit refresh", 1, 0, 0, 0, 0)

	// The slot is free again: the next refresh is admitted and succeeds as a hit.
	require.NoError(t, winner.fetch(ctx, false))
	requireRemoteDerivedCounters(t, "next admitted hit", 2, 0, 0, 0, 0)

	// A failing refresh classifies as failed and still returns its slot.
	server.setStatus(http.StatusInternalServerError)
	require.Error(t, winner.fetch(ctx, false))
	stats := gate.Stats()
	require.EqualValues(t, 1, stats.Deferred)
	require.EqualValues(t, 1, stats.Failed)
	require.GreaterOrEqual(t, stats.Succeeded, uint64(3), "cold, hit and second hit succeeded")
	require.Zero(t, stats.Inflight, "every outcome must return the slot")
}
