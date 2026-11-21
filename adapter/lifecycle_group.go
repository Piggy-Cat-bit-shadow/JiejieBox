package adapter

import (
	"os"
	"slices"
	"sync"

	E "github.com/sagernet/sing/common/exceptions"
)

// groupMember is one component registered in a LifecycleGroup, kept in reverse-start close order.
//
// The name is stored with the component because the group now closes the component's Scope ITSELF
// (see Start), so the "close <name>" trace and the per-component close monitor have to be driven from
// here. The alternative - keeping the name inside a parent-side close cleanup - is exactly the extra
// nesting this convergence removes.
type groupMember struct {
	component Lifecycle
	name      string
}

// LifecycleGroup owns components that can be replaced while their parent is
// running. Each component has a separate scope; removed scopes are released
// immediately instead of accumulating in the parent's cleanup list.
//
// # One Scope per component, not two
//
// The group used to build a Scope per component and then call Scope.Start on it, which built a SECOND
// Scope for the same component and handed that one to the component. Both scopes existed for the
// whole life of the component, each with its own cancel context, children map and cleanup queue, and
// the only thing the outer one did was close the inner one from a cleanup. The component's lifetime
// was already independent at the outer level (that is the group's entire reason to exist - Remove),
// so the inner layer was pure duplication: MEASURED in adapter/lifecycle_group_floor_test.go.
//
// The collapsed shape keeps every property the component can observe:
//
//   - a component still gets its OWN Scope, so Remove still tears down exactly one component and the
//     parent's cleanup list still does not accumulate removed ones;
//   - close order is still reverse registration order (Close walks `order` backwards);
//   - a component is still started with the same monitor, elapsed trace and error wrapping, because
//     all of that lives in startOwned and closeComponentScope, not in the second Scope;
//   - a component still observes cancellation of the group and of its own removal through the Scope
//     context it is handed, and a Start still cannot return success for a Scope that closed under it.
//
// What the component can no longer do is treat the Scope it was handed as a CHILD it may close from
// one of its own cleanups. Nothing in this tree does that (a component's own nested scopes are created
// by the component, and closing those from a cleanup is the supported direction), and it is the rule
// the lifecycle module already states: never register a cleanup that closes the Scope it runs in.
type LifecycleGroup struct {
	closed bool
	access sync.Mutex
	parent *Scope
	scopes map[Lifecycle]*Scope
	order  []groupMember
}

func NewLifecycleGroup(parent *Scope) *LifecycleGroup {
	g := &LifecycleGroup{parent: parent, scopes: make(map[Lifecycle]*Scope)}
	parent.Add(g.Close)
	return g
}

func (g *LifecycleGroup) Start(name string, component Lifecycle, stage StartStage) error {
	g.access.Lock()
	defer g.access.Unlock()
	if g.closed {
		return os.ErrClosed
	}
	if err := g.parent.Context().Err(); err != nil {
		return err
	}
	scope := g.scopes[component]
	if scope == nil {
		scope = NewScope(g.parent.Context(), g.parent.logger)
		g.scopes[component] = scope
		g.order = append(g.order, groupMember{component: component, name: name})
	}
	// The group's Scope for this component IS the component's Scope: no nested child is created, and
	// a re-entered stage reuses the same one exactly as before.
	return scope.startOwned(name, component, stage)
}

func (g *LifecycleGroup) Remove(component Lifecycle) error {
	g.access.Lock()
	scope := g.scopes[component]
	if scope == nil {
		g.access.Unlock()
		return nil
	}
	delete(g.scopes, component)
	var name string
	for i, item := range g.order {
		if item.component == component {
			name = item.name
			g.order = slices.Delete(g.order, i, i+1)
			break
		}
	}
	g.access.Unlock()
	// Close OUTSIDE the membership lock: this lock is taken by every Start and every Remove in the
	// group, and Close runs the component's cleanups, which can park for as long as the component's
	// teardown takes. Holding it across that drain made one slow node's teardown a global barrier:
	// MEASURED (adapter/outbound, parked cleanup) a hand-off lease release for an UNRELATED object
	// blocked in lifecycle.Remove waiting for this lock, so a dial completing during another node's
	// teardown could not finish. The component is already out of the maps when the lock is dropped,
	// so a concurrent Start for it would create a new scope rather than resume this one; the manager
	// serialises Start and Remove for the same component itself (mutation, and createAccess for the
	// late-publication path), so that resurrection is not reachable.
	//
	// The close goes through the same helper group.Close uses, because the removed component's
	// teardown used to be reported by the nested child's close cleanup: that cleanup is gone with the
	// nested scope, and the trace, the monitor and the "close <name>" error context have to follow
	// the ownership rather than disappear with it.
	return closeComponentScope(g.parent.logger, name, scope)
}

func (g *LifecycleGroup) Close() error {
	g.access.Lock()
	defer g.access.Unlock()
	g.closed = true
	var err error
	for i := len(g.order) - 1; i >= 0; i-- {
		member := g.order[i]
		// The per-component close keeps the trace, the close monitor and the "close <name>" error
		// context that the nested child's close cleanup used to provide; only the nested Scope is
		// gone.
		err = E.Errors(err, closeComponentScope(g.parent.logger, member.name, g.scopes[member.component]))
	}
	g.scopes = nil
	g.order = nil
	return err
}
