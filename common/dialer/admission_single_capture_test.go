package dialer

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/netcap"
	C "github.com/sagernet/sing-box/constant"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// One flow, one worldview - proved by making the network CHANGE its answer mid-flow.
//
// # Why not just count the reads
//
// Counting looked like the obvious seam and it is the wrong one. Every ownership check
// (localDialOwnership.owns) re-reads the snapshot too, because detecting a superseded generation REQUIRES
// a fresh look - that is invariant B, and removing it would be a regression, not a saving. So a per-flow
// read count mixes "captured a policy" with "validated that my policy is still current", and any
// threshold over it would be a number nobody could defend.
//
// The property that actually has to hold is narrower and observable: THE VERDICT A FLOW APPLIES IS THE ONE
// IT CAPTURED. So the manager below answers differently on every read, and each test asks which answer
// the stage used:
//
//	first read   IPv6 unavailable   <- the flow's capture, and the answer it must keep using
//	later reads  IPv6 provisioned   <- a fresher reading the flow must NOT substitute for its own
//
// A stage that re-captured would silently adopt the later answer, and the flow would then be deciding on
// two different networks - the defect this phase exists to remove. Before the per-flow policy existed
// that is exactly what happened, and no test could see it because every fixture answered the same way
// every time.

// scriptedObservationManager hands out one capability per read and repeats the last one.
type scriptedObservationManager struct {
	adapter.NetworkManager

	access   sync.Mutex
	reads    int
	verdicts []adapter.NetworkFamilyCapability
	epoch    uint64
	stable   bool
}

func (m *scriptedObservationManager) next() adapter.NetworkFamilyCapability {
	m.access.Lock()
	defer m.access.Unlock()
	index := m.reads
	m.reads++
	if len(m.verdicts) == 0 {
		return adapter.NetworkFamilyCapability{}
	}
	if index >= len(m.verdicts) {
		index = len(m.verdicts) - 1
	}
	return m.verdicts[index]
}

func (m *scriptedObservationManager) readCount() int {
	m.access.Lock()
	defer m.access.Unlock()
	return m.reads
}

func (m *scriptedObservationManager) NetworkFamilyCapabilitySnapshot() (uint64, bool, adapter.NetworkFamilyCapability) {
	// The epoch and the settled flag are read UNDER THE LOCK every writer takes.
	//
	// The first version returned them straight from the fields and guarded only `next()`, and
	// `go test -race` reported it: four of this round's six race reports came from this one line, against
	// any test that advanced the epoch while a flow was reading it. The fixture is where a race is easiest
	// to miss, because a fixture that is only ever read sequentially hides the defect until a test grows a
	// concurrent writer - which is exactly what the stress and transition tests did.
	m.access.Lock()
	epoch, stable := m.epoch, m.stable
	m.access.Unlock()
	return epoch, stable, m.next()
}

// NetworkTransitionSnapshot is part of the same optional contract, and it is not optional here:
// NetworkFamilyCapabilitySnapshotter EMBEDS NetworkTransitionSnapshotter, so a manager that answers only
// the family snapshotter is not one - the observer would fall back to "no source" and then every
// assertion below would pass by measuring a flow that was never told anything.
func (m *scriptedObservationManager) NetworkTransitionSnapshot() (uint64, bool) {
	return m.epoch, m.stable
}

// capWith builds one capability value with the egress term left Unknown, so the family term is the only
// thing in play.
func capWith(ipv4, ipv6 netcap.FamilyState) adapter.NetworkFamilyCapability {
	return adapter.NetworkFamilyCapability{Capability: netcap.FamilyCapability{IPv4: ipv4, IPv6: ipv6}}
}

// scriptedLeaf is the smallest dialer that is BOTH a family-health owner and a local-family owner, which
// is what a resolve wrapper walks to.
type scriptedLeaf struct {
	owner *DefaultDialer
	inner *stallingDialer
}

func (d *scriptedLeaf) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	return d.inner.DialContext(ctx, network, destination)
}

func (d *scriptedLeaf) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return d.inner.ListenPacket(ctx, destination)
}

func (d *scriptedLeaf) newDualStackScheduler(fallbackDelay time.Duration, admission admissionPolicy) *candidateScheduler {
	return d.owner.newDualStackScheduler(fallbackDelay, admission)
}

func (d *scriptedLeaf) localFamilyPolicy() localFamilyPolicy {
	return d.owner.localFamilyPolicy()
}

func (d *scriptedLeaf) tried() []netip.Addr {
	d.inner.access.Lock()
	defer d.inner.access.Unlock()
	return append([]netip.Addr(nil), d.inner.tried...)
}

// newChangingNetworkChain builds a resolve dialer over a leaf whose manager changes its answer after the
// first read.
func newChangingNetworkChain(t *testing.T, first adapter.NetworkFamilyCapability) (*resolveDialer, *scriptedLeaf, *scriptedObservationManager) {
	t.Helper()
	manager := &scriptedObservationManager{
		verdicts: []adapter.NetworkFamilyCapability{first, capWith(netcap.StateProvisioned, netcap.StateProvisioned)},
		epoch:    7,
		stable:   true,
	}
	owner := &DefaultDialer{familyHealth: newFamilyHealth()}
	owner.network = newNetworkObserver(manager)

	inner := newStallingDialer()
	inner.stall = nil
	inner.connectDelay = 0
	inner.packetConn = &countingPacketConn{}
	leaf := &scriptedLeaf{owner: owner, inner: inner}

	return &resolveDialer{dialer: leaf, fallbackDelay: 10 * time.Millisecond}, leaf, manager
}

// TestTheSchedulerUsesTheFlowsCapturedVerdict is §30 Q7: a scheduler must not refresh its own worldview.
//
// The flow captures a policy that says IPv6 is unavailable. The manager's NEXT answer says IPv6 is fine.
// A scheduler built from a fresh reading would accept the IPv6 candidate - and the scheduler is the object
// that removes candidates BEFORE a socket exists, so nothing downstream could rescue a family it wrongly
// dropped or wrongly kept.
func TestTheSchedulerUsesTheFlowsCapturedVerdict(t *testing.T) {
	resolver, leaf, manager := newChangingNetworkChain(t, capWith(netcap.StateProvisioned, netcap.StateUnavailable))
	ipv6 := netip.MustParseAddr("2001:db8::2c")

	admission := resolver.captureAdmissionPolicy()
	require.Equal(t, admissionNoLocalFamily, admission.verdict(ipv6),
		"the fixture must capture the verdict this test is about")

	// The network now says something else. Anything that re-reads will disagree with the capture.
	require.Equal(t, capWith(netcap.StateProvisioned, netcap.StateProvisioned), manager.next(),
		"the fixture must change its answer, or the assertion below proves nothing")
	manager.access.Lock()
	manager.reads = len(manager.verdicts) - 1 // the manager now always answers "dual-stack"
	manager.access.Unlock()

	scheduler := leaf.newDualStackScheduler(time.Millisecond, admission)
	// Field-wise, because localFamilyPolicy holds a predicate func and reflect.DeepEqual refuses to
	// compare funcs - a struct comparison would fail for a reason unrelated to the rule under test.
	require.Equal(t, admission.localFamily.ownership.epoch, scheduler.localFamily.ownership.epoch)
	require.Equal(t, admission.localFamily.ownership.startedStable, scheduler.localFamily.ownership.startedStable)
	require.Equal(t, admission.localFamily.capability, scheduler.localFamily.capability,
		"the scheduler took its family verdict from somewhere other than the flow's policy")
	require.EqualValues(t, admission.generation(), scheduler.networkGeneration,
		"and its health namespace from somewhere other than the flow's policy")

	plan := planCandidates([]netip.Addr{ipv6}, netip.Addr{}, C.DomainStrategyAsIS)
	_, refused := scheduler.filterLocalFamily(plan)
	require.Equal(t, familyIPv6, refused.family,
		"the scheduler admitted an IPv6 candidate its flow had already refused. It re-read the network "+
			"instead of using the policy it was given, which is how one connection ends up planned against "+
			"one network and scheduled against another")
}

// TestRecoveryUsesTheFlowsCapturedVerdict is the same question for the other stage that used to re-capture.
//
// Recovery's filter decides which family ENDS the lookup, so a stale or refreshed answer here does not
// merely cost an attempt: it stops the lookup on a family the policy forbade, or it discards the family
// the policy admitted while the other answer is still in flight.
func TestRecoveryUsesTheFlowsCapturedVerdict(t *testing.T) {
	resolver, _, _ := newChangingNetworkChain(t, capWith(netcap.StateProvisioned, netcap.StateUnavailable))
	ipv4 := netip.MustParseAddr("192.0.2.92")
	ipv6 := netip.MustParseAddr("2001:db8::2c")

	admission := resolver.captureAdmissionPolicy()

	recovered := resolver.admittedRecoveredCandidates([]netip.Addr{ipv6, ipv4}, admission)
	require.Equal(t, []netip.Addr{ipv4}, recovered,
		"recovery kept an IPv6 candidate the flow's policy had already refused: its filter used a "+
			"different reading of the network than the flow's own")
}

// TestAPacketFlowUsesItsCapturedVerdict covers the arm where the phase found a live defect.
//
// A fix that applied the policy by capturing a SECOND time would pass the behaviour tests and still
// violate the flow's single worldview, so the arm is checked the same way as the stream path: the flow
// captures "IPv6 unavailable", the manager then answers "dual-stack", and the domain arm must still
// refuse the IPv6 answer.
func TestAPacketFlowUsesItsCapturedVerdict(t *testing.T) {
	resolver, leaf, _ := newChangingNetworkChain(t, capWith(netcap.StateProvisioned, netcap.StateUnavailable))
	ipv6 := netip.MustParseAddr("2001:db8::2c")

	router := newScriptedFamilyRouter()
	router.addressesA = nil
	router.addressesAAAA = []netip.Addr{ipv6}
	resolver.router = router

	conn, err := resolver.ListenPacket(context.Background(), M.Socksaddr{Fqdn: "v6only.example", Port: 53})
	if conn != nil {
		conn.Close()
	}
	require.Error(t, err, "the flow's own verdict refuses IPv6, so the flow must fail rather than adopt "+
		"the manager's later, more permissive answer")
	require.Empty(t, leaf.tried(), "and no socket may be created for the refused family")
}

// TestTheFlowsPolicyAndItsSchedulerShareOneGeneration pins §11 for the wiring rather than the value.
//
// The scheduler's health namespace and its family verdict come from the SAME capture as the flow's
// decision. They used to be two: the owner captured the verdict for the scheduler while the wrapper read
// the generation separately, so a transition landing between them filed one network's health under
// another network's identity.
func TestTheFlowsPolicyAndItsSchedulerShareOneGeneration(t *testing.T) {
	const epoch = 0xBEEF
	manager := &scriptedObservationManager{
		verdicts: []adapter.NetworkFamilyCapability{capWith(netcap.StateProvisioned, netcap.StateProvisioned)},
		epoch:    epoch,
		stable:   true,
	}
	owner := &DefaultDialer{familyHealth: newFamilyHealth()}
	owner.network = newNetworkObserver(manager)

	admission := (&resolveDialer{dialer: &scriptedLeaf{owner: owner, inner: newStallingDialer()}}).captureAdmissionPolicy()
	scheduler := owner.newDualStackScheduler(time.Millisecond, admission)

	require.EqualValues(t, epoch, admission.generation())
	require.EqualValues(t, epoch, scheduler.networkGeneration,
		"the scheduler's health namespace came from somewhere other than the flow's policy")
	require.EqualValues(t, admission.generation(), scheduler.networkGeneration)

	// Field-wise, because localFamilyPolicy holds a predicate func and require.Equal refuses to compare
	// funcs - which would have made a struct comparison fail for the wrong reason.
	require.Equal(t, admission.localFamily.ownership.epoch, scheduler.localFamily.ownership.epoch)
	require.Equal(t, admission.localFamily.ownership.startedStable, scheduler.localFamily.ownership.startedStable)
	require.Equal(t, admission.localFamily.capability, scheduler.localFamily.capability)
	require.Equal(t, 1, manager.readCount(), "one capture produced both fields")
}
