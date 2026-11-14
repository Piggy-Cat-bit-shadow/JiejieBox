//go:build with_naive_outbound

package naive

import (
	"context"
	"encoding/pem"
	"net"
	"strings"

	"github.com/sagernet/cronet-go"
	_ "github.com/sagernet/cronet-go/all"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/dialer"
	"github.com/sagernet/sing-box/common/resourceguard"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/common/uot"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/filemanager"
)

func RegisterOutbound(registry *outbound.Registry) {
	outbound.Register[option.NaiveOutboundOptions](registry, C.TypeNaive, NewOutbound)
}

type Outbound struct {
	outbound.Adapter
	ctx       context.Context
	logger    logger.ContextLogger
	client    *cronet.NaiveClient
	uotClient *uot.Client
	// fallbackToH2 is the opt-in H3->H2 retry (quic_fallback_to_h2). It is read
	// on the dial path only; when false the outbound behaves exactly as before.
	fallbackToH2 bool
	// h3Fallback is owned by this outbound. It is not shared with any other
	// protocol and holds no per-target state; see h3_fallback.go.
	h3Fallback h3Fallback
	// h2Fallback is the optional engine-level QUIC-disabled client used for the
	// opt-in H2 retry; nil unless the operator opted in. See h2_fallback_engine.go.
	h2Fallback *h2FallbackClient
	// nativeCreation is the admission + accounting seam at the engine's native
	// socket-creation callback. See native_creation.go.
	nativeCreation *nativeCreationDialer
	// engineVersion is the loaded engine's own version string, recorded once at
	// Start. See native_contract.go for why it is not read before the engine exists.
	engineVersion string
}

func NewOutbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.NaiveOutboundOptions) (adapter.Outbound, error) {
	if options.TLS == nil || !options.TLS.Enabled {
		return nil, C.ErrTLSRequired
	}
	if options.TLS.DisableSNI {
		return nil, E.New("disable_sni is not supported on naive outbound")
	}
	if options.TLS.Insecure {
		return nil, E.New("insecure is not supported on naive outbound")
	}
	if len(options.TLS.ALPN) > 0 {
		return nil, E.New("alpn is not supported on naive outbound")
	}
	if options.TLS.MinVersion != "" {
		return nil, E.New("min_version is not supported on naive outbound")
	}
	if options.TLS.MaxVersion != "" {
		return nil, E.New("max_version is not supported on naive outbound")
	}
	if len(options.TLS.CipherSuites) > 0 {
		return nil, E.New("cipher_suites is not supported on naive outbound")
	}
	if len(options.TLS.CurvePreferences) > 0 {
		return nil, E.New("curve_preferences is not supported on naive outbound")
	}
	if len(options.TLS.ClientCertificate) > 0 || options.TLS.ClientCertificatePath != "" {
		return nil, E.New("client_certificate is not supported on naive outbound")
	}
	if len(options.TLS.ClientKey) > 0 || options.TLS.ClientKeyPath != "" {
		return nil, E.New("client_key is not supported on naive outbound")
	}
	if options.TLS.Fragment || options.TLS.RecordFragment {
		return nil, E.New("fragment is not supported on naive outbound")
	}
	if options.TLS.KernelTx || options.TLS.KernelRx {
		return nil, E.New("kernel TLS is not supported on naive outbound")
	}
	if options.TLS.UTLS != nil && options.TLS.UTLS.Enabled {
		return nil, E.New("uTLS is not supported on naive outbound")
	}
	if options.TLS.Reality != nil && options.TLS.Reality.Enabled {
		return nil, E.New("reality is not supported on naive outbound")
	}
	// The shared OutboundTLSOptions exposes both pin fields, but the Cronet engine is only given
	// trusted roots. Accepting a pin here would silently downgrade an operator's explicit
	// certificate/public-key pin to ordinary CA verification. Reject it until the Cronet pin
	// callback is wired.
	if len(options.TLS.CertificateSHA256) > 0 || len(options.TLS.CertificatePublicKeySHA256) > 0 {
		return nil, E.New("certificate pinning is not supported on naive outbound")
	}
	// The fallback only exists for a forced-H3 attempt. Without quic: true there
	// is no H3 attempt to fall back from, so the combination is a configuration
	// error rather than a silently inert field.
	if options.QUICFallbackToH2 && !options.QUIC {
		return nil, E.New("quic_fallback_to_h2 requires quic: true: ",
			"without it there is no H3 attempt to fall back from")
	}

	// Reject a reserved-header collision at configuration time. cronet-go also
	// refuses it in NewNaiveClient, but failing here means the operator sees a
	// configuration error naming the offending header instead of a runtime failure
	// that depends on the native library loading first.
	if err := validateReservedExtraHeaders(options.ExtraHeaders.Build()); err != nil {
		return nil, err
	}

	serverAddress := options.ServerOptions.Build()

	var serverName string
	if options.TLS.ServerName != "" {
		serverName = options.TLS.ServerName
	} else {
		serverName = serverAddress.AddrString()
	}

	outboundDialer, err := dialer.NewWithOptions(dialer.Options{
		Context:          ctx,
		Options:          options.DialerOptions,
		RemoteIsDomain:   true,
		ResolverOnDetour: true,
		NewDialer:        true,
	})
	if err != nil {
		return nil, err
	}

	var trustedRootCertificates string
	if len(options.TLS.Certificate) > 0 {
		trustedRootCertificates = strings.Join(options.TLS.Certificate, "\n")
	} else if options.TLS.CertificatePath != "" {
		content, err := filemanager.ReadFile(ctx, options.TLS.CertificatePath)
		if err != nil {
			return nil, E.Cause(err, "read certificate")
		}
		trustedRootCertificates = string(content)
	}

	extraHeaders := make(map[string]string)
	for key, values := range options.ExtraHeaders.Build() {
		if len(values) > 0 {
			extraHeaders[key] = values[0]
		}
	}

	// Chromium's DNS goes through this bridge rather than the system resolver, so a Naive
	// connection cannot leak a query past sing-box's DNS policy.
	//
	// # The server name is resolved once, by the dialer
	//
	// cronet-go answers the A/AAAA query for the configured server name with the synthetic
	// address 192.0.2.1 and maps that address back to the configured server address in its
	// TCP/UDP socket callbacks. The custom dialer below therefore receives a name (or the
	// literal address the operator configured) and performs the only real resolution,
	// through the policy below. Chromium's answer is a routing marker, not a DNS result, so
	// the two layers are not two authorities and the host's dual-stack scheduling is kept.
	//
	// This is also why the dialer chain must be a ResolveDialer: the policy that resolves
	// the server is the fork's, not Chromium's.
	// Checked at construction rather than inside the resolver closure below, where an unchecked assertion
	// would panic during a DNS exchange instead of failing the configuration load, and where returning an
	// error is not even possible. Same defect class as the direct outbound's, fixed there first.
	resolveDialerForQueryOptions, isResolveDialer := outboundDialer.(dialer.ResolveDialer)
	if !isResolveDialer {
		return nil, E.New("naive outbound: the dialer chain did not produce a resolve dialer, which this outbound requires for its DNS policy; this is a defect in the dialer factory rather than in the configuration")
	}
	// The engine's native socket-creation callback (Cronet_Engine_SetDialer) is the only place a
	// physical Naive connection exists, so the process admission gate and the creation counters
	// belong there - not on DialEarly, which counts CONNECTs, and not on the engine, which counts
	// outbounds. See native_creation.go for what this seam can and cannot observe.
	// The ResolveDialer capability is captured above, before the wrap: the wrapper is a plain
	// N.Dialer and would hide it.
	creationDialer := newNativeCreationDialer(outboundDialer, resourceguard.FromContext(ctx), serverAddress.String())
	dnsRouter := service.FromContext[adapter.DNSRouter](ctx)
	dnsResolver := newCronetDNSResolver(dnsRouter, resolveDialerForQueryOptions, logger)

	var echEnabled bool
	var echConfigList []byte
	var echQueryServerName string
	if options.TLS.ECH != nil && options.TLS.ECH.Enabled {
		echEnabled = true
		echQueryServerName = options.TLS.ECH.QueryServerName
		var echConfig []byte
		if len(options.TLS.ECH.Config) > 0 {
			echConfig = []byte(strings.Join(options.TLS.ECH.Config, "\n"))
		} else if options.TLS.ECH.ConfigPath != "" {
			content, err := filemanager.ReadFile(ctx, options.TLS.ECH.ConfigPath)
			if err != nil {
				return nil, E.Cause(err, "read ECH config")
			}
			echConfig = content
		}
		if len(echConfig) > 0 {
			block, rest := pem.Decode(echConfig)
			if block == nil || block.Type != "ECH CONFIGS" || len(rest) > 0 {
				return nil, E.New("invalid ECH configs pem")
			}
			echConfigList = block.Bytes
		}
	}
	quicCongestionControl, err := resolveCronetCongestionControl(options.QUICCongestionControl)
	if err != nil {
		return nil, err
	}
	if options.QUICCongestionControl == "" {
		logger.Info("naive: quic_congestion_control is unset; using CUBIC. ",
			"Set \"bbr\" explicitly on lossy paths")
	} // InsecureConcurrencySingleEngine is passed through only when the operator asked
	// for it. cronet-go ORs this with `runtime.GOOS == "ios"`, so leaving it false
	// keeps the platform behaviour exactly as upstream: N engines on macOS, one
	// engine on iOS. Setting it true is what makes the single-engine + isolation-key
	// shape selectable on macOS for the A/B described in
	// docs/ENGINEERING-NOTES.md.
	clientOptions := buildCronetNaiveClientOptions(cronetNaiveClientParams{
		ctx:                     ctx,
		logger:                  logger,
		serverAddress:           serverAddress,
		serverName:              serverName,
		options:                 options,
		extraHeaders:            extraHeaders,
		trustedRootCertificates: trustedRootCertificates,
		dialer:                  creationDialer,
		dnsResolver:             dnsResolver,
		echEnabled:              echEnabled,
		echConfigList:           echConfigList,
		echQueryServerName:      echQueryServerName,
		quicCongestionControl:   quicCongestionControl,
	})
	client, err := cronet.NewNaiveClient(clientOptions)
	if err != nil {
		return nil, err
	}
	naiveOutbound := &Outbound{
		Adapter:        outbound.NewAdapterWithDialerOptions(C.TypeNaive, tag, nil, options.DialerOptions),
		ctx:            ctx,
		logger:         logger,
		client:         client,
		fallbackToH2:   options.QUICFallbackToH2,
		nativeCreation: creationDialer,
	}
	if options.QUICFallbackToH2 {
		// The H2 retry needs an ENGINE with QUIC disabled: the pinned Cronet has no
		// per-request switch, and the H3-first engine may still follow a cached Alt-Svc.
		// The second engine is created on the first fallback (see h2FallbackClient) and
		// is only a guarantee for that flow when creation succeeds.
		fallbackOptions := clientOptions
		fallbackOptions.QUIC = false
		naiveOutbound.h2Fallback = newH2FallbackClient(func() (naiveH2Client, error) {
			fallbackClient, fallbackErr := cronet.NewNaiveClient(fallbackOptions)
			if fallbackErr != nil {
				return nil, fallbackErr
			}
			if startErr := fallbackClient.Start(); startErr != nil {
				_ = fallbackClient.Close()
				return nil, startErr
			}
			return fallbackClient, nil
		})
	}
	uotOptions := common.PtrValueOrDefault(options.UDPOverTCP)
	if uotOptions.Enabled {
		naiveOutbound.uotClient = &uot.Client{
			Dialer:  &naiveDialer{NaiveClient: client, owner: naiveOutbound},
			Version: uotOptions.Version,
		}
	}
	var networks []string
	if naiveOutbound.uotClient != nil {
		networks = []string{N.NetworkTCP, N.NetworkUDP}
	} else {
		networks = []string{N.NetworkTCP}
	}
	naiveOutbound.Adapter = outbound.NewAdapterWithDialerOptions(C.TypeNaive, tag, networks, options.DialerOptions)
	return naiveOutbound, nil
}

// resolveCronetCongestionControl maps quic_congestion_control onto Cronet tags.
//
// An unset value and "cubic" both select CQBC, the tag QuicSentPacketManager
// honours unconditionally. They do NOT select Cronet's build default: "unset"
// must mean the same algorithm as on the naive inbound, both trusttunnel halves
// and the HTTP/3 client, and a build default can move with a dependency bump
// without the configuration changing. "bbr" and "bbr2" select the two BBR tags
// explicitly; "reno" selects RENO.
func resolveCronetCongestionControl(name string) (cronet.QUICCongestionControl, error) {
	switch name {
	case "", "cubic":
		return cronet.QUICCongestionControlCubic, nil
	case "bbr":
		return cronet.QUICCongestionControlBBR, nil
	case "bbr2":
		return cronet.QUICCongestionControlBBRv2, nil
	case "reno":
		return cronet.QUICCongestionControlReno, nil
	default:
		return "", E.New("unknown quic congestion control: ", name)
	}
}

func (h *Outbound) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	if stage != adapter.StartStateStart {
		return nil
	}
	err := h.client.Start()
	if err != nil {
		return err
	}
	scope.Add(h.client.Close)
	if h.h2Fallback != nil {
		// A no-op when the lazy engine was never created.
		scope.Add(h.h2Fallback.Close)
	}
	// Idle connections are the only reclaim action this outbound can offer a process under
	// pressure: a busy CONNECT cannot be closed without cutting the user's flow, and the
	// engine's pool is idle-only by definition.
	if guard := resourceguard.FromContext(h.ctx); guard != nil {
		guard.RegisterShed(h.client.CloseAllConnections)
	}
	// Record WHICH native artifact and WHICH binding generation this process is running. The two
	// are separate facts (see native_contract.go): the module version names the Go binding, the
	// engine version comes from the loaded library, and the archive bytes those resolve to are in
	// release/cronet-provenance*.txt.
	h.engineVersion = h.client.Engine().Version()
	contract := h.NativeContract()
	h.logger.Info("NaiveProxy started, cronet-go=", contract.ModuleVersion,
		" (fork=", contract.ModuleReplaced, "), engine=", contract.EngineVersion,
		", native creation callback=", contract.CreationCallback)
	return nil
}

func (h *Outbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	switch N.NetworkName(network) {
	case N.NetworkTCP:
		h.logger.InfoContext(ctx, "outbound connection to ", destination)
		if h.fallbackToH2 {
			return h.observeTunnel(func() (net.Conn, error) {
				return h.dialWithH3Fallback(ctx, destination)
			})
		}
		// dialNaiveTunnel completes the CONNECT handshake, so a 407 or a failed
		// first hop is a DialContext error with adapter.FailureFacts attached,
		// not a connection that closes on its first read. See failure.go.
		return h.observeTunnel(func() (net.Conn, error) {
			return dialNaiveTunnel(ctx, h.client, destination)
		})
	case N.NetworkUDP:
		if h.uotClient == nil {
			return nil, E.New("UDP is not supported unless UDP over TCP is enabled")
		}
		h.logger.InfoContext(ctx, "outbound UoT packet connection to ", destination)
		return h.uotClient.DialContext(ctx, network, destination)
	default:
		return nil, E.Extend(N.ErrUnknownNetwork, network)
	}
}

func (h *Outbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	if h.uotClient == nil {
		return nil, E.New("UDP is not supported unless UDP over TCP is enabled")
	}
	return h.uotClient.ListenPacket(ctx, destination)
}

func (h *Outbound) InterfaceUpdated(ctx context.Context) {
	// The fallback window describes the network being left, so it is cleared
	// with the connections it describes: a failure observed on the old network
	// must not send the first CONNECT on the new one straight to HTTP/2.
	h.h3Fallback.reset()
	h.client.CloseAllConnections()
	if h.h2Fallback != nil {
		if fallbackClient := h.h2Fallback.peek(); fallbackClient != nil {
			fallbackClient.CloseAllConnections()
		}
	}
}

func (h *Outbound) Client() *cronet.NaiveClient {
	return h.client
}

type naiveDialer struct {
	*cronet.NaiveClient
	// owner is the outbound this dialer belongs to. It is used for the opt-in
	// H3->H2 retry; a nil owner (tests) means "no fallback".
	owner *Outbound
}

// DialContext is the entry the UoT client dials through. It uses the same
// handshaking tunnel as the TCP path: a UoT session rides inside one CONNECT, so
// a rejected CONNECT must fail here rather than after the session header was
// written into a tunnel that does not exist.
func (d *naiveDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	if N.NetworkName(network) != N.NetworkTCP {
		return nil, E.Extend(N.ErrUnknownNetwork, network)
	}
	if d.owner == nil {
		return dialNaiveTunnel(ctx, d.NaiveClient, destination)
	}
	if d.owner.fallbackToH2 {
		return d.owner.observeTunnel(func() (net.Conn, error) {
			return d.owner.dialWithH3Fallback(ctx, destination)
		})
	}
	return d.owner.observeTunnel(func() (net.Conn, error) {
		return dialNaiveTunnel(ctx, d.NaiveClient, destination)
	})
}

// cronetNaiveClientParams carries everything buildCronetNaiveClientOptions needs.
//
// The fields are unexported and the struct is internal: it exists to make the
// configuration -> Cronet mapping a pure function that a test can call, not to
// become part of any interface.
type cronetNaiveClientParams struct {
	ctx                     context.Context
	logger                  logger.ContextLogger
	serverAddress           M.Socksaddr
	serverName              string
	options                 option.NaiveOutboundOptions
	extraHeaders            map[string]string
	trustedRootCertificates string
	dialer                  N.Dialer
	dnsResolver             cronet.DNSResolverFunc
	echEnabled              bool
	echConfigList           []byte
	echQueryServerName      string
	quicCongestionControl   cronet.QUICCongestionControl
}

// buildCronetNaiveClientOptions maps a validated sing-box configuration onto
// cronet.NaiveClientOptions.
//
// It is a pure function with no side effects, which is what makes the plumbing
// testable: previously this mapping lived inline in NewOutbound, so a test could
// only assert that the option struct held a bool - it could not show that the value
// reached the Cronet constructor at all. The mapping is deliberately kept
// mechanical (no validation, no defaults beyond the switch statements already
// performed by the caller) so that reading it is enough to see every field.
func buildCronetNaiveClientOptions(params cronetNaiveClientParams) cronet.NaiveClientOptions {
	return cronet.NaiveClientOptions{
		Context:       params.ctx,
		Logger:        params.logger,
		ServerAddress: params.serverAddress,
		ServerName:    params.serverName,
		Username:      params.options.Username,
		Password:      params.options.Password,
		// InsecureConcurrency sets how many isolated sessions/pools to use;
		// TestForceSingleEngine sets how many Cronet engines back them. They are
		// independent: cronet-go decides with
		//   singleEngine: TestForceSingleEngine || runtime.GOOS == "ios"
		//   engineCount := 1; if concurrency > 1 && !singleEngine { engineCount = concurrency }
		// so leaving the switch false preserves the upstream macOS layout of N
		// engines.
		InsecureConcurrency:      params.options.InsecureConcurrency,
		TestForceSingleEngine:    params.options.InsecureConcurrencySingleEngine,
		ExtraHeaders:             params.extraHeaders,
		ReceiveWindow:            params.options.ReceiveWindow.Value(),
		TrustedRootCertificates:  params.trustedRootCertificates,
		Dialer:                   params.dialer,
		DNSResolver:              params.dnsResolver,
		ECHEnabled:               params.echEnabled,
		ECHConfigList:            params.echConfigList,
		ECHQueryServerName:       params.echQueryServerName,
		QUIC:                     params.options.QUIC,
		QUICCongestionControl:    params.quicCongestionControl,
		QUICSessionReceiveWindow: params.options.QUICSessionReceiveWindow.Value(),
	}
}

// validateReservedExtraHeaders rejects extra_headers entries that would override a
// Naive control header.
//
// The check delegates to cronet.IsReservedNaiveHeader so the reserved set has
// exactly one definition, shared with the code that builds the CONNECT request.
// Duplicating the list here would let the two drift, which is how the original
// override bug would come back.
func validateReservedExtraHeaders(extraHeaders map[string][]string) error {
	for key := range extraHeaders {
		if cronet.IsReservedNaiveHeader(key) {
			return E.New("extra_headers must not override the reserved Naive control header ",
				key, "; it would change protocol behaviour instead of adding a request header")
		}
	}
	return nil
}
