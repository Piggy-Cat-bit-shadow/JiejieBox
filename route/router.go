package route

import (
	"context"
	"errors"
	"os"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/power"
	"github.com/sagernet/sing-box/common/process"
	"github.com/sagernet/sing-box/common/taskmonitor"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	R "github.com/sagernet/sing-box/route/rule"
	"github.com/sagernet/sing-box/service/powerreport"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/task"
	"github.com/sagernet/sing/contrab/freelru"
	"github.com/sagernet/sing/contrab/maphash"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/pause"
)

var _ adapter.Router = (*Router)(nil)
var _ adapter.ExpiredCacheReleaser = (*Router)(nil)

type Router struct {
	ctx    context.Context
	logger log.ContextLogger
	// logFactory is kept for its level alone, so a hot-path diagnostic can be skipped rather than
	// built and discarded. It is nil in tests that construct a Router directly, which is why every
	// use of it goes through debugLogging.
	logFactory   log.Factory
	inbound      adapter.InboundManager
	outbound     adapter.OutboundManager
	dns          adapter.DNSRouter
	dnsTransport adapter.DNSTransportManager
	connection   adapter.ConnectionManager
	network      adapter.NetworkManager
	// powerGovernor is the fork's sleep authority, or nil when none was installed - which is what
	// every test that builds a Router directly has, so every use of it is nil-checked.
	powerGovernor *power.Governor
	// powerReport is the power-report service, or nil when this build has none. It is held for ONE
	// reason: an active recorder counts every connection the userspace path opens, and a bypassed flow
	// would never be counted - the same argument the connection-tracker refusal already makes, applied
	// to the other accounting consumer. A bypass that silently loses statistics is not an optimisation.
	powerReport       *powerreport.Manager
	httpClientManager adapter.HTTPClientManager
	rules             []adapter.Rule
	needFindProcess   bool
	needProcessPaths  bool
	// deferredProcessPaths marks the configuration whose path rules may be completed on demand:
	// the owner is resolved up front (cheap, from the socket table) and the /proc walk happens only
	// when a rule that reads a path is actually reached. A rule-set reference cannot be deferred,
	// because a remote document may carry process conditions this configuration never mentions.
	deferredProcessPaths bool
	// ruleNeedsPaths is parallel to rules: ruleNeedsPaths[i] reports that evaluating rules[i] may
	// read a path, so the walk must be completed before that evaluation.
	ruleNeedsPaths []bool
	// ruleProcessNeeds is parallel to rules as well: it says WHICH process facts rules[i] may read.
	// The walk accumulates the needs of the rules it actually reached, so the bypass decision can
	// tell "the path was asked for and is unknown" from "the path was never relevant".
	ruleProcessNeeds []processNeed
	// upfrontProcessNeed is the need implied by the UP-FRONT full lookup (needProcessPaths).
	// It is credited to every flow, because that lookup ran before the walk started.
	upfrontProcessNeed processNeed
	needFindNeighbor   bool
	leaseFiles         []string
	ruleSets           []adapter.RuleSet
	ruleSetMap         map[string]adapter.RuleSet
	ruleSetUpdater     *R.RuleSetUpdater
	processSearcher    process.Searcher
	processCache       *freelru.Cache[processCacheKey, processCacheEntry]
	neighborResolver   adapter.NeighborResolver
	pauseManager       pause.Manager
	trackers           []adapter.ConnectionTracker
	platformInterface  adapter.PlatformInterface

	// trafficClassPolicies holds the per-outbound explicit traffic class from the configuration.
	// It is set once during box setup and only read at flow setup.
	trafficClassPolicies TrafficClassPolicies

	// fastPathPolicies holds the explicit fast_path intent of each logical group that declared one.
	// Like trafficClassPolicies it is set once during box setup and only read at flow setup, and it
	// is keyed by TAG - never by leaf - so a policy cannot follow a node between groups. See
	// resolveFastPathRequest.
	fastPathPolicies FastPathPolicies

	// dnsHijackInFlight counts hijacked DNS packets currently being resolved. It bounds the
	// goroutines that HijackDNSPacket hands to the DNS router, so a dead resolver cannot turn a
	// query flood into unbounded growth. See dnsHijackConcurrency.
	dnsHijackInFlight atomic.Int64

	// referenceManager owns the idle/keep decision and the reusable pools. It is optional: a Router
	// built directly in a test has none, and the memory-trim pass is then a no-op.
	referenceManager *ReferenceManager
}

// SetReferenceManager attaches the reference manager, so the memory-trim pass can reach the reusable
// pools it already knows how to release.
func (r *Router) SetReferenceManager(manager *ReferenceManager) {
	r.referenceManager = manager
}

// dnsProcessPathCompleter is the setter a DNS router exposes so the layer that owns the process
// lookup can complete a DNS rule's path on demand. It is an interface here for the same reason the
// rest of the seams are: route must not import dns, and dns must not import route.
type dnsProcessPathCompleter interface {
	SetProcessPathCompleter(complete func(ctx context.Context, metadata *adapter.InboundContext))
}

func NewRouter(ctx context.Context, logFactory log.Factory, options option.RouteOptions, dnsOptions option.DNSOptions) *Router {
	// A rule-set document is remote and mutable, so it can carry process conditions this
	// configuration never mentions; and the explicit find_process option asks for the process of
	// every flow. Both keep the up-front full lookup. Everything else - a plain
	// process_name/process_path/package_name rule, in the ROUTING list or in the DNS list - is
	// completed when that rule is actually reached, so a flow decided by an earlier domain or
	// address rule never pays for the /proc walk.
	//
	// The DNS list no longer forces the up-front walk: its rules are completed inside the DNS router's
	// own rule walk by the completer installed below. Folding it in here charged every CONNECTION for
	// a fact only a DNS QUERY could need.
	needProcessPaths := hasRule(options.Rules, usesRuleSet) || options.FindProcess
	deferredProcessPaths := !needProcessPaths && hasRule(options.Rules, isProcessPathRule)
	// What the up-front lookup was asked for. A rule-set document may carry any process condition,
	// so it is credited with the PATH need - the same conservative reading needsProcessPaths uses.
	// The package bit is not implied: a rule-set naming a package on a platform without a package
	// manager could never match, and demanding a package list would refuse the fast path wholesale.
	upfrontProcessNeed := processNeedNone
	if options.FindProcess || hasRule(options.Rules, usesRuleSet) {
		upfrontProcessNeed = processNeedPath
	}
	router := &Router{
		ctx:                ctx,
		logger:             logFactory.NewLogger("router"),
		logFactory:         logFactory,
		inbound:            service.FromContext[adapter.InboundManager](ctx),
		outbound:           service.FromContext[adapter.OutboundManager](ctx),
		dns:                service.FromContext[adapter.DNSRouter](ctx),
		dnsTransport:       service.FromContext[adapter.DNSTransportManager](ctx),
		connection:         service.FromContext[adapter.ConnectionManager](ctx),
		powerGovernor:      service.FromContext[*power.Governor](ctx),
		powerReport:        service.FromContext[*powerreport.Manager](ctx),
		network:            service.FromContext[adapter.NetworkManager](ctx),
		httpClientManager:  service.FromContext[adapter.HTTPClientManager](ctx),
		rules:              make([]adapter.Rule, 0, len(options.Rules)),
		ruleSetMap:         make(map[string]adapter.RuleSet),
		needFindProcess:    hasRule(options.Rules, isProcessRule) || hasDNSRule(dnsOptions.Rules, isProcessDNSRule) || options.FindProcess,
		needProcessPaths:   needProcessPaths,
		upfrontProcessNeed: upfrontProcessNeed,

		deferredProcessPaths: deferredProcessPaths,
		needFindNeighbor:     hasRule(options.Rules, isNeighborRule) || hasDNSRule(dnsOptions.Rules, isNeighborDNSRule) || hasLocalNeighborDNSServer(dnsOptions.Servers) || options.FindNeighbor,
		leaseFiles:           options.DHCPLeaseFiles,
		pauseManager:         service.FromContext[pause.Manager](ctx),
		platformInterface:    service.FromContext[adapter.PlatformInterface](ctx),
	}
	// The DNS rule walk defers its path need to this layer, which owns the lookup and its cache.
	// Installing the completer is what turns the deferral on; a DNS router without it keeps
	// evaluating rules against the metadata it was given.
	if completer, isCompleter := router.dns.(dnsProcessPathCompleter); isCompleter {
		completer.SetProcessPathCompleter(router.completeDNSProcessPaths)
	}
	return router
}

// completeDNSProcessPaths fills a DNS rule's process path from the query's own tuple.
//
// The metadata is the DNS query's, not a connection's: a DNS query has its own source and
// destination, and using a connection tuple here would describe a different socket.
func (r *Router) completeDNSProcessPaths(ctx context.Context, metadata *adapter.InboundContext) {
	r.ensureProcessPaths(ctx, metadata)
}

func (r *Router) Initialize(rules []option.Rule, ruleSets []option.RuleSet) error {
	for i, options := range rules {
		err := R.ValidateNoNestedRuleActions(options)
		if err != nil {
			return E.Cause(err, "parse rule[", i, "]")
		}
		rule, err := R.NewRule(r.ctx, r.logger, options, false)
		if err != nil {
			return E.Cause(err, "parse rule[", i, "]")
		}
		r.rules = append(r.rules, rule)
		// Computed from the OPTIONS, in the same order as r.rules is built, so the flag and the
		// rule can never drift apart. needsProcessPaths treats a rule-set reference as a path
		// need, which is the conservative reading: the document may carry process conditions.
		r.ruleNeedsPaths = append(r.ruleNeedsPaths, needsProcessPaths([]option.Rule{options}))
		r.ruleProcessNeeds = append(r.ruleProcessNeeds, processNeedForRule(options))
	}
	for i, options := range ruleSets {
		for _, tag := range options.Tag {
			if _, exists := r.ruleSetMap[tag]; exists {
				return E.New("duplicate rule-set tag: ", tag)
			}
			ruleSet, err := R.NewRuleSet(r.ctx, r.logger, tag, options)
			if err != nil {
				return E.Cause(err, "parse rule-set[", i, "]")
			}
			r.ruleSets = append(r.ruleSets, ruleSet)
			r.ruleSetMap[tag] = ruleSet
		}
	}
	return nil
}

func (r *Router) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	monitor := taskmonitor.New(r.logger, C.StartTimeout)
	switch stage {
	case adapter.StartStateInitialize:
		for _, ruleSet := range r.ruleSets {
			scope.Add(ruleSet.Close)
		}
		if r.needFindNeighbor {
			if r.platformInterface != nil && r.platformInterface.UsePlatformNeighborResolver() {
				monitor.Start("initialize neighbor resolver")
				resolver := newPlatformNeighborResolver(r.logger, r.platformInterface)
				err := resolver.Start()
				monitor.Finish()
				if err != nil {
					r.logger.Error(E.Cause(err, "start neighbor resolver"))
				} else {
					r.neighborResolver = resolver
					scope.Add(resolver.Close)
				}
			} else {
				monitor.Start("initialize neighbor resolver")
				resolver, err := newNeighborResolver(r.logger, r.leaseFiles)
				monitor.Finish()
				if err != nil {
					if !errors.Is(err, os.ErrInvalid) {
						r.logger.Error(E.Cause(err, "create neighbor resolver"))
					}
				} else {
					err = resolver.Start()
					if err != nil {
						r.logger.Error(E.Cause(err, "start neighbor resolver"))
					} else {
						r.neighborResolver = resolver
						scope.Add(resolver.Close)
					}
				}
			}
		}
	case adapter.StartStateStart:
		var startContext *adapter.HTTPStartContext
		if len(r.ruleSets) > 0 {
			monitor.Start("initialize rule-set")
			startContext = adapter.NewHTTPStartContext()
			var ruleSetStartGroup task.Group
			for i, ruleSet := range r.ruleSets {
				ruleSetInPlace := ruleSet
				ruleSetStartGroup.Append0(func(ctx context.Context) error {
					err := ruleSetInPlace.StartContext(ctx, startContext)
					if err != nil {
						return E.Cause(err, "initialize rule-set[", i, "]")
					}
					return nil
				})
			}
			ruleSetStartGroup.Concurrency(5)
			ruleSetStartGroup.FastFail()
			err := ruleSetStartGroup.Run(r.ctx)
			monitor.Finish()
			if err != nil {
				return err
			}
		}
		if startContext != nil {
			startContext.Close()
		}
		r.ruleSetUpdater = R.NewRuleSetUpdater(r.ctx, r.ruleSets)
		if r.ruleSetUpdater != nil {
			scope.Add(r.ruleSetUpdater.Close)
		}
		r.network.Initialize(r.ruleSets)
		needFindProcess := r.needFindProcess
		for _, ruleSet := range r.ruleSets {
			metadata := ruleSet.Metadata()
			if metadata.ContainsProcessRule {
				needFindProcess = true
			}
		}
		if C.IsAndroid && r.platformInterface != nil {
			needFindProcess = true
		}
		r.needFindProcess = needFindProcess
		if needFindProcess {
			if r.platformInterface != nil && r.platformInterface.UsePlatformConnectionOwnerFinder() {
				r.processSearcher = newPlatformSearcher(r.platformInterface)
			} else {
				monitor.Start("initialize process searcher")
				searcher, err := process.NewSearcher(process.Config{
					Logger:         r.logger,
					PackageManager: r.network.PackageManager(),
				})
				monitor.Finish()
				if err != nil {
					if !errors.Is(err, os.ErrInvalid) {
						r.logger.Warn(E.Cause(err, "create process searcher"))
					}
				} else {
					r.processSearcher = searcher
				}
			}
		}
		if r.processSearcher != nil {
			scope.Add(r.processSearcher.Close)
			processCache := common.Must1(freelru.New[processCacheKey, processCacheEntry](256, maphash.NewHasher[processCacheKey]().Hash32, true))
			processCache.SetLifetime(200 * time.Millisecond)
			r.processCache = processCache
		}
	case adapter.StartStatePostStart:
		for i, rule := range r.rules {
			scope.Add(rule.Close)
			monitor.Start("initialize rule[", i, "]")
			err := rule.Start()
			monitor.Finish()
			if err != nil {
				return E.Cause(err, "initialize rule[", i, "]")
			}
		}
		if r.ruleSetUpdater != nil {
			r.ruleSetUpdater.Start()
		}
		return nil
	case adapter.StartStateStarted:
		for _, ruleSet := range r.ruleSets {
			ruleSet.Cleanup()
		}
		runtime.GC()
	}
	return nil
}

func (r *Router) RuleSet(tag string) (adapter.RuleSet, bool) {
	ruleSet, loaded := r.ruleSetMap[tag]
	return ruleSet, loaded
}

func (r *Router) Rules() []adapter.Rule {
	return r.rules
}

func (r *Router) AppendTracker(tracker adapter.ConnectionTracker) {
	r.trackers = append(r.trackers, tracker)
}

// SetTrafficClassPolicies installs the per-outbound explicit traffic class from the configuration.
//
// Called once during box setup, before any flow is routed. A router that was never given policies
// still classifies by tag, because automatic detection needs no configuration.
func (r *Router) SetTrafficClassPolicies(policies TrafficClassPolicies) {
	r.trafficClassPolicies = policies
}

// SetFastPathPolicies installs the explicit fast_path intent of the logical groups that declared
// one.
//
// Called once during box setup, before any flow is routed. A router that was never given policies
// still falls back to automatic tag detection, exactly as traffic classification does - so this
// setter is an override channel, not the only way a group can become authorised.
func (r *Router) SetFastPathPolicies(policies FastPathPolicies) {
	r.fastPathPolicies = policies
}

func (r *Router) NeedFindProcess() bool {
	return r.needFindProcess
}

func (r *Router) NeedFindNeighbor() bool {
	return r.needFindNeighbor
}

func (r *Router) NeighborResolver() adapter.NeighborResolver {
	return r.neighborResolver
}

// TrimIdleResources is the router's half of the progressive memory pass: it releases reusable
// pools (outbound idle connections and DNS transport connections) and nothing else.
//
// It is deliberately separate from ResetNetwork, which is a barrier - it advances the DNS
// generation and retires every transport - and therefore costs a rebuild. Memory that is only
// elevated must not pay that.
func (r *Router) TrimIdleResources() {
	if r.referenceManager != nil {
		r.referenceManager.TrimIdleResources()
		return
	}
	// A router built without a reference manager (a unit fixture, or the window before box wiring)
	// still owns the outbound set it can reach. Endpoints are not reachable from this type; a real
	// box always attaches the manager, whose walk covers them.
	if r.outbound != nil {
		for _, outbound := range r.outbound.Outbounds() {
			keeper, isKeeper := outbound.(adapter.IdleConnectionReleaser)
			if isKeeper {
				keeper.CloseIdleConnections()
			}
		}
	}
}

// TrimExpiredCaches is the L1 pass: it removes entries the read path can no longer serve and does
// not touch a connection, a reference count or an epoch.
//
// The two halves are the DNS caches (exact, negative and reverse mapping, each with its own lifetime
// and its own reuse semantics - see dns.Client.TrimExpiredCaches) and the process lookup cache,
// which is a 200ms memoisation whose absence only causes another lookup. Neither can make the next
// demand dial a different path.
func (r *Router) TrimExpiredCaches() int {
	trimmed := 0
	if trimmer, isTrimmer := r.dns.(adapter.ExpiredCacheReleaser); isTrimmer {
		trimmed += trimmer.TrimExpiredCaches()
	}
	if r.processCache != nil {
		before := r.processCache.Len()
		r.processCache.PurgeExpired()
		if after := r.processCache.Len(); after < before {
			trimmed += before - after
		}
	}
	return trimmed
}

func (r *Router) ResetNetwork() {
	r.httpClientManager.ResetNetwork()
	r.dns.ResetNetwork()
	if r.processCache != nil {
		r.processCache.Purge()
	}
	if r.processSearcher != nil {
		r.processSearcher.ResetCache()
	}
}

// debugLogging reports whether a debug-level message would actually be emitted.
//
// It exists so that the arguments of a hot-path debug message can be left unbuilt. A nil factory -
// which is what a Router built directly in a test has - reports false, so the message is skipped
// rather than causing a nil dereference; no test loses an assertion it was making about logging,
// because none of them asserted on this.
func (r *Router) debugLogging() bool {
	return r.logFactory != nil && r.logFactory.Level() >= log.LevelDebug
}
