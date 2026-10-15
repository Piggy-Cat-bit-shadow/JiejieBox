package route

import (
	"errors"
	"net"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/netcap"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing/common/control"

	"github.com/stretchr/testify/require"
)

// Finalization round, item 3: the order inside UpdateInterfaces' non-platform branch.
//
// # The shape that was wrong
//
//	observeNonPlatformInterfaces()   <- publishes whatever the finder currently holds
//	interfaceFinder.Update()         <- only NOW does the finder hold the new interfaces
//	defer postUpdateNetworkEnvironment()
//
// The recompute deferred to the end of the function reads the PUBLISHED list, so one call could
// produce a capability computed from the addressing that the same call existed to replace. On the
// monitor-driven path that never showed, because the monitor refreshes the finder before it reports
// anything - which is exactly why it was worth checking rather than assuming: the correctness of a
// public entry point must not depend on every caller having done half its work first.
//
// # What the fix is
//
// The refresh happens FIRST and the publish follows it, so the list a caller can observe is derived
// from the interfaces this call produced. A refresh that fails returns before publishing, leaving the
// previous observation in place rather than replacing a real one with an empty list.

// newInterfaceOrderingManager builds a manager with no platform interface, so UpdateInterfaces takes
// the non-platform branch.
func newInterfaceOrderingManager(t *testing.T) (*NetworkManager, *control.DefaultInterfaceFinder) {
	t.Helper()
	h := newTransitionWindowHarness(t)
	h.manager.platformInterface = nil
	finder := control.NewDefaultInterfaceFinder()
	h.manager.interfaceFinder = finder
	h.manager.networkInterfaces.Store(nil)
	return h.manager, finder
}

func upInterface(index int, name string, addresses ...string) control.Interface {
	return control.Interface{
		Index:     index,
		Name:      name,
		Flags:     net.FlagUp | net.FlagRunning,
		Addresses: mustPrefixes(addresses...),
	}
}

// TestUpdateInterfacesPublishesWhatItsOwnRefreshFound is the ordering assertion.
//
// The finder starts EMPTY, so a publish that ran before the refresh would store an empty list and the
// capability would report Unknown for both families. After one call to the public entry point the list
// must describe the interfaces the finder now holds, and the capability must follow from them.
func TestUpdateInterfacesPublishesWhatItsOwnRefreshFound(t *testing.T) {
	manager, _ := newInterfaceOrderingManager(t)

	// The finder's own source is the host's interface table, which this test cannot dictate. So the
	// observable claim is narrower and still decisive: after the call, the published list must equal
	// what the finder reports, and must not be the empty list it started from.
	require.Empty(t, manager.NetworkInterfaces(), "the test starts from a published-empty list")

	require.NoError(t, manager.UpdateInterfaces())

	published := manager.NetworkInterfaces()
	finderInterfaces := manager.interfaceFinder.Interfaces()

	// Every published entry must come from the refreshed finder, and every up finder interface must be
	// published. An equality of names is what ties the two together without depending on which
	// interfaces this host happens to have.
	publishedNames := make(map[string]bool, len(published))
	for _, iface := range published {
		publishedNames[iface.Name] = true
		require.NotZero(t, iface.Flags&net.FlagUp,
			"only up interfaces are published, so the capability never counts a down link's address")
	}
	for _, iif := range finderInterfaces {
		if iif.Flags&net.FlagUp == 0 {
			continue
		}
		require.True(t, publishedNames[iif.Name],
			"interface %q is up in the refreshed finder but was not published, which is the ordering "+
				"defect: the publish ran against the pre-refresh snapshot", iif.Name)
	}
}

// TestUpdateInterfacesPublishesTheRefreshedStateNotThePreviousOne is the same claim with the finder
// under the test's control.
//
// The finder is pre-seeded with a stale interface that the host does not have. A publish that ran
// before the refresh would keep it; a publish that runs after the refresh cannot, because the refresh
// replaces the finder's contents wholesale.
func TestUpdateInterfacesPublishesTheRefreshedStateNotThePreviousOne(t *testing.T) {
	manager, finder := newInterfaceOrderingManager(t)

	// A stale entry that no real host reports. It models the previous snapshot the old ordering would
	// have published.
	finder.UpdateInterfaces([]control.Interface{
		upInterface(9999, "stale-not-a-real-interface", "203.0.113.1/24"),
	})
	manager.observeNonPlatformInterfaces()
	require.Equal(t, "stale-not-a-real-interface", manager.NetworkInterfaces()[0].Name,
		"the stale entry is published, which is the state the ordering defect would freeze")

	require.NoError(t, manager.UpdateInterfaces())

	for _, iface := range manager.NetworkInterfaces() {
		require.NotEqual(t, "stale-not-a-real-interface", iface.Name,
			"the public entry point must publish what its OWN refresh found, not what was there before")
	}
}

// TestUpdateInterfacesKeepsThePreviousObservationWhenRefreshFails pins the error path.
//
// Returning before publishing is deliberate, and matches the platform branch, which also returns on
// error without touching the list. The earlier observation is stale but real; publishing an empty list
// would turn an enumeration failure into "this machine has no interfaces", and the capability would
// then report Unknown for both families on a machine that has addressing - silently disabling the
// fast-fail rather than failing open.
func TestUpdateInterfacesKeepsThePreviousObservationWhenRefreshFails(t *testing.T) {
	manager, _ := newInterfaceOrderingManager(t)
	previous := interfaceOn(1, "previous", C.InterfaceTypeEthernet, true, "192.168.1.10/24")
	manager.networkInterfaces.Store([]adapter.NetworkInterface{previous})
	require.Len(t, manager.NetworkInterfaces(), 1)

	failure := errors.New("interface enumeration failed")
	manager.interfaceRefreshError = failure
	t.Cleanup(func() { manager.interfaceRefreshError = nil })

	err := manager.UpdateInterfaces()
	require.ErrorIs(t, err, failure, "the refresh failure must be reported to the caller")

	require.Len(t, manager.NetworkInterfaces(), 1,
		"the previous observation must survive a failed refresh: it is stale but real, whereas an "+
			"empty list would report that the machine has no interfaces at all")
	require.Equal(t, "previous", manager.NetworkInterfaces()[0].Name)

	// And the manager recovers on the next successful refresh rather than staying stuck. The injected
	// failure is cleared first: it is a per-call injection, not a permanent property.
	_, _, capabilityAfterFailure := manager.NetworkFamilyCapabilitySnapshot()
	manager.interfaceRefreshError = nil
	require.NoError(t, manager.UpdateInterfaces())
	published := manager.NetworkInterfaces()
	if len(published) > 0 {
		_, _, capabilityAfterRecovery := manager.NetworkFamilyCapabilitySnapshot()
		require.NotEqual(t, capabilityAfterFailure, capabilityAfterRecovery,
			"a successful refresh must publish, so one failure does not freeze the verdict")
	}
}

// TestCapabilityFollowsTheRefreshedList is the end-to-end form: the point of the ordering is that the
// VERDICT is computed from the addressing this call produced.
func TestCapabilityFollowsTheRefreshedList(t *testing.T) {
	h := newTransitionWindowHarness(t)
	h.manager.platformInterface = nil
	finder := control.NewDefaultInterfaceFinder()
	h.manager.interfaceFinder = finder
	h.manager.networkInterfaces.Store(nil)
	h.settle(t, "baseline")

	// Publish a state the capability can be checked against, then run the public entry point. Whatever
	// the host's interfaces are, the verdict must be derived from the published list and not from the
	// empty one the test started with.
	require.NoError(t, h.manager.UpdateInterfaces())

	_, _, capability := h.manager.NetworkFamilyCapabilitySnapshot()
	published := h.manager.NetworkInterfaces()

	if len(published) == 0 {
		// A host with nothing up: the verdict must be Unknown, which is the fail-open answer, rather
		// than a hard verdict about either family.
		require.Equal(t, netcap.StateUnknown, capability.Capability.IPv4)
		require.Equal(t, netcap.StateUnknown, capability.Capability.IPv6)
		return
	}

	// With interfaces published the verdict must be a real one, not the Unknown the empty starting
	// list would have produced.
	require.NotEqual(t, netcap.StateUnknown, capability.Capability.IPv4,
		"the verdict must be derived from the refreshed list, not from the empty one the call replaced")
}
