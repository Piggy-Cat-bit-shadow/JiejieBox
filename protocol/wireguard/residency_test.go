package wireguard

// Q15: the WireGuard endpoint's device-instance ledger.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWireGuardDeviceResidencyLedger(t *testing.T) {
	endpoint := &Endpoint{}
	initial := endpoint.ResidencyLedger()
	require.True(t, initial.Answered)
	require.False(t, initial.Exact)
	require.Zero(t, initial.Live())

	endpoint.started.Store(true)
	idle := endpoint.ResidencyLedger()
	require.EqualValues(t, 1, idle.Idle)

	endpoint.lastActivity.Store(time.Now().UnixNano())
	active := endpoint.ResidencyLedger()
	require.EqualValues(t, 1, active.Active)

	endpoint.started.Store(false)
	endpoint.terminated.Add(1)
	terminated := endpoint.ResidencyLedger()
	require.Zero(t, terminated.Live())
	require.EqualValues(t, 1, terminated.Terminated)
}
