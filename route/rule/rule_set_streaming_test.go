package rule

// 2 — the streaming download path with the artifact store configured.
//
// The body is written to a bounded temp artifact while it is hashed; the parser reads that file;
// only a validated candidate is published. These tests pin the memory property (the response is
// not buffered as a whole), the size limit, truncation/bad-tail rejection, cancellation cleanup,
// and that a failed download leaves the previous generation live.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing/service/filemanager"

	"github.com/stretchr/testify/require"
)

func ruleSetArtifactRoot(t *testing.T, ctx context.Context) string {
	t.Helper()
	return filemanager.BasePath(ctx, "rule-set-artifacts")
}

func countTempArtifactDirs(t *testing.T, root string) int {
	t.Helper()
	count := 0
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err == nil && entry.IsDir() && strings.HasPrefix(entry.Name(), ".tmp-") {
			count++
		}
		return nil
	})
	return count
}

func TestStreamedStoreDownloadDoesNotBufferTheWholeBody(t *testing.T) {
	ctx := ruleSetArtifactContext(t)
	payload := writeRuleSetPayload(t, "10.0.0.0/8")
	const tailSize = 4 << 20
	tail := make([]byte, tailSize)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Length", strconv.Itoa(len(payload)+len(tail)))
		_, _ = writer.Write(payload)
		_, _ = writer.Write(tail)
	}))
	t.Cleanup(server.Close)

	ruleSet := newStoredRemoteRuleSet(t, ctx, server.URL, "store-stream")
	ruleSet.httpClient = server.Client()

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	require.NoError(t, ruleSet.fetch(ctx, true))
	runtime.ReadMemStats(&after)
	allocated := after.TotalAlloc - before.TotalAlloc
	t.Logf("stored stream load with a %d-byte tail allocated %d bytes", tailSize, allocated)
	require.Less(t, allocated, uint64(tailSize/2),
		"the %d-byte tail must not be retained as a whole response slice", tailSize)
	require.True(t, remoteRuleSetMatches(t, ruleSet, "10.1.2.3"))
	require.NotNil(t, ruleSet.artifactLease)
	require.Zero(t, countTempArtifactDirs(t, ruleSetArtifactRoot(t, ctx)), "the staged temp must be gone after publish")
}

func TestAnOversizedStreamIsRejectedAndLeavesNoVersion(t *testing.T) {
	ctx := ruleSetArtifactContext(t)
	previousLimit := maxRemoteRuleSetSize
	maxRemoteRuleSetSize = 1024
	t.Cleanup(func() { maxRemoteRuleSetSize = previousLimit })

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write(make([]byte, 8192))
	}))
	t.Cleanup(server.Close)
	ruleSet := newStoredRemoteRuleSet(t, ctx, server.URL, "store-oversize")
	ruleSet.httpClient = server.Client()

	err := ruleSet.fetch(ctx, true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds")
	_, loadErr := ruleSet.artifactStore.Load(ruleSet.artifactKey)
	require.ErrorIs(t, loadErr, os.ErrNotExist)
	require.Zero(t, countTempArtifactDirs(t, ruleSetArtifactRoot(t, ctx)))
}

func TestATruncatedStoredDownloadKeepsThePublishedGeneration(t *testing.T) {
	ctx := ruleSetArtifactContext(t)
	good := writeRuleSetPayload(t, "10.0.0.0/8")
	next := writeRuleSetPayload(t, "192.168.0.0/16")
	var short atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if short.Load() {
			writer.Header().Set("Content-Length", strconv.Itoa(len(next)+1024))
			_, _ = writer.Write(next)
			return
		}
		writer.Header().Set("Content-Length", strconv.Itoa(len(good)))
		_, _ = writer.Write(good)
	}))
	t.Cleanup(server.Close)
	ruleSet := newStoredRemoteRuleSet(t, ctx, server.URL, "store-truncated")
	ruleSet.httpClient = server.Client()
	require.NoError(t, ruleSet.fetch(ctx, true))
	published := ruleSet.artifactLease.ID()

	short.Store(true)
	require.Error(t, ruleSet.fetch(ctx, true))
	require.True(t, remoteRuleSetMatches(t, ruleSet, "10.1.2.3"), "the old rules keep serving")
	require.False(t, remoteRuleSetMatches(t, ruleSet, "192.168.1.1"))
	require.Equal(t, published, ruleSet.artifactLease.ID(), "the old lease is still the live one")
	require.Zero(t, countTempArtifactDirs(t, ruleSetArtifactRoot(t, ctx)))
}

func TestABadCompressionTailLeavesThePreviousVersionLive(t *testing.T) {
	ctx := ruleSetArtifactContext(t)
	good := writeRuleSetPayload(t, "10.0.0.0/8")
	corrupt := append(writeRuleSetPayload(t, "192.168.0.0/16")[:8], 0xFF, 0xFF)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/bad" {
			_, _ = writer.Write(corrupt)
			return
		}
		_, _ = writer.Write(good)
	}))
	t.Cleanup(server.Close)
	ruleSet := newStoredRemoteRuleSet(t, ctx, server.URL+"/good", "store-bad-tail")
	ruleSet.httpClient = server.Client()
	require.NoError(t, ruleSet.fetch(ctx, true))
	published := ruleSet.artifactLease.ID()

	ruleSet.url = server.URL + "/bad"
	require.Error(t, ruleSet.fetch(ctx, true))
	require.True(t, remoteRuleSetMatches(t, ruleSet, "10.1.2.3"))
	require.Equal(t, published, ruleSet.artifactLease.ID())
	require.Zero(t, countTempArtifactDirs(t, ruleSetArtifactRoot(t, ctx)))
}

func TestACancelledStoredDownloadLeavesNoTempOrVersion(t *testing.T) {
	ctx := ruleSetArtifactContext(t)
	payload := writeRuleSetPayload(t, "10.0.0.0/8")
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		_, _ = writer.Write(payload[:len(payload)/2])
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
		close(started)
		<-request.Context().Done()
	}))
	t.Cleanup(server.Close)
	ruleSet := newStoredRemoteRuleSet(t, ctx, server.URL, "store-cancel")
	ruleSet.httpClient = server.Client()

	fetchContext, cancel := context.WithCancel(ctx)
	result := make(chan error, 1)
	go func() { result <- ruleSet.fetch(fetchContext, true) }()
	<-started
	cancel()
	select {
	case err := <-result:
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the cancelled stored download did not return")
	}
	_, loadErr := ruleSet.artifactStore.Load(ruleSet.artifactKey)
	require.ErrorIs(t, loadErr, os.ErrNotExist)
	require.Zero(t, countTempArtifactDirs(t, ruleSetArtifactRoot(t, ctx)))
}
