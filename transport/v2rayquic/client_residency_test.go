//go:build with_quic

package v2rayquic

// Q15: the pool's object ledger, watched through a real connection's lifecycle.
//
// The assertions are about STATES, not about numbers that happen to be there: an idle pooled
// connection, an active one with a stream, a generation detached because the network moved (still
// resident), and the release that finally completes. "Draining is not released" is the specific
// property the audit asks for, so it is asserted while the old generation is still carrying a
// stream - a ledger that counted the detach as a release would fail here.

import (
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/runtimecoord"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// ledgerOf is a shorthand that also asserts the ledger claims to be exact.
func ledgerOf(t *testing.T, client *Client) (creating, active, idle, draining, terminated uint64) {
	t.Helper()
	ledger := client.ResidencyLedger()
	require.True(t, ledger.Exact, "this transport counts real objects and must say so")
	require.NotEmpty(t, ledger.Note, "an exact ledger must still say what it counts")
	return ledger.Creating, ledger.Active, ledger.Idle, ledger.Draining, ledger.Terminated
}

// TestTheLedgerFollowsTheConnectionLifecycle walks one connection through the four states.
func TestTheLedgerFollowsTheConnectionLifecycle(t *testing.T) {
	server := startMigrationServer(t, nil)
	dialer := &migratingDialer{base: &N.DefaultDialer{}}
	coordinator := runtimecoord.New()
	t.Cleanup(func() { _ = coordinator.Close() })
	client := newGenerationClient(t, server, dialer, coordinator)
	t.Cleanup(func() { _ = client.Close() })

	// Nothing has been dialled: no objects.
	creating, active, idle, draining, terminated := ledgerOf(t, client)
	require.Zero(t, creating)
	require.Zero(t, active)
	require.Zero(t, idle)
	require.Zero(t, draining)
	require.Zero(t, terminated)

	// A published connection with no stream is IDLE.
	stream := openStream(t, client)
	sendRecord(t, stream, 0, "ledger")
	_, active, idle, _, _ = ledgerOf(t, client)
	require.Equal(t, uint64(1), active, "a connection carrying a stream must be active")
	require.Zero(t, idle)

	// Its stream ends: the connection is still held, and it is now idle.
	require.NoError(t, stream.Close())
	require.Eventually(t, func() bool {
		_, _, currentIdle, _, _ := ledgerOf(t, client)
		return currentIdle == 1
	}, time.Second, time.Millisecond, "the connection never returned to the idle state")
	_, active, _, _, _ = ledgerOf(t, client)
	require.Zero(t, active)
}

// TestADrainingGenerationIsResidentUntilItIsReleased is the "a retiring generation cannot be
// counted as already released" property.
func TestADrainingGenerationIsResidentUntilItIsReleased(t *testing.T) {
	server := startMigrationServer(t, nil)
	dialer := &migratingDialer{base: &N.DefaultDialer{}}
	coordinator := runtimecoord.New()
	t.Cleanup(func() { _ = coordinator.Close() })
	client := newGenerationClient(t, server, dialer, coordinator)
	t.Cleanup(func() { _ = client.Close() })

	oldStream := openStream(t, client)
	sendRecord(t, oldStream, 0, "old-generation")

	// The network generation moves and a new demand arrives: the old generation is detached from
	// the pool, and a new one is dialled for the demand.
	coordinator.Advance(1)
	newStream := openStream(t, client)
	sendRecord(t, newStream, 0, "new-generation")

	_, active, _, draining, terminated := ledgerOf(t, client)
	require.Equal(t, uint64(1), active, "the new generation is the active object")
	require.Equal(t, uint64(1), draining,
		"the detached generation still carries a stream and must be counted as resident (draining)")
	require.Zero(t, terminated, "the detach must not be counted as a release")

	// The old generation's last stream ends: now it is released, and only now.
	require.NoError(t, oldStream.Close())
	require.Eventually(t, func() bool {
		_, _, _, currentDraining, currentTerminated := ledgerOf(t, client)
		return currentDraining == 0 && currentTerminated == 1
	}, 5*time.Second, time.Millisecond,
		"the draining generation was never released, or was released too early")

	// The surviving generation is still there: a release must not be counted twice or for the
	// wrong object.
	_, active, _, _, terminated = ledgerOf(t, client)
	require.Equal(t, uint64(1), active)
	require.Equal(t, uint64(1), terminated)
	require.Equal(t, 2, dialer.count(), "sanity: one handshake socket per generation")
}
