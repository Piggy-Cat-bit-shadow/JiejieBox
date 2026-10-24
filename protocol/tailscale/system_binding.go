//go:build with_tailscale

package tailscale

import (
	"context"
	"net"
	"net/netip"
	"syscall"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/control"
	"github.com/sagernet/sing/common/logger"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/tailscale/net/netmon"
	"github.com/sagernet/tailscale/types/nettype"
)

type systemBinding struct {
	control      func(network, address string, conn syscall.RawConn) error
	listenPacket func(ctx context.Context, network, address string) (nettype.PacketConn, error)
	defaultRoute func() (netmon.DefaultRouteDetails, error)
}

// newSystemBinding builds the network hooks an endpoint or a tailcat node dials through.
//
// # Why it also returns a cleanup
//
// When the platform owns the network interfaces it registers a process-GLOBAL interface getter with
// tailscale's netmon, and a global outlives the object that installed it. Nothing used to clear it,
// so after Close it kept a closure over a NetworkManager that had been torn down - and it survived
// into a later Box whose platform interface does not re-register, at which point netmon reads
// interfaces from a dead manager.
//
// The cleanup is returned rather than registered here because only the CALLER owns a scope, and it
// is registered on that scope so it runs at teardown and on EVERY failure path the scope already
// covers. Upstream performs the same reset from its close path
// (`netmon.RegisterInterfaceGetter(nil)`); this is the same fact expressed where this tree can
// honour it.
//
// A nil cleanup means nothing was registered, which is the case whenever the platform does not own
// the interfaces. Callers must tolerate nil: the reset is a no-op then, but relying on that would
// make the contract depend on a property of netmon rather than on this function's own return.
func newSystemBinding(ctx context.Context, logger logger.ContextLogger) (systemBinding, func() error, error) {
	network := service.FromContext[adapter.NetworkManager](ctx)
	platformInterface := service.FromContext[adapter.PlatformInterface](ctx)
	if platformInterface != nil && platformInterface.UsePlatformNetworkInterfaces() {
		err := network.UpdateInterfaces()
		if err != nil {
			return systemBinding{}, nil, err
		}
		netmon.RegisterInterfaceGetter(func() ([]netmon.Interface, error) {
			return common.Map(network.InterfaceFinder().Interfaces(), func(it control.Interface) netmon.Interface {
				return netmon.Interface{
					Interface: &net.Interface{
						Index:        it.Index,
						MTU:          it.MTU,
						Name:         it.Name,
						HardwareAddr: it.HardwareAddr,
						Flags:        it.Flags,
					},
					AltAddrs: common.Map(it.Addresses, func(it netip.Prefix) net.Addr {
						return &net.IPNet{
							IP:   it.Addr().AsSlice(),
							Mask: net.CIDRMask(it.Bits(), it.Addr().BitLen()),
						}
					}),
				}
			}), nil
		})
		defaultRoute := func() (netmon.DefaultRouteDetails, error) {
			defaultInterface := network.InterfaceMonitor().DefaultInterface()
			if defaultInterface == nil {
				return netmon.DefaultRouteDetails{}, nil
			}
			return netmon.DefaultRouteDetails{
				InterfaceName:  defaultInterface.Name,
				InterfaceIndex: defaultInterface.Index,
			}, nil
		}
		registerCleanup := func() error {
			netmon.RegisterInterfaceGetter(nil)
			return nil
		}
		binding, cleanup, err := newSystemBindingWithCleanup(ctx, logger, registerCleanup)
		binding.defaultRoute = defaultRoute
		return binding, cleanup, err
	}
	return newSystemBindingWithCleanup(ctx, logger, nil)
}

// newSystemBindingWithCleanup is the body of newSystemBinding once the interface-getter decision has
// been made, so every path below returns the same cleanup. It has several return points, and one of
// them silently dropping the cleanup is exactly the kind of omission this fix exists to remove.
func newSystemBindingWithCleanup(ctx context.Context, logger logger.ContextLogger, cleanup func() error) (systemBinding, func() error, error) {
	network := service.FromContext[adapter.NetworkManager](ctx)
	platformInterface := service.FromContext[adapter.PlatformInterface](ctx)
	if network.AutoRedirectOutputMark() != 0 {
		return systemBinding{control: network.AutoRedirectOutputMarkFunc()}, cleanup, nil
	}
	if platformInterface != nil && platformInterface.UsePlatformNetworkInterfaces() {
		if platformInterface.UsePlatformAutoDetectInterfaceControl() {
			return systemBinding{control: func(network, address string, conn syscall.RawConn) error {
				return control.Raw(conn, func(fileDescriptor uintptr) error {
					return platformInterface.AutoDetectInterfaceControl(int(fileDescriptor))
				})
			}}, cleanup, nil
		}
		// NEPacketTunnelProvider sockets are excluded from tunnel routes by
		// NECP; the empty override only suppresses tailscale's own
		// default-interface bind, which would select the sing-box utun.
		return systemBinding{control: func(string, string, syscall.RawConn) error {
			return nil
		}}, cleanup, nil
	}
	bindFunc := network.AutoDetectInterfaceFunc()
	if bindFunc == nil {
		return systemBinding{}, cleanup, nil
	}
	return systemBinding{
		control: bindFunc,
		listenPacket: func(ctx context.Context, networkName string, address string) (nettype.PacketConn, error) {
			listenConfig := net.ListenConfig{
				Control: control.Append(bindFunc, control.DisableUDPNetReset()),
			}
			packetConn, err := listenConfig.ListenPacket(ctx, networkName, address)
			if err != nil {
				return nil, err
			}
			udpConn := packetConn.(*net.UDPConn)
			egressPool := tun.NewUDPEgressPool(tun.UDPEgressPoolOptions{
				Logger:           logger,
				Network:          networkName,
				InterfaceFinder:  network.InterfaceFinder(),
				InterfaceMonitor: network.InterfaceMonitor(),
				IsExempt: func() bool {
					return network.AutoRedirectOutputMark() != 0
				},
			})
			if !egressPool.SetEgressPort(udpConn.LocalAddr().(*net.UDPAddr).AddrPort().Port()) {
				egressPool.Close()
				return udpConn, nil
			}
			return tun.NewUDPEgressConn(udpConn, egressPool), nil
		},
	}, cleanup, nil
}

func (b systemBinding) hooks(dialer N.Dialer) netmon.Hooks {
	return netmon.Hooks{Dialer: dialer, Control: b.control, ListenPacket: b.listenPacket, DefaultRoute: b.defaultRoute}
}

// registerSystemBindingCleanup puts a newSystemBinding cleanup on a scope, tolerating the nil a
// binding without a platform interface getter returns.
//
// It exists so the three call sites cannot each get the decision subtly wrong: a nil cleanup is
// legal and means nothing was registered, and a nil scope is what a caller that never reached its
// Start stage passes. Neither is an error, and neither should be silently turned into a panic in a
// teardown that runs on a failure path.
func registerSystemBindingCleanup(scope *adapter.Scope, cleanup func() error) {
	if scope == nil || cleanup == nil {
		return
	}
	scope.Add(cleanup)
}

// cleanupSystemBindingOnContext is the same guarantee for a caller that has no scope, because it
// builds its node lazily rather than during a Start stage.
//
// `context.AfterFunc` runs the reset when the lifetime context is done - which for a lazily built
// node is the only moment that exists - and it also runs it EARLY if the function returns true. It
// is used instead of a goroutine so a caller that never cancels its context leaks nothing extra:
// AfterFunc is stopped by that cancellation, and the reset is idempotent.
//
// A nil context or a nil cleanup is a no-op, matching registerSystemBindingCleanup: neither is an
// error, and neither should become a panic inside a teardown.
func cleanupSystemBindingOnContext(ctx context.Context, cleanup func() error) {
	if ctx == nil || cleanup == nil {
		return
	}
	context.AfterFunc(ctx, func() {
		_ = cleanup()
	})
}
