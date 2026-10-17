package hysteria2

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/dialer"
	"github.com/sagernet/sing-box/common/tls"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	qtls "github.com/sagernet/sing-quic"
	"github.com/sagernet/sing-quic/hysteria"
	"github.com/sagernet/sing-quic/hysteria2"
	"github.com/sagernet/sing-quic/hysteria2/realm"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/bufio"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
)

func RegisterOutbound(registry *outbound.Registry) {
	outbound.Register[option.Hysteria2OutboundOptions](registry, C.TypeHysteria2, NewOutbound)
}

var (
	_ adapter.Outbound                = (*Outbound)(nil)
	_ adapter.InterfaceUpdateListener = (*Outbound)(nil)
	_ adapter.IdleConnectionKeeper    = (*Outbound)(nil)
	_ adapter.OutboundWithMultiplex   = (*Outbound)(nil)
)

type Outbound struct {
	outbound.Adapter
	logger logger.ContextLogger
	client *hysteria2.Client
}

func NewOutbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.Hysteria2OutboundOptions) (adapter.Outbound, error) {
	if options.TLS == nil || !options.TLS.Enabled {
		return nil, C.ErrTLSRequired
	}
	tlsServerAddress, tlsOptions, err := outboundTLSOptions(options)
	if err != nil {
		return nil, err
	}
	tlsConfig, err := tls.NewClient(ctx, logger, tlsServerAddress, tlsOptions)
	if err != nil {
		return nil, err
	}
	var salamanderPassword string
	var geckoPassword string
	var geckoMinPacketSize, geckoMaxPacketSize int
	if options.Obfs != nil {
		if options.Obfs.Password == "" {
			return nil, E.New("missing obfs password")
		}
		switch options.Obfs.Type {
		case hysteria2.ObfsTypeSalamander:
			salamanderPassword = options.Obfs.Password
		case hysteria2.ObfsTypeGecko:
			geckoPassword = options.Obfs.Password
			geckoMinPacketSize = options.Obfs.GeckoOptions.MinPacketSize
			geckoMaxPacketSize = options.Obfs.GeckoOptions.MaxPacketSize
		default:
			return nil, E.New("unknown obfs type: ", options.Obfs.Type)
		}
	}
	realmSTUNServersIsDomain := options.Realm != nil && options.Realm.STUNServersIsDomain()
	outboundDialer, err := dialer.NewWithOptions(dialer.Options{
		Context:          ctx,
		Options:          options.DialerOptions,
		RemoteIsDomain:   options.ServerIsDomain() || realmSTUNServersIsDomain,
		ResolverOnDetour: realmSTUNServersIsDomain,
		NewDialer:        realmSTUNServersIsDomain,
	})
	if err != nil {
		return nil, err
	}
	var realmOptions *realm.Options
	if options.Realm != nil {
		var queryOptions adapter.DNSQueryOptions
		if realmSTUNServersIsDomain {
			// Checked, not assumed: `NewDialer` and `ResolverOnDetour` are set only when a realm STUN server
			// is a domain, which is the same condition this branch tests - but that coupling lives in this
			// file's option literals, not in the type system, so an unchecked assertion would turn a future
			// divergence into a panic during configuration load. Same defect class as the direct outbound's.
			resolveDialerForQueryOptions, isResolveDialer := outboundDialer.(dialer.ResolveDialer)
			if !isResolveDialer {
				return nil, E.New("hysteria2 outbound: the dialer chain did not produce a resolve dialer, which this outbound requires for its realm STUN servers; this is a defect in the dialer factory rather than in the configuration")
			}
			queryOptions = resolveDialerForQueryOptions.QueryOptions()
		}
		var httpClientTransport adapter.HTTPTransport
		httpClientTransport, err = service.FromContext[adapter.HTTPClientManager](ctx).ResolveTransport(ctx, logger, common.PtrValueOrDefault(options.Realm.HTTPClient))
		if err != nil {
			return nil, E.Cause(err, "create realm http client")
		}
		dnsRouter := service.FromContext[adapter.DNSRouter](ctx)
		realmOptions = &realm.Options{
			ServerURL:   options.Realm.ServerURL,
			Token:       options.Realm.Token,
			RealmID:     options.Realm.RealmID,
			STUNServers: options.Realm.STUNServers,
			HTTPClient:  &http.Client{Transport: httpClientTransport},
			Resolver: func(ctx context.Context, host string, ipv4, ipv6 bool) ([]netip.Addr, error) {
				dnsOptions := queryOptions
				switch {
				case ipv4 && !ipv6:
					dnsOptions.Strategy = C.DomainStrategyIPv4Only
				case !ipv4 && ipv6:
					dnsOptions.Strategy = C.DomainStrategyIPv6Only
				}
				return dnsRouter.Lookup(ctx, host, dnsOptions)
			},
			Logger:    logger,
			IPVersion: options.Realm.IPVersion,
		}
		if options.Realm.PortMapping != nil && options.Realm.PortMapping.Enabled {
			realmOptions.PortMapping = &realm.PortMappingOptions{
				Timeout:  time.Duration(options.Realm.PortMapping.Timeout),
				Lifetime: time.Duration(options.Realm.PortMapping.Lifetime),
			}
		}
	}
	networkList := options.Network.Build()
	// A proven lower tunnel caps what this QUIC connection may put on the wire.
	//
	// # Where the ceiling comes from, and where it stops
	//
	// `dialer.DetourPathCapacity` resolves the endpoint this outbound's `detour` names, when that
	// endpoint publishes a FIXED inner IP capacity (MASQUE and WireGuard do; a group does not, and is
	// reported unknown). The QUIC payload ceiling is that capacity minus this protocol's own IPv6/IPv4
	// overhead, computed once in common/dialer so HY2 and TUIC cannot disagree about the arithmetic.
	//
	// An unknown path yields no ceiling and the configured value is used unchanged, so a direct dial
	// keeps exactly its previous behaviour.
	//
	// # The two sizes are two different statements, and travel as two fields
	//
	// `InitialPacketSize` is a PREFERENCE: a size this outbound would like, computed from the operator's
	// `initial_packet_size` and the ceiling. quic-go may legitimately replace it - the ChromeParrot
	// branch pins `chromeInitialPacketSize` (1250) whenever the fingerprint is on, and the fingerprint
	// is hysteria2's default.
	//
	// `InitialPacketSizeCeiling` is a CONSTRAINT: the largest packet this path can carry. It is applied
	// by quic-go AFTER the fingerprint override, so it cannot be discarded, and it also caps what path
	// MTU discovery may grow to.
	//
	// An earlier revision tried to carry the constraint through `DisablePathMTUDiscovery`, on the theory
	// that "a size plus PMTUD off" means "ceiling". It does not: a direct-path operator who sets
	// `disable_path_mtu_discovery` for their own reasons produces the same pair of values, and their
	// preference was then read as a proven capacity - and, because this layer forwarded only its own
	// conclusion, their setting was also swallowed. Both are fixed by giving each meaning its own field.
	pathCapacity := dialer.DetourPathCapacity(ctx, options.DialerOptions.Detour)
	quicPayloadCeiling, hasCeiling := pathCapacity.QuicPayloadCeiling()
	effectiveInitialPacketSize := dialer.ClampToCeiling(options.InitialPacketSize, quicPayloadCeiling, hasCeiling)
	sizing := quicPathSizing(options.DisablePathMTUDiscovery, quicPayloadCeiling, hasCeiling)
	// The two cases below are the two ways ClampToCeiling can move a value, and they are NOT the same
	// event, so they must not produce the same sentence.
	//
	//	dialer.ClampToCeiling's own semantics: "a configured value of zero, with a ceiling: the ceiling
	//	is used. Zero means 'no preference', and the ceiling IS the preference."
	//
	// So with nothing configured the ceiling SUPPLIES the value rather than capping anything, and saying
	// it "caps initial_packet_size 0" describes an override of an operator's choice that was never made.
	// An operator reading the old line - on a configuration that sets no initial_packet_size at all -
	// would go looking for what had set it to 0.
	if hasCeiling && dialer.CeilingSuppliesTheSize(options.InitialPacketSize, effectiveInitialPacketSize, quicPayloadCeiling) {
		logger.Info("path capacity: inner MTU ", pathCapacity.InnerMTU,
			" proves ", quicPayloadCeiling,
			" bytes of QUIC payload, which is used because no initial_packet_size was configured")
	} else if hasCeiling && effectiveInitialPacketSize != options.InitialPacketSize {
		logger.Info("path capacity: inner MTU ", pathCapacity.InnerMTU,
			" caps the QUIC payload at ", quicPayloadCeiling,
			", so initial_packet_size ",
			options.InitialPacketSize, " becomes ", effectiveInitialPacketSize)
	}
	// A ceiling below the QUIC minimum is a configuration-level impossibility, not a value to clamp:
	// an Initial packet cannot be smaller than 1200, so there is no safe number to send.
	if hasCeiling && effectiveInitialPacketSize < dialer.MinimumQUICInitialPacketSize {
		return nil, E.New("hysteria2: the lower tunnel (inner MTU ", pathCapacity.InnerMTU,
			", ", pathCapacity.Family.String(),
			") leaves ", quicPayloadCeiling, " bytes of UDP payload, which cannot carry a QUIC Initial ",
			"packet (minimum ", dialer.MinimumQUICInitialPacketSize,
			"). This path cannot carry a standard QUIC handshake; configure a larger tunnel MTU or ",
			"reach this server without this detour")
	}
	client, err := hysteria2.NewClient(hysteria2.ClientOptions{
		Context:            ctx,
		Dialer:             outboundDialer,
		Logger:             logger,
		BrutalDebug:        options.BrutalDebug,
		ServerAddress:      options.ServerOptions.Build(),
		ServerPorts:        options.ServerPorts,
		HopInterval:        time.Duration(options.HopInterval),
		HopIntervalMax:     time.Duration(options.HopIntervalMax),
		SendBPS:            uint64(options.UpMbps * hysteria.MbpsToBps),
		ReceiveBPS:         uint64(options.DownMbps * hysteria.MbpsToBps),
		SalamanderPassword: salamanderPassword,
		GeckoPassword:      geckoPassword,
		GeckoMinPacketSize: geckoMinPacketSize,
		GeckoMaxPacketSize: geckoMaxPacketSize,
		Password:           options.Password,
		TLSConfig:          tlsConfig,
		QUICOptions: qtls.QUICOptions{
			IdleTimeout:              options.IdleTimeout.Build(),
			KeepAlivePeriod:          options.KeepAlivePeriod.Build(),
			StreamReceiveWindow:      options.StreamReceiveWindow.Value(),
			ConnectionReceiveWindow:  options.ConnectionReceiveWindow.Value(),
			MaxConcurrentStreams:     options.MaxConcurrentStreams,
			InitialPacketSize:        effectiveInitialPacketSize,
			InitialPacketSizeCeiling: sizing.HardCeiling,
			DisablePathMTUDiscovery:  sizing.DisablePMTUD,
		},
		UDPDisabled:  !common.Contains(networkList, N.NetworkUDP),
		BBRProfile:   options.BBRProfile,
		ChromeParrot: !options.DisableChromeParrot,
		RealmOptions: realmOptions,
	})
	if err != nil {
		return nil, err
	}
	return &Outbound{
		Adapter: outbound.NewAdapterWithDialerOptions(C.TypeHysteria2, tag, networkList, options.DialerOptions, options.ServerIsDomain()),
		logger:  logger,
		client:  client,
	}, nil
}

// quicPathSizing is the one place that decides the two sizing facts a connection is given, kept
// apart from the client construction so both can be asserted rather than inspected.
//
// The two fields it fills are independent statements, and this function is where that has to stay
// true:
//
//	HardCeiling    - the largest packet the path is known to carry. Zero (quic-go's "no ceiling")
//	                 for an unknown path, and never derived from the operator's PMTUD policy: a
//	                 preference is not a measurement.
//	DisablePMTUD   - true when the operator asked for it, or when a ceiling makes growth unsafe.
//	                 Either reason is sufficient; neither is guessed at.
//
// # Why a proven ceiling turns PMTUD off
//
// Not for the first flight - the ceiling is applied to that by quic-go regardless. It is because
// path MTU discovery GROWS the packet size after the handshake (measured: 1232 -> 1438 on a real
// loopback path), and a connection that has been told the path carries 1232 has no business probing
// for more. quic-go caps that growth at the ceiling as well, so the ceiling holds either way, but
// leaving PMTUD on would mean probing a path whose capacity is already known.
//
// # Why the operator's own setting is forwarded rather than replaced
//
// An earlier revision forwarded only its own conclusion - `DisablePathMTUDiscovery: hasCeiling` -
// which silently swallowed the operator's flag on every path with no ceiling, and made a
// direct-path operator's `disable_path_mtu_discovery` indistinguishable from a proven capacity.
// Both directions of that are defects, and this signature is what stops them coming back: there is
// no way to express the old behaviour without deleting a parameter.
func quicPathSizing(userDisablePMTUD bool, ceiling uint32, hasCeiling bool) struct {
	HardCeiling  int
	DisablePMTUD bool
} {
	var sizing struct {
		HardCeiling  int
		DisablePMTUD bool
	}
	if hasCeiling {
		sizing.HardCeiling = int(ceiling)
	}
	sizing.DisablePMTUD = userDisablePMTUD || hasCeiling
	return sizing
}

func outboundTLSOptions(options option.Hysteria2OutboundOptions) (string, option.OutboundTLSOptions, error) {
	tlsOptions := common.PtrValueOrDefault(options.TLS)
	if options.Realm == nil {
		return options.Server, tlsOptions, nil
	}
	if options.Server != "" || options.ServerPort != 0 || len(options.ServerPorts) > 0 {
		return "", tlsOptions, E.New("realm conflicts with server, server_port, and server_ports")
	}
	serverURL, err := url.Parse(options.Realm.ServerURL)
	if err != nil {
		return "", tlsOptions, E.Cause(err, "parse realm server_url")
	}
	serverName := serverURL.Hostname()
	if serverName == "" {
		return "", tlsOptions, E.New("missing host in realm server_url")
	}
	return serverName, tlsOptions, nil
}

func (h *Outbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	switch N.NetworkName(network) {
	case N.NetworkTCP:
		h.logger.InfoContext(ctx, "outbound connection to ", destination)
		return h.client.DialConn(ctx, destination)
	case N.NetworkUDP:
		conn, err := h.ListenPacket(ctx, destination)
		if err != nil {
			return nil, err
		}
		return bufio.NewBindPacketConn(conn, destination), nil
	default:
		return nil, E.New("unsupported network: ", network)
	}
}

func (h *Outbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	h.logger.InfoContext(ctx, "outbound packet connection to ", destination)
	return h.client.ListenPacket(ctx)
}

func (h *Outbound) InterfaceUpdated(ctx context.Context) {
	h.client.CloseWithError(E.New("network changed"))
}

func (h *Outbound) MultiplexEnabled() bool {
	return true
}

func (h *Outbound) SetKeepIdleConnections(keep bool) {
	h.client.SetKeepIdleConnections(keep)
}

func (h *Outbound) CloseIdleConnections() {
	h.client.CloseIdleConnections()
}

func (h *Outbound) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	if stage != adapter.StartStateInitialize {
		return nil
	}
	scope.Add(func() error {
		return h.client.CloseWithError(os.ErrClosed)
	})
	return nil
}
