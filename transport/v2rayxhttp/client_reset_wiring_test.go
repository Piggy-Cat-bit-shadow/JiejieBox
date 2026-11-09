//go:build with_xhttp

package v2rayxhttp

// What a physical network change does to this pool, through the entry the outbounds use.
//
// The pool's own boundary action (RetireSuspect) is pinned in wake_drain_test.go; this file pins that
// the V2Ray outbounds' network-change entry - the Reset capability protocol/vless looks for before it
// falls back to Close - is that action and not a plain Close. The difference matters in two places:
// a busy connection must be DRAINED (its stream keeps its transport), and the pool must start a new
// generation, which is what lets the inventory tell pre-change connections from the ones a new stream
// dials afterwards.
//
// A plain Close would empty the pool too, so this is not about the old flows surviving - it is about
// the call being the reset-shaped one the rest of the tree already uses for a network change.

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClientResetIsTheNetworkChangeAction(t *testing.T) {
	t.Parallel()

	// One stream per connection, so a stream-holding connection cannot also absorb the next demand:
	// that is what makes the two roles below distinct connections.
	manager, conns := poolOf(t, xmuxConfig{maxConcurrency: intRange{1, 1}})
	client := &Client{xmux: manager}

	busy, _ := manager.get()
	busy.addOpenUsage(1)
	idle, _ := manager.get()
	require.NotSame(t, busy, idle, "the fixture needs two connections: one busy, one idle")
	require.Len(t, conns(), 2)
	generationBefore := manager.generation

	require.NoError(t, client.Reset())

	// The busy connection is only drained: its stream keeps its transport.
	require.Zero(t, conns()[0].closes(), "the reset tore down a connection carrying a live stream")
	require.True(t, busy.draining.Load(), "the reset did not refuse new work on the busy connection")
	// The idle connection is closed now.
	require.Equal(t, 1, conns()[1].closes(), "the reset left an idle connection open")

	// The pool starts a new generation: a new stream dials a fresh connection instead of the one that
	// carried the old network.
	require.Greater(t, manager.generation, generationBefore, "the reset did not start a new pool generation")
	next, _ := manager.get()
	require.NotSame(t, busy, next, "the reset handed a pre-change connection to a new stream")
	require.Len(t, conns(), 3, "the post-change stream did not dial a fresh connection")

	// The drained connection is retired with its last stream, not leaked and not torn down early.
	require.Zero(t, conns()[0].closes())
	busy.addOpenUsage(-1)
	require.Equal(t, 1, conns()[0].closes(), "the drained connection was not retired when its last stream ended")
}
