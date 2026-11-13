package trusttunnel

import (
	"context"
	"errors"
	"strings"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/inbound"
	"github.com/sagernet/sing-box/common/listener"
	"github.com/sagernet/sing-box/common/tls"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/transport/trusttunnel"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"golang.org/x/net/http2"
)

const nextProtoH3 = "h3"

var _ trusttunnel.Handler = (*Inbound)(nil)

func RegisterInbound(registry *inbound.Registry) {
	inbound.Register[option.TrustTunnelInboundOptions](registry, C.TypeTrustTunnel, NewInbound)
}

type Inbound struct {
	inbound.Adapter
	router    adapter.ConnectionRouterEx
	logger    logger.ContextLogger
	listener  *listener.Listener
	network   []string
	tlsConfig tls.ServerConfig
	service   *trusttunnel.Service
}

func NewInbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.TrustTunnelInboundOptions) (adapter.Inbound, error) {
	if options.TLS == nil || !options.TLS.Enabled {
		return nil, C.ErrTLSRequired
	}
	if len(options.Users) == 0 {
		return nil, E.New("missing users")
	}
	for index, user := range options.Users {
		if user.Username == "" {
			return nil, E.New("users[", index, "]: missing username")
		}
		if user.Password == "" {
			return nil, E.New("users[", index, "]: missing password")
		}
	}
	network := options.Network.Build()
	for _, networkName := range network {
		switch networkName {
		case N.NetworkTCP, N.NetworkUDP:
		default:
			return nil, E.New("unsupported network: ", networkName, ", network selects the listeners: tcp (HTTP/2) and/or udp (HTTP/3)")
		}
	}
	switch options.QUICCongestionControl {
	case "", "bbr", "cubic", "reno":
	default:
		return nil, E.New("unknown quic_congestion_control: ", options.QUICCongestionControl)
	}
	tlsConfig, err := tls.NewServer(ctx, logger, *options.TLS)
	if err != nil {
		return nil, err
	}
	inbound := &Inbound{
		Adapter: inbound.NewAdapter(C.TypeTrustTunnel, tag),
		router:  router,
		logger:  logger,
		listener: listener.New(listener.Options{
			Context: ctx,
			Logger:  logger,
			Listen:  options.ListenOptions,
		}),
		network:   network,
		tlsConfig: tlsConfig,
	}
	inbound.service, err = trusttunnel.NewService(trusttunnel.ServiceOptions{
		Context: ctx,
		Logger:  logger,
		Handler: inbound,
		Users:   options.Users,
		QUIC: &trusttunnel.QUICOptions{
			CongestionControl: options.QUICCongestionControl,
		},
	})
	if err != nil {
		return nil, err
	}
	return inbound, nil
}

func (h *Inbound) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	if stage != adapter.StartStateStart {
		return nil
	}
	err := h.tlsConfig.Start()
	if err != nil {
		return E.Cause(err, "create TLS config")
	}
	scope.Add(h.tlsConfig.Close)
	scope.Add(func() error {
		// The service has already closed the listeners it was serving.
		err := h.listener.Close()
		if E.IsClosed(err) {
			return nil
		}
		return err
	})
	scope.Add(h.service.Close)
	serveTCP := common.Contains(h.network, N.NetworkTCP)
	serveQUIC := common.Contains(h.network, N.NetworkUDP)
	// A nil config returns at once: ErrQUICNotIncluded without with_quic, an
	// argument error with it.
	if serveQUIC && errors.Is(h.service.ServeQUIC(nil, nil), trusttunnel.ErrQUICNotIncluded) {
		if !serveTCP {
			return trusttunnel.ErrQUICNotIncluded
		}
		h.logger.Warn("HTTP/3 disabled: ", trusttunnel.ErrQUICNotIncluded)
		serveQUIC = false
	}
	// ALPN is settled before either listener serves, so that the two serve
	// loops never rewrite the shared config concurrently.
	//
	// A dual TCP+QUIC inbound must not share one ALPN list. h3 is QUIC-only and
	// h2/http1.1 are TCP-only; a shared list lets a TCP client negotiate h3 (a
	// protocol it cannot speak) and a QUIC client negotiate h2. The TCP side keeps
	// the shared config; the QUIC side gets a transport-scoped view, because cloning
	// the shared config would drop the certificate provider, ACME service and file
	// watcher, and mutating the shared list per handshake would race TCP.
	tcpProtos, quicProtos := resolveTransportALPN(h.tlsConfig.NextProtos(), serveTCP, serveQUIC)
	if serveTCP && common.Contains(h.tlsConfig.NextProtos(), nextProtoH3) {
		h.logger.Warn("trusttunnel: tls.alpn contains h3; h3 is QUIC-only and is not offered on the TCP listener")
	}
	if serveTCP {
		h.tlsConfig.SetNextProtos(tcpProtos)
	} else if serveQUIC {
		h.tlsConfig.SetNextProtos(quicProtos)
	}
	quicTLSConfig := h.tlsConfig
	if serveTCP && serveQUIC {
		quicTLSConfig = tls.TransportALPNView(h.tlsConfig, quicProtos)
	}
	h.logger.Info("trusttunnel: ALPN resolved: tcp=", strings.Join(tcpProtos, ","),
		" quic=", strings.Join(quicProtos, ","))
	if serveTCP {
		tcpListener, err := h.listener.ListenTCP()
		if err != nil {
			return err
		}
		go func() {
			sErr := h.service.ServeTCP(tcpListener, h.tlsConfig)
			if sErr != nil && !E.IsClosed(sErr) {
				h.logger.Error("serve HTTP/2: ", sErr)
			}
		}()
	}
	if serveQUIC {
		udpConn, err := h.listener.ListenUDP()
		if err != nil {
			return err
		}
		go func() {
			sErr := h.service.ServeQUIC(udpConn, quicTLSConfig)
			if sErr != nil && !E.IsClosed(sErr) {
				h.logger.Error("serve HTTP/3: ", sErr)
			}
		}()
	}
	return nil
}

// resolveTransportALPN computes each transport's ALPN list from the configured list.
//
// The TCP list never contains h3 and always contains h2. The QUIC list is h3 only:
// h2/http1.1 are not valid over QUIC, and the QUIC view must not expose a protocol
// the transport cannot serve. Keeping this pure is what lets the invariant be tested
// without binding listeners.
func resolveTransportALPN(configured []string, serveTCP, serveQUIC bool) (tcpProtos, quicProtos []string) {
	if serveTCP {
		for _, protocol := range configured {
			if protocol != nextProtoH3 {
				tcpProtos = append(tcpProtos, protocol)
			}
		}
		if !common.Contains(tcpProtos, http2.NextProtoTLS) {
			tcpProtos = append([]string{http2.NextProtoTLS}, tcpProtos...)
		}
	}
	if serveQUIC {
		quicProtos = []string{nextProtoH3}
	}
	return tcpProtos, quicProtos
}

func (h *Inbound) NewConnection(ctx context.Context, conn *trusttunnel.ServerConn, request trusttunnel.Metadata) {
	ctx = log.ContextWithNewID(ctx)
	h.logger.InfoContext(ctx, "[", request.User, "] inbound connection from ", request.Source)
	h.logger.InfoContext(ctx, "[", request.User, "] inbound connection to ", request.Destination)
	metadata := h.newMetadata(request)
	metadata.OriginDestination = M.SocksaddrFromNet(conn.LocalAddr()).Unwrap()
	// The CONNECT is answered on the router's first read (200) or by Fail
	// (502 with X-Warning), so a failed dial reaches the client as an error.
	h.router.RouteConnectionEx(ctx, conn, metadata, N.OnceClose(func(err error) {
		if err != nil {
			conn.Fail(err)
		} else {
			conn.Close()
		}
	}))
}

func (h *Inbound) NewPacketConnection(ctx context.Context, conn N.PacketConn, request trusttunnel.Metadata) {
	ctx = log.ContextWithNewID(ctx)
	h.logger.InfoContext(ctx, "[", request.User, "] inbound packet connection from ", request.Source)
	h.logger.InfoContext(ctx, "[", request.User, "] inbound packet connection to ", request.Destination)
	h.router.RoutePacketConnectionEx(ctx, conn, h.newMetadata(request), N.OnceClose(func(error) {
		conn.Close()
	}))
}

func (h *Inbound) newMetadata(request trusttunnel.Metadata) adapter.InboundContext {
	var metadata adapter.InboundContext
	metadata.Inbound = h.Tag()
	metadata.InboundType = h.Type()
	//nolint:staticcheck
	metadata.InboundDetour = h.listener.ListenOptions().Detour
	metadata.Source = request.Source
	metadata.Destination = request.Destination
	metadata.User = request.User
	return metadata
}
