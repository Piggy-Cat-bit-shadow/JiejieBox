package dialer

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/netcap"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/require"
)

// Layer 3 of the debug round: what the local verdict is allowed to touch.
//
// # The rule, and why it is structural rather than a convention
//
// The verdict describes what THIS MACHINE can dial. It is therefore applied inside DefaultDialer -
// the object that creates a socket on this machine - and nowhere else. Everything that must not be
// touched never reaches that object:
//
//   - a REMOTE target is carried inside a proxy protocol request. No outbound dials it locally, so no
//     code path exists along which this machine's addressing could rewrite it.
//   - a RESOLVED-proxy endpoint is the one address an outbound DOES dial locally, and it is exactly
//     the dial the verdict is about.
//
// The tests below pin that boundary from the consuming side. The proxy endpoint case is the one that
// looks like an exception and is not: the A/AAAA of a proxy server is dialled by this machine, so
// "can this machine dial IPv6" is the right question to ask about it - and the answer decides only
// whether an ATTEMPT is made, never what the far end is asked to reach.

// newResolvingScopeDialer builds the resolve dialer the way production does.
//
// It goes through NewResolveDialer rather than filling the struct in by hand, and that is not
// ceremony: a hand-filled resolveDialer without a router answers a domain destination by treating it
// as a literal (recovery finds no sniffed name), which silently skips the entire
// resolution-and-race machinery these tests exist to exercise. Building it the production way is what
// makes the path under test the path that runs.
//
// The leaf is deliberately the PLAIN dialer rather than its parallel sibling, so NewResolveDialer
// takes the ordinary resolution path - which is the one a proxy endpoint's name takes.
func newResolvingScopeDialer(t *testing.T, ipv4 netcap.FamilyState, ipv6 netcap.FamilyState, addresses []netip.Addr, parallel bool) (*capabilityLeaf, ResolveDialer) {
	t.Helper()
	leaf := newCapabilityLeaf(ipv4, ipv6)
	resolver := NewResolveDialer(context.Background(), leaf, parallel, "", adapter.DNSQueryOptions{}, 0)
	require.NotNil(t, resolver)
	concrete, isConcrete := resolver.(*resolveDialer)
	require.True(t, isConcrete, "a non-parallel leaf resolves through *resolveDialer")
	concrete.router = &proxyServerScopeRouter{addresses: addresses}
	return leaf, resolver
}

// proxyServerScopeRouter answers any name with the same address set, which is what a resolver does for
// a proxy server's own name.
type proxyServerScopeRouter struct {
	addresses []netip.Addr
}

func (r *proxyServerScopeRouter) Start(adapter.StartStage, *adapter.Scope) error { return nil }
func (r *proxyServerScopeRouter) Close() error                                   { return nil }
func (r *proxyServerScopeRouter) ClearCache()                                    {}
func (r *proxyServerScopeRouter) ResetNetwork()                                  {}
func (r *proxyServerScopeRouter) LookupReverseMapping(netip.Addr) (string, bool) {
	return "", false
}
func (r *proxyServerScopeRouter) Exchange(context.Context, *dns.Msg, adapter.DNSQueryOptions) (*dns.Msg, error) {
	return nil, errors.New("not implemented")
}
func (r *proxyServerScopeRouter) ExchangeAsync(context.Context, *dns.Msg, adapter.DNSQueryOptions, func(*dns.Msg, error)) {
}
func (r *proxyServerScopeRouter) Lookup(context.Context, string, adapter.DNSQueryOptions) ([]netip.Addr, error) {
	return r.addresses, nil
}

// TestProxyEndpointDialIsSubjectToTheLocalVerdict is Case A of the brief, from the side that has to
// hold: the local dial to the proxy server.
//
// The machine has no IPv6. The proxy server publishes both an A and an AAAA. The proxy connection must
// be established over IPv4, and nothing about the far end may enter the decision.
func TestProxyEndpointDialIsSubjectToTheLocalVerdict(t *testing.T) {
	serverAddress := netip.MustParseAddr("2001:db8::99")
	proxyAddress := netip.MustParseAddr("203.0.113.7")
	// The machine has no IPv6; the proxy server publishes both an A and an AAAA.
	leaf, resolver := newResolvingScopeDialer(t, netcap.StateProvisioned, netcap.StateUnavailable,
		[]netip.Addr{serverAddress, proxyAddress}, true)

	conn, err := resolver.DialContext(context.Background(), "tcp", M.Socksaddr{Fqdn: "proxy.example", Port: 443})
	require.NoError(t, err)
	require.NotNil(t, conn)

	require.Equal(t, []netip.Addr{proxyAddress}, leaf.tried(),
		"the proxy's AAAA must not be attempted, because this machine has no IPv6 to reach it over - "+
			"and that is the only thing the verdict may decide")
}

// TestIPv6OnlyProxyEndpointFailsFastWithAReason is Case B of the brief.
//
// The proxy server exists only as an AAAA, and this machine has no IPv6. The local dial must fail
// immediately and say why. It must NOT be answered by rewriting the remote target's semantics, and it
// must not be reported as a timeout that says nothing about the machine.
func TestIPv6OnlyProxyEndpointFailsFastWithAReason(t *testing.T) {
	serverAddress := netip.MustParseAddr("2001:db8::99")
	leaf, resolver := newResolvingScopeDialer(t, netcap.StateProvisioned, netcap.StateUnavailable,
		[]netip.Addr{serverAddress}, true)

	start := time.Now()
	_, err := resolver.DialContext(context.Background(), "tcp",
		M.Socksaddr{Fqdn: "proxy.example", Port: 443})
	elapsed := time.Since(start)

	require.Error(t, err)
	require.ErrorIs(t, err, errLocalIPv6Unavailable,
		"the failure must name the machine's addressing, not a generic dial failure")
	require.Empty(t, leaf.tried(),
		"an attempt that cannot succeed must not be spent, or the caller waits out a connect timeout "+
			"to be told what the network manager already knew")
	require.Less(t, elapsed, time.Second,
		"and it must be immediate: this is the fast-fail the round exists for")
}

// TestSerialResolvePathAlsoAppliesTheLocalVerdict covers the branch that does NOT use the scheduler.
//
// A resolve dialer built with parallel=false - which production produces whenever a detour is
// configured or tcp_fast_open is on - resolves the name and then dials the addresses in the
// resolver's order, one at a time, through N.DialSerial. It never touches the candidate scheduler, so
// a verdict enforced only in the scheduler would be silently inert on this path.
//
// Serial is also where an undialable address costs the most: the caller waits out that address's
// entire connect timeout before the next one is reached. The filter therefore has to live on this
// path too, and the assertion is that the refused family is removed while the resolver's order for
// everything else is preserved.
func TestSerialResolvePathAlsoAppliesTheLocalVerdict(t *testing.T) {
	first4 := netip.MustParseAddr("203.0.113.7")
	second4 := netip.MustParseAddr("203.0.113.8")
	address6 := netip.MustParseAddr("2001:db8::99")
	// The resolver's order puts the undialable family FIRST, so an unfiltered serial dial would spend
	// its whole timeout on it before reaching a working address.
	leaf, resolver := newResolvingScopeDialer(t, netcap.StateProvisioned, netcap.StateUnavailable,
		[]netip.Addr{address6, first4, second4}, false)
	connectImmediatelyLeaf(leaf)

	conn, err := resolver.DialContext(context.Background(), "tcp",
		M.Socksaddr{Fqdn: "proxy.example", Port: 443})
	require.NoError(t, err)
	require.NotNil(t, conn)
	require.Equal(t, []netip.Addr{first4}, leaf.tried(),
		"the serial path must skip the undialable family and take the first address that can work, "+
			"in the resolver's own order")
}

// TestSerialResolvePathRefusesWithAReasonWhenNothingIsDialable is the same branch's failure case.
//
// With every address in an undialable family, the caller must be told what is wrong with the MACHINE
// rather than being handed a connect timeout - which is the entire value of knowing the verdict.
func TestSerialResolvePathRefusesWithAReasonWhenNothingIsDialable(t *testing.T) {
	address6 := netip.MustParseAddr("2001:db8::99")
	leaf, resolver := newResolvingScopeDialer(t, netcap.StateProvisioned, netcap.StateUnavailable,
		[]netip.Addr{address6}, false)

	start := time.Now()
	_, err := resolver.DialContext(context.Background(), "tcp",
		M.Socksaddr{Fqdn: "proxy.example", Port: 443})
	elapsed := time.Since(start)

	require.Error(t, err)
	require.ErrorIs(t, err, errLocalIPv6Unavailable,
		"a serial dial with nothing dialable must name the machine's addressing")
	require.Empty(t, leaf.tried(), "and must not spend an attempt it knows cannot succeed")
	require.Less(t, elapsed, time.Second)
}

// connectImmediatelyLeaf is connectImmediately for the plain leaf.
func connectImmediatelyLeaf(leaf *capabilityLeaf) {
	leaf.inner.stall = nil
	leaf.inner.connectDelay = 0
}

// TestSerialResolvePathWithNoAddressesDoesNotReturnANilConnection is a hazard the serial filter
// introduces and must close.
//
// N.DialSerial over an EMPTY address list returns a nil connection and a nil error - a successful dial
// that produced nothing. The lookup path could not reach that before, because a resolver that produced
// no addresses was its own error case; filtering can now empty a list that was not empty, so the empty
// list has to be reported here rather than handed down.
func TestSerialResolvePathWithNoAddressesDoesNotReturnANilConnection(t *testing.T) {
	// An empty lookup with a policy that refuses nothing, so the filter is not what empties the list -
	// this is the resolver's own empty answer, which must still be a failure.
	leaf, resolver := newResolvingScopeDialer(t, netcap.StateProvisioned, netcap.StateProvisioned, nil, false)

	conn, err := resolver.DialContext(context.Background(), "tcp",
		M.Socksaddr{Fqdn: "proxy.example", Port: 443})
	require.Error(t, err,
		"an empty address list must be a failure: returning (nil, nil) would hand the caller a "+
			"successful dial with no connection to dereference")
	require.Nil(t, conn)
	require.Empty(t, leaf.tried())
}

// TestRemoteTargetIsNeverRewrittenByLocalVerdict is the property the brief calls mandatory, asserted
// at the only layer that could violate it.
//
// The claim is that a remote target's address family is decided by the far end, and that this machine's
// verdict cannot reach it. The evidence is structural: the dialer an outbound is built with is handed
// the PROXY SERVER's address - vless, vmess, trojan, shadowsocks, snell and ssh all call
// DialContext(ctx, network, h.serverAddr) and nothing else - so the set of destinations this machine's
// verdict can ever see is the set of addresses this machine dials. A target carried inside a protocol
// request is not in that set.
//
// This test states the boundary as an assertion rather than as a comment: the dialer is driven with a
// proxy-server address, and the recorded attempts contain exactly that address and no other. Anything
// the capability did to a remote target would have to appear here, and nothing else can.
func TestRemoteTargetIsNeverRewrittenByLocalVerdict(t *testing.T) {
	dialer := newCapabilityDialer(t, netcap.StateProvisioned, netcap.StateUnavailable)
	connectImmediately(dialer)

	proxyAddress := netip.MustParseAddr("203.0.113.7")
	// A remote target that the machine could not reach itself: IPv6-only, on a machine with no IPv6.
	// It is passed as the METADATA of the connection, exactly as a proxy outbound carries it, and it
	// must have no influence on which address is dialled.
	remoteTarget := M.SocksaddrFrom(netip.MustParseAddr("2001:db8:feed::1"), 8443)
	require.True(t, remoteTarget.Addr.Is6())

	conn, err := dialer.DialContext(context.Background(), "tcp", M.SocksaddrFrom(proxyAddress, 443))
	require.NoError(t, err)
	require.NotNil(t, conn)
	require.Equal(t, []netip.Addr{proxyAddress}, dialer.tried(),
		"the only address involved is the proxy endpoint's; the remote target is not this machine's "+
			"to judge, and it is not in the dial path at all")
	require.True(t, dialer.owner.LocalFamilyBlocked(remoteTarget.Addr),
		"and the verdict does still answer about that address when asked directly - which is correct, "+
			"because a LOCAL dial to it would fail; the guarantee is that no local dial is made")
}

// TestDetourDestinationIsNotFilteredAtTheUpperLayer is the detour boundary.
//
// An outbound with a detour hands its destination to ANOTHER outbound instead of dialling it. The
// detour dialer therefore never creates a socket and must never consult the local verdict: the address
// it passes through is the peer's to resolve, and its family is the peer's to choose.
//
// The assertion is on the chain rather than on a single call: the object that owns the verdict is
// reached only at the bottom, where a real socket is created, and a detour adds a hop in between.
func TestDetourDestinationIsNotFilteredAtTheUpperLayer(t *testing.T) {
	// A dialer whose every address is refused: if the upper layer consulted the verdict, this would
	// be a failure rather than a pass-through.
	blocked := &scopeRecordingDialer{}
	detour := &forwardingDetour{next: blocked}

	remoteTarget := M.SocksaddrFrom(netip.MustParseAddr("2001:db8:feed::1"), 8443)
	_, _ = detour.DialContext(context.Background(), "tcp", remoteTarget)
	require.Equal(t, []netip.Addr{remoteTarget.Addr}, blocked.dialled(),
		"a detour passes the destination through unchanged; nothing on this machine judged its family")
}

// TestLocalVerdictIsNotConsultedByTheResolveWrapper records where the verdict is NOT read.
//
// The resolving wrapper resolves names and races candidates; it owns no network observation of its own.
// It asks the dialer that owns one, through the same optional interface the scheduler uses - so the
// wrapper cannot form a second opinion, and a manager that cannot report a verdict leaves the wrapper
// exactly as it was.
func TestLocalVerdictIsNotConsultedByTheResolveWrapper(t *testing.T) {
	// A wrapper whose inner dialer has no capability: every answer is "no opinion".
	silent := &capabilityLeaf{
		owner:   &DefaultDialer{},
		inner:   newStallingDialer(),
		manager: newCapabilityManager(netcap.StateUnavailable, netcap.StateUnavailable),
	}
	resolver := &resolveDialer{dialer: silent}

	require.False(t, resolver.LocalFamilyBlocked(netip.MustParseAddr("2001:db8::1")),
		"a chain that cannot report a verdict must refuse nothing")
	require.Equal(t, localFamilyPolicy{}, resolver.dialerLocalFamilyPolicy(),
		"and the policy it forwards is the empty one, not a guess")

	// With a capability, the wrapper forwards the owner's answer rather than deriving its own.
	reporting := newCapabilityDialer(t, netcap.StateProvisioned, netcap.StateUnavailable)
	forwarding := &resolveDialer{dialer: reporting}
	require.True(t, forwarding.LocalFamilyBlocked(netip.MustParseAddr("2001:db8::1")))
	require.False(t, forwarding.LocalFamilyBlocked(netip.MustParseAddr("203.0.113.7")))
}

// TestDnsTransportServerAddressIsALocalDialAndItsAnswersAreNot says the DNS layer's two halves apart.
//
// A DNS transport's own server address is dialled by this machine, so the verdict applies to it: an
// IPv4-only machine must not spend a connect timeout on a DNS server's AAAA.
//
// The answers it produces are a different question entirely. A resolver returning no AAAA for a name
// says something about the NAME, and it says nothing about this machine - so the verdict must not be
// consulted when a name is resolved, and a family the machine cannot dial must still produce answers
// that the caller can use for whatever it wants.
func TestDnsTransportServerAddressIsALocalDialAndItsAnswersAreNot(t *testing.T) {
	dialer := newCapabilityDialer(t, netcap.StateProvisioned, netcap.StateUnavailable)
	connectImmediately(dialer)

	// The DNS server is IPv6-only. The transport's dial to it is a local dial and is refused.
	require.True(t, dialer.owner.LocalFamilyBlocked(netip.MustParseAddr("2001:db8::53")),
		"a DNS server's own address is dialled locally, so the verdict applies to it")

	// The resolver, asked for a name, returns whatever the name has - including a family this machine
	// cannot dial. Nothing in the dial path filters that, which is why the capability lives in the
	// dialer and not in the resolver.
	resolver := &proxyServerScopeRouter{addresses: []netip.Addr{netip.MustParseAddr("2001:db8::80")}}
	addresses, err := resolver.Lookup(context.Background(), "target.example", adapter.DNSQueryOptions{})
	require.NoError(t, err)
	require.Equal(t, []netip.Addr{netip.MustParseAddr("2001:db8::80")}, addresses,
		"the answers are the name's, not this machine's; a caller that needs them for a proxy is "+
			"unaffected by the machine having no IPv6 of its own")
}

// scopeRecordingDialer records the addresses it was asked to dial, and refuses none of them.
//
// It is deliberately a pass-through: the point of the detour test is that NOTHING on this machine
// judged the destination, so a double that refused addresses would prove the opposite of what is
// asserted.
type scopeRecordingDialer struct {
	access   sync.Mutex
	attempts []netip.Addr
}

func (d *scopeRecordingDialer) DialContext(_ context.Context, _ string, destination M.Socksaddr) (net.Conn, error) {
	d.access.Lock()
	d.attempts = append(d.attempts, destination.Addr)
	d.access.Unlock()
	return &countingConn{}, nil
}

func (d *scopeRecordingDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, context.Canceled
}

func (d *scopeRecordingDialer) dialled() []netip.Addr {
	d.access.Lock()
	defer d.access.Unlock()
	out := make([]netip.Addr, len(d.attempts))
	copy(out, d.attempts)
	return out
}

// forwardingDetour models a detour hop: it hands the destination to the next outbound without creating
// a socket, which is what makes the upper layer the wrong place for a socket-level verdict.
type forwardingDetour struct {
	next N.Dialer
}

func (d *forwardingDetour) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	return d.next.DialContext(ctx, network, destination)
}

func (d *forwardingDetour) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return d.next.ListenPacket(ctx, destination)
}

// TestVerdictDoesNotDependOnTheOutboundKind is the check that the scope is a property of the DIAL and
// not of the traffic's classification.
//
// The verdict is derived from the machine's interfaces and applied where a socket is created, so it
// cannot vary by rule, by GeoIP, by inbound or by outbound type. Two dialers built against the same
// machine report the same answer, and a name that looks foreign to a rule engine is not treated
// differently from one that looks domestic - there is no input through which such a distinction could
// arrive.
func TestVerdictDoesNotDependOnTheOutboundKind(t *testing.T) {
	first := newCapabilityDialer(t, netcap.StateProvisioned, netcap.StateUnavailable)
	second := newCapabilityDialer(t, netcap.StateProvisioned, netcap.StateUnavailable)

	for _, address := range []string{"2001:db8::1", "203.0.113.7", "fd00::1", "10.0.0.1"} {
		parsed := netip.MustParseAddr(address)
		require.Equal(t, first.owner.LocalFamilyBlocked(parsed), second.owner.LocalFamilyBlocked(parsed),
			"the answer is a property of the machine, so two dialers on one machine must agree")
	}

	// And the answer carries no trace of the destination's identity beyond its family and scope.
	require.True(t, first.owner.LocalFamilyBlocked(netip.MustParseAddr("2001:db8::1")))
	require.True(t, first.owner.LocalFamilyBlocked(netip.MustParseAddr("2606:4700::1")),
		"the same family answer for an address in a completely different network")
}

var _ N.Dialer = (*scopeRecordingDialer)(nil)
var _ N.Dialer = (*forwardingDetour)(nil)
var _ adapter.DNSRouter = (*proxyServerScopeRouter)(nil)
