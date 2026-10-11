package libbox

import (
	"github.com/sagernet/sing-box/service/powerreport"
	tun "github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common/control"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	"github.com/sagernet/sing/common/x/list"
)

var (
	_ tun.DefaultInterfaceMonitor = (*platformDefaultInterfaceMonitor)(nil)
	_ InterfaceUpdateListener     = (*platformDefaultInterfaceMonitor)(nil)
)

type platformDefaultInterfaceMonitor struct {
	*platformInterfaceWrapper
	logger                      logger.Logger
	callbacks                   list.List[tun.DefaultInterfaceUpdateCallback]
	myInterfaces                []string
	defaultInterfaceInitialized bool
	lastNetworkPath             string

	// closed fences a delivery that arrives after this monitor's owner is gone.
	//
	// `defaultInterface` lives on the EMBEDDED `platformInterfaceWrapper`, which is created once per
	// `CommandServer` and shared by every per-Box monitor, so it is process-global state with more
	// than one possible writer across a reload. Nothing in this file said which monitor a delivery
	// belonged to: `updateDefaultInterface` wrote that field before its own dedup, the `-1` branch
	// wrote it with no dedup at all, and there was no closed flag, no generation, and no "am I the
	// installed monitor" test.
	//
	// The measured consequence is a closed monitor's late callback overwriting the live monitor's
	// state, so the shared field names the interface the device has LEFT while the monitor that is
	// actually installed still names the right one - and the live monitor's dedup then suppresses the
	// honest repeat that would have repaired it, so the contradiction persists rather than
	// self-correcting. What that costs: the wrong interface and the wrong expensive/constrained flags
	// for Apple's `NetworkInterfaces()`, and the wrong interface for every dial on the non-platform
	// path.
	//
	// Guarded by `defaultInterfaceAccess`, which already guards the field this protects.
	closed bool
}

func (m *platformDefaultInterfaceMonitor) Start() error {
	return m.iif.StartDefaultInterfaceMonitor(m)
}

func (m *platformDefaultInterfaceMonitor) Close() error {
	// Fence FIRST, and under the same lock the delivery path takes, so a callback already in flight
	// either lands before this and is then reported by the live monitor, or is refused by it. See the
	// `closed` field for what the missing fence cost.
	m.defaultInterfaceAccess.Lock()
	m.closed = true
	m.defaultInterfaceAccess.Unlock()
	return m.iif.CloseDefaultInterfaceMonitor(m)
}

func (m *platformDefaultInterfaceMonitor) DefaultInterface() *control.Interface {
	m.defaultInterfaceAccess.Lock()
	defer m.defaultInterfaceAccess.Unlock()
	return m.defaultInterface
}

func (m *platformDefaultInterfaceMonitor) OverrideAndroidVPN() bool {
	return false
}

func (m *platformDefaultInterfaceMonitor) AndroidVPNEnabled() bool {
	return false
}

func (m *platformDefaultInterfaceMonitor) RegisterCallback(callback tun.DefaultInterfaceUpdateCallback) *list.Element[tun.DefaultInterfaceUpdateCallback] {
	m.defaultInterfaceAccess.Lock()
	defer m.defaultInterfaceAccess.Unlock()
	return m.callbacks.PushBack(callback)
}

func (m *platformDefaultInterfaceMonitor) UnregisterCallback(element *list.Element[tun.DefaultInterfaceUpdateCallback]) {
	m.defaultInterfaceAccess.Lock()
	defer m.defaultInterfaceAccess.Unlock()
	m.callbacks.Remove(element)
}

func (m *platformDefaultInterfaceMonitor) UpdateNetworkPath(networkPath string) {
	if networkPath != m.lastNetworkPath {
		m.lastNetworkPath = networkPath
		m.logger.Debug("updated network path: ", networkPath)
	}
	if m.powerManager == nil {
		return
	}
	recorder := m.powerManager.Recorder()
	if recorder == nil {
		return
	}
	recorder.UpdateNetworkPath(networkPath)
}

func (m *platformDefaultInterfaceMonitor) UpdateDefaultInterface(interfaceName string, interfaceIndex32 int32, isExpensive bool, isConstrained bool) {
	if sFixAndroidStack {
		done := make(chan struct{})
		go func() {
			m.updateDefaultInterface(interfaceName, interfaceIndex32, isExpensive, isConstrained)
			close(done)
		}()
		<-done
	} else {
		m.updateDefaultInterface(interfaceName, interfaceIndex32, isExpensive, isConstrained)
	}
}

func (m *platformDefaultInterfaceMonitor) updateDefaultInterface(interfaceName string, interfaceIndex32 int32, isExpensive bool, isConstrained bool) {
	// The fence, before anything observable happens. This monitor's owner is gone, so `defaultInterface`
	// - which belongs to the wrapper shared by every Box - now belongs to whoever replaced it, and a
	// delivery from here would overwrite that monitor's state with an interface the device has left.
	m.defaultInterfaceAccess.Lock()
	if m.closed {
		m.defaultInterfaceAccess.Unlock()
		return
	}
	m.defaultInterfaceAccess.Unlock()
	var recorder *powerreport.Recorder
	if m.powerManager != nil {
		recorder = m.powerManager.Recorder()
	}
	if recorder != nil {
		networkType := interfaceName
		if interfaceIndex32 == -1 {
			networkType = "none"
		} else {
			if isExpensive {
				networkType += ",expensive"
			}
			if isConstrained {
				networkType += ",constrained"
			}
		}
		recorder.UpdateNetworkType(networkType)
	}
	m.isExpensive = isExpensive
	m.isConstrained = isConstrained
	err := m.networkManager.UpdateInterfaces()
	if err != nil {
		m.logger.Error(E.Cause(err, "update interfaces"))
	}
	m.defaultInterfaceAccess.Lock()
	if interfaceIndex32 == -1 {
		m.defaultInterface = nil
		m.defaultInterfaceInitialized = true
		callbacks := m.callbacks.Array()
		m.defaultInterfaceAccess.Unlock()
		for _, callback := range callbacks {
			callback(nil, 0)
		}
		return
	}
	oldInterface := m.defaultInterface
	newInterface, err := m.networkManager.InterfaceFinder().ByIndex(int(interfaceIndex32))
	if err != nil {
		m.defaultInterfaceAccess.Unlock()
		m.logger.Error(E.Cause(err, "find updated interface: ", interfaceName))
		return
	}
	m.defaultInterface = newInterface
	if m.defaultInterfaceInitialized && oldInterface != nil && oldInterface.Name == m.defaultInterface.Name && oldInterface.Index == m.defaultInterface.Index {
		m.defaultInterfaceAccess.Unlock()
		return
	}
	m.defaultInterfaceInitialized = true
	callbacks := m.callbacks.Array()
	m.defaultInterfaceAccess.Unlock()
	for _, callback := range callbacks {
		callback(newInterface, 0)
	}
}

func (m *platformDefaultInterfaceMonitor) RegisterMyInterface(interfaceName string) {
	m.defaultInterfaceAccess.Lock()
	defer m.defaultInterfaceAccess.Unlock()
	m.myInterfaces = append(m.myInterfaces, interfaceName)
}

func (m *platformDefaultInterfaceMonitor) MyInterfaces() []string {
	m.defaultInterfaceAccess.Lock()
	defer m.defaultInterfaceAccess.Unlock()
	return m.myInterfaces
}
