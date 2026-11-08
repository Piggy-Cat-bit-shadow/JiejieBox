package route

import (
	"context"
	"slices"
	"sync/atomic"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/power"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/experimental/clashmode"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/observable"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/pause"
)

var _ adapter.LifecycleService = (*ReferenceManager)(nil)

type ReferenceManager struct {
	ctx                    context.Context
	logger                 log.ContextLogger
	rules                  []option.Rule
	dnsRules               []option.DNSRule
	staticOutbounds        []string
	staticTransports       []string
	subscriber             *observable.Subscriber[struct{}]
	pauseManager           pause.Manager
	devicePaused           atomic.Bool
	keepIdle               map[any]bool
	unreferencedTransports map[string]bool
	// powerGovernor is the sleep authority, and the source of the reuse epoch. It is nil in a build
	// with no governor installed, in which case a resume boundary does not exist and nothing here
	// changes - the same zero-configuration rule the rest of the power wiring follows.
	powerGovernor *power.Governor
	// closed makes a late reuse boundary a no-op. The governor is closed before the scope that owns
	// this object, so a boundary can still be in flight while the pools it would retire are being
	// torn down; retiring is idempotent, but reaching into a half-closed manager is not something to
	// leave to chance. Close wins: after this is set, no boundary is acted on and nothing is woken.
	closed atomic.Bool
}

func NewReferenceManager(ctx context.Context, logger log.ContextLogger, options option.Options) *ReferenceManager {
	var (
		staticOutbounds  []string
		staticTransports []string
	)
	if options.NTP != nil && options.NTP.Enabled && options.NTP.Detour != "" {
		staticOutbounds = append(staticOutbounds, options.NTP.Detour)
	}
	for _, outboundOptions := range options.Outbounds {
		staticTransports = appendDomainResolver(staticTransports, outboundOptions.Options)
	}
	for _, endpointOptions := range options.Endpoints {
		staticTransports = appendDomainResolver(staticTransports, endpointOptions.Options)
	}
	var (
		rules    []option.Rule
		dnsRules []option.DNSRule
	)
	if options.Route != nil {
		rules = options.Route.Rules
	}
	if options.DNS != nil {
		dnsRules = options.DNS.Rules
	}
	return &ReferenceManager{
		ctx:              ctx,
		logger:           logger,
		rules:            rules,
		dnsRules:         dnsRules,
		staticOutbounds:  staticOutbounds,
		staticTransports: staticTransports,
		pauseManager:     service.FromContext[pause.Manager](ctx),
		// The governor is a service so that a component asks it instead of inventing a second epoch;
		// the router does the same for traffic observation.
		powerGovernor: service.FromContext[*power.Governor](ctx),
	}
}

func appendDomainResolver(transports []string, rawOptions any) []string {
	dialerOptionsWrapper, isDialerOptionsWrapper := rawOptions.(option.DialerOptionsWrapper)
	if !isDialerOptionsWrapper {
		return transports
	}
	dialerOptions := dialerOptionsWrapper.TakeDialerOptions()
	if dialerOptions.DomainResolver == nil || dialerOptions.DomainResolver.Server == "" {
		return transports
	}
	return append(transports, dialerOptions.DomainResolver.Server)
}

func (m *ReferenceManager) Name() string {
	return "reference manager"
}

func (m *ReferenceManager) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	if stage != adapter.StartStateStarted {
		return nil
	}
	m.subscriber = observable.NewSubscriber[struct{}](1)
	scope.Add(m.subscriber.Close)
	history := service.PtrFromContext[urltest.HistoryStorage](m.ctx)
	if history != nil {
		history.AddUpdateHook(m.subscriber)
	}
	clashMode := service.PtrFromContext[clashmode.Manager](m.ctx)
	if clashMode != nil {
		clashMode.AddUpdateHook(m.subscriber)
	}
	if m.pauseManager != nil {
		m.devicePaused.Store(m.pauseManager.IsDevicePaused())
	}
	m.update()
	go m.loop()
	if m.pauseManager != nil {
		pauseCallback := m.pauseManager.RegisterCallback(func(event int) {
			switch event {
			case pause.EventDevicePaused:
				m.devicePaused.Store(true)
			case pause.EventDeviceWake:
				m.devicePaused.Store(false)
			default:
				return
			}
			m.subscriber.Emit(struct{}{})
		})
		scope.Add(func() error {
			m.pauseManager.UnregisterCallback(pauseCallback)
			return nil
		})
	}
	// The reuse boundary is the OTHER half of the sleep story, and it is not the pause callback above.
	//
	// The pause callback is about eligibility: which outbound may keep an idle connection, which
	// on-demand tunnel is suspended. It runs for a device pause and a device wake, and on the Apple
	// client the wake may never arrive at all. The reuse boundary is about TRUST: a sleep ended, so
	// reusable state that predates it has not been verified since, and the first flow after the
	// boundary must not be the thing that discovers a blackholed socket. It arrives on a platform
	// resume even when the device wake does not.
	if m.powerGovernor != nil {
		m.powerGovernor.AddReuseObserver(m.onReuseBoundary)
	}
	// The OOM report reads the owner inventory through this capability rather than through a second
	// list of managers, so the report and the walks above cannot disagree about who is reachable. It
	// is registered as the interface, not as *ReferenceManager, which is also what keeps
	// service/oomkiller from importing the route package.
	//
	// A context without a service registry is a bare Scope or a unit test; there is no report for it
	// to feed, so the registration is skipped instead of turning a missing registry into a panic.
	if service.RegistryFromContext(m.ctx) != nil {
		service.MustRegister[adapter.ResidencyReporter](m.ctx, m)
	}
	// Registered last so that it is the FIRST cleanup to run: the manager must be marked closed
	// before the pools and managers it reaches are torn down, not after.
	scope.Add(func() error {
		m.closed.Store(true)
		return nil
	})
	return nil
}

// onReuseBoundary is the core's only reaction to a resume boundary, and it is deliberately the
// narrowest one that satisfies the requirement.
//
// What it does: refuses new work on the reusable resources that predate the sleep, and retires the
// ones that are carrying nothing, so the next demand dials a path that is known to be new rather than
// one that merely looks alive. What it does NOT do, and must not be changed to do:
//
//   - it does not reset the network. A reset is reserved for a real network transition, which is a
//     different epoch with a different mechanism (common/runtimecoord); doing it here would kill
//     every active flow, dial everything at once and make the sleep boundary a reconnect storm.
//   - it does not terminate an active stream. A pool that can express "no new work on this, let the
//     work already on it finish" is asked for exactly that (adapter.ReuseSuspect, and today the XHTTP
//     XMUX pool is the one that can). Every other keeper only closes resources with no active user
//     traffic: an HTTP/2 pool closes connections with no in-flight request, a QUIC transport closes
//     only when its stream count is zero, and an in-flight response body holds its epoch open that
//     way by design.
//   - it does not touch the on-demand tunnels. WireGuard, MASQUE, OpenVPN, OpenConnect and the
//     Tailscale endpoint implement SetKeepIdleConnections - a suspend/resume of the tunnel itself,
//     which is not idle-only - and not CloseIdleConnections, so they are not reachable from this walk
//     and are not woken by it. Resuming them here would be a dial with no demand behind it.
//   - it does not dial. Refusing new work and retiring an idle pool cannot start a connection; the
//     next demand does, and that demand was going to dial anyway.
//
// The suspect band retires nothing by policy: it advances the epoch so the distrust is visible and
// monotonic, and leaves the pool alone because a mid-length sleep usually survives and churning it
// would cost a handshake for no correctness gain. Those values live in power.Policy, not here.
func (m *ReferenceManager) onReuseBoundary(boundary power.ReuseBoundary) {
	if m.closed.Load() {
		return
	}
	switch boundary.Action {
	case power.ReuseRetire:
		retired := m.retireSuspectResources()
		if m.logger != nil {
			m.logger.Debug("reuse: epoch ", boundary.Epoch, ", sleep ", boundary.Sleep,
				", retiring idle connections of ", retired, " reusable pool(s); active flows untouched")
		}
	case power.ReuseSuspect:
		if m.logger != nil {
			m.logger.Debug("reuse: epoch ", boundary.Epoch, ", sleep ", boundary.Sleep,
				", reusable state suspect; pools kept by policy")
		}
	}
}

func (m *ReferenceManager) loop() {
	subscription, done := m.subscriber.Subscription()
	for {
		select {
		case <-subscription:
		case <-done:
			return
		}
		m.update()
	}
}

func (m *ReferenceManager) update() {
	var mode string
	clashMode := service.PtrFromContext[clashmode.Manager](m.ctx)
	if clashMode != nil {
		mode = clashMode.Mode()
	}
	outboundManager := service.FromContext[adapter.OutboundManager](m.ctx)
	endpointManager := service.FromContext[adapter.EndpointManager](m.ctx)
	inboundManager := service.FromContext[adapter.InboundManager](m.ctx)
	serviceManager := service.FromContext[adapter.ServiceManager](m.ctx)
	transportManager := service.FromContext[adapter.DNSTransportManager](m.ctx)
	networkManager := service.FromContext[adapter.NetworkManager](m.ctx)
	httpClientManager := service.FromContext[adapter.HTTPClientManager](m.ctx)

	transportQueue := slices.Clone(m.staticTransports)
	outboundQueue := slices.Clone(m.staticOutbounds)
	if !collectDNSRuleReferences(m.dnsRules, mode, &transportQueue) {
		defaultTransport := transportManager.Default()
		if defaultTransport != nil {
			transportQueue = append(transportQueue, defaultTransport.Tag())
		}
	}
	transportQueue = append(transportQueue, networkManager.DefaultOptions().DomainResolver)
	if !collectRuleReferences(m.rules, mode, &outboundQueue, &transportQueue) {
		defaultOutbound := outboundManager.Default()
		if defaultOutbound != nil {
			outboundQueue = append(outboundQueue, defaultOutbound.Tag())
		}
	}
	var onDemandEndpoints []adapter.OnDemandEndpoint
	for _, endpoint := range endpointManager.Endpoints() {
		onDemandEndpoint, isOnDemandEndpoint := endpoint.(adapter.OnDemandEndpoint)
		if isOnDemandEndpoint && onDemandEndpoint.OnDemand() {
			onDemandEndpoints = append(onDemandEndpoints, onDemandEndpoint)
			continue
		}
		outboundQueue = append(outboundQueue, endpoint.Tag())
	}
	for _, inbound := range inboundManager.Inbounds() {
		referrer, isReferrer := inbound.(adapter.Referrer)
		if isReferrer {
			outboundQueue = append(outboundQueue, referrer.References()...)
		}
	}
	for _, boxService := range serviceManager.Services() {
		referrer, isReferrer := boxService.(adapter.Referrer)
		if isReferrer {
			outboundQueue = append(outboundQueue, referrer.References()...)
		}
	}
	referrer, isReferrer := httpClientManager.(adapter.Referrer)
	if isReferrer {
		outboundQueue = append(outboundQueue, referrer.References()...)
	}

	referencedTransports := make(map[string]bool)
	for len(transportQueue) > 0 {
		tag := transportQueue[0]
		transportQueue = transportQueue[1:]
		if tag == "" || referencedTransports[tag] {
			continue
		}
		transport, loaded := transportManager.Transport(tag)
		if !loaded {
			continue
		}
		referencedTransports[tag] = true
		transportQueue = append(transportQueue, transport.Dependencies()...)
		transportReferrer, isTransportReferrer := transport.(adapter.Referrer)
		if isTransportReferrer {
			outboundQueue = append(outboundQueue, transportReferrer.References()...)
		}
	}
	referencedOutbounds := make(map[string]bool)
	for len(outboundQueue) > 0 {
		tag := outboundQueue[0]
		outboundQueue = outboundQueue[1:]
		if tag == "" || referencedOutbounds[tag] {
			continue
		}
		outbound, loaded := outboundManager.Outbound(tag)
		if !loaded {
			continue
		}
		referencedOutbounds[tag] = true
		outboundReferrer, isOutboundReferrer := outbound.(adapter.Referrer)
		if isOutboundReferrer {
			outboundQueue = append(outboundQueue, outboundReferrer.References()...)
		} else {
			outboundQueue = append(outboundQueue, outbound.Dependencies()...)
		}
	}

	devicePaused := m.devicePaused.Load()
	keepIdle := make(map[any]bool)
	for _, outbound := range outboundManager.Outbounds() {
		keeper, isKeeper := outbound.(adapter.IdleConnectionKeeper)
		if !isKeeper {
			continue
		}
		m.applyKeepIdle(keepIdle, idleTarget{
			value:    outbound,
			keeper:   keeper,
			kind:     "outbound/",
			action:   "closing idle connections",
			typeName: outbound.Type(),
			tag:      outbound.Tag(),
			keep:     referencedOutbounds[outbound.Tag()],
		})
	}
	for _, endpoint := range onDemandEndpoints {
		m.applyKeepIdle(keepIdle, idleTarget{
			value:        endpoint,
			keeper:       endpoint,
			kind:         "endpoint/",
			action:       "suspending",
			typeName:     endpoint.Type(),
			tag:          endpoint.Tag(),
			keep:         referencedOutbounds[endpoint.Tag()] && !devicePaused,
			devicePaused: devicePaused,
		})
	}
	unreferencedTransports := make(map[string]bool)
	for _, transport := range transportManager.Transports() {
		tag := transport.Tag()
		referenced := referencedTransports[tag]
		keeper, isKeeper := transport.(adapter.IdleConnectionKeeper)
		if isKeeper {
			m.applyKeepIdle(keepIdle, idleTarget{
				value:    transport,
				keeper:   keeper,
				kind:     "dns/",
				action:   "closing idle connections",
				typeName: transport.Type(),
				tag:      tag,
				keep:     referenced,
			})
			continue
		}
		if referenced {
			continue
		}
		unreferencedTransports[tag] = true
		if m.unreferencedTransports != nil && !m.unreferencedTransports[tag] {
			m.logger.Debug("dns/", transport.Type(), "[", tag, "] is unreferenced, resetting")
			transport.Reset()
		}
	}
	m.keepIdle = keepIdle
	m.unreferencedTransports = unreferencedTransports
}

type idleKeeper interface {
	SetKeepIdleConnections(keep bool)
}

type idleTarget struct {
	value        any
	keeper       idleKeeper
	kind         string
	action       string
	typeName     string
	tag          string
	keep         bool
	devicePaused bool
}

func (m *ReferenceManager) applyKeepIdle(keepIdle map[any]bool, target idleTarget) {
	keepIdle[target.value] = target.keep
	previous, tracked := m.keepIdle[target.value]
	if tracked && previous == target.keep {
		return
	}
	if !target.keep {
		if target.devicePaused {
			m.logger.Debug(target.kind, target.typeName, "[", target.tag, "] device paused, ", target.action)
		} else {
			m.logger.Debug(target.kind, target.typeName, "[", target.tag, "] is unreferenced, ", target.action)
		}
	}
	target.keeper.SetKeepIdleConnections(target.keep)
}

// TrimIdleResources is the progressive memory pass.
//
// # Why it is not CloseIdleConnections
//
// CloseIdleConnections is called from paths that must release everything reusable: DEEP_IDLE, a
// pause, a memory-pressure reset. Those are all points where the device is genuinely not being used
// and the cost of a later handshake is paid once.
//
// Trim is the weaker pass for memory that is merely elevated: it releases what is reusable and
// cheap to lose, and it must not be able to cause a dial. If a trim could start a connection it
// would be a reconnect trigger wearing a memory-management name, which is exactly the shape the
// memory-pressure path must not have.
//
// In this tree the two are the same set of operations, because closing an idle pool cannot dial:
// every keeper drops a connection that has no active user traffic, and the next demand re-dials.
// The distinction that matters is therefore at the CALLER - when to be aggressive - and this method
// exists so that caller has a name for the weaker pass and the stronger one is not silently reused
// for it.
func (m *ReferenceManager) TrimIdleResources() {
	m.retireIdleResources()
}

func (m *ReferenceManager) CloseIdleConnections() {
	m.retireIdleResources()
}

// reusableOwnerKind names one class of owner in the retirement walks. The kind is what the residency
// snapshot reports per owner, and what a log line or a test can name without knowing the interface.
type reusableOwnerKind uint8

const (
	reusableOwnerOutbound reusableOwnerKind = iota
	reusableOwnerEndpoint
	reusableOwnerDNSTransport
	reusableOwnerHTTPClient
)

// eachReusableOwner is the ONE enumeration the two retirement walks and the residency snapshot read.
//
// # Why it is shared
//
// The walks had three hand-written manager lists and one HTTP client special case; adding an owner to
// one list was silently not adding it to the others, which is the shape of the bug that left the HTTP
// client pool unreachable through four reviews (see adapter.IdleConnectionReleaser). The shared walk
// makes the owner set a fact of this one function: the trim, the reuse boundary and the report cannot
// disagree about who can be asked.
//
// The HTTP client manager is visited as its own kind rather than as a fourth list entry because it is
// a single service, not a manager of many. It owns the fourth set of reusable resources - the pooled
// connections behind provider refresh, remote rule sets, the dashboard and the API - and its
// CloseIdleConnections is the right idle-only action (see common/httpclient.Manager for why it is not
// ResetNetwork).
//
// The callback receives `any` because the kind, not a common interface, is what the callers switch on:
// the trim asks for IdleConnectionReleaser, the boundary additionally asks for ReuseSuspect, and the
// snapshot only counts. Asserting a common interface here would be the same "assert the bundled
// capability" mistake in a different place.
func (m *ReferenceManager) eachReusableOwner(visit func(kind reusableOwnerKind, owner any)) {
	if outboundManager := service.FromContext[adapter.OutboundManager](m.ctx); outboundManager != nil {
		for _, outbound := range outboundManager.Outbounds() {
			visit(reusableOwnerOutbound, outbound)
		}
	}
	if endpointManager := service.FromContext[adapter.EndpointManager](m.ctx); endpointManager != nil {
		for _, endpoint := range endpointManager.Endpoints() {
			visit(reusableOwnerEndpoint, endpoint)
		}
	}
	if transportManager := service.FromContext[adapter.DNSTransportManager](m.ctx); transportManager != nil {
		for _, transport := range transportManager.Transports() {
			visit(reusableOwnerDNSTransport, transport)
		}
	}
	if httpClientManager := service.FromContext[adapter.HTTPClientManager](m.ctx); httpClientManager != nil {
		visit(reusableOwnerHTTPClient, httpClientManager)
	}
}

// retireIdleResources is the one walk over every reusable pool this core owns, and it reports how
// many it reached.
//
// Its contract is the reason it is safe to call from a resume boundary as well as from DEEP_IDLE and
// from the memory pass: it can only ever close a resource that has no active user traffic, and it
// can never dial. That is a property of the keepers, not of this function, and it is what every
// review of a new keeper has to establish - a keeper whose CloseIdleConnections can terminate a
// stream carrying traffic does not belong in this list.
//
// The walk asks for adapter.IdleConnectionReleaser, NOT the bundled adapter.IdleConnectionKeeper: a
// pool that can only drop what is idle is exactly what the trim needs, and demanding the eligibility
// axis silently excludes it. See adapter.IdleConnectionReleaser for the full account.
//
// Endpoints are walked as well as outbounds and DNS transports, because the type assertion is the
// only thing that decides: today none of the endpoint kinds implements CloseIdleConnections (they
// implement SetKeepIdleConnections, which suspends the tunnel - a different and much larger action),
// so the walk is inert for them, and a future endpoint that does implement it is covered without
// another review of this file.
//
// The returned number is a count of OWNERS THE WALK COULD ASK, not of resources it closed. It never
// was a resource count, and ResidencySnapshot exists so a report can say that without changing this
// return value.
func (m *ReferenceManager) retireIdleResources() int {
	retired := 0
	m.eachReusableOwner(func(_ reusableOwnerKind, owner any) {
		releaser, isReleaser := owner.(adapter.IdleConnectionReleaser)
		if isReleaser {
			releaser.CloseIdleConnections()
			retired++
		}
	})
	return retired
}

// ResidencySnapshot inventories the owners the retirement walks can reach, by kind.
//
// It reads the SAME enumeration the walks act on, so a manager that is walked is counted and a
// manager that is not walked cannot be counted. It performs no action and takes no lock of its own:
// it only asserts the release capability, which is a pure type check.
//
// The counts are owner-reach counts - see adapter.ResidencyOwnerCount for what they are not. The
// active/idle/draining ledger of a pool, and the proof that a teardown finished, belong to that pool
// and are not inferred here.
func (m *ReferenceManager) ResidencySnapshot() adapter.ResidencySnapshot {
	var snapshot adapter.ResidencySnapshot
	m.eachReusableOwner(func(kind reusableOwnerKind, owner any) {
		count := residencyCountFor(&snapshot, kind)
		count.Reachable++
		if _, isReleaser := owner.(adapter.IdleConnectionReleaser); isReleaser {
			count.Releasable++
		}
	})
	return snapshot
}

// residencyCountFor returns the field of the snapshot that a kind contributes to. The panic is the
// compile-time-like guard for a new kind: every owner class must be reported somewhere rather than
// silently dropped from the inventory.
func residencyCountFor(snapshot *adapter.ResidencySnapshot, kind reusableOwnerKind) *adapter.ResidencyOwnerCount {
	switch kind {
	case reusableOwnerOutbound:
		return &snapshot.Outbounds
	case reusableOwnerEndpoint:
		return &snapshot.Endpoints
	case reusableOwnerDNSTransport:
		return &snapshot.DNSTransports
	case reusableOwnerHTTPClient:
		return &snapshot.HTTPClients
	default:
		panic("route: unknown reusable owner kind")
	}
}

// retireSuspectResources is the reuse boundary's walk, and it differs from retireIdleResources in
// exactly one way: a pool that can express "no NEW work on the resources you hold now, while the work
// already on them finishes" is asked for that AS WELL AS being asked to drop what is idle.
//
// Both actions are applied, and the drain goes first. That order is not cosmetic: draining is what
// stops a new stream from attaching to a session nobody trusts, so it has to be the first thing that
// happens at the boundary; the idle release that follows is then the pool's own answer for whatever
// was not drained. A pool that implements only the idle half - which is every pool except the XHTTP
// XMUX session pool today - gets exactly the behaviour it had before this walk existed, which is why
// this ordering removes nothing.
//
// The distinction between the two actions is not cosmetic either. A multiplexed session with a stream
// still open is the case the idle-only action gets wrong in both directions: it cannot be closed (the
// stream loses its transport) and it must not be left in the pool (an open stream is not proof the
// path survived the sleep - it can be open and silent), so the only correct action is to stop adding
// new work to it and let it drain. adapter.ReuseSuspect is that capability.
func (m *ReferenceManager) retireSuspectResources() int {
	retired := 0
	m.eachReusableOwner(func(_ reusableOwnerKind, owner any) {
		retired = m.retireSuspectTarget(retired, owner)
	})
	return retired
}

// retireSuspectTarget applies the strongest action a reusable owner can express: refuse new work on
// what it holds, drop what is idle, and report whether it owns anything reusable at all.
//
// The assertion is the RELEASE half of the keeper capability, not the bundled interface, and the
// reason is at adapter.IdleConnectionReleaser: the walk calls CloseIdleConnections and nothing else,
// and asking for SetKeepIdleConnections as well silently excluded every owner that can only drop
// what is idle - which is how the HTTP client pools stayed unreachable through four passes of this
// code being read as correct.
func (m *ReferenceManager) retireSuspectTarget(retired int, target any) int {
	if drainer, isDrainer := target.(adapter.ReuseSuspect); isDrainer {
		drainer.RetireSuspect()
	}
	keeper, isKeeper := target.(adapter.IdleConnectionReleaser)
	if isKeeper {
		keeper.CloseIdleConnections()
		retired++
	}
	return retired
}
