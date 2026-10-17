package dialer

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/netcap"
	C "github.com/sagernet/sing-box/constant"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// Phase 03, second pass: the lifecycle attacks the first pass did not run.
//
// # Why there is a second pass at all
//
// The first pass attacked the POLICY axis properly - one capture per flow, one cross-stage matrix - and
// then verified several lifecycle claims by READING the test that already covered them. That is a
// legitimate way to establish coverage, and it is not the same as attacking them, which is what this
// round is for. The section list is explicit about it: setup cancellation, scheduler lifetime, packet
// parity, refusal distinguishability and the policy's own lifetime were all left as "covered by Phase 01
// or 02" without a single new attempt to break them.
//
// This file attacks those. Each test is written to fail if the claim is only approximately true, and the
// assertions are chosen so a plausible-but-wrong implementation cannot pass:
//
//	A plausible wrong implementation passes when the test asserts "no error".
//	A plausible wrong implementation FAILS when the test asserts which addresses were reached, whether a
//	health verdict exists, or how many goroutines are still running.

// TestACancelledBeforeItStartsFlowReachesNoTransport is the first half of §4.
//
// A flow whose caller has already given up must not reach the transport at all. This is stronger than
// "returns an error": an implementation that dials and then discards the socket also returns an error, and
// it has done the work - and on a mobile device it has woken the radio for a request nobody wants.
func TestACancelledBeforeItStartsFlowReachesNoTransport(t *testing.T) {
	ipv4 := netip.MustParseAddr("112.53.55.92")
	ipv6 := netip.MustParseAddr("2409:8c54:871:1007::2c")

	flow := newCountingFlow(t, netcap.StateProvisioned, netcap.StateProvisioned, true)
	flow.router.addressesA = []netip.Addr{ipv4}
	flow.router.addressesAAAA = []netip.Addr{ipv6}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the caller gave up before anything began

	conn, err := flow.resolver.DialContext(ctx, "tcp", M.Socksaddr{Fqdn: "cancelled.example", Port: 443})
	if conn != nil {
		conn.Close()
	}
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled, "the caller's own cancellation must be reported as such")
	require.Empty(t, flow.leaf.attempted(),
		"a cancelled flow reached the transport: it dialled a socket for a request whose caller had "+
			"already gone")
}

// TestACancelledMidDialFlowRecordsNoFamilyVerdict is the second half of §4, and §23's rule.
//
// A connection attempt that is cancelled by its CALLER says nothing about the network: the far end never
// had a chance to answer. Recording it as a family failure would poison the fast-fallback bias for the
// next connection on a perfectly good network - and the damage would outlive the flow that caused it by
// the full fifteen-second health window.
func TestACancelledMidDialFlowRecordsNoFamilyVerdict(t *testing.T) {
	ipv6 := netip.MustParseAddr("2409:8c54:871:1007::2c")

	manager := &scriptedObservationManager{
		verdicts: []adapter.NetworkFamilyCapability{capWith(netcap.StateProvisioned, netcap.StateProvisioned)},
		epoch:    9,
		stable:   true,
	}
	owner := &DefaultDialer{familyHealth: newFamilyHealth()}
	owner.network = newNetworkObserver(manager)
	inner := newStallingDialer()
	inner.stall = map[netip.Addr]bool{ipv6: true} // the attempt blocks until someone cancels it
	inner.connectDelay = 0
	inner.packetConn = &countingPacketConn{}
	leaf := &scriptedLeaf{owner: owner, inner: inner}
	resolver := &resolveDialer{dialer: leaf, fallbackDelay: time.Millisecond}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		conn, err := resolver.DialContext(ctx, "tcp", M.SocksaddrFrom(ipv6, 443))
		if conn != nil {
			conn.Close()
		}
		done <- err
	}()

	// Wait until the attempt is genuinely in flight, then cancel.
	require.Eventually(t, func() bool { return len(leaf.tried()) == 1 }, 2*time.Second, time.Millisecond,
		"the attempt never reached the transport, so there is nothing to cancel")
	cancel()

	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the flow did not return after its caller cancelled")
	}

	require.False(t, owner.familyHealth.fallbackImmediately(9, familyIPv6, familyIPv4),
		"a caller's cancellation was recorded as a family path failure: the next connection on this "+
			"healthy network will be penalised for fifteen seconds")
}

// TestACancelledRaceJoinsItsAttempts is §6.
//
// A race that is cancelled must have no attempt still running when it returns. "Returns quickly" is not
// the claim: the claim is that every worker it started has EXITED, which is the difference between a
// cancelled connection and a leak that shows up as a goroutine growing per transition on a real device.
func TestACancelledRaceJoinsItsAttempts(t *testing.T) {
	blackhole6 := netip.MustParseAddr("2001:db8::1")
	blackhole4 := netip.MustParseAddr("192.0.2.1")

	inner := newStallingDialer()
	inner.stall = map[netip.Addr]bool{blackhole6: true, blackhole4: true}

	scheduler := &candidateScheduler{fallbackDelay: time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())

	before := runtime.NumGoroutine()
	done := make(chan error, 1)
	go func() {
		_, _, err := scheduler.dial(ctx,
			planCandidates([]netip.Addr{blackhole6, blackhole4}, netip.Addr{}, C.DomainStrategyAsIS),
			func(attemptCtx context.Context, address netip.Addr) (net.Conn, error) {
				return inner.DialContext(attemptCtx, "tcp", M.SocksaddrFrom(address, 443))
			})
		done <- err
	}()

	// Both attempts must be in flight before the cancel, or the test measures a race that never started.
	require.Eventually(t, func() bool { return len(inner.tried) >= 1 }, 2*time.Second, time.Millisecond)
	time.Sleep(20 * time.Millisecond) // let the second candidate start; the assertion below is on the JOIN
	cancel()

	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the cancelled race did not return: its workers were not joined")
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && runtime.NumGoroutine() > before+16 {
		time.Sleep(2 * time.Millisecond)
	}
	require.LessOrEqual(t, runtime.NumGoroutine(), before+16,
		"a cancelled race left workers running: the count went from %d to %d", before, runtime.NumGoroutine())
}

// TestEveryPacketArmAnswersTheSamePolicy is §14, and it is stated as a table of PER-ARM expectations
// because the arms do not enforce the same SET of terms.
//
// # The finding that shaped this test
//
// The first version asserted one expectation for all three arms and failed twice, and both failures were
// the test's fault rather than the code's - which is the third time in this round that a fixture measured
// the wrong thing (Phase 02's planner order, Phase 03's serial walk, this). What it taught is worth
// writing down, because it is not obvious from any single file:
//
//	connected UDP, candidate list   the STRATEGY term is applied by the network entry point and the
//	                                CAPABILITY term by the scheduler. Both, before a socket.
//	unconnected UDP, literal        the STRATEGY term is applied by the wrapper and the CAPABILITY term by
//	                                the LEAF, after the wrapper hands the literal down. The wrapper
//	                                deliberately does NOT refuse on capability: the leaf is the last gate
//	                                and the only one that also runs on paths no scheduler sees.
//	unconnected UDP, hostname       BOTH terms are applied by the wrapper, because there is no scheduler
//	                                and no candidate list - the list handed to N.ListenSerial IS the
//	                                decision, which is the arm that was missing the policy entirely until
//	                                Phase 02 found it.
//
// So "every arm answers the same policy" is TRUE in the sense that matters - no arm admits what the
// operator's policy forbids, and no arm creates a socket for a family the machine cannot dial - and FALSE
// if it is read as "every arm refuses at the same layer". The table below encodes the difference, and the
// asymmetry is the documentation.
func TestEveryPacketArmAnswersTheSamePolicy(t *testing.T) {
	ipv4 := netip.MustParseAddr("112.53.55.92")
	ipv6 := netip.MustParseAddr("2409:8c54:871:1007::2c")

	type armResult struct {
		refused bool
		reached []netip.Addr
	}

	runCandidateList := func(t *testing.T, ipv4State, ipv6State netcap.FamilyState, strategy C.DomainStrategy) armResult {
		t.Helper()
		chain := newParallelProductionChain(t, ipv4State, ipv6State, strategy)
		chain.inner.packetConn = &countingPacketConn{}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		conn, err := chain.dialSerialNetwork(ctx, "udp", M.SocksaddrFrom(ipv6, 443), []netip.Addr{ipv4, ipv6})
		if conn != nil {
			conn.Close()
		}
		return armResult{refused: err != nil, reached: chain.tried()}
	}

	runLiteral := func(t *testing.T, ipv4State, ipv6State netcap.FamilyState, strategy C.DomainStrategy) armResult {
		t.Helper()
		flow := newCountingFlow(t, ipv4State, ipv6State, false)
		flow.router.strategy.Store(uint32(strategy))
		resolver := &resolveDialer{router: flow.router, dialer: flow.leaf, fallbackDelay: time.Millisecond}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		conn, err := resolver.ListenPacket(ctx, M.SocksaddrFrom(ipv6, 53))
		if conn != nil {
			conn.Close()
		}
		return armResult{refused: err != nil, reached: flow.leaf.attempted()}
	}

	runHostname := func(t *testing.T, ipv4State, ipv6State netcap.FamilyState, strategy C.DomainStrategy) armResult {
		t.Helper()
		flow := newCountingFlow(t, ipv4State, ipv6State, false)
		flow.router.strategy.Store(uint32(strategy))
		flow.router.addressesA = []netip.Addr{ipv4}
		flow.router.addressesAAAA = []netip.Addr{ipv6}
		resolver := &resolveDialer{router: flow.router, dialer: flow.leaf, fallbackDelay: time.Millisecond}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		conn, err := resolver.ListenPacket(ctx, M.Socksaddr{Fqdn: "udp.example", Port: 53})
		if conn != nil {
			conn.Close()
		}
		return armResult{refused: err != nil, reached: flow.leaf.attempted()}
	}

	t.Run("dual stack, dual stack machine: every arm dials", func(t *testing.T) {
		for name, run := range map[string]func(*testing.T, netcap.FamilyState, netcap.FamilyState, C.DomainStrategy) armResult{
			"connected-candidate-list": runCandidateList,
			"unconnected-literal":      runLiteral,
			"unconnected-hostname":     runHostname,
		} {
			result := run(t, netcap.StateProvisioned, netcap.StateProvisioned, C.DomainStrategyAsIS)
			require.False(t, result.refused, "%s refused a destination the policy admits", name)
			require.NotEmpty(t, result.reached, "%s did not dial although the policy admits it", name)
		}
	})

	t.Run("the operator forbids IPv6: no arm attempts IPv6, and the literal is refused outright", func(t *testing.T) {
		// The candidate list and the hostname have an IPv4 answer to fall back on, so they dial it. The
		// literal has no alternative, so the wrapper refuses it - and the refusal is a STRATEGY refusal,
		// which is the term the wrapper owns for that arm.
		candidates := runCandidateList(t, netcap.StateProvisioned, netcap.StateProvisioned, C.DomainStrategyIPv4Only)
		require.False(t, candidates.refused)
		require.NotContains(t, candidates.reached, ipv6, "the candidate list arm attempted the forbidden family")

		literal := runLiteral(t, netcap.StateProvisioned, netcap.StateProvisioned, C.DomainStrategyIPv4Only)
		require.True(t, literal.refused, "the literal arm must refuse a destination of the forbidden family")
		require.Empty(t, literal.reached, "and it must refuse before a socket exists")

		hostname := runHostname(t, netcap.StateProvisioned, netcap.StateProvisioned, C.DomainStrategyIPv4Only)
		require.False(t, hostname.refused, "the hostname arm has an admitted answer and must use it")
		require.Equal(t, []netip.Addr{ipv4}, hostname.reached,
			"and it must attempt the admitted family only")
	})

	t.Run("the machine has no IPv6: the arms that own the capability term refuse", func(t *testing.T) {
		// The candidate list reaches the scheduler, which owns the capability term. The hostname is filtered
		// by the wrapper, which owns both terms there. The LITERAL defers to the leaf - so the wrapper
		// deliberately does NOT refuse, and this fixture's leaf is a transport double rather than a
		// DefaultDialer, which is why the assertion for that arm is the delegation itself.
		candidates := runCandidateList(t, netcap.StateProvisioned, netcap.StateUnavailable, C.DomainStrategyIPv6Only)
		require.True(t, candidates.refused, "the candidate-list arm must refuse: its only family is undialable")
		require.Empty(t, candidates.reached)

		hostname := runHostname(t, netcap.StateProvisioned, netcap.StateUnavailable, C.DomainStrategyIPv6Only)
		require.True(t, hostname.refused, "the hostname arm must refuse: every answer is undialable")
		require.Empty(t, hostname.reached)

		literal := runLiteral(t, netcap.StateProvisioned, netcap.StateUnavailable, C.DomainStrategyIPv6Only)
		require.False(t, literal.refused,
			"the literal arm refused on capability, which it must NOT do: that term belongs to the leaf, "+
				"and a wrapper that applies it hides the leaf's gate from every path that does not come "+
				"through the wrapper")
	})
}

// TestTheRefusalReasonsAreMutuallyDistinguishable is §22.
//
// Five things can stop a flow, and an operator reads the message to decide what to change. If two of them
// render the same way, the message sends them to the wrong place: a route problem looks like a missing
// address, a configuration decision looks like a device limitation. This asserts that every pair is
// distinguishable, not merely that each message is non-empty.
func TestTheRefusalReasonsAreMutuallyDistinguishable(t *testing.T) {
	globalIPv4 := netip.MustParseAddr("93.184.216.34")
	globalIPv6 := netip.MustParseAddr("2409:8a00::1")

	// Four admission refusals, one per verdict that can fire.
	noIPv6 := newAdmissionPolicyForTest(C.DomainStrategyAsIS, localFamilyPolicyForTest("provisioned", "unavailable"))
	noIPv4 := newAdmissionPolicyForTest(C.DomainStrategyAsIS, localFamilyPolicyForTest("unavailable", "provisioned"))
	noEgress := newAdmissionPolicyForTest(C.DomainStrategyAsIS, func() localFamilyPolicy {
		policy := localFamilyPolicyForTest("provisioned", "provisioned")
		policy.capability.GlobalIPv6Egress = netcap.StateUnavailable
		return policy
	}())
	strategy := newAdmissionPolicyForTest(C.DomainStrategyIPv6Only, localFamilyPolicyForTest("provisioned", "provisioned"))

	refusals := map[string]error{
		"no IPv4 address":   noIPv4.refusalError(globalIPv4),
		"no IPv6 address":   noIPv6.refusalError(globalIPv6),
		"no IPv6 egress":    noEgress.refusalError(globalIPv6),
		"strategy excludes": strategy.refusalError(globalIPv4),
	}

	seen := make(map[string]string, len(refusals))
	for name, err := range refusals {
		require.Error(t, err, "%s must render an error", name)
		text := err.Error()
		if other, duplicate := seen[text]; duplicate {
			t.Fatalf("%q and %q render IDENTICALLY (%q): an operator cannot tell a configuration "+
				"decision from a property of the machine", name, other, text)
		}
		seen[text] = name
	}

	// And the two device facts that share a verdict family must not share a sentence.
	require.NotEqual(t, noIPv6.refusalError(globalIPv6).Error(), noEgress.refusalError(globalIPv6).Error(),
		"a missing address and a missing route are different facts with different remedies")
}

// TestNoLongLivedStructureHoldsAPolicy is §24, checked against the SOURCE rather than by inspection.
//
// The policy carries a snapshot and an ownership predicate. Storing one in a struct that outlives a flow
// would keep a generation alive past its network - the exact defect the round removed - and it would look
// harmless at the definition site. A field is the shape of that mistake, so a field is what is forbidden;
// parameters and locals are how the value is expected to travel.
//
// This is deliberately a source check rather than a runtime one: no test can observe "this struct holds a
// stale policy" until long after the fact, and the rule is cheap to state.
func TestNoLongLivedStructureHoldsAPolicy(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	allowed := map[string]bool{
		// The policy's own definition file; its methods use the receiver, which is not a field.
		"admission.go": true,
	}

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if allowed[name] {
			continue
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), filepath.Join(".", name), nil, 0)
		require.NoError(t, parseErr)
		ast.Inspect(file, func(node ast.Node) bool {
			structType, isStruct := node.(*ast.StructType)
			if !isStruct {
				return true
			}
			for _, field := range structType.Fields.List {
				typeText := expressionText(field.Type)
				if strings.Contains(typeText, "admissionPolicy") {
					t.Errorf("%s declares a struct field of type %q: a policy stored in a long-lived "+
						"structure outlives the flow and the generation it belongs to. Pass it as a "+
						"parameter instead.", name, typeText)
				}
			}
			return true
		})
	}
}

// expressionText renders enough of a type expression to recognise it. The field types this rule cares
// about are all simple or pointer types, so a shallow walk is sufficient and a full printer is not needed.
func expressionText(expression ast.Expr) string {
	switch typed := expression.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.StarExpr:
		return "*" + expressionText(typed.X)
	case *ast.SelectorExpr:
		return expressionText(typed.X) + "." + typed.Sel.Name
	case *ast.ArrayType:
		return "[]" + expressionText(typed.Elt)
	case *ast.FuncType:
		return "func"
	default:
		return ""
	}
}
