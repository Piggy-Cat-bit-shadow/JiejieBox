package provider

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/urltest"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/batch"
	E "github.com/sagernet/sing/common/exceptions"
	F "github.com/sagernet/sing/common/format"
	"github.com/sagernet/sing/common/x/list"
	"github.com/sagernet/sing/service"
)

// Adapter is the part every provider shares: it owns the outbounds the provider has published, the
// update callbacks the groups subscribe to, and the optional health check.
//
// # Why the outbound map is guarded separately from the update path
//
// The traffic-facing read is `Outbounds()`/`Outbound(tag)`: a group rebuilds its membership from it
// on the provider's own goroutine, and the Clash API may read it at any time. The write happens
// once per apply. A dedicated RWMutex keeps a reader from ever observing a half-published map, and
// lets the apply path hold the write lock for exactly the swap rather than across construction.
type Adapter struct {
	ctx             context.Context
	cancel          context.CancelFunc
	workers         sync.WaitGroup
	closed          bool
	workerAccess    sync.Mutex
	outbound        adapter.DynamicOutboundManager
	router          adapter.Router
	logFactory      log.Factory
	logger          log.ContextLogger
	providerType    string
	providerTag     string
	outboundsAccess sync.RWMutex
	outbounds       []adapter.Outbound
	outboundsByTag  map[string]adapter.Outbound
	// appliedOutbounds is the option set that produced the published objects. It is what makes an
	// update a diff: an entry whose options are unchanged is not reconstructed, so a subscription
	// refresh that only reorders or reformats does not drop live connections.
	appliedOutbounds map[string]option.Outbound
	// updateAccess serialises applies. The concrete providers already serialise their own refresh
	// cycles (remote through its updating flag, local through its file mutex), but UpdateNodes is an
	// exported entry point and two callers that interleave would publish a map assembled from both.
	updateAccess   sync.Mutex
	ticker         *time.Ticker
	checking       atomic.Bool
	history        *urltest.HistoryStorage
	callbackAccess sync.Mutex
	callbacks      list.List[adapter.ProviderUpdateCallback]

	link     string
	enabled  bool
	timeout  time.Duration
	interval time.Duration
}

func NewAdapter(ctx context.Context, router adapter.Router, outbound adapter.OutboundManager, logFactory log.Factory, logger log.ContextLogger, providerTag string, providerType string, options option.ProviderHealthCheckOptions) Adapter {
	timeout := time.Duration(options.Timeout)
	if timeout == 0 {
		timeout = 3 * time.Second
	}
	interval := time.Duration(options.Interval)
	if interval == 0 {
		interval = 10 * time.Minute
	}
	if interval < time.Minute {
		interval = time.Minute
	}
	dynamicOutbound, _ := outbound.(adapter.DynamicOutboundManager)
	ctx, cancel := context.WithCancel(ctx)
	return Adapter{
		cancel:           cancel,
		appliedOutbounds: make(map[string]option.Outbound),
		ctx:              ctx,
		outbound:         dynamicOutbound,
		router:           router,
		logFactory:       logFactory,
		logger:           logger,
		providerType:     providerType,
		providerTag:      providerTag,

		enabled:  options.Enabled,
		link:     options.URL,
		timeout:  timeout,
		interval: interval,
	}
}

func (a *Adapter) Start() error {
	a.history = service.PtrFromContext[urltest.HistoryStorage](a.ctx)
	if a.history == nil {
		return E.New("missing URL test history storage")
	}
	if a.enabled {
		a.ticker = time.NewTicker(a.interval)
		a.run(a.loopCheck)
	}
	return nil
}

func (a *Adapter) Type() string {
	return a.providerType
}

func (a *Adapter) Tag() string {
	return a.providerTag
}

func (a *Adapter) Outbounds() []adapter.Outbound {
	a.outboundsAccess.RLock()
	defer a.outboundsAccess.RUnlock()
	outbounds := make([]adapter.Outbound, len(a.outbounds))
	copy(outbounds, a.outbounds)
	return outbounds
}

func (a *Adapter) Outbound(tag string) (adapter.Outbound, bool) {
	a.outboundsAccess.RLock()
	defer a.outboundsAccess.RUnlock()
	detour, ok := a.outboundsByTag[tag]
	return detour, ok
}

// resolveOutboundTags gives every node its provider-scoped tag.
//
// A duplicate inside one subscription is renamed rather than refused: a subscription that repeats a
// tag is broken but still has usable nodes, and dropping the whole provider over one entry would
// punish the working nodes. The rename is deterministic (first occurrence keeps the tag, later ones
// get " (2)", " (3)" ...), so a refresh produces the same names and the diff stays stable.
func (a *Adapter) resolveOutboundTags(newOpts []option.Outbound) []string {
	tags := make([]string, len(newOpts))
	seen := make(map[string]bool)
	for i, opt := range newOpts {
		var baseTag string
		if opt.Tag != "" {
			baseTag = F.ToString(a.providerTag, "/", opt.Tag)
		} else {
			baseTag = F.ToString(a.providerTag, "/", i)
		}
		tag := baseTag
		for n := 2; seen[tag]; n++ {
			tag = F.ToString(baseTag, " (", n, ")")
		}
		if tag != baseTag {
			a.logger.Warn("duplicate outbound tag ", baseTag, " in provider, renamed to ", tag)
		}
		seen[tag] = true
		tags[i] = tag
	}
	return tags
}

func (a *Adapter) HealthCheck(ctx context.Context) (map[string]uint16, error) {
	if a.ticker != nil {
		a.ticker.Reset(a.interval)
	}
	return a.healthcheck(ctx)
}

func (a *Adapter) RegisterCallback(callback adapter.ProviderUpdateCallback) *list.Element[adapter.ProviderUpdateCallback] {
	a.callbackAccess.Lock()
	defer a.callbackAccess.Unlock()
	return a.callbacks.PushBack(callback)
}

func (a *Adapter) UnregisterCallback(element *list.Element[adapter.ProviderUpdateCallback]) {
	if element == nil {
		return
	}
	a.callbackAccess.Lock()
	defer a.callbackAccess.Unlock()
	a.callbacks.Remove(element)
}

// UpdateGroups notifies the groups that named this provider. The callbacks are taken under the lock
// and invoked outside it: a callback rebuilds the group's membership and may read the provider's
// outbounds, which is an RLock on the same provider, and holding the callback list lock while a
// callback runs would serialise unrelated providers for no reason.
func (a *Adapter) UpdateGroups() {
	a.callbackAccess.Lock()
	callbacks := make([]adapter.ProviderUpdateCallback, 0, a.callbacks.Len())
	for element := a.callbacks.Front(); element != nil; element = element.Next() {
		callbacks = append(callbacks, element.Value)
	}
	a.callbackAccess.Unlock()
	for _, callback := range callbacks {
		if err := callback(a.providerTag); err != nil {
			a.logger.Error("update group: ", err)
		}
	}
}

func (a *Adapter) Close() error {
	a.workerAccess.Lock()
	a.closed = true
	a.cancel()
	a.workerAccess.Unlock()
	a.workers.Wait()
	if a.ticker != nil {
		a.ticker.Stop()
	}
	a.outboundsAccess.Lock()
	outbounds := a.outbounds
	a.outbounds = nil
	a.outboundsByTag = nil
	a.outboundsAccess.Unlock()
	if a.outbound == nil {
		// No dynamic manager: the provider never published anything, so there is nothing to
		// unlink. Reported as success rather than a nil dereference during teardown.
		return nil
	}
	var err error
	for _, ob := range outbounds {
		if err2 := a.outbound.Remove(ob.Tag()); err2 != nil {
			err = E.Append(err, err2, func(err error) error {
				return E.Cause(err, "close outbound [", ob.Tag(), "]")
			})
		}
	}
	return err
}

func (a *Adapter) loopCheck() {
	a.healthcheck(a.ctx)
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-a.ticker.C:
			a.healthcheck(a.ctx)
		}
	}
}

// healthcheck measures every published node and records the result in the shared URL-test history,
// which is the same store the urltest group reads. A failed probe deletes the entry instead of
// storing a zero: "not measured" and "measured at 0ms" are different facts, and a group must not
// treat a failed probe as the fastest node.
func (a *Adapter) healthcheck(ctx context.Context) (map[string]uint16, error) {
	result := make(map[string]uint16)
	if a.checking.Swap(true) {
		return result, nil
	}
	defer a.checking.Store(false)
	outbounds := a.Outbounds()
	b, _ := batch.New(ctx, batch.WithConcurrencyNum[any](10))
	var resultAccess sync.Mutex
	checked := make(map[string]bool)
	for _, detour := range outbounds {
		tag := detour.Tag()
		if checked[tag] {
			continue
		}
		checked[tag] = true
		b.Go(tag, func() (any, error) {
			probeCtx, cancel := context.WithTimeout(ctx, a.timeout)
			defer cancel()
			delay, err := urltest.URLTest(probeCtx, a.link, detour)
			if err != nil {
				a.logger.Debug("outbound ", tag, " unavailable: ", err)
				a.history.DeleteURLTestHistory(tag)
			} else {
				a.logger.Debug("outbound ", tag, " available: ", delay, "ms")
				a.history.StoreURLTestHistory(tag, &adapter.URLTestHistory{
					Time:  time.Now(),
					Delay: delay,
				})
				resultAccess.Lock()
				result[tag] = delay
				resultAccess.Unlock()
			}
			return nil, nil
		})
	}
	b.Wait()
	return result, nil
}

// RewriteDetourForProvider maps a detour that names a SIBLING node onto the tag that sibling will
// have after the provider prefix is applied. A detour that names anything else is left alone: it
// belongs to the main configuration's namespace, and the provider has no business rewriting it.
func (a *Adapter) RewriteDetourForProvider(opts []option.Outbound) {
	tagMapping := make(map[string]string)
	for _, opt := range opts {
		if opt.Tag != "" {
			tagMapping[opt.Tag] = F.ToString(a.providerTag, "/", opt.Tag)
		}
	}
	for i := range opts {
		if dialerWrapper, ok := opts[i].Options.(option.DialerOptionsWrapper); ok {
			dialerOptions := dialerWrapper.TakeDialerOptions()
			if newDetour, found := tagMapping[dialerOptions.Detour]; found {
				dialerOptions.Detour = newDetour
				dialerWrapper.ReplaceDialerOptions(dialerOptions)
			}
		}
	}
}

// removeUseless unlinks the nodes a new subscription no longer names. It runs after the new set is
// published, so a removal failure cannot affect the nodes that are still serving.
func (a *Adapter) removeUseless(newTags []string) {
	a.outboundsAccess.RLock()
	if len(a.outbounds) == 0 {
		a.outboundsAccess.RUnlock()
		return
	}
	exists := make(map[string]bool, len(newTags))
	for _, tag := range newTags {
		exists[tag] = true
	}
	snap := make([]adapter.Outbound, len(a.outbounds))
	copy(snap, a.outbounds)
	a.outboundsAccess.RUnlock()
	// Retry in passes, because the outbound manager refuses to remove a node that a LIVE node still
	// names as a dependency. A subscription that drops a chain is ordered any way it likes, so the
	// dependent has to go first; a pass that removes what it can and retries the rest converges on
	// that order without the adapter having to reconstruct the graph. A pass that makes no progress
	// reports the remaining refusals and stops.
	var pending []adapter.Outbound
	for _, ob := range snap {
		if !exists[ob.Tag()] {
			pending = append(pending, ob)
		}
	}
	var lastErr error
	for len(pending) > 0 {
		var remaining []adapter.Outbound
		lastErr = nil
		for _, ob := range pending {
			if err := a.outbound.Remove(ob.Tag()); err != nil {
				remaining = append(remaining, ob)
				lastErr = err
				continue
			}
		}
		if len(remaining) == len(pending) {
			a.logger.Error(lastErr, "close outbound [", remaining[0].Tag(), "]")
			return
		}
		pending = remaining
	}
}

// run starts a background worker that is joined by Close. A worker started after Close returns
// instead of leaking past the provider it belongs to.
func (a *Adapter) run(fn func()) {
	a.workerAccess.Lock()
	defer a.workerAccess.Unlock()
	if a.closed {
		return
	}
	a.workers.Add(1)
	go func() { defer a.workers.Done(); fn() }()
}

// Run is the provider-facing form of run: a concrete provider lives in another package, so it
// cannot call the unexported method.
func (a *Adapter) Run(fn func()) {
	a.run(fn)
}
