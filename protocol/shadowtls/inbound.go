package shadowtls

import (
	"context"
	"net"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/inbound"
	"github.com/sagernet/sing-box/common/dialer"
	"github.com/sagernet/sing-box/common/listener"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-shadowtls"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/auth"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

func RegisterInbound(registry *inbound.Registry) {
	inbound.Register[option.ShadowTLSInboundOptions](registry, C.TypeShadowTLS, NewInbound)
}

type Inbound struct {
	inbound.Adapter
	router     adapter.Router
	logger     logger.ContextLogger
	listener   *listener.Listener
	service    *shadowtls.Service
	references []string
	// preAuthTimeout bounds the read of the first TLS record on the public
	// connection, so a peer that sends nothing (or half a ClientHello) cannot
	// hold a socket and a goroutine open indefinitely. See pre_auth_timeout.go.
	preAuthTimeout time.Duration
}

func NewInbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.ShadowTLSInboundOptions) (adapter.Inbound, error) {
	inbound := &Inbound{
		Adapter:        inbound.NewAdapter(C.TypeShadowTLS, tag),
		router:         router,
		logger:         logger,
		preAuthTimeout: defaultPreAuthTimeout,
	}

	if options.Version == 0 {
		options.Version = 1
	}

	var handshakeForServerName map[string]shadowtls.HandshakeConfig
	if options.Version > 1 {
		handshakeForServerName = make(map[string]shadowtls.HandshakeConfig)
		if options.HandshakeForServerName != nil {
			for _, entry := range options.HandshakeForServerName.Entries() {
				if entry.Value.Detour != "" {
					inbound.references = append(inbound.references, entry.Value.Detour)
				}
				handshakeDialer, err := dialer.New(ctx, entry.Value.DialerOptions, entry.Value.ServerIsDomain())
				if err != nil {
					return nil, err
				}
				handshakeForServerName[entry.Key] = shadowtls.HandshakeConfig{
					Server: entry.Value.ServerOptions.Build(),
					Dialer: newTrackingDialer(handshakeDialer),
				}
			}
		}
	}
	serverIsDomain := options.Handshake.ServerIsDomain()
	if options.WildcardSNI != option.ShadowTLSWildcardSNIOff {
		serverIsDomain = true
	}
	handshakeDialer, err := dialer.New(ctx, options.Handshake.DialerOptions, serverIsDomain)
	if err != nil {
		return nil, err
	}
	if options.Handshake.Detour != "" {
		inbound.references = append(inbound.references, options.Handshake.Detour)
	}
	service, err := shadowtls.NewService(shadowtls.ServiceConfig{
		Version:  options.Version,
		Password: options.Password,
		Users: common.Map(options.Users, func(it option.ShadowTLSUser) shadowtls.User {
			return shadowtls.User(it)
		}),
		Handshake: shadowtls.HandshakeConfig{
			Server: options.Handshake.ServerOptions.Build(),
			Dialer: newTrackingDialer(handshakeDialer),
		},
		HandshakeForServerName: handshakeForServerName,
		StrictMode:             options.StrictMode,
		WildcardSNI:            shadowtls.WildcardSNI(options.WildcardSNI),
		Handler:                (*inboundHandler)(inbound),
		Logger:                 logger,
	})
	if err != nil {
		return nil, err
	}
	inbound.service = service
	inbound.listener = listener.New(listener.Options{
		Context:           ctx,
		Logger:            logger,
		Network:           []string{N.NetworkTCP},
		Listen:            options.ListenOptions,
		ConnectionHandler: inbound,
	})
	return inbound, nil
}

func (h *Inbound) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	if stage != adapter.StartStateStart {
		return nil
	}
	err := h.listener.Start()
	if err != nil {
		return err
	}
	scope.Add(h.listener.Close)
	return nil
}

func (h *Inbound) NewConnection(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	// Bound ONLY the pre-authentication phase: the deadline is armed here and is
	// cleared the moment the first TLS record has been read in full, which is
	// before the service either authenticates the peer or relays it to the
	// handshake target. An established session -- and a fallback browser session
	// being relayed to the decoy -- therefore runs with no deadline at all.
	conn = newFirstRecordTimeoutConn(conn, h.preAuthTimeout)
	// The handshake may dial a target connection that sing-shadowtls abandons on an
	// error return. The dialers handed to the service record what they opened under
	// this call's own context, so whatever the service did not close can be released
	// the moment it returns. See handshake_target_cleanup.go.
	targets := &handshakeTargets{}
	handshakeCtx := adapter.WithContext(log.ContextWithNewID(ctx), &metadata)
	err := h.service.NewConnection(contextWithHandshakeTargets(handshakeCtx, targets), conn, metadata.Source, metadata.Destination, onClose)
	targets.closeLeftovers()
	N.CloseOnHandshakeFailure(conn, onClose, err)
	if err != nil {
		// A non-TLS probe, a truncated record or a scanner disconnect all land
		// here as read errors. They are routine on a public port and are
		// classified by TYPE (see probe_log.go); an unrecognised failure still
		// stays at error level.
		//
		// The pre-authentication read timeout lands here too, as a read
		// *net.OpError on OUR OWN socket, and is therefore classified as an
		// expected probe outcome rather than a fault - the same severity
		// protocol/anytls gives its equivalent bound. A timeout on the DIAL to
		// the handshake target is a different shape and still stays visible.
		logShadowTLSError(ctx, h.logger, metadata.Source, err)
	}
}

type inboundHandler Inbound

func (h *inboundHandler) NewConnectionEx(ctx context.Context, conn net.Conn, source M.Socksaddr, destination M.Socksaddr, onClose N.CloseHandlerFunc) {
	var metadata adapter.InboundContext
	metadata.Inbound = h.Tag()
	metadata.InboundType = h.Type()
	//nolint:staticcheck
	metadata.InboundDetour = h.listener.ListenOptions().Detour
	//nolint:staticcheck
	metadata.Source = source
	metadata.Destination = destination
	if userName, _ := auth.UserFromContext[string](ctx); userName != "" {
		metadata.User = userName
		h.logger.InfoContext(ctx, "[", userName, "] inbound connection to ", metadata.Destination)
	} else {
		h.logger.InfoContext(ctx, "inbound connection to ", metadata.Destination)
	}
	h.router.RouteConnectionEx(ctx, conn, metadata, onClose)
}

func (h *Inbound) References() []string {
	return h.references
}
