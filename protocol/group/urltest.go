package group

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	D "github.com/sagernet/sing-box/common/dialer"
	"github.com/sagernet/sing-box/common/interrupt"
	"github.com/sagernet/sing-box/common/resourceguard"
	"github.com/sagernet/sing-box/common/runtimecoord"
	"github.com/sagernet/sing-box/common/urltest"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/common/x/list"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/pause"
)

func RegisterURLTest(registry *outbound.Registry) {
	outbound.Register[option.URLTestOutboundOptions](registry, C.TypeURLTest, NewURLTest)
}

var (
	_ adapter.OutboundGroup           = (*URLTest)(nil)
	_ adapter.InterfaceUpdateListener = (*URLTest)(nil)
	_ adapter.Referrer                = (*URLTest)(nil)
	_ D.ParallelNetworkDialer         = (*URLTest)(nil)
	// A urltest may be fed by outbound providers: an empty member list before the provider has
	// published is a WAIT, not an empty declaration. See adapter.ProviderBackedGroup.
	_ adapter.ProviderBackedGroup = (*URLTest)(nil)
	// Like a selector, a urltest only chooses which member serves a flow - the delay probes it runs
	// in the background dial the members directly and never pass through the router, so they cannot
	// inherit a business flow's policy. See adapter.FastPathAuthorizedGroup.
	_ adapter.FastPathAuthorizedGroup = (*URLTest)(nil)
	// A urltest may be kept out of the client's group list while staying a full group.
	_ adapter.HiddenOutboundGroup = (*URLTest)(nil)
)

// FastPathTransparent implements adapter.FastPathAuthorizedGroup.
//
// The group's entire effect on a business flow is the choice of member; it holds no per-flow state
// and owns no dial. Its background delay probes are not business flows at all - they call the member
// outbound's DialContext directly rather than going through the router - so they neither inherit nor
// need a fast_path policy.
func (s *URLTest) FastPathTransparent() bool { return true }

// Hidden implements adapter.HiddenOutboundGroup.
//
// It answers a DISPLAY question only: the group keeps building, probing, switching and serving its
// members, and remains a legal member of another group and a legal route target.
func (s *URLTest) Hidden() bool { return s.hidden }

type URLTest struct {
	outbound.Adapter
	ctx      context.Context
	outbound adapter.OutboundManager
	logger   log.ContextLogger
	tags     []string
	link     string
	// expectedStatus is the configured expression, kept for diagnostics.
	expectedStatus string
	// scope identifies the target this group measures against. Selection, skipping and health
	// checks must only read measurements for THIS target.
	scope       urltest.MeasurementScope
	interval    time.Duration
	tolerance   uint16
	idleTimeout time.Duration
	// group is the live group, published atomically.
	//
	// It was a plain pointer guarded by checkAccess, but only the writers and two readers took that
	// lock: the traffic path dereferenced it directly. Start replaces the pointer, so a dial could
	// read it mid-replacement - a genuine data race on a field every packet touches, and an
	// inconsistent read for the caller.
	//
	// An atomic pointer makes every read race-free without putting a lock on the traffic path, which
	// is the same reasoning already applied to selectedState.
	group atomic.Pointer[URLTestGroup]
	// lifecycleAccess serialises Start against Close and guards closed.
	//
	// It is a separate, short-lived lock: it is held while deciding and publishing, and NEVER while
	// a group is being constructed or closed. Those run lifecycle callbacks - a group unregisters
	// pause callbacks and stops a ticker - and holding a lock across one would risk a deadlock if a
	// callback touched the wrapper.
	lifecycleAccess sync.Mutex
	// closed makes the wrapper terminal. A closed wrapper stays closed.
	closed                       bool
	checkAccess                  sync.Mutex
	interruptExternalConnections bool
	// hidden is a display-only, immutable configuration fact.
	//
	// It lives on the wrapper rather than on the URLTestGroup the wrapper publishes, for the same
	// reason failoverConfigured does: a provider publication REPLACES the published group, so a
	// property stored there would be silently lost on the first republication.
	hidden bool

	// Provider membership. stateAccess guards tags; providerAccess serialises rebuilds;
	// lifecycleAccess (above) guards the published group pointer, started and scope.
	stateAccess     sync.RWMutex
	providerAccess  sync.Mutex
	provider        adapter.ProviderManager
	providers       map[string]adapter.Provider
	outboundsCache  map[string][]adapter.Outbound
	providerTags    []string
	exclude         *regexp.Regexp
	include         *regexp.Regexp
	useAllProviders bool
	started         bool
	lifecycleScope  *adapter.Scope

	// failoverAccess guards the configured probing policy. It is applied to EVERY group this
	// wrapper builds, because a provider publication rebuilds the group: a policy stored only on
	// the current group would be silently lost on the first republication.
	failoverAccess sync.RWMutex
	failoverPolicy urltest.FailoverPolicy
	// failoverConfigured distinguishes "no policy was configured" (the compatibility mode) from a
	// configured policy whose fields happen to be zero. The zero FailoverPolicy is NOT disabled, so
	// treating it as the default would turn the policy on for every existing configuration.
	failoverConfigured bool
}

func NewURLTest(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.URLTestOutboundOptions) (adapter.Outbound, error) {
	outbound := &URLTest{
		Adapter:                      outbound.NewAdapter(C.TypeURLTest, tag, []string{N.NetworkTCP, N.NetworkUDP}, options.Outbounds),
		ctx:                          ctx,
		outbound:                     service.FromContext[adapter.OutboundManager](ctx),
		logger:                       logger,
		tags:                         options.Outbounds,
		link:                         options.URL,
		expectedStatus:               options.ExpectedStatus,
		interval:                     time.Duration(options.Interval),
		tolerance:                    options.Tolerance,
		idleTimeout:                  time.Duration(options.IdleTimeout),
		interruptExternalConnections: options.InterruptExistConnections,
		hidden:                       options.Hidden,
		provider:                     service.FromContext[adapter.ProviderManager](ctx),
		providers:                    make(map[string]adapter.Provider),
		outboundsCache:               make(map[string][]adapter.Outbound),
		providerTags:                 options.Providers,
		exclude:                      (*regexp.Regexp)(options.Exclude),
		include:                      (*regexp.Regexp)(options.Include),
		useAllProviders:              options.UseAllProviders,
	}
	// The configuration surface maps onto the policy here, once. An absent block keeps the historical
	// probing behaviour; a present one opts in, even when it names no field (the defaults).
	if options.Failover != nil {
		outbound.ApplyFailoverPolicy(urltest.FailoverPolicy{
			WidenAfter:   options.Failover.WidenAfter,
			SettleAfter:  options.Failover.SettleAfter,
			RecoverAfter: options.Failover.RecoverAfter,
			ToleranceMs:  options.Failover.Tolerance,
		})
	}
	// See the matching comment in NewSelector: a repeated member is legal, and the group's member
	// list keeps it while selection treats the tag as one member.
	if len(outbound.tags) == 0 && len(outbound.providerTags) == 0 && !outbound.useAllProviders {
		return nil, E.New("missing outbound and provider tags")
	}
	return outbound, nil
}

func (s *URLTest) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	switch stage {
	case adapter.StartStateStart:
		s.providerAccess.Lock()
		defer s.providerAccess.Unlock()
		if err := s.resolveProviders(); err != nil {
			return err
		}
		tags, outbounds, cache, err := collectProviderOutbounds("", s.Dependencies(), s.outbound, s.providers, s.providerTags, s.outboundsCache, s.exclude, s.include)
		if err != nil {
			return err
		}
		group, err := s.buildGroup(outbounds)
		if err != nil {
			return err
		}
		// Publish the initial membership through the manager's graph validation, exactly like a
		// provider update: an inline provider has already published nodes by the time this Start
		// runs, and a node detouring back into this group would otherwise be selected here without
		// the cycle ever being walked. See publishProviderMembership.
		var (
			previous  *URLTestGroup
			commitErr error
		)
		publishErr := publishProviderMembership(s.outbound, s.Tag(), tags, func() {
			s.lifecycleAccess.Lock()
			if s.closed {
				s.lifecycleAccess.Unlock()
				commitErr = os.ErrClosed
				return
			}
			// Detach the previous group before replacing it, and close it OUTSIDE the lock (below):
			// Close runs an arbitrary lifecycle callback (unregister pause callbacks, stop the
			// ticker), so holding the lock across it would risk a deadlock.
			previous = s.group.Swap(group)
			s.lifecycleScope = scope
			s.lifecycleAccess.Unlock()
			s.stateAccess.Lock()
			s.tags, s.outboundsCache = tags, cache
			s.stateAccess.Unlock()
		})
		if publishErr == nil {
			publishErr = commitErr
		}
		if publishErr != nil {
			// The prospective membership was refused (or the wrapper is closing): release the group
			// that was built for it and leave the previous state exactly as it was.
			if group != nil {
				_ = group.Close()
			}
			return publishErr
		}
		if previous != nil {
			_ = previous.Close()
		}
		// The scope owns each generation's teardown, so a teardown that races Start cannot leave a
		// group running unreferenced. A group that is replaced later is closed at that point; its
		// cleanup remains registered and closing an already-closed group is a no-op.
		if scope != nil {
			scope.Add(func() error {
				if group != nil {
					return group.Close()
				}
				return nil
			})
		}
		for _, providerTag := range s.providerTags {
			provider := s.providers[providerTag]
			element := provider.RegisterCallback(s.onProviderUpdated)
			if scope != nil {
				scope.Add(func() error {
					provider.UnregisterCallback(element)
					return nil
				})
			}
		}
	case adapter.StartStateStarted:
		return s.PostStart()
	}
	return nil
}

// resolveProviders binds the provider tags this group names. UseAllProviders snapshots the
// configured providers at start.
func (s *URLTest) resolveProviders() error {
	if len(s.providerTags) == 0 && !s.useAllProviders {
		return nil
	}
	if s.provider == nil {
		return E.New(s.Tag(), ": providers are configured but the provider manager is not available")
	}
	if s.useAllProviders {
		var providerTags []string
		for _, provider := range s.provider.Providers() {
			providerTags = append(providerTags, provider.Tag())
			s.providers[provider.Tag()] = provider
		}
		if len(providerTags) == 0 && len(s.Dependencies()) == 0 {
			return E.New(s.Tag(), ": use_all_providers is set but no outbound providers are configured")
		}
		// The resolved list is what ProviderMembership publishes; the write goes under stateAccess
		// so a diagnostics read cannot race it. See the matching comment in selector.go.
		s.stateAccess.Lock()
		s.providerTags = providerTags
		s.stateAccess.Unlock()
		return nil
	}
	for i, tag := range s.providerTags {
		provider, loaded := s.provider.Get(tag)
		if !loaded {
			return E.New("outbound provider ", i, " not found: ", tag)
		}
		s.providers[tag] = provider
	}
	return nil
}

// buildGroup constructs a probe group over the current member set. No members is a legal state: a
// provider may not have published anything yet, and the wrapper reports "no member" on the dial
// path instead of inventing one.
func (s *URLTest) buildGroup(outbounds []adapter.Outbound) (*URLTestGroup, error) {
	if len(outbounds) == 0 {
		return nil, nil
	}
	group, err := NewURLTestGroupWithExpected(s.ctx, s.outbound, s.logger, outbounds, s.link, s.expectedStatus, s.interval, s.tolerance, s.idleTimeout, s.interruptExternalConnections)
	if err != nil {
		return nil, err
	}
	// The configured policy is re-applied to EVERY generation. A provider publication replaces the
	// group object, so a policy installed only on the previous one would silently revert the
	// configuration to the compatibility mode after the first refresh.
	s.failoverAccess.RLock()
	policy := s.failoverPolicy
	configured := s.failoverConfigured
	s.failoverAccess.RUnlock()
	if !configured {
		policy = urltest.FailoverPolicy{Disabled: true}
	}
	group.ApplyFailoverPolicy(policy)
	return group, nil
}

// ApplyFailoverPolicy installs the stable/failover probing policy on this group and on every group
// it rebuilds after a provider publication.
//
// It is the WIRING POINT for the configuration surface: the configuration window maps its fields to
// a FailoverPolicy and calls this once, at construction time. A configuration that does not call it
// keeps the historical behaviour - every member probed every round - byte for byte.
func (s *URLTest) ApplyFailoverPolicy(policy urltest.FailoverPolicy) {
	if s == nil {
		return
	}
	s.failoverAccess.Lock()
	s.failoverPolicy = policy
	s.failoverConfigured = true
	s.failoverAccess.Unlock()
	if group := s.group.Load(); group != nil {
		group.ApplyFailoverPolicy(policy)
	}
}

// HealthSnapshot samples the current group's health and queue state.
func (s *URLTest) HealthSnapshot() HealthSnapshot {
	if s == nil {
		return HealthSnapshot{}
	}
	group := s.group.Load()
	if group == nil {
		return HealthSnapshot{Disabled: true}
	}
	return group.HealthSnapshot()
}

// QueueSnapshot samples the current group's admission counters only. See URLTestGroup.QueueSnapshot:
// this is the cheap form for a sampler.
func (s *URLTest) QueueSnapshot() (inFlight int, queued int, limit int, sharedInFlight int, participants int) {
	if s == nil {
		return 0, 0, 0, 0, 0
	}
	group := s.group.Load()
	if group == nil {
		return 0, 0, 0, 0, 0
	}
	return group.QueueSnapshot()
}

// CheckOutboundsHealth runs one health round on the current group with explicit force semantics.
//
// force=false is the periodic semantics, where the stable/failover probing policy decides the probe
// set; force=true measures every member. It is exported on the wrapper so a caller that holds the
// OUTBOUND (a diagnostics or scale harness) can drive a round without reaching into the group, and
// so the two round shapes are the same ones the ticker and a manual test use. The zero-argument
// CheckOutbounds entry is the background form and stays forced, which is what its callers mean.
func (s *URLTest) CheckOutboundsHealth(ctx context.Context, force bool) {
	if s == nil {
		return
	}
	if group := s.group.Load(); group != nil {
		group.CheckOutbounds(ctx, force)
	}
}

// onProviderUpdated rebuilds the probe group after a provider published a new node set.
//
// The group is RECREATED rather than mutated: its health loop, ticker and per-target state are
// owned by the group, and a membership change invalidates the selection it made. The measurement
// history lives in the shared URL-test storage, so the new generation starts with the results that
// are still relevant.
func (s *URLTest) onProviderUpdated(tag string) error {
	s.providerAccess.Lock()
	tags, outbounds, cache, err := collectProviderOutbounds(tag, s.Dependencies(), s.outbound, s.providers, s.providerTags, s.outboundsCache, s.exclude, s.include)
	if err != nil {
		s.providerAccess.Unlock()
		return E.Cause(err, s.Tag())
	}
	group, err := s.buildGroup(outbounds)
	if err != nil {
		s.providerAccess.Unlock()
		return err
	}
	// Validate the prospective membership BEFORE the generation is swapped. A provider update whose
	// node detours back into this group closes a recursion; refusing it here keeps the previous
	// members, the previous probe generation and its selection exactly as they were.
	var (
		previous  *URLTestGroup
		started   bool
		scope     *adapter.Scope
		commitErr error
	)
	publishErr := publishProviderMembership(s.outbound, s.Tag(), tags, func() {
		s.lifecycleAccess.Lock()
		if s.closed {
			s.lifecycleAccess.Unlock()
			commitErr = os.ErrClosed
			return
		}
		previous = s.group.Swap(group)
		started = s.started
		scope = s.lifecycleScope
		s.lifecycleAccess.Unlock()
		s.stateAccess.Lock()
		s.tags, s.outboundsCache = tags, cache
		s.stateAccess.Unlock()
	})
	if publishErr == nil {
		publishErr = commitErr
	}
	if publishErr != nil {
		if group != nil {
			_ = group.Close()
		}
		s.providerAccess.Unlock()
		return publishErr
	}
	if scope != nil && group != nil {
		scope.Add(group.Close)
	}
	if previous != nil {
		_ = previous.Close()
	}
	if group != nil && started {
		group.PostStart()
	}
	s.providerAccess.Unlock()
	return nil
}

func (s *URLTest) PostStart() error {
	s.lifecycleAccess.Lock()
	if s.closed {
		s.lifecycleAccess.Unlock()
		return os.ErrClosed
	}
	s.started = true
	group := s.currentGroup()
	s.lifecycleAccess.Unlock()
	if group == nil {
		// No members yet (a provider has not published anything). That is not a closed wrapper and
		// not a failure: there is simply no background work to start until the first node arrives.
		return nil
	}
	group.PostStart()
	return nil
}

func (s *URLTest) Close() error {
	// Terminal BEFORE detaching, so a Start that is mid-construction cannot publish afterwards.
	s.lifecycleAccess.Lock()
	if s.closed {
		s.lifecycleAccess.Unlock()
		return nil
	}
	s.closed = true
	detached := s.group.Swap(nil)
	s.lifecycleAccess.Unlock()

	// Close OUTSIDE the lock: the group runs lifecycle callbacks, and holding the lock across them
	// would deadlock if a callback touched the wrapper.
	return common.Close(
		common.PtrOrNil(detached),
	)
}

func (s *URLTest) All() []string {
	s.stateAccess.RLock()
	defer s.stateAccess.RUnlock()
	return slices.Clone(s.tags)
}

// ProviderMembership implements adapter.ProviderBackedGroup; see the matching method in selector.go.
func (s *URLTest) ProviderMembership() ([]string, bool) {
	s.stateAccess.RLock()
	defer s.stateAccess.RUnlock()
	return slices.Clone(s.providerTags), s.useAllProviders
}

// noGroupError explains a dial that has no group to send through. It distinguishes a closed wrapper
// from a group whose provider has not published a node yet, because the two need different actions.
func (s *URLTest) noGroupError() error {
	s.lifecycleAccess.Lock()
	closed := s.closed
	s.lifecycleAccess.Unlock()
	if closed {
		return os.ErrClosed
	}
	providerTags, usesAllProviders := s.ProviderMembership()
	switch {
	case len(providerTags) > 0:
		if published := s.publishedNodeCount(providerTags); published > 0 {
			return E.New("outbound group ", s.Tag(), " has no available member yet: outbound provider ",
				strings.Join(providerTags, ", "), " has published ", published, " node(s), but this group's ",
				"include/exclude filters exclude all of them")
		}
		return E.New("outbound group ", s.Tag(), " has no available member yet: it is waiting for outbound provider ",
			strings.Join(providerTags, ", "), " to publish nodes")
	case usesAllProviders:
		return E.New("outbound group ", s.Tag(), " has no available member yet: it is waiting for the configured outbound providers to publish nodes")
	}
	return E.New("outbound group ", s.Tag(), " has no available member yet")
}

// publishedNodeCount is the diagnostic read described on Selector.publishedNodeCount.
func (s *URLTest) publishedNodeCount(providerTags []string) int {
	s.providerAccess.Lock()
	defer s.providerAccess.Unlock()
	count := 0
	for _, tag := range providerTags {
		if provider := s.providers[tag]; provider != nil {
			count += len(provider.Outbounds())
		}
	}
	return count
}

// currentGroup returns the live group, or nil when the wrapper has none.
//
// Every reader goes through here, so the pointer is loaded once and the nil check cannot race with
// a concurrent Start or Close.
func (s *URLTest) currentGroup() *URLTestGroup {
	return s.group.Load()
}

// Selected returns the member the current probe generation chose, or nil.
//
// It is a snapshot for immediate inspection: a provider refresh rebuilds the probe generation and
// may eventually close this object after the publication barrier. Dial through
// DialContext/ListenPacket (which lease the member for the hand-off) or hold an explicit lease; do
// not keep the object and dial it after an update. See adapter.OutboundGroup.Selected.
func (s *URLTest) Selected(network string) adapter.Outbound {
	current := s.currentGroup()
	if current == nil {
		return nil
	}
	var outbound adapter.Outbound
	if state := current.selected.Load(); state != nil {
		if network == N.NetworkUDP {
			outbound = state.udp
		} else {
			outbound = state.tcp
		}
	}
	if outbound == nil {
		outbound, _ = current.Select(network)
	}
	return outbound
}

func (s *URLTest) AttachConnection(closer io.Closer) func() {
	current := s.currentGroup()
	if current == nil {
		return func() {}
	}
	current.Touch()
	return current.interruptGroup.Add(closer, true)
}

func (s *URLTest) References() []string {
	group := s.currentGroup()
	if group == nil {
		return nil
	}
	state := group.selected.Load()
	if state == nil {
		return nil
	}
	var references []string
	if state.tcp != nil {
		references = append(references, state.tcp.Tag())
	}
	if state.udp != nil && state.udp != state.tcp {
		references = append(references, state.udp.Tag())
	}
	return references
}

func (s *URLTest) URLTest(ctx context.Context) (map[string]uint16, error) {
	group := s.currentGroup()
	if group == nil {
		// A closed wrapper has no measurement to run. Reporting success with no results would tell
		// the caller the test ran and found nothing, which is a different statement.
		return nil, s.noGroupError()
	}
	return group.URLTest(ctx)
}

func (s *URLTest) CheckOutbounds() {
	// No group means there is nothing to check. A no-op rather than an error: this is called from
	// lifecycle and refresh paths that ignore the result, and there is no failure a caller could act
	// on.
	//
	// The origin defaults to automatic, which is the safe direction: an automatic round does not
	// wake an idle resource. A caller that is acting on a person's request must say so through
	// CheckOutboundsContext.
	s.CheckOutboundsContext(s.ctx)
}

// CheckOutboundsContext runs a health round, carrying the caller's declared measurement origin.
//
// The caller's context contributes VALUES only - it does not become the operation's base, which
// stays the group's own so that Close still cancels and the Box services stay reachable; see
// operationContext. It exists so the origin marker reaches the measurement.
func (s *URLTest) CheckOutboundsContext(ctx context.Context) {
	group := s.currentGroup()
	if group == nil {
		return
	}
	group.CheckOutbounds(ctx, true)
}

func (s *URLTest) PerformUpdateCheck() {
	group := s.currentGroup()
	if group == nil {
		return
	}
	group.performUpdateCheck()
}

func (s *URLTest) InterfaceUpdated(ctx context.Context) {
	group := s.currentGroup()
	if group == nil {
		return
	}
	if group.pause.IsDevicePaused() || group.pause.IsNetworkPaused() {
		return
	}
	go func() {
		s.checkAccess.Lock()
		defer s.checkAccess.Unlock()
		if ctx.Err() != nil {
			return
		}
		group.CheckOutbounds(ctx, true)
	}()
}

// acquireMember pins the selected member for the hand-off about to happen. See the matching helper
// in selector.go and adapter.OutboundLeaseManager: the publication barrier must not close an object
// a dial already obtained.
func (s *URLTest) acquireMember(member adapter.Outbound) (func(), bool) {
	if manager, isManager := s.outbound.(adapter.OutboundLeaseManager); isManager {
		return manager.AcquireOutbound(member)
	}
	return func() {}, true
}

// leaseSelected pins outbound for the hand-off. If a publication closed it before the pin was taken,
// it re-reads the generation's selection (the callback already moved it to the replacement) and pins
// that instead; ok=false means even the re-read member was closed, so the caller refuses the flow
// instead of dialling a closed object.
func (s *URLTest) leaseSelected(current *URLTestGroup, network string, outbound adapter.Outbound) (adapter.Outbound, func(), bool) {
	if release, ok := s.acquireMember(outbound); ok {
		return outbound, release, true
	}
	// The generation this call started with holds a member that the publication barrier already
	// closed. Re-read the LIVE generation: Start/onProviderUpdated swap the pointer before the
	// barrier runs, so the replacement is already visible there.
	reselectGroup := current
	if latest := s.currentGroup(); latest != nil {
		reselectGroup = latest
	}
	var next adapter.Outbound
	if state := reselectGroup.selected.Load(); state != nil {
		if network == N.NetworkUDP {
			next = state.udp
		} else {
			next = state.tcp
		}
	}
	if next == nil {
		next, _ = reselectGroup.Select(network)
	}
	if next == nil {
		return nil, func() {}, false
	}
	release, ok := s.acquireMember(next)
	return next, release, ok
}
func (s *URLTest) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	current := s.currentGroup()
	if current == nil {
		return nil, s.noGroupError()
	}
	current.Touch()
	var outbound adapter.Outbound
	if state := current.selected.Load(); state != nil {
		switch N.NetworkName(network) {
		case N.NetworkTCP:
			outbound = state.tcp
		case N.NetworkUDP:
			outbound = state.udp
		default:
			return nil, E.Extend(N.ErrUnknownNetwork, network)
		}
	} else if N.NetworkName(network) != N.NetworkTCP && N.NetworkName(network) != N.NetworkUDP {
		return nil, E.Extend(N.ErrUnknownNetwork, network)
	}
	if outbound == nil {
		outbound, _ = current.Select(network)
	}
	if outbound == nil {
		return nil, E.New("missing supported outbound")
	}
	outbound, release, ok := s.leaseSelected(current, network, outbound)
	if !ok {
		return nil, E.New("outbound group ", s.Tag(), ": the selected member was replaced during connection setup")
	}
	defer release()
	adapter.RecordDialStep(ctx, s, outbound)
	conn, err := outbound.DialContext(ctx, network, destination)
	if err == nil {
		return current.interruptGroup.NewConn(conn, interrupt.IsExternalConnectionFromContext(ctx)), nil
	}
	s.logger.ErrorContext(ctx, err)
	// A real connection failed, so the node is not usable now regardless of what an earlier
	// measurement said. Only this target's result is invalidated, and only if this outbound is
	// still the selected one - a concurrent update may already have moved on.
	//
	// A DIAGNOSTIC dial is exempt: it runs the production path so the report can describe it, but it
	// is not a flow. Clearing the selection here would let a probe taken while the user's network was
	// briefly down move the member their traffic uses - and the forced recheck that follows the clear
	// can delete the member's health measurement, so the probe would rewrite the evidence it was
	// meant to read. The dial and its failure still reach the caller and the report.
	if !adapter.IsDiagnosticDial(ctx) {
		current.clearSelectionFor(network, outbound)
	}
	return nil, err
}

func (s *URLTest) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	current := s.currentGroup()
	if current == nil {
		return nil, s.noGroupError()
	}
	current.Touch()
	var outbound adapter.Outbound
	if state := current.selected.Load(); state != nil {
		outbound = state.udp
	}
	if outbound == nil {
		outbound, _ = current.Select(N.NetworkUDP)
	}
	if outbound == nil {
		return nil, E.New("missing supported outbound")
	}
	outbound, release, ok := s.leaseSelected(current, N.NetworkUDP, outbound)
	if !ok {
		return nil, E.New("outbound group ", s.Tag(), ": the selected member was replaced during connection setup")
	}
	defer release()
	adapter.RecordDialStep(ctx, s, outbound)
	conn, err := outbound.ListenPacket(ctx, destination)
	if err == nil {
		return current.interruptGroup.NewPacketConn(conn, interrupt.IsExternalConnectionFromContext(ctx)), nil
	}
	s.logger.ErrorContext(ctx, err)
	if !adapter.IsDiagnosticDial(ctx) {
		current.clearSelectionFor(N.NetworkUDP, outbound)
	}
	return nil, err
}

func (s *URLTest) DialParallelNetwork(ctx context.Context, network string, destination M.Socksaddr, destinationAddresses []netip.Addr, strategy *C.NetworkStrategy, interfaceType []C.InterfaceType, fallbackInterfaceType []C.InterfaceType, fallbackDelay time.Duration) (net.Conn, error) {
	current := s.currentGroup()
	if current == nil {
		return nil, s.noGroupError()
	}
	current.Touch()
	var outbound adapter.Outbound
	if state := current.selected.Load(); state != nil {
		switch N.NetworkName(network) {
		case N.NetworkTCP:
			outbound = state.tcp
		case N.NetworkUDP:
			outbound = state.udp
		default:
			return nil, E.Extend(N.ErrUnknownNetwork, network)
		}
	} else if N.NetworkName(network) != N.NetworkTCP && N.NetworkName(network) != N.NetworkUDP {
		return nil, E.Extend(N.ErrUnknownNetwork, network)
	}
	if outbound == nil {
		outbound, _ = current.Select(network)
	}
	if outbound == nil {
		return nil, E.New("missing supported outbound")
	}
	outbound, release, ok := s.leaseSelected(current, network, outbound)
	if !ok {
		return nil, E.New("outbound group ", s.Tag(), ": the selected member was replaced during connection setup")
	}
	defer release()
	adapter.RecordDialStep(ctx, s, outbound)
	var (
		conn net.Conn
		err  error
	)
	if parallel, isParallel := outbound.(D.ParallelNetworkDialer); isParallel {
		conn, err = parallel.DialParallelNetwork(ctx, network, destination, destinationAddresses, strategy, interfaceType, fallbackInterfaceType, fallbackDelay)
	} else {
		conn, err = D.DialSerialNetwork(ctx, outbound, network, destination, destinationAddresses, strategy, interfaceType, fallbackInterfaceType, fallbackDelay)
	}
	if err == nil {
		return current.interruptGroup.NewConn(conn, interrupt.IsExternalConnectionFromContext(ctx)), nil
	}
	s.logger.ErrorContext(ctx, err)
	if !adapter.IsDiagnosticDial(ctx) {
		current.clearSelectionFor(network, outbound)
	}
	return nil, err
}

func (s *URLTest) ListenSerialNetworkPacket(ctx context.Context, destination M.Socksaddr, destinationAddresses []netip.Addr, strategy *C.NetworkStrategy, interfaceType []C.InterfaceType, fallbackInterfaceType []C.InterfaceType, fallbackDelay time.Duration) (net.PacketConn, netip.Addr, error) {
	current := s.currentGroup()
	if current == nil {
		return nil, netip.Addr{}, os.ErrClosed
	}
	current.Touch()
	var outbound adapter.Outbound
	if state := current.selected.Load(); state != nil {
		outbound = state.udp
	}
	if outbound == nil {
		outbound, _ = current.Select(N.NetworkUDP)
	}
	if outbound == nil {
		return nil, netip.Addr{}, E.New("missing supported outbound")
	}
	outbound, release, ok := s.leaseSelected(current, N.NetworkUDP, outbound)
	if !ok {
		return nil, netip.Addr{}, E.New("outbound group ", s.Tag(), ": the selected member was replaced during connection setup")
	}
	defer release()
	adapter.RecordDialStep(ctx, s, outbound)
	var (
		conn    net.PacketConn
		address netip.Addr
		err     error
	)
	if parallel, isParallel := outbound.(D.ParallelNetworkDialer); isParallel {
		conn, address, err = parallel.ListenSerialNetworkPacket(ctx, destination, destinationAddresses, strategy, interfaceType, fallbackInterfaceType, fallbackDelay)
	} else {
		conn, address, err = D.ListenSerialNetworkPacket(ctx, outbound, destination, destinationAddresses, strategy, interfaceType, fallbackInterfaceType, fallbackDelay)
	}
	if err == nil {
		return current.interruptGroup.NewPacketConn(conn, interrupt.IsExternalConnectionFromContext(ctx)), address, nil
	}
	s.logger.ErrorContext(ctx, err)
	if !adapter.IsDiagnosticDial(ctx) {
		current.clearSelectionFor(N.NetworkUDP, outbound)
	}
	return nil, address, err
}

// selectedState is one immutable generation of the group's selection.
//
// It is replaced wholesale, never mutated, so a reader that has loaded a pointer can use both
// fields without them changing underneath it.
type selectedState struct {
	tcp adapter.Outbound
	udp adapter.Outbound
}

type URLTestGroup struct {
	ctx context.Context
	// cancelBackground cancels ctx, which is a child of the caller's context created by
	// NewURLTestGroup. Every background check runs on it, so Close stops work that is already
	// running rather than only preventing new work from starting.
	cancelBackground context.CancelFunc
	outbound         adapter.OutboundManager
	pause            pause.Manager
	pauseCallback    *list.Element[pause.Callback]
	logger           log.Logger
	outbounds        []adapter.Outbound
	// link is target.RequestURL: the spelling the operator configured, which is what is fetched.
	//
	// It is deliberately NOT the canonical identity. Storing the canonical form here is what let a
	// scope canonicalisation rewrite the request on the automatic group path.
	link string
	// target carries the three separate uses of the configured URL.
	target urltest.MeasurementTarget
	// expected is the parsed status set its health checks accept, and is part of the scope.
	expected urltest.ExpectedStatus
	// scope identifies the target this group measures against, so selection and skipping only
	// ever read measurements made against that same target.
	scope       urltest.MeasurementScope
	interval    time.Duration
	tolerance   uint16
	idleTimeout time.Duration
	history     *urltest.HistoryStorage
	// failover is the stable/failover PROBE policy. Its zero value here (disabled) keeps the
	// historical behaviour: every member is probed every round. The configuration window will map
	// the policy fields onto it; until then the wiring is exercised by the in-package tests, which
	// set it explicitly.
	failover *urltest.FailoverState
	// checking serialises health ROUNDS. It is what stops a periodic check and a forced recheck
	// from measuring the same members at the same time.
	checking atomic.Bool
	// recheckAccess guards the two fields below.
	//
	// This is not a traffic hot path - it is touched at most once per failing connection - so a
	// mutex is the right tool. Two independent atomics could each be correct alone while the pair
	// still lost a request, which is exactly the bug being fixed here.
	recheckAccess sync.Mutex
	// recheckRequested counts recheck requests; recheckServed counts those a completed round has
	// accounted for. Debt exists while requested > served.
	//
	// # Why a sequence and not a flag
	//
	// A flag says only "something is owed". That is not enough to recover from a panic, because the
	// recovery has to tell apart two debts that look identical to a boolean:
	//
	//	the debt the PANICKING round was already serving  -> must NOT be retried
	//	debt created WHILE it ran, by a genuine failure    -> must NOT be lost
	//
	// Retrying the first turns a reproducible panic into an unbounded retry loop. Dropping the
	// second leaves debt with no worker to serve it. A sequence distinguishes them by value: a
	// round records the sequence it is serving, so anything above that number arrived during the
	// round and needs a replacement worker.
	recheckRequested uint64
	recheckServed    uint64
	// recheckWorker records that a worker exists to serve the outstanding sequence, so a burst does
	// not start a goroutine per failure.
	recheckWorker bool
	// recheckRuns counts forced rounds actually executed. It exists so a test can assert the
	// guarantee as a fact rather than inferring it from timing.
	recheckRuns atomic.Int32
	// forcedRoundOverride replaces the body of one forced round in tests.
	//
	// It exists because the worker's panic recovery guards work that runs on the worker's OWN
	// goroutine, and nothing in the production code path can be made to panic on demand from a
	// test. Nil in production, where runForcedRound is used directly.
	forcedRoundOverride func()
	// selected holds the TCP and UDP choices as ONE immutable generation.
	//
	// They were two bare interface fields, written by the background health check and read by
	// real traffic. That is a data race, and reading them separately could yield two different
	// generations within one operation. Publishing them together means a reader loads once and
	// sees a consistent pair.
	selected atomic.Pointer[selectedState]
	// probeCursorTag is the tag the previous health round started its stale tail AFTER, so the order a
	// large provider is walked in rotates instead of always beginning with the same member. It is
	// anchored on a tag rather than an index because members are added, removed and reordered between
	// rounds; see orderProbeMembers.
	probeCursorTag               atomic.Pointer[string]
	interruptGroup               *interrupt.Group
	interruptExternalConnections bool
	access                       sync.Mutex
	updateAccess                 sync.Mutex
	// selectionCommit serialises the two paths that DECIDE a selection from evidence: the
	// evidence-based update and the failover policy's commit. It is taken before updateAccess (and
	// before any history lock), so the two paths cannot interleave and cannot invert locks: the
	// failover commit holds it while it validates the evidence under the history's own lock and
	// publishes the decision.
	selectionCommit sync.Mutex
	ticker          *time.Ticker
	close           chan struct{}
	started         bool
	// closed is the terminal state. It is set by Close under access and checked by Touch, so a
	// Touch can never start background work on a closed group.
	closed     bool
	lastActive common.TypedValue[time.Time]
}

func NewURLTestGroup(ctx context.Context, outboundManager adapter.OutboundManager, logger log.Logger, outbounds []adapter.Outbound, link string, interval time.Duration, tolerance uint16, idleTimeout time.Duration, interruptExternalConnections bool) (*URLTestGroup, error) {
	return NewURLTestGroupWithExpected(ctx, outboundManager, logger, outbounds, link, "", interval, tolerance, idleTimeout, interruptExternalConnections)
}

// NewURLTestGroupWithExpected builds a group whose health checks accept only the given HTTP
// statuses.
//
// An empty expectedStatus means no constraint, which is the historical behaviour and keeps every
// existing configuration working unchanged. A configuration that needs a strict 204 says so.
func NewURLTestGroupWithExpected(ctx context.Context, outboundManager adapter.OutboundManager, logger log.Logger, outbounds []adapter.Outbound, link string, expectedStatus string, interval time.Duration, tolerance uint16, idleTimeout time.Duration, interruptExternalConnections bool) (*URLTestGroup, error) {
	if interval == 0 {
		interval = C.DefaultURLTestInterval
	}
	if tolerance == 0 {
		tolerance = 50
	}
	if idleTimeout == 0 {
		idleTimeout = C.DefaultURLTestIdleTimeout
	}
	// Durations are validated rather than assumed.
	//
	// A negative value used to reach time.NewTicker, which panics at runtime - long after the
	// configuration was accepted, and with a stack trace instead of a diagnostic.
	if interval < 0 {
		return nil, E.New("interval must not be negative")
	}
	if idleTimeout < 0 {
		return nil, E.New("idle_timeout must not be negative")
	}
	if interval > idleTimeout {
		return nil, E.New("interval must be less or equal than idle_timeout")
	}
	history := service.PtrFromContext[urltest.HistoryStorage](ctx)
	if history == nil {
		return nil, E.New("missing URL test history storage")
	}
	// Resolve the target once, here, and refuse an unusable one now.
	//
	// Deferring this would let a group start, look healthy, and only discover after the first
	// interval that every measurement fails - by which time the operator has been told nothing.
	// The request target and health identity are kept separate: RequestURL controls what is
	// fetched, ScopeURL controls which health evidence the result belongs to.
	//
	// The expected status is parsed HERE, with the target, so an unusable expression fails
	// configuration rather than only surfacing when the first background check runs.
	expected, err := urltest.ParseExpectedStatus(expectedStatus)
	if err != nil {
		return nil, E.Cause(err, "invalid expected_status")
	}
	// The request target and the health identity are intentionally kept SEPARATE:
	// RequestURL controls what is fetched, and ScopeURL controls which health evidence the result
	// belongs to. Collapsing them into one string means a canonicalisation chosen for identity
	// silently rewrites what is requested - which is what this group used to do, by storing the
	// canonical form as its link and measuring with it.
	target, err := urltest.ParseMeasurementTarget(link)
	if err != nil {
		return nil, E.Cause(err, "invalid URL test target")
	}
	scope := urltest.MeasurementScope{
		URL:      target.ScopeURL,
		Expected: expected.Canonical(),
	}
	// The group owns its background lifetime. Storing the caller's context alone meant Close could
	// not stop a check that was already running: it could only stop the loop that schedules them,
	// and a recheck requested by a failing connection started a goroutine nothing could cancel.
	groupCtx, cancelBackground := context.WithCancel(ctx)

	return &URLTestGroup{
		ctx:                          groupCtx,
		cancelBackground:             cancelBackground,
		outbound:                     outboundManager,
		logger:                       logger,
		outbounds:                    outbounds,
		link:                         target.RequestURL,
		target:                       target,
		expected:                     expected,
		scope:                        scope,
		interval:                     interval,
		tolerance:                    tolerance,
		failover:                     urltest.NewFailoverState(urltest.FailoverPolicy{Disabled: true}, ""),
		idleTimeout:                  idleTimeout,
		history:                      history,
		close:                        make(chan struct{}),
		pause:                        service.FromContext[pause.Manager](ctx),
		interruptGroup:               interrupt.NewGroup(),
		interruptExternalConnections: interruptExternalConnections,
	}, nil
}

func (g *URLTestGroup) PostStart() {
	g.access.Lock()
	defer g.access.Unlock()
	// A closed group is terminal. Setting started here would leave the group reporting itself
	// started while it is closed, and would queue background work for a group whose services are
	// gone - the exact contradiction `closed` exists to prevent.
	if g.closed {
		return
	}
	g.started = true
	g.lastActive.Store(time.Now())
	go g.CheckOutbounds(g.ctx, false)
}

// MeasurementScope reports the target and status set this group measures against.
//
// It is read-only and exists so a display layer can read the group's OWN health evidence rather
// than the process-wide display history. Selecting a member from one measurement and showing
// another's delay is how a UI ends up contradicting the selection it is describing.
func (s *URLTest) MeasurementScope() urltest.MeasurementScope {
	// A closed wrapper has no scope. Returning the zero value rather than panicking keeps this a
	// safe read for a display layer, which asks for it to label a group and can legitimately race a
	// teardown.
	group := s.currentGroup()
	if group == nil {
		return urltest.MeasurementScope{}
	}
	return group.scope
}

// backgroundContext reports the context this group's background work runs on.
//
// It exists so a test can assert that Close cancels in-flight work, rather than inferring it from
// the absence of some other effect.
func (g *URLTestGroup) backgroundContext() context.Context {
	return g.ctx
}

func (g *URLTestGroup) Touch() {
	g.access.Lock()
	defer g.access.Unlock()

	// Both flags are read UNDER THE LOCK. The started check used to happen before it, which was a
	// data race against PostStart and Close, and let a Touch interleave with a Close: it could
	// observe started == true, then register a ticker and a pause callback on a group that was
	// being torn down, leaving a background task running after close.
	if !g.started || g.closed {
		return
	}
	if g.ticker != nil {
		g.lastActive.Store(time.Now())
		return
	}
	ticker := time.NewTicker(g.interval)
	g.ticker = ticker
	g.pauseCallback = pause.RegisterTicker(g.pause, ticker, g.interval, nil)
	go g.loopCheck(ticker, g.close)
}

func (g *URLTestGroup) Close() error {
	g.access.Lock()
	defer g.access.Unlock()

	// Idempotent: a second Close must not stop a ticker twice, unregister a nil element, or close
	// an already-closed channel.
	if g.closed {
		return nil
	}

	// Terminal BEFORE anything else. A group that only cleared its ticker would still report
	// itself started, so a concurrent or later Touch could start fresh background work on it -
	// which is exactly what "closed" has to prevent.
	g.closed = true
	g.started = false

	// The ticker may never have been created: a group can be started and closed without being
	// touched. The previous early return in that case skipped the terminal transition entirely,
	// leaving the group startable.
	if g.ticker != nil {
		g.ticker.Stop()
		g.ticker = nil
	}
	if g.pauseCallback != nil {
		g.pause.UnregisterCallback(g.pauseCallback)
		g.pauseCallback = nil
	}
	close(g.close)

	// Stop work that is already running. Stopping the ticker only stops future scheduling.
	if g.cancelBackground != nil {
		g.cancelBackground()
		g.cancelBackground = nil
	}
	return nil
}

func (g *URLTestGroup) Select(network string) (adapter.Outbound, bool) {
	var minDelay uint16
	var minOutbound adapter.Outbound
	switch network {
	case N.NetworkTCP:
		if state := g.selected.Load(); state != nil && state.tcp != nil {
			if history := g.history.LoadURLTestHistoryFor(RealTag(state.tcp, N.NetworkTCP), g.scope); history != nil {
				minOutbound = state.tcp
				minDelay = history.Delay
			}
		}
	case N.NetworkUDP:
		if state := g.selected.Load(); state != nil && state.udp != nil {
			if history := g.history.LoadURLTestHistoryFor(RealTag(state.udp, N.NetworkUDP), g.scope); history != nil {
				minOutbound = state.udp
				minDelay = history.Delay
			}
		}
	}
	for _, detour := range g.outbounds {
		if !common.Contains(detour.Network(), network) {
			continue
		}
		history := g.history.LoadURLTestHistoryFor(RealTag(detour, network), g.scope)
		if history == nil {
			continue
		}
		// Widened to uint32 before adding.
		//
		// Both operands are uint16, so `history.Delay + g.tolerance` wraps: with a delay of 30000
		// and a tolerance of 50000 the sum is 14464, and the comparison then prefers a node that
		// is actually FAR slower. The tolerance is a ceiling on acceptable difference, and a
		// ceiling that inverts the ordering is worse than no tolerance at all.
		if minDelay == 0 || uint32(minDelay) > uint32(history.Delay)+uint32(g.tolerance) {
			minDelay = history.Delay
			minOutbound = detour
		}
	}
	if minOutbound == nil {
		for _, detour := range g.outbounds {
			if !common.Contains(detour.Network(), network) {
				continue
			}
			return detour, false
		}
		return nil, false
	}
	return minOutbound, true
}

// backgroundRoundAllowed reports whether the periodic health round may run now.
//
// A manual/forced round is not gated: an operator asking for a measurement is not background work.
func (g *URLTestGroup) backgroundRoundAllowed() bool {
	return resourceguard.BackgroundAllowed(g.ctx)
}

func (g *URLTestGroup) loopCheck(ticker *time.Ticker, closeChan <-chan struct{}) {
	if time.Since(g.lastActive.Load()) > g.interval && g.backgroundRoundAllowed() {
		g.lastActive.Store(time.Now())
		g.CheckOutbounds(g.ctx, false)
	}
	for {
		select {
		case <-closeChan:
			return
		case <-ticker.C:
		}
		if time.Since(g.lastActive.Load()) > g.idleTimeout {
			g.access.Lock()
			if g.ticker == ticker {
				g.ticker.Stop()
				g.ticker = nil
				g.pause.UnregisterCallback(g.pauseCallback)
				g.pauseCallback = nil
			}
			g.access.Unlock()
			return
		}
		if g.backgroundRoundAllowed() {
			g.CheckOutbounds(g.ctx, false)
		}
	}
}

func (g *URLTestGroup) CheckOutbounds(ctx context.Context, force bool) {
	_, _ = g.urlTest(ctx, force)
}

func (g *URLTestGroup) URLTest(ctx context.Context) (map[string]uint16, error) {
	return g.urlTest(ctx, true)
}

// ApplyFailoverPolicy installs the stable/failover probing policy on this group.
//
// It is the WIRING POINT for the configuration surface: the configuration window maps its fields to
// a FailoverPolicy and calls this once, at construction time, before the group starts. The zero
// policy (or a nil call) keeps the historical behaviour - every member probed every round - so an
// installation that does not call it is byte-for-byte the compatibility mode.
//
// It must be called before the group's first round (PostStart starts the ticker). The policy is
// read by the health-round worker, which is serialised by checking; a later call would race that
// reader, which is why this is a construction-time setter rather than a live knob.
func (g *URLTestGroup) ApplyFailoverPolicy(policy urltest.FailoverPolicy) {
	if g == nil {
		return
	}
	state := urltest.NewFailoverState(policy, g.currentSelectedTag())
	g.access.Lock()
	g.failover = state
	g.access.Unlock()
}

// failoverState returns the current policy pointer. The pointer is replaced by
// ApplyFailoverPolicy, so every reader takes it under the group's access lock; the state's own
// methods are internally locked against the round that mutates it.
func (g *URLTestGroup) failoverState() *urltest.FailoverState {
	g.access.Lock()
	defer g.access.Unlock()
	return g.failover
}

// HealthSnapshot is the per-group health state a scale or recovery run can record.
//
// It exists so a real-Box test can state the SELECTION facts per window (members, selected, stable,
// widened) instead of inferring them from probe counts. It returns no policy internals: the stable
// slot and the widened flag are the two facts that describe which probe set the next round will use.
//
// The coordinator/share counters are sampled from the group's own Box context, so a test can record
// the real queue depth and probe concurrency per window - the two numbers a probe count cannot give.
type HealthSnapshot struct {
	Members  int
	Selected string
	Stable   string
	Widened  bool
	Disabled bool

	// CoordinatorInFlight and CoordinatorQueued are the Box-wide measurement admission counters at
	// sampling time. CoordinatorLimit is the configured bound (0 means no coordinator).
	CoordinatorInFlight int
	CoordinatorQueued   int
	CoordinatorLimit    int
	// SharedProbesInFlight and Participants are the shared-measurement registry's counters.
	SharedProbesInFlight int
	Participants         int
	// NextProbes is the probe set the policy would measure in the NEXT periodic round. A narrowed
	// set is one member; a widened one is every member. It makes "the probe set stays narrow"
	// assertable even when the round itself skips because the evidence is fresh.
	NextProbes []string
}

// QueueSnapshot samples only the admission counters, without walking the member set.
//
// It exists for a SAMPLER: HealthSnapshot builds the next probe set, which is O(members), so a
// per-millisecond sampler of ten thousand nodes would allocate ten thousand strings per tick and
// distort the very round it is measuring (measured: a 4x slowdown in a real-Box run before this was
// split out). A sampler that only needs the queue uses this.
func (g *URLTestGroup) QueueSnapshot() (inFlight int, queued int, limit int, sharedInFlight int, participants int) {
	if g == nil {
		return 0, 0, 0, 0, 0
	}
	if coordinator := urltest.CoordinatorFromContext(g.ctx); coordinator != nil {
		inFlight = coordinator.InFlight()
		queued = coordinator.Queued()
		limit = coordinator.Limit()
	}
	if share := urltest.MeasurementShareFromContext(g.ctx); share != nil {
		sharedInFlight = share.InFlight()
		participants = share.Participants()
	}
	return
}

// HealthSnapshot samples the group's current health state.
func (g *URLTestGroup) HealthSnapshot() HealthSnapshot {
	if g == nil {
		return HealthSnapshot{}
	}
	snapshot := HealthSnapshot{Members: len(g.outbounds), Selected: g.currentSelectedTag()}
	snapshot.CoordinatorInFlight, snapshot.CoordinatorQueued, snapshot.CoordinatorLimit,
		snapshot.SharedProbesInFlight, snapshot.Participants = g.QueueSnapshot()
	failover := g.failoverState()
	if failover == nil {
		snapshot.Disabled = true
		return snapshot
	}
	snapshot.Disabled = failover.Disabled()
	snapshot.Stable = failover.Stable()
	snapshot.Widened = failover.Widened()
	tags := make([]string, 0, len(g.outbounds))
	for _, member := range g.outbounds {
		tags = append(tags, member.Tag())
	}
	snapshot.NextProbes = failover.Probes(tags)
	return snapshot
}

// operationContext anchors a health operation to the group's own Box context.
//
// # Why the caller's context cannot be the base
//
// The group owns g.ctx, which carries the Box's services - the URL-test Coordinator, the certificate
// roots, the time service - and the group's lifetime. The public entries used to pass the CALLER's
// context straight through to Measure, and the production callers are the Clash API (the HTTP
// request's context) and the native command client (a gRPC context). Neither carries Box services,
// so supplying one replaced the Box context entirely and three invariants broke together:
//
//   - the per-Box concurrency limit did not apply, because the Coordinator could not be reached
//   - a private-root endpoint failed here while the identical native measurement succeeded, because
//     the roots could not be reached
//   - Close did not cancel the measurement, because Close cancels g.ctx and the measurement was not
//     using it
//
// The caller's context therefore contributes only what a caller legitimately owns: a deadline and
// cancellation. Everything else comes from the group.
//
// It returns an error when the group is terminal, so no network work starts for a group whose
// services are gone.
func (g *URLTestGroup) operationContext(caller context.Context) (context.Context, context.CancelFunc, error) {
	if g.ctx.Err() != nil {
		return nil, nil, os.ErrClosed
	}

	// Values and lifetime come from g.ctx.
	operationCtx, cancelOperation := context.WithCancel(g.ctx)

	if caller == nil {
		return operationCtx, cancelOperation, nil
	}

	// The caller's deadline is layered ON TOP, so it still bounds the operation while the group's
	// own cancellation stays in force.
	if deadline, hasDeadline := caller.Deadline(); hasDeadline {
		var cancelDeadline context.CancelFunc
		operationCtx, cancelDeadline = context.WithDeadline(operationCtx, deadline)
		cancelBase := cancelOperation
		cancelOperation = func() {
			cancelDeadline()
			cancelBase()
		}
	}

	// The caller's cancellation is wired in WITHOUT becoming the base. A caller that gives up stops
	// the operation, and a caller that never cancels cannot keep it alive past Close - which is
	// what the group's own context still governs.
	stopCallerWatch := context.AfterFunc(caller, cancelOperation)
	cancelBase := cancelOperation
	cancelOperation = func() {
		stopCallerWatch()
		cancelBase()
	}

	return operationCtx, cancelOperation, nil
}

func (g *URLTestGroup) urlTest(caller context.Context, force bool) (map[string]uint16, error) {
	// Read the caller's declared origin BEFORE the operation context is rebuilt.
	//
	// operationContext anchors the operation to the group's own context and drops the caller's
	// values, keeping only its deadline and cancellation. That is deliberate, but it means a marker
	// put on the caller's context never reaches the measurement - so the decision is taken here and
	// re-applied below. Forgetting this is not a cosmetic bug: an automatic round would look
	// foreground and wake an idle tunnel engine.
	foregroundProbe := runtimecoord.ProbeOriginIsForeground(caller)
	// Anchor to the group's own Box context before anything else.
	//
	// A terminal group returns here, so a closed group cannot start a health round at all - its
	// services are gone and any result would be written for a group that no longer exists.
	ctx, cancelOperation, contextErr := g.operationContext(caller)
	if contextErr != nil {
		// A terminal group ran no measurement, so it has no result to report.
		//
		// Returning an empty map with a nil error says "the probe completed and every node was
		// unreachable", which the Clash API serialises as a 200 response body. The background path
		// discards this value, so only a SYNCHRONOUS caller is affected - and it is exactly the
		// caller that cannot distinguish an empty result from a failed one.
		return nil, contextErr
	}
	defer cancelOperation()

	// The automatic/foreground decision travels with the round, and the DEFAULT is automatic: a
	// caller that declares nothing is background work, so an idle endpoint will not be woken to be
	// measured. It is applied to the OPERATION context, because that is the one the measurement runs
	// on. See runtimecoord.ContextWithProbeOrigin.
	ctx = runtimecoord.MeasurementContext(ctx, foregroundProbe)

	if g.checking.Swap(true) {
		// A round is already running, so this one cannot proceed.
		//
		// # Why a FORCED round must not simply return here
		//
		// Returning silently is correct for a periodic check - the running round measures the same
		// members against the same target, so the work is genuinely redundant. It is NOT correct
		// for a forced round: the caller asked because a traffic failure contradicted the current
		// measurement, and the running round is a periodic one that will SKIP members whose history
		// is still fresh. Dropping it means the contradiction is never re-examined, which is the
		// defect this handoff exists to remove.
		//
		// The request is therefore queued. The worker that owns this round will run it afterwards.
		if force {
			g.queueForcedRecheck()
			// A caller that asked for a SYNCHRONOUS result must not be told it succeeded.
			//
			// The deferred round has not run yet, so no result exists. Returning an empty map with a
			// nil error says "every node is unreachable", and the Clash API serialises exactly that
			// as the response body with HTTP 200 - a client asking to test a group would be told the
			// test completed and found nothing usable.
			//
			// The work is not lost: it is queued and will run. What cannot be reported is a result
			// that does not exist yet, so the caller is told the round was deferred.
			return nil, E.New("health check already in progress; the forced round was queued")
		}
		// A periodic round is genuinely redundant here: the running round measures the same members
		// against the same target. Nothing is owed and nothing was requested synchronously.
		return make(map[string]uint16), nil
	}
	defer g.checking.Store(false)
	return g.runHealthRound(ctx, force)
}

// runHealthRound performs one complete health round under a panic boundary.
//
// # Why a round-level boundary is needed
//
// A member's own probe is already contained: a panic inside DialContext becomes that member's
// error, so one bad node cannot take the process down. But substantial work runs OUTSIDE that
// boundary, on a goroutine that recovers nothing - batch.Go runs its closure on a fresh goroutine,
// and the traversal recurses inside it. Nested All(), Selected(), RealTag, the history reads and
// performUpdateCheck are all outside the member boundary.
//
// A panic in any of them reaches the runtime and terminates the whole process. A configuration
// describing a cycle, or a group whose traversal misbehaves, was therefore enough to kill sing-box
// rather than failing one round - and the round is the unit that can be abandoned safely.
//
// The three boundaries are separate and all three are required: member (one node fails), round (one
// round fails), worker (the forced-round state machine stays consistent).
//
// A contained panic is an ERROR, never a silent success: the round reports no results, and the
// caller sees a failed round rather than an empty but successful one.
func (g *URLTestGroup) runHealthRound(ctx context.Context, force bool) (result map[string]uint16, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if g.logger != nil {
				g.logger.Error("health round panicked: ", recovered)
			}
			result = make(map[string]uint16)
			err = E.New("health round panicked: ", recovered)
		}
	}()

	members, nextCursorTag := g.orderHealthRoundMembers()
	if nextCursorTag != "" {
		g.probeCursorTag.Store(&nextCursorTag)
	}
	// Seed the policy's stable slot from the group's COMMITTED selection before any round, including
	// a forced one.
	//
	// A forced round measures every member, so the policy sees a full observation set and would
	// otherwise pick the fastest measurement as its stable member - a second selection authority
	// beside the group's own tolerance rule. AdoptStable only fills an EMPTY slot, so a policy that
	// already holds a member is never overridden; what this guarantees is that a freshly built group
	// (a provider republication rebuilds it) starts from what traffic actually uses.
	if failover := g.failoverState(); failover != nil {
		failover.AdoptStable(g.currentSelectedTag())
	}
	probeMembers := g.healthProbeMembers(members, force)
	result, publications, evidence, roundErr := runMeasurementRound(ctx, g.outbound, g.history, g.logger, probeMembers, g.link, g.expected, g.interval, force, TestHistoryHealth)
	if roundErr != nil {
		// The round never began: the Box's round budget or the caller's context refused it before
		// any member was measured. Reporting an empty map with a nil error would tell the caller
		// "the test succeeded and every node is unreachable" - and a synchronous caller (the Clash
		// API, the native command) serialises exactly that as a successful, empty response.
		return make(map[string]uint16), roundErr
	}
	selection, selectionEvidence := g.observeFailoverRound(members, probeMembers, result, publications, evidence)
	g.commitFailoverSelection(selection, selectionEvidence)
	g.performUpdateCheck()
	return result, nil
}

// healthProbeMembers applies the stable/failover policy's probe set to a PERIODIC round.
//
// A disabled policy (the default until the configuration maps it) and every forced/manual round
// keep the historical behaviour: every member is measured. The power saving is exactly "a periodic
// check of a healthy stable member measures that member and nothing else"; an explicit test must
// never be narrowed.
func (g *URLTestGroup) healthProbeMembers(members []adapter.Outbound, force bool) []adapter.Outbound {
	failover := g.failoverState()
	if failover == nil || force || failover.Disabled() {
		return members
	}
	failover.AdoptStable(g.currentSelectedTag())
	tags := make([]string, len(members))
	for index, member := range members {
		tags[index] = member.Tag()
	}
	probes := failover.Probes(tags)
	if len(probes) == len(members) {
		return members
	}
	keep := make(map[string]bool, len(probes))
	for _, tag := range probes {
		keep[tag] = true
	}
	filtered := make([]adapter.Outbound, 0, len(probes))
	for _, member := range members {
		if keep[member.Tag()] {
			filtered = append(filtered, member)
		}
	}
	if len(filtered) == 0 {
		return members
	}
	return filtered
}

// observeFailoverRound feeds the round's observations to the policy and returns the member it says
// should carry traffic now (empty when the selection did not change), together with the provenance
// of the observation that justified it.
//
// The observations come from the round's PUBLICATION verdicts, not from "is it in the result map":
// a member that was not published was not measured for this state, and treating that absence as a
// failure is how a refused publication (a drop, a network change) became a reason to widen the probe
// set and eventually to move traffic.
//
// A success carries the provenance recorded with it: the commit re-checks it because a drop can land
// between this decision and the selection store.
func (g *URLTestGroup) observeFailoverRound(members []adapter.Outbound, probeMembers []adapter.Outbound, result map[string]uint16, publications map[string]roundPublication, evidence map[string]roundEvidence) (string, *roundEvidence) {
	failover := g.failoverState()
	if failover == nil || failover.Disabled() {
		return "", nil
	}
	tags := make([]string, len(members))
	for index, member := range members {
		tags[index] = member.Tag()
	}
	observations := make(map[string]urltest.FailoverObservation, len(probeMembers))
	for _, member := range probeMembers {
		tag := member.Tag()
		switch publications[tag] {
		case roundPublishedSuccess:
			observations[tag] = urltest.FailoverObservation{Measured: true, OK: true, DelayMs: result[tag]}
		case roundPublishedFailure:
			observations[tag] = urltest.FailoverObservation{Measured: true, OK: false}
		default:
			// Not measured, or measured for a state this round does not report: no observation at
			// all. The policy treats "not probed" as "not a failure" by design.
		}
	}
	decision := failover.Decide(tags, observations)
	if decision.Changed && decision.Selected != "" {
		if recorded, hasEvidence := evidence[decision.Selected]; hasEvidence {
			return decision.Selected, &recorded
		}
		return decision.Selected, nil
	}
	return "", nil
}

// currentSelectedTag is the member traffic currently uses, which seeds the policy's stable slot.
func (g *URLTestGroup) currentSelectedTag() string {
	state := g.selected.Load()
	if state == nil {
		return ""
	}
	if state.tcp != nil {
		return state.tcp.Tag()
	}
	if state.udp != nil {
		return state.udp.Tag()
	}
	return ""
}

// commitFailoverSelection publishes the policy's choice through the SAME selection state every
// other path uses, so readers see it and existing connections are interrupted only when the member
// actually changed.
//
// # The evidence gate and the commit are one step
//
// The decision was computed from a round's observations, and a drop can land between that decision
// and this call. The commit therefore runs INSIDE the history's own lock
// (`CommitAgainstEvidence`), which refuses it when the member's evidence is gone or its provenance
// (tag/scope stamps, network generation) has moved. The check and the store cannot be separated by
// a drop, which is exactly how the reviewed failure moved traffic to a member whose evidence had
// just been removed.
//
// The probe's OBJECT identity is checked the same way: a same-tag replacement means the observation
// describes an object the group no longer uses, so the commit is refused.
func (g *URLTestGroup) commitFailoverSelection(tag string, evidence *roundEvidence) {
	if tag == "" || g.outbound == nil {
		return
	}
	outbound, loaded := g.outbound.Outbound(tag)
	if !loaded || outbound == nil {
		return
	}
	var ownership *urltest.EvidenceOwnership
	if evidence != nil {
		if evidence.nodeIdentity != nodeIdentity(outbound) {
			g.logger.Debug("failover commit refused: ", tag, " was replaced after its probe")
			return
		}
		ownership = &evidence.ownership
	}
	// Serialise with the evidence-based selection update: both decide a selection, and the failover
	// commit holds the storage lock while it publishes, so the two must not interleave.
	g.selectionCommit.Lock()
	defer g.selectionCommit.Unlock()
	g.updateAccess.Lock()
	previous := g.selected.Load()
	next := &selectedState{}
	if previous != nil {
		next.tcp = previous.tcp
		next.udp = previous.udp
	}
	// "Changed" means the member traffic was already using was replaced. The FIRST selection is not
	// an interruption (nothing was using the old member), which is the same distinction
	// performUpdateCheck makes.
	updated := false
	selected := false
	if common.Contains(outbound.Network(), N.NetworkTCP) && next.tcp != outbound {
		if next.tcp != nil {
			updated = true
		}
		next.tcp = outbound
		selected = true
	}
	if common.Contains(outbound.Network(), N.NetworkUDP) && next.udp != outbound {
		if next.udp != nil {
			updated = true
		}
		next.udp = outbound
		selected = true
	}
	committed := g.history.CommitAgainstEvidence(tag, g.scope, ownership, func() {
		g.selected.Store(next)
	})
	g.updateAccess.Unlock()
	if !committed {
		g.logger.Debug("failover commit refused: ", tag, " no longer owns its evidence")
		return
	}
	if updated && g.interruptGroup != nil {
		g.interruptGroup.Interrupt(g.interruptExternalConnections)
	}
	if selected {
		g.history.NotifyUpdated()
	}
}

// orderHealthRoundMembers returns this round's member order and the cursor anchor for
// the next round.
func (g *URLTestGroup) orderHealthRoundMembers() ([]adapter.Outbound, string) {
	selectedTag := ""
	if state := g.selected.Load(); state != nil {
		switch {
		case state.tcp != nil:
			selectedTag = state.tcp.Tag()
		case state.udp != nil:
			selectedTag = state.udp.Tag()
		}
	}
	afterTag := ""
	if cursor := g.probeCursorTag.Load(); cursor != nil {
		afterTag = *cursor
	}
	var hasEvidence func(tag string) bool
	if g.history != nil {
		hasEvidence = func(tag string) bool {
			return g.history.LoadURLTestHistoryFor(tag, g.scope) != nil
		}
	}
	return orderProbeMembers(g.outbounds, selectedTag, hasEvidence, afterTag)
}

// orderProbeMembers puts the members a health round should submit first, first.
//
// The automatic round used to walk the member list in configuration order: stable, but
// arbitrary, and identical every round. With a provider of thousands of members the
// same head was always measured before the same tail, and the member live traffic was
// actually using had no special place. The order below serves what is load-bearing
// first and still rotates the rest:
//
//  1. the SELECTED member - live traffic depends on its evidence being current;
//  2. members with NO health evidence for this target - untested, or a failed check
//     deleted their entry; they need a measurement more than a fresh one does;
//  3. everybody else (evidence that is stale), starting AFTER the tag the previous
//     round started with, so successive rounds do not always begin at the same member.
//
// The rotation anchor is a TAG, not an index: a member added or removed between rounds
// would shift every index silently. When the anchor is gone the tail simply starts at
// the top of the remaining order, which is a stable re-anchor rather than a random one.
//
// The order is only a submission order. Coverage does not depend on it: every member
// whose evidence is stale is still submitted (the probe batch bounds concurrency, not
// membership), and a forced round skips the freshness check entirely.
func orderProbeMembers(members []adapter.Outbound, selectedTag string, hasEvidence func(tag string) bool, afterTag string) (ordered []adapter.Outbound, nextAfter string) {
	if len(members) < 2 {
		return members, ""
	}
	ordered = make([]adapter.Outbound, 0, len(members))
	var untested, stale []adapter.Outbound
	if selectedTag != "" {
		for _, member := range members {
			if member != nil && member.Tag() == selectedTag {
				ordered = append(ordered, member)
				break
			}
		}
	}
	for _, member := range members {
		if member == nil {
			continue
		}
		tag := member.Tag()
		if tag == selectedTag {
			continue
		}
		if hasEvidence != nil && hasEvidence(tag) {
			stale = append(stale, member)
		} else {
			untested = append(untested, member)
		}
	}
	ordered = append(ordered, untested...)
	if len(stale) == 0 {
		return ordered, ""
	}
	start := 0
	if afterTag != "" {
		for index, member := range stale {
			if member.Tag() == afterTag {
				start = (index + 1) % len(stale)
				break
			}
		}
	}
	// The anchor for the next round is the member this round starts the tail with, so
	// successive rounds advance one member at a time and wrap at the end.
	nextAfter = stale[start].Tag()
	ordered = append(ordered, stale[start:]...)
	ordered = append(ordered, stale[:start]...)
	return ordered, nextAfter
}

// queueForcedRecheck records that a forced round is owed and ensures a worker will run it.
//
// It is used when a round is already in flight, so the request cannot be served now.
func (g *URLTestGroup) queueForcedRecheck() {
	g.recheckAccess.Lock()
	g.recheckRequested++
	startWorker := !g.recheckWorker
	if startWorker {
		g.recheckWorker = true
	}
	g.recheckAccess.Unlock()

	if startWorker {
		go g.drainHealthRechecks()
	}
}

type urlTestBatch struct {
	ctx      context.Context
	outbound adapter.OutboundManager
	history  *urltest.HistoryStorage
	logger   log.Logger
	// scope is the target every measurement in this batch shares, so results are stored and
	// freshness is judged against the right key.
	scope   urltest.MeasurementScope
	batch   *probeBatch
	checked map[string]bool
	groups  []adapter.OutboundGroup
	// mode decides which evidence layer this round may write.
	mode TestHistoryMode
	// expected is the status set this round accepts, and is part of the scope it stores under.
	expected urltest.ExpectedStatus
	// networkCounter is the reset epoch of the network this round runs on, when the Box can report
	// it. It is read INSIDE the evidence write (through EvidenceOwnership.StillCurrent), so a reset
	// that lands between the decision to publish and the write itself is observed by the write.
	networkCounter adapter.NetworkResetCounter
	access         sync.Mutex
	result         map[string]uint16
	// publications records, per tag, whether this round has a right to REPORT what its probe found.
	// It is the same authorization the evidence write used, which is what stops a refused result
	// from being read as a success (or, after gating the result, as a failure) by the failover
	// policy.
	publications map[string]roundPublication
	// evidence is the provenance of every published success: the ownership the write used and the
	// LIVE OBJECT that was probed. Convergence and the selection commit both re-check it, so a
	// result that was legal when written but invalidated afterwards cannot be reported, observed by
	// the failover policy, or committed as a selection.
	evidence map[string]roundEvidence
}

// roundEvidence is one published result's provenance: what the write was authorized under, and the
// object that was measured.
type roundEvidence struct {
	ownership    urltest.EvidenceOwnership
	nodeIdentity string
}

// roundPublication is one member's publication verdict for one round.
//
// It is three-valued on purpose. "Not published" is not failure: the probe may have been cancelled,
// superseded by a drop, measured on a network that has since changed, or skipped because its
// evidence is still fresh. Only an authorized success and an authorized failure are observations
// about the member.
type roundPublication int

const (
	// roundNotPublished: the probe produced no evidence this round has the right to report.
	roundNotPublished roundPublication = iota
	// roundPublishedSuccess: the result was authorized and stored (or is the current evidence).
	roundPublishedSuccess
	// roundPublishedFailure: the failure was authorized and its evidence removal was applied.
	roundPublishedFailure
)

// TestHistoryMode says which evidence a measurement round is allowed to write.
//
// The distinction is not cosmetic. Selection reads the health layer, so a manual diagnostic that
// wrote there could move live traffic on the strength of a measurement taken against an unrelated
// URL - and would let a user grow that layer without bound by testing arbitrary URLs.
type TestHistoryMode int

const (
	// TestHistoryDisplayOnly records what was measured, for display, and nothing else.
	//
	// Used by every manual entry point: a Clash delay probe, a native single-node test, and a
	// native test of a generic group. None of them measured against a group's own target.
	TestHistoryDisplayOnly TestHistoryMode = iota

	// TestHistoryHealth records automatic health evidence, which selection reads.
	//
	// Used only by a URLTest group checking its own configured target.
	TestHistoryHealth
)

// TestHistoryModeFromForce maps the historical force flag onto the explicit mode.
//
// Deprecated naming kept so existing internal callers stay readable: a forced round is an automatic
// health check, an unforced one is the periodic check, and both are health.
func TestHistoryModeFromForce(force bool) TestHistoryMode {
	_ = force
	return TestHistoryHealth
}

func URLTestOutbounds(ctx context.Context, outboundManager adapter.OutboundManager, history *urltest.HistoryStorage, logger log.Logger, outbounds []adapter.Outbound, link string, interval time.Duration, force bool) map[string]uint16 {
	return URLTestOutboundsWithMode(ctx, outboundManager, history, logger, outbounds, link, interval, force, TestHistoryHealth)
}

// URLTestOutboundsWithMode runs one measurement round and records it according to mode.
func URLTestOutboundsWithMode(ctx context.Context, outboundManager adapter.OutboundManager, history *urltest.HistoryStorage, logger log.Logger, outbounds []adapter.Outbound, link string, interval time.Duration, force bool, mode TestHistoryMode) map[string]uint16 {
	return URLTestOutboundsWithTarget(ctx, outboundManager, history, logger, outbounds, link, nil, interval, force, mode)
}

// URLTestOutboundsWithTarget runs one measurement round against an explicit status set.
//
// The target is resolved once for the whole batch. An unusable one means there is nothing to
// measure, so the batch reports no results rather than failing every node individually.
//
// The returned map contains only what this round has the right to report: a probe whose evidence was
// refused (superseded by a drop, or measured on a network generation that has since moved) is NOT in
// the map, so a caller cannot read a refused result as a success.
func URLTestOutboundsWithTarget(ctx context.Context, outboundManager adapter.OutboundManager, history *urltest.HistoryStorage, logger log.Logger, outbounds []adapter.Outbound, link string, expected urltest.ExpectedStatus, interval time.Duration, force bool, mode TestHistoryMode) map[string]uint16 {
	result, _, _ := urlTestOutboundsWithTarget(ctx, outboundManager, history, logger, outbounds, link, expected, interval, force, mode)
	return result
}

// urlTestOutboundsWithTarget is the round plus its publication verdicts.
//
// The group's failover policy needs the verdicts, not the result map: a member that was not
// published was not measured (or its measurement does not describe the current state), and inferring
// "failed" from its absence is exactly how a refused publication used to move the selection.
func urlTestOutboundsWithTarget(ctx context.Context, outboundManager adapter.OutboundManager, history *urltest.HistoryStorage, logger log.Logger, outbounds []adapter.Outbound, link string, expected urltest.ExpectedStatus, interval time.Duration, force bool, mode TestHistoryMode) (map[string]uint16, map[string]roundPublication, map[string]roundEvidence) {
	result, publications, evidence, _ := runMeasurementRound(ctx, outboundManager, history, logger, outbounds, link, expected, interval, force, mode)
	return result, publications, evidence
}

// runMeasurementRound is the round itself, plus why it did not start when it did not.
//
// The error is ONLY "this round never began": it is returned when the Box's round budget or the
// caller's context refused admission before any member was measured. It exists because the group's
// synchronous entry point cannot otherwise tell "no member answered" from "nothing was measured" -
// both would be an empty map, and the Clash API serialises an empty map with a nil error as "the
// test succeeded and every node is unreachable".
func runMeasurementRound(ctx context.Context, outboundManager adapter.OutboundManager, history *urltest.HistoryStorage, logger log.Logger, outbounds []adapter.Outbound, link string, expected urltest.ExpectedStatus, interval time.Duration, force bool, mode TestHistoryMode) (map[string]uint16, map[string]roundPublication, map[string]roundEvidence, error) {
	// The Box's ROUND budget is taken before this round creates anything.
	//
	// # Why here
	//
	// This is the one entry point every round goes through: a group health check, a Clash delay
	// probe, a native group test. Taking the budget here means a new caller cannot forget it, and it
	// is taken BEFORE newProbeBatch, so the workers and the submitted-task queue of a round that is
	// not admitted do not exist at all - which is the difference between "bounded" and "bounded
	// after allocating".
	//
	// # Why a wait and not a refusal
	//
	// A refusal here would drop members: a 10,000-node round must answer all 10,000 nodes, and a
	// refused round has measured none of them. MEASURED in round 08: refusing at the measurement
	// queue answered 9,976 of 10,000. So the bound is on how many rounds PRODUCE at once, and a
	// round that cannot get a slot waits for one. The caller is held back; no node goes unmeasured.
	//
	// # Fairness
	//
	// A caller the product treats as demand keeps its queue position: an interactive round is
	// admitted ahead of queued background rounds, exactly as AcquirePriority does for one probe.
	if coordinator := urltest.CoordinatorFromContext(ctx); coordinator != nil {
		acquireRound := coordinator.AcquireRound
		if !adapter.IsBackgroundProbe(ctx) {
			acquireRound = coordinator.AcquireRoundPriority
		}
		releaseRound, roundErr := acquireRound(ctx)
		if roundErr != nil {
			// Nothing was measured, so there is no result to report and no evidence to touch. The
			// caller is told why instead of being handed an empty success.
			if logger != nil {
				logger.Debug("url test round not started: ", roundErr)
			}
			return map[string]uint16{}, map[string]roundPublication{}, map[string]roundEvidence{}, roundErr
		}
		// Released AFTER the join below (defers run last-in-first-out), so a new round is never
		// admitted while this one's workers are still unwinding.
		defer releaseRound()
	}

	// The round owns a child context, so an abort can stop the members it already started.
	//
	// # Why the join is deferred rather than inlined
	//
	// The traversal recurses and can panic - a nested group's All() or Selected(), a cycle,
	// anything outside the per-member boundary. On that path `b.Wait()` used to be skipped: the
	// round unwound and reported failure to its caller while probes started earlier were still
	// running, and they went on to write health and display history for a round that was over.
	//
	// Cancelling and joining in a defer makes the guarantee independent of how the function exits.
	// A panic still propagates; what changes is that it propagates with nothing left behind.
	roundCtx, cancelRound := context.WithCancel(ctx)
	b := newProbeBatch(cancelRound, 10, 20)

	// The defer is the ABORT guarantee, not the normal path.
	//
	// It runs on every exit, including a panic that unwinds past the join below, and it is what
	// makes "the round has returned" mean "nothing this round started is still running". On the
	// normal path the join below has already happened, so this is a no-op there - which is why it
	// can be unconditional.
	defer func() {
		cancelRound()
		b.Wait()
	}()

	// Parse the target ONCE and keep all three uses distinct.
	//
	// This used to do `link = scope.URL`, which replaced the request target with the canonical
	// identity - so everything measured through this batch fetched a rewritten URL. The batch now
	// carries the request spelling for the measurement and the scope for storage.
	target, targetErr := urltest.ParseMeasurementTarget(link)
	if targetErr != nil {
		logger.Error("invalid URL test target: ", targetErr)
		return map[string]uint16{}, map[string]roundPublication{}, map[string]roundEvidence{}, nil
	}
	scope := urltest.MeasurementScope{
		URL:      target.ScopeURL,
		Expected: expected.Canonical(),
	}
	// The reset epoch is captured from the same context the probes read it from, so the publication
	// check and the measurement key cannot disagree about which generation this round belongs to.
	var networkCounter adapter.NetworkResetCounter
	if manager := service.FromContext[adapter.NetworkManager](roundCtx); manager != nil {
		networkCounter, _ = manager.(adapter.NetworkResetCounter)
	}
	testBatch := &urlTestBatch{
		ctx:            roundCtx,
		outbound:       outboundManager,
		history:        history,
		logger:         logger,
		scope:          scope,
		batch:          b,
		checked:        make(map[string]bool),
		result:         make(map[string]uint16),
		publications:   make(map[string]roundPublication),
		evidence:       make(map[string]roundEvidence),
		mode:           mode,
		expected:       expected,
		networkCounter: networkCounter,
	}
	testBatch.test(outbounds, target.RequestURL, interval, force)

	// Join BEFORE post-processing on the normal path.
	//
	// The deferred join alone is not enough here: it runs after this function body, so reading the
	// results first would race the members that produce them - a nested group's result is assembled
	// from what its members recorded.
	b.Wait()

	// CONVERGENCE: every probe has finished, so the round is about to report its results and then
	// act on them. A result whose evidence was invalidated after its legal write (the member was
	// dropped while another probe was still in flight) must not survive this point.
	testBatch.revalidatePublished()

	for _, outboundGroup := range testBatch.groups {
		// A nested group reports the delay of whichever leaf it currently selects.
		//
		// # Where that figure comes from depends on what this round WROTE
		//
		// A health round stores under the group's scope, so the health layer holds this round's
		// result for the selected leaf. A DISPLAY-ONLY round stores in the display layer instead and
		// never touches health, so reading health here returned either nothing or - worse - a value
		// left by an earlier automatic check. The client was then shown a number that did not come
		// from the measurement it asked for.
		//
		// The leaf this round measured is therefore resolved the same way in both cases, and the
		// result is taken from THIS round's own per-tag results. That is the measurement that just
		// ran, for both modes, and it cannot be a stale value from another layer.
		//
		// The nested group's own entry is published only when its leaf's was: a group cannot report a
		// measurement that the evidence layer refused for the member that actually answered.
		realLeaf := RealTag(outboundGroup, N.NetworkTCP)
		if delay, measured := testBatch.result[realLeaf]; measured {
			testBatch.result[outboundGroup.Tag()] = delay
			testBatch.publications[outboundGroup.Tag()] = testBatch.publications[realLeaf]
			// The group's entry stands for the leaf that answered, so it carries that leaf's
			// evidence ownership - but the OBJECT the round reports is the group itself, so its
			// identity is what a later replacement check compares.
			if evidence, recorded := testBatch.evidence[realLeaf]; recorded {
				evidence.nodeIdentity = nodeIdentity(outboundGroup)
				testBatch.evidence[outboundGroup.Tag()] = evidence
			}
		}
	}
	return testBatch.result, testBatch.publications, testBatch.evidence, nil
}

// measure runs one member's probe, converting a panic into an error.
//
// # Why a member panic must not escape
//
// batch.Go runs each member on its own goroutine and does NOT recover. A panic there therefore
// reaches the runtime, which terminates the whole process - a single misbehaving outbound would
// take down sing-box rather than being reported as one unusable node.
//
// Converting it to an error also keeps the round's own bookkeeping correct: the member is treated
// as unavailable, exactly like a failed dial, so the batch still completes and the other members are
// still measured.
func (b *urlTestBatch) measure(ctx context.Context, link string, detour adapter.Outbound) (measurement urltest.Measurement, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = E.New("measurement panicked: ", recovered)
		}
	}()
	return urltest.Measure(ctx, urltest.MeasureOptions{
		Link:           link,
		ExpectedStatus: b.expected,
		Key:            b.measurementKey(ctx, link, detour),
	}, detour)
}

// publish records one member's publication verdict, and the delay when it is a published success.
//
// The result map and the verdict map are written under the same lock, so a reader can never see a
// result whose verdict is missing (or the reverse).
func (b *urlTestBatch) publish(tag string, publication roundPublication, delay uint16) {
	b.access.Lock()
	b.publications[tag] = publication
	if publication == roundPublishedSuccess {
		b.result[tag] = delay
	}
	b.access.Unlock()
}

// recordEvidence stores one published result's provenance.
func (b *urlTestBatch) recordEvidence(tag string, evidence roundEvidence) {
	b.access.Lock()
	b.evidence[tag] = evidence
	b.access.Unlock()
}

// revalidatePublished drops every published result whose evidence no longer holds.
//
// It is the CONVERGENCE boundary: the writes were authorized when they happened, but another
// member's probe can outlive a drop of this member (retirement, path invalidation, a network reset).
// Such a result is removed from the round's result map and its publication verdict becomes "not
// published", so the failover policy sees no observation at all - the reviewed failure consumed the
// stale success as "the member answered".
func (b *urlTestBatch) revalidatePublished() {
	b.access.Lock()
	defer b.access.Unlock()
	for tag, publication := range b.publications {
		if publication != roundPublishedSuccess {
			continue
		}
		evidence, recorded := b.evidence[tag]
		if !recorded {
			// A display-only batch records no evidence map entries; nothing to revalidate against.
			continue
		}
		invalid := false
		if b.history != nil && !b.history.EvidenceStillOwned(tag, b.scope, evidence.ownership) {
			invalid = true
		}
		// The OBJECT must still be the one that was measured: a provider republication replaces the
		// member under the same tag, and the retired object's result answers a question about a
		// socket the new one may not share.
		//
		// "Not found" is NOT treated as replaced: a batch driven directly by a caller whose manager
		// does not list the member (the component tests, a partial registry) cannot verify the
		// identity, and inventing a replacement there would drop every result of that batch. A
		// retirement is detected by the evidence drop the provider performs.
		if b.outbound != nil {
			if current, loaded := b.outbound.Outbound(tag); loaded && current != nil && nodeIdentity(current) != evidence.nodeIdentity {
				invalid = true
			}
		}
		if invalid {
			delete(b.result, tag)
			b.publications[tag] = roundNotPublished
			delete(b.evidence, tag)
			b.logger.Debug("outbound ", tag, " result dropped: its evidence was invalidated after the write")
		}
	}
}

// evidenceOwnership is the authority this measurement's result carries.
//
// The stamps and the reset generation travel with the PROBE, not with the caller: a round that
// joined someone else's probe publishes under the probe's provenance, which is why a drop or a
// network reset that happened after the probe started invalidates it for every waiter.
func (b *urlTestBatch) evidenceOwnership(measurement urltest.Measurement) urltest.EvidenceOwnership {
	ownership := urltest.EvidenceOwnership{
		TagEpoch:   measurement.EvidenceEpochTag,
		ScopeEpoch: measurement.EvidenceEpochScope,
	}
	if b.networkCounter != nil {
		probeGeneration := measurement.NetworkResetGeneration
		// Evaluated under the storage lock, immediately before the write. An atomic read of the
		// manager's counter, so it can neither block nor re-enter the storage.
		ownership.StillCurrent = func() bool {
			return b.networkCounter.NetworkResetGeneration() == probeGeneration
		}
	}
	return ownership
}

// measurementKey names the question this probe asks, so P02B can share one measurement between
// equivalent health probes.
//
// The network epoch is the one fact a group cannot invent and must not omit: an answer measured
// on the previous network is still fresh by its timestamps but answers a different question, so
// a key without the epoch would let a probe after a network change be served by one from before
// it. A context without a network manager yields epoch zero, which simply means no sharing
// across generations rather than a wrong share.
//
// # The dimensions left empty, and why each is empty rather than guessed
//
//   - Instance: the registry is the scope. The share lives on the Box's context, so two Boxes
//     cannot reach one registry; a name here would only make two equivalent callers spell the key
//     differently and lose the share.
//   - Path: the outbound passed to Measure IS the leaf, and its tag names the route it dials. A
//     nested group member is expanded to its leaves before it gets here (see test()).
//   - Method / TLSFingerprint: this measurement implementation performs HEAD with one fixed TLS
//     policy; MeasureOptions has no knob for either. They stay empty so there is one spelling.
//
// Any change that lets a caller vary one of those must fill the field in the same change: an empty
// dimension would then let two different questions share one answer. TestTheProductionMeasurementKey
// NamesEveryFactThatCanVary pins what is actually built here.
func (b *urlTestBatch) measurementKey(ctx context.Context, link string, detour adapter.Outbound) *urltest.MeasurementKey {
	var epoch uint64
	var resetGeneration uint64
	if manager := service.FromContext[adapter.NetworkManager](ctx); manager != nil {
		// The fingerprint names WHICH network the question is about; the reset generation names the
		// boundary the probe belongs to. Both are needed: a device that goes A -> B -> A has the
		// same fingerprint again, while a probe from the first A measured a path the second A does
		// not have. A manager that cannot report the counter keeps the previous behaviour (zero,
		// meaning "no boundary knowledge"), which is the documented degradation.
		epoch = manager.NetworkEnvironment()
		if counter, isCounter := manager.(adapter.NetworkResetCounter); isCounter {
			resetGeneration = counter.NetworkResetGeneration()
		}
	}
	// The evidence-drop epochs at REQUEST time are part of the question. A round that begins after
	// a drop asks a different question than the probe it would otherwise join, so it starts its
	// own probe instead of attaching to invalidated work.
	tagEpoch, scopeEpoch := b.history.EvidenceStamp(detour.Tag(), b.scope)
	return &urltest.MeasurementKey{
		NetworkEpoch:           epoch,
		NetworkResetGeneration: resetGeneration,
		Node:                   detour.Tag(),
		NodeIdentity:           nodeIdentity(detour),
		Target:                 link,
		ExpectedStatus:         b.expected.Key(),
		Purpose:                b.measurementPurpose(),
		EvidenceEpochTag:       tagEpoch,
		EvidenceEpochScope:     scopeEpoch,
	}
}

// evidenceTime prefers the PROBE's own start for evidence ordering. attemptStart is only a
// fallback for a measurement that failed before a probe existed (and for callers that predate the
// provenance field).
func evidenceTime(attemptStart, probeStarted time.Time) time.Time {
	if probeStarted.IsZero() {
		return attemptStart
	}
	return probeStarted
}

// measurementPurpose maps the round's evidence layer onto the measurement question.
//
// The two are the same mapping seen from opposite ends: a health round may move selection, so its
// measurement is health evidence and may be shared with another health round; a display round
// writes the display layer, so its measurement must never be shared AS health evidence - the share
// only merges PurposeHealth, and a display probe that claimed to be health could answer a real
// health round (or be answered by one) without the group ever measuring for itself.
func (b *urlTestBatch) measurementPurpose() urltest.MeasurementPurpose {
	if b.mode == TestHistoryHealth {
		return urltest.PurposeHealth
	}
	return urltest.PurposeDisplay
}

// nodeIdentity names the live object, not just its tag.
//
// Outbounds are pointers in every implementation this tree registers, and the pointer is the only
// per-object identity the outbound contract exposes: a provider publication replaces the object,
// so a probe in flight against the retired one cannot answer for the new one. The tag is kept in
// the key beside it for logs, but it is not the identity.
func nodeIdentity(outbound adapter.Outbound) string {
	if outbound == nil {
		return ""
	}
	return fmt.Sprintf("%p", outbound)
}

func (b *urlTestBatch) test(outbounds []adapter.Outbound, link string, interval time.Duration, force bool) {
	for _, detour := range outbounds {
		tag := detour.Tag()
		if b.checked[tag] {
			continue
		}
		switch nested := detour.(type) {
		case adapter.OutboundGroup:
			// EVERY group member - including a nested URLTest group - is expanded down to its
			// leaves and measured against THIS batch's target.
			//
			// A nested *URLTest used to be special-cased: it ran the child's own urlTest(), which
			// measured the child's leaves against the CHILD's configured URL and rewrote the
			// child's selection. The parent then folded those delays into its own ranking.
			//
			// Two things were wrong with that. The parent ranked a member by a delay measured
			// against a target the parent never chose, so a member could win or lose on an
			// unrelated measurement. And a parent's health check silently re-ran and mutated the
			// child's policy, which is the child's own business.
			//
			// Recursing to leaves means the parent evaluates "what delay would this member give
			// me, on my target, right now", which is what it actually needs to rank. The child's
			// own selection is untouched; the child's own health check remains the only thing that
			// changes it.
			b.checked[tag] = true
			b.groups = append(b.groups, nested)
			b.test(common.FilterNotNil(common.Map(nested.All(), func(it string) adapter.Outbound {
				member, _ := b.outbound.Outbound(it)
				return member
			})), link, interval, force)
		default:
			// The freshness check is scoped: a result against a DIFFERENT target says nothing
			// about this one, so it must not cause this test to be skipped.
			history := b.history.LoadURLTestHistoryFor(tag, b.scope)
			if !force && history != nil && time.Since(history.Time) < interval {
				continue
			}
			b.checked[tag] = true
			b.batch.Go(tag, func() (any, error) {
				// The ATTEMPT's start, not its completion.
				//
				// Evidence is ordered by when the probe began: a probe that started before a
				// network change describes the path that existed then, so it must not be stored as
				// if it were a fresh look at the new one, and it must not replace a result that a
				// later attempt already stored.
				//
				// The stamp comes from nextAttemptStart rather than time.Now directly: the store
				// accepts equal timestamps (a coarse clock must not lose a measurement), so two
				// attempts that began in the same clock tick would otherwise be unordered and the
				// older, slower one could be the last writer. See attempt_start.go.
				attemptStart := nextAttemptStart()
				// NO per-member timeout here.
				//
				// Measure establishes the active probe budget itself, starting from the moment it
				// holds a coordinator slot. Wrapping it again here would charge the queue wait
				// against that budget from the outside, so the inner ordering fix would not help:
				// a member could still time out while waiting for a slot and be reported as
				// unhealthy without ever being dialled.
				//
				// b.ctx still governs the round as a whole, so the batch remains cancellable.
				//
				// Called directly. batch.Go is already running this on its own goroutine, and
				// Measure honours the context, so the previous goroutine-plus-channel-plus-select
				// added nothing but a second race: when the context expired while the result was
				// also ready, Go picked between the two cases at random and the measurement's
				// outcome became non-deterministic.
				measurement, testErr := b.measure(b.ctx, link, detour)
				if testErr != nil {
					if b.ctx.Err() != nil {
						return nil, nil
					}
					if adapter.IsResourceSuspended(testErr) {
						// The endpoint is idle and this is background work, so it refused to wake.
						//
						// This is NOT evidence about the node: nothing was dialled, and the node may
						// be perfectly healthy. Removing its health history would make the group
						// treat a deliberately idle member as unreachable and could move the
						// selection because a timer fired. Leave the evidence alone - "not measured"
						// is not "unhealthy" - and let real traffic wake the endpoint when there is
						// demand for it.
						b.logger.Debug("outbound ", tag, " skipped: resource is idle and this probe may not wake it")
						return nil, nil
					}
					if adapter.IsOwnTeardown(testErr) || adapter.IsCallerCancellation(testErr) {
						// The measurement never happened, and not because of the node.
						//
						// net.ErrClosed is THIS process ending the operation: a network transition
						// closes the members' transports, so every probe in flight across the
						// transition fails with it, and a group close or a manual reset does the
						// same. context.Canceled is the caller going away. Neither is a fact about
						// the member, and both used to fall through to the branch below and DELETE
						// its health evidence - so a device that changed networks could move the
						// selection. That is the same mistake as waking an idle tunnel to measure
						// it: a fact about the core read as a fact about the node.
						//
						// The two are kept as separate facts rather than merged into one
						// "not measured" predicate because the failover retry has to be able to
						// tell them apart later; see adapter.IsCallerCancellation.
						b.logger.Debug("outbound ", tag, " not measured: ", testErr)
						return nil, nil
					}
					if facts, hasFacts := adapter.FailureFactsOf(testErr); hasFacts {
						switch facts.Origin {
						case adapter.FailureOriginTarget, adapter.FailureOriginLocal:
							// A target that refused the probe URL, or a local
							// policy/configuration failure, is not evidence that this member's
							// proxy path is down. Remove nothing: the next probe or the next
							// real demand can still use the last valid measurement.
							b.logger.Debug("outbound ", tag, " measurement target/local failure, health left alone: ", testErr)
							return nil, nil
						}
					}
					b.logger.Debug("outbound ", tag, " unavailable: ", testErr)
					if b.mode == TestHistoryHealth && b.history != nil {
						// Only this target's health result is removed. The display entry is left
						// alone: a failed check against this group's target does not invalidate a
						// previous success against another one.
						//
						// The removal is scoped to THIS attempt: if a result that started after it
						// is already stored, that result is newer evidence and survives. It is also
						// scoped to the attempt's OWNERSHIP: a failure measured on the previous
						// network, or before an evidence drop, has no right to remove the current
						// state's evidence.
						if b.history.DeleteHealthHistoryOlderThanOwned(tag, b.scope,
							evidenceTime(attemptStart, measurement.ProbeStarted), b.evidenceOwnership(measurement)) {
							b.publish(tag, roundPublishedFailure, 0)
						}
					}
				} else {
					b.logger.Debug("outbound ", tag, " available: ", measurement.Delay, "ms")
					// The scope comes from the measurement itself, so the key cannot disagree
					// with what was actually requested.
					//
					// The stored time is the PROBE's start, not this caller's attempt start: a
					// caller that joined an in-flight probe is receiving an observation made from
					// that probe's moment, and the drop stamps travel with the probe too. Storing
					// this round's own stamps is exactly how a post-drop joiner used to resurrect
					// evidence measured before the drop.
					health := &adapter.URLTestHistory{
						Time:  evidenceTime(attemptStart, measurement.ProbeStarted),
						Delay: measurement.Delay,
					}
					// ONE decision: the evidence write authorizes the result. A result the storage
					// refused (a drop, or a network reset that landed before the write) is not
					// reported as this round's measurement, because every consumer of the map -
					// the Clash API, a nested group, the failover policy - reads it as a success.
					published := true
					ownership := b.evidenceOwnership(measurement)
					if b.history != nil {
						if b.mode == TestHistoryHealth {
							published = b.history.StoreHealthHistoryOwned(tag, measurement.Scope, health, ownership)
						} else {
							published = b.history.StoreDisplayHistoryOwned(tag, measurement.Scope, health, ownership)
						}
					}
					if published {
						b.publish(tag, roundPublishedSuccess, measurement.Delay)
						// The provenance travels with the result: convergence re-checks it, and the
						// selection commit requires it, because a drop can land after this write.
						b.recordEvidence(tag, roundEvidence{ownership: ownership, nodeIdentity: nodeIdentity(detour)})
						b.logger.Debug("outbound ", tag, " result published: ", measurement.Delay, "ms")
					} else {
						// Not a failure: the probe succeeded, but the state it measured is not the
						// state this round reports. Saying so is what keeps "not evidence" from
						// being mistaken for "unhealthy".
						b.logger.Debug("outbound ", tag, " result discarded: it no longer owns this target's evidence")
					}
				}
				return nil, nil
			})
		}
	}
}

// clearSelectionFor drops the failed outbound from the live selection WITHOUT touching its health
// evidence, and requests a bounded health recheck.
//
// # Why a traffic failure is not evidence about the node
//
// This used to delete the node's measurement, on the reasoning that a real connection failing says
// something about the node regardless of what a test showed. It does not. DialContext reports
// whatever went wrong on the path, and the overwhelming majority of causes have nothing to do with
// the proxy: the target refused the connection, the target's port is closed, the remote reset, the
// destination does not exist. A node that carried the connection perfectly produces every one of
// them.
//
// Deleting a correct measurement because a website was down is not merely wasteful - the recheck it
// triggers re-selects from the reduced set, so the group can move to a WORSE node on the strength
// of a third party refusing a connection.
//
// So a traffic failure clears the SELECTION (the caller needs a working path now) and requests a
// recheck. The recheck is what decides whether the node is actually unhealthy, and only its failure
// removes the measurement.
//
// # Why the selection is still cleared
//
// The node just failed to carry traffic, so keeping it selected would make the next connection fail
// the same way. Clearing it lets the immediate re-selection pick another node; if the recheck
// confirms the node is healthy, the measurement is still there and it can be chosen again.
//
// # Why the comparison
//
// The current selection is re-read under the lock and compared: a concurrent update may already have
// replaced this outbound, and clearing the newer choice would undo that work.
//
// # Why only TCP clears the measurement's meaning
//
// A delay measurement dials over TCP and speaks HTTP over it, so the stored value describes the TCP
// path and remains true after a UDP failure. TCP and UDP selection read the same entry, so a UDP
// failure must not move the TCP selection as a side effect.
func (g *URLTestGroup) clearSelectionFor(network string, failed adapter.Outbound) {
	if failed == nil {
		return
	}

	g.updateAccess.Lock()
	previous := g.selected.Load()
	next := &selectedState{}
	if previous != nil {
		next.tcp = previous.tcp
		next.udp = previous.udp
	}

	cleared := false
	switch N.NetworkName(network) {
	case N.NetworkTCP:
		if next.tcp == failed {
			next.tcp = nil
			cleared = true
		}
	case N.NetworkUDP:
		if next.udp == failed {
			next.udp = nil
			cleared = true
		}
	}
	if cleared {
		g.selected.Store(next)
	}
	g.updateAccess.Unlock()

	if cleared {
		// Request a health recheck rather than deleting the measurement. The recheck is
		// single-flight, so a burst of failing connections produces one round of probes rather
		// than one per failure.
		g.requestHealthRecheck()
	}
}

// requestHealthRecheck asks for one background health check, collapsing concurrent requests.
//
// A burst of failing connections must not start a burst of probes: each would dial every member,
// which on a mobile device is exactly the traffic storm a health check exists to avoid. The group
// already has a single-flight guard for its checks, so this defers to it and does not add a second
// mechanism.
func (g *URLTestGroup) requestHealthRecheck() {
	// A closed group must not start work. A check against a torn-down group would measure nothing
	// and write history for a group that no longer exists.
	g.access.Lock()
	closed := g.closed
	g.access.Unlock()
	if closed {
		return
	}

	g.recheckAccess.Lock()
	// Re-check terminal INSIDE the lock that records the debt.
	//
	// The check above runs before this lock, so a Close can land in between. Without this second
	// check the request would record debt on a group that can never serve it, and - because the
	// group is terminal - that debt is exactly what the exit path has to retire, so nothing is
	// gained by admitting it. Refusing here keeps "requested > served" meaning "work that is still
	// possible".
	if g.ctx.Err() != nil {
		g.recheckAccess.Unlock()
		return
	}
	// Record the debt. This is the whole point of the redesign: whether or not a round is running,
	// the request is remembered, because a forced probe that never happens leaves the node that
	// just failed to carry traffic eligible for selection on the strength of the measurement the
	// failure already contradicted.
	g.recheckRequested++
	if g.recheckWorker {
		// A worker already exists and will observe the new sequence. Starting another would make a
		// burst of failing connections a burst of goroutines.
		g.recheckAccess.Unlock()
		return
	}
	g.recheckWorker = true
	g.recheckAccess.Unlock()

	go g.drainHealthRechecks()
}

// drainHealthRechecks runs forced rounds until no request is owed.
//
// # The handoff this exists to guarantee
//
// A request arriving while a round is running is recorded, not dropped:
//
//	request   -> recheckRequested++
//	worker    -> records the sequence it is serving, runs a forced round
//	             (a request arriving during it raises recheckRequested further)
//	worker    -> marks that sequence served, and if more was requested runs ONE more round
//	worker    -> only then gives up the worker slot
//
// Ordering the last two steps under the lock is what makes it race-free: a request either raises the
// sequence in time for the final comparison, or finds no worker and starts one.
//
// Many failures therefore coalesce into at most one extra round, and none are lost. The same
// sequence is what lets the panic path tell apart debt this round already owned - which must not be
// retried - from debt that arrived while it ran, which must not be dropped.
func (g *URLTestGroup) drainHealthRechecks() {
	// The worker gives up its slot on EVERY exit, including a panic, and decides there whether a
	// replacement is owed.
	//
	// # What a panic must and must not do
	//
	// MUST NOT retry the round that panicked. A reproducible panic would then become an unbounded
	// retry loop that pins a CPU and floods the log.
	//
	// MUST NOT drop debt created WHILE that round ran. A genuine traffic failure arriving during
	// the round is a real request nobody else will serve: the previous design released `checking`
	// and the worker while leaving the queued flag set, which is debt with no worker to serve it.
	//
	// `retired` is the sequence this worker has accounted for, either by completing a round or by
	// ABANDONING the one that panicked.
	//
	// It starts at the sequence the worker was created to serve, NOT at recheckServed. The
	// difference is the whole point: a round that panics abandons the debt it was serving, so that
	// debt must be marked retired rather than left outstanding - leaving it outstanding is what
	// would make the replacement worker retry the panicking round forever.
	//
	// Anything ABOVE the round's own serving mark arrived while it ran, so it is genuinely new debt
	// and the tail starts a replacement worker to serve exactly that.
	g.recheckAccess.Lock()
	retired := g.recheckRequested
	g.recheckAccess.Unlock()

	// servingMark is the sequence the CURRENT round is serving. A panic retires exactly this, so the
	// panicking round's own debt is abandoned while anything newer survives.
	var servingMark uint64

	defer func() {
		if recovered := recover(); recovered != nil {
			if g.logger != nil {
				g.logger.Error("health recheck panicked: ", recovered)
			}
		}
		g.checking.Store(false)

		g.recheckAccess.Lock()
		g.recheckWorker = false
		if servingMark > g.recheckServed {
			// The round was abandoned rather than completed, so its debt is retired rather than
			// served. Writing it back to recheckServed is what keeps the counter pair meaningful:
			// "requested > served" must mean "somebody still owes a round", and an abandoned round
			// is nobody's debt any more. Leaving it outstanding would also make a replacement
			// worker replay the same panicking round without bound.
			g.recheckServed = servingMark
		}

		// A TERMINAL group owes nothing.
		//
		// Its remaining debt can never be served, so it must be retired rather than carried. The
		// comparison below is otherwise permanently true on a canceled group - every exiting worker
		// would spawn a replacement, which would start, find the context canceled, exit, and spawn
		// another. Close did not stop the group's health work; it converted it into goroutine churn
		// that outlives the group.
		if g.ctx.Err() != nil {
			g.recheckServed = g.recheckRequested
		}

		retired = g.recheckServed
		replacement := g.ctx.Err() == nil && g.recheckRequested > retired
		if replacement {
			// Claim the worker slot while still holding the lock, so a request arriving now either
			// observes a worker or starts one itself. There is no window in which it waits for a
			// worker that has already decided to exit.
			g.recheckWorker = true
		}
		g.recheckAccess.Unlock()

		if replacement {
			go g.drainHealthRechecks()
		}
	}()

	for {
		g.recheckAccess.Lock()
		// This round serves every request made up to now. Many failures therefore still coalesce
		// into ONE round: the sequence is not one-round-per-request.
		serving := g.recheckRequested
		g.recheckAccess.Unlock()

		if g.ctx.Err() != nil {
			break
		}

		// Run the round DIRECTLY, through the same path a periodic check uses.
		//
		// The worker must not call CheckOutbounds here. CheckOutbounds routes through the
		// re-queuing guard, and this worker is itself the drain of that queue: if the guard were
		// still held - by the round this worker was queued behind, whose deferred release has not
		// run yet - the call would re-queue the very request being served, and the loop would
		// re-trigger itself without bound. Running the round directly removes that coupling.
		if !g.checking.CompareAndSwap(false, true) {
			// Another round is still finishing. Wait for it rather than spin, then serve the debt.
			//
			// This is a bounded wait on a mutex-guarded handoff, not a traffic path.
			time.Sleep(time.Millisecond)
			continue
		}

		g.recheckRuns.Add(1)
		// Record the mark BEFORE running, so the panic path can retire exactly this round's debt
		// rather than re-deriving it from state the panic may have left inconsistent.
		g.recheckAccess.Lock()
		servingMark = serving
		g.recheckAccess.Unlock()

		g.runForcedRound()
		g.checking.Store(false)

		// The round completed, so everything up to `serving` is now accounted for.
		g.recheckAccess.Lock()
		if serving > g.recheckServed {
			g.recheckServed = serving
		}
		retired = g.recheckServed
		more := g.recheckRequested > g.recheckServed && g.ctx.Err() == nil
		if !more {
			g.recheckWorker = false
		}
		g.recheckAccess.Unlock()
		if !more {
			return
		}
	}
}

// runForcedRound performs one forced health measurement and re-evaluates the selection.
//
// force = true. A recheck must actually PROBE: with force = false the batch skips any member whose
// measurement is younger than the interval, which is exactly the state a traffic failure leaves
// behind - nothing would be re-measured, and the selection that follows would re-read the very
// measurement the failure just contradicted.
//
// It is a separate function so the worker's ownership of the `checking` guard is visible at the
// call site.
func (g *URLTestGroup) runForcedRound() {
	if g.forcedRoundOverride != nil {
		g.forcedRoundOverride()
		return
	}
	// The SAME round implementation the periodic path uses.
	//
	// Maintaining two copies is how the two paths drift: a fix applied to one silently does not
	// reach the other. Sharing the call also means the round-level panic boundary covers the forced
	// path without a second recover.
	_, _ = g.runHealthRound(g.ctx, true)
}

// clearSelected is invalidateSelected without the re-selection, so the cleared generation can be
// observed on its own. It exists because the two effects are separate concerns: which fields the
// invalidation clears is a rule, and which node selection then settles on is a policy.
func (g *URLTestGroup) clearSelected(network string, failed adapter.Outbound) {
	if failed == nil {
		return
	}
	g.updateAccess.Lock()
	defer g.updateAccess.Unlock()

	previous := g.selected.Load()
	if previous == nil {
		return
	}
	next := &selectedState{tcp: previous.tcp, udp: previous.udp}
	switch N.NetworkName(network) {
	case N.NetworkTCP:
		if next.tcp == failed {
			next.tcp = nil
		}
	case N.NetworkUDP:
		if next.udp == failed {
			next.udp = nil
		}
	}
	g.selected.Store(next)
}

func (g *URLTestGroup) performUpdateCheck() {
	// The same serialisation the failover commit takes: both paths decide a selection from evidence,
	// and this one reads the evidence (which takes the history locks) while it builds the state.
	g.selectionCommit.Lock()
	defer g.selectionCommit.Unlock()
	g.updateAccess.Lock()
	defer g.updateAccess.Unlock()
	var (
		updated  bool
		selected bool
	)

	// The previous generation is read once and the next is built from it, so TCP and UDP are
	// published together. Assigning each field separately would let a reader observe one updated
	// network and one stale one.
	previous := g.selected.Load()
	next := &selectedState{}
	if previous != nil {
		next.tcp = previous.tcp
		next.udp = previous.udp
	}

	if outbound, exists := g.Select(N.NetworkTCP); outbound != nil && (next.tcp == nil || (exists && outbound != next.tcp)) {
		if next.tcp != nil {
			updated = true
		}
		next.tcp = outbound
		selected = true
	}
	if outbound, exists := g.Select(N.NetworkUDP); outbound != nil && (next.udp == nil || (exists && outbound != next.udp)) {
		if next.udp != nil {
			updated = true
		}
		next.udp = outbound
		selected = true
	}

	g.selected.Store(next)
	if updated {
		g.interruptGroup.Interrupt(g.interruptExternalConnections)
	}
	if selected {
		g.history.NotifyUpdated()
	}
}
