package dialer

import (
	"context"
	"net"
	"net/netip"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/netcap"
	C "github.com/sagernet/sing-box/constant"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// The consumption half of the local family capability: which candidates the local dial path is
// allowed to spend an attempt on.
//
// Three properties are asserted together, because a design that gets any one of them wrong is worse
// than not having the feature:
//
//	only a PROVABLE absence removes a candidate   - never an unproven or unknown one
//	the removal reaches the LATE stream too       - otherwise the slow family walks back in
//	nothing else changes                          - the strategy still leads, and a local-scope
//	                                                destination is still dialled

// capabilityManager is a network manager that reports an ownership pair and a family capability.
//
// It is deliberately a real implementation of the optional interfaces rather than a mock of the
// dialer: the thing under test is the assertion the dialer performs against those interfaces, so a
// test double that satisfied the same assertions any other way would prove nothing about production.
type capabilityManager struct {
	epoch                 atomic.Uint64
	stable                atomic.Bool
	ipv4                  atomic.Uint32
	ipv6                  atomic.Uint32
	transitionSnapshotter atomic.Uint32 // unused; keeps the struct shape explicit
}

func newCapabilityManager(ipv4 netcap.FamilyState, ipv6 netcap.FamilyState) *capabilityManager {
	manager := &capabilityManager{}
	manager.stable.Store(true)
	manager.setStates(ipv4, ipv6)
	return manager
}

func (m *capabilityManager) setStates(ipv4 netcap.FamilyState, ipv6 netcap.FamilyState) {
	m.ipv4.Store(uint32(ipv4))
	m.ipv6.Store(uint32(ipv6))
}

func (m *capabilityManager) NetworkResetGeneration() uint64 { return m.epoch.Load() }

func (m *capabilityManager) NetworkTransitionStable() bool { return m.stable.Load() }

func (m *capabilityManager) NetworkTransitionSnapshot() (uint64, bool) {
	return m.epoch.Load(), m.stable.Load()
}

func (m *capabilityManager) NetworkFamilyCapabilitySnapshot() (uint64, bool, adapter.NetworkFamilyCapability) {
	return m.epoch.Load(), m.stable.Load(), adapter.NetworkFamilyCapability{
		Capability: netcap.FamilyCapability{
			IPv4: netcap.FamilyState(m.ipv4.Load()),
			IPv6: netcap.FamilyState(m.ipv6.Load()),
		},
	}
}

var (
	_ adapter.NetworkResetCounter                = (*capabilityManager)(nil)
	_ adapter.NetworkTransitionState             = (*capabilityManager)(nil)
	_ adapter.NetworkTransitionSnapshotter       = (*capabilityManager)(nil)
	_ adapter.NetworkFamilyCapabilitySnapshotter = (*capabilityManager)(nil)
)

// capabilityLeaf is a plain dialer whose fake transport is driven by a real DefaultDialer's
// capability.
//
// # Why the leaf and the parallel layer are two types
//
// A dialer that also implements ParallelInterfaceDialer is wrapped by NewResolveDialer as a
// resolveParallelNetworkDialer, which is correct in production and wrong for a test that wants the
// ordinary resolution path. Keeping the leaf plain is what lets the same doubles exercise both
// shapes - the parallel one for the direct outbound, the plain one for a proxy endpoint - without
// either test accidentally running the other's code.
type capabilityLeaf struct {
	owner   *DefaultDialer
	inner   *stallingDialer
	manager *capabilityManager
}

func (d *capabilityLeaf) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	return d.inner.DialContext(ctx, network, destination)
}

func (d *capabilityLeaf) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return d.inner.ListenPacket(ctx, destination)
}

func (d *capabilityLeaf) newDualStackScheduler(fallbackDelay time.Duration) *candidateScheduler {
	return d.owner.newDualStackScheduler(fallbackDelay)
}

func (d *capabilityLeaf) localFamilyPolicy() localFamilyPolicy {
	return d.owner.localFamilyPolicy()
}

func (d *capabilityLeaf) tried() []netip.Addr {
	d.inner.access.Lock()
	defer d.inner.access.Unlock()
	out := make([]netip.Addr, len(d.inner.tried))
	copy(out, d.inner.tried)
	return out
}

// capabilityDialer adds the parallel-interface shape on top of the leaf, which is what the direct
// outbound holds.
type capabilityDialer struct {
	*capabilityLeaf
}

func newCapabilityDialer(t *testing.T, ipv4 netcap.FamilyState, ipv6 netcap.FamilyState) *capabilityDialer {
	t.Helper()
	return &capabilityDialer{capabilityLeaf: newCapabilityLeaf(ipv4, ipv6)}
}

func newCapabilityLeaf(ipv4 netcap.FamilyState, ipv6 netcap.FamilyState) *capabilityLeaf {
	manager := newCapabilityManager(ipv4, ipv6)
	owner := &DefaultDialer{familyHealth: newFamilyHealth()}
	owner.familySnapshot = manager.NetworkFamilyCapabilitySnapshot
	return &capabilityLeaf{owner: owner, inner: newStallingDialer(), manager: manager}
}

// DialParallelInterface satisfies ParallelInterfaceDialer. The scheduler only uses it to route the
// per-attempt connect, so delegating to the fake is enough.
func (d *capabilityDialer) DialParallelInterface(ctx context.Context, network string, destination M.Socksaddr, strategy *C.NetworkStrategy, interfaceType []C.InterfaceType, fallbackInterfaceType []C.InterfaceType, fallbackDelay time.Duration) (net.Conn, error) {
	return d.inner.DialContext(ctx, network, destination)
}

func (d *capabilityDialer) ListenSerialInterfacePacket(ctx context.Context, destination M.Socksaddr, strategy *C.NetworkStrategy, interfaceType []C.InterfaceType, fallbackInterfaceType []C.InterfaceType, fallbackDelay time.Duration) (net.PacketConn, error) {
	return nil, context.Canceled
}

var (
	address6 = netip.MustParseAddr("2001:db8::1")
	address4 = netip.MustParseAddr("192.0.2.1")
)

// connectImmediately makes every attempt succeed at once.
//
// A test that asserts WHICH families were offered to the transport needs both attempts to be able to
// complete, so it can assert on the winner. A stalling fake would instead let the first success cancel
// the other attempt, and an attempt cancelled by the caller records nothing by design - so an
// assertion about the attempt list would be about the race rather than about the filter.
func connectImmediately(dialer *capabilityDialer) {
	dialer.inner.stall = nil
	dialer.inner.connectDelay = 0
}

// TestUnavailableFamilyIsNeverAttempted is the fast-fail, stated as the user's case: a dual-stack
// name on a machine with no IPv6 addressing.
//
// The assertion is on what was DIALLED, not on a counter or a log line: an implementation that
// filtered the plan but let the scheduler start the address anyway would pass a shallower test and
// still cost the caller a connect timeout.
func TestUnavailableFamilyIsNeverAttempted(t *testing.T) {
	dialer := newCapabilityDialer(t, netcap.StateProvisioned, netcap.StateUnavailable)
	connectImmediately(dialer)

	conn, err := DialParallelNetwork(context.Background(), dialer, "tcp",
		M.SocksaddrFrom(address6, 443),
		[]netip.Addr{address6, address4},
		true, nil, nil, nil, 5*time.Millisecond)
	require.NoError(t, err)
	require.NotNil(t, conn)

	require.Equal(t, []netip.Addr{address4}, dialer.tried(),
		"the AAAA half of the answer must not be attempted at all")
}

// TestUnavailableFamilyDoesNotDelayTheHealthyOne is the latency half of the same case, and the
// sharper of the two.
//
// The fallback delay is deliberately enormous. If the unavailable family were merely deprioritized,
// or filtered only after being launched, both addresses would appear in the attempt list and the
// healthy one would arrive a full delay later. The only way the assertion below holds is if IPv6 was
// removed from the plan BEFORE the schedule was built, so the healthy family is not merely first -
// it is the only candidate.
func TestUnavailableFamilyDoesNotDelayTheHealthyOne(t *testing.T) {
	dialer := newCapabilityDialer(t, netcap.StateProvisioned, netcap.StateUnavailable)
	connectImmediately(dialer)

	start := time.Now()
	conn, err := DialParallelNetwork(context.Background(), dialer, "tcp",
		M.SocksaddrFrom(address4, 443),
		[]netip.Addr{address4, address6},
		false, nil, nil, nil, 30*time.Second)
	require.NoError(t, err)
	require.NotNil(t, conn)

	require.Less(t, time.Since(start), time.Second,
		"a refused candidate must not cost the caller the fallback delay it was never scheduled behind")
	require.Equal(t, []netip.Addr{address4}, dialer.tried())
}

// TestUnavailableFamilyIsRefusedFromTheLateStream is the half a planner-only filter cannot reach.
//
// A and AAAA arrive independently, so the family that answers second never passes through the planner:
// it is appended to the launch queue straight from the resolver's stream. Without the filter on that
// path, a blackholed family would still be launched whenever it happened to answer last - which is
// the more common ordering, because the slow answer is the one that arrives after the race started.
func TestUnavailableFamilyIsRefusedFromTheLateStream(t *testing.T) {
	// IPv6 is provably absent. Its candidate arrives FIRST here, which means the scheduler has already
	// launched it before the second candidate arrives - so this also pins that a refused candidate
	// never reaches the transport even when it wins the arrival order.
	dialer := newCapabilityDialer(t, netcap.StateProvisioned, netcap.StateUnavailable)
	scheduler := dialer.newDualStackScheduler(5 * time.Millisecond)

	late := make(chan dualStackCandidate, 2)
	late <- dualStackCandidate{address: address6, family: familyIPv6}
	late <- dualStackCandidate{address: address4, family: familyIPv4}
	close(late)

	// The plan starts empty and is fed entirely by the late stream, which is the production shape for
	// a name whose families answer at different times.
	conn, winner, err := scheduler.dialWithLateCandidates(context.Background(),
		planCandidates(nil, netip.Addr{}, 0), late,
		func(ctx context.Context, address netip.Addr) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp", M.SocksaddrFrom(address, 443))
		})
	require.NoError(t, err)
	require.NotNil(t, conn)
	require.Equal(t, address4, winner,
		"the family the machine can dial is the only one that may win")
	require.Equal(t, []netip.Addr{address4}, dialer.tried(),
		"a late candidate is subject to the same local-family verdict as a planned one")
}

// TestLateCandidateCanJoinWhenTheRaceIsAlreadyRunning is the other arrival order.
//
// The dialable family answers first and the refused one second, so the refusal is exercised on a
// stream the scheduler is already reading rather than at launch time.
func TestLateCandidateCanJoinWhenTheRaceIsAlreadyRunning(t *testing.T) {
	dialer := newCapabilityDialer(t, netcap.StateProvisioned, netcap.StateUnavailable)
	connectImmediately(dialer)
	scheduler := dialer.newDualStackScheduler(time.Millisecond)

	late := make(chan dualStackCandidate, 2)
	late <- dualStackCandidate{address: address4, family: familyIPv4}
	late <- dualStackCandidate{address: address6, family: familyIPv6}
	close(late)

	conn, winner, err := scheduler.dialWithLateCandidates(context.Background(),
		planCandidates(nil, netip.Addr{}, 0), late,
		func(ctx context.Context, address netip.Addr) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp", M.SocksaddrFrom(address, 443))
		})
	require.NoError(t, err)
	require.NotNil(t, conn)
	require.Equal(t, address4, winner)
	require.Equal(t, []netip.Addr{address4}, dialer.tried(),
		"the second arrival must be refused before it is ever launched")
}

// TestLateCandidateCanJoinOnceItsFamilyBecomesAvailable is the other side of the same rule: the
// filter must never become lock-in.
//
// The capability is re-read per dial, so a family that was absent when one connection started is
// attempted normally by the next one. This is what keeps the design from needing an invalidation
// protocol: nothing is cached across calls.
func TestLateCandidateCanJoinOnceItsFamilyBecomesAvailable(t *testing.T) {
	dialer := newCapabilityDialer(t, netcap.StateProvisioned, netcap.StateUnavailable)

	// First dial: IPv6 is absent, so the IPv6 address is refused even though it is a late arrival.
	late := make(chan dualStackCandidate, 1)
	late <- dualStackCandidate{address: address6, family: familyIPv6}
	close(late)
	scheduler := dialer.newDualStackScheduler(5 * time.Millisecond)
	_, _, err := scheduler.dialWithLateCandidates(context.Background(),
		planCandidates(nil, netip.Addr{}, 0), late,
		func(ctx context.Context, address netip.Addr) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp", M.SocksaddrFrom(address, 443))
		})
	require.Error(t, err, "the only candidate was refused, so there was nothing to dial")
	require.Empty(t, dialer.tried())

	// The network gains IPv6 addressing - the same network, so no transition is involved.
	dialer.manager.setStates(netcap.StateProvisioned, netcap.StateProvisioned)

	conn, err := DialParallelNetwork(context.Background(), dialer, "tcp",
		M.SocksaddrFrom(address6, 443),
		[]netip.Addr{address6},
		true, nil, nil, nil, 5*time.Second)
	require.NoError(t, err)
	require.NotNil(t, conn)
	require.Equal(t, []netip.Addr{address6}, dialer.tried(),
		"a family must be usable again the moment it is present")
}

// TestUnknownAndProvisionedFamiliesAreNeverFiltered is the fail-open rule as the dial path sees it.
//
// Four inputs reach the dialer in production: a manager that cannot report a capability at all, a
// manager whose capability was never computed, a family that is present but unproven, and a family
// that is present and healthy. Not one of them may cost a candidate.
func TestUnknownAndProvisionedFamiliesAreNeverFiltered(t *testing.T) {
	testCases := []struct {
		name         string
		ipv4         netcap.FamilyState
		ipv6         netcap.FamilyState
		expectDialed netip.Addr
	}{
		{
			name: "both unknown, as a manager that never computed a capability reports",
			ipv4: netcap.StateUnknown,
			ipv6: netcap.StateUnknown,
			// Prefer IPv6 and both are offered, so the first candidate is the one that wins.
			expectDialed: address6,
		},
		{
			name:         "ipv6 provisioned but unproven",
			ipv4:         netcap.StateProvisioned,
			ipv6:         netcap.StateProvisioned,
			expectDialed: address6,
		},
		{
			// The reserved states have no producer yet. They must already behave as "no
			// restriction", so that turning one on later cannot silently start deleting candidates.
			name:         "validated and degraded are not restrictions",
			ipv4:         netcap.StateValidated,
			ipv6:         netcap.StateDegraded,
			expectDialed: address6,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			dialer := newCapabilityDialer(t, testCase.ipv4, testCase.ipv6)
			connectImmediately(dialer)
			conn, err := DialParallelNetwork(context.Background(), dialer, "tcp",
				M.SocksaddrFrom(address6, 443),
				[]netip.Addr{address6, address4},
				true, nil, nil, nil, 5*time.Millisecond)
			require.NoError(t, err)
			require.NotNil(t, conn)
			// Both candidates are offered, so whichever the race picks is in the attempt list. The
			// OTHER one may or may not be there: an attempt cancelled by the winner records nothing,
			// which is the designed behaviour rather than a gap in the assertion.
			require.Contains(t, dialer.tried(), testCase.expectDialed,
				"the family that wins must have been dialled")
		})
	}
}

// TestDialerWithoutACapabilityIsUnchanged is the degradation contract.
//
// A network manager that does not implement the optional capability - every third-party
// implementation, and every test double that does not opt in - must keep its previous behaviour
// exactly. The zero policy restricts nothing, and this asserts that against the real production
// constructor path rather than against the policy type alone.
func TestDialerWithoutACapabilityIsUnchanged(t *testing.T) {
	owner := &DefaultDialer{familyHealth: newFamilyHealth()}
	require.Nil(t, owner.familySnapshot, "a DefaultDialer built without a manager has no snapshot")

	inner := newStallingDialer()
	dialer := &capabilityDialer{capabilityLeaf: &capabilityLeaf{
		owner:   owner,
		inner:   inner,
		manager: newCapabilityManager(netcap.StateUnknown, netcap.StateUnknown),
	}}
	connectImmediately(dialer)

	conn, err := DialParallelNetwork(context.Background(), dialer, "tcp",
		M.SocksaddrFrom(address6, 443),
		[]netip.Addr{address6, address4},
		true, nil, nil, nil, 5*time.Millisecond)
	require.NoError(t, err)
	require.NotNil(t, conn)
	// Prefer IPv6, and nothing restricts either family, so the preferred family decides. The losing
	// attempt may or may not appear in the list - one cancelled by the winner records nothing by
	// design - so only the winner is asserted.
	require.Contains(t, dialer.tried(), address6,
		"with no capability reported, the configured preference must still decide")
}

// TestLocalScopeDestinationsAreExempt pins the exemption, which is the rule that keeps the verdict
// from deleting working local services.
//
// The structural verdict says "this machine has no address of family F", which is a statement about
// reaching REMOTE destinations of F. A ULA, link-local or loopback destination is not reached that
// way, and a campus network that blackholes public IPv6 routinely still serves ULA internally. A gate
// that applied the verdict to those would delete a working local service on the strength of a
// verdict about the Internet.
//
// The assertion is on the gate itself because that is the decision under test: whether a socket is
// then created is the transport's business, and a test that depended on a real socket would be
// asserting the host's network configuration rather than this rule.
func TestLocalScopeDestinationsAreExempt(t *testing.T) {
	// Every family is provably absent, so the gate has the strongest possible reason to fire.
	dialer := newCapabilityDialer(t, netcap.StateUnavailable, netcap.StateUnavailable)

	for _, address := range []string{
		"fd00::1",  // unique local: the campus-internal case
		"fe80::1",  // link-local: what makes a link-local destination dialable
		"::1",      // loopback
		"10.0.0.1", // RFC 1918
		"169.254.1.1",
		"127.0.0.1",
		"::", // unspecified: "no destination family at all"
		"0.0.0.0",
	} {
		parsed := netip.MustParseAddr(address)
		require.False(t, dialer.owner.LocalFamilyBlocked(parsed),
			"%s is not reached over the public path this verdict describes", address)
	}

	// The gate does fire for a remote destination of an absent family - otherwise the exemption above
	// would just be "the gate never fires".
	require.True(t, dialer.owner.LocalFamilyBlocked(address6))
	require.True(t, dialer.owner.LocalFamilyBlocked(address4))
}

// TestConnectedPacketPathRefusesAProvablyAbsentFamily is the gate on the one dial entry point that
// creates a socket without a candidate race.
//
// A connected UDP socket is a socket this machine creates for a destination it dials, and a UDP flow
// to an address with no local path is the worst case in the whole feature: nothing answers, so there
// is no failure to react to and the caller waits out its own timeout.
func TestConnectedPacketPathRefusesAProvablyAbsentFamily(t *testing.T) {
	dialer := newCapabilityDialer(t, netcap.StateProvisioned, netcap.StateUnavailable)
	_, err := dialer.owner.ListenPacket(context.Background(), M.SocksaddrFrom(address6, 443))
	require.ErrorIs(t, err, errLocalIPv6Unavailable)
	require.Contains(t, err.Error(), "ipv6",
		"the refusal must name the family, so it is not mistaken for a generic dial failure")

	// The other family is present, so the same call is NOT refused by the gate; it reaches the
	// transport, which is what the gate must allow.
	require.False(t, dialer.owner.LocalFamilyBlocked(address4))
}

// TestLocalFamilyBlockedIsTheSameVerdictTheDialPathUses keeps the native-bypass answer and the
// userspace answer from drifting apart.
//
// They are two consumption points of one question, and the bypass refusal is only safe because it
// agrees with what the userspace path would have done. A second implementation of the family test
// would let them disagree, and the disagreement would show up as a flow that bypasses into a stall
// the dialer believed it had prevented.
func TestLocalFamilyBlockedIsTheSameVerdictTheDialPathUses(t *testing.T) {
	dialer := newCapabilityDialer(t, netcap.StateProvisioned, netcap.StateUnavailable)

	require.True(t, dialer.owner.LocalFamilyBlocked(address6),
		"an address of a provably absent family must be refused to the bypass as well")
	require.False(t, dialer.owner.LocalFamilyBlocked(address4))
	require.False(t, dialer.owner.LocalFamilyBlocked(netip.MustParseAddr("fd00::1")),
		"a local-scope destination is not reached over the public path this verdict describes")

	// The resolve wrapper is what the direct outbound actually holds, so it must answer the same way.
	wrapped := &resolveDialer{dialer: dialer}
	require.True(t, wrapped.LocalFamilyBlocked(address6))
	require.False(t, wrapped.LocalFamilyBlocked(address4))
}

// TestStrategyStillLeadsWhenAFamilyIsFiltered guards the interaction between the two independent
// reasons a candidate can be dropped.
//
// They must not be confused with each other, because they send an operator to different places: a
// strict family strategy is a CONFIGURATION decision, while a provably absent family is a property of
// the DEVICE. AddressAllowedByStrategy answers the first question and the local family policy answers
// the second, and this pins that they answer about different things.
func TestStrategyStillLeadsWhenAFamilyIsFiltered(t *testing.T) {
	dialer := newCapabilityDialer(t, netcap.StateProvisioned, netcap.StateUnavailable)

	// The strategy restricts the RESOLVED list by family, so with ipv6_only an IPv4 address is
	// excluded by configuration alone - regardless of what the machine can dial.
	require.False(t, AddressAllowedByStrategy(address4, C.DomainStrategyIPv6Only))
	require.True(t, AddressAllowedByStrategy(address6, C.DomainStrategyIPv6Only))

	// The machine-level verdict is independent of the strategy and about a different address: this
	// device has no IPv6 addressing at all, so even an address the strategy permits is refused.
	require.True(t, dialer.owner.LocalFamilyBlocked(address6))
	require.False(t, dialer.owner.LocalFamilyBlocked(address4))
}

// TestHardUnavailableErrorNamesTheMachineAndTheFamily pins the diagnostic contract.
//
// The two refusals a user can meet look alike in a log and mean different things: one is a
// configuration decision and one is a property of the device. This asserts the second says so.
func TestHardUnavailableErrorNamesTheMachineAndTheFamily(t *testing.T) {
	err := hardUnavailableError(familyIPv6)
	require.Contains(t, err.Error(), "ipv6")
	require.Contains(t, err.Error(), "no interface on this machine")
	require.NotContains(t, err.Error(), "192.0.2.1",
		"the refusal is a property of the machine, not of one destination")
}

// TestFilterLocalFamilyLeavesTheCallersDataAlone is the ownership half of the filter.
//
// planCandidates hands its result to whoever asked - the direct outbound passes one it built from
// flow metadata - so rewriting that slice as a side effect of dialling would mutate data the
// scheduler does not own. The filter therefore copies when it removes and touches nothing when it
// does not.
func TestFilterLocalFamilyLeavesTheCallersDataAlone(t *testing.T) {
	scheduler := &candidateScheduler{
		localFamily: newLocalFamilyPolicy(netcap.FamilyCapability{
			IPv4: netcap.StateProvisioned,
			IPv6: netcap.StateUnavailable,
		}),
	}
	plan := planCandidates([]netip.Addr{address6, address4}, netip.Addr{}, 0)
	callersCopy := append([]dualStackCandidate(nil), plan.candidates...)

	filtered, refused := scheduler.filterLocalFamily(plan)
	require.Equal(t, familyIPv6, refused)
	require.Len(t, filtered.candidates, 1)
	require.Equal(t, address4, filtered.candidates[0].address)
	require.Equal(t, callersCopy, plan.candidates,
		"the slice the caller built must be exactly as it was; the filter copies rather than compacts")
	require.NotEqual(t, reflect.ValueOf(callersCopy).Pointer(), reflect.ValueOf(filtered.candidates).Pointer(),
		"and the filtered slice is its own allocation, not a view into the caller's")
}

// TestFilterLocalFamilyLeavesAnAllowedPlanAlone is the common case, asserted explicitly because it is
// the one that runs on every connection - and it must be allocation-free and header-identical.
func TestFilterLocalFamilyLeavesAnAllowedPlanAlone(t *testing.T) {
	scheduler := &candidateScheduler{}
	plan := planCandidates([]netip.Addr{address6, address4}, netip.Addr{}, 0)
	original := plan.candidates
	filtered, refused := scheduler.filterLocalFamily(plan)
	require.Equal(t, familyInvalid, refused)
	require.Len(t, filtered.candidates, 2, "a zero policy removes nothing")
	require.Equal(t, reflect.ValueOf(original).Pointer(), reflect.ValueOf(filtered.candidates).Pointer(),
		"nothing is refused, so nothing is copied either")
}

// TestUnspecifiedAndConnectedPacketPathGatesAreDistinct records the wildcard exemption through the
// packet entry point itself, so it is the production gate that is exercised rather than a helper.
//
// An unspecified destination is how a caller asks for a socket with no destination family at all -
// the shape a listener uses. Gating it on the machine's addressing would refuse listeners on a
// machine that legitimately has one family, which is the opposite of the feature's purpose.
func TestUnspecifiedAndConnectedPacketPathGatesAreDistinct(t *testing.T) {
	dialer := newCapabilityDialer(t, netcap.StateUnavailable, netcap.StateUnavailable)

	// A wildcard destination has no family for the verdict to be about, so the gate must not fire.
	// The inner transport is a real transport here - the dialer's own zero value - so the call may
	// succeed or fail on its own, but it must never produce the capability's refusal.
	for _, destination := range []M.Socksaddr{
		M.SocksaddrFrom(netip.IPv4Unspecified(), 0),
		M.SocksaddrFrom(netip.IPv6Unspecified(), 0),
	} {
		_, err := dialer.owner.ListenPacket(context.Background(), destination)
		require.NotErrorIs(t, err, errLocalIPv4Unavailable)
		require.NotErrorIs(t, err, errLocalIPv6Unavailable)
	}

	// The same gate DOES fire for a connected destination of an absent family. Both families are
	// absent here, so whichever the destination belongs to is refused.
	_, err := dialer.owner.ListenPacket(context.Background(), M.SocksaddrFrom(address6, 53))
	require.ErrorIs(t, err, errLocalIPv6Unavailable)
	_, err = dialer.owner.ListenPacket(context.Background(), M.SocksaddrFrom(address4, 53))
	require.ErrorIs(t, err, errLocalIPv4Unavailable)
}

// localFamilyPolicyForTest builds a policy from textual state names, so a test or benchmark names the
// case it means instead of a numeric constant.
func localFamilyPolicyForTest(ipv4 string, ipv6 string) localFamilyPolicy {
	return newLocalFamilyPolicy(netcap.FamilyCapability{
		IPv4: parseFamilyState(ipv4),
		IPv6: parseFamilyState(ipv6),
	})
}

func parseFamilyState(name string) netcap.FamilyState {
	switch name {
	case "unknown":
		return netcap.StateUnknown
	case "unavailable":
		return netcap.StateUnavailable
	case "provisioned":
		return netcap.StateProvisioned
	case "validated":
		return netcap.StateValidated
	case "degraded":
		return netcap.StateDegraded
	default:
		panic("unknown family state: " + name)
	}
}

// TestRecoveryDoesNotOfferCandidatesTheMachineCannotDial covers the literal-recovery path.
//
// The scheduler would refuse those candidates anyway, but recovery's RESULT is what decides whether
// the caller races recovery at all or keeps waiting for the original attempt. A recovered list that
// looks non-empty while containing only unreachable addresses sends the caller into a race that
// cannot produce a connection - and this is the path a TUN client takes for every literal
// destination whose own family is broken, which is exactly the flow this round is about.
func TestRecoveryDoesNotOfferCandidatesTheMachineCannotDial(t *testing.T) {
	dialer := newCapabilityDialer(t, netcap.StateUnavailable, netcap.StateProvisioned)
	resolver := &resolveDialer{dialer: dialer}

	t.Run("all candidates undialable", func(t *testing.T) {
		require.Nil(t, resolver.withoutUndialableCandidates([]netip.Addr{address4}),
			"a list of addresses this machine cannot reach must become empty, so the caller does not "+
				"race a recovery that cannot connect")
	})

	t.Run("mixed list keeps the dialable family", func(t *testing.T) {
		recovered := resolver.withoutUndialableCandidates([]netip.Addr{address4, address6})
		require.Equal(t, []netip.Addr{address6}, recovered,
			"the usable family survives, and the resolver's order within it is preserved")
	})

	t.Run("local scope is never removed", func(t *testing.T) {
		internal := netip.MustParseAddr("fd00::1")
		recovered := resolver.withoutUndialableCandidates([]netip.Addr{internal})
		require.Equal(t, []netip.Addr{internal}, recovered,
			"a ULA target is not reached over the public path the verdict describes, so a campus "+
				"network that blackholes public IPv6 still reaches its own services")
	})

	t.Run("no capability reported leaves the list exactly as it was", func(t *testing.T) {
		silent := &resolveDialer{dialer: &capabilityLeaf{
			owner:   &DefaultDialer{},
			inner:   newStallingDialer(),
			manager: newCapabilityManager(netcap.StateUnavailable, netcap.StateUnavailable),
		}}
		addresses := []netip.Addr{address4, address6}
		require.Equal(t, addresses, silent.withoutUndialableCandidates(addresses),
			"a chain that cannot report the capability keeps its previous behaviour exactly")
	})
}

// TestCapabilityFallbackWithoutASnapshotterIsSilent covers the optional-interface boundary: a manager
// that reports an ownership pair but no capability must leave the dial path exactly as it was.
//
// This is the shape every third-party NetworkManager and every test double has, so it is the shape
// that must not regress. A DefaultDialer built without a manager is the same case.
func TestCapabilityFallbackWithoutASnapshotterIsSilent(t *testing.T) {
	owner := &DefaultDialer{familyHealth: newFamilyHealth()}
	require.Nil(t, owner.familySnapshot, "a dialer with no manager has no snapshot")
	require.False(t, owner.LocalFamilyBlocked(address6))
	require.False(t, owner.captureLocalFamilyPolicy().excluded(familyIPv6))
	require.False(t, owner.captureLocalFamilyPolicy().excluded(familyIPv4))

	// A manager that reports the ownership pair but NOT the capability is the same case: the type
	// assertion in NewDefault fails, the snapshot stays nil, and nothing is filtered.
	var ownershipOnly any = &ownershipOnlyManager{}
	_, isCapability := ownershipOnly.(adapter.NetworkFamilyCapabilitySnapshotter)
	require.False(t, isCapability)
}

// ownershipOnlyManager implements the older, narrower capability: the ownership pair without the
// local family verdict.
type ownershipOnlyManager struct{}

func (m *ownershipOnlyManager) NetworkResetGeneration() uint64 { return 0 }
func (m *ownershipOnlyManager) NetworkTransitionStable() bool  { return true }
func (m *ownershipOnlyManager) NetworkTransitionSnapshot() (uint64, bool) {
	return 0, true
}

var _ adapter.NetworkTransitionSnapshotter = (*ownershipOnlyManager)(nil)
