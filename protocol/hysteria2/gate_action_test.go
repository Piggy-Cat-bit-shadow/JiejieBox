package hysteria2

// The generation gate's action must be the RESET, not the gated listener (audit P14, window B).
//
// GenerationGate serialises its transition with `resetAccess`, held ACROSS the owner's action so a
// demand that starts during the transition waits for it instead of borrowing the half-reset resource.
// That only works if the action does not come back through the gate: NetworkChanged -> applyTransition
// -> reset. For every owner but one the action is `resetForNetworkChange`, a plain close.
//
// This outbound's SINGLE-client path wired the action as `InterfaceUpdated` - the method that itself
// calls `NetworkChanged`. On the first real transition (an epoch that has moved past the one the gate
// was built with, so the once-per-generation guard lets it through) the call re-enters
// applyTransition while the same goroutine holds resetAccess: a non-reentrant mutex, so the listener
// blocks forever. The transition never runs, the mutex is never released, and every later
// BeforeDial on this outbound blocks on the same mutex before it can even decide staleness - the
// outbound is dead until the process restarts.
//
// The tests build the outbound through NewOutbound with a real coordinator, so the wiring under test
// is the production one rather than a copy of it in the test.

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/runtimecoord"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// newGateActionSubject builds the production single-client outbound with a coordinator attached. No
// transport is dialled: NewOutbound constructs the carrier lazily.
func newGateActionSubject(t *testing.T, coordinator *runtimecoord.Coordinator) *Outbound {
	t.Helper()
	ctx := service.ContextWith[*runtimecoord.Coordinator](context.Background(), coordinator)
	created, err := NewOutbound(ctx, nil, log.NewNOPFactory().NewLogger("gate-action"), "hysteria2-gate-action",
		option.Hysteria2OutboundOptions{
			ServerOptions: option.ServerOptions{Server: "127.0.0.1", ServerPort: 1},
			Password:      "gate-action-password",
			OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{
				TLS: &option.OutboundTLSOptions{Enabled: true, ServerName: "localhost", Insecure: true},
			},
		})
	require.NoError(t, err)
	outbound, isOutbound := created.(*Outbound)
	require.True(t, isOutbound)
	t.Cleanup(func() { _ = outbound.client.CloseWithError(net.ErrClosed) })
	t.Cleanup(func() { outbound.generation.Close() })
	return outbound
}

// waitOrFail runs call and fails the test if it does not return inside budget. The goroutine is
// deliberately not reclaimed on failure: the point of the failure is that it cannot be.
func waitOrFail(t *testing.T, budget time.Duration, what string, call func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		call()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(budget):
		t.Fatalf("%s did not return within %s: it is blocked on the generation gate's transition lock", what, budget)
	}
}

// notify drives one listener-phase notification and fails fast if it blocks, so a red run reports the
// deadlock instead of hanging the test binary.
func notify(t *testing.T, subject *Outbound) {
	t.Helper()
	waitOrFail(t, 2*time.Second, "the listener phase (InterfaceUpdated)", func() {
		subject.InterfaceUpdated(context.Background())
	})
}

// TestTheListenerTransitionIsNotReentrant is the regression: the first listener-phase notification
// after a published generation must return, run the reset exactly once, and leave the gate usable for
// the demands that follow.
func TestTheListenerTransitionIsNotReentrant(t *testing.T) {
	coordinator := runtimecoord.New()
	t.Cleanup(func() { _ = coordinator.Close() })
	subject := newGateActionSubject(t, coordinator)

	// The physical transition: the coordinator publishes generation 1, and only then the listener
	// phase reaches this outbound.
	coordinator.Advance(1)
	notify(t, subject)

	// Positive control: the notification did run the owner's action, once.
	require.EqualValues(t, 1, subject.client.generation,
		"the listener notification did not run the network-change reset")

	// A demand that arrives after the transition must reach its decision: a gate left locked would
	// block here forever, which is the shape that makes every later dial hang.
	waitOrFail(t, 2*time.Second, "a demand's BeforeDial", func() {
		_ = subject.generation.BeforeDial(context.Background())
	})
	require.EqualValues(t, 1, subject.client.generation,
		"a demand re-ran the transition for a generation the listener already applied")
}

// TestTheSecondNotificationIsStillDeduplicated is the guard-rail: routing the action through the
// reset (rather than through the listener) must not turn a duplicate notification into a second
// destructive pass, and a genuinely newer generation must still run it.
func TestTheSecondNotificationIsStillDeduplicated(t *testing.T) {
	coordinator := runtimecoord.New()
	t.Cleanup(func() { _ = coordinator.Close() })
	subject := newGateActionSubject(t, coordinator)

	coordinator.Advance(1)
	notify(t, subject)
	notify(t, subject)
	require.EqualValues(t, 1, subject.client.generation,
		"a duplicate notification for one generation ran the reset twice")

	coordinator.Advance(2)
	notify(t, subject)
	require.EqualValues(t, 2, subject.client.generation,
		"a newer generation did not run the reset")
}
