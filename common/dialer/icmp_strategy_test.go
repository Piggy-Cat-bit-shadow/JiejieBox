package dialer

import (
	"errors"
	"reflect"
	"syscall"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	tun "github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common/control"

	"github.com/stretchr/testify/require"
)

// The ICMP path used to pick a plain family dialer, so it ignored the configured network strategy
// and interface types: an ICMP probe left through the default route while TCP/UDP used the bound
// interface (upstream 38bf6879b). These tests pin the decision and the candidate order without a real
// socket; the actual setsockopt is the package implementation used by every other bind path.

type icmpTestMonitor struct {
	tun.DefaultInterfaceMonitor
	defaultInterface *control.Interface
	myInterfaces     []string
}

func (m *icmpTestMonitor) DefaultInterface() *control.Interface { return m.defaultInterface }
func (m *icmpTestMonitor) MyInterfaces() []string               { return m.myInterfaces }

type icmpTestNetworkManager struct {
	adapter.NetworkManager
	interfaces []adapter.NetworkInterface
	monitor    tun.DefaultInterfaceMonitor
}

func (m *icmpTestNetworkManager) NetworkInterfaces() []adapter.NetworkInterface {
	return m.interfaces
}

func (m *icmpTestNetworkManager) InterfaceMonitor() tun.DefaultInterfaceMonitor { return m.monitor }

type icmpBindCall struct {
	network        string
	address        string
	interfaceName  string
	interfaceIndex int
}

func installICMPBindProbe(t *testing.T, failures map[int]error) *[]icmpBindCall {
	t.Helper()
	original := icmpBindToInterface
	t.Cleanup(func() { icmpBindToInterface = original })
	var calls []icmpBindCall
	icmpBindToInterface = func(finder control.InterfaceFinder, conn syscall.RawConn, network, address, interfaceName string, interfaceIndex int, preferInterfaceName bool) error {
		calls = append(calls, icmpBindCall{network: network, address: address, interfaceName: interfaceName, interfaceIndex: interfaceIndex})
		if err, failed := failures[interfaceIndex]; failed {
			return err
		}
		return nil
	}
	return &calls
}

func newICMPTestDialer(strategy *C.NetworkStrategy, interfaceType []C.InterfaceType, fallbackInterfaceType []C.InterfaceType, defaultNetworkStrategy bool, interfaces []adapter.NetworkInterface, defaultInterface *control.Interface) *DefaultDialer {
	monitor := &icmpTestMonitor{defaultInterface: defaultInterface}
	manager := &icmpTestNetworkManager{interfaces: interfaces, monitor: monitor}
	return &DefaultDialer{
		networkManager:         manager,
		networkStrategy:        strategy,
		networkType:            interfaceType,
		fallbackNetworkType:    fallbackInterfaceType,
		defaultNetworkStrategy: defaultNetworkStrategy,
	}
}

func ethernetInterface(name string, index int) adapter.NetworkInterface {
	return adapter.NetworkInterface{
		Interface: control.Interface{Name: name, Index: index},
		Type:      C.InterfaceTypeEthernet,
	}
}

func cellularInterface(name string, index int) adapter.NetworkInterface {
	return adapter.NetworkInterface{
		Interface: control.Interface{Name: name, Index: index},
		Type:      C.InterfaceTypeCellular,
	}
}

func TestICMPControlWithoutStrategyUsesTheFamilyControl(t *testing.T) {
	dialer := newICMPTestDialer(nil, nil, nil, false, nil, nil)
	called := false
	dialer.dialer4.Control = func(network, address string, conn syscall.RawConn) error {
		called = true
		return nil
	}
	icmpControl := dialer.ICMPControl()
	require.NoError(t, icmpControl("ip4:icmp", "192.0.2.1", nil))
	require.True(t, called, "the family control was not used when no strategy is configured")
	require.Equal(t,
		reflect.ValueOf(dialer.dialer4.Control).Pointer(),
		reflect.ValueOf(icmpControl).Pointer(),
		"the no-strategy path must return the family control itself")
}

func TestICMPControlBindsTheStrategyInterface(t *testing.T) {
	calls := installICMPBindProbe(t, nil)
	strategy := C.NetworkStrategyDefault
	dialer := newICMPTestDialer(&strategy, []C.InterfaceType{C.InterfaceTypeEthernet}, nil, false,
		[]adapter.NetworkInterface{ethernetInterface("eth0", 5), cellularInterface("wwan0", 6)}, nil)

	icmpControl := dialer.ICMPControl()
	require.NoError(t, icmpControl("ip4:icmp", "192.0.2.1", nil))
	require.Len(t, *calls, 1)
	require.Equal(t, "eth0", (*calls)[0].interfaceName)
	require.Equal(t, 5, (*calls)[0].interfaceIndex)
}

func TestICMPControlSkipsTheDefaultInterface(t *testing.T) {
	calls := installICMPBindProbe(t, nil)
	strategy := C.NetworkStrategyDefault
	defaultInterface := &control.Interface{Name: "eth0", Index: 5}
	dialer := newICMPTestDialer(&strategy, nil, nil, false,
		[]adapter.NetworkInterface{ethernetInterface("eth0", 5)}, defaultInterface)

	require.NoError(t, dialer.ICMPControl()("ip4:icmp", "192.0.2.1", nil))
	require.Empty(t, *calls, "binding the current default interface is unnecessary")
}

func TestICMPControlTriesEveryCandidate(t *testing.T) {
	calls := installICMPBindProbe(t, map[int]error{5: syscall.EPERM})
	strategy := C.NetworkStrategyHybrid
	dialer := newICMPTestDialer(&strategy, nil, nil, false,
		[]adapter.NetworkInterface{ethernetInterface("eth0", 5), cellularInterface("wwan0", 6)}, nil)

	require.NoError(t, dialer.ICMPControl()("ip4:icmp", "192.0.2.1", nil))
	require.Len(t, *calls, 2)
	require.Equal(t, "eth0", (*calls)[0].interfaceName)
	require.Equal(t, "wwan0", (*calls)[1].interfaceName)
}

func TestICMPControlHonoursTheFallbackTypes(t *testing.T) {
	calls := installICMPBindProbe(t, map[int]error{5: errors.New("primary unavailable")})
	strategy := C.NetworkStrategyFallback
	dialer := newICMPTestDialer(&strategy, nil, []C.InterfaceType{C.InterfaceTypeCellular}, false,
		[]adapter.NetworkInterface{ethernetInterface("eth0", 5), cellularInterface("wwan0", 6)}, nil)

	require.NoError(t, dialer.ICMPControl()("ip4:icmp", "192.0.2.1", nil))
	require.Len(t, *calls, 2)
	require.Equal(t, "wwan0", (*calls)[1].interfaceName)
}

func TestICMPControlToleratesEPERMOnlyForTheDefaultStrategy(t *testing.T) {
	installICMPBindProbe(t, map[int]error{5: syscall.EPERM})
	interfaces := []adapter.NetworkInterface{ethernetInterface("eth0", 5)}

	defaultStrategy := C.NetworkStrategyDefault
	tolerant := newICMPTestDialer(&defaultStrategy, []C.InterfaceType{C.InterfaceTypeEthernet}, nil, true, interfaces, nil)
	require.NoError(t, tolerant.ICMPControl()("ip4:icmp", "192.0.2.1", nil),
		"the default strategy tolerates a denied bind-interface syscall")

	explicit := newICMPTestDialer(&defaultStrategy, []C.InterfaceType{C.InterfaceTypeEthernet}, nil, false, interfaces, nil)
	require.ErrorIs(t, explicit.ICMPControl()("ip4:icmp", "192.0.2.1", nil), syscall.EPERM,
		"an explicitly configured strategy keeps the error")
}

func TestICMPControlRejectsAnEmptyInterfaceSet(t *testing.T) {
	installICMPBindProbe(t, nil)
	strategy := C.NetworkStrategyDefault
	dialer := newICMPTestDialer(&strategy, nil, nil, false, nil, nil)
	require.Error(t, dialer.ICMPControl()("ip4:icmp", "192.0.2.1", nil))
}

func TestICMPControlIgnoresThePingDestination(t *testing.T) {
	// The control is destination-independent: it is installed once and the ping port reuses it for
	// both families. This pins that the old per-destination family switch is gone.
	calls := installICMPBindProbe(t, nil)
	strategy := C.NetworkStrategyHybrid
	dialer := newICMPTestDialer(&strategy, nil, nil, false,
		[]adapter.NetworkInterface{ethernetInterface("eth0", 5)}, nil)
	icmpControl := dialer.ICMPControl()
	require.NoError(t, icmpControl("ip4:icmp", "192.0.2.1", nil))
	require.NoError(t, icmpControl("ip6:ipv6-icmp", "2001:db8::1", nil))
	require.Len(t, *calls, 2)
	require.Equal(t, "ip4:icmp", (*calls)[0].network)
	require.Equal(t, "ip6:ipv6-icmp", (*calls)[1].network)
}
