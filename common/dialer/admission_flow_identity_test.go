package dialer

import (
	"context"
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

// Three gaps the mutation campaign of §28 found, each in a place the existing suite could not see.
//
// # Why these are here and not in the files they belong to
//
// Each one is the mirror image of a case that WAS covered, which is how a suite grows a hole: the
// covered direction looks like the whole rule. Naming them together makes the pairing obvious to the next
// reader, so the next test is added to the pair rather than to one half of it.

// TestAStrategyExcludedOriginalIsNeverPromotedBack is the mirror of
// TestNetworkEntryPointDoesNotWidenAStrictStrategy.
//
// That test uses ipv6_only with an IPv4 candidate, so the ORIGINAL (IPv6) is admitted and only the
// caller's extra address is excluded. Nobody tested the other half: an original the policy EXCLUDES,
// alongside a candidate it admits. `promoteOriginal` re-inserts whatever it is handed and cannot consult
// the policy - it is a planner rule - so the caller is the only place the rule can be applied, and with
// that check removed the excluded literal is planned FIRST and a dual-stack machine dials it.
//
// Measured with the check replaced by `if true`: the excluded IPv6 literal is attempted under ipv4_only.
func TestAStrategyExcludedOriginalIsNeverPromotedBack(t *testing.T) {
	chain := newParallelProductionChain(t, netcap.StateProvisioned, netcap.StateProvisioned, C.DomainStrategyIPv4Only)
	originalIPv6 := netip.MustParseAddr("2001:db8::15")
	providedIPv4 := netip.MustParseAddr("192.0.2.92")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, err := chain.dialSerialNetwork(ctx, "tcp", M.SocksaddrFrom(originalIPv6, 80), []netip.Addr{originalIPv6, providedIPv4})
	require.NoError(t, err, "the policy admits the IPv4 candidate, so the flow must be served")
	if conn != nil {
		conn.Close()
	}

	require.NotContains(t, chain.tried(), originalIPv6,
		"the application's original endpoint was dialled although the operator's own policy excludes its "+
			"family: promotion is a planner rule and cannot consult the policy, so the refusal has to "+
			"happen before the planner is handed the address")
	require.Equal(t, []netip.Addr{providedIPv4}, chain.tried())
}

// fixedAnswerRouter answers Lookup with a fixed list and does NOT implement DNSDualStackRouter.
//
// The distinction is the whole point: a streaming router takes raceResolvedName's producer path, and a
// NON-streaming one takes the planner path through raceCandidatesExcluding. A regression in that specific
// function is invisible from the streaming path, which is why the coverage gap below survived.
type fixedAnswerRouter struct {
	adapter.DNSRouter
	answers []netip.Addr
	calls   atomic.Int32
}

func (r *fixedAnswerRouter) Lookup(ctx context.Context, domain string, options adapter.DNSQueryOptions) ([]netip.Addr, error) {
	r.calls.Add(1)
	return r.answers, nil
}

// TestAResolvedNameIsAdmittedByTheFlowsOwnCapture is the end-to-end form of the capture rule.
//
// The flow captures "IPv6 unavailable"; the manager's later answers say dual-stack. A resolved name is
// planned and scheduled entirely inside that later window, so a stage that re-captured would admit the
// AAAA candidate and dial a family the flow had already refused.
//
// This is the arm the direct unit tests cannot reach: TestTheSchedulerUsesTheFlowsCapturedVerdict builds a
// scheduler by hand, and TestRecoveryUsesTheFlowsCapturedVerdict calls the recovery filter directly. Neither
// runs raceCandidatesExcluding, which is where the planner and the scheduler are joined.
//
// # Why the answer list holds ONLY the refused family
//
// The first version of this test offered both families and asserted the IPv6 one was not dialled, and it
// passed against the regression it was written for. The reason is the planner: under AsIS it puts the
// IPv4 family first, so the IPv6 candidate was never reached in either version and the assertion
// measured the ORDER rather than the policy. With only the refused family offered, the verdict is the
// only thing that can stop it, and the flow must fail with the machine's own reason instead of opening a
// socket that cannot work.
func TestAResolvedNameIsAdmittedByTheFlowsOwnCapture(t *testing.T) {
	resolver, leaf, _ := newChangingNetworkChain(t, capWith(netcap.StateProvisioned, netcap.StateUnavailable))
	ipv6 := netip.MustParseAddr("2001:db8::2c")

	router := &fixedAnswerRouter{answers: []netip.Addr{ipv6}}
	resolver.router = router
	resolver.parallel = true

	conn, err := resolver.DialContext(context.Background(), "tcp", M.Socksaddr{Fqdn: "v6only.example", Port: 443})
	if conn != nil {
		conn.Close()
	}

	require.EqualValues(t, 1, router.calls.Load(), "the fixture must have taken the planner path")
	require.ErrorIs(t, err, errLocalIPv6Unavailable,
		"the flow's own capture refuses IPv6, so the refusal must be the machine's verdict")
	require.Empty(t, leaf.tried(),
		"the resolved IPv6 candidate was dialled although the flow's own policy refused that family: the "+
			"planner-to-scheduler seam captured the network a second time and adopted its later, more "+
			"permissive answer")
}

// changingStrategyRouter reports a strategy the test can change, and counts how often it was asked.
type changingStrategyRouter struct {
	adapter.DNSRouter
	strategy atomic.Uint32
	calls    atomic.Int32
}

func (r *changingStrategyRouter) ResolveStrategy(adapter.DNSQueryOptions) C.DomainStrategy {
	r.calls.Add(1)
	return C.DomainStrategy(r.strategy.Load())
}

// TestTheEffectiveStrategyIsResolvedOncePerDialer explains the one §28 mutation that is EQUIVALENT.
//
// "The same flow reads the strategy twice" cannot change any decision, because the strategy is
// configuration resolved ONCE per dialer and memoised - not state re-read per flow. That is why it is safe
// to keep the strategy term in a per-flow value that every stage asks: even if a stage asked the dialer
// again instead of taking the field, it would receive the same answer.
//
// The asymmetry that made the policy necessary is on the OTHER term: the capability changes with the
// network, so a second capture of IT returns a different value. This test pins the half that cannot drift,
// so a future "simplification" that un-memoises the strategy is caught here rather than as a flow that
// plans against one network and schedules against another.
func TestTheEffectiveStrategyIsResolvedOncePerDialer(t *testing.T) {
	router := &changingStrategyRouter{}
	router.strategy.Store(uint32(C.DomainStrategyIPv4Only))
	resolver := &resolveDialer{router: router}

	first := resolver.effectiveFamilyStrategy()
	require.Equal(t, C.DomainStrategyIPv4Only, first)

	// The configuration changes - a mode switch, a policy reload - after the first flow.
	router.strategy.Store(uint32(C.DomainStrategyIPv6Only))
	second := resolver.effectiveFamilyStrategy()

	require.Equal(t, first, second,
		"the effective strategy changed mid-dialer. It is configuration, resolved once, and the whole "+
			"point of resolving it that way is that no stage of a flow can observe two values")
	require.EqualValues(t, 1, router.calls.Load(),
		"the resolver was asked more than once, so the value is no longer memoised per dialer")
}
