package rule

// 1A — an older refresh that finishes last cannot republish its rules, lease or digest.

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"

	"github.com/stretchr/testify/require"
)

func TestADelayedOlderNotificationCannotReplaceTheNewerGeneration(t *testing.T) {
	ctx := ruleSetArtifactContext(t)
	older := writeRuleSetPayload(t, "10.0.0.0/8")
	newer := writeRuleSetPayload(t, "192.168.0.0/16")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			_, _ = writer.Write(older)
			return
		}
		_, _ = writer.Write(newer)
	}))
	t.Cleanup(server.Close)

	rules := newStoredRemoteRuleSet(t, ctx, server.URL, "delayed-notification")
	rules.httpClient = server.Client()
	paused := make(chan struct{})
	resume := make(chan struct{})
	var callbacks atomic.Int32
	rules.RegisterCallback(func(adapter.RuleSet) {
		if callbacks.Add(1) == 1 {
			close(paused)
			<-resume
		}
	})

	first := make(chan error, 1)
	go func() { first <- rules.fetch(ctx, true) }()
	select {
	case <-paused:
	case err := <-first:
		close(resume)
		t.Fatalf("older refresh failed before its notification: %v", err)
	case <-time.After(5 * time.Second):
		close(resume)
		t.Fatal("older refresh did not reach its notification")
	}
	// The older generation is already committed; its notification is blocked. A second refresh
	// commits the newer generation completely.
	require.NoError(t, rules.fetch(ctx, true))
	close(resume)
	require.NoError(t, <-first)

	require.True(t, remoteRuleSetMatches(t, rules, "192.168.1.1"), "the newer rules are live")
	require.False(t, remoteRuleSetMatches(t, rules, "10.1.2.3"))
	require.Equal(t, ruleSetDigest(newer), rules.artifactLease.ID(),
		"the lease must still name the newer version after the delayed callback returns")
	if references := rules.referenceSet(); references != nil {
		recorded := references.Snapshot()
		require.Equal(t, ruleSetDigest(newer), recorded[rules.artifactKey],
			"the recorded digest must still be the newer version")
	}
}

func TestASuperseded304DoesNotOverwriteTheNewerETag(t *testing.T) {
	ctx := ruleSetArtifactContext(t)
	first := writeRuleSetPayload(t, "10.0.0.0/8")
	second := writeRuleSetPayload(t, "192.168.0.0/16")
	releaseStale := make(chan struct{})
	staleStarted := make(chan struct{})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch requests.Add(1) {
		case 1:
			writer.Header().Set("Etag", `"v1"`)
			_, _ = writer.Write(first)
		case 2:
			// The stale conditional request: its 304 arrives after the newer refresh committed.
			close(staleStarted)
			<-releaseStale
			writer.WriteHeader(http.StatusNotModified)
		default:
			writer.Header().Set("Etag", `"v2"`)
			_, _ = writer.Write(second)
		}
	}))
	t.Cleanup(server.Close)
	ruleSet := newStoredRemoteRuleSet(t, ctx, server.URL, "superseded-304")
	ruleSet.httpClient = server.Client()
	require.NoError(t, ruleSet.fetch(ctx, true))

	staleResult := make(chan error, 1)
	go func() { staleResult <- ruleSet.fetch(ctx, true) }()
	<-staleStarted
	require.NoError(t, ruleSet.fetch(ctx, true))
	close(releaseStale)
	require.NoError(t, <-staleResult)

	_, etag := ruleSet.updateMetadata()
	require.Equal(t, `"v2"`, etag, "a superseded 304 must not replace the newer ETag")
	require.True(t, remoteRuleSetMatches(t, ruleSet, "192.168.1.1"))
	require.Equal(t, ruleSetDigest(second), ruleSet.artifactLease.ID())
}

func TestClosingDuringADelayedNotificationDoesNotReinstallTheLease(t *testing.T) {
	ctx := ruleSetArtifactContext(t)
	payload := writeRuleSetPayload(t, "10.0.0.0/8")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write(payload)
	}))
	t.Cleanup(server.Close)

	rules := newStoredRemoteRuleSet(t, ctx, server.URL, "close-notification")
	rules.httpClient = server.Client()
	paused := make(chan struct{})
	release := make(chan struct{})
	rules.RegisterCallback(func(adapter.RuleSet) {
		close(paused)
		<-release
	})
	done := make(chan error, 1)
	go func() { done <- rules.fetch(ctx, true) }()
	<-paused
	require.NoError(t, rules.Close())
	close(release)
	require.NoError(t, <-done)
	require.Nil(t, rules.artifactLease, "a closed rule set must not hold a lease installed by the callback")
}
