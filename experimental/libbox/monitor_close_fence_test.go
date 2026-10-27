package libbox

import (
	"sync"
	"testing"

	"github.com/sagernet/sing/common/control"
	"github.com/sagernet/sing/common/logger"

	"github.com/stretchr/testify/require"
)

// blockingRefreshManager holds the interface refresh open, which is the window a monitor Close can
// land in on a real platform.
type blockingRefreshManager struct {
	*orderingNetworkManager
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (m *blockingRefreshManager) RefreshInterfaces() error {
	m.once.Do(func() { close(m.started) })
	<-m.release
	return nil
}

// TestPlatformMonitorRefusesADeliveryThatClosedDuringRefresh pins the second half of the fence.
//
// A delivery checks `closed` when it enters, then refreshes the interface list - a platform
// enumeration that can take real time - and only then writes the default interface. A Close inside
// that refresh was previously invisible to the delivery, which then wrote the interface the device
// had left into the wrapper shared with the monitor that replaced it. The check at the top only
// refuses deliveries that START after Close; the write must be refused as well.
func TestPlatformMonitorRefusesADeliveryThatClosedDuringRefresh(t *testing.T) {
	base := &orderingNetworkManager{finder: &staticInterfaceFinder{interfaces: []control.Interface{
		{Index: 3, Name: "wlan0"},
	}}}
	manager := &blockingRefreshManager{
		orderingNetworkManager: base,
		started:                make(chan struct{}),
		release:                make(chan struct{}),
	}
	monitor := &platformDefaultInterfaceMonitor{
		platformInterfaceWrapper: &platformInterfaceWrapper{networkManager: manager},
		logger:                   logger.NOP(),
	}
	delivered := make(chan *control.Interface, 1)
	monitor.RegisterCallback(func(updated *control.Interface, _ int) {
		delivered <- updated
	})

	done := make(chan struct{})
	go func() {
		monitor.UpdateDefaultInterface("wlan0", 3, false, false)
		close(done)
	}()
	<-manager.started

	// The shape Close uses: fence under the lock the delivery will take for its write, then let the
	// platform teardown run. The order matters - the delivery is already past its first check.
	monitor.defaultInterfaceAccess.Lock()
	monitor.closed = true
	monitor.defaultInterfaceAccess.Unlock()
	close(manager.release)
	<-done

	require.Nil(t, monitor.DefaultInterface(),
		"a delivery that was closed during its refresh published the interface anyway")
	select {
	case updated := <-delivered:
		t.Fatalf("a closed monitor delivered %v to its callbacks", updated)
	default:
	}
}
