package outbound

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/physicalpath"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
)

var (
	_ adapter.OutboundManager        = (*Manager)(nil)
	_ adapter.DynamicOutboundManager = (*Manager)(nil)
)

type Manager struct {
	// mutation serialises the runtime verbs - Start, Replace and Remove - so a provider update
	// cannot interleave with another update or with the start itself. access still guards the
	// maps and the slice; mutation is the coarser lock around "decide, construct, publish".
	mutation sync.Mutex
	// graphAccess serialises every publication that can ADD an edge to the outbound graph: a node
	// replacement, a removal's dependent scan, and a group's membership publication. It is
	// deliberately narrower than mutation - Start holds mutation for its whole body, and a group's
	// Start runs inside it, so a membership publication that took mutation would deadlock against
	// the start that is publishing it. See graph.go.
	graphAccess sync.Mutex
	lifecycle   *adapter.LifecycleGroup
	stage       adapter.StartStage
	// dnsTransports is the DNS half of the cross-kind graph, recorded when ValidateCrossKindCycles
	// runs so a provider published AFTER that point can still be validated against it. A cycle
	// closed by a late node - dns transport -> outbound -> dns transport - would otherwise be
	// invisible to every check that ran before the node existed.
	dnsTransports []adapter.DNSTransport
	// ownedTags records which owner (a provider tag) published a tag. A retired version of a tag
	// may only be closed after its owner completed a publication round; see ReplaceOwned.
	ownedTags map[string]string
	// retired holds replaced objects whose closure is deferred until the ownership rule below says
	// no new dial can obtain them. See CompletePublication.
	retired []*retiredOutbound
	// users counts in-flight hand-offs per object, including objects that are still published. It is
	// keyed by identity because a tag can move to a replacement while a dial still holds the old object.
	users map[adapter.Outbound]int

	registry                adapter.OutboundRegistry
	endpoint                adapter.EndpointManager
	defaultTag              string
	access                  sync.RWMutex
	outbounds               []adapter.Outbound
	outboundByTag           map[string]adapter.Outbound
	defaultOutbound         adapter.Outbound
	defaultOutboundFallback func() (adapter.Outbound, error)
	physicalPath            *physicalPathValidation
	// logger receives the non-fatal validation findings. It is set by the Box through SetLogger so a
	// warning is produced by the real start path with the normal log interface and level, rather
	// than by a package-level sink.
	logger log.ContextLogger
	// deliveredNetworks is what the ROUTES proved can reach a tag: the networks of the routing
	// rules that name it and constrain the network explicitly. A tag absent from the map has
	// nothing proven about it, which is not the same as "nothing reaches it".
	deliveredNetworks map[string][]string
	// defaultDomainResolver reports the tag `route.default_domain_resolver` names, or "". The
	// cross-kind cycle check reads it: an object that declares no resolver of its own still resolves
	// through this one, so it is the edge that closes the loop for that object.
	defaultDomainResolver func() string
}

// SetDefaultDomainResolver installs a reader for the tag `route.default_domain_resolver` names.
//
// # Why the graph needs it
//
// The cross-kind cycle check models each object's resolver edge. An object that declares NONE of its
// own still resolves through the network-wide default, so the default is the edge that closes the
// loop for it - and MEASURED, a configuration differing from a refused one only in WHERE the
// resolver was named was ACCEPTED while the runtime closes the cycle.
//
// The Box installs it after the route options are known. A manager that never installs it keeps the
// previous behaviour, so no embedder changes.
func (m *Manager) SetDefaultDomainResolver(defaultResolver func() string) {
	m.access.Lock()
	m.defaultDomainResolver = defaultResolver
	m.access.Unlock()
}

// DefaultDomainResolverTag reports the tag `route.default_domain_resolver` names, or "".
//
// It exists for the provider bootstrap diagnosis: whether a download's own hostname resolution can
// close a loop through the provider's nodes depends on which resolver the dialer falls back to. The
// answer is live (the Box installs a closure), so a provider started later sees the current value.
func (m *Manager) DefaultDomainResolverTag() string {
	m.access.RLock()
	resolver := m.defaultDomainResolver
	m.access.RUnlock()
	if resolver == nil {
		return ""
	}
	return resolver()
}

// physicalPathValidation is everything the reachable-leaf dry run needs that is not readable from
// the outbound objects themselves.
//
// # Why the manager owns the dry run rather than the Box
//
// The manager is the only component that holds the whole outbound graph on every construction
// path, and it is the component that already refuses a circular dependency in its own namespace -
// the dry run is the same class of start-time decision, next to the sort that already exists for
// the outbound-only case. Doing it here means the check cannot be forgotten by a caller, and it
// does not duplicate anything: the sort still owns "does the dependency exist" and "is the
// outbound graph acyclic", and the dry run owns "is every reachable LEAF usable", which the sort
// cannot see because it materialises no group members.
type physicalPathValidation struct {
	declarations  physicalpath.Declarations
	resolverFor   func(tag string) string
	defaultConfig bool
	// authorityFor is the LIVE answer to "would a name resolved by this outbound have any DNS
	// authority", built on common/dialer.ResolveAuthority. It is installed by SetResolverAuthority
	// AFTER the DNS transport manager exists, because the manager is what the answer depends on and
	// it is constructed later than this seam. Until then the dry run falls back to the static pair
	// above, which is what an embedder that never calls the setter keeps.
	authorityFor func(tag string) bool
}

// SetLogger installs the logger the reachable-leaf dry run reports non-fatal findings through.
//
// It is a setter rather than a constructor argument because the logger belongs to the Box, and the
// manager is built before the logger factory exists on some embedder paths. A nil logger simply
// means the non-fatal findings are not reported; it must never turn them into an error.
func (m *Manager) SetLogger(logger log.ContextLogger) {
	m.access.Lock()
	m.logger = logger
	m.access.Unlock()
}

// EnablePhysicalPathValidation installs the facts the reachable-leaf dry run needs beyond the
// outbound objects: which tags DECLARED destination_dns_ownership, and how a domain resolver is
// derived for one of them.
//
// A Manager without this runs no dry run, which is what keeps a manager built by a test or an
// embedder that has no configuration text behaving exactly as before. The Box always installs it.
func (m *Manager) EnablePhysicalPathValidation(declarations physicalpath.Declarations, resolverFor func(tag string) string, defaultDomainResolver bool) {
	m.access.Lock()
	m.physicalPath = &physicalPathValidation{
		declarations:  declarations,
		resolverFor:   resolverFor,
		defaultConfig: defaultDomainResolver,
	}
	m.access.Unlock()
}

// SetResolverAuthority installs the LIVE DNS-authority answer, which supersedes the static
// `defaultDomainResolver` bool passed to EnablePhysicalPathValidation.
//
// # Why this is a second call rather than an argument
//
// The answer depends on the DNS transport manager's state, and that manager is constructed AFTER
// this seam is installed (box.go builds the validation seam, then the DNS manager). The static bool
// could only express "route.default_domain_resolver is set", which is two of the four cases
// common/dialer.ResolveAuthority distinguishes, and the two it missed included the one where a name
// resolves fine - so a working configuration was refused.
//
// authorityFor is expected to be built on common/dialer.ResolveAuthority, which is the function
// NewDNSQueryOptions itself is built on. That is the whole point: one function, two callers, so the
// validator cannot hold an opinion the dial path does not.
//
// Passing nil restores the static behaviour.
func (m *Manager) SetResolverAuthority(authorityFor func(tag string) bool) {
	m.access.Lock()
	if m.physicalPath != nil {
		m.physicalPath.authorityFor = authorityFor
	}
	m.access.Unlock()
}

// EnablePhysicalPathDelivery installs what the ROUTES proved can reach a tag: for each tag named by
// a routing rule that constrains the network explicitly, the set of those networks.
//
// # Why delivery is a separate fact from the declarations
//
// It is the fact that decides whether a network requirement is a PROOF or a guess. A requirement
// with no delivery behind it must not be invented - the dry run falls back to what the objects
// advertise between them - and a delivery that IS declared is what lets the dry run refuse a leaf
// that a rule delivers UDP to while it declares it carries only TCP. Installing it separately keeps
// a Manager that was never given it behaving exactly as one that has no route model at all, which
// is what every embedder that calls EnablePhysicalPathValidation alone keeps.
//
// A nil or empty map means nothing was proven, never "no requirement": the two are distinguished by
// presence, so a caller cannot accidentally forbid every network by passing nothing.
func (m *Manager) EnablePhysicalPathDelivery(delivered map[string][]string) {
	m.access.Lock()
	m.deliveredNetworks = delivered
	m.access.Unlock()
}

func NewManager(registry adapter.OutboundRegistry, endpoint adapter.EndpointManager, defaultTag string) *Manager {
	return &Manager{
		registry:      registry,
		endpoint:      endpoint,
		defaultTag:    defaultTag,
		outboundByTag: make(map[string]adapter.Outbound),
		ownedTags:     make(map[string]string),
		users:         make(map[adapter.Outbound]int),
	}
}

func (m *Manager) Initialize(defaultOutboundFallback func() (adapter.Outbound, error)) {
	m.defaultOutboundFallback = defaultOutboundFallback
}

func (m *Manager) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	m.mutation.Lock()
	defer m.mutation.Unlock()
	m.stage = stage
	if m.lifecycle == nil {
		m.lifecycle = adapter.NewLifecycleGroup(scope)
	}
	m.access.Lock()
	if stage == adapter.StartStateInitialize {
		if m.defaultTag != "" && m.defaultOutbound == nil {
			defaultEndpoint, loaded := m.endpoint.Get(m.defaultTag)
			if !loaded {
				m.access.Unlock()
				return E.New("default outbound not found: ", m.defaultTag)
			}
			m.defaultOutbound = defaultEndpoint
		}
		if m.defaultOutbound == nil {
			directOutbound, err := m.defaultOutboundFallback()
			if err != nil {
				m.access.Unlock()
				return E.Cause(err, "create direct outbound for fallback")
			}
			m.outbounds = appendOutbound(m.outbounds, directOutbound)
			m.outboundByTag[directOutbound.Tag()] = directOutbound
			m.defaultOutbound = directOutbound
		}
	}
	outbounds := m.outbounds
	validation := m.physicalPath
	delivered := m.deliveredNetworks
	// The logger is snapshotted with the rest for the same reason SetLogger takes the lock: a
	// concurrent SetLogger must not race with a Start that is already reading the field. Reading it
	// later from the divergent copy would be the same unsynchronised access this avoids.
	loggerForStart := m.logger
	m.access.Unlock()
	if stage == adapter.StartStateStart {
		// The start-order sort runs FIRST, and it is the same call the start itself makes.
		//
		// # Why the order between the two checks is not a detail
		//
		// The sort owns "does a declared dependency exist" and "is the declared graph acyclic", and
		// its message names the dependency chain, which is the more precise report for an outbound
		// cycle. The dry run below walks the same declared edges, so without this ordering it would
		// reach the same cycle first and replace that message with its own - a second answer to a
		// question that already has one. Running the sort here, before the outbound objects are
		// started, gives the pre-existing answer, and the dry run then owns only what the sort
		// cannot see: a group's FULL membership, which the sort never materialises.
		startable, err := m.lintOutbounds(append(append([]adapter.Outbound(nil), outbounds...),
			common.Map(m.endpoint.Endpoints(), func(it adapter.Endpoint) adapter.Outbound { return it })...))
		if err != nil {
			return err
		}
		// The reachable-leaf dry run runs HERE, after the sort and before any outbound is started.
		//
		// # Why it belongs in this manager rather than in a Box stage
		//
		// A group materialises only the member it selected. A configuration whose selector points
		// at a healthy node and whose second member is not usable therefore starts successfully and
		// fails at the FIRST SWITCH - which is decided by a health check or by a user action, so
		// the failure arrives as a traffic outage with no configuration change behind it. Running
		// the dry run here, in front of the start, on the whole graph, is the smallest place that
		// can see every member.
		//
		// It is deliberately NOT duplicated in box.go: this manager owns the outbound namespace and
		// already refuses a circular dependency in it, and a second copy of the traversal in the
		// Box would be a second answer to the same question. box.go supplies only the facts the
		// objects cannot report - see EnablePhysicalPathValidation.
		if validation != nil {
			pathReport, reportErr := m.validatePhysicalPaths(outbounds, validation, delivered)
			if reportErr != nil {
				return reportErr
			}
			// Fatal findings still refuse the start; capability findings are reported once and the
			// start proceeds.
			//
			// A path that cannot carry one network is a risk about a FUTURE flow, not a reason the
			// rest of the configuration cannot run: the runtime already fails that flow on its own.
			// Missing members, identity collisions and unfulfillable declarations stay fatal - they
			// make the configuration unbuildable, and starting would only move the failure later.
			if fatalErr := pathReport.Err(); fatalErr != nil {
				return fatalErr
			}
			if warningErr := pathReport.WarningErr(); warningErr != nil && loggerForStart != nil {
				loggerForStart.Warn(warningErr)
			}
		}
		return m.startOutbounds(append(outbounds, common.Map(m.endpoint.Endpoints(), func(it adapter.Endpoint) adapter.Outbound { return it })...), startable)
	}
	for _, outbound := range outbounds {
		lifecycle, isLifecycle := outbound.(adapter.Lifecycle)
		if !isLifecycle {
			continue
		}
		name := "outbound/" + outbound.Type() + "[" + outbound.Tag() + "]"
		err := m.lifecycle.Start(name, lifecycle, stage)
		if err != nil {
			return err
		}
	}
	return nil
}

// validatePhysicalPaths runs the reachable-leaf dry run over the whole outbound graph.
//
// It is read-only: it dials nothing, resolves nothing, changes no selection and consumes no
// rotation. See common/physicalpath for the model and the per-leaf contract.
func (m *Manager) validatePhysicalPaths(outbounds []adapter.Outbound, validation *physicalPathValidation, delivered map[string][]string) (physicalpath.Report, error) {
	resolver := physicalpath.NewResolver(m.Outbound, physicalpath.Snapshot{}).
		WithDomainResolvers(validation.resolverFor, validation.defaultConfig)
	if validation.authorityFor != nil {
		resolver = resolver.WithResolverAuthority(validation.authorityFor)
	}
	// The roots are ordered so a GROUP is validated before the objects it names.
	//
	// # Why the order is not cosmetic
	//
	// One broken object is reachable by many routes: as a selector's unselected member, and as a
	// top-level outbound in its own right. Reporting it once per route turns one defect into three
	// lines that look like three defects, and the route through the group is the one that carries
	// the information an operator needs - it names the selection step that would hand the flow to
	// it. Validating the groups first and then suppressing the routes they already describe is
	// what makes the report one line per defect.
	//
	// # Why each root is validated on its own
	//
	// The requirement is a fact about ONE root - what the routes proved reaches it, or, when
	// nothing is proven, what its own reachable objects advertise. Passing one set for a batch of
	// roots is precisely the mistake that made the requirement the union of every outbound in the
	// configuration, so the set is computed per root and handed to a call for that root.
	type rootEntry struct {
		root     adapter.Outbound
		optional bool
	}
	entries := make([]rootEntry, 0, len(outbounds)+4)
	for _, outbound := range outbounds {
		if _, isGroup := outbound.(adapter.OutboundGroup); isGroup {
			entries = append(entries, rootEntry{root: outbound})
		}
	}
	for _, outbound := range outbounds {
		if _, isGroup := outbound.(adapter.OutboundGroup); isGroup {
			continue
		}
		entries = append(entries, rootEntry{root: outbound})
	}
	// The default outbound and the endpoints are roots in their own right: the default is what an
	// unmatched flow reaches, and an endpoint is a tunnel the device can be routed into directly.
	// An object a group report already described is not repeated.
	if defaultOutbound := m.Default(); defaultOutbound != nil {
		entries = append(entries, rootEntry{root: defaultOutbound, optional: true})
	}
	for _, endpoint := range m.endpoint.Endpoints() {
		entries = append(entries, rootEntry{root: endpoint, optional: true})
	}
	report := physicalpath.Report{}
	// described is the set of tags a previous root already REPRESENTED, and it is what keeps one
	// defect from being reported once per route that reaches it. It is filled from the tags this
	// root actually covers.
	described := make(map[string]bool)
	// reported is the set of ROOTS that have already contributed a failure, and it is deliberately
	// narrower than described.
	//
	// # Why the key is the root and not the leaf
	//
	// Keying on the leaf made the verdict depend on the order the outbounds were DECLARED in, and
	// suppressed a real one. MEASURED, on a configuration whose two routing rules both deliver udp
	// to one tcp-only leaf, once through a selector and once directly:
	//
	//	outbounds: [tcp-only, A(selector -> tcp-only), B(selector -> tcp-only)]
	//	rules:     {network: udp, outbound: A}, {network: udp, outbound: B}
	//
	// `tcp-only` is a declared outbound, so the manager validates it as a root FIRST and records its
	// tag in `reported`. `A` then failed against that leaf and its failure was DROPPED, so the report
	// named only `outbound/tcp-only` and never mentioned the route. Reordering the outbounds changes
	// which root is named. `Failure.Root` is what tells an operator which entry point to fix, so a
	// unit that hides it is the wrong unit.
	//
	// The duplication the mechanism exists for is one OBJECT described once per route, which the
	// `described` set already handles - a group member that is also a top-level outbound is skipped
	// as a ROOT when `described` covers its tag. `reported` is the narrow second guard that stops one
	// root contributing the same failure twice.
	//
	// # Why a verdict cannot be suppressed by a tag that never failed
	//
	// The requirement is a fact about ONE root: what the routes proved reaches it, or what its own
	// objects advertise. A tag that appeared in an earlier root's route WITHOUT failing has not
	// been judged against this root's requirement, so suppressing its failure here makes the
	// report depend on the order the outbounds happen to be declared in - the same configuration
	// refused when a dependency is listed first and accepted when it is listed last. Filtering on
	// the failures that were actually reported keeps the deduplication (one line per defect) and
	// drops the order dependence.
	reported := make(map[string]bool)
	seenRoot := make(map[adapter.Outbound]bool)
	for _, entry := range entries {
		root := entry.root
		if root == nil || seenRoot[root] {
			continue
		}
		if entry.optional && described[root.Tag()] {
			continue
		}
		seenRoot[root] = true
		rootReport, err := physicalpath.ValidateRoots(resolver, []adapter.Outbound{root}, m.endpoint, deliveredNetworksFor(delivered, root), validation.declarations)
		if err != nil {
			return physicalpath.Report{}, err
		}
		report.Roots = append(report.Roots, rootReport.Roots...)
		report.Nodes = append(report.Nodes, rootReport.Nodes...)
		for _, failure := range rootReport.Failures {
			if reported[failure.Root] {
				continue
			}
			reported[failure.Root] = true
			report.Failures = append(report.Failures, failure)
		}
		// The set of tags this root DESCRIBED. The roots that follow must not repeat them: a
		// broken member of a group is also a top-level outbound in its own right, and reporting it
		// once per route turns one defect into three lines that look like three defects.
		for _, node := range rootReport.Nodes {
			described[node.Hop] = true
			for _, tag := range strings.Split(node.Path, " -> ") {
				described[tag] = true
			}
		}
		for _, failure := range rootReport.Failures {
			described[failure.Leaf] = true
		}
	}
	return report, nil
}

// deliveredNetworksFor reports the networks PROVEN to reach one root, or nil when nothing is.
//
// Nil and an empty slice both mean "nothing proven" to the dry run, so a tag that no rule
// constrains is reported the same way as a tag no rule names: the requirement then comes from what
// the root's own objects advertise.
func deliveredNetworksFor(delivered map[string][]string, root adapter.Outbound) []string {
	if len(delivered) == 0 {
		return nil
	}
	return delivered[root.Tag()]
}

// lintOutbounds is the start-order sort and the per-kind dependency validation, separated from the
// start itself so it can run BEFORE the reachable-leaf dry run.
//
// It returns the topological order it computed, so an outbound is sorted exactly once per Start:
// the dry run and the checks that follow it must not pay for a second sort, and - more importantly
// - must not be able to disagree with the order the start will actually use.
//
// The two failure modes it owns are the ones it always owned: `dependency[tag] not found for
// outbound[tag]` for a declared tag that names nothing, and `circular outbound dependency: a -> b
// -> a` for a declared cycle. Both are reported from the declared DEPENDENCIES, which is the same
// edge set the dry run reads.
func (m *Manager) lintOutbounds(outbounds []adapter.Outbound) ([]adapter.Outbound, error) {
	// A TAG BELONGS TO ONE OBJECT, and the sort below cannot work otherwise.
	//
	// The ordering is keyed by tag, which is what the dependency edges name, so two objects sharing a
	// tag are one key for two nodes and the loop can never mark both as started. MEASURED, before
	// this check existed: an endpoint tagged `direct` collides with the `direct` outbound this
	// manager creates as its own fallback, and `Start` PANICS with a nil dereference at the
	// `currentOutbound.Tag()` call below - a crash on a configuration, not a diagnostic. It is
	// reachable from a shipped protocol: `{"endpoints":[{"type":"wireguard","tag":"direct",...}]}`
	// with no declared outbound tagged `direct`.
	//
	// The collision is reported rather than resolved, because which object the tag was meant for is
	// the user's decision. This manager cannot be the one that decides it: the two namespaces are
	// populated by different managers at different times, and nothing rejects a shared tag at
	// registration - see tag_collision_test.go, which documents exactly this contract.
	type seenObject struct {
		outbound adapter.Outbound
		kind     string
	}
	seenTag := make(map[string]seenObject, len(outbounds))
	for _, outboundToCheck := range outbounds {
		if outboundToCheck == nil {
			return nil, E.New("the outbound set contains a nil object, so its dependency graph " +
				"cannot be ordered")
		}
		tag := outboundToCheck.Tag()
		thisKind := "outbound"
		if m.endpoint != nil && len(m.endpoint.Endpoints()) > 0 {
			for _, candidate := range m.endpoint.Endpoints() {
				if candidate == outboundToCheck {
					thisKind = "endpoint"
					break
				}
			}
		}
		previous, duplicate := seenTag[tag]
		if !duplicate {
			seenTag[tag] = seenObject{outbound: outboundToCheck, kind: thisKind}
			continue
		}
		if previous.outbound == outboundToCheck {
			// The SAME object in the list twice is a caller's bookkeeping slip rather than a
			// configuration error, and reporting it as a name clash would send the user looking for
			// a second object that does not exist.
			return nil, E.New("the object ", tag, " appears twice in the outbound set, so its "+
				"dependency graph cannot be ordered")
		}
		return nil, E.New("two objects claim the tag ", tag, ": ", describeOutbound(previous.outbound, previous.kind),
			" and ", describeOutbound(outboundToCheck, thisKind), "; a tag resolves to one object, so rename "+
				"one of them - the tag is how every dependency edge and every routing rule names it")
	}
	started := make(map[string]bool, len(outbounds))
	order := make([]adapter.Outbound, 0, len(outbounds))
	for {
		canContinue := false
	startOne:
		for _, outboundToStart := range outbounds {
			outboundTag := outboundToStart.Tag()
			if started[outboundTag] {
				continue
			}
			dependencies := outboundToStart.Dependencies()
			for _, dependency := range dependencies {
				if !started[dependency] {
					continue startOne
				}
			}
			started[outboundTag] = true
			order = append(order, outboundToStart)
			canContinue = true
		}
		if len(started) == len(outbounds) {
			break
		}
		if canContinue {
			continue
		}
		currentOutbound := common.Find(outbounds, func(it adapter.Outbound) bool {
			return !started[it.Tag()]
		})
		// Unreachable while the tags are unique, because then every pending object is a `Find`
		// candidate. Kept as a diagnostic rather than a nil dereference: the guarantee above is what
		// makes it unreachable, and a crash is the wrong way to discover that it stopped holding.
		if currentOutbound == nil {
			return nil, E.New("the outbound graph cannot be ordered and no unstarted object could " +
				"be found, which means the tag set is not the one this sort was given")
		}
		var lintOutbound func(oTree []string, oCurrent adapter.Outbound) error
		lintOutbound = func(oTree []string, oCurrent adapter.Outbound) error {
			problemOutboundTag := common.Find(oCurrent.Dependencies(), func(it string) bool {
				return !started[it]
			})
			if common.Contains(oTree, problemOutboundTag) {
				return E.New("circular outbound dependency: ", strings.Join(oTree, " -> "), " -> ", problemOutboundTag)
			}
			problemOutbound := common.Find(outbounds, func(it adapter.Outbound) bool {
				return it.Tag() == problemOutboundTag
			})
			if problemOutbound == nil {
				return E.New("dependency[", problemOutboundTag, "] not found for outbound[", oCurrent.Tag(), "]")
			}
			return lintOutbound(append(oTree, problemOutboundTag), problemOutbound)
		}
		return nil, lintOutbound([]string{currentOutbound.Tag()}, currentOutbound)
	}
	return order, nil
}

// describeOutbound names an object the way a user recognises it, and it is guaranteed to
// DISCRIMINATE two objects that share a tag.
//
// # Why the KIND is derived from the manager, not from an interface assertion
//
// The first version asked `_, isEndpoint := outbound.(adapter.Endpoint)` and labelled the object an
// endpoint when it was satisfied. MEASURED, `*direct.Outbound` SATISFIES that interface - it has a
// `Start(stage, scope)` method - so the refusal reported BOTH halves as `endpoint/...`, one of them
// being the plain `direct` outbound. A diagnostic that mislabels one of the two objects it asks the
// user to choose between is worse than no diagnostic, and the interface was never a reliable answer:
// `adapter.Endpoint` is a loose structural shape that many outbounds satisfy by accident.
//
// The manager KNOWS which namespace it holds an object in, so the kind is passed in rather than
// guessed. The concrete Go type is appended as well, because `Type()` is not unique across this
// fork's registrations either.
func describeOutbound(outbound adapter.Outbound, kind string) string {
	description := kind + "/" + outbound.Tag() + " (" + outbound.Type()
	if concrete := fmt.Sprintf("%T", outbound); concrete != "" {
		description += ", " + concrete
	}
	return description + ")"
}

// startOutbounds starts the outbounds in the order lintOutbounds computed.
//
// The order is passed in rather than recomputed: a second sort could disagree with the one the
// validation saw, and the whole point of moving the validation in front of the start is that both
// describe the same graph.
func (m *Manager) startOutbounds(outbounds []adapter.Outbound, order []adapter.Outbound) error {
	started := make(map[string]bool, len(outbounds))
	for _, outboundToStart := range order {
		outboundTag := outboundToStart.Tag()
		started[outboundTag] = true
		if endpoint, isEndpoint := outboundToStart.(adapter.Endpoint); isEndpoint {
			err := m.endpoint.StartEndpoint(endpoint)
			if err != nil {
				return err
			}
			continue
		}
		lifecycle, isLifecycle := outboundToStart.(adapter.Lifecycle)
		if !isLifecycle {
			continue
		}
		name := "outbound/" + outboundToStart.Type() + "[" + outboundTag + "]"
		err := m.lifecycle.Start(name, lifecycle, adapter.StartStateStart)
		if err != nil {
			return err
		}
	}
	// An endpoint attached to this manager but not part of the sorted set - which happens when an
	// endpoint is registered after the sort read the list - is still started, because the endpoint
	// manager owns its lifecycle. This preserves the previous behaviour exactly for that case.
	for _, outboundToStart := range outbounds {
		if started[outboundToStart.Tag()] {
			continue
		}
		endpoint, isEndpoint := outboundToStart.(adapter.Endpoint)
		if !isEndpoint {
			continue
		}
		if err := m.endpoint.StartEndpoint(endpoint); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) Outbounds() []adapter.Outbound {
	m.access.RLock()
	defer m.access.RUnlock()
	// The slice header is returned WITHOUT copying, and every mutation below publishes a NEW slice
	// instead of writing this one.
	//
	// # Why that is the contract, not a micro-optimisation
	//
	// A reader (route.ReferenceManager's walk is one) takes the header under the lock and then
	// iterates it after releasing it. Mutating the backing array in place - replacing one element,
	// or shifting after a removal - therefore writes memory a reader is walking. MEASURED by `-race`
	// during provider teardown: `Manager.unlinkLocked`'s append-shift raced with
	// `ReferenceManager.update` reading the same array, in a test that only called Close. Copy-on-
	// write makes the published slice immutable, so a reader's snapshot stays valid for as long as
	// it holds it.
	return m.outbounds
}

func (m *Manager) Outbound(tag string) (adapter.Outbound, bool) {
	m.access.RLock()
	outbound, found := m.outboundByTag[tag]
	endpoint := m.endpoint
	m.access.RUnlock()
	if found {
		return outbound, true
	}
	// An embedder may build a manager without an endpoint namespace (NewManager accepts a nil
	// endpoint, and the graph tests do exactly that). Treating it as "no fallback" is the only
	// answer that keeps the lookup total; dereferencing it turns a missing tag into a panic.
	if endpoint == nil {
		return nil, false
	}
	return endpoint.Get(tag)
}

func (m *Manager) Default() adapter.Outbound {
	m.access.RLock()
	defer m.access.RUnlock()
	return m.defaultOutbound
}

func (m *Manager) Create(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, inboundType string, options any) error {
	if tag == "" {
		return os.ErrInvalid
	}

	// Reject a duplicate BEFORE constructing anything.
	//
	// Constructing first and replacing afterwards discards an object whose constructor side effects
	// cannot be undone - a bridge outbound claims a process-global slot at construction, for example
	// - and it silently runs a configuration the user did not write.
	m.access.Lock()
	if _, loaded := m.outboundByTag[tag]; loaded {
		m.access.Unlock()
		return E.New("outbound ", tag, " already exists")
	}
	m.access.Unlock()

	outbound, err := m.registry.CreateOutbound(ctx, router, logger, tag, inboundType, options)
	if err != nil {
		return err
	}
	m.access.Lock()
	defer m.access.Unlock()
	// Re-check under the lock: the constructor runs outside it, so another goroutine may have
	// installed this tag meanwhile. The loser releases what it built rather than leaking it.
	if _, loaded := m.outboundByTag[tag]; loaded {
		_ = common.Close(outbound)
		return E.New("outbound ", tag, " already exists")
	}
	m.outbounds = appendOutbound(m.outbounds, outbound)
	m.outboundByTag[tag] = outbound
	if tag == m.defaultTag || (m.defaultTag == "" && m.defaultOutbound == nil) {
		m.defaultOutbound = outbound
	}
	return nil
}

// Replace publishes a runtime replacement for one tag: prepare, start, swap, then release the old
// object. It is the only mutation that may target an existing tag, because that is what a provider
// update is - the same node, redefined.
//
// # Why prepare-then-swap rather than remove-then-create
//
// A constructor or Start that fails must leave the working node in place. Removing first would turn
// a bad subscription entry into an outage for a node that had been serving traffic, and the
// provider would then have to decide whether to restore it. Here the old object is unlinked only
// after the new one is fully started, and the caller receives the error with the old node intact.
//
// The stages the new object is started through are bounded by m.stage: a replacement created after
// the Box finished starting must not receive a stage the Box has already left, and one created
// during the start must receive every stage the manager has reached.
func (m *Manager) Replace(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, outboundType string, options any) error {
	return m.replace(ctx, router, logger, tag, outboundType, options, "")
}

// ReplaceOwned is Replace for an owner-managed namespace (an outbound provider).
//
// # The ownership rule it starts
//
// The replacement becomes addressable by tag immediately, but the object it replaced is NOT closed
// here. A provider-fed group still holds the previous object until its callback rebuilds the member
// list, and closing it now would hand a new dial a closed object while a valid replacement is
// already published. MEASURED on the unfixed manager: a selector kept dialling the old object and
// every dial in that window failed with net.ErrClosed.
//
// The old object is parked as RETIRED with its owner. It becomes eligible for closure only when
// `CompletePublication(owner, tags)` says the owner notified every group that names it and every
// callback succeeded, AND no live object outside the owner still names the tag (a cached direct
// detour or DNS transport would otherwise keep resolving the retired object). Until then it stays
// open: a dial in the window gets a working object, never a closed one.
func (m *Manager) ReplaceOwned(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, outboundType string, options any, owner string) error {
	if owner == "" {
		return os.ErrInvalid
	}
	return m.replace(ctx, router, logger, tag, outboundType, options, owner)
}

func (m *Manager) replace(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, outboundType string, options any, owner string) error {
	if tag == "" {
		return os.ErrInvalid
	}
	m.mutation.Lock()
	defer m.mutation.Unlock()
	outbound, err := m.registry.CreateOutbound(ctx, router, logger, tag, outboundType, options)
	if err != nil {
		return err
	}
	if m.lifecycle != nil {
		if lifecycle, isLifecycle := outbound.(adapter.Lifecycle); isLifecycle {
			name := "outbound/" + outbound.Type() + "[" + tag + "]"
			for _, stage := range adapter.ListStartStages {
				if stage > m.stage {
					break
				}
				if err = m.lifecycle.Start(name, lifecycle, stage); err != nil {
					// Release the partial start, then leave the old object untouched.
					return E.Errors(err, m.lifecycle.Remove(lifecycle))
				}
			}
		}
	}
	// The prospective graph is validated BEFORE the swap, under the graph lock, so an invalid
	// replacement leaves the previous node published and serving.
	//
	// # Why this check exists in addition to the start-order sort
	//
	// The sort only sees `Dependencies()` - the tags an object declared. A provider node's edge to a
	// group is declared here, but the group's edge BACK to this node lives in the group's published
	// membership, which the sort never walked because the provider had not published when it ran.
	// MEASURED: that pair starts successfully and recurses on the first dial. See graph.go.
	m.graphAccess.Lock()
	if err = m.validateGraphLocked(graphProposal{
		replace: map[string]adapter.Outbound{tag: outbound},
	}); err != nil {
		m.graphAccess.Unlock()
		// The new object was constructed and started; it must not leak, and the old node must stay.
		m.release(outbound)
		return err
	}
	m.access.Lock()
	old := m.outboundByTag[tag]
	if old == nil {
		m.outbounds = appendOutbound(m.outbounds, outbound)
	} else {
		m.outbounds = replaceOutbound(m.outbounds, old, outbound)
	}
	m.outboundByTag[tag] = outbound
	if owner != "" {
		m.ownedTags[tag] = owner
	}
	if old != nil && m.defaultOutbound == old {
		m.defaultOutbound = outbound
	} else if tag == m.defaultTag || (m.defaultTag == "" && m.defaultOutbound == nil) {
		m.defaultOutbound = outbound
	}
	var closeNow, sweep []adapter.Outbound
	if old != nil {
		if owner == "" {
			// A caller that is not an owner-managed publisher keeps the historical behaviour: the
			// previous object is closed at the swap. Such a caller has no group-callback phase.
			closeNow = append(closeNow, old)
		} else {
			m.retired = append(m.retired, &retiredOutbound{outbound: old, tag: tag, owner: owner})
		}
	}
	sweep = m.takeSweepableRetiredLocked()
	m.access.Unlock()
	m.graphAccess.Unlock()
	for _, object := range closeNow {
		m.release(object)
	}
	for _, object := range sweep {
		m.release(object)
	}
	return nil
}

// retiredOutbound is a replaced object parked until no new dial can obtain it.
type retiredOutbound struct {
	outbound adapter.Outbound
	tag      string
	owner    string
	// ready is set by CompletePublication: the owner has notified every group that names it, and
	// every callback succeeded. Only external references and in-flight users can still keep the
	// object open after that.
	ready bool
	// released is set exactly once when this entry is handed to Close, so a late lease release or a
	// second sweep cannot close the object twice.
	released bool
}

// AcquireOutbound pins a retired object for one hand-off: from the moment a caller obtained it to
// the moment its dial/listen call returns a connection (or fails).
//
// # Why this is needed on top of the publication barrier
//
// CompletePublication proves that every GROUP rebuilt its membership, not that a dial which already
// read the old selection has finished with it. MEASURED deterministically: a member's DialContext
// was blocked at entry, the provider replaced the tag and completed the publication round, and the
// old object was closed while that dial was still inside it - the dial then failed with
// net.ErrClosed although a valid replacement was published.
//
// The lease is per object and non-blocking: it takes m.access only for the counter update, never
// holds a lock across the network dial, and a release closes the object only when the publication
// barrier already passed and the count reaches zero. Callers that do not implement the manager (test
// doubles) get a no-op.
func (m *Manager) AcquireOutbound(object adapter.Outbound) (release func(), ok bool) {
	if m == nil || object == nil {
		return func() {}, false
	}
	// Fast path for the overwhelmingly common case: a non-provider object has no retirement
	// lifecycle, so a read lock and two map lookups are enough and the hot dial path never takes the
	// exclusive manager lock. Only objects published by a provider need a user count.
	m.access.RLock()
	_, owned := m.ownedTags[object.Tag()]
	current := m.outboundByTag[object.Tag()]
	m.access.RUnlock()
	if !owned {
		return func() {}, true
	}
	if current == object {
		// Still published: the lease is what carries the user count across a later retirement.
		m.access.Lock()
		m.users[object]++
		m.access.Unlock()
		return m.leaseRelease(object), true
	}
	m.access.Lock()
	entry := m.retiredEntryLocked(object)
	if entry != nil && !entry.released {
		m.users[object]++
		m.access.Unlock()
		return m.leaseRelease(object), true
	}
	m.access.Unlock()
	// The tag is owned by a provider but this object is neither published nor retired: it was
	// already closed, and a dial must not use it.
	return func() {}, false
}

// leaseRelease makes the release idempotent: a caller that defers it and a retry path cannot double
// count one hand-off.
func (m *Manager) leaseRelease(object adapter.Outbound) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			m.releaseLease(object)
		})
	}
}

// releaseLease drops one in-flight user and closes the entry if it became closable.
func (m *Manager) releaseLease(object adapter.Outbound) {
	if m == nil || object == nil {
		return
	}
	m.mutation.Lock()
	m.graphAccess.Lock()
	m.access.Lock()
	if count := m.users[object]; count > 1 {
		m.users[object] = count - 1
	} else {
		delete(m.users, object)
	}
	entry := m.retiredEntryLocked(object)
	if entry == nil || entry.released || m.users[object] != 0 || !entry.ready || m.externalReferenceLocked(entry.tag, entry.owner) {
		m.access.Unlock()
		m.graphAccess.Unlock()
		m.mutation.Unlock()
		return
	}
	entry.released = true
	for index, candidate := range m.retired {
		if candidate == entry {
			m.retired = append(m.retired[:index], m.retired[index+1:]...)
			break
		}
	}
	m.access.Unlock()
	m.graphAccess.Unlock()
	m.mutation.Unlock()
	m.release(object)
}

// retiredEntryLocked finds the retirement record for an object identity. The caller holds m.access.
func (m *Manager) retiredEntryLocked(object adapter.Outbound) *retiredOutbound {
	for _, candidate := range m.retired {
		if candidate.outbound == object {
			return candidate
		}
	}
	return nil
}

// CompletePublication is the owner's publication barrier: it releases the retired objects this
// owner parked for the named tags, and retries older READY entries whose external references have
// since disappeared.
//
// The provider calls it only after UpdateGroups returned nil, i.e. after every callback subscribed
// to that provider rebuilt its membership from the provider's current list. A failed round calls
// nothing, so the previous objects stay open and keep serving the previous selection - the failed
// update preserves the old state instead of breaking it.
//
// An entry is NOT closed while a live object outside the owner still names its tag. A direct
// `detour` or a DNS transport that was resolved before the swap may hold the old object in a
// one-shot cache; releasing it would make that holder fail on its next use. Those are retained (and
// released later by Remove/Replace or at teardown), which is the deliberate conservative edge: a
// configuration that references provider nodes directly is documented as not redirected by a
// refresh.
func (m *Manager) CompletePublication(owner string, tags []string) error {
	if m == nil || owner == "" {
		return nil
	}
	wanted := make(map[string]bool, len(tags))
	for _, tag := range tags {
		wanted[tag] = true
	}
	m.mutation.Lock()
	m.graphAccess.Lock()
	m.access.Lock()
	var release []adapter.Outbound
	kept := make([]*retiredOutbound, 0, len(m.retired))
	for _, entry := range m.retired {
		if entry.owner != owner {
			kept = append(kept, entry)
			continue
		}
		if wanted[entry.tag] {
			entry.ready = true
		}
		if entry.ready && m.users[entry.outbound] == 0 && !m.externalReferenceLocked(entry.tag, owner) {
			entry.released = true
			release = append(release, entry.outbound)
			continue
		}
		kept = append(kept, entry)
	}
	m.retired = kept
	m.access.Unlock()
	m.graphAccess.Unlock()
	m.mutation.Unlock()
	for _, object := range release {
		m.release(object)
	}
	return nil
}

// externalReferenceLocked reports whether a live object OUTSIDE owner still names tag in a way that
// can resolve to an object rather than to the tag's current publisher.
//
// Groups are excluded on purpose: a provider-fed group lists member TAGS, and after a successful
// publication round it resolves them through this manager again, so its stale listing is not a
// reference to the retired object. Every other live object (a static outbound, an endpoint, a DNS
// transport) may have built a detour dialer and cached the object, so it is treated as one.
func (m *Manager) externalReferenceLocked(tag string, owner string) bool {
	for _, other := range m.outbounds {
		if m.ownedTags[other.Tag()] == owner {
			continue
		}
		if _, isGroup := other.(adapter.OutboundGroup); isGroup {
			continue
		}
		if objectReferencesTag(other, tag) {
			return true
		}
	}
	if m.endpoint != nil {
		for _, endpoint := range m.endpoint.Endpoints() {
			if objectReferencesTag(endpoint, tag) {
				return true
			}
		}
	}
	for _, transport := range m.dnsTransports {
		if transportReferencesTag(transport, tag) {
			return true
		}
	}
	return false
}

// takeSweepableRetiredLocked removes READY retired entries that no longer have an external
// reference and returns them for closure outside the locks. It is called after a namespace mutation
// (Replace/Remove/Create) because that mutation may have been the last holder of a tag.
func (m *Manager) takeSweepableRetiredLocked() []adapter.Outbound {
	if len(m.retired) == 0 {
		return nil
	}
	var release []adapter.Outbound
	kept := make([]*retiredOutbound, 0, len(m.retired))
	for _, entry := range m.retired {
		if entry.ready && m.users[entry.outbound] == 0 && !m.externalReferenceLocked(entry.tag, entry.owner) {
			entry.released = true
			release = append(release, entry.outbound)
			continue
		}
		kept = append(kept, entry)
	}
	m.retired = kept
	return release
}

// Remove unlinks a runtime outbound and releases what it owns.
//
// It refuses while a remaining object still names the tag as a dependency. "Names" includes the
// DECLARED dependencies (a detour, a group's configured members) AND the membership a group has
// PUBLISHED: a selector fed by a provider lists the provider's nodes in `All()` without naming them
// in its options, and that is exactly the reference a provider update replaces. MEASURED before the
// membership was read: the provider removed a node the selector had just selected, closed it, and
// the selector kept handing flows to the closed object until its own callback landed.
//
// It refuses while a DNS transport's detour or resolver still names the tag for the same reason:
// the transport is not in this manager's list, but it holds a live reference.
func (m *Manager) Remove(tag string) error {
	m.mutation.Lock()
	defer m.mutation.Unlock()
	m.graphAccess.Lock()
	m.access.Lock()
	outbound, found := m.outboundByTag[tag]
	if !found {
		m.access.Unlock()
		m.graphAccess.Unlock()
		return os.ErrInvalid
	}
	dependents := m.dependentsLocked(outbound, tag)
	if len(dependents) > 0 {
		m.access.Unlock()
		m.graphAccess.Unlock()
		return E.New("outbound[", tag, "] is depended by ", strings.Join(dependents, ", "))
	}
	wasDefault := m.unlinkLocked(tag, outbound)
	fallback := m.defaultAfterRemovalLocked(wasDefault)
	sweep := m.takeSweepableRetiredLocked()
	m.access.Unlock()
	m.graphAccess.Unlock()
	if fallback != nil && m.logger != nil {
		m.logger.Info("updated default outbound to ", fallback.Tag())
	}
	m.release(outbound)
	for _, object := range sweep {
		m.release(object)
	}
	return nil
}

// Release unlinks a runtime outbound WITHOUT the dependent guard, for teardown only.
//
// # Why this exists rather than a Remove that happens to succeed
//
// The Box closes components in reverse registration order, and a provider starts AFTER the outbound
// manager, so the provider is closed while the groups that name its nodes are still registered and
// still list them. A membership-aware Remove would refuse every unlink, and a perfectly valid
// configuration would report an error on every normal shutdown.
//
// The safety argument is the teardown ordering itself: the whole object graph is being dismantled,
// no flow can be routed into a group that is about to be closed, and the provider owns the nodes it
// unlinks. It must never be used as a way around the guard while traffic is possible - Remove is
// the guarded verb, and this is not exported through adapter.DynamicOutboundManager.
func (m *Manager) Release(tag string) error {
	m.mutation.Lock()
	defer m.mutation.Unlock()
	m.graphAccess.Lock()
	m.access.Lock()
	outbound, found := m.outboundByTag[tag]
	if !found {
		m.access.Unlock()
		m.graphAccess.Unlock()
		return os.ErrInvalid
	}
	wasDefault := m.unlinkLocked(tag, outbound)
	m.defaultAfterRemovalLocked(wasDefault)
	m.access.Unlock()
	m.graphAccess.Unlock()
	m.release(outbound)
	return nil
}

// dependentsLocked lists every live object that would still name tag after outbound is unlinked.
// The caller holds m.access.
func (m *Manager) dependentsLocked(outbound adapter.Outbound, tag string) []string {
	var dependents []string
	for _, other := range m.outbounds {
		if other == outbound {
			continue
		}
		if objectReferencesTag(other, tag) {
			dependents = append(dependents, other.Tag())
		}
	}
	if m.endpoint != nil {
		for _, endpoint := range m.endpoint.Endpoints() {
			if objectReferencesTag(endpoint, tag) {
				dependents = append(dependents, endpoint.Tag())
			}
		}
	}
	for _, transport := range m.dnsTransports {
		if transportReferencesTag(transport, tag) {
			dependents = append(dependents, "dns/"+transport.Tag())
		}
	}
	return dependents
}

// transportReferencesTag reports whether a DNS transport would still resolve tag at request time:
// its own resolver chain `Dependencies()`, or the outbound its `detour` names through References().
func transportReferencesTag(transport adapter.DNSTransport, tag string) bool {
	if transport == nil {
		return false
	}
	if common.Contains(transport.Dependencies(), tag) {
		return true
	}
	if referrer, isReferrer := transport.(adapter.Referrer); isReferrer {
		if common.Contains(referrer.References(), tag) {
			return true
		}
	}
	return false
}

// objectReferencesTag reports whether an object would still resolve tag at dial time.
//
// All three publications are read: the declared dependencies (what the sort validated), the group
// membership (what the group can select), and the references (what a DNS transport detours to or a
// group currently selected). A tag is only removable when NONE of them names it.
func objectReferencesTag(object adapter.Outbound, tag string) bool {
	if object == nil {
		return false
	}
	if common.Contains(object.Dependencies(), tag) {
		return true
	}
	if group, isGroup := object.(adapter.OutboundGroup); isGroup {
		if common.Contains(group.All(), tag) {
			return true
		}
	}
	if referrer, isReferrer := object.(adapter.Referrer); isReferrer {
		if common.Contains(referrer.References(), tag) {
			return true
		}
	}
	return false
}

// unlinkLocked removes the tag and its object from the published maps. The caller holds m.access.
// It reports whether the object was the default, which the caller uses to publish the fallback and
// to log the change outside the lock.
func (m *Manager) unlinkLocked(tag string, outbound adapter.Outbound) bool {
	delete(m.outboundByTag, tag)
	delete(m.ownedTags, tag)
	m.outbounds = removeOutbound(m.outbounds, outbound)
	if m.defaultOutbound == outbound {
		m.defaultOutbound = nil
		return true
	}
	return false
}

// appendOutbound publishes a new slice with the object appended, so a reader holding the previous
// header keeps walking an array nobody writes. See Outbounds.
func appendOutbound(outbounds []adapter.Outbound, outbound adapter.Outbound) []adapter.Outbound {
	next := make([]adapter.Outbound, len(outbounds), len(outbounds)+1)
	copy(next, outbounds)
	return append(next, outbound)
}

// replaceOutbound publishes a new slice with old replaced by replacement.
func replaceOutbound(outbounds []adapter.Outbound, old adapter.Outbound, replacement adapter.Outbound) []adapter.Outbound {
	next := make([]adapter.Outbound, len(outbounds))
	copy(next, outbounds)
	for i, item := range next {
		if item == old {
			next[i] = replacement
			break
		}
	}
	return next
}

// removeOutbound publishes a new slice without the object.
func removeOutbound(outbounds []adapter.Outbound, outbound adapter.Outbound) []adapter.Outbound {
	next := make([]adapter.Outbound, 0, len(outbounds)-1)
	for _, item := range outbounds {
		if item == outbound {
			continue
		}
		next = append(next, item)
	}
	return next
}

// defaultAfterRemovalLocked republishes a default when the removed object was it. The caller holds
// m.access. It returns the new default (nil when there is none) so the caller can log outside the
// lock.
func (m *Manager) defaultAfterRemovalLocked(wasDefault bool) adapter.Outbound {
	if !wasDefault {
		return nil
	}
	if len(m.outbounds) > 0 {
		m.defaultOutbound = m.outbounds[0]
		return m.defaultOutbound
	}
	m.defaultOutbound = nil
	return nil
}

// release closes an outbound that is no longer published. A component started through the
// LifecycleGroup is released through its own scope, which is what keeps the parent's cleanup list
// from retaining a component that has already gone.
func (m *Manager) release(outbound adapter.Outbound) {
	if m.lifecycle != nil {
		if lifecycle, isLifecycle := outbound.(adapter.Lifecycle); isLifecycle {
			if err := m.lifecycle.Remove(lifecycle); err != nil && m.logger != nil {
				m.logger.Error(err, "close outbound [", outbound.Tag(), "]")
			}
			return
		}
	}
	if err := common.Close(outbound); err != nil && m.logger != nil {
		m.logger.Error(err, "close outbound [", outbound.Tag(), "]")
	}
}
