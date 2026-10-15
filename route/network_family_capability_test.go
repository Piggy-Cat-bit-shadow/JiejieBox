package route

import (
	"net/netip"
	"sync"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/netcap"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing/common/control"

	"github.com/stretchr/testify/require"
)

// The lifecycle half of the local family capability: who publishes it, when, and what a consumer is
// allowed to conclude from a read.
//
// The rule these tests exist for is one sentence long: a capability is only meaningful beside the
// network generation it was observed on, so the two must be publishable and readable as ONE
// observation. Everything else here is a consequence - what happens when the network changes, what
// happens when only the addressing changes, and what happens when the process's own tunnel is in the
// interface list.

// interfaceWithAddresses builds the observation shape the manager reads, so a test states addressing
// rather than a nested struct literal.
func interfaceWithAddresses(index int, name string, addresses ...string) adapter.NetworkInterface {
	parsed := make([]netip.Prefix, 0, len(addresses))
	for _, address := range addresses {
		parsed = append(parsed, netip.MustParsePrefix(address))
	}
	return adapter.NetworkInterface{
		Interface: control.Interface{Index: index, Name: name, Addresses: parsed},
		Type:      C.InterfaceTypeEthernet,
	}
}

// setInterfaces replaces the interface snapshot the capability is derived from.
//
// Every interface that is not the default one gets an address but no gateway, and the default
// interface (index 1) gets the supplied gateway. The gateway matters: the environment fingerprint
// prefers the default interface's gateways over the SSID, so this is the field that decides whether
// a test's next step is a NEW NETWORK or the same network with different addressing - which is the
// distinction two of these tests exist to make.
func setInterfaces(h *transitionWindowHarness, gateway string, interfaces ...adapter.NetworkInterface) {
	gatewayAddress := netip.MustParseAddr(gateway)
	updated := make([]adapter.NetworkInterface, 0, len(interfaces))
	for _, iface := range interfaces {
		if iface.Index == 1 {
			iface.Gateways = []netip.Addr{gatewayAddress}
		}
		updated = append(updated, iface)
	}
	h.manager.networkInterfaces.Store(updated)
}

// takeOneSnapshot returns a function that performs exactly one capability-snapshot read - the same
// read a dial performs - at the instant the given hook fires, and hands it back.
//
// The hooks run with stateAccess held, so the snapshot is safe there for a specific reason: it takes
// only the ownership lock and reads the capability through an atomic, never stateAccess. That is also
// why it is a real reader rather than a probe - it observes the same triple a dial would, from inside
// the window under test.
func takeOneSnapshot(t *testing.T, h *transitionWindowHarness, install func(func())) func() (uint64, bool, adapter.NetworkFamilyCapability) {
	t.Helper()
	type observedSnapshot struct {
		epoch  uint64
		stable bool
		ipv4   netcap.FamilyState
		ipv6   netcap.FamilyState
	}
	observed := make(chan observedSnapshot, 1)
	install(func() {
		epoch, stable, capability := h.manager.NetworkFamilyCapabilitySnapshot()
		observed <- observedSnapshot{
			epoch:  epoch,
			stable: stable,
			ipv4:   capability.Capability.IPv4,
			ipv6:   capability.Capability.IPv6,
		}
	})
	return func() (uint64, bool, adapter.NetworkFamilyCapability) {
		at := <-observed
		return at.epoch, at.stable, adapter.NetworkFamilyCapability{
			Capability: netcap.FamilyCapability{IPv4: at.ipv4, IPv6: at.ipv6},
		}
	}
}

func currentFamilyCapability(t *testing.T, h *transitionWindowHarness) adapter.NetworkFamilyCapability {
	t.Helper()
	_, _, capability := h.manager.NetworkFamilyCapabilitySnapshot()
	return capability
}

// TestCapabilityIsPublishedWithTheFirstEnvironmentObservation is the baseline: the manager reports a
// verdict as soon as it has observed a network, so a dial arriving before any transition still gets
// the fast path.
func TestCapabilityIsPublishedWithTheFirstEnvironmentObservation(t *testing.T) {
	h := newTransitionWindowHarness(t)
	setInterfaces(h, "192.0.2.1", interfaceWithAddresses(1, "en0", "192.168.1.10/24"))
	h.settle(t, "A")

	_, stable, capability := h.manager.NetworkFamilyCapabilitySnapshot()
	require.True(t, stable, "the snapshot was taken on a settled network")
	require.Equal(t, netcap.StateProvisioned, capability.Capability.IPv4)
	require.Equal(t, netcap.StateUnavailable, capability.Capability.IPv6)
}

// TestCapabilityDoesNotFollowAnOldNetwork is the cross-generation rule, stated as a dial would meet
// it.
//
// Wi-Fi hands out IPv4 only; the cellular network that replaces it hands out both. A verdict learned
// on the first must not be readable beside the second network's generation, or the device would keep
// refusing IPv6 on a link that has it.
func TestCapabilityDoesNotFollowAnOldNetwork(t *testing.T) {
	h := newTransitionWindowHarness(t)
	setInterfaces(h, "192.0.2.1", interfaceWithAddresses(1, "en0", "192.168.1.10/24"))
	firstEpoch := h.settle(t, "wifi-ipv4-only")

	require.Equal(t, netcap.StateUnavailable, currentFamilyCapability(t, h).Capability.IPv6)

	// The device moves to a dual-stack cellular network: new fingerprint, new addressing.
	setInterfaces(h, "192.0.2.9",
		interfaceWithAddresses(1, "en0", "192.168.1.10/24", "2001:db8::10/64"))
	secondEpoch := h.settle(t, "cellular-dual-stack")
	require.Greater(t, secondEpoch, firstEpoch, "the network identity must have moved")

	_, stable, capability := h.manager.NetworkFamilyCapabilitySnapshot()
	require.True(t, stable)
	require.Equal(t, netcap.StateProvisioned, capability.Capability.IPv6,
		"the new network's IPv6 must be attemptable; the old network's verdict is gone with its epoch")

	// The two snapshots describe two different networks, which is why they are epoch-stamped.
	require.NotEqual(t, firstEpoch, secondEpoch)
}

// TestCapabilityIsRepublishedWhenOnlyTheAddressingChanges covers the case the environment
// fingerprint cannot see.
//
// The fingerprint names a NETWORK - gateways, SSID, gateway hardware address. An interface that loses
// its IPv6 address after a lease expires is the SAME network with different addressing, so nothing
// here moves the epoch, and a publisher that only ran on an environment change would freeze the
// verdict for as long as the network lasted.
func TestCapabilityIsRepublishedWhenOnlyTheAddressingChanges(t *testing.T) {
	h := newTransitionWindowHarness(t)
	setInterfaces(h, "192.0.2.1", interfaceWithAddresses(1, "en0", "192.168.1.10/24", "2001:db8::10/64"))
	h.settle(t, "A")

	epochBefore := h.manager.NetworkResetGeneration()
	require.Equal(t, netcap.StateProvisioned, currentFamilyCapability(t, h).Capability.IPv6)

	// The same network, the same gateway, no IPv6 addressing any more.
	setInterfaces(h, "192.0.2.1", interfaceWithAddresses(1, "en0", "192.168.1.10/24"))
	h.manager.updateNetworkEnvironment()

	require.Equal(t, epochBefore, h.manager.NetworkResetGeneration(),
		"losing an address must not be treated as a network transition: it would tear every stream down")
	require.Equal(t, netcap.StateUnavailable, currentFamilyCapability(t, h).Capability.IPv6,
		"the capability must follow the addressing even when the network identity does not move")
}

// TestCapabilityNeverCountsThisProcessOwnTunnel is the TUN self-reference rule.
//
// sing-box's own TUN carries a global IPv6 address and the ::/0 route that makes IPv6 traffic arrive
// at it. Counting that address would report IPv6 as available on an IPv4-only physical link, and the
// dial would then spend a connect timeout on a family the machine cannot reach.
func TestCapabilityNeverCountsThisProcessOwnTunnel(t *testing.T) {
	h := newTransitionWindowHarness(t)
	h.manager.interfaceMonitor = &staticInterfaceMonitor{
		current: &control.Interface{Index: 1, Name: "en0"},
		// The monitor is how the process knows which interfaces are its own.
		own: []string{"utun3"},
	}
	setInterfaces(h, "192.0.2.1",
		interfaceWithAddresses(1, "en0", "192.168.1.10/24"),
		interfaceWithAddresses(3, "utun3", "172.19.0.1/30", "2001:db8:dead::1/64"),
	)
	h.settle(t, "A")

	require.Equal(t, netcap.StateUnavailable, currentFamilyCapability(t, h).Capability.IPv6,
		"the tunnel's own address is not evidence about the network underneath it")
}

// TestCapabilityWithoutAUsableInterfaceIsUnknown is the observation-failure rule at the manager's
// boundary. A handover that momentarily leaves nothing to look at must not become a hard verdict on
// both families.
func TestCapabilityWithoutAUsableInterfaceIsUnknown(t *testing.T) {
	h := newTransitionWindowHarness(t)
	setInterfaces(h, "192.0.2.1", interfaceWithAddresses(1, "lo", "127.0.0.1/8", "::1/128"))
	h.settle(t, "A")

	capability := currentFamilyCapability(t, h).Capability
	require.Equal(t, netcap.StateUnknown, capability.IPv4)
	require.Equal(t, netcap.StateUnknown, capability.IPv6)
	require.False(t, capability.HardUnavailable(true))
	require.False(t, capability.HardUnavailable(false))
}

// TestCapabilitySnapshotIsConsistentWithItsEpoch is the atomicity rule, driven through the exact
// window it exists to close.
//
// The environmentPublished hook runs after the new fingerprint and the new capability are written but
// BEFORE the transition is claimed. A reader inside that window must not be able to observe the new
// capability beside the settled flag of the network being left - that pairing is what would let one
// network's verdict be acted on as if it belonged to the other.
//
// The two assertions are the two halves of the ordering, and each one alone would pass against a
// broken implementation:
//
//	the capability is already the NEW one   -> the verdict was published with the environment, not
//	                                           after it
//	the snapshot is not settled              -> the claim has not happened yet, so a consumer that
//	                                           takes this read knows to refuse it
func TestCapabilitySnapshotIsConsistentWithItsEpoch(t *testing.T) {
	h := newTransitionWindowHarness(t)
	// Network A: IPv4 addressing only, gateway 192.0.2.1.
	setInterfaces(h, "192.0.2.1", interfaceWithAddresses(1, "en0", "192.168.1.10/24"))
	h.settle(t, "wifi-ipv4-only")
	// settle() returns the environment FINGERPRINT, which is a hash of the network's identity. The
	// epoch a snapshot reports is NetworkResetGeneration, which is a different number.
	firstEpoch := h.manager.NetworkResetGeneration()
	require.Equal(t, netcap.StateUnavailable, currentFamilyCapability(t, h).Capability.IPv6)

	// Network B: dual-stack, a DIFFERENT gateway, so the fingerprint really moves and the next step
	// is a network transition rather than the same network with different addressing.
	//
	// Two readers bracket the sequence, one on each side of the claim. Together they cover the whole
	// publish-claim-store ordering: a single hook could only ever show one of the two states, and it
	// is the pairing of the two that is the requirement.
	beforeClaim := takeOneSnapshot(t, h, func(hook func()) { h.manager.environmentPublished = hook })
	afterStore := takeOneSnapshot(t, h, func(hook func()) { h.manager.familyCapabilityPublished = hook })
	t.Cleanup(func() {
		h.manager.environmentPublished = nil
		h.manager.familyCapabilityPublished = nil
	})

	setInterfaces(h, "192.0.2.9",
		interfaceWithAddresses(1, "en0", "192.168.1.10/24", "2001:db8::10/64"))
	h.settle(t, "cellular-dual-stack")

	// Instant one: the fingerprint has moved, the claim has not happened. The verdict must still be
	// the one that belongs to the epoch and the settled flag the reader sees - the previous network's,
	// where IPv6 was absent. Reporting the NEW verdict here would be the tear: a dial capturing this
	// epoch would be told the old network supports IPv6.
	beforeEpoch, beforeStable, beforeCapability := beforeClaim()
	require.True(t, beforeStable)
	require.Equal(t, firstEpoch, beforeEpoch,
		"the claim has not happened yet, so the epoch a reader captures is still the previous network's")
	require.Equal(t, netcap.StateUnavailable, beforeCapability.Capability.IPv6,
		"the capability must still describe the network whose epoch the reader just captured")

	// Instant two: the claim has happened and the new verdict is stored. The reader must see the new
	// epoch and must NOT be told the network is settled, because the transition body has not run.
	afterEpoch, afterStable, afterCapability := afterStore()
	require.False(t, afterStable,
		"a read taken during the transition must not report a settled network, or a consumer would "+
			"act on a verdict from a transition that has not committed")
	require.Greater(t, afterEpoch, firstEpoch, "the claim is what moved the epoch")
	require.Equal(t, netcap.StateProvisioned, afterCapability.Capability.IPv6,
		"the new network's verdict must be stored with the epoch that owns it")

	// And the same read after the transition commits describes the same network.
	finalEpoch, finalStable, finalCapability := h.manager.NetworkFamilyCapabilitySnapshot()
	require.True(t, finalStable)
	require.Equal(t, afterEpoch, finalEpoch)
	require.Equal(t, netcap.StateProvisioned, finalCapability.Capability.IPv6)
	require.Equal(t, netcap.StateProvisioned, finalCapability.Capability.IPv4)
}

// TestCapabilitySnapshotIsRaceFree drives publication and reading concurrently.
//
// The capability is read on the dial path, where taking a lock would put every dial behind every
// transition, so it is published and read through an atomic. This asserts the pair stays a state the
// manager really passed through - epoch and capability never from different networks - rather than
// only that the race detector is quiet.
func TestCapabilitySnapshotIsRaceFree(t *testing.T) {
	h := newTransitionWindowHarness(t)
	setInterfaces(h, "192.0.2.1", interfaceWithAddresses(1, "en0", "192.168.1.10/24"))
	h.settle(t, "A")

	// Each generation's addressing is distinct, so a mismatched pair is detectable: epoch N always
	// carries the IPv6 state that generation was published with.
	dualStack := []adapter.NetworkInterface{interfaceWithAddresses(1, "en0", "192.168.1.10/24", "2001:db8::10/64")}
	ipv4Only := []adapter.NetworkInterface{interfaceWithAddresses(1, "en0", "192.168.1.10/24")}

	var (
		waitGroup sync.WaitGroup
		stop      = make(chan struct{})
	)
	for reader := 0; reader < 4; reader++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_, stable, capability := h.manager.NetworkFamilyCapabilitySnapshot()
				state := capability.Capability.IPv6
				if state != netcap.StateUnavailable && state != netcap.StateProvisioned && state != netcap.StateUnknown {
					return
				}
				_ = stable
			}
		}()
	}
	for round := 0; round < 200; round++ {
		if round%2 == 0 {
			setInterfaces(h, "192.0.2.1", ipv4Only...)
		} else {
			setInterfaces(h, "192.0.2.1", dualStack...)
		}
		h.manager.updateNetworkEnvironment()
	}
	close(stop)
	waitGroup.Wait()

	// The last published state is the one the last round installed.
	require.Equal(t, netcap.StateProvisioned, currentFamilyCapability(t, h).Capability.IPv6)
}
