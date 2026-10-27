package tun

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	tun "github.com/sagernet/sing-tun"

	"github.com/stretchr/testify/require"
)

type resetCountingStack struct {
	tun.Stack
	resets atomic.Int32
}

func (s *resetCountingStack) ResetNetwork() {
	s.resets.Add(1)
}

// TestInterfaceUpdatedBeforeStackStartIsDeferred pins the startup window: StartStateStart publishes
// t.tunStack before StartStatePostStart calls Start, while NetworkManager may already deliver
// InterfaceUpdated. The reset must not reach a Go stack whose dispatcher is not built yet.
func TestInterfaceUpdatedBeforeStackStartIsDeferred(t *testing.T) {
	stack := &resetCountingStack{}
	inbound := &Inbound{tunStack: stack}

	inbound.InterfaceUpdated(context.Background())
	require.Zero(t, stack.resets.Load(), "a pre-Start reset must not reach the stack")
	require.True(t, inbound.stackResetPending.Load(), "the reset must be remembered, not dropped")

	inbound.stackDidStart()
	require.Equal(t, int32(1), stack.resets.Load(), "the deferred reset must run exactly once after Start")
	require.False(t, inbound.stackResetPending.Load())

	inbound.stackDidStart()
	require.Equal(t, int32(1), stack.resets.Load(), "a duplicate activation must not replay the reset")
}

func TestInterfaceUpdatedAfterCloseDoesNotReset(t *testing.T) {
	stack := &resetCountingStack{}
	inbound := &Inbound{tunStack: stack, stackStarted: atomic.Bool{}}
	inbound.stackStarted.Store(true)
	inbound.closed.Store(true)

	inbound.InterfaceUpdated(context.Background())
	require.Zero(t, stack.resets.Load(), "a closed inbound must not reset its stack")
}

// TestInterfaceUpdatedAfterStartResetsEveryTime is the normal post-Start path. It exists because the
// pending flag is only for the startup handoff; a running stack must not require a pending flag to
// reset, and consecutive updates are independent events.
func TestInterfaceUpdatedAfterStartResetsEveryTime(t *testing.T) {
	stack := &resetCountingStack{}
	inbound := &Inbound{tunStack: stack}
	inbound.stackStarted.Store(true)

	inbound.InterfaceUpdated(context.Background())
	inbound.InterfaceUpdated(context.Background())
	inbound.InterfaceUpdated(context.Background())

	require.Equal(t, int32(3), stack.resets.Load(),
		"every post-Start update must reset; consecutive updates are independent events")
	require.False(t, inbound.stackResetPending.Load())
}

func TestInterfaceUpdatedAfterStartDoesNotMergeAPriorPendingIntoTheNewEvent(t *testing.T) {
	stack := &resetCountingStack{}
	inbound := &Inbound{tunStack: stack}
	inbound.stackStarted.Store(true)
	inbound.stackResetPending.Store(true) // a pre-Start event that Start has not consumed yet

	inbound.InterfaceUpdated(context.Background())

	require.Equal(t, int32(2), stack.resets.Load(),
		"the prior pending reset and the new post-Start reset are distinct events and both must run")
	require.False(t, inbound.stackResetPending.Load())
}

func TestInterfaceUpdatedConcurrentAfterStartDoesNotCoalesce(t *testing.T) {
	stack := &resetCountingStack{}
	inbound := &Inbound{tunStack: stack}
	inbound.stackStarted.Store(true)

	const updates = 16
	var waitGroup sync.WaitGroup
	waitGroup.Add(updates)
	for range updates {
		go func() {
			defer waitGroup.Done()
			inbound.InterfaceUpdated(context.Background())
		}()
	}
	waitGroup.Wait()

	require.Equal(t, int32(updates), stack.resets.Load(),
		"each concurrent post-Start update is an independent event")
}

func TestInterfaceUpdatedConcurrentBeforeStartCoalescesToOnePending(t *testing.T) {
	stack := &resetCountingStack{}
	inbound := &Inbound{tunStack: stack}

	const updates = 16
	var waitGroup sync.WaitGroup
	waitGroup.Add(updates)
	for range updates {
		go func() {
			defer waitGroup.Done()
			inbound.InterfaceUpdated(context.Background())
		}()
	}
	waitGroup.Wait()
	require.True(t, inbound.stackResetPending.Load())
	require.Zero(t, stack.resets.Load(), "nothing may reset before Start")

	inbound.stackDidStart()
	require.Equal(t, int32(1), stack.resets.Load(),
		"all pre-Start updates are the same deferred state and must collapse to one reset")
}

// TestInterfaceUpdatedInterleavedWithStackStartDoesNotLoseReset pins the handoff hole: the update
// goroutine observes stackStarted=false, then Start publishes true and finds no pending flag, and
// only then does the update store the flag. The post-store re-check must recover that reset.
func TestInterfaceUpdatedInterleavedWithStackStartDoesNotLoseReset(t *testing.T) {
	stack := &resetCountingStack{}
	inbound := &Inbound{tunStack: stack}

	observedNotStarted := make(chan struct{})
	releaseUpdate := make(chan struct{})
	inbound.testBeforePendingResetStore = func() {
		close(observedNotStarted)
		<-releaseUpdate
	}

	updateDone := make(chan struct{})
	go func() {
		defer close(updateDone)
		inbound.InterfaceUpdated(context.Background())
	}()

	<-observedNotStarted
	// This is the interleaving the old code lost: Start runs while the update is between its
	// stackStarted load and its pending store, so it sees no pending flag.
	inbound.stackDidStart()
	close(releaseUpdate)
	<-updateDone

	require.Equal(t, int32(1), stack.resets.Load(),
		"the reset published after Start must be replayed exactly once")
	require.False(t, inbound.stackResetPending.Load())

	inbound.stackDidStart()
	require.Equal(t, int32(1), stack.resets.Load(), "a duplicate activation must not replay it")
}
