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
			queryOptions = outboundDialer.(dialer.ResolveDialer).QueryOptions()
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
	// # How the ceiling is meant to reach the wire, and where it stops today
	//
	// The ceiling reaches quic-go as `InitialPacketSize`. On its own that is not enough: the pinned
	// quic-go replaces the field with `chromeInitialPacketSize` (1250) whenever `ChromeParrot` is set,
	// and ChromeParrot is hysteria2's DEFAULT. So the value is paired with `DisablePathMTUDiscovery`,
	// which is the signal for "this size is a ceiling, not a preference" - see the long note at the
	// `QUICOptions` literal below for the full reasoning and the cost.
	//
	// **That pairing is inert until the pinned library is replaced.** The library change is written and
	// tested but not pushed, so a known 1232 ceiling still emits 1250 today. Stated here rather than left
	// to be discovered: a reader who measures this tree and finds 1250 is seeing the documented state,
	// not a bug in this file.
	//
	// Note also that `TestPathCeilingReachesTheWireOnlyWithoutChromeParrot` in this package currently
	// asserts the 1250 for the ChromeParrot arm. That assertion pins the OLD behaviour deliberately and
	// will go red when the pin moves - it must be split into ceiling-present and ceiling-absent arms at
	// that point, not weakened.
	pathCapacity := dialer.DetourPathCapacity(ctx, options.DialerOptions.Detour)
	quicPayloadCeiling, hasCeiling := pathCapacity.QuicPayloadCeiling()
	effectiveInitialPacketSize := dialer.ClampToCeiling(options.InitialPacketSize, quicPayloadCeiling, hasCeiling)
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
	if hasCeiling && ceilingSuppliesTheSize(options.InitialPacketSize, effectiveInitialPacketSize, quicPayloadCeiling) {
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
			IdleTimeout:             options.IdleTimeout.Build(),
			KeepAlivePeriod:         options.KeepAlivePeriod.Build(),
			StreamReceiveWindow:     options.StreamReceiveWindow.Value(),
			ConnectionReceiveWindow: options.ConnectionReceiveWindow.Value(),
			MaxConcurrentStreams:    options.MaxConcurrentStreams,
			InitialPacketSize:       effectiveInitialPacketSize,
			// `DisablePathMTUDiscovery` carries TWO facts here, and neither reaches the wire until the
			// pinned quic-go is changed. Read this whole note before touching either.
			//
			// # 1. It is a no-op against the library this tree currently pins
			//
			// The pinned fork's ChromeParrot branch overwrites the size unconditionally:
			//
			//	initialPacketSize = chromeInitialPacketSize   // 1250
			//
			// and it never reads this flag inside that branch - the flag is only copied to the result
			// afterwards, at config.go:145. So setting it here changes NOTHING today: an outbound with a
			// known 1232 ceiling still emits 1250. This is deliberate and sequenced rather than an
			// oversight: the value has to be computed and handed down BEFORE the library can honour it,
			// and the library change is written and tested but not yet pushed. The moment the pin moves,
			// this line starts mattering - which is why the pairing is written now, with the reasoning,
			// instead of being added in the same commit as the pin bump and reviewed in a hurry.
			//
			// # 2. Once the pin moves it carries the ceiling, and must not carry the user's flag
			//
			// The two are the same number on the wire, so the library needs a separate signal to tell a
			// size CEILING from a size PREFERENCE. With no ceiling, ChromeParrot must keep its 1250-byte
			// fingerprint even when the user asked for something smaller; that contract predates this
			// code and is covered by tests.
			//
			// So what is forwarded is whether a ceiling is in force - a fact this layer establishes and
			// the library cannot - and NOT the operator's own `disable_path_mtu_discovery`. Forwarding
			// theirs would make a direct-path user's preference read as a constraint and cost them the
			// fingerprint, for no reason. Consequences, stated rather than hidden:
			//
			//   * no ceiling  -> false, so PMTUD stays enabled and the fingerprint is untouched.
			//   * a ceiling   -> true, so PMTUD is off. That is also the correct policy here, and it is
			//                    load-bearing beyond the first flight: with PMTUD active the connection
			//                    GROWS its packet size after the handshake (measured: 1232 -> 1438 over a
			//                    real loopback path), which would exceed a proven 1232 ceiling by more
			//                    than the first flight ever did. Pinning it is half the fix, not a detail.
			//   * the operator's own `disable_path_mtu_discovery` no longer reaches quic-go on this path.
			//     A real behaviour change for a direct-path user who set it, and the price of not
			//     conflating the two meanings.
			DisablePathMTUDiscovery: hasCeiling,
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
		Adapter: outbound.NewAdapterWithDialerOptions(C.TypeHysteria2, tag, networkList, options.DialerOptions),
		logger:  logger,
		client:  client,
	}, nil
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

// ceilingSuppliesTheSize reports whether a proven ceiling PROVIDED the effective size rather than capping
// a configured one.
//
// It is a named function rather than an inline expression so the distinction can be tested against the
// real code path: a test that retypes an inline condition does not fail when the original drifts, which
// was demonstrated by mutation before this was extracted.
//
// The order at the call site matters and is why this is not folded into that `else if`: both cases end at
// the SAME number, so the clamp test (`effective != configured`) is also true whenever the ceiling
// supplied the value. Testing it first would classify every supply as a clamp, and nothing would fail.
func ceilingSuppliesTheSize(configured, effective int, ceiling uint32) bool {
	return configured <= 0 && effective == int(ceiling)
}
