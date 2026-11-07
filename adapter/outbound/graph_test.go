package outbound

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// Tests for the runtime graph validation in graph.go.
//
// # Why these are not dial tests
//
// The defect is a graph that starts successfully and recurses FOREVER on the first dial. Exercising
// it with a real dial would risk an unbounded recursion (or a stack exhaustion) in the test process,
// so every test here asserts the START-TIME decision on the graph instead: which publication is
// refused, and what the previous state still is. The Box-level acceptance tests in the root package
// assert the same verdict through `box.New` + `Start`, which is the path a user runs.

// graphNodeOptions is what graphGraphRegistry accepts.
type graphNodeOptions struct {
	Dependencies []string
}

// graphNode is a leaf with declared dependencies that records its own close.
type graphNode struct {
	tag          string
	dependencies []string
	closed       atomic.Int32
}

func (n *graphNode) Type() string           { return "test" }
func (n *graphNode) Tag() string            { return n.tag }
func (n *graphNode) Network() []string      { return []string{"tcp", "udp"} }
func (n *graphNode) Dependencies() []string { return n.dependencies }
func (n *graphNode) Close() error           { n.closed.Add(1); return nil }
func (n *graphNode) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return nil, errors.New("the graph tests must never dial")
}

func (n *graphNode) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("the graph tests must never dial")
}

// graphGroup is a provider-fed group: its declared dependencies are empty (it names providers
// instead), while All() is the membership it publishes. That split is the whole shape of the defect.
type graphGroup struct {
	tag    string
	access sync.RWMutex
	// members is what All() reports.
	members []string
	// selected is what References() reports, when non-empty.
	selected string
}

func (g *graphGroup) Type() string           { return "selector" }
func (g *graphGroup) Tag() string            { return g.tag }
func (g *graphGroup) Network() []string      { return []string{"tcp", "udp"} }
func (g *graphGroup) Dependencies() []string { return nil }

func (g *graphGroup) All() []string {
	g.access.RLock()
	defer g.access.RUnlock()
	return append([]string(nil), g.members...)
}

func (g *graphGroup) References() []string {
	g.access.RLock()
	defer g.access.RUnlock()
	if g.selected == "" {
		return nil
	}
	return []string{g.selected}
}

func (g *graphGroup) Selected(string) adapter.Outbound { return nil }

func (g *graphGroup) AttachConnection(io.Closer) func() { return func() {} }

func (g *graphGroup) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return nil, errors.New("the graph tests must never dial")
}

func (g *graphGroup) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("the graph tests must never dial")
}

func (g *graphGroup) setMembers(members []string, selected string) {
	g.access.Lock()
	g.members = append([]string(nil), members...)
	g.selected = selected
	g.access.Unlock()
}

type graphRegistry struct{}

func (r *graphRegistry) CreateOptions(string) (any, bool) { return &graphNodeOptions{}, true }
func (r *graphRegistry) OptionTypes() []string            { return []string{"test"} }

func (r *graphRegistry) CreateOutbound(_ context.Context, _ adapter.Router, _ log.ContextLogger, tag string, _ string, options any) (adapter.Outbound, error) {
	var dependencies []string
	if value, isValue := options.(*graphNodeOptions); isValue {
		dependencies = value.Dependencies
	}
	return &graphNode{tag: tag, dependencies: dependencies}, nil
}

func newGraphManager(t *testing.T) *Manager {
	t.Helper()
	manager := NewManager(&graphRegistry{}, nil, "")
	manager.Initialize(func() (adapter.Outbound, error) {
		return &graphNode{tag: "direct"}, nil
	})
	return manager
}

func replaceNode(t *testing.T, manager *Manager, tag string, dependencies ...string) error {
	t.Helper()
	return manager.Replace(context.Background(), nil, log.NewNOPFactory().NewLogger("test"),
		tag, "test", &graphNodeOptions{Dependencies: dependencies})
}

// TestReplaceRefusesANodeThatClosesACycleThroughPublishedMembership is the dynamic-cycle defect at
// its publication point.
//
// The group is fed by the provider and has already published `p/a`; the provider then installs a
// replacement for `p/a` that dials through that group. Accepting it makes every dial recurse
// (`p/a` -> `g` -> `p/a`), and the start-order sort cannot see it because the group declares no
// member and `p/a` alone is acyclic.
func TestReplaceRefusesANodeThatClosesACycleThroughPublishedMembership(t *testing.T) {
	manager := newGraphManager(t)
	group := &graphGroup{tag: "g"}
	group.setMembers([]string{"p/a"}, "p/a")
	installOutbound(t, manager, group)

	err := replaceNode(t, manager, "p/a", "g")
	require.Error(t, err, "the replacement would close g -> p/a -> g")
	require.Contains(t, err.Error(), "circular")
	_, published := manager.Outbound("p/a")
	require.False(t, published, "a refused replacement must not be published")
}

// TestReplaceKeepsThePreviousNodeWhenTheReplacementIsRefused pins prepare-then-swap for the graph
// verdict: the node that was serving stays published and is not closed.
func TestReplaceKeepsThePreviousNodeWhenTheReplacementIsRefused(t *testing.T) {
	manager := newGraphManager(t)
	old := &graphNode{tag: "p/a"}
	installOutbound(t, manager, old)
	group := &graphGroup{tag: "g"}
	group.setMembers([]string{"p/a"}, "p/a")
	installOutbound(t, manager, group)

	err := replaceNode(t, manager, "p/a", "g")
	require.ErrorContains(t, err, "circular")

	current, published := manager.Outbound("p/a")
	require.True(t, published)
	require.Same(t, old, current, "the previous node must remain the published one")
	require.Zero(t, old.closed.Load(), "the previous node must not be closed by a refused replacement")
}

// TestUpdateGroupMembershipRefusesAndKeepsThePreviousMembership is the group-publication half: the
// manager must not run the commit when the membership would close a cycle, so the caller keeps the
// members and the selection it had.
func TestUpdateGroupMembershipRefusesAndKeepsThePreviousMembership(t *testing.T) {
	manager := newGraphManager(t)
	group := &graphGroup{tag: "g"}
	group.setMembers(nil, "")
	installOutbound(t, manager, group)
	installOutbound(t, manager, &graphNode{tag: "p/a", dependencies: []string{"g"}})

	committed := false
	err := manager.UpdateGroupMembership("g", []string{"p/a"}, func() {
		committed = true
	})
	require.ErrorContains(t, err, "circular")
	require.False(t, committed, "the commit must not run when the graph is refused")
	require.Empty(t, group.All(), "the previous membership must be untouched")
}

// TestRemoveRefusesAProviderNodeAGroupStillNames covers the closure window: a provider retires a
// node the selector has already published as a member. Removing it would close an object the dial
// path can still reach, so it must be refused while the membership names it.
func TestRemoveRefusesAProviderNodeAGroupStillNames(t *testing.T) {
	manager := newGraphManager(t)
	node := &graphNode{tag: "p/a"}
	installOutbound(t, manager, node)
	group := &graphGroup{tag: "g"}
	// NO selection is declared, so only the published MEMBERSHIP names the tag. That is the shape a
	// provider update creates: the node is a member before (or without) being the current choice.
	group.setMembers([]string{"p/a"}, "")
	installOutbound(t, manager, group)

	err := manager.Remove("p/a")
	require.ErrorContains(t, err, "is depended by g")
	require.True(t, node.closed.Load() == 0, "a refused removal must not close the node")
	_, published := manager.Outbound("p/a")
	require.True(t, published, "a refused removal must leave the node addressable for its dependent")

	// Once the group drops the member, the same removal succeeds and releases the object.
	group.setMembers(nil, "")
	require.NoError(t, manager.Remove("p/a"))
	require.EqualValues(t, 1, node.closed.Load())
	_, published = manager.Outbound("p/a")
	require.False(t, published)
}

// TestRemoveRefusesADNSTransportDetour is the cross-namespace half of the dependent guard: a DNS
// transport's detour names the outbound, and the transport is not in this manager's list.
func TestRemoveRefusesADNSTransportDetour(t *testing.T) {
	manager := newGraphManager(t)
	node := &graphNode{tag: "proxy"}
	installOutbound(t, manager, node)
	transport := &crossKindTestTransport{tag: "remote", detoursTo: []string{"proxy"}}
	require.NoError(t, manager.ValidateCrossKindCycles([]adapter.DNSTransport{transport}))

	err := manager.Remove("proxy")
	require.ErrorContains(t, err, "dns/remote")
	require.Zero(t, node.closed.Load())
}

// TestReleaseUnlinksDespiteGroupMembership is the teardown verb: providers are closed before the
// groups that name their nodes, so the normal close path must be able to unlink.
func TestReleaseUnlinksDespiteGroupMembership(t *testing.T) {
	manager := newGraphManager(t)
	node := &graphNode{tag: "p/a"}
	installOutbound(t, manager, node)
	group := &graphGroup{tag: "g"}
	group.setMembers([]string{"p/a"}, "p/a")
	installOutbound(t, manager, group)

	require.NoError(t, manager.Release("p/a"))
	require.EqualValues(t, 1, node.closed.Load())
	_, published := manager.Outbound("p/a")
	require.False(t, published)
}

// TestLatePublishedNodeCannotCloseACrossKindCycle is the DNS half of the dynamic case.
//
// The transports were validated at start, when the provider had published nothing. When the group
// later publishes the provider node - and that node resolves its server through the transport that
// detours into the group - the closing edge must be refused at publication time.
func TestLatePublishedNodeCannotCloseACrossKindCycle(t *testing.T) {
	manager := newGraphManager(t)
	group := &graphGroup{tag: "g"}
	group.setMembers(nil, "")
	installOutbound(t, manager, group)
	installOutbound(t, manager, &crossKindTestOutbound{tag: "p/a", resolver: "remote", kind: "socks"})
	require.NoError(t, manager.ValidateCrossKindCycles([]adapter.DNSTransport{
		&crossKindTestTransport{tag: "remote", detoursTo: []string{"g"}},
	}))

	committed := false
	err := manager.UpdateGroupMembership("g", []string{"p/a"}, func() { committed = true })
	require.ErrorContains(t, err, "circular dependency between DNS server and outbound")
	require.False(t, committed)
}

// TestCrossKindValidationReadsPublishedMembership pins the start-time extension: the same cycle is
// refused by ValidateCrossKindCycles when the membership already exists when it runs.
func TestCrossKindValidationReadsPublishedMembership(t *testing.T) {
	manager := newGraphManager(t)
	group := &graphGroup{tag: "g"}
	group.setMembers([]string{"p/a"}, "p/a")
	installOutbound(t, manager, group)
	installOutbound(t, manager, &crossKindTestOutbound{tag: "p/a", resolver: "remote", kind: "socks"})

	err := manager.ValidateCrossKindCycles([]adapter.DNSTransport{
		&crossKindTestTransport{tag: "remote", detoursTo: []string{"g"}},
	})
	require.ErrorContains(t, err, "circular dependency between DNS server and outbound")
}

// TestOutboundsSnapshotsSurviveConcurrentMutations is the race regression.
//
// `Outbounds()` returns the published slice header, and consumers such as
// route.ReferenceManager's retire walk iterate it AFTER releasing the manager lock. Mutating the
// backing array in place (an element swap on Replace, an append-shift on Remove) is a data race with
// that walk; `-race` caught it during provider teardown in the Box tests. Every mutation now
// publishes a new slice, so a snapshot stays immutable for as long as a reader holds it.
func TestOutboundsSnapshotsSurviveConcurrentMutations(t *testing.T) {
	manager := newGraphManager(t)
	installOutbound(t, manager, &graphNode{tag: "a"})
	installOutbound(t, manager, &graphNode{tag: "b"})

	stop := make(chan struct{})
	var waitGroup sync.WaitGroup
	waitGroup.Add(1)
	go func() {
		defer waitGroup.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			snapshot := manager.Outbounds()
			for _, object := range snapshot {
				_ = object.Tag()
			}
		}
	}()

	for i := 0; i < 50; i++ {
		require.NoError(t, replaceNode(t, manager, "a"))
		require.NoError(t, manager.Remove("b"))
		installOutbound(t, manager, &graphNode{tag: "b"})
	}
	close(stop)
	waitGroup.Wait()
}

// replaceOwnedNode publishes a node through the owner-aware replacement path.
func replaceOwnedNode(t *testing.T, manager *Manager, tag string, owner string, dependencies ...string) error {
	t.Helper()
	return manager.ReplaceOwned(context.Background(), nil, log.NewNOPFactory().NewLogger("test"),
		tag, "test", &graphNodeOptions{Dependencies: dependencies}, owner)
}

// TestRetiredObjectSurvivesACachedDirectDetourUntilTheReferenceGoes is the direct-detour ownership
// edge.
//
// A live object whose detour names the replaced tag may have resolved the previous object into a
// one-shot detour cache. Closing it at the publication barrier would make that holder fail on its
// next use, so the object is retained until the naming object is gone. The retention is released by
// the removal that deletes the last external reference.
func TestRetiredObjectSurvivesACachedDirectDetourUntilTheReferenceGoes(t *testing.T) {
	manager := newGraphManager(t)
	require.NoError(t, replaceOwnedNode(t, manager, "p/X", "p"))
	first, loaded := manager.Outbound("p/X")
	require.True(t, loaded)
	anchor := &graphNode{tag: "anchor", dependencies: []string{"p/X"}}
	installOutbound(t, manager, anchor)

	require.NoError(t, replaceOwnedNode(t, manager, "p/X", "p"))
	require.NoError(t, manager.CompletePublication("p", []string{"p/X"}))
	require.Zero(t, first.(*graphNode).closed.Load(),
		"a live direct detour may still resolve the retired object; it must stay open")

	require.NoError(t, manager.Remove("anchor"))
	require.EqualValues(t, 1, first.(*graphNode).closed.Load(),
		"removing the last external reference releases the retired object")
}

// TestRetiredObjectSurvivesADNSTransportReference is the cross-kind version: a DNS transport's
// detour names the replaced tag and has no removal API, so the retired object is retained for the
// lifetime of the manager (a documented conservative edge, see the module notes).
func TestRetiredObjectSurvivesADNSTransportReference(t *testing.T) {
	manager := newGraphManager(t)
	require.NoError(t, replaceOwnedNode(t, manager, "p/X", "p"))
	first, loaded := manager.Outbound("p/X")
	require.True(t, loaded)
	require.NoError(t, manager.ValidateCrossKindCycles([]adapter.DNSTransport{
		&crossKindTestTransport{tag: "remote", detoursTo: []string{"p/X"}},
	}))
	require.NoError(t, replaceOwnedNode(t, manager, "p/X", "p"))
	require.NoError(t, manager.CompletePublication("p", []string{"p/X"}))
	require.Zero(t, first.(*graphNode).closed.Load(),
		"a DNS transport may hold the retired object in its detour cache; it must stay open")
}

// TestConcurrentMembershipPublicationsCannotBothCloseACycle serialises the graph decision.
//
// Two groups each publish a member whose node detours into the OTHER group. Each publication alone
// is acyclic; together they form g1 -> a -> g2 -> b -> g1. If both validated against the other's
// OLD membership and then both published, both would pass and the runtime graph would recurse.
// Exactly one publication must be accepted, and the graph afterwards must still be acyclic.
func TestConcurrentMembershipPublicationsCannotBothCloseACycle(t *testing.T) {
	manager := newGraphManager(t)
	g1 := &graphGroup{tag: "g1"}
	g2 := &graphGroup{tag: "g2"}
	installOutbound(t, manager, g1)
	installOutbound(t, manager, g2)
	installOutbound(t, manager, &graphNode{tag: "a", dependencies: []string{"g2"}})
	installOutbound(t, manager, &graphNode{tag: "b", dependencies: []string{"g1"}})

	var (
		access    sync.Mutex
		commits   int
		refusals  int
		start     = make(chan struct{})
		waitGroup sync.WaitGroup
	)
	publish := func(group *graphGroup, members []string) {
		defer waitGroup.Done()
		<-start
		err := manager.UpdateGroupMembership(group.Tag(), members, func() {
			access.Lock()
			commits++
			access.Unlock()
			group.setMembers(members, members[0])
		})
		if err != nil {
			access.Lock()
			refusals++
			access.Unlock()
			return
		}
	}
	waitGroup.Add(2)
	go publish(g1, []string{"a"})
	go publish(g2, []string{"b"})
	close(start)
	waitGroup.Wait()

	access.Lock()
	defer access.Unlock()
	require.Equal(t, 1, commits, "exactly one of the two mutually-closing publications may commit")
	require.Equal(t, 1, refusals, "the other must be refused, not silently dropped")

	manager.graphAccess.Lock()
	graphErr := manager.validateGraphLocked(graphProposal{})
	manager.graphAccess.Unlock()
	require.NoError(t, graphErr, "the published graph must still be acyclic")
}
