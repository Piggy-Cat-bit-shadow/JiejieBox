package runtimecoord

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGenerationGateAppliesResetInTheWindow is the Q12 contract at the mechanism level: a published
// generation makes the gate run the owner's network-change action BEFORE the first hand-out, and a
// successful creation is the only thing that acknowledges the new generation.
func TestGenerationGateAppliesResetInTheWindow(t *testing.T) {
	coordinator := New()
	defer coordinator.Close()
	var resets atomic.Int32
	gate := NewGenerationGate(coordinator, "test/resource", func(context.Context) {
		resets.Add(1)
	})
	require.NotNil(t, gate)
	defer gate.Close()

	ctx := context.Background()
	gate.BeforeDial(ctx)
	require.Zero(t, resets.Load(), "a current generation must not run the reset action")

	gate.AfterDial(nil)
	require.False(t, gate.Stale())

	// The network manager publishes the new generation; the listener phase has NOT run yet.
	coordinator.Advance(1)
	require.True(t, gate.Stale())

	gate.BeforeDial(ctx)
	require.EqualValues(t, 1, resets.Load(), "the first hand-out in the window must run the owner's action")
	require.True(t, gate.Stale(), "running the action is not proof that a new session exists")

	gate.BeforeDial(ctx)
	require.EqualValues(t, 2, resets.Load(), "a second hand-out before any success repeats the action")

	// A failed creation must not acknowledge: the generation has produced no usable connection.
	gate.AfterDial(errors.New("creation failed"))
	require.True(t, gate.Stale())
	gate.BeforeDial(ctx)
	require.EqualValues(t, 3, resets.Load())

	gate.AfterDial(nil)
	require.False(t, gate.Stale(), "a successful creation acknowledges the generation")
	gate.BeforeDial(ctx)
	require.EqualValues(t, 3, resets.Load(), "no further reset once the generation is current")

	coordinator.Advance(2)
	gate.BeforeDial(ctx)
	require.EqualValues(t, 4, resets.Load())
}

// TestGenerationGateWithoutACoordinatorIsANoOp is the embedder path: no coordinator means no
// generation to compare, and inventing one would make every dial reset a healthy resource.
func TestGenerationGateWithoutACoordinatorIsANoOp(t *testing.T) {
	require.Nil(t, NewGenerationGate(nil, "test/resource", func(context.Context) {}))
	require.NotPanics(t, func() {
		var gate *GenerationGate
		gate.BeforeDial(context.Background())
		gate.AfterDial(nil)
		require.False(t, gate.Stale())
		gate.Close()
	})
}

// TestGenerationGateCloseUnregisters makes the lifecycle explicit: a reloaded outbound must not
// leave its registration behind in the coordinator.
func TestGenerationGateCloseUnregisters(t *testing.T) {
	coordinator := New()
	defer coordinator.Close()
	gate := NewGenerationGate(coordinator, "test/resource", func(context.Context) {})
	require.NotNil(t, gate)
	gate.Close()
	gate.Close()
	coordinator.Advance(1)
	require.True(t, gate.Stale(), "a closed registration reports stale rather than pretending to be current")
}

// TestGenerationGateCloseIsConcurrentSafe pins the idempotence contract at the primitive, not only
// at a sequential second call. Two owners can reach the same endpoint's Close (the Scope cleanup and
// the manager's duplicate-tag loser); a plain `if remove != nil { remove(); remove = nil }` is an
// unsynchronised read-modify-write that runs the unregister twice and is reported by the race
// detector. The remove function must run exactly once.
func TestGenerationGateCloseIsConcurrentSafe(t *testing.T) {
	var removed atomic.Int32
	gate := &GenerationGate{remove: func() { removed.Add(1) }}

	const callers = 32
	var waitGroup sync.WaitGroup
	waitGroup.Add(callers)
	for index := 0; index < callers; index++ {
		go func() {
			defer waitGroup.Done()
			gate.Close()
		}()
	}
	waitGroup.Wait()
	require.EqualValues(t, 1, removed.Load(), "the unregister must run exactly once")
}
