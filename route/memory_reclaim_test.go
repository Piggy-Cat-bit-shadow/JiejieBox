package route

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"

	"github.com/stretchr/testify/require"
)

// Tests for the memory reclaim entry points added or corrected in the layered-reclaim round.
//
// They assert the PROPERTY each layer promises, not that a function was called:
//
//   - the pool pass reaches the router's single walk and does not also walk owners itself;
//   - the cache pass is optional and touches no owner;
//   - a critical release cannot reset a pool while a progressive trim is walking it, and vice
//     versa.

// countingIdleOutbound implements only the release half. The nil embedded interface is never
// called: the walk asserts IdleConnectionReleaser and calls CloseIdleConnections, which is the
// point of the split interface.
type countingIdleOutbound struct {
	adapter.Outbound
	closes atomic.Int32
}

func (o *countingIdleOutbound) CloseIdleConnections() {
	o.closes.Add(1)
}

type outboundList struct {
	adapter.OutboundManager
	outbounds []adapter.Outbound
}

func (m *outboundList) Outbounds() []adapter.Outbound { return m.outbounds }

type cacheTrimmingRouter struct {
	adapter.Router
	calls   atomic.Int32
	removed int
}

func (r *cacheTrimmingRouter) TrimExpiredCaches() int {
	r.calls.Add(1)
	return r.removed
}

func TestTrimMemoryDelegatesToTheRoutersSingleWalk(t *testing.T) {
	router := newCountingRouter()
	outbound := &countingIdleOutbound{}
	manager := &NetworkManager{
		router:   router,
		endpoint: &emptyEndpointManager{},
		outbound: &outboundList{outbounds: []adapter.Outbound{outbound}},
	}

	manager.TrimMemory(context.Background())

	require.Equal(t, 1, router.trimCount())
	require.Zero(t, outbound.closes.Load(),
		"the manager walked the outbounds itself after the router already walked them")
}

// A manager with no router is the only case where the local walk is the sole owner walk, and it
// must still close each owner exactly once.
func TestTrimMemoryWithoutARouterClosesEachOwnerOnce(t *testing.T) {
	outbound := &countingIdleOutbound{}
	manager := &NetworkManager{
		endpoint: &emptyEndpointManager{},
		outbound: &outboundList{outbounds: []adapter.Outbound{outbound}},
	}

	manager.TrimMemory(context.Background())

	require.Equal(t, int32(1), outbound.closes.Load())
}

func TestTrimCachesUsesTheOptionalCapability(t *testing.T) {
	t.Run("capable", func(t *testing.T) {
		router := &cacheTrimmingRouter{removed: 7}
		manager := &NetworkManager{router: router}
		require.Equal(t, 7, manager.TrimCaches(context.Background()))
		require.Equal(t, int32(1), router.calls.Load())
	})
	t.Run("not capable", func(t *testing.T) {
		manager := &NetworkManager{router: newCountingRouter()}
		require.Zero(t, manager.TrimCaches(context.Background()))
	})
}

// blockingTrimRouter is a router whose progressive pass can be held inside the walk, which is the
// state a release has to stay out of.
type blockingTrimRouter struct {
	adapter.Router
	trimStarted chan struct{}
	trimRelease chan struct{}
	startOnce   sync.Once
	trims       atomic.Int32
	resets      atomic.Int32
}

func newBlockingTrimRouter() *blockingTrimRouter {
	return &blockingTrimRouter{
		trimStarted: make(chan struct{}),
		trimRelease: make(chan struct{}),
	}
}

func (r *blockingTrimRouter) TrimIdleResources() {
	r.trims.Add(1)
	r.startOnce.Do(func() { close(r.trimStarted) })
	<-r.trimRelease
}

func (r *blockingTrimRouter) ResetNetwork() {
	r.resets.Add(1)
}

// TestReleaseMemoryWaitsForAnInFlightTrim is the memory-pass serialization property.
//
// TrimMemory is inside a pool walk. ReleaseMemory is the aggressive pass and resets the same
// resources. Before the gate, a platform RUNNING_LOW and a RUNNING_CRITICAL notification could
// dispatch these on independent goroutines and reset a pool the trim was closing. The assertion is
// ordering: the reset must not begin until the walk has returned.
func TestReleaseMemoryWaitsForAnInFlightTrim(t *testing.T) {
	manager, _ := newCoordinatorHarness(t)
	router := newBlockingTrimRouter()
	manager.router = router

	trimDone := make(chan struct{})
	go func() {
		defer close(trimDone)
		manager.TrimMemory(context.Background())
	}()
	select {
	case <-router.trimStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("the trim never entered the router walk")
	}

	releaseDone := make(chan struct{})
	go func() {
		defer close(releaseDone)
		manager.ReleaseMemory(context.Background())
	}()

	select {
	case <-releaseDone:
		t.Fatal("ReleaseMemory completed while TrimMemory was still inside the pool walk")
	case <-time.After(100 * time.Millisecond):
	}
	require.Zero(t, router.resets.Load(),
		"the reset began while the trim was still walking the same pools")

	close(router.trimRelease)
	select {
	case <-trimDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the trim did not finish")
	}
	select {
	case <-releaseDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the release did not finish after the trim returned")
	}
	require.Equal(t, int32(1), router.resets.Load())
	require.Equal(t, int32(1), router.trims.Load())
}

// TestReleaseMemoryClosesIdleOwnersOnce covers the aggressive pass's own walk: it resets and then
// releases the owners that survived the reset, exactly once each.
func TestReleaseMemoryClosesIdleOwnersOnce(t *testing.T) {
	manager, _ := newCoordinatorHarness(t)
	outbound := &countingIdleOutbound{}
	manager.outbound = &outboundList{outbounds: []adapter.Outbound{outbound}}
	manager.router = newCountingRouter()

	manager.ReleaseMemory(context.Background())

	require.Equal(t, int32(1), outbound.closes.Load())
}

// trimmingDNSRouter is the router's view of the DNS layer: the TrimExpiredCaches capability and
// nothing else.
type trimmingDNSRouter struct {
	adapter.DNSRouter
	calls   atomic.Int32
	removed int
}

func (r *trimmingDNSRouter) TrimExpiredCaches() int {
	r.calls.Add(1)
	return r.removed
}

func TestRouterTrimExpiredCachesDelegatesToDNS(t *testing.T) {
	dnsRouter := &trimmingDNSRouter{removed: 3}
	router := &Router{dns: dnsRouter}

	require.Equal(t, 3, router.TrimExpiredCaches())
	require.Equal(t, int32(1), dnsRouter.calls.Load())
}

// A router whose DNS layer cannot answer the capability must return zero rather than panic or fall
// back to a destructive clear.
func TestRouterTrimExpiredCachesWithoutADNSLayer(t *testing.T) {
	require.Zero(t, (&Router{}).TrimExpiredCaches())
}
