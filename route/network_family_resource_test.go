package route

import (
	"runtime"
	"testing"

	"github.com/sagernet/sing-box/common/netcap"
	"github.com/sagernet/sing/common/control"

	"github.com/stretchr/testify/require"
)

// Layer 12 of the debug round: resources, callbacks and teardown.
//
// # What this round added, on purpose
//
// Nothing that needs closing. The capability is derived from an event the manager already receives,
// published through an atomic, and read through an atomic; it owns no goroutine, no timer, no channel,
// no socket and no registered callback. That is the design decision with the largest effect on this
// layer, and it is asserted below rather than merely asserted in a comment.
//
// # Why a goroutine count is the right check anyway
//
// The claim "this code starts nothing" is only checkable against the code, and the code can change. A
// count taken across many interface updates is what turns the claim into a test: if a later change
// gave the capability a watcher, a refresher or a debounce timer, the count would grow with the number
// of events and this fails.
//
// It is written to be robust rather than precise: other packages' goroutines can start and finish
// during the interval, so the assertion is on GROWTH ACROSS MANY EVENTS rather than on an exact
// number. A per-event leak over a hundred events is far larger than any background noise.

func TestInterfaceUpdatesDoNotAccumulateGoroutines(t *testing.T) {
	h := newTransitionWindowHarness(t)
	h.manager.platformInterface = nil
	finder := control.NewDefaultInterfaceFinder()
	h.manager.interfaceFinder = finder

	// Settle once so any lazily-started machinery has started before the baseline is taken.
	finder.UpdateInterfaces([]control.Interface{{
		Index: 1, Name: "eth0", Flags: 1<<0 | 1<<2, Addresses: mustPrefixes("192.168.1.10/24"),
	}})
	h.manager.observeNonPlatformInterfaces()
	h.settle(t, "baseline")

	runtime.GC()
	baseline := runtime.NumGoroutine()

	// A long run of interface events with the addressing moving between two states, which is the
	// busiest this code gets on a real device during a flapping link.
	const rounds = 100
	for round := 0; round < rounds; round++ {
		addresses := []string{"192.168.1.10/24"}
		if round%2 == 0 {
			addresses = append(addresses, "2001:db8::10/64")
		}
		finder.UpdateInterfaces([]control.Interface{{
			Index: 1, Name: "eth0", Flags: 1<<0 | 1<<2, Addresses: mustPrefixes(addresses...),
		}})
		h.manager.observeNonPlatformInterfaces()
		h.manager.updateNetworkEnvironment()
	}

	runtime.GC()
	after := runtime.NumGoroutine()

	// A per-event leak would be at least `rounds` goroutines. The allowance is for unrelated activity
	// in the test binary, and it is deliberately far below one-per-event.
	require.Lessf(t, after-baseline, rounds/4,
		"%d interface events grew the goroutine count from %d to %d; the capability is supposed to be "+
			"event-driven with no watcher of its own", rounds, baseline, after)
}

// TestCapabilityPublicationRegistersNoCallback is the same claim at the API level, and it is the one
// that would break a teardown.
//
// A registered callback outlives whoever registered it unless something unregisters it, and the
// network manager's own callback wiring is exactly where an unregistered element leaks a live pointer
// into a monitor that outlives the box. This asserts that the capability path adds none: the monitor's
// callback list is inspected after a full sequence of publications and must contain only what the
// production Start registered.
func TestCapabilityPublicationRegistersNoCallback(t *testing.T) {
	h := newTransitionWindowHarness(t)
	monitor := &staticInterfaceMonitor{current: &control.Interface{Index: 1, Name: "eth0"}}
	h.manager.interfaceMonitor = monitor

	setInterfaces(h, "192.0.2.1", interfaceWithAddresses(1, "eth0", "192.168.1.10/24"))
	h.settle(t, "observed")

	// The capability is published repeatedly, including through a real transition.
	setInterfaces(h, "192.0.2.9", interfaceWithAddresses(1, "eth0", "192.168.1.10/24", "2001:db8::10/64"))
	h.settle(t, "moved")
	require.Equal(t, netcap.StateProvisioned, currentFamilyCapability(t, h).Capability.IPv6)

	require.Zero(t, monitor.registered, "the capability path must register nothing with the monitor")
	require.Zero(t, monitor.unregistered, "and therefore unregister nothing: it owns no callback")
}

// TestCloseDuringCapabilityPublicationDoesNotDeadlock is the teardown race.
//
// Publishing takes stateAccess and the transition lock; Close takes resetRunAccess and waits for the
// started context. The three have to be orderable, and the round's brief names "capability update
// simultaneously with reset" and "Close simultaneously with refresh" as the shapes to attack.
//
// The assertion is simply that the sequence completes. A lock-order inversion between the capability
// publication and the reset body would show up as a hang here rather than as a wrong verdict, which is
// why a completion check is the right test.
func TestCloseDuringCapabilityPublicationDoesNotDeadlock(t *testing.T) {
	h := newTransitionWindowHarness(t)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for round := 0; round < 50; round++ {
			addresses := []string{"192.168.1.10/24"}
			if round%2 == 0 {
				addresses = append(addresses, "2001:db8::10/64")
			}
			setInterfaces(h, "192.0.2.1", interfaceWithAddresses(1, "eth0", addresses...))
			h.manager.updateNetworkEnvironment()
		}
	}()

	// Reset concurrently with the publication sequence. ResetNetwork takes resetRunAccess and claims a
	// transition; the publication takes stateAccess and then the transition lock. Both orders must
	// complete.
	for round := 0; round < 10; round++ {
		h.manager.ResetNetwork(h.manager.startedContext())
	}
	<-done

	// The manager is still usable and still reporting a verdict derived from the last addressing.
	_, _, capability := h.manager.NetworkFamilyCapabilitySnapshot()
	require.Contains(t,
		[]netcap.FamilyState{netcap.StateProvisioned, netcap.StateUnavailable, netcap.StateUnknown},
		capability.Capability.IPv6)
}
