package dns

// The joint chain the three-system rounds kept recording as NOT_COVERED (audit quadruple B, Q05): a
// REAL DNS cache answer feeding the REAL dual-stack dialer, across a network-generation change, with
// the dial landing on a real socket.
//
// The halves were covered separately - dns owns the cache namespace and the answer stamp; common/dialer
// owns the candidate plan and the transition interleaving (through a scripted resolver) - and the
// joint was not, because `dns` imports `common/dialer`, so the harness can only live here.
//
// What the test observes is the only thing that matters: which loopback address the dial actually
// connects to. The scripted transport answers 127.0.0.1 before the change and 127.0.0.2 after it, and
// two listeners on one port record which address accepted.
//
// The positive control is the first arm: without a generation change the second dial must be served
// from the cache (no new A query, the same listener). Without that arm, "it dialed the new address"
// could be true of a cache that is not in the loop at all.
//
// A-family queries are counted separately because the dialer asks for both families: an empty AAAA
// answer is not cached, so counting every exchange would grow by one per dial no matter what the A
// cache did.

import (
	"context"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/dialer"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/service"

	mDNS "github.com/miekg/dns"
	cControl "github.com/sagernet/sing/common/control"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/stretchr/testify/require"
)

// candidateTransport answers A records from a switchable address and counts A queries.
type candidateTransport struct {
	adapter.DNSTransport
	access   sync.Mutex
	address  netip.Addr
	aQueries int
}

func (t *candidateTransport) Type() string                                   { return "candidate" }
func (t *candidateTransport) Tag() string                                    { return "candidate" }
func (t *candidateTransport) Reset()                                         {}
func (t *candidateTransport) Close() error                                   { return nil }
func (t *candidateTransport) Dependencies() []string                         { return nil }
func (t *candidateTransport) Start(adapter.StartStage, *adapter.Scope) error { return nil }

func (t *candidateTransport) setAddress(address netip.Addr) {
	t.access.Lock()
	t.address = address
	t.access.Unlock()
}

func (t *candidateTransport) aQueryCount() int {
	t.access.Lock()
	defer t.access.Unlock()
	return t.aQueries
}

func (t *candidateTransport) Exchange(ctx context.Context, message *mDNS.Msg) (*mDNS.Msg, error) {
	t.access.Lock()
	address := t.address
	question := message.Question[0]
	if question.Qtype == mDNS.TypeA {
		t.aQueries++
	}
	t.access.Unlock()
	response := new(mDNS.Msg)
	response.SetReply(message)
	if question.Qtype == mDNS.TypeA {
		response.Answer = append(response.Answer, &mDNS.A{
			Hdr: mDNS.RR_Header{Name: question.Name, Rrtype: mDNS.TypeA, Class: mDNS.ClassINET, Ttl: 3600},
			A:   address.AsSlice(),
		})
	}
	return response, nil
}

func (t *candidateTransport) ExchangeAsync(ctx context.Context, message *mDNS.Msg, callback func(*mDNS.Msg, error)) {
	go callback(t.Exchange(ctx, message))
}

// candidateNetworkManager answers only the questions the DNS client asks of it. The embedded interface
// leaves the rest unimplemented, so an unexpected call fails loudly instead of silently doing nothing.
type candidateNetworkManager struct {
	adapter.NetworkManager
	environment *atomic.Uint64
}

func (m *candidateNetworkManager) NetworkEnvironment() uint64 { return m.environment.Load() }

// InterfaceFinder is read by the dialer's constructor in production; this test's dialer has no manager.
func (m *candidateNetworkManager) InterfaceFinder() cControl.InterfaceFinder {
	return cControl.NewDefaultInterfaceFinder()
}

func (m *candidateNetworkManager) DefaultOptions() adapter.NetworkOptions {
	return adapter.NetworkOptions{}
}

func (m *candidateNetworkManager) AutoDetectInterface() bool { return false }

// candidateRouter is the adapter.DNSRouter the dialer asks: it forwards into the real Client cache path.
type candidateRouter struct {
	adapter.DNSRouter
	client    *Client
	transport adapter.DNSTransport
}

func (r *candidateRouter) Lookup(ctx context.Context, domain string, options adapter.DNSQueryOptions) ([]netip.Addr, error) {
	return r.client.Lookup(ctx, r.transport, domain, options, nil)
}

// candidateListener records every accepted connection.
type candidateListener struct {
	listener net.Listener
	accepted atomic.Int32
}

func startCandidateListener(t *testing.T, address string) *candidateListener {
	t.Helper()
	listener, err := net.Listen("tcp", address)
	require.NoError(t, err)
	recording := &candidateListener{listener: listener}
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			recording.accepted.Add(1)
			_ = conn.Close()
		}
	}()
	t.Cleanup(func() { _ = listener.Close() })
	return recording
}

func (l *candidateListener) port() int { return l.listener.Addr().(*net.TCPAddr).Port }

func TestTheRealCacheFeedsTheRealDialerAcrossANetworkChange(t *testing.T) {
	// Two loopback addresses on ONE port: the destination is a domain plus a port, so the address the
	// resolver returns decides which listener the dial lands on.
	firstListener := startCandidateListener(t, "127.0.0.1:0")
	port := firstListener.port()
	secondListener := startCandidateListener(t, net.JoinHostPort("127.0.0.2", strconv.Itoa(port)))
	require.Equal(t, port, secondListener.port())

	generation := &atomic.Uint64{}
	generation.Store(1)
	transport := &candidateTransport{address: netip.MustParseAddr("127.0.0.1")}
	// The client reads its environment fingerprint from the network manager, exactly as the router
	// wires it. The dialer gets a context WITHOUT a manager: this test dials directly, and a nil manager
	// is the dialer's own supported path (it falls back to the default interface finder), so the fake
	// only has to answer the one question the client asks.
	managerContext := service.ContextWith[adapter.NetworkManager](context.Background(),
		&candidateNetworkManager{environment: generation})
	dnsClient := NewClient(ClientOptions{
		Context:           managerContext,
		Logger:            log.NewNOPFactory().NewLogger("joint-candidate"),
		NetworkGeneration: func() uint64 { return generation.Load() },
	})
	dnsClient.Start()

	ctx := service.ContextWith[adapter.DNSRouter](context.Background(),
		&candidateRouter{client: dnsClient, transport: transport})
	dialerInstance, err := dialer.NewWithOptions(dialer.Options{
		Context:        ctx,
		Options:        option.DialerOptions{},
		RemoteIsDomain: true,
	})
	require.NoError(t, err)

	destination := M.ParseSocksaddrHostPort("joint.test", uint16(port))
	dial := func() {
		t.Helper()
		conn, dialErr := dialerInstance.DialContext(context.Background(), "tcp", destination)
		require.NoError(t, dialErr)
		_ = conn.Close()
	}

	// Arm 1 - the cache is in the loop. The first dial resolves and lands on the first listener; the
	// second dial, with no generation change, must be served from the cache: same listener, no new A
	// query.
	dial()
	require.Eventually(t, func() bool { return firstListener.accepted.Load() == 1 }, 5*time.Second, time.Millisecond)
	aQueriesAfterFirst := transport.aQueryCount()
	require.NotZero(t, aQueriesAfterFirst,
		"the first dial must have resolved, or the cache control below is meaningless")
	dial()
	require.Eventually(t, func() bool { return firstListener.accepted.Load() == 2 }, 5*time.Second, time.Millisecond)
	require.Equal(t, aQueriesAfterFirst, transport.aQueryCount(),
		"the second dial re-resolved without a network change: the cache is not in this chain and the test proves nothing")

	// Arm 2 - the transition. The answer changes, the generation advances and the client's transport
	// pins move to it, which is what the router's ResetNetwork does. The next dial must land on the
	// SECOND listener, which can only happen if the cache did not serve the old-generation answer.
	transport.setAddress(netip.MustParseAddr("127.0.0.2"))
	generation.Add(1)
	dnsClient.refreshTransportEnvironments()
	dial()
	require.Eventually(t, func() bool { return secondListener.accepted.Load() == 1 }, 5*time.Second, time.Millisecond,
		"the dial after the network change landed on the old address: a cached answer crossed the generation boundary")
	require.Greater(t, transport.aQueryCount(), aQueriesAfterFirst,
		"the post-change dial must have re-resolved in the new environment, not reused the old answer")
	require.EqualValues(t, 2, firstListener.accepted.Load(),
		"the old-generation listener must not receive the post-change dial")
}
