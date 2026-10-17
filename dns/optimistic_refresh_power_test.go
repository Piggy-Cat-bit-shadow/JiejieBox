package dns

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/power"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/service"

	mDNS "github.com/miekg/dns"
	"github.com/stretchr/testify/require"
)

// The optimistic refresh must obey the power lifecycle.
//
// # The gap this closes
//
// `backgroundRefreshDNS` is the only worker in this package that makes a network round trip nobody
// asked for. It is scheduled from a cache HIT, to renew an entry the caller has already been
// served, so it is speculative by construction - and it consulted no power state at all: this
// package contained no reference to `common/power`, to `sing/service/pause`, or to any `Allow()`
// predicate. A device that had just gone to sleep would therefore wake the radio to renew a record
// nobody was resolving, once per stale entry, for the whole sleep.
//
// # Why the predicate is `Active` and not `Allow().NetworkProbe`
//
// Measured against `common/power`: `recomputeLocked` maps devicePaused OR networkPaused to
// `StateQuiescent`, and `DefaultPolicy().Quiescent` is `Allow{HealthCheck: true, NetworkProbe:
// true}`. A `NetworkProbe` gate would therefore PERMIT the refresh in exactly the state a sleeping
// device sits in - a gate that reads as protection and is none. `Active()` is the governor's own
// statement of whether speculative background work may run, and it is false for the whole of a
// sleep and for the idle countdown behind it.
//
// # What must NOT change
//
// The forward path. This gate sits in the refresh goroutine only, after the caller's answer has
// already been delivered, so suppressing it costs freshness and never correctness: the next demand
// that finds the entry unusable resolves normally. That is the rule the bounded familyHealth cache
// established for every other cache in this tree.

// refreshGatePolicy is DefaultPolicy with a fast wake stagger.
//
// The stagger values are the only thing changed, and they have to be: DefaultPolicy spreads a wake
// over fifteen seconds, which is right for a phone and wrong for a test. A non-zero
// NetworkProbe stagger is kept - 50 ms instead of 5 s - so the WAKING band this gate has to reason
// about is still exercised, and the test still finishes in milliseconds.
func refreshGatePolicy() power.Policy {
	policy := power.DefaultPolicy()
	policy.WakeStagger = power.WakeStagger{
		HealthCheck:     0,
		ProviderRefresh: 10 * time.Millisecond,
		NetworkProbe:    50 * time.Millisecond,
		Statistics:      10 * time.Millisecond,
	}
	return policy
}

// newSleepGatedClient builds a real DNS client over a settled network with the fork's power
// governor installed - which is what production does (`box.go` registers the governor in the
// service registry) and what every fixture in this package previously omitted.
//
// A nil governor builds the no-power-concept shape that every non-Apple caller has, so the
// compatibility case is the same fixture with one argument changed rather than a second private
// copy of the wiring that could drift.
func newSleepGatedClient(t *testing.T, governor *power.Governor, transport adapter.DNSTransport) *Client {
	t.Helper()
	manager := &transitionStateManager{}
	manager.environment.Store(0xA)
	manager.routeEpoch.Store(1)
	manager.stable.Store(true)

	ctx := service.ContextWith[adapter.NetworkManager](context.Background(), manager)
	if governor != nil {
		ctx = service.ContextWith[*power.Governor](ctx, governor)
	}
	router, err := NewRouter(ctx, log.NewNOPFactory(), option.DNSOptions{})
	require.NoError(t, err)
	concrete, isConcrete := router.client.(*Client)
	require.True(t, isConcrete)
	router.concreteClient = concrete
	concrete.Start()
	concrete.networkManager = manager
	router.transport = &listingTransportManager{
		fakeDNSTransportManager: &fakeDNSTransportManager{
			transports:       map[string]adapter.DNSTransport{transport.Tag(): transport},
			defaultTransport: transport,
		},
		transports: []adapter.DNSTransport{transport},
	}
	return concrete
}

// newSettledTransport is a transport whose backend address never moves, so nothing in this file
// depends on a transition.
func newSettledTransport(tag string) *rebindingWindowTransport {
	backend := netip.MustParseAddr("10.1.0.1")
	return &rebindingWindowTransport{
		tag:         tag,
		environment: []string{"wifi"},
		current:     func() netip.Addr { return backend },
	}
}

// primeStaleEntry puts a real answer in the cache and then ages it into the optimistic window: past
// its TTL, still servable. That is the state `backgroundRefreshDNS` is scheduled from.
func primeStaleEntry(t *testing.T, client *Client, transport adapter.DNSTransport, domain string) dnsCacheKey {
	t.Helper()
	_, err := client.Exchange(context.Background(), transport, messageFor(domain),
		adapter.DNSQueryOptions{}, nil)
	require.NoError(t, err)

	// Widen the optimistic window rather than reaching into the cache's expiry bookkeeping, exactly
	// as the ownership tests do.
	client.optimisticTimeout = time.Hour
	probe := probeMessage(domain)
	key := client.newCacheKey(transport, probe.Question[0], probe, adapter.DNSQueryOptions{})
	entry, _, _ := client.loadResponse(key)
	require.NotNil(t, entry, "the priming exchange must have cached an entry")
	client.cache.AddWithLifetime(key, entry, -time.Minute)
	_, _, isStale := client.loadResponse(key)
	require.True(t, isStale,
		"the entry must actually be stale, or this exercises the fresh-hit path instead of the "+
			"optimistic one it is named for")
	return key
}

// scheduleRefresh runs the real refresh entry point and JOINS on it.
//
// The join is what makes the suppression assertion below meaningful. `backgroundRefreshDNS` stores
// its key synchronously before starting the goroutine and deletes it in a defer, so
// `waitForRefreshIdle` returning means the goroutine has run to completion - a reading taken after
// it is final, not a sample of a race.
func scheduleRefresh(t *testing.T, client *Client, transport adapter.DNSTransport, domain string, key dnsCacheKey) {
	t.Helper()
	probe := probeMessage(domain)
	client.backgroundRefreshDNS(transport, key,
		client.prepareExchangeMessage(probe.Copy(), adapter.DNSQueryOptions{}),
		adapter.DNSQueryOptions{}, nil)
	waitForRefreshIdle(t, client)
}

// TestOptimisticRefreshRunsWhileTheDeviceIsActive is the CONTROL.
//
// Without it the suppression test proves nothing: "no round trip happened" is also what a fixture
// that never schedules a refresh reports, and what a refresh broken in some unrelated way reports.
func TestOptimisticRefreshRunsWhileTheDeviceIsActive(t *testing.T) {
	governor := power.NewGovernor(refreshGatePolicy())
	t.Cleanup(governor.Close)

	transport := newSettledTransport("refresh-active")
	client := newSleepGatedClient(t, governor, transport)
	require.Same(t, governor, client.powerGovernor,
		"the fixture must actually install the governor, or the gate under test is never evaluated")
	require.True(t, governor.Active(), "a fresh governor must report ACTIVE")
	require.False(t, client.backgroundRefreshSuppressed(), "and it must not suppress anything")

	key := primeStaleEntry(t, client, transport, "active.example.")
	queriesBefore := transport.queries

	scheduleRefresh(t, client, transport, "active.example.", key)

	require.Equal(t, queriesBefore+1, transport.queries,
		"with the device active the refresh must still make its round trip. This is the control: "+
			"if it fails, the suppression test below measures nothing")
	stored, _, _ := client.loadResponse(key)
	require.NotNil(t, stored, "the refresh must have left an entry behind")
}

// TestOptimisticRefreshIsSuppressedWhileTheDeviceIsPaused is the property.
func TestOptimisticRefreshIsSuppressedWhileTheDeviceIsPaused(t *testing.T) {
	governor := power.NewGovernor(refreshGatePolicy())
	t.Cleanup(governor.Close)

	transport := newSettledTransport("refresh-asleep")
	client := newSleepGatedClient(t, governor, transport)
	require.Same(t, governor, client.powerGovernor)

	// The entry is primed while the device is awake, which is the only time it could legitimately
	// have been learned.
	key := primeStaleEntry(t, client, transport, "asleep.example.")

	governor.DevicePaused()
	require.False(t, governor.Active(),
		"pausing the device must take the governor out of ACTIVE, or there is no state for this "+
			"test to suppress against")
	require.True(t, client.backgroundRefreshSuppressed(),
		"and the gate must read that state as suppressed. allow().NetworkProbe is TRUE here - "+
			"StateQuiescent permits health checks and probes - so a predicate built on it would "+
			"read as protection and be none")

	queriesBefore := transport.queries
	scheduleRefresh(t, client, transport, "asleep.example.", key)

	require.Equal(t, queriesBefore, transport.queries,
		"the background refresh made a round trip while the device was asleep. Its caller had "+
			"already been served, so this woke the radio for a record nobody was resolving - the "+
			"exact budget the tunnel exists to save. Skipping costs freshness only, because the "+
			"forward path resolves on the next demand")

	stored, ttl, _ := client.loadResponse(key)
	require.NotNil(t, stored, "the stale entry must still be servable: suppression is not eviction")
	require.Len(t, stored.Answer, 1)
	require.Equal(t, "10.1.0.1", stored.Answer[0].(*mDNS.A).A.String(),
		"a suppressed refresh deposited an answer")
	require.EqualValues(t, 0, ttl, "and it must not have renewed the entry's lifetime")

	// After the wake the governor is in the WAKING band, where the stagger - not this gate -
	// decides. DNS renewal is the network-probe category, so it stays withheld for the stagger.
	governor.DeviceWake()
	require.Equal(t, power.StateWaking, governor.State(),
		"a wake with a non-zero stagger must enter WAKING; a zero-stagger policy would make the "+
			"band below untestable")
	require.True(t, client.backgroundRefreshSuppressed(),
		"the waking band must still withhold the refresh until its own category is released, or "+
			"every subsystem would resume in the same instant the stagger exists to prevent")

	// The gate must reopen, or it is an outage rather than a saving.
	require.Eventually(t, func() bool { return !client.backgroundRefreshSuppressed() },
		5*time.Second, 5*time.Millisecond,
		"the gate never reopened after the wake. Suppressing speculative work during sleep is a "+
			"saving only if the work comes back when the device does")

	queriesAfterWake := transport.queries
	scheduleRefresh(t, client, transport, "asleep.example.", key)
	require.Equal(t, queriesAfterWake+1, transport.queries,
		"the refresh did not resume after the wake")
}

// TestOptimisticRefreshIsUnchangedWithoutAGovernor pins the compatibility half.
//
// Every caller that has no power concept - the CLI, the tests, any platform without the Apple power
// bridge - must keep the previous behaviour exactly. A gate that defaulted to "suppressed" when the
// governor is absent would silently stop renewing every cache entry in the product.
func TestOptimisticRefreshIsUnchangedWithoutAGovernor(t *testing.T) {
	transport := newSettledTransport("refresh-no-governor")
	client := newSleepGatedClient(t, nil, transport)

	require.Nil(t, client.powerGovernor, "no governor was registered, so none may be reported")

	key := primeStaleEntry(t, client, transport, "plain.example.")
	queriesBefore := transport.queries
	scheduleRefresh(t, client, transport, "plain.example.", key)
	require.Equal(t, queriesBefore+1, transport.queries,
		"without a governor the refresh must behave exactly as it did before this gate existed")
}
