package varch_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
)

type lifeComp struct {
	name string

	mutex    sync.Mutex
	scopes   []*adapter.Scope
	cleanups atomic.Int32
	stageErr map[adapter.StartStage]error
	start    func(stage adapter.StartStage, scope *adapter.Scope) error
	cleanup  func() error
}

func (c *lifeComp) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	c.mutex.Lock()
	c.scopes = append(c.scopes, scope)
	c.mutex.Unlock()
	if c.start != nil {
		if err := c.start(stage, scope); err != nil {
			return err
		}
	}
	if stage == adapter.StartStateInitialize {
		scope.AddNamed(c.name, func() error {
			c.cleanups.Add(1)
			if c.cleanup != nil {
				return c.cleanup()
			}
			return nil
		})
	}
	if err := c.stageErr[stage]; err != nil {
		return err
	}
	return nil
}

func (c *lifeComp) scope() *adapter.Scope {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if len(c.scopes) == 0 {
		return nil
	}
	return c.scopes[0]
}

func newTestParent(t *testing.T) *adapter.Scope {
	t.Helper()
	parent := adapter.NewScope(context.Background(), log.NewNOPFactory().Logger())
	t.Cleanup(func() { _ = parent.Close() })
	return parent
}

func startAllStages(t *testing.T, group *adapter.LifecycleGroup, name string, component adapter.Lifecycle) {
	t.Helper()
	for _, stage := range adapter.ListStartStages {
		if err := group.Start(name, component, stage); err != nil {
			t.Fatalf("start %s stage %v: %v", name, stage, err)
		}
	}
}

func waitClosed(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not finish within 5s", what)
	}
}

// TestArchDOneScopePerComponentAndStageReentry: every stage of one component is started against the
// SAME Scope; the group no longer builds a second layer per component.
func TestArchDOneScopePerComponentAndStageReentry(t *testing.T) {
	parent := newTestParent(t)
	group := adapter.NewLifecycleGroup(parent)
	component := &lifeComp{name: "one"}
	startAllStages(t, group, "one", component)
	component.mutex.Lock()
	scopes := append([]*adapter.Scope(nil), component.scopes...)
	component.mutex.Unlock()
	if len(scopes) != len(adapter.ListStartStages) {
		t.Fatalf("recorded %d scopes, want %d", len(scopes), len(adapter.ListStartStages))
	}
	for i := range scopes {
		if scopes[i] != scopes[0] {
			t.Fatalf("stage %v got a different Scope than stage %v", adapter.ListStartStages[i], adapter.ListStartStages[0])
		}
	}
	if err := group.Close(); err != nil {
		t.Fatalf("group.Close: %v", err)
	}
	if got := component.cleanups.Load(); got != 1 {
		t.Fatalf("cleanup ran %d times, want 1", got)
	}
	if err := scopes[0].Context().Err(); !errors.Is(err, context.Canceled) {
		t.Fatalf("component context after Close = %v, want context.Canceled", err)
	}
}

// TestArchDStartFailurePerStage: a failure at every stage is wrapped with stage and component name,
// and the registered cleanup still runs exactly once at Close.
func TestArchDStartFailurePerStage(t *testing.T) {
	for _, stage := range adapter.ListStartStages {
		stage := stage
		t.Run(fmt.Sprint(stage), func(t *testing.T) {
			parent := newTestParent(t)
			group := adapter.NewLifecycleGroup(parent)
			component := &lifeComp{name: "failing", stageErr: map[adapter.StartStage]error{stage: errors.New("boom")}}
			var startErr error
			for _, current := range adapter.ListStartStages {
				startErr = group.Start("failing", component, current)
				if current == stage {
					break
				}
				if startErr != nil {
					t.Fatalf("stage %v failed before the injected stage: %v", current, startErr)
				}
			}
			if startErr == nil {
				t.Fatalf("stage %v did not fail", stage)
			}
			text := startErr.Error()
			if !strings.Contains(text, "boom") || !strings.Contains(text, "failing") || !strings.Contains(text, fmt.Sprint(stage)) {
				t.Fatalf("start error %q does not carry stage+name+cause", text)
			}
			if err := group.Close(); err != nil {
				t.Fatalf("group.Close: %v", err)
			}
			if got := component.cleanups.Load(); got != 1 {
				t.Fatalf("cleanup ran %d times, want 1", got)
			}
		})
	}
}

// TestArchDInFlightStartCancelledByParentClose: a Start blocked inside the component sees its own
// context cancelled when the parent closes, and the group Start cannot report success.
func TestArchDInFlightStartCancelledByParentClose(t *testing.T) {
	parent := newTestParent(t)
	group := adapter.NewLifecycleGroup(parent)
	entered := make(chan struct{})
	component := &lifeComp{name: "slow"}
	component.start = func(stage adapter.StartStage, scope *adapter.Scope) error {
		if stage != adapter.StartStateStart {
			return nil
		}
		close(entered)
		<-scope.Context().Done()
		return nil
	}
	startResult := make(chan error, 1)
	go func() {
		startResult <- group.Start("slow", component, adapter.StartStateStart)
	}()
	waitClosed(t, entered, "component Start")
	closeResult := make(chan error, 1)
	go func() { closeResult <- parent.Close() }()
	select {
	case err := <-startResult:
		if err == nil {
			t.Fatal("in-flight Start returned success after the parent closed")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("in-flight Start error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight Start did not return after the parent context was cancelled")
	}
	select {
	case err := <-closeResult:
		if err != nil {
			t.Fatalf("parent.Close: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("parent.Close did not finish after the in-flight Start returned")
	}
}

// TestArchDRemoveClosesOnlyThatComponent: Remove drains one component, leaves the sibling's context
// live, and a later group.Close drains the sibling exactly once.
func TestArchDRemoveClosesOnlyThatComponent(t *testing.T) {
	parent := newTestParent(t)
	group := adapter.NewLifecycleGroup(parent)
	first := &lifeComp{name: "first"}
	second := &lifeComp{name: "second"}
	startAllStages(t, group, "first", first)
	startAllStages(t, group, "second", second)
	firstScope, secondScope := first.scope(), second.scope()

	if err := group.Remove(first); err != nil {
		t.Fatalf("Remove(first): %v", err)
	}
	if got := first.cleanups.Load(); got != 1 {
		t.Fatalf("removed component cleanup ran %d times, want 1", got)
	}
	if firstScope.Context().Err() == nil {
		t.Fatal("removed component context still live")
	}
	if secondScope.Context().Err() != nil {
		t.Fatal("sibling context was cancelled by Remove")
	}
	if got := second.cleanups.Load(); got != 0 {
		t.Fatalf("sibling cleanup ran %d times before Close", got)
	}
	if err := group.Remove(first); err != nil {
		t.Fatalf("second Remove(first): %v", err)
	}
	if got := first.cleanups.Load(); got != 1 {
		t.Fatalf("cleanup re-ran on a second Remove: %d", got)
	}
	if err := group.Close(); err != nil {
		t.Fatalf("group.Close: %v", err)
	}
	if got := second.cleanups.Load(); got != 1 {
		t.Fatalf("sibling cleanup ran %d times, want 1", got)
	}
	if got := first.cleanups.Load(); got != 1 {
		t.Fatalf("removed component cleanup ran %d times, want 1", got)
	}
}

// TestArchDReverseOrderAndErrorAggregation: close order is reverse start order and two real close
// errors are both carried in the aggregate; a cancelled cleanup is filtered as an orderly shutdown.
func TestArchDReverseOrderAndErrorAggregation(t *testing.T) {
	parent := newTestParent(t)
	group := adapter.NewLifecycleGroup(parent)
	var order []string
	var orderMutex sync.Mutex
	record := func(name string) {
		orderMutex.Lock()
		order = append(order, name)
		orderMutex.Unlock()
	}
	groupA := &lifeComp{name: "a", cleanup: func() error { record("a"); return errors.New("error-a") }}
	groupB := &lifeComp{name: "b", cleanup: func() error { record("b"); return errors.New("error-b") }}
	groupC := &lifeComp{name: "c", cleanup: func() error { record("c"); return context.Canceled }}
	startAllStages(t, group, "a", groupA)
	startAllStages(t, group, "b", groupB)
	startAllStages(t, group, "c", groupC)
	err := group.Close()
	if err == nil {
		t.Fatal("group.Close did not report the cleanup errors")
	}
	text := err.Error()
	if !strings.Contains(text, "error-a") || !strings.Contains(text, "error-b") {
		t.Fatalf("aggregate %q lost a component error", text)
	}
	if errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled cleanup leaked into the aggregate: %v", err)
	}
	orderMutex.Lock()
	got := fmt.Sprint(order)
	orderMutex.Unlock()
	if got != "[c b a]" {
		t.Fatalf("close order = %s, want [c b a]", got)
	}
}

// TestArchDCloseIsTerminalAndLateAddRunsSynchronously: Close is terminal (Start refuses, Remove is a
// no-op, a second Close does not re-run cleanups) and Add after Close runs the cleanup inline.
func TestArchDCloseIsTerminalAndLateAddRunsSynchronously(t *testing.T) {
	parent := newTestParent(t)
	group := adapter.NewLifecycleGroup(parent)
	component := &lifeComp{name: "terminal"}
	startAllStages(t, group, "terminal", component)
	scope := component.scope()
	if err := group.Close(); err != nil {
		t.Fatalf("group.Close: %v", err)
	}
	if err := group.Close(); err != nil {
		t.Fatalf("second group.Close: %v", err)
	}
	if got := component.cleanups.Load(); got != 1 {
		t.Fatalf("cleanup ran %d times after two Close calls", got)
	}
	if err := group.Start("terminal", component, adapter.StartStateStart); err == nil {
		t.Fatal("Start after Close returned nil")
	}
	if err := group.Remove(component); err != nil {
		t.Fatalf("Remove after Close: %v", err)
	}
	lateRan := false
	scope.Add(func() error {
		lateRan = true
		return nil
	})
	if !lateRan {
		t.Fatal("cleanup registered after Close did not run synchronously")
	}
	// A cleanup registered after close that fails is logged and dropped; the call must still return.
	scope.Add(func() error { return os.ErrClosed })
}

// TestArchDCleanupCanReenterRemoveOfItsOwnComponent covers the supported reentrancy: a cleanup that
// runs from Remove (which dropped the membership lock before closing) may call Remove for itself.
func TestArchDCleanupCanReenterRemoveOfItsOwnComponent(t *testing.T) {
	parent := newTestParent(t)
	group := adapter.NewLifecycleGroup(parent)
	done := make(chan struct{})
	component := &lifeComp{name: "reenter"}
	component.cleanup = func() error {
		// Must not deadlock: the membership lock is not held across the drain.
		if err := group.Remove(component); err != nil {
			t.Errorf("reentrant Remove: %v", err)
		}
		close(done)
		return nil
	}
	startAllStages(t, group, "reenter", component)
	if err := group.Remove(component); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	waitClosed(t, done, "cleanup reentrant Remove")
}

// TestArchDRemoveDoesNotHoldMembershipLockAcrossDrain is the regression guard for the hand-off
// property: while one component's cleanup is parked, the group must still accept Start for another
// component. A locked drain would block Start until the parked cleanup finished.
func TestArchDRemoveDoesNotHoldMembershipLockAcrossDrain(t *testing.T) {
	parent := newTestParent(t)
	group := adapter.NewLifecycleGroup(parent)
	cleanupStarted := make(chan struct{})
	releaseCleanup := make(chan struct{})
	parked := &lifeComp{name: "parked"}
	parked.cleanup = func() error {
		close(cleanupStarted)
		<-releaseCleanup
		return nil
	}
	startAllStages(t, group, "parked", parked)

	removeDone := make(chan error, 1)
	go func() { removeDone <- group.Remove(parked) }()
	waitClosed(t, cleanupStarted, "parked cleanup")

	startReturned := make(chan error, 1)
	go func() {
		other := &lifeComp{name: "other"}
		startReturned <- group.Start("other", other, adapter.StartStateInitialize)
	}()
	select {
	case err := <-startReturned:
		if err != nil {
			t.Fatalf("Start while another component is draining: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start blocked behind a parked cleanup: the membership lock is held across the drain")
	}
	close(releaseCleanup)
	select {
	case err := <-removeDone:
		if err != nil {
			t.Fatalf("Remove: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Remove did not finish after the cleanup was released")
	}
	if err := group.Close(); err != nil {
		t.Fatalf("group.Close: %v", err)
	}
}

// TestArchDParentCloseCancelsAllComponentContexts checks cancellation propagation: after the group
// is closed, every component's own Scope context is done.
func TestArchDParentCloseCancelsAllComponentContexts(t *testing.T) {
	parent := newTestParent(t)
	group := adapter.NewLifecycleGroup(parent)
	var components []*lifeComp
	for i := 0; i < 3; i++ {
		component := &lifeComp{name: fmt.Sprintf("c%d", i)}
		startAllStages(t, group, component.name, component)
		components = append(components, component)
	}
	if err := parent.Close(); err != nil {
		t.Fatalf("parent.Close: %v", err)
	}
	for i, component := range components {
		scope := component.scope()
		if scope == nil {
			t.Fatalf("component %d has no scope", i)
		}
		if !errors.Is(scope.Context().Err(), context.Canceled) {
			t.Fatalf("component %d context = %v, want context.Canceled", i, scope.Context().Err())
		}
	}
}
