package trojan

import (
	"context"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/dialer"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"
)

// Deferred (lazy) Trojan expansion.
//
// # Scope, stated honestly
//
// This is a PROTOTYPE for ONE light protocol. It covers an option shape it can prove is expandable
// without any error moving stages, and hands EVERY other shape back to the eager constructor
// (handled=false) - transports, multiplex, uTLS/REALITY/ECH, certificates, TLS fragmentation, kTLS,
// port binding, routing marks, netns, network strategy, an explicit domain resolver. Those fall back
// because their construction can fail or load resources at construction time, and moving such an
// error from configuration time to first dial is exactly the contract this prototype must not break.
// Nothing here claims coverage of other protocols or of the unsupported options.
//
// # Why the recipe is a copy and not a wrapper around *option.TrojanOutboundOptions
//
// Keeping the original options pointer would keep the whole decoded tree alive (TLS container,
// transport options, multiplex options, dialer option pointers), so most of the measured cold cost
// would remain and only the wrapper would be new. The recipe below copies every field the eligible
// shape can carry into plain values, owns the slices and pointers it keeps, and never stores the
// options tree. `outboundOptions` rebuilds a value-equal options struct; a test pins that equality
// for the eligible corpus, so the recipe cannot silently drift from the eager shape.
//
// # What is deliberately NOT shared
//
// Two nodes with the same server, password and TLS settings do NOT share one expansion. Sharing
// needs a key with the real scope (Box + Provider), the configuration semantics and a generation in
// it, and an owner-refcounted release; a key that is only "same config" would let one provider's
// teardown close another's live node. The recipe is cheap enough that the sharing question can stay
// open without affecting the per-node saving (see the report's remaining-items list).

// lazyDialerRecipe is the owned copy of the dialer options the eligible shape may carry.
type lazyDialerRecipe struct {
	connectTimeout             badoption.Duration
	tcpFastOpen                bool
	tcpMultiPath               bool
	disableTCPKeepAlive        bool
	tcpKeepAlive               badoption.Duration
	tcpKeepAliveInterval       badoption.Duration
	tcpKeepAliveSystemDefaults bool
	reuseAddr                  bool
	protectPath                string
	udpBindPort                uint16
	udpFragment                *bool
	udpFragmentDefault         bool
	domainStrategy             option.DomainStrategy
}

// lazyRecipe is the immutable description of one Trojan node.
type lazyRecipe struct {
	server     string
	serverPort uint16
	password   string
	network    option.NetworkList

	tlsEngine           string
	tlsDisableSNI       bool
	tlsServerName       string
	tlsInsecure         bool
	tlsALPN             badoption.Listable[string]
	tlsHandshakeTimeout badoption.Duration

	multiplex *option.OutboundMultiplexOptions

	dialer lazyDialerRecipe
}

// lazyTrojanOutbound adds the Trojan capability surface to the generic deferred object.
//
// Every method answers WITHOUT expanding the node: the capability facts are decided from the
// recipe, and a query for a capability must never build a TLS or transport object. When an
// expansion exists, the forwarding methods take a hand-off reference through WithRuntime so an
// eviction cannot release it underneath the call.
type lazyTrojanOutbound struct {
	*outbound.LazyOutbound[lazyRecipe]
}

var (
	_ adapter.Outbound                = (*lazyTrojanOutbound)(nil)
	_ adapter.Lifecycle               = (*lazyTrojanOutbound)(nil)
	_ adapter.OutboundWithMultiplex   = (*lazyTrojanOutbound)(nil)
	_ adapter.InterfaceUpdateListener = (*lazyTrojanOutbound)(nil)
	_ adapter.IdleConnectionKeeper    = (*lazyTrojanOutbound)(nil)
	_ adapter.ResidencyLedgerReporter = (*lazyTrojanOutbound)(nil)
)

// MultiplexEnabled answers from the recipe: the eligible shape has no multiplex, which is one of
// the reasons it is eligible. It never expands the node.
func (l *lazyTrojanOutbound) MultiplexEnabled() bool {
	return false
}

// InterfaceUpdated forwards the network-change action to an existing expansion, and does nothing
// when there is none: a node built after the transition creates its generation gate against the
// CURRENT epoch, so there is nothing stale to reset.
func (l *lazyTrojanOutbound) InterfaceUpdated(ctx context.Context) {
	l.WithRuntime(func(runtime adapter.Outbound) {
		if listener, isListener := runtime.(adapter.InterfaceUpdateListener); isListener {
			listener.InterfaceUpdated(ctx)
		}
	})
}

// SetKeepIdleConnections records the reference manager's answer for future expansions and forwards
// it to the current one. It must not expand the node.
func (l *lazyTrojanOutbound) SetKeepIdleConnections(keep bool) {
	l.SetIdleConnectionsPolicy(keep)
	l.WithRuntime(func(runtime adapter.Outbound) {
		if keeper, isKeeper := runtime.(adapter.IdleConnectionKeeper); isKeeper {
			keeper.SetKeepIdleConnections(keep)
		}
	})
}

// CloseIdleConnections forwards the trim action. A never-built node holds no pooled object, so the
// correct answer is a no-op rather than an expansion.
func (l *lazyTrojanOutbound) CloseIdleConnections() {
	l.WithRuntime(func(runtime adapter.Outbound) {
		if keeper, isKeeper := runtime.(adapter.IdleConnectionKeeper); isKeeper {
			keeper.CloseIdleConnections()
		}
	})
}

// ResidencyLedger reports an honest "unknown" for a node that was never expanded: it holds no
// pooled object yet, and constructing one to answer a diagnostic would defeat the object layer.
func (l *lazyTrojanOutbound) ResidencyLedger() adapter.ResidencyLedger {
	var ledger adapter.ResidencyLedger
	expanded := l.WithRuntime(func(runtime adapter.Outbound) {
		if reporter, isReporter := runtime.(adapter.ResidencyLedgerReporter); isReporter {
			ledger = reporter.ResidencyLedger()
			return
		}
		ledger = adapter.ResidencyLedger{
			Answered: false,
			Note:     "the expanded outbound exposes no object ledger",
		}
	})
	if expanded {
		return ledger
	}
	return adapter.ResidencyLedger{
		Answered: false,
		Note:     "lazy trojan node: not expanded, so it owns no pooled object to report",
	}
}

// DeferredConstructor is the manager seam for the Trojan prototype.
func DeferredConstructor() outbound.DeferredOutboundConstructor {
	return deferredTrojanOutbound
}

// deferredTrojanOutbound is the outbound.DeferredOutboundConstructor implementation.
//
// `owner` is accepted but not used here: this prototype covers the option SHAPE, and the same
// recipe is correct for a static node and a provider node. A product that wants to restrict the
// deferred path to provider-fed nodes can wrap This constructor and answer handled=false for
// owner==""; the manager passes the real scope, so the wrapper does not have to guess from the tag.
func deferredTrojanOutbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, outboundType string, options any, owner string) (adapter.Outbound, bool, error) {
	if outboundType != C.TypeTrojan {
		return nil, false, nil
	}
	trojanOptions, isTrojan := options.(*option.TrojanOutboundOptions)
	if !isTrojan || trojanOptions == nil {
		return nil, false, nil
	}
	recipe, _ := newLazyRecipe(ctx, trojanOptions)
	if recipe == nil {
		// The eager constructor keeps this shape - including whichever stage reports its errors.
		return nil, false, nil
	}
	adapterOwner := outbound.NewAdapterWithDialerOptions(C.TypeTrojan, tag, trojanOptions.Network.Build(), trojanOptions.DialerOptions, trojanOptions.ServerIsDomain())
	deferred := outbound.NewLazyOutbound[lazyRecipe](adapterOwner, logger, ctx, *recipe, func(buildContext context.Context, buildRecipe lazyRecipe) (adapter.Outbound, error) {
		// The SAME constructor the eager path uses. One owner of recipe-to-runtime behaviour.
		return NewOutbound(buildContext, router, logger, tag, buildRecipe.outboundOptions())
	})
	return &lazyTrojanOutbound{LazyOutbound: deferred}, true, nil
}

// LazyEligible reports whether one Trojan option shape is covered by the deferred path, and if not,
// a short stable reason. It is the diagnostic a management view uses to say how much of a
// subscription the restricted path actually reaches; it constructs nothing and dials nothing.
func LazyEligible(ctx context.Context, options *option.TrojanOutboundOptions) (bool, string) {
	recipe, reason := newLazyRecipe(ctx, options)
	return recipe != nil, reason
}

// newLazyRecipe validates the options and copies them into an owned recipe. A nil recipe means the
// eager constructor must keep this node; the reason is for diagnostics only and has no control flow.
//
// # Why this is allowed to be a pre-check rather than a second constructor
//
// Every field this function accepts is one whose construction path cannot return an error and does
// not load a file, a socket or a goroutine:
//
//   - TLS: the std engine with no certificate material, no pinned hash, no version/cipher override,
//     no uTLS/REALITY/ECH and no fragmentation. `newSTDClient` can only fail for one of those, and
//     it is a pure in-memory build for the accepted shape (`Server != ""` below keeps its
//     missing-server-name error out of reach).
//   - Dialer: only fields that `NewDefault` reads without returning an error. `bind_interface`,
//     `routing_mark`, `bind_address_no_port`, `netns`, `network_strategy` and an explicit
//     `domain_resolver` are exactly the ones that can fail, so they fall back.
//   - A DOMAIN server additionally runs `dialer.ResolveAuthority` here. That function is documented
//     as a pure read; if it fails, the eager path must report the error, so this node falls back
//     rather than deferring a configuration error to the first dial.
//   - Transport and multiplex are excluded outright: their constructors allocate pooled state and
//     validate protocol/fingerprint values.
func newLazyRecipe(ctx context.Context, options *option.TrojanOutboundOptions) (*lazyRecipe, string) {
	if options == nil {
		return nil, "nil-options"
	}
	if options.Transport != nil {
		return nil, "transport"
	}
	if options.Multiplex != nil && options.Multiplex.Enabled {
		return nil, "multiplex"
	}
	tlsOptions := options.TLS
	if tlsOptions == nil || !tlsOptions.Enabled {
		return nil, "tls-disabled"
	}
	if tlsOptions.Engine != "" && tlsOptions.Engine != C.TLSEngineGo {
		return nil, "tls-engine"
	}
	if tlsOptions.Reality != nil || tlsOptions.UTLS != nil || tlsOptions.ECH != nil {
		return nil, "tls-exotic"
	}
	if len(tlsOptions.Certificate) > 0 || tlsOptions.CertificatePath != "" ||
		len(tlsOptions.CertificateSHA256) > 0 || len(tlsOptions.CertificatePublicKeySHA256) > 0 {
		return nil, "tls-certificate"
	}
	if len(tlsOptions.ClientCertificate) > 0 || tlsOptions.ClientCertificatePath != "" ||
		len(tlsOptions.ClientKey) > 0 || tlsOptions.ClientKeyPath != "" {
		return nil, "tls-client-certificate"
	}
	if tlsOptions.MinVersion != "" || tlsOptions.MaxVersion != "" ||
		tlsOptions.CipherSuites != nil || tlsOptions.CurvePreferences != nil {
		return nil, "tls-version-or-ciphers"
	}
	if tlsOptions.Fragment || tlsOptions.FragmentFallbackDelay != 0 || tlsOptions.RecordFragment ||
		tlsOptions.Spoof != "" || tlsOptions.SpoofMethod != "" {
		return nil, "tls-fragment-or-spoof"
	}
	if tlsOptions.KernelTx || tlsOptions.KernelRx {
		return nil, "tls-ktls"
	}
	if options.Server == "" {
		return nil, "server-empty"
	}
	dialerOptions := options.DialerOptions
	if dialerOptions.Detour != "" ||
		dialerOptions.BindInterface != "" ||
		dialerOptions.Inet4BindAddress != nil ||
		dialerOptions.Inet6BindAddress != nil ||
		dialerOptions.BindAddressNoPort ||
		dialerOptions.RoutingMark != 0 ||
		dialerOptions.NetNs != "" ||
		dialerOptions.DomainResolver != nil ||
		dialerOptions.NetworkStrategy != nil ||
		len(dialerOptions.NetworkType) > 0 ||
		len(dialerOptions.FallbackNetworkType) > 0 ||
		dialerOptions.FallbackDelay != 0 ||
		dialerOptions.DestinationDNSOwnership {
		return nil, "dialer-options"
	}
	if options.ServerIsDomain() {
		// The dialer that NewOutbound builds for a domain server reads the DNS authority. The only
		// error this can report with an implicit resolver is a default resolver that names a missing
		// transport; that is a configuration error and must be reported by the eager constructor at
		// this same stage, so the node falls back instead of deferring it.
		if _, authorityErr := dialer.ResolveAuthority(ctx, nil, false); authorityErr != nil {
			return nil, "domain-resolver"
		}
	}
	recipe := &lazyRecipe{
		server:              options.Server,
		serverPort:          options.ServerPort,
		password:            options.Password,
		network:             options.Network,
		tlsEngine:           tlsOptions.Engine,
		tlsDisableSNI:       tlsOptions.DisableSNI,
		tlsServerName:       tlsOptions.ServerName,
		tlsInsecure:         tlsOptions.Insecure,
		tlsALPN:             copyListable(tlsOptions.ALPN),
		tlsHandshakeTimeout: tlsOptions.HandshakeTimeout,
		multiplex:           copyMultiplex(options.Multiplex),
		dialer: lazyDialerRecipe{
			connectTimeout:             dialerOptions.ConnectTimeout,
			tcpFastOpen:                dialerOptions.TCPFastOpen,
			tcpMultiPath:               dialerOptions.TCPMultiPath,
			disableTCPKeepAlive:        dialerOptions.DisableTCPKeepAlive,
			tcpKeepAlive:               dialerOptions.TCPKeepAlive,
			tcpKeepAliveInterval:       dialerOptions.TCPKeepAliveInterval,
			tcpKeepAliveSystemDefaults: dialerOptions.TCPKeepAliveSystemDefaults,
			reuseAddr:                  dialerOptions.ReuseAddr,
			protectPath:                dialerOptions.ProtectPath,
			udpBindPort:                dialerOptions.UDPBindPort,
			udpFragment:                copyBoolPtr(dialerOptions.UDPFragment),
			udpFragmentDefault:         dialerOptions.UDPFragmentDefault,
			domainStrategy:             dialerOptions.DomainStrategy,
		},
	}
	return recipe, ""
}

// outboundOptions rebuilds the eager constructor's input from the recipe.
//
// It must be VALUE-EQUAL to the original options for every eligible shape: provider change
// detection compares the stored options with the new document, and a recipe that dropped or
// reordered a field would make an unchanged node look changed (or, worse, make a changed node look
// unchanged). lazy_test.go's corpus test pins this with reflect.DeepEqual.
func (r lazyRecipe) outboundOptions() option.TrojanOutboundOptions {
	return option.TrojanOutboundOptions{
		DialerOptions: option.DialerOptions{
			AbstractDialerOptions: option.AbstractDialerOptions{
				ConnectTimeout:             r.dialer.connectTimeout,
				TCPFastOpen:                r.dialer.tcpFastOpen,
				TCPMultiPath:               r.dialer.tcpMultiPath,
				DisableTCPKeepAlive:        r.dialer.disableTCPKeepAlive,
				TCPKeepAlive:               r.dialer.tcpKeepAlive,
				TCPKeepAliveInterval:       r.dialer.tcpKeepAliveInterval,
				TCPKeepAliveSystemDefaults: r.dialer.tcpKeepAliveSystemDefaults,
				ReuseAddr:                  r.dialer.reuseAddr,
				ProtectPath:                r.dialer.protectPath,
				UDPBindPort:                r.dialer.udpBindPort,
				UDPFragment:                copyBoolPtr(r.dialer.udpFragment),
				UDPFragmentDefault:         r.dialer.udpFragmentDefault,
				DomainStrategy:             r.dialer.domainStrategy,
			},
		},
		ServerOptions: option.ServerOptions{
			Server:     r.server,
			ServerPort: r.serverPort,
		},
		Password: r.password,
		Network:  r.network,
		OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{
			TLS: &option.OutboundTLSOptions{
				Enabled:          true,
				Engine:           r.tlsEngine,
				DisableSNI:       r.tlsDisableSNI,
				ServerName:       r.tlsServerName,
				Insecure:         r.tlsInsecure,
				ALPN:             copyListable(r.tlsALPN),
				HandshakeTimeout: r.tlsHandshakeTimeout,
			},
		},
		Multiplex: copyMultiplex(r.multiplex),
	}
}

func copyListable(source badoption.Listable[string]) badoption.Listable[string] {
	if source == nil {
		return nil
	}
	copied := make(badoption.Listable[string], len(source))
	copy(copied, source)
	return copied
}

func copyMultiplex(source *option.OutboundMultiplexOptions) *option.OutboundMultiplexOptions {
	if source == nil {
		return nil
	}
	copied := *source
	if source.Brutal != nil {
		brutal := *source.Brutal
		copied.Brutal = &brutal
	}
	return &copied
}

func copyBoolPtr(source *bool) *bool {
	if source == nil {
		return nil
	}
	copied := *source
	return &copied
}
