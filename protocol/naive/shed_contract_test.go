//go:build with_naive_outbound

package naive

import (
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/resourceguard"
	"github.com/stretchr/testify/require"
)

// countingPoolCloser is the engine's only close primitive with a call counter. It stands in for
// *cronet.NaiveClient at the one seam the contract is about, so the test needs no native engine.
type countingPoolCloser struct {
	calls atomic.Int64
}

func (c *countingPoolCloser) CloseAllConnections() {
	c.calls.Add(1)
}

// TestNaiveShedHookNeverCutsALiveTunnel is the pin on the contract between
// resourceguard.RegisterShed and this outbound.
//
// # What it proves
//
// A guard that is already under Critical runs every shed hook it holds, and it runs them on the
// goroutine that observed the transition — a dial. This test wires the outbound's pressure seam
// exactly as Start does, forces the guard into Critical, and requires that nothing reached the
// engine's close primitive. The tunnel is live in this scenario by construction: CloseAllConnections
// is the only close primitive the pin exposes, and it ends the CONNECT streams that carry traffic
// (cronet-go test/integration_test.go, TestCloseAllConnectionsThenClientClose).
//
// # How it fails when the mechanism comes back
//
// Re-registering `guard.RegisterShed(client.CloseAllConnections)` inside registerNaiveShedHook turns
// this test red on the first shed (calls == 1). That mutation was run as the negative control for
// this finding.
func TestNaiveShedHookNeverCutsALiveTunnel(t *testing.T) {
	guard := resourceguard.NewGuard(resourceguard.DefaultNetworkExtensionConfig(), func() resourceguard.Level {
		return resourceguard.LevelCritical
	})
	pool := &countingPoolCloser{}
	registerNaiveShedHook(guard, pool)

	// Reading the level advances the state machine, applies the plan and runs the shed hooks of the
	// transition into Critical — the same edge a dial would observe.
	require.Equal(t, resourceguard.LevelCritical, guard.Level())
	require.EqualValues(t, 0, pool.calls.Load(),
		"the guard's shed hook closed the engine pool; a hook must not close a connection that is "+
			"serving a flow (resourceguard.RegisterShed), and this pin's only close primitive "+
			"terminates live CONNECT streams")
}

// TestNaiveDoesNotClaimAnIdleOnlyReclaimAction records the gap this outbound cannot close at the
// current pin, and refuses to let it rot into a silent assumption.
//
// adapter.IdleConnectionReleaser is what the memory-trim walk asks for (route/reference.go
// eachReusableOwner): a reclaim action that may only drop resources carrying no user traffic. The
// pinned cronet API has no such action — Engine.CloseAllConnections is the whole surface — so this
// outbound does not implement it and the trim pass cannot reach the engine pool.
//
// The assertion is deliberately in the "must stay absent" direction. When the dependency gains an
// idle-only close, this test fails, which is the prompt to implement the method, register it as the
// shed hook instead of an unconditional close, and delete this test. A silent capability claim would
// be the worse outcome: a trim that believes it reached this pool but did not.
func TestNaiveDoesNotClaimAnIdleOnlyReclaimAction(t *testing.T) {
	var owner any = (*Outbound)(nil)
	_, isIdleReleaser := owner.(adapter.IdleConnectionReleaser)
	require.False(t, isIdleReleaser,
		"the naive outbound now satisfies adapter.IdleConnectionReleaser: if that came from a "+
			"narrower close primitive in the cronet pin, wire it into registerNaiveShedHook, cover "+
			"the reuse/reclaim path with a test that keeps a LIVE tunnel open across a trim, and "+
			"retire this pin. Until then the trim walk must not be told this pool can be released "+
			"by an action that would cut the flows it exists to protect")
}
