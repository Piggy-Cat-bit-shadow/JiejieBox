package dialer

import (
	"context"
	"net"
	"net/netip"
	"time"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/experimental/deprecated"
	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
)

type Options struct {
	Context                 context.Context
	Options                 option.DialerOptions
	RemoteIsDomain          bool
	DirectResolver          bool
	ResolverOnDetour        bool
	NewDialer               bool
	DisableEmptyDirectCheck bool
	DirectOutbound          bool
	DefaultOutbound         bool
	// PreferIPv4ForServer asks the resolver for this dialer's SERVER address to put IPv4 first.
	//
	// It exists for a proxy whose server name resolves to both families on a host whose ACTIVE
	// network is IPv4: the IPv6 candidate can belong to a secondary interface (a cellular
	// `pdp_ip0` beside Wi-Fi), so it is reachable and wins a Happy-Eyeballs race, but its bulk
	// throughput is a fraction of the active path's. Field log: a Mac on IPv4 Wi-Fi with a
	// cellular IPv6 route selected the proxy server over IPv6; short TLS requests still looked
	// fine, while YouTube video (bulk TCP and UoT/QUIC through the same AnyTLS session) stalled.
	//
	// It is deliberately not a global domain strategy and not applied to target names: only the
	// connection to the proxy server is affected, and an explicit resolver/legacy strategy still
	// wins below.
	PreferIPv4ForServer bool
}

// TODO: merge with NewWithOptions
func New(ctx context.Context, options option.DialerOptions, remoteIsDomain bool) (N.Dialer, error) {
	return NewWithOptions(Options{
		Context:        ctx,
		Options:        options,
		RemoteIsDomain: remoteIsDomain,
	})
}

func NewWithOptions(options Options) (N.Dialer, error) {
	dialOptions := options.Options
	var (
		dialer N.Dialer
		err    error
	)
	hasDetour := dialOptions.Detour != "" || options.DefaultOutbound
	if dialOptions.Detour != "" {
		outboundManager := service.FromContext[adapter.OutboundManager](options.Context)
		if outboundManager == nil {
			return nil, E.New("missing outbound manager")
		}
		dialer = NewDetour(outboundManager, dialOptions.Detour, options.DisableEmptyDirectCheck)
	} else if options.DefaultOutbound {
		outboundManager := service.FromContext[adapter.OutboundManager](options.Context)
		if outboundManager == nil {
			return nil, E.New("missing outbound manager")
		}
		dialer = NewDefaultOutboundDetour(outboundManager)
	} else {
		dialer, err = NewDefault(options.Context, dialOptions)
		if err != nil {
			return nil, err
		}
	}
	if options.RemoteIsDomain && (!hasDetour || options.ResolverOnDetour || dialOptions.DomainResolver != nil && dialOptions.DomainResolver.Server != "") {
		var (
			server          string
			dnsQueryOptions adapter.DNSQueryOptions
		)
		hasDomainResolver := dialOptions.DomainResolver != nil && dialOptions.DomainResolver.Server != ""
		if options.DirectResolver {
			if !hasDomainResolver {
				return nil, E.New("missing domain resolver for domain server address")
			}
			dnsQueryOptions = domainResolveQueryOptions(dialOptions.DomainResolver)
		} else {
			dnsQueryOptions, err = NewDNSQueryOptions(options.Context, dialOptions.DomainResolver, options.NewDialer)
			if err != nil {
				return nil, err
			}
		}
		if hasDomainResolver {
			server = dialOptions.DomainResolver.Server
		}
		// The server preference is applied only when no resolver policy has chosen a family.
		// An explicit `domain_resolver.strategy` or the legacy `domain_strategy` below remains
		// authoritative; this fills the default that would otherwise be AsIS.
		if options.PreferIPv4ForServer {
			dnsQueryOptions = preferIPv4ForServer(dnsQueryOptions)
		}
		if
		//nolint:staticcheck
		dialOptions.DomainStrategy != option.DomainStrategy(C.DomainStrategyAsIS) && (!hasDomainResolver || dialOptions.DomainResolver.Strategy == option.DomainStrategy(C.DomainStrategyAsIS)) {
			//nolint:staticcheck
			dnsQueryOptions.Strategy = C.DomainStrategy(dialOptions.DomainStrategy)
			deprecated.Report(options.Context, deprecated.OptionLegacyDomainStrategyOptions)
		}
		dialer = NewResolveDialer(
			options.Context,
			dialer,
			dialOptions.Detour == "" && !dialOptions.TCPFastOpen,
			server,
			dnsQueryOptions,
			time.Duration(dialOptions.FallbackDelay),
		)
	}
	return dialer, nil
}

// preferIPv4ForServer fills the family policy for a proxy server name only when nothing else has
// chosen one. An explicit resolver policy (or the legacy domain_strategy, applied after this call)
// is authoritative; this changes the DEFAULT so a dual-stack server name is not decided by the
// ancillary interface's IPv6 route.
func preferIPv4ForServer(options adapter.DNSQueryOptions) adapter.DNSQueryOptions {
	if options.Strategy == C.DomainStrategyAsIS {
		options.Strategy = C.DomainStrategyPreferIPv4
	}
	return options
}

// ResolverAuthority names WHICH source of DNS authority a name resolved by this outbound would
// actually use. It is the single semantic source of truth for that question: the dial path
// (NewDNSQueryOptions, below) and the static validator (common/physicalpath) both ask it, instead of
// each deriving the answer from the transport manager's state.
//
// # Why this exists
//
// The validator used to model the precedence independently and knew only two of these four cases, so
// it refused a configuration whose runtime path resolves names successfully. The two answers were
// different questions wearing the same name. The precedence is stated once, here, and both callers
// compare against the same enum.
//
// # Why `AuthorityNone` is not the same as "the lookup fails"
//
// `AuthorityNone` means no resolver is configured anywhere AND the transport manager holds no usable
// default. That is the only case where a named destination provably cannot be resolved. When the
// manager holds any transport at all, resolution is possible, and reporting it as impossible is
// exactly the false deny this enum removes.
type ResolverAuthority uint8

const (
	// AuthorityNone means nothing in this configuration can resolve a name. It is the ONLY case a
	// validator may refuse a configuration for.
	AuthorityNone ResolverAuthority = iota
	// AuthorityExplicit means the outbound declared `domain_resolver` and that transport exists.
	AuthorityExplicit
	// AuthorityNetworkDefault means `route.default_domain_resolver` names a transport that exists.
	AuthorityNetworkDefault
	// AuthorityTransportDefault means neither was declared, and the DNS transport manager holds a
	// transport of its own to answer with. `NewDNSQueryOptions` hands back an EMPTY policy here and
	// lets the router resolve through its own default path, which is deliberate: a target resolution
	// must not fail construction just because no resolver was named (dialer.go's TargetQueryOptions
	// doc states this). It is a WORKING path, not a missing one.
	AuthorityTransportDefault
)

// ResolveAuthority reports which source of DNS authority this outbound's name resolutions would use.
//
// It is a pure read: it constructs nothing and dials nothing. `newDialer` selects between the two
// callers' meanings of the no-resolver case, exactly as NewDNSQueryOptions documents.
func ResolveAuthority(ctx context.Context, domainResolver *option.DomainResolveOptions, newDialer bool) (ResolverAuthority, error) {
	// The transport manager may be ABSENT. `service.FromContext` answers nil for a context that has
	// fewer services registered than a running box - a test double, an embedder that drives a dialer
	// directly - and the first version dereferenced it in the named-resolver branch, which PANICKED
	// where every other branch of this function reports honestly.
	//
	// The nil case is reported as "no authority" rather than as an error, matching the network-manager
	// branch below: with no transport manager there is nothing to resolve with, and that is a fact
	// about the context rather than a misconfiguration to complain about.
	dnsTransport := service.FromContext[adapter.DNSTransportManager](ctx)
	if dnsTransport == nil {
		if domainResolver != nil && domainResolver.Server != "" && newDialer {
			return AuthorityNone, E.New("domain resolver not found: ", domainResolver.Server)
		}
		return AuthorityNone, nil
	}
	if domainResolver != nil && domainResolver.Server != "" {
		if _, loaded := dnsTransport.Transport(domainResolver.Server); !loaded {
			return AuthorityNone, E.New("domain resolver not found: ", domainResolver.Server)
		}
		return AuthorityExplicit, nil
	}
	networkManager := service.FromContext[adapter.NetworkManager](ctx)
	if networkManager == nil {
		// No network manager means no default resolver is configured anywhere. Reporting that
		// honestly beats dereferencing a nil service: construction must not panic on a context that
		// simply has fewer services registered than a running box.
		return AuthorityNone, nil
	}
	defaultOptions := networkManager.DefaultOptions()
	if defaultOptions.DomainResolver != "" {
		if _, loaded := dnsTransport.Transport(defaultOptions.DomainResolver); !loaded {
			return AuthorityNone, E.New("default domain resolver not found: ", defaultOptions.DomainResolver)
		}
		return AuthorityNetworkDefault, nil
	}
	if len(dnsTransport.Transports()) < 2 {
		// The manager holds a transport of its own, so a name CAN be resolved.
		return AuthorityTransportDefault, nil
	}
	if newDialer {
		return AuthorityNone, E.New("missing domain resolver for domain server address")
	}
	deprecated.Report(ctx, deprecated.OptionMissingDomainResolver)
	return AuthorityNone, nil
}

func NewDNSQueryOptions(ctx context.Context, domainResolver *option.DomainResolveOptions, newDialer bool) (adapter.DNSQueryOptions, error) {
	authority, err := ResolveAuthority(ctx, domainResolver, newDialer)
	if err != nil {
		return adapter.DNSQueryOptions{}, err
	}
	dnsTransport := service.FromContext[adapter.DNSTransportManager](ctx)
	if dnsTransport == nil {
		// Unreachable through ResolveAuthority - it only reports Explicit or NetworkDefault when it
		// found the transport in this same manager - but stated so the invariant is structural rather
		// than incidental. The empty policy is what AuthorityNone returns, and the router resolves
		// through its own default path from there, which is the documented behaviour rather than a
		// degraded one.
		return adapter.DNSQueryOptions{}, nil
	}
	switch authority {
	case AuthorityExplicit:
		dnsQueryOptions := domainResolveQueryOptions(domainResolver)
		dnsQueryOptions.Transport, _ = dnsTransport.Transport(domainResolver.Server)
		return dnsQueryOptions, nil
	case AuthorityNetworkDefault:
		networkManager := service.FromContext[adapter.NetworkManager](ctx)
		defaultOptions := networkManager.DefaultOptions()
		dnsQueryOptions := defaultOptions.DomainResolveOptions
		dnsQueryOptions.Transport, _ = dnsTransport.Transport(defaultOptions.DomainResolver)
		return dnsQueryOptions, nil
	case AuthorityTransportDefault:
		return adapter.DNSQueryOptions{Transport: dnsTransport.Default()}, nil
	default:
		// AuthorityNone. The policy is empty in both of its sub-cases; the difference is only
		// whether the caller was told it is fatal, and ResolveAuthority has already decided that.
		return adapter.DNSQueryOptions{}, nil
	}
}

func domainResolveQueryOptions(domainResolver *option.DomainResolveOptions) adapter.DNSQueryOptions {
	return adapter.DNSQueryOptions{
		Strategy:               C.DomainStrategy(domainResolver.Strategy),
		Timeout:                time.Duration(domainResolver.Timeout),
		DisableCache:           domainResolver.DisableCache,
		DisableOptimisticCache: domainResolver.DisableOptimisticCache,
		RewriteTTL:             domainResolver.RewriteTTL,
		ClientSubnet:           domainResolver.ClientSubnet.Build(netip.Prefix{}),
	}
}

type ParallelInterfaceDialer interface {
	N.Dialer
	DialParallelInterface(ctx context.Context, network string, destination M.Socksaddr, strategy *C.NetworkStrategy, interfaceType []C.InterfaceType, fallbackInterfaceType []C.InterfaceType, fallbackDelay time.Duration) (net.Conn, error)
	ListenSerialInterfacePacket(ctx context.Context, destination M.Socksaddr, strategy *C.NetworkStrategy, interfaceType []C.InterfaceType, fallbackInterfaceType []C.InterfaceType, fallbackDelay time.Duration) (net.PacketConn, error)
}

type ParallelNetworkDialer interface {
	DialParallelNetwork(ctx context.Context, network string, destination M.Socksaddr, destinationAddresses []netip.Addr, strategy *C.NetworkStrategy, interfaceType []C.InterfaceType, fallbackInterfaceType []C.InterfaceType, fallbackDelay time.Duration) (net.Conn, error)
	ListenSerialNetworkPacket(ctx context.Context, destination M.Socksaddr, destinationAddresses []netip.Addr, strategy *C.NetworkStrategy, interfaceType []C.InterfaceType, fallbackInterfaceType []C.InterfaceType, fallbackDelay time.Duration) (net.PacketConn, netip.Addr, error)
}

type PacketDialerWithDestination interface {
	ListenPacketWithDestination(ctx context.Context, destination M.Socksaddr) (net.PacketConn, netip.Addr, error)
}

// TargetQueryOptions derives the DNS query policy for a name this outbound resolves ITSELF, rather
// than one the dialer resolves in order to reach the proxy server.
//
// # Why this is separate from the dialer's own resolve policy
//
// NewWithOptions only builds a ResolveDialer when RemoteIsDomain is true, i.e. when the PROXY
// SERVER's address is a hostname. An outbound that must also resolve a TARGET name -- SOCKS4 is the
// case that exists today, because its request format carries only a 4-byte IPv4 address -- would
// get nothing from that dialer when the server happens to be an IP literal.
//
// Deriving the target policy from that would make the target's DNS authority depend on an unrelated
// fact about how the server is addressed. Both names come from the same DialerOptions, so both must
// be derived from it, and the derivation is shared rather than duplicated.
//
// # Why the last "no transports" branch differs from the dialer's
//
// NewDNSQueryOptions treats a missing domain resolver as fatal when it is building a dialer for a
// DOMAIN SERVER (newDialer=true): the connection cannot be made at all without it. A target
// resolution is not in that position -- the outbound still works for IP targets, and the router
// falls back to its own default path -- so this reports the empty policy rather than failing
// construction. Turning a working outbound into a construction error would be a behaviour change
// beyond the bug being fixed.
func TargetQueryOptions(ctx context.Context, options option.DialerOptions) (adapter.DNSQueryOptions, error) {
	hasDomainResolver := options.DomainResolver != nil && options.DomainResolver.Server != ""

	dnsQueryOptions, err := NewDNSQueryOptions(ctx, options.DomainResolver, false)
	if err != nil {
		return adapter.DNSQueryOptions{}, err
	}

	// The legacy domain_strategy applies only when the operator did not set a strategy on the
	// resolver itself. This mirrors NewWithOptions exactly, so both derivations agree.
	if
	//nolint:staticcheck
	options.DomainStrategy != option.DomainStrategy(C.DomainStrategyAsIS) &&
		(!hasDomainResolver ||
			options.DomainResolver.Strategy == option.DomainStrategy(C.DomainStrategyAsIS)) {
		//nolint:staticcheck
		dnsQueryOptions.Strategy = C.DomainStrategy(options.DomainStrategy)
		deprecated.Report(ctx, deprecated.OptionLegacyDomainStrategyOptions)
	}
	return dnsQueryOptions, nil
}
