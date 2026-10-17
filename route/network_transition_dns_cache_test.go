package route

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"

	"github.com/stretchr/testify/require"
)

// Tests for the DNS half of a network transition's boundary.
//
// # The two operations, and why only one of them belongs to a transition
//
// The DNS layer exposes two cache-control entry points, and they mean different things:
//
//	ResetNetwork  moves the network barrier. The DNS generation advances first, so every answer in
//	              flight is attributable to the network it was asked on, and every transport is
//	              re-pinned to the environment it now serves. Entries belonging to the OLD environment
//	              simply stop being reachable; nothing is thrown away.
//	ClearCache    discards what is cached. Correct when the answers themselves may have changed for a
//	              reason that is not the network - a mode switch, a policy change.
//
// A network change is the first and not the second. A/AAAA records do not become wrong because the
// device moved: what changes is which FAMILY this machine can reach, and that is decided per dial by
// the candidate admission path, not by the cache. Flushing on a transition therefore buys nothing and
// costs a burst of upstream queries at exactly the moment a device is re-addressing.
//
// # Why this needed pinning
//
// Measured: adding `r.dns.ClearCache()` to Router.ResetNetwork - one line, plausible, and the shape a
// reader would reach for when told "the network changed" - left the entire suite green. dns/ pins what
// the CACHE does with a generation; nothing pinned what the NETWORK LAYER asks the cache to do.

// countingDNSRouter reports which of the two cache-control entry points the network layer used.
//
// Embedding adapter.DNSRouter keeps this a fragment: the interface it does not override is nil, so a
// call to any other method would panic rather than silently answer nothing - which is the right
// failure for a double whose whole purpose is to be asked exactly two questions.
type countingDNSRouter struct {
	adapter.DNSRouter
	resets  atomic.Int64
	flushes atomic.Int64
}

func (r *countingDNSRouter) ResetNetwork() { r.resets.Add(1) }
func (r *countingDNSRouter) ClearCache()   { r.flushes.Add(1) }

// countingHTTPClientManager records the other pool a transition retires, so the test can show that the
// reset body ran at all rather than merely that it did not flush.
type countingHTTPClientManager struct {
	adapter.HTTPClientManager
	resets atomic.Int64
}

func (m *countingHTTPClientManager) ResetNetwork() { m.resets.Add(1) }

// TestANetworkTransitionIsABarrierAndNotACacheFlush drives one real reset through the production entry
// point and asserts what the DNS layer was asked to do.
//
// resetNetworkLocked calls Router.ResetNetwork() as its unconditional last statement, so the counts
// below are the numbers a network transition produces - not a direct call to the router in isolation.
func TestANetworkTransitionIsABarrierAndNotACacheFlush(t *testing.T) {
	dnsRouter := new(countingDNSRouter)
	httpClients := new(countingHTTPClientManager)
	router := &Router{
		logger:            log.NewNOPFactory().Logger(),
		dns:               dnsRouter,
		httpClientManager: httpClients,
	}

	harness := newInterfaceTransitionHarness(t)
	harness.manager.router = router

	harness.manager.ResetNetwork(context.Background())

	require.EqualValues(t, 1, httpClients.resets.Load(),
		"the HTTP client pools were not retired by the transition, so the reset body did not run and "+
			"the assertions below would prove nothing")
	require.EqualValues(t, 1, dnsRouter.resets.Load(),
		"one transition must move the DNS barrier exactly once. The barrier is what makes an answer "+
			"asked for on the old network unattributable to the new one")
	require.EqualValues(t, 0, dnsRouter.flushes.Load(),
		"the transition flushed the DNS cache. A network change does not make an A or AAAA record "+
			"wrong - it changes which family this machine can reach, and that is decided per dial by "+
			"candidate admission. Flushing therefore buys nothing and costs a burst of upstream "+
			"queries during the one moment the device is re-addressing")
}
