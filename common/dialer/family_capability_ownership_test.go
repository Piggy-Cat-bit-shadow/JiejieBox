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
	"github.com/sagernet/sing/common/control"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// Finalization round: capability decisions bound to network ownership.
//
// # The semantic race these tests close
//
// Every variable involved is correctly synchronized, so the race detector has nothing to say. The
// defect is in the ORDER:
//
//	Wi-Fi:      IPv6 Unavailable, epoch N, settled
//	dial:       reads the family verdict        <- N's answer
//	            <- the device hands over to cellular: IPv6 Provisioned, epoch N+1
//	dial:       applies N's answer to an attempt that belongs to N+1
//
// The result is that a network the device has LEFT decides a dial on the network it is ON. Nothing
// downstream rescues it either: the scheduler removes candidates before a socket exists, so the
// hand-over epoch check only ever sees attempts that were actually made.
//
// # What the fix is, in one sentence
//
// The verdict is never readable without the network ownership of the same capture, and it refuses
// nothing unless that capture still owns the network - enforced inside the policy, not at the call
// sites, so no consumer can forget it.
//
// # Why these tests are deterministic
//
// Each one drives the real capture function against a fake manager whose epoch and verdict the test
// moves, so "the network changed after the capture" is a statement the test makes rather than a
// window it hopes to hit. No sleeping, no repeating until it breaks.

// ownershipManager is the part of a network manager that ownership and the family verdict are read
// from.
//
// It is deliberately NOT the real route.NetworkManager: this is the dialer's package, and the real
// one lives in another package's test build. What is exercised is the same code path - the production
// capture function reading a manager that reports a transition snapshot - so the ordering under test
// is production ordering.
type ownershipManager struct {
	// NetworkManager is embedded and nil: this double supplies the optional capabilities and nothing
	// else, which is the manager shape these tests are about.
	adapter.NetworkManager
	epoch  atomic.Uint64
	stable atomic.Bool
	ipv4   atomic.Uint32
	ipv6   atomic.Uint32
}

func newOwnershipManager(ipv4 netcap.FamilyState, ipv6 netcap.FamilyState) *ownershipManager {
	manager := &ownershipManager{}
	manager.stable.Store(true)
	manager.setStates(ipv4, ipv6)
	return manager
}

func (m *ownershipManager) setStates(ipv4 netcap.FamilyState, ipv6 netcap.FamilyState) {
	m.ipv4.Store(uint32(ipv4))
	m.ipv6.Store(uint32(ipv6))
}

// transitionTo models a completed hand-over: a new epoch, settled, with the new network's verdict.
func (m *ownershipManager) transitionTo(ipv4 netcap.FamilyState, ipv6 netcap.FamilyState) {
	m.epoch.Add(1)
	m.setStates(ipv4, ipv6)
	m.stable.Store(true)
}

// beginTransition models a transition that has claimed ownership and not yet settled. The verdict is
// deliberately left as the PREVIOUS network's, which is one of the states the publisher really passes
// through.
func (m *ownershipManager) beginTransition() {
	m.epoch.Add(1)
	m.stable.Store(false)
}

func (m *ownershipManager) NetworkResetGeneration() uint64 { return m.epoch.Load() }
func (m *ownershipManager) NetworkTransitionStable() bool  { return m.stable.Load() }
func (m *ownershipManager) NetworkTransitionSnapshot() (uint64, bool) {
	return m.epoch.Load(), m.stable.Load()
}
func (m *ownershipManager) NetworkFamilyCapabilitySnapshot() (uint64, bool, adapter.NetworkFamilyCapability) {
	return m.epoch.Load(), m.stable.Load(), adapter.NetworkFamilyCapability{
		Capability: netcap.FamilyCapability{
			IPv4: netcap.FamilyState(m.ipv4.Load()),
			IPv6: netcap.FamilyState(m.ipv6.Load()),
		},
	}
}

// InterfaceFinder is part of NetworkManager, and a DefaultDialer built through the real constructor
// asks for it. The harness builds the dialer directly, so this only satisfies the shape.
func (m *ownershipManager) InterfaceFinder() control.InterfaceFinder {
	return control.NewDefaultInterfaceFinder()
}

var (
	_ adapter.NetworkResetCounter                = (*ownershipManager)(nil)
	_ adapter.NetworkTransitionState             = (*ownershipManager)(nil)
	_ adapter.NetworkTransitionSnapshotter       = (*ownershipManager)(nil)
	_ adapter.NetworkFamilyCapabilitySnapshotter = (*ownershipManager)(nil)
)

// ownedDialer is a real DefaultDialer whose sockets are fake, wired to a manager whose epoch, settled
// state and verdict the test controls.
type ownedDialer struct {
	dialer  *DefaultDialer
	leaf    *capabilityLeaf
	manager *ownershipManager
}

func newOwnedDialer(ipv4 netcap.FamilyState, ipv6 netcap.FamilyState) *ownedDialer {
	manager := newOwnershipManager(ipv4, ipv6)
	owner := &DefaultDialer{familyHealth: newFamilyHealth()}
	// The same resolution NewDefault performs, from the same optional interfaces, so the capture under
	// test is the production capture rather than a hand-wired approximation of it.
	owner.network = newNetworkObserver(manager)
	leaf := &capabilityLeaf{owner: owner, inner: newStallingDialer(), manager: nil}
	leaf.inner.stall = nil
	leaf.inner.connectDelay = 0
	return &ownedDialer{dialer: owner, leaf: leaf, manager: manager}
}

// addressSet is the two-family candidate list every scheduler case uses.
func addressSet() []netip.Addr {
	return []netip.Addr{address6, address4}
}

// TestOldVerdictCannotDenyNewGenerationScheduler is section 6.3: the scheduler.
//
// This is the most dangerous of the consumer paths, because the scheduler removes candidates BEFORE a
// socket exists. Nothing downstream can notice: the hand-over epoch check only validates attempts that
// were actually made, so a family the scheduler dropped is a family that was never tried, and the
// connection simply does not happen.
func TestOldVerdictCannotDenyNewGenerationScheduler(t *testing.T) {
	// Network A: IPv6 provably absent.
	owned := newOwnedDialer(netcap.StateProvisioned, netcap.StateUnavailable)

	// The dial begins here. This is the capture that must own the decision.
	scheduler := owned.leaf.newDualStackScheduler(5 * time.Millisecond)
	require.False(t, scheduler.localFamily.restrictsNothing(),
		"on network A the verdict does restrict IPv6, so the test is about a live restriction")

	// The device hands over to network B, where IPv6 is present, and the transition completes.
	owned.manager.transitionTo(netcap.StateProvisioned, netcap.StateProvisioned)

	// The scheduler now filters. It must NOT remove IPv6: its verdict belongs to a network the device
	// has left.
	plan := planCandidates(addressSet(), netip.Addr{}, 0)
	filtered, refused := scheduler.filterLocalFamily(plan)
	require.False(t, refused.refused(),
		"a verdict from the replaced network must not refuse anything on this one")
	require.Len(t, filtered.candidates, 2,
		"IPv6 must survive: network A's answer says nothing about network B")

	// And end to end: the race really does reach the socket layer with IPv6.
	//
	// The plan leads with IPv6 so that IPv6 is the family that WINS. Asserting on the loser instead
	// would prove nothing: a losing attempt is cancelled by the winner and records no attempt at all,
	// by design, so an attempt list containing only the loser is indistinguishable from one where the
	// loser was never launched.
	ipv6First := planCandidates(addressSet(), netip.Addr{}, C.DomainStrategyPreferIPv6)
	require.Equal(t, address6, ipv6First.candidates[0].address, "the plan must lead with IPv6")

	conn, winner, err := scheduler.dial(context.Background(), ipv6First,
		func(ctx context.Context, address netip.Addr) (net.Conn, error) {
			return owned.leaf.DialContext(ctx, "tcp", M.SocksaddrFrom(address, 443))
		})
	require.NoError(t, err)
	require.NotNil(t, conn)
	require.Equal(t, address6, winner,
		"the successor generation's IPv6 must be dialled and must be able to win")
	require.Contains(t, owned.leaf.tried(), address6)
}

// TestOldVerdictCannotDenyNewGenerationSchedulerLateStream is the same attack on the late-arrival
// path, which is a separate code path in the scheduler and has to be closed separately.
func TestOldVerdictCannotDenyNewGenerationSchedulerLateStream(t *testing.T) {
	owned := newOwnedDialer(netcap.StateProvisioned, netcap.StateUnavailable)
	scheduler := owned.leaf.newDualStackScheduler(5 * time.Millisecond)

	owned.manager.transitionTo(netcap.StateProvisioned, netcap.StateProvisioned)

	late := make(chan dualStackCandidate, 2)
	late <- dualStackCandidate{address: address6, family: familyIPv6}
	late <- dualStackCandidate{address: address4, family: familyIPv4}
	close(late)

	conn, winner, err := scheduler.dialWithLateCandidates(context.Background(),
		planCandidates(nil, netip.Addr{}, 0), late,
		func(ctx context.Context, address netip.Addr) (net.Conn, error) {
			return owned.leaf.DialContext(ctx, "tcp", M.SocksaddrFrom(address, 443))
		})
	require.NoError(t, err)
	require.NotNil(t, conn)
	require.Contains(t, []netip.Addr{address6, address4}, winner)
	require.Contains(t, owned.leaf.tried(), address6,
		"a late IPv6 candidate must not be dropped on the strength of the replaced network's verdict")
}

// TestOldVerdictCannotDenyNewGenerationLiteral is section 6.4: the real hard gate on the dial path.
//
// It drives DefaultDialer.DialContext, not a helper, so the assertion is on the refusal a caller would
// actually receive. The error is the thing being ruled out: reporting "this machine has no IPv6" for a
// network that has IPv6 is the failure mode the check exists to prevent.
func TestOldVerdictCannotDenyNewGenerationLiteral(t *testing.T) {
	owned := newOwnedDialer(netcap.StateProvisioned, netcap.StateUnavailable)

	owned.manager.transitionTo(netcap.StateProvisioned, netcap.StateProvisioned)

	conn, err := owned.dialer.DialContext(context.Background(), "tcp", M.SocksaddrFrom(address6, 443))
	require.NotErrorIs(t, err, errLocalIPv6Unavailable,
		"a stale verdict must never be reported as a property of the machine's CURRENT network")
	if conn != nil {
		conn.Close()
	}
}

// TestOldVerdictCannotDenyNewGenerationLiteralCaptureWindow is the same rule at the finest grain: the
// capture happens, the network changes, and the gate is evaluated afterwards.
//
// A policy captured on A is held across the hand-over and then asked about an A address. That is
// exactly the ordering the scheduler and the serial path can produce, and it must deny nothing.
func TestOldVerdictCannotDenyNewGenerationLiteralCaptureWindow(t *testing.T) {
	owned := newOwnedDialer(netcap.StateProvisioned, netcap.StateUnavailable)

	// The capture: network A. This is what a dial reads when it begins.
	captured := owned.dialer.captureLocalFamilyPolicy()
	require.True(t, captured.hardUndialable(address6),
		"while A is current, the verdict does refuse IPv6 - otherwise the test proves nothing")

	owned.manager.transitionTo(netcap.StateProvisioned, netcap.StateProvisioned)

	require.False(t, captured.hardUndialable(address6),
		"once the generation has moved on, the captured verdict must refuse nothing")
	require.True(t, captured.restrictsNothing(),
		"and it must report itself as unable to restrict, so no fast path skips a filter it would "+
			"have applied")
}

// TestOldVerdictCannotDenyNewGenerationSerial is section 6.5: the parallel=false resolve path.
//
// That branch never touches the scheduler, so it had to be given the same ownership semantics
// separately - and it is the path production takes whenever a detour is configured or tcp_fast_open
// is on.
func TestOldVerdictCannotDenyNewGenerationSerial(t *testing.T) {
	owned := newOwnedDialer(netcap.StateProvisioned, netcap.StateUnavailable)

	resolver := &resolveDialer{
		router: &proxyServerScopeRouter{addresses: []netip.Addr{address6}},
		dialer: owned.leaf,
	}

	// Capture on network A, where IPv6 is absent.
	owned.manager.transitionTo(netcap.StateProvisioned, netcap.StateProvisioned)

	conn, err := resolver.DialContext(context.Background(), "tcp",
		M.Socksaddr{Fqdn: "proxy.example", Port: 443})
	require.NotErrorIs(t, err, errLocalIPv6Unavailable,
		"the serial path must not refuse on a verdict from the network the device left")
	if conn != nil {
		conn.Close()
	}
	require.Contains(t, owned.leaf.tried(), address6,
		"and it must actually dial the successor generation's IPv6")
}

// TestOldVerdictCannotDenyNewGenerationBypass is section 6.6: the native bypass.
//
// The polarity here is inverted on purpose. A stale verdict refusing a bypass would take a WORKING
// optimisation away from the current network on the strength of the previous one's addressing - so
// the conservative answer is "do not block", and the flow keeps the fast path.
func TestOldVerdictCannotDenyNewGenerationBypass(t *testing.T) {
	owned := newOwnedDialer(netcap.StateProvisioned, netcap.StateUnavailable)

	require.True(t, owned.dialer.LocalFamilyBlocked(address6),
		"on network A the bypass is correctly refused for IPv6")

	owned.manager.transitionTo(netcap.StateProvisioned, netcap.StateProvisioned)

	require.False(t, owned.dialer.LocalFamilyBlocked(address6),
		"once the device is on a network with IPv6, a verdict from the old one must not keep the "+
			"fast path closed")
	require.False(t, owned.dialer.LocalFamilyBlocked(address4))
}

// TestDuringTransitionCannotHardDeny is section 6.2, and it is Invariant A rather than Invariant B.
//
// A transition has claimed ownership and has not settled. The family verdict is whatever the publisher
// last stored, which may be the network being left - so a consumer that reads it here has no stable
// network for the answer to be about. Every consumer must therefore fail open, and the dial is
// rejected later by the hand-over check if the transition really did move the network out from under
// it.
func TestDuringTransitionCannotHardDeny(t *testing.T) {
	owned := newOwnedDialer(netcap.StateProvisioned, netcap.StateUnavailable)

	// A transition begins: the epoch moves, the network is no longer settled, and the verdict is still
	// the one the previous network produced - which is a state the publisher really passes through.
	owned.manager.beginTransition()
	require.False(t, owned.manager.NetworkTransitionStable())

	captured := owned.dialer.captureLocalFamilyPolicy()
	require.False(t, captured.ownership.startedStable,
		"the capture must record that it began unsettled")
	require.False(t, captured.hardUndialable(address6),
		"a capture taken during a transition must deny nothing: there is no settled network for its "+
			"verdict to describe")
	require.True(t, captured.restrictsNothing())

	// The scheduler and the literal gate agree, because they consume the same policy.
	scheduler := &candidateScheduler{localFamily: captured}
	plan := planCandidates(addressSet(), netip.Addr{}, 0)
	filtered, refused := scheduler.filterLocalFamily(plan)
	require.False(t, refused.refused())
	require.Len(t, filtered.candidates, 2)

	// And the bypass stays open, which is the conservative direction for an optimisation.
	require.False(t, owned.dialer.LocalFamilyBlocked(address6))
}

// TestOwnedVerdictStillHardDeniesOnItsOwnNetwork is the control, and without it every assertion above
// would pass against an implementation that simply never refuses anything.
//
// On a settled network, where the capture owns the generation it read, the verdict must still refuse
// the absent family - on all four consumers.
func TestOwnedVerdictStillHardDeniesOnItsOwnNetwork(t *testing.T) {
	owned := newOwnedDialer(netcap.StateProvisioned, netcap.StateUnavailable)

	captured := owned.dialer.captureLocalFamilyPolicy()
	require.False(t, captured.restrictsNothing(), "a live verdict must report itself as restricting")
	require.True(t, captured.hardUndialable(address6))
	require.False(t, captured.hardUndialable(address4))

	// The scheduler removes it from the plan and from the late stream.
	scheduler := &candidateScheduler{localFamily: captured}
	filtered, refused := scheduler.filterLocalFamily(planCandidates(addressSet(), netip.Addr{}, 0))
	require.Equal(t, familyIPv6, refused.family)
	require.Len(t, filtered.candidates, 1)
	require.Equal(t, address4, filtered.candidates[0].address)

	// The literal gate refuses it, with the machine named.
	_, err := owned.dialer.DialContext(context.Background(), "tcp", M.SocksaddrFrom(address6, 443))
	require.ErrorIs(t, err, errLocalIPv6Unavailable)

	// The bypass is refused.
	require.True(t, owned.dialer.LocalFamilyBlocked(address6))

	// And the address of the family that IS present is untouched.
	conn, err := owned.dialer.DialContext(context.Background(), "tcp", M.SocksaddrFrom(address4, 443))
	require.NoError(t, err)
	require.NotNil(t, conn)
	conn.Close()
}

// TestOwnershipIsCapturedEvenWithoutACapability protects the dial's existing claim on a network
// generation.
//
// Ownership is not a property of this feature. A manager that reports an epoch but no family verdict -
// every third-party NetworkManager, and every test double that does not opt in - must still get its
// hand-over check. Capturing the verdict first and returning early when there is none would silently
// remove that check and hand back connections dialled across a reset, which is a far worse regression
// than a missing fast-fail.
func TestOwnershipIsCapturedEvenWithoutACapability(t *testing.T) {
	epoch := &atomic.Uint64{}
	owner := &DefaultDialer{network: newNetworkObserver(&epochNetworkManager{
		epoch:  epoch,
		finder: control.NewDefaultInterfaceFinder(),
	})}

	policy := owner.captureLocalFamilyPolicy()
	require.NotNil(t, policy.ownership.predicate,
		"a manager that reports an epoch must still get an ownership capture")
	require.True(t, policy.ownership.owns())
	require.True(t, policy.restrictsNothing(), "with no verdict there is nothing to restrict")

	epoch.Add(1)
	require.False(t, policy.ownership.owns(),
		"and the predicate must notice the reset, so the hand-over check keeps working")
}

// TestUnstableThenSettledCaptureStillDeniesNothing is the combination that a naive epoch comparison
// gets wrong.
//
// A capture taken DURING a transition sees the transition's OWN epoch. Once the transition commits,
// the epoch has not moved again, so a comparison of epochs alone says "still current" - while the
// verdict the capture holds predates the transition that has since committed. Remembering that the
// dial STARTED unsettled is what closes it, and this asserts the policy inherits that protection
// rather than re-deriving it.
func TestUnstableThenSettledCaptureStillDeniesNothing(t *testing.T) {
	owned := newOwnedDialer(netcap.StateProvisioned, netcap.StateUnavailable)

	owned.manager.beginTransition()
	captured := owned.dialer.captureLocalFamilyPolicy()
	capturedEpoch := captured.ownership.epoch

	// The transition commits without the epoch moving again.
	owned.manager.stable.Store(true)
	require.Equal(t, capturedEpoch, owned.manager.NetworkResetGeneration(),
		"a commit settles the state rather than advancing the epoch - that is why the flag is needed")

	require.False(t, captured.ownership.owns(),
		"a capture that began during a transition is not laundered into a valid one by the commit")
	require.False(t, captured.hardUndialable(address6))
}

// TestLegacyPolicyConstructionIsOwnedByConstruction keeps the helper used by tests and by callers that
// hold a verdict with no network ordering.
//
// It must behave exactly as the round before this one did - the verdict applies - because with no
// lifecycle in the picture there is no transition for it to be stale against.
func TestLegacyPolicyConstructionIsOwnedByConstruction(t *testing.T) {
	policy := newLocalFamilyPolicy(netcap.FamilyCapability{
		IPv4: netcap.StateProvisioned,
		IPv6: netcap.StateUnavailable,
	})
	require.True(t, policy.ownership.owns())
	require.True(t, policy.hardUndialable(address6))
	require.False(t, policy.hardUndialable(address4))
	require.False(t, policy.restrictsNothing())
}

func (m *ownershipManager) NetworkEnvironment() uint64 { return 0 }
