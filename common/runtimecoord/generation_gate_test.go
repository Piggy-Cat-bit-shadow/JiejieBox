package runtimecoord

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGenerationGateAppliesResetOncePerGeneration is the Q12 contract at the mechanism level: a
// published generation makes the gate run the owner's network-change action BEFORE the first
// hand-out, and a successful creation is the only thing that acknowledges the new generation. The
// action itself runs ONCE for that generation: a failed creation keeps the gate stale (so the next
// attempt retries), but retrying must not destroy the replacement a concurrent attempt just built.
func TestGenerationGateAppliesResetOncePerGeneration(t *testing.T) {
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
	require.EqualValues(t, 1, resets.Load(),
		"a second hand-out for the SAME generation must not destroy what the first attempt is building")

	// A failed creation must not acknowledge: the generation has produced no usable connection.
	gate.AfterDial(errors.New("creation failed"))
	require.True(t, gate.Stale())
	gate.BeforeDial(ctx)
	require.EqualValues(t, 1, resets.Load(),
		"a failed attempt may retry the creation, but not re-run the generation's destructive action")

	gate.AfterDial(nil)
	require.False(t, gate.Stale(), "a successful creation acknowledges the generation")
	gate.BeforeDial(ctx)
	require.EqualValues(t, 1, resets.Load(), "no further reset once the generation is current")

	// A NEW generation is a new transition and must run the action again.
	coordinator.Advance(2)
	gate.BeforeDial(ctx)
	require.EqualValues(t, 2, resets.Load())
}

// TestGenerationGateConcurrentHandOutsRunOneReset is the incident's burst: after one published
// generation, many dials arrive together. Every one of them must see the transition applied (the
// gate blocks on the lock while it runs), and the destructive action must run exactly once - not
// once per dial, which closed the sessions the dials were opening.
func TestGenerationGateConcurrentHandOutsRunOneReset(t *testing.T) {
	coordinator := New()
	defer coordinator.Close()
	var resets atomic.Int32
	gate := NewGenerationGate(coordinator, "test/resource", func(context.Context) {
		resets.Add(1)
	})
	defer gate.Close()
	gate.AfterDial(nil)
	coordinator.Advance(1)

	const callers = 64
	var waitGroup sync.WaitGroup
	waitGroup.Add(callers)
	for index := 0; index < callers; index++ {
		go func() {
			defer waitGroup.Done()
			gate.BeforeDial(context.Background())
		}()
	}
	waitGroup.Wait()
	require.EqualValues(t, 1, resets.Load(), "a burst of hand-outs must coalesce into one transition action")
}

// TestGenerationGateListenerAndDemandShareOneTransition pins the publish-to-listener window: the
// listener callback and a demand arriving before it are the same transition, so the gate runs the
// owner's action once. A healthy creation that acknowledged the generation is not reset by a late
// duplicate listener notification.
func TestGenerationGateListenerAndDemandShareOneTransition(t *testing.T) {
	coordinator := New()
	defer coordinator.Close()
	var resets atomic.Int32
	gate := NewGenerationGate(coordinator, "test/resource", func(context.Context) {
		resets.Add(1)
	})
	defer gate.Close()
	gate.AfterDial(nil)

	coordinator.Advance(1)
	gate.NetworkChanged(context.Background())
	require.EqualValues(t, 1, resets.Load(), "the listener must run the transition")
	gate.BeforeDial(context.Background())
	require.EqualValues(t, 1, resets.Load(), "the demand in the same transition must not repeat it")

	gate.AfterDial(nil)
	gate.NetworkChanged(context.Background())
	require.EqualValues(t, 1, resets.Load(), "a duplicate notification on a current generation is a no-op")
}

// TestGenerationGateWithoutACoordinatorIsANoOp is the embedder path: no coordinator means no
// generation to compare, and inventing one would make every dial reset a healthy resource.
func TestGenerationGateWithoutACoordinatorIsANoOp(t *testing.T) {
	require.Nil(t, NewGenerationGate(nil, "test/resource", func(context.Context) {}))
	require.NotPanics(t, func() {
		var gate *GenerationGate
		gate.BeforeDial(context.Background())
		gate.NetworkChanged(context.Background())
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
