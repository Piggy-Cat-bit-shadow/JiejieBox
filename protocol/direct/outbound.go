package direct

import (
	"context"
	"net"
	"net/netip"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/dialer"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing-tun/ping"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/control"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
)

func RegisterOutbound(registry *outbound.Registry) {
	outbound.Register[option.DirectOutboundOptions](registry, C.TypeDirect, NewOutbound)
}

var (
	_ N.ParallelDialer                = (*Outbound)(nil)
	_ dialer.ParallelNetworkDialer    = (*Outbound)(nil)
	_ dialer.DirectDialer             = (*Outbound)(nil)
	_ adapter.FlowOutbound            = (*Outbound)(nil)
	_ adapter.BypassableOutbound      = (*Outbound)(nil)
	_ adapter.InterfaceUpdateListener = (*Outbound)(nil)
)

type Outbound struct {
	outbound.Adapter
	ctx            context.Context
	logger         logger.ContextLogger
	network        adapter.NetworkManager
	dialer         dialer.ParallelInterfaceDialer
	domainStrategy C.DomainStrategy
	fallbackDelay  time.Duration
	// semantics is the precomputed native-bypass profile of this outbound's dial options. It is
	// built once, here, from the same options the dialer was built from, and it is the only thing
	// that answers "would the userspace dial path do anything special for this flow?".
	semantics dialer.SocketSemantics
	// familyStrategy reports the effective address-family policy of the resolver this outbound was
	// given, or nil when it has none. It is a function rather than a value because the effective
	// policy can come from the resolver's own configuration and is only known once the router can
	// be asked.
	familyStrategy func() C.DomainStrategy
	// localFamily reports whether this machine has no usable path for an address's family, or nil
	// when the dialer chain cannot say. It is what keeps a bypass from handing the platform an
	// address the userspace path would have refused immediately; see
	// dialer.BlockerLocalFamilyUnavailable.
	localFamily func(netip.Addr) bool
	myAddresses common.TypedValue[[]netip.Prefix]
	icmpPort    *ping.Port
}

func NewOutbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.DirectOutboundOptions) (adapter.Outbound, error) {
	options.UDPFragmentDefault = true
	if options.Detour != "" {
		return nil, E.New("`detour` is not supported in direct context")
	}
	outboundDialer, err := dialer.NewWithOptions(dialer.Options{
		Context:        ctx,
		Options:        options.DialerOptions,
		RemoteIsDomain: true,
		DirectOutbound: true,
	})
	if err != nil {
		return nil, err
	}
	// The field is declared at the parallel-INTERFACE type because that is the narrowest surface an
	// attempt needs, and the richer method it also implements is asked for by type assertion at the
	// point of use (see scheduleNetwork).
	//
	// The assertion is CHECKED, and that matters. `dialer.NewWithOptions` returns whatever the leaf
	// factory produced, wrapped in a resolve dialer only when its own conditions hold; nothing in the
	// type system makes "the result of that call implements ParallelInterfaceDialer" true. An unchecked
	// assertion here turns a future change in the factory - or an option combination that skips the
	// wrapper - into a PANIC during configuration load, where every other failure in this constructor
	// is a returned error an operator can read and fix. Measured: reverting `NewResolveDialer` to return
	// the plain wrapper makes this line panic.
	parallelDialer, isParallelDialer := outboundDialer.(dialer.ParallelInterfaceDialer)
	if !isParallelDialer {
		return nil, E.New("direct outbound: the dialer chain produced an object that does not offer the ",
			"per-attempt parallel surface this outbound schedules attempts through; this is a defect in ",
			"the dialer factory rather than in the configuration")
	}
	outbound := &Outbound{
		Adapter: outbound.NewAdapterWithDialerOptions(C.TypeDirect, tag, []string{N.NetworkTCP, N.NetworkUDP, N.NetworkICMP}, options.DialerOptions, true),
		ctx:     ctx,
		logger:  logger,
		network: service.FromContext[adapter.NetworkManager](ctx),
		//nolint:staticcheck
		domainStrategy: C.DomainStrategy(options.DomainStrategy),
		fallbackDelay:  time.Duration(options.FallbackDelay),
		dialer:         parallelDialer,
		semantics:      dialer.NativeBypassSemantics(options.DialerOptions),
	}
	if reporter, isReporter := outboundDialer.(dialer.ResolveDialer); isReporter {
		outbound.familyStrategy = reporter.EffectiveFamilyStrategy
	}
	// Discovered through the chain rather than configured, for the same reason the resolver is: the
	// object that knows is the dialer that will actually create the socket, and the bypass must not
	// hold a second opinion about the machine's addressing.
	if localFamily, isLocalFamily := outboundDialer.(dialer.LocalFamilyDialer); isLocalFamily {
		outbound.localFamily = localFamily.LocalFamilyBlocked
	}
	//nolint:staticcheck
	if options.ProxyProtocol != 0 {
		return nil, E.New("Proxy Protocol is deprecated and removed in sing-box 1.6.0")
	}
	if defaultDialer, isDefaultDialer := common.Cast[*dialer.DefaultDialer](outbound.dialer); isDefaultDialer {
		outbound.icmpPort = ping.NewPort(ctx, logger, func(destination netip.Addr) control.Func {
			return defaultDialer.DialerForICMPDestination(destination).Control
		}, 0)
	}
	return outbound, nil
}

func (h *Outbound) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	switch stage {
	case adapter.StartStateInitialize:
		if h.icmpPort != nil {
			scope.Add(h.icmpPort.Close)
		}
	case adapter.StartStatePostStart, adapter.StartStateStarted:
		if len(h.myAddresses.Load()) == 0 {
			h.fetchMyAddresses()
		}
	}
	return nil
}

// DialsDestinationLocally implements adapter.LocalDestinationDialer.
//
// This outbound IS the destination connection: it opens an operating-system socket to the
// destination (`dialer.NewWithOptions` with `DirectOutbound: true` above), so a destination DOMAIN
// is resolved on this device and the name never leaves it. There is no peer and no request, which is
// why `destination_dns_ownership` - documented as a promise about what a downstream PROXY receives -
// has no failure mode here and cannot be violated.
//
// It reports true unconditionally because the property is structural rather than configured: there
// is no option that would make this outbound hand a domain to someone else. `detour` is refused
// outright ("`detour` is not supported in direct context"), so no other hop can take the request
// either.
func (h *Outbound) DialsDestinationLocally() bool {
	return true
}

func (h *Outbound) fetchMyAddresses() {
	interfaceMonitor := h.network.InterfaceMonitor()
	if interfaceMonitor == nil {
		return
	}
	myInterfaceNames := interfaceMonitor.MyInterfaces()
	if len(myInterfaceNames) == 0 {
		return
	}
	var (
		myAddresses []netip.Prefix
		found       bool
	)
	for _, myInterfaceName := range myInterfaceNames {
		myInterface, err := h.network.InterfaceFinder().ByName(myInterfaceName)
		if err != nil {
			continue
		}
		found = true
		myAddresses = append(myAddresses, myInterface.Addresses...)
	}
	if !found {
		return
	}
	h.myAddresses.Store(myAddresses)
}

func (h *Outbound) InterfaceUpdated(ctx context.Context) {
	h.fetchMyAddresses()
	if h.icmpPort != nil {
		h.icmpPort.Close()
	}
}

func (h *Outbound) isMyLoopbackAddress(addresses ...netip.Addr) bool {
	for _, prefix := range h.myAddresses.Load() {
		for _, address := range addresses {
			if !C.IsDarwin && prefix.Addr() == address {
				continue
			}
			if prefix.Contains(address) {
				return true
			}
		}
	}
	return false
}

func (h *Outbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	if h.isMyLoopbackAddress(destination.Addr) {
		return nil, E.New("loopback connection to TUN range")
	}
	ctx, metadata := adapter.ExtendContext(ctx)
	metadata.Outbound = h.Tag()
	metadata.Destination = destination
	network = N.NetworkName(network)
	switch network {
	case N.NetworkTCP:
		h.logger.InfoContext(ctx, "outbound connection to ", destination)
	case N.NetworkUDP:
		h.logger.InfoContext(ctx, "outbound packet connection to ", destination)
	}
	return h.dialer.DialContext(ctx, network, destination)
}

func (h *Outbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	if h.isMyLoopbackAddress(destination.Addr) {
		return nil, E.New("loopback connection to TUN range")
	}
	ctx, metadata := adapter.ExtendContext(ctx)
	metadata.Outbound = h.Tag()
	metadata.Destination = destination
	h.logger.InfoContext(ctx, "outbound packet connection")
	conn, err := h.dialer.ListenPacket(ctx, destination)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

func (h *Outbound) PreMatchFlow(network string, destination netip.Addr) adapter.PreMatchAction {
	if network == N.NetworkICMP && h.icmpPort != nil {
		return adapter.PreMatchFlow
	}
	return adapter.PreMatchContinue
}

func (h *Outbound) PortAddresses() (netip.Addr, netip.Addr) {
	return h.icmpPort.PortAddresses()
}

func (h *Outbound) PortMTU() uint32 {
	return h.icmpPort.PortMTU()
}

func (h *Outbound) AttachReturn(returnPath tun.Return) error {
	return h.icmpPort.AttachReturn(returnPath)
}

func (h *Outbound) DetachReturn(returnPath tun.Return) error {
	return h.icmpPort.DetachReturn(returnPath)
}

func (h *Outbound) WritePackets(packets [][]byte) error {
	return h.icmpPort.WritePackets(packets)
}

func (h *Outbound) DialParallel(ctx context.Context, network string, destination M.Socksaddr, destinationAddresses []netip.Addr) (net.Conn, error) {
	if h.isMyLoopbackAddress(destinationAddresses...) {
		return nil, E.New("loopback connection to TUN range")
	}
	ctx, metadata := adapter.ExtendContext(ctx)
	metadata.Outbound = h.Tag()
	metadata.Destination = destination
	network = N.NetworkName(network)
	switch network {
	case N.NetworkTCP:
		h.logger.InfoContext(ctx, "outbound connection to ", destination)
	case N.NetworkUDP:
		h.logger.InfoContext(ctx, "outbound packet connection to ", destination)
	}
	return h.scheduleNetwork(ctx, network, destination, destinationAddresses, len(destinationAddresses) > 0 && destinationAddresses[0].Is6(), nil, nil, nil, h.fallbackDelay)
}

func (h *Outbound) DialParallelNetwork(ctx context.Context, network string, destination M.Socksaddr, destinationAddresses []netip.Addr, networkStrategy *C.NetworkStrategy, networkType []C.InterfaceType, fallbackNetworkType []C.InterfaceType, fallbackDelay time.Duration) (net.Conn, error) {
	if h.isMyLoopbackAddress(destinationAddresses...) {
		return nil, E.New("loopback connection to TUN range")
	}
	ctx, metadata := adapter.ExtendContext(ctx)
	metadata.Outbound = h.Tag()
	metadata.Destination = destination
	network = N.NetworkName(network)
	switch network {
	case N.NetworkTCP:
		h.logger.InfoContext(ctx, "outbound connection to ", destination)
	case N.NetworkUDP:
		h.logger.InfoContext(ctx, "outbound packet connection to ", destination)
	}
	return h.scheduleNetwork(ctx, network, destination, destinationAddresses, len(destinationAddresses) > 0 && destinationAddresses[0].Is6(), networkStrategy, networkType, fallbackNetworkType, fallbackDelay)
}

// scheduleNetwork hands the candidate list to whichever of the two dialing surfaces this outbound's
// dialer actually has.
//
// # Why this is a dispatch rather than a direct call to the package helper
//
// h.dialer is stored as ParallelInterfaceDialer because that is the narrowest surface this outbound
// needs for an ATTEMPT, and because a detour dialer has no parallel-network surface at all. But the
// object inside that field is usually a *dialer.resolveParallelNetworkDialer, which implements the
// richer ParallelNetworkDialer - and its DialParallelNetwork is the only place domain recovery for a
// literal destination lives: the sniffed-domain lookup, the late-candidate feed, the family fallback.
//
// Calling the package-level helper directly passes h.dialer in at the NARROW type, and
// DialParallelNetwork's body never asks whether the object it was handed could have discovered more - it
// plans the candidates it is given and schedules them. So the richer method was never invoked, and a TUN
// flow to a global IPv6 literal with a sniffed domain was refused by the local-family gate while the
// recovery machinery sat one call away with the name already in the context metadata. In the production
// log that is the WeChat flow with `sniffed protocol: http, domain: szminorshort.weixin.qq.com` followed
// by the IPv6 refusal and no `dns: lookup domain` line between them.
//
// Fixing the wrapper alone was not enough: `DialSerialNetwork` does reach
// resolveParallelNetworkDialer.DialParallelNetwork, but only when it is handed the wrapper. The
// connection manager hands it the OUTBOUND, so this method is the actual entry point and the
// capability has to survive here.
//
// # Scope, exactly
//
// The STREAM entry points needed this: `dialer.DialParallelNetwork` takes the narrow type and does not
// re-dispatch. The PACKET one did not - `dialer.ListenSerialNetworkPacket` type-asserts the richer
// interface itself, so a wrapped dialer already reached its recovery, and listenNetworkPacket below is
// consistency rather than a fix. Writing the two asymmetrically would mean the next reader has to work
// out which helper re-dispatches, and getting that wrong is precisely how this bug happened.
//
// # Why this cannot recurse
//
// The wrapper schedules each attempt onto the object it was built over (`attemptDialer`), which is the
// leaf, never the outbound: `NewResolveDialer` constructs it over the output of the leaf factory, and
// the wrapper the outbound holds is the object this field was assigned from. So turning here reaches
// the scheduler and then a real socket, which is the same graph
// resolveParallelNetworkDialer.attemptDialer documents.
//
// # Why the fallback is not dead code
//
// A detour routes to another OUTBOUND instead of creating a socket, so its dialer has no parallel
// network surface and takes the package helper exactly as before, with no scheduler of its own and no
// recovery - which is correct, because the destination of a detoured flow is the proxy's business and
// this machine's addressing must never rewrite it.
func (h *Outbound) scheduleNetwork(ctx context.Context, network string, destination M.Socksaddr, destinationAddresses []netip.Addr, preferIPv6 bool, networkStrategy *C.NetworkStrategy, networkType []C.InterfaceType, fallbackNetworkType []C.InterfaceType, fallbackDelay time.Duration) (net.Conn, error) {
	if networkDialer, isNetworkDialer := h.dialer.(dialer.ParallelNetworkDialer); isNetworkDialer {
		return networkDialer.DialParallelNetwork(ctx, network, destination, destinationAddresses, networkStrategy, networkType, fallbackNetworkType, fallbackDelay)
	}
	return dialer.DialParallelNetwork(ctx, h.dialer, network, destination, destinationAddresses, preferIPv6, networkStrategy, networkType, fallbackNetworkType, fallbackDelay)
}

func (h *Outbound) ListenSerialNetworkPacket(ctx context.Context, destination M.Socksaddr, destinationAddresses []netip.Addr, networkStrategy *C.NetworkStrategy, networkType []C.InterfaceType, fallbackNetworkType []C.InterfaceType, fallbackDelay time.Duration) (net.PacketConn, netip.Addr, error) {
	if h.isMyLoopbackAddress(destinationAddresses...) {
		return nil, netip.Addr{}, E.New("loopback connection to TUN range")
	}
	ctx, metadata := adapter.ExtendContext(ctx)
	metadata.Outbound = h.Tag()
	metadata.Destination = destination
	h.logger.InfoContext(ctx, "outbound packet connection")
	return h.listenNetworkPacket(ctx, destination, destinationAddresses, networkStrategy, networkType, fallbackNetworkType, fallbackDelay)
}

// listenNetworkPacket is scheduleNetwork's packet counterpart.
//
// # Scope, stated honestly
//
// This one is CONSISTENCY, not a repair. `dialer.ListenSerialNetworkPacket` type-asserts the richer
// interface itself (default_parallel_network.go, its first act), so a wrapped dialer already reached the
// wrapper's packet arm - and with it recovery - before this existed. Leaving the two arms asymmetrical
// would mean the next reader has to work out which package helper re-dispatches, and getting that wrong
// is exactly how the stream defect survived two rounds of fixes. Both arms now say what they mean: hand
// the list to the surface the object HAS.
//
// It is also not dead weight. A helper that later drops its internal assertion, or a wrapper added
// between this outbound and the resolve dialer, would silently make the packet path depend on a narrow
// call again; here the capability is asked for at the point that holds it.
//
// # Why the packet path needs the candidate at all
//
// An unconnected packet flow has no scheduler - a UDP socket creation is not a verified outcome, so
// racing two sockets would deliver duplicate datagrams and award a winner that never sent anything.
// `ListenSerialNetworkPacket` is a serial walk whose ONLY fallback is another candidate. On a machine
// whose global IPv6 does not route, a UDP socket to it can still be created, so an unrecovered global
// IPv6 literal can win the walk and leave the client with a connection that dies at the first send. The
// sniffed-domain candidate is therefore not an optimisation on this path; it is the only thing that can
// move the flow to a family that works.
//
// The wrapper's packet arm runs that lookup up front, bounded by the caller's context, and orders the
// recovered addresses BEHIND the application's own - so the walk keeps the "the literal is the endpoint
// the application chose" rule while giving it somewhere to go when the literal cannot be used.
func (h *Outbound) listenNetworkPacket(ctx context.Context, destination M.Socksaddr, destinationAddresses []netip.Addr, networkStrategy *C.NetworkStrategy, networkType []C.InterfaceType, fallbackNetworkType []C.InterfaceType, fallbackDelay time.Duration) (net.PacketConn, netip.Addr, error) {
	if networkDialer, isNetworkDialer := h.dialer.(dialer.ParallelNetworkDialer); isNetworkDialer {
		return networkDialer.ListenSerialNetworkPacket(ctx, destination, destinationAddresses, networkStrategy, networkType, fallbackNetworkType, fallbackDelay)
	}
	return dialer.ListenSerialNetworkPacket(ctx, h.dialer, destination, destinationAddresses, networkStrategy, networkType, fallbackNetworkType, fallbackDelay)
}

// IsEmpty reports whether this outbound asks the socket layer for nothing at all.
//
// It is NOT the bypass question, and the difference is the point of the profile: a direct outbound
// configured only with a domain_resolver is not empty, because it resolves names for the flows that
// need it, while a literal-IP flow through the same outbound still needs no resolution and is
// reproducible by the platform's own connect.
func (h *Outbound) IsEmpty() bool {
	return h.semantics.IsPlain()
}

// CanBypass reports whether a connection over this network to this literal destination may skip the
// userspace data path.
//
// # What it answers, and how
//
// It is a conjunction of three things the userspace path would otherwise do:
//
//	the dial options        the profile built once from the same options the dialer was built from
//	the ambient policy      the network manager's default bind, mark, strategy and fallback
//	the self-address guard  the check that keeps a TUN from routing a connection into itself
//
// # Why the options are no longer compared wholesale
//
// The previous implementation asked whether the outbound's options were literally empty. That is
// safe but it answers the wrong question: a configured option only disqualifies a flow if it would
// AFFECT that flow. The production topology is the case in point - its direct outbound carries
// domain_resolver and nothing else - and a literal-IP flow through it never reaches the resolver.
// Comparing the configuration refused the bypass; comparing the semantics does not.
//
// The conservative direction is preserved where it matters. Every option that changes a socket, a
// bind, a mark, a timeout, TCP behaviour, interface selection or the resolution of a name still
// refuses, an option nobody has classified refuses, and the ambient-policy and self-address guards
// are unchanged.
func (h *Outbound) CanBypass(network string, destination netip.Addr) bool {
	return h.BypassBlockers(network, destination) == dialer.BlockerNone
}

// BypassBlockers reports every reason this outbound cannot be reproduced by the platform's own
// connect for this flow. The empty set means it can.
//
// It exists beside CanBypass so a refusal can be attributed rather than merely observed: knowing
// that the fast path is off is not actionable, and knowing it is off because of a routing mark is.
// The cost is one profile lookup and two comparisons on a path that runs once per flow.
func (h *Outbound) BypassBlockers(network string, destination netip.Addr) dialer.NativeBypassBlocker {
	facts := dialer.NativeBypassFacts{
		Network: network,
		// Always true here, and not by assumption: the router only consults a bypassable outbound
		// after establishing that the destination is a literal with no candidate list, no sniffed
		// domain and no rewrite. A flow that needs a name resolved never reaches this method.
		DestinationIsLiteral: true,
		Destination:          destination,
	}
	if h.familyStrategy != nil {
		facts.FamilyStrategy = h.familyStrategy()
	}
	blockers := h.semantics.Blockers(facts)
	blockers |= dialer.NetworkPolicyBlockers(h.network)
	if h.localFamily != nil && h.localFamily(destination) {
		// The userspace path refuses this attempt before creating a socket, because the machine has
		// no interface carrying an address of this family. A bypass is a connect performed by the
		// platform, which has no such judgement, so without this the flow would spend the full
		// connect timeout on an address that cannot be reached - the exact wait the capability
		// exists to remove. Refusing the bypass sends the flow back to the path that fails it
		// immediately with a reason.
		blockers |= dialer.BlockerLocalFamilyUnavailable
	}
	if h.isMyLoopbackAddress(destination) {
		// The userspace path refuses to dial this host's own addresses so a TUN cannot route a
		// connection back into itself. A bypass is a connect performed by the platform, which has no
		// such guard, so this check is the only thing standing between the fast path and that loop.
		blockers |= dialer.BlockerSelfAddress
	}
	return blockers
}
