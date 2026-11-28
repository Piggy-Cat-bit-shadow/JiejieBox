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
