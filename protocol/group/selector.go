package group

import (
	"context"
	"io"
	"net"
	"net/netip"
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
	"github.com/sagernet/sing-box/common/urltest"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
)

func RegisterSelector(registry *outbound.Registry) {
	outbound.Register[option.SelectorOutboundOptions](registry, C.TypeSelector, NewSelector)
}

var (
	_ adapter.OutboundGroup = (*Selector)(nil)
	_ adapter.Referrer      = (*Selector)(nil)
	// A selector may be fed by outbound providers, so an empty member list before the provider has
	// published is a WAIT rather than an empty declaration. See adapter.ProviderBackedGroup.
	_ adapter.ProviderBackedGroup = (*Selector)(nil)
	// A selector only chooses which member serves a flow: it does not own the dial and adds no
	// per-flow behaviour of its own, so a chain through it is transparent and it may carry the
	// fast_path group policy. See adapter.FastPathAuthorizedGroup.
	_ adapter.FastPathAuthorizedGroup = (*Selector)(nil)
	_ D.ParallelNetworkDialer         = (*Selector)(nil)
)

// FastPathTransparent implements adapter.FastPathAuthorizedGroup.
//
// A selector's entire effect on a flow is the choice of member. It holds no per-flow state, owns no
// dial, and performs no retry, so a chain through it reaches its leaf with nothing lost - which is
// exactly the claim the fast path needs before it may skip the group.
//
// Compare LoadBalance, which deliberately does not declare this: it can own the dial of the flow it
// selected, so bypassing it would discard the retry its configuration asked for.
func (s *Selector) FastPathTransparent() bool { return true }

type Selector struct {
	outbound.Adapter
	ctx                          context.Context
	outbound                     adapter.OutboundManager
	logger                       logger.ContextLogger
	tags                         []string
	defaultTag                   string
	outbounds                    map[string]adapter.Outbound
	selected                     common.TypedValue[adapter.Outbound]
	history                      *urltest.HistoryStorage
	interruptGroup               *interrupt.Group
	interruptExternalConnections bool
	// preference is the evidence Start gathered about the PREFERENCE it did not commit to, in one
	// immutable value so a reader never sees half of an update and never races Start. It is nil
	// until Start has looked.
	preference atomic.Pointer[selectionPreference]
	// selectionRevision counts the times this selector's selection CHANGED. A caller that reads it
	// before and after a selection can tell whether its confirmation describes the state it asked
	// for, and a confirmation carrying an older revision can be discarded instead of settling a
	// newer request.
	selectionRevision atomic.Uint64

	// stateAccess guards tags/outbounds/outboundsCache. The traffic path reads `selected` (an
	// atomic), not these maps, so a provider update never puts a lock in front of a dial.
	stateAccess sync.RWMutex
	// providerAccess serialises membership rebuilds: a rebuild reads every provider's published
	// list and then publishes one map, and two rebuilds interleaving would let the older map win.
	providerAccess sync.Mutex

	provider          adapter.ProviderManager
	providers         map[string]adapter.Provider
	outboundsCache    map[string][]adapter.Outbound
	providerTags      []string
	exclude           *regexp.Regexp
	include           *regexp.Regexp
	useAllProviders   bool
	providerScheduler providerUpdateCheckScheduler
}

// selectionPreference is what the cachefile said, recorded by Start.
//
// It exists so a later reader does not have to re-read the cachefile and does not have to race
// Start to see it, and it keeps the two outcomes apart: a stored member that this selector really
// declares, and a stored member that it does not. The second is a configuration fact an operator
// needs - "your stored selection was refused" is not "you have no stored selection".
type selectionPreference struct {
	persisted string
	rejected  string
}

// SelectionState names which of the four things is known about a selector's selection.
//
// # Why these four are separate and must not be merged
//
// They are four different claims, and only the last is about traffic:
//
//	ConfiguredDefault       the initial preference in the configuration
//	PersistedSelection      the member the cachefile holds, once validated as legal
//	LiveCommittedSelection  what the live slot actually holds after Start
//	Unknown                 not enough evidence yet
//
// A report that collapses them describes a path the traffic may not take. `References` therefore
// publishes only the third, and this type is how a reader sees the other two WITHOUT being told
// they are live.
type SelectionState uint8

const (
	// SelectionUnknown means Start has not run and the configuration names no preference either.
	// The selector would fall back to the first declared member, and that is a fallback rather than
	// a preference, so it is not reported as one.
	SelectionUnknown SelectionState = iota
	// SelectionConfigured means the configuration names a default member. It is a PREFERENCE that
	// Start would install, not a commitment.
	SelectionConfigured
	// SelectionPersisted means the cachefile names a member this selector really declares. It is
	// the preference Start will restore, and it is likewise not a commitment.
	SelectionPersisted
	// SelectionCommitted means Start or SelectOutbound has put a member in the live slot. This is
	// the only state that describes the traffic.
	SelectionCommitted
)

func (s SelectionState) String() string {
	switch s {
	case SelectionConfigured:
		return "configured"
	case SelectionPersisted:
		return "persisted"
	case SelectionCommitted:
		return "committed"
	default:
		return "unknown"
	}
}

// SelectionReport is one read of a selector's selection state, with the evidence for every state it
// is not in.
//
// It is a VALUE: every field is a copy, so a caller cannot mutate the selector through it.
type SelectionReport struct {
	// State is the one state this report is in.
	State SelectionState
	// Tag is the member the state names: the committed member, or the preferred one, or "" when
	// the state names none.
	Tag string
	// ConfiguredDefault is the configuration's default, or "" when none was given.
	ConfiguredDefault string
	// Persisted is the cachefile's member, reported only when it is a legal member of this
	// selector. An illegal one is reported in PersistedRejected instead.
	Persisted string
	// PersistedRejected is the cachefile's member when it is NOT a member of this selector, so a
	// reader can tell "no stored preference" from "a stored preference that was refused".
	PersistedRejected string
	// Committed is the member the live slot holds, or "" when nothing is committed. It is non-empty
	// exactly when State is SelectionCommitted, and it is the only field that describes traffic.
	Committed string
}

// CommittedMember reports the member the live slot holds, and whether there is one.
func (r SelectionReport) CommittedMember() (string, bool) {
	return r.Committed, r.Committed != ""
}

func NewSelector(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.SelectorOutboundOptions) (adapter.Outbound, error) {
	outbound := &Selector{
		Adapter:                      outbound.NewAdapter(C.TypeSelector, tag, nil, options.Outbounds),
		ctx:                          ctx,
		outbound:                     service.FromContext[adapter.OutboundManager](ctx),
		logger:                       logger,
		tags:                         options.Outbounds,
		defaultTag:                   options.Default,
		outbounds:                    make(map[string]adapter.Outbound),
		outboundsCache:               make(map[string][]adapter.Outbound),
		history:                      service.PtrFromContext[urltest.HistoryStorage](ctx),
		interruptGroup:               interrupt.NewGroup(),
		interruptExternalConnections: options.InterruptExistConnections,

		provider:        service.FromContext[adapter.ProviderManager](ctx),
		providers:       make(map[string]adapter.Provider),
		providerTags:    options.Providers,
		exclude:         (*regexp.Regexp)(options.Exclude),
		include:         (*regexp.Regexp)(options.Include),
		useAllProviders: options.UseAllProviders,
	}
	// A member may be declared more than once. That is one edge, not a configuration error: route
	// resolution and loadbalance both define what it means (one path, a proportional share), and the
	// route package's nested-chain test pins it. Rejecting it here made an existing configuration
	// unloadable, so the duplicate stays legal and the member map below collapses it to one object.
	if len(outbound.tags) == 0 && len(outbound.providerTags) == 0 && !outbound.useAllProviders {
		return nil, E.New("missing outbound and provider tags")
	}
	return outbound, nil
}

func (s *Selector) Network() []string {
	selected := s.selected.Load()
	if selected == nil {
		return []string{N.NetworkTCP, N.NetworkUDP}
	}
	return selected.Network()
}

func (s *Selector) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	if stage != adapter.StartStateStart {
		return nil
	}
	s.providerAccess.Lock()
	defer s.providerAccess.Unlock()
	if err := s.resolveProviders(); err != nil {
		return err
	}
	tags, outbounds, cache, err := collectProviderOutbounds("", s.Dependencies(), s.outbound, s.providers, s.providerTags, s.outboundsCache, s.exclude, s.include)
	if err != nil {
		return err
	}
	outboundByTag := make(map[string]adapter.Outbound, len(outbounds))
	for _, detour := range outbounds {
		outboundByTag[detour.Tag()] = detour
	}
	selected, err := s.selectMember(outboundByTag, tags, false)
	if err != nil {
		return err
	}
	// The initial membership is published through the SAME graph validation as a provider update.
	//
	// # Why Start is not exempt
	//
	// An INLINE provider publishes its nodes during box.New, before the outbound manager starts, so
	// a selector that names it has a non-empty `All()` at Start. MEASURED: an inline node with
	// `detour: <this selector>` was accepted - the start-time dry run ran BEFORE this method, when
	// the member list was still empty, and nothing validated the graph the group was about to
	// publish. Handing the list to the manager closes that window.
	err = publishProviderMembership(s.outbound, s.Tag(), tags, func() {
		s.stateAccess.Lock()
		s.tags, s.outbounds, s.outboundsCache = tags, outboundByTag, cache
		s.stateAccess.Unlock()
	})
	if err != nil {
		return err
	}
	// The preference evidence is recorded against the FULL member set, provider nodes included:
	// a stored member that names a provider node is legal the moment the provider publishes it.
	if preference := s.preferenceRecord(tags); preference != nil {
		s.preference.Store(preference)
	}
	if selected != nil {
		s.selected.Store(selected)
	}
	for _, providerTag := range s.providerTags {
		provider := s.providers[providerTag]
		element := provider.RegisterCallback(s.onProviderUpdated)
		scope.Add(func() error {
			provider.UnregisterCallback(element)
			return nil
		})
	}
	return nil
}

// resolveProviders binds the provider tags this selector names. UseAllProviders snapshots the
// configured providers at start, so a provider added later needs a restart to be picked up.
func (s *Selector) resolveProviders() error {
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
			// use_all_providers with no provider and no static member is a configuration that can
			// never have a member. Starting it would leave a permanently empty group whose dial error
			// says it is "waiting for providers" that do not exist.
			return E.New(s.Tag(), ": use_all_providers is set but no outbound providers are configured")
		}
		// The resolved list is what ProviderMembership publishes, and that getter must be safe to
		// call from a diagnostic path while the Box is running: it takes stateAccess, so the write
		// is published under the same lock.
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

// preferenceRecord is the cachefile evidence Start reports about a member it did NOT have to use.
func (s *Selector) preferenceRecord(tags []string) *selectionPreference {
	if s.Tag() == "" {
		return nil
	}
	cacheFile := service.FromContext[adapter.CacheFile](s.ctx)
	if cacheFile == nil {
		return nil
	}
	stored := cacheFile.LoadSelected(s.Tag())
	if stored == "" {
		return nil
	}
	if slices.Contains(tags, stored) {
		return &selectionPreference{persisted: stored}
	}
	return &selectionPreference{rejected: stored}
}

// selectMember applies the documented precedence: the stored selection, the previously committed
// member (during a provider update), the configured default, then the first declared member. An
// empty group selects nothing; the dial path reports that instead of inheriting a node the user did
// not configure.
func (s *Selector) selectMember(outbounds map[string]adapter.Outbound, tags []string, preserve bool) (adapter.Outbound, error) {
	if s.Tag() != "" {
		cacheFile := service.FromContext[adapter.CacheFile](s.ctx)
		if cacheFile != nil {
			if stored := cacheFile.LoadSelected(s.Tag()); stored != "" {
				if detour, loaded := outbounds[stored]; loaded {
					return detour, nil
				}
			}
		}
	}
	if preserve {
		if previous := s.selected.Load(); previous != nil {
			if current, loaded := outbounds[previous.Tag()]; loaded {
				return current, nil
			}
		}
	}
	if s.defaultTag != "" {
		detour, loaded := outbounds[s.defaultTag]
		if !loaded {
			return nil, E.New("default outbound not found: ", s.defaultTag)
		}
		return detour, nil
	}
	if len(tags) == 0 {
		return nil, nil
	}
	return outbounds[tags[0]], nil
}

// onProviderUpdated rebuilds this selector's membership after one provider published a new node set.
//
// The committed member is preserved when its tag still exists (that is what a user expects from a
// subscription refresh); when it does not, the same precedence Start uses picks a replacement. The
// interrupt is fired only when the published member actually changed.
func (s *Selector) onProviderUpdated(tag string) error {
	s.providerAccess.Lock()
	tags, outbounds, cache, err := collectProviderOutbounds(tag, s.Dependencies(), s.outbound, s.providers, s.providerTags, s.outboundsCache, s.exclude, s.include)
	if err != nil {
		s.providerAccess.Unlock()
		return E.Cause(err, s.Tag())
	}
	outboundByTag := make(map[string]adapter.Outbound, len(outbounds))
	for _, detour := range outbounds {
		outboundByTag[detour.Tag()] = detour
	}
	selected, err := s.selectMember(outboundByTag, tags, true)
	if err != nil {
		s.providerAccess.Unlock()
		return err
	}
	// The prospective membership is validated before it is published: a provider update that would
	// close a recursion is refused here, and the previous members AND selection stay in place.
	var changed bool
	publishErr := publishProviderMembership(s.outbound, s.Tag(), tags, func() {
		s.stateAccess.Lock()
		previous := s.selected.Swap(selected)
		if previous != selected {
			// A provider republication that moves the selection is a state change like any other:
			// the revision must advance, or a caller that reads it cannot tell that the member moved
			// underneath it.
			changed = true
			s.selectionRevision.Add(1)
		}
		s.tags, s.outbounds, s.outboundsCache = tags, outboundByTag, cache
		s.stateAccess.Unlock()
	})
	if publishErr != nil {
		s.providerAccess.Unlock()
		return publishErr
	}
	s.providerAccess.Unlock()
	if changed {
		s.interruptGroup.Interrupt(s.interruptExternalConnections)
		if s.history != nil {
			s.history.NotifyUpdated()
		}
	}
	return nil
}

func (s *Selector) All() []string {
	s.stateAccess.RLock()
	defer s.stateAccess.RUnlock()
	return slices.Clone(s.tags)
}

// Selected returns the member the selector committed to, or nil.
//
// It is a snapshot for immediate inspection: a provider refresh may replace the tag and eventually
// close this object after the publication barrier. Dial through DialContext/ListenPacket (which
// lease the member for the hand-off) or hold an explicit lease; do not keep the object and dial it
// after an update. See adapter.OutboundGroup.Selected for the full contract.
func (s *Selector) Selected(network string) adapter.Outbound {
	return s.selected.Load()
}

func (s *Selector) AttachConnection(closer io.Closer) func() {
	return s.interruptGroup.Add(closer, true)
}

// References publishes the member this selector has COMMITTED to, and nothing before it has one.
//
// # Why an uncommitted selector publishes nothing (#P2)
//
// This used to return the FIRST declared member while nothing was selected:
//
//	if selected == nil {
//		return s.tags[:1]
//	}
//
// and that is a claim the selector cannot make. Before Start the live slot is empty, so no member is
// live - and the first declared member is not even the preference in the ordinary case: a selector
// with `default: B` prefers B, and one whose cachefile holds B will restore B, and both were
// reported as A. Two consumers turn a one-element answer into a statement about LIVE state, because
// that is the contract `adapter.Referrer` documents and the only shape this return type has:
//
//   - common/physicalpath/leaves.go's `selectedTag` requires exactly one reference and uses it to
//     set `PathNode.IsCurrent`, "the path the root resolves to RIGHT NOW".
//   - common/physicalpath/status.go's `controlNode` sets `Decision` from it and `Committed = true`.
//
// So the lie reached a report whose whole purpose is to distinguish the live path from another
// reachable member. Returning the default instead would be the same defect wearing a better name,
// because `controlNode` maps any single reference to `Committed = true`: a CONFIGURED preference
// would be displayed as a COMMITTED one.
//
// Nothing is the honest answer, and it is the answer the siblings already give:
// URLTest.References (protocol/group/urltest.go) returns nil until a generation is selected, and
// common/physicalpath's own `testGroup` fixture returns nil for an unselected group
// (common/physicalpath/physicalpath_test.go). A consumer that needs the PREFERENCE asks
// SelectionStatus, which names it and says it is not committed.
//
// # Why this is not a dependency-graph change
//
// Neither structural reader reads this method for an outbound. The start-order sort
// (adapter/outbound/manager.go's `lintOutbounds`, and its cycle lint) and the cross-kind cycle check
// (adapter/outbound/cross_kind_cycle.go's `edges`) both walk `Dependencies()`, which for a selector
// is the whole declared tag list (outbound.Adapter.Dependencies, built from options.Outbounds) - so
// the graph they validate is unchanged by this. The cross-kind walk reads `References()` only for a
// DNS TRANSPORT, which a selector is not.
//
// route/reference.go's idle walk does follow `References()` for a Referrer outbound, and it is the
// one caller that would notice: it is reached from ReferenceManager.update, whose first run is at
// adapter.StartStateStarted - after every outbound, started at StartStateStart - so it reads a
// committed selection and never this branch. Its walk also unions nothing else for a Referrer, which
// is why the branch must not be invented here rather than merely be wrong.
func (s *Selector) References() []string {
	selected := s.selected.Load()
	if selected == nil {
		return nil
	}
	return []string{selected.Tag()}
}

// ProviderMembership implements adapter.ProviderBackedGroup.
//
// It reports the provider feed as a fact about the GROUP, which is what the read-only walk needs to
// tell "waiting for a provider" apart from "declared empty". It reads configuration state only: no
// member is selected, no callback is registered, and the answer does not change once Start has
// resolved `use_all_providers`.
func (s *Selector) ProviderMembership() ([]string, bool) {
	s.stateAccess.RLock()
	defer s.stateAccess.RUnlock()
	return slices.Clone(s.providerTags), s.useAllProviders
}

// SelectionStatus reports which of the four states this selector is in, with the evidence for the
// states it is not in.
//
// # Read-only, and safe to call at any time
//
// It reads four things and mutates none: the committed member (an atomic), the preference evidence
// Start recorded (an atomic), the immutable declared tag list and default, and - only while nothing
// is committed - the cachefile, through the same read-only `LoadSelected` Start performs. It does
// NOT read the `outbounds` map, which Start fills and which therefore must not be touched from a
// diagnostics path that may run concurrently with Start; legality of a stored tag is tested against
// the DECLARED list instead. Those are the same set: Start builds `outbounds` from `tags` and fails
// outright if any tag is missing.
//
// Reading it consumes no preview, takes no selection, advances no cursor, and changes no state -
// the property `SnapshotStatus` is required to have, asserted in
// TestReadingStatusConsumesNoSelection.
func (s *Selector) SelectionStatus() SelectionReport {
	report := SelectionReport{ConfiguredDefault: s.defaultTag}
	committed := s.selected.Load()
	if preference := s.preference.Load(); preference != nil {
		// Start has looked, so its record is the evidence - including when the cachefile held
		// nothing, which Start records by leaving no member in the record.
		report.Persisted = preference.persisted
		report.PersistedRejected = preference.rejected
	} else if committed == nil {
		// Nothing is committed and Start has not looked, so the persisted preference - which is
		// real state that exists before Start - is read here.
		report.Persisted, report.PersistedRejected = s.readPersistedPreference()
	}
	if committed != nil {
		report.State = SelectionCommitted
		report.Tag = committed.Tag()
		report.Committed = committed.Tag()
		return report
	}
	switch {
	case report.Persisted != "":
		report.State = SelectionPersisted
		report.Tag = report.Persisted
	case s.defaultTag != "":
		report.State = SelectionConfigured
		report.Tag = s.defaultTag
	default:
		report.State = SelectionUnknown
	}
	return report
}

// readPersistedPreference reads the cachefile's member and reports whether this selector declares
// it.
//
// It is the one read SelectionStatus performs, and it is the same read-only `LoadSelected` Start
// performs. Legality is tested against the DECLARED tag list rather than against the `outbounds`
// map, because Start fills that map and a diagnostics path may run while it does; the two are the
// same set, since Start builds the map from the list and fails outright if a tag is missing.
func (s *Selector) readPersistedPreference() (persisted string, rejected string) {
	if s.Tag() == "" {
		return "", ""
	}
	cacheFile := service.FromContext[adapter.CacheFile](s.ctx)
	if cacheFile == nil {
		return "", ""
	}
	stored := cacheFile.LoadSelected(s.Tag())
	if stored == "" {
		return "", ""
	}
	s.stateAccess.RLock()
	declared := slices.Contains(s.tags, stored)
	s.stateAccess.RUnlock()
	if declared {
		return stored, ""
	}
	return "", stored
}

// Revision is the selector's selection generation: it advances every time the selected member
// changes, and is unchanged by a selection that selects what was already selected.
func (s *Selector) Revision() uint64 {
	if s == nil {
		return 0
	}
	return s.selectionRevision.Load()
}

// SelectionSnapshot is one consistent answer to "select this member".
//
// AppliedTag is the member the group holds AFTER the call, read under the same hold that applied
// it. PreviousRevision is the revision the decision was taken against (what a caller's
// expectedRevision is compared with), and Revision is the state the group is in afterwards. A
// caller that stored (AppliedTag, Revision) from this value has a pair that existed together.
type SelectionSnapshot struct {
	AppliedTag       string
	PreviousRevision uint64
	Revision         uint64
}

// SelectOutboundSnapshot applies a selection and reports the resulting state atomically.
//
// The four facts - the revision before, the selection, the member it produced, the revision after -
// used to be collected by the CALLER in four separate reads. Each read was safe on its own, but the
// combination spanned two commits: a second selection landing between them produced (member=b,
// revision=2) for an operation that only ever produced revision 1. They are now one transition under
// the selector's existing write ownership (providerAccess, the same lock a provider republication
// takes), so a caller can never observe a pair that did not exist.
func (s *Selector) SelectOutboundSnapshot(tag string) (SelectionSnapshot, bool) {
	if s == nil {
		return SelectionSnapshot{}, false
	}
	s.providerAccess.Lock()
	s.stateAccess.RLock()
	detour, loaded := s.outbounds[tag]
	s.stateAccess.RUnlock()
	if !loaded {
		s.providerAccess.Unlock()
		return SelectionSnapshot{}, false
	}
	before := s.selectionRevision.Load()
	changed := s.selected.Swap(detour) != detour
	if changed {
		s.selectionRevision.Add(1)
		if s.Tag() != "" {
			cacheFile := service.FromContext[adapter.CacheFile](s.ctx)
			if cacheFile != nil {
				err := cacheFile.StoreSelected(s.Tag(), tag)
				if err != nil {
					s.logger.Error("store selected: ", err)
				}
			}
		}
	}
	snapshot := SelectionSnapshot{
		PreviousRevision: before,
		Revision:         s.selectionRevision.Load(),
	}
	if selected := s.selected.Load(); selected != nil {
		snapshot.AppliedTag = selected.Tag()
	}
	s.providerAccess.Unlock()
	// The interrupt and the history notification stay OUTSIDE the lock, as before: they call into
	// other owners, and holding providerAccess across them would widen the critical section beyond
	// the state transition this method exists to make atomic.
	if changed {
		s.interruptGroup.Interrupt(s.interruptExternalConnections)
		if s.history != nil {
			s.history.NotifyUpdated()
		}
	}
	return snapshot, true
}

func (s *Selector) SelectOutbound(tag string) bool {
	_, ok := s.SelectOutboundSnapshot(tag)
	return ok
}

// currentMember returns the committed member, or an error naming the group when nothing has been
// committed yet. A provider-only selector starts empty when its provider has no cache, and that
// state must fail the dial with an explanation rather than a nil dereference.
//
// The message names the provider feed when there is one: "has no available member yet" alone reads
// like a broken group, while the real state may be "the subscription has not been downloaded yet".
// The two need different actions from the operator, and the group knows which it is.
func (s *Selector) currentMember() (adapter.Outbound, error) {
	member := s.selected.Load()
	if member == nil {
		return nil, s.emptyMembershipError()
	}
	return member, nil
}

// emptyMembershipError explains why the group has no member, distinguishing the three states an
// operator has to tell apart: nothing published yet, provider published but the group's filters
// excluded everything, and a group with no member source at all.
func (s *Selector) emptyMembershipError() error {
	providerTags, usesAllProviders := s.ProviderMembership()
	switch {
	case len(providerTags) > 0:
		published := s.publishedNodeCount(providerTags)
		if published > 0 {
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

// publishedNodeCount is a diagnostic read of how many nodes the named providers currently publish.
// It takes providerAccess because two rebuilds may swap the provider list concurrently; the dial
// path only pays for it when the group has no member at all.
func (s *Selector) publishedNodeCount(providerTags []string) int {
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

// acquireMember pins the member for the hand-off about to happen. A provider refresh may replace
// and eventually close this object; the lease keeps it open until the dial/listen call returns, so a
// connection is never established through an object that was closed under it. The manager releases
// the lease when the publication barrier already passed and this was the last user.
func (s *Selector) acquireMember(member adapter.Outbound) (func(), bool) {
	if manager, isManager := s.outbound.(adapter.OutboundLeaseManager); isManager {
		return manager.AcquireOutbound(member)
	}
	return func() {}, true
}

// currentMemberLeased returns the committed member together with a lease for the hand-off.
//
// The lease is acquired immediately after the atomic load, and a publication that closed the object
// in that gap makes AcquireOutbound report ok=false; the loop then re-reads the selection, which the
// publication callback has already moved to the replacement (the barrier runs after the callback).
func (s *Selector) currentMemberLeased() (adapter.Outbound, func(), error) {
	for attempt := 0; attempt < 4; attempt++ {
		member, err := s.currentMember()
		if err != nil {
			return nil, nil, err
		}
		release, ok := s.acquireMember(member)
		if ok {
			return member, release, nil
		}
	}
	return nil, nil, E.New("outbound group ", s.Tag(), ": the selected member was replaced during connection setup")
}
func (s *Selector) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	member, release, err := s.currentMemberLeased()
	if err != nil {
		return nil, err
	}
	defer release()
	conn, err := member.DialContext(ctx, network, destination)
	if err != nil {
		return nil, err
	}
	return s.interruptGroup.NewConn(conn, interrupt.IsExternalConnectionFromContext(ctx)), nil
}

// DialParallelNetwork forwards the candidate list to the selected member after the group has made
// its one selection. Without this method the route's DialSerialNetwork falls back to one
// DialContext per candidate, so a blackholed first address consumes the shared deadline and the
// remaining family is never tried. The selected member may itself be a group; the same method
// forwards the list again.
func (s *Selector) DialParallelNetwork(ctx context.Context, network string, destination M.Socksaddr, destinationAddresses []netip.Addr, strategy *C.NetworkStrategy, interfaceType []C.InterfaceType, fallbackInterfaceType []C.InterfaceType, fallbackDelay time.Duration) (net.Conn, error) {
	member, release, err := s.currentMemberLeased()
	if err != nil {
		return nil, err
	}
	defer release()
	if parallel, isParallel := member.(D.ParallelNetworkDialer); isParallel {
		conn, err := parallel.DialParallelNetwork(ctx, network, destination, destinationAddresses, strategy, interfaceType, fallbackInterfaceType, fallbackDelay)
		if err != nil {
			return nil, err
		}
		return s.interruptGroup.NewConn(conn, interrupt.IsExternalConnectionFromContext(ctx)), nil
	}
	conn, err := D.DialSerialNetwork(ctx, member, network, destination, destinationAddresses, strategy, interfaceType, fallbackInterfaceType, fallbackDelay)
	if err != nil {
		return nil, err
	}
	return s.interruptGroup.NewConn(conn, interrupt.IsExternalConnectionFromContext(ctx)), nil
}

func (s *Selector) ListenSerialNetworkPacket(ctx context.Context, destination M.Socksaddr, destinationAddresses []netip.Addr, strategy *C.NetworkStrategy, interfaceType []C.InterfaceType, fallbackInterfaceType []C.InterfaceType, fallbackDelay time.Duration) (net.PacketConn, netip.Addr, error) {
	member, release, err := s.currentMemberLeased()
	if err != nil {
		return nil, netip.Addr{}, err
	}
	defer release()
	if parallel, isParallel := member.(D.ParallelNetworkDialer); isParallel {
		conn, address, err := parallel.ListenSerialNetworkPacket(ctx, destination, destinationAddresses, strategy, interfaceType, fallbackInterfaceType, fallbackDelay)
		if err != nil {
			return nil, address, err
		}
		return s.interruptGroup.NewPacketConn(conn, interrupt.IsExternalConnectionFromContext(ctx)), address, nil
	}
	conn, address, err := D.ListenSerialNetworkPacket(ctx, member, destination, destinationAddresses, strategy, interfaceType, fallbackInterfaceType, fallbackDelay)
	if err != nil {
		return nil, address, err
	}
	return s.interruptGroup.NewPacketConn(conn, interrupt.IsExternalConnectionFromContext(ctx)), address, nil
}

func (s *Selector) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	member, release, err := s.currentMemberLeased()
	if err != nil {
		return nil, err
	}
	defer release()
	conn, err := member.ListenPacket(ctx, destination)
	if err != nil {
		return nil, err
	}
	return s.interruptGroup.NewPacketConn(conn, interrupt.IsExternalConnectionFromContext(ctx)), nil
}

// ResolveURLTestLeaf resolves a detour to the real leaf outbound behind it.
//
// A plain outbound resolves to itself; a group resolves to its current selection, and that
// selection may itself be a group. The result is the node that would actually carry the traffic,
// which is what a measurement's result must be attributed to.
//
// # Why this must be resolved BEFORE measuring
//
// A group's selection can change while a measurement is in flight - a periodic check may finish,
// or a failing connection may clear it. Resolving afterwards would attribute the delay to whatever
// the group had moved TO, which may never have been measured at all. The caller resolves once, up
// front, and the attribution is then a fact about the connection that was actually tested.
//
// # Cycles
//
// A configuration can describe a cycle, and the traversal must not recurse into it. Each visited
// outbound is recorded by identity, so a cycle is detected on the second visit rather than followed
// forever. The previous implementation was a bare loop with no record, so a cycle hung.
func ResolveURLTestLeaf(detour adapter.Outbound, network string) (leaf adapter.Outbound, err error) {
	// Contain a panic from the traversal itself.
	//
	// # Why the resolver and not each caller
	//
	// This walks user-configured objects: Selected() and Tag() are implemented by every group type,
	// including ones this package does not own. A panic there would otherwise reach the runtime and
	// terminate the process, and the callers are on display paths - the Clash API's node list and
	// the native UI both resolve a leaf to label a measurement - where a crash is far worse than an
	// unlabelled node.
	//
	// Recovering here covers every caller at once, including ones added later. The traversal cannot
	// return a partial answer either: either a leaf is resolved or the caller is told it could not
	// be, so no caller can act on half a chain.
	defer func() {
		if recovered := recover(); recovered != nil {
			leaf = nil
			err = E.New("resolve outbound leaf panicked: ", recovered)
		}
	}()

	if detour == nil {
		return nil, E.New("nil detour")
	}
	visited := make(map[adapter.Outbound]struct{}, 4)
	for {
		group, isGroup := detour.(adapter.OutboundGroup)
		if !isGroup {
			return detour, nil
		}
		if _, seen := visited[detour]; seen {
			return nil, E.New("outbound group cycle detected at ", detour.Tag())
		}
		visited[detour] = struct{}{}

		var next adapter.Outbound
		if flowAware, isFlowAware := group.(adapter.FlowAwareOutboundGroup); isFlowAware {
			// A preview. This traversal labels a measurement, and a measurement is not a
			// user flow: it has no metadata to balance on and it does not own the
			// connection it is about to make. Letting it consume a rotation or write an
			// affinity pin would let a health check move the member a real flow is then
			// given.
			next = flowAware.SelectForFlow(nil, network, false)
		} else {
			next = group.Selected(network)
		}
		if next == nil {
			return nil, E.New("outbound group ", group.Tag(), " has no selected member for ", network)
		}
		detour = next
	}
}

// RealTag resolves a detour to the tag of its real leaf outbound, or empty if it cannot be
// resolved.
//
// It is a convenience wrapper over ResolveURLTestLeaf: a caller that needs the outbound itself, or
// that needs to distinguish "no selection" from "cycle", should use that directly.
func RealTag(detour adapter.Outbound, network string) string {
	leaf, err := ResolveURLTestLeaf(detour, network)
	if err != nil || leaf == nil {
		return ""
	}
	return leaf.Tag()
}
