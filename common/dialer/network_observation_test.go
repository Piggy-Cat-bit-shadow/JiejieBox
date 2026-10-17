package dialer

import (
	"net/netip"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/netcap"
	"github.com/sagernet/sing/common/control"

	"github.com/stretchr/testify/require"
)

// One dialer, one source of network facts.
//
// # The shape these pin
//
// The dialer used to resolve FOUR optional capabilities independently and store four closures, and it had
// TWO capture functions - captureEpoch and captureLocalFamilyPolicy - each with its own precedence over
// those closures. The same defect was found and repaired in both of them, in separate commits, in the
// same shape: a manager that reported a fact was read through an accessor that could not carry it, and the
// answer it gave was silently dropped.
//
// Repair was possible at all because each defect was locally visible. What no local repair could fix was
// that the two captures could choose DIFFERENT interfaces for the same manager, and that the candidate
// scheduler took a further reading of the same instant (the environment fingerprint, separately from the
// verdict it belongs to).
//
// There is now one resolution, taken at construction, and one capture that every consumer projects from.
// The tests below are about the resolution itself - which interface an answer comes from - because that is
// the fact the previous structure could get wrong while every individual function stayed correct.

// countingNetworkManager implements every optional interface and counts how often each is used.
type countingNetworkManager struct {
	adapter.NetworkManager
	generation      atomic.Uint64
	settled         atomic.Bool
	familyCalls     atomic.Int32
	counterCalls    atomic.Int32
	stateCalls      atomic.Int32
	transitionCalls atomic.Int32
}

func newCountingNetworkManager() *countingNetworkManager {
	manager := &countingNetworkManager{}
	manager.settled.Store(true)
	manager.generation.Store(1)
	return manager
}

func (m *countingNetworkManager) NetworkResetGeneration() uint64 {
	m.counterCalls.Add(1)
	return m.generation.Load()
}

func (m *countingNetworkManager) NetworkTransitionStable() bool {
	m.stateCalls.Add(1)
	return m.settled.Load()
}

func (m *countingNetworkManager) NetworkTransitionSnapshot() (uint64, bool) {
	m.transitionCalls.Add(1)
	return m.generation.Load(), m.settled.Load()
}

// NetworkFamilyCapabilitySnapshot is the whole observation this manager can offer, and every assertion
// below expects the dialer to take it from here rather than from any narrower accessor.
func (m *countingNetworkManager) NetworkFamilyCapabilitySnapshot() (uint64, bool, adapter.NetworkFamilyCapability) {
	m.familyCalls.Add(1)
	return m.generation.Load(), m.settled.Load(), adapter.NetworkFamilyCapability{
		Capability: netcap.FamilyCapability{
			IPv4: netcap.StateProvisioned,
			IPv6: netcap.StateUnavailable,
		},
	}
}

// The manager methods a DefaultDialer consults at construction, all answering "nothing configured": this
// double exists to describe what the manager can REPORT about the network lifecycle, not to carry a
// configuration.
func (m *countingNetworkManager) DefaultOptions() adapter.NetworkOptions {
	return adapter.NetworkOptions{}
}

func (m *countingNetworkManager) AutoDetectInterface() bool { return false }

func (m *countingNetworkManager) AutoRedirectOutputMarkFunc() control.Func {
	return func(network, address string, conn syscall.RawConn) error { return nil }
}

func (m *countingNetworkManager) InterfaceFinder() control.InterfaceFinder {
	return control.NewDefaultInterfaceFinder()
}

var (
	_ adapter.NetworkFamilyCapabilitySnapshotter = (*countingNetworkManager)(nil)
	_ adapter.NetworkTransitionSnapshotter       = (*countingNetworkManager)(nil)
	_ adapter.NetworkTransitionState             = (*countingNetworkManager)(nil)
	_ adapter.NetworkResetCounter                = (*countingNetworkManager)(nil)
)

// generationPairManager reports the two individual getters and no snapshot.
type generationPairManager struct {
	adapter.NetworkManager
	generation atomic.Uint64
	settled    atomic.Bool
}

func (m *generationPairManager) NetworkResetGeneration() uint64 { return m.generation.Load() }
func (m *generationPairManager) NetworkTransitionStable() bool  { return m.settled.Load() }

var (
	_ adapter.NetworkResetCounter    = (*generationPairManager)(nil)
	_ adapter.NetworkTransitionState = (*generationPairManager)(nil)
	_                                = func() {
		var manager any = &generationPairManager{}
		if _, isSnapshotter := manager.(adapter.NetworkTransitionSnapshotter); isSnapshotter {
			panic("the double must not implement NetworkTransitionSnapshotter")
		}
	}
)

// stateOnlyManager reports the settled flag and nothing else.
type stateOnlyManager struct {
	adapter.NetworkManager
	settled atomic.Bool
}

func (m *stateOnlyManager) NetworkTransitionStable() bool { return m.settled.Load() }

// nothingManager reports no optional interface at all.
type nothingManager struct {
	adapter.NetworkManager
}

// TestTheRichestInterfaceIsAlwaysTheSource is the resolution rule, one shape at a time.
//
// The precedent for getting this wrong is in this package's history: a manager that reported a snapshot
// and not the narrower getter was read through the getter, and answered "no opinion" about a fact it had
// just reported. The rule is by capability, strongest first, and it is stated once.
func TestTheRichestInterfaceIsAlwaysTheSource(t *testing.T) {
	full := newCountingNetworkManager()
	require.Equal(t, sourceFamilyCapability, newNetworkObserver(full).source,
		"a manager offering the family observation must be read through it, even though it offers every "+
			"narrower one as well")

	family := newFamilySnapshotOnlyManager(netcap.StateProvisioned, netcap.StateUnavailable)
	require.Equal(t, sourceFamilyCapability, newNetworkObserver(family).source)

	require.Equal(t, sourceTransitionPair, newNetworkObserver(&pairSnapshotOnlyManager{}).source,
		"the pair outranks the two getters")

	require.Equal(t, sourceGeneration, newNetworkObserver(&generationPairManager{}).source,
		"two getters are the last resort before a single one")

	require.Equal(t, sourceStability, newNetworkObserver(&stateOnlyManager{}).source,
		"a settled flag with no generation is a source of its own")

	require.Equal(t, sourceNone, newNetworkObserver(&nothingManager{}).source)
	require.Equal(t, sourceNone, newNetworkObserver(nil).source,
		"a dialer built without a manager has no source and must not refuse anything")
}

// TestTheHealthNamespaceComesFromTheCapture is the scheduler's regression.
//
// The candidate scheduler namespaces what it learns about a family by the network it learned it on, and it
// used to be built from TWO readings: the environment fingerprint from one call, and the family verdict
// with its ownership from another. A transition landing between them scoped that scheduler's health to the
// network being left while its verdict and epoch came from the network being joined - and nothing
// downstream could notice, because both readings are individually correct and the composition is what was
// wrong.
//
// The namespace is now the generation, which arrives in the capture itself. There is no second reading
// left to disagree, and the assertion is that the scheduler holds exactly the generation its policy was
// captured in.
func TestTheHealthNamespaceComesFromTheCapture(t *testing.T) {
	manager := newCountingNetworkManager()
	manager.generation.Store(4242)

	owner := &DefaultDialer{network: newNetworkObserver(manager), familyHealth: newFamilyHealth()}
	scheduler := owner.newDualStackScheduler(0, admissionForTest(owner))

	require.EqualValues(t, 4242, scheduler.networkGeneration,
		"the scheduler's health namespace must be the generation in the observation it carries")
	require.EqualValues(t, 4242, scheduler.localFamily.ownership.epoch,
		"and it must be the same value the verdict is bound to, not a second reading of it")
	require.Equal(t, int32(1), manager.familyCalls.Load(), "one observation, not one per field")
	require.Equal(t, int32(0), manager.counterCalls.Load(),
		"and not a narrower accessor that could describe a different generation")
	require.Equal(t, int32(0), manager.transitionCalls.Load())
}

// TestTheHandOverCheckIsTheGatesOwnCapture keeps the two halves of a dial on one observation.
//
// captureEpoch used to be a second implementation of the capture, so the gate that decided the dial and
// the check that handed its result over could consult different interfaces. It is now the ownership half
// of the very same capture, and this asserts that neither asks anything but the resolved source - which is
// what makes "the same observation" structural rather than a rule each call site has to remember.
func TestTheHandOverCheckIsTheGatesOwnCapture(t *testing.T) {
	manager := newCountingNetworkManager()
	manager.generation.Store(7)

	owner := &DefaultDialer{network: newNetworkObserver(manager)}
	handOver := owner.captureEpoch()

	require.Equal(t, int32(1), manager.familyCalls.Load(), "the capture comes from the resolved source")
	require.Equal(t, int32(0), manager.counterCalls.Load(),
		"and never from a narrower accessor that could describe a different generation")
	require.Equal(t, int32(0), manager.transitionCalls.Load())

	require.True(t, handOver(), "the generation has not moved")

	manager.generation.Store(8)
	require.False(t, handOver(), "a superseded capture is refused at hand-over")
	require.Equal(t, int32(0), manager.counterCalls.Load(),
		"the predicate re-reads the same source, so the narrow accessor is still untouched")
}

// TestASettledFlagOnlyManagerCannotLaunderADuringCapture pins the one behaviour this unification
// deliberately tightens.
//
// A manager that can report only "is the network settled" used to be checked for that at hand-over and NOT
// for the state the dial began in, so a dial started during a transition became acceptable the moment the
// transition committed: it was produced while ownership was unresolved and was handed over because the
// network merely settled afterwards. The one formula now applies the "began settled" term wherever the
// source can report the settled state at all, which is the direction the rest of this package already
// documents as required.
//
// No production manager has this shape, so nothing observable changes for a real device; the point is that
// the degradation is now the same rule as every other shape rather than a separate, weaker one.
func TestASettledFlagOnlyManagerCannotLaunderADuringCapture(t *testing.T) {
	manager := &stateOnlyManager{}
	manager.settled.Store(false)

	owner := &DefaultDialer{network: newNetworkObserver(manager)}
	handOver := owner.captureEpoch()
	require.False(t, handOver(), "a dial begun while unsettled must not be handed over")

	manager.settled.Store(true)
	require.False(t, handOver(),
		"and a later commit must not launder it: a source with no generation still knows the dial BEGAN "+
			"unsettled, and that is the fact that disqualifies it")

	require.True(t, owner.captureEpoch()(),
		"a dial begun after the commit is accepted, so this is not 'refuse everything'")
}

// TestThePolicyCarriesTheObservationItWasBuiltFrom pins that the verdict and its ownership are one value.
func TestThePolicyCarriesTheObservationItWasBuiltFrom(t *testing.T) {
	manager := newCountingNetworkManager()
	manager.generation.Store(99)

	policy := (&DefaultDialer{network: newNetworkObserver(manager)}).captureLocalFamilyPolicy()
	require.EqualValues(t, 99, policy.ownership.epoch)
	require.True(t, policy.ownership.owns())
	require.False(t, policy.restrictsNothing(),
		"the verdict in this observation says IPv6 is unavailable, so the policy restricts something")
	require.True(t, policy.hardUndialable(netip.MustParseAddr("2001:4860:4860::8888")))
	require.False(t, policy.hardUndialable(netip.MustParseAddr("192.0.2.7")))
}

// TestAFamilyVerdictIsNeverTakenWithoutItsOwnership protects the invariant that motivates the value type.
//
// A manager that reports an epoch and no verdict must still get a hand-over check, and a manager that
// reports a verdict must never be trusted without one. Both are assertions about the same accessor, which
// is why they are here together: the two halves cannot be obtained separately, so a caller cannot hold one
// and forget the other.
func TestAFamilyVerdictIsNeverTakenWithoutItsOwnership(t *testing.T) {
	generation := &generationPairManager{}
	generation.generation.Store(5)
	generation.settled.Store(true)

	policy := (&DefaultDialer{network: newNetworkObserver(generation)}).captureLocalFamilyPolicy()
	require.NotNil(t, policy.ownership.predicate, "an epoch is enough to get a hand-over check")
	require.True(t, policy.restrictsNothing(), "and with no verdict there is nothing to restrict")

	generation.generation.Store(6)
	require.False(t, policy.ownership.owns(), "the check still notices a reset")
}
