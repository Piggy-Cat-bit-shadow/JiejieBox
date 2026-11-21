package adapter

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/log"
	E "github.com/sagernet/sing/common/exceptions"

	"github.com/stretchr/testify/require"
)

// Behavioural equivalence of the two ownership shapes, driven through the SAME suite.
//
// # Why the suite is shared
//
// lifecycle_group_floor_test.go measures what the nested layer costs. A saving is only worth taking
// if the shape that replaces it keeps every property the component and the Manager can observe, so
// this file runs one matrix against BOTH arms:
//
//   - nested-2-layer is newNestedLifecycleGroup, the pre-B1 shape kept as the control arm;
//   - owned-1-layer is the real LifecycleGroup after the convergence.
//
// Anything that differs between the arms is therefore a semantic delta, not a test artefact. The two
// known deltas - which Scope object carries the bounded close diagnostic, and how promptly
// cancellation reaches a component - are stated in the report; everything else is asserted identical.
//
// # Interleaving
//
// Concurrency cases are driven by barriers and one-shot channels, never by sleeps: a component can
// only reach its registration after the test releases it, and the test only releases it after the
// observation it needs. The one bounded wait is in the safe direction - it can fail a "did not
// return" claim only by waiting longer, never pass a "returned" one spuriously. No race detector is
// available in this environment (CGO_ENABLED=0), so the concurrency conclusions here come from this
// deterministic interleaving plus the lock-order audit in reports/L-scope.md, and are NOT race-tested.

// shapedLifecycle is a component double that records every Scope it was handed and every stage it was
// started with, and defers the body to a function so one double serves every case.
type shapedLifecycle struct {
	name string

	access  sync.Mutex
	scopes  []*Scope
	stages  []StartStage
	cleaned atomic.Int32
	body    func(component *shapedLifecycle, stage StartStage, scope *Scope) error
}

func (c *shapedLifecycle) Start(stage StartStage, scope *Scope) error {
	c.access.Lock()
	c.scopes = append(c.scopes, scope)
	c.stages = append(c.stages, stage)
	c.access.Unlock()
	if c.body != nil {
		return c.body(c, stage, scope)
	}
	return nil
}

func (c *shapedLifecycle) observedScopes() []*Scope {
	c.access.Lock()
	defer c.access.Unlock()
	return append([]*Scope(nil), c.scopes...)
}

func (c *shapedLifecycle) observedStages() []StartStage {
	c.access.Lock()
	defer c.access.Unlock()
	return append([]StartStage(nil), c.stages...)
}

// addCleanup registers one counted cleanup on a scope.
func (c *shapedLifecycle) addCleanup(scope *Scope) {
	scope.Add(func() error {
		c.cleaned.Add(1)
		return nil
	})
}

func newShapedGroup(t *testing.T, shape floorShape) (*Scope, floorGroup) {
	t.Helper()
	parent := NewScope(context.Background(), log.NewNOPFactory().Logger())
	t.Cleanup(func() { _ = parent.Close() })
	return parent, shape.build(parent)
}

// forEachShape runs one subtest per arm.
func forEachShape(t *testing.T, run func(t *testing.T, shape floorShape)) {
	t.Helper()
	for _, shape := range floorShapes {
		t.Run(shape.name, func(t *testing.T) { run(t, shape) })
	}
}

// awaitReturned reports whether done was closed inside the window. Used only for the negative
// direction ("this must NOT have returned yet"): a slow machine makes it less sensitive, never wrong.
func awaitReturned(done <-chan struct{}, window time.Duration) bool {
	select {
	case <-done:
		return true
	case <-time.After(window):
		return false
	}
}

// TestGroupShapeOneScopePerComponent pins the object chain the whole convergence is about: a component
// started through all four stages sees exactly ONE Scope, and two components never share it.
//
// This is the property that must hold in BOTH shapes. In the nested shape that one Scope is the nested
// child; in the converged shape it is the Scope the group owns. If a later change made the converged
// group hand out the shared parent Scope, or a new nested layer appear, this fails.
func TestGroupShapeOneScopePerComponent(t *testing.T) {
	forEachShape(t, func(t *testing.T, shape floorShape) {
		_, group := newShapedGroup(t, shape)
		first := &shapedLifecycle{name: "first"}
		second := &shapedLifecycle{name: "second"}
		for _, stage := range ListStartStages {
			require.NoError(t, group.Start(first.name, first, stage))
			require.NoError(t, group.Start(second.name, second, stage))
		}

		firstScopes := first.observedScopes()
		secondScopes := second.observedScopes()
		require.Len(t, firstScopes, len(ListStartStages))
		for _, scope := range firstScopes {
			require.Same(t, firstScopes[0], scope,
				"a component must keep one Scope across the stages: a second one would be a second "+
					"cancellation context and a second cleanup queue for one lifecycle")
		}
		require.NotSame(t, firstScopes[0], secondScopes[0],
			"components must not share a Scope: independent removal is the group's contract")
		require.Equal(t, ListStartStages, first.observedStages())
		require.NoError(t, group.Close())
	})
}

// TestGroupShapeRemoveClosesOnlyThatComponent is the independent-lifecycle invariant, and it is also
// what would break if the convergence hung every component off one shared context.
func TestGroupShapeRemoveClosesOnlyThatComponent(t *testing.T) {
	forEachShape(t, func(t *testing.T, shape floorShape) {
		_, group := newShapedGroup(t, shape)
		removed := &shapedLifecycle{name: "removed"}
		kept := &shapedLifecycle{name: "kept"}
		var removedScope, keptScope *Scope

		removed.body = func(c *shapedLifecycle, stage StartStage, scope *Scope) error {
			removedScope = scope
			c.addCleanup(scope)
			return nil
		}
		kept.body = func(c *shapedLifecycle, stage StartStage, scope *Scope) error {
			keptScope = scope
			c.addCleanup(scope)
			return nil
		}
		require.NoError(t, group.Start(removed.name, removed, StartStateStart))
		require.NoError(t, group.Start(kept.name, kept, StartStateStart))

		require.NoError(t, group.Remove(removed))
		require.Equal(t, int32(1), removed.cleaned.Load(),
			"Remove must drain the removed component's cleanups")
		require.ErrorIs(t, removedScope.Context().Err(), context.Canceled,
			"and cancel the Scope the removed component was handed")
		require.NoError(t, keptScope.Context().Err(),
			"the other component's Scope must stay live: one slow or removed node is not a group-wide "+
				"cancellation")
		require.Equal(t, int32(0), kept.cleaned.Load())

		// The removed component's Scope is the closing-and-closed one: a registration that arrives
		// afterwards runs here and now, exactly once. This is the same leak-prevention path as a late
		// registration after Close, reached through Remove.
		var removedLate atomic.Int32
		removedScope.Add(func() error { removedLate.Add(1); return nil })
		require.Equal(t, int32(1), removedLate.Load(),
			"a cleanup registered after Remove must run synchronously")

		require.NoError(t, group.Close())
		require.Equal(t, int32(1), kept.cleaned.Load(), "the surviving component closes with the group")
		require.Equal(t, int32(1), removed.cleaned.Load(), "and the removed one is not closed twice")
	})
}

// TestGroupShapeClosesInReverseStartOrder is the ordering contract the parent tree relies on.
func TestGroupShapeClosesInReverseStartOrder(t *testing.T) {
	forEachShape(t, func(t *testing.T, shape floorShape) {
		_, group := newShapedGroup(t, shape)
		var (
			orderAccess sync.Mutex
			order       []string
		)
		for _, name := range []string{"one", "two", "three"} {
			component := &shapedLifecycle{name: name}
			component.body = func(c *shapedLifecycle, stage StartStage, scope *Scope) error {
				scope.Add(func() error {
					orderAccess.Lock()
					order = append(order, c.name)
					orderAccess.Unlock()
					return nil
				})
				return nil
			}
			require.NoError(t, group.Start(name, component, StartStateStart))
		}
		require.NoError(t, group.Close())
		orderAccess.Lock()
		defer orderAccess.Unlock()
		require.Equal(t, []string{"three", "two", "one"}, order,
			"components must close in reverse start order")
	})
}

// TestGroupShapeCloseIsIdempotentAndTerminal covers repeated Close and Start-after-Close.
func TestGroupShapeCloseIsIdempotentAndTerminal(t *testing.T) {
	forEachShape(t, func(t *testing.T, shape floorShape) {
		_, group := newShapedGroup(t, shape)
		component := &shapedLifecycle{name: "component"}
		component.body = func(c *shapedLifecycle, stage StartStage, scope *Scope) error {
			c.addCleanup(scope)
			return nil
		}
		require.NoError(t, group.Start(component.name, component, StartStateStart))
		require.NoError(t, group.Close())
		require.Equal(t, int32(1), component.cleaned.Load())
		require.NoError(t, group.Close())
		require.Equal(t, int32(1), component.cleaned.Load(), "a repeated Close runs nothing twice")
		require.ErrorIs(t, group.Start(component.name, component, StartStateStart), os.ErrClosed,
			"a closed group must refuse to start: the Scope it would hand out has no owner")
		require.NoError(t, group.Remove(component), "removing from a closed group is a no-op")
	})
}

// TestGroupShapeStartFailureIsWrappedPerStage pins the error contract of every stage: the failure is
// wrapped with "<stage> <name>" and the original error stays in the chain.
func TestGroupShapeStartFailureIsWrappedPerStage(t *testing.T) {
	forEachShape(t, func(t *testing.T, shape floorShape) {
		_, group := newShapedGroup(t, shape)
		startErr := errors.New("dial the transport")
		for _, stage := range ListStartStages {
			component := &shapedLifecycle{name: "failing"}
			component.body = func(c *shapedLifecycle, stage StartStage, scope *Scope) error {
				c.addCleanup(scope)
				return startErr
			}
			err := group.Start(component.name, component, stage)
			require.ErrorIs(t, err, startErr)
			require.Contains(t, err.Error(), stage.String()+" failing",
				"the stage and the owner must both be in the message")
			require.Equal(t, int32(0), component.cleaned.Load(),
				"nothing runs the cleanup yet: the component's Scope still owns it")
		}
		require.NoError(t, group.Close())
	})
}

// TestGroupShapeCleanupFailureCarriesTheOwnerIntoTheResult is the close-side error contract.
//
// A real teardown failure must reach the caller of Close AND of Remove, wrapped with "close <name>",
// exactly as the nested child's close cleanup used to wrap it. This is the case the convergence could
// silently drop, because the wrapping used to live in the cleanup that disappeared with the child.
func TestGroupShapeCleanupFailureCarriesTheOwnerIntoTheResult(t *testing.T) {
	forEachShape(t, func(t *testing.T, shape floorShape) {
		closeErr := errors.New("unmount route: operation not permitted")
		failing := func(name string) *shapedLifecycle {
			component := &shapedLifecycle{name: name}
			component.body = func(c *shapedLifecycle, stage StartStage, scope *Scope) error {
				scope.Add(func() error { return closeErr })
				return nil
			}
			return component
		}

		// Close path.
		_, group := newShapedGroup(t, shape)
		closedByGroup := failing("closed-by-group")
		require.NoError(t, group.Start(closedByGroup.name, closedByGroup, StartStateStart))
		closeResult := group.Close()
		require.ErrorIs(t, closeResult, closeErr,
			"a real teardown failure must reach the caller of Close")
		require.Contains(t, closeResult.Error(), "close closed-by-group",
			"the failure must name the component that failed to close")

		// Remove path, on a fresh group so the two results cannot be confused.
		_, secondGroup := newShapedGroup(t, shape)
		removed := failing("removed")
		require.NoError(t, secondGroup.Start(removed.name, removed, StartStateStart))
		removeResult := secondGroup.Remove(removed)
		require.ErrorIs(t, removeResult, closeErr,
			"Remove must report its component's teardown failure")
		require.Contains(t, removeResult.Error(), "close removed",
			"and name the component, as the nested close cleanup did")
		require.NoError(t, secondGroup.Close(), "the group itself had nothing left to close")
	})
}

// TestGroupShapeInFlightStartIsCancelledByTheContextTree is the §9 "cancel an in-flight Start" case,
// and it pins WHY it is reachable at all.
//
// LifecycleGroup.Start holds the membership mutex across the component's Start (pre-existing, NOT
// changed by the convergence - see TestGroupShapeRemoveIsSerialisedBehindAnInFlightStart), so neither
// Remove nor the group's own Close can cancel a Start that is still running: they cannot even acquire
// the lock. The cancellation that does reach an in-flight Start comes from the CONTEXT TREE instead:
// the parent Scope's context is cancelled by the parent's close before its cleanup queue is drained,
// and context cancellation propagates through the Scope's own cancel contexts immediately - in the
// nested shape through BOTH layers, because the second Scope's context is a child of the first's.
//
// That is the property the convergence must keep: a component that watches the Scope it was handed
// still learns about the shutdown while it is inside Start, whichever shape owns that Scope.
func TestGroupShapeInFlightStartIsCancelledByTheContextTree(t *testing.T) {
	forEachShape(t, func(t *testing.T, shape floorShape) {
		parent := NewScope(context.Background(), log.NewNOPFactory().Logger())
		group := shape.build(parent)
		entered := make(chan struct{})
		var lateRuns atomic.Int32

		component := &shapedLifecycle{name: "in-flight"}
		component.body = func(c *shapedLifecycle, stage StartStage, scope *Scope) error {
			close(entered)
			// The component waits for the cancellation it was promised, not for a test-side release:
			// this is what a real component with a startup gate does.
			<-scope.Context().Done()
			scope.Add(func() error {
				lateRuns.Add(1)
				return nil
			})
			return nil
		}

		startDone := make(chan error, 1)
		go func() { startDone <- group.Start(component.name, component, StartStateStart) }()
		<-entered
		parentDone := make(chan error, 1)
		go func() { parentDone <- parent.Close() }()

		select {
		case err := <-startDone:
			require.ErrorIs(t, err, context.Canceled,
				"a Start that was still running when the Scope tree was cancelled must not report "+
					"success: the caller would treat a released component as a live one")
		case <-time.After(5 * time.Second):
			t.Fatal("the in-flight Start was never cancelled: the parent's context cancellation must " +
				"propagate to the Scope the component was handed, independently of the close drain")
		}
		select {
		case err := <-parentDone:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("the parent Close never returned")
		}
		require.Equal(t, int32(1), lateRuns.Load(),
			"the cleanup the component registered before returning must run exactly once")
	})
}

// TestGroupShapeRemoveIsSerialisedBehindAnInFlightStart documents, as a measurement and NOT as an
// assertion, the group-level lock ordering that the convergence leaves untouched: Start holds the
// membership mutex across the component's Start, so Remove for the SAME component waits for it.
//
// The Scope contract ("Close does not wait for an in-flight Start") is about Scope.Close; the group
// adds a lock above it. The Manager serialises Start and Remove for one component itself, so this is
// the shipped behaviour, and this test only records it so a future change cannot move it silently.
func TestGroupShapeRemoveIsSerialisedBehindAnInFlightStart(t *testing.T) {
	forEachShape(t, func(t *testing.T, shape floorShape) {
		_, group := newShapedGroup(t, shape)
		entered := make(chan struct{})
		release := make(chan struct{})
		var scope *Scope
		var cleaned atomic.Int32
		component := &shapedLifecycle{name: "in-flight"}
		component.body = func(c *shapedLifecycle, stage StartStage, handed *Scope) error {
			scope = handed
			close(entered)
			<-release
			scope.Add(func() error { cleaned.Add(1); return nil })
			return nil
		}

		startDone := make(chan error, 1)
		go func() { startDone <- group.Start(component.name, component, StartStateStart) }()
		<-entered
		removeReturned := make(chan struct{})
		go func() {
			_ = group.Remove(component)
			close(removeReturned)
		}()
		blocked := !awaitReturned(removeReturned, 200*time.Millisecond)
		t.Logf("L-SCOPE remove_serialised_behind_in_flight_start=%v", blocked)
		require.True(t, blocked,
			"the group membership mutex is held across Start; if this ever stops being true the "+
				"'Remove cannot cancel an in-flight Start' premise of the test above changes too")
		close(release)
		// The Start completes first, and it completes SUCCESSFULLY: Remove has not cancelled anything
		// yet, because it is still waiting for the lock. That "success immediately followed by a
		// removal" window is the shipped ordering, and it is why the Manager serialises Start and
		// Remove for one component itself rather than relying on the group.
		startErr := <-startDone
		t.Logf("L-SCOPE start_result_before_remove_landed=%v", startErr)
		require.NoError(t, startErr)
		<-removeReturned
		require.Equal(t, int32(1), cleaned.Load(),
			"the removal that landed after the Start still drains what that Start registered")
		require.ErrorIs(t, scope.Context().Err(), context.Canceled)
	})
}

// TestGroupShapeLateCleanupAfterCloseRunsSynchronously is the leak-prevention path at group level.
//
// A component can register work from a goroutine that outlives its Start (a resolver started in the
// background is the real shape). If that registration lands after the component's Scope has been
// drained, the cleanup must run on the caller's goroutine immediately; queueing it would leak the
// resource permanently and silently.
func TestGroupShapeLateCleanupAfterCloseRunsSynchronously(t *testing.T) {
	forEachShape(t, func(t *testing.T, shape floorShape) {
		_, group := newShapedGroup(t, shape)
		var scope *Scope
		component := &shapedLifecycle{name: "component"}
		component.body = func(c *shapedLifecycle, stage StartStage, handed *Scope) error {
			scope = handed
			return nil
		}
		require.NoError(t, group.Start(component.name, component, StartStateStart))
		require.NoError(t, group.Close())

		var lateRuns atomic.Int32
		scope.Add(func() error {
			lateRuns.Add(1)
			return nil
		})
		require.Equal(t, int32(1), lateRuns.Load(),
			"a cleanup registered after the component's Scope closed must run synchronously, exactly "+
				"once")
		scope.Add(func() error {
			lateRuns.Add(1)
			return nil
		})
		require.Equal(t, int32(2), lateRuns.Load(),
			"and the next one too: the queue is gone, not merely drained once")
	})
}

// TestGroupShapeCleanupCanReenterRemoveOfItsOwnComponent is the "cleanup reentrancy" case that the
// Remove-outside-the-lock fix made safe. The drain runs with the membership lock released, so a
// cleanup that removes its own (already removed) component must return instead of deadlocking, and
// must not close anything a second time.
func TestGroupShapeCleanupCanReenterRemoveOfItsOwnComponent(t *testing.T) {
	forEachShape(t, func(t *testing.T, shape floorShape) {
		_, group := newShapedGroup(t, shape)
		component := &shapedLifecycle{name: "reentrant"}
		reentrantDone := make(chan error, 1)
		var runs atomic.Int32
		component.body = func(c *shapedLifecycle, stage StartStage, scope *Scope) error {
			scope.Add(func() error {
				runs.Add(1)
				reentrantDone <- group.Remove(c)
				return nil
			})
			return nil
		}
		require.NoError(t, group.Start(component.name, component, StartStateStart))

		removeDone := make(chan error, 1)
		go func() { removeDone <- group.Remove(component) }()
		select {
		case err := <-removeDone:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("Remove deadlocked: the cleanup re-entered Remove for its own component while " +
				"the group membership lock was held")
		}
		require.Equal(t, int32(1), runs.Load(), "the cleanup ran once")
		select {
		case err := <-reentrantDone:
			require.NoError(t, err,
				"the re-entrant Remove finds the component already gone and is a no-op")
		case <-time.After(5 * time.Second):
			t.Fatal("the re-entrant Remove never returned")
		}
	})
}

// TestGroupShapeConcurrentStartAndCloseExactlyOnce is the barrier form of the Start/Close race.
//
// Neither ordering is an error, exactly-once is the invariant: every cleanup a successful Start
// registered must run exactly once, whichever side of Close it landed on.
func TestGroupShapeConcurrentStartAndCloseExactlyOnce(t *testing.T) {
	forEachShape(t, func(t *testing.T, shape floorShape) {
		const rounds = 200
		for round := 0; round < rounds; round++ {
			_, group := newShapedGroup(t, shape)
			var registered atomic.Int32
			var cleaned atomic.Int32
			component := &shapedLifecycle{name: "raced"}
			component.body = func(c *shapedLifecycle, stage StartStage, scope *Scope) error {
				scope.Add(func() error { cleaned.Add(1); return nil })
				registered.Add(1)
				return nil
			}

			barrier := make(chan struct{})
			var waitGroup sync.WaitGroup
			waitGroup.Add(2)
			var startErr error
			go func() {
				defer waitGroup.Done()
				<-barrier
				startErr = group.Start(component.name, component, StartStateStart)
			}()
			go func() {
				defer waitGroup.Done()
				<-barrier
				_ = group.Close()
			}()
			close(barrier)
			waitGroup.Wait()

			if startErr == nil {
				require.Equal(t, registered.Load(), cleaned.Load(),
					"round %d: a Start that reported success must have its cleanup released exactly "+
						"once, whether Close saw it in the queue or Add ran it late", round)
			}
			require.LessOrEqual(t, cleaned.Load(), registered.Load(),
				"round %d: no cleanup may run twice", round)
			_ = group.Close()
			require.Equal(t, registered.Load(), cleaned.Load(),
				"round %d: closing the group must release every registration exactly once", round)
		}
	})
}

// TestGroupShapeBoundedCloseDiagnosticNamesTheStuckCleanup keeps the diagnostic contract: a drain that
// outlives the watchdog reports the cleanup it is stuck on, once, and still finishes.
func TestGroupShapeBoundedCloseDiagnosticNamesTheStuckCleanup(t *testing.T) {
	forEachShape(t, func(t *testing.T, shape floorShape) {
		shrinkCloseWatchdog(t, 100*time.Millisecond)
		logger := &lateCleanupLogger{}
		parent := NewScope(context.Background(), logger)
		group := shape.build(parent)
		t.Cleanup(func() { _ = parent.Close() })

		entered := make(chan struct{})
		release := make(chan struct{})
		component := &shapedLifecycle{name: "parked"}
		component.body = func(c *shapedLifecycle, stage StartStage, scope *Scope) error {
			scope.AddNamed("close parked", func() error {
				close(entered)
				<-release
				return nil
			})
			return nil
		}
		require.NoError(t, group.Start(component.name, component, StartStateStart))

		closed := make(chan error, 1)
		go func() { closed <- group.Close() }()
		<-entered
		require.Eventually(t, func() bool {
			return strings.Contains(logger.captured(), "current: close parked")
		}, 5*time.Second, 5*time.Millisecond,
			"the bounded close report must name the cleanup the drain is stuck on")
		close(release)
		require.NoError(t, <-closed)
	})
}

// TestGroupShapeDiagnosticStartTraceStillNamesTheStage pins the start-side contract that the
// convergence must not drop: a slow stage still produces the "<stage> <name> ..." trace through the
// shared startOwned path. LogElapsed only emits after a second, so the test drives a 1.1s Start.
func TestGroupShapeDiagnosticStartTraceStillNamesTheStage(t *testing.T) {
	if testing.Short() {
		t.Skip("the slow-start trace needs a >1s Start to reach LogElapsed")
	}
	forEachShape(t, func(t *testing.T, shape floorShape) {
		logger := &lateCleanupLogger{}
		parent := NewScope(context.Background(), logger)
		group := shape.build(parent)
		t.Cleanup(func() { _ = parent.Close() })

		component := &shapedLifecycle{name: "slow"}
		component.body = func(c *shapedLifecycle, stage StartStage, scope *Scope) error {
			time.Sleep(1100 * time.Millisecond)
			return nil
		}
		require.NoError(t, group.Start(component.name, component, StartStateStart))
		require.Contains(t, logger.captured(), "start slow completed",
			"a stage that outlives the trace timer must still be reported with its own name")
		require.NoError(t, group.Close())
	})
}

// TestGroupShapeErrorAggregationAcrossComponents pins that one failing component does not hide another
// one's failure, and that the CLOSED/CANCELLED filter still applies on the group path (a component that
// is already gone is not a close failure).
func TestGroupShapeErrorAggregationAcrossComponents(t *testing.T) {
	forEachShape(t, func(t *testing.T, shape floorShape) {
		_, group := newShapedGroup(t, shape)
		firstErr := errors.New("remove route: operation not permitted")
		secondErr := errors.New("persist rule-set: disk quota exceeded")
		for index, componentErr := range []error{firstErr, secondErr} {
			name := []string{"first", "second"}[index]
			component := &shapedLifecycle{name: name}
			component.body = func(c *shapedLifecycle, stage StartStage, scope *Scope) error {
				scope.Add(func() error { return componentErr })
				return nil
			}
			require.NoError(t, group.Start(name, component, StartStateStart))
		}
		// An already-closed component is not a failure of the group close.
		gone := &shapedLifecycle{name: "gone"}
		gone.body = func(c *shapedLifecycle, stage StartStage, scope *Scope) error {
			scope.Add(func() error { return E.Cause(net.ErrClosed, "close listener") })
			return nil
		}
		require.NoError(t, group.Start(gone.name, gone, StartStateStart))

		closeErr := group.Close()
		require.ErrorIs(t, closeErr, firstErr)
		require.ErrorIs(t, closeErr, secondErr,
			"both failures must be in the aggregate: a group close must not stop at the first")
		require.NotErrorIs(t, closeErr, net.ErrClosed)
	})
}
