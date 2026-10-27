package tun

import (
	"context"
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
