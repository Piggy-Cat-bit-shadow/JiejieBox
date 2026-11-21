package adapter

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"slices"
	"sync"
	"testing"

	"github.com/sagernet/sing-box/log"
	E "github.com/sagernet/sing/common/exceptions"
)

// The heap floor of the Scope chain, measured for both ownership shapes.
//
// # What this measures
//
// B1 of the work order asks for one thing before any code moves: how many bytes per component do the
// TWO Scope layers actually cost, separated from the component, the group index, and the parent? So
// this file holds two group shapes with IDENTICAL public semantics:
//
//   - nested: the pre-B1 shape, a Scope per component plus a nested child Scope created by
//     Scope.Start (copied verbatim below from lifecycle_group.go at the baseline commit, so both
//     shapes can be measured in one process on the same workload);
//   - owned: the converged shape, where LifecycleGroup.Start hands the component the Scope the group
//     already owns for it.
//
// Both are driven through the same interface and the same component double, so the difference is the
// second layer and nothing else. The behavioural equivalence of the two shapes is asserted separately
// in lifecycle_group_convergence_test.go.
//
// # How it measures
//
// Every sample is a live-heap delta around one cycle: settle (two forced GCs), read MemStats, build
// and start the workload, keep it reachable, settle again, read MemStats, then KeepAlive. That is Live
// Heap after a forced GC, not RSS and not TotalAlloc - TotalAlloc is reported separately because the
// allocation VOLUME of the two shapes differs even where the retained set does not.
//
// The fixed cost (parent Scope + group + the component slice) is measured in the same process at
// n=0, five times, and the MEDIAN is subtracted, so the reported per-component number is marginal.
// Each cell is run cyclesPerFloorCell times, and the whole binary is meant to be run several times as
// separate processes; the report takes the median and the range over the process medians.
//
// # Why it is gated
//
// A memory sample must not share a machine (or a heap) with the rest of the suite: the numbers are
// only comparable when nothing else is allocating. Set L_SCOPE_FLOOR=1 to run it.

const cyclesPerFloorCell = 5

// floorComponent is the component double. It registers exactly one cleanup in the first Start stage
// and does nothing else, so the measured cost is the ownership scaffolding rather than the component.
// The cleanup closure captures nothing and is therefore a static function value in both shapes.
type floorComponent struct {
	name string
}

func (c *floorComponent) Start(stage StartStage, scope *Scope) error {
	if stage == StartStateStart {
		scope.Add(func() error { return nil })
	}
	return nil
}

// floorGroup is what the two shapes have in common: the public surface the Manager uses.
type floorGroup interface {
	Start(name string, component Lifecycle, stage StartStage) error
	Remove(component Lifecycle) error
	Close() error
}

// nestedLifecycleGroup is the pre-B1 two-layer group. It is a verbatim copy of
// lifecycle_group.go@1d5bca3 Start/Remove/Close - including the property the last fix added, that
// Remove closes the component scope OUTSIDE the membership lock - kept ONLY as the control arm of the
// measurement and the behaviour comparison. Production code no longer contains this shape.
type nestedLifecycleGroup struct {
	closed bool
	access sync.Mutex
	parent *Scope
	scopes map[Lifecycle]*Scope
	order  []Lifecycle
}

func newNestedLifecycleGroup(parent *Scope) *nestedLifecycleGroup {
	g := &nestedLifecycleGroup{parent: parent, scopes: make(map[Lifecycle]*Scope)}
	parent.Add(g.Close)
	return g
}

func (g *nestedLifecycleGroup) Start(name string, component Lifecycle, stage StartStage) error {
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

func (g *nestedLifecycleGroup) Remove(component Lifecycle) error {
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
	return scope.Close()
}

func (g *nestedLifecycleGroup) Close() error {
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

// floorShape is one arm of the comparison: how a group is built on a parent Scope.
type floorShape struct {
	name  string
	build func(parent *Scope) floorGroup
}

var floorShapes = []floorShape{
	{name: "nested-2-layer", build: func(parent *Scope) floorGroup { return newNestedLifecycleGroup(parent) }},
	{name: "owned-1-layer", build: func(parent *Scope) floorGroup { return NewLifecycleGroup(parent) }},
}

// heapSample is one live-heap reading.
type heapSample struct {
	live      uint64
	objects   uint64
	allocated uint64
	goroutine int
}

func readHeap() heapSample {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return heapSample{
		live:      stats.HeapAlloc,
		objects:   stats.HeapObjects,
		allocated: stats.TotalAlloc,
		goroutine: runtime.NumGoroutine(),
	}
}

// sub returns after-before. TotalAlloc is monotonic, HeapAlloc/HeapObjects are live values, so the
// subtraction is the delta of the window in all three cases.
func (after heapSample) sub(before heapSample) heapSample {
	return heapSample{
		live:      after.live - before.live,
		objects:   after.objects - before.objects,
		allocated: after.allocated - before.allocated,
		goroutine: after.goroutine - before.goroutine,
	}
}

func settleHeap() {
	runtime.GC()
	runtime.GC()
}

// floorWorkload is everything a cycle holds live. It is a struct, not a []any, so the holder itself
// costs one allocation in both shapes.
type floorWorkload struct {
	parent     *Scope
	group      floorGroup
	components []*floorComponent
}

// buildFloorWorkload builds and starts n components through one group. Every component runs one Start
// stage: the shape of the ownership chain does not depend on how many stages a real component is
// started through, and one stage keeps a 10000-component sample affordable.
func buildFloorWorkload(shape floorShape, n int) *floorWorkload {
	parent := NewScope(context.Background(), log.NewNOPFactory().Logger())
	workload := &floorWorkload{
		parent:     parent,
		group:      shape.build(parent),
		components: make([]*floorComponent, n),
	}
	for index := range n {
		component := &floorComponent{name: fmt.Sprintf("node-%d", index)}
		workload.components[index] = component
		if err := workload.group.Start(component.name, component, StartStateStart); err != nil {
			panic(err)
		}
	}
	return workload
}

// measureFloor runs one cycle and returns the live-heap delta it produced.
func measureFloor(shape floorShape, n int) heapSample {
	settleHeap()
	before := readHeap()
	workload := buildFloorWorkload(shape, n)
	settleHeap()
	after := readHeap()
	sample := after.sub(before)
	runtime.KeepAlive(workload)
	runtime.KeepAlive(workload.components)
	return sample
}

// medianSample returns the per-field median of the samples. It is used for the n=0 baseline, where
// the whole signal is a couple of kilobytes and one noisy GC would otherwise dominate the subtraction.
func medianSample(samples []heapSample) heapSample {
	if len(samples) == 0 {
		return heapSample{}
	}
	lives := make([]uint64, len(samples))
	objects := make([]uint64, len(samples))
	goroutines := make([]int, len(samples))
	for index, sample := range samples {
		lives[index] = sample.live
		objects[index] = sample.objects
		goroutines[index] = sample.goroutine
	}
	slices.Sort(lives)
	slices.Sort(objects)
	slices.Sort(goroutines)
	middle := len(samples) / 2
	return heapSample{
		live:      lives[middle],
		objects:   objects[middle],
		goroutine: goroutines[middle],
	}
}

// TestLifecycleGroupScopeFloor is the measurement. It prints one machine-readable line per cell; the
// report aggregates them across processes.
func TestLifecycleGroupScopeFloor(t *testing.T) {
	if os.Getenv("L_SCOPE_FLOOR") != "1" {
		t.Skip("set L_SCOPE_FLOOR=1 to run the Scope floor measurement (serial; not part of the unit suite)")
	}
	sizes := []int{0, 100, 1000, 10000}
	for _, shape := range floorShapes {
		baselines := make([]heapSample, cyclesPerFloorCell)
		for cycle := range baselines {
			baselines[cycle] = measureFloor(shape, 0)
		}
		baseline := medianSample(baselines)
		fmt.Printf("L-SCOPE floor shape=%s n=0 live=%d objects=%d alloc=0 per_component=0 objects_per_component=0\n",
			shape.name, baseline.live, baseline.objects)
		for _, n := range sizes[1:] {
			for cycle := range cyclesPerFloorCell {
				sample := measureFloor(shape, n)
				marginal := sample.sub(baseline)
				fmt.Printf("L-SCOPE floor shape=%s n=%d run=%d live=%d objects=%d alloc=%d per_component=%.1f objects_per_component=%.3f goroutines=%d\n",
					shape.name, n, cycle, marginal.live, marginal.objects, sample.allocated,
					float64(marginal.live)/float64(n), float64(marginal.objects)/float64(n), marginal.goroutine)
			}
		}
	}
	measureFloorParts(t)
}

// measureFloorParts decomposes the number: the pieces the two shapes are built from, measured under
// the same protocol, so the per-component difference can be checked against its parts instead of
// being asserted.
func measureFloorParts(t *testing.T) {
	parent := NewScope(context.Background(), log.NewNOPFactory().Logger())
	defer parent.Close()

	measure := func(label string, build func(n int) []any) {
		for _, n := range []int{100, 1000, 10000} {
			settleHeap()
			before := readHeap()
			held := build(n)
			settleHeap()
			after := readHeap()
			sample := after.sub(before)
			fmt.Printf("L-SCOPE part %s n=%d live=%d objects=%d per_item=%.1f objects_per_item=%.3f\n",
				label, n, sample.live, sample.objects,
				float64(sample.live)/float64(n), float64(sample.objects)/float64(n))
			runtime.KeepAlive(held)
		}
	}

	// One bare Scope: the struct, its cancel context, and its registration in the parent's cancelCtx.
	measure("scope-only", func(n int) []any {
		scopes := make([]*Scope, n)
		for index := range n {
			scopes[index] = NewScope(parent.Context(), nil)
		}
		return []any{scopes}
	})
	// One cancel context, without a Scope struct.
	measure("cancel-context-only", func(n int) []any {
		cancels := make([]context.CancelFunc, n)
		for index := range n {
			_, cancels[index] = context.WithCancel(parent.Context())
		}
		return []any{cancels}
	})
	// One empty children map, the allocation NewScope used to make unconditionally.
	measure("empty-children-map", func(n int) []any {
		maps := make([]map[Lifecycle]*Scope, n)
		for index := range n {
			maps[index] = make(map[Lifecycle]*Scope)
		}
		return []any{maps}
	})
	// The group index alone: what both shapes pay for membership.
	measure("group-index-entry", func(n int) []any {
		index := make(map[Lifecycle]*Scope, n)
		keys := make([]*floorComponent, n)
		for number := range n {
			key := &floorComponent{name: "index"}
			keys[number] = key
			index[key] = nil
		}
		return []any{index, keys}
	})
	// The component double plus its slot in the group's order slice: the part that is NOT Scope
	// scaffolding and is identical in both shapes.
	measure("component-only", func(n int) []any {
		components := make([]*floorComponent, n)
		for index := range n {
			components[index] = &floorComponent{name: fmt.Sprintf("node-%d", index)}
		}
		return []any{components}
	})
	// The nested layer measured on its own, through the public Scope.Start: one parent Scope, n
	// children. This is the independent cross-check of the shape difference - it should equal
	// nested-2-layer minus owned-1-layer (plus the 8 bytes of the two order-slice entry widths), and
	// it is the upper bound of what removing the layer can return.
	measure("nested-child-layer-via-Start", func(n int) []any {
		childParent := NewScope(context.Background(), nil)
		components := make([]*floorComponent, n)
		for index := range n {
			component := &floorComponent{name: fmt.Sprintf("node-%d", index)}
			components[index] = component
			if err := childParent.Start(component.name, component, StartStateStart); err != nil {
				panic(err)
			}
		}
		return []any{childParent, components}
	})
	// A children map holding exactly ONE entry. This is the shape the nested layer forces on every
	// component's outer Scope: a map allocated for a single child, plus its bucket. One parent shared
	// by n children hides this cost, which is why it is measured separately.
	measure("children-map-one-entry", func(n int) []any {
		maps := make([]map[Lifecycle]*Scope, n)
		keys := make([]*floorComponent, n)
		for index := range n {
			key := &floorComponent{name: "child"}
			keys[index] = key
			single := make(map[Lifecycle]*Scope)
			single[key] = nil
			maps[index] = single
		}
		return []any{maps, keys}
	})
	// One intermediate cancel context per item, each with a child: this is the context-side link the
	// nested layer adds (S2 registered in S1's cancelCtx children map), and it allocates that map plus
	// a bucket per component.
	measure("cancel-context-with-child-map", func(n int) []any {
		cancels := make([]context.CancelFunc, n*2)
		for index := range n {
			intermediate, cancelIntermediate := context.WithCancel(parent.Context())
			_, cancelChild := context.WithCancel(intermediate)
			cancels[index*2] = cancelIntermediate
			cancels[index*2+1] = cancelChild
		}
		return []any{cancels}
	})
}
