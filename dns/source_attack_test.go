package dns

import (
	"context"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"

	mDNS "github.com/miekg/dns"
	"github.com/stretchr/testify/require"
)

// Two self-designed attacks for the source contracts.
//
// # Attack 1: the same entry through two hit paths
//
// The client has two ways to serve a cache hit: the direct path (`questionCache`) and the shared
// wait inside `beginExchange`. Both select the same entry, so both must report the same source. The
// C2 fix changed the direct path; a population that fixes one path and leaves the other reading a
// later value is exactly the class this round is about. The waiter's checker advances the
// environment after the hit is selected, and the two reported stamps must agree.
//
// # Attack 2: does a disabled cache weaken the family guard?
//
// The forbidden-family rule is a POLICY, not a caching decision: a caller that forces IPv4 while a
// legacy rule forces IPv6 must still issue nothing. The source work touched the same lookup item
// builder, so this attack checks that DisableCache did not accidentally turn the guard into a
// cache-only thing.

func TestSameEntryThroughTwoHitPathsKeepsOneSource(t *testing.T) {
	generation := &atomic.Uint64{}
	generation.Store(1)
	transport := newCountingSourceTransport(netip.MustParseAddr("192.0.2.1"))
	transport.entered = make(chan struct{})
	transport.release = make(chan struct{})
	client := sourceClient(generation)
	options := adapter.DNSQueryOptions{LookupStrategy: C.DomainStrategyIPv4Only}
	domain := "two-paths.test."
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	type outcome struct {
		stamp uint64
		known bool
		err   error
	}
	first := make(chan outcome, 1)
	go func() {
		_, stamp, known, err := client.lookupWithSource(ctx, transport, domain, options, nil)
		first <- outcome{stamp: stamp, known: known, err: err}
	}()
	select {
	case <-transport.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the first exchange never began")
	}

	checkerRan := make(chan struct{})
	second := make(chan outcome, 1)
	go func() {
		_, stamp, known, err := client.lookupWithSource(ctx, transport, domain, options, func(*mDNS.Msg) bool {
			// The entry has been selected; the environment moves before the caller receives it.
			generation.Add(1)
			close(checkerRan)
			return true
		})
		second <- outcome{stamp: stamp, known: known, err: err}
	}()
	// Let the second caller park on the shared exchange before the answer is published.
	time.Sleep(20 * time.Millisecond)
	close(transport.release)

	firstOutcome := <-first
	secondOutcome := <-second
	require.NoError(t, firstOutcome.err)
	require.NoError(t, secondOutcome.err)
	require.True(t, firstOutcome.known)
	require.True(t, secondOutcome.known)
	require.EqualValues(t, 1, firstOutcome.stamp, "the exchange was asked on generation 1")
	require.Equal(t, firstOutcome.stamp, secondOutcome.stamp,
		"the direct hit and the shared-wait hit must report the same source for the same entry")
	require.Equal(t, 1, transport.exchangeCount(), "the waiter must share the exchange")
	select {
	case <-checkerRan:
	default:
		t.Fatal("the shared-wait hit must run the caller's response checker")
	}
}

func TestDisableCacheDoesNotBypassTheForbiddenFamilyGuard(t *testing.T) {
	for _, disableCache := range []bool{false, true} {
		transport := &queryRecordingTransport{addressA: "192.0.2.1", address6: "2001:db8::1"}
		router := legacyRuleRouter(t, transport, []option.DNSRule{
			legacyFamilyStrategyRule("final", C.DomainStrategyIPv6Only),
		})
		_, err := router.Lookup(context.Background(), "guard.test.", adapter.DNSQueryOptions{
			LookupStrategy: C.DomainStrategyIPv4Only,
			DisableCache:   disableCache,
		})
		require.Error(t, err, "the conflicting strict families must still be refused")
		require.Empty(t, transport.observed(),
			"the forbidden family must not reach the resolver (disableCache=%v)", disableCache)
	}
}
