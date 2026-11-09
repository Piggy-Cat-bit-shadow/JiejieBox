package rule

// F03 — the second build consumer: a remote rule-set refresh takes the box's build
// reservation, so it cannot run beside a provider refresh and double the peak.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/buildgate"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

func TestARuleSetBuildIsDeferredWhileAnotherHoldsTheReservation(t *testing.T) {
	payload := newRemoteRuleSetPayload(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = writer.Write(payload)
	}))
	t.Cleanup(server.Close)

	ruleSet, err := NewRemoteRuleSet(context.Background(), log.NewNOPFactory().Logger(), "build-gate-test", option.RuleSet{
		Type:   C.RuleSetTypeRemote,
		Format: C.RuleSetFormatBinary,
		RemoteOptions: option.RemoteRuleSet{
			URL: server.URL,
		},
	})
	require.NoError(t, err)
	ruleSet.httpClient = server.Client()

	gate := buildgate.New(1, 0, time.Millisecond)
	hold, err := gate.Acquire(context.Background())
	require.NoError(t, err)
	gateCtx := buildgate.ContextWithGate(context.Background(), gate)

	// The reservation is held: the refresh must be deferred BEFORE it downloads anything.
	fetchErr := ruleSet.fetch(gateCtx, true)
	require.ErrorIs(t, fetchErr, buildgate.ErrDeferred)
	require.Zero(t, requests.Load(), "a deferred build must not download the document")
	metadata := &adapter.InboundContext{Destination: M.ParseSocksaddr("10.1.2.3:443")}
	require.False(t, ruleSet.Match(metadata), "a deferred build must not publish rules")

	// Once the reservation is free, the same refresh goes through.
	hold()
	require.NoError(t, ruleSet.fetch(gateCtx, true))
	require.EqualValues(t, 1, requests.Load())
	require.True(t, ruleSet.Match(metadata), "the published rules are live")
}

func TestAnUngatedRuleSetFetchIsUnchanged(t *testing.T) {
	payload := newRemoteRuleSetPayload(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write(payload)
	}))
	t.Cleanup(server.Close)
	ruleSet, err := NewRemoteRuleSet(context.Background(), log.NewNOPFactory().Logger(), "ungated-test", option.RuleSet{
		Type:   C.RuleSetTypeRemote,
		Format: C.RuleSetFormatBinary,
		RemoteOptions: option.RemoteRuleSet{
			URL: server.URL,
		},
	})
	require.NoError(t, err)
	ruleSet.httpClient = server.Client()
	require.NoError(t, ruleSet.fetch(context.Background(), true), "a caller without a reservation is not gated")
	metadata := &adapter.InboundContext{Destination: M.ParseSocksaddr("10.1.2.3:443")}
	require.True(t, ruleSet.Match(metadata))
}
