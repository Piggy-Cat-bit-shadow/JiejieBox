package remote

// E — whose report is it?
//
// `UpdateWithReportContext` returns the report of the attempt it made, and the attempt's report is
// what a caller needs in order to tell "published, then the wait ended" from "never published".
// The subtle failure is the attempt that never started: the provider refuses it (its own timer is
// already refreshing it, or the build gate deferred it) and `LastApplyReport` still holds the
// PREVIOUS attempt - which may say "committed". Returning that with this attempt's error is how a
// publish from minutes ago gets reported as the result of a refresh that never happened.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAReportFromAnEarlierAttemptIsNeverReturnedWithThisAttemptsError drives the refusal the
// provider makes while its own timer holds it.
func TestAReportFromAnEarlierAttemptIsNeverReturnedWithThisAttemptsError(t *testing.T) {
	t.Parallel()
	document := `{"outbounds": [{"type": "test", "tag": "hk"}]}`
	server := serveDocument(t, &document)
	provider, _ := newRemoteHarness(t, server.URL)
	provider.httpClient = server.Client()

	first, err := provider.UpdateWithReportContext(context.Background())
	require.NoError(t, err)
	require.Equal(t, ApplyStatusCommitted, first.Status)
	require.NotNil(t, provider.LastApplyReport(), "the successful attempt is recorded")

	// The provider's own timer holds the fetch. `updating` is the flag the real fetch sets, so
	// holding it is exactly the state the manual entry gets refused in.
	provider.updating.Store(true)
	second, err := provider.UpdateWithReportContext(context.Background())
	require.Error(t, err, "a provider its own timer holds is refused")
	require.Nil(t, second, "the refusal must not hand back the successful attempt's report")

	// The stored report is still the first attempt: it is a fact about the provider, and the point
	// is that the caller was not given it as the answer to a different attempt.
	stored := provider.LastApplyReport()
	require.NotNil(t, stored)
	require.Equal(t, first.RequestedRevision, stored.RequestedRevision)
}

// TestTheRefreshedAttemptIsTheOneReported keeps the other direction: an attempt that ran returns its
// own report, with the revision counter advanced by exactly one.
func TestTheRefreshedAttemptIsTheOneReported(t *testing.T) {
	t.Parallel()
	document := `{"outbounds": [{"type": "test", "tag": "hk"}]}`
	server := serveDocument(t, &document)
	provider, _ := newRemoteHarness(t, server.URL)
	provider.httpClient = server.Client()

	first, err := provider.UpdateWithReportContext(context.Background())
	require.NoError(t, err)
	second, err := provider.UpdateWithReportContext(context.Background())
	require.NoError(t, err)
	require.NotNil(t, second)
	require.Equal(t, first.RequestedRevision+1, second.RequestedRevision)
	require.Equal(t, ApplyStatusCommitted, second.Status,
		"the served document is applied by this attempt, not inherited from the earlier one")
	require.Equal(t, second.RequestedRevision, second.AppliedRevision,
		"this attempt's publish carries this attempt's revision")
}

// TestACancelledFetchReportsItsOwnAttempt is the shape the manual entry depends on: the attempt ran,
// the fetch was cancelled by the caller's context, and the report describes the attempt that was
// cancelled rather than an older one.
func TestACancelledFetchReportsItsOwnAttempt(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if requests.Add(1) == 1 {
			_, _ = writer.Write([]byte(`{"outbounds": [{"type": "test", "tag": "hk"}]}`))
			return
		}
		// The second request never answers: the caller's deadline is what ends it.
		<-request.Context().Done()
	}))
	t.Cleanup(server.Close)
	provider, _ := newRemoteHarness(t, server.URL)
	provider.httpClient = server.Client()

	first, err := provider.UpdateWithReportContext(context.Background())
	require.NoError(t, err)
	require.Equal(t, ApplyStatusCommitted, first.Status)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report, err := provider.UpdateWithReportContext(ctx)
	require.Error(t, err)
	require.NotNil(t, report, "the attempt ran, so its report belongs to the caller")
	require.Greater(t, report.RequestedRevision, first.RequestedRevision,
		"the report names the cancelled attempt, not the earlier one")
}
