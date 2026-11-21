package trojan

import (
	"context"
	"errors"
	"net"
	"os"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// Lifecycle, recipe and fallback tests for the deferred Trojan prototype.
//
// # What is asserted here, and why with an observable builder
//
// "The first concurrent demand builds once", "a failed build is retried without leaking", "Close
// during a build publishes nothing" are statements about INTERNAL transitions. Asserting them by
// sleeping and then looking at memory would let a broken implementation pass whenever the scheduler
// happened to be kind, so every test counts builds and records the runtime's own Start/cleanup calls
// through a controlled fake builder. The real protocol is exercised separately against a real TLS
// Trojan server (lazy_connection_test.go); the business path must not be proven by the fake alone.

// lazyTestRuntime is a controlled expansion: it records Start and cleanup, and its DialContext can
// be blocked so a test can place the object in a precise state.
type lazyTestRuntime struct {
	adapter.Outbound
	tag string

	startCalls  atomic.Int32
	cleanupOnce sync.Once
	cleanupRuns atomic.Int32
	dialCalls   atomic.Int32

	startErr error
	// registerCleanup makes Start register a scope cleanup, which is how a real outbound owns its
	// teardown. It is on by default for runtimes built by the helpers below.
	registerCleanup bool
	onDial          func()
	dialErr         error
}

func (r *lazyTestRuntime) Type() string           { return "lazy-test" }
func (r *lazyTestRuntime) Tag() string            { return r.tag }
func (r *lazyTestRuntime) Network() []string      { return []string{"tcp"} }
func (r *lazyTestRuntime) Dependencies() []string { return nil }

func (r *lazyTestRuntime) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	r.startCalls.Add(1)
	if stage == adapter.StartStateInitialize && r.registerCleanup {
		scope.AddNamed("lazy-test["+r.tag+"]", func() error {
			r.cleanupOnce.Do(func() { r.cleanupRuns.Add(1) })
			return nil
		})
	}
	return r.startErr
}

func (r *lazyTestRuntime) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	r.dialCalls.Add(1)
	if r.onDial != nil {
		r.onDial()
	}
	if r.dialErr != nil {
		return nil, r.dialErr
	}
	client, server := net.Pipe()
	_ = server.Close()
	return client, nil
}

func (r *lazyTestRuntime) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("lazy test runtime has no packet listener")
}

// eligibleTrojanOptions is the representative cold subset: a literal server, TLS on, no transport,
// no multiplex.
func eligibleTrojanOptions() *option.TrojanOutboundOptions {
	return &option.TrojanOutboundOptions{
		ServerOptions:               option.ServerOptions{Server: "198.51.100.7", ServerPort: 443},
		Password:                    "cold-node-password",
		OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{TLS: &option.OutboundTLSOptions{Enabled: true, Insecure: true, ServerName: "cold.example"}},
	}
}

func newLazyTest(t *testing.T, builder outbound.LazyBuilder[string]) (*outbound.LazyOutbound[string], *adapter.Scope) {
	t.Helper()
	owner := outbound.NewAdapter("trojan", "lazy-test-node", []string{"tcp"}, nil)
	lazy := outbound.NewLazyOutbound[string](owner, log.NewNOPFactory().NewLogger("lazy-test"), context.Background(), "recipe", builder)
	scope := adapter.NewScope(context.Background(), log.NewNOPFactory().Logger())
	require.NoError(t, lazy.Start(adapter.StartStateInitialize, scope))
	t.Cleanup(func() { _ = scope.Close() })
	return lazy, scope
}

func dialLazy(t *testing.T, lazy *outbound.LazyOutbound[string]) (net.Conn, error) {
	t.Helper()
	return lazy.DialContext(context.Background(), "tcp", M.Socksaddr{})
}

// TestLazyMetadataQueriesDoNotExpand is requirement 4 at the object level: a name, a capability or a
// diagnostic must never build a TLS or transport object.
func TestLazyMetadataQueriesDoNotExpand(t *testing.T) {
	options := eligibleTrojanOptions()
	created, handled, err := deferredTrojanOutbound(context.Background(), nil, log.NewNOPFactory().NewLogger("deferred"), "cold-node", C.TypeTrojan, options, "")
	require.NoError(t, err)
	require.True(t, handled, "this option shape is eligible, so the deferred path must own it")
	deferred := created.(*lazyTrojanOutbound)

	scope := adapter.NewScope(context.Background(), log.NewNOPFactory().Logger())
	for _, stage := range adapter.ListStartStages {
		require.NoError(t, deferred.Start(stage, scope), "stage ", stage.String())
	}

	require.Equal(t, "cold-node", deferred.Tag())
	require.Equal(t, C.TypeTrojan, deferred.Type())
	require.Equal(t, []string{"tcp", "udp"}, deferred.Network())
	require.Empty(t, deferred.Dependencies())
	require.False(t, deferred.MultiplexEnabled(), "the eligible shape has no multiplex")
	ledger := deferred.ResidencyLedger()
	require.False(t, ledger.Answered, "a never-expanded node cannot report pooled objects, and must not build to do so")

	deferred.SetKeepIdleConnections(false)
	deferred.CloseIdleConnections()
	deferred.InterfaceUpdated(context.Background())
	require.False(t, deferred.Evict(), "there is nothing to evict before the first expansion")

	require.Equal(t, 0, deferred.Builds(),
		"no metadata, capability, start stage, scope registration or diagnostic may build the node; "+
			"the first expansion belongs to the first dial")
	expanded, isExpanded := deferred.Built()
	require.False(t, isExpanded)
	require.Nil(t, expanded)
	require.Equal(t, outbound.LazyStateNeverBuilt, deferred.State())

	require.NoError(t, scope.Close())
	require.Equal(t, outbound.LazyStateClosed, deferred.State(), "the scope cleanup owns the teardown")
}

// TestLazyConcurrentFirstDemandBuildsOnce is requirement 6: the first concurrent demand expands the
// node exactly once, and every waiter gets that one runtime.
func TestLazyConcurrentFirstDemandBuildsOnce(t *testing.T) {
	var builds atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	lazy, _ := newLazyTest(t, func(ctx context.Context, recipe string) (adapter.Outbound, error) {
		if builds.Add(1) == 1 {
			close(entered)
		}
		<-release
		return &lazyTestRuntime{tag: "concurrent", registerCleanup: true}, nil
	})

	const demanders = 32
	var waitGroup sync.WaitGroup
	errs := make(chan error, demanders)
	waitGroup.Add(demanders)
	for range demanders {
		go func() {
			defer waitGroup.Done()
			conn, err := dialLazy(t, lazy)
			if err != nil {
				errs <- err
				return
			}
			_ = conn.Close()
		}()
	}
	<-entered
	close(release)
	waitGroup.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, builds.Load(),
		"%d concurrent first demands must run the builder once; a second build would mean every "+
			"node in a cold profile pays its expansion twice", demanders)
	require.Equal(t, 1, lazy.Builds())
	require.Equal(t, outbound.LazyStateReady, lazy.State())
}

// TestLazyBuildFailureIsRetryableAndDoesNotLeak covers the failed-build transition.
func TestLazyBuildFailureIsRetryableAndDoesNotLeak(t *testing.T) {
	var attempts atomic.Int32
	partialRuntime := &lazyTestRuntime{tag: "partial", registerCleanup: true}
	lazy, _ := newLazyTest(t, func(ctx context.Context, recipe string) (adapter.Outbound, error) {
		switch attempts.Add(1) {
		case 1:
			return nil, errors.New("transient build failure")
		case 2:
			// A runtime whose own Start fails: the deferred layer must release whatever that partial
			// start registered, or every failed cold dial would leak a scope's worth of resources.
			partialRuntime.startErr = errors.New("start refused")
			return partialRuntime, nil
		default:
			return &lazyTestRuntime{tag: "healthy", registerCleanup: true}, nil
		}
	})

	_, err := dialLazy(t, lazy)
	require.Error(t, err)
	require.Contains(t, err.Error(), "transient build failure")
	require.Equal(t, 1, lazy.Builds())
	require.Equal(t, outbound.LazyStateNeverBuilt, lazy.State(), "a failed build must return to never-built")

	_, err = dialLazy(t, lazy)
	require.Error(t, err)
	require.Contains(t, err.Error(), "start refused")
	require.Equal(t, 2, lazy.Builds())
	require.Equal(t, outbound.LazyStateNeverBuilt, lazy.State())
	require.EqualValues(t, 1, partialRuntime.startCalls.Load())
	require.EqualValues(t, 1, partialRuntime.cleanupRuns.Load(),
		"the cleanup the partial Start registered must run when the build is discarded")

	conn, err := dialLazy(t, lazy)
	require.NoError(t, err, "a node whose build failed must be retryable")
	_ = conn.Close()
	require.Equal(t, 3, lazy.Builds())
	require.Equal(t, outbound.LazyStateReady, lazy.State())
}

// TestLazyCloseDuringBuildPublishesNothing is requirement 6's late-result rule.
func TestLazyCloseDuringBuildPublishesNothing(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	built := &lazyTestRuntime{tag: "late", registerCleanup: true}
	lazy, _ := newLazyTest(t, func(ctx context.Context, recipe string) (adapter.Outbound, error) {
		close(entered)
		<-release
		return built, nil
	})
	dialResult := make(chan error, 1)
	go func() {
		_, err := dialLazy(t, lazy)
		dialResult <- err
	}()
	<-entered
	require.Equal(t, outbound.LazyStateBuilding, lazy.State())

	require.NoError(t, lazy.Close())
	require.Equal(t, outbound.LazyStateRetiring, lazy.State(),
		"a close during a build retires the object; the in-flight result must not be published")
	close(release)
	require.ErrorIs(t, <-dialResult, os.ErrClosed)
	require.Equal(t, outbound.LazyStateClosed, lazy.State())
	builtObject, isBuilt := lazy.Built()
	require.False(t, isBuilt)
	require.Nil(t, builtObject)
	require.EqualValues(t, 0, built.startCalls.Load(),
		"a runtime built after Close must not be started: nothing can own its resources, so starting "+
			"it would either leak them or publish a live object behind a closed one")
}

// TestLazyScopeOwnsTheExpansion pins the existing Scope contract: the manager's child scope is the
// only owner, and closing it releases the expansion exactly once.
func TestLazyScopeOwnsTheExpansion(t *testing.T) {
	runtime := &lazyTestRuntime{tag: "scoped", registerCleanup: true}
	lazy, scope := newLazyTest(t, func(ctx context.Context, recipe string) (adapter.Outbound, error) {
		return runtime, nil
	})
	conn, err := dialLazy(t, lazy)
	require.NoError(t, err)
	_ = conn.Close()
	require.Equal(t, outbound.LazyStateReady, lazy.State())
	require.EqualValues(t, 0, runtime.cleanupRuns.Load())

	require.NoError(t, scope.Close())
	require.EqualValues(t, 1, runtime.cleanupRuns.Load(), "the scope must run the expansion's cleanup once")
	require.Equal(t, outbound.LazyStateClosed, lazy.State())
	_, err = dialLazy(t, lazy)
	require.ErrorIs(t, err, os.ErrClosed, "a closed node must never build again")
}

// TestLazyEvictionReleasesIdleExpansionAndAllowsRebuild is the eviction/rebuild contract.
func TestLazyEvictionReleasesIdleExpansionAndAllowsRebuild(t *testing.T) {
	var builds atomic.Int32
	lazy, _ := newLazyTest(t, func(ctx context.Context, recipe string) (adapter.Outbound, error) {
		builds.Add(1)
		return &lazyTestRuntime{tag: "evictable", registerCleanup: true}, nil
	})
	conn, err := dialLazy(t, lazy)
	require.NoError(t, err)
	_ = conn.Close()
	_, expanded := lazy.Built()
	require.True(t, expanded)

	require.True(t, lazy.Evict(), "an idle expansion must be evictable")
	require.Equal(t, outbound.LazyStateNeverBuilt, lazy.State(), "eviction returns the node to its recipe")
	_, stillExpanded := lazy.Built()
	require.False(t, stillExpanded)

	conn, err = dialLazy(t, lazy)
	require.NoError(t, err, "an evicted node is rebuilt by the next demand")
	_ = conn.Close()
	require.EqualValues(t, 2, builds.Load())
	require.Equal(t, 2, lazy.Builds())
}

// TestLazyEvictionRefusesDuringAHandoff is the safety half of eviction: an expansion that a dial
// already obtained must not be released underneath it, and neither may one whose connection is still
// open - that is what "the policy only reclaims idle nodes" means at the object level.
func TestLazyEvictionRefusesDuringAHandoff(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	runtime := &lazyTestRuntime{tag: "busy", registerCleanup: true}
	runtime.onDial = func() {
		close(entered)
		<-release
	}
	lazy, _ := newLazyTest(t, func(ctx context.Context, recipe string) (adapter.Outbound, error) {
		return runtime, nil
	})
	type dialOutcome struct {
		conn net.Conn
		err  error
	}
	dialResult := make(chan dialOutcome, 1)
	go func() {
		conn, err := dialLazy(t, lazy)
		dialResult <- dialOutcome{conn: conn, err: err}
	}()
	<-entered
	require.False(t, lazy.Evict(), "a runtime a dial is using is not an idle object")
	require.Equal(t, outbound.LazyStateReady, lazy.State())
	close(release)
	outcome := <-dialResult
	require.NoError(t, outcome.err)
	require.False(t, lazy.Evict(), "an open connection keeps the node in use after the dial returned")
	require.NoError(t, outcome.conn.Close())
	require.True(t, lazy.Evict(), "closing the connection is what makes the expansion idle")
}

// TestLazyRecipeRebuildsValueEqualOptions is the "single owner of recipe-to-runtime" invariant: the
// recipe must not lose, reorder or re-default a single field of an eligible options value, because
// provider change detection compares that value and a difference would rebuild an unchanged node.
func TestLazyRecipeRebuildsValueEqualOptions(t *testing.T) {
	udpFragmentEnabled := true
	corpus := map[string]*option.TrojanOutboundOptions{
		"plain-insecure": {
			ServerOptions:               option.ServerOptions{Server: "198.51.100.7", ServerPort: 443},
			Password:                    "password",
			OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{TLS: &option.OutboundTLSOptions{Enabled: true, Insecure: true, ServerName: "cold.example"}},
		},
		"empty-password": {
			ServerOptions:               option.ServerOptions{Server: "198.51.100.7", ServerPort: 443},
			OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{TLS: &option.OutboundTLSOptions{Enabled: true, Insecure: true}},
		},
		"alpn-and-sni": {
			ServerOptions:               option.ServerOptions{Server: "198.51.100.7", ServerPort: 8443},
			Password:                    "password",
			OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{TLS: &option.OutboundTLSOptions{Enabled: true, ServerName: "edge.example", ALPN: badoption.Listable[string]{"h2", "http/1.1"}, HandshakeTimeout: 3000}},
		},
		"empty-alpn-slice": {
			ServerOptions:               option.ServerOptions{Server: "198.51.100.7", ServerPort: 443},
			Password:                    "password",
			OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{TLS: &option.OutboundTLSOptions{Enabled: true, Insecure: true, ALPN: badoption.Listable[string]{}}},
		},
		"disable-sni": {
			ServerOptions:               option.ServerOptions{Server: "198.51.100.7", ServerPort: 443},
			Password:                    "password",
			OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{TLS: &option.OutboundTLSOptions{Enabled: true, DisableSNI: true, ServerName: "edge.example"}},
		},
		"go-engine": {
			ServerOptions:               option.ServerOptions{Server: "198.51.100.7", ServerPort: 443},
			Password:                    "password",
			OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{TLS: &option.OutboundTLSOptions{Enabled: true, Insecure: true, Engine: C.TLSEngineGo}},
		},
		"dialer-options": {
			ServerOptions: option.ServerOptions{Server: "198.51.100.7", ServerPort: 443},
			Password:      "password",
			DialerOptions: option.DialerOptions{AbstractDialerOptions: option.AbstractDialerOptions{
				ConnectTimeout:       3000,
				TCPFastOpen:          true,
				TCPMultiPath:         true,
				DisableTCPKeepAlive:  true,
				TCPKeepAlive:         7000,
				TCPKeepAliveInterval: 8000,
				ReuseAddr:            true,
				ProtectPath:          "/tmp/protect.sock",
				UDPFragment:          &udpFragmentEnabled,
				DomainStrategy:       option.DomainStrategy(C.DomainStrategyPreferIPv4),
			}},
			OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{TLS: &option.OutboundTLSOptions{Enabled: true, Insecure: true}},
		},
		"multiplex-disabled": {
			ServerOptions:               option.ServerOptions{Server: "198.51.100.7", ServerPort: 443},
			Password:                    "password",
			Multiplex:                   &option.OutboundMultiplexOptions{Enabled: false, Protocol: "smux", MaxConnections: 2, Brutal: &option.BrutalOptions{UpMbps: 3}},
			OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{TLS: &option.OutboundTLSOptions{Enabled: true, Insecure: true}},
		},
		"domain-server": {
			ServerOptions:               option.ServerOptions{Server: "cold.example", ServerPort: 443},
			Password:                    "password",
			OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{TLS: &option.OutboundTLSOptions{Enabled: true, Insecure: true, ServerName: "cold.example"}},
		},
		"network-tcp": {
			ServerOptions:               option.ServerOptions{Server: "198.51.100.7", ServerPort: 443},
			Password:                    "password",
			Network:                     "tcp",
			OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{TLS: &option.OutboundTLSOptions{Enabled: true, Insecure: true}},
		},
	}
	for name, options := range corpus {
		t.Run(name, func(t *testing.T) {
			eligible, reason := LazyEligible(context.Background(), options)
			require.True(t, eligible, "shape must be eligible, reason: ", reason)
			recipe, recipeReason := newLazyRecipe(context.Background(), options)
			require.NotNil(t, recipe, recipeReason)
			rebuilt := recipe.outboundOptions()
			require.True(t, reflect.DeepEqual(*options, rebuilt),
				"the recipe must rebuild a VALUE-EQUAL options struct; got %#v want %#v", rebuilt, *options)
		})
	}
}

// TestLazyFallsBackForUnsupportedShapes is the "restricted path" statement: the deferred layer owns
// only the shapes it can prove, and everything else keeps the eager constructor and its error stage.
func TestLazyFallsBackForUnsupportedShapes(t *testing.T) {
	base := func() *option.TrojanOutboundOptions {
		return &option.TrojanOutboundOptions{
			ServerOptions:               option.ServerOptions{Server: "198.51.100.7", ServerPort: 443},
			Password:                    "password",
			OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{TLS: &option.OutboundTLSOptions{Enabled: true, Insecure: true}},
		}
	}
	corpus := map[string]func(*option.TrojanOutboundOptions){
		"transport": func(o *option.TrojanOutboundOptions) { o.Transport = &option.V2RayTransportOptions{Type: "ws"} },
		"multiplex": func(o *option.TrojanOutboundOptions) { o.Multiplex = &option.OutboundMultiplexOptions{Enabled: true} },
		"utls": func(o *option.TrojanOutboundOptions) {
			o.TLS.UTLS = &option.OutboundUTLSOptions{Enabled: true, Fingerprint: "chrome"}
		},
		"reality":          func(o *option.TrojanOutboundOptions) { o.TLS.Reality = &option.OutboundRealityOptions{Enabled: true} },
		"ech":              func(o *option.TrojanOutboundOptions) { o.TLS.ECH = &option.OutboundECHOptions{Enabled: true} },
		"certificate-path": func(o *option.TrojanOutboundOptions) { o.TLS.CertificatePath = "ca.pem" },
		"min-version":      func(o *option.TrojanOutboundOptions) { o.TLS.MinVersion = "1.3" },
		"fragment":         func(o *option.TrojanOutboundOptions) { o.TLS.Fragment = true },
		"ktls":             func(o *option.TrojanOutboundOptions) { o.TLS.KernelTx = true },
		"tls-disabled":     func(o *option.TrojanOutboundOptions) { o.TLS.Enabled = false },
		"detour":           func(o *option.TrojanOutboundOptions) { o.Detour = "proxy" },
		"bind-interface":   func(o *option.TrojanOutboundOptions) { o.BindInterface = "en0" },
		"routing-mark":     func(o *option.TrojanOutboundOptions) { o.RoutingMark = 1 },
		"domain-resolver": func(o *option.TrojanOutboundOptions) {
			o.DomainResolver = &option.DomainResolveOptions{Server: "dns"}
		},
		"network-strategy": func(o *option.TrojanOutboundOptions) {
			strategy := option.NetworkStrategy(0)
			o.NetworkStrategy = &strategy
		},
		"server-empty":    func(o *option.TrojanOutboundOptions) { o.Server = "" },
		"destination-dns": func(o *option.TrojanOutboundOptions) { o.DestinationDNSOwnership = true },
	}
	for name, mutate := range corpus {
		t.Run(name, func(t *testing.T) {
			options := base()
			mutate(options)
			eligible, reason := LazyEligible(context.Background(), options)
			require.False(t, eligible, "this shape must keep the eager constructor")
			require.NotEmpty(t, reason)
			_, handled, err := deferredTrojanOutbound(context.Background(), nil, log.NewNOPFactory().NewLogger("t"), "node", C.TypeTrojan, options, "")
			require.NoError(t, err)
			require.False(t, handled, "the fallback must be explicit: handled=false leaves the eager path intact")
		})
	}
	// A different protocol is never touched by this constructor.
	_, handled, err := deferredTrojanOutbound(context.Background(), nil, log.NewNOPFactory().NewLogger("t"), "node", "vless", base(), "")
	require.NoError(t, err)
	require.False(t, handled)
}

// TestLazyNeverDefersAConfigurationError is the error-stage invariant: if eager construction fails,
// the deferred path must NOT own the node, because that would move the error from configuration
// time to the first dial.
func TestLazyNeverDefersAConfigurationError(t *testing.T) {
	cases := map[string]*option.TrojanOutboundOptions{
		"bad-min-version": {
			ServerOptions:               option.ServerOptions{Server: "198.51.100.7", ServerPort: 443},
			Password:                    "password",
			OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{TLS: &option.OutboundTLSOptions{Enabled: true, MinVersion: "9.9"}},
		},
		"unknown-cipher": {
			ServerOptions:               option.ServerOptions{Server: "198.51.100.7", ServerPort: 443},
			Password:                    "password",
			OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{TLS: &option.OutboundTLSOptions{Enabled: true, CipherSuites: []string{"NOT-A-CIPHER"}}},
		},
		"missing-certificate": {
			ServerOptions:               option.ServerOptions{Server: "198.51.100.7", ServerPort: 443},
			Password:                    "password",
			OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{TLS: &option.OutboundTLSOptions{Enabled: true, CertificatePath: "does-not-exist.pem"}},
		},
		"unknown-transport": {
			ServerOptions:               option.ServerOptions{Server: "198.51.100.7", ServerPort: 443},
			Password:                    "password",
			Transport:                   &option.V2RayTransportOptions{Type: "not-a-transport"},
			OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{TLS: &option.OutboundTLSOptions{Enabled: true, Insecure: true}},
		},
	}
	for name, options := range cases {
		t.Run(name, func(t *testing.T) {
			_, eagerErr := NewOutbound(context.Background(), nil, log.NewNOPFactory().NewLogger("eager"), "node", *options)
			require.Error(t, eagerErr, "the fixture must be a configuration the eager path refuses")
			recipe, reason := newLazyRecipe(context.Background(), options)
			require.Nil(t, recipe, "a node whose eager construction fails must never be deferred")
			require.NotEmpty(t, reason)
			_, handled, err := deferredTrojanOutbound(context.Background(), nil, log.NewNOPFactory().NewLogger("t"), "node", C.TypeTrojan, options, "")
			require.NoError(t, err)
			require.False(t, handled)
		})
	}
}

// TestLazyRejectsASecondExpansionRace makes sure the "ready" path is the only one that hands out a
// runtime, and that a Close racing the hand-off is visible as a closed node rather than a nil one.
func TestLazyCloseAfterReadyIsTerminal(t *testing.T) {
	runtime := &lazyTestRuntime{tag: "terminal", registerCleanup: true}
	lazy, _ := newLazyTest(t, func(ctx context.Context, recipe string) (adapter.Outbound, error) {
		return runtime, nil
	})
	conn, err := dialLazy(t, lazy)
	require.NoError(t, err)
	_ = conn.Close()
	require.NoError(t, lazy.Close())
	require.Equal(t, outbound.LazyStateClosed, lazy.State())
	require.NoError(t, lazy.Close(), "Close must be idempotent")
	require.EqualValues(t, 1, runtime.cleanupRuns.Load())
	_, err = dialLazy(t, lazy)
	require.ErrorIs(t, err, os.ErrClosed)
	require.Equal(t, 1, lazy.Builds(), "a closed node must not re-expand")
}

// testHeapLive is live heap after two forced collections.
func testHeapLive() int64 {
	runtime.GC()
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return int64(stats.HeapAlloc)
}

// TestLazyEvictionChurnDoesNotLeak is the leak statement for the evict/rebuild cycle: a policy that
// evicts idle expansions would run this thousands of times, and a build scope or lifetime context
// retained per cycle would be invisible in a single-cycle test.
func TestLazyEvictionChurnDoesNotLeak(t *testing.T) {
	lazy, _ := newLazyTest(t, func(ctx context.Context, recipe string) (adapter.Outbound, error) {
		return &lazyTestRuntime{tag: "churn", registerCleanup: true}, nil
	})
	// Warm up the allocator and the test's own state before the baseline.
	conn, err := dialLazy(t, lazy)
	require.NoError(t, err)
	_ = conn.Close()
	require.True(t, lazy.Evict())
	before := testHeapLive()
	const cycles = 200
	for range cycles {
		conn, dialErr := dialLazy(t, lazy)
		require.NoError(t, dialErr)
		_ = conn.Close()
		require.True(t, lazy.Evict())
	}
	after := testHeapLive()
	t.Logf("MEASURED evict/rebuild churn: %d cycles, live heap delta %+d bytes, builds %d",
		cycles, after-before, lazy.Builds())
	require.LessOrEqual(t, after-before, int64(256<<10),
		"evict/rebuild churn retained %d bytes over %d cycles: a per-build scope or lifetime context "+
			"that eviction does not release would grow without bound", after-before, cycles)
	require.Equal(t, cycles+1, lazy.Builds())
}

// TestLazyConcurrentCloseAndDial does not assert a particular winner; it asserts the state machine
// leaks nothing and never returns a runtime it then tears down under the caller. It is also the
// race-detector target for the Close/acquire window.
func TestLazyConcurrentCloseAndDial(t *testing.T) {
	for range 50 {
		runtime := &lazyTestRuntime{tag: "race", registerCleanup: true}
		lazy, _ := newLazyTest(t, func(ctx context.Context, recipe string) (adapter.Outbound, error) {
			return runtime, nil
		})
		var waitGroup sync.WaitGroup
		waitGroup.Add(2)
		go func() {
			defer waitGroup.Done()
			conn, err := dialLazy(t, lazy)
			if err == nil {
				_ = conn.Close()
			}
		}()
		go func() {
			defer waitGroup.Done()
			_ = lazy.Close()
		}()
		waitGroup.Wait()
		require.LessOrEqual(t, runtime.cleanupRuns.Load(), int32(1), "teardown must run at most once")
		require.Equal(t, outbound.LazyStateClosed, lazy.State(),
			"once Close and the last demand have both returned, the node is terminal")
		if built, isBuilt := lazy.Built(); isBuilt {
			require.Nil(t, built, "a closed node must not publish an expansion")
		}
	}
}
