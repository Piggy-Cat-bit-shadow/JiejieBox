package runtimecoord

// E-line (lifecycle coordination) probe: may a torn-down owner still be made to run its
// network-change action by a demand that arrives afterwards?
//
// Registration.Snapshot reports a CLOSED registration as stale forever, deliberately: a pool
// that has been torn down must never be mistaken for current. GenerationGate.BeforeDial reads
// that stale answer as "run the owner transition", without asking whether the owner still
// exists. The transport-level pool that shares this problem guarded it explicitly
// (transport/v2rayhttp close_retire_test.go), so the question is whether the protocol-level
// gate needs the same guard.
//
// FAILED on H0 012e63e95 and passes once both entries short-circuit on a closed registration.

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGenerationGateDoesNotRunTheOwnerActionAfterClose is the lifecycle probe.
//
// The sequence is the production one: the owner registers, one transition is handled while it is
// alive (listener side), then the owner is closed by its Scope cleanup, and the device publishes
// another generation afterwards - a reload, a second interface change during teardown, or simply the
// next network event on a process that outlives the outbound. A demand that still holds the closed
// outbound (a group member being replaced, a retry already in flight) then reaches BeforeDial.
func TestGenerationGateDoesNotRunTheOwnerActionAfterClose(t *testing.T) {
	coordinator := New()
	defer coordinator.Close()
	var resets atomic.Int32
	gate := NewGenerationGate(coordinator, "e-life/postclose", func(context.Context) {
		resets.Add(1)
	})
	require.NotNil(t, gate)

	// The owner is alive: one transition reaches it through the listener entry, as production does.
	coordinator.Advance(1)
	gate.NetworkChanged(context.Background())
	require.EqualValues(t, 1, resets.Load())

	// The owner is torn down (Scope cleanup order: the outbound's Close runs the gate's Close).
	gate.Close()

	// The device moves on without it.
	coordinator.Advance(2)

	_ = gate.BeforeDial(context.Background())

	require.EqualValues(t, 1, resets.Load(),
		"a closed owner ran its destructive network-change action after Close; the gate cannot tell "+
			"'this generation is unhandled' from 'this owner no longer exists'")
}

// TestCloseLandedBetweenTheSnapshotAndTheActionIsStillRespected pins the interleaving the entry
// checks cannot cover, and is why the guard also sits inside applyTransition.
//
// BeforeDial observes Snapshot() and only then reaches the action. A close that returns in that gap
// is exactly the race the entry check looks like it prevents: the entry check already passed, the
// snapshot already reported the closed registration as stale, and the owner is about to be told to
// tear down a transport it no longer owns. The test puts Close() in that gap deterministically with
// the existing snapshot hook instead of relying on timing.
func TestCloseLandedBetweenTheSnapshotAndTheActionIsStillRespected(t *testing.T) {
	coordinator := New()
	defer coordinator.Close()
	var resets atomic.Int32
	gate := NewGenerationGate(coordinator, "e-life/close-in-the-gap", func(context.Context) {
		resets.Add(1)
	})
	require.NotNil(t, gate)

	coordinator.Advance(1)

	// Close() runs after the snapshot and before the gate decides what to do with it.
	previous := testHookAfterGenerationSnapshot
	t.Cleanup(func() { testHookAfterGenerationSnapshot = previous })
	testHookAfterGenerationSnapshot = func() {
		testHookAfterGenerationSnapshot = previous
		gate.Close()
	}

	_ = gate.BeforeDial(context.Background())

	require.Zero(t, resets.Load(),
		"a Close that returned between the generation snapshot and the action was ignored: the "+
			"owner was made to run its destructive transition after its own teardown")

	// Control: a live registration with the same hook installed still runs its transition once.
	liveCoordinator := New()
	defer liveCoordinator.Close()
	var liveResets atomic.Int32
	liveGate := NewGenerationGate(liveCoordinator, "e-life/close-in-the-gap/live", func(context.Context) {
		liveResets.Add(1)
	})
	liveCoordinator.Advance(1)
	testHookAfterGenerationSnapshot = func() { testHookAfterGenerationSnapshot = previous }
	_ = liveGate.BeforeDial(context.Background())
	require.EqualValues(t, 1, liveResets.Load(),
		"a live registration stopped running its transition, so the guard is over-broad")
}
