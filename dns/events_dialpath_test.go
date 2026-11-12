package dns

// The dial-path projection on a query event.
//
// # What these cases are for
//
// A DNS event that names only the transport leaves the reader to guess which outbound carried it.
// The dangerous way to answer that is to read the selector when the event is CONSUMED: by then the
// group may have moved, and the answer would be credited to a node that never carried it. These
// cases drive the real Exchange path with transports that record a path the way real decision points
// do, and pin what an event says, what it refuses to say, and why.

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"

	mDNS "github.com/miekg/dns"

	"github.com/stretchr/testify/require"
)

// newPathClient builds a client with the given bus over a path transport.
func newPathClient(t *testing.T, bus *EventBus, disableCache bool) *Client {
	t.Helper()
	return NewClient(ClientOptions{
		Context:      context.Background(),
		Logger:       log.NewNOPFactory().Logger(),
		EventBus:     bus,
		DisableCache: disableCache,
	})
}

// pathTransport is a transport that records a dial path the way the real boundary and groups do.
type pathTransport struct {
	tag string
	// record is called with the recorder installed on the exchange context, when there is one.
	record func(path *adapter.DialPath)
	// nested runs a SECOND exchange inside this one, the way a transport that must resolve its own
	// server name does while its dial is in flight.
	nested func(ctx context.Context)
	// sawRecorder reports whether the exchange context carried a recorder at all.
	sawRecorder bool
}

func (t *pathTransport) Start(adapter.StartStage, *adapter.Scope) error { return nil }
func (t *pathTransport) Close() error                                   { return nil }
func (t *pathTransport) Type() string                                   { return "path" }
func (t *pathTransport) Dependencies() []string                         { return nil }
func (t *pathTransport) Reset()                                         {}

func (t *pathTransport) Tag() string {
	if t.tag != "" {
		return t.tag
	}
	return "path"
}

func (t *pathTransport) Exchange(ctx context.Context, message *mDNS.Msg) (*mDNS.Msg, error) {
	path := adapter.DialPathFromContext(ctx)
	t.sawRecorder = path != nil
	if path != nil && t.record != nil {
		t.record(path)
	}
	if t.nested != nil {
		t.nested(ctx)
	}
	return FixedResponse(message.Id, message.Question[0], []netip.Addr{netip.MustParseAddr("192.0.2.1")}, 300), nil
}

func (t *pathTransport) ExchangeAsync(ctx context.Context, message *mDNS.Msg, callback func(response *mDNS.Msg, err error)) {
	go func() {
		callback(t.Exchange(ctx, message))
	}()
}

// TestQueryEventProjectsTheDialPath pins the whole projection: the chain, the leaf, the revision and
// the absence of a reason.
func TestQueryEventProjectsTheDialPath(t *testing.T) {
	t.Parallel()

	bus := NewEventBus(4)
	events, unsubscribe := bus.Subscribe(false, 0)
	defer unsubscribe()
	transport := &pathTransport{record: func(path *adapter.DialPath) {
		path.BeginDial()
		path.Record("proxy", 0, false)
		path.Record("node-a", 7, true)
	}}
	client := newPathClient(t, bus, true)

	runPathExchange(t, client, transport, pathMessage())
	event := receiveEvent(t, events)

	require.NotNil(t, event.DialPath)
	require.Equal(t, []string{"proxy", "node-a"}, event.DialPath.Steps,
		"the chain is the detour followed by the group's choice, in decision order")
	require.Equal(t, "node-a", event.DialPath.Leaf)
	require.Equal(t, uint64(7), event.DialPath.Revision)
	require.True(t, event.DialPath.RevisionKnown)
	require.Empty(t, event.DialPath.Reason, "a complete projection carries no reason")
	require.False(t, event.DialPath.Truncated)
}

// TestQueryEventSaysWhyTheDialPathIsUnknown is the negative control: an exchange that never reached
// a new connection must say so rather than present an empty chain as a complete answer.
func TestQueryEventSaysWhyTheDialPathIsUnknown(t *testing.T) {
	t.Parallel()

	bus := NewEventBus(4)
	events, unsubscribe := bus.Subscribe(false, 0)
	defer unsubscribe()
	transport := &pathTransport{record: func(path *adapter.DialPath) {
		path.BeginDial()
		path.Record("proxy", 0, false)
	}}
	client := newPathClient(t, bus, false)

	runPathExchange(t, client, transport, pathMessage())
	first := receiveEvent(t, events)
	require.Equal(t, QuerySourceUpstream, first.Source)

	runPathExchange(t, client, transport, pathMessage())
	second := receiveEvent(t, events)
	require.Equal(t, QuerySourceCache, second.Source)
	require.NotNil(t, second.DialPath)
	require.Empty(t, second.DialPath.Steps, "a cache hit established no connection")
	require.Equal(t, string(adapter.DialPathReasonNoConnection), second.DialPath.Reason,
		"an empty path must say WHY it is empty")
}

// TestQueryEventReportsADirectDial: no configured outbound carried the connection, and the projection
// says that instead of naming the built-in direct path as an outbound.
func TestQueryEventReportsADirectDial(t *testing.T) {
	t.Parallel()

	bus := NewEventBus(4)
	events, unsubscribe := bus.Subscribe(false, 0)
	defer unsubscribe()
	transport := &pathTransport{record: func(path *adapter.DialPath) {
		path.BeginDial()
		path.MarkDirect()
	}}
	client := newPathClient(t, bus, true)

	runPathExchange(t, client, transport, pathMessage())
	event := receiveEvent(t, events)

	require.NotNil(t, event.DialPath)
	require.Empty(t, event.DialPath.Steps)
	require.Equal(t, string(adapter.DialPathReasonDirect), event.DialPath.Reason)
}

// TestNoSubscriberMeansNoDialPathRecorder is the cost control: without a subscriber there is no
// event to fill in, so no recorder is installed and the dial path pays nothing.
func TestNoSubscriberMeansNoDialPathRecorder(t *testing.T) {
	t.Parallel()

	bus := NewEventBus(4)
	transport := &pathTransport{}
	client := newPathClient(t, bus, true)

	runPathExchange(t, client, transport, pathMessage())
	require.False(t, transport.sawRecorder,
		"a process nobody is observing must not allocate a dial-path record per query")
	require.Equal(t, uint64(0), bus.Dropped())
}

// TestDialPathInAnEventIsBounded: the recorder caps the chain and the event carries the truncation,
// so a pathological graph cannot grow an event.
func TestDialPathInAnEventIsBounded(t *testing.T) {
	t.Parallel()

	bus := NewEventBus(4)
	events, unsubscribe := bus.Subscribe(false, 0)
	defer unsubscribe()
	transport := &pathTransport{record: func(path *adapter.DialPath) {
		path.BeginDial()
		for index := 0; index < 32; index++ {
			path.Record("hop", 0, false)
		}
	}}
	client := newPathClient(t, bus, true)

	runPathExchange(t, client, transport, pathMessage())
	event := receiveEvent(t, events)

	require.NotNil(t, event.DialPath)
	require.Len(t, event.DialPath.Steps, 8, "the chain is capped at the recorder's bound")
	require.True(t, event.DialPath.Truncated, "hitting the cap must be visible to a reader")
}

// TestANestedExchangeDoesNotPolluteTheOuterPath: a DNS transport that resolves its own server name
// runs a SECOND exchange while the outer dial is in flight. The nested dial must not be recorded
// into the outer exchange's path - the outer event has to describe one connection, not two.
func TestANestedExchangeDoesNotPolluteTheOuterPath(t *testing.T) {
	t.Parallel()

	bus := NewEventBus(4)
	events, unsubscribe := bus.Subscribe(false, 0)
	defer unsubscribe()

	// The inner client has no bus of its own, which is the case that used to inherit the outer
	// record instead of shadowing it.
	innerTransport := &pathTransport{tag: "inner", record: func(path *adapter.DialPath) {
		path.BeginDial()
		path.Record("nested-hop", 0, false)
	}}
	innerClient := newPathClient(t, nil, true)

	outerTransport := &pathTransport{record: func(path *adapter.DialPath) {
		path.BeginDial()
		path.Record("proxy", 0, false)
		path.Record("node-a", 2, true)
	}}
	outerClient := newPathClient(t, bus, true)

	// Wire the nested exchange into the outer one through the transport's own behaviour.
	outerTransport.nested = func(ctx context.Context) {
		_, _ = innerClient.Exchange(ctx, innerTransport, pathMessage(), adapter.DNSQueryOptions{}, nil)
	}

	runPathExchange(t, outerClient, outerTransport, pathMessage())
	event := receiveEvent(t, events)

	require.NotNil(t, event.DialPath)
	require.Equal(t, []string{"proxy", "node-a"}, event.DialPath.Steps,
		"the nested exchange's dial belongs to the nested query, not to this event")
	require.Equal(t, "node-a", event.DialPath.Leaf)
}

// TestFanOutSuppressesTheSteps: concurrent member dials write into one record, so the surviving chain
// is an accident of scheduling. The event refuses to name one and says why.
func TestFanOutSuppressesTheSteps(t *testing.T) {
	t.Parallel()

	bus := NewEventBus(4)
	events, unsubscribe := bus.Subscribe(false, 0)
	defer unsubscribe()
	transport := &pathTransport{record: func(path *adapter.DialPath) {
		path.BeginDial()
		path.Record("dns-group-member", 0, false)
		path.MarkTransportFanOut()
	}}
	client := newPathClient(t, bus, true)

	runPathExchange(t, client, transport, pathMessage())
	event := receiveEvent(t, events)

	require.NotNil(t, event.DialPath)
	require.Empty(t, event.DialPath.Steps, "a fan-out has no single path to report")
	require.Equal(t, string(adapter.DialPathReasonTransportFanOut), event.DialPath.Reason)
}

// TestTransportMemberIsProjected: for a DNS transport group, the member transport that ran the
// exchange is a separate fact from the outbound chain, and both appear.
func TestTransportMemberIsProjected(t *testing.T) {
	t.Parallel()

	bus := NewEventBus(4)
	events, unsubscribe := bus.Subscribe(false, 0)
	defer unsubscribe()
	transport := &pathTransport{tag: "dns-group", record: func(path *adapter.DialPath) {
		path.RecordTransportMember("dns-group", "hk-resolver")
		path.BeginDial()
		path.Record("proxy", 0, false)
		path.Record("node-a", 1, true)
	}}
	client := newPathClient(t, bus, true)

	runPathExchange(t, client, transport, pathMessage())
	event := receiveEvent(t, events)

	require.NotNil(t, event.DialPath)
	require.Equal(t, "dns-group", event.TransportTag)
	require.Equal(t, "hk-resolver", event.DialPath.TransportMember)
	require.Equal(t, "node-a", event.DialPath.Leaf)
}

// runPathExchange performs one exchange against a path transport.
func runPathExchange(t *testing.T, client *Client, transport *pathTransport, message *mDNS.Msg) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := client.Exchange(ctx, transport, message, adapter.DNSQueryOptions{}, nil)
	require.NoError(t, err)
}

func pathMessage() *mDNS.Msg {
	message := new(mDNS.Msg)
	message.SetQuestion("path.example.org.", mDNS.TypeA)
	return message
}
