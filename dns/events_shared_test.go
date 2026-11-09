package dns

// O03 — "shared" is a fact of its own, next to "cache".
//
// A caller that finds the exchange key taken waits for whoever holds it, and the answer it finally
// reads is that caller's. It usually reports Source=cache, because what it read is the entry the
// other caller stored - but "I joined a query that was already in flight" and "this answer was
// already in the cache when I asked" are different facts. Only the second one means the resolver
// had answered this question for anyone before this caller arrived.

import (
	"context"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"

	mDNS "github.com/miekg/dns"

	"github.com/stretchr/testify/require"
)

func TestAJoinedQueryIsReportedAsSharedNotAsAnIndependentHit(t *testing.T) {
	bus := NewEventBus(8)
	events, unsubscribe := bus.Subscribe(false, 0)
	defer unsubscribe()

	release := make(chan struct{})
	transport := &herdTransport{holdFirst: release}
	client := newEventClient(t, transport, bus, false)

	// The leader asks, and the transport holds it open so the second caller really waits.
	leaderDone := make(chan struct{})
	go func() {
		defer close(leaderDone)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = client.Exchange(ctx, transport, herdMessage(), adapter.DNSQueryOptions{}, nil)
	}()

	// Wait until the leader is inside the transport, which is the state the joiner needs.
	require.Eventually(t, func() bool { return transport.queryCount.Load() == 1 }, time.Second, time.Millisecond)

	joinerDone := make(chan struct{})
	go func() {
		defer close(joinerDone)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := client.Exchange(ctx, transport, herdMessageCopy(), adapter.DNSQueryOptions{}, nil)
		require.NoError(t, err)
	}()

	// Give the joiner time to install itself as a waiter before the leader completes.
	time.Sleep(20 * time.Millisecond)
	close(release)
	<-leaderDone
	<-joinerDone

	// The two events are emitted by two goroutines, so their ORDER is not the contract; which fact
	// each carries is.
	first := receiveEvent(t, events)
	second := receiveEvent(t, events)
	var leaderEvent, joinerEvent QueryEvent
	if first.Shared {
		joinerEvent, leaderEvent = first, second
	} else {
		leaderEvent, joinerEvent = first, second
	}

	require.False(t, leaderEvent.Shared, "the caller that performed the exchange did not join anything")
	require.Equal(t, QuerySourceUpstream, leaderEvent.Source)

	require.True(t, joinerEvent.Shared,
		"the answer came from the leader's in-flight exchange, which is not the same fact as a cache hit")
	require.Equal(t, QuerySourceCache, joinerEvent.Source)
	require.EqualValues(t, 1, transport.queryCount.Load(),
		"only the leader reached the transport, which is what makes the joiner's answer shared")
}

func TestACacheHitThatWasAlreadyThereIsNotShared(t *testing.T) {
	bus := NewEventBus(8)
	events, unsubscribe := bus.Subscribe(false, 0)
	defer unsubscribe()

	transport := &herdTransport{}
	client := newEventClient(t, transport, bus, false)

	runEventExchange(t, client, transport, herdMessage())
	first := receiveEvent(t, events)
	require.False(t, first.Shared)

	// Sequential, so nobody was in flight: this is a cache entry that already existed.
	runEventExchange(t, client, transport, herdMessage())
	second := receiveEvent(t, events)
	require.Equal(t, QuerySourceCache, second.Source)
	require.False(t, second.Shared,
		"an answer that was already cached must not be reported as joined work")
}

// herdMessageCopy returns the same question as an independent message value, so two callers ask
// the same cache question without sharing one *dns.Msg.
func herdMessageCopy() *mDNS.Msg {
	message := new(mDNS.Msg)
	message.Id = 1
	message.RecursionDesired = true
	message.Question = []mDNS.Question{{Name: "herd.example.org.", Qtype: mDNS.TypeA, Qclass: mDNS.ClassINET}}
	return message
}
