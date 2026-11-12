package outbound

import (
	"slices"
	"strings"

	"github.com/sagernet/sing-box/adapter"
	E "github.com/sagernet/sing/common/exceptions"
)

// Runtime graph validation.
//
// # The invariant this file exists for
//
// The outbound manager's start-order sort refuses a cycle in `Dependencies()` - the tags an object
// declared in its own options. That is the whole graph for a static configuration, but it is NOT
// the whole graph once providers exist: a provider publishes nodes after Start, and a group's
// membership (`All()`) is what makes a published node an edge. MEASURED on the unfixed tree:
//
//	provider p publishes `p/a` with `detour: g`
//	selector `g` names provider p and therefore selects `p/a`
//
// `g` was constructed with no members and its options declare none, so the sort sees no cycle;
// `p/a` is fine on its own when it is installed, so `Replace` sees no cycle either. The recursion
// appears only after the provider's callback makes `p/a` selectable, and then it is unbounded:
// dial `p/a` -> dial through `g` -> select `p/a` -> dial through `g` ...
//
// So the graph that must be acyclic is the UNION of the declared dependencies and the published
// membership, and it must be re-validated on every mutation that can add an edge: a runtime node
// replacement, a runtime removal, and a group publishing a new member list. The static sort keeps
// owning "does a declared dependency exist" and its own cycle message; this check owns the graph the
// runtime will actually walk after publication.
//
// # Why the membership is the edge set, not the selection
//
// A selector's `All()` lists every member it can be switched to, and the Clash API can switch to any
// of them. A cycle that exists in the membership graph - `g -> p/a -> g` - is therefore reachable at
// any time even when the CURRENT selection is a harmless member. The static sort already treats all
// declared members as edges for the same reason, so using the membership here is also what keeps the
// static and dynamic verdicts identical for the same final graph.

// graphProposal is one prospective change to the published graph, already constructed.
//
// # Why a proposal rather than "validate after publishing"
//
// A validation that runs after the mutation can only tell the caller that the graph is now broken.
// The requirement is that an invalid update leaves the PREVIOUS valid nodes and selection in place,
// so the check must run on the prospective graph and the mutation must happen only when it passes.
type graphProposal struct {
	// replace maps a tag to the object that would take its place. A nil object means the tag would
	// be absent; Remove uses that form.
	replace map[string]adapter.Outbound
	// members maps a group tag to the member list it is about to publish, replacing the list the
	// live object currently reports.
	members map[string][]string
}

// UpdateGroupMembership validates the graph as it would be after groupTag publishes members, and
// runs commit under the graph lock when the graph is acyclic.
//
// It is the group-facing half of this file: see adapter.GroupMembershipPublisher for why the
// manager mediates the publication rather than letting the group commit first.
func (m *Manager) UpdateGroupMembership(groupTag string, members []string, commit func()) error {
	if m == nil {
		commit()
		return nil
	}
	m.graphAccess.Lock()
	// A NON-NIL copy even when members is empty: the proposal distinguishes "this group publishes
	// nothing" from "this group is not part of the proposal", and a nil slice would read as the
	// second and validate the OLD membership.
	proposed := make([]string, len(members))
	copy(proposed, members)
	err := m.validateGraphLocked(graphProposal{
		members: map[string][]string{groupTag: proposed},
	})
	if err != nil {
		m.graphAccess.Unlock()
		return err
	}
	commit()
	m.graphAccess.Unlock()
	// A group's membership IS the reference edge set for everything it names, so a committed
	// membership is a graph change even when its selection stayed the same byte for byte - a new
	// member that is not selected yet is still referenced, and a dropped member is not. Emitted
	// after the graph lock is released so the consumer may read this manager (and the group) back.
	m.notifyGraphUpdated()
	return nil
}

// validateGraphLocked validates the OUTBOUND graph with the proposal applied.
//
// It runs under m.graphAccess (so no other publication can interleave) and must be called with that
// lock held. It takes m.access only to SNAPSHOT the node table: the walk itself reads objects and
// group memberships, and a validator that held the manager lock while calling into user-configured
// groups would let a group callback that waits for a publication block the whole namespace.
func (m *Manager) validateGraphLocked(proposal graphProposal) error {
	nodes, transports, defaultResolver := m.graphSnapshot()
	for tag, object := range proposal.replace {
		if object == nil {
			delete(nodes, tag)
			continue
		}
		nodes[tag] = object
	}
	err := validateAcyclicOutboundGraph(nodes, proposal.members)
	if err != nil {
		return err
	}
	if len(transports) == 0 {
		return nil
	}
	// The DNS half of the graph, with the SAME nodes and the same membership overrides: a cycle that
	// runs DNS transport -> outbound -> DNS transport is not visible to the outbound-only walk, and
	// it must be refused before a published node makes it reachable.
	graph := &crossKindGraph{
		outbounds:            nodes,
		transports:           transports,
		defaultResolver:      defaultResolver,
		readsDefaultResolver: m.readsDefaultResolverRef(),
		memberOverrides:      proposal.members,
	}
	return graph.validate()
}

// graphSnapshot copies the manager's node table and the DNS transports it was given at start.
//
// The tag lookup follows the runtime rule - outbound namespace first, endpoint namespace second -
// because a tag that resolves to two objects would otherwise make the validator describe a graph the
// process does not execute. The collision itself is refused earlier (box.New and lintOutbounds).
func (m *Manager) graphSnapshot() (map[string]adapter.Outbound, map[string]adapter.DNSTransport, func() string) {
	m.access.RLock()
	nodes := make(map[string]adapter.Outbound, len(m.outbounds)+4)
	for _, outbound := range m.outbounds {
		nodes[outbound.Tag()] = outbound
	}
	transports := make(map[string]adapter.DNSTransport, len(m.dnsTransports))
	for _, transport := range m.dnsTransports {
		transports[transport.Tag()] = transport
	}
	defaultResolver := m.defaultDomainResolver
	m.access.RUnlock()
	if m.endpoint != nil {
		for _, endpoint := range m.endpoint.Endpoints() {
			if _, shadowed := nodes[endpoint.Tag()]; !shadowed {
				nodes[endpoint.Tag()] = endpoint
			}
		}
	}
	return nodes, transports, defaultResolver
}

// validateAcyclicOutboundGraph refuses the first cycle in the outbound namespace.
//
// Membership overrides replace the LIVE member list of the named group, because the caller is asking
// about a graph that does not exist yet. Declared dependencies are always included: they are the
// edges the start-order sort validated and the object will dial through whatever its group lists.
func validateAcyclicOutboundGraph(nodes map[string]adapter.Outbound, overrides map[string][]string) error {
	if len(nodes) == 0 {
		return nil
	}
	state := make(map[string]uint8, len(nodes))
	var path []string
	var visit func(tag string) error
	visit = func(tag string) error {
		switch state[tag] {
		case graphVisited:
			return nil
		case graphVisiting:
			// Report the loop from the node it closes on, not everything walked before it: an
			// operator looking for "the edge from proxy to bootstrap" that does not exist is
			// worse off than one shown the exact cycle.
			start := 0
			for index, element := range path {
				if element == tag {
					start = index
					break
				}
			}
			loop := append(append([]string(nil), path[start:]...), tag)
			return E.New("circular outbound dependency: ", strings.Join(loop, " -> "))
		}
		state[tag] = graphVisiting
		path = append(path, tag)
		for _, dependency := range outboundGraphEdges(nodes[tag], overrides[tag]) {
			if _, known := nodes[dependency]; !known {
				// A tag that names nothing is the start-order sort's message ("dependency[tag] not
				// found"), and repeating it here as a cycle would send the operator looking for a
				// loop that is not there.
				continue
			}
			if err := visit(dependency); err != nil {
				return err
			}
		}
		path = path[:len(path)-1]
		state[tag] = graphVisited
		return nil
	}
	for _, tag := range sortedTags(nodes) {
		if err := visit(tag); err != nil {
			return err
		}
	}
	return nil
}

const (
	graphUnvisited uint8 = iota
	graphVisiting
	graphVisited
)

// outboundGraphEdges reports every tag one node can hand the flow to: the declared dependencies, the
// group membership, and the references a Referrer publishes.
//
// `override` is non-nil only for the group whose prospective membership is being validated. In that
// case the override REPLACES both All() and References(): the selection that comes with it is a
// member of the new list, and keeping the old selection as an edge would validate a graph the commit
// is about to make impossible - the stale edge would be a false positive.
func outboundGraphEdges(object adapter.Outbound, override []string) []string {
	if object == nil {
		return nil
	}
	edges := make([]string, 0, 4)
	edges = append(edges, object.Dependencies()...)
	if override != nil {
		edges = append(edges, override...)
	} else {
		if group, isGroup := object.(adapter.OutboundGroup); isGroup {
			edges = append(edges, group.All()...)
		}
		if referrer, isReferrer := object.(adapter.Referrer); isReferrer {
			edges = append(edges, referrer.References()...)
		}
	}
	return dedupeTags(edges)
}

func dedupeTags(tags []string) []string {
	if len(tags) < 2 {
		return tags
	}
	unique := tags[:0]
	seen := make(map[string]bool, len(tags))
	for _, tag := range tags {
		if tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		unique = append(unique, tag)
	}
	return unique
}

func sortedTags(nodes map[string]adapter.Outbound) []string {
	tags := make([]string, 0, len(nodes))
	for tag := range nodes {
		tags = append(tags, tag)
	}
	// Sorted starts, so the reported cycle is reproducible. Map iteration order would make the same
	// configuration name a different entry point on each run.
	slices.Sort(tags)
	return tags
}
