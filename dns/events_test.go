package dns

// O03 — the query event surface.
//
// The events exist to make a query attributable: which transport, where the answer came
// from, what the outcome was, and - only when a subscriber asks - which records it carried.
// These cases drive the REAL Exchange path (the same client the router uses) with the
// package's existing fake transport, so they pin the facts at the point they are produced.

import (
	"context"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"

	mDNS "github.com/miekg/dns"

	"github.com/stretchr/testify/require"
)

// newEventClient builds a client with the given bus over the herd fake transport.
func newEventClient(t *testing.T, transport *herdTransport, bus *EventBus, disableCache bool) *Client {
	t.Helper()
	return NewClient(ClientOptions{
		Context:      context.Background(),
		Logger:       log.NewNOPFactory().Logger(),
		EventBus:     bus,
		DisableCache: disableCache,
	})
}

// receiveEvent takes one event or fails the test.
func receiveEvent(t *testing.T, events <-chan QueryEvent) QueryEvent {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(5 * time.Second):
		t.Fatal("no query event was delivered")
		return QueryEvent{}
	}
}

// runEventExchange performs one exchange against the herd message.
func runEventExchange(t *testing.T, client *Client, transport *herdTransport, message *mDNS.Msg) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := client.Exchange(ctx, transport, message, adapter.DNSQueryOptions{}, nil)
	require.NoError(t, err)
}

func TestQueryEventAttributesAnUpstreamAnswer(t *testing.T) {
	bus := NewEventBus(4)
	events, unsubscribe := bus.Subscribe(false, 0)
	defer unsubscribe()
	transport := &herdTransport{}
	client := newEventClient(t, transport, bus, true)

	runEventExchange(t, client, transport, herdMessage())
	event := receiveEvent(t, events)

	require.Equal(t, "herd", event.TransportTag, "the event must name the transport that carried the query")
	require.Equal(t, "herd.example.org.", event.Name)
	require.EqualValues(t, 1, event.QType)
	require.Equal(t, QuerySourceUpstream, event.Source)
	require.Equal(t, 0, event.RCode, "a successful answer carries its response code")
	require.Empty(t, event.Err)
	require.Empty(t, event.Answers, "answers are opt-in; the default subscriber must not receive records")
}

func TestQueryEventMarksACacheHit(t *testing.T) {
	bus := NewEventBus(4)
	events, unsubscribe := bus.Subscribe(false, 0)
	defer unsubscribe()
	transport := &herdTransport{}
	client := newEventClient(t, transport, bus, false)

	runEventExchange(t, client, transport, herdMessage())
	first := receiveEvent(t, events)
	require.Equal(t, QuerySourceUpstream, first.Source)

	runEventExchange(t, client, transport, herdMessage())
	second := receiveEvent(t, events)
	require.Equal(t, QuerySourceCache, second.Source,
		"a cache hit is a different fact from an upstream answer and must be reported as one")
	require.Equal(t, 0, second.RCode)
	require.EqualValues(t, 1, transport.queryCount.Load(), "the cached answer must not have queried upstream again")
}

func TestQueryEventReportsATransportFailure(t *testing.T) {
	bus := NewEventBus(4)
	events, unsubscribe := bus.Subscribe(false, 0)
	defer unsubscribe()
	transport := &herdTransport{failures: 1 << 30}
	client := newEventClient(t, transport, bus, true)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := client.Exchange(ctx, transport, herdMessage(), adapter.DNSQueryOptions{}, nil)
	require.Error(t, err)

	event := receiveEvent(t, events)
	require.Equal(t, QuerySourceUpstream, event.Source)
	require.Equal(t, -1, event.RCode, "no response means no response code, not NOERROR")
	require.NotEmpty(t, event.Err)
}

func TestAnswersAreOptInAndBounded(t *testing.T) {
	bus := NewEventBus(4)
	events, unsubscribe := bus.Subscribe(true, 1)
	defer unsubscribe()
	transport := &herdTransport{}
	client := newEventClient(t, transport, bus, true)

	runEventExchange(t, client, transport, herdMessage())
	event := receiveEvent(t, events)
	require.Len(t, event.Answers, 1, "an opted-in subscriber receives the record, capped at what it asked for")
	require.Contains(t, event.Answers[0], "herd.example.org.")

	// A second subscriber that wants no answers gets none from the same query.
	plain, unsubscribePlain := bus.Subscribe(false, 0)
	defer unsubscribePlain()
	runEventExchange(t, client, transport, herdMessage())
	_ = receiveEvent(t, events) // the answer-carrying subscriber
	plainEvent := receiveEvent(t, plain)
	require.Empty(t, plainEvent.Answers)
}

func TestNoSubscriberReceivesNothingAndDropsNothing(t *testing.T) {
	bus := NewEventBus(2)
	events, unsubscribe := bus.Subscribe(false, 0)
	unsubscribe()
	require.False(t, bus.Active())

	transport := &herdTransport{}
	client := newEventClient(t, transport, bus, true)
	for range 3 {
		runEventExchange(t, client, transport, herdMessage())
	}
	select {
	case event := <-events:
		t.Fatalf("a query with no active subscriber produced an event: %+v", event)
	default:
	}
	require.Zero(t, bus.Dropped(), "with nobody subscribed there is nothing to drop")
}

func TestASlowSubscriberLosesEventsInsteadOfBlockingAQuery(t *testing.T) {
	bus := NewEventBus(1)
	_, unsubscribe := bus.Subscribe(false, 0)
	defer unsubscribe()

	transport := &herdTransport{}
	client := newEventClient(t, transport, bus, true)
	for range 4 {
		runEventExchange(t, client, transport, herdMessage())
	}
	require.Positive(t, bus.Dropped(),
		"a subscriber that does not drain must lose events and the drop must be observable")
}

func TestUnsubscribeStopsDelivery(t *testing.T) {
	bus := NewEventBus(2)
	events, unsubscribe := bus.Subscribe(false, 0)
	unsubscribe()
	require.False(t, bus.Active())

	transport := &herdTransport{}
	client := newEventClient(t, transport, bus, true)
	runEventExchange(t, client, transport, herdMessage())
	select {
	case event := <-events:
		t.Fatalf("an unsubscribed channel still received an event: %+v", event)
	default:
	}
}
