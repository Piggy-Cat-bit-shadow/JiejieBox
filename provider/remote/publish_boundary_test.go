package remote

// F-2 - the publish boundary, one phase later than the body read.
//
// The end-of-body check covers a caller that leaves while the document is being DOWNLOADED. This one
// covers the caller that leaves while it is being PARSED: decoding a subscription runs user-registered
// option types, and one of them can take long enough (or cancel, as this fixture does) between the
// read and the first irreversible step.
//
// The defect it pins: the parse ran on the provider's lifecycle context, so a cancellation arriving
// during it was invisible, the whole document was published and the report said `committed` with a nil
// error. Now the caller's context is checked between the pure phase (parse, decode, filter) and the
// publish (`UpdateNodes`).

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// cancelOnDecodeOptions cancels whatever context the current test registered, at the exact moment the
// document is being decoded.
//
// The cancel is carried in an atomic value rather than a package variable so the fixture stays correct
// under `-race` with parallel tests: two tests can hold different cancels without sharing a write.
type cancelOnDecodeOptions struct{}

var cancelOnDecode atomic.Value

func (*cancelOnDecodeOptions) UnmarshalJSON([]byte) error {
	if cancel, ok := cancelOnDecode.Load().(context.CancelFunc); ok {
		cancel()
	}
	return nil
}

// TestACancellationDuringParsingStopsBeforeThePublish registers an option type whose decoder cancels
// the caller, then asks for a refresh. The document must not be published, and the report must say the
// attempt ended without applying anything.
func TestACancellationDuringParsingStopsBeforeThePublish(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancelOnDecode.Store(context.CancelFunc(cancel))
	t.Cleanup(func() { cancelOnDecode.Store(context.CancelFunc(nil)) })

	document := `{"outbounds": [{"type": "cancel-on-decode", "tag": "late"}]}`
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(document))
	}))
	t.Cleanup(server.Close)
	provider, _ := newRemoteHarness(t, server.URL)
	provider.httpClient = server.Client()
	registry := service.FromContext[adapter.OutboundRegistry](provider.ctx).(*outbound.Registry)
	outbound.Register[cancelOnDecodeOptions](registry, "cancel-on-decode",
		func(_ context.Context, _ adapter.Router, _ log.ContextLogger, tag string, _ cancelOnDecodeOptions) (adapter.Outbound, error) {
			return &remoteTestNode{Adapter: outbound.NewAdapter("cancel-on-decode", tag, []string{"tcp"}, nil)}, nil
		})

	report, err := provider.UpdateWithReportContext(ctx)
	require.ErrorIs(t, err, context.Canceled,
		"the caller's cancellation is reported, not swallowed by the parse")
	require.NotNil(t, report, "the attempt ran, so it has a report of its own")
	require.Equal(t, ApplyStatusUnknown, report.Status,
		"a document parsed but never installed must not be reported as committed")
	require.Empty(t, report.AppliedTags)
	require.True(t, report.Downloaded, "the body WAS downloaded; that part of the attempt is real")
	require.Empty(t, providerTagsOf(provider),
		"nothing may be published: the cancellation arrived before the first irreversible step")
}
