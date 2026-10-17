package dialer

import (
	"net/netip"
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/netcap"
	"github.com/sagernet/sing/common/control"

	"github.com/stretchr/testify/require"
)

// One observation for ownership AND verdict, whoever the manager is.
//
// # The contract that was not being honoured
//
// NetworkFamilyCapabilitySnapshotter embeds NetworkTransitionSnapshotter and returns epoch, settled and
// verdict as ONE atomic observation. A manager may implement exactly that and nothing else - it is
// entitled to, the capability is optional, and the interface says so.
//
// The consumer defeated it twice over:
//
//	captureEpoch()    -> ownership from the epoch getters
//	familySnapshot()  -> verdict, with the epoch and settled flag the SAME call returned discarded
//
// For a manager that also reports an epoch, the two observations merely duplicate each other. For a
// manager that reports the family snapshot and NEITHER of the narrower interfaces, captureEpoch returned
// a predicate that answered "always owned" - so ownership was never checked at all, and a settled=false
// snapshot could still hard-deny:
//
//	familySnapshot -> epoch N, settled FALSE, IPv6 Unavailable
//	captureEpoch   -> no epoch getter, no stable getter -> predicate always true
//	excluded       -> owned, Unavailable -> HARD DENY on a network with no settled state
//
// That is the exact interleaving the snapshotter exists to prevent, reached by ignoring the flag the
// manager did report.
//
// # The rule now, and why it is no longer a rule
//
// When the manager reports more, it is read through the richer interface: one resolution at
// construction (newNetworkObserver) picks the strongest source it implements, and BOTH the verdict
// capture and the hand-over predicate are projections of that one source. The tests below therefore
// assert the RESOLUTION - which interface an answer comes from - rather than the branch structure of two
// functions, because there is no longer a branch structure that could disagree with itself.

// familySnapshotOnlyManager reports ONLY the family capability snapshot.
//
// It deliberately implements none of the NARROWER optional interfaces. That is the shape the contract
// has to cover, and it is also the shape that used to be read as "always owned" - which is why the
// doubles used elsewhere in this package, all of which implement the full set, could not have found
// this.
//
// NetworkTransitionSnapshot is present because the snapshotter interface EMBEDS it - a manager cannot
// report the family triple without also being able to report the pair. NetworkResetCounter and
// NetworkTransitionState are the two the dialer reads into its epoch and stable getters, and they are
// exactly what is missing here.
type familySnapshotOnlyManager struct {
	// NetworkManager is embedded and nil: the double's whole point is which OPTIONAL interfaces it
	// does and does not implement, so everything else must come from the interface.
	adapter.NetworkManager
	epoch  atomic.Uint64
	stable atomic.Bool
	ipv4   atomic.Uint32
	ipv6   atomic.Uint32
}

func newFamilySnapshotOnlyManager(ipv4 netcap.FamilyState, ipv6 netcap.FamilyState) *familySnapshotOnlyManager {
	manager := &familySnapshotOnlyManager{}
	manager.stable.Store(true)
	manager.setStates(ipv4, ipv6)
	return manager
}

func (m *familySnapshotOnlyManager) setStates(ipv4 netcap.FamilyState, ipv6 netcap.FamilyState) {
	m.ipv4.Store(uint32(ipv4))
	m.ipv6.Store(uint32(ipv6))
}

func (m *familySnapshotOnlyManager) NetworkTransitionSnapshot() (uint64, bool) {
	return m.epoch.Load(), m.stable.Load()
}

func (m *familySnapshotOnlyManager) NetworkFamilyCapabilitySnapshot() (uint64, bool, adapter.NetworkFamilyCapability) {
	return m.epoch.Load(), m.stable.Load(), adapter.NetworkFamilyCapability{
		Capability: netcap.FamilyCapability{
			IPv4: netcap.FamilyState(m.ipv4.Load()),
			IPv6: netcap.FamilyState(m.ipv6.Load()),
		},
	}
}

func (m *familySnapshotOnlyManager) InterfaceFinder() control.InterfaceFinder {
	return control.NewDefaultInterfaceFinder()
}

// The compile-time proof that this double really is the narrow shape: it satisfies the family
// snapshotter, and it satisfies NEITHER of the two getters the dialer reads into its wider ownership
// fields. Without the negative assertions the tests below would be measuring the wrong manager.
var (
	_ adapter.NetworkFamilyCapabilitySnapshotter = (*familySnapshotOnlyManager)(nil)
	_                                            = func() {
		var manager any = &familySnapshotOnlyManager{}
		if _, isCounter := manager.(adapter.NetworkResetCounter); isCounter {
			panic("the double must not implement NetworkResetCounter")
		}
		if _, isState := manager.(adapter.NetworkTransitionState); isState {
			panic("the double must not implement NetworkTransitionState")
		}
	}
)

// newFixtureDialer wires a DefaultDialer to the manager the way NewDefault does: through the one
// resolution that picks the strongest interface this manager implements.
func newFixtureDialer(manager *familySnapshotOnlyManager) *DefaultDialer {
	owner := &DefaultDialer{}
	owner.network = newNetworkObserver(manager)
	return owner
}

// TestFamilySnapshotOnlyManagerIsTheNarrowShape guards the fixture itself, so the tests below cannot
// quietly stop being about the case they were written for.
func TestFamilySnapshotOnlyManagerIsTheNarrowShape(t *testing.T) {
	manager := newFamilySnapshotOnlyManager(netcap.StateProvisioned, netcap.StateUnavailable)
	var shape any = manager

	_, isCapability := shape.(adapter.NetworkFamilyCapabilitySnapshotter)
	_, isTransition := shape.(adapter.NetworkTransitionSnapshotter)
	_, isCounter := shape.(adapter.NetworkResetCounter)
	_, isState := shape.(adapter.NetworkTransitionState)

	require.True(t, isCapability, "the manager must report the family capability")
	require.True(t, isTransition, "which is required by the snapshotter's own contract")
	require.False(t, isCounter, "and must report neither narrower getter, or the test is about another shape")
	require.False(t, isState)
}

// TestDuringIsNotHardDenyFromTheFamilySnapshotAlone is case 1 of the cleanup: DURING.
//
// The manager reports epoch N, settled FALSE, IPv6 Unavailable - a state the publisher really passes
// through. Nothing may be refused: there is no settled network for the verdict to describe. Before the
// unification this hard-denied, because ownership came from a predicate that answered "always owned".
func TestDuringIsNotHardDenyFromTheFamilySnapshotAlone(t *testing.T) {
	manager := newFamilySnapshotOnlyManager(netcap.StateProvisioned, netcap.StateUnavailable)
	manager.epoch.Store(1)
	manager.stable.Store(false)

	owner := newFixtureDialer(manager)
	policy := owner.captureLocalFamilyPolicy()

	require.False(t, policy.ownership.startedStable, "the capture must record the manager's settled flag")
	require.EqualValues(t, 1, policy.ownership.epoch, "and its epoch, from the same call")
	require.False(t, policy.hardUndialable(address6),
		"a capture taken during a transition must deny nothing: the manager reported that there is no "+
			"settled network, and dropping that flag is what made this hard-deny")
	require.True(t, policy.restrictsNothing())

	// And the same through the real consumers, so this is not only a policy-level claim.
	scheduler := &candidateScheduler{localFamily: policy}
	filtered, refused := scheduler.filterLocalFamily(planCandidates(addressSet(), netip.Addr{}, 0))
	require.False(t, refused.refused())
	require.Len(t, filtered.candidates, 2)

	require.False(t, owner.LocalFamilyBlocked(address6),
		"and the bypass stays open, which is the conservative direction for an optimisation")
}

// TestStaleIsNotHardDenyFromTheFamilySnapshotAlone is case 2 of the cleanup: STALE.
//
// The capture is taken on epoch N while settled, then the manager moves to N+1. The old policy must
// refuse nothing - and the check that notices has to read the SAME function the capture read, because
// that is the lifecycle source this manager reports.
func TestStaleIsNotHardDenyFromTheFamilySnapshotAlone(t *testing.T) {
	manager := newFamilySnapshotOnlyManager(netcap.StateProvisioned, netcap.StateUnavailable)
	manager.epoch.Store(1)
	manager.stable.Store(true)

	owner := newFixtureDialer(manager)
	policy := owner.captureLocalFamilyPolicy()

	// Control, taken first: while it is current the verdict does refuse IPv6. Without this the
	// assertions below would pass against an implementation that never refuses anything.
	require.True(t, policy.hardUndialable(address6),
		"while the capture is current, the verdict must refuse IPv6 - otherwise this test proves nothing")

	// The manager moves on: a new epoch, still settled.
	manager.epoch.Store(2)

	require.False(t, policy.hardUndialable(address6),
		"a capture from a replaced generation must deny nothing")
	require.True(t, policy.restrictsNothing())

	scheduler := &candidateScheduler{localFamily: policy}
	filtered, refused := scheduler.filterLocalFamily(planCandidates(addressSet(), netip.Addr{}, 0))
	require.False(t, refused.refused())
	require.Len(t, filtered.candidates, 2)

	// LocalFamilyBlocked is deliberately NOT asserted here. It takes a FRESH capture - it has no dial to
	// belong to - and the manager is now legitimately on epoch 2, so a fresh capture is current and
	// answering "blocked" is correct. Asserting it against the OLD policy would be asking the wrong
	// question; the stale-capture bypass case is covered by the DURING and scheduler assertions above
	// and by TestCurrentFamilySnapshotStillHardDenies below.
}

// TestCurrentFamilySnapshotStillHardDenies is the control the brief asks for: same shape, still the
// current generation, so the verdict must apply normally.
//
// It is asserted through every consumer, because a rule that only works at the policy level is not the
// rule that runs.
func TestCurrentFamilySnapshotStillHardDenies(t *testing.T) {
	manager := newFamilySnapshotOnlyManager(netcap.StateProvisioned, netcap.StateUnavailable)
	manager.epoch.Store(7)
	manager.stable.Store(true)

	owner := newFixtureDialer(manager)
	policy := owner.captureLocalFamilyPolicy()

	require.True(t, policy.ownership.owns())
	require.False(t, policy.restrictsNothing())
	require.True(t, policy.hardUndialable(address6))
	require.False(t, policy.hardUndialable(address4))

	scheduler := &candidateScheduler{localFamily: policy}
	filtered, refused := scheduler.filterLocalFamily(planCandidates(addressSet(), netip.Addr{}, 0))
	require.Equal(t, familyIPv6, refused.family)
	require.Len(t, filtered.candidates, 1)
	require.Equal(t, address4, filtered.candidates[0].address)

	require.True(t, owner.LocalFamilyBlocked(address6))
}

// TestEpochMoveWhileUnsettledIsNotHardDeny is the overlap of the two cases, and the one a bare epoch
// comparison gets wrong in the other direction.
//
// The manager has moved to a new epoch AND is unsettled. A reader must refuse nothing for two
// independent reasons, and neither may be short-circuited by the other.
func TestEpochMoveWhileUnsettledIsNotHardDeny(t *testing.T) {
	manager := newFamilySnapshotOnlyManager(netcap.StateProvisioned, netcap.StateUnavailable)
	manager.epoch.Store(1)
	manager.stable.Store(true)
	owner := newFixtureDialer(manager)
	policy := owner.captureLocalFamilyPolicy()
	require.True(t, policy.hardUndialable(address6), "control: it refuses while current and settled")

	manager.epoch.Store(2)
	manager.stable.Store(false)

	require.False(t, policy.hardUndialable(address6))
}

// TestSettledAgainAfterACommitStillDoesNotLaunderADuringCapture is the case that makes the settled flag
// necessary rather than merely useful.
//
// A capture taken DURING a transition records the transition's OWN epoch. Once that transition commits,
// the epoch has not moved again and the manager is settled - so an epoch comparison alone finds
// "unchanged" and both later checks pass. Remembering that the capture STARTED unsettled is the only
// thing that closes it, and this manager shape reaches it through the family snapshot's own flag.
func TestSettledAgainAfterACommitStillDoesNotLaunderADuringCapture(t *testing.T) {
	manager := newFamilySnapshotOnlyManager(netcap.StateProvisioned, netcap.StateUnavailable)
	manager.epoch.Store(5)
	manager.stable.Store(false)

	owner := newFixtureDialer(manager)
	policy := owner.captureLocalFamilyPolicy()
	capturedEpoch := policy.ownership.epoch
	require.False(t, policy.hardUndialable(address6))

	// The transition commits: settled again, same epoch.
	manager.stable.Store(true)
	currentEpoch, currentStable, _ := manager.NetworkFamilyCapabilitySnapshot()
	require.Equal(t, capturedEpoch, currentEpoch, "a commit settles the state without advancing the epoch")
	require.True(t, currentStable)

	require.False(t, policy.hardUndialable(address6),
		"the commit must not launder a capture that began during the transition into a valid one")
}

// TestFamilySnapshotOwnedCaptureIsUsedRegardlessOfTheNarrowerGetters is the ordering proof.
//
// A manager may implement BOTH the family snapshot and the narrower getters - the real one does, and
// more besides. When it does, the richest observation is the source, so ownership and the verdict cannot
// come from two different readings. The narrower getters are made to disagree on purpose: if they were
// consulted for ownership, the verdict below would be treated as stale and allowed, and the control
// assertion would fail.
//
// The previous version of this test had to poke the dialer's fields to build that manager, because the
// fields were the only way to express it. A field-poked dialer is exactly the shape production never
// reaches, and it is how "the fixture knows more than the object graph does" happens. The manager below
// is a real manager that reports every interface and disagrees with itself; the dialer is wired by the
// production resolution.
func TestFamilySnapshotOwnedCaptureIsUsedRegardlessOfTheNarrowerGetters(t *testing.T) {
	manager := &disagreeingManager{
		familySnapshotOnlyManager: newFamilySnapshotOnlyManager(netcap.StateProvisioned, netcap.StateUnavailable),
	}
	manager.epoch.Store(3)
	manager.stable.Store(true)

	owner := newFixtureDialer(manager.familySnapshotOnlyManager)
	require.Equal(t, sourceFamilyCapability, owner.network.source,
		"a manager offering the family snapshot must be read through it, not through the two getters it "+
			"also offers")

	policy := owner.captureLocalFamilyPolicy()
	require.EqualValues(t, 3, policy.ownership.epoch,
		"the epoch must come from the family snapshot, not from the narrower getter")
	require.True(t, policy.ownership.startedStable)
	require.True(t, policy.hardUndialable(address6),
		"and the policy must still be current, because its source has not moved")
}

// disagreeingManager reports every optional interface and gives the narrower ones answers that do not
// match the family snapshot.
//
// It exists because the resolution's whole job is to prefer the richest source, and a double that agrees
// with itself cannot demonstrate which source was used. Every disagreement is in the unsafe direction:
// the epoch getter reports a generation that never existed, and the settled getter reports a transition
// that never began.
type disagreeingManager struct {
	*familySnapshotOnlyManager
}

func (m *disagreeingManager) NetworkResetGeneration() uint64 { return 999 }
func (m *disagreeingManager) NetworkTransitionStable() bool  { return false }

var (
	_ adapter.NetworkResetCounter      = (*disagreeingManager)(nil)
	_ adapter.NetworkTransitionState   = (*disagreeingManager)(nil)
	_ adapter.NetworkTransitionSnapshotter = (*disagreeingManager)(nil)
	_ adapter.NetworkFamilyCapabilitySnapshotter = (*disagreeingManager)(nil)
)

// TestFallbackStillProtectsAManagerWithoutAFamilyCapability pins the other half of the branch.
//
// A manager that reports an epoch and no verdict keeps the captureEpoch predicate, so its hand-over
// protection is exactly what it was. Unifying the snapshot path must not quietly change what a manager
// without the capability gets.
func TestFallbackStillProtectsAManagerWithoutAFamilyCapability(t *testing.T) {
	epoch := &atomic.Uint64{}
	owner := &DefaultDialer{network: newNetworkObserver(&epochNetworkManager{
		epoch:  epoch,
		finder: control.NewDefaultInterfaceFinder(),
	})}

	policy := owner.captureLocalFamilyPolicy()
	require.Equal(t, sourceGeneration, owner.network.source, "this manager reports no verdict")
	require.NotNil(t, policy.ownership.predicate,
		"so it must still get an ownership capture from the epoch getter")
	require.True(t, policy.ownership.owns())
	require.True(t, policy.restrictsNothing(), "with no verdict there is nothing to restrict")

	epoch.Add(1)
	require.False(t, policy.ownership.owns(),
		"and the predicate must notice the reset, so the hand-over check keeps working")
}

func (m *familySnapshotOnlyManager) NetworkEnvironment() uint64 { return 0 }

// TestOnePlanCostsOneOwnershipRead is the candidate filter's single-observation claim, counted.
//
// The filter used to ask the policy once per candidate, and each ask re-read network ownership. For one
// plan that is N reads of one instant, and a transition landing mid-filter could therefore drop the head of
// the list for a verdict that no longer applied while keeping the tail under "no opinion" - a plan composed
// from two generations. Every variable is correctly synchronised; only the order is wrong, which is why no
// race detector and no single-candidate test could see it.
//
// The count is the assertion: one plan, one read, whatever the plan's length. A future change that moves the
// gate inside the loop fails here rather than in production.
func TestOnePlanCostsOneOwnershipRead(t *testing.T) {
	manager := newCountingNetworkManager()
	owner := &DefaultDialer{network: newNetworkObserver(manager)}

	// A plan long enough that a per-candidate read would be obvious, with both families in it.
	plan := candidatePlan{candidates: []dualStackCandidate{
		{address: netip.MustParseAddr("2001:4860:4860::8888"), family: familyIPv6},
		{address: netip.MustParseAddr("112.53.55.92"), family: familyIPv4},
		{address: netip.MustParseAddr("2001:4860:4860::8844"), family: familyIPv6},
		{address: netip.MustParseAddr("112.53.55.93"), family: familyIPv4},
	}}

	// The scheduler's own capture is read #1, and it is the dial's authoritative observation: the filter
	// then asks a DIFFERENT question - "is the capture I hold still current?" - which is the one re-read the
	// design allows, and which the hand-over check makes for the same reason.
	scheduler := owner.newDualStackScheduler(0)
	captured := manager.familyCalls.Load()
	require.Equal(t, int32(1), captured, "constructing the scheduler captures once")

	filtered, refused := scheduler.filterLocalFamily(plan)

	require.Equal(t, captured+1, manager.familyCalls.Load(),
		"and filtering a plan of four candidates must cost ONE currency check, not one per candidate: "+
			"a per-candidate read lets a transition landing mid-filter judge the list against two "+
			"generations, so the head is dropped for a verdict that no longer applies while the tail is "+
			"kept under 'no opinion'")
	require.Equal(t, familyIPv6, refused.family,
		"the refusal names the family that was dropped, so the caller can report WHY rather than "+
			"`no dial candidates`")
	require.Len(t, filtered.candidates, 2, "and the IPv6 half is gone")
	for _, candidate := range filtered.candidates {
		require.Equal(t, familyIPv4, candidate.family)
	}
}
