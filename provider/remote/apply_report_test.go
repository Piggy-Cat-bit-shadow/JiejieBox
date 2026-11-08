package remote

// C03 — the application report from the real refresh path.
//
// Each case drives the production fetch and reads the report it recorded, and then checks the
// report's claims against what is actually published: a report is only worth having if its
// per-tag lists agree with the provider's real members.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

func serveDocument(t *testing.T, document *string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(*document))
	}))
	t.Cleanup(server.Close)
	return server
}

func TestReportCommitsANewDocument(t *testing.T) {
	t.Parallel()
	document := `{"outbounds": [{"type": "test", "tag": "hk"}]}`
	server := serveDocument(t, &document)
	provider, _ := newRemoteHarness(t, server.URL)
	provider.httpClient = server.Client()

	report, err := provider.UpdateWithReport()
	require.NoError(t, err)
	require.NotNil(t, report)
	require.Equal(t, ApplyStatusCommitted, report.Status)
	require.True(t, report.Downloaded)
	require.EqualValues(t, 1, report.RequestedRevision)
	require.EqualValues(t, 1, report.AppliedRevision)
	require.Equal(t, []string{"p/hk"}, report.AppliedTags)
	require.Empty(t, report.PendingTags)
	require.False(t, report.GroupRejected)
	require.False(t, report.RestartRequired)
	require.Empty(t, report.AncillaryError)
	// The report's claim agrees with what is actually published.
	require.Equal(t, providerTagsOf(provider), report.AppliedTags)
}

func TestReportMarksAnUnchangedDocument(t *testing.T) {
	t.Parallel()
	document := `{"outbounds": [{"type": "test", "tag": "hk"}]}`
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if requests.Add(1) > 1 && request.Header.Get("If-None-Match") != "" {
			writer.WriteHeader(http.StatusNotModified)
			return
		}
		writer.Header().Set("Etag", `"v1"`)
		_, _ = writer.Write([]byte(document))
	}))
	t.Cleanup(server.Close)
	provider, _ := newRemoteHarness(t, server.URL)
	provider.httpClient = server.Client()

	first, err := provider.UpdateWithReport()
	require.NoError(t, err)
	require.Equal(t, ApplyStatusCommitted, first.Status)
	require.EqualValues(t, 1, first.AppliedRevision)

	second, err := provider.UpdateWithReport()
	require.NoError(t, err)
	require.Equal(t, ApplyStatusUnchanged, second.Status)
	require.False(t, second.Downloaded, "a 304 is unchanged, not downloaded")
	require.EqualValues(t, 2, second.RequestedRevision, "the attempt is counted")
	require.EqualValues(t, 1, second.AppliedRevision, "an unchanged refresh applies no new revision")
	require.Equal(t, []string{"p/hk"}, providerTagsOf(provider), "the published set is untouched")
}

func TestReportIsPartialWhenAGroupRefusesTheDocument(t *testing.T) {
	t.Parallel()
	document := `{"outbounds": [{"type": "test", "tag": "hk"}]}`
	server := serveDocument(t, &document)
	provider, _ := newRemoteHarness(t, server.URL)
	provider.httpClient = server.Client()
	provider.RegisterCallback(func(string) error { return errors.New("group refused the membership") })

	report, err := provider.UpdateWithReport()
	require.Error(t, err)
	require.NotNil(t, report)
	require.Equal(t, ApplyStatusPartial, report.Status)
	require.True(t, report.GroupRejected)
	require.True(t, report.RestartRequired,
		"a refused membership is not fixed by a retry, and the report must say so")
	require.Equal(t, []string{"p/hk"}, report.AppliedTags, "the nodes are installed even though the group refused")
	require.Equal(t, []string{"p/hk"}, providerTagsOf(provider))
	require.NotEmpty(t, report.Diagnostics)
}

func TestReportNamesPendingMembersWhenANodeCannotBeBuilt(t *testing.T) {
	t.Parallel()
	document := `{"outbounds": [{"type": "test", "tag": "hk"}, {"type": "unbuildable", "tag": "bad"}]}`
	server := serveDocument(t, &document)
	provider, _ := newRemoteHarness(t, server.URL)
	provider.httpClient = server.Client()

	// Register a node type whose construction fails, through the same registry the decoder uses.
	registry, isRegistry := service.FromContext[option.OutboundOptionsRegistry](provider.ctx).(*outbound.Registry)
	require.True(t, isRegistry)
	outbound.Register[option.StubOptions](registry, "unbuildable", func(context.Context, adapter.Router, log.ContextLogger, string, option.StubOptions) (adapter.Outbound, error) {
		return nil, errors.New("cannot construct this node")
	})

	report, err := provider.UpdateWithReport()
	require.Error(t, err)
	require.NotNil(t, report)
	require.Equal(t, ApplyStatusPartial, report.Status)
	require.Equal(t, []string{"p/hk"}, report.AppliedTags)
	require.Equal(t, []string{"p/bad"}, report.PendingTags,
		"a member the document asked for that is not published must be named, not counted")
	require.Equal(t, []string{"p/hk"}, providerTagsOf(provider))
	require.NotEmpty(t, report.Diagnostics)
}

func TestReportExpressesAPostPublishCacheFailureSeparately(t *testing.T) {
	t.Parallel()
	document := `{"outbounds": [{"type": "test", "tag": "hk"}]}`
	server := serveDocument(t, &document)
	provider, _ := newRemoteHarness(t, server.URL)
	provider.httpClient = server.Client()

	// The cache path's parent is a FILE, so the directory cannot be created and the cache write
	// fails AFTER the nodes are published. That is the ancillary case: live update, failed cache.
	file, err := os.CreateTemp(t.TempDir(), "blocker")
	require.NoError(t, err)
	require.NoError(t, file.Close())
	provider.path = file.Name() + "/cache.json"

	report, updateErr := provider.UpdateWithReport()
	require.Error(t, updateErr, "the old error contract still reports the failure")
	require.NotNil(t, report)
	require.Equal(t, ApplyStatusCommitted, report.Status,
		"the nodes are live, so the attempt is not a failed apply")
	require.NotEmpty(t, report.AncillaryError, "the cache failure must be expressed separately")
	require.Equal(t, []string{"p/hk"}, report.AppliedTags)
	require.Equal(t, []string{"p/hk"}, providerTagsOf(provider))
}

func TestUpdateStillReturnsOnlyTheError(t *testing.T) {
	t.Parallel()
	document := `{"outbounds": [{"type": "test", "tag": "hk"}]}`
	server := serveDocument(t, &document)
	provider, _ := newRemoteHarness(t, server.URL)
	provider.httpClient = server.Client()

	require.NoError(t, provider.Update())
	report := provider.LastApplyReport()
	require.NotNil(t, report)
	require.Equal(t, ApplyStatusCommitted, report.Status)
	// The returned report is a copy: mutating it cannot change what the provider recorded.
	report.Status = ApplyStatusUnknown
	require.Equal(t, ApplyStatusCommitted, provider.LastApplyReport().Status)
}
