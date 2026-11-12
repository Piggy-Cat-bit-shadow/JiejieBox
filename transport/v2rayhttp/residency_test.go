package v2rayhttp

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHTTPStreamResidencyLedger(t *testing.T) {
	client := &Client{}
	initial := client.ResidencyLedger()
	require.True(t, initial.Answered)
	require.False(t, initial.Exact)
	require.Zero(t, initial.Live())

	left, right := net.Pipe()
	defer right.Close()
	client.ledger.creating.Store(1)
	creating := client.ResidencyLedger()
	require.EqualValues(t, 1, creating.Creating)

	tracked := NewLateHTTPConn(left, nil)
	client.ledger.creating.Store(0)
	client.attachLedger(tracked)
	active := client.ResidencyLedger()
	require.EqualValues(t, 1, active.Active)

	require.NoError(t, tracked.Close())
	terminated := client.ResidencyLedger()
	require.Zero(t, terminated.Active)
	require.EqualValues(t, 1, terminated.Terminated)
	require.NoError(t, tracked.Close(), "double close must not double count")
	require.EqualValues(t, 1, client.ResidencyLedger().Terminated)
}
