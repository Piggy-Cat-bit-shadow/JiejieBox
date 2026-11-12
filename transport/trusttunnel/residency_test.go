package trusttunnel

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSessionResidencyLedgerFollowsTheStateMachine is the Q15 object-identity contract for this
// transport: the one session object moves Creating -> Active -> Idle -> Terminated, a retired session
// that still carries a stream is Draining (not released), and every completed release is counted.
func TestSessionResidencyLedgerFollowsTheStateMachine(t *testing.T) {
	for _, protocol := range clientProtocols() {
		protocol := protocol
		t.Run(protocol, func(t *testing.T) {
			client := newSharedOfferClient(t, protocol, func(context.Context) (sessionConn, error) {
				return newFakeSession(), nil
			})
			initial := client.ResidencyLedger()
			require.True(t, initial.Answered)
			require.True(t, initial.Exact)
			require.Zero(t, initial.Live())

			ctx := testContext(t)
			conn, err := client.DialContext(ctx, holdDestination())
			require.NoError(t, err)
			active := client.ResidencyLedger()
			require.EqualValues(t, 1, active.Active)
			require.Zero(t, active.Idle)

			require.NoError(t, conn.Close())
			idle := client.ResidencyLedger()
			require.Zero(t, idle.Active)
			require.EqualValues(t, 1, idle.Idle, "a session with no stream is idle, not released")

			client.RetireSuspect()
			retired := client.ResidencyLedger()
			require.Zero(t, retired.Live(), "an idle retired session is closed at the boundary")
			require.EqualValues(t, 1, retired.Terminated)

			conn, err = client.DialContext(ctx, holdDestination())
			require.NoError(t, err)
			client.RetireSuspect()
			draining := client.ResidencyLedger()
			require.EqualValues(t, 1, draining.Draining,
				"a retired session that still carries a stream is draining, not released")
			require.EqualValues(t, 1, draining.Terminated)

			require.NoError(t, conn.Close())
			drained := client.ResidencyLedger()
			require.Zero(t, drained.Draining)
			require.EqualValues(t, 2, drained.Terminated)
		})
	}
}
