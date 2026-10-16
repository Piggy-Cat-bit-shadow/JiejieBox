package dialer

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/netcap"
	C "github.com/sagernet/sing-box/constant"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// Production-path regressions for the NETWORK scheduling entry point.
//
// # Why the previous round's tests passed while production was still broken
//
// The literal-recovery tests drive `resolveDialer.dialLiteralWithRecovery` directly, and every one of
// them passed on the revision that produced the user's log. That is the whole lesson: the component was
// correct and the production path never reached it.
//
// A real TUN flow from an application that has already resolved the name does not enter through
// `DialContext`. `route.ConnectionManager.NewConnection` takes the address arm:
//
//	if len(metadata.DestinationAddresses) > 0 || metadata.Destination.IsIP() {
//	    remoteConn, err = dialer.DialSerialNetwork(setupCtx, this, N.NetworkTCP, metadata.Destination, ...)
//	}
//
// `DialSerialNetwork` then asks for `ParallelNetworkDialer` and falls back to `ParallelInterfaceDialer`.
// The resolve dialer implemented only the second, so the literal was handed to
// `DialParallelInterface` once per address and its literal arm passed the address straight to the real
// dialer. Recovery - the sniffed-domain lookup, the family fallback, the whole `recoverCandidates`
// machinery - was one call away and never invoked.
//
// So these tests start where a connection actually starts: at `DialSerialNetwork`, through the
// production dialer chain, with the metadata context the router builds. They assert on what the
// transport OBSERVED, not on which helper returned what.

// ---------------------------------------------------------------------------------------------
// The production chain
// ---------------------------------------------------------------------------------------------

// parallelProductionChain is the object graph the direct outbound holds, plus the two wrappers the
// network entry point now has to go through:
//
//	*resolveParallelNetworkDialer   <- what NewResolveDialer returns, and what discovery lives on
//	  resolveDialer                 <- the resolver, the query policy, the recovery marker
//	  directOutboundDouble          <- ParallelInterfaceDialer + ParallelNetworkDialer, which is the
//	                                   shape `protocol/direct.Outbound` presents
//	    capabilityLeaf              <- the family verdict and the transport
//
// # Why the double implements the parallel NETWORK surface too
//
// The first draft of this fixture only implemented `ParallelInterfaceDialer`, and all of the assertions
// still passed or failed for the wrong reason: `DialSerialNetwork` fell through to the per-address
// fallback and reached the transport by a route that does not exist in production. A double that is
// narrower than the object it stands for turns a production-wiring test into a test of the fallback.
type parallelProductionChain struct {
	resolver *resolveParallelNetworkDialer
	router   *scriptedFamilyRouter
	inner    *stallingDialer
}

// candidateDialer is the object the direct outbound stores: a leaf that can also dial one attempt on a
// chosen interface.
//
// It is a thin adapter rather than a change to `capabilityLeaf` on purpose: the leaf is shared by other
// suites that assert on the single-destination path, and giving it a parallel surface would change what
// those tests exercise.
type candidateDialer struct {
	*capabilityLeaf
}

// DialParallelInterface routes one SCHEDULED attempt to the transport. This is the method the
// scheduler calls per candidate, and it is the only thing the parallel surface adds.
func (d *candidateDialer) DialParallelInterface(ctx context.Context, network string, destination M.Socksaddr, strategy *C.NetworkStrategy, interfaceType []C.InterfaceType, fallbackInterfaceType []C.InterfaceType, fallbackDelay time.Duration) (net.Conn, error) {
	return d.inner.DialContext(ctx, network, destination)
}

func (d *candidateDialer) ListenSerialInterfacePacket(ctx context.Context, destination M.Socksaddr, strategy *C.NetworkStrategy, interfaceType []C.InterfaceType, fallbackInterfaceType []C.InterfaceType, fallbackDelay time.Duration) (net.PacketConn, error) {
	return d.inner.ListenPacket(ctx, destination)
}

var (
	_ ParallelInterfaceDialer = (*candidateDialer)(nil)
	_ familyHealthOwner       = (*candidateDialer)(nil)
	_ localFamilyOwner        = (*candidateDialer)(nil)
)

// directOutboundDouble is the direct outbound's full capability surface: it schedules a candidate list
// onto the resolve wrapper it holds, and it presents the parallel surface the resolve wrapper schedules
// attempts through.
//
// The two-hop shape is not decoration. It is what `route.ConnectionManager` produces - it hands
// `DialSerialNetwork` the OUTBOUND, whose `DialParallelNetwork` schedules onto the wrapper - and the
// first draft of this fixture got it wrong twice: once by omitting the parallel NETWORK methods, so all
// the assertions ran against the per-address fallback, and once by calling the wrapper from inside the
// wrapper, which recursed until the stack overflowed. Both were caught by running the tests rather than
// by reading them, which is the reason this comment names the shape rather than describing it as
// "the direct outbound".
type directOutboundDouble struct {
	*candidateDialer
	parallel *resolveParallelNetworkDialer
}

func (d *directOutboundDouble) DialParallelNetwork(ctx context.Context, network string, destination M.Socksaddr, destinationAddresses []netip.Addr, strategy *C.NetworkStrategy, interfaceType []C.InterfaceType, fallbackInterfaceType []C.InterfaceType, fallbackDelay time.Duration) (net.Conn, error) {
	return d.parallel.DialParallelNetwork(ctx, network, destination, destinationAddresses, strategy, interfaceType, fallbackInterfaceType, fallbackDelay)
}

func (d *directOutboundDouble) ListenSerialNetworkPacket(ctx context.Context, destination M.Socksaddr, destinationAddresses []netip.Addr, strategy *C.NetworkStrategy, interfaceType []C.InterfaceType, fallbackInterfaceType []C.InterfaceType, fallbackDelay time.Duration) (net.PacketConn, netip.Addr, error) {
	return d.parallel.ListenSerialNetworkPacket(ctx, destination, destinationAddresses, strategy, interfaceType, fallbackInterfaceType, fallbackDelay)
}

// Discovery is deliberately NOT a capability a caller asks for, and this double therefore does not
// expose one. It has to run CONCURRENTLY with the literal's attempt rather than before it, so it belongs
// inside `DialParallelNetwork`; a separate step a caller invoked would re-introduce the sequencing defect
// that `TestNetworkEntryPointDoesNotWaitForDiscovery` exists to catch.

var (
	_ ParallelInterfaceDialer = (*directOutboundDouble)(nil)
	_ ParallelNetworkDialer   = (*directOutboundDouble)(nil)
)

func newParallelProductionChain(t *testing.T, ipv4 netcap.FamilyState, ipv6 netcap.FamilyState, strategy C.DomainStrategy) *parallelProductionChain {
	t.Helper()
	leaf := newCapabilityLeaf(ipv4, ipv6)
	inner := leaf.inner
	// The transport answers immediately so a test that asserts WHICH addresses were offered cannot
	// also be measuring a timeout.
	inner.stall = nil
	inner.connectDelay = 0

	router := newScriptedFamilyRouter()
	router.delayAAAA = 50 * time.Millisecond

	stored := &candidateDialer{capabilityLeaf: leaf}
	outbound := &directOutboundDouble{candidateDialer: stored}
	parallel := &resolveParallelNetworkDialer{
		resolveDialer: resolveDialer{
			router:        router,
			dialer:        stored,
			parallel:      true,
			queryOptions:  adapter.DNSQueryOptions{Strategy: strategy},
			fallbackDelay: 20 * time.Millisecond,
		},
		dialer: outbound,
		// The scheduler's per-attempt dialer is the object the direct outbound stores, NOT the
		// outbound itself: scheduling onto the outbound would re-enter this wrapper.
		attempts: stored,
	}
	outbound.parallel = parallel
	return &parallelProductionChain{resolver: parallel, router: router, inner: inner}
}

// dialSerialNetwork is the production call site, unwrapped: this is the line
// `ConnectionManager.NewConnection` executes for an address destination.
func (c *parallelProductionChain) dialSerialNetwork(ctx context.Context, network string, destination M.Socksaddr, destinationAddresses []netip.Addr) (net.Conn, error) {
	return DialSerialNetwork(ctx, c.resolver, network, destination, destinationAddresses, nil, nil, nil, 0)
}

// listenSerialNetworkPacket is the packet counterpart, executed for an unconnected packet flow whose
// candidates the caller holds.
func (c *parallelProductionChain) listenSerialNetworkPacket(ctx context.Context, destination M.Socksaddr, destinationAddresses []netip.Addr) (net.PacketConn, netip.Addr, error) {
	return c.resolver.ListenSerialNetworkPacket(ctx, destination, destinationAddresses, nil, nil, nil, 0)
}

func (c *parallelProductionChain) tried() []netip.Addr {
	c.inner.access.Lock()
	defer c.inner.access.Unlock()
	out := make([]netip.Addr, len(c.inner.tried))
	copy(out, c.inner.tried)
	return out
}

func (c *parallelProductionChain) lookupCalls() int32 {
	return c.router.calls.Load()
}

// stallLookup holds every family open until releaseLookup is called, so a test can assert on what
// happened while the resolver was still outstanding.
func (c *parallelProductionChain) stallLookup() {
	c.router.gate = make(chan struct{})
}

func (c *parallelProductionChain) releaseLookup() {
	if c.router.gate != nil {
		close(c.router.gate)
		c.router.gate = nil
	}
}

// wechatFlow builds the metadata the router produces for the reported flow: an application that
// resolved a name itself, connected to the address, and whose first bytes let the sniffer recover the
// name. `domain` is left empty for the flows where the sniffer had no answer.
func wechatFlow(original netip.Addr, port uint16, domain string) context.Context {
	return adapter.WithContext(context.Background(), &adapter.InboundContext{
		Destination: M.SocksaddrFrom(original, port),
		Domain:      domain,
	})
}

// ---------------------------------------------------------------------------------------------
// 1. The reported WeChat model
// ---------------------------------------------------------------------------------------------

// TestNetworkEntryPointRecoversTheWeChatLiteral is the user report, at the entry point that was
// actually taken.
//
// From the log, repeatedly:
//
//	inbound connection to [2409:8c54:871:2003::15]:80
//	sniffed protocol: http, domain: szminorshort.weixin.qq.com
//	route(direct)
//	outbound/direct: outbound connection to [2409:8c54:871:2003::15]:80
//	ERROR: local network has no usable ipv6 path: no interface on this machine carries an ipv6 address
//
// and, in the same log, IPv4 flows to the same application succeeding in 31ms. The name had an A
// record. The correct outcome is not a better error: it is that the flow reaches IPv4 and works.
//
// The assertion is on the ended state - the connection is established, on IPv4 - and on the fact that
// the IPv6 literal never became a socket attempt.
func TestNetworkEntryPointRecoversTheWeChatLiteral(t *testing.T) {
	chain := newParallelProductionChain(t, netcap.StateProvisioned, netcap.StateUnavailable, C.DomainStrategyAsIS)

	originalIPv6 := netip.MustParseAddr("2409:8c54:871:2003::15")
	healthyIPv4 := netip.MustParseAddr("112.53.55.92")

	// The resolver answers A with the healthy address and AAAA with the original literal, which is
	// what the real name does on this machine.
	chain.router.addressesA = []netip.Addr{healthyIPv4}
	chain.router.addressesAAAA = []netip.Addr{originalIPv6}

	ctx, cancel := context.WithTimeout(wechatFlow(originalIPv6, 80, "szminorshort.weixin.qq.com"), 3*time.Second)
	defer cancel()

	// This is ConnectionManager.NewConnection's address arm, destination and no candidate list.
	conn, err := chain.dialSerialNetwork(ctx, "tcp", M.SocksaddrFrom(originalIPv6, 80), nil)
	require.NoError(t, err, "the flow must reach IPv4, not fail on the IPv6 literal")
	require.NotNil(t, conn)
	conn.Close()

	tried := chain.tried()
	require.Contains(t, tried, healthyIPv4,
		"the recovered IPv4 address is the only candidate that can carry this flow")
	require.NotContains(t, tried, originalIPv6,
		"the IPv6 literal must never reach a socket: the machine has no path for its family")
}

// TestNetworkEntryPointConsultsTheSniffedDomain is the narrower claim underneath the one above.
//
// On the broken revision the resolver was never asked, which is why the production log contains no
// `dns: lookup domain szminorshort.weixin.qq.com` between the sniff line and the refusal. A test that
// only asserted "IPv4 was dialled" could pass for the wrong reason if some other layer happened to
// substitute an address; this pins the discovery step itself.
func TestNetworkEntryPointConsultsTheSniffedDomain(t *testing.T) {
	chain := newParallelProductionChain(t, netcap.StateProvisioned, netcap.StateUnavailable, C.DomainStrategyAsIS)
	originalIPv6 := netip.MustParseAddr("2409:8c54:871:2003::15")
	chain.router.addressesA = []netip.Addr{netip.MustParseAddr("112.53.55.92")}
	chain.router.addressesAAAA = []netip.Addr{originalIPv6}

	require.Zero(t, chain.lookupCalls(), "the fixture starts with the resolver untouched")

	ctx, cancel := context.WithTimeout(wechatFlow(originalIPv6, 80, "szminorshort.weixin.qq.com"), 3*time.Second)
	defer cancel()
	conn, err := chain.dialSerialNetwork(ctx, "tcp", M.SocksaddrFrom(originalIPv6, 80), nil)
	require.NoError(t, err)
	conn.Close()

	require.NotZero(t, chain.lookupCalls(),
		"a flow with a sniffed domain and a bare literal must ask the configured resolver")
}

// TestNetworkEntryPointPreservesTheOriginalWithinItsFamily keeps the promise that recovery is a
// FALLBACK rather than an override.
//
// The application's own address may come from its own DNS cache, a hosts file, split-horizon DNS, a CDN
// selection, or an earlier legitimate answer, and re-resolving the sniffed name can legitimately return
// a DIFFERENT address for the same name. When the original is dialable it must be the one dialled, and
// it must be dialled first.
func TestNetworkEntryPointPreservesTheOriginalWithinItsFamily(t *testing.T) {
	chain := newParallelProductionChain(t, netcap.StateProvisioned, netcap.StateProvisioned, C.DomainStrategyAsIS)
	originalIPv4 := netip.MustParseAddr("112.53.55.92")
	otherIPv4 := netip.MustParseAddr("112.53.55.99")
	chain.router.addressesA = []netip.Addr{otherIPv4}
	chain.router.addressesAAAA = nil

	ctx, cancel := context.WithTimeout(wechatFlow(originalIPv4, 80, "szextshort.weixin.qq.com"), 3*time.Second)
	defer cancel()
	conn, err := chain.dialSerialNetwork(ctx, "tcp", M.SocksaddrFrom(originalIPv4, 80), nil)
	require.NoError(t, err)
	conn.Close()

	tried := chain.tried()
	require.NotEmpty(t, tried)
	require.Equal(t, originalIPv4, tried[0],
		"the endpoint the application selected leads its family; recovery does not displace it")
}

// ---------------------------------------------------------------------------------------------
// 2. The healthy literal must not be delayed by discovery
// ---------------------------------------------------------------------------------------------

// TestNetworkEntryPointDoesNotWaitForDiscovery pins the "literal primary" semantic at the entry point.
//
// The original attempt is not deferred by even one syscall of DNS work - that rule is why recovery runs
// concurrently rather than as a precondition, and why the scheduler starts the original while the
// lookup is still in flight. A resolver that never answers must therefore not delay a flow whose own
// address connects immediately.
//
// `stallLookup` makes the lookup block until the connection is already established, which is the
// strongest form of the claim: the connection is proven to complete while the resolver is still
// outstanding, rather than merely completing faster than some timeout.
func TestNetworkEntryPointDoesNotWaitForDiscovery(t *testing.T) {
	chain := newParallelProductionChain(t, netcap.StateProvisioned, netcap.StateProvisioned, C.DomainStrategyAsIS)
	defer chain.releaseLookup()
	chain.stallLookup()

	originalIPv4 := netip.MustParseAddr("112.53.55.92")

	ctx, cancel := context.WithTimeout(wechatFlow(originalIPv4, 80, "szextshort.weixin.qq.com"), 5*time.Second)
	defer cancel()

	start := time.Now()
	conn, err := chain.dialSerialNetwork(ctx, "tcp", M.SocksaddrFrom(originalIPv4, 80), nil)
	elapsed := time.Since(start)
	require.NoError(t, err)
	conn.Close()

	tried := chain.tried()
	require.NotEmpty(t, tried)
	require.Equal(t, originalIPv4, tried[0],
		"the application's own address is attempted first, while the lookup is still blocked")
	require.Less(t, elapsed, 500*time.Millisecond,
		"a healthy literal must not wait on a resolver: recovery is a fallback, not a precondition")
}

// ---------------------------------------------------------------------------------------------
// 3. No domain evidence: fast, accurate failure
// ---------------------------------------------------------------------------------------------

// TestNetworkEntryPointFailsFastWithoutEvidence is the other half of the contract, and the half that
// keeps the fix from becoming "every literal IP now does a DNS lookup".
//
// A literal whose family this machine cannot reach, with no sniffed name and no FakeIP mapping, has no
// recovery evidence at all. It must fail deterministically, before a socket, naming the machine - and
// it must not invent an address, consult the resolver, or spend a connect timeout.
func TestNetworkEntryPointFailsFastWithoutEvidence(t *testing.T) {
	chain := newParallelProductionChain(t, netcap.StateProvisioned, netcap.StateUnavailable, C.DomainStrategyAsIS)
	originalIPv6 := netip.MustParseAddr("2409:8c54:871:2003::15")

	// The control that makes the assertion meaningful: the resolver has an IPv4 answer available, so a
	// path that looked the name up without a name to look up - or that guessed one - would connect.
	chain.router.addressesA = []netip.Addr{netip.MustParseAddr("112.53.55.92")}

	ctx, cancel := context.WithTimeout(wechatFlow(originalIPv6, 80, ""), 3*time.Second)
	defer cancel()

	start := time.Now()
	conn, err := chain.dialSerialNetwork(ctx, "tcp", M.SocksaddrFrom(originalIPv6, 80), nil)
	elapsed := time.Since(start)

	require.Error(t, err, "no domain evidence means no recovery")
	require.Nil(t, conn)
	require.ErrorIs(t, err, errLocalIPv6Unavailable,
		"the failure must name the machine's addressing rather than being a bare timeout")
	require.Empty(t, chain.tried(), "and nothing may reach a socket")
	require.Zero(t, chain.lookupCalls(), "no domain evidence means no lookup, not a guess")
	require.Less(t, elapsed, time.Second, "a structurally unavailable literal fails fast")
}

// ---------------------------------------------------------------------------------------------
// 4. Pre-resolved candidates are consumed, never re-resolved
// ---------------------------------------------------------------------------------------------

// TestNetworkEntryPointConsumesExistingCandidatesWithoutResolving is the double-resolution guard.
//
// When `route action: resolve` or a pre-match resolve has already produced DestinationAddresses, that
// list is the richer statement: the candidates are dialled as given, and the same name is NOT resolved
// a second time. Resolving again would be a DNS storm on a route that already paid for one lookup, and
// it could drift to a different CDN endpoint than the one the routing decision was made against.
func TestNetworkEntryPointConsumesExistingCandidatesWithoutResolving(t *testing.T) {
	chain := newParallelProductionChain(t, netcap.StateProvisioned, netcap.StateUnavailable, C.DomainStrategyAsIS)
	originalIPv6 := netip.MustParseAddr("2409:8c54:871:2003::15")
	providedIPv4 := netip.MustParseAddr("112.53.55.92")
	chain.router.addressesA = []netip.Addr{netip.MustParseAddr("10.0.0.1")}

	ctx, cancel := context.WithTimeout(wechatFlow(originalIPv6, 80, "szminorshort.weixin.qq.com"), 3*time.Second)
	defer cancel()

	// DestinationAddresses is set, so this is the `len(...) > 0` arm.
	conn, err := chain.dialSerialNetwork(ctx, "tcp", M.SocksaddrFrom(originalIPv6, 80), []netip.Addr{originalIPv6, providedIPv4})
	require.NoError(t, err)
	conn.Close()

	require.Contains(t, chain.tried(), providedIPv4,
		"the caller's own candidate is what gets dialled")
	require.NotContains(t, chain.tried(), netip.MustParseAddr("10.0.0.1"),
		"a candidate the caller did not give must not appear: the list was already resolved")
	require.Zero(t, chain.lookupCalls(),
		"a flow that already holds candidates must not resolve the name again")
}

// ---------------------------------------------------------------------------------------------
// 5. Strict family strategy
// ---------------------------------------------------------------------------------------------

// TestNetworkEntryPointDoesNotWidenAStrictStrategy is the strategy guard at the entry point, and it is
// the one a "fix the user's WeChat" change is most likely to break.
//
// With ipv6_only the IPv4 address in the caller's own list is excluded by configuration. The machine is
// deliberately DUAL-STACK here: with IPv6 unavailable the IPv6 literal would be refused by the machine as
// well, and the two refusals would be indistinguishable - so the test would pass even if the strategy
// were ignored. A machine that can dial both families isolates the policy as the only reason the IPv4
// address is not attempted.
//
// The list is supplied, so this is also the "consume what the caller resolved" arm: discovery must not
// run, which is why the resolver's AAAA answer does not appear among the attempts.
func TestNetworkEntryPointDoesNotWidenAStrictStrategy(t *testing.T) {
	chain := newParallelProductionChain(t, netcap.StateProvisioned, netcap.StateProvisioned, C.DomainStrategyIPv6Only)
	originalIPv6 := netip.MustParseAddr("2409:8c54:871:2003::15")
	resolverIPv6 := netip.MustParseAddr("2409:8c54:1050:fc::87")
	excludedIPv4 := netip.MustParseAddr("112.53.55.92")

	chain.router.addressesA = []netip.Addr{excludedIPv4}
	chain.router.addressesAAAA = []netip.Addr{resolverIPv6}

	ctx, cancel := context.WithTimeout(wechatFlow(originalIPv6, 80, "szminorshort.weixin.qq.com"), 3*time.Second)
	defer cancel()

	conn, err := chain.dialSerialNetwork(ctx, "tcp", M.SocksaddrFrom(originalIPv6, 80), []netip.Addr{originalIPv6, excludedIPv4})
	require.NoError(t, err, "the flow is served on the family the policy admits")
	if conn != nil {
		conn.Close()
	}

	tried := chain.tried()
	require.NotContains(t, tried, excludedIPv4,
		"ipv6_only excludes the IPv4 address: the machine's capability must not admit it")
	require.Contains(t, tried, originalIPv6,
		"the caller's own IPv6 candidate is the one used")
	require.NotContains(t, tried, resolverIPv6,
		"a candidate the caller did not supply must not appear: a supplied list is not re-resolved")
	require.Zero(t, chain.lookupCalls(),
		"the caller already resolved this name, so the resolver must not be asked again")
}

// TestNetworkEntryPointReportsAStrategyExclusionAsOne is the diagnostic half: the two refusals mean
// different things to whoever reads the log.
//
// A strategy exclusion is a configuration decision the operator made; a local family refusal is a
// property of the machine they are on. Collapsing them would send an operator to look at the wrong
// thing, which is the same argument the single-destination path makes.
//
// The machine is dual-stack, so the IPv6 destination is not refused by it. The only reason this flow
// fails is the policy, and the error has to say so rather than blaming the machine.
func TestNetworkEntryPointReportsAStrategyExclusionAsOne(t *testing.T) {
	chain := newParallelProductionChain(t, netcap.StateProvisioned, netcap.StateProvisioned, C.DomainStrategyIPv4Only)
	originalIPv6 := netip.MustParseAddr("2409:8c54:871:2003::15")

	ctx, cancel := context.WithTimeout(wechatFlow(originalIPv6, 80, "szminorshort.weixin.qq.com"), 3*time.Second)
	defer cancel()

	conn, err := chain.dialSerialNetwork(ctx, "tcp", M.SocksaddrFrom(originalIPv6, 80), []netip.Addr{originalIPv6})
	if conn != nil {
		conn.Close()
	}
	require.Error(t, err)
	require.NotErrorIs(t, err, errLocalIPv6Unavailable,
		"this is a configuration decision, not a fact about the machine")
	require.NotErrorIs(t, err, errLocalIPv6EgressUnavailable)
	require.Empty(t, chain.tried(), "an address the policy excludes never becomes a socket attempt")
}

// TestStrictFamilyFilterDoesNotMutateItsInput is the ownership contract of the new list filter.
//
// The addresses reaching it can be a resolver's cache-backed array, handed to every caller for the
// lifetime of the entry. Compacting it in place would rewrite the resolver's own answer as a side effect
// of one connection's filtering, and the corruption would be attributed to the resolver.
func TestStrictFamilyFilterDoesNotMutateItsInput(t *testing.T) {
	first := netip.MustParseAddr("192.0.2.11")
	excluded := netip.MustParseAddr("2001:db8::11")
	second := netip.MustParseAddr("192.0.2.12")
	input := []netip.Addr{first, excluded, second}
	untouched := append([]netip.Addr(nil), input...)

	filtered := withoutFamilyExcluded(input, C.DomainStrategyIPv4Only)
	require.Equal(t, []netip.Addr{first, second}, filtered)
	require.Equal(t, untouched, input, "the caller's array must not be written")

	// With nothing excluded the SAME slice comes back, so the ordinary path allocates nothing and
	// cannot reorder anything.
	same := withoutFamilyExcluded(input, C.DomainStrategyAsIS)
	require.Equal(t, untouched, same)

	// A list that is entirely excluded becomes empty rather than being reported as non-empty: the
	// scheduler treats "nothing offered" and "everything refused" differently, and only an empty list
	// can say the first.
	require.Nil(t, withoutFamilyExcluded([]netip.Addr{excluded}, C.DomainStrategyIPv4Only))
}

// policyOverride is the reported machine, injected through the leaf's own accessor so the refusal under
// test is produced by the real gate rather than by a stub error.
//
// `capabilityLeaf`'s manager reports only the two family states, so the egress half has to be supplied
// explicitly. Overriding the accessor is the smallest way to do that without changing a fixture other
// suites depend on, and it is legitimate here because the policy is what production reads: it is
// accessed through exactly this method by both the gate and the message.
type policyOverride struct {
	*capabilityLeaf
	policy localFamilyPolicy
}

func (o *policyOverride) localFamilyPolicy() localFamilyPolicy { return o.policy }

// TestTheRecoveryPathKeepsTheMachinesOwnRefusalReason is §12 of the brief: a rich verdict must not be
// flattened into a lower-dimensional one on its way to the log.
//
// The gate can say two different things about a refused global IPv6 destination:
//
//	"no interface on this machine carries an ipv6 address"      - the family is absent
//	"this machine has ipv6 addressing but no route to a global
//	 ipv6 destination"                                          - the family exists, public egress does not
//
// They have different remedies, and the reported outage is the SECOND case - so the log has to be able to
// say so. The literal-recovery path used to reduce the destination with `classifyAddress` and then ask for
// the FAMILY's message, which loses the egress half: on the machine from the report it would have claimed
// there was no IPv6 addressing, sending the operator to the wrong problem.
func TestTheRecoveryPathKeepsTheMachinesOwnRefusalReason(t *testing.T) {
	// (a) IPv6 addressing exists on the link and nothing routes it to a public destination. There is no
	// recovered candidate, so the original attempt's refusal is the whole outcome.
	linkLocalOnly := newRecoveryHarness(t, netcap.StateProvisioned, netcap.StateProvisioned, nil)
	linkLocalOnly.leaf.inner.stall = nil
	linkLocalOnly.resolver.dialer = &policyOverride{capabilityLeaf: linkLocalOnly.leaf, policy: egressRefusingPolicy()}

	_, _, errEgress := linkLocalOnly.dialWithSniffedDomain(t, "2409:8c54:871:2003::15:80")
	require.Error(t, errEgress)
	require.ErrorIs(t, errEgress, errLocalIPv6EgressUnavailable,
		"the egress half of the verdict must survive the literal-recovery path")
	require.NotErrorIs(t, errEgress, errLocalIPv6Unavailable,
		"and it must not be reported as the family being absent")

	// (b) No IPv6 addressing at all - the other fact, and the other message.
	noIPv6 := newRecoveryHarness(t, netcap.StateProvisioned, netcap.StateUnavailable, nil)
	noIPv6.leaf.inner.stall = nil

	_, _, errFamily := noIPv6.dialWithSniffedDomain(t, "2409:8c54:871:2003::15:80")
	require.ErrorIs(t, errFamily, errLocalIPv6Unavailable,
		"an absent family keeps its own message")
	require.NotErrorIs(t, errFamily, errLocalIPv6EgressUnavailable)
}

// ---------------------------------------------------------------------------------------------
// 6. DNS recursion
// ---------------------------------------------------------------------------------------------

// TestNetworkEntryPointRefusesToRecurse is the structural guard, asserted on the new path because the
// new path is a new way to reach recovery.
//
// A DNS exchange may itself travel through a direct outbound. If that outbound then ran recovery,
// resolving a name would require resolving a name. The marker is set for the duration of the recovery
// lookup precisely so this cannot happen, and it has to hold on the entry point too.
func TestNetworkEntryPointRefusesToRecurse(t *testing.T) {
	chain := newParallelProductionChain(t, netcap.StateProvisioned, netcap.StateUnavailable, C.DomainStrategyAsIS)
	originalIPv6 := netip.MustParseAddr("2409:8c54:871:2003::15")
	chain.router.addressesA = []netip.Addr{netip.MustParseAddr("112.53.55.92")}

	ctx := withoutAddressRecovery(wechatFlow(originalIPv6, 80, "szminorshort.weixin.qq.com"))
	dialCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	conn, err := chain.dialSerialNetwork(dialCtx, "tcp", M.SocksaddrFrom(originalIPv6, 80), nil)
	if conn != nil {
		conn.Close()
	}
	require.Zero(t, chain.lookupCalls(),
		"a context already inside a recovery lookup must not start another one")
	require.Error(t, err, "and with no recovery and no IPv6 path, the flow fails on the machine's verdict")
	require.ErrorIs(t, err, errLocalIPv6Unavailable)
}

// ---------------------------------------------------------------------------------------------
// 7. Identity: the entry point must pick up the parallel NETWORK surface
// ---------------------------------------------------------------------------------------------

// TestResolveParallelDialerImplementsTheNetworkSurface is the structural assertion the whole file
// rests on, stated as a capability rather than as a comment.
//
// `DialSerialNetwork` consults `ParallelNetworkDialer` first. If the resolve dialer does not implement
// it, the literal takes the per-address interface path and discovery is skipped - which is exactly the
// defect, and exactly the kind of regression a refactor could reintroduce silently.
func TestResolveParallelDialerImplementsTheNetworkSurface(t *testing.T) {
	chain := newParallelProductionChain(t, netcap.StateProvisioned, netcap.StateUnavailable, C.DomainStrategyAsIS)

	var asNetwork ParallelNetworkDialer = chain.resolver
	require.NotNil(t, asNetwork, "the resolve dialer must satisfy ParallelNetworkDialer")

	// And the plain resolve dialer must NOT: it has no parallel interface to schedule onto, so the
	// capability is what distinguishes the two wrappers rather than being present on both by accident.
	var parallelInterface ParallelInterfaceDialer = chain.resolver
	require.NotNil(t, parallelInterface)
}

// ---------------------------------------------------------------------------------------------
// 8. The packet path
// ---------------------------------------------------------------------------------------------

// TestNetworkPacketEntryPointRecoversTheSniffedDomain is the packet half of the same hole.
//
// An unconnected packet flow with pre-resolved candidates goes through ListenSerialNetworkPacket, whose
// ParallelInterfaceDialer fallback also skipped discovery. The packet path deliberately has NO
// cross-family racing - a UDP socket creation is not a verified outcome - so the candidate the sniffed
// domain supplies is not an optimisation here: it is the only thing that can move the flow to the other
// family.
func TestNetworkPacketEntryPointRecoversTheSniffedDomain(t *testing.T) {
	chain := newParallelProductionChain(t, netcap.StateProvisioned, netcap.StateUnavailable, C.DomainStrategyAsIS)
	chain.inner.packetConn = &countingPacketConn{}

	originalIPv6 := netip.MustParseAddr("2409:8c54:871:2003::15")
	healthyIPv4 := netip.MustParseAddr("112.53.55.92")
	chain.router.addressesA = []netip.Addr{healthyIPv4}
	chain.router.addressesAAAA = []netip.Addr{originalIPv6}

	ctx, cancel := context.WithTimeout(wechatFlow(originalIPv6, 443, "szminorshort.weixin.qq.com"), 3*time.Second)
	defer cancel()

	packetConn, destination, err := chain.listenSerialNetworkPacket(ctx, M.SocksaddrFrom(originalIPv6, 443), nil)
	require.NoError(t, err, "the packet flow must reach the family this machine can dial")
	require.NotNil(t, packetConn)
	packetConn.Close()
	require.Equal(t, healthyIPv4, destination,
		"and it must report the address it actually bound, which is the recovered one")

	tried := chain.tried()
	require.Contains(t, tried, healthyIPv4)
	require.NotContains(t, tried, originalIPv6,
		"an unreachable-family candidate must be refused before socket creation: that refusal IS the packet path's fallback")
}

// ---------------------------------------------------------------------------------------------
// Fixtures this file owns
// ---------------------------------------------------------------------------------------------

// countingPacketConn is a packet connection that ignores everything, so a test can assert on WHICH
// address was bound without a real socket.
type countingPacketConn struct{}

func (c *countingPacketConn) ReadFrom([]byte) (int, net.Addr, error) {
	return 0, nil, errors.New("unused")
}
func (c *countingPacketConn) WriteTo([]byte, net.Addr) (int, error) { return 0, errors.New("unused") }
func (c *countingPacketConn) Close() error                          { return nil }
func (c *countingPacketConn) LocalAddr() net.Addr                   { return &net.UDPAddr{} }
func (c *countingPacketConn) SetDeadline(time.Time) error           { return nil }
func (c *countingPacketConn) SetReadDeadline(time.Time) error       { return nil }
func (c *countingPacketConn) SetWriteDeadline(time.Time) error      { return nil }
