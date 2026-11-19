package tuic

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing-box/common/runtimecoord"
	"github.com/stretchr/testify/require"
)

// TestInterfaceUpdatedRoutesThroughTheGenerationGate pins the listener half of the transition
// contract.
//
// A physical network change advances the coordinator's epoch and only THEN tells the listeners. A
// demand that arrives inside that window already runs the owner's network-change action through the
// gate's hand-out path, builds a session on the new network and acknowledges the generation. When the
// listener phase reaches this owner a moment later, running the action again destroys exactly that
// session - which is the double transition this test exists to prevent: the push notification and the
// demand are the SAME action, so they must be one action.
//
// The observation is the gate's own reset callback, which is the only thing the owner can execute here
// (no transport is constructed): a listener that routes through the gate runs it once per generation,
// and one that calls the action directly runs it per notification.
func TestInterfaceUpdatedRoutesThroughTheGenerationGate(t *testing.T) {
	coordinator := runtimecoord.New()
	t.Cleanup(func() { _ = coordinator.Close() })
	var resets atomic.Int64
	subject := &Outbound{}
	subject.generation = runtimecoord.NewGenerationGate(coordinator, "tuic/test", func(context.Context) {
		resets.Add(1)
	})

	// The listener phase of a transition that published generation 1.
	coordinator.Advance(1)
	subject.InterfaceUpdated(context.Background())
	require.EqualValues(t, 1, resets.Load(),
		"the listener must run the network-change action for the new generation")

	// The same generation again: a duplicate notification, or a demand that already applied it.
	subject.InterfaceUpdated(context.Background())
	require.EqualValues(t, 1, resets.Load(),
		"a second destructive pass for ONE generation is what closes the session the first one built")

	// A genuinely newer generation must run it again, or the transport would never rebind.
	coordinator.Advance(2)
	subject.InterfaceUpdated(context.Background())
	require.EqualValues(t, 2, resets.Load())
}
