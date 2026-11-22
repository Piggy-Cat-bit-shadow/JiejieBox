package daemon

// E1 (Box level) — provider update × build not finished × Box.Close.
//
// The provider-layer test in `provider/remote/close_during_build_test.go` pins the reservation's
// classification in isolation. This one pins the thing only a real Box can show: that the same
// three-way interleaving through `StartedService`/`Instance.Close` publishes nothing, mutates no
// artifact or reference, and leaves the shared artifact directory good enough for the NEXT Box to
// start cleanly.
//
// # The interleaving, and why it is formed with barriers
//
//	1. a Box starts and its remote provider publishes an initial document (a build that succeeded)
//	2. a manual provider refresh is admitted and reaches the server; the server sends headers and
//	   stops, so the build is provably mid-download (the build reservation reports Inflight == 1)
//	3. the service is closed - the Box's scope tears the provider down
//
// No step waits for a probability: step 2 signals from inside the HTTP handler, step 3 is called
// after that signal, and the assertions read the build reservation, the provider's own report, the
// node constructor counter, and the on-disk artifact store.
//
// # What is asserted, and which failure each assertion catches
//
//	provider report    unknown, no applied revision, no UpdatedAt movement: a late publish would
//	                   make the report committed or move the timestamp
//	node construction  the new document's node is never constructed: a publish is a construction
//	build reservation  final state is abandoned (not succeeded, not failed) with Inflight back to 0
//	artifact store     the version list is byte-for-byte the one the initial build produced
//	reference set      the exact-version record is unchanged, so a recovery cannot pin a version
//	                   this aborted attempt never published
//	next Box           a new service on the SAME directory starts and publishes normally

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/buildgate"
	providerremote "github.com/sagernet/sing-box/provider/remote"

	"github.com/stretchr/testify/require"
)

const (
	crossSystemInitialDocument = `{"outbounds": [{"type": "cross-system-node", "tag": "live"}]}`
	crossSystemLateDocument    = `{"outbounds": [{"type": "cross-system-node", "tag": "late"}]}`
)

// crossSystemProviderProfile is the smallest product profile whose provider build is observable:
// a remote provider, a selector that consumes it (so the publish path goes through the group
// callback registration), and a direct fallback.
func crossSystemProviderProfile(providerURL string) string {
	return crossSystemProviderProfileWithCache(providerURL, "")
}

// crossSystemProviderProfileWithCache is the same profile with an explicit cache-file path. Two
// concurrently running Boxes on one working directory need distinct paths: the cache file is a
// bbolt database with an exclusive lock (see TestConcurrentBoxesCannotShareOneCacheFile...).
func crossSystemProviderProfileWithCache(providerURL string, cachePath string) string {
	experimental := ""
	if cachePath != "" {
		experimental = fmt.Sprintf(`"experimental": {"cache_file": {"enabled": true, "path": %q}},`, cachePath)
	}
	return fmt.Sprintf(`{
  "log": {"disabled": true},
  %s
  "providers": [{"type": "remote", "tag": "nodes", "url": %q}],
  "outbounds": [
    {"type": "selector", "tag": "proxy", "providers": ["nodes"]},
    {"type": "direct", "tag": "direct"}
  ],
  "route": {
    "rules": [{"domain": ["unused.example"], "outbound": "proxy"}],
    "final": "direct"
  }
}`, experimental, providerURL)
}

func TestClosingTheBoxDuringAProviderBuildPublishesNothingAndTheNextBoxStartsClean(t *testing.T) {
	directory := t.TempDir()
	recorder := &crossSystemRecorder{}
	// The gate is installed by the TEST, which production deliberately permits: the Box keeps an
	// embedder's reservation (`box_buildgate.go`, TestAnEmbeddersReservationIsKept). Holding it here
	// is what makes the build's outcome readable instead of inferred.
	gate := buildgate.New(2, 4, time.Second)
	ctx := buildgate.ContextWithGate(crossSystemContext(t, directory, recorder), gate)

	var (
		requests   atomic.Int32
		stallAfter atomic.Bool
		stalled    = make(chan struct{})
		stallOnce  sync.Once
	)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if !stallAfter.Load() {
			_, _ = writer.Write([]byte(crossSystemInitialDocument))
			return
		}
		// The build is now past admission and inside the download. Flush the headers so the client
		// has the response, then stop: only the provider's cancellation can end this.
		writer.WriteHeader(http.StatusOK)
		writer.(http.Flusher).Flush()
		stallOnce.Do(func() { close(stalled) })
		<-request.Context().Done()
	}))
	t.Cleanup(server.Close)

	service := NewStartedService(ServiceOptions{Context: ctx})
	t.Cleanup(func() {
		_ = service.CloseService()
		service.Close()
	})
	require.NoError(t, service.StartOrReloadService(context.Background(), crossSystemProviderProfile(server.URL), nil))

	instance := service.instance
	require.NotNil(t, instance)
	providerAdapter, loaded := instance.providerManager.Get("nodes")
	require.True(t, loaded)
	remote, isRemote := providerAdapter.(*providerremote.ProviderRemote)
	require.True(t, isRemote, "the Box must serve the real remote provider")

	// A Box start does not block on the first subscription download (it runs on the provider's own
	// goroutine), so the initial build is awaited rather than assumed. The report is awaited
	// together with the nodes because a publish becomes visible a moment before `recordApply`
	// stores the report: waiting on one of the two alone leaves that window.
	require.Eventually(t, func() bool {
		report := remote.LastApplyReport()
		return len(providerNodesOf(t, providerAdapter)) == 1 &&
			report != nil && report.Status == providerremote.ApplyStatusCommitted
	}, 10*time.Second, 5*time.Millisecond, "the initial document was never published and reported")
	require.Equal(t, []string{"nodes/live"}, providerNodesOf(t, providerAdapter))
	initialReport := remote.LastApplyReport()
	require.NotNil(t, initialReport)
	require.Equal(t, providerremote.ApplyStatusCommitted, initialReport.Status)
	updatedAtInitial := remote.UpdatedAt()
	initialGate := gate.Stats()
	require.EqualValues(t, 1, initialGate.Succeeded, "the initial document was a build that succeeded")
	require.Zero(t, initialGate.Inflight)

	providerStoreRoot := filepath.Join(directory, "provider-artifacts")
	versionsBefore := crossSystemArtifactVersions(t, providerStoreRoot)
	require.Len(t, versionsBefore, 1, "the initial document produced exactly one stored snapshot")
	referencesBefore := instance.artifactRefs.Snapshot()
	require.NotEmpty(t, referencesBefore, "the initial publish recorded the version it used")
	constructedBefore := recorder.snapshot()

	// Arm the stall and refresh through the real manual entry. The refresh runs on a goroutine so
	// the test can close the service while the build is provably still in flight.
	stallAfter.Store(true)
	refreshDone := make(chan *ProviderRefreshSummary, 1)
	go func() {
		summary, _ := service.RefreshProviders(context.Background(), []string{"nodes"}, 5*time.Second)
		refreshDone <- summary
	}()
	select {
	case <-stalled:
	case <-time.After(10 * time.Second):
		t.Fatal("the refresh never reached the provider's server")
	}
	require.EqualValues(t, 2, requests.Load(), "the refresh must be the only new request")
	inflight := gate.Stats()
	require.EqualValues(t, 1, inflight.Inflight, "the build reservation must be held by the in-flight build")
	require.EqualValues(t, 2, inflight.AdmittedBuilds)

	closed := make(chan error, 1)
	go func() { closed <- service.CloseService() }()
	select {
	case err := <-closed:
		require.NoError(t, err)
	case <-time.After(15 * time.Second):
		t.Fatal("CloseService waited for a provider download it should have cancelled")
	}

	var summary *ProviderRefreshSummary
	select {
	case summary = <-refreshDone:
	case <-time.After(10 * time.Second):
		t.Fatal("the manual refresh did not return after the service closed")
	}
	require.NotNil(t, summary)
	require.Len(t, summary.Results, 1)
	// The provider's own cancellation is observed by the manual entry as an attempt that ended
	// without a publish. Its exact label is not a product contract here; "not published" is.
	require.NotEqual(t, ProviderRefreshPublished, summary.Results[0].Status,
		"a build cancelled by Close must not be reported as published")
	require.Contains(t, summary.Results[0].Reason, "context canceled",
		"the reason must name the cancellation: %+v", summary.Results[0])

	// The provider's own report: the attempt ran, installed nothing, and moved nothing.
	reportAfter := remote.LastApplyReport()
	require.NotNil(t, reportAfter)
	require.Equal(t, providerremote.ApplyStatusUnknown, reportAfter.Status,
		"a cancelled build is 'nothing applied', never 'committed'")
	require.Equal(t, initialReport.AppliedRevision, reportAfter.AppliedRevision,
		"the aborted attempt must not advance the applied revision")
	require.Greater(t, reportAfter.RequestedRevision, initialReport.RequestedRevision,
		"the report must describe the attempt that was cancelled, not the earlier one")
	require.Equal(t, updatedAtInitial, remote.UpdatedAt(), "no publish after Close: the timestamp must not move")

	// The node-construction witness: the late document's node was never built, and the initial
	// document's node was built exactly once. A publish after Close would construct it.
	require.Zero(t, recorder.countMatching("late"),
		"the cancelled document's node was constructed: the build published after Close")
	require.Equal(t, constructedBefore, recorder.snapshot(),
		"no new node may be constructed by a build that was cancelled")

	// The build reservation's final state.
	finalGate := gate.Stats()
	require.EqualValues(t, 1, finalGate.Succeeded, "only the initial build succeeded")
	require.EqualValues(t, 1, finalGate.Abandoned, "the closed build is abandoned, not failed")
	require.Zero(t, finalGate.Failed, "a cancellation is not a provider failure")
	require.Zero(t, finalGate.Inflight, "Close must return the reservation")
	require.EqualValues(t, 2, finalGate.AdmittedBuilds)

	// No artifact, rule or reference mutation: the only thing this attempt could write is the
	// snapshot of nodes it published, and it published none.
	require.Equal(t, versionsBefore, crossSystemArtifactVersions(t, providerStoreRoot),
		"the aborted build changed the artifact store: a cancelled build must not leave a version")
	require.Equal(t, referencesBefore, instance.artifactRefs.Snapshot(),
		"the aborted build changed the exact-version record: a recovery would pin a version that "+
			"was never published")

	// A new service on the SAME artifact directory and the SAME subscription URL is the "later a
	// new Box starts cleanly" leg. Its context is a fresh product registry set with a fresh build
	// reservation, exactly what the daemon builds for the next profile.
	stallAfter.Store(false)
	secondRecorder := &crossSystemRecorder{}
	secondService := NewStartedService(ServiceOptions{Context: crossSystemContext(t, directory, secondRecorder)})
	t.Cleanup(func() {
		_ = secondService.CloseService()
		secondService.Close()
	})
	require.NoError(t, secondService.StartOrReloadService(context.Background(), crossSystemProviderProfile(server.URL), nil),
		"a new Box must start cleanly on the directory the aborted build shared")

	secondInstance := secondService.instance
	require.NotNil(t, secondInstance)
	secondProvider, loaded := secondInstance.providerManager.Get("nodes")
	require.True(t, loaded)
	secondRemote, isRemote := secondProvider.(*providerremote.ProviderRemote)
	require.True(t, isRemote)
	// The new Box may restore the document from the directory's persisted subscription cache
	// instead of downloading it - that is a legitimate clean start, and it is why this leg asserts
	// the published graph rather than a fetch count. A restore performs no build and therefore has
	// no report, so the report is asserted after the explicit refresh below.
	require.Eventually(t, func() bool {
		return len(providerNodesOf(t, secondProvider)) == 1
	}, 10*time.Second, 5*time.Millisecond, "the next Box never published the stored document")
	require.Equal(t, []string{"nodes/live"}, providerNodesOf(t, secondProvider))
	require.Positive(t, secondRecorder.countMatching("live"),
		"the next Box constructed the stored document's node")

	// A manual refresh from the new Box is a fresh BUILD against the same directory. It must
	// commit normally: the aborted build left no half-published state that would block it.
	secondSummary, err := secondService.RefreshProviders(context.Background(), []string{"nodes"}, 5*time.Second)
	require.NoError(t, err)
	require.Len(t, secondSummary.Results, 1)
	require.Equal(t, ProviderRefreshPublished, secondSummary.Results[0].Status,
		"the next Box's own build must commit: %+v", secondSummary.Results[0])
	secondReport := secondRemote.LastApplyReport()
	require.NotNil(t, secondReport)
	require.Equal(t, providerremote.ApplyStatusCommitted, secondReport.Status,
		"the next Box's own build must commit normally")
	require.Empty(t, secondReport.PendingTags)
	require.NotEmpty(t, secondInstance.artifactRefs.Snapshot(),
		"the next Box records the version it published")

	// The directory now holds exactly the snapshots the two successful builds produced (the same
	// document, so one content-addressed version), and no trace of the cancelled attempt.
	require.Len(t, crossSystemArtifactVersions(t, providerStoreRoot), 1)
}
