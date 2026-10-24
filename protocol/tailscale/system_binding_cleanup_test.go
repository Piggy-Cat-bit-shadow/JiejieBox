//go:build with_tailscale

package tailscale

import (
	"context"
	"net"
	"syscall"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common/control"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/tailscale/net/netmon"

	"github.com/stretchr/testify/require"
)

// The process-global interface getter must be CLEARED when its owner goes away.
//
// # Why this needs a test rather than a comment
//
// `netmon.RegisterInterfaceGetter` writes a package global in a dependency
// (`github.com/sagernet/tailscale/net/netmon/state.go`), and a global outlives the object that
// installed it. Nothing used to clear it, so after a Box closed, `altNetInterfaces` still held a
// closure over that Box's NetworkManager - and it survived into a LATER Box whose platform interface
// does not re-register, at which point netmon reads interfaces out of a manager that has been torn
// down. The failure is silent and cross-instance, which is exactly why it went unnoticed.
//
// # What is observable
//
// `netmon.GetInterfaceList()` returns the registered getter's result when one is set, and the real
// system interfaces otherwise. That is the whole observable surface, and it is enough: a test can
// prove the getter is installed, prove it is read, and prove the reset takes it back out.
//
// The check is a COUNT rather than an identity because `netmon.Interface` carries a `*net.Interface`
// built by this package from a `control.Interface`, and the point under test is which provider
// answered - not what it answered with.

// platformNetworkStub claims the platform owns the network interfaces, and offers exactly one.
type platformNetworkStub struct {
	adapter.PlatformInterface
}

func (platformNetworkStub) UsePlatformNetworkInterfaces() bool { return true }

// UsePlatformAutoDetectInterfaceControl is answered for the same reason as the network manager's
// methods, and it answers FALSE so the binding takes the platform-interface branch rather than the
// file-descriptor-control one, which would need a real descriptor.
func (platformNetworkStub) UsePlatformAutoDetectInterfaceControl() bool { return false }

// networkManagerStub answers the methods newSystemBinding reaches, and nothing else. They are
// answered rather than left to the embedded nil: a double that panics on a method under test reports
// the fixture instead of the code.
type networkManagerStub struct {
	adapter.NetworkManager
	interfaces       []control.Interface
	updates          int
	defaultInterface *control.Interface
}

type staticDefaultInterfaceMonitor struct {
	tun.DefaultInterfaceMonitor
	iface *control.Interface
}

func (m *staticDefaultInterfaceMonitor) DefaultInterface() *control.Interface {
	return m.iface
}

func (m *networkManagerStub) InterfaceMonitor() tun.DefaultInterfaceMonitor {
	return &staticDefaultInterfaceMonitor{iface: m.defaultInterface}
}

func (m *networkManagerStub) UpdateInterfaces() error {
	m.updates++
	return nil
}

func (m *networkManagerStub) InterfaceFinder() control.InterfaceFinder {
	return &staticInterfaceFinder{interfaces: m.interfaces}
}

// AutoRedirectOutputMark is read BEFORE the platform branches, so it must answer. Zero means "no
// auto-redirect mark configured", which is the ordinary desktop case and leaves the binding on the
// platform-interface path this test is about.
func (m *networkManagerStub) AutoRedirectOutputMark() uint32 { return 0 }

// AutoDetectInterfaceFunc must be non-nil for the last branch, so a binding that falls past the
// platform branches still gets a control function.
func (m *networkManagerStub) AutoDetectInterfaceFunc() control.Func {
	return func(network, address string, conn syscall.RawConn) error { return nil }
}

// staticInterfaceFinder is an InterfaceFinder whose list is fixed, so the test cannot depend on the
// machine it runs on.
type staticInterfaceFinder struct {
	control.InterfaceFinder
	interfaces []control.Interface
}

func (f *staticInterfaceFinder) Interfaces() []control.Interface { return f.interfaces }

// TestTheInterfaceGetterIsInstalledReadAndCleared is the whole contract in one test, because the
// three facts are only meaningful together: a getter that is installed but never read proves
// nothing, and a reset that was never needed cannot be distinguished from one that does not work.
func TestTheInterfaceGetterIsInstalledReadAndCleared(t *testing.T) {
	// This test writes a PROCESS GLOBAL, so it must not run beside another test that reads one.
	// Nothing else in this package touches netmon, and the reset below restores the global even on
	// failure.

	// The stub offers ONE interface with a name the machine cannot plausibly have, so a match is
	// unambiguous.
	const stubName = "sing-box-tailscale-system-binding-test"
	networkManager := &networkManagerStub{
		interfaces:       []control.Interface{{Name: stubName, Index: 4242}},
		defaultInterface: &control.Interface{Name: stubName, Index: 4242},
	}
	ctx := service.ContextWith[adapter.NetworkManager](context.Background(), networkManager)
	ctx = service.ContextWith[adapter.PlatformInterface](ctx, platformNetworkStub{})

	binding, cleanup, err := newSystemBinding(ctx, nil)
	require.NoError(t, err)
	require.NotNil(t, cleanup,
		"the platform owns the interfaces, so a getter was registered and the caller MUST be given "+
			"the means to clear it: a process global outlives this object")

	// Restore the global whatever happens next. The cleanup is idempotent, so calling it twice is
	// safe.
	t.Cleanup(func() { _ = cleanup() })

	// The registration is read back through the dependency's own public accessor rather than
	// through this package's return value, so the test cannot pass on a getter that was built and
	// never installed.
	list, err := netmon.GetInterfaceList()
	require.NoError(t, err)
	require.Len(t, list, 1,
		"the registered getter must be what netmon reads: %d interfaces came back", len(list))
	require.NotNil(t, list[0].Interface)
	require.Equal(t, stubName, list[0].Interface.Name,
		"and it must be the one this package supplied, not the machine's")

	routeHook := binding.hooks(nil).DefaultRoute
	require.NotNil(t, routeHook, "the binding must expose the platform default route to netmon")
	details, err := routeHook()
	require.NoError(t, err)
	require.Equal(t, stubName, details.InterfaceName,
		"the default-route hook must answer from the NetworkManager, not the machine")

	// And the cleanup takes it back out.
	require.NoError(t, cleanup())
	afterReset, err := netmon.GetInterfaceList()
	require.NoError(t, err)
	require.NotEqual(t, 1, len(afterReset),
		"after the reset netmon must fall back to the system list, which cannot be exactly this "+
			"stub's single interface")

	// The binding itself is still usable: the reset clears the GLOBAL, not the object the caller
	// holds.
	require.NotNil(t, binding.hooks(nil).Control)
}

// TestASystemBindingWithoutAPlatformGetterReturnsNoCleanup is the other half of the contract.
//
// When the platform does not own the interfaces nothing is registered, so there is nothing to clear.
// Returning a cleanup anyway would make every eager call site register a no-op on its scope, and
// returning nil without documenting it is what would make a caller reach for the global
// unconditionally.
func TestASystemBindingWithoutAPlatformGetterReturnsNoCleanup(t *testing.T) {
	networkManager := &networkManagerStub{}
	ctx := service.ContextWith[adapter.NetworkManager](context.Background(), networkManager)

	binding, cleanup, err := newSystemBinding(ctx, nil)
	require.NoError(t, err)
	require.Nil(t, cleanup,
		"nothing was registered, so there is nothing to clear and the caller must be able to tell")
	require.NotNil(t, binding.hooks(nil).Control)

	// And the helpers tolerate that nil, because every call site passes whatever it got.
	require.NotPanics(t, func() {
		registerSystemBindingCleanup(nil, cleanup)
		cleanupSystemBindingOnContext(context.Background(), cleanup)
	})
}

// TestTheLazyCallSiteClearsTheGetterWhenItsContextEnds covers the one caller that cannot use a
// scope.
//
// `TailcatOutbound` builds its node lazily, on first use, so it has no Start stage to register on -
// only the lifetime context captured from the scope. The reset must therefore be tied to that
// context, and this test pins that it actually fires.
func TestTheLazyCallSiteClearsTheGetterWhenItsContextEnds(t *testing.T) {
	const stubName = "sing-box-tailscale-lazy-binding-test"
	networkManager := &networkManagerStub{
		interfaces: []control.Interface{{Name: stubName, Index: 4243}},
	}
	buildContext := service.ContextWith[adapter.NetworkManager](context.Background(), networkManager)
	buildContext = service.ContextWith[adapter.PlatformInterface](buildContext, platformNetworkStub{})

	_, cleanup, err := newSystemBinding(buildContext, nil)
	require.NoError(t, err)
	require.NotNil(t, cleanup)
	t.Cleanup(func() { _ = cleanup() })

	list, err := netmon.GetInterfaceList()
	require.NoError(t, err)
	require.Len(t, list, 1)

	lifetime, cancelLifetime := context.WithCancel(context.Background())
	cleanupSystemBindingOnContext(lifetime, cleanup)

	// The getter is still installed: the outbound is live.
	live, err := netmon.GetInterfaceList()
	require.NoError(t, err)
	require.Len(t, live, 1, "the reset must not run before the lifetime ends")

	cancelLifetime()

	// context.AfterFunc runs on cancellation, but asynchronously - so this waits for the observable
	// effect rather than for a fixed delay.
	require.Eventually(t, func() bool {
		after, afterErr := netmon.GetInterfaceList()
		return afterErr == nil && len(after) != 1
	}, defaultEventuallyTimeout, defaultEventuallyTick,
		"cancelling the lifetime context must clear the process-global interface getter: a later "+
			"Box would otherwise read interfaces out of this outbound's closed manager")
}

// The two knobs the Eventually above uses, so the wait is stated once.
const (
	defaultEventuallyTimeout = 5 * time.Second
	defaultEventuallyTick    = 10 * time.Millisecond
)

// netInterfacesForTest is a compile-time reminder that the fallback the reset restores is the
// standard library's own list, which is what makes the count comparison meaningful.
var _ = net.Interfaces
