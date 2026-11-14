package dns

import (
	"context"
	"errors"
	"net/netip"
	"sync"
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
	lookups   atomic.Int32
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
	c.lookups.Add(1)
	return []netip.Addr{netip.MustParseAddr("192.0.2.1")}, nil
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

// Lookup is the public entry used by dialers and rule matching. It does not pass through Exchange,
// so before this test the query budget covered DNS messages but not the lookups that resolve a
// destination for a dial.
func TestSaturatedDNSGateRefusesRouterLookup(t *testing.T) {
	router := newGateTestRouter(t)
	router.queryGate = resourceguard.NewBoundedGate(1, 0, 50*time.Millisecond)
	held, err := router.queryGate.Acquire(context.Background(), resourceguard.ClassNormal)
	require.NoError(t, err)
	defer held.Release()

	client := &gateStubClient{}
	router.client = client
	options := adapter.DNSQueryOptions{Transport: gateStubTransport{}}

	_, err = router.Lookup(context.Background(), "example.com", options)
	require.ErrorIs(t, err, resourceguard.ErrQueueFull,
		"a saturated DNS budget must refuse the lookup, not run it")
	require.Zero(t, client.lookups.Load(), "the client lookup ran despite the refusal")

	held.Release()
	addresses, err := router.Lookup(context.Background(), "example.com", options)
	require.NoError(t, err)
	require.Len(t, addresses, 1)
	require.EqualValues(t, 1, client.lookups.Load())
	require.Zero(t, router.queryGate.Stats().Inflight,
		"the lookup must return its slot when it finishes")
}

// pendingDNSClient keeps the async callback until the test fires it, which is what makes the permit
// lifetime observable: the gate slot must be held while the exchange is in flight and returned exactly
// once when the client completes it.
type pendingDNSClient struct {
	access    sync.Mutex
	callbacks []func(*mDNS.Msg, error)
}

func (c *pendingDNSClient) Start() {}

func (c *pendingDNSClient) Exchange(context.Context, adapter.DNSTransport, *mDNS.Msg, adapter.DNSQueryOptions, func(*mDNS.Msg) bool) (*mDNS.Msg, error) {
	return nil, errors.New("synchronous exchange is not used by this test")
}

func (c *pendingDNSClient) ExchangeAsync(_ context.Context, _ adapter.DNSTransport, _ *mDNS.Msg, _ adapter.DNSQueryOptions, _ func(*mDNS.Msg) bool, callback func(*mDNS.Msg, error)) {
	c.access.Lock()
	c.callbacks = append(c.callbacks, callback)
	c.access.Unlock()
}

func (c *pendingDNSClient) Lookup(context.Context, adapter.DNSTransport, string, adapter.DNSQueryOptions, func(*mDNS.Msg) bool) ([]netip.Addr, error) {
	return nil, nil
}

func (c *pendingDNSClient) ClearCache() {}

func (c *pendingDNSClient) callbackCount() int {
	c.access.Lock()
	defer c.access.Unlock()
	return len(c.callbacks)
}

func (c *pendingDNSClient) fire(index int, response *mDNS.Msg, err error) {
	c.access.Lock()
	callback := c.callbacks[index]
	c.access.Unlock()
	callback(response, err)
}

// The permit covers the WHOLE async exchange, not just its start: while the client is still working,
// the slot is held (that is the concurrency bound), and it is returned exactly once when the callback
// fires. A second callback from a misbehaving transport must not release it twice.
func TestAsyncExchangeHoldsOnePermitUntilCallback(t *testing.T) {
	router := newGateTestRouter(t)
	router.queryGate = resourceguard.NewBoundedGate(1, 0, time.Second)
	client := &pendingDNSClient{}
	router.client = client
	options := adapter.DNSQueryOptions{Transport: gateStubTransport{}}
	message := gateTestMessage(t)

	first := make(chan error, 1)
	router.ExchangeAsync(context.Background(), message, options, func(_ *mDNS.Msg, exchangeErr error) {
		first <- exchangeErr
	})
	require.Eventually(t, func() bool { return client.callbackCount() == 1 }, 2*time.Second, time.Millisecond)
	require.Equal(t, 1, router.queryGate.Stats().Inflight,
		"the in-flight exchange must hold its slot")

	// The one slot is held, so a second exchange is refused synchronously rather than queued.
	second := make(chan error, 1)
	router.ExchangeAsync(context.Background(), message, options, func(_ *mDNS.Msg, exchangeErr error) {
		second <- exchangeErr
	})
	require.ErrorIs(t, <-second, resourceguard.ErrQueueFull)
	require.Equal(t, 1, client.callbackCount(), "the refused exchange reached the client")

	response := new(mDNS.Msg)
	response.SetReply(message)
	client.fire(0, response, nil)
	require.NoError(t, <-first)
	require.Zero(t, router.queryGate.Stats().Inflight, "the completed exchange must return its slot")

	before := router.queryGate.Stats().Released
	client.fire(0, nil, errors.New("duplicate callback"))
	require.Equal(t, before, router.queryGate.Stats().Released,
		"a duplicate callback must not release the permit twice")
	require.Zero(t, router.queryGate.Stats().Inflight)
}

// The error completion path returns the slot too: an upstream failure is not a reason to keep a
// concurrency slot until the process exits.
func TestAsyncExchangeErrorReleasesThePermit(t *testing.T) {
	router := newGateTestRouter(t)
	router.queryGate = resourceguard.NewBoundedGate(1, 0, time.Second)
	client := &pendingDNSClient{}
	router.client = client
	options := adapter.DNSQueryOptions{Transport: gateStubTransport{}}

	done := make(chan error, 1)
	router.ExchangeAsync(context.Background(), gateTestMessage(t), options, func(_ *mDNS.Msg, exchangeErr error) {
		done <- exchangeErr
	})
	require.Eventually(t, func() bool { return client.callbackCount() == 1 }, 2*time.Second, time.Millisecond)
	require.Equal(t, 1, router.queryGate.Stats().Inflight)

	failure := errors.New("upstream refused")
	client.fire(0, nil, failure)
	require.ErrorIs(t, <-done, failure)
	require.Zero(t, router.queryGate.Stats().Inflight)
	require.EqualValues(t, 1, router.queryGate.Stats().Released)
}

// ExchangeAsync's admission is synchronous. A refusal must be visible to the caller as a callback
// that has ALREADY run when the function returns, not as a callback scheduled for later; otherwise a
// caller that starts a timer or releases state on return would race the refusal.
func TestExchangeAsyncRefusalCallsBackBeforeReturning(t *testing.T) {
	router := newGateTestRouter(t)
	router.queryGate = resourceguard.NewBoundedGate(1, 0, 50*time.Millisecond)
	held, err := router.queryGate.Acquire(context.Background(), resourceguard.ClassNormal)
	require.NoError(t, err)
	defer held.Release()

	client := &gateStubClient{}
	router.client = client
	var calls atomic.Int32
	router.ExchangeAsync(context.Background(), gateTestMessage(t), adapter.DNSQueryOptions{Transport: gateStubTransport{}}, func(_ *mDNS.Msg, exchangeErr error) {
		require.ErrorIs(t, exchangeErr, resourceguard.ErrQueueFull)
		calls.Add(1)
	})
	require.EqualValues(t, 1, calls.Load(),
		"the refusal callback was not invoked before ExchangeAsync returned")
	require.Zero(t, client.asyncs.Load(), "the refused exchange reached the client")
}

// The same contract applies to a client that completes synchronously: the callback may run before the
// call returns, so callers must not hold a lock across ExchangeAsync that the callback needs.
func TestExchangeAsyncMayCallBackBeforeReturning(t *testing.T) {
	router := newGateTestRouter(t)
	client := &gateStubClient{}
	router.client = client
	var called atomic.Bool
	router.ExchangeAsync(context.Background(), gateTestMessage(t), adapter.DNSQueryOptions{Transport: gateStubTransport{}}, func(_ *mDNS.Msg, exchangeErr error) {
		require.NoError(t, exchangeErr)
		called.Store(true)
	})
	require.True(t, called.Load(),
		"the synchronous client completion was not visible before ExchangeAsync returned")
	require.EqualValues(t, 1, client.asyncs.Load())
	require.Zero(t, router.queryGate.Stats().Inflight)
}
