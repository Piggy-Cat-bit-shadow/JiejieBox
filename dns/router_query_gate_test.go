package dns

import (
	"context"
	"errors"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/resourceguard"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"

	mDNS "github.com/miekg/dns"

	"github.com/stretchr/testify/require"
)

// The router must bound how many DNS exchanges it runs.
//
// # The gap these tests were written against
//
// The hijacked-DNS entry (route/dns.go) has a 256-in-flight cap, but every other query path - rule
// resolution, sniffing, provider lookups, dual-stack family probes - reaches the client without one.
// The client spawns per-exchange work (a dual-stack lookup is two exchanges), so a flow burst
// multiplies into an unbounded number of query goroutines and per-query state.
//
// The gate is a goroutine/queue bound, not a pressure policy: DNS is how a flow is recovered, so it
// must not be throttled harder merely because the process is under memory pressure.

type gateStubTransport struct{}

func (gateStubTransport) Type() string           { return "stub" }
func (gateStubTransport) Tag() string            { return "stub" }
func (gateStubTransport) Dependencies() []string { return nil }
func (gateStubTransport) Reset()                 {}
func (gateStubTransport) Start(adapter.StartStage, *adapter.Scope) error {
	return nil
}
func (gateStubTransport) Exchange(context.Context, *mDNS.Msg) (*mDNS.Msg, error) {
	return nil, errors.New("stub transport")
}
func (gateStubTransport) ExchangeAsync(_ context.Context, _ *mDNS.Msg, callback func(*mDNS.Msg, error)) {
	callback(nil, errors.New("stub transport"))
}

type gateStubClient struct {
	exchanges atomic.Int32
	asyncs    atomic.Int32
}

func (c *gateStubClient) Start() {}

func (c *gateStubClient) Exchange(_ context.Context, _ adapter.DNSTransport, message *mDNS.Msg, _ adapter.DNSQueryOptions, _ func(*mDNS.Msg) bool) (*mDNS.Msg, error) {
	c.exchanges.Add(1)
	response := new(mDNS.Msg)
	response.SetReply(message)
	return response, nil
}

func (c *gateStubClient) ExchangeAsync(_ context.Context, _ adapter.DNSTransport, message *mDNS.Msg, _ adapter.DNSQueryOptions, _ func(*mDNS.Msg) bool, callback func(*mDNS.Msg, error)) {
	c.asyncs.Add(1)
	response := new(mDNS.Msg)
	response.SetReply(message)
	callback(response, nil)
}

func (c *gateStubClient) Lookup(context.Context, adapter.DNSTransport, string, adapter.DNSQueryOptions, func(*mDNS.Msg) bool) ([]netip.Addr, error) {
	return nil, nil
}

func (c *gateStubClient) ClearCache() {}

func newGateTestRouter(t *testing.T) *Router {
	t.Helper()
	router, err := NewRouter(context.Background(), log.NewNOPFactory(), option.DNSOptions{})
	require.NoError(t, err)
	require.NotNil(t, router.queryGate, "the router must own a DNS query gate")
	return router
}

func gateTestMessage(t *testing.T) *mDNS.Msg {
	t.Helper()
	message := new(mDNS.Msg)
	message.SetQuestion("example.com.", mDNS.TypeA)
	return message
}

func TestDNSQueryGateIsCreatedWithTheDocumentedPlan(t *testing.T) {
	router := newGateTestRouter(t)
	stats := router.queryGate.Stats()
	require.Equal(t, defaultDNSQueryLimit, stats.Limit)
	require.Equal(t, defaultDNSQueryQueue, stats.QueueLimit)
	require.Equal(t, defaultDNSQueryWait, stats.WaitBudget)
}

func TestSaturatedDNSGateRefusesBeforeTheClientExchange(t *testing.T) {
	router := newGateTestRouter(t)
	router.queryGate = resourceguard.NewBoundedGate(1, 0, 50*time.Millisecond)
	held, err := router.queryGate.Acquire(context.Background(), resourceguard.ClassNormal)
	require.NoError(t, err)
	defer held.Release()

	client := &gateStubClient{}
	router.client = client
	options := adapter.DNSQueryOptions{Transport: gateStubTransport{}}

	_, err = router.Exchange(context.Background(), gateTestMessage(t), options)
	require.ErrorIs(t, err, resourceguard.ErrQueueFull,
		"a saturated DNS budget must refuse the exchange, not run it")
	require.Zero(t, client.exchanges.Load(), "the client exchange ran despite the refusal")
	require.Equal(t, 1, router.queryGate.Stats().Inflight,
		"the refused query left a slot behind (1 is this test's own held permit)")

	held.Release()
	response, err := router.Exchange(context.Background(), gateTestMessage(t), options)
	require.NoError(t, err)
	require.NotNil(t, response)
	require.EqualValues(t, 1, client.exchanges.Load())
	require.Zero(t, router.queryGate.Stats().Inflight,
		"the synchronous exchange must return its slot when it finishes")
}

func TestSaturatedDNSGateRefusesTheAsyncExchange(t *testing.T) {
	router := newGateTestRouter(t)
	router.queryGate = resourceguard.NewBoundedGate(1, 0, 50*time.Millisecond)
	held, err := router.queryGate.Acquire(context.Background(), resourceguard.ClassNormal)
	require.NoError(t, err)
	defer held.Release()

	client := &gateStubClient{}
	router.client = client
	options := adapter.DNSQueryOptions{Transport: gateStubTransport{}}

	done := make(chan error, 1)
	router.ExchangeAsync(context.Background(), gateTestMessage(t), options, func(_ *mDNS.Msg, exchangeErr error) {
		done <- exchangeErr
	})
	select {
	case exchangeErr := <-done:
		require.ErrorIs(t, exchangeErr, resourceguard.ErrQueueFull)
	case <-time.After(2 * time.Second):
		t.Fatal("a refused async exchange never called back")
	}
	require.Zero(t, client.asyncs.Load(), "the async exchange ran despite the refusal")

	held.Release()
	router.ExchangeAsync(context.Background(), gateTestMessage(t), options, func(_ *mDNS.Msg, exchangeErr error) {
		done <- exchangeErr
	})
	select {
	case exchangeErr := <-done:
		require.NoError(t, exchangeErr)
	case <-time.After(2 * time.Second):
		t.Fatal("the admitted async exchange never completed")
	}
	require.EqualValues(t, 1, client.asyncs.Load())
	require.Zero(t, router.queryGate.Stats().Inflight, "the async exchange leaked its slot")
}
