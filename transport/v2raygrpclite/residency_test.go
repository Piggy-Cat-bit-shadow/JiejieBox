package v2raygrpclite

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGRPCStreamResidencyLedger(t *testing.T) {
	client := &Client{}
	initial := client.ResidencyLedger()
	require.True(t, initial.Answered)
	require.False(t, initial.Exact)
	require.Zero(t, initial.Live())

	left, right := net.Pipe()
	defer right.Close()
	conn := newLateGunConn(left, nil)
	client.attachLedger(conn)
	active := client.ResidencyLedger()
	require.EqualValues(t, 1, active.Active)

	require.NoError(t, conn.Close())
	terminated := client.ResidencyLedger()
	require.Zero(t, terminated.Active)
	require.EqualValues(t, 1, terminated.Terminated)
	require.NoError(t, conn.Close(), "double close must not double count")
	require.EqualValues(t, 1, client.ResidencyLedger().Terminated)
}
