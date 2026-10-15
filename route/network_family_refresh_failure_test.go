package route

import (
	"errors"
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/netcap"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing/common/control"

	"github.com/stretchr/testify/require"
)

// Finalization: a failed interface refresh must not promote the previous network's verdict to the new
// generation's ownership.
//
// # The defect, exactly
//
// UpdateInterfaces established the environment boundary in an unconditional `defer`, so it ran on the
// refresh-failure return path too. Meanwhile the failure deliberately leaves the previous interface
// list in place - it is stale but real, which is better than reporting that the machine has no
// interfaces at all.
//
// The two together promote a stale observation:
//
//	Wi-Fi, IPv4 only          IPv6 = Unavailable, epoch N
//	                          <- hand over to dual-stack cellular
//	refresh FAILS             the list still describes Wi-Fi
//	recompute still runs      the fingerprint moved, so it claims epoch N+1
//	                          and publishes Wi-Fi's Unavailable as N+1's verdict
//
// The consequence is worse than a wrong verdict, and that is what makes it a semantic defect rather
// than an inconvenience: the verdict now carries the NEW generation's ownership. The dialer's
// ownership check - the whole subject of the previous round - compares epochs and finds them equal, so
// it has nothing to reject. There is no later moment at which the mistake is corrected, and the device
// sits on a dual-stack network with its IPv6 refused by a network it has left.
//
// # The invariant
//
// An environment boundary is established only on evidence that was actually observed. A refresh that
// failed publishes nothing, claims nothing, and leaves both the environment and the verdict exactly as
// they were.

// platformInterfaceStub supplies the platform path's answers, so a test can drive a platform
// interface hand-over with the interface list under its own control.
type platformInterfaceStub struct {
	adapter.PlatformInterface
	interfaces []adapter.NetworkInterface
}

func (p *platformInterfaceStub) UsePlatformNetworkInterfaces() bool { return true }

func (p *platformInterfaceStub) NetworkInterfaces() ([]adapter.NetworkInterface, error) {
	return p.interfaces, nil
}

// platformHandoverHarness models the exact timeline: settled on network A with IPv4-only addressing,
// then a hand-over to dual-stack network B whose observation the test can fail on demand.
type platformHandoverHarness struct {
	manager *NetworkManager
	stub    *platformInterfaceStub
}

func newPlatformHandoverHarness(t *testing.T) *platformHandoverHarness {
	t.Helper()
	h := newTransitionWindowHarness(t)
	// A platform interface that supplies the list, because that is the path where the default
	// interface and the interface list can disagree - the non-platform path derives both from the same
	// finder refresh.
	stub := &platformInterfaceStub{}
	monitor := &staticInterfaceMonitor{current: &control.Interface{Index: 1, Name: "wlan0"}}
	h.manager.platformInterface = stub
	h.manager.interfaceMonitor = monitor
	// The platform branch pushes its list INTO the finder on every platform except Darwin, so the
	// finder has to exist even though the failure under test is induced before it is used.
	h.manager.interfaceFinder = control.NewDefaultInterfaceFinder()

	harness := &platformHandoverHarness{manager: h.manager, stub: stub}

	// Network A: IPv4 only, gateway 192.0.2.1.
	harness.observe(t, []adapter.NetworkInterface{
		interfaceOn(1, "wlan0", C.InterfaceTypeWIFI, true, "192.168.1.10/24"),
	}, "192.0.2.1")
	harness.settleOn(t, "wifi-ipv4-only")

	require.Equal(t, netcap.StateUnavailable, currentFamilyCapability(t, h).Capability.IPv6,
		"the harness must start from a network whose verdict really does refuse IPv6")
	return harness
}

// observe replaces the platform's interface list and runs the public entry point, so the environment
// is published exactly the way production publishes it.
func (h *platformHandoverHarness) observe(t *testing.T, interfaces []adapter.NetworkInterface, gateway string) {
	t.Helper()
	gatewayAddress := netip.MustParseAddr(gateway)
	for index := range interfaces {
		if interfaces[index].Index == 1 {
			interfaces[index].Gateways = []netip.Addr{gatewayAddress}
		}
	}
	h.stub.interfaces = interfaces
}

// settleOn publishes the environment through the public entry point and asserts the network settled,
// which is what makes the next observation a genuine transition.
func (h *platformHandoverHarness) settleOn(t *testing.T, ssid string) {
	t.Helper()
	h.manager.stateAccess.Lock()
	h.manager.wifiState = adapter.WIFIState{SSID: ssid}
	h.manager.stateAccess.Unlock()
	require.NoError(t, h.manager.UpdateInterfaces())
	require.True(t, h.manager.NetworkTransitionStable(),
		"a completed update must leave the network settled")
}

// TestFailedRefreshDoesNotPromoteTheOldVerdictToTheNewGeneration is the reproduction.
//
// The hand-over really happens - the platform reports a dual-stack interface and a new gateway - but
// the observation FAILS. The assertion is that the previous network's IPv6 refusal does not come back
// wearing the new generation's ownership.
func TestFailedRefreshDoesNotPromoteTheOldVerdictToTheNewGeneration(t *testing.T) {
	harness := newPlatformHandoverHarness(t)
	epochOnWifi := harness.manager.NetworkResetGeneration()
	verdictOnWifi := currentFamilyCapabilityOf(t, harness.manager).Capability
	require.Equal(t, netcap.StateUnavailable, verdictOnWifi.IPv6)

	// The device hands over to dual-stack cellular. The platform would report it...
	harness.observe(t, []adapter.NetworkInterface{
		interfaceOn(1, "rmnet0", C.InterfaceTypeCellular, true, "10.20.0.5/24", "2001:db8::5/64"),
	}, "192.0.2.9")
	harness.manager.interfaceMonitor.(*staticInterfaceMonitor).current = &control.Interface{Index: 1, Name: "rmnet0"}
	harness.manager.stateAccess.Lock()
	harness.manager.wifiState = adapter.WIFIState{SSID: "cellular-dual-stack"}
	harness.manager.stateAccess.Unlock()

	// ...but the observation fails, before the interface list is replaced.
	failure := errors.New("platform interface enumeration failed")
	harness.manager.interfaceRefreshError = failure
	err := harness.manager.UpdateInterfaces()
	require.ErrorIs(t, err, failure, "the failure must be reported to the caller")
	harness.manager.interfaceRefreshError = nil

	// THE ASSERTION. The stale list must not have been promoted to the new generation.
	epochAfterFailure, stableAfterFailure, capabilityAfterFailure := harness.manager.NetworkFamilyCapabilitySnapshot()
	require.Equal(t, epochOnWifi, epochAfterFailure,
		"a failed observation must not claim an epoch: claiming one hands the stale verdict the new "+
			"generation's ownership, and the dialer's ownership check then has nothing to reject")
	require.Equal(t, verdictOnWifi, capabilityAfterFailure.Capability)
	require.True(t, stableAfterFailure, "and it must not leave the network unsettled either")

	// The stale list itself is still in place - that part is deliberate, see the ordering test.
	for _, iface := range harness.manager.NetworkInterfaces() {
		require.NotEqual(t, "rmnet0", iface.Name,
			"the failed refresh must not have published anything")
	}
}

// TestTheNextSuccessfulObservationTakesTheDeferredBoundary is the other half: skipping the recompute
// DEFERS the boundary rather than losing it.
//
// The fingerprint the failed call would have published is still unpublished, so the next observation -
// which by definition has a list to publish - takes the same boundary, and it does so with the new
// network's own verdict rather than the old one's.
func TestTheNextSuccessfulObservationTakesTheDeferredBoundary(t *testing.T) {
	harness := newPlatformHandoverHarness(t)
	epochOnWifi := harness.manager.NetworkResetGeneration()

	// A failed hand-over, as above.
	harness.observe(t, []adapter.NetworkInterface{
		interfaceOn(1, "rmnet0", C.InterfaceTypeCellular, true, "10.20.0.5/24", "2001:db8::5/64"),
	}, "192.0.2.9")
	harness.manager.interfaceMonitor.(*staticInterfaceMonitor).current = &control.Interface{Index: 1, Name: "rmnet0"}
	harness.manager.stateAccess.Lock()
	harness.manager.wifiState = adapter.WIFIState{SSID: "cellular-dual-stack"}
	harness.manager.stateAccess.Unlock()

	harness.manager.interfaceRefreshError = errors.New("transient failure")
	require.Error(t, harness.manager.UpdateInterfaces())
	harness.manager.interfaceRefreshError = nil
	require.Equal(t, epochOnWifi, harness.manager.NetworkResetGeneration())

	// The monitor reports again, and this time the observation succeeds.
	require.NoError(t, harness.manager.UpdateInterfaces())

	epochAfterRecovery, stableAfterRecovery, capabilityAfterRecovery := harness.manager.NetworkFamilyCapabilitySnapshot()
	require.True(t, stableAfterRecovery)
	require.Greater(t, epochAfterRecovery, epochOnWifi,
		"the boundary the failed call deferred must be taken by the next observation that succeeds")
	require.Equal(t, netcap.StateProvisioned, capabilityAfterRecovery.Capability.IPv6,
		"and it must carry the NEW network's verdict: the deferred boundary is the moment the device's "+
			"own IPv6 becomes attemptable again")
}

// TestNonPlatformRefreshFailureAlsoSkipsTheBoundary is the same rule on the branch that does not have a
// platform interface, because the unconditional defer was shared by both.
func TestNonPlatformRefreshFailureAlsoSkipsTheBoundary(t *testing.T) {
	manager, _ := newInterfaceOrderingManager(t)
	h := &transitionWindowHarness{manager: manager}

	setInterfaces(h, "192.0.2.1", interfaceOn(1, "eth0", C.InterfaceTypeEthernet, true, "192.168.1.10/24"))
	h.settle(t, "wired-ipv4-only")
	epochBefore := manager.NetworkResetGeneration()
	require.Equal(t, netcap.StateUnavailable, currentFamilyCapability(t, h).Capability.IPv6)

	// The environment moves - a new gateway and SSID - while the refresh fails.
	setInterfaces(h, "192.0.2.9", interfaceOn(1, "eth0", C.InterfaceTypeEthernet, true, "192.168.1.10/24"))
	h.setSSID("moved")
	manager.interfaceRefreshError = errors.New("finder refresh failed")
	require.Error(t, manager.UpdateInterfaces())
	manager.interfaceRefreshError = nil

	epochAfter, _, capabilityAfter := manager.NetworkFamilyCapabilitySnapshot()
	require.Equal(t, epochBefore, epochAfter,
		"a failed finder refresh must not claim a generation either")
	require.Equal(t, netcap.StateUnavailable, capabilityAfter.Capability.IPv6,
		"and the verdict stays the one that legitimately belongs to the generation it was observed on")
}

// TestSuccessfulRefreshStillTakesTheBoundaryImmediately is the control.
//
// Without it, every assertion above would pass against an implementation that simply never recomputed.
func TestSuccessfulRefreshStillTakesTheBoundaryImmediately(t *testing.T) {
	harness := newPlatformHandoverHarness(t)
	epochOnWifi := harness.manager.NetworkResetGeneration()

	harness.observe(t, []adapter.NetworkInterface{
		interfaceOn(1, "rmnet0", C.InterfaceTypeCellular, true, "10.20.0.5/24", "2001:db8::5/64"),
	}, "192.0.2.9")
	harness.manager.interfaceMonitor.(*staticInterfaceMonitor).current = &control.Interface{Index: 1, Name: "rmnet0"}
	harness.manager.stateAccess.Lock()
	harness.manager.wifiState = adapter.WIFIState{SSID: "cellular-dual-stack"}
	harness.manager.stateAccess.Unlock()

	require.NoError(t, harness.manager.UpdateInterfaces())

	epochAfter, stableAfter, capability := harness.manager.NetworkFamilyCapabilitySnapshot()
	require.True(t, stableAfter)
	require.Greater(t, epochAfter, epochOnWifi, "a successful observation takes its boundary at once")
	require.Equal(t, netcap.StateProvisioned, capability.Capability.IPv6,
		"with the new network's verdict, which is the whole point of observing before deciding")
}

// TestUpdateInterfacesFailureIsIdempotentForTheVerdict checks that repeated failures cannot erode
// either the environment or the verdict, which matters because a flapping interface retries.
func TestUpdateInterfacesFailureIsIdempotentForTheVerdict(t *testing.T) {
	harness := newPlatformHandoverHarness(t)
	epochOnWifi := harness.manager.NetworkResetGeneration()
	verdictOnWifi := currentFamilyCapabilityOf(t, harness.manager).Capability

	harness.observe(t, []adapter.NetworkInterface{
		interfaceOn(1, "rmnet0", C.InterfaceTypeCellular, true, "10.20.0.5/24", "2001:db8::5/64"),
	}, "192.0.2.9")
	harness.manager.interfaceMonitor.(*staticInterfaceMonitor).current = &control.Interface{Index: 1, Name: "rmnet0"}
	harness.manager.stateAccess.Lock()
	harness.manager.wifiState = adapter.WIFIState{SSID: "cellular-dual-stack"}
	harness.manager.stateAccess.Unlock()
	harness.manager.interfaceRefreshError = errors.New("flapping")

	for round := 0; round < 10; round++ {
		require.Error(t, harness.manager.UpdateInterfaces())
		epoch, stable, capability := harness.manager.NetworkFamilyCapabilitySnapshot()
		require.Equal(t, epochOnWifi, epoch, "failure %d must not advance the epoch", round)
		require.True(t, stable)
		require.Equal(t, verdictOnWifi, capability.Capability)
	}
}
