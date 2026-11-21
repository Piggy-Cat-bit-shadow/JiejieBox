package adapter

import (
	"os"
	"slices"
	"sync"

	E "github.com/sagernet/sing/common/exceptions"
)

// LifecycleGroup owns components that can be replaced while their parent is
// running. Each component has a separate scope; removed scopes are released
// immediately instead of accumulating in the parent's cleanup list.
type LifecycleGroup struct {
	closed bool
	access sync.Mutex
	parent *Scope
	scopes map[Lifecycle]*Scope
	order  []Lifecycle
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
		g.order = append(g.order, component)
	}
	return scope.Start(name, component, stage)
}

func (g *LifecycleGroup) Remove(component Lifecycle) error {
	g.access.Lock()
	scope := g.scopes[component]
	if scope == nil {
		g.access.Unlock()
		return nil
	}
	delete(g.scopes, component)
	for i, item := range g.order {
		if item == component {
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
	return scope.Close()
}

func (g *LifecycleGroup) Close() error {
	g.access.Lock()
	defer g.access.Unlock()
	g.closed = true
	var err error
	for i := len(g.order) - 1; i >= 0; i-- {
		err = E.Errors(err, g.scopes[g.order[i]].Close())
	}
	g.scopes = nil
	g.order = nil
	return err
}
