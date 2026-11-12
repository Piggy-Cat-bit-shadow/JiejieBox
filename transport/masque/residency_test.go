package masque

// Q15: the MASQUE client's session-identity ledger.

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func newLedgerSession(client *Client, ready bool) *clientSession {
	session := &clientSession{client: client, ready: ready}
	session.publishStateLocked()
	return session
}

func TestMASQUESessionResidencyLedger(t *testing.T) {
	client := &Client{}
	initial := client.ResidencyLedger()
	require.True(t, initial.Answered)
	require.Zero(t, initial.Live())

	// A session that is published but not ready is Creating.
	session := newLedgerSession(client, false)
	client.current = session
	creating := client.ResidencyLedger()
	require.EqualValues(t, 1, creating.Creating)
	require.Zero(t, creating.Terminated)

	// Ready with no packet yet is Idle; recent payload makes it Active.
	session.ready = true
	session.publishStateLocked()
	idle := client.ResidencyLedger()
	require.EqualValues(t, 1, idle.Idle)

	session.markActivity()
	active := client.ResidencyLedger()
	require.EqualValues(t, 1, active.Active)

	// A session cancelled by RestartSession and still published is Draining.
	client.restarting = true
	client.sessionsTerminated.Store(0)
	draining := client.ResidencyLedger()
	require.EqualValues(t, 1, draining.Draining)
	client.restarting = false

	// The session loop ended: terminated is monotonic and the object is gone.
	client.current = nil
	client.sessionsTerminated.Add(1)
	terminated := client.ResidencyLedger()
	require.Zero(t, terminated.Live())
	require.EqualValues(t, 1, terminated.Terminated)

	// A loop that is running without a published session is still Creating.
	client.activeLoops.Store(1)
	retrying := client.ResidencyLedger()
	require.EqualValues(t, 1, retrying.Creating)
	client.activeLoops.Store(0)
}
