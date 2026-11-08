package remote

// F03 — the build reservation, from the provider side.
//
// A provider refresh is a BUILD: it downloads a document, parses it, constructs outbounds and
// swaps them in, all while the previous version may still be serving traffic. The reservation
// bounds how many of those run at once, and its refusal must be a retryable deferral that
// leaves the old version exactly where it was.
//
// These cases drive the real fetch path against an httptest document, with the reservation
// carried on the context the way the box will carry it.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/buildgate"

	"github.com/stretchr/testify/require"
)

const buildDeferralDocument = `{"outbounds": [{"type": "test", "tag": "hk"}]}`

func TestABuildIsDeferredWhileAnotherHoldsTheReservation(t *testing.T) {
	t.Parallel()
	releaseFirst := make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(releaseFirst) })
	firstStarted := make(chan struct{}, 1)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if requests.Load() == 1 {
			firstStarted <- struct{}{}
			select {
			case <-releaseFirst:
			case <-request.Context().Done():
				return
			}
		}
		_, _ = writer.Write([]byte(buildDeferralDocument))
	}))
	t.Cleanup(server.Close)
	// Registered after Close so it runs FIRST: a blocked request must be released before the
	// test server waits for it.
	t.Cleanup(releaseOnce)

	gate := buildgate.New(1, 0, time.Millisecond)
	first, _ := newRemoteHarness(t, server.URL)
	first.httpClient = server.Client()
	second, _ := newRemoteHarness(t, server.URL)
	second.httpClient = server.Client()

	firstCtx := buildgate.ContextWithGate(context.Background(), gate)
	secondCtx := buildgate.ContextWithGate(context.Background(), gate)

	firstDone := make(chan error, 1)
	go func() { firstDone <- first.fetch(firstCtx, true) }()
	select {
	case <-firstStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("the first build never reached the server")
	}

	// The reservation is held by the first build, so the second must be refused BEFORE it
	// downloads anything: the server must not see a second request.
	err := second.fetch(secondCtx, true)
	require.ErrorIs(t, err, buildgate.ErrDeferred,
		"a build that cannot take the reservation must report a retryable deferral")
	require.EqualValues(t, 1, requests.Load(), "a deferred build must not download")
	require.Empty(t, providerTagsOf(second), "a deferred build must not publish anything")

	releaseOnce()
	require.NoError(t, <-firstDone)
	require.Equal(t, []string{"p/hk"}, providerTagsOf(first))
	require.EqualValues(t, 1, gate.Stats().AdmittedBuilds)
	require.EqualValues(t, 1, gate.Stats().Deferred)

	// Retry after the pressure is gone: the same provider now builds.
	require.NoError(t, second.fetch(secondCtx, true))
	require.Equal(t, []string{"p/hk"}, providerTagsOf(second))
	require.EqualValues(t, 2, gate.Stats().AdmittedBuilds)
}

func TestADeferredBuildLeavesTheOldVersionServing(t *testing.T) {
	t.Parallel()
	releaseFirst := make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(releaseFirst) })
	firstStarted := make(chan struct{}, 1)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if requests.Load() == 2 {
			firstStarted <- struct{}{}
			select {
			case <-releaseFirst:
			case <-request.Context().Done():
				return
			}
		}
		_, _ = writer.Write([]byte(buildDeferralDocument))
	}))
	t.Cleanup(server.Close)
	// Registered after Close so it runs FIRST: a blocked request must be released before the
	// test server waits for it.
	t.Cleanup(releaseOnce)

	gate := buildgate.New(1, 0, time.Millisecond)
	holder, _ := newRemoteHarness(t, server.URL)
	holder.httpClient = server.Client()
	serving, _ := newRemoteHarness(t, server.URL)
	serving.httpClient = server.Client()

	// The serving provider already published a version.
	require.NoError(t, serving.fetch(buildgate.ContextWithGate(context.Background(), gate), true))
	require.Equal(t, []string{"p/hk"}, providerTagsOf(serving))
	updatedBefore := serving.UpdatedAt()

	// Another provider takes the reservation, so the serving provider's next refresh defers.
	holderDone := make(chan error, 1)
	go func() { holderDone <- holder.fetch(buildgate.ContextWithGate(context.Background(), gate), true) }()
	select {
	case <-firstStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("the holding build never reached the server")
	}
	err := serving.fetch(buildgate.ContextWithGate(context.Background(), gate), true)
	require.ErrorIs(t, err, buildgate.ErrDeferred)
	require.Equal(t, []string{"p/hk"}, providerTagsOf(serving),
		"a deferred refresh must leave the version that is serving traffic in place")
	require.Equal(t, updatedBefore, serving.UpdatedAt(), "a deferred build did not update anything")

	releaseOnce()
	require.NoError(t, <-holderDone)
}

func TestWithoutAGateTheBuildIsUngated(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(buildDeferralDocument))
	}))
	t.Cleanup(server.Close)
	provider, _ := newRemoteHarness(t, server.URL)
	provider.httpClient = server.Client()
	require.NoError(t, provider.fetch(context.Background(), true))
	require.Equal(t, []string{"p/hk"}, providerTagsOf(provider))
}
