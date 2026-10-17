package dns

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"

	"github.com/stretchr/testify/require"
)

// What a network reset does to the answers the cache is already holding, on the network the device did
// not leave.
//
// # Why "not flushed" is the wrong way to state this
//
// The rule is not "a reset keeps the cache". It is that the two facts are independent:
//
//	DNS answer validity       a statement about a NAME at a moment, which a path change does not alter
//	                          by itself - the A record it points at is the same record on the next link
//	local family reachability which family THIS machine can currently dial, decided per candidate
//
// A reset that leaves the environment fingerprint unchanged - the same network, re-pinned transports, a
// fresh generation because a reset is a barrier - is exactly the case where the first fact still holds
// and the second one may not. The cache must therefore still answer, and the dial path must decide the
// family again.
//
// # The half that was missing
//
// The sibling tests in this package pin the NEGATIVE direction thoroughly: an answer from generation N
// must not be readable or storable on generation N+1, and an answer belonging to environment A must not
// answer a query on environment B (cache_generation_guard_test.go, environment_pin_staleness_test.go,
// cross_generation_answer_test.go). The POSITIVE direction - that a reset which does not move the
// environment leaves a correct answer being served - had no test at all. Measured: purging the exact
// cache from Router.ResetNetwork left every package's suite green.
//
// That is worth pinning because the two directions are one decision made in different places. The
// negative direction is produced by the namespace and the generation guard; the positive direction is
// produced by the cache simply not being touched. Nothing else in the tree would notice if the second
// one became a purge.

// TestResetNetworkStillServesACachedAnswerOnTheSameNetwork is the invariant.
//
// The probe is a repeat lookup through the PRODUCTION path, counting upstream queries, rather than a
// hand-built cache key: the lookup applies its own options, so a key the test constructs is not
// necessarily the key the lookup used, and an assertion on the wrong key proves nothing.
func TestResetNetworkStillServesACachedAnswerOnTheSameNetwork(t *testing.T) {
	var generation atomic.Uint64
	client := newGenerationGuardedClient(t, &generation)
	// The transport participates in environment hashing, so environmentHash reads the network manager.
	// Its value is deliberately constant: this test is about a reset that does NOT move the environment.
	client.networkManager = &generationNetworkManager{}
	transport := &fixedFingerprintTransport{tag: "boundary", environment: []string{"wifi"}}

	router := &Router{
		logger: log.NewNOPFactory().Logger(),
		transport: &fakeDNSTransportManager{
			transports:       map[string]adapter.DNSTransport{transport.Tag(): transport},
			defaultTransport: transport,
		},
		client: client,
	}
	client.networkGeneration = router.dnsGeneration

	first, err := router.Lookup(context.Background(), "boundary.internal.", adapter.DNSQueryOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, first, "the fixture produced no answer, so the cache would never be populated")
	// The count rather than a literal: Lookup asks the families it was asked for, and the number of
	// upstream queries one lookup costs is not what this test is about. What matters is that the
	// count before and after the reset are the SAME.
	warmed := transport.queries.Load()
	require.NotZero(t, warmed, "the first lookup never reached the transport, so nothing was cached")

	// The barrier. No environment move: this is the same network, re-observed.
	generationBefore := router.dnsGeneration()
	router.ResetNetwork()
	require.Equal(t, generationBefore+1, router.dnsGeneration(),
		"the reset must advance the DNS generation, or nothing below is testing a reset")

	second, err := router.Lookup(context.Background(), "boundary.internal.", adapter.DNSQueryOptions{})
	require.NoError(t, err)
	require.Equal(t, first, second, "the answer to the same question on the same network must not change")
	require.EqualValues(t, warmed, transport.queries.Load(),
		"a network reset turned a cache hit into an upstream query. The cached answer is still a "+
			"correct statement about this name on this network - what a reset changes is which FAMILY "+
			"this machine can dial, and that is decided per candidate by the dial path, not by the "+
			"resolver's cache")

	// The negative control: a real flush DOES reach the transport. Without this the assertion above
	// would also pass on a fixture whose cache was never populated at all.
	router.ClearCache()
	_, err = router.Lookup(context.Background(), "boundary.internal.", adapter.DNSQueryOptions{})
	require.NoError(t, err)
	require.Greater(t, transport.queries.Load(), warmed,
		"ClearCache did not reach the transport, so the test cannot distinguish a kept cache from an "+
			"unused one")
}
