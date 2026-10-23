package dialer

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"slices"
	"syscall"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/listener"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/service/powerreport"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/bufio"
	"github.com/sagernet/sing/common/control"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"github.com/database64128/tfo-go/v2"
)

// Compile-time pins for the capabilities this type is the ONLY production implementer of.
//
// They are here because of how the same defect has recurred: a capability reached by runtime assertion
// stops being reachable when a wrapper in between is added, and nothing notices until real traffic cannot
// use it. A pin cannot prevent that, but it does make the implementer's side of every assertion a
// compile-time fact instead of a claim in a comment - so a refactor that drops the method fails the build
// rather than a production dial.
var (
	_ ParallelInterfaceDialer = (*DefaultDialer)(nil)
	_ UDPListener             = (*DefaultDialer)(nil)
	_ LocalFamilyDialer       = (*DefaultDialer)(nil)
	// The two owners are unexported, and they are what every wrapper and hop in the chain walks to.
	_ localFamilyOwner  = (*DefaultDialer)(nil)
	_ familyHealthOwner = (*DefaultDialer)(nil)
)

type DefaultDialer struct {
	dialer4            tfo.Dialer
	dialer6            tfo.Dialer
	udpDialer4         net.Dialer
	udpDialer6         net.Dialer
	udpListener        net.ListenConfig
	udpAddr4           string
	udpAddr6           string
	netns              string
	autoDetectBindFunc control.Func
	connectionManager  adapter.ConnectionManager
	networkManager     adapter.NetworkManager
	// network is the SINGLE resolved source of network observations this dialer reads.
	//
	// It replaces four independently-wired optional capabilities - an epoch getter, a settled-state
	// getter, a transition snapshot and a family-capability snapshot - which were resolved in two
	// different places with two different precedence orders. Every fact a dial decides on now comes
	// from one reading of one source: the generation a hand-over is validated against, the settled
	// state, the address-family verdict, and the same generation again as the namespace the scheduler
	// files what it learns about a family under. See networkObserver for why the resolution is taken
	// once, and what each shape degrades to.
	network networkObserver
	// powerManager, outboundManager and dnsTransportManager are attribution sources for the power
	// report; none of them is consulted for a decision.
	powerManager           *powerreport.Manager
	outboundManager        adapter.OutboundManager
	dnsTransportManager    adapter.DNSTransportManager
	networkStrategy        *C.NetworkStrategy
	defaultNetworkStrategy bool
	networkType            []C.InterfaceType
	fallbackNetworkType    []C.InterfaceType
	networkFallbackDelay   time.Duration
	networkLastFallback    common.TypedValue[time.Time]

	// familyHealth remembers which address family recently failed to establish a connection,
	// so the next connection does not pay the fallback delay again to rediscover it.
	//
	// It lives on the dialer, not in a package-level variable: two dialers with different
	// configurations (a direct one and a detour-bound one) do not necessarily share an
	// underlay, and sharing a verdict between them would penalise a path one of them never
	// used. It is created once here and reused for the dialer's lifetime - creating it per
	// connection would mean it never had any history to reuse.
	familyHealth *familyHealth
}

func NewDefault(ctx context.Context, options option.DialerOptions) (*DefaultDialer, error) {
	connectionManager := service.FromContext[adapter.ConnectionManager](ctx)
	networkManager := service.FromContext[adapter.NetworkManager](ctx)

	// ONE resolution of the network facts, from the strongest interface the manager implements.
	//
	// This used to be four independent resolutions - an epoch getter, a settled-state getter, a
	// transition snapshot and a family-capability snapshot - each optional, each wired separately. A
	// manager that reported a richer observation than one of those four could be read through the
	// narrower one, dropping the answer it gave; that defect was found and fixed twice, in two files,
	// because each resolution had its own precedence and neither could see the other. Resolving once
	// makes the preference a property of this constructor rather than of every consumer.
	network := newNetworkObserver(networkManager)
	platformInterface := service.FromContext[adapter.PlatformInterface](ctx)

	var (
		dialer                 net.Dialer
		listenConfig           net.ListenConfig
		interfaceFinder        control.InterfaceFinder
		networkStrategy        *C.NetworkStrategy
		defaultNetworkStrategy bool
		networkType            []C.InterfaceType
		fallbackNetworkType    []C.InterfaceType
		networkFallbackDelay   time.Duration
		autoDetectBindFunc     control.Func
	)
	if networkManager != nil {
		interfaceFinder = networkManager.InterfaceFinder()
	} else {
		interfaceFinder = control.NewDefaultInterfaceFinder()
	}
	socketBufferFunc := control.UDPSocketBuffer(listener.UDPSocketBufferSize())
	dialer.Control = control.Append(dialer.Control, socketBufferFunc)
	listenConfig.Control = control.Append(listenConfig.Control, socketBufferFunc)
	if options.BindInterface != "" {
		if !(C.IsLinux || C.IsDarwin || C.IsWindows) {
			return nil, E.New("`bind_interface` is only supported on Linux, macOS and Windows")
		}
		bindFunc := control.BindToInterface(interfaceFinder, options.BindInterface, -1)
		dialer.Control = control.Append(dialer.Control, bindFunc)
		listenConfig.Control = control.Append(listenConfig.Control, bindFunc)
	}
	if options.RoutingMark > 0 {
		if !C.IsLinux {
			return nil, E.New("`routing_mark` is only supported on Linux")
		}
		dialer.Control = control.Append(dialer.Control, setMarkWrapper(networkManager, uint32(options.RoutingMark), false))
		listenConfig.Control = control.Append(listenConfig.Control, setMarkWrapper(networkManager, uint32(options.RoutingMark), false))
	}
	disableDefaultBind := options.BindInterface != "" || options.Inet4BindAddress != nil || options.Inet6BindAddress != nil
	if disableDefaultBind || options.TCPFastOpen {
		if options.NetworkStrategy != nil || len(options.NetworkType) > 0 && options.FallbackNetworkType == nil && options.FallbackDelay == 0 {
			return nil, E.New("`network_strategy` is conflict with `bind_interface`, `inet4_bind_address`, `inet6_bind_address` and `tcp_fast_open`")
		}
	}

	if networkManager != nil {
		defaultOptions := networkManager.DefaultOptions()
		if defaultOptions.BindInterface != "" && !disableDefaultBind {
			bindFunc := control.BindToInterface(networkManager.InterfaceFinder(), defaultOptions.BindInterface, -1)
			dialer.Control = control.Append(dialer.Control, bindFunc)
			listenConfig.Control = control.Append(listenConfig.Control, bindFunc)
		} else if networkManager.AutoDetectInterface() && !disableDefaultBind {
			if platformInterface != nil && platformInterface.UsePlatformNetworkInterfaces() {
				networkStrategy = (*C.NetworkStrategy)(options.NetworkStrategy)
				networkType = common.Map(options.NetworkType, option.InterfaceType.Build)
				fallbackNetworkType = common.Map(options.FallbackNetworkType, option.InterfaceType.Build)
				if networkStrategy == nil && len(networkType) == 0 && len(fallbackNetworkType) == 0 {
					networkStrategy = defaultOptions.NetworkStrategy
					networkType = defaultOptions.NetworkType
					fallbackNetworkType = defaultOptions.FallbackNetworkType
				}
				networkFallbackDelay = time.Duration(options.FallbackDelay)
				if networkFallbackDelay == 0 && defaultOptions.FallbackDelay != 0 {
					networkFallbackDelay = defaultOptions.FallbackDelay
				}
				if networkStrategy == nil {
					networkStrategy = common.Ptr(C.NetworkStrategyDefault)
					defaultNetworkStrategy = true
				}
				bindFunc := networkManager.ProtectFunc()
				dialer.Control = control.Append(dialer.Control, bindFunc)
				listenConfig.Control = control.Append(listenConfig.Control, bindFunc)
			} else {
				bindFunc := networkManager.AutoDetectInterfaceFunc()
				dialer.Control = control.Append(dialer.Control, bindFunc)
				autoDetectBindFunc = bindFunc
			}
		}
		if options.RoutingMark == 0 && defaultOptions.RoutingMark != 0 {
			dialer.Control = control.Append(dialer.Control, setMarkWrapper(networkManager, defaultOptions.RoutingMark, true))
			listenConfig.Control = control.Append(listenConfig.Control, setMarkWrapper(networkManager, defaultOptions.RoutingMark, true))
		}
	}
	if networkManager != nil {
		markFunc := networkManager.AutoRedirectOutputMarkFunc()
		dialer.Control = control.Append(dialer.Control, markFunc)
		listenConfig.Control = control.Append(listenConfig.Control, markFunc)
	}
	if options.ReuseAddr {
		listenConfig.Control = control.Append(listenConfig.Control, control.ReuseAddr())
	}
	if options.ProtectPath != "" {
		dialer.Control = control.Append(dialer.Control, control.ProtectPath(options.ProtectPath))
		listenConfig.Control = control.Append(listenConfig.Control, control.ProtectPath(options.ProtectPath))
	}
	if options.BindAddressNoPort {
		if !C.IsLinux {
			return nil, E.New("`bind_address_no_port` is only supported on Linux")
		}
		dialer.Control = control.Append(dialer.Control, control.BindAddressNoPort())
	}
	if options.ConnectTimeout != 0 {
		dialer.Timeout = time.Duration(options.ConnectTimeout)
	} else {
		dialer.Timeout = C.TCPConnectTimeout
	}
	if options.DisableTCPKeepAlive {
		dialer.KeepAlive = -1
		dialer.KeepAliveConfig.Enable = false
	} else if options.TCPKeepAliveSystemDefaults {
		dialer.KeepAliveConfig = net.KeepAliveConfig{
			Enable:   true,
			Idle:     -1,
			Interval: -1,
			Count:    -1,
		}
	} else {
		keepIdle := time.Duration(options.TCPKeepAlive)
		if keepIdle == 0 {
			keepIdle = C.TCPKeepAliveInitial
		}
		keepInterval := time.Duration(options.TCPKeepAliveInterval)
		if keepInterval == 0 {
			keepInterval = C.TCPKeepAliveInterval
		}
		dialer.KeepAliveConfig = net.KeepAliveConfig{
			Enable:   true,
			Idle:     keepIdle,
			Interval: keepInterval,
		}
	}
	var udpFragment bool
	if options.UDPFragment != nil {
		udpFragment = *options.UDPFragment
	} else {
		udpFragment = options.UDPFragmentDefault
	}
	if udpFragment {
		dialer.Control = control.Append(dialer.Control, control.EnableUDPFragment())
		listenConfig.Control = control.Append(listenConfig.Control, control.EnableUDPFragment())
	} else {
		dialer.Control = control.Append(dialer.Control, control.DisableUDPFragment())
		listenConfig.Control = control.Append(listenConfig.Control, control.DisableUDPFragment())
	}
	var (
		dialer4    = dialer
		udpDialer4 = dialer
		udpAddr4   string
	)
	if options.Inet4BindAddress != nil {
		bindAddr := options.Inet4BindAddress.Build(netip.IPv4Unspecified())
		dialer4.LocalAddr = &net.TCPAddr{IP: bindAddr.AsSlice()}
		udpDialer4.LocalAddr = &net.UDPAddr{IP: bindAddr.AsSlice(), Port: int(options.UDPBindPort)}
		udpAddr4 = M.SocksaddrFrom(bindAddr, options.UDPBindPort).String()
	} else if options.UDPBindPort != 0 {
		udpDialer4.LocalAddr = &net.UDPAddr{IP: net.IPv4zero, Port: int(options.UDPBindPort)}
		udpAddr4 = M.SocksaddrFrom(netip.IPv4Unspecified(), options.UDPBindPort).String()
	}
	var (
		dialer6    = dialer
		udpDialer6 = dialer
		udpAddr6   string
	)
	if options.Inet6BindAddress != nil {
		bindAddr := options.Inet6BindAddress.Build(netip.IPv6Unspecified())
		dialer6.LocalAddr = &net.TCPAddr{IP: bindAddr.AsSlice()}
		udpDialer6.LocalAddr = &net.UDPAddr{IP: bindAddr.AsSlice(), Port: int(options.UDPBindPort)}
		udpAddr6 = M.SocksaddrFrom(bindAddr, options.UDPBindPort).String()
	} else if options.UDPBindPort != 0 {
		udpDialer6.LocalAddr = &net.UDPAddr{IP: net.IPv6unspecified, Port: int(options.UDPBindPort)}
		udpAddr6 = M.SocksaddrFrom(netip.IPv6Unspecified(), options.UDPBindPort).String()
	}
	if options.TCPMultiPath {
		dialer4.SetMultipathTCP(true)
	}
	tcpDialer4 := tfo.Dialer{Dialer: dialer4, DisableTFO: !options.TCPFastOpen}
	tcpDialer6 := tfo.Dialer{Dialer: dialer6, DisableTFO: !options.TCPFastOpen}
	return &DefaultDialer{
		dialer4:                tcpDialer4,
		dialer6:                tcpDialer6,
		udpDialer4:             udpDialer4,
		udpDialer6:             udpDialer6,
		udpListener:            listenConfig,
		udpAddr4:               udpAddr4,
		udpAddr6:               udpAddr6,
		netns:                  options.NetNs,
		autoDetectBindFunc:     autoDetectBindFunc,
		connectionManager:      connectionManager,
		networkManager:         networkManager,
		network:                network,
		familyHealth:           newFamilyHealth(),
		powerManager:           service.FromContext[*powerreport.Manager](ctx),
		outboundManager:        service.FromContext[adapter.OutboundManager](ctx),
		dnsTransportManager:    service.FromContext[adapter.DNSTransportManager](ctx),
		networkStrategy:        networkStrategy,
		defaultNetworkStrategy: defaultNetworkStrategy,
		networkType:            networkType,
		fallbackNetworkType:    fallbackNetworkType,
		networkFallbackDelay:   networkFallbackDelay,
	}, nil
}

func setMarkWrapper(networkManager adapter.NetworkManager, mark uint32, isDefault bool) control.Func {
	if networkManager == nil {
		return control.RoutingMark(mark)
	}
	return func(network, address string, conn syscall.RawConn) error {
		if networkManager.AutoRedirectOutputMark() != 0 {
			if isDefault {
				return E.New("`route.default_mark` is conflict with `tun.auto_redirect`")
			} else {
				return E.New("`routing_mark` is conflict with `tun.auto_redirect`")
			}
		}
		return control.RoutingMark(mark)(network, address, conn)
	}
}

func (d *DefaultDialer) DialContext(ctx context.Context, network string, address M.Socksaddr) (net.Conn, error) {
	if !address.IsValid() {
		return nil, E.New("invalid address")
	} else if address.IsDomain() {
		return nil, E.New("domain not resolved")
	}
	// Ownership and the family verdict are captured as ONE observation, before any raw work.
	//
	// They must be one capture for two reasons. The gate below makes a HARD decision - it refuses a
	// socket that will not otherwise be retried by anything - so it may only act on a verdict from the
	// network generation this dial belongs to. And capturing the epoch after the gate, which is what
	// this used to do, meant the gate could refuse on a verdict from generation N while the dial then
	// recorded ownership of generation N+1: a decision and a claim taken from two different networks.
	//
	// Capturing before the dial also keeps the original reason for the ordering: a reset landing
	// anywhere between here and the hand-over is detected, and capturing after the dial would let a
	// reset slip in before the capture and make a connection from the previous network look current.
	localFamily := d.captureLocalFamilyPolicy()
	epochCurrent := localFamily.ownership.predicate
	if epochCurrent == nil {
		// No manager, or one that cannot report an epoch: there is no ordering to enforce, so the
		// hand-over check accepts everything, exactly as it did before this round.
		epochCurrent = func() bool { return true }
	}
	// The last gate before a socket is created for a destination this machine dials itself.
	//
	// # Why this is enforced here as well as in the candidate scheduler
	//
	// The scheduler removes a provably undialable family from a candidate race, which is where the
	// fast-fail has its value: the address is never attempted, no fallback interval is spent on it,
	// and the healthy family leads. This check covers what the scheduler cannot see - a caller that
	// arrives with one explicit address and no candidate list, which is every UDP packet connection
	// and every direct entry into this dialer.
	//
	// # Why the policy decides, rather than this function
	//
	// excludedAddress is where ownership is consulted, so a verdict captured during a transition or
	// superseded by one refuses nothing here. That is Invariant A and B in one place: this call site
	// has no way to skip the check, and no second opinion about which generation it is in.
	//
	// # Why a refusal cannot be mistaken for a network failure
	//
	// It is produced before any socket exists and is not a path failure by any classifier: it is not
	// a syscall error, not a timeout, and not a cancellation. Family health therefore records
	// nothing, which is correct - a family with no addressing is not "recently unlucky", it is
	// absent, and the scheduler's fallback bias already handles the case this file exists for.
	//
	// # Why a local address is exempt
	//
	// A loopback, link-local, unique-local or unspecified destination is not reached over the
	// public path this verdict describes. Refusing one would break the connections that must keep
	// working precisely when public addressing is gone: a local resolver, a LAN peer, a value the
	// caller bound explicitly.
	//
	// That exemption is no longer written here. It is a term of the decision - scope decides which
	// question applies to a destination - so it lives in hardUndialableError with the others, which is
	// what stops this call site from being one of five places that each have to remember it.
	if refusal := localFamily.hardUndialableError(address.Addr); refusal != nil {
		return nil, refusal
	}
	if d.networkStrategy == nil {
		conn, err := listener.ListenNetworkNamespace[net.Conn](ctx, d.netns, func() (net.Conn, error) {
			switch N.NetworkName(network) {
			case N.NetworkUDP:
				if !address.IsIPv6() {
					return d.udpDialer4.DialContext(ctx, network, address.String())
				} else {
					return d.udpDialer6.DialContext(ctx, network, address.String())
				}
			}
			if !address.IsIPv6() {
				return DialSlowContext(&d.dialer4, ctx, d.netns, network, address)
			} else {
				return DialSlowContext(&d.dialer6, ctx, d.netns, network, address)
			}
		})
		tracked, trackErr := d.trackConn(ctx, address, conn, err)
		if trackErr != nil {
			return nil, trackErr
		}
		return d.stillCurrentEpoch(epochCurrent, tracked)
	} else {
		return d.dialParallelInterfaceEpoch(ctx, network, address, d.networkStrategy, d.networkType, d.fallbackNetworkType, d.networkFallbackDelay, epochCurrent)
	}
}

// DialParallelInterface dials across the configured interfaces, racing them.
//
// The epoch is captured here rather than by the caller because this is a public entry point: a
// production dial through a network strategy arrives directly, and a connection it produced belongs
// to the network it was dialled for just as much as one from DialContext.
func (d *DefaultDialer) DialParallelInterface(ctx context.Context, network string, address M.Socksaddr, strategy *C.NetworkStrategy, interfaceType []C.InterfaceType, fallbackInterfaceType []C.InterfaceType, fallbackDelay time.Duration) (net.Conn, error) {
	return d.dialParallelInterfaceEpoch(ctx, network, address, strategy, interfaceType, fallbackInterfaceType, fallbackDelay, d.captureEpoch())
}

func (d *DefaultDialer) dialParallelInterfaceEpoch(ctx context.Context, network string, address M.Socksaddr, strategy *C.NetworkStrategy, interfaceType []C.InterfaceType, fallbackInterfaceType []C.InterfaceType, fallbackDelay time.Duration, epochCurrent func() bool) (net.Conn, error) {
	if strategy == nil {
		strategy = d.networkStrategy
	}
	if strategy == nil {
		return d.DialContext(ctx, network, address)
	}
	if len(interfaceType) == 0 {
		interfaceType = d.networkType
	}
	if len(fallbackInterfaceType) == 0 {
		fallbackInterfaceType = d.fallbackNetworkType
	}
	if fallbackDelay == 0 {
		fallbackDelay = d.networkFallbackDelay
	}
	var dialer net.Dialer
	if N.NetworkName(network) == N.NetworkTCP {
		dialer = d.dialer4.Dialer
	} else {
		dialer = d.udpDialer4
	}
	fastFallback := time.Since(d.networkLastFallback.Load()) < C.TCPTimeout
	var (
		conn      net.Conn
		isPrimary bool
		err       error
	)
	if !fastFallback {
		conn, isPrimary, err = d.dialParallelInterface(ctx, dialer, network, address.String(), *strategy, interfaceType, fallbackInterfaceType, fallbackDelay)
	} else {
		conn, isPrimary, err = d.dialParallelInterfaceFastFallback(ctx, dialer, network, address.String(), *strategy, interfaceType, fallbackInterfaceType, fallbackDelay, d.networkLastFallback.Store)
	}
	if err != nil {
		// bind interface failed on legacy xiaomi systems
		if d.defaultNetworkStrategy && errors.Is(err, syscall.EPERM) {
			d.networkStrategy = nil
			return d.DialContext(ctx, network, address)
		} else {
			return nil, err
		}
	}
	if !fastFallback && !isPrimary {
		d.networkLastFallback.Store(time.Now())
	}
	tracked, trackErr := d.trackConn(ctx, address, conn, nil)
	if trackErr != nil {
		return nil, trackErr
	}
	return d.stillCurrentEpoch(epochCurrent, tracked)
}

func (d *DefaultDialer) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	// Captured before the socket is created, for the same reason as a dial - and captured as ONE
	// observation together with the family verdict, so the gate below cannot act on one generation's
	// verdict while the hand-over check validates another's ownership.
	localFamily := d.captureLocalFamilyPolicy()
	epochCurrent := localFamily.ownership.predicate
	if epochCurrent == nil {
		epochCurrent = func() bool { return true }
	}
	// A connected packet socket is a socket this machine creates for a destination it dials, so it
	// is subject to the same local verdict as a stream. It is checked here rather than in the
	// scheduler because a packet connection does not go through a candidate race at all.
	//
	// The check is skipped for an unspecified address, which is how a caller asks for a wildcard
	// socket with no destination family at all - the shape a listener uses, and one the packet
	// transports rely on.
	if destination.Addr.IsValid() && !destination.Addr.IsUnspecified() {
		if refusal := localFamily.hardUndialableError(destination.Addr); refusal != nil {
			return nil, refusal
		}
	}
	if d.networkStrategy == nil {
		packetConn, err := listener.ListenNetworkNamespace[net.PacketConn](ctx, d.netns, func() (net.PacketConn, error) {
			listenConfig := d.udpListener
			if d.autoDetectBindFunc != nil {
				listenConfig.Control = control.Append(listenConfig.Control, func(network, address string, conn syscall.RawConn) error {
					if destination.Addr.IsValid() {
						return d.autoDetectBindFunc(network, destination.String(), conn)
					}
					return d.autoDetectBindFunc(network, address, conn)
				})
			}
			if destination.IsIPv6() {
				return listenConfig.ListenPacket(ctx, N.NetworkUDP, d.udpAddr6)
			} else if destination.IsIPv4() && !destination.Addr.IsUnspecified() {
				return listenConfig.ListenPacket(ctx, N.NetworkUDP+"4", d.udpAddr4)
			} else {
				return listenConfig.ListenPacket(ctx, N.NetworkUDP, d.udpAddr4)
			}
		})
		tracked, trackErr := d.trackPacketConn(ctx, destination, packetConn, err)
		if trackErr != nil {
			return nil, trackErr
		}
		return d.stillCurrentEpochPacketConn(epochCurrent, tracked)
	} else {
		// The SAME capture is threaded through, so the interface path gates and validates on one
		// observation rather than taking a second one inside - which is what this call used to do, and
		// what let the gate decide on a generation this operation never claimed.
		return d.listenSerialInterfacePacketEpoch(ctx, destination, d.networkStrategy, d.networkType, d.fallbackNetworkType, d.networkFallbackDelay, localFamily)
	}
}

func (d *DefaultDialer) DialerForICMPDestination(destination netip.Addr) net.Dialer {
	if !destination.Is6() {
		return d.dialer4.Dialer
	} else {
		return d.dialer6.Dialer
	}
}

// ListenSerialInterfacePacket binds a socket to a chosen interface.
//
// Like DialParallelInterface this is a public entry point, so it captures its own ownership: a caller
// that arrives here directly gets the same guarantee as one that arrived through ListenPacket.
//
// The capture is taken ONCE and serves both the family gate and the hand-over check. It used to be two
// independent captures - `captureEpoch()` here and `captureLocalFamilyPolicy()` inside - read through two
// different optional interfaces, so the two could observe different network states within one call. That
// is the same teardown `DialContext` and `ListenPacket` already avoid by threading one policy through the
// whole operation, and this entry point was the one that did not.
//
// The harm was bounded - the hand-over check still validated the FIRST capture, so no connection produced
// under a divergent gate could be handed over - but the gate could hard-deny an operation whose own
// ownership capture said "not owned", which is the direction the design says must fail open. Measured on
// a manager whose snapshot reported an epoch the capability interface had already moved past: the gate
// refused with a family verdict for an operation that began unsettled.
func (d *DefaultDialer) ListenSerialInterfacePacket(ctx context.Context, destination M.Socksaddr, strategy *C.NetworkStrategy, interfaceType []C.InterfaceType, fallbackInterfaceType []C.InterfaceType, fallbackDelay time.Duration) (net.PacketConn, error) {
	return d.listenSerialInterfacePacketEpoch(ctx, destination, strategy, interfaceType, fallbackInterfaceType, fallbackDelay, d.captureLocalFamilyPolicy())
}

func (d *DefaultDialer) listenSerialInterfacePacketEpoch(ctx context.Context, destination M.Socksaddr, strategy *C.NetworkStrategy, interfaceType []C.InterfaceType, fallbackInterfaceType []C.InterfaceType, fallbackDelay time.Duration, localFamily localFamilyPolicy) (net.PacketConn, error) {
	if strategy == nil {
		strategy = d.networkStrategy
	}
	if strategy == nil {
		return d.ListenPacket(ctx, destination)
	}
	// The local family gate, for the same reason ListenPacket has one and with the same shape.
	//
	// # Why this entry point needs its own
	//
	// This function is a PUBLIC entry point (see the wrapper's comment) and it CREATES the packet
	// socket itself, so it cannot inherit the gate from ListenPacket: with a non-nil strategy it never
	// calls it. The package-level serial packet loop reaches exactly this function through its
	// ParallelInterfaceDialer branch -
	//
	//	default_parallel_network.go:167-172
	//	    conn, err := parallelDialer.ListenSerialInterfacePacket(ctx, ...)
	//
	// - which is the branch under the comment stating that "a refusal happens BEFORE socket creation,
	// in DefaultDialer.ListenPacket, so a family this machine provably cannot reach never enters it".
	// For that branch the comment was describing a gate that was not there. MEASURED, before this fix,
	// on a machine with no IPv6 addressing:
	//
	//	ListenPacket                  -> refused before socket creation   (control)
	//	ListenSerialInterfacePacket   -> err=nil, an IPv6 packet socket was created
	//
	// # Why it matters more here than for a stream
	//
	// A packet listener has no racer and no recovery: the serial loop's FIRST successfully created
	// socket wins outright and is never replaced (default_parallel_network.go:148-165). A stream that
	// picks the wrong family reconnects; a packet listener picks the wrong family and delivers nothing,
	// which is the reported `open UDP connection to [2606:...]` followed by `send peer batch:
	// unreachable network`. The pre-socket refusal is the ONLY thing that can move it to the other
	// family, so a missing gate is not a lost optimisation here - it is the difference between a
	// working fallback and a dead tunnel.
	//
	// The unspecified-address exception is the same one ListenPacket documents: it is how a caller asks
	// for a wildcard socket with no destination family at all.
	//
	// `localFamily` is the capture taken by the entry point, so this gate and the hand-over check below
	// describe ONE observation of the network. Re-capturing here would let the gate decide on a verdict
	// from a generation the operation never claimed.
	if destination.Addr.IsValid() && !destination.Addr.IsUnspecified() {
		if refusal := localFamily.hardUndialableError(destination.Addr); refusal != nil {
			return nil, refusal
		}
	}
	if len(interfaceType) == 0 {
		interfaceType = d.networkType
	}
	if len(fallbackInterfaceType) == 0 {
		fallbackInterfaceType = d.fallbackNetworkType
	}
	if fallbackDelay == 0 {
		fallbackDelay = d.networkFallbackDelay
	}
	network := N.NetworkUDP
	if destination.IsIPv4() && !destination.Addr.IsUnspecified() {
		network += "4"
	}
	packetConn, err := d.listenSerialInterfacePacket(ctx, d.udpListener, network, "", *strategy, interfaceType, fallbackInterfaceType, fallbackDelay)
	if err != nil {
		// bind interface failed on legacy xiaomi systems
		if d.defaultNetworkStrategy && errors.Is(err, syscall.EPERM) {
			d.networkStrategy = nil
			return d.ListenPacket(ctx, destination)
		} else {
			return nil, err
		}
	}
	tracked, trackErr := d.trackPacketConn(ctx, destination, packetConn, nil)
	if trackErr != nil {
		return nil, trackErr
	}
	return d.stillCurrentEpochPacketConn(localFamily.ownership.predicate, tracked)
}

func (d *DefaultDialer) UDPListenerControl() (control.Func, bool) {
	egressEnabled := d.autoDetectBindFunc != nil && d.netns == ""
	listenerControl := d.udpListener.Control
	if egressEnabled && d.networkManager.AutoRedirectOutputMark() == 0 {
		listenerControl = control.Append(listenerControl, d.autoDetectBindFunc)
	}
	return listenerControl, egressEnabled
}

// captureEpoch records the network epoch a raw operation is about to begin in.
//
// It returns a predicate that reports whether that epoch is still current, so the caller does not
// have to carry a possibly-nil function around.
// captureEpoch returns a predicate reporting whether the connection produced by this dial may still
// be handed to the caller.
//
// # Two questions, not one
//
//	"has the epoch moved since I captured it?"   rejects a dial that began BEFORE a transition
//	"is the network settled right now?"          rejects a dial taken DURING one
//
// The second is not implied by the first. A dial started while a transition is pending captures the
// transition's OWN epoch, so the epoch comparison finds them equal and would hand the connection
// over - even though the DNS generation has not advanced and the transport pins still name the
// network being left. Everything such a connection observes belongs to the old network while the
// caller believes it belongs to the new one.
//
// Requiring a settled network at hand-over time closes that: a connection dialled during a
// transition is refused, and the caller retries - which is the correct transient failure, not a
// permanent one, because the transition does complete.
//
// # Why this is now a projection rather than a second implementation
//
// It used to be the second of two branch trees over the same four optional interfaces, with its own
// precedence, and it was fixed separately from the other one for the same defect - a manager that
// reported a fact read through an accessor that could not carry it. Two implementations of "which
// generation am I in" can prefer different sources for the same manager, and nothing in the type
// system keeps them in step.
//
// It is now the ownership half of captureLocalFamilyPolicy: literally the same capture, with the
// verdict left unread. A caller cannot obtain a hand-over check from a different observation than the
// gate that decided the dial, and an entry point that has already captured a policy passes that
// capture through instead of taking a second one (see ListenSerialInterfacePacket).
func (d *DefaultDialer) captureEpoch() func() bool {
	ownership := d.captureLocalFamilyPolicy().ownership
	if ownership.predicate == nil {
		// No lifecycle source: the check does not apply, so it must not refuse anything. This is the
		// same degradation a manager with no epoch has always had.
		return func() bool { return true }
	}
	return ownership.predicate
}

// errNetworkChanged reports that a network operation completed across a network reset.
//
// It is deliberately not a new public error type: the operation did not fail for a reason the
// caller can act on differently from any other transient network failure, and the dial is retryable
// in exactly the way a failed one is.
var errNetworkChanged = E.New("network changed while dialling")

// stillCurrentEpoch closes the connection and reports an error when the network changed while the
// operation was in flight.
//
// # Why this runs AFTER the connection is tracked
//
// The obvious order - verify the epoch, then hand the connection to the manager - has a window the
// check cannot see:
//
//	verify epoch            (still current)
//	                        <- a reset runs here: CloseAll sees nothing to close
//	track
//
// and the connection escapes. Tracking FIRST closes that window in both directions:
//
//	reset before the track   -> the epoch check below rejects it, and the connection is closed here
//	reset after the track    -> CloseAll can see it and closes it
//
// There is no ordering in which the connection is neither rejected nor reachable by CloseAll.
func (d *DefaultDialer) stillCurrentEpoch(epochCurrent func() bool, conn net.Conn) (net.Conn, error) {
	if epochCurrent() {
		return conn, nil
	}
	_ = conn.Close()
	return nil, errNetworkChanged
}

func (d *DefaultDialer) trackConn(ctx context.Context, destination M.Socksaddr, conn net.Conn, err error) (net.Conn, error) {
	if err != nil {
		return conn, err
	}
	if nativeConn, isUDPConn := conn.(*net.UDPConn); isUDPConn {
		var rawConn syscall.RawConn
		rawConn, err = nativeConn.SyscallConn()
		if err != nil {
			conn.Close()
			return nil, err
		}
		conn = &udpConn{Conn: conn, rawConn: rawConn}
	}
	if d.connectionManager != nil {
		conn = d.connectionManager.TrackConn(conn)
	}
	if d.powerManager != nil {
		recorder := d.powerManager.Recorder()
		if recorder != nil {
			recorder.CountConnectionOpened()
			attribution := d.dialAttribution(ctx, destination)
			outboundCounter := recorder.TrafficCounter(powerreport.TrafficOutbound, attribution.Outbound)
			dnsCounter := recorder.TrafficCounter(powerreport.TrafficDNSTransport, attribution.DNS)
			outboundCounter.CountDial()
			dnsCounter.CountDial()
			conn = bufio.NewCounterConn(conn, []N.CountFunc{func(n int64) {
				outboundCounter.CountIn(n)
				dnsCounter.CountIn(n)
				recorder.Touch(powerreport.DirectionInbound, int(n), attribution)
			}}, []N.CountFunc{func(n int64) {
				outboundCounter.CountOut(n)
				dnsCounter.CountOut(n)
				recorder.Touch(powerreport.DirectionOutbound, int(n), attribution)
			}})
		}
	}
	return conn, nil
}

// stillCurrentEpochPacketConn is stillCurrentEpoch for a packet connection.
//
// A listening socket has the same ownership question as a dialled one: it was bound to the previous
// network's interface, so handing it to a caller after a reset gives them a socket on a network that
// has been left.
func (d *DefaultDialer) stillCurrentEpochPacketConn(epochCurrent func() bool, conn net.PacketConn) (net.PacketConn, error) {
	// A manager that can report no ownership constrains nothing, which is the same degradation every
	// other optional capability here takes. The family-policy capture leaves the predicate nil in that
	// case rather than inventing a permissive one, so it is normalised HERE - the one place that has to
	// call it - instead of at each caller, where a missed normalisation would be a nil dereference.
	if epochCurrent == nil {
		epochCurrent = func() bool { return true }
	}
	if epochCurrent() {
		return conn, nil
	}
	_ = conn.Close()
	return nil, errNetworkChanged
}

func (d *DefaultDialer) trackPacketConn(ctx context.Context, destination M.Socksaddr, conn net.PacketConn, err error) (net.PacketConn, error) {
	if err != nil {
		return conn, err
	}
	if d.connectionManager != nil {
		conn = d.connectionManager.TrackPacketConn(conn)
	}
	if d.powerManager != nil {
		recorder := d.powerManager.Recorder()
		if recorder != nil {
			recorder.CountConnectionOpened()
			attribution := d.dialAttribution(ctx, destination)
			outboundCounter := recorder.TrafficCounter(powerreport.TrafficOutbound, attribution.Outbound)
			dnsCounter := recorder.TrafficCounter(powerreport.TrafficDNSTransport, attribution.DNS)
			outboundCounter.CountDial()
			dnsCounter.CountDial()
			conn = bufio.NewNetPacketConn(bufio.NewCounterPacketConn(bufio.NewPacketConn(conn), []N.CountFunc{func(n int64) {
				outboundCounter.CountIn(n)
				dnsCounter.CountIn(n)
				recorder.Touch(powerreport.DirectionInbound, int(n), attribution)
			}}, []N.CountFunc{func(n int64) {
				outboundCounter.CountOut(n)
				dnsCounter.CountOut(n)
				recorder.Touch(powerreport.DirectionOutbound, int(n), attribution)
			}}))
		}
	}
	return conn, nil
}

func (d *DefaultDialer) dialAttribution(ctx context.Context, destination M.Socksaddr) *powerreport.Attribution {
	attribution := &powerreport.Attribution{}
	dnsTransportTag, hasDNSTransport := adapter.DNSTransportTagFromContext(ctx)
	if hasDNSTransport {
		attribution.DNS = dnsTransportTag
		if d.dnsTransportManager != nil {
			transport, loaded := d.dnsTransportManager.Transport(dnsTransportTag)
			if loaded {
				attribution.DNSType = transport.Type()
			}
		}
	}
	metadata := adapter.ContextFrom(ctx)
	if metadata == nil {
		attribution.Destination = destination.String()
		return attribution
	}
	attribution.Inbound = metadata.Inbound
	attribution.InboundType = metadata.InboundType
	attribution.Network = metadata.Network
	if metadata.Source.IsValid() {
		attribution.Source = metadata.Source.String()
	}
	attribution.Domain = metadata.Domain
	attribution.Protocol = metadata.Protocol
	attribution.User = metadata.User
	if metadata.ProcessInfo != nil {
		attribution.Process = &powerreport.ProcessAttribution{
			ProcessID:    metadata.ProcessInfo.ProcessID,
			UserID:       metadata.ProcessInfo.UserId,
			UserName:     metadata.ProcessInfo.UserName,
			ProcessPaths: metadata.ProcessInfo.ProcessPaths,
			PackageNames: metadata.ProcessInfo.PackageNames,
		}
	}
	attribution.Rule = metadata.RouteRule
	attribution.Outbound = metadata.Outbound
	attribution.Chain = common.Map(metadata.OutboundChain, adapter.Outbound.Tag)
	slices.Reverse(attribution.Chain)
	if d.outboundManager != nil {
		if metadata.Outbound != "" {
			outbound, loaded := d.outboundManager.Outbound(metadata.Outbound)
			if loaded {
				attribution.OutboundType = outbound.Type()
			}
		}
	}
	if metadata.Destination.IsValid() {
		attribution.Destination = metadata.Destination.String()
		if metadata.Destination != destination {
			attribution.Server = destination.String()
		}
	} else {
		attribution.Destination = destination.String()
	}
	return attribution
}

// The network generation this package used to expose as `networkGeneration()` is gone as a production
// surface, and the reason is worth one paragraph because the function looked harmless.
//
// It was `d.captureLocalFamilyPolicy().ownership.epoch`: a FRESH capture per call, whose only readers were
// two tests asking "which namespace will the health table use". Since this round the answer comes from the
// flow's own policy (`admissionPolicy.generation()`), so the accessor had no production caller - and an
// accessor that takes a fresh reading of the network to answer a question about one flow is exactly the
// shape the admission policy was built to remove. Leaving it would have been a signpost pointing at the
// pattern the round deletes: a reader who found it would have no reason to suspect that a per-flow value
// exists.
//
// The health-namespace reasoning it carried - the generation rather than the environment fingerprint,
// because the fingerprint was a second reading of the same network - lives where the namespace is now
// decided, in DefaultDialer.newDualStackScheduler and admissionPolicy.generation.

// newDualStackScheduler builds a scheduler from ONE flow's admission policy, wired to this dialer's
// family health.
//
// Every production construction goes through here, so a scheduler cannot be created without
// health by accident - which is how the fast-fallback path ended up existing only in tests
// while the production schedulers passed health: nil.
//
// # Why the policy is a PARAMETER now, and not captured here
//
// It used to capture its own: `policy := d.captureLocalFamilyPolicy()`. That made this the THIRD capture
// of one flow - the literal path had taken one to admit the original, the recovery path had taken one to
// decide which family ends the lookup, and this took another - so one connection could filter its
// candidates against three readings of one network, and the family that recovery had just refused could
// be re-admitted by a later capture in a different instant.
//
// The policy now comes from the flow that is starting, so the scheduler's verdict, its health namespace
// and every decision made before it belong to the SAME reading. The scheduler still carries the two
// fields rather than the policy value, because it is the object that most needs them on the hot path and
// because `localFamily` and `networkGeneration` are how the rest of that file names them.
//
// # Why the health namespace comes out of the SAME value
//
// It used to be a second call - `d.networkEnvironment()` beside `d.captureLocalFamilyPolicy()` - so a
// transition landing between them stored one network's identity on a scheduler holding another's
// verdict. The consequence is bounded (a health verdict filed under the wrong network loses the
// fast-fallback bias for one dial, it does not deny a flow) but it is the same defect as the rest of this
// round: two readings composed into one decision. Both fields now come from one value, so there is
// nothing left to read twice. See admissionPolicy.generation.
//
// # Why the scheduler still removes candidates itself
//
// It is the last gate before a socket exists, so nothing downstream can rescue a family it dropped; a
// scheduler built on a verdict it no longer owns would delete the successor generation's candidates and no
// later validation would notice. That is why localFamilyPolicy's ownership predicate is consulted on
// every refusal rather than resolved once.
func (d *DefaultDialer) newDualStackScheduler(fallbackDelay time.Duration, admission admissionPolicy) *candidateScheduler {
	return &candidateScheduler{
		fallbackDelay:     fallbackDelay,
		health:            d.familyHealth,
		networkGeneration: admission.generation(),
		localFamily:       admission.localFamily,
	}
}

// localFamilyPolicy reads this dialer's view of which families the machine can dial, as one
// observation with the network ownership it belongs to.
//
// It is exposed on the owner rather than on each consumer so that the scheduler, the literal path
// and the recovery path all ask one question of one object. A caller that read the manager itself
// would be taking a second observation of the same instant, and the two could disagree.
func (d *DefaultDialer) localFamilyPolicy() localFamilyPolicy {
	return d.captureLocalFamilyPolicy()
}
