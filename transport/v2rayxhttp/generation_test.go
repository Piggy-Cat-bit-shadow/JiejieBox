package v2rayxhttp

// Q12: the XMUX pool's network generation ownership.
//
// The audit found this transport had a real reuse boundary (RetireSuspect) but nothing called it when
// the network moved: the epoch is published before the listener phase, so a dial in that window was
// served by a connection built for the path that just ended. The fix is at the hand-out - prepare
// before the dial, settle after it - and these tests drive exactly those production steps.
//
// The pool's own drain semantics are pinned by xmux_test.go and wake_drain_test.go; what is new here
// is the WIRING from the network epoch to that boundary, and the late-result rule. The connections are
// the package's existing fake, so no HTTP server stands in for the pool.

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/runtimecoord"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// generationTestClient builds the production client shell around a real xmux pool whose connection
// constructor is the package's fake: these tests are about the generation steps, not HTTP framing.
func generationTestClient(t *testing.T) (*Client, *runtimecoord.Coordinator) {
	t.Helper()
	coordinator := runtimecoord.New()
	t.Cleanup(func() { _ = coordinator.Close() })
	ctx := service.ContextWith[*runtimecoord.Coordinator](context.Background(), coordinator)
	manager, _ := poolOf(t, xmuxConfig{maxConcurrency: intRange{4, 4}})
	client := &Client{
		xmux:        manager,
		coordinator: service.FromContext[*runtimecoord.Coordinator](ctx),
	}
	client.registration, client.removeRegistration = generationRegistration(client.coordinator, "v2rayxhttp/test")
	t.Cleanup(func() { _ = client.Close() })
	return client, coordinator
}

// TestTheHandoutRunsThePoolsOwnBoundaryOnANetworkChange pins the wiring: a published generation makes
// the pool stale, and prepareGeneration - the step DialContext runs first - moves the POOL generation
// through its own reuse boundary instead of a private close path.
func TestTheHandoutRunsThePoolsOwnBoundaryOnANetworkChange(t *testing.T) {
	t.Parallel()
	client, coordinator := generationTestClient(t)
	require.False(t, client.generationStale())
	before := client.xmux.stats().Generation

	require.Equal(t, uint64(0), client.prepareGeneration(), "no change yet: the epoch is zero")
	require.Equal(t, before, client.xmux.stats().Generation, "nothing to retire, nothing moves")

	coordinator.Advance(1)
	require.True(t, client.generationStale(), "a published generation must be visible before any listener ran")
	startGeneration := client.prepareGeneration()
	require.EqualValues(t, 1, startGeneration)
	require.Greater(t, client.xmux.stats().Generation, before,
		"the hand-out must run the pool's own reuse boundary, not a private close")
	// The registration is acknowledged by the first stream that really completes on the new
	// generation, not by the retirement itself - a retirement is not evidence the new path works.
	require.True(t, client.generationStale())
	client.acknowledgeGeneration()
	require.False(t, client.generationStale())
}

// TestALateDialIsDiscardedAndItsConnectionClosed pins the late-result rule at the settle step.
func TestALateDialIsDiscardedAndItsConnectionClosed(t *testing.T) {
	t.Parallel()
	client, coordinator := generationTestClient(t)
	startGeneration := client.prepareGeneration()

	left, right := net.Pipe()
	defer right.Close()
	// A peer whose remote end is closed reports the close as an error; the deadline keeps a
	// regression (a leaked connection) from hanging the test instead of failing it.
	require.NoError(t, right.SetReadDeadline(time.Now().Add(5*time.Second)))
	coordinator.Advance(1)

	conn, err := client.settleCompletedGeneration(left, startGeneration)
	require.ErrorIs(t, err, staleGenerationError{})
	require.Nil(t, conn)
	buffer := make([]byte, 1)
	_, readErr := right.Read(buffer)
	require.Error(t, readErr, "the discarded connection must be closed, not leaked")
}

// TestTheSettleStepKeepsCurrentWork keeps the other half: a dial that did NOT cross a change is
// returned, and the pool is acknowledged rather than discarded.
func TestTheSettleStepKeepsCurrentWork(t *testing.T) {
	t.Parallel()
	client, _ := generationTestClient(t)
	startGeneration := client.prepareGeneration()
	require.Equal(t, startGeneration, client.currentGeneration())

	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	if client.currentGeneration() != startGeneration {
		conn, err := client.settleCompletedGeneration(left, startGeneration)
		require.NoError(t, err)
		require.Equal(t, left, conn)
	}
	client.acknowledgeGeneration()
	require.False(t, client.generationStale())
}

// TestNoCoordinatorMeansNoGenerationCheck keeps the embedder contract: no coordinator, no
// registration, and the hand-out is a no-op.
func TestNoCoordinatorMeansNoGenerationCheck(t *testing.T) {
	t.Parallel()
	manager, _ := poolOf(t, xmuxConfig{maxConcurrency: intRange{4, 4}})
	client := &Client{xmux: manager}
	require.Nil(t, client.registration)
	require.False(t, client.generationStale())
	require.EqualValues(t, 0, client.prepareGeneration())
	before := client.xmux.stats().Generation
	require.Equal(t, before, client.xmux.stats().Generation)
}
