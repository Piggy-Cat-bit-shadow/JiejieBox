package power

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Registering an observer concurrently with a state change must be race-free.
//
// # The race these pin
//
// `flushNotifications` snapshots the observer SLICES under the lock and then walks them
// after releasing it:
//
//	g.access.Lock()
//	observers := g.observers
//	reuseObservers := g.reuseObservers
//	g.access.Unlock()
//	for _, observer := range observers { observer(state) }
//
// `AddObserver`/`AddReuseObserver` append to those same fields under the lock. A slice
// header is two words - pointer and length - and copying it does not copy the backing
// array, so the append writes to the very memory the loop is reading. The snapshot is
// therefore only safe if nobody appends while it is being walked, which is exactly what
// the unlocked walk gives up.
//
// This is the same defect the fork already documented and fixed for the urltest update
// hooks (common/urltest/hook_race_test.go: "the slice header and its backing array were
// therefore read and written concurrently"), and its fix is the same one: COPY the list
// under the lock, then iterate the copy.
//
// # Why the notification is checked as well as the race
//
// A lost notification is the functional half. `flushNotifications` clears
// `pendingNotify`/`pendingReuse` before it walks, so an observer registered after the
// snapshot never learns about the transition it was registered for - it is told nothing
// and the debt is already cleared. On a device axis that means a subsystem that starts
// during a resume can miss the reuse boundary and keep using retired resources.

// TestObserverRegistrationIsRaceFree drives state changes while observers are being
// registered. Run under -race this detects the unsynchronised slice access.
func TestObserverRegistrationIsRaceFree(t *testing.T) {
	governor := NewGovernor(DefaultPolicy())
	t.Cleanup(governor.Close)

	const iterations = 400

	var (
		waitGroup sync.WaitGroup
		start     = make(chan struct{})
	)

	// Writer: drives real transitions, each of which owes a notification.
	waitGroup.Add(1)
	go func() {
		defer waitGroup.Done()
		<-start
		for index := range iterations {
			if index%2 == 0 {
				governor.DevicePaused()
			} else {
				governor.DeviceWake()
			}
		}
	}()

	// Registrars: append to the observer lists while the writer is notifying.
	for range 3 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			for range iterations {
				governor.AddObserver(func(State) {})
				governor.AddReuseObserver(func(ReuseBoundary) {})
			}
		}()
	}

	close(start)
	waitGroup.Wait()

	// Every registration must have landed: the append path may not lose entries to the
	// unsynchronised walk.
	governor.access.Lock()
	observerCount := len(governor.observers)
	reuseCount := len(governor.reuseObservers)
	governor.access.Unlock()
	require.Equal(t, 3*iterations, observerCount,
		"every AddObserver must be recorded")
	require.Equal(t, 3*iterations, reuseCount,
		"every AddReuseObserver must be recorded")
}

// TestObserverRegisteredDuringNotificationIsStillNotified is the functional half.
//
// An observer registered while a notification is in flight must not be silently skipped
// for a transition that is still owed: `pendingNotify` is cleared before the walk, so a
// missed entry is a debt that is never paid.
//
// The interleaving is forced through the observer list itself - the first observer
// registers the second one - which is deterministic and is also the realistic shape:
// a subsystem that starts a child component at a boundary.
func TestObserverRegisteredDuringNotificationIsStillNotified(t *testing.T) {
	governor := NewGovernor(DefaultPolicy())
	t.Cleanup(governor.Close)

	lateDelivered := make(chan State, 8)
	// The guard must cover the REGISTRATION, not the whole observer body: it is what makes the
	// interleaving happen exactly once, and leaving it around the body would make the first observer
	// return early on the delivery pass that is meant to reach the observer it registered.
	var registerOnce sync.Once

	governor.AddObserver(func(State) {
		registerOnce.Do(func() {
			governor.AddObserver(func(state State) {
				select {
				case lateDelivered <- state:
				default:
				}
			})
		})
	})

	// One transition owes one notification. The observer registered by the running observer is
	// registered before the walk ends, so it must be told about the transition it was registered
	// for rather than having its notification dropped with the debt.
	governor.DevicePaused()

	select {
	case <-lateDelivered:
	case <-time.After(5 * time.Second):
		t.Fatal("an observer registered during a notification was never notified: the debt was " +
			"cleared before the walk, so a subsystem that starts at a boundary can miss the " +
			"transition it registered for")
	}

	// And the governor must still be usable: the next transition reaches the new observer too.
	governor.DeviceWake()
	governor.DevicePaused()
}
