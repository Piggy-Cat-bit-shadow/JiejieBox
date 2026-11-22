package provider

import (
	"context"
	"errors"
	"os"
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

// providerPhase is the provider's terminal state machine. It is guarded by Adapter.publishAccess;
// there is no lock-free read of it on the publication path, because Read-then-act would be exactly
// the TOCTOU the phase exists to remove.
type providerPhase uint8

const (
	// providerPhaseOpen is the only phase in which an apply may commit a publication.
	providerPhaseOpen providerPhase = iota
	// providerPhaseClosing is set by Close before it cancels and drains. A publisher that observes it
	// refuses, and an in-flight apply is expected to unwind through its cancellation checks.
	providerPhaseClosing
	// providerPhaseClosed is the terminal phase. No provider publication can be committed after it,
	// and the snapshot has been (or is being) retired.
	providerPhaseClosed
)

// defaultCloseDrainTimeout bounds how long Close waits for an apply that was already running when
// it began. The provider context is cancelled BEFORE the wait, and UpdateNodes checks it between
// nodes, so the ordinary case drains in one in-flight manager call. The bound exists for the
// pathological case - a constructor or Start that ignores cancellation - where waiting forever
// would turn provider teardown into a deadlock. When it expires the provider is sealed anyway, the
// apply reclaims its own candidates when it unwedges, and the remaining manager releases are
// finished by a reclamation goroutine.
const defaultCloseDrainTimeout = 10 * time.Second

// errProviderClosed is the refusal every publication path returns once the provider reached its
// terminal state.
var errProviderClosed = E.New("provider is closed")

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
	// snapshot is the configuration record set that produced the published objects. It replaces the
	// map of option.Outbound this adapter used to retain: the record is the canonical byte encoding
	// of the exact value the apply compared, so an unchanged refresh is still detected, but the
	// decoded options tree is no longer kept alive per node. See snapshot.go.
	snapshotAccess sync.RWMutex
	snapshot       *appliedSnapshot
	// snapshotGeneration names the next store generation. Guarded by updateAccess.
	snapshotGeneration uint64
	// updateAccess serialises applies. The concrete providers already serialise their own refresh
	// cycles (remote through its updating flag, local through its file mutex), but UpdateNodes is an
	// exported entry point and two callers that interleave would publish a map assembled from both.
	updateAccess sync.Mutex
	// opaqueRecords counts nodes whose configuration could not be canonically encoded, so they are
	// kept with a record that never matches and are rebuilt on every refresh. That is the safe
	// direction, but an encoder regression would otherwise be completely silent; the count and the
	// first cause are reported once per apply and stay queryable by a test.
	opaqueRecords  atomic.Int64
	opaqueCause    atomic.Value
	ticker         *time.Ticker
	checking       atomic.Bool
	history        *urltest.HistoryStorage
	callbackAccess sync.Mutex
	callbacks      list.List[adapter.ProviderUpdateCallback]
	// retired is what a previous apply asked the manager to unlink but could not, because a group
	// still named it in its published membership. UpdateGroups retries after the callbacks, when the
	// memberships have been rebuilt; until then the node stays LIVE rather than closed under a
	// selector that still holds it. The OBJECT is kept, not the tag: the provider's own tag map no
	// longer contains it by then, and re-resolving the tag would either miss it or find a stranger.
	retiredAccess sync.Mutex
	retired       []adapter.Outbound
	// pendingTags are the tags this provider replaced since the last completed publication round.
	// They are handed to the manager's CompletePublication barrier only after UpdateGroups notified
	// every subscribed group successfully; a failed round keeps them for the next success.
	pendingAccess sync.Mutex
	pendingTags   map[string]bool

	// publishAccess is the terminal publication gate. It guards phase and every point at which the
	// provider COMMITS a publication (the outbounds map, the record snapshot, and the pending
	// bookkeeping). Close takes the write side for the terminal transition, so a publisher either
	// committed before the transition - and is then torn down by Close - or observes the terminal
	// phase and refuses. A manager replacement is deliberately NOT made under this lock: the
	// construction inside Replace can block, and Close must not wait for it. Such a replacement is
	// a PROVISIONAL candidate owned by the apply until it passes this gate; when the gate is closed
	// the apply reclaims the candidate itself. See UpdateNodes and Close.
	publishAccess sync.RWMutex
	phase         providerPhase
	// applyWork counts UpdateNodes calls that passed the gate at their entry. Close cancels the
	// provider context first and then waits for it with a bound, so an apply that honours the
	// cancellation cannot keep Close waiting past one in-flight manager call.
	applyWork sync.WaitGroup
	// closeDone is closed when the (first) Close performed its terminal transition. A second Close
	// waits for it and reports the same result, which keeps teardown idempotent.
	closeDone chan struct{}
	closeErr  error
	// closeDrainTimeout bounds the wait for an apply that was already inside UpdateNodes.
	// Production leaves the default; a test that deliberately blocks a constructor may shorten it.
	closeDrainTimeout time.Duration
	// testAfterNodeInstall and testBeforeCommit are deterministic barrier seams for the close
	// protocol tests. They run only when installed by a test; production leaves them nil. They
	// exist because the two windows this protocol closes - "a replacement was installed, the
	// provider has not published it yet" and "the batch is complete, the snapshot is not swapped
	// yet" - cannot be hit dependably by scheduling.
	testAfterNodeInstall func(tag string)
	testBeforeCommit     func()

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
		cancel:            cancel,
		pendingTags:       make(map[string]bool),
		closeDone:         make(chan struct{}),
		closeDrainTimeout: defaultCloseDrainTimeout,
		ctx:               ctx,
		outbound:          dynamicOutbound,
		router:            router,
		logFactory:        logFactory,
		logger:            logger,
		providerType:      providerType,
		providerTag:       providerTag,

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
// ResolvedTags returns the published tags a document would produce, in document order.
//
// It is the same rendering the apply uses (provider tag, "/", document tag, with a numeric
// suffix for a duplicate), exposed so a caller can compare a document with what is published
// without duplicating the naming rules.
func (a *Adapter) ResolvedTags(newOpts []option.Outbound) []string {
	if len(newOpts) == 0 {
		return nil
	}
	return a.resolveOutboundTags(newOpts)
}

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
//
// It returns the callbacks' errors. A group REFUSES a membership that would close a cycle in the
// published graph, and that refusal is a fact the apply path has to see: a synchronous provider
// (a local file read at start) turns it into a start failure, exactly like the static cycle the
// same graph would produce; an asynchronous refresh logs it and keeps the previous members and
// selection. Swallowing it would make the invalid update look applied.
func (a *Adapter) UpdateGroups() error {
	// Serialise the whole notification round with applies. Without this, a callback could observe
	// the manager mid-apply (new nodes installed) while this provider's published list is still the
	// previous document, rebuild a membership from mixed state, and let CompletePublication end the
	// retirement wait for objects that round did not actually replace.
	a.updateAccess.Lock()
	defer a.updateAccess.Unlock()
	// A callback round publishes group membership and ends the retirement wait, so it belongs to the
	// provider's publication lifetime: Close must drain it like an apply, and a round that starts
	// after the terminal transition is refused. This is also what keeps Close from clearing the
	// retired/pending bookkeeping under a callback round that would write it back.
	if !a.beginApply() {
		return E.New("provider ", a.providerTag, ": ", errProviderClosed)
	}
	defer a.endApply()
	a.callbackAccess.Lock()
	callbacks := make([]adapter.ProviderUpdateCallback, 0, a.callbacks.Len())
	for element := a.callbacks.Front(); element != nil; element = element.Next() {
		callbacks = append(callbacks, element.Value)
	}
	a.callbackAccess.Unlock()
	var updateErr error
	for _, callback := range callbacks {
		if err := callback(a.providerTag); err != nil {
			a.logger.Error("update group: ", err)
			updateErr = E.Errors(updateErr, err)
		}
	}
	// A removal the previous apply could not finish is retried HERE, after the memberships were
	// rebuilt. Running it before the callbacks is what closed a node a selector still held: the
	// group's new member list did not exist yet, so nothing had dropped the reference.
	a.retryRetired()
	if updateErr == nil {
		// Only a round in which every group rebuilt its membership may end the retirement wait for
		// the objects this provider replaced. On failure the old objects keep serving the previous
		// selection, and the tags stay pending for the next successful round.
		a.completePublication()
	}
	return updateErr
}

// replaceOutbound uses the owner-aware replacement when the manager provides it, so the replaced
// object is parked instead of closed before the groups are notified.
func (a *Adapter) replaceOutbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, outboundType string, options any) error {
	var err error
	if owned, isOwned := a.outbound.(adapter.OwnedDynamicOutboundManager); isOwned {
		err = owned.ReplaceOwned(ctx, router, logger, tag, outboundType, options, a.providerTag)
	} else {
		err = a.outbound.Replace(ctx, router, logger, tag, outboundType, options)
	}
	if err != nil {
		return err
	}
	// A measurement is a fact about ONE object, and the history is keyed by TAG. A subscription that
	// keeps the tag and points it at a different server is the ordinary case, so the delay stored for
	// the replaced object must go with it: otherwise the URL-test group orders its members on evidence
	// about a server that is gone, and the dashboard shows a node that was never probed in this
	// configuration as measured. The router clears history when a handshake FAILS (route/conn.go:190),
	// which cannot help a node replaced while idle; this is the edge that can.
	a.history.DeleteURLTestHistory(tag)
	return nil
}

// markPendingReplacement records a same-tag replacement for the next publication barrier.
func (a *Adapter) markPendingReplacement(tag string) {
	a.pendingAccess.Lock()
	a.pendingTags[tag] = true
	a.pendingAccess.Unlock()
}

// completePublication ends the retirement wait for every tag replaced since the last successful
// round. Tags are only cleared once the manager accepted the completion.
func (a *Adapter) completePublication() {
	publisher, isPublisher := a.outbound.(adapter.OwnedDynamicOutboundManager)
	if !isPublisher {
		return
	}
	a.pendingAccess.Lock()
	if len(a.pendingTags) == 0 {
		a.pendingAccess.Unlock()
		return
	}
	tags := make([]string, 0, len(a.pendingTags))
	for tag := range a.pendingTags {
		tags = append(tags, tag)
	}
	a.pendingTags = make(map[string]bool, len(tags))
	a.pendingAccess.Unlock()
	if err := publisher.CompletePublication(a.providerTag, tags); err != nil {
		a.logger.Error(err, "complete outbound publication")
		a.pendingAccess.Lock()
		for _, tag := range tags {
			a.pendingTags[tag] = true
		}
		a.pendingAccess.Unlock()
	}
}

// hasUpdateCallbacks reports whether any group subscribed to this provider. With no subscribers
// there is no callback phase to wait for, so an apply may complete its replacements immediately.
func (a *Adapter) hasUpdateCallbacks() bool {
	a.callbackAccess.Lock()
	defer a.callbackAccess.Unlock()
	return a.callbacks.Len() > 0
}

// retryRetired attempts the removals an earlier apply deferred, and keeps the rest for the next
// one. It is called with no provider locks held.
func (a *Adapter) retryRetired() {
	a.retiredAccess.Lock()
	retired := a.retired
	a.retired = nil
	a.retiredAccess.Unlock()
	if len(retired) == 0 {
		return
	}
	remaining := a.removeRetiredObjects(retired)
	if len(remaining) == 0 {
		return
	}
	a.retiredAccess.Lock()
	a.retired = append(a.retired, remaining...)
	a.retiredAccess.Unlock()
}

// beginApply registers an apply with the publication gate. The phase test and the registration are
// ONE critical section with Close's terminal transition: an apply is either counted before Close
// starts draining (and is therefore waited for), or it observes the non-open phase and constructs
// nothing. The test at the entry is an optimisation, not the authority - every publication point
// tests the phase again under the same gate.
func (a *Adapter) beginApply() bool {
	a.publishAccess.Lock()
	defer a.publishAccess.Unlock()
	if a.phase != providerPhaseOpen {
		return false
	}
	a.applyWork.Add(1)
	return true
}

// endApply releases the apply's registration. It must run exactly once for a beginApply that
// returned true, including on every error path.
func (a *Adapter) endApply() {
	a.applyWork.Done()
}

// publicationOpen reports whether the provider is still accepting a publication. It is only a
// predicate: a caller that then publishes must hold the gate across the publication itself, which
// is what makes it authoritative rather than a check-then-act race.
func (a *Adapter) publicationOpen() bool {
	a.publishAccess.RLock()
	defer a.publishAccess.RUnlock()
	return a.phase == providerPhaseOpen
}

// phaseState is the test/diagnostic read of the terminal state machine.
func (a *Adapter) phaseState() providerPhase {
	a.publishAccess.RLock()
	defer a.publishAccess.RUnlock()
	return a.phase
}

// waitForApplyDrain waits, bounded, until every apply registered through beginApply has returned.
// The caller has already moved the phase out of open under publishAccess, so no new apply can
// register: WaitGroup.Add is never concurrent with this Wait.
func (a *Adapter) waitForApplyDrain() bool {
	done := make(chan struct{})
	go func() {
		a.applyWork.Wait()
		close(done)
	}()
	timer := time.NewTimer(a.closeDrainTimeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

// releasePublishedTag unlinks and closes one of the provider's manager objects. A missing tag is
// not a teardown failure: a late apply that observed the terminal phase reclaims its own candidate,
// so the object may already be gone. Closing an object twice is prevented by the manager's unlink
// critical section - only the caller that actually unlinked a tag releases that object.
func (a *Adapter) releasePublishedTag(tag string) error {
	if a.outbound == nil {
		return nil
	}
	if releaser, isReleaser := a.outbound.(adapter.DynamicOutboundReleaser); isReleaser {
		return releaser.Release(tag)
	}
	return a.outbound.Remove(tag)
}

// finishRetiredPublication runs the manager's ownership barrier for the tags this provider still
// owns. During a refresh the barrier is driven by UpdateGroups after the callbacks; at close there
// is no later round, so it is this provider's last chance to release the objects it parked. A user
// lease that is still outstanding keeps its object open and the manager releases it at lease end,
// exactly as during a refresh.
func (a *Adapter) finishRetiredPublication(tags []string) {
	publisher, isPublisher := a.outbound.(adapter.OwnedDynamicOutboundManager)
	if !isPublisher || len(tags) == 0 {
		return
	}
	if err := publisher.CompletePublication(a.providerTag, tags); err != nil && a.logger != nil {
		a.logger.Error(err, "complete outbound publication at close")
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

	// Terminal transition, in two steps. The closing phase is published first, after the provider
	// context was cancelled, so an apply that is already running can observe the cancellation and
	// unwind while Close drains it. The closed phase is published only after the drain, and it is
	// taken under the same gate every publication point uses.
	a.publishAccess.Lock()
	if a.phase != providerPhaseOpen {
		done := a.closeDone
		a.publishAccess.Unlock()
		<-done
		return a.closeErr
	}
	a.phase = providerPhaseClosing
	a.publishAccess.Unlock()

	drained := a.waitForApplyDrain()
	if !drained && a.logger != nil {
		a.logger.Warn("provider ", a.providerTag, ": an in-flight update did not return within ",
			a.closeDrainTimeout, "; the provider is sealed and late candidates will be reclaimed")
	}

	a.publishAccess.Lock()
	a.phase = providerPhaseClosed
	a.outboundsAccess.Lock()
	outbounds := a.outbounds
	a.outbounds = nil
	a.outboundsByTag = nil
	a.outboundsAccess.Unlock()
	a.retiredAccess.Lock()
	retired := a.retired
	a.retired = nil
	a.retiredAccess.Unlock()
	a.pendingAccess.Lock()
	pending := make([]string, 0, len(a.pendingTags))
	for tag := range a.pendingTags {
		pending = append(pending, tag)
	}
	a.pendingTags = make(map[string]bool)
	a.pendingAccess.Unlock()
	a.publishAccess.Unlock()

	teardown := func() error {
		var err error
		for _, ob := range outbounds {
			// Teardown ordering: this provider is closed BEFORE the outbound manager and its groups,
			// so a membership-aware Remove would refuse every node this provider still feeds.
			// Release is the ordering-aware verb for exactly that moment; see
			// adapter.DynamicOutboundReleaser.
			releaseErr := a.releasePublishedTag(ob.Tag())
			if releaseErr != nil && !errors.Is(releaseErr, os.ErrInvalid) {
				err = E.Append(err, releaseErr, func(err error) error {
					return E.Cause(err, "close outbound [", ob.Tag(), "]")
				})
			}
		}
		for _, ob := range retired {
			// Objects an earlier apply asked the manager to unlink but could not (a group still named
			// them) are released here: at teardown there is no membership left to protect, and
			// leaving them would keep the provider's objects alive past its own Close.
			releaseErr := a.releasePublishedTag(ob.Tag())
			if releaseErr != nil && !errors.Is(releaseErr, os.ErrInvalid) {
				err = E.Append(err, releaseErr, func(err error) error {
					return E.Cause(err, "close retired outbound [", ob.Tag(), "]")
				})
			}
		}
		tags := make([]string, 0, len(outbounds)+len(pending))
		for _, ob := range outbounds {
			tags = append(tags, ob.Tag())
		}
		tags = append(tags, pending...)
		a.finishRetiredPublication(tags)
		return err
	}

	var closeErr error
	switch {
	case a.outbound == nil:
		// No dynamic manager: the provider never published anything, so there is nothing to unlink.
		// Reported as success rather than a nil dereference during teardown.
		a.closeSnapshot()
	case drained:
		closeErr = teardown()
		// The records are retired only after every outbound they described has been released: the
		// store must not close before its last legitimate reader, and releasing the nodes is what
		// proves the provider itself is done reading. A lease held from outside still keeps its
		// generation alive.
		a.closeSnapshot()
	default:
		// An apply is still inside a manager call that ignored cancellation. The snapshot can be
		// retired now: publication points test the closed phase, so it cannot be replaced. The
		// manager verbs go to a goroutine because Release/Remove take the manager's mutation lock,
		// which the stuck replacement is holding - waiting for it here is the unbounded Close this
		// branch exists to prevent.
		a.closeSnapshot()
		go func() {
			_ = teardown()
		}()
	}

	if !drained {
		closeErr = E.Errors(closeErr, E.New("provider ", a.providerTag,
			": an in-flight update did not stop within ", a.closeDrainTimeout,
			"; the provider was sealed and its outbounds are reclaimed in the background"))
	}
	a.publishAccess.Lock()
	a.closeErr = closeErr
	close(a.closeDone)
	a.publishAccess.Unlock()
	return closeErr
}

// CheckDownloadPath refuses a download path that can only be completed by THIS provider's own
// nodes.
//
// # The two shapes the download path can take
//
// A remote provider fetches through an HTTP transport that is either
//
//  1. an explicit `http_client` (or a deprecated `download_detour`), whose dialer may carry a
//     `detour`; or
//  2. the implicit default transport created by the Box, which is built with
//     `dialer.Options.DefaultOutbound = true` - so its dialer follows `route.final` (the outbound
//     manager's default). That is the COMMON case, and it is the one that hides the loop best: a
//     configuration whose `route.final` is the subscription's own selector downloads the
//     subscription THROUGH that selector, which has no member until the download succeeds.
//
// Both are checked here. The loop is not a crash: it is a group that stays empty forever, with a UI
// that lists it as a configured proxy and a dial path that always refuses. MEASURED through
// `box.New` + `Start` with `route.final` naming the provider-fed selector:
//
//	provider/remote[p]: update outbound provider: download provider document: Get "...": outbound
//	group g has no available member yet: it is waiting for outbound provider p to publish nodes
//
// repeated every refresh interval. The message is a runtime error with no start-time verdict; this
// check turns it into one.
//
// # Why it is conservative
//
// Only a group that is EXCLUSIVELY fed by this provider and currently has no member is refused. A
// group fed by another provider, or holding a static member, may be populated without this
// provider's first download and is left alone - refusing it would be the false deny this check
// exists to avoid. A DNS-mediated bootstrap (the provider's own hostname resolved through a DNS
// server that detours into the same group) is NOT covered: it depends on runtime resolution and is
// recorded as a known limitation rather than guessed at here.
func (a *Adapter) CheckDownloadPath(detour string, followsDefaultOutbound bool) error {
	if a.outbound == nil {
		return nil
	}
	if detour != "" {
		start, loaded := a.outbound.Outbound(detour)
		if !loaded {
			// A missing detour tag is the configuration's own error, reported by the start-time
			// validation; naming it here as a bootstrap loop would mislead.
			return nil
		}
		return a.checkDownloadChain(start, "detour "+detour, "download through")
	}
	if !followsDefaultOutbound {
		// An explicit http_client without a detour dials directly: the download does not depend on
		// any outbound, so no outbound graph can close a loop through it.
		return nil
	}
	start := a.outbound.Default()
	if start == nil {
		return nil
	}
	return a.checkDownloadChain(start, "route.final", "download through")
}

// CheckDownloadResolver refuses a DNS-mediated bootstrap: the provider's download host is a domain,
// its dialer resolves it through resolverTag, and that DNS transport detours into a group only this
// provider can populate.
//
// # Why this is a separate check from CheckDownloadPath
//
// The outbound dial chain is only half of a download. The HTTP client must first resolve its server
// name, and that resolution runs through route.default_domain_resolver (or the http_client's own
// domain_resolver) BEFORE any outbound is dialled. When the resolver's transport detours into the
// provider's own empty group, the first download blocks in DNS: no outbound ever gets a chance to
// report the loop, and the Box starts with a group that can never be populated.
func (a *Adapter) CheckDownloadResolver(resolverTag string) error {
	if resolverTag == "" || a.outbound == nil {
		return nil
	}
	dnsManager := service.FromContext[adapter.DNSTransportManager](a.ctx)
	if dnsManager == nil {
		return nil
	}
	transport, loaded := dnsManager.Transport(resolverTag)
	if !loaded {
		// A missing resolver tag is reported by the DNS transport manager at start; naming it here
		// as a bootstrap loop would mislead.
		return nil
	}
	visited := make(map[string]bool)
	queue := []adapter.DNSTransport{transport}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current == nil || visited[current.Tag()] {
			continue
		}
		visited[current.Tag()] = true
		// A DNS resolver chain: a transport's own domain_resolver is another DNS transport.
		for _, dependency := range current.Dependencies() {
			if next, isTransport := dnsManager.Transport(dependency); isTransport {
				queue = append(queue, next)
			}
		}
		referrer, isReferrer := current.(adapter.Referrer)
		if !isReferrer {
			continue
		}
		for _, reference := range referrer.References() {
			object, isOutbound := a.outbound.Outbound(reference)
			if !isOutbound {
				continue
			}
			path := "dns/" + current.Tag() + " detour " + reference
			if err := a.checkDownloadChain(object, path, "resolve the download host through"); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkDownloadChain walks the dial chain the download would traverse and refuses the first
// provider-fed empty group this provider alone feeds.
func (a *Adapter) checkDownloadChain(start adapter.Outbound, path string, verb string) error {
	providerManager := service.FromContext[adapter.ProviderManager](a.ctx)
	visited := make(map[string]bool)
	queue := []adapter.Outbound{start}
	for len(queue) > 0 {
		object := queue[0]
		queue = queue[1:]
		if object == nil || visited[object.Tag()] {
			continue
		}
		visited[object.Tag()] = true
		group, isGroup := object.(adapter.OutboundGroup)
		if !isGroup {
			for _, dependency := range object.Dependencies() {
				if resolved, loaded := a.outbound.Outbound(dependency); loaded {
					queue = append(queue, resolved)
				}
			}
			continue
		}
		var providerTags []string
		usesAllProviders := false
		if backed, isBacked := object.(adapter.ProviderBackedGroup); isBacked {
			providerTags, usesAllProviders = backed.ProviderMembership()
		}
		if len(group.All()) == 0 && fedOnlyByThisProvider(providerTags, usesAllProviders, a.providerTag, providerManager) {
			return E.New("provider ", a.providerTag, " cannot ", verb, " outbound group ", group.Tag(),
				" (the path is ", path, "): the group is fed only by this provider and has no ",
				"member until this provider's first download succeeds, so the download is waiting on ",
				"itself. Route the download through an http_client whose dialer does not depend on ",
				"this group, give the group a static member, or take it out of the download path")
		}
		// Follow the member the group would actually dial. References() names the committed
		// selection for a selector/urltest; a group that reports none (a balancing group, or one
		// before its first publish) is walked across its members, which over-approximates the dial
		// path rather than missing it.
		references := []string(nil)
		if referrer, isReferrer := object.(adapter.Referrer); isReferrer {
			references = referrer.References()
		}
		members := references
		if len(members) != 1 {
			members = group.All()
		}
		for _, member := range members {
			if resolved, loaded := a.outbound.Outbound(member); loaded {
				queue = append(queue, resolved)
			}
		}
	}
	return nil
}

// fedOnlyByThisProvider reports whether a provider-fed group can only be populated by self.
func fedOnlyByThisProvider(providerTags []string, usesAllProviders bool, self string, manager adapter.ProviderManager) bool {
	if usesAllProviders {
		// use_all_providers: another configured provider could publish the member this download
		// needs, so the loop is only certain when this provider is the only one.
		if manager == nil {
			return true
		}
		count := 0
		for range manager.Providers() {
			count++
		}
		return count <= 1
	}
	return len(providerTags) == 1 && providerTags[0] == self
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
//
// It returns the objects it could NOT remove. Those are the ones a group still names in its
// published membership; UpdateGroups retries them after the group callbacks, which is the only point
// at which the membership is known to have been rebuilt.
func (a *Adapter) removeUseless(newTags []string) []adapter.Outbound {
	a.outboundsAccess.RLock()
	if len(a.outbounds) == 0 {
		a.outboundsAccess.RUnlock()
		return nil
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
	return a.removeRetiredObjects(pending)
}

// removeRetiredObjects attempts removals in dependency-safe passes and returns what stayed.
func (a *Adapter) removeRetiredObjects(pending []adapter.Outbound) []adapter.Outbound {
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
			// A removed node is not merely hidden: its tag is free to be reused by the next
			// subscription, and a leftover delay would then be inherited by an unrelated object.
			a.history.DeleteURLTestHistory(ob.Tag())
		}
		if len(remaining) == len(pending) {
			if a.logger != nil && lastErr != nil {
				a.logger.Error(lastErr, "close outbound [", remaining[0].Tag(), "]")
			}
			return remaining
		}
		pending = remaining
	}
	return nil
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
