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

// TestADeferredRefreshKeepsServingThePublishedVersion pins the rule-set half of "an old version
// stays usable". A refresh that is deferred before it touches the network must leave the version
// that is already serving traffic exactly as it was: no partial publication, no lost rules.
func TestADeferredRefreshKeepsServingThePublishedVersion(t *testing.T) {
	payload := newRemoteRuleSetPayload(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = writer.Write(payload)
	}))
	t.Cleanup(server.Close)

	ruleSet, err := NewRemoteRuleSet(context.Background(), log.NewNOPFactory().Logger(), "old-version-test", option.RuleSet{
		Type:   C.RuleSetTypeRemote,
		Format: C.RuleSetFormatBinary,
		RemoteOptions: option.RemoteRuleSet{
			URL: server.URL,
		},
	})
	require.NoError(t, err)
	ruleSet.httpClient = server.Client()
	gate := buildgate.New(1, 0, time.Millisecond)
	gateCtx := buildgate.ContextWithGate(context.Background(), gate)

	// First refresh publishes a version that matches.
	require.NoError(t, ruleSet.fetch(gateCtx, true))
	metadata := &adapter.InboundContext{Destination: M.ParseSocksaddr("10.1.2.3:443")}
	require.True(t, ruleSet.Match(metadata))
	published := requests.Load()

	// A second refresh is deferred while another build holds the reservation.
	hold, err := gate.Acquire(context.Background())
	require.NoError(t, err)
	require.ErrorIs(t, ruleSet.fetch(gateCtx, false), buildgate.ErrDeferred)
	require.Equal(t, published, requests.Load(), "a deferred refresh must not download")
	require.True(t, ruleSet.Match(metadata), "the published version keeps serving traffic")

	// The slot comes back and the same refresh goes through.
	hold()
	require.NoError(t, ruleSet.fetch(gateCtx, false))
	require.Equal(t, published+1, requests.Load())
	require.True(t, ruleSet.Match(metadata))
}

// TestAFailedBuildReleasesTheReservation pins that the slot is released on the failing paths too.
// With a limit of one, a leaked reservation would refuse every later build in the box.
func TestAFailedBuildReleasesTheReservation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	ruleSet, err := NewRemoteRuleSet(context.Background(), log.NewNOPFactory().Logger(), "failed-build-test", option.RuleSet{
		Type:   C.RuleSetTypeRemote,
		Format: C.RuleSetFormatBinary,
		RemoteOptions: option.RemoteRuleSet{
			URL: server.URL,
		},
	})
	require.NoError(t, err)
	ruleSet.httpClient = server.Client()
	gate := buildgate.New(1, 0, time.Millisecond)
	gateCtx := buildgate.ContextWithGate(context.Background(), gate)

	fetchErr := ruleSet.fetch(gateCtx, true)
	require.Error(t, fetchErr)
	require.NotErrorIs(t, fetchErr, buildgate.ErrDeferred, "the build ran; it was the download that failed")

	release, acquireErr := gate.Acquire(context.Background())
	require.NoError(t, acquireErr, "a failed build must return the reservation")
	release()
}
