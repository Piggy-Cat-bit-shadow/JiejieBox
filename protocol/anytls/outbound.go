package anytls

import (
	"context"
	"net"
	"os"
	"strings"

	"github.com/sagernet/sing-anytls"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/dialer"
	"github.com/sagernet/sing-box/common/tls"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/common/uot"
)

func RegisterOutbound(registry *outbound.Registry) {
	outbound.Register[option.AnyTLSOutboundOptions](registry, C.TypeAnyTLS, NewOutbound)
}

var (
	_ adapter.OutboundWithMultiplex   = (*Outbound)(nil)
	_ adapter.InterfaceUpdateListener = (*Outbound)(nil)
	_ adapter.IdleConnectionKeeper    = (*Outbound)(nil)
)

type Outbound struct {
	outbound.Adapter
	dialer        tls.Dialer
	server        M.Socksaddr
	tlsConfig     tls.Config
	clientOptions anytls.ClientOptions
	client        *anytls.Client
	uotClient     *uot.Client
	logger        log.ContextLogger
}

func NewOutbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.AnyTLSOutboundOptions) (adapter.Outbound, error) {
	outbound := &Outbound{
		Adapter: outbound.NewAdapterWithDialerOptions(C.TypeAnyTLS, tag, []string{N.NetworkTCP, N.NetworkUDP}, options.DialerOptions, options.ServerIsDomain()),
		server:  options.ServerOptions.Build(),
		logger:  logger,
	}
	if options.TLS == nil || !options.TLS.Enabled {
		return nil, C.ErrTLSRequired
	}
	if options.DialerOptions.TCPFastOpen {
		return nil, E.New("tcp_fast_open is not supported with anytls outbound")
	}

	tlsConfig, err := tls.NewClient(ctx, logger, options.Server, common.PtrValueOrDefault(options.TLS))
	if err != nil {
		return nil, err
	}
	outbound.tlsConfig = tlsConfig

	outboundDialer, err := dialer.NewWithOptions(dialer.Options{
		Context:        ctx,
		Options:        options.DialerOptions,
		RemoteIsDomain: options.ServerIsDomain(),
		// The AnyTLS server is the one hop every YouTube stream shares. On a dual-stack host
		// whose active path is IPv4, resolving this name to a secondary interface's IPv6 first
		// makes short requests look healthy while bulk video through the session stalls; see
		// dialer.Options.PreferIPv4ForServer. Target destinations are not affected.
		PreferIPv4ForServer: true,
	})
	if err != nil {
		return nil, err
	}

	outbound.dialer = tls.NewDialer(outboundDialer, tlsConfig)

	outbound.clientOptions = anytls.ClientOptions{
		Password:                 options.Password,
		ClientMetadata:           options.ClientMetadata,
		DialOut:                  outbound.dialOut,
		IdleSessionCheckInterval: options.IdleSessionCheckInterval.Build(),
		IdleSessionTimeout:       options.IdleSessionTimeout.Build(),
		MinIdleSession:           options.MinIdleSession,
		Logger:                   logger,
	}
	return outbound, nil
}

func (h *Outbound) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	if stage != adapter.StartStateInitialize {
		return nil
	}
	client, err := anytls.NewClient(h.clientOptions)
	if err != nil {
		return err
	}
	h.client = client
	scope.Add(client.Close)
	h.uotClient = &uot.Client{
		Dialer:  anytlsDialer(client.DialContext),
		Version: uot.Version,
	}
	return nil
}

type anytlsDialer func(ctx context.Context, destination M.Socksaddr) (net.Conn, error)

func (d anytlsDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	return d(ctx, destination)
}

func (d anytlsDialer) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, os.ErrInvalid
}

func (h *Outbound) dialOut(ctx context.Context) (net.Conn, error) {
	return h.dialer.DialTLSContext(adapter.ContextForMultiplexSession(ctx), h.server)
}

func (h *Outbound) MultiplexEnabled() bool {
	return true
}

func (h *Outbound) InterfaceUpdated(ctx context.Context) {
	h.client.Reset()
}

func (h *Outbound) SetKeepIdleConnections(keep bool) {
	h.client.SetKeepIdleConnections(keep)
}

func (h *Outbound) CloseIdleConnections() {
	h.client.CloseIdleConnections()
}

func (h *Outbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	ctx, metadata := adapter.ExtendContext(ctx)
	metadata.Outbound = h.Tag()
	metadata.Destination = destination
	switch N.NetworkName(network) {
	case N.NetworkTCP:
		h.logger.InfoContext(ctx, "outbound connection to ", destination)
		return h.dialThroughProxy(ctx, destination)
	case N.NetworkUDP:
		h.logger.InfoContext(ctx, "outbound UoT packet connection to ", destination)
		conn, err := h.uotClient.DialContext(ctx, network, destination)
		if err != nil {
			return nil, h.nameTheProxy(err)
		}
		return conn, nil
	}
	return nil, os.ErrInvalid
}

// dialThroughProxy dials the destination through this outbound's AnyTLS session.
func (h *Outbound) dialThroughProxy(ctx context.Context, destination M.Socksaddr) (net.Conn, error) {
	conn, err := h.client.DialContext(ctx, destination)
	if err != nil {
		return nil, h.nameTheProxy(err)
	}
	return conn, nil
}

// nameTheProxy attaches which proxy answered to a failure this outbound did not originate.
//
// # Why the error needs it
//
// The AnyTLS stream reports a target the PROXY's resolver could not resolve as
// `anytls: remote: lookup <name>: NXDOMAIN` - the exact text of a reproducible failure in the production
// log, where `analytics.immersivetranslate.com` resolved fine through Cloudflare and Google from this
// machine but not through the proxy. Nothing in that message says WHICH proxy, so an operator with several
// AI/overseas outbounds cannot tell whose remote resolver to look at; the tag and the server add the two
// facts that identify it.
//
// # Why this is not a fix for the NXDOMAIN
//
// It cannot be: `remote:` means the lookup happened on the proxy's side, against the proxy's own resolver
// configuration, and the destination is demonstrably valid elsewhere. Wrapping the failure at the outbound
// boundary is the most this repository can do - turn "some proxy could not resolve this" into "the AI
// outbound at 95.169.1.53 could not resolve this". Anything more would mean inventing a second resolution
// path this side of the wire, which is precisely the `destination_dns_ownership` promise the AnyTLS
// outbound makes instead.
//
// The message is prefixed rather than suffixed so it survives the connection logger, which reports the
// leading segment of the cause chain.
func (h *Outbound) nameTheProxy(err error) error {
	if err == nil {
		return nil
	}
	// Idempotent: the message is one line in a connection report, and a retry chain can pass the same
	// failure through here more than once. A second identical prefix says nothing the first did not.
	prefix := "proxy " + h.Tag() + " (" + h.server.String() + ")"
	if strings.Contains(err.Error(), prefix) {
		return err
	}
	return E.Cause(err, prefix)
}

func (h *Outbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	ctx, metadata := adapter.ExtendContext(ctx)
	metadata.Outbound = h.Tag()
	metadata.Destination = destination
	h.logger.InfoContext(ctx, "outbound UoT packet connection to ", destination)
	return h.uotClient.ListenPacket(ctx, destination)
}
