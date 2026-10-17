package route

import (
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing/common/winpowrprof"

	"github.com/stretchr/testify/require"
)

// What the OUTCOME of two reset causes arriving together is, not merely that they did not overlap.
//
// # The gap
//
// `TestAllResetEntriesShareOneSerialisation` proves no two resets overlap. That is a real invariant and it is
// not the interesting one here: a serialisation test passes just as happily when one event is DROPPED, or when
// two events coalesce into a state neither of them asked for. A lost or double-applied transition is invisible
// to non-overlap, so the outcome is asserted instead:
//
//	the environment the events published is the one the manager reports
//	the network ends SETTLED, with no transition left pending
//	the DNS plane saw exactly as many resets as the epoch advanced (none dropped, none duplicated)
//
// The two events are deliberately of different kinds: an SSID change moves the environment fingerprint and
// claims a transition of its own, while a power resume dispatches a reset from its own goroutine under a
// different lock discipline. That is the pair an ordinary radio handover produces on a laptop waking up.
//
// # Why the reset COUNT is not asserted exactly
//
// Two resets and three resets are both correct here, and which one happens is a legitimate coalescing
// decision: the resume's reset body re-derives the environment, so it can absorb the SSID change before the
// standalone recompute runs - in which case the recompute correctly finds nothing to do. Asserting a fixed
// number would pin an implementation detail and fail on the correct behaviour. What must NOT happen is
// absorbed-ness that leaves the environment stale, and that is what the assertions below cover.

// TestAWIFICChangeAndAPowerResumeEndInOneSettledState is the outcome assertion.
func TestAWIFICChangeAndAPowerResumeEndInOneSettledState(t *testing.T) {
	manager := newPowerHarness(t)
	router := manager.router.(*countingRouter)

	// The device was suspended, so the resume is a genuine network event rather than the ignored case.
	manager.notifyWindowsPowerEvent(winpowrprof.EVENT_SUSPEND)
	waitForResets(t, router, 1)

	start := make(chan struct{})
	var waitGroup sync.WaitGroup
	waitGroup.Add(2)
	go func() {
		defer waitGroup.Done()
		<-start
		// The radio came up on a network the manager has not seen: this moves the fingerprint and must
		// claim its own transition unless another reset absorbs it.
		manager.setWIFIStateForTest("SSID-after-resume")
		manager.postUpdateNetworkEnvironment()
	}()
	go func() {
		defer waitGroup.Done()
		<-start
		manager.notifyWindowsPowerEvent(winpowrprof.EVENT_RESUME)
	}()
	close(start)
	waitGroup.Wait()

	// The resume dispatches asynchronously; join on the reset signal rather than on a guess.
	waitForResets(t, router, 2)
	waitForSettledTransition(t, manager)

	require.Equal(t, "SSID-after-resume", manager.WIFIState().SSID,
		"the last notification published this network, so it must be the one the manager reports: a lost "+
			"environment update is exactly the failure non-overlap cannot see")

	// The environment the manager ended on must be the one a publish of that SSID produces. Comparing
	// fingerprints rather than strings is what distinguishes "the field was written" from "the network was
	// published": a dropped recompute leaves the field updated and the fingerprint stale.
	control := newPowerHarness(t)
	control.setWIFIStateForTest("SSID-after-resume")
	control.postUpdateNetworkEnvironment()
	require.Equal(t, control.NetworkEnvironment(), manager.NetworkEnvironment(),
		"the racing events must leave the manager on the environment those events describe, with the same "+
			"fingerprint a clean publish produces")

	require.EqualValues(t, dnsResetCount(router), manager.NetworkResetGeneration(),
		"every reset that ran advanced exactly one generation and the DNS plane saw the same number: a "+
			"duplicate would leave DNS on a state no reset produced, a dropped one would leave it on the "+
			"previous generation")

	resets := dnsResetCount(router)
	require.GreaterOrEqual(t, resets, 2, "the suspend and at least one of the two racing events reset")
	require.LessOrEqual(t, resets, 3,
		"three events, so at most three resets: more would mean the resume re-entered")
}

// waitForSettledTransition blocks until the manager reports a settled network, or fails.
//
// It is a watchdog over an invariant the reset paths are required to restore, not the synchroniser for the
// test's own events - those are joined by `waitForResets`.
func waitForSettledTransition(t *testing.T, manager *NetworkManager) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if manager.NetworkTransitionStable() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the network never settled after a Wi-Fi change racing a power resume: every connection " +
				"would be refused until the next network event")
		}
		time.Sleep(time.Millisecond)
	}
}
