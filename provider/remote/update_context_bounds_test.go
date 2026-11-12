package remote

// F — the three bounds a provider refresh must satisfy, each pinned where it failed before.
//
//  1. the Box's build gate still decides, even when the caller passes its own context. A manual
//     refresh that replaces the provider's context tree silently disables the gate: a quota-exhausted
//     Box sends the HTTP request anyway.
//  2. the provider's lifecycle still cancels. `Close` cancels the provider's context; a fetch that
//     does not descend from it keeps downloading until the caller gives up.
//  3. a cancellation that arrives while the body is being read must stop BEFORE the publish. Reading
//     the document is not committing it, and a publish after the caller left is an update nobody
//     asked for - reported as `committed` to a caller that had already gone.

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/buildgate"

	"github.com/stretchr/testify/require"
)

// TestAManualRefreshRespectsTheBoxBuildGate holds the Box's only build slot, then asks for a manual
// refresh. The gate refuses, so the provider must not have sent a request and must not report an
// attempt: "the build was deferred" is not "the provider failed", and it is certainly not "the
// document was refreshed".
func TestAManualRefreshRespectsTheBoxBuildGate(t *testing.T) {
	t.Parallel()
	document := `{"outbounds": [{"type": "test", "tag": "hk"}]}`
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = writer.Write([]byte(document))
	}))
	t.Cleanup(server.Close)

	provider, _ := newRemoteHarness(t, server.URL)
	provider.httpClient = server.Client()

	// The Box's gate, with its single slot already taken by another build.
	gate := buildgate.New(1, 0, 0)
	release, err := gate.Acquire(context.Background())
	require.NoError(t, err)
	defer release()
	provider.ctx = buildgate.ContextWithGate(provider.ctx, gate)

	report, err := provider.UpdateWithReportContext(context.Background())
	require.Error(t, err, "a deferred build is a refusal the caller can retry")
	require.Nil(t, report, "an attempt that never started has no report of its own")
	require.Zero(t, requests.Load(), "the gate must stop the HTTP request, not just the publish")
	require.Empty(t, providerTagsOf(provider), "nothing was published")
}

// TestClosingTheProviderCancelsAnInFlightFetch blocks a real server until it sees its own request
// context end, then closes the provider. The peer must observe the cancellation within a bounded
// wait - that is the difference between "the download honours the provider's lifecycle" and "the
// download runs until the caller's own deadline".
func TestClosingTheProviderCancelsAnInFlightFetch(t *testing.T) {
	t.Parallel()
	requestArrived := make(chan struct{})
	peerSawCancellation := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		close(requestArrived)
		<-request.Context().Done()
		close(peerSawCancellation)
	}))
	t.Cleanup(server.Close)

	provider, _ := newRemoteHarness(t, server.URL)
	provider.httpClient = server.Client()

	fetchDone := make(chan error, 1)
	go func() {
		_, err := provider.UpdateWithReportContext(context.Background())
		fetchDone <- err
	}()
	select {
	case <-requestArrived:
	case <-time.After(5 * time.Second):
		t.Fatal("the provider never sent its request")
	}
	require.NoError(t, provider.Close())

	select {
	case <-peerSawCancellation:
	case <-time.After(5 * time.Second):
		t.Fatal("the peer never saw its request cancelled; the fetch does not descend from the provider's lifecycle")
	}
	select {
	case err := <-fetchDone:
		require.Error(t, err, "the fetch reports the cancellation, not a completed download")
	case <-time.After(5 * time.Second):
		t.Fatal("the fetch did not return after the provider was closed")
	}
}

// cancellingEOFBody hands the whole document to the reader and then cancels the caller at the exact
// moment the body ends - the last instant before the publish.
type cancellingEOFBody struct {
	data   []byte
	cancel context.CancelFunc
}

func (b *cancellingEOFBody) Read(buffer []byte) (int, error) {
	if len(b.data) > 0 {
		read := copy(buffer, b.data)
		b.data = b.data[read:]
		return read, nil
	}
	b.cancel()
	return 0, io.EOF
}

func (b *cancellingEOFBody) Close() error { return nil }

type roundTripperFunc func(request *http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

// TestACancellationWhileReadingTheBodyDoesNotPublish is the publish boundary: the document was fully
// read, and the caller left before anything was installed. The previous build published here and
// returned `committed`; the node would then be live for a request whose answer nobody would read.
func TestACancellationWhileReadingTheBodyDoesNotPublish(t *testing.T) {
	t.Parallel()
	document := `{"outbounds": [{"type": "test", "tag": "hk"}]}`
	provider, _ := newRemoteHarness(t, "https://provider.invalid/subscription")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	provider.httpClient = &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       &cancellingEOFBody{data: []byte(document), cancel: cancel},
			Request:    request,
		}, nil
	})}

	report, err := provider.UpdateWithReportContext(ctx)
	require.Error(t, err, "the cancellation is reported instead of a completed refresh")
	require.EqualError(t, context.Canceled, context.Canceled.Error())
	require.ErrorIs(t, err, context.Canceled)
	require.NotNil(t, report, "the attempt ran, so its own report describes it")
	require.NotEqual(t, ApplyStatusCommitted, report.Status,
		"a document read but never installed must not be reported as committed")
	require.Empty(t, providerTagsOf(provider),
		"the node must not be published: reading is not committing, and the caller has left")
	require.False(t, report.Downloaded && len(report.AppliedTags) > 0,
		"nothing was applied, so nothing may be listed as applied")
	_ = bytes.MinRead
}

// TestACancelledProviderBuildIsCountedAsAbandonedNotFailed is the terminal-classification half of
// the same event: the build the test above proves does not publish is also a build whose outcome
// must not be reported as a FAILURE.
//
// The two owners of the Box's build budget classify the same fact, so they have to agree:
// a rule-set refresh a newer generation superseded returns its slot through `Release` and is
// counted as abandoned (`route/rule/rule_set_remote.go`), while a provider refresh the caller
// cancelled used to fall through to `Fail` because its report status is still `unknown`. The
// numbers the OOM-killer service exports as `failedBuilds` / `abandonedBuilds` then describe
// cancellations as failures, which is exactly the signal an operator would read as "the
// provider is failing" while it is only being cancelled.
func TestACancelledProviderBuildIsCountedAsAbandonedNotFailed(t *testing.T) {
	t.Parallel()
	document := `{"outbounds": [{"type": "test", "tag": "hk"}]}`
	provider, _ := newRemoteHarness(t, "https://provider.invalid/subscription")
	gate := buildgate.New(4, 4, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = buildgate.ContextWithGate(ctx, gate)
	provider.httpClient = &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       &cancellingEOFBody{data: []byte(document), cancel: cancel},
			Request:    request,
		}, nil
	})}

	report, err := provider.UpdateWithReportContext(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.NotNil(t, report)
	require.NotEqual(t, ApplyStatusCommitted, report.Status)

	stats := gate.Stats()
	require.EqualValues(t, 1, stats.AdmittedBuilds, "the build was admitted before it was cancelled")
	require.EqualValues(t, 1, stats.Abandoned,
		"a build the caller cancelled published nothing and is abandoned, not failed")
	require.EqualValues(t, 0, stats.Failed,
		"counting a cancellation as a failure reports the provider as broken while it was only cancelled")
}

// TestAFailedProviderBuildIsCountedAsFailed is the negative control for the classification above:
// a refresh that ends in an error still counts as a failure, so the rule did not become "every
// non-committed build is abandoned".
func TestAFailedProviderBuildIsCountedAsFailed(t *testing.T) {
	t.Parallel()
	provider, _ := newRemoteHarness(t, "https://provider.invalid/subscription")
	gate := buildgate.New(4, 4, 0)
	ctx := buildgate.ContextWithGate(context.Background(), gate)
	provider.httpClient = &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Header:     http.Header{},
			Body:       io.NopCloser(bytes.NewReader(nil)),
			Request:    request,
		}, nil
	})}

	report, err := provider.UpdateWithReportContext(ctx)
	require.Error(t, err, "an unexpected HTTP status is an error, not an abandoned build")
	require.NotNil(t, report)

	stats := gate.Stats()
	require.EqualValues(t, 1, stats.AdmittedBuilds)
	require.EqualValues(t, 1, stats.Failed, "a real error must still be counted as a failure")
	require.EqualValues(t, 0, stats.Abandoned, "an error is not an abandonment")
}
