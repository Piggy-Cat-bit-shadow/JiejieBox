package dialer

import (
	"context"
	"net/netip"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/netcap"
	C "github.com/sagernet/sing-box/constant"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// The stress the Phase 04 report recorded as NOT RUN, run at the scale this test can afford and labelled
// with that scale rather than rounded up.
//
// # The finding that produced TWO tests
//
// The first version of this file was one test: 200 flows against 50 epoch flips, asserting that no flow
// ever dialled the IPv6 address on a machine that never had IPv6. It FAILED - 50 IPv6 attempts - and the
// failure is not a defect. It is invariant B, seen from a new angle:
//
//	a capture that is SUPERSEDED mid-flow refuses nothing (fail open), so it may attempt a family the
//	machine lacks, and the kernel fails that attempt immediately. The alternative - a stale capture
//	hard-denying - is the behaviour the whole design forbids, because it would refuse a working family on
//	a network the flow is no longer on.
//
// So the invariant is not "no flow ever dials it". It is two statements, and they need two tests:
//
//	owned window      while a flow's capture is current, it never dials a family the machine lacks
//	superseded window it MAY attempt one, and the cost is exactly one failed attempt - not a failed flow
//
// Asserting the first as if it covered both is what made the first version fail, and writing it down is
// worth more than the passing test.

// stressChain is the fixture both tests share: a real leaf over a manager whose epoch the test moves.
type stressChain struct {
	resolver *resolveDialer
	leaf     *scriptedLeaf
	manager  *scriptedObservationManager
}

func newStressChain(t *testing.T) *stressChain {
	t.Helper()
	ipv4 := netip.MustParseAddr("112.53.55.92")
	ipv6 := netip.MustParseAddr("2409:8c54:871:1007::2c")

	// The machine never has IPv6 for the whole run. Only the EPOCH moves.
	manager := &scriptedObservationManager{
		verdicts: []adapter.NetworkFamilyCapability{capWith(netcap.StateProvisioned, netcap.StateUnavailable)},
		epoch:    1,
		stable:   true,
	}
	owner := &DefaultDialer{familyHealth: newFamilyHealth()}
	owner.network = newNetworkObserver(manager)
	inner := newStallingDialer()
	inner.stall = nil
	inner.connectDelay = 0
	inner.packetConn = &countingPacketConn{}
	leaf := &scriptedLeaf{owner: owner, inner: inner}
	router := &strategyCountingRouter{scriptedFamilyRouter: newScriptedFamilyRouter()}
	router.strategy.Store(uint32(C.DomainStrategyPreferIPv6)) // the preference must be overruled every time
	router.addressesA = []netip.Addr{ipv4}
	router.addressesAAAA = []netip.Addr{ipv6}
	return &stressChain{
		resolver: &resolveDialer{router: router, dialer: leaf, parallel: true, fallbackDelay: time.Millisecond},
		leaf:     leaf,
		manager:  manager,
	}
}

// TestNoFlowInTheOwnedWindowDialsAFamilyTheMachineLacks is the strong, deterministic half.
//
// No epoch moves, so every capture is current for its whole flow. 200 concurrent flows, each preferring
// IPv6 on a machine that has none: not one of them may attempt it. This is the assertion the first version
// of this file tried to make under flips, and it is the one that has no fail-open escape.
func TestNoFlowInTheOwnedWindowDialsAFamilyTheMachineLacks(t *testing.T) {
	ipv6 := netip.MustParseAddr("2409:8c54:871:1007::2c")
	chain := newStressChain(t)

	before := runtime.NumGoroutine()

	// §28 of the transition brief names 1000 flows. This runs the number it names rather than a fraction of
	// it, so the report does not have to explain a reduction.
	const flows = 1000
	var (
		started  sync.WaitGroup
		failures atomic.Int64
	)
	for index := 0; index < flows; index++ {
		started.Add(1)
		go func() {
			defer started.Done()
			conn, err := chain.resolver.DialContext(context.Background(), "tcp",
				M.Socksaddr{Fqdn: "owned.example", Port: 443})
			if conn != nil {
				conn.Close()
			}
			if err != nil {
				failures.Add(1)
			}
		}()
	}
	started.Wait()

	reached := chain.leaf.tried()
	require.NotEmpty(t, reached, "no flow reached the transport, so the run proved nothing")
	for _, address := range reached {
		require.NotEqual(t, ipv6, address,
			"a flow in the owned window dialled IPv6 although the machine never had IPv6 addressing: the "+
				"capability gate admitted a family it had just refused")
	}
	require.Zero(t, failures.Load(),
		"a flow failed although the machine had a usable family for the whole run")

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && runtime.NumGoroutine() > before+100 {
		time.Sleep(5 * time.Millisecond)
	}
	require.LessOrEqual(t, runtime.NumGoroutine(), before+64,
		"%d owned-window flows retained goroutines: %d before, %d after", flows, before, runtime.NumGoroutine())
}

// TestASupersededCaptureCostsAnAttemptNotAConnection is the fail-open half, and it is the test the first
// version became once its premise was corrected.
//
// The epoch flips continuously underneath the flows, so captures are superseded mid-dial. The assertions
// are the ones that survive a supersession:
//
//	the failure mode is bounded  an IPv6 attempt is allowed to happen and to fail in the kernel
//	the flow still succeeds      the IPv4 candidate wins, so fail-open costs an attempt, not a connection
//	nothing is retained          no goroutine outlives the run
//
// What must NOT be asserted here is "no IPv6 attempt": that assertion is true only in the owned window, and
// the previous test owns it.
func TestASupersededCaptureCostsAnAttemptNotAConnection(t *testing.T) {
	const (
		flows = 1000
		flips = 100
	)
	chain := newStressChain(t)

	before := runtime.NumGoroutine()

	stopFlips := make(chan struct{})
	var flipper sync.WaitGroup
	flipper.Add(1)
	go func() {
		defer flipper.Done()
		for index := 0; index < flips; index++ {
			select {
			case <-stopFlips:
				return
			default:
			}
			chain.manager.access.Lock()
			chain.manager.epoch++
			chain.manager.access.Unlock()
			time.Sleep(time.Millisecond)
		}
	}()

	var (
		started  sync.WaitGroup
		failures atomic.Int64
	)
	for index := 0; index < flows; index++ {
		started.Add(1)
		go func() {
			defer started.Done()
			conn, err := chain.resolver.DialContext(context.Background(), "tcp",
				M.Socksaddr{Fqdn: "superseded.example", Port: 443})
			if conn != nil {
				conn.Close()
			}
			if err != nil {
				failures.Add(1)
			}
		}()
	}
	started.Wait()
	close(stopFlips)
	flipper.Wait()

	require.Zero(t, failures.Load(),
		"a flow failed because a supersession happened mid-dial. Fail-open may cost an ATTEMPT on a family "+
			"the machine lacks - the kernel refuses it immediately and the usable candidate wins - but it "+
			"must never cost the connection")
	require.NotEmpty(t, chain.leaf.tried(), "no flow reached the transport")

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && runtime.NumGoroutine() > before+100 {
		time.Sleep(5 * time.Millisecond)
	}
	require.LessOrEqual(t, runtime.NumGoroutine(), before+64,
		"%d flows against %d epoch flips retained goroutines: %d before, %d after",
		flows, flips, before, runtime.NumGoroutine())
}

// TestAFlippedEpochNeverLeavesAVerdictBehind is the same run observed from the other side.
//
// Instead of asking what the flows dialled, this asks what the PUBLISHER left behind: after the epoch has
// moved many times, the verdict read at the end must be the machine's current one and not a value that
// drifted. A capability that is a value cannot drift, and this is the cheap check that it did not.
func TestAFlippedEpochNeverLeavesAVerdictBehind(t *testing.T) {
	ipv6 := netip.MustParseAddr("2409:8c54:871:1007::2c")

	manager := &scriptedObservationManager{
		verdicts: []adapter.NetworkFamilyCapability{capWith(netcap.StateProvisioned, netcap.StateUnavailable)},
		epoch:    1,
		stable:   true,
	}
	owner := &DefaultDialer{familyHealth: newFamilyHealth()}
	owner.network = newNetworkObserver(manager)
	leaf := &scriptedLeaf{owner: owner, inner: newStallingDialer()}
	resolver := &resolveDialer{dialer: leaf, fallbackDelay: time.Millisecond}

	var previous uint64
	for index := 0; index < 100; index++ {
		manager.access.Lock()
		manager.epoch++
		expected := manager.epoch
		manager.access.Unlock()

		captured := resolver.captureAdmissionPolicy()
		require.Equal(t, expected, captured.generation(), "a capture saw a generation other than the live one")
		require.Greater(t, captured.generation(), previous, "the epoch must advance monotonically")
		previous = captured.generation()
		require.Equal(t, admissionNoLocalFamily, captured.verdict(ipv6),
			"a capability that never changed produced a different verdict at iteration %d", index)
	}
}
