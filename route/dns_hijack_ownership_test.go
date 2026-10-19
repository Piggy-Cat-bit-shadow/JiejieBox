package route

import (
	"context"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/require"
)

// A hijacked DNS packet must be parsed while the caller's payload is still valid.
//
// # The failure this pins
//
// The tun stacks hand HijackDNSPacket a view into a read slot that they recycle on the next
// burst. The function returns before the exchange finishes, so anything the goroutine reads out
// of that view later is a read of memory its owner may already have rewritten. The parse must
// therefore happen on the caller's goroutine, and the goroutine must receive the parsed message.
//
// # How the test makes the race deterministic
//
// The dispatch hook parks the goroutine before it touches the query. While it is parked the test
// zeroes the payload - exactly what a reused read slot does - and only then releases the
// goroutine. Before the fix the goroutine unpacks the zeroed buffer, fails, and the router never
// sees a query; after the fix the already-parsed message is what crosses the boundary, so the
// original ID, question and EDNS0 survive the overwrite.
func TestDNSPacketHijackOwnsTheQueryAcrossPayloadReuse(t *testing.T) {
	// Not parallel: the dispatch hook is a single package-level slot by design.
	recorder := newRecordingExchangeRouter()
	router := &Router{
		logger: log.NewNOPFactory().Logger(),
		dns:    recorder,
	}

	query := new(dns.Msg)
	query.SetQuestion("owned.example.", dns.TypeAAAA)
	query.Id = 0x4242
	query.SetEdns0(1232, false)
	payload, err := query.Pack()
	require.NoError(t, err)

	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	hook := func() {
		once.Do(func() { close(entered) })
		<-release
	}
	testHookDNSPacketDispatch.Store(&hook)
	t.Cleanup(func() { testHookDNSPacketDispatch.Store(nil) })

	router.HijackDNSPacket(
		context.Background(),
		payload,
		discardingPacketWriter{},
		adapter.InboundContext{
			Inbound:     "tun-in",
			InboundType: "tun",
			Network:     "udp",
			Source:      M.ParseSocksaddr("10.0.0.2:41000"),
			Destination: M.ParseSocksaddr("1.1.1.1:53"),
		},
	)

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the hijacked DNS dispatch goroutine never started")
	}

	// Recycle the payload the way sing-tun recycles a read slot on the next burst.
	for i := range payload {
		payload[i] = 0
	}
	close(release)

	select {
	case got := <-recorder.messages:
		require.Equal(t, uint16(0x4242), got.Id, "the query ID must survive the payload being recycled")
		require.Len(t, got.Question, 1)
		require.Equal(t, "owned.example.", got.Question[0].Name)
		require.Equal(t, uint16(dns.TypeAAAA), got.Question[0].Qtype)
		opt := got.IsEdns0()
		require.NotNil(t, opt, "the EDNS0 record must survive the payload being recycled")
		require.Equal(t, uint16(1232), opt.UDPSize())
	case <-time.After(2 * time.Second):
		t.Fatal("the DNS router never saw the hijacked query: the dispatch goroutine parsed the recycled payload instead of the query captured in the caller")
	}

	require.Eventually(t, func() bool {
		return router.dnsHijackInFlight.Load() == 0
	}, time.Second, time.Millisecond, "the in-flight counter must be released after the dispatch")
}

// Two queries that share one buffer must not be able to cross.
//
// This is the steady-state shape of the bug: a pool or a read slot is reused for the next query
// while the previous exchange is still in flight. It is a regression test rather than a red test:
// with the parse on the caller's goroutine the two queries are independent by construction, while
// the old code made the outcome scheduling-dependent.
func TestDNSPacketHijackParsesEachQueryBeforeTheNextReuse(t *testing.T) {
	recorder := newRecordingExchangeRouter()
	router := &Router{
		logger: log.NewNOPFactory().Logger(),
		dns:    recorder,
	}

	buffer := make([]byte, 512)
	first := new(dns.Msg)
	first.SetQuestion("first.example.", dns.TypeA)
	first.Id = 0x1001
	firstPayload, err := first.Pack()
	require.NoError(t, err)
	second := new(dns.Msg)
	second.SetQuestion("second.example.", dns.TypeHTTPS)
	second.Id = 0x2002
	secondPayload, err := second.Pack()
	require.NoError(t, err)

	copy(buffer, firstPayload)
	router.HijackDNSPacket(context.Background(), buffer[:len(firstPayload)], discardingPacketWriter{}, adapter.InboundContext{
		Network: "udp", Source: M.ParseSocksaddr("10.0.0.2:41000"), Destination: M.ParseSocksaddr("1.1.1.1:53"),
	})
	// The slot is reused before the first exchange has a chance to finish.
	for i := range buffer {
		buffer[i] = 0
	}
	copy(buffer, secondPayload)
	router.HijackDNSPacket(context.Background(), buffer[:len(secondPayload)], discardingPacketWriter{}, adapter.InboundContext{
		Network: "udp", Source: M.ParseSocksaddr("10.0.0.2:41000"), Destination: M.ParseSocksaddr("1.1.1.1:53"),
	})

	got := make(map[string]uint16, 2)
	for i := 0; i < 2; i++ {
		select {
		case message := <-recorder.messages:
			require.Len(t, message.Question, 1)
			got[message.Question[0].Name] = message.Id
		case <-time.After(2 * time.Second):
			t.Fatal("both hijacked queries must reach the DNS router")
		}
	}
	require.Equal(t, uint16(0x1001), got["first.example."])
	require.Equal(t, uint16(0x2002), got["second.example."])
}

// recordingExchangeRouter records the parsed query and completes immediately, so a test can
// inspect exactly what crossed the goroutine boundary.
type recordingExchangeRouter struct {
	messages chan *dns.Msg
}

func newRecordingExchangeRouter() *recordingExchangeRouter {
	return &recordingExchangeRouter{messages: make(chan *dns.Msg, 8)}
}

func (r *recordingExchangeRouter) ExchangeAsync(_ context.Context, message *dns.Msg, _ adapter.DNSQueryOptions, callback func(response *dns.Msg, err error)) {
	r.messages <- message
	callback(nil, E.New("recording exchange completed"))
}

func (r *recordingExchangeRouter) Exchange(ctx context.Context, message *dns.Msg, options adapter.DNSQueryOptions) (*dns.Msg, error) {
	r.ExchangeAsync(ctx, message, options, func(response *dns.Msg, err error) {})
	return nil, E.New("not used")
}

func (r *recordingExchangeRouter) Lookup(ctx context.Context, domain string, options adapter.DNSQueryOptions) ([]netip.Addr, error) {
	return nil, E.New("not used")
}

func (r *recordingExchangeRouter) ClearCache()   {}
func (r *recordingExchangeRouter) ResetNetwork() {}

func (r *recordingExchangeRouter) LookupReverseMapping(ip netip.Addr) (string, bool) {
	return "", false
}

func (r *recordingExchangeRouter) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	return nil
}

func (r *recordingExchangeRouter) Close() error { return nil }
