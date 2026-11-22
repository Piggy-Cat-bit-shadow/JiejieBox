//go:build unix

package rule

// Warm-path smoke tests for the remote derived cache: a real RemoteRuleSet load through the
// product's StartContext recovery and through the HTTP 200 staging path must hit the persisted
// conversion of the exact source bytes and skip both the source decode and the SRSM write. The
// comprehensive F/E/C matrix lives in the test file owned by the test agent; this file is the
// author's minimal proof that the integration is wired into the real paths.

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/filemanager"

	"github.com/stretchr/testify/require"
)

// remoteTestTransport serves one payload for every request; it is the seam StartContext resolves.
type remoteTestTransport struct {
	hits    atomic.Int32
	payload []byte
}

func (t *remoteTestTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	t.hits.Add(1)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(t.payload)),
	}, nil
}

func (t *remoteTestTransport) CloseIdleConnections() {}
func (t *remoteTestTransport) Reset()                {}

type remoteTestHTTPClientManager struct {
	adapter.HTTPClientManager
	transport adapter.HTTPTransport
}

func (m *remoteTestHTTPClientManager) DefaultTransport() adapter.HTTPTransport { return m.transport }

// remoteStartContext builds a context that can drive the real StartContext (a registered HTTP client
// manager is the only service transport resolution needs) with a private file base for the stores.
func remoteStartContext(t *testing.T, payload []byte) (context.Context, *remoteTestTransport) {
	t.Helper()
	directory := t.TempDir()
	ctx := service.ContextWithDefaultRegistry(context.Background())
	ctx = filemanager.WithDefault(ctx, directory, directory, -1, -1)
	transport := &remoteTestTransport{payload: payload}
	service.MustRegister[adapter.HTTPClientManager](ctx, &remoteTestHTTPClientManager{transport: transport})
	return ctx, transport
}

func newRemoteRuleSetForStart(t *testing.T, ctx context.Context, sourceURL string, tag string) *RemoteRuleSet {
	t.Helper()
	ruleSet, err := NewRemoteRuleSet(ctx, log.NewNOPFactory().Logger(), tag, option.RuleSet{
		Type:          constant.RuleSetTypeRemote,
		Format:        constant.RuleSetFormatBinary,
		RemoteOptions: option.RemoteRuleSet{URL: sourceURL},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = ruleSet.Close() })
	return ruleSet
}

// TestRemoteDerivedStartContextWarmRestart is F01 through the real product entry: the first Box
// starts with an empty store (download + convert), the second starts from the stored version and
// must build from the derived mapping. This is the path a restart actually takes.
func TestRemoteDerivedStartContextWarmRestart(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	payload := writeRuleSetPayload(t, "10.0.0.0/8")
	ctx, transport := remoteStartContext(t, payload)

	first := newRemoteRuleSetForStart(t, ctx, "https://rules.example/set.srs", "remote-start-warm")
	resetRemoteDerivedDiagnostics()
	require.NoError(t, first.StartContext(ctx, adapter.NewHTTPStartContext()))
	require.EqualValues(t, 1, transport.hits.Load(), "the cold start downloads")
	require.True(t, remoteRuleSetMatches(t, first, "10.1.2.3"))
	require.Equal(t, ruleSetDigest(payload), first.artifactLease.ID())
	hits, misses, decodes, writes, heapBuilds := remoteDerivedDiagnostics()
	require.Zero(t, hits)
	require.EqualValues(t, 1, misses)
	require.EqualValues(t, 1, decodes)
	require.EqualValues(t, 1, writes)
	require.Zero(t, heapBuilds)
	require.NoError(t, first.Close())

	// A new Box against the same base is the restart: StartContext restores the stored version and
	// builds from its conversion without a request, a decode or an SRSM write.
	second := newRemoteRuleSetForStart(t, ctx, "https://rules.example/set.srs", "remote-start-warm")
	resetRemoteDerivedDiagnostics()
	require.NoError(t, second.StartContext(ctx, adapter.NewHTTPStartContext()))
	require.EqualValues(t, 1, transport.hits.Load(), "a warm restore must not refetch")
	require.True(t, remoteRuleSetMatches(t, second, "10.1.2.3"))
	require.Equal(t, ruleSetDigest(payload), second.artifactLease.ID())
	hits, misses, decodes, writes, heapBuilds = remoteDerivedDiagnostics()
	require.EqualValues(t, 1, hits)
	require.Zero(t, misses)
	require.Zero(t, decodes, "a derived hit must not parse the source")
	require.Zero(t, writes, "a derived hit must not write an SRSM")
	require.Zero(t, heapBuilds)
	require.True(t, second.Metadata().ContainsIPCIDRRule, "metadata is rebuilt for the new Box")
	require.NoError(t, second.Close())
}

// TestRemoteDerivedHTTP200ReusesTheSameContent is F03: a second 200 carrying the same bytes hits the
// conversion published by the first 200 and still republishes the source version (the download is
// never trusted less because a conversion exists).
func TestRemoteDerivedHTTP200ReusesTheSameContent(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	ctx := ruleSetArtifactContext(t)
	var payload atomic.Value
	payload.Store(writeRuleSetPayload(t, "10.0.0.0/8"))
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write(payload.Load().([]byte))
	}))
	t.Cleanup(server.Close)

	ruleSet := newStoredRemoteRuleSet(t, ctx, server.URL, "remote-derived-200")
	ruleSet.httpClient = server.Client()
	resetRemoteDerivedDiagnostics()
	require.NoError(t, ruleSet.fetch(ctx, true))
	require.True(t, remoteRuleSetMatches(t, ruleSet, "10.1.2.3"))
	firstID := ruleSet.artifactLease.ID()

	require.NoError(t, ruleSet.fetch(ctx, true))
	hits, misses, decodes, writes, _ := remoteDerivedDiagnostics()
	require.EqualValues(t, 1, hits, "the unchanged content must hit its conversion")
	require.EqualValues(t, 1, misses)
	require.EqualValues(t, 1, decodes, "only the cold fetch parses")
	require.EqualValues(t, 1, writes)
	require.True(t, remoteRuleSetMatches(t, ruleSet, "10.1.2.3"))
	require.Equal(t, firstID, ruleSet.artifactLease.ID(), "the same bytes keep the same source version")

	// Content changes: the new bytes miss, are parsed and served, and their conversion is published.
	payload.Store(writeRuleSetPayload(t, "192.168.0.0/16"))
	require.NoError(t, ruleSet.fetch(ctx, true))
	require.True(t, remoteRuleSetMatches(t, ruleSet, "192.168.1.1"))
	hits, misses, decodes, writes, _ = remoteDerivedDiagnostics()
	require.EqualValues(t, 1, hits)
	require.EqualValues(t, 2, misses)
	require.EqualValues(t, 2, decodes)
	require.EqualValues(t, 2, writes)

	// Fetching the changed content again hits the conversion published for it.
	require.NoError(t, ruleSet.fetch(ctx, true))
	hits, misses, decodes, writes, _ = remoteDerivedDiagnostics()
	require.EqualValues(t, 2, hits)
	require.EqualValues(t, 2, misses)
	require.EqualValues(t, 2, decodes)
	require.EqualValues(t, 2, writes)
}

// TestRemoteDerivedE09StagedRewriteWindow is the HTTP 200 half of E09: the body Stage verified is
// rewritten before the loader parses (miss) or publishes (hit) it. Neither B's rules nor B's bytes may
// be published under A's digest, and the live A generation must keep serving.
func TestRemoteDerivedE09StagedRewriteWindow(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	ctx, base := derivedTestContext(t)
	payloadA := writeRuleSetPayload(t, "10.0.0.0/8")
	payloadB := writeRuleSetPayload(t, "192.168.0.0/16")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write(payloadA)
	}))
	t.Cleanup(server.Close)

	ruleSet := newStoredRemoteRuleSet(t, ctx, server.URL, "remote-e09-staged")
	ruleSet.httpClient = server.Client()
	require.NoError(t, ruleSet.fetch(ctx, true))
	require.True(t, remoteRuleSetMatches(t, ruleSet, "10.1.2.3"))
	require.Equal(t, ruleSetDigest(payloadA), ruleSet.artifactLease.ID())

	// MISS arm: no conversion exists, so the staged bytes would be parsed and converted directly.
	remoteDerivedTestRemoveDerivedVersions(t, base)
	var rewrites atomic.Int32
	remoteStagedTestHook = func(path string) {
		if rewrites.Add(1) == 1 {
			require.NoError(t, os.WriteFile(path, payloadB, 0o600))
		}
	}
	t.Cleanup(func() { remoteStagedTestHook = nil })
	resetRemoteDerivedDiagnostics()
	require.Error(t, ruleSet.fetch(ctx, true), "a staged rewrite must fail, not publish B under A")
	require.True(t, remoteRuleSetMatches(t, ruleSet, "10.1.2.3"), "the live A generation keeps serving")
	require.False(t, remoteRuleSetMatches(t, ruleSet, "192.168.1.1"))
	require.Equal(t, ruleSetDigest(payloadA), ruleSet.artifactLease.ID())
	hits, misses, decodes, writes, _ := remoteDerivedDiagnostics()
	require.EqualValues(t, 1, misses)
	require.EqualValues(t, 1, decodes, "the parsed bytes are counted before the mismatch is refused")
	require.Zero(t, writes, "nothing may be converted or published from rewritten bytes")
	require.Zero(t, hits)
	require.Empty(t, remoteDerivedTestStoreDerivedVersions(t, base), "no conversion may be published")
	require.Len(t, remoteDerivedTestStoreSourceVersions(t, base), 1, "no source version may be added")

	// The same fetch without the rewrite succeeds and publishes the conversion of A.
	remoteStagedTestHook = nil
	require.NoError(t, ruleSet.fetch(ctx, true))
	require.True(t, remoteRuleSetMatches(t, ruleSet, "10.1.2.3"))

	// HIT arm: the conversion exists, so the loader would only publish the source artifact; a rewrite
	// must still be caught before that publication.
	remoteStagedTestHook = func(path string) {
		require.NoError(t, os.WriteFile(path, payloadB, 0o600))
	}
	require.Error(t, ruleSet.fetch(ctx, true), "a staged rewrite must fail on the hit path too")
	require.True(t, remoteRuleSetMatches(t, ruleSet, "10.1.2.3"))
	require.Equal(t, ruleSetDigest(payloadA), ruleSet.artifactLease.ID())
	require.Len(t, remoteDerivedTestStoreSourceVersions(t, base), 1, "the rewritten bytes must not be published")
}
