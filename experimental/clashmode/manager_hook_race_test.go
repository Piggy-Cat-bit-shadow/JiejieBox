package clashmode

import (
	"context"
	"sync"
	"testing"

	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/common/observable"

	"github.com/stretchr/testify/require"
)

// Registering an update hook concurrently with a mode switch must be race-free.
//
// # The shape under test
//
// `SetMode` snapshots the hook list under the control lock and walks it after releasing it:
//
//	m.updateAccess.Lock()
//	...
//	updateHooks := m.updateHooks          // slice HEADER only: pointer + length
//	m.updateAccess.Unlock()
//	for _, hook := range updateHooks { hook.Emit(struct{}{}) }
//
// `AddUpdateHook` appends to the same field under the same lock. A slice header is two words and
// copying it does not copy the backing array, so an append that happens while the loop is running
// writes the memory the loop is reading.
//
// The reasoning that dismissed this as "only the control plane" is not a safety argument: the race
// detector is the authority on whether it is a race. Two of the three call sites are startup-time
// registrations (`route/reference.go`, `daemon/attached_service.go`) and the third is a test, but
// "currently only called at startup" is a property of today's callers, not of the code.
//
// # Why this is asserted rather than merely reasoned about
//
// The sibling defect in `common/power` was fixed on exactly this reasoning, and its functional half
// (a notification owed and never paid) was reproducible while its data-race half was not observed in
// the window the test happened to run. The measurement here is therefore the test itself, run under
// `-race`; a clean run is reported as "not observed", not as "not a race".

// TestAddUpdateHookConcurrentWithSetMode drives registrations against switches.
//
// Run with `-race`, this is the measurement. Without it, the assertion at the end still checks that
// no registration was lost, which is the functional half: the loop walks a snapshot, so a hook
// appended during it is simply not notified for that switch.
func TestAddUpdateHookConcurrentWithSetMode(t *testing.T) {
	const (
		iterations = 300
		registrars = 3
	)

	manager := NewManager(
		context.Background(),
		log.NewNOPFactory().Logger(),
		"Rule",
		[]string{"Rule", "Global", "Direct"},
	)

	var (
		waitGroup sync.WaitGroup
		start     = make(chan struct{})
	)

	// Switcher: publishes a different mode each time, so every call owes a notification.
	waitGroup.Add(1)
	go func() {
		defer waitGroup.Done()
		<-start
		modes := []string{"Global", "Direct", "Rule"}
		for index := range iterations {
			manager.SetMode(modes[index%len(modes)])
		}
	}()

	// Registrars: append to the hook list while the switcher is walking it.
	for range registrars {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			for range iterations {
				subscriber := observable.NewSubscriber[struct{}](1)
				manager.AddUpdateHook(subscriber)
			}
		}()
	}

	close(start)
	waitGroup.Wait()

	manager.updateAccess.Lock()
	hookCount := len(manager.updateHooks)
	manager.updateAccess.Unlock()

	require.Equal(t, registrars*iterations, hookCount,
		"every AddUpdateHook must be recorded: the append must not lose entries to a walk that is "+
			"reading the same backing array")
}

// TestSetModeDeliversToAHookRegisteredDuringTheWalk is the functional half.
//
// A hook registered while a switch is being delivered is not in the snapshot, so it misses that
// switch. For a mode change that is recoverable - the hook is a channel-backed subscriber and the
// NEXT switch reaches it - so this test only pins that the loss is bounded and that the manager stays
// usable, rather than asserting the stronger property the governor needed.
//
// It is deliberately NOT asserted that the late hook must see the in-flight switch: unlike a reuse
// boundary, a mode is a LEVEL that the hook can read for itself via `Mode()`, so "missed an edge"
// costs nothing here. Stating the asymmetry is the point - the same code shape has a different
// severity in the two packages.
func TestSetModeDeliversToAHookRegisteredDuringTheWalk(t *testing.T) {
	manager := NewManager(
		context.Background(),
		log.NewNOPFactory().Logger(),
		"Rule",
		[]string{"Rule", "Global", "Direct"},
	)

	before := observable.NewSubscriber[struct{}](8)
	manager.AddUpdateHook(before)

	// Register a second hook, then switch: the walk snapshot is taken at the switch, so a hook added
	// before it must be reached.
	after := observable.NewSubscriber[struct{}](8)
	manager.AddUpdateHook(after)

	manager.SetMode("Global")

	subscriptionAfter, _ := after.Subscription()
	require.Equal(t, "Global", manager.Mode())
	require.Len(t, subscriptionAfter, 1,
		"a hook registered before the switch must be notified by it")

	// The manager stays usable, and the next switch reaches both.
	manager.SetMode("Direct")
	subscriptionBefore, _ := before.Subscription()
	require.Len(t, subscriptionBefore, 2,
		"both switches must have reached the first hook")
	require.Equal(t, "Direct", manager.Mode())
}
