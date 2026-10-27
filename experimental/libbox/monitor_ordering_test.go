package libbox

import (
	"sync"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing/common/control"
	"github.com/sagernet/sing/common/logger"

	"github.com/stretchr/testify/require"
)

// orderingNetworkManager records the two halves of the interface refresh separately, so a test can
// tell "the list was refreshed" from "a boundary was published".
type orderingNetworkManager struct {
	adapter.NetworkManager
	access    sync.Mutex
	refresh   int
	update    int
	env       int
	finder    control.InterfaceFinder
	delivered []*control.Interface
}

func (m *orderingNetworkManager) RefreshInterfaces() error {
	m.access.Lock()
	defer m.access.Unlock()
	m.refresh++
	return nil
}

func (m *orderingNetworkManager) UpdateInterfaces() error {
	m.access.Lock()
	defer m.access.Unlock()
	m.update++
	return nil
}

func (m *orderingNetworkManager) UpdateNetworkEnvironment() {
	m.access.Lock()
	defer m.access.Unlock()
	m.env++
}

func (m *orderingNetworkManager) InterfaceFinder() control.InterfaceFinder { return m.finder }

func (m *orderingNetworkManager) counts() (int, int, int) {
	m.access.Lock()
	defer m.access.Unlock()
	return m.refresh, m.update, m.env
}

func newOrderingFixture(t *testing.T) (*platformDefaultInterfaceMonitor, *orderingNetworkManager) {
	t.Helper()
	manager := &orderingNetworkManager{finder: &staticInterfaceFinder{interfaces: []control.Interface{
		{Index: 3, Name: "wlan0"},
		{Index: 7, Name: "rmnet0"},
	}}}
	monitor := &platformDefaultInterfaceMonitor{
		platformInterfaceWrapper: &platformInterfaceWrapper{networkManager: manager},
		logger:                   logger.NOP(),
	}
	monitor.RegisterCallback(func(updated *control.Interface, _ int) {
		manager.access.Lock()
		manager.delivered = append(manager.delivered, updated)
		manager.access.Unlock()
	})
	return monitor, manager
}

// TestPlatformMonitorRefreshesBeforePublishingTheDefault pins the split: the list is refreshed through
// RefreshInterfaces and the boundary is left to the default-interface callback.
//
// The combined UpdateInterfaces recomputes the environment as part of the refresh, and at this point
// the monitor still names the OLD default interface. On the loss edge that recompute can publish a
// zero environment and run a reset while the pause state has not been published - the window the
// deferral exists to prevent.
func TestPlatformMonitorRefreshesBeforePublishingTheDefault(t *testing.T) {
	monitor, manager := newOrderingFixture(t)

	monitor.UpdateDefaultInterface("wlan0", 3, false, false)
	refresh, update, env := manager.counts()
	require.Equal(t, 1, refresh, "the list was not refreshed")
	require.Zero(t, update, "the combined call ran; the monitor did not split the refresh")
	require.Zero(t, env, "a boundary was published while the default interface was still the old one")
	require.Len(t, manager.delivered, 1)
	require.Equal(t, "wlan0", manager.delivered[0].Name)

	// The loss edge: the list is refreshed, the default is cleared, and NO boundary is published -
	// the pause state published by the nil callback is what defers the rebuild.
	monitor.UpdateDefaultInterface("", -1, false, false)
	refresh, update, env = manager.counts()
	require.Equal(t, 2, refresh)
	require.Zero(t, update)
	require.Zero(t, env, "a boundary was published on the loss edge, before the pause state existed")
	require.Len(t, manager.delivered, 2)
	require.Nil(t, manager.delivered[1])
}

// TestPlatformMonitorPublishesTheEnvironmentWhenTheDefaultIsUnchanged is the other side of the split:
// a gateway or SSID change on the same interface produces no callback, so nothing else would publish
// the boundary. The refresh must not swallow it.
func TestPlatformMonitorPublishesTheEnvironmentWhenTheDefaultIsUnchanged(t *testing.T) {
	monitor, manager := newOrderingFixture(t)

	monitor.UpdateDefaultInterface("wlan0", 3, false, false)
	monitor.UpdateDefaultInterface("wlan0", 3, false, false)

	refresh, update, env := manager.counts()
	require.Equal(t, 2, refresh, "every report refreshes the list")
	require.Zero(t, update)
	require.Equal(t, 1, env,
		"a same-default report published no environment boundary; a gateway/SSID change on the same "+
			"path would be lost")
	require.Len(t, manager.delivered, 1, "a repeated report of the current interface is not a transition")
}
