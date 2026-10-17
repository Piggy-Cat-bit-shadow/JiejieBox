package dialer

import (
	"context"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/netcap"
	C "github.com/sagernet/sing-box/constant"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// Phase 03, first round: prove the policy really is a snapshot, by COUNTING.
//
// # Why the counter sits at the owner and not at the network manager
//
// Phase 02 rejected "count the manager reads" as a measurement, and the reason has not changed: the
// ownership predicate re-reads the snapshot on every check, deliberately, so a manager-side count mixes
// "captured a policy" with "validated that my policy is still current" and no threshold over it can be
// defended.
//
// There is a seam that does separate them. A capture is the only thing that reaches the OWNER through
// `dialerLocalFamilyPolicy()`, and the ownership predicate never does - it works from the value it already
// holds. So counting the owner's `localFamilyPolicy()` calls counts captures exactly, and the expected
// number for one flow is **one**.
//
// # What the leaf's own gate adds, and why it is not counted here
//
// The real `DefaultDialer` captures once more, immediately before a socket is created, as its last-resort
// gate (§R of the Phase 02 report). This fixture's leaf delegates the dial to a transport double, so that
// per-socket capture does not happen and the count below is the DECISION path alone - which is the
// quantity the phase is about. The per-socket capture has its own tests
// (TestUnavailableFamilyIsNeverAttempted and the leaf-bypass suite).

// captureCountingLeaf counts every time the flow asked the machine for its verdict.
type captureCountingLeaf struct {
	owner *DefaultDialer
	inner *stallingDialer
	reads atomic.Int32
}

func (d *captureCountingLeaf) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	return d.inner.DialContext(ctx, network, destination)
}

func (d *captureCountingLeaf) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return d.inner.ListenPacket(ctx, destination)
}

func (d *captureCountingLeaf) newDualStackScheduler(fallbackDelay time.Duration, admission admissionPolicy) *candidateScheduler {
	return d.owner.newDualStackScheduler(fallbackDelay, admission)
}

// localFamilyPolicy is the capture seam: the wrapper's chain walk is the only caller.
func (d *captureCountingLeaf) localFamilyPolicy() localFamilyPolicy {
	d.reads.Add(1)
	return d.owner.localFamilyPolicy()
}

func (d *captureCountingLeaf) captures() int32 { return d.reads.Load() }

// attempted is the transport double's record, copied so a caller cannot race the dial goroutines.
func (d *captureCountingLeaf) attempted() []netip.Addr {
	d.inner.access.Lock()
	defer d.inner.access.Unlock()
	return append([]netip.Addr(nil), d.inner.tried...)
}

// strategyCountingRouter counts how often the strategy was resolved, and can change its answer.
type strategyCountingRouter struct {
	*scriptedFamilyRouter
	strategy      atomic.Uint32
	strategyCalls atomic.Int32
}

func (r *strategyCountingRouter) ResolveStrategy(adapter.DNSQueryOptions) C.DomainStrategy {
	r.strategyCalls.Add(1)
	return C.DomainStrategy(r.strategy.Load())
}

// countingFlow is one flow's fixture: a resolve wrapper over a leaf that counts captures.
type countingFlow struct {
	resolver *resolveDialer
	leaf     *captureCountingLeaf
	router   *strategyCountingRouter
}

func newCountingFlow(t *testing.T, ipv4, ipv6 netcap.FamilyState, parallel bool) *countingFlow {
	t.Helper()
	manager := &scriptedObservationManager{
		verdicts: []adapter.NetworkFamilyCapability{capWith(ipv4, ipv6)},
		epoch:    11,
		stable:   true,
	}
	owner := &DefaultDialer{familyHealth: newFamilyHealth()}
	owner.network = newNetworkObserver(manager)

	inner := newStallingDialer()
	inner.stall = nil
	inner.connectDelay = 0
	inner.packetConn = &countingPacketConn{}
	leaf := &captureCountingLeaf{owner: owner, inner: inner}

	router := &strategyCountingRouter{scriptedFamilyRouter: newScriptedFamilyRouter()}
	router.strategy.Store(uint32(C.DomainStrategyPreferIPv4))
	return &countingFlow{
		resolver: &resolveDialer{router: router, dialer: leaf, parallel: parallel, fallbackDelay: 5 * time.Millisecond},
		leaf:     leaf,
		router:   router,
	}
}

// assertOneCapture is the assertion every shape below makes, with the strategy term checked at the same
// time because §2 asks for both counts.
func (f *countingFlow) assertOneCapture(t *testing.T, what string) {
	t.Helper()
	require.EqualValues(t, 1, f.leaf.captures(),
		"%s captured the machine's verdict %d times. One flow reads ONE worldview; a second capture is how "+
			"one connection ends up planned against one network and dialled on another", what, f.leaf.captures())
	require.EqualValues(t, 1, f.router.strategyCalls.Load(),
		"%s resolved the family strategy %d times. It is configuration, resolved once per dialer and "+
			"memoised - the second read can only be a reader that went to the source instead of taking "+
			"the flow's value", what, f.router.strategyCalls.Load())
}

// TestOneFlowCapturesThePolicyExactlyOnce is §2, over every shape a stream or packet flow takes.
//
// Each sub-case is a different ARRIVAL path for candidates, because that is where the captures lived
// before this round: the literal path captured to admit the original, recovery captured to decide which
// family ends the lookup, and the scheduler captured to build its gate. The assertion is one number, and
// it is the only measurement that can tell "one reading" from "three mutually consistent readings".
func TestOneFlowCapturesThePolicyExactlyOnce(t *testing.T) {
	ipv4 := netip.MustParseAddr("112.53.55.92")
	ipv6 := netip.MustParseAddr("2409:8c54:871:1007::2c")

	t.Run("resolved hostname, streaming router", func(t *testing.T) {
		flow := newCountingFlow(t, netcap.StateProvisioned, netcap.StateProvisioned, true)
		flow.router.addressesA = []netip.Addr{ipv4}
		flow.router.addressesAAAA = []netip.Addr{ipv6}

		conn, err := flow.resolver.DialContext(context.Background(), "tcp", M.Socksaddr{Fqdn: "dual.example", Port: 443})
		require.NoError(t, err)
		conn.Close()
		flow.assertOneCapture(t, "a streaming hostname flow")
	})

	t.Run("resolved hostname, planner path", func(t *testing.T) {
		flow := newCountingFlow(t, netcap.StateProvisioned, netcap.StateProvisioned, true)
		// A router without LookupFamilies takes raceResolvedName's planner arm.
		flow.resolver.router = &fixedAnswerRouter{answers: []netip.Addr{ipv4, ipv6}}

		conn, err := flow.resolver.DialContext(context.Background(), "tcp", M.Socksaddr{Fqdn: "dual.example", Port: 443})
		require.NoError(t, err)
		conn.Close()
		// The substitution above bypasses the counting router, so only the capture count applies.
		require.EqualValues(t, 1, flow.leaf.captures(), "a planner-path hostname flow")
	})

	t.Run("literal with recovery", func(t *testing.T) {
		flow := newCountingFlow(t, netcap.StateProvisioned, netcap.StateUnavailable, false)
		flow.router.addressesA = []netip.Addr{ipv4}
		flow.router.addressesAAAA = []netip.Addr{ipv6}

		ctx := adapter.WithContext(context.Background(), &adapter.InboundContext{
			Destination: M.SocksaddrFrom(ipv6, 80),
			Domain:      "szminorshort.weixin.qq.com",
		})
		conn, err := flow.resolver.DialContext(ctx, "tcp", M.SocksaddrFrom(ipv6, 80))
		require.NoError(t, err)
		conn.Close()
		flow.assertOneCapture(t, "a literal flow with recovery")
	})

	t.Run("literal without recovery evidence", func(t *testing.T) {
		flow := newCountingFlow(t, netcap.StateProvisioned, netcap.StateProvisioned, false)

		conn, err := flow.resolver.DialContext(context.Background(), "tcp", M.SocksaddrFrom(ipv4, 80))
		require.NoError(t, err)
		conn.Close()
		flow.assertOneCapture(t, "a bare literal flow")
	})

	t.Run("serial hostname", func(t *testing.T) {
		flow := newCountingFlow(t, netcap.StateProvisioned, netcap.StateProvisioned, false)
		flow.router.addressesA = []netip.Addr{ipv4}
		flow.router.addressesAAAA = []netip.Addr{ipv6}

		conn, err := flow.resolver.DialContext(context.Background(), "tcp", M.Socksaddr{Fqdn: "serial.example", Port: 443})
		require.NoError(t, err)
		conn.Close()
		flow.assertOneCapture(t, "a serial hostname flow")
	})

	t.Run("packet literal", func(t *testing.T) {
		flow := newCountingFlow(t, netcap.StateProvisioned, netcap.StateProvisioned, false)

		conn, err := flow.resolver.ListenPacket(context.Background(), M.SocksaddrFrom(ipv4, 53))
		require.NoError(t, err)
		conn.Close()
		flow.assertOneCapture(t, "a packet flow to a literal")
	})

	t.Run("packet hostname", func(t *testing.T) {
		flow := newCountingFlow(t, netcap.StateProvisioned, netcap.StateProvisioned, false)
		flow.router.addressesA = []netip.Addr{ipv4}
		flow.router.addressesAAAA = []netip.Addr{ipv6}

		conn, err := flow.resolver.ListenPacket(context.Background(), M.Socksaddr{Fqdn: "udp.example", Port: 53})
		require.NoError(t, err)
		conn.Close()
		flow.assertOneCapture(t, "a packet flow to a hostname")
	})
}

// TestAStrictFlowResolvesTheStrategyFromConfiguration is the other half of §2's count.
//
// With an explicit `_only` strategy the router must not be asked at all: the answer is configuration and
// there is nothing to resolve. A flow that asked anyway would be reading the world twice for a value it
// was given.
func TestAStrictFlowResolvesTheStrategyFromConfiguration(t *testing.T) {
	ipv4 := netip.MustParseAddr("112.53.55.92")
	ipv6 := netip.MustParseAddr("2409:8c54:871:1007::2c")

	flow := newCountingFlow(t, netcap.StateProvisioned, netcap.StateProvisioned, false)
	flow.resolver.queryOptions.Strategy = C.DomainStrategyIPv4Only
	flow.router.addressesA = []netip.Addr{ipv4}
	flow.router.addressesAAAA = []netip.Addr{ipv6}

	conn, err := flow.resolver.DialContext(context.Background(), "tcp", M.Socksaddr{Fqdn: "strict.example", Port: 443})
	require.NoError(t, err)
	conn.Close()

	require.EqualValues(t, 0, flow.router.strategyCalls.Load(),
		"an explicit strategy was resolved through the router anyway")
	require.EqualValues(t, 1, flow.leaf.captures(), "and the capability was still captured exactly once")
	require.Equal(t, []netip.Addr{ipv4}, flow.leaf.attempted(),
		"the strict policy must have removed the IPv6 candidate before anything was attempted")
}

// TestACapabilityChangeUnderALiveFlowCannotReachIt is §3, corrected to the case that is reachable.
//
// # Why the first version of this test was wrong, and what it taught
//
// It moved the manager's GENERATION mid-flow and expected the captured policy to keep refusing. It does
// not, and it must not: the ownership predicate saw the epoch move, correctly judged the capture
// superseded, and refused nothing - invariant B. The test was asserting that a STALE capture still
// hard-denies, which is the one thing the design forbids.
//
// The mistake exposed a structural fact worth stating plainly: **the network epoch cannot move without a
// lifecycle cancel**, because it advances only inside beginTransition, and every path that claims one
// pairs it with a cancelled setup. So "the policy was refreshed mid-flow" is impossible for the generation
// term - ownership catches it before any stage could act on it.
//
// The term that CAN move under a live flow is the CAPABILITY. A route flap, an address appearing, a DHCPv6
// lease expiring all republish the verdict under the INCUMBENT epoch (Phase 01 §D). Ownership cannot
// detect that - the epoch is unchanged, so the capture is still current - and the only thing standing
// between the flow and a mid-dial verdict change is the snapshot itself. That is the case attacked here.
func TestACapabilityChangeUnderALiveFlowCannotReachIt(t *testing.T) {
	ipv4 := netip.MustParseAddr("112.53.55.92")
	ipv6 := netip.MustParseAddr("2409:8c54:871:1007::2c")

	// ONE epoch throughout: this is a capability-only change, which is what a route flap produces.
	manager := &scriptedObservationManager{
		verdicts: []adapter.NetworkFamilyCapability{
			capWith(netcap.StateProvisioned, netcap.StateUnavailable), // the route is gone
			capWith(netcap.StateProvisioned, netcap.StateProvisioned), // the route is back
		},
		epoch:  5,
		stable: true,
	}
	owner := &DefaultDialer{familyHealth: newFamilyHealth()}
	owner.network = newNetworkObserver(manager)
	inner := newStallingDialer()
	inner.stall = nil
	inner.connectDelay = 0
	inner.packetConn = &countingPacketConn{}
	leaf := &captureCountingLeaf{owner: owner, inner: inner}
	router := &strategyCountingRouter{scriptedFamilyRouter: newScriptedFamilyRouter()}
	router.strategy.Store(uint32(C.DomainStrategyPreferIPv4))
	resolver := &resolveDialer{router: router, dialer: leaf, fallbackDelay: 5 * time.Millisecond}

	// The flow captures while the route is gone.
	admission := resolver.captureAdmissionPolicy()
	require.EqualValues(t, 5, admission.generation())
	require.Equal(t, admissionNoLocalFamily, admission.verdict(ipv6),
		"the fixture must capture the verdict this test is about")

	// The route comes back. Same epoch, so no ownership term can notice.
	manager.access.Lock()
	manager.reads = 1 // every later read answers "dual stack"
	manager.access.Unlock()

	// The capture is still CURRENT - which is exactly why the snapshot has to hold the line.
	require.True(t, admission.localFamily.ownership.owns(),
		"the capture must still be current, or this test measures supersession rather than the snapshot")
	require.Equal(t, admissionNoLocalFamily, admission.verdict(ipv6),
		"the policy's verdict moved with the manager's later answer")
	require.False(t, admission.admits(ipv6))
	require.True(t, admission.admits(ipv4))

	// A full flow through it dials by the captured verdict: the manager now claims a global IPv6 route and
	// the flow still refuses the IPv6 candidate.
	router.addressesA = []netip.Addr{ipv4}
	router.addressesAAAA = []netip.Addr{ipv6}
	conn, err := resolver.DialContext(context.Background(), "tcp", M.Socksaddr{Fqdn: "flap.example", Port: 443})
	require.NoError(t, err)
	conn.Close()
	require.NotContains(t, leaf.attempted(), ipv6,
		"the flow adopted the manager's newer capability mid-dial, so the snapshot was only a view")

	// A NEW flow gets the new capability under the SAME generation - which is why the generation cannot be
	// the discriminator on its own, and why the flow has to carry the VALUE.
	next := resolver.captureAdmissionPolicy()
	require.EqualValues(t, 5, next.generation(), "a capability-only change does not advance the epoch")
	require.Equal(t, admissionAllowed, next.verdict(ipv6), "and the new flow sees the new capability")
}
