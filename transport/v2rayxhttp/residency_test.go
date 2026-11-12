package v2rayxhttp

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestResidencyLedgerMapsTheXMUXInventory is the Q15 object-identity contract for this transport:
// the pool's own Active/Idle/Draining/Released inventory is the ledger, and a pool that was never
// constructed answers "unknown" rather than a precise zero.
func TestResidencyLedgerMapsTheXMUXInventory(t *testing.T) {
	manager, _ := poolOf(t, xmuxConfig{maxConnections: intRange{2, 2}})
	busy, _ := manager.get()
	busy.addOpenUsage(1)
	idle, _ := manager.get()
	require.NotSame(t, busy, idle)

	client := &Client{xmux: manager}
	initial := client.ResidencyLedger()
	require.True(t, initial.Answered)
	require.True(t, initial.Exact)
	require.EqualValues(t, 1, initial.Active)
	require.EqualValues(t, 1, initial.Idle)
	require.Zero(t, initial.Draining)
	require.Zero(t, initial.Creating)

	client.RetireSuspect()
	retired := client.ResidencyLedger()
	require.Zero(t, retired.Idle, "an idle connection is closed at the reuse boundary")
	require.EqualValues(t, 1, retired.Draining,
		"a connection that still carries a stream is draining, not released")
	require.EqualValues(t, 1, retired.Terminated, "the idle connection's close actually ran")

	busy.addOpenUsage(-1)
	released := client.ResidencyLedger()
	require.Zero(t, released.Draining)
	require.EqualValues(t, 2, released.Terminated)
	require.Zero(t, released.Live())

	unbuilt := (*Client)(nil).ResidencyLedger()
	require.False(t, unbuilt.Answered)
	require.NotEmpty(t, unbuilt.Note)
}
