package rule

// D — a cancelled rule-set fetch returns its build reservation.
//
// A refresh that is cancelled mid-download (the caller stopped it, or the box is closing) must
// finish by releasing the shared build slot, not by leaving the reservation held. The next refresh
// then starts immediately instead of being deferred behind a build that no longer exists.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/buildgate"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"

	"github.com/stretchr/testify/require"
)

func newCancelTestRuleSet(ctx context.Context, tag string, sourceURL string) *RemoteRuleSet {
	ruleSet, err := NewRemoteRuleSet(ctx, log.NewNOPFactory().Logger(), tag, option.RuleSet{
		Type:   C.RuleSetTypeRemote,
		Format: C.RuleSetFormatBinary,
		RemoteOptions: option.RemoteRuleSet{
			URL: sourceURL,
		},
	})
	if err != nil {
		panic(err)
	}
	return ruleSet
}

func TestACancelledRuleSetRefreshReturnsItsBuildSlot(t *testing.T) {
	payload := writeRuleSetPayload(t, "10.0.0.0/8")
	started := make(chan struct{})
	unblock := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		_, _ = writer.Write(payload[:len(payload)/2])
		if flusher, isFlusher := writer.(http.Flusher); isFlusher {
			flusher.Flush()
		}
		select {
		case <-started:
		default:
			close(started)
		}
		select {
		case <-request.Context().Done():
		case <-unblock:
		}
	}))
	t.Cleanup(func() {
		close(unblock)
		server.Close()
	})

	gate := buildgate.New(1, 1, time.Second)
	ctx := buildgate.ContextWithGate(context.Background(), gate)
	ruleSet := newCancelTestRuleSet(ctx, "cancel-quota", server.URL)
	t.Cleanup(func() { _ = ruleSet.Close() })
	ruleSet.httpClient = server.Client()

	fetchContext, cancel := context.WithCancel(ctx)
	fetchResult := make(chan error, 1)
	go func() {
		fetchResult <- ruleSet.fetch(fetchContext, true)
	}()
	<-started
	require.Equal(t, 1, gate.Stats().Inflight, "the cancelled fetch owned the slot before it was cancelled")
	cancel()
	select {
	case fetchErr := <-fetchResult:
		require.Error(t, fetchErr)
	case <-time.After(5 * time.Second):
		t.Fatal("the cancelled fetch did not return")
	}
	require.Zero(t, gate.Stats().Inflight, "the cancelled fetch must release its build slot")
	require.Zero(t, gate.Stats().Waiters)

	// A new refresh, on a new set, is admitted instead of being deferred behind the cancelled
	// build; the same shared reservation is used.
	complete := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write(payload)
	}))
	t.Cleanup(complete.Close)
	next := newCancelTestRuleSet(ctx, "cancel-quota", complete.URL)
	t.Cleanup(func() { _ = next.Close() })
	next.httpClient = complete.Client()
	admission := make(chan error, 1)
	go func() { admission <- next.fetch(ctx, true) }()
	select {
	case err := <-admission:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the refresh after the cancellation was not admitted")
	}
	require.True(t, remoteRuleSetMatches(t, next, "10.1.2.3"), "the next refresh used the returned slot")
	require.Zero(t, gate.Stats().Inflight)
}
