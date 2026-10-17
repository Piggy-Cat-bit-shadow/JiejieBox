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
	// upstreamReporter is the hop protocol: `common.Cast` and the dialer chain walks follow it to reach the
	// capabilities that live below an outbound (the family verdict, the shared family health). Pinned
	// because losing it is silent - the chain would simply answer "this machine has no opinion".
	_ upstreamReporter = (*Outbound)(nil)
)

// upstreamReporter is the structural form of `Upstream() any`, kept local so the pin reads as a contract
// rather than as a mention of an anonymous interface.
type upstreamReporter interface {
	Upstream() any
}

type Outbound struct {
	outbound.Adapter
	ctx     context.Context
	logger  logger.ContextLogger
	network adapter.NetworkManager
	// surface is the capability set the dialer factory produced for this outbound, resolved ONCE here.
	//
	// It replaces a field declared at the per-attempt interface plus two separately-captured closures. The
	// declaration at the narrow type is what lost sniffed-domain recovery for a literal destination twice
	// running; a field that holds the resolved surface cannot, because the capability is read from the
	// object the factory produced rather than re-discovered at the point of use.
	surface        dialer.LocalDialSurface
	domainStrategy C.DomainStrategy
	fallbackDelay  time.Duration
	// semantics is the precomputed native-bypass profile of this outbound's dial options. It is
	// built once, here, from the same options the dialer was built from, and it is the only thing
	// that answers "would the userspace dial path do anything special for this flow?".
	semantics   dialer.SocketSemantics
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
	// The surface is resolved from the object the factory returned, and a chain that cannot schedule
	// candidates is refused HERE rather than degraded at the point of use. See dialer.LocalDialSurface:
	// the capability this guarantees is the one whose silent loss took two rounds to find.
	surface, err := dialer.NewLocalDialSurface(outboundDialer)
	if err != nil {
		return nil, E.Cause(err, "direct outbound")
	}
	outbound := &Outbound{
		Adapter: outbound.NewAdapterWithDialerOptions(C.TypeDirect, tag, []string{N.NetworkTCP, N.NetworkUDP, N.NetworkICMP}, options.DialerOptions, true),
		ctx:     ctx,
		logger:  logger,
		network: service.FromContext[adapter.NetworkManager](ctx),
		//nolint:staticcheck
		domainStrategy: C.DomainStrategy(options.DomainStrategy),
		fallbackDelay:  time.Duration(options.FallbackDelay),
		surface:        surface,
		semantics:      dialer.NativeBypassSemantics(options.DialerOptions),
	}
	//nolint:staticcheck
	if options.ProxyProtocol != 0 {
		return nil, E.New("Proxy Protocol is deprecated and removed in sing-box 1.6.0")
	}
	if defaultDialer, isDefaultDialer := common.Cast[*dialer.DefaultDialer](outbound.surface.Attempt); isDefaultDialer {
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

// Upstream returns the dialer chain this outbound dials through.
//
// # Why an outbound reports a dialer
//
// This type is a HOP, not a wrapper: it holds a resolved `LocalDialSurface` whose `Attempt` is the chain
// that creates this machine's sockets. The capabilities that live in that chain - the family verdict, the
// shared family health, the recovery candidate filtering - belong to the leaf, and they are reached
// through UNEXPORTED interfaces (`localFamilyOwner`, `familyHealthOwner`) that a type in another package
// cannot implement: Go's unexported-method rule forbids it.
//
// So it says where they are, on the protocol the resolve wrappers already implement and `common.Cast`
// already walks. A dialer built OVER this outbound - `daemon/started_service.go` builds exactly that for
// the network-quality and STUN probes - then reaches the real owner instead of stopping at the hop.
// Before this, that chain answered "this machine has no opinion about address families", which reads as
// "it can dial anything": the shared family health was lost and the recovery candidate filtering became a
// no-op, silently.
func (h *Outbound) Upstream() any {
	return h.surface.Attempt
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
	return h.surface.Attempt.DialContext(ctx, network, destination)
}

func (h *Outbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	if h.isMyLoopbackAddress(destination.Addr) {
		return nil, E.New("loopback connection to TUN range")
	}
	ctx, metadata := adapter.ExtendContext(ctx)
	metadata.Outbound = h.Tag()
	metadata.Destination = destination
	h.logger.InfoContext(ctx, "outbound packet connection to ", destination)
	conn, err := h.surface.Attempt.ListenPacket(ctx, destination)
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

// scheduleNetwork hands the candidate list to the candidate-planning surface this outbound's dialer has.
//
// # Why the surface is a FIELD and not a type assertion
//
// This method used to assert `ParallelNetworkDialer` on `h.dialer`, which was declared at the per-attempt
// interface because that is the narrowest surface an attempt needs. The object inside it was normally a
// *dialer.resolveParallelNetworkDialer, which implements the richer surface - and its DialParallelNetwork
// is the only place domain recovery for a literal destination lives: the sniffed-domain lookup, the
// late-candidate feed, the family fallback.
//
// Calling the package-level helper directly passed h.dialer in at the NARROW type, and
// DialParallelNetwork's body never asks whether the object it was handed could have discovered more - it
// plans the candidates it is given and schedules them. So the richer method was never invoked, and a TUN
// flow to a global IPv6 literal with a sniffed domain was refused by the local-family gate while the
// recovery machinery sat one call away with the name already in the context metadata. In the production
// log that is the WeChat flow with `sniffed protocol: http, domain: szminorshort.weixin.qq.com` followed
// by the IPv6 refusal and no `dns: lookup domain` line between them.
//
// Fixing the wrapper alone was not enough: `DialSerialNetwork` does reach
// resolveParallelNetworkDialer.DialParallelNetwork, but only when it is handed the wrapper. The
// connection manager hands it the OUTBOUND, so this method is the actual entry point and the capability
// has to survive here - which it now does by CONSTRUCTION: `NewOutbound` resolves the surface once and
// refuses a chain without this capability, so there is no narrower type for anything to fall back to.
//
// # Why the fallback that used to be here is gone
//
// It existed for a detour-shaped chain, and a direct outbound cannot have one: `detour` is refused
// outright above. Keeping it meant that if the factory ever stopped producing the planning surface, this
// method would silently take the narrower path again and every test would still pass - which is exactly how
// the defect survived two rounds. The refusal is now at construction, where an operator can read it.
//
// # Why this cannot recurse
//
// The wrapper schedules each attempt onto the object it was built over (`attemptDialer`), which is the
// leaf, never the outbound: `NewResolveDialer` constructs it over the output of the leaf factory. So
// turning here reaches the scheduler and then a real socket, which is the same graph
// resolveParallelNetworkDialer.attemptDialer documents.
func (h *Outbound) scheduleNetwork(ctx context.Context, network string, destination M.Socksaddr, destinationAddresses []netip.Addr, preferIPv6 bool, networkStrategy *C.NetworkStrategy, networkType []C.InterfaceType, fallbackNetworkType []C.InterfaceType, fallbackDelay time.Duration) (net.Conn, error) {
	return h.surface.Network.DialParallelNetwork(ctx, network, destination, destinationAddresses, networkStrategy, networkType, fallbackNetworkType, fallbackDelay)
}

func (h *Outbound) ListenSerialNetworkPacket(ctx context.Context, destination M.Socksaddr, destinationAddresses []netip.Addr, networkStrategy *C.NetworkStrategy, networkType []C.InterfaceType, fallbackNetworkType []C.InterfaceType, fallbackDelay time.Duration) (net.PacketConn, netip.Addr, error) {
	if h.isMyLoopbackAddress(destinationAddresses...) {
		return nil, netip.Addr{}, E.New("loopback connection to TUN range")
	}
	ctx, metadata := adapter.ExtendContext(ctx)
	metadata.Outbound = h.Tag()
	metadata.Destination = destination
	// The destination is part of this line, as it is on every other outbound's packet report
	// (`outbound/http[...]: outbound packet connection to host:port`) and on this outbound's own stream
	// and connected-packet lines. It was missing here and in ListenPacket, which is why the production
	// log contains sixteen `outbound/direct[direct]: outbound packet connection` lines with nothing to
	// identify the flow they belonged to - each followed by a refusal that names a machine property
	// rather than a destination. A packet connection is the case where the destination is most needed:
	// the refusal happens before a socket exists, so the log line is the only record of what was asked
	// for.
	h.logger.InfoContext(ctx, "outbound packet connection to ", destination)
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
// call again; here the capability is read from the surface resolved at construction, and a chain that
// lacks it never becomes an outbound at all.
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
// recovered addresses BEHIND the application's own - so the walk keeps "the literal is the endpoint
// the application chose" rule while giving it somewhere to go when the literal cannot be used.
func (h *Outbound) listenNetworkPacket(ctx context.Context, destination M.Socksaddr, destinationAddresses []netip.Addr, networkStrategy *C.NetworkStrategy, networkType []C.InterfaceType, fallbackNetworkType []C.InterfaceType, fallbackDelay time.Duration) (net.PacketConn, netip.Addr, error) {
	return h.surface.Network.ListenSerialNetworkPacket(ctx, destination, destinationAddresses, networkStrategy, networkType, fallbackNetworkType, fallbackDelay)
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
//
// # Why every term now comes from the resolved surface
//
// The family strategy and the local family verdict used to be two closures captured at construction, both
// read off the same chain by type assertion at a point where the chain happened to be in hand. That is the
// same discovery pattern this round removes: a closure is a second field that can be left nil by a future
// edit, and the shape it was captured from is invisible to the reader of this function. The surface holds
// them as named, resolvable capabilities, so "the bypass cannot ask what the dial path asks" is a
// construction-time impossibility rather than a review question.
func (h *Outbound) BypassBlockers(network string, destination netip.Addr) dialer.NativeBypassBlocker {
	facts := dialer.NativeBypassFacts{
		Network: network,
		// Always true here, and not by assumption: the router only consults a bypassable outbound
		// after establishing that the destination is a literal with no candidate list, no sniffed
		// domain and no rewrite. A flow that needs a name resolved never reaches this method.
		DestinationIsLiteral: true,
		Destination:          destination,
	}
	if h.surface.Resolve != nil {
		// The userspace path applies this policy to the LITERAL destination as well as to resolved
		// candidates, so the profile has to ask the same object the dial path asks. The previous version
		// inferred it from whether the outbound had configured a resolver, which is a different fact: the
		// effective policy can come from route.default_domain_resolver or dns.strategy, and inferring it
		// from the local configuration allowed a bypass for a family the dial path refused.
		facts.FamilyStrategy = h.surface.Resolve.EffectiveFamilyStrategy()
	}
	blockers := h.semantics.Blockers(facts)
	blockers |= dialer.NetworkPolicyBlockers(h.network)
	if h.surface.LocalFamily != nil && h.surface.LocalFamily.LocalFamilyBlocked(destination) {
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
