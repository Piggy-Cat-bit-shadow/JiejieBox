package remote

// E1 — provider update × build not finished × Close, at the build reservation.
//
// # The scenario
//
// A provider refresh is a BUILD: it holds one slot of the Box-wide build reservation from before
// the HTTP request until the document is committed (or abandoned). The three-way interleaving this
// file drives is the one the closeout report lists as never executed:
//
//	provider refresh started -> the build is still in flight -> Close
//
// The ordering is formed with barriers, not sleeps: the test server receives the request and then
// stops sending the body, so the build is provably mid-download (the reservation is held and
// `Inflight == 1`) when Close runs. Close is the same call the Box makes on its providers during
// Box.Close/Instance.Close, and the assertions are the ones a reload must not violate:
//
//	no publish        the served document never reaches its nodes
//	reservation       the slot comes back as ABANDONED, not succeeded and not failed: the caller
//	                  left before anything was installed, and counting that as a provider error is
//	                  what would make an operator read a normal reload as a broken provider
//	no second attempt the closed provider refuses further work instead of racing the teardown
//	later clean start the same gate still admits a new build, so no permit was leaked
//
// The fixture is the real fetch path (provider/remote's own harness), because the classification
// under test lives in `fetchReport`'s deferred outcome and in `Close`'s cancellation order.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/buildgate"

	"github.com/stretchr/testify/require"
)

// stallingDocumentServer serves the document until `stallFrom`, and from that request number on it
// sends the response HEADERS and then stops. That is the exact position a build occupies after
// admission and before the commit: the reservation is held, the request has left the device, and
// the only way out is the caller's cancellation.
//
// The server never writes a partial body: a partial read would fail in the parser, which is a
// different outcome from the one this test is about (a build cancelled while the body is still
// arriving).
func stallingDocumentServer(t *testing.T, document string, stallFrom int32) (*httptest.Server, <-chan struct{}, *atomic.Int32) {
	t.Helper()
	stalled := make(chan struct{})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if requests.Add(1) < stallFrom {
			_, _ = writer.Write([]byte(document))
			return
		}
		writer.WriteHeader(http.StatusOK)
		writer.(http.Flusher).Flush()
		select {
		case <-stalled:
		default:
			close(stalled)
		}
		<-request.Context().Done()
	}))
	t.Cleanup(server.Close)
	return server, stalled, &requests
}

func TestACloseDuringABuildAbandonsTheReservationAndPublishesNothing(t *testing.T) {
	document := `{"outbounds": [{"type": "test", "tag": "hk"}]}`
	server, secondRequest, requests := stallingDocumentServer(t, document, 2)

	gate := buildgate.New(1, 4, 5*time.Second)
	provider, _ := newRemoteHarness(t, server.URL)
	provider.httpClient = server.Client()
	// The provider's own tree carries the Box's gate. Close cancels this context, and fetchContext
	// turns that cancellation into the in-flight fetch's cancellation - the same link the Box relies
	// on when its provider manager closes.
	provider.ctx = buildgate.ContextWithGate(provider.ctx, gate)

	// The first build is a normal one: it establishes that the gate genuinely ran, so the counters
	// below are a change rather than a constant.
	require.NoError(t, provider.fetch(provider.ctx, true))
	require.Equal(t, []string{"p/hk"}, providerTagsOf(provider))
	updatedAtInitial := provider.UpdatedAt()
	initialStats := gate.Stats()
	require.EqualValues(t, 1, initialStats.AdmittedBuilds)
	require.EqualValues(t, 1, initialStats.Succeeded)
	require.Zero(t, initialStats.Abandoned)

	// The second build is the one the test closes. It is a manual refresh so the call is owned by
	// this test rather than by a timer; the provider's own timer takes the identical path.
	type attempt struct {
		report *ApplyReport
		err    error
	}
	attemptDone := make(chan attempt, 1)
	go func() {
		report, err := provider.UpdateWithReportContext(context.Background())
		attemptDone <- attempt{report: report, err: err}
	}()

	select {
	case <-secondRequest:
	case <-time.After(10 * time.Second):
		t.Fatal("the refresh never reached the server")
	}
	// BARRIER: the build is admitted and mid-download. Nothing about this depends on how fast the
	// machine is: the server will not write the body until the request is cancelled.
	require.EqualValues(t, 2, requests.Load(), "the refresh must be the only new request")
	inflight := gate.Stats()
	require.EqualValues(t, 1, inflight.Inflight, "the reservation must be held by the build being closed")
	require.EqualValues(t, 2, inflight.AdmittedBuilds)

	// Close is the Box's teardown of this owner. It must cancel the in-flight build and join it
	// instead of waiting for a body the server will never send.
	closed := make(chan error, 1)
	go func() { closed <- provider.Close() }()
	select {
	case err := <-closed:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("Close waited for a download it should have cancelled")
	}

	var result attempt
	select {
	case result = <-attemptDone:
	case <-time.After(10 * time.Second):
		t.Fatal("the cancelled build never returned")
	}
	require.Error(t, result.err,
		"the caller's cancellation must be reported, not the server's silence")
	require.ErrorContains(t, result.err, context.Canceled.Error(),
		"the report must name the cancellation")
	require.NotNil(t, result.report, "the attempt ran, so it has a report of its own")
	require.Equal(t, ApplyStatusUnknown, result.report.Status,
		"a build that was cancelled before publishing must not be reported as committed")
	require.False(t, result.report.Downloaded, "the body was never fully read")
	require.Empty(t, result.report.AppliedTags)
	require.Empty(t, providerTagsOf(provider),
		"the provider's teardown released its published nodes and the cancelled build re-added none: "+
			"a publish after Close would have left a node behind")
	require.Equal(t, updatedAtInitial, provider.UpdatedAt(),
		"no publish after close means the update timestamp must not move either")

	// The reservation outcome is the third leg of the interleaving.
	after := gate.Stats()
	require.EqualValues(t, 1, after.Abandoned,
		"a build cancelled by Close is ABANDONED - it neither succeeded nor failed")
	require.EqualValues(t, 1, after.Succeeded, "only the initial build succeeded")
	require.Zero(t, after.Failed, "a cancellation is not a provider error")
	require.Zero(t, after.Inflight, "Close must return the slot, not leave the gate held")
	require.EqualValues(t, 2, after.AdmittedBuilds)

	// A closed provider must refuse further work rather than race its own teardown. The refusal is
	// checked to be a cancellation of the provider's lifecycle, not a network error.
	require.Error(t, provider.fetch(context.Background(), true),
		"a fetch on a closed provider must not be attempted")
	require.Empty(t, providerTagsOf(provider),
		"the refused fetch must not resurrect the released nodes")
	require.Equal(t, ApplyStatusUnknown, provider.LastApplyReport().Status,
		"the closed provider's last report is still the cancelled build, not a later attempt")

	// The same gate, a new server, a new provider: this is the "later a new Box starts cleanly"
	// leg. Reusing the SAME gate is deliberate - a leaked permit would defer this build.
	healthy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(document))
	}))
	t.Cleanup(healthy.Close)
	fresh, _ := newRemoteHarness(t, healthy.URL)
	fresh.httpClient = healthy.Client()
	fresh.ctx = buildgate.ContextWithGate(fresh.ctx, gate)
	require.NoError(t, fresh.fetch(fresh.ctx, true),
		"no permit may be leaked by the close: the reservation must admit the next build")
	require.Equal(t, []string{"p/hk"}, providerTagsOf(fresh))
	recovered := gate.Stats()
	require.EqualValues(t, 2, recovered.Succeeded)
	require.EqualValues(t, 1, recovered.Abandoned)
	require.Zero(t, recovered.Inflight)
}

func TestABuildDeferredByAnotherBuildIsRetryableOnceThatBuildIsClosed(t *testing.T) {
	document := `{"outbounds": [{"type": "test", "tag": "hk"}]}`
	holderServer, stalledRequest, _ := stallingDocumentServer(t, document, 1)

	var subjectRequests atomic.Int32
	subjectServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		subjectRequests.Add(1)
		_, _ = writer.Write([]byte(document))
	}))
	t.Cleanup(subjectServer.Close)

	// One slot: the holder's stalled build occupies it for the whole test.
	gate := buildgate.New(1, 1, time.Second)
	gateContext := buildgate.ContextWithGate(context.Background(), gate)

	holder, _ := newRemoteHarness(t, holderServer.URL)
	holder.httpClient = holderServer.Client()
	// Gated lifecycle, exactly like a Box's provider: Close cancels this context and the derived
	// refresh context with it.
	holder.ctx = buildgate.ContextWithGate(holder.ctx, gate)
	holderDone := make(chan struct{}, 1)
	go func() {
		_, _ = holder.UpdateWithReportContext(context.Background())
		holderDone <- struct{}{}
	}()
	select {
	case <-stalledRequest:
	case <-time.After(10 * time.Second):
		t.Fatal("the holding build never reached the server")
	}

	subject, _ := newRemoteHarness(t, subjectServer.URL)
	subject.httpClient = subjectServer.Client()
	err := subject.fetch(gateContext, true)
	require.ErrorIs(t, err, buildgate.ErrDeferred,
		"the second provider's build must be refused while the first holds the only slot")
	require.Zero(t, subjectRequests.Load(), "a deferred build must not download")
	require.Empty(t, providerTagsOf(subject), "a deferred build must not publish")

	// Close the holder while its build is still in flight. That is the three-way interleaving:
	// a second provider wants the reservation, the first build is unfinished, and the owner goes
	// away. The slot must come back as abandoned, which is what makes the deferred retry possible.
	require.NoError(t, holder.Close())
	select {
	case <-holderDone:
	case <-time.After(10 * time.Second):
		t.Fatal("the cancelled holding build never returned")
	}
	require.Empty(t, providerTagsOf(holder),
		"the cancelled holding build published nothing")
	holderStats := gate.Stats()
	require.EqualValues(t, 1, holderStats.Abandoned)
	require.Zero(t, holderStats.Inflight)

	// The deferred provider retries and now publishes. Its own retry is what the provider's timer
	// does on the next tick; no operator action is needed.
	require.NoError(t, subject.fetch(gateContext, true))
	require.Equal(t, []string{"p/hk"}, providerTagsOf(subject))
	require.EqualValues(t, 1, subjectRequests.Load())
	finalStats := gate.Stats()
	require.EqualValues(t, 2, finalStats.AdmittedBuilds)
	require.EqualValues(t, 1, finalStats.Succeeded)
	require.EqualValues(t, 1, finalStats.Deferred)
	require.Zero(t, finalStats.Inflight)
}
