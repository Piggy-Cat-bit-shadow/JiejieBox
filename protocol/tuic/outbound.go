package tuic

import (
	"context"
	"net"
	"os"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/dialer"
	"github.com/sagernet/sing-box/common/runtimecoord"
	"github.com/sagernet/sing-box/common/tls"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	qtls "github.com/sagernet/sing-quic"
	"github.com/sagernet/sing-quic/tuic"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/bufio"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/common/uot"
	"github.com/sagernet/sing/service"

	"github.com/gofrs/uuid/v5"
)

func RegisterOutbound(registry *outbound.Registry) {
	outbound.Register[option.TUICOutboundOptions](registry, C.TypeTUIC, NewOutbound)
}

var (
	_ adapter.InterfaceUpdateListener = (*Outbound)(nil)
	_ adapter.IdleConnectionKeeper    = (*Outbound)(nil)
	_ adapter.OutboundWithMultiplex   = (*Outbound)(nil)
)

type Outbound struct {
	outbound.Adapter
	logger logger.ContextLogger
	// client is one or two family-restricted sing-quic clients (audit Q05). A single-family path
	// behaves exactly as before; a dual-stack domain races the two families on the first creation.
	client    *familyCarrier
	udpStream bool
	// generation closes the publish-to-listener window: a dial that arrives after the network
	// manager advanced the epoch but before the listener closed the session closes it here first,
	// so the demand cannot attach to a session built for the previous network.
	generation *runtimecoord.GenerationGate
}

func NewOutbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.TUICOutboundOptions) (adapter.Outbound, error) {
	if options.TLS == nil || !options.TLS.Enabled {
		return nil, C.ErrTLSRequired
	}
	tlsConfig, err := tls.NewClient(ctx, logger, options.Server, common.PtrValueOrDefault(options.TLS))
	if err != nil {
		return nil, err
	}
	userUUID, err := uuid.FromString(options.UUID)
	if err != nil {
		return nil, E.Cause(err, "invalid uuid")
	}
	var tuicUDPStream bool
	if options.UDPOverStream && options.UDPRelayMode != "" {
		return nil, E.New("udp_over_stream is conflict with udp_relay_mode")
	}
	// The empty value is the documented default, and it must be spelled out here: a value with a
	// typo used to fall through the switch and silently mean `native`, so a user who wrote
	// "qiuc" got a working relay in the wrong mode and no signal at all - while the neighbouring
	// `congestion_control` typo does produce an error. An unrecognised value is now a
	// configuration error.
	switch options.UDPRelayMode {
	case "", "native":
	case "quic":
		tuicUDPStream = true
	default:
		return nil, E.New("unknown udp_relay_mode: ", options.UDPRelayMode, " (expected native or quic)")
	}
	outboundDialer, err := dialer.New(ctx, options.DialerOptions, options.ServerIsDomain())
	if err != nil {
		return nil, err
	}
	// A dual-stack domain is the only shape where a second family can exist; a literal server has
	// one address, so it keeps the previous single-client behaviour exactly.
	var familyDialers [2]N.Dialer
	if options.ServerIsDomain() {
		for index, strategy := range []option.DomainStrategy{
			option.DomainStrategy(C.DomainStrategyIPv4Only),
			option.DomainStrategy(C.DomainStrategyIPv6Only),
		} {
			familyOptions := options.DialerOptions
			familyOptions.DomainStrategy = strategy
			familyDialer, familyErr := dialer.New(ctx, familyOptions, true)
			if familyErr != nil {
				logger.Warn("tuic: cannot build the ", strategy, " family dialer, single-family carrier kept: ", familyErr)
				familyDialers = [2]N.Dialer{}
				break
			}
			familyDialers[index] = familyDialer
		}
	}
	// A proven lower tunnel caps what this QUIC connection may put on the wire.
	//
	// # TUIC goes through the same path as hysteria2, and the lines that say so
	//
	// TUIC does NOT build its own quic.Config. The pinned sing-quic TUIC client does it, and it calls
	// the SAME applier hysteria2's does - in the module pinned by go.mod
	// (github.com/sagernet/sing-quic v0.7.2-0.20260929152029-258509488380):
	//
	//	quic.go:54-78     func ApplyQUICOptions(quicConfig *quic.Config, options QUICOptions) {
	//	quic.go:72-74         if options.InitialPacketSize > 0 {
	//	quic.go:73                quicConfig.InitialPacketSize = uint16(options.InitialPacketSize)
	//
	//	tuic/client.go:60-65  quicConfig := &quic.Config{
	//	                          DisablePathMTUDiscovery: !(GOOS == windows|linux|android|darwin),
	//	                          EnableDatagrams: true, MaxIncomingUniStreams: 1 << 60,
	//	                      }
	//	                      qtls.ApplyQUICOptions(quicConfig, options.QUICOptions)
	//	tuic/client.go:170/172 qtls.DialEarly / qtls.Dial(ctx, udpConn, c.tlsConfig, c.quicConfig)
	//
	// The field this outbound sets below (QUICOptions.InitialPacketSize) is the one read at
	// quic.go:72, so the ceiling reaches the same library field hysteria2's does.
	//
	// # And the difference that makes this clamp effective where hysteria2's is not
	//
	// Grep for ChromeParrot in this module: `tuic` never sets it, and nothing in sing-quic's TUIC path
	// can - quic-go's own guard says so, internal/handshake/tls_conn_utls.go:58
	// ("quic: tls.Config.VerifyConnection is not supported with ChromeParrot"). Only hysteria2's
	// outbound sets the field (protocol/hysteria2/outbound.go: `ChromeParrot: !options.DisableChromeParrot`),
	// and the pinned quic-go then REPLACES whatever the caller configured with
	// chromeInitialPacketSize = 1250 (quic-go config.go:109-121). TUIC never enters that branch, so for
	// TUIC the ceiling is what actually goes on the wire. MEASURED, not inferred:
	// `TestTheCeilingReachesTheTUICFirstDatagram` in this package reads len(p) of the first datagram the
	// real client config produces.
	//
	// An unknown path yields no ceiling and the configured value is used unchanged, so a direct dial
	// keeps exactly its previous behaviour.
	pathCapacity := dialer.DetourPathCapacity(ctx, options.DialerOptions.Detour)
	quicPayloadCeiling, hasCeiling := pathCapacity.QuicPayloadCeiling()
	effectiveInitialPacketSize := dialer.ClampToCeiling(options.InitialPacketSize, quicPayloadCeiling, hasCeiling)
	// The ceiling can move the size two ways and they are not the same event: with nothing configured it
	// SUPPLIES the value (ClampToCeiling's own doc: "Zero means 'no preference', and the ceiling IS the
	// preference"), and above the ceiling it CLAMPS one. Reporting a supply as a cap tells a reader to go
	// looking for whatever set initial_packet_size to 0, which is not in their configuration.
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
	// an Initial packet cannot be smaller than 1200 (RFC 9000 section 14.1), so there is no safe number
	// to send. The same refusal, with the same wording, is what hysteria2 already does - the two
	// protocols share the helper, so they must share the boundary too.
	if hasCeiling && effectiveInitialPacketSize < dialer.MinimumQUICInitialPacketSize {
		return nil, E.New("tuic: the lower tunnel (inner MTU ", pathCapacity.InnerMTU,
			", ", pathCapacity.Family.String(),
			") leaves ", quicPayloadCeiling, " bytes of UDP payload, which cannot carry a QUIC Initial ",
			"packet (minimum ", dialer.MinimumQUICInitialPacketSize,
			"). This path cannot carry a standard QUIC handshake; configure a larger tunnel MTU or ",
			"reach this server without this detour")
	}
	hardCeiling := 0
	disablePMTUD := options.DisablePathMTUDiscovery
	if hasCeiling {
		hardCeiling = int(quicPayloadCeiling)
		disablePMTUD = true
	}
	newClient := func(clientDialer N.Dialer) (*tuic.Client, error) {
		return tuic.NewClient(tuic.ClientOptions{
			Context:       ctx,
			Dialer:        clientDialer,
			ServerAddress: options.ServerOptions.Build(),
			TLSConfig:     tlsConfig,
			QUICOptions: qtls.QUICOptions{
				IdleTimeout:              options.IdleTimeout.Build(),
				KeepAlivePeriod:          options.KeepAlivePeriod.Build(),
				StreamReceiveWindow:      options.StreamReceiveWindow.Value(),
				ConnectionReceiveWindow:  options.ConnectionReceiveWindow.Value(),
				MaxConcurrentStreams:     options.MaxConcurrentStreams,
				InitialPacketSize:        effectiveInitialPacketSize,
				InitialPacketSizeCeiling: hardCeiling,
				DisablePathMTUDiscovery:  disablePMTUD,
			},
			UUID:              userUUID,
			Password:          options.Password,
			CongestionControl: options.CongestionControl,
			UDPStream:         tuicUDPStream,
			ZeroRTTHandshake:  options.ZeroRTTHandshake,
			Heartbeat:         time.Duration(options.Heartbeat),
		})
	}
	var carrier *familyCarrier
	if familyDialers[0] != nil && familyDialers[1] != nil {
		primary, primaryErr := newClient(familyDialers[0])
		if primaryErr != nil {
			return nil, primaryErr
		}
		secondary, secondaryErr := newClient(familyDialers[1])
		if secondaryErr != nil {
			_ = primary.CloseWithError(net.ErrClosed)
			return nil, secondaryErr
		}
		carrier = newFamilyCarrier(func(family int) (*tuic.Client, error) {
			return newClient(familyDialers[family-1])
		})
		carrier.clients[0] = primary
		carrier.clients[1] = secondary
		carrier.families = 2
	} else {
		client, clientErr := newClient(outboundDialer)
		if clientErr != nil {
			return nil, clientErr
		}
		carrier = newFamilyCarrier(func(int) (*tuic.Client, error) {
			return newClient(outboundDialer)
		})
		carrier.clients[0] = client
		carrier.families = 1
	}
	created := &Outbound{
		Adapter:   outbound.NewAdapterWithDialerOptions(C.TypeTUIC, tag, options.Network.Build(), options.DialerOptions, options.ServerIsDomain()),
		logger:    logger,
		client:    carrier,
		udpStream: options.UDPOverStream,
	}
	created.generation = runtimecoord.NewGenerationGate(
		service.FromContext[*runtimecoord.Coordinator](ctx),
		"tuic/"+options.ServerOptions.Build().String(),
		created.InterfaceUpdated,
	)
	return created, nil
}

func (h *Outbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	if h.generation != nil {
		h.generation.BeforeDial(ctx)
	}
	conn, err := h.dialContext(ctx, network, destination)
	if h.generation != nil {
		h.generation.AfterDial(err)
	}
	return conn, err
}

func (h *Outbound) dialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	switch N.NetworkName(network) {
	case N.NetworkTCP:
		h.logger.InfoContext(ctx, "outbound connection to ", destination)
		return h.client.DialConn(ctx, destination)
	case N.NetworkUDP:
		if h.udpStream {
			h.logger.InfoContext(ctx, "outbound stream packet connection to ", destination)
			streamConn, err := h.client.DialConn(ctx, uot.RequestDestination(uot.Version))
			if err != nil {
				return nil, err
			}
			return uot.NewLazyConn(streamConn, uot.Request{
				IsConnect:   true,
				Destination: destination,
			}), nil
		} else {
			conn, err := h.listenPacket(ctx, destination)
			if err != nil {
				return nil, err
			}
			return bufio.NewBindPacketConn(conn, destination), nil
		}
	default:
		return nil, E.New("unsupported network: ", network)
	}
}

func (h *Outbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	if h.generation != nil {
		h.generation.BeforeDial(ctx)
	}
	conn, err := h.listenPacket(ctx, destination)
	if h.generation != nil {
		h.generation.AfterDial(err)
	}
	return conn, err
}

func (h *Outbound) listenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	if h.udpStream {
		h.logger.InfoContext(ctx, "outbound stream packet connection to ", destination)
		streamConn, err := h.client.DialConn(ctx, uot.RequestDestination(uot.Version))
		if err != nil {
			return nil, err
		}
		return uot.NewLazyConn(streamConn, uot.Request{
			IsConnect:   false,
			Destination: destination,
		}), nil
	} else {
		h.logger.InfoContext(ctx, "outbound packet connection to ", destination)
		return h.client.ListenPacket(ctx)
	}
}

func (h *Outbound) InterfaceUpdated(ctx context.Context) {
	_ = h.client.CloseWithError(E.New("network changed"))
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
		h.generation.Close()
		return h.client.CloseWithError(os.ErrClosed)
	})
	return nil
}

// ResidencyLedger implements adapter.ResidencyLedgerReporter honestly: the pinned sing-quic client
// owns the session objects and exposes no observation interface, so this owner cannot answer. It is
// reported as unknown rather than as a precise zero.
func (h *Outbound) ResidencyLedger() adapter.ResidencyLedger {
	if h.client != nil {
		return h.client.ResidencyLedger()
	}
	return adapter.ResidencyLedger{
		Answered: false,
		Note:     "the TUIC carrier was not constructed; this outbound cannot answer",
	}
}
