package route

// P04 — the DNS half of the on-demand completion must look up the DNS QUERY's own socket tuple.
//
// A hijacked DNS query arrives with the client's source and the DNS server as the destination.
// dispatchDNSPacket clears metadata.Destination before handing the query to the DNS router (the
// destination of a DNS query is not a connection target), so the completion triggered inside the
// DNS rule walk had no destination to look the socket up with: processCacheQuery fell back to an
// invalid AddrPort and the searcher was asked about a socket that does not exist. The owner-only
// lookup that runs first still had the tuple, which is why only the completion was wrong.
//
// The fix keeps the tuple on OriginDestination - the field that already means "the address the
// client intended", which the process lookup prefers - so both lookups name the same socket.

import (
	"context"
	"io"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/contrab/freelru"
	"github.com/sagernet/sing/contrab/maphash"
	"github.com/sagernet/sing/service"

	mDNS "github.com/miekg/dns"

	"github.com/stretchr/testify/require"
)

// tupleRecordingSearcher records the tuple the completion asked about.
type tupleRecordingSearcher struct {
	lastDestination atomic.Pointer[netip.AddrPort]
}

func (s *tupleRecordingSearcher) FindProcessInfo(_ context.Context, _ string, _ netip.AddrPort, destination netip.AddrPort) (*adapter.ConnectionOwner, error) {
	recorded := destination
	s.lastDestination.Store(&recorded)
	return &adapter.ConnectionOwner{UserId: 1000, ProcessPaths: []string{"/usr/bin/curl"}}, nil
}

func (s *tupleRecordingSearcher) FindProcessOwner(context.Context, string, netip.AddrPort, netip.AddrPort) (*adapter.ConnectionOwner, error) {
	return &adapter.ConnectionOwner{UserId: 1000}, nil
}

func (s *tupleRecordingSearcher) Close() error { return nil }
func (s *tupleRecordingSearcher) ResetCache()  {}

// capturingDNSRouter stands in for the DNS router: it records the metadata it was handed and runs
// the completer route installed, exactly as the DNS rule walk does when it reaches a path rule.
type capturingDNSRouter struct {
	adapter.DNSRouter
	completer func(ctx context.Context, metadata *adapter.InboundContext)
	seen      adapter.InboundContext
	queries   atomic.Int32
}

func (r *capturingDNSRouter) SetProcessPathCompleter(complete func(ctx context.Context, metadata *adapter.InboundContext)) {
	r.completer = complete
}

func (r *capturingDNSRouter) ExchangeAsync(ctx context.Context, _ *mDNS.Msg, _ adapter.DNSQueryOptions, callback func(*mDNS.Msg, error)) {
	r.queries.Add(1)
	metadata := adapter.ContextFrom(ctx)
	if metadata != nil {
		r.seen = *metadata
		if r.completer != nil {
			copy := *metadata
			r.completer(ctx, &copy)
		}
	}
	callback(nil, E.New("test stopped the exchange"))
}

// scriptedDNSConn serves one length-prefixed DNS request and then EOF, which is what the TCP DNS
// stream handler consumes.
type scriptedDNSConn struct {
	net.Conn
	payload []byte
	offset  int
}

func (c *scriptedDNSConn) Read(p []byte) (int, error) {
	if c.offset >= len(c.payload) {
		return 0, io.EOF
	}
	n := copy(p, c.payload[c.offset:])
	c.offset += n
	return n, nil
}

func (c *scriptedDNSConn) Write(p []byte) (int, error) { return len(p), nil }
func (c *scriptedDNSConn) Close() error                { return nil }
func (c *scriptedDNSConn) LocalAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 53}
}
func (c *scriptedDNSConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(192, 168, 1, 2), Port: 40001}
}
func (c *scriptedDNSConn) SetDeadline(time.Time) error      { return nil }
func (c *scriptedDNSConn) SetReadDeadline(time.Time) error  { return nil }
func (c *scriptedDNSConn) SetWriteDeadline(time.Time) error { return nil }

// framedDNSQuery returns the TCP DNS framing: a two-byte length followed by the packed message.
func framedDNSQuery(t *testing.T, message *mDNS.Msg) []byte {
	t.Helper()
	packed, err := message.Pack()
	require.NoError(t, err)
	framed := make([]byte, 2+len(packed))
	framed[0] = byte(len(packed) >> 8)
	framed[1] = byte(len(packed))
	copy(framed[2:], packed)
	return framed
}

// newTupleTestRouter builds a route router whose DNS router is the capture, with a real process
// cache and the local-source platform the lookup requires.
func newTupleTestRouter(t *testing.T, searcher *tupleRecordingSearcher) (*Router, *capturingDNSRouter) {
	t.Helper()
	dnsRouter := &capturingDNSRouter{}
	ctx := service.ContextWith[adapter.DNSRouter](context.Background(), dnsRouter)
	router := NewRouter(ctx, log.NewNOPFactory(), option.RouteOptions{}, option.DNSOptions{})
	router.ctx = context.Background()
	router.processSearcher = searcher
	router.platformInterface = &localSourcePlatform{address: netip.MustParseAddr("192.168.1.2")}
	router.processCache = common.Must1(freelru.New[processCacheKey, processCacheEntry](
		256, maphash.NewHasher[processCacheKey]().Hash32, true))
	router.processCache.SetLifetime(time.Second)
	return router, dnsRouter
}

// TestTheCompletionLooksUpTheDNSQueryTuple is the regression.
func TestTheCompletionLooksUpTheDNSQueryTuple(t *testing.T) {
	searcher := &tupleRecordingSearcher{}
	router, dnsRouter := newTupleTestRouter(t, searcher)

	message := &mDNS.Msg{
		MsgHdr:   mDNS.MsgHdr{Id: 1, RecursionDesired: true},
		Question: []mDNS.Question{{Name: "race.example.org.", Qtype: mDNS.TypeA, Qclass: mDNS.ClassINET}},
	}
	metadata := adapter.InboundContext{
		InboundType: C.TypeTun,
		Network:     N.NetworkUDP,
		Source:      M.SocksaddrFrom(netip.MustParseAddr("192.168.1.2"), 40000),
		Destination: M.SocksaddrFrom(netip.MustParseAddr("8.8.8.8"), 53),
	}
	router.dispatchDNSPacket(context.Background(), message, discardingPacketWriter{}, metadata)

	require.EqualValues(t, 1, dnsRouter.queries.Load(), "the query reached the DNS router")
	require.Equal(t, M.Socksaddr{}, dnsRouter.seen.Destination,
		"the DNS query's Destination is cleared, as before: it is not a connection target")
	require.True(t, dnsRouter.seen.OriginDestination.IsValid(),
		"the original socket tuple must survive for the process lookup")
	require.Equal(t, "8.8.8.8:53", dnsRouter.seen.OriginDestination.String())

	recorded := searcher.lastDestination.Load()
	require.NotNil(t, recorded, "the completion ran the full lookup")
	require.Equal(t, "8.8.8.8:53", recorded.String(),
		"the completion must look up the socket the client opened, not an empty destination")
}

// TestTheStreamCompletionLooksUpTheDNSQueryTuple is the same contract for the TCP hijack.
func TestTheStreamCompletionLooksUpTheDNSQueryTuple(t *testing.T) {
	searcher := &tupleRecordingSearcher{}
	router, dnsRouter := newTupleTestRouter(t, searcher)

	metadata := adapter.InboundContext{
		InboundType: C.TypeTun,
		Protocol:    C.ProtocolDNS,
		Network:     N.NetworkTCP,
		Source:      M.SocksaddrFrom(netip.MustParseAddr("192.168.1.2"), 40001),
		Destination: M.SocksaddrFrom(netip.MustParseAddr("8.8.4.4"), 53),
	}
	message := &mDNS.Msg{
		MsgHdr:   mDNS.MsgHdr{Id: 2, RecursionDesired: true},
		Question: []mDNS.Question{{Name: "race.example.org.", Qtype: mDNS.TypeA, Qclass: mDNS.ClassINET}},
	}
	_ = router.hijackDNSStream(context.Background(), &scriptedDNSConn{payload: framedDNSQuery(t, message)}, metadata)

	require.EqualValues(t, 1, dnsRouter.queries.Load(), "the stream handler reached the DNS router")
	require.True(t, dnsRouter.seen.OriginDestination.IsValid(),
		"the TCP hijack must keep the tuple too")
	require.Equal(t, "8.8.4.4:53", dnsRouter.seen.OriginDestination.String())

	recorded := searcher.lastDestination.Load()
	require.NotNil(t, recorded)
	require.Equal(t, "8.8.4.4:53", recorded.String())
}
